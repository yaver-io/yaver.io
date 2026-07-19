// Mobile mirror of web/components/dashboard/DevicesView.tsx →
// DeviceDetailsPanel + ConnectionSection. Surfaces the same
// transport classification, relay version, LAN/Tailscale/Cloudflare
// breakdown, and runtime info on the phone.

import React, { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Modal, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import * as Clipboard from "expo-clipboard";
import { useRouter } from "expo-router";
import { useDevice, type Device } from "../context/DeviceContext";
import { useColors, useTheme } from "../context/ThemeContext";
import { quicClient, type RunnerAuthStatusRow } from "../lib/quic";
import { getConvexSiteUrl } from "../lib/auth";
import { probeMobileDeviceStatus } from "../lib/deviceStatus";
import RunnerAuthModal from "./RunnerAuthModal";
import { OpenCodeConfigModal } from "./OpenCodeConfigModal";
import { ScreenlogSection } from "./ScreenlogSection";
import { NetCaptureSection } from "./NetCaptureSection";
import { StorageSection } from "./StorageSection";
import { ProcessMonitorSection } from "./ProcessMonitorSection";

const CODING_AGENTS: ReadonlyArray<{ id: "claude" | "codex" | "opencode"; label: string }> = [
  { id: "claude", label: "Claude Code" },
  { id: "codex", label: "Codex" },
  { id: "opencode", label: "OpenCode" },
];

// Static model list per runner — mirrors Convex backend/convex/aiModels.ts
// PREDEFINED_MODELS so this surface stays in sync with the agent's
// /agent/runners response without needing a network round-trip just to
// render the picker. modelIds are canonical full IDs that the
// underlying SDK / CLI accepts directly (`claude --model
// claude-sonnet-4-6` for Anthropic, `codex --model gpt-5.4` for
// OpenAI). When the Convex list grows, update both places in lockstep.
const MODELS_BY_RUNNER: Record<string, ReadonlyArray<{ id: string; label: string }>> = {
  claude: [
    { id: "claude-opus-4-7", label: "Opus 4.7" },
    { id: "claude-sonnet-4-6", label: "Sonnet 4.6" },
    { id: "claude-haiku-4-5-20251001", label: "Haiku 4.5" },
  ],
  codex: [
    { id: "gpt-5.3-codex", label: "GPT-5.3 Codex" },
    { id: "gpt-5.4", label: "GPT-5.4" },
    { id: "gpt-5.4-mini", label: "GPT-5.4 Mini" },
  ],
};
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

function formatMemoryMb(value: number | undefined): string | null {
  if (typeof value !== "number" || value <= 0) return null;
  if (value >= 1024) return `${(value / 1024).toFixed(value >= 10 * 1024 ? 0 : 1)} GB`;
  return `${Math.round(value)} MB`;
}

// Byte formatter for the agent-update download line. Local by the same
// logic as formatMemoryMb above: the only other copies live inside
// app/phone-projects.tsx and app/phone-project/[slug].tsx, both private
// to those screens, and importing a helper out of an expo-router route
// into a shared component would wire a component to a screen. Kept to
// MB like those copies — agent binaries are tens of MB, never GB.
function formatBytes(bytes: number): string {
  if (!bytes || bytes < 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function formatList(items: string[] | undefined): string | null {
  if (!Array.isArray(items)) return null;
  const cleaned = items.map((item) => String(item || "").trim()).filter(Boolean);
  return cleaned.length > 0 ? cleaned.join(", ") : null;
}

function DetailRow({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  const c = useColors();
  return (
    <View style={{ flexDirection: "row", alignItems: "flex-start", paddingVertical: 6 }}>
      <Text style={{ color: c.textMuted, fontSize: 12, width: 110 }}>{label}</Text>
      <Text style={{
        flex: 1,
        color: c.textPrimary,
        fontSize: 13,
        textAlign: "right",
        fontFamily: mono ? "Menlo" : undefined,
      }}>
        {value}
      </Text>
    </View>
  );
}

// Deploy capability, probed by the agent rather than guessed from the OS.
//
// The distinction is the whole point: `publishCapabilities` is a GOOS switch
// that claims any Mac ships iOS even with no Xcode and a keychain that cannot
// unlock headlessly. These come from running the toolchain — which makes AGE
// part of the meaning, so the probe time is always rendered. A green pill is
// "true as of then", never "true now".
const DEPLOY_TARGET_LABELS: Record<string, string> = {
  npm: "npm",
  testflight: "TestFlight",
  playstore: "Play internal",
  "playstore-production": "Play production",
  convex: "Convex",
  "convex-selfhosted": "Convex (self-hosted)",
  cloudflare: "Cloudflare",
  vercel: "Vercel",
  netlify: "Netlify",
  firebase: "Firebase",
  fly: "Fly",
  pages: "Pages",
  railway: "Railway",
  "supabase-db": "Supabase DB",
  "supabase-functions": "Supabase Fns",
};

// Two refresh cycles. One missed is normal (the box slept); two means it
// stopped reporting and the list must not be presented as current.
const DEPLOY_PROBE_STALE_MS = 12 * 60 * 60 * 1000;

function deployProbeAge(at: string | undefined): { text: string; stale: boolean } | null {
  if (!at) return null;
  const t = Date.parse(at);
  if (Number.isNaN(t)) return null;
  const ms = Date.now() - t;
  const stale = ms > DEPLOY_PROBE_STALE_MS;
  const mins = Math.floor(ms / 60000);
  if (mins < 1) return { text: "just now", stale };
  if (mins < 60) return { text: `${mins}m ago`, stale };
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return { text: `${hrs}h ago`, stale };
  return { text: `${Math.floor(hrs / 24)}d ago`, stale };
}

function DeployCapabilityRow({ device }: { device: Device }) {
  const c = useColors();
  const ready = device.deployCapabilities ?? [];
  const blocked = device.deployCapabilitiesBlocked ?? [];
  const age = deployProbeAge(device.deployCapabilitiesAt);

  // "Never probed" and "probed, ships nothing" are different facts. Collapsing
  // them would reintroduce exactly the ambiguity this field exists to remove.
  if (ready.length === 0 && blocked.length === 0) {
    return (
      <DetailRow
        label="Can deploy"
        value={<Text style={{ color: c.textMuted, fontSize: 12 }}>not reported yet (needs agent 1.99.319+)</Text>}
      />
    );
  }

  return (
    <View style={{ paddingVertical: 6 }}>
      <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 6 }}>Can deploy</Text>
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
        {ready.map((t) => (
          <View key={t} style={{ backgroundColor: c.successBg, borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3 }}>
            <Text style={{ color: c.success, fontSize: 12 }}>{DEPLOY_TARGET_LABELS[t] ?? t}</Text>
          </View>
        ))}
        {blocked.map((t) => (
          <View key={t} style={{ borderColor: c.border, borderWidth: 1, borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3 }}>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>{DEPLOY_TARGET_LABELS[t] ?? t}</Text>
          </View>
        ))}
      </View>
      {age ? (
        <Text style={{ color: age.stale ? c.warn : c.textMuted, fontSize: 11, marginTop: 6 }}>
          {age.stale ? "\u26a0 probed " : "probed "}{age.text}{age.stale ? " \u2014 may be out of date" : ""}
        </Text>
      ) : null}
    </View>
  );
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
/**
 * VoiceHintsRow — the names you SAY for this machine.
 *
 * Deliberately separate from AliasRow: an alias is one short token you type at
 * a shell (`yaver ssh pokayoke`); these are many, natural, and never typed —
 * "my mac mini", "the box at maltepe". They matter most in the car, where
 * Apple's CarPlay voice category forbids drawing a device picker, so the spoken
 * name is the ONLY handle a driver has on a machine. Comma-separated because a
 * chip editor is more taps than a driver-adjacent feature deserves.
 */
function VoiceHintsRow({ device }: { device: Device }) {
  const c = useColors();
  const { setDeviceVoiceHints } = useDevice();
  const [editing, setEditing] = useState(false);
  const current = device.voiceHints ?? [];
  const [draft, setDraft] = useState(current.join(", "));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!editing) {
      setDraft((device.voiceHints ?? []).join(", "));
      setError(null);
    }
  }, [device.voiceHints, editing]);

  const commit = async () => {
    const hints = draft
      .split(",")
      .map((h) => h.trim())
      .filter(Boolean);
    setSaving(true);
    setError(null);
    const res = await setDeviceVoiceHints(device, hints);
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
        <Text style={{ color: c.textMuted, fontSize: 12, width: 110 }}>Say it as</Text>
        <View style={{ flex: 1, flexDirection: "row", alignItems: "center" }}>
          <Text
            style={{
              color: current.length ? "#c4b5fd" : c.textMuted,
              fontSize: 13,
              flex: 1,
            }}
            numberOfLines={1}
          >
            {current.length ? current.join(" · ") : "tap to add spoken names"}
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
      <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 2 }}>Say it as</Text>
      <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 6 }}>
        Comma-separated. Used by car voice: “switch to my mac mini”.
      </Text>
      <TextInput
        value={draft}
        onChangeText={setDraft}
        autoFocus
        autoCapitalize="none"
        editable={!saving}
        placeholder="my mac mini, the box at maltepe"
        placeholderTextColor={c.textMuted}
        style={{
          color: c.textPrimary,
          backgroundColor: c.bgCard,
          borderWidth: 1,
          borderColor: c.border,
          borderRadius: 6,
          paddingHorizontal: 10,
          paddingVertical: 8,
          fontSize: 13,
        }}
      />
      <View style={{ flexDirection: "row", gap: 6, marginTop: 6 }}>
        <Pressable
          onPress={() => void commit()}
          disabled={saving}
          style={{
            paddingHorizontal: 12,
            paddingVertical: 8,
            borderRadius: 6,
            backgroundColor: "rgba(139,92,246,0.15)",
            borderWidth: 1,
            borderColor: "rgba(139,92,246,0.45)",
            opacity: saving ? 0.5 : 1,
          }}
        >
          <Text style={{ color: "#c4b5fd", fontSize: 12, fontWeight: "700" }}>
            {saving ? "..." : "Save"}
          </Text>
        </Pressable>
        <Pressable
          onPress={() => setEditing(false)}
          disabled={saving}
          style={{
            paddingHorizontal: 12,
            paddingVertical: 8,
            borderRadius: 6,
            borderWidth: 1,
            borderColor: c.border,
          }}
        >
          <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600" }}>Cancel</Text>
        </Pressable>
      </View>
      {error ? (
        <Text style={{ color: "#fda4af", fontSize: 11, marginTop: 6 }}>{error}</Text>
      ) : null}
    </View>
  );
}

