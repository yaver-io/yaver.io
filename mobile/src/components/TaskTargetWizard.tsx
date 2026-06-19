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
  Alert,
  Modal,
  View,
  Text,
  ScrollView,
  Pressable,
  ActivityIndicator,
} from "react-native";

import { useColors } from "../context/ThemeContext";
import { useDevice, type Device } from "../context/DeviceContext";
import { useResponsiveLayout } from "../hooks/useResponsiveLayout";
import { useTabletContentStyle } from "../hooks/useTabletContentStyle";
import { quicClient, type RunnerAuthStatusRow, type OpenCodeConfigSummary } from "../lib/quic";
import { connectionManager } from "../lib/connectionManager";
import { eligibleRemoteBoxDevices, versionPatchDistance } from "../lib/devicePicker";
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

// "switching" is the only out-of-band pane left after the wizard
// flattened device + agent + model into a single unified view —
// it covers the brief moment between hitting Continue and the
// compose modal taking over (or surfacing a "couldn't switch" error).
type Pane = "unified" | "switching";

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
    // Yaver's primary use case is BYOK with ChatGPT Max / Pro plans —
    // the Codex CLI accepts those accounts but the OpenAI API rejects
    // them for the "API-only" models (gpt-5.5, gpt-5.5-pro) with:
    //   "The 'gpt-5.5-pro' model is not supported when using Codex
    //    with a ChatGPT account."
    // We don't surface those at all so a free-tier vibing user never
    // ends up in the crash loop their plan doesn't allow.
    { id: "gpt-5.3-codex", label: "GPT-5.3 Codex" },
    { id: "gpt-5.4", label: "GPT-5.4" },
    { id: "gpt-5.4-mini", label: "GPT-5.4 Mini" },
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

