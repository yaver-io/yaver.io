// Mobile mirror of web/components/dashboard/DevicesView.tsx →
// DeviceDetailsPanel + ConnectionSection. Surfaces the same
// transport classification, relay version, LAN/Tailscale/Cloudflare
// breakdown, and runtime info on the phone.

import React, { useCallback, useEffect, useState } from "react";
import { Alert, Modal, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import * as Clipboard from "expo-clipboard";
import { useRouter } from "expo-router";
import { useDevice, type Device } from "../context/DeviceContext";
import { useColors } from "../context/ThemeContext";
import { quicClient, type RunnerAuthStatusRow } from "../lib/quic";
import { probeMobileDeviceStatus } from "../lib/deviceStatus";
import RunnerAuthModal from "./RunnerAuthModal";

const CODING_AGENTS: ReadonlyArray<{ id: "claude" | "codex" | "opencode"; label: string }> = [
  { id: "claude", label: "Claude Code" },
  { id: "codex", label: "Codex" },
  { id: "opencode", label: "OpenCode" },
];
import {
  classifyTransport,
  fetchRelayHealth,
  transportToneRGB,
  type TransportInfo,
} from "../lib/transport";

function transportFor(device: Device): TransportInfo {
  return classifyTransport({
    host: device.host,
    port: device.port,
    localIps: device.lanIps,
    publicEndpoints: device.publicEndpoints,
    tunnelUrl: device.tunnelUrl,
    activeRelayUrl: quicClient.activeRelayBaseUrl ?? null,
    activeTunnelUrl: quicClient.activeTunnelBaseUrl ?? null,
    platform: device.os,
    name: device.name,
  });
}

function timeAgo(epochMs: number | undefined): string {
  if (!epochMs || epochMs <= 0) return "—";
  const diff = Math.max(0, Date.now() - epochMs);
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return sec <= 5 ? "just now" : `${sec}s ago`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

function sshSelectorForDevice(device: Pick<Device, "alias" | "id">): string {
  const alias = String(device.alias || "").trim();
  if (alias) return `@${alias}`;
  return device.id.slice(0, 8);
}

function stripSSHHost(raw: string | undefined): string {
  const text = String(raw || "").trim();
  if (!text) return "";
  try {
    if (text.startsWith("http://") || text.startsWith("https://")) {
      return new URL(text).host;
    }
  } catch {}
  return text.replace(/^https?:\/\//, "").replace(/\/+$/, "");
}

function isUsefulDirectSSHHost(host: string): boolean {
  return Boolean(
    host &&
      host !== "0.0.0.0" &&
      host !== "::" &&
      host !== "::1" &&
      !host.startsWith("127.") &&
      !/^172\.(1[6-9]|2\d|3[0-1])\.0\.1$/.test(host),
  );
}

function directSSHHostForDevice(device: Pick<Device, "publicEndpoints" | "lanIps" | "host">): string {
  for (const endpoint of device.publicEndpoints || []) {
    const host = stripSSHHost(endpoint);
    if (isUsefulDirectSSHHost(host)) return host;
  }
  for (const ip of device.lanIps || []) {
    if (/^100\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(ip)) return ip;
  }
  for (const ip of device.lanIps || []) {
    if (isUsefulDirectSSHHost(ip)) return ip;
  }
  const host = stripSSHHost(device.host);
  if (isUsefulDirectSSHHost(host)) return host;
  return "";
}

function sshCommandForDevice(device: Pick<Device, "alias" | "id">): string {
  return `yaver ssh ${sshSelectorForDevice(device)}`;
}

// Inline alias editor — shown only on owned devices. Tap the chip
// to edit, Save to commit, Clear to remove. Surfaces server-side
// uniqueness errors verbatim so the user knows which alias is taken.
function AliasRow({ device }: { device: Device }) {
  const c = useColors();
  const { setDeviceAlias } = useDevice();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(device.alias ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!editing) {
      setDraft(device.alias ?? "");
      setError(null);
    }
  }, [device.alias, editing]);

  const commit = async (next: string) => {
    setSaving(true);
    setError(null);
    const res = await setDeviceAlias(device, next);
    setSaving(false);
    if (!res.ok) {
      setError(res.error);
      return;
    }
    setEditing(false);
  };

  if (!editing) {
    return (
      <Pressable
        onPress={() => setEditing(true)}
        style={{ flexDirection: "row", alignItems: "center", paddingVertical: 6 }}
      >
        <Text style={{ color: c.textMuted, fontSize: 12, width: 110 }}>Alias</Text>
        <View style={{ flex: 1, flexDirection: "row", alignItems: "center" }}>
          <Text style={{
            color: device.alias ? "#a7f3d0" : c.textMuted,
            fontSize: 13,
            fontFamily: device.alias ? "Menlo" : undefined,
            fontWeight: device.alias ? "600" : "400",
          }}>
            {device.alias ? `@${device.alias}` : "tap to set"}
          </Text>
          <Text style={{ color: c.accent, fontSize: 11, marginLeft: 8, fontWeight: "600" }}>
            EDIT
          </Text>
        </View>
      </Pressable>
    );
  }

  return (
    <View style={{ paddingVertical: 6 }}>
      <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 6 }}>Alias</Text>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <TextInput
          value={draft}
          onChangeText={setDraft}
          autoFocus
          autoCapitalize="none"
          autoCorrect={false}
          editable={!saving}
          placeholder="prod-mac"
          placeholderTextColor={c.textMuted}
          style={{
            flex: 1,
            color: c.textPrimary,
            backgroundColor: c.bgCard,
            borderWidth: 1,
            borderColor: c.border,
            borderRadius: 6,
            paddingHorizontal: 10,
            paddingVertical: 8,
            fontFamily: "Menlo",
            fontSize: 13,
          }}
        />
        <Pressable
          onPress={() => void commit(draft.trim().toLowerCase())}
          disabled={saving}
          style={{
            paddingHorizontal: 12,
            paddingVertical: 8,
            borderRadius: 6,
            backgroundColor: "rgba(16,185,129,0.15)",
            borderWidth: 1,
            borderColor: "rgba(16,185,129,0.45)",
            opacity: saving ? 0.5 : 1,
          }}
        >
          <Text style={{ color: "#a7f3d0", fontSize: 12, fontWeight: "700" }}>
            {saving ? "..." : "Save"}
          </Text>
        </Pressable>
        {device.alias ? (
          <Pressable
            onPress={() => void commit("")}
            disabled={saving}
            style={{
              paddingHorizontal: 12,
              paddingVertical: 8,
              borderRadius: 6,
              backgroundColor: "rgba(244,63,94,0.10)",
              borderWidth: 1,
              borderColor: "rgba(244,63,94,0.40)",
              opacity: saving ? 0.5 : 1,
            }}
          >
            <Text style={{ color: "#fecdd3", fontSize: 12, fontWeight: "700" }}>Clear</Text>
          </Pressable>
        ) : null}
        <Pressable
          onPress={() => { setEditing(false); setError(null); }}
          disabled={saving}
          style={{ paddingHorizontal: 8, paddingVertical: 8 }}
        >
          <Text style={{ color: c.textMuted, fontSize: 12 }}>Cancel</Text>
        </Pressable>
      </View>
      {error ? (
        <Text style={{ marginTop: 6, fontSize: 11, color: "#fecdd3" }}>{error}</Text>
      ) : null}
    </View>
  );
}