// uniqueness errors verbatim so the user knows which alias is taken.
/**
 * Guest-only exit from a share, mirroring the web card's "Remove my access".
 * Two-step by design: this reaches Convex and drops every machine the host
 * shared, on all of the user's surfaces — not just this row.
 *
 * Reversible: the host can share again and the guest can accept again.
 */
function LeaveShareRow({ device }: { device: Device }) {
  const c = useColors();
  const { leaveSharedAccess } = useDevice();
  const [leaving, setLeaving] = useState(false);
  const hostLabel = device.hostName || device.hostEmail || "this host";

  const confirmLeave = () => {
    Alert.alert(
      `Remove your access to ${hostLabel}'s machines?`,
      `You'll lose access to every machine ${hostLabel} shared with you, on all your devices.\n\n${hostLabel} can share again later, and you can accept again.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Remove access",
          style: "destructive",
          onPress: async () => {
            setLeaving(true);
            try {
              const res = await leaveSharedAccess(device);
              Alert.alert(
                "Access removed",
                `You no longer have access to ${res.hostName}'s machines. They can share again whenever you both want.`,
              );
            } catch (e: any) {
              Alert.alert("Error", e?.message || "Failed to remove access");
            } finally {
              setLeaving(false);
            }
          },
        },
      ],
    );
  };

  return (
    <Pressable
      onPress={leaving ? undefined : confirmLeave}
      disabled={leaving}
      style={{ paddingVertical: 8, opacity: leaving ? 0.5 : 1 }}
    >
      <Text style={{ color: c.error, fontSize: 12, fontWeight: "700" }}>
        {leaving ? "Removing…" : "Remove my access"}
      </Text>
      <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>
        Drops every machine {hostLabel} shared with you. They can share again later.
      </Text>
    </Pressable>
  );
}

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
  const { activeDevice, connectionStatus, selectDevice } = useDevice();
  const isActive = Boolean(activeDevice && activeDevice.id === device.id && connectionStatus === "connected");
  const sshCommand = sshCommandForDevice(device);
  const directSSHHost = directSSHHostForDevice(device);

  return (
    <View style={{
      flexDirection: "row", flexWrap: "wrap", gap: 8, paddingHorizontal: 16, paddingTop: 12, paddingBottom: 4,
    }}>
      <Pressable
        onPress={() => {
          // Make this device the active connection if it isn't already; the
          // shell screen connects once the agent connection comes up (and has
          // its own picker to hop between boxes). No "connect first" friction.
          if (!isActive) {
            selectDevice(device).catch(() => {});
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
          <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 4 }}>(connects on open)</Text>
        ) : null}
      </Pressable>
      <Pressable
        onPress={() => {
          // Remote Desktop rides the active quicClient.baseUrl just like Shell.
          if (!isActive) {
            selectDevice(device).catch(() => {});
          }
          onClose();
          setTimeout(() => { router.push("/remote-desktop"); }, 200);
        }}
        style={{
          flexDirection: "row", alignItems: "center", gap: 6,
          paddingHorizontal: 12, paddingVertical: 8,
          borderRadius: 8,
          backgroundColor: isActive ? "rgba(217,70,239,0.12)" : "rgba(75,85,99,0.10)",
          borderWidth: 1,
          borderColor: isActive ? "rgba(217,70,239,0.45)" : c.border,
        }}
      >
        <Text style={{ color: isActive ? "#f0abfc" : c.textMuted, fontSize: 13, fontWeight: "700" }}>
          🖥  Desktop
        </Text>
      </Pressable>
      <Pressable
        onPress={() => {
          // Apple TV remote rides the active quicClient transport like Desktop.
          if (!isActive) {
            selectDevice(device).catch(() => {});
          }
          onClose();
          setTimeout(() => { router.push("/appletv-remote"); }, 200);
        }}
        style={{
          flexDirection: "row", alignItems: "center", gap: 6,
          paddingHorizontal: 12, paddingVertical: 8,
          borderRadius: 8,
          backgroundColor: isActive ? "rgba(2,132,199,0.12)" : "rgba(75,85,99,0.10)",
          borderWidth: 1,
          borderColor: isActive ? "rgba(2,132,199,0.45)" : c.border,
        }}
      >
        <Text style={{ color: isActive ? "#7dd3fc" : c.textMuted, fontSize: 13, fontWeight: "700" }}>
          📺  Apple TV
        </Text>
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

