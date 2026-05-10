// TaskTargetWizard — opt-in 3-pane picker that runs before the tasks
// `+` compose modal when DeviceState.multiTargetMode is true. Lets a
// user route a single task to a specific machine + coding agent
// instead of the currently-connected one.
//
// Pane A: pick a device. needsAuth devices route through
//   recoverDeviceAuth (with a confirm — recoverDeviceAuth internally
//   calls selectDevice and tears down the active connection).
// Pane B: pick a runner. needs-auth runners mount RunnerAuthModal
//   peered via `target=<deviceId>` so the user never leaves their
//   current connection during the OAuth handshake.
// Pane C: switch the QUIC client to the chosen device (selectDevice)
//   and hand off to the compose modal.
//
// Lazy audit per device on first expand (saves relay bandwidth).
// Single-device shortcut: skips Pane A entirely.

import React from "react";
import {
  Modal,
  View,
  Text,
  ScrollView,
  Pressable,
  ActivityIndicator,
} from "react-native";

import { useColors } from "../context/ThemeContext";
import { useDevice, type Device } from "../context/DeviceContext";
import { quicClient, type RunnerAuthStatusRow, type OpenCodeConfigSummary } from "../lib/quic";
import { connectionManager } from "../lib/connectionManager";
import RunnerAuthModal from "./RunnerAuthModal";

export interface TaskTarget {
  deviceId: string;
  deviceName: string;
  /** Tasks API runner id ("claude-code" / "codex" / "opencode"). */
  runner: "claude-code" | "codex" | "opencode";
  /** Optional pre-picked model from primaryModelByDevice. */
  model?: string;
  /** OpenCode-only: which agent (build / plan / custom) the user
   *  picked from the remote box's opencode.json. Forwarded as `mode`
   *  to sendTask, which the agent passes through to `--agent`. */
  opencodeMode?: string;
}

interface Props {
  visible: boolean;
  onCancel: () => void;
  onConfirmed: (target: TaskTarget) => void;
}

// Maps the tasks-API runner id (used by sendTask + selectedRunner state
// in tasks.tsx) to the runner-auth/audit id (used by /runner-auth/status
// rows and RunnerAuthModal). Codex + OpenCode share their id; Claude
// is "claude-code" in tasks but "claude" in runner-auth.
const RUNNERS: Array<{
  taskId: TaskTarget["runner"];
  auditId: "claude" | "codex" | "opencode";
  label: string;
}> = [
  { taskId: "claude-code", auditId: "claude", label: "Claude Code" },
  { taskId: "codex", auditId: "codex", label: "Codex" },
  { taskId: "opencode", auditId: "opencode", label: "OpenCode" },
];

type Pane = "device" | "agent" | "switching";

/** Runner ↔ model registry. First entry per runner is the "best
 *  default" we hand out when the user hasn't picked one for that
 *  (device, runner) pair yet. Model ids mirror the agent's
 *  `fallbackRunnerModels` (desktop/agent/httpserver.go) — keep them
 *  in sync; a model id passed to the wrong runner is what crashed
 *  Claude Code with `GPT-5.4` because Codex's default leaked across.
 *
 *  Why hardcoded here instead of fetched from /agent/runners:
 *    - The list rarely changes (new model every few months) and
 *      mirroring it on the client lets us render the picker
 *      synchronously without a spinner on every wizard open.
 *    - The agent endpoint already has the SAME list as a fallback
 *      for users on older runners, so the failure mode is symmetric.
 *  When the agent ships a new model, bump both this constant and
 *  fallbackRunnerModels in the same change. */
const MODELS_BY_RUNNER: Record<TaskTarget["runner"], { id: string; label: string }[]> = {
  "claude-code": [
    { id: "claude-opus-4-7", label: "Opus 4.7 (best)" },
    { id: "claude-sonnet-4-6", label: "Sonnet 4.6" },
    { id: "claude-haiku-4-5", label: "Haiku 4.5 (fast)" },
    { id: "claude-opus-4-6", label: "Opus 4.6" },
    { id: "claude-sonnet-4-5", label: "Sonnet 4.5" },
  ],
  codex: [
    { id: "gpt-5.5-pro", label: "GPT-5.5 Pro (best)" },
    { id: "gpt-5.5", label: "GPT-5.5" },
    { id: "gpt-5.4", label: "GPT-5.4" },
    { id: "gpt-5.4-mini", label: "GPT-5.4 Mini (fast)" },
    { id: "gpt-5.3-codex", label: "GPT-5.3 Codex" },
  ],
  // OpenCode picks model+provider via opencode.json on the host, not
  // via a wizard-level model id. The runner's own agents pane handles
  // that — leave the array empty so renderAgentPane skips the model
  // picker and shows the existing OpenCode mode picker instead.
  opencode: [],
};