// Quick-action row in the modal header. Currently a single "Open
// Shell" button that pushes to /shell — disabled (with a hint) for
// devices that aren't the currently active connection because the
// shell screen rides the active quicClient.baseUrl.
function ShellActionRow({ device, onClose }: { device: Device; onClose: () => void }) {
  const c = useColors();
  const router = useRouter();
  const { activeDevice, connectionStatus } = useDevice();
  const isActive = Boolean(activeDevice && activeDevice.id === device.id && connectionStatus === "connected");
  const sshCommand = sshCommandForDevice(device);
  const directSSHHost = directSSHHostForDevice(device);

  return (
    <View style={{
      flexDirection: "row", gap: 8, paddingHorizontal: 16, paddingTop: 12, paddingBottom: 4,
    }}>
      <Pressable
        onPress={() => {
          if (!isActive) {
            Alert.alert(
              "Connect first",
              `Open ${device.name} from the home screen so the agent connection is active, then come back to start a shell.`,
            );
            return;
          }
          onClose();
          // Slight delay so the modal close animation doesn't fight
          // the navigation push on iOS.
          setTimeout(() => { router.push("/shell"); }, 200);
        }}
        style={{
          flexDirection: "row", alignItems: "center", gap: 6,
          paddingHorizontal: 12, paddingVertical: 8,
          borderRadius: 8,
          backgroundColor: isActive ? "rgba(34,211,238,0.12)" : "rgba(75,85,99,0.10)",
          borderWidth: 1,
          borderColor: isActive ? "rgba(34,211,238,0.45)" : c.border,
        }}
      >
        <Text style={{ color: isActive ? "#67e8f9" : c.textMuted, fontSize: 13, fontWeight: "700" }}>
          ⌨  Open Shell
        </Text>
        {!isActive ? (
          <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 4 }}>(connect first)</Text>
        ) : null}
      </Pressable>
      <Pressable
        onPress={async () => {
          try {
            await Clipboard.setStringAsync(sshCommand);
            const detail = directSSHHost ? `\n\nDirect fallback: ssh ${directSSHHost}` : "";
            Alert.alert("SSH command copied", `${sshCommand}${detail}`);
          } catch (e: any) {
            Alert.alert("Clipboard Failed", e?.message ?? String(e));
          }
        }}
        style={{
          flexDirection: "row", alignItems: "center", gap: 6,
          paddingHorizontal: 12, paddingVertical: 8,
          borderRadius: 8,
          backgroundColor: "rgba(16,185,129,0.12)",
          borderWidth: 1,
          borderColor: "rgba(16,185,129,0.45)",
        }}
      >
        <Text style={{ color: "#a7f3d0", fontSize: 13, fontWeight: "700" }}>
          ⎘  Copy SSH
        </Text>
      </Pressable>
    </View>
  );
}