// RecycleBoxRow removed from mobile: provisioning/recycling/removing a
// cloud box is web-dashboard-only (App Store 3.1.3 — no in-app
// management of paid infra — and the phone stays a thin WhatsApp-Web
// companion). The agent `recycle` verb is still driven from web/CLI.

export interface DeviceDetailsModalProps {
  device: Device | null;
  agentVersion?: string | null;
  visible: boolean;
  onClose: () => void;
  // When `inline` is true, the body renders as a plain View instead
  // of inside a Modal — used by the tablet-landscape master-detail
  // shell on devices.tsx so the same content reads as a persistent
  // right pane. `visible` and `onClose` still apply: visible=false
  // collapses to nothing, the Done button still calls onClose.
  inline?: boolean;
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

// WatchdogRecoverRow asks the currently-active agent to SSH-recover
// THIS device (the one being viewed in the modal). Only shown when:
//   - we're connected to a different device (the watchdog candidate)
//   - this device is the offline/wedged target
// The flow: phone → watchdog (relay/direct) → SSH → wedged target.
// Idempotent: systemctl restart / launchctl kickstart / nohup fallback.
function WatchdogRecoverRow({ device }: { device: Device }) {
  const c = useColors();
  const { activeDevice, connectionStatus } = useDevice();
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<null | { ok: boolean; line: string }>(null);

  const watchdog =
    activeDevice && connectionStatus === "connected" && activeDevice.id !== device.id
      ? activeDevice
      : null;

  if (!watchdog) {
    return (
      <Text style={{ color: c.textMuted, fontSize: 11, fontStyle: "italic" }}>
        Connect to another device first — that device acts as the watchdog and SSHes into this one to restart its agent.
      </Text>
    );
  }

  const onPress = async () => {
    setBusy(true);
    setResult(null);
    try {
      const res = await quicClient.recoverPeer(device.id);
      if (res.ok) {
        setResult({ ok: true, line: res.outcome });
      } else {
        setResult({ ok: false, line: res.error });
      }
    } catch (e: any) {
      setResult({ ok: false, line: e?.message || String(e) });
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
          backgroundColor: "rgba(245,158,11,0.12)",
          borderWidth: 1,
          borderColor: "rgba(245,158,11,0.45)",
          opacity: busy ? 0.5 : 1,
        }}
      >
        <Text style={{ color: "#fcd34d", fontSize: 12, fontWeight: "700" }}>
          {busy ? `Recovering via ${watchdog.name}...` : `Recover via ${watchdog.name}`}
        </Text>
      </Pressable>
      {result ? (
        <View style={{ marginTop: 8 }}>
          <Text style={{ fontSize: 12, fontWeight: "600", color: result.ok ? "#a7f3d0" : "#fecdd3" }}>
            {result.ok ? "✓ " : "✗ "}{result.line}
          </Text>
        </View>
      ) : null}
    </View>
  );
}

// Light semver comparison — major.minor.patch numeric, ignores any
// pre-release suffix. Returns -1 / 0 / 1. Just enough for "is the
// device behind the latest GitHub release?" — we don't need the
// full semver spec on mobile and didn't want to ship a dependency
// for a 5-line helper.
function compareSemverLite(a: string, b: string): number {
  const parse = (s: string) =>
    s
      .replace(/^v/i, "")
      .split(/[.+-]/)
      .map((x) => Number.parseInt(x, 10))
      .filter((n) => !Number.isNaN(n));
  const pa = parse(a);
  const pb = parse(b);
  const len = Math.max(pa.length, pb.length);
  for (let i = 0; i < len; i++) {
    const x = pa[i] ?? 0;
    const y = pb[i] ?? 0;
    if (x !== y) return x < y ? -1 : 1;
  }
  return 0;
}

// Phase → label map for the agent self-update stream. Every phase the
// agent emits on /streams/agent-update maps to exactly one step, and the
// labels are copied verbatim from the web AgentUpdateModal's STEPS so
// both surfaces narrate an update with the same words. "starting" is a
// client-side pseudo-phase covering the gap between the POST and the
// first frame; it folds onto "queued".
const AGENT_UPDATE_STEPS: ReadonlyArray<{ phase: string; label: string }> = [
  { phase: "queued", label: "Preparing" },
  { phase: "fetch_release", label: "Checking GitHub for the new version" },
  { phase: "check", label: "Found a new version" },
  { phase: "download", label: "Downloading the new binary" },
  { phase: "extract", label: "Unpacking" },
  { phase: "replace", label: "Replacing the running binary" },
  { phase: "restart", label: "Restarting" },
  { phase: "ready", label: "Ready" },
];

// Progress fraction for the update bar. Steps carry the bar on their own
// except during download, where the byte counts fill that step's slot —
// otherwise the bar would sit still through the slowest phase (a ~50 MB
// pull on a Pi over cellular).
function agentUpdateProgressPct(
  phase: string,
  bytes: { read: number; total: number } | undefined,
): number {
  const total = AGENT_UPDATE_STEPS.length;
  const idx = Math.max(0, AGENT_UPDATE_STEPS.findIndex((s) => s.phase === phase));
  if (phase === "download" && bytes && bytes.total > 0) {
    const dl = Math.max(0, Math.min(1, bytes.read / bytes.total));
    return ((idx + dl) / total) * 100;
  }
  return ((idx + 1) / total) * 100;
}

