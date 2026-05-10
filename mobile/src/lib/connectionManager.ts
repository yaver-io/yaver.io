// connectionManager.ts — multi-device connection pool.
//
// History: until this file existed, `quicClient` was a singleton that the
// mobile app reattached to a different machine every time the user picked
// one. Switching devices dropped whatever connection was in flight, and
// any UI surface that wanted to show live state from another box had to
// proxy every call through the focused agent (`/peer/<id>/...`). The
// "Pick a coding agent" wizard hit two cliffs that fell out of that:
//   1. Self-targeted peer proxies returned errProxyLocal, so the wizard
//      silently rendered "Not installed on this device" for runners that
//      were actually present (fixed in quic.ts via peerEndpoint).
//   2. There was no way to push a long-running task to box B while you
//      kept watching box A — switching tore down the active stream.
//
// The manager exposes a `Map<deviceId, QuicClient>` so every device the
// app has signed into can keep a live QUIC connection in parallel. The
// "focused" id is just a UI affordance — it controls which client the
// legacy `quicClient` Proxy delegates to so the 15+ existing call sites
// that import that singleton keep working unchanged. New code that
// genuinely cares about a specific device should call
// `connectionManager.clientFor(deviceId)` instead of routing through the
// focused Proxy.
//
// We deliberately do NOT cap the pool. The user opted into "every device
// the user has marked online stays connected forever" when this was
// scoped — battery management is a follow-up (cellular-aware idle, LRU
// eviction, opt-out per device).

import { QuicClient, createQuicClient, setQuicClientResolver } from "./quic";
import type { TunnelServer, RelayServer } from "./quic";

type ManagerListener = () => void;

/** Snapshot of one device's spot in the pool — what the UI needs to
 *  render badges ("3 connected") without poking at private QuicClient
 *  state. Keep this minimal; richer per-device state lives on the
 *  QuicClient instance and is reachable via `clientFor(deviceId)`. */
export interface PooledClientSnapshot {
  deviceId: string;
  connected: boolean;
  focused: boolean;
}

class ConnectionManager {
  private clients = new Map<string, QuicClient>();
  private focusedId: string | null = null;
  // Stable QuicClient used by the Proxy when no device is focused yet —
  // e.g. during cold start, before refreshDevices has run, or after the
  // user signs out. Without this the Proxy would deref null on every
  // boot-time call and crash the app.
  private fallback: QuicClient = createQuicClient();
  private listeners = new Set<ManagerListener>();

  // Manager-level snapshot of the connection prerequisites every per-
  // device client needs before it can talk to its agent: relay list,
  // bearer token, forceRelay preference, optional session-scoped
  // tunnels. The manager is the source of truth so a freshly-created
  // client (made on first selectDevice for that device) is born with
  // the same configuration the rest of the pool already has — without
  // this, the new client had empty relays + null token and Connect
  // 100%-of-the-time failed silently.
  private latestRelays: RelayServer[] = [];
  private latestForceRelay = false;
  private latestToken: string | null = null;
  private latestSessionTunnels: TunnelServer[] = [];

  /** Returns the QuicClient currently treated as focused, or the fallback
   *  instance when none is set. Callers that don't care about identity
   *  (e.g. anything that just wants the user's "primary" connection)
   *  should go through the legacy `quicClient` Proxy instead — this
   *  method exists for the Proxy itself and for code paths that need to
   *  branch on whether a real focus has been chosen. */
  active(): QuicClient {
    if (this.focusedId) {
      const c = this.clients.get(this.focusedId);
      if (c) return c;
    }
    return this.fallback;
  }

  focusedDeviceId(): string | null {
    return this.focusedId;
  }

  /** Get-or-create a QuicClient for the given deviceId. The returned
   *  client is NOT auto-connected; call `connectClient` (or invoke
   *  `client.connect(...)` directly from DeviceContext, which already
   *  handles transport + relay selection) to bring it up. */
  clientFor(deviceId: string): QuicClient {
    const id = deviceId.trim();
    if (!id) return this.fallback;
    const existing = this.clients.get(id);
    if (existing) return existing;
    const fresh = createQuicClient();
    // Hydrate the new client with the manager-level prerequisites so
    // its very first connect attempt has the same relay candidates,
    // forceRelay preference, and bearer token every other client in
    // the pool already has. The order here mirrors how DeviceContext
    // sets these on a singleton during boot.
    if (this.latestRelays.length > 0) {
      try { fresh.setRelayServers(this.latestRelays); } catch {}
    }
    try { fresh.setForceRelay(this.latestForceRelay); } catch {}
    if (this.latestToken) {
      try { fresh.setToken(this.latestToken); } catch {}
    }
    if (this.latestSessionTunnels.length > 0) {
      try { fresh.setSessionTunnelServers(this.latestSessionTunnels); } catch {}
    }
    this.clients.set(id, fresh);
    this.notify();
    return fresh;
  }