function FactoryResetAuthRow({ device }: { device: Device }) {
  const c = useColors();
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const onPress = () => {
    Alert.alert(
      `Factory-reset auth on "${device.name}"?`,
      "The agent will exit and restart in bootstrap mode. You'll re-pair from this app or the dashboard. Projects, vault, workspace files are preserved.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Reset Auth",
          style: "destructive",
          onPress: async () => {
            setBusy(true);
            setMsg(null);
            try {
              const res = await quicClient.factoryResetDeviceAuth(device.id);
              if (res.ok) {
                setMsg("✓ reset triggered — re-pair when the agent comes back (~10s)");
              } else {
                setMsg(`✗ ${res.error}`);
              }
            } catch (e: any) {
              setMsg(`✗ ${e?.message ?? String(e)}`);
            } finally {
              setBusy(false);
            }
          },
        },
      ],
    );
  };
  return (
    <View>
      <Pressable
        onPress={onPress}
        disabled={busy}
        style={{
          alignSelf: "flex-start",
          paddingHorizontal: 12,
          paddingVertical: 8,
          borderRadius: 8,
          backgroundColor: "rgba(244,63,94,0.12)",
          borderWidth: 1,
          borderColor: "rgba(244,63,94,0.45)",
          opacity: busy ? 0.5 : 1,
        }}
      >
        <Text style={{ color: "#fecdd3", fontSize: 12, fontWeight: "700" }}>
          {busy ? "Resetting..." : "Reset Auth"}
        </Text>
      </Pressable>
      {msg ? (
        <Text style={{
          marginTop: 8,
          fontSize: 11,
          color: msg.startsWith("✓") ? "#a7f3d0" : "#fecdd3",
        }}>
          {msg}
        </Text>
      ) : null}
    </View>
  );
}

export interface DeviceDetailsModalProps {
  device: Device | null;
  agentVersion?: string | null;
  visible: boolean;
  onClose: () => void;
}