// Coding agents auth + default-runner picker. Same agent surface (claude /
// codex / opencode) on every device, so we render the rows from a constant
// instead of relying on whatever the agent reports — that way "agent
// installed but never authed" still surfaces a Sign in button. authConfigured
// comes from /runner-auth/status (per-device, peered when not active).
export function CodingAgentsSection({ device }: { device: Device }) {
  const c = useColors();
  const {
    activeDevice,
    connectionStatus,
    primaryRunnerByDevice,
    primaryModelByDevice,
    setPrimaryRunnerForDevice,
  } = useDevice();
  // The Convex session token backs the update fallback. It isn't on the
  // DeviceState interface — same `as any` read PingRow and
  // WirelessPhonesSection already use in this file.
  const { token } = useDevice() as any;
  const [statusRows, setStatusRows] = useState<RunnerAuthStatusRow[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [authModalRunner, setAuthModalRunner] = useState<string | null>(null);
  // OpenCode doesn't use the Claude/Codex browser OAuth path. Route its
  // "Sign in" to the config sheet instead of the browser-auth modal,
  // which errors with "unsupported runner" for anything but claude/codex.
  const [showOpenCodeConfig, setShowOpenCodeConfig] = useState(false);
  const [defaultBusy, setDefaultBusy] = useState<string | null>(null);
  const [modelBusy, setModelBusy] = useState<string | null>(null);
  // Per-runner install state (claude / codex / opencode). Backs the
  // Install button shown next to the "not installed on agent"
  // subtitle. lastLine is the most recent npm progress line so a
  // long-running install on a Pi / ARM cloud box shows something
  // changing instead of looking frozen.
  const [installState, setInstallState] = useState<
    Record<string, { kind: "installing" | "ok" | "fail"; lastLine?: string; error?: string }>
  >({});
  // Agent-update state for THIS device. Backs the "vX.Y.Z → vA.B.C
  // Update" row at the top of the section, and mirrors the web Devices
  // view AgentUpdateModal's step model + vocabulary.
  //
  // Two paths, in preference order:
  //   1. direct — POST /agent/update on the box, then follow
  //      /streams/agent-update for live phases + download bytes. Only
  //      this path can show progress, so it is always tried first.
  //   2. requested — POST /devices/request-update on Convex, which
  //      parks a desiredAgentVersion on the device row that the agent
  //      claims off its next heartbeat. No progress to show (nobody is
  //      connected to the box), but it works when the box is asleep, on
  //      cellular, or behind a NAT we can't punch — which is the whole
  //      reason the fallback exists. Unreachable must never dead-end.
  //
  // SSE is local-device-only: handlePeerProxy buffers the upstream body,
  // so a peer'd stream yields nothing until the agent restarts. Peer
  // targets fall back to polling /info — same as before this row grew a
  // progress bar.
  const [latestVersion, setLatestVersion] = useState<string>("");
  const [latestProbed, setLatestProbed] = useState(false);
  const [updateState, setUpdateState] = useState<
    | { kind: "idle" }
    | { kind: "updating"; phase: string; bytes?: { read: number; total: number } }
    | { kind: "requested"; version: string }
    | { kind: "done"; newVersion: string }
    | { kind: "fail"; error: string }
  >({ kind: "idle" });
  // Live SSE unsubscribe for the in-flight update, held in a ref so the
  // unmount cleanup can reach it without re-running runUpdate.
  const stopUpdateStreamRef = useRef<(() => void) | null>(null);
  useEffect(() => () => {
    stopUpdateStreamRef.current?.();
    stopUpdateStreamRef.current = null;
  }, []);

  const isActive = Boolean(activeDevice && activeDevice.id === device.id && connectionStatus === "connected");
  // /runner-auth/status routes via /peer/<id> when target is set; pass it
  // for non-active devices so the agent forwards the call to the right peer.
  const target = isActive ? undefined : device.id;
  const currentDefault = primaryRunnerByDevice[device.id] || "";
  const currentModel = primaryModelByDevice[device.id] || "";

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

  // Probe agent-update on mount + whenever target changes so the
  // version chip shows the current → latest gap as soon as the
  // sheet opens. `latestProbed` distinguishes "probe still in flight"
  // from "probe came back empty" (an unreachable box) — without it the
  // Update button, which now shows whenever the box isn't *confirmed*
  // current, would flash on for every device and then vanish a moment
  // later on the boxes that are already up to date.
  useEffect(() => {
    let cancelled = false;
    setLatestProbed(false);
    (async () => {
      const st = await quicClient.getAgentUpdateStatus(target);
      if (cancelled) return;
      const lv = String(st?.latestVersion || "").replace(/^v/i, "");
      setLatestVersion(lv);
      setLatestProbed(true);
    })();
    return () => { cancelled = true; };
  }, [target]);

  const findStatus = (id: string): RunnerAuthStatusRow | undefined =>
    (statusRows || []).find((r) => {
      const rid = String(r.id || "").toLowerCase();
      return rid === id || (id === "claude" && rid === "claude-code");
    });

  const currentVersion = String(device.agentVersion || "").replace(/^v/i, "");
  // Offer the action unless we've CONFIRMED the box is current. The old
  // gate also required a known latestVersion, which reads fine until you
  // notice where latestVersion comes from: getAgentUpdateStatus, which
  // returns null whenever the box is unreachable. So the one case this
  // feature exists for — a box you can't reach — was the exact case that
  // hid the button. Mobile has no reachability-independent version oracle
  // (web fetches GitHub releases/latest directly), so "unknown" now means
  // "offer it" rather than "hide it": the worst case is a queued request
  // the box no-ops on, versus no way to update an offline box at all.
  const onLatest =
    !!currentVersion &&
    !!latestVersion &&
    compareSemverLite(currentVersion, latestVersion) >= 0;
  const updateAvailable = latestProbed && !onLatest;

  // Fallback path: park desired state on the device row via Convex and
  // let the box pick it up on its next heartbeat. Deliberately does not
  // pin `version` — "latest" resolves at apply time on the box, so a
  // request that sits for a day still installs the newest release rather
  // than whatever we happened to have probed here.
  const requestUpdateViaConvex = useCallback(async () => {
    if (!token) {
      setUpdateState({
        kind: "fail",
        error: "Sign in on this phone to queue an update for this machine.",
      });
      return;
    }
    try {
      const res = await fetch(`${getConvexSiteUrl()}/devices/request-update`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ deviceId: device.id, version: "latest" }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        // Convex authors these (Unauthorized / Device not found) for a
        // human, so they are safe to show verbatim — unlike a transport
        // error string, which is what the direct path guards against.
        setUpdateState({ kind: "fail", error: body?.error || `HTTP ${res.status}` });
        return;
      }
      setUpdateState({ kind: "requested", version: String(body?.requestedVersion || "latest") });
    } catch {
      // The phone can't even reach Convex — that's this device's network,
      // not the box's, and it's the one failure the user can act on.
      setUpdateState({
        kind: "fail",
        error: "No connection — check this phone's network and try again.",
      });
    }
  }, [token, device.id]);

  const runUpdate = useCallback(async () => {
    stopUpdateStreamRef.current?.();
    stopUpdateStreamRef.current = null;
    setUpdateState({ kind: "updating", phase: "queued" });

    // 1. CONNECT FIRST. Only the direct path can show live progress, so
    //    always try the box before parking desired state.
    //
    //    quicClient.isConnected is checked up front because
    //    triggerAgentUpdate calls assertConnected(), which throws the raw
    //    "QuicClient is not connected. Call connect() first." string —
    //    the exact leak app/(tabs)/tasks.tsx:2945 documents. A device row
    //    can read "online" (presence is heartbeat-derived) while the QUIC
    //    client is mid-handshake or dropped, so this is a routine state,
    //    not an error. Every unreachable shape lands in the same place:
    //    the Convex fallback, never a dead-end.
    if (!quicClient.isConnected) {
      await requestUpdateViaConvex();
      return;
    }
    let resp: Awaited<ReturnType<typeof quicClient.triggerAgentUpdate>>;
    try {
      resp = await quicClient.triggerAgentUpdate(target);
    } catch {
      await requestUpdateViaConvex();
      return;
    }
    if (!resp.ok) {
      // Covers relay 502s, a dead peer behind the /peer proxy, and the
      // box dropping between the presence beat and this POST.
      await requestUpdateViaConvex();
      return;
    }
    if (resp.started === false) {
      // Reachable and refusing: the box's updater believes it is current.
      // Queueing desired state would only make it refuse again on the next
      // beat, so surface the agent's own reasoning instead — matches the
      // web modal, which explains the stale-updateRepo case the same way.
      setUpdateState({
        kind: "fail",
        error: `${device.name} reports it's already up to date (it has v${
          resp.currentVersion || currentVersion || "?"
        }). Its auto-update repo may be stale.`,
      });
      return;
    }

    // 2. Live progress — local device only (see the state comment).
    let streamError: string | null = null;
    if (!target) {
      stopUpdateStreamRef.current = quicClient.streamAgentUpdate((ev) => {
        if (ev.type !== "progress" || typeof ev.phase !== "string") return;
        if (ev.phase === "error") {
          streamError = (ev.text || "").trim() || "the agent reported an update error";
          return;
        }
        setUpdateState((prev) =>
          prev.kind === "updating"
            ? {
                kind: "updating",
                phase: ev.phase as string,
                bytes:
                  typeof ev.bytes === "number"
                    ? { read: ev.bytes, total: typeof ev.total === "number" ? ev.total : -1 }
                    : prev.bytes,
              }
            : prev,
        );
      });
    }

    // 3. Poll /info until the version flips or we time out (90 s budget —
    //    matches the web modal's restart watchdog). This, not the stream,
    //    is what confirms success: the agent replaces its own binary and
    //    restarts, which kills the stream by design.
    try {
      const deadline = Date.now() + 90_000;
      while (Date.now() < deadline) {
        await new Promise((res) => setTimeout(res, 2500));
        if (streamError) {
          setUpdateState({ kind: "fail", error: streamError });
          return;
        }
        const info = await quicClient.getInfoFor(target);
        const newV = String(info?.version || "").replace(/^v/i, "");
        if (newV && (latestVersion === "" || compareSemverLite(newV, latestVersion) >= 0)) {
          setUpdateState({ kind: "done", newVersion: newV });
          return;
        }
      }
      setUpdateState({
        kind: "fail",
        error: streamError || "Restart timed out — the box may need manual intervention.",
      });
    } finally {
      stopUpdateStreamRef.current?.();
      stopUpdateStreamRef.current = null;
    }
  }, [target, latestVersion, currentVersion, device.name, requestUpdateViaConvex]);

  const runInstall = useCallback(
    async (runnerId: "claude" | "codex" | "opencode") => {
      setInstallState((prev) => ({
        ...prev,
        [runnerId]: { kind: "installing", lastLine: "" },
      }));
      try {
        const result = await quicClient.installRunner(runnerId, {
          target,
          onProgress: (line) => {
            const trimmed = line.trim();
            if (!trimmed) return;
            setInstallState((prev) => ({
              ...prev,
              [runnerId]: {
                kind: "installing",
                lastLine: trimmed.slice(0, 100),
              },
            }));
          },
        });
        if (result.ok) {
          setInstallState((prev) => ({ ...prev, [runnerId]: { kind: "ok" } }));
          // Refresh /runner-auth/status so the row flips out of
          // "not installed on agent" into "<version> · not signed in"
          // and the Sign in button takes over.
          await refresh();
        } else {
          setInstallState((prev) => ({
            ...prev,
            [runnerId]: { kind: "fail", error: result.error || "install failed" },
          }));
        }
      } catch (err: any) {
        setInstallState((prev) => ({
          ...prev,
          [runnerId]: { kind: "fail", error: err?.message || String(err) },
        }));
      }
    },
    [refresh, target],
  );

  return (
    <View style={{
      borderWidth: 1, borderColor: c.border, borderRadius: 8,
      backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
    }}>
      <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
        CODING AGENTS
      </Text>

      {/* Agent self-update row. Reaches every device: the direct path
          rides quicClient.triggerAgentUpdate(target) → /agent/update
          (via /peer/<id> for non-active boxes), and anything it can't
          reach falls through to the Convex desired-state request. Shows
          current version always; "Update →" appears unless we've
          confirmed the box is already on latest. */}
      <View
        style={{
          paddingVertical: 8,
          borderBottomWidth: 1,
          borderBottomColor: c.border,
          marginBottom: 8,
        }}
      >
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <View style={{ flex: 1 }}>
            <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>
              Agent
            </Text>
            <Text
              style={{
                color:
                  updateState.kind === "fail"
                    ? "#ef4444"
                    : updateState.kind === "done"
                    ? "#22c55e"
                    : updateState.kind === "requested"
                    ? "#f59e0b"
                    : c.textMuted,
                fontSize: 11,
                marginTop: 2,
              }}
            >
              {updateState.kind === "updating"
                ? (AGENT_UPDATE_STEPS.find((s) => s.phase === updateState.phase)?.label ??
                   AGENT_UPDATE_STEPS[0].label)
                : updateState.kind === "requested"
                ? // The honest fallback line. Says what happened (we
                  // couldn't reach it), what we did instead (queued), and
                  // when it lands (next check-in) — no raw error, no
                  // false claim that anything is installing right now.
                  `couldn't reach ${device.name} — update queued; it installs ${
                    updateState.version === "latest" ? "the latest agent" : `v${updateState.version}`
                  } the next time it checks in`
                : updateState.kind === "done"
                ? `✓ now on v${updateState.newVersion}`
                : updateState.kind === "fail"
                ? `update failed — ${updateState.error}`
                : onLatest
                ? `v${currentVersion} · ✓ latest`
                : currentVersion && latestVersion
                ? `v${currentVersion} · update → v${latestVersion}`
                : currentVersion
                ? `v${currentVersion}`
                : "version unknown"}
            </Text>
          </View>
          {updateState.kind === "updating" ? (
            <Text style={{ color: "#f59e0b", fontSize: 12, fontWeight: "700" }}>⟳</Text>
          ) : updateAvailable && updateState.kind !== "done" && updateState.kind !== "requested" ? (
            <Pressable
              onPress={() => { void runUpdate(); }}
              style={{
                paddingHorizontal: 12, paddingVertical: 6, borderRadius: 8,
                backgroundColor: "#f59e0b22", borderWidth: 1, borderColor: "#f59e0b66",
              }}
            >
              <Text style={{ color: "#f59e0b", fontSize: 12, fontWeight: "700" }}>
                {updateState.kind === "fail" ? "Retry →" : "Update →"}
              </Text>
            </Pressable>
          ) : null}
        </View>

        {/* Progress — direct path only. The step counter mirrors the web
            modal ("step 4 of 8"); the bar blends download bytes into the
            download step so it keeps moving through the slow phase. */}
        {updateState.kind === "updating" ? (
          <View style={{ marginTop: 8 }}>
            <View style={{ flexDirection: "row", alignItems: "center", marginBottom: 4 }}>
              <Text style={{ color: c.textMuted, fontSize: 10, flex: 1 }}>
                {updateState.phase === "download" && updateState.bytes
                  ? updateState.bytes.total > 0
                    ? `${formatBytes(updateState.bytes.read)} of ${formatBytes(updateState.bytes.total)} (${Math.round(
                        (updateState.bytes.read * 100) / updateState.bytes.total,
                      )}%)`
                    : `${formatBytes(updateState.bytes.read)} downloaded`
                  : updateState.phase === "restart"
                  ? "Waiting for the agent to come back on the new version"
                  : `on ${device.name}`}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 10 }}>
                step{" "}
                {Math.min(
                  Math.max(0, AGENT_UPDATE_STEPS.findIndex((s) => s.phase === updateState.phase)) + 1,
                  AGENT_UPDATE_STEPS.length,
                )}{" "}
                of {AGENT_UPDATE_STEPS.length}
              </Text>
            </View>
            <View style={{ height: 4, borderRadius: 2, backgroundColor: c.border, overflow: "hidden" }}>
              <View
                style={{
                  height: 4,
                  borderRadius: 2,
                  backgroundColor: "#f59e0b",
                  width: `${Math.max(2, agentUpdateProgressPct(updateState.phase, updateState.bytes))}%`,
                }}
              />
            </View>
          </View>
        ) : null}
      </View>

      {/* Install + auth status + sign-in.
          Three-state subtitle:
            • not installed → "not installed on agent" (warning, no Sign in)
            • installed + not authed → "<version> · not signed in" (warning, Sign in)
            • installed + authed → "<version> · ✓ signed in" (ok)
          The agent (1.99.147+) reports `installed`, `authConfigured`, and
          `version` in /runner-auth/status. Older agents that omit `version`
          fall back to just the auth state — same UX as before. */}
      {CODING_AGENTS.map(({ id, label }) => {
        const row = findStatus(id);
        const installed = row?.installed === true;
        const authed = row?.authConfigured === true;
        const version = (row?.version || "").trim();
        const versionPrefix = version ? `${version} · ` : "";
        const inst = installState[id];
        // Installed + "signed in" ≠ usable: OpenCode can hold a key yet have its
        // default model point at a provider with no key. The agent reports that
        // via ready=false/error — surface it instead of a misleading "✓ signed
        // in", and keep the fix CTA available.
        const notReady = installed && authed && (row?.ready === false || !!(row?.error || "").trim());
        let subtitle: string;
        let tone: string;
        if (loading && !row) {
          subtitle = "checking…";
          tone = c.textMuted;
        } else if (inst?.kind === "installing") {
          subtitle = inst.lastLine ? `installing… ${inst.lastLine}` : "installing…";
          tone = "#f59e0b";
        } else if (inst?.kind === "fail") {
          subtitle = inst.error ? `install failed — ${inst.error}` : "install failed";
          tone = "#ef4444";
        } else if (!installed) {
          subtitle = "not installed on agent";
          tone = "#f59e0b";
        } else if (notReady) {
          subtitle = (row?.error || "installed but needs configuration").trim();
          tone = "#f59e0b";
        } else if (authed) {
          subtitle = `${versionPrefix}✓ signed in`;
          tone = "#22c55e";
        } else {
          subtitle = `${versionPrefix}not signed in`;
          tone = "#f59e0b";
        }
        // Show the fix CTA when not-authed OR authed-but-not-ready (config gap).
        // Missing runner → the Install button takes this spot instead.
        const showSignIn = installed && (!authed || notReady);
        const showInstall = !installed && !loading && inst?.kind !== "installing";
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
                color: tone,
                fontSize: 11,
                marginTop: 2,
              }}>
                {subtitle}
              </Text>
            </View>
            {showSignIn ? (
              <Pressable
                onPress={() => {
                  const nid = String(id).toLowerCase();
                  // opencode authenticates through provider config, not
                  // browser OAuth — send it to the config sheet.
                  if (nid === "opencode") {
                    setShowOpenCodeConfig(true);
                  } else {
                    setAuthModalRunner(id);
                  }
                }}
                style={{
                  paddingHorizontal: 12, paddingVertical: 6, borderRadius: 8,
                  backgroundColor: "#f59e0b22", borderWidth: 1, borderColor: "#f59e0b66",
                }}
              >
                <Text style={{ color: "#f59e0b", fontSize: 12, fontWeight: "700" }}>
                  {String(id).toLowerCase() === "opencode" ? "Set up →" : "Sign in →"}
                </Text>
              </Pressable>
            ) : null}
            {showInstall ? (
              <Pressable
                onPress={() => { void runInstall(id); }}
                style={{
                  paddingHorizontal: 12, paddingVertical: 6, borderRadius: 8,
                  backgroundColor: "#0ea5e922", borderWidth: 1, borderColor: "#0ea5e966",
                }}
              >
                <Text style={{ color: "#0ea5e9", fontSize: 12, fontWeight: "700" }}>Install</Text>
              </Pressable>
            ) : null}
            {inst?.kind === "installing" ? (
              <Text style={{ color: "#f59e0b", fontSize: 12, fontWeight: "700" }}>⟳</Text>
            ) : null}
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
                  // For opencode the real model + provider live in
                  // opencode.json on the agent, not in the static
                  // CODING_AGENTS map. Fetch them so Convex stores the
                  // user's actual config (e.g. zai / glm-4.7) instead
                  // of leaving model+provider empty — which would make
                  // every other surface (web devices view, sidebar)
                  // fall back to its own first-catalogue guess.
                  if (id === "opencode") {
                    const cfg = await quicClient.getOpenCodeConfig(target);
                    const m = (cfg?.model || "").trim();
                    const slash = m.indexOf("/");
                    const providerHint = slash > 0 ? m.slice(0, slash) : "";
                    await setPrimaryRunnerForDevice(
                      device.id,
                      id,
                      m || null,
                      null,
                      providerHint || null,
                    );
                  } else {
                    await setPrimaryRunnerForDevice(device.id, id);
                  }
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

      {/* Model picker for the currently-selected default runner. Only
          renders when the runner has multiple selectable models in the
          static list — keeps the modal clean for runners that don't
          have a model concept (opencode, etc.). Tap writes through to
          userSettings.primaryRunnerForDevice with both runnerId AND
          model so the per-device pick persists across sessions. */}
      {(() => {
        const runnerForModels =
          currentDefault === "claude-code" ? "claude" : currentDefault;
        const models = MODELS_BY_RUNNER[runnerForModels] || [];
        if (models.length < 2) return null;
        const effectiveModel = currentModel || models[0]?.id;
        return (
          <>
            <Text style={{
              color: c.textMuted, fontSize: 11, fontWeight: "600",
              marginTop: 14, marginBottom: 6,
            }}>
              Default model
            </Text>
            <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
              {models.map(({ id, label }) => {
                const isPicked = effectiveModel === id;
                const busy = modelBusy === id;
                return (
                  <Pressable
                    key={`model-pill-${id}`}
                    disabled={busy || isPicked}
                    onPress={async () => {
                      setModelBusy(id);
                      try {
                        // Re-pin the same runner with the new model.
                        await setPrimaryRunnerForDevice(device.id, runnerForModels, id);
                      } catch (err: any) {
                        Alert.alert("Failed", err?.message || "Could not save model");
                      } finally {
                        setModelBusy(null);
                      }
                    }}
                    style={{
                      paddingHorizontal: 10, paddingVertical: 6, borderRadius: 14,
                      backgroundColor: isPicked ? c.accent + "22" : "transparent",
                      borderWidth: 1, borderColor: isPicked ? c.accent + "88" : c.border,
                      opacity: busy ? 0.5 : 1,
                    }}
                  >
                    <Text style={{
                      color: isPicked ? c.accent : c.textPrimary,
                      fontSize: 12,
                      fontWeight: isPicked ? "700" : "500",
                    }}>
                      {isPicked ? `★ ${label}` : label}
                    </Text>
                  </Pressable>
                );
              })}
            </View>
          </>
        );
      })()}

      <RunnerAuthModal
        visible={!!authModalRunner}
        runner={authModalRunner || ""}
        deviceName={device.name}
        target={target}
        onClose={() => {
          setAuthModalRunner(null);
          // Always re-poll on close — not just on onCompleted. The agent
          // may have written the new auth file (codex device-auth, claude
          // paste-back) without the modal observing the "completed"
          // status flip (iOS suspends JS while the in-app browser sheet
          // covers the screen, so the polling interval can miss it). The
          // user closes the modal manually, and without this refresh the
          // CODING AGENTS section sticks on the stale "not signed in".
          void refresh();
        }}
        onCompleted={() => {
          setAuthModalRunner(null);
          void refresh();
        }}
      />
      <OpenCodeConfigModal
        visible={showOpenCodeConfig}
        startInAddProvider
        target={target}
        onClose={() => {
          setShowOpenCodeConfig(false);
          void refresh();
        }}
      />
    </View>
  );
}