/** True when `modelId` is a known model for `runner`. Strict membership
 *  check — without this the wizard would happily forward Codex's
 *  GPT-5.4 default into a Claude Code task and the agent process
 *  would crash on launch. */
function isModelCompatibleWithRunner(modelId: string | undefined | null, runner: TaskTarget["runner"]): boolean {
  if (!modelId) return false;
  const list = MODELS_BY_RUNNER[runner];
  return list.some((m) => m.id === modelId);
}

/** Return the "best" default model id for the runner (first entry in
 *  the list — by convention the highest-capability option). Used to
 *  pre-fill `pickedModel` whenever the user picks a runner without a
 *  prior compatible choice. */
function defaultModelForRunner(runner: TaskTarget["runner"]): string | null {
  const list = MODELS_BY_RUNNER[runner];
  if (!list || list.length === 0) return null;
  return list[0].id;
}

/** Distance between two semver-like strings. Returns 0 when equal, a
 *  positive integer when `current` is older, -1 when we can't decide
 *  (different major series, malformed strings, etc.) — render the version
 *  string but skip the "X behind" suffix in that case.
 *
 *  Yaver versions today are 1.99.<patch> on every channel, so the diff is
 *  almost always patch-only. Major + minor must match exactly; patch
 *  difference is the count returned. */
function versionPatchDistance(current: string, latest: string): number {
  const c = current.trim();
  const l = latest.trim();
  if (!c || !l) return -1;
  if (c === l) return 0;
  const parse = (s: string): [number, number, number] | null => {
    const m = /^(\d+)\.(\d+)\.(\d+)/.exec(s);
    if (!m) return null;
    return [Number(m[1]), Number(m[2]), Number(m[3])];
  };
  const cv = parse(c);
  const lv = parse(l);
  if (!cv || !lv) return -1;
  if (cv[0] !== lv[0] || cv[1] !== lv[1]) return -1;
  return Math.max(0, lv[2] - cv[2]);
}