export default function TaskTargetWizard({ visible, onCancel, onConfirmed }: Props) {
  const c = useColors();
  const layout = useResponsiveLayout();
  const tabletContent = useTabletContentStyle("regular");
  const {
    devices,
    activeDevice,
    selectDevice,
    recoverDeviceAuth,
    primaryRunnerByDevice,
    primaryModelByDevice,
    latestCliVersion,
    connectedDeviceIds,
    lastError,
  } = useDevice();
  // Quick lookup for "is there already a warm pooled connection to this
  // device?" — separate from activeDevice (the focused one). With the
  // multi-device pool a non-focused box can still be live, so the card
  // surfaces both states distinctly.
  const connectedSet = React.useMemo(() => new Set(connectedDeviceIds), [connectedDeviceIds]);

  const [pane, setPane] = React.useState<Pane>("unified");
  const [pickedDevice, setPickedDevice] = React.useState<Device | null>(null);
  const [pickedRunner, setPickedRunner] = React.useState<TaskTarget["runner"] | null>(null);
  // Ping results per device. The card subtitle reads as
  // "Connected · 87ms" when fresh, "Connected · pinging…" while
  // probing, "Live · last ping failed" when the pool client says
  // connected but a recent ping timed out — that mismatch is what
  // the user means by "make yaver ping show connected etc".
  // null = not pinged yet, undefined = in flight.
  const [pingByDevice, setPingByDevice] = React.useState<
    Record<string, { rttMs: number; ok: boolean; at: number } | null>
  >({});
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
  const [runnerSetupBusyKey, setRunnerSetupBusyKey] = React.useState<string | null>(null);
  const [runnerActionErrorByKey, setRunnerActionErrorByKey] = React.useState<Record<string, string>>({});
  const [switchError, setSwitchError] = React.useState<string | null>(null);

  // Reset everything on close.
  React.useEffect(() => {
    if (visible) return;
    setPane("unified");
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
    setRunnerSetupBusyKey(null);
    setRunnerActionErrorByKey({});
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

  // Per-device live ping. Confirms the pool's "connected" claim with a
  // round-trip /health probe — without it, a pool client that lost
  // tunnel state (relay password rotation, agent restart) would still
  // render as Connected in the wizard until a task actually failed.
  // 5-second timeout matches QuicClient.ping.
  const runPing = React.useCallback(async (device: Device) => {
    const direct = connectionManager.clientFor(device.id);
    if (!direct.isConnected) {
      // No pool client to ping. Wait until the user picks the device
      // (which connects it) before showing rtt.
      return;
    }
    try {
      const result = await direct.ping();
      setPingByDevice((p) => ({
        ...p,
        [device.id]: { rttMs: result.rttMs, ok: result.ok, at: Date.now() },
      }));
    } catch {
      setPingByDevice((p) => ({
        ...p,
        [device.id]: { rttMs: -1, ok: false, at: Date.now() },
      }));
    }
  }, []);

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
    // Stay in unified pane — picked device's agents render inline.
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
      if (auditId === "opencode") {
        Alert.alert(
          "OpenCode needs provider setup",
          "OpenCode is installed on this machine, but it still needs provider credentials or config on the remote box before it can run tasks.",
        );
        return;
      }
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
      // Skip the connection switch when the picked device's pool
      // client is already live — that's the multi-device fast path.
      // Without this, even a sibling box with a warm pool connection
      // got fed through selectDevice (which would race the warm-up
      // and sometimes flip the connection to "disconnected" in the
      // process). Now we only call selectDevice when the client
      // genuinely isn't connected yet.
      const directClient = connectionManager.clientFor(pickedDevice.id);
      if (!directClient.isConnected) {
        await selectDevice(pickedDevice);
      } else if (activeDevice?.id !== pickedDevice.id) {
        // Already pooled — just shift focus to it without re-connecting.
        connectionManager.setFocused(pickedDevice.id);
      }
      // Re-check the picked device's client directly (instead of going
      // through the focused-client Proxy). The Proxy's isConnected
      // depends on focus, which can race during the switch; the
      // per-device isConnected is the source of truth.
      if (!connectionManager.clientFor(pickedDevice.id).isConnected) {
        // Surface the underlying connect error when DeviceContext
        // captured one — generic "Could not reach this device" was
        // hiding the real reason (e.g. relay password mismatch,
        // device.host stale after DHCP renewal, agent down).
        const detail = (lastError || "").trim();
        throw new Error(
          detail
            ? `Couldn't reach ${pickedDevice.name}: ${detail}`
            : `Couldn't reach ${pickedDevice.name}.`,
        );
      }
      // Resolve the model SYNCHRONOUSLY here instead of trusting
      // `pickedModel` from state. The useEffect that keeps pickedModel
      // in lockstep with pickedRunner runs after a render commit —
      // when the user taps Claude Code and immediately taps Continue,
      // pickedModel can still be the previous runner's default
      // (e.g. Codex's gpt-5.4). That stale value passed to claude-cli
      // crashed the agent process every time the user picked
      // Claude Code from a wizard that had Codex pre-seeded. Compute
      // here from the user's most recent picks so there's no race.
      let safeModel: string | undefined = pickedModel ?? undefined;
      if (safeModel && !isModelCompatibleWithRunner(safeModel, pickedRunner)) {
        safeModel = undefined;
      }
      if (!safeModel) {
        const saved = primaryModelByDevice[pickedDevice.id];
        if (saved && isModelCompatibleWithRunner(saved, pickedRunner)) {
          safeModel = saved;
        } else {
          safeModel = defaultModelForRunner(pickedRunner) ?? undefined;
        }
      }
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

  const handleInstallRunner = async (
    device: Device,
    auditId: "claude" | "codex" | "opencode",
  ) => {
    const key = `${device.id}:${auditId}`;
    setRunnerSetupBusyKey(key);
    setRunnerActionErrorByKey((prev) => ({ ...prev, [key]: "" }));
    try {
      const result = await quicClient.runnerAuthSetup({
        runner: auditId,
        installIfMissing: true,
        allowInstallOnly: true,
      }, device.id);
      if (!result.ok) {
        throw new Error(result.error || `Couldn't install ${auditId}.`);
      }
      await runAudit(device);
      if (result.authConfigured) {
        const taskId = auditId === "claude" ? "claude-code" : auditId;
        setPickedDevice(device);
        setPickedRunner(taskId as TaskTarget["runner"]);
        return;
      }
      if (auditId === "claude" || auditId === "codex") {
        setRunnerAuthFor({ deviceId: device.id, deviceName: device.name, runner: auditId });
      }
    } catch (err: any) {
      setRunnerActionErrorByKey((prev) => ({
        ...prev,
        [key]: err?.message || `Couldn't install ${auditId}.`,
      }));
    } finally {
      setRunnerSetupBusyKey(null);
    }
  };

  // ─────────────────────────────────────────────────────────────────
  // Render

  // Eligible = either pool-connected (instant target) OR online with a
  // fresh heartbeat (tap-to-connect target). User asked for "show
  // other at least live (heartbeat machines) as well too" — those
  // boxes are tappable; we connect on pick. Devices needing yaver
  // auth or fully offline still get filtered out so the list stays
  // honest. Sort connected first, then by name.
  const eligibleDevices = React.useMemo(() => {
    return eligibleRemoteBoxDevices(devices, connectedSet, activeDevice?.id);
  }, [devices, connectedSet, activeDevice?.id]);

  // Auto-ping all connected devices when the wizard opens. Without
  // this, a stale pool client (relay password rotated, agent restarted
  // after our last connect) would render Connected without ever being
  // confirmed end-to-end — and the user would only find out via a
  // failed task. Pinging here surfaces the truth on the card before
  // they tap Continue.
  React.useEffect(() => {
    if (!visible) return;
    for (const d of eligibleDevices) {
      if (connectedSet.has(d.id)) {
        void runPing(d);
      }
    }
  }, [visible, eligibleDevices, connectedSet, runPing]);

  // Single-device shortcut: if there's exactly one machine in the
  // wizard's eligible list, auto-pick it on open. Inline agent
  // picker handles the rest — no pane swap. Aligned with
  // eligibleDevices (not the raw online filter) so the auto-pick
  // fires whenever the user-visible list collapses to a single row,
  // not whenever the underlying device count happens to be 1.
  React.useEffect(() => {
    if (!visible || pane !== "unified") return;
    if (eligibleDevices.length === 1) {
      const only = eligibleDevices[0];
      if (pickedDevice?.id === only.id) return;
      setPickedDevice(only);
      const seed = primaryRunnerByDevice[only.id];
      if (seed) {
        const tid = seed === "claude" ? "claude-code" : (seed as TaskTarget["runner"]);
        setPickedRunner(tid);
      }
      void runAudit(only);
    }
  }, [visible, pane, eligibleDevices, pickedDevice?.id, primaryRunnerByDevice, runAudit]);

  // Unified pane: a single scrolling view that lists every CONNECTED
  // device, expanding the picked one inline to show agents + model.
  // The user explicitly asked for "one page not two" — the previous
  // 2-step wizard (Pick a machine → Pick a coding agent) doubled the
  // tap budget for the common case of picking the same box you were
  // already on with a different agent.
  const renderUnifiedPane = () => (
    <ScrollView
      style={{ flex: 1 }}
      contentContainerStyle={[{ padding: 16, paddingBottom: 32 }, tabletContent]}
    >
      <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700", marginBottom: 4 }}>
        Send a task
      </Text>
      <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 20 }}>
        Pick a connected machine, then choose its agent and model.
      </Text>
      {!activeDevice ? (
        <View style={{ padding: 16, borderRadius: 10, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard }}>
          <Text style={{ color: c.textPrimary, fontWeight: "600", marginBottom: 6 }}>
            Connect to any device first
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>
            Pick one from the Devices tab and come back — the wizard only lists machines that have a live connection.
          </Text>
        </View>
      ) : eligibleDevices.length === 0 ? (
        <View style={{ padding: 16, borderRadius: 10, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard }}>
          <Text style={{ color: c.textPrimary, fontWeight: "600", marginBottom: 6 }}>
            No connected machines
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>
            Open the Devices tab, tap Connect on the box you want to use, then return here.
          </Text>
        </View>
      ) : (
        eligibleDevices.map((d) => {
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
          const expanded = pickedDevice?.id === d.id;
          const rows = auditByDevice[d.id];
          return (
            <View
              key={d.id}
              style={{
                marginBottom: 10,
                borderRadius: 10,
                borderWidth: expanded ? 1.5 : 1,
                borderColor: expanded ? c.accent : c.border,
                backgroundColor: c.bgCard,
                overflow: "hidden",
              }}
            >
              <Pressable
                onPress={() => handlePickDevice(d)}
                disabled={disabled}
                style={({ pressed }) => ({
                  padding: 14,
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
                      {(() => {
                        const ping = pingByDevice[d.id];
                        // Status copy is "what's the truest state we
                        // can tell the user right now". A successful
                        // ping wins (we know the agent is reachable);
                        // a failed ping after the pool said connected
                        // is the most actionable warning, since it
                        // means the next task probably crashes.
                        if (connectedSet.has(d.id)) {
                          if (ping && ping.ok) return `Connected · ${ping.rttMs}ms`;
                          if (ping && !ping.ok) return "Connected (pool) · ping failed";
                          return "Connected · pinging…";
                        }
                        return d.online ? "Live · tap to connect" : "Offline";
                      })()}
                      {activeDevice?.id === d.id ? " · Focused" : ""}
                      {versionSuffix && !outdated ? versionSuffix : ""}
                    </Text>
                    {outdated ? (
                      <Text style={{ color: "#d97706", fontSize: 11, marginTop: 2, fontWeight: "600" }}>
                        yaver {agentVer} · {distance} version{distance === 1 ? "" : "s"} behind {latestCliVersion}
                      </Text>
                    ) : null}
                  </View>
                  <Text style={{ color: c.accent, fontSize: 18 }}>{expanded ? "▾" : "›"}</Text>
                </View>
              </Pressable>
              {expanded ? renderInlineAgentSection(d, rows) : null}
            </View>
          );
        })
      )}
      <View style={{ height: 16 }} />
      {/* Sticky Continue button at the bottom of the unified scroll
          view. Disabled until both a device and a runner (with a
          model where required) are picked. */}
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
          {!pickedDevice
            ? "Pick a machine to continue"
            : !pickedRunner
              ? "Pick an agent to continue"
              : "Continue"}
        </Text>
      </Pressable>
    </ScrollView>
  );

  // Inline agent + model picker rendered under the picked device card
  // in the unified pane. Pulled out as a function so the pane render
  // stays readable.
  const renderInlineAgentSection = (
    d: Device,
    rows: RunnerAuthStatusRow[] | null | undefined,
  ) => (
    <View style={{ paddingHorizontal: 14, paddingBottom: 14, borderTopWidth: 1, borderTopColor: c.border }}>
      <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "700", marginTop: 10, marginBottom: 8, letterSpacing: 0.5 }}>
        AGENT
      </Text>
      {auditingId === d.id || rows === undefined ? (
        <View style={{ flexDirection: "row", alignItems: "center", paddingVertical: 12 }}>
          <ActivityIndicator color={c.accent} />
          <Text style={{ color: c.textMuted, marginLeft: 10, fontSize: 12 }}>Checking agent state…</Text>
        </View>
      ) : (
        RUNNERS.map(({ taskId, auditId, label }) => {
          const actionKey = `${d.id}:${auditId}`;
          const row = auditFor(d.id, auditId);
          const failed = auditFailed(d.id);
          const installed = !!row?.installed;
          const authed = !!row?.authConfigured;
          const ready = installed && authed;
          const selected = pickedRunner === taskId;
          const setupBusy = runnerSetupBusyKey === actionKey;
          const actionError = runnerActionErrorByKey[actionKey];
          const subtitle = failed
            ? "Couldn't audit this device — tap to retry"
            : !installed
              ? "Not installed on this device"
              : authed
                ? `Ready${row?.version ? ` · ${row.version}` : ""}`
                : auditId === "opencode"
                  ? "Needs provider setup on this device"
                  : "Needs auth — tap to set up";
          return (
            <View
              key={taskId}
              style={{
                marginBottom: 8,
                padding: 12,
                borderRadius: 8,
                borderWidth: 1,
                borderColor: selected ? c.accent : c.border,
                backgroundColor: c.bg,
                opacity: setupBusy ? 0.75 : 1,
              }}
            >
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                <View style={{ flex: 1, paddingRight: 12 }}>
                  <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>{label}</Text>
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }}>{subtitle}</Text>
                  {actionError ? (
                    <Text style={{ color: "#dc2626", fontSize: 11, marginTop: 6 }}>{actionError}</Text>
                  ) : null}
                </View>
                {ready ? (
                  <Pressable
                    onPress={() => handlePickRunner(taskId, auditId)}
                    style={({ pressed }) => ({
                      paddingHorizontal: 12,
                      paddingVertical: 8,
                      borderRadius: 8,
                      backgroundColor: selected ? c.accent + "22" : c.bgCard,
                      borderWidth: 1,
                      borderColor: selected ? c.accent : c.border,
                      opacity: pressed ? 0.85 : 1,
                    })}
                  >
                    <Text style={{ color: c.accent, fontWeight: "700", fontSize: 12 }}>
                      {selected ? "SELECTED" : "USE"}
                    </Text>
                  </Pressable>
                ) : failed ? (
                  <Pressable
                    onPress={() => void runAudit(d)}
                    style={({ pressed }) => ({
                      paddingHorizontal: 12,
                      paddingVertical: 8,
                      borderRadius: 8,
                      backgroundColor: c.bgCard,
                      borderWidth: 1,
                      borderColor: c.border,
                      opacity: pressed ? 0.85 : 1,
                    })}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 11, fontWeight: "700" }}>RETRY</Text>
                  </Pressable>
                ) : !installed ? (
                  <Pressable
                    onPress={() => void handleInstallRunner(d, auditId)}
                    disabled={setupBusy}
                    style={({ pressed }) => ({
                      paddingHorizontal: 12,
                      paddingVertical: 8,
                      borderRadius: 8,
                      backgroundColor: c.accent + "18",
                      borderWidth: 1,
                      borderColor: c.accent,
                      opacity: setupBusy ? 0.6 : pressed ? 0.85 : 1,
                    })}
                  >
                    {setupBusy ? (
                      <ActivityIndicator size="small" color={c.accent} />
                    ) : (
                      <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>INSTALL</Text>
                    )}
                  </Pressable>
                ) : auditId === "opencode" ? (
                  <Pressable
                    onPress={() => handlePickRunner(taskId, auditId)}
                    style={({ pressed }) => ({
                      paddingHorizontal: 12,
                      paddingVertical: 8,
                      borderRadius: 8,
                      backgroundColor: c.bgCard,
                      borderWidth: 1,
                      borderColor: c.border,
                      opacity: pressed ? 0.85 : 1,
                    })}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 11, fontWeight: "700" }}>DETAILS</Text>
                  </Pressable>
                ) : (
                  <Pressable
                    onPress={() => handlePickRunner(taskId, auditId)}
                    style={({ pressed }) => ({
                      paddingHorizontal: 12,
                      paddingVertical: 8,
                      borderRadius: 8,
                      backgroundColor: c.accent + "18",
                      borderWidth: 1,
                      borderColor: c.accent,
                      opacity: pressed ? 0.85 : 1,
                    })}
                  >
                    <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>AUTH</Text>
                  </Pressable>
                )}
              </View>
            </View>
          );
        })
      )}
      {/* Per-runner model picker — only when the picked device matches
          this card and runner is Claude Code or Codex. OpenCode picks
          model + provider from the host's opencode.json, handled in
          the OpenCode block below. */}
      {pickedDevice?.id === d.id && pickedRunner && (pickedRunner === "claude-code" || pickedRunner === "codex") ? (
        <View style={{ marginTop: 6 }}>
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
                  marginBottom: 6,
                  padding: 10,
                  borderRadius: 8,
                  borderWidth: 1,
                  borderColor: sel ? c.accent : c.border,
                  backgroundColor: c.bg,
                  opacity: pressed ? 0.85 : 1,
                })}
              >
                <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                  <View style={{ flex: 1, paddingRight: 10 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>
                      {m.label}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }} numberOfLines={1}>
                      {m.id}
                    </Text>
                  </View>
                  {sel ? <Text style={{ color: c.accent, fontWeight: "700", fontSize: 12 }}>SELECTED</Text> : null}
                </View>
              </Pressable>
            );
          })}
        </View>
      ) : null}
      {/* OpenCode mode picker — shown when this card's device is
          picked and the runner is OpenCode. Modes come from the
          remote box's opencode.json. */}
      {pickedDevice?.id === d.id && pickedRunner === "opencode" ? (
        (() => {
          const cfg = opencodeByDevice[d.id];
          const loading = opencodeLoadingId === d.id || cfg === undefined;
          const agents = (cfg?.agents || []).filter((a) => !!a?.name);
          const fallback = ["build", "plan"];
          const showFallback = !loading && agents.length === 0;
          return (
            <View style={{ marginTop: 6 }}>
              <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "700", marginBottom: 8, letterSpacing: 0.5 }}>
                OPENCODE AGENT
              </Text>
              {loading ? (
                <View style={{ flexDirection: "row", alignItems: "center", paddingVertical: 8 }}>
                  <ActivityIndicator color={c.accent} />
                  <Text style={{ color: c.textMuted, marginLeft: 10, fontSize: 12 }}>
                    Reading opencode.json…
                  </Text>
                </View>
              ) : showFallback ? (
                <>
                  <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 8 }}>
                    Couldn't read opencode.json — using defaults.
                  </Text>
                  {fallback.map((mode) => {
                    const sel = pickedOpencodeMode === mode;
                    return (
                      <Pressable
                        key={mode}
                        onPress={() => setPickedOpencodeMode(mode)}
                        style={({ pressed }) => ({
                          marginBottom: 6,
                          padding: 10,
                          borderRadius: 8,
                          borderWidth: 1,
                          borderColor: sel ? c.accent : c.border,
                          backgroundColor: c.bg,
                          opacity: pressed ? 0.85 : 1,
                        })}
                      >
                        <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>{mode}</Text>
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
                        marginBottom: 6,
                        padding: 10,
                        borderRadius: 8,
                        borderWidth: 1,
                        borderColor: sel ? c.accent : c.border,
                        backgroundColor: c.bg,
                        opacity: pressed ? 0.85 : 1,
                      })}
                    >
                      <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                        <View style={{ flex: 1, paddingRight: 10 }}>
                          <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }} numberOfLines={1}>
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
            </View>
          );
        })()
      ) : null}
    </View>
  );

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
            onPress={() => setPane("unified")}
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
            <Pressable
              onPress={onCancel}
              hitSlop={10}
              style={({ pressed }) => ({
                paddingHorizontal: 12,
                paddingVertical: 7,
                borderRadius: 8,
                borderWidth: 1,
                borderColor: c.border,
                backgroundColor: pressed ? c.bgCard : "transparent",
                opacity: pressed ? 0.85 : 1,
              })}
            >
              <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>Cancel</Text>
            </Pressable>
            <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>
              {pane === "switching" ? "Switching" : "New task"}
            </Text>
            <View style={{ width: 70 }} />
          </View>
          {pane === "unified" && renderUnifiedPane()}
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