function isHardwareProfileIncomplete(hardware: Device["hardwareProfile"]): boolean {
  if (!hardware) return true;
  const hasCpu = !!hardware.cpu;
  const hasRam = typeof hardware.ramMb === "number" && hardware.ramMb > 0;
  const hasCores = typeof hardware.numCores === "number" && hardware.numCores > 0;
  const hasArch = !!hardware.arch;
  // Any of these missing means the agent never sent (or failed to detect)
  // a complete profile — kick the refresh.
  return !(hasCpu && hasRam && hasCores && hasArch);
}

// WiFi-paired phone listing — what `yaver wireless detect` would show on
// this machine. Per the privacy contract this list never goes to Convex;
// it's fetched live from the agent's /wireless/devices endpoint each time
// the modal opens. Surfaces up to N devices reachable via xcrun devicectl
// (iOS) and `adb devices` IP:port serials (Android 11+ wireless debug).
interface AgentWireDevice {
  udid: string;
  name?: string;
  platform: "ios" | "android";
  os?: string;
}

function WirelessPhonesSection({ device }: { device: Device }) {
  const c = useColors();
  const { token } = useDevice() as any;
  const [phones, setPhones] = useState<AgentWireDevice[] | null>(null);
  const [hint, setHint] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    (async () => {
      const relay = quicClient.getRelayServers()[0];
      const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
      const candidates: string[] = [];
      if (relay?.httpUrl) {
        if (relay.password) headers["X-Relay-Password"] = relay.password;
        candidates.push(`${relay.httpUrl}/d/${encodeURIComponent(device.id)}`);
      }
      // Direct LAN fallback (works only when the phone is on the same
      // WiFi as the agent — same trade-off as the rest of the modal).
      candidates.push(`http://${device.host}:${device.port}`);
      let lastErr = "no candidates";
      for (const base of candidates) {
        if (cancelled) return;
        try {
          const res = await fetch(`${base}/wireless/devices`, {
            headers,
            signal: AbortSignal.timeout(8000),
          });
          if (!res.ok) {
            if (res.status === 404) {
              if (!cancelled) {
                setError("agent does not yet expose /wireless/devices (update the agent on this machine)");
                setLoading(false);
              }
              return;
            }
            lastErr = `HTTP ${res.status}`;
            continue;
          }
          const body = (await res.json()) as { devices?: AgentWireDevice[]; hint?: string };
          if (cancelled) return;
          setPhones(Array.isArray(body.devices) ? body.devices : []);
          setHint(typeof body.hint === "string" ? body.hint : "");
          setError(null);
          setLoading(false);
          return;
        } catch (err) {
          lastErr = err instanceof Error ? err.message : "fetch failed";
        }
      }
      if (!cancelled) {
        setError(lastErr);
        setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [device.id, device.host, device.port, token]);

  return (
    <View style={{
      borderWidth: 1, borderColor: c.border, borderRadius: 8,
      backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
    }}>
      <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
        WIFI-PAIRED PHONES
      </Text>
      {loading && !phones ? (
        <Text style={{ color: c.textMuted, fontSize: 12 }}>Probing this machine for WiFi-paired iPhones / Androids…</Text>
      ) : phones && phones.length > 0 ? (
        <View style={{ gap: 6 }}>
          {phones.map((d) => (
            <View
              key={`wp:${device.id}:${d.udid}`}
              style={{
                flexDirection: "row", alignItems: "center", gap: 8,
                paddingVertical: 4, paddingHorizontal: 8,
                borderWidth: 1, borderColor: c.border, borderRadius: 6,
                backgroundColor: c.bg,
              }}
            >
              <Text style={{ color: c.textPrimary, fontSize: 11, fontWeight: "700", textTransform: "uppercase" }}>
                {d.platform}
              </Text>
              <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }} numberOfLines={1}>
                {d.name || "(unknown)"}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 10, fontFamily: "Courier" }} numberOfLines={1}>
                {d.udid.length > 14 ? `${d.udid.slice(0, 12)}…` : d.udid}
              </Text>
            </View>
          ))}
        </View>
      ) : phones && phones.length === 0 ? (
        <Text style={{ color: c.textMuted, fontSize: 12 }}>
          No WiFi-paired phones detected{hint ? ` — ${hint}` : ""}.
        </Text>
      ) : error ? (
        <Text style={{ color: c.textMuted, fontSize: 12 }}>
          Phone list unavailable — {error}.
        </Text>
      ) : null}
    </View>
  );
}