export default function TaskTargetWizard({ visible, onCancel, onConfirmed }: Props) {
  const c = useColors();
  const {
    devices,
    activeDevice,
    selectDevice,
    recoverDeviceAuth,
    primaryRunnerByDevice,
    primaryModelByDevice,
    latestCliVersion,
    connectedDeviceIds,
  } = useDevice();
  // Quick lookup for "is there already a warm pooled connection to this
  // device?" — separate from activeDevice (the focused one). With the
  // multi-device pool a non-focused box can still be live, so the card
  // surfaces both states distinctly.
  const connectedSet = React.useMemo(() => new Set(connectedDeviceIds), [connectedDeviceIds]);

  const [pane, setPane] = React.useState<Pane>("device");
  const [pickedDevice, setPickedDevice] = React.useState<Device | null>(null);
  const [pickedRunner, setPickedRunner] = React.useState<TaskTarget["runner"] | null>(null);
  // null = audit attempted but failed (network/peer-proxy/etc) — UI must
  // render "Couldn't audit" rather than masquerading as "not installed".
  // [] = audit succeeded but agent reported no rows.
  const [auditByDevice, setAuditByDevice] = React.useState<Record<string, RunnerAuthStatusRow[] | null>>({});
  const [auditingId, setAuditingId] = React.useState<string | null>(null);
  // OpenCode config per device — agents (modes) + providers come from the
  // remote box's opencode.json so the wizard reflects the user's actual
  // setup on that machine, not a hardcoded list. Lazy-fetched the first
  // time OpenCode is selected for a given device.
  const [opencodeByDevice, setOpencodeByDevice] = React.useState<Record<string, OpenCodeConfigSummary | null>>({});
  const [opencodeLoadingId, setOpencodeLoadingId] = React.useState<string | null>(null);
  const [pickedOpencodeMode, setPickedOpencodeMode] = React.useState<string | null>(null);
  // Per-runner model id the user actually wants for THIS task. Kept
  // separate from primaryModelByDevice (which is keyed by device, not
  // runner) so switching from Codex back to Claude Code never lets a
  // gpt-5.4 default leak into the claude --model flag — the kind of
  // mismatch that crashes the agent process on launch.
  const [pickedModel, setPickedModel] = React.useState<string | null>(null);
  const [recoveryConfirm, setRecoveryConfirm] = React.useState<Device | null>(null);
  const [runnerAuthFor, setRunnerAuthFor] = React.useState<{
    deviceId: string;
    deviceName: string;
    runner: "claude" | "codex" | "opencode";
  } | null>(null);
  const [switchError, setSwitchError] = React.useState<string | null>(null);

  // Reset everything on close.
  React.useEffect(() => {
    if (visible) return;
    setPane("device");
    setPickedDevice(null);
    setPickedRunner(null);
    setAuditByDevice({});
    setAuditingId(null);
    setOpencodeByDevice({});
    setOpencodeLoadingId(null);
    setPickedOpencodeMode(null);
    setPickedModel(null);
    setRecoveryConfirm(null);
    setRunnerAuthFor(null);
    setSwitchError(null);
  }, [visible]);

  const fetchOpencode = React.useCallback(async (device: Device) => {
    if (!quicClient.isConnected) {
      setOpencodeByDevice((p) => ({ ...p, [device.id]: null }));
      return;
    }
    setOpencodeLoadingId(device.id);
    try {
      const cfg = await quicClient.getOpenCodeConfig(device.id);
      setOpencodeByDevice((p) => ({ ...p, [device.id]: cfg }));
    } catch {
      setOpencodeByDevice((p) => ({ ...p, [device.id]: null }));
    } finally {
      setOpencodeLoadingId(null);
    }
  }, []);

  const runAudit = React.useCallback(async (device: Device) => {
    if (!quicClient.isConnected) {
      setAuditByDevice((p) => ({ ...p, [device.id]: null }));
      return;
    }
    setAuditingId(device.id);
    try {
      // Prefer a direct connection to the audited device when one is
      // already pooled — same machine, no peer-proxy hop, no risk of
      // hitting errProxyLocal when the audit target happens to equal
      // the focused device. Falls back to the focused client (which
      // peerEndpoint already routes correctly for both self-target
      // and remote-peer cases).
      const direct = connectionManager.clientFor(device.id);
      const rows = direct.isConnected
        ? await direct.runnerAuthStatusOrNull()
        : await quicClient.runnerAuthStatusOrNull(device.id);
      setAuditByDevice((p) => ({ ...p, [device.id]: rows }));
    } catch {
      setAuditByDevice((p) => ({ ...p, [device.id]: null }));
    } finally {
      setAuditingId(null);
    }
  }, []);

  // Single-device shortcut: if there's exactly one online + authed
  // device, skip Pane A entirely.
  React.useEffect(() => {
    if (!visible || pane !== "device") return;
    const eligible = devices.filter((d) => d.online && !d.needsAuth);
    if (eligible.length === 1) {
      const only = eligible[0];
      setPickedDevice(only);
      // pre-seed runner from primaryRunnerByDevice if present
      const seed = primaryRunnerByDevice[only.id];
      if (seed) {
        const tid = seed === "claude" ? "claude-code" : (seed as TaskTarget["runner"]);
        setPickedRunner(tid);
      }
      setPane("agent");
      void runAudit(only);
    }
  }, [visible, pane, devices, primaryRunnerByDevice, runAudit]);

  // Keep `pickedModel` in lockstep with `pickedRunner`. When the user
  // toggles between Claude Code and Codex (or the auto-seed flips
  // either of them on), reset to a runner-compatible default — the
  // saved primaryModelByDevice value is reused only when it actually
  // belongs to the new runner. Without this gate, Codex's gpt-5.4
  // default would carry over into a Claude Code task and crash the
  // claude CLI on launch with an unknown-model error.
  React.useEffect(() => {
    if (!pickedDevice || !pickedRunner) {
      setPickedModel(null);
      return;
    }
    const saved = primaryModelByDevice[pickedDevice.id];
    if (saved && isModelCompatibleWithRunner(saved, pickedRunner)) {
      setPickedModel(saved);
      return;
    }
    setPickedModel(defaultModelForRunner(pickedRunner));
  }, [pickedDevice?.id, pickedRunner, primaryModelByDevice]);

  const handlePickDevice = async (device: Device) => {
    if (device.needsAuth && device.online) {
      setRecoveryConfirm(device);
      return;
    }
    if (!device.online) return;
    setPickedDevice(device);
    const seed = primaryRunnerByDevice[device.id];
    if (seed) {
      const tid = seed === "claude" ? "claude-code" : (seed as TaskTarget["runner"]);
      setPickedRunner(tid);
    } else {
      setPickedRunner(null);
    }
    setPane("agent");
    if (!auditByDevice[device.id]) void runAudit(device);
  };

  const handleRecoverConfirmed = async () => {
    const device = recoveryConfirm;
    if (!device) return;
    setRecoveryConfirm(null);
    try {
      await recoverDeviceAuth(device);
    } catch {
      // failures surface through DeviceContext.lastError
    }
  };

  const auditFor = (deviceId: string, auditId: string): RunnerAuthStatusRow | undefined =>
    auditByDevice[deviceId]?.find((r) => r.id === auditId);

  // Did the audit fail for this device? Distinguishes "we couldn't reach
  // the agent" (null) from "the agent answered with rows that don't list
  // this runner" (row undefined inside an array).
  const auditFailed = (deviceId: string): boolean =>
    auditByDevice[deviceId] === null;

  const handlePickRunner = (taskId: TaskTarget["runner"], auditId: "claude" | "codex" | "opencode") => {
    if (!pickedDevice) return;
    const row = auditFor(pickedDevice.id, auditId);
    if (!row || !row.installed) return;
    if (!row.authConfigured) {
      setRunnerAuthFor({ deviceId: pickedDevice.id, deviceName: pickedDevice.name, runner: auditId });
      return;
    }
    setPickedRunner(taskId);
    // OpenCode's modes/providers come from the remote box's
    // opencode.json. Fetch on first selection so the user picks from
    // their actual setup, not a hardcoded list. Codex + Claude don't
    // need a sub-step — they go straight to Continue.
    if (taskId === "opencode") {
      // Pre-seed mode from primaryModeByDevice if the user already
      // confirmed one for this device on a previous task.
      setPickedOpencodeMode(null);
      if (opencodeByDevice[pickedDevice.id] === undefined) {
        void fetchOpencode(pickedDevice);
      }
    } else {
      setPickedOpencodeMode(null);
    }
  };

  const continueDisabled = !pickedDevice
    || !pickedRunner
    || (pickedRunner === "opencode" && !pickedOpencodeMode);

  const handleContinue = async () => {
    if (!pickedDevice || !pickedRunner) return;
    setPane("switching");
    setSwitchError(null);
    try {
      // Skip the connection switch when we're already on this device.
      if (activeDevice?.id !== pickedDevice.id) {
        await selectDevice(pickedDevice);
      }
      if (!quicClient.isConnected) {
        throw new Error("Could not reach this device.");
      }
      // Only forward the model when it's actually compatible with the
      // runner the user just picked — peace of mind belt over the
      // useEffect that already keeps pickedModel in sync. A stray
      // gpt-5.* getting through to claude-code is what produced the
      // "Agent process crashed (attempt N/4)" loop on Mobiles-Mac-mini.
      const safeModel = pickedModel && isModelCompatibleWithRunner(pickedModel, pickedRunner)
        ? pickedModel
        : undefined;
      onConfirmed({
        deviceId: pickedDevice.id,
        deviceName: pickedDevice.name,
        runner: pickedRunner,
        model: safeModel,
        opencodeMode: pickedRunner === "opencode" && pickedOpencodeMode ? pickedOpencodeMode : undefined,
      });
    } catch (err: any) {
      setSwitchError(err?.message || "Failed to connect.");
    }
  };

  // ─────────────────────────────────────────────────────────────────
  // Render

  const sortedDevices = React.useMemo(() => {
    return [...devices].sort((a, b) => {
      const aReady = a.online && !a.needsAuth ? 0 : a.online ? 1 : 2;
      const bReady = b.online && !b.needsAuth ? 0 : b.online ? 1 : 2;
      if (aReady !== bReady) return aReady - bReady;
      return a.name.localeCompare(b.name);
    });
  }, [devices]);

  const renderDevicePane = () => (
    <ScrollView style={{ flex: 1 }} contentContainerStyle={{ padding: 16, paddingBottom: 32 }}>
      <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700", marginBottom: 4 }}>
        Pick a machine
      </Text>
      <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 20 }}>
        Where should this task run? Auth state is shown per device.
      </Text>
      {!activeDevice ? (
        <View style={{ padding: 16, borderRadius: 10, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard }}>
          <Text style={{ color: c.textPrimary, fontWeight: "600", marginBottom: 6 }}>
            Connect to any device first
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>
            The wizard queries each remote machine through the device you're attached to. Pick one from the Devices tab, then come back.
          </Text>
        </View>
      ) : sortedDevices.length === 0 ? (
        <Text style={{ color: c.textMuted, fontSize: 13 }}>No devices yet. Sign in on a machine first.</Text>
      ) : (
        sortedDevices.map((d) => {
          const ready = d.online && !d.needsAuth;
          const offlineNeedsAuth = d.needsAuth && !d.online;
          const disabled = !d.online || offlineNeedsAuth;
          // Agent-version line. Always render the bare version when present;
          // when we also have a latest reference and the device is on the
          // same major.minor series, append "· current" or "· N behind".
          const agentVer = (d.agentVersion || "").trim();
          const distance = agentVer && latestCliVersion
            ? versionPatchDistance(agentVer, latestCliVersion)
            : -1;
          const outdated = distance > 0;
          const versionSuffix = !agentVer
            ? ""
            : distance < 0
              ? ` · yaver ${agentVer}`
              : distance === 0
                ? ` · yaver ${agentVer} · current`
                : ` · yaver ${agentVer} · ${distance} behind`;
          return (
            <Pressable
              key={d.id}
              onPress={() => handlePickDevice(d)}
              disabled={disabled}
              style={({ pressed }) => ({
                marginBottom: 10,
                padding: 14,
                borderRadius: 10,
                borderWidth: 1,
                borderColor: ready ? c.border : c.borderSubtle,
                backgroundColor: c.bgCard,
                opacity: disabled ? 0.55 : pressed ? 0.85 : 1,
              })}
            >
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                <View style={{ flex: 1, paddingRight: 12 }}>
                  <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }} numberOfLines={1}>
                    {d.name}
                    {d.alias ? <Text style={{ color: c.textMuted, fontWeight: "400" }}>  @{d.alias}</Text> : null}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                    {d.online ? "Online" : "Offline"}
                    {d.needsAuth ? " · Needs Yaver auth" : ""}
                    {activeDevice?.id === d.id
                      ? " · Connected"
                      : connectedSet.has(d.id)
                        ? " · Live (pooled)"
                        : ""}
                    {versionSuffix && !outdated ? versionSuffix : ""}
                  </Text>
                  {outdated ? (
                    <Text style={{ color: "#d97706", fontSize: 11, marginTop: 2, fontWeight: "600" }}>
                      yaver {agentVer} · {distance} version{distance === 1 ? "" : "s"} behind {latestCliVersion}
                    </Text>
                  ) : null}
                </View>
                {ready ? (
                  <Text style={{ color: c.accent, fontSize: 18 }}>›</Text>
                ) : d.needsAuth && d.online ? (
                  <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>AUTHENTICATE</Text>
                ) : (
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>Wake first</Text>
                )}
              </View>
            </Pressable>
          );
        })
      )}
    </ScrollView>
  );

  const renderAgentPane = () => {
    if (!pickedDevice) return null;
    const rows = auditByDevice[pickedDevice.id];
    return (
      <ScrollView style={{ flex: 1 }} contentContainerStyle={{ padding: 16, paddingBottom: 32 }}>
        <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700", marginBottom: 4 }}>
          Pick a coding agent
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 20 }} numberOfLines={2}>
          On {pickedDevice.name}. Tap an agent that needs auth to set it up here.
        </Text>
        {auditingId === pickedDevice.id || rows === undefined ? (
          <View style={{ flexDirection: "row", alignItems: "center", paddingVertical: 12 }}>
            <ActivityIndicator color={c.accent} />
            <Text style={{ color: c.textMuted, marginLeft: 10, fontSize: 12 }}>Checking agent state…</Text>
          </View>
        ) : (
          RUNNERS.map(({ taskId, auditId, label }) => {
            const row = auditFor(pickedDevice.id, auditId);
            const failed = auditFailed(pickedDevice.id);
            const installed = !!row?.installed;
            const authed = !!row?.authConfigured;
            const ready = installed && authed;
            const selected = pickedRunner === taskId;
            const subtitle = failed
              ? "Couldn't audit this device — tap to retry"
              : !installed
                ? "Not installed on this device"
                : authed
                  ? `Ready${row?.version ? ` · ${row.version}` : ""}`
                  : "Needs auth — tap to set up";
            return (
              <Pressable
                key={taskId}
                onPress={() => failed ? runAudit(pickedDevice) : handlePickRunner(taskId, auditId)}
                disabled={!failed && !installed}
                style={({ pressed }) => ({
                  marginBottom: 10,
                  padding: 14,
                  borderRadius: 10,
                  borderWidth: 1,
                  borderColor: selected ? c.accent : c.border,
                  backgroundColor: c.bgCard,
                  opacity: (!failed && !installed) ? 0.5 : pressed ? 0.85 : 1,
                })}
              >
                <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                  <View style={{ flex: 1, paddingRight: 12 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }}>{label}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>{subtitle}</Text>
                  </View>
                  {ready ? (
                    selected ? (
                      <Text style={{ color: c.accent, fontWeight: "700", fontSize: 13 }}>SELECTED</Text>
                    ) : (
                      <Text style={{ color: c.accent, fontSize: 18 }}>›</Text>
                    )
                  ) : installed ? (
                    <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>AUTHENTICATE</Text>
                  ) : null}
                </View>
              </Pressable>
            );
          })
        )}
        {/* Per-runner model picker. Claude Code (Opus / Sonnet / Haiku)
            and Codex (GPT-5 family) need a model. Picking the wrong
            family is what crashed claude-cli on Mobiles-Mac-mini —
            Codex's gpt-5.4 default carried over when the user switched
            runners — so the picker is unconditional for these two and
            the default lights up the highest-capability option. The
            useEffect above keeps `pickedModel` in sync with the runner
            so we never forward a mismatched id. */}
        {pickedRunner && (pickedRunner === "claude-code" || pickedRunner === "codex") ? (
          <View style={{ marginTop: 4, marginBottom: 12 }}>
            <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "700", marginBottom: 8, letterSpacing: 0.5 }}>
              MODEL
            </Text>
            {MODELS_BY_RUNNER[pickedRunner].map((m) => {
              const sel = pickedModel === m.id;
              return (
                <Pressable
                  key={m.id}
                  onPress={() => setPickedModel(m.id)}
                  style={({ pressed }) => ({
                    marginBottom: 8,
                    padding: 12,
                    borderRadius: 8,
                    borderWidth: 1,
                    borderColor: sel ? c.accent : c.border,
                    backgroundColor: c.bgCard,
                    opacity: pressed ? 0.85 : 1,
                  })}
                >
                  <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                    <View style={{ flex: 1, paddingRight: 10 }}>
                      <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>
                        {m.label}
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }} numberOfLines={1}>
                        {m.id}
                      </Text>
                    </View>
                    {sel ? (
                      <Text style={{ color: c.accent, fontWeight: "700", fontSize: 12 }}>SELECTED</Text>
                    ) : null}
                  </View>
                </Pressable>
              );
            })}
          </View>
        ) : null}
        {/* OpenCode sub-step: agents (modes) + providers come from the
            remote box's opencode.json so the picker reflects that
            machine's actual setup, not a hardcoded list. Only rendered
            when OpenCode is the picked runner (and authed, since
            handlePickRunner gates on authConfigured before flipping
            pickedRunner to opencode). */}
        {pickedRunner === "opencode" ? (
          (() => {
            const cfg = opencodeByDevice[pickedDevice.id];
            const loading = opencodeLoadingId === pickedDevice.id || cfg === undefined;
            const agents = (cfg?.agents || []).filter((a) => !!a?.name);
            const fallback = ["build", "plan"]; // sensible defaults if config probe failed
            const showFallback = !loading && agents.length === 0;
            return (
              <View style={{ marginTop: 4, marginBottom: 12 }}>
                <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "700", marginBottom: 8, letterSpacing: 0.5 }}>
                  OPENCODE AGENT
                </Text>
                {loading ? (
                  <View style={{ flexDirection: "row", alignItems: "center", paddingVertical: 8 }}>
                    <ActivityIndicator color={c.accent} />
                    <Text style={{ color: c.textMuted, marginLeft: 10, fontSize: 12 }}>
                      Reading opencode.json on {pickedDevice.name}…
                    </Text>
                  </View>
                ) : showFallback ? (
                  <>
                    <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 8 }}>
                      Couldn't read opencode.json on this device — using defaults.
                    </Text>
                    {fallback.map((mode) => {
                      const sel = pickedOpencodeMode === mode;
                      return (
                        <Pressable
                          key={mode}
                          onPress={() => setPickedOpencodeMode(mode)}
                          style={({ pressed }) => ({
                            marginBottom: 8,
                            padding: 12,
                            borderRadius: 8,
                            borderWidth: 1,
                            borderColor: sel ? c.accent : c.border,
                            backgroundColor: c.bgCard,
                            opacity: pressed ? 0.85 : 1,
                          })}
                        >
                          <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>{mode}</Text>
                        </Pressable>
                      );
                    })}
                  </>
                ) : (
                  agents.map((a) => {
                    const sel = pickedOpencodeMode === a.name;
                    return (
                      <Pressable
                        key={a.name}
                        onPress={() => setPickedOpencodeMode(a.name)}
                        style={({ pressed }) => ({
                          marginBottom: 8,
                          padding: 12,
                          borderRadius: 8,
                          borderWidth: 1,
                          borderColor: sel ? c.accent : c.border,
                          backgroundColor: c.bgCard,
                          opacity: pressed ? 0.85 : 1,
                        })}
                      >
                        <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                          <View style={{ flex: 1, paddingRight: 10 }}>
                            <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }} numberOfLines={1}>
                              {a.name}
                              {a.isBuiltin ? <Text style={{ color: c.textMuted, fontWeight: "400" }}>  (builtin)</Text> : null}
                            </Text>
                            {a.model || a.description ? (
                              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }} numberOfLines={2}>
                                {a.description || a.model}
                              </Text>
                            ) : null}
                          </View>
                          {sel ? <Text style={{ color: c.accent, fontWeight: "700", fontSize: 12 }}>SELECTED</Text> : null}
                        </View>
                      </Pressable>
                    );
                  })
                )}
                {!loading && cfg && (cfg.providers?.length || 0) > 0 ? (
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                    Providers on this device: {(cfg.providers || []).map((p) => p.name || p.id).filter(Boolean).join(", ")}
                  </Text>
                ) : null}
              </View>
            );
          })()
        ) : null}
        <View style={{ height: 16 }} />
        <Pressable
          onPress={handleContinue}
          disabled={continueDisabled}
          style={({ pressed }) => ({
            backgroundColor: continueDisabled ? c.border : c.accent,
            paddingVertical: 14,
            borderRadius: 10,
            alignItems: "center",
            opacity: pressed ? 0.85 : 1,
          })}
        >
          <Text style={{ color: continueDisabled ? c.textMuted : "#000", fontWeight: "700" }}>
            {continueDisabled ? "Pick an agent to continue" : "Continue"}
          </Text>
        </Pressable>
        {/* "Back to machines" used to be a faint gray link at the bottom
            and users were missing it — it's the only escape hatch when
            the picked machine has every runner stuck on "needs auth"
            (Continue is disabled, Cancel kills the whole wizard). Promoted
            to an outlined accent button on the same visual weight tier as
            Continue so it's findable at a glance. */}
        <Pressable
          onPress={() => setPane("device")}
          style={({ pressed }) => ({
            marginTop: 10,
            paddingVertical: 13,
            borderRadius: 10,
            borderWidth: 1.5,
            borderColor: c.accent,
            alignItems: "center",
            flexDirection: "row",
            justifyContent: "center",
            backgroundColor: pressed ? c.bgCard : "transparent",
            opacity: pressed ? 0.85 : 1,
          })}
        >
          <Text style={{ color: c.accent, fontSize: 16, fontWeight: "700", marginRight: 6 }}>‹</Text>
          <Text style={{ color: c.accent, fontSize: 14, fontWeight: "700", letterSpacing: 0.3 }}>
            Back to machines
          </Text>
        </Pressable>
      </ScrollView>
    );
  };

  const renderSwitchingPane = () => (
    <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 24 }}>
      {switchError ? (
        <>
          <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600", marginBottom: 6 }}>
            Couldn't switch
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 20, textAlign: "center" }}>
            {switchError}
          </Text>
          <Pressable
            onPress={() => setPane("agent")}
            style={({ pressed }) => ({
              backgroundColor: c.accent,
              paddingVertical: 12,
              paddingHorizontal: 22,
              borderRadius: 10,
              opacity: pressed ? 0.85 : 1,
            })}
          >
            <Text style={{ color: "#000", fontWeight: "700" }}>Try again</Text>
          </Pressable>
        </>
      ) : (
        <>
          <ActivityIndicator color={c.accent} size="large" />
          <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 14 }}>
            Connecting to {pickedDevice?.name}…
          </Text>
        </>
      )}
    </View>
  );

  return (
    <>
      <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onCancel}>
        <View style={{ flex: 1, backgroundColor: c.bg }}>
          <View
            style={{
              flexDirection: "row",
              alignItems: "center",
              justifyContent: "space-between",
              paddingHorizontal: 16,
              paddingTop: 14,
              paddingBottom: 10,
              borderBottomWidth: 1,
              borderBottomColor: c.border,
            }}
          >
            <Pressable onPress={onCancel} hitSlop={10}>
              <Text style={{ color: c.textMuted, fontSize: 14 }}>Cancel</Text>
            </Pressable>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>
              {pane === "device" ? "Step 1 of 2" : pane === "agent" ? "Step 2 of 2" : "Switching"}
            </Text>
            <View style={{ width: 50 }} />
          </View>
          {pane === "device" && renderDevicePane()}
          {pane === "agent" && renderAgentPane()}
          {pane === "switching" && renderSwitchingPane()}
        </View>
      </Modal>

      {/* Confirm sheet for Yaver-level recovery: recoverDeviceAuth tears
          down the active connection, so the user must opt in. */}
      <Modal
        visible={!!recoveryConfirm}
        transparent
        animationType="fade"
        onRequestClose={() => setRecoveryConfirm(null)}
      >
        <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.6)", alignItems: "center", justifyContent: "center", padding: 24 }}>
          <View style={{ backgroundColor: c.bgCard, borderRadius: 12, padding: 20, maxWidth: 380, width: "100%" }}>
            <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700", marginBottom: 8 }}>
              Authenticate {recoveryConfirm?.name}?
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 18 }}>
              This will switch your connection to that machine. Anything streaming here will pause.
            </Text>
            <View style={{ flexDirection: "row", justifyContent: "flex-end", gap: 12 }}>
              <Pressable onPress={() => setRecoveryConfirm(null)} style={({ pressed }) => ({ opacity: pressed ? 0.6 : 1, paddingVertical: 8, paddingHorizontal: 14 })}>
                <Text style={{ color: c.textMuted }}>Cancel</Text>
              </Pressable>
              <Pressable
                onPress={handleRecoverConfirmed}
                style={({ pressed }) => ({
                  backgroundColor: c.accent,
                  borderRadius: 8,
                  paddingVertical: 8,
                  paddingHorizontal: 16,
                  opacity: pressed ? 0.85 : 1,
                })}
              >
                <Text style={{ color: "#000", fontWeight: "700" }}>Continue</Text>
              </Pressable>
            </View>
          </View>
        </View>
      </Modal>

      {/* Runner-auth mid-step: peered via target=<deviceId> so we never
          leave the active connection. RunnerAuthModal accepts
          "claude" / "claude-code" — pass the audit id ("claude"). */}
      {runnerAuthFor ? (
        <RunnerAuthModal
          visible={!!runnerAuthFor}
          runner={runnerAuthFor.runner}
          deviceName={runnerAuthFor.deviceName}
          target={runnerAuthFor.deviceId}
          onClose={() => setRunnerAuthFor(null)}
          onCompleted={() => {
            const did = runnerAuthFor.deviceId;
            setRunnerAuthFor(null);
            const dev = devices.find((d) => d.id === did);
            if (dev) void runAudit(dev);
          }}
        />
      ) : null}
    </>
  );
}
