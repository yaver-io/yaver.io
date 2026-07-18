"use client";

// Cloud-console-style "open shell from console" modal.
// Hosts the existing TerminalView (xterm.js + agent /ws/terminal PTY
// over relay) so the device's shell opens directly in the dashboard
// without needing a local SSH or terminal app.
//
// States:
//   - needs-reauth: device is online but the agent's Convex session
//     expired. Convex will reject the WebSocket session-token issue,
//     so we route the user to Rescue → Reset Auth or `yaver auth` on
//     the box instead of presenting a dead terminal.
//   - not-connected: agentClient is pointed elsewhere (or nowhere).
//     We show a "Connect & open shell" CTA.
//   - connecting: a connect attempt is in flight. BOUNDED — see below.
//   - failed: the connect attempt is over and it did not work. This
//     state exists because without it the modal hung on "Connecting to
//     @linux-2…" forever: page.tsx sets connectedDevice *before* it
//     probes and never clears it on failure, so isCurrentDeviceSelected
//     stays true while agentClient sits in "error" with a backoff
//     reconnect pending. The user saw a spinner-shaped lie for a box
//     that had already returned "Could not reach agent (direct, tunnel,
//     or relay)". We now read agentClient's own connectionState (plus a
//     hard timeout, so a wedged attempt can't stall us either) and show
//     the real reason with a retry.
//   - ready: mount TerminalView. WebSocket is created/torn down with
//     the modal lifecycle (mount on open, dispose on close).

"use client";

import { useEffect, useState } from "react";
import TerminalView from "./TerminalView";
import { agentClient, type ConnectAttemptDiagnostic } from "@/lib/agent-client";
import { getLastFailure, subscribeLastFailure } from "@/lib/probe-backoff";
import { deriveBrowserReach, type BrowserReach } from "@/lib/device-lifecycle";
import type { Device } from "@/lib/use-devices";

// A connect attempt that hasn't resolved by now is not going to. attemptConnect
// budgets 8s per relay candidate, 8s per tunnel candidate and 5s direct, then
// page.tsx may run one auto-reauth + a second full pass. 90s covers the whole
// worst case with headroom; past that we stop claiming progress.
const CONNECT_STALL_MS = 90_000;
function useAgentConnectionState(): string {
  const [state, setState] = useState<string>(() => agentClient.connectionState);
  useEffect(() => {
    const unsubscribe = agentClient.on("connectionState", (s) => setState(s));
    setState(agentClient.connectionState);
    return unsubscribe;
  }, []);
  return state;
}

/** Same reachability signal the device card badge and CTAs use. */
function useBrowserReach(device: Device): BrowserReach {
  const [failure, setFailure] = useState(() => getLastFailure(device.id));
  useEffect(() => {
    setFailure(getLastFailure(device.id));
    return subscribeLastFailure(() => setFailure(getLastFailure(device.id)));
  }, [device.id]);
  return deriveBrowserReach(device, failure);
}

function describeDiagnostic(d: ConnectAttemptDiagnostic): string {
  const where = d.path === "relay" ? `relay${d.relayId ? ` (${d.relayId})` : ""}` : d.path;
  const why = d.authExpired
    ? "agent session expired"
    : d.error || (d.status ? `HTTP ${d.status}` : "failed");
  return `${where}: ${why}`;
}