function HardwareCapabilitiesSection({ device }: { device: Device }) {
  const c = useColors();
  const hardware = device.hardwareProfile;
  const osValue = [hardware?.os || device.os, hardware?.osVersion].filter(Boolean).join(" ");
  const iosSimulators = formatList(hardware?.iosSimulators);
  const androidEmulators = formatList(hardware?.androidEmulators);

  const incomplete = isHardwareProfileIncomplete(hardware);
  const [refreshState, setRefreshState] = useState<
    | { phase: "idle" }
    | { phase: "refreshing" }
    | { phase: "ok"; via: string }
    | { phase: "error"; message: string }
  >({ phase: "idle" });

  const triggerRefresh = useCallback(async () => {
    setRefreshState({ phase: "refreshing" });
    try {
      const res = await quicClient.refreshDeviceHardware(device.id);
      if (res.ok) {
        // Convex live query updates the device row → DetailRow re-renders.
        setRefreshState({ phase: "ok", via: res.via });
      } else {
        setRefreshState({ phase: "error", message: res.error });
      }
    } catch (e: unknown) {
      setRefreshState({ phase: "error", message: e instanceof Error ? e.message : String(e) });
    }
  }, [device.id]);

  // Auto-fire once when the modal first sees an incomplete profile for this
  // device. Re-arms if the user opens a different device. The refresh is
  // idempotent server-side, so a duplicate kick is harmless.
  useEffect(() => {
    if (!incomplete) return;
    if (refreshState.phase !== "idle") return;
    void triggerRefresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [device.id, incomplete]);

  // If a previously-empty profile filled in via Convex live query, drop the
  // "ok" / "refreshing" banner so the user just sees the populated rows.
  useEffect(() => {
    if (!incomplete && (refreshState.phase === "ok" || refreshState.phase === "refreshing")) {
      setRefreshState({ phase: "idle" });
    }
  }, [incomplete, refreshState.phase]);

  const placeholder = incomplete && refreshState.phase === "refreshing" ? "detecting…" : "—";

  return (
    <View style={{
      borderWidth: 1, borderColor: c.border, borderRadius: 8,
      backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
    }}>
      <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 8 }}>
        <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1 }}>
          HARDWARE
        </Text>
        {incomplete ? (
          <Pressable
            onPress={triggerRefresh}
            disabled={refreshState.phase === "refreshing"}
            style={{
              paddingHorizontal: 8,
              paddingVertical: 4,
              borderRadius: 6,
              borderWidth: 1,
              borderColor: c.border,
              opacity: refreshState.phase === "refreshing" ? 0.5 : 1,
            }}
          >
            <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600" }}>
              {refreshState.phase === "refreshing" ? "Refreshing…" : "Refresh"}
            </Text>
          </Pressable>
        ) : null}
      </View>
      <DetailRow label="OS" value={osValue || placeholder} />
      <DetailRow label="CPU" value={hardware?.cpu || placeholder} mono={!!hardware?.cpu} />
      <DetailRow label="RAM" value={formatMemoryMb(hardware?.ramMb) || placeholder} />
      <DetailRow label="GPU" value={hardware?.gpu || placeholder} mono={!!hardware?.gpu} />
      <DetailRow label="VRAM" value={formatMemoryMb(hardware?.vramMb) || placeholder} />
      <DetailRow label="Cores" value={typeof hardware?.numCores === "number" && hardware.numCores > 0 ? String(hardware.numCores) : placeholder} />
      <DetailRow label="Arch" value={hardware?.arch || placeholder} mono={!!hardware?.arch} />
      <DeployCapabilityRow device={device} />
      {iosSimulators ? <DetailRow label="iOS simulators" value={iosSimulators} mono /> : null}
      {androidEmulators ? <DetailRow label="Android emulators" value={androidEmulators} mono /> : null}
      {refreshState.phase === "error" ? (
        <Text style={{ color: "#fecdd3", fontSize: 11, marginTop: 6 }}>
          ✗ {refreshState.message}
        </Text>
      ) : null}
    </View>
  );
}