  /** Make `deviceId` the focus that the Proxy resolves to. Pass null
   *  when the user explicitly disconnects from everything (the focused
   *  Proxy will then fall back to the boot-time stub). Does not change
   *  pool membership — the previously-focused client stays connected
   *  in the pool and remains addressable via `clientFor(prevId)`. */
  setFocused(deviceId: string | null): void {
    const next = deviceId?.trim() || null;
    if (next === this.focusedId) return;
    this.focusedId = next;
    this.notify();
  }

  /** Stop and remove a single client. Called when the user explicitly
   *  drops a device (long-press → Disconnect) or when a device goes
   *  offline. If the dropped device was focused, focus clears — the
   *  caller decides whether to refocus another device. */
  disconnect(deviceId: string): void {
    const id = deviceId.trim();
    if (!id) return;
    const client = this.clients.get(id);
    if (!client) return;
    try {
      client.disconnect();
    } catch {
      // Tearing down a half-open client can throw; we just want it gone.
    }
    this.clients.delete(id);
    if (this.focusedId === id) {
      this.focusedId = null;
    }
    this.notify();
  }

  /** Drop every pooled client. Used on sign-out — every per-user
   *  connection must die before the next user picks up. */
  disconnectAll(): void {
    for (const [, client] of this.clients) {
      try {
        client.disconnect();
      } catch {
        // ignore; we're nuking the pool
      }
    }
    this.clients.clear();
    this.focusedId = null;
    this.notify();
  }

  /** Apply common state (relay servers, token, tunnel list) to every
   *  pooled client at once. The legacy code did this via a single
   *  `quicClient.setRelayServers(...)` call — now that we have N
   *  clients, fan it out. Cheap (a few small array assignments per
   *  client) and idempotent. */
  applyToAll(mutator: (client: QuicClient) => void): void {
    mutator(this.fallback);
    for (const [, client] of this.clients) {
      try {
        mutator(client);
      } catch {
        // Per-client mutator failure shouldn't poison siblings.
      }
    }
  }

  /** Snapshot of every pooled client — feeds the UI badge that says
   *  "3 devices connected" without leaking QuicClient instances out of
   *  the manager. */
  snapshot(): PooledClientSnapshot[] {
    const out: PooledClientSnapshot[] = [];
    for (const [deviceId, client] of this.clients) {
      out.push({
        deviceId,
        connected: client.isConnected,
        focused: deviceId === this.focusedId,
      });
    }
    return out;
  }

  /** IDs of every device whose client is currently `isConnected`.
   *  Subset of the pool — a device that was just added but hasn't
   *  finished its first connect attempt won't appear here yet. */
  connectedDeviceIds(): string[] {
    const out: string[] = [];
    for (const [deviceId, client] of this.clients) {
      if (client.isConnected) out.push(deviceId);
    }
    return out;
  }

  /** Subscribe for pool-membership / focus changes. Note this does NOT
   *  fire on the underlying QuicClient state changes — those have their
   *  own listener API per client. */
  subscribe(fn: ManagerListener): () => void {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  }

  private notify(): void {
    for (const fn of this.listeners) {
      try {
        fn();
      } catch {
        // never let a listener throw stop the others.
      }
    }
  }

  // Convenience helpers used by DeviceContext during boot. They
  // accept the same shapes DeviceContext was passing into the legacy
  // singleton — keeps the call-site diff minimal.

  setRelayServersOnAll(relays: RelayServer[]): void {
    this.latestRelays = [...relays];
    this.applyToAll((c) => c.setRelayServers(relays));
  }

  setForceRelayOnAll(force: boolean): void {
    this.latestForceRelay = force;
    this.applyToAll((c) => c.setForceRelay(force));
  }

  setTokenOnAll(token: string): void {
    this.latestToken = token;
    this.applyToAll((c) => c.setToken(token));
  }

  setSessionTunnelServersOnAll(tunnels: TunnelServer[]): void {
    this.latestSessionTunnels = [...tunnels];
    this.applyToAll((c) => c.setSessionTunnelServers(tunnels));
  }
}

export const connectionManager = new ConnectionManager();

// Wire the legacy `quicClient` Proxy in quic.ts so that property
// accesses on it dispatch to whichever pooled client is currently
// focused. Done at module init — once this file is imported anywhere
// (DeviceContext does it during the app's boot path), every existing
// `quicClient.foo()` call automatically benefits from the multi-device
// pool.
setQuicClientResolver(() => connectionManager.active());