export default function WebShellModal({
  device,
  launch,
  isCurrentDeviceSelected,
  isCurrentDeviceConnected,
  onClose,
  onConnect,
  onOpenRescue,
}: {
  device: Device;
  launch?: "claude" | "codex" | "opencode";
  isCurrentDeviceSelected: boolean;
  isCurrentDeviceConnected: boolean;
  onClose: () => void;
  onConnect: () => void;
  onOpenRescue?: () => void;
}) {
  const [maximized, setMaximized] = useState(false);
  const connState = useAgentConnectionState();
  const reach = useBrowserReach(device);
  const hasRelay = agentClient.configuredRelayServers.length > 0;

  // Bounded "connecting": if the attempt neither succeeds nor flips
  // agentClient to "error" within CONNECT_STALL_MS, we call it failed
  // ourselves rather than spinning forever.
  const [stalled, setStalled] = useState(false);
  const attemptActive = isCurrentDeviceSelected && !isCurrentDeviceConnected && connState !== "error";
  useEffect(() => {
    if (!attemptActive) {
      setStalled(false);
      return;
    }
    setStalled(false);
    const t = setTimeout(() => setStalled(true), CONNECT_STALL_MS);
    return () => clearTimeout(t);
  }, [attemptActive, device.id]);

  const reauthRequired = Boolean(device.needsAuth) && !device.isGuest;
  const state: "needs-reauth" | "not-connected" | "connecting" | "failed" | "ready" = reauthRequired
    ? "needs-reauth"
    : isCurrentDeviceConnected
      ? "ready"
      : !isCurrentDeviceSelected
        ? "not-connected"
        : connState === "error" || stalled
          ? "failed"
          : "connecting";

  const failureDiagnostics = state === "failed" ? agentClient.lastConnectDiagnostics.filter((d) => !d.ok) : [];
  const failureHeadline = stalled && connState !== "error"
    ? "The connect attempt didn't finish."
    : failureDiagnostics.some((d) => d.authExpired)
      ? "The agent answered, but its Yaver session is expired."
      : reach.label
        ? reach.label
        : "Could not reach the agent (direct, tunnel, or relay).";
  const title = launch === "claude"
    ? "Claude"
    : launch === "codex"
      ? "Codex"
      : launch === "opencode"
        ? "OpenCode"
        : "Shell";

  return (
    <div
      className="fixed inset-0 z-50 flex items-stretch justify-center bg-black/70 backdrop-blur-sm p-0 sm:p-8"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className={`flex w-full flex-col overflow-hidden border border-slate-200 bg-white shadow-2xl dark:border-surface-700 dark:bg-[#0b0d10] ${
          maximized ? "max-w-none rounded-none h-screen sm:rounded-none" : "max-w-5xl rounded-none sm:rounded-xl"
        }`}
      >
        <div className="flex items-center justify-between border-b border-slate-200 bg-slate-50/95 px-4 py-2.5 dark:border-surface-800 dark:bg-surface-900/80">
          <div className="flex items-center gap-2 min-w-0">
            <span className={`inline-flex h-2 w-2 rounded-full ${state === "ready" ? "bg-emerald-400" : state === "needs-reauth" ? "bg-amber-400" : state === "failed" ? "bg-rose-400" : state === "connecting" ? "bg-cyan-400" : "bg-slate-400 dark:bg-surface-500"}`} />
            <span className="truncate text-[13px] font-semibold text-slate-900 dark:text-surface-100">
              {title} · {device.alias ? `@${device.alias}` : device.name}
            </span>
            <span className="hidden sm:inline truncate text-[11px] text-slate-500 dark:text-surface-500">
              {device.host}:{device.port}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <span className="hidden sm:inline rounded-full border border-slate-200 bg-white px-2 py-0.5 text-[10px] uppercase tracking-[0.14em] text-slate-500 dark:border-surface-700 dark:bg-surface-950/60 dark:text-surface-400">
              {state === "needs-reauth" ? "agent auth required" : state === "failed" ? "unreachable" : state === "connecting" ? "connecting…" : "via relay · PTY"}
            </span>
            <button
              onClick={() => setMaximized((m) => !m)}
              className="rounded-md border border-slate-200 bg-white px-2.5 py-1 text-[11px] text-slate-600 hover:border-slate-300 hover:text-slate-900 dark:border-surface-700 dark:bg-surface-950 dark:text-surface-300 dark:hover:border-surface-600 dark:hover:text-surface-100"
              title={maximized ? "Restore" : "Maximize"}
            >
              {maximized ? "❐" : "⛶"}
            </button>
            <button
              onClick={onClose}
              className="rounded-md border border-slate-200 bg-white px-2.5 py-1 text-[11px] text-slate-600 hover:border-slate-300 hover:text-slate-900 dark:border-surface-700 dark:bg-surface-950 dark:text-surface-300 dark:hover:border-surface-600 dark:hover:text-surface-100"
              title="Close (Esc)"
            >
              Close
            </button>
          </div>
        </div>
        <div className={`flex-1 overflow-hidden ${state === "ready" ? "bg-[#0b0d10]" : "bg-slate-50/70 dark:bg-transparent p-2"}`}>
          {state === "ready" ? (
            <TerminalView launch={launch} />
          ) : state === "needs-reauth" ? (
            <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center text-slate-700 dark:text-surface-300">
              <div className="rounded-full border border-amber-300 bg-amber-50 px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-amber-700 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
                Reauth required
              </div>
              <p className="max-w-md text-[13px] leading-5">
                The agent on{" "}
                <span className="font-mono text-amber-700 dark:text-amber-200">
                  {device.alias ? `@${device.alias}` : device.name}
                </span>{" "}
                is reachable but its Yaver session expired. Convex won&apos;t
                authenticate the PTY WebSocket until the agent re-auths.
              </p>
              <div className="flex w-full max-w-md flex-col gap-2 text-left text-[12px] text-slate-700 dark:text-surface-300">
                <div className="rounded-md border border-slate-200 bg-white p-3 dark:border-surface-700 dark:bg-surface-900/60">
                  <p className="text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500 dark:text-surface-400">
                    From this dashboard
                  </p>
                  <p className="mt-1">
                    Open <span className="text-amber-700 dark:text-amber-200">Rescue → Reset Auth</span>{" "}
                    on the device card. The agent restarts in bootstrap mode
                    and you re-pair from the mobile app or by running{" "}
                    <code className="rounded bg-slate-100 px-1 py-0.5 text-amber-700 dark:bg-surface-950 dark:text-amber-200">yaver auth</code>{" "}
                    on the box.
                  </p>
                  {onOpenRescue ? (
                    <button
                      onClick={() => { onClose(); onOpenRescue(); }}
                      className="mt-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-1.5 text-[11px] font-semibold text-amber-700 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200 dark:hover:bg-amber-500/15"
                    >
                      Open Rescue
                    </button>
                  ) : null}
                </div>
                <div className="rounded-md border border-slate-200 bg-white p-3 dark:border-surface-700 dark:bg-surface-900/60">
                  <p className="text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500 dark:text-surface-400">
                    From the device terminal
                  </p>
                  <p className="mt-1">
                    Run{" "}
                    <code className="rounded bg-slate-100 px-1 py-0.5 text-emerald-700 dark:bg-surface-950 dark:text-emerald-200">yaver auth</code>{" "}
                    on the box (browser sign-in opens automatically). Once it
                    finishes, click Connect &amp; open shell here.
                  </p>
                </div>
                <div className="rounded-md border border-slate-200 bg-white p-3 dark:border-surface-700 dark:bg-surface-900/60">
                  <p className="text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500 dark:text-surface-400">
                    From the mobile app
                  </p>
                  <p className="mt-1">
                    Open the device in the Yaver app and tap{" "}
                    <span className="text-sky-700 dark:text-sky-200">Reauth this device</span> in
                    the attention banner — pairing finishes over the relay
                    even if you&apos;re off the device&apos;s LAN.
                  </p>
                </div>
              </div>
            </div>
          ) : state === "connecting" ? (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center text-slate-700 dark:text-surface-300">
              <p className="text-[13px]">
                Connecting to{" "}
                <span className="font-mono text-cyan-700 dark:text-cyan-300">
                  {device.alias ? `@${device.alias}` : device.name}
                </span>{" "}
                before opening the PTY.
              </p>
              <p className="text-[11px] text-slate-500 dark:text-surface-500">
                Remote machines such as relay-only boxes need the dashboard connection to finish first.
              </p>
            </div>
          ) : state === "failed" ? (
            <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center text-slate-700 dark:text-surface-300">
              <div className="rounded-full border border-rose-300 bg-rose-50 px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-200">
                Can&apos;t open shell
              </div>
              <p className="max-w-md text-[13px] leading-5">
                {failureHeadline}{" "}
                <span className="font-mono text-rose-700 dark:text-rose-200">
                  {device.alias ? `@${device.alias}` : device.name}
                </span>{" "}
                heartbeats to Convex, but this browser has no working path to it,
                so there is no PTY to attach to.
              </p>
              {failureDiagnostics.length ? (
                <ul className="w-full max-w-md space-y-1 rounded-md border border-slate-200 bg-white p-3 text-left font-mono text-[11px] text-slate-600 dark:border-surface-700 dark:bg-surface-900/60 dark:text-surface-400">
                  {failureDiagnostics.map((d, i) => (
                    <li key={`${d.path}-${i}`} className="truncate">· {describeDiagnostic(d)}</li>
                  ))}
                </ul>
              ) : null}
              <p className="max-w-md text-[11px] leading-4 text-slate-500 dark:text-surface-500">
                {failureDiagnostics.some((d) => d.authExpired) || reach.reason === "unauthorized"
                  ? "The box refused our token — run `yaver auth` on it (or use Rescue → Reset Auth), then retry."
                  : hasRelay
                    ? "Its QUIC tunnel to the relay is most likely down. Restart the agent with `yaver serve` on the box, then retry."
                    : "No relay server is configured for this dashboard, so a box that isn't reachable on your LAN can't be reached at all from the browser."}
              </p>
              <div className="flex flex-wrap items-center justify-center gap-2">
                <button
                  onClick={onConnect}
                  className="rounded-md border border-slate-300 bg-white px-4 py-2 text-[12px] font-semibold text-slate-700 hover:bg-slate-50 dark:border-surface-600 dark:bg-surface-900 dark:text-surface-200 dark:hover:bg-surface-800"
                >
                  Retry
                </button>
                {onOpenRescue ? (
                  <button
                    onClick={() => { onClose(); onOpenRescue(); }}
                    className="rounded-md border border-amber-300 bg-amber-50 px-4 py-2 text-[12px] font-semibold text-amber-700 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200 dark:hover:bg-amber-500/15"
                  >
                    Open Rescue
                  </button>
                ) : null}
              </div>
            </div>
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center text-slate-700 dark:text-surface-300">
              <p className="text-[13px]">
                Browser shell needs an active agent connection to{" "}
                <span className="font-mono text-emerald-700 dark:text-emerald-300">
                  {device.alias ? `@${device.alias}` : device.name}
                </span>
                .
              </p>
              {/* Don't promise a connection we already know is failing. The
                  device card downgrades its own badge off this same record
                  ("Ready to Connect (Unauthorized)"); the shell CTA used to
                  ignore it and invite a click that could only end in a hang. */}
              {reach.unreachable ? (
                <p className="max-w-md rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-[11px] leading-4 text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
                  Last browser probe of this box failed: <span className="font-semibold">{reach.label}</span>.{" "}
                  {reach.detail} Connecting will probably fail too.
                </p>
              ) : null}
              <button
                onClick={onConnect}
                className={reach.unreachable
                  ? "rounded-md border border-slate-300 bg-white px-4 py-2 text-[12px] font-semibold text-slate-700 hover:bg-slate-50 dark:border-surface-600 dark:bg-surface-900 dark:text-surface-200 dark:hover:bg-surface-800"
                  : "rounded-md border border-emerald-300 bg-emerald-50 px-4 py-2 text-[12px] font-semibold text-emerald-700 hover:bg-emerald-100 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200 dark:hover:bg-emerald-500/15"}
              >
                {reach.unreachable ? "Try connecting anyway" : "Connect & open shell"}
              </button>
              <p className="text-[11px] text-slate-500 dark:text-surface-500">
                {hasRelay
                  ? "Once connected the PTY opens through the relay — works even when direct LAN is unreachable."
                  : "No relay server is configured, so this needs the box to be reachable directly from your browser."}
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