export default function DeviceDetailsModal({ device, agentVersion, visible, onClose, inline }: DeviceDetailsModalProps) {
  const c = useColors();
  const { isDark } = useTheme();
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

  const palette = transportToneRGB(t.tone, isDark);

  const lanIps = device.lanIps || [];
  const meshIp = lanIps.find((ip) => /^100\.(9[6-9]|10\d|11[0-1])\./.test(ip));
  const tailscaleIp = lanIps.find((ip) => /^100\.(6[4-9]|[7-9]\d|1[0-1]\d|12[0-7])\./.test(ip) && ip !== meshIp);
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

  // CRITICAL: do NOT declare a `Wrap` component here. The previous
  // version defined a wrapping component inside the render — React
  // treats every render as a new component type, unmounts and
  // remounts the Modal on every parent re-render, and during the
  // remount window the user sees a blank white pageSheet with no
  // children (heartbeats fire several times per minute, so a
  // rapid-tap puts the user squarely on a remount cycle). The body
  // is now built once below and conditionally placed inside a Modal
  // (phone / portrait tablet) or a plain View (master-detail inline
  // mode on tablet landscape). Same render output, zero remount
  // churn.
  if (inline && !visible) return null;

  const body = (
    <>
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

        {/* Quick actions row — cloud-console-style "open shell from console".
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
            {meshIp ? <Row label="Yaver Mesh IP" value={`${meshIp}:${device.port ?? 18080}`} mono /> : null}
            {tailscaleIp ? <Row label="Private network IP" value={`${tailscaleIp}:${device.port ?? 18080}`} mono /> : null}
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
            {!device.isGuest ? <VoiceHintsRow device={device} /> : null}
            {device.hwid ? <Row label="Hardware ID" value={device.hwid.slice(0, 16) + "…"} mono /> : null}
            {device.publicKey ? <Row label="Primary key" value={device.publicKey.slice(0, 16) + "…"} mono /> : null}
            {device.accessScope ? <Row label="Access scope" value={device.accessScope} /> : null}
            {device.priorityMode ? <Row label="Priority mode" value={device.priorityMode} /> : null}
            {device.isGuest && device.hostName ? <Row label="Shared from" value={device.hostName} /> : null}
            {device.isGuest ? <LeaveShareRow device={device} /> : null}
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
                      : "Convex hasn't seen a heartbeat from this box recently. If the agent is still up, this opens a one-time browser sign-in that re-authorizes it over the relay. If the box is fully down, this first asks an online device you're signed into to SSH in and restart Yaver, waits for it to come back, then re-auths over the relay — no laptop needed (it needs another online device to do the SSH for you)."}
                  </Text>
                  <OwnerClaimAuthRow device={device} />
                  <View style={{ height: 14 }} />

                  <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600", marginBottom: 4 }}>
                    Recover via watchdog
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 10 }}>
                    Ask another device you&apos;re signed into to SSH this one and restart its agent service. Useful when the agent process crashed but the box is still up — no OTP, no browser. Tries systemd, launchd, then a nohup fallback.
                  </Text>
                  <WatchdogRecoverRow device={device} />
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
              {/* Recycle/replace a cloud box is intentionally NOT on
                  mobile — adding or removing infrastructure is a
                  web-dashboard-only action (App Store 3.1.3; the phone
                  stays a thin WhatsApp-Web-style companion). Recovery
                  (re-auth, watchdog, factory-reset) stays — it mutates
                  agent state, never provisions or tears down a box. */}
            </View>
          ) : null}

          {/* Coding agents — auth status + sign-in + default runner picker.
              Replaces the old RUNNERS section, which surfaced active task
              entries (often empty) instead of the actually-useful
              "is claude/codex/opencode signed in on this box?" view. */}
          <CodingAgentsSection device={device} />
          <ScreenlogSection device={device} />
          <NetCaptureSection device={device} />
          <HardwareCapabilitiesSection device={device} />
          <StorageSection device={device} />
          <ProcessMonitorSection device={device} />
          <WirelessPhonesSection device={device} />

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
    </>
  );

  if (inline) {
    return <View style={{ flex: 1, backgroundColor: c.bg }}>{body}</View>;
  }
  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <View style={{ flex: 1, backgroundColor: c.bg }}>{body}</View>
    </Modal>
  );
}