function OwnerClaimAuthRow({ device }: { device: Device }) {
  const c = useColors();
  const { recoverDeviceAuth, refreshDevices } = useDevice();
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  // Routes through the smart dispatcher in DeviceContext, NOT the
  // raw quicClient.ownerClaimDevice. The previous implementation hit
  // owner-claim directly, which is only registered while the agent
  // is in bootstrap mode — once the agent transitions to
  // auth-expired (its token went stale, it kept the row, it just
  // can't talk to Convex), the relay returns 404 for the owner-claim
  // path and the user sees "✗ 404 on relay public-free" against an
  // agent that's actually fine. recoverDeviceAuth probes /info first
  // to figure out which recovery surface is live (owner-claim vs
  // /auth/recover vs device-code OAuth) and routes accordingly.
  const onPress = async () => {
    setBusy(true);
    setMsg(null);
    try {
      const res = await recoverDeviceAuth(device);
      if (res && (res as any).ok !== false) {
        const where = (res as any).targetUrl
          ? ` (${(res as any).targetUrl})`
          : "";
        setMsg(`✓ recovered${where}`);
        setTimeout(() => { refreshDevices().catch(() => {}); }, 1000);
      } else {
        const err = (res as any)?.error || "recovery failed";
        setMsg(`✗ ${err}`);
      }
    } catch (e: any) {
      setMsg(`✗ ${e?.message ?? String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <View>
      <Pressable
        onPress={onPress}
        disabled={busy}
        style={{
          alignSelf: "flex-start",
          paddingHorizontal: 12,
          paddingVertical: 8,
          borderRadius: 8,
          backgroundColor: "rgba(34,197,94,0.12)",
          borderWidth: 1,
          borderColor: "rgba(34,197,94,0.45)",
          opacity: busy ? 0.5 : 1,
        }}
      >
        <Text style={{ color: "#bbf7d0", fontSize: 12, fontWeight: "700" }}>
          {busy ? "Recovering..." : "Recover Yaver Auth"}
        </Text>
      </Pressable>
      {msg ? (
        <Text style={{
          marginTop: 8,
          fontSize: 11,
          color: msg.startsWith("✓") ? "#a7f3d0" : "#fecdd3",
        }}>
          {msg}
        </Text>
      ) : null}
    </View>
  );
}

// PingRow is the mobile counterpart to the CLI's `yaver primary ping`:
// one-shot HTTP /info probe via the same transport stack the connect
// flow uses (relay first, then direct), rendered as one short summary
// line. Answers two questions in one tap: (1) is the box reachable
// at all, (2) is its Yaver auth valid and owned by the same user.
function PingRow({ device }: { device: Device }) {
  const c = useColors();
  const { token, user } = useDevice() as any;
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<null | {
    ok: boolean;
    line: string;
    detail?: string;
    elapsedMs?: number;
  }>(null);

  const onPress = async () => {
    setBusy(true);
    setResult(null);
    const t0 = Date.now();
    try {
      const probe = await probeMobileDeviceStatus(
        { id: device.id, host: device.host, port: device.port, lanIps: device.lanIps },
        token,
        8000,
      );
      const elapsedMs = Date.now() - t0;
      if (!probe.reachable) {
        setResult({
          ok: false,
          line: "unreachable",
          detail: "every transport candidate failed — try `yaver primary auth`",
          elapsedMs,
        });
        return;
      }
      const info = (probe.info || {}) as Record<string, any>;
      const version = info.version || "?";
      const lifecycle = probe.lifecycleState || info.lifecycleState || "unknown";
      const ownerId =
        info.ownerUserId ||
        info.ownerUserID ||
        info.ownerId ||
        "";
      const myId = user?.id || user?.userId || "";
      const ownerEmail = info.ownerEmail || "";
      let ownerLabel = "";
      if (ownerId && myId && ownerId === myId) {
        ownerLabel = `owner ${ownerEmail || "you"} (you)`;
      } else if (ownerId && myId && ownerId !== myId) {
        ownerLabel = `owner ${ownerEmail || ownerId} (NOT you)`;
      }
      const transport = probe.path === "relay" ? "relay" : "direct";
      const head = `via ${transport} · ${elapsedMs}ms`;
      const body = [
        `agent ${version}`,
        `lifecycle ${lifecycle}`,
        ownerLabel,
      ].filter(Boolean).join(" · ");
      setResult({ ok: true, line: head, detail: body, elapsedMs });
    } catch (e: any) {
      setResult({
        ok: false,
        line: "ping failed",
        detail: e?.message || String(e),
        elapsedMs: Date.now() - t0,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <View>
      <Pressable
        onPress={onPress}
        disabled={busy}
        style={{
          alignSelf: "flex-start",
          paddingHorizontal: 12,
          paddingVertical: 8,
          borderRadius: 8,
          backgroundColor: "rgba(99,102,241,0.12)",
          borderWidth: 1,
          borderColor: "rgba(99,102,241,0.45)",
          opacity: busy ? 0.5 : 1,
        }}
      >
        <Text style={{ color: "#c7d2fe", fontSize: 12, fontWeight: "700" }}>
          {busy ? "Pinging..." : "Ping"}
        </Text>
      </Pressable>
      {result ? (
        <View style={{ marginTop: 8 }}>
          <Text style={{
            fontSize: 12,
            fontWeight: "600",
            color: result.ok ? "#a7f3d0" : "#fecdd3",
          }}>
            {result.ok ? "✓ " : "✗ "}{result.line}
          </Text>
          {result.detail ? (
            <Text style={{ marginTop: 2, fontSize: 11, color: c.textMuted }}>
              {result.detail}
            </Text>
          ) : null}
        </View>
      ) : null}
    </View>
  );
}

// Coding agents auth + default-runner picker. Same agent surface (claude /
// codex / opencode) on every device, so we render the rows from a constant
// instead of relying on whatever the agent reports — that way "agent
// installed but never authed" still surfaces a Sign in button. authConfigured
// comes from /runner-auth/status (per-device, peered when not active).
function CodingAgentsSection({ device }: { device: Device }) {
  const c = useColors();
  const { activeDevice, connectionStatus, primaryRunnerByDevice, setPrimaryRunnerForDevice } = useDevice();
  const [statusRows, setStatusRows] = useState<RunnerAuthStatusRow[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [authModalRunner, setAuthModalRunner] = useState<string | null>(null);
  const [defaultBusy, setDefaultBusy] = useState<string | null>(null);

  const isActive = Boolean(activeDevice && activeDevice.id === device.id && connectionStatus === "connected");
  // /runner-auth/status routes via /peer/<id> when target is set; pass it
  // for non-active devices so the agent forwards the call to the right peer.
  const target = isActive ? undefined : device.id;
  const currentDefault = primaryRunnerByDevice[device.id] || "";

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const rows = await quicClient.runnerAuthStatus(target);
      setStatusRows(rows || []);
    } catch {
      setStatusRows([]);
    } finally {
      setLoading(false);
    }
  }, [target]);

  useEffect(() => { void refresh(); }, [refresh]);

  const findStatus = (id: string): RunnerAuthStatusRow | undefined =>
    (statusRows || []).find((r) => {
      const rid = String(r.id || "").toLowerCase();
      return rid === id || (id === "claude" && rid === "claude-code");
    });

  return (
    <View style={{
      borderWidth: 1, borderColor: c.border, borderRadius: 8,
      backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
    }}>
      <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
        CODING AGENTS
      </Text>

      {/* Auth status + sign-in. */}
      {CODING_AGENTS.map(({ id, label }) => {
        const row = findStatus(id);
        const authed = row?.authConfigured === true;
        return (
          <View
            key={id}
            style={{
              flexDirection: "row",
              alignItems: "center",
              paddingVertical: 8,
              borderTopWidth: id === CODING_AGENTS[0].id ? 0 : 1,
              borderTopColor: c.border,
              gap: 8,
            }}
          >
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>{label}</Text>
              <Text style={{
                color: authed ? "#22c55e" : loading ? c.textMuted : "#f59e0b",
                fontSize: 11,
                marginTop: 2,
              }}>
                {loading && !row ? "checking…" : authed ? "✓ signed in" : "not signed in"}
              </Text>
            </View>
            {authed ? null : (
              <Pressable
                onPress={() => setAuthModalRunner(id)}
                style={{
                  paddingHorizontal: 12, paddingVertical: 6, borderRadius: 8,
                  backgroundColor: "#f59e0b22", borderWidth: 1, borderColor: "#f59e0b66",
                }}
              >
                <Text style={{ color: "#f59e0b", fontSize: 12, fontWeight: "700" }}>Sign in →</Text>
              </Pressable>
            )}
          </View>
        );
      })}

      {/* Default runner pill row. Tapping a pill writes through to userSettings.
          Stays usable even when the runner isn't authed yet — picking it as
          default is just a preference; sign-in is a separate action. */}
      <Text style={{
        color: c.textMuted, fontSize: 11, fontWeight: "600",
        marginTop: 14, marginBottom: 6,
      }}>
        Default runner
      </Text>
      <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
        {CODING_AGENTS.map(({ id, label }) => {
          const isDefault = currentDefault === id || (id === "claude" && currentDefault === "claude-code");
          const busy = defaultBusy === id;
          return (
            <Pressable
              key={`pill-${id}`}
              disabled={busy || isDefault}
              onPress={async () => {
                setDefaultBusy(id);
                try {
                  await setPrimaryRunnerForDevice(device.id, id);
                } catch (err: any) {
                  Alert.alert("Failed", err?.message || "Could not save default runner");
                } finally {
                  setDefaultBusy(null);
                }
              }}
              style={{
                paddingHorizontal: 10, paddingVertical: 6, borderRadius: 14,
                backgroundColor: isDefault ? c.accent + "22" : "transparent",
                borderWidth: 1, borderColor: isDefault ? c.accent + "88" : c.border,
                opacity: busy ? 0.5 : 1,
              }}
            >
              <Text style={{
                color: isDefault ? c.accent : c.textPrimary,
                fontSize: 12,
                fontWeight: isDefault ? "700" : "500",
              }}>
                {isDefault ? `★ ${label}` : label}
              </Text>
            </Pressable>
          );
        })}
      </View>

      <RunnerAuthModal
        visible={!!authModalRunner}
        runner={authModalRunner || ""}
        deviceName={device.name}
        target={target}
        onClose={() => setAuthModalRunner(null)}
        onCompleted={() => {
          setAuthModalRunner(null);
          // Re-poll status so the row flips to ✓ signed in immediately.
          void refresh();
        }}
      />
    </View>
  );
}

export default function DeviceDetailsModal({ device, agentVersion, visible, onClose }: DeviceDetailsModalProps) {
  const c = useColors();
  const t = device ? transportFor(device) : null;
  const [relayHealth, setRelayHealth] = useState<{ version?: string; tunnels?: number; activeDevices?: number } | null>(null);

  useEffect(() => {
    if (!visible || !t || !t.url) { setRelayHealth(null); return; }
    if (t.primary !== "yaver-public-relay" && t.primary !== "self-hosted-relay") {
      setRelayHealth(null);
      return;
    }
    let cancelled = false;
    const ac = new AbortController();
    void fetchRelayHealth(t.url, ac.signal).then((h) => {
      if (!cancelled) setRelayHealth(h);
    });
    return () => { cancelled = true; ac.abort(); };
  }, [visible, t?.url, t?.primary]);

  if (!device || !t) return null;

  const palette = transportToneRGB(t.tone);

  const lanIps = device.lanIps || [];
  const tailscaleIp = lanIps.find((ip) => /^100\.(6[4-9]|[7-9]\d|1[0-1]\d|12[0-7])\./.test(ip));
  const wslIp = lanIps.find((ip) => /^172\.(1[6-9]|2\d|3[0-1])\./.test(ip));
  const privateLanIps = lanIps.filter((ip) => /^(10\.|192\.168\.)/.test(ip) && ip !== tailscaleIp);

  const Row = ({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) => (
    <View style={{ flexDirection: "row", justifyContent: "space-between", paddingVertical: 4, gap: 12 }}>
      <Text style={{ color: c.textMuted, fontSize: 12 }}>{label}</Text>
      <Text style={{
        color: c.textPrimary,
        fontSize: 12,
        fontFamily: mono ? "Menlo" : undefined,
        flexShrink: 1,
        textAlign: "right",
      }}>
        {value || "—"}
      </Text>
    </View>
  );

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <View style={{
          flexDirection: "row", justifyContent: "space-between", alignItems: "center",
          paddingHorizontal: 16, paddingTop: 16, paddingBottom: 12,
          borderBottomWidth: 1, borderBottomColor: c.border,
        }}>
          <View style={{ flex: 1 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
              <Text style={{ color: c.textPrimary, fontSize: 18, fontWeight: "700" }}>{device.name}</Text>
              {device.alias ? (
                <View style={{
                  paddingHorizontal: 6, paddingVertical: 2, borderRadius: 6,
                  backgroundColor: "rgba(16,185,129,0.15)",
                  borderWidth: 1, borderColor: "rgba(16,185,129,0.45)",
                }}>
                  <Text style={{ color: "#a7f3d0", fontSize: 11, fontWeight: "700", fontFamily: "Menlo" }}>
                    @{device.alias}
                  </Text>
                </View>
              ) : null}
            </View>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>{device.os} · {device.host}:{device.port}</Text>
          </View>
          <Pressable onPress={onClose} style={{ paddingHorizontal: 12, paddingVertical: 6 }}>
            <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>Done</Text>
          </Pressable>
        </View>

        {/* Quick actions row — Hetzner-style "open shell from console".
            Mobile shell only works when this device is the active
            connection (the WS rides quicClient.baseUrl). For non-active
            devices we still show the button but it'll route through
            the shell screen, which guards against the wrong-device case. */}
        <ShellActionRow device={device} onClose={onClose} />

        <ScrollView style={{ flex: 1 }} contentContainerStyle={{ padding: 16 }}>
          {/* Connection */}
          <View style={{
            borderWidth: 1, borderColor: c.border, borderRadius: 8,
            backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
          }}>
            <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 8 }}>
              <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1 }}>CONNECTION</Text>
              <View style={{
                paddingHorizontal: 6, paddingVertical: 2, borderRadius: 6,
                backgroundColor: palette.bg, borderWidth: 1, borderColor: palette.border,
              }}>
                <Text style={{ color: palette.text, fontSize: 10, fontWeight: "700", letterSpacing: 0.6 }}>
                  {t.label.toUpperCase()}
                </Text>
              </View>
            </View>
            <Row label="Active path" value={t.detail} />
            {t.url ? <Row label="URL" value={t.url} mono /> : null}
            {(t.primary === "yaver-public-relay" || t.primary === "self-hosted-relay") ? (
              <Row
                label="Relay version"
                value={
                  relayHealth?.version
                    ? `v${relayHealth.version}${typeof relayHealth.tunnels === "number" ? ` · ${relayHealth.tunnels} tunnel${relayHealth.tunnels === 1 ? "" : "s"}` : ""}`
                    : "probing…"
                }
              />
            ) : null}
            {device.tunnelUrl ? <Row label="Tunnel URL" value={device.tunnelUrl} mono /> : null}
            {tailscaleIp ? <Row label="Tailscale IP" value={`${tailscaleIp}:${device.port ?? 18080}`} mono /> : null}
            {wslIp ? <Row label="WSL2 NAT IP" value={`${wslIp}:${device.port ?? 18080}`} mono /> : null}
            {privateLanIps.length ? <Row label="LAN IPs" value={privateLanIps.join(", ")} mono /> : null}
            {(device.publicEndpoints || []).length ? <Row label="Public endpoints" value={(device.publicEndpoints || []).join(", ")} mono /> : null}
            <Row label="Reported host" value={`${device.host}:${device.port ?? 18080}`} mono />
          </View>

          {/* Identity */}
          <View style={{
            borderWidth: 1, borderColor: c.border, borderRadius: 8,
            backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
          }}>
            <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
              IDENTITY
            </Text>
            <Row label="Device ID" value={device.id} mono />
            {!device.isGuest ? <AliasRow device={device} /> : device.alias ? <Row label="Alias" value={`@${device.alias}`} mono /> : null}
            {device.hwid ? <Row label="Hardware ID" value={device.hwid.slice(0, 16) + "…"} mono /> : null}
            {device.publicKey ? <Row label="Primary key" value={device.publicKey.slice(0, 16) + "…"} mono /> : null}
            {device.accessScope ? <Row label="Access scope" value={device.accessScope} /> : null}
            {device.priorityMode ? <Row label="Priority mode" value={device.priorityMode} /> : null}
            {device.isGuest && device.hostName ? <Row label="Shared from" value={device.hostName} /> : null}
          </View>

          {/* Runtime */}
          <View style={{
            borderWidth: 1, borderColor: c.border, borderRadius: 8,
            backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
          }}>
            <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
              RUNTIME
            </Text>
            <Row label="Status" value={device.online ? "Online" : "Offline"} />
            {agentVersion ? <Row label="Yaver version" value={`v${agentVersion}`} mono /> : null}
            {device.needsAuth ? <Row label="Auth" value="Needs auth" /> : null}
            <Row label="Last agent signal" value={timeAgo(device.lastSeen)} />
            {device.lastTunnelEvent?.at ? (
              <Row
                label="Live signal"
                value={`${device.lastTunnelEvent.online ? "relay-online" : "relay-offline"} (${timeAgo(device.lastTunnelEvent.at)})`}
              />
            ) : null}
            {device.peerState ? (
              <Row
                label="Peer bus"
                value={`${device.peerState}${device.peerLastSeen ? ` (${timeAgo(device.peerLastSeen)})` : ""}`}
              />
            ) : null}
            {device.edgeProfile ? (
              <>
                <Row label="Local inference" value={device.edgeProfile.supportsLocalInference ? "yes" : "no"} />
                <Row label="Max model" value={device.edgeProfile.maxModelClass} />
                {typeof device.edgeProfile.batteryPct === "number" ? (
                  <Row label="Battery" value={`${device.edgeProfile.batteryPct}%${device.edgeProfile.isCharging ? " (charging)" : ""}`} />
                ) : null}
                {device.edgeProfile.thermalState ? <Row label="Thermal" value={device.edgeProfile.thermalState} /> : null}
              </>
            ) : null}
          </View>

          {/* Recovery — owner-only. Hidden for guests because they
              can't factory-reset the host's auth (the agent enforces
              this via Convex check in handleAuthFactoryReset). */}
          {!device.isGuest ? (
            <View style={{
              borderWidth: 1, borderColor: c.border, borderRadius: 8,
              backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
            }}>
              <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
                RECOVERY
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600", marginBottom: 4 }}>
                Ping
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 10 }}>
                One-shot reachability + auth check (mirrors `yaver ping {device.alias || device.name}` and `yaver primary ping`). Confirms the box responds to /info and that its Yaver auth is valid for your account.
              </Text>
              <PingRow device={device} />
              <View style={{ height: 14 }} />

              {(device.needsAuth || !device.online) ? (
                <>
                  <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600", marginBottom: 4 }}>
                    Re-auth Yaver (headless)
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 10 }}>
                    {device.needsAuth
                      ? "The agent on this box is reachable but its Yaver session expired (or never claimed). Claim it back to your account over relay — agent identity, projects, and vault stay intact."
                      : "Convex hasn't seen a heartbeat from this box recently. If the agent is still up on its public endpoint, this opens a one-time browser sign-in that re-authorizes it without SSH and without wiping the machine. If the box is fully down, reach for `yaver primary auth` from your laptop or `yaver ssh primary` first."}
                  </Text>
                  <OwnerClaimAuthRow device={device} />
                  <View style={{ height: 14 }} />
                </>
              ) : null}
              <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600", marginBottom: 4 }}>
                Factory-reset Yaver auth
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 10 }}>
                Wipe the agent's local auth + device id and put it back into bootstrap mode. Use this only when normal Yaver recovery fails or the machine belongs to a different account. Projects, vault, and workspace files are preserved.
              </Text>
              <FactoryResetAuthRow device={device} />
            </View>
          ) : null}

          {/* Coding agents — auth status + sign-in + default runner picker.
              Replaces the old RUNNERS section, which surfaced active task
              entries (often empty) instead of the actually-useful
              "is claude/codex/opencode signed in on this box?" view. */}
          <CodingAgentsSection device={device} />

          {/* Active task runners — only shown if there are any. */}
          {(device.runners || []).filter((r) => r.pid).length > 0 ? (
            <View style={{
              borderWidth: 1, borderColor: c.border, borderRadius: 8,
              backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
            }}>
              <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
                ACTIVE TASKS
              </Text>
              {(device.runners || []).filter((r) => r.pid).map((r, i) => (
                <Row key={`r-${i}`} label={r.runnerId || "runner"} value={`${r.title || ""} · pid ${r.pid}`} />
              ))}
            </View>
          ) : null}
        </ScrollView>
      </View>
    </Modal>
  );
}
