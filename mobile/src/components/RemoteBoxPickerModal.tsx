import React from "react";
import {
  ActivityIndicator,
  Alert,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  Text,
  View,
} from "react-native";

import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../context/ThemeContext";
import { useAuth } from "../context/AuthContext";
import { AppScreenHeader } from "./AppScreenHeader";
import EmptyState from "./EmptyState";
import RunnerAuthModal from "./RunnerAuthModal";
import { useDevice, type Device } from "../context/DeviceContext";
import { useTabletContentStyle } from "../hooks/useTabletContentStyle";
import { connectionManager } from "../lib/connectionManager";
import { eligibleRemoteBoxDevices, versionPatchDistance } from "../lib/devicePicker";
import {
  lastSeenLabel,
  probeMobileDeviceStatus,
  type CodingRunnerProbe,
} from "../lib/deviceStatus";
import { probeDeviceWithRepair } from "../lib/probeWithRepair";
import {
  useParkedMachines,
  deriveWakeView,
  specSummary,
  timeAgo,
  WAKE_STAGES,
} from "../lib/parkedMachines";
import type { ManagedCloudMachineSummary } from "../lib/subscription";

interface Props {
  visible: boolean;
  onClose: () => void;
  onSelected?: (device: Device) => void;
}

type CodingStatus = {
  ready: boolean;
  runners: CodingRunnerProbe[];
  path?: "relay" | "direct";
  error?: string;
};

const CODING_RUNNER_ORDER = ["codex", "claude", "claude-code", "opencode"];

function runnerDisplayName(id: string): string {
  const normalized = id.toLowerCase();
  if (normalized === "codex") return "Codex";
  if (normalized === "claude" || normalized === "claude-code") return "Claude";
  if (normalized === "opencode") return "OpenCode";
  return id;
}

function sortedCodingRunners(runners: CodingRunnerProbe[]): CodingRunnerProbe[] {
  return [...runners]
    .filter((r) => CODING_RUNNER_ORDER.includes(r.id))
    .sort((a, b) => {
      const ai = CODING_RUNNER_ORDER.indexOf(a.id);
      const bi = CODING_RUNNER_ORDER.indexOf(b.id);
      return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi);
    });
}

async function waitForClientConnected(deviceId: string, timeoutMs = 2500): Promise<boolean> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (connectionManager.clientFor(deviceId).isConnected) return true;
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  return connectionManager.clientFor(deviceId).isConnected;
}

function managedMachineName(m: ManagedCloudMachineSummary): string {
  const raw = (m.hostname || m.serverType || m.machineType || "Managed box").trim();
  return raw || "Managed box";
}

function isYaverAuthWakeBlocker(message?: string | null): boolean {
  const text = String(message || "").toLowerCase();
  return (
    text.includes("yaver agent is signed out") ||
    text.includes("yaver agent session expired") ||
    text.includes("waiting to be claimed") ||
    text.includes("sign this machine in") ||
    text.includes("could never register") ||
    text.includes("re-authorize") ||
    // The string abandonWake actually writes (cloudMachines.ts:2376) is
    // "…was not authorized in time. Parked again to stop the meter — sign it
    // in, then wake." None of the phrases above appear in it, so the terminal
    // card told the user to sign the box in while offering no button to do it.
    // Matching prose is brittle by nature; lastWakeOutcome === "needs-auth" is
    // the structured signal and is checked alongside this at the call site.
    text.includes("waiting for yaver sign-in") ||
    text.includes("sign it in, then wake")
  );
}

// A single parked/managed machine rendered inline in the picker list, styled to
// match the device rows. Its primary action is Wake (a parked box is not a
// selectable remote box until it's awake); once it wakes it self-registers as a
// device and moves into the connectable list above. Shows the same staged
// waking-up ladder the Infra tab uses so the transition (parked → resuming →
// active) reads as forward motion, not a bare spinner.
function SleepingMachineRow({
  c,
  machine,
  waking,
  error,
  signingIn,
  onWake,
  onSignIn,
  deviceReachable = false,
}: {
  c: any;
  machine: ManagedCloudMachineSummary;
  waking: boolean;
  error?: string;
  signingIn?: boolean;
  onWake: () => void;
  onSignIn?: () => void;
  // See the same prop on infra.tsx: deriveWakeView refuses a confident 100% for
  // a box nobody has established they can reach. Defaults false, which holds the
  // ladder one rung short rather than claiming done.
  deviceReachable?: boolean;
}) {
  const view = deriveWakeView(machine, waking, deviceReachable);
  const parked = view.tone === "parked";
  const failed = view.tone === "error";
  // Prefer the STRUCTURED signal over prose. lastWakeOutcome is written
  // precisely for this ("needs-auth" at cloudMachines.ts:2348, preserved through
  // abandonWake), so a reworded reason string can no longer silently remove the
  // only button that fixes the box.
  const authBlocked =
    (failed || machine.status === "paused") &&
    (machine.lastWakeOutcome === "needs-auth" ||
      isYaverAuthWakeBlocker(view.error || error || machine.errorMessage));
  const accent = failed ? c.warn : c.accent;
  const slept = timeAgo(machine.lastParkedAt);

  // Live elapsed clock + gentle in-stage creep so a multi-minute wake reads as
  // motion, not a frozen bar with no sense of time ("user just waits"). System A
  // (WakeProgress) already does this; the picker's sleeping-row didn't. Elapsed
  // starts when we first observe inFlight; creep resets each time the stage
  // advances and asymptotes toward — but never reaches — the next stage floor.
  const [wakeElapsedMs, setWakeElapsedMs] = React.useState(0);
  const wakeStartRef = React.useRef<number | null>(null);
  const stageStartRef = React.useRef<{ stage: number; at: number }>({ stage: -1, at: 0 });
  // Anchor the clock to the SERVER's wakeStartedAt, not to first render. The
  // local-ref version restarted at 0:00 (and reset "~3:00 left" with it) every
  // time this card remounted — closing the picker and reopening it made a
  // six-minute wake claim it had just begun. Fall back to lastWokeAt, then to
  // local time, so a row from an older backend still shows motion.
  const serverWakeStartedAt =
    typeof machine.wakeStartedAt === "number" && machine.wakeStartedAt > 0
      ? machine.wakeStartedAt
      : typeof machine.lastWokeAt === "number" && machine.lastWokeAt > 0
        ? machine.lastWokeAt
        : null;
  React.useEffect(() => {
    if (!view.inFlight) {
      wakeStartRef.current = null;
      setWakeElapsedMs(0);
      return;
    }
    // Re-anchor whenever the server's start moves (a NEW wake run), otherwise
    // keep the existing anchor so the clock is continuous across remounts.
    if (serverWakeStartedAt != null) wakeStartRef.current = serverWakeStartedAt;
    else if (wakeStartRef.current == null) wakeStartRef.current = Date.now();
    const tick = () => setWakeElapsedMs(Math.max(0, Date.now() - (wakeStartRef.current ?? Date.now())));
    tick();
    const iv = setInterval(tick, 1000);
    return () => clearInterval(iv);
  }, [view.inFlight, serverWakeStartedAt]);
  if (stageStartRef.current.stage !== view.stageIndex) {
    stageStartRef.current = { stage: view.stageIndex, at: Date.now() };
  }
  const NEXT_STAGE_FLOOR = [35, 60, 88, 100];
  const nextFloor = NEXT_STAGE_FLOOR[view.stageIndex] ?? view.percent;
  // Same remount trap as the elapsed clock: a locally-stamped stage start makes
  // the creep restart from zero on every mount, so the bar visibly snaps
  // BACKWARDS when you reopen the picker. provisionPhaseAt is the server's own
  // "when did this phase begin", which survives remounts.
  const stageAnchorAt =
    typeof machine.provisionPhaseAt === "number" && machine.provisionPhaseAt > 0
      ? machine.provisionPhaseAt
      : stageStartRef.current.at;
  const stageElapsedMs = view.inFlight ? Math.max(0, Date.now() - stageAnchorAt) : 0;
  const creepHeadroom = Math.max(0, nextFloor - 1 - view.percent);
  const wakeCreep = creepHeadroom * (1 - Math.exp(-stageElapsedMs / 60000));
  const shownPercent = Math.min(nextFloor - 1, view.percent + wakeCreep);
  const wakeClock = (ms: number) => {
    const s = Math.max(0, Math.floor(ms / 1000));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  };
  // ETA. Prefer THIS box's own measured last wake over a constant — a constant
  // is wrong for every box but one. Only fall back to the class-based guess
  // when the box has never completed a wake we timed.
  const measuredWakeMs =
    typeof machine.lastWakeDurationMs === "number" && machine.lastWakeDurationMs > 15_000
      ? machine.lastWakeDurationMs
      : null;
  const wakeTotalEstMs =
    measuredWakeMs ??
    (machine.hasVolume ? 120_000 : machine.bootImageSource === "vanilla" ? 300_000 : 180_000);
  const wakeRemainMs = Math.max(0, wakeTotalEstMs - wakeElapsedMs);
  // Once the estimate is blown, STOP PROMISING. The old code fell through to a
  // permanent "almost there…", which is the one string guaranteed to be a lie:
  // it kept implying imminent completion for a box that was blocked on the user
  // (awaiting-yaver-auth) and would never finish on its own. Past the estimate
  // we state the overrun and say nothing about when it will end.
  const wakeOverdue = wakeRemainMs <= 0;
  const wakeEta = wakeOverdue
    ? `over ${wakeClock(wakeTotalEstMs)} — taking longer than usual`
    : `~${wakeClock(wakeRemainMs)} left`;
  // The box is blocked on a human, not on time. This is NOT a slow wake and must
  // never be dressed as one.
  const blockedOnSignIn = machine.provisionPhase === "awaiting-yaver-auth";
  // Say what the box/provider actually reported, rather than a generic rung.
  // "Hetzner: initializing" is a fact; "almost there" was a guess.
  const providerNote =
    typeof machine.providerStatus === "string" && machine.providerStatus.trim()
      ? machine.providerStatus.trim()
      : null;

  return (
    <View
      style={{
        marginBottom: 10,
        padding: 14,
        borderRadius: 10,
        borderWidth: 1,
        borderColor: view.inFlight ? c.accent : c.border,
        backgroundColor: c.bgCard,
      }}
    >
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <View style={{ flex: 1, paddingRight: 12 }}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }} numberOfLines={1}>
            {managedMachineName(machine)}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }} numberOfLines={1}>
            {specSummary(machine)}
          </Text>
          {parked && slept ? (
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>Slept {slept}</Text>
          ) : null}
        </View>
        {parked ? (
          <View style={{ backgroundColor: c.warn + "22", paddingHorizontal: 8, paddingVertical: 3, borderRadius: 999 }}>
            <Text style={{ color: c.warn, fontSize: 10, fontWeight: "800", letterSpacing: 0.5 }}>ASLEEP</Text>
          </View>
        ) : view.inFlight ? (
          <View style={{ backgroundColor: c.accent + "22", paddingHorizontal: 8, paddingVertical: 3, borderRadius: 999 }}>
            <Text style={{ color: c.accent, fontSize: 10, fontWeight: "800", letterSpacing: 0.5 }}>WAKING</Text>
          </View>
        ) : null}
      </View>

      {/* Blocked on a human, so it renders OUTSIDE the in-flight ladder below:
          deriveWakeView returns inFlight:false for awaiting-yaver-auth, which
          made an earlier version of this block dead code. No bar here on
          purpose — there is no progress to show, only a fact and an action. */}
      {blockedOnSignIn ? (
        <View style={{ gap: 4, marginTop: 12 }}>
          <Text style={{ color: c.warn, fontSize: 11, fontWeight: "700" }}>
            Waiting for you to sign this box in · awake {wakeClock(wakeElapsedMs)}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 10 }}>
            The box is running and answering, but its Yaver agent session expired — it cannot
            finish on its own. It stays billed until it is signed in or parks itself.
          </Text>
          {providerNote ? (
            <Text style={{ color: c.textMuted, fontSize: 10 }}>Provider: {providerNote}</Text>
          ) : null}
        </View>
      ) : null}

      {/* Staged waking-up ladder — driven by the box's real status + phase. */}
      {view.inFlight && !blockedOnSignIn ? (
        <View style={{ gap: 6, marginTop: 12 }}>
          <View style={{ height: 6, borderRadius: 3, backgroundColor: c.border, overflow: "hidden" }}>
            <View
              style={{
                height: 6,
                borderRadius: 3,
                backgroundColor: c.accent,
                width: `${Math.max(5, Math.min(100, shownPercent))}%`,
              }}
            />
          </View>
          <Text style={{ color: blockedOnSignIn || wakeOverdue ? c.warn : c.textMuted, fontSize: 11 }}>
            {blockedOnSignIn
              ? `Waiting for you to sign this box in · ${wakeClock(wakeElapsedMs)}`
              : `${WAKE_STAGES[view.stageIndex]?.label ?? "Waking up"}… · ${wakeClock(wakeElapsedMs)} · ${wakeEta}`}
          </Text>
          {/* The provider's own word for the server, so a long wake is explained
              rather than just endured. */}
          {providerNote && !blockedOnSignIn ? (
            <Text style={{ color: c.textMuted, fontSize: 10 }}>Provider: {providerNote}</Text>
          ) : null}
          {blockedOnSignIn ? (
            <Text style={{ color: c.textMuted, fontSize: 10 }}>
              The box is awake, but its Yaver agent session expired — it cannot finish on its own.
            </Text>
          ) : machine.hasVolume ? (
            <Text style={{ color: c.success, fontSize: 10 }}>⚡ Fast wake — data on a persistent volume (~1-2 min).</Text>
          ) : machine.bootImageSource === "vanilla" ? (
            <Text style={{ color: c.textMuted, fontSize: 10 }}>First boot — building the image (~3-5 min).</Text>
          ) : null}
        </View>
      ) : null}

      {failed && view.error ? (
        <Text style={{ color: c.warn, fontSize: 11, marginTop: 8 }}>{view.error}</Text>
      ) : null}
      {error ? <Text style={{ color: c.warn, fontSize: 11, marginTop: 6 }}>{error}</Text> : null}

      {/* Offer sign-in while the box is AWAKE and blocked on it (blockedOnSignIn),
          not only after it has already been re-parked. Gating this on the error
          tone alone inverted the whole flow: during the one window where
          recovery can actually reach a live agent, the button was hidden; by the
          time it appeared, abandonWake had deleted the box, so `direct` recovery
          had nothing to talk to and fell through to a device-code OAuth pointing
          at a machine that no longer existed. */}
      {(authBlocked || blockedOnSignIn) && onSignIn ? (
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 10, marginTop: 12 }}>
          <Pressable
            onPress={onSignIn}
            disabled={signingIn}
            style={({ pressed }) => ({
              paddingHorizontal: 16,
              paddingVertical: 9,
              borderRadius: 8,
              backgroundColor: c.accent,
              flexDirection: "row",
              alignItems: "center",
              opacity: signingIn ? 0.6 : pressed ? 0.85 : 1,
            })}
          >
            {signingIn ? (
              <ActivityIndicator size="small" color="#000" />
            ) : (
              <Text style={{ color: "#000", fontSize: 13, fontWeight: "700" }}>
                Sign this machine in
              </Text>
            )}
          </Pressable>
          <Pressable
            onPress={onWake}
            disabled={waking || signingIn}
            style={({ pressed }) => ({
              paddingHorizontal: 14,
              paddingVertical: 9,
              borderRadius: 8,
              borderWidth: 1,
              borderColor: c.warn + "66",
              opacity: waking || signingIn ? 0.55 : pressed ? 0.75 : 1,
            })}
          >
            <Text style={{ color: c.warn, fontSize: 13, fontWeight: "700" }}>
              Retry wake
            </Text>
          </Pressable>
        </View>
      ) : parked || failed ? (
        <Pressable
          onPress={onWake}
          disabled={waking}
          style={({ pressed }) => ({
            alignSelf: "flex-start",
            marginTop: 12,
            paddingHorizontal: 16,
            paddingVertical: 9,
            borderRadius: 8,
            backgroundColor: accent,
            flexDirection: "row",
            alignItems: "center",
            opacity: waking ? 0.6 : pressed ? 0.85 : 1,
          })}
        >
          {waking ? (
            <ActivityIndicator size="small" color="#000" />
          ) : (
            <Text style={{ color: "#000", fontSize: 13, fontWeight: "700" }}>
              {failed ? "Try wake again" : "⏻ Wake"}
            </Text>
          )}
        </Pressable>
      ) : null}
    </View>
  );
}

export default function RemoteBoxPickerModal({ visible, onClose, onSelected }: Props) {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const tabletContent = useTabletContentStyle("regular");
  const deviceCtx = useDevice();
  const {
    devices,
    activeDevice,
    selectDevice,
    connectedDeviceIds,
    primaryDeviceId,
    secondaryDeviceId,
    latestCliVersion,
    lastError,
  } = deviceCtx;
  const { token } = useAuth();

  const connectedSet = React.useMemo(() => new Set(connectedDeviceIds), [connectedDeviceIds]);
  const eligibleDevices = React.useMemo(
    () =>
      eligibleRemoteBoxDevices(devices, connectedSet, activeDevice?.id).sort((a, b) => {
        const rank = (device: Device) => {
          if (device.id === primaryDeviceId) return 0;
          if (device.id === secondaryDeviceId) return 1;
          if (device.id === activeDevice?.id) return 2;
          return 3;
        };
        const delta = rank(a) - rank(b);
        if (delta !== 0) return delta;
        return a.name.localeCompare(b.name);
      }),
    [devices, connectedSet, activeDevice?.id, primaryDeviceId, secondaryDeviceId],
  );

  // Parked/managed machines — not devices, so they never surface in the
  // eligibleDevices list. We pull them from /subscription (via the same hook
  // the Infra tab uses) so a sleeping box can be seen + woken from where the
  // remote box is chosen, not only in Infra. Once a machine wakes to "active"
  // it self-registers as a normal device and drops out of this list.
  const {
    machines: managedMachines,
    wakingId: managedWakingId,
    errors: managedWakeErrors,
    lastFailure: managedLastFailure,
    justWoke: managedJustWoke,
    wake: wakeManagedMachine,
    refresh: refreshManagedMachines,
  } = useParkedMachines(token);
  const [recoveringMachineId, setRecoveringMachineId] = React.useState<string | null>(null);
  const [recoveringDeviceId, setRecoveringDeviceId] = React.useState<string | null>(null);
  const sleepingMachines = React.useMemo(
    () =>
      managedMachines.filter(
        // Reachability is unknown for a bare list filter, and false is the
        // honest answer: a box we have not probed must not be filtered out as
        // already-online.
        (m) => deriveWakeView(m, managedWakingId === m.id, false).tone !== "online",
      ),
    [managedMachines, managedWakingId],
  );

  const [pickedDeviceId, setPickedDeviceId] = React.useState<string | null>(null);
  const [switching, setSwitching] = React.useState(false);
  const [switchError, setSwitchError] = React.useState<string | null>(null);
  // Brief "✓ connected" confirmation shown before the modal auto-closes, so a
  // successful switch isn't an instant silent dismiss (which read as "did it
  // even work?"). Holds the connected device's name.
  const [switchSuccess, setSwitchSuccess] = React.useState<string | null>(null);
  // Live "connecting" feedback — a real reachability probe result shown while
  // we connect, instead of a bare spinner. "Pinging…" → "Reachable via direct
  // (40ms)" / "No route — box is online but not reachable".
  const [probeStage, setProbeStage] = React.useState<string | null>(null);
  const [pingByDevice, setPingByDevice] = React.useState<
    Record<string, { rttMs: number; ok: boolean; at: number }>
  >({});
  const [codingStatusByDevice, setCodingStatusByDevice] = React.useState<
    Record<string, CodingStatus | null>
  >({});
  // Remote runner sign-in, launched straight from the row that reports the
  // problem. `target` routes the OAuth via /peer/<id>, so this works for a box
  // the phone is not currently attached to — which is the normal case when you
  // are standing in the picker deciding where to send work.
  const [authTarget, setAuthTarget] = React.useState<
    { deviceId: string; deviceName: string; runner: string } | null
  >(null);
  React.useEffect(() => {
    if (!visible) {
      setSwitching(false);
      setSwitchError(null);
      setSwitchSuccess(null);
      setProbeStage(null);
      return;
    }
    if (activeDevice?.id && eligibleDevices.some((d) => d.id === activeDevice.id)) {
      setPickedDeviceId(activeDevice.id);
      return;
    }
    setPickedDeviceId(eligibleDevices[0]?.id ?? null);
  }, [visible, activeDevice?.id, eligibleDevices]);

  const runPing = React.useCallback(async (device: Device) => {
    const direct = connectionManager.clientFor(device.id);
    if (!direct.isConnected) return;
    try {
      const result = await direct.ping();
      setPingByDevice((prev) => ({
        ...prev,
        [device.id]: { rttMs: result.rttMs, ok: result.ok, at: Date.now() },
      }));
    } catch {
      setPingByDevice((prev) => ({
        ...prev,
        [device.id]: { rttMs: -1, ok: false, at: Date.now() },
      }));
    }
  }, []);

  // Active reachability probe for a box that Convex reports as DOWN. Unlike
  // runPing (which only pings already-pooled clients), this walks relay →
  // direct so the user can confirm a machine is actually up before
  // committing to a switch. A reachable result flips the row to connectable.
  const [offlineProbe, setOfflineProbe] = React.useState<
    Record<string, { ok: boolean; line: string; busy?: boolean }>
  >({});
  const probeOffline = React.useCallback(
    async (device: Device) => {
      setOfflineProbe((p) => ({ ...p, [device.id]: { ok: false, line: "", busy: true } }));
      try {
        const r = await probeMobileDeviceStatus(
          { id: device.id, host: (device as any).host, port: (device as any).port, lanIps: (device as any).lanIps },
          token,
          8000,
        );
        setOfflineProbe((p) => ({
          ...p,
          [device.id]: r.reachable
            ? { ok: true, line: `reachable · ${r.path === "relay" ? "relay" : "direct"}` }
            : { ok: false, line: "still unreachable" },
        }));
      } catch {
        setOfflineProbe((p) => ({ ...p, [device.id]: { ok: false, line: "ping failed" } }));
      }
    },
    [token],
  );

  React.useEffect(() => {
    if (!visible) return;
    for (const device of eligibleDevices) {
      if (connectedSet.has(device.id)) {
        void runPing(device);
      }
    }
  }, [visible, eligibleDevices, connectedSet, runPing]);

  React.useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    for (const device of eligibleDevices) {
      if (codingStatusByDevice[device.id] !== undefined) continue;
      const load = async () => {
        try {
          const probe = await probeMobileDeviceStatus(
            { id: device.id, host: (device as any).host, port: (device as any).port, lanIps: (device as any).lanIps },
            token,
            8000,
          );
          if (cancelled) return;
          setCodingStatusByDevice((prev) => ({
            ...prev,
            [device.id]: probe.reachable
              ? {
                  ready: probe.codingReady,
                  runners: probe.codingRunners,
                  path: probe.path,
                }
              : {
                  ready: false,
                  runners: [],
                  error: probe.error || "Coding agent status unavailable",
                },
          }));
        } catch (err: any) {
          if (cancelled) return;
          setCodingStatusByDevice((prev) => ({
            ...prev,
            [device.id]: {
              ready: false,
              runners: [],
              error: err?.message || "Coding agent status unavailable",
            },
          }));
        }
      };
      void load();
    }
    return () => {
      cancelled = true;
    };
  }, [visible, eligibleDevices, codingStatusByDevice, token]);

  const pickedDevice = eligibleDevices.find((d) => d.id === pickedDeviceId) ?? null;

  const handleContinue = React.useCallback(async (targetOverride?: Device | null) => {
    const target = targetOverride ?? pickedDevice;
    if (!target) return;
    setSwitching(true);
    setSwitchError(null);
    setProbeStage(`Pinging ${target.name}…`);
    try {
      // Probe-first: do a real reachability check (the same relay+direct race
      // `yaver ping` uses) and SHOW the result, instead of spinning blindly for
      // up to 20s. If nothing answers, fail fast with an honest reason rather
      // than grinding through the full connect timeout.
      // Probe + relay-credential self-heal. This ladder now lives in
      // lib/probeWithRepair so the AUTOMATIC connect path (DeviceContext) runs
      // the identical sequence — it previously did a bare probe with no repair
      // rung and a tighter timeout, which made the default path strictly weaker
      // than this manual one. Same function, same stage strings, both surfaces.
      const { probe } = await probeDeviceWithRepair(
        {
          id: target.id,
          name: target.name,
          host: (target as any).host,
          port: (target as any).port,
          lanIps: (target as any).lanIps,
        },
        {
          token,
          timeoutMs: 4000,
          onStage: setProbeStage,
          repairRelay: deviceCtx.repairRelay,
        },
      );

      if (probe?.reachable) {
        if (probe.authExpired) {
          throw new Error(
            `${target.name} is reachable, but its Yaver session expired. Open Devices and run Re-auth Yaver, or use the sign-in action when this is a waking managed box.`,
          );
        }
        if (probe.bootstrap) {
          throw new Error(
            `${target.name} is reachable, but its Yaver agent is waiting to be claimed. Open Devices and reclaim/sign in this machine before using it.`,
          );
        }
        setProbeStage(`Reachable via ${probe.path === "relay" ? "relay" : "direct"} — connecting…`);
      } else {
        // Online-but-unreachable: name it and stop, don't fake a 20s attempt.
        throw new Error(
          `Couldn't reach ${target.name} — it's online but no transport answered (${probe?.error || "no route"}). ` +
            `If it's a remote box, it may be heartbeating without a live relay tunnel.`,
        );
      }
      // Always route through DeviceContext.selectDevice — even when
      // the picked box already has a pooled-connected client. The
      // earlier optimization called connectionManager.setFocused()
      // directly in that case, which updates the focused pointer
      // in the pool but does NOT update activeDevice /
      // connectionStatus in React state. Result: the legacy
      // quicClient Proxy correctly forwarded to the new device,
      // but the Reload tab kept reading stale activeDevice +
      // showed "Not connected" after a successful switch.
      // selectDevice short-circuits internally when the client is
      // already connected (see DeviceContext.selectDevice ~line
      // 1032 — sets connectionStatus straight back to "connected"
      // after the optimistic "connecting" tick), so calling it
      // unconditionally is safe and idempotent.
      if (activeDevice?.id !== target.id || !connectionManager.clientFor(target.id).isConnected) {
        await selectDevice(target);
      }
      if (!connectionManager.clientFor(target.id).isConnected) {
        await waitForClientConnected(target.id);
      }
      if (!connectionManager.clientFor(target.id).isConnected) {
        const detail = (lastError || "").trim();
        throw new Error(
          detail
            ? `Couldn't reach ${target.name}: ${detail}`
            : `Couldn't reach ${target.name}.`,
        );
      }
      // Success — show a brief "✓ connected" confirmation instead of a silent
      // dismiss, then hand off + close. Keep `switching` true so the success
      // view stays up during the short delay.
      setSwitchSuccess(target.name);
      setTimeout(() => {
        onSelected?.(target);
        onClose();
      }, 1100);
    } catch (err: any) {
      // Keep `switching` true (do NOT clear it) so the error view with the
      // failure detail + Try again renders instead of dropping back to the
      // list, which made failures look identical to successes.
      setSwitchError(err?.message || "Failed to switch remote box.");
    }
  }, [pickedDevice, selectDevice, activeDevice?.id, lastError, onSelected, onClose, deviceCtx]);

  const pickedDeviceIsCurrent = !!pickedDevice && activeDevice?.id === pickedDevice.id;
  const pickedDeviceIsConnected = !!pickedDevice && connectedSet.has(pickedDevice.id);

  // Sign in a real (self-hosted) box that came back in bootstrap mode. The
  // managed path below builds a synthetic Device because a parked cloud box
  // has no device row yet; here the row is real and already carries the host
  // + public key, so it goes straight to recoverDeviceAuth. Confirm first —
  // recoverDeviceAuth calls selectDevice internally and tears down the
  // currently active connection.
  const signInDevice = React.useCallback(
    async (device: Device) => {
      const proceed = await new Promise<boolean>((resolve) => {
        Alert.alert(
          "Sign this machine in?",
          `${device.name} is running but its Yaver session expired. Yaver will push this phone's session to it. Your current connection will be dropped while it reconnects.`,
          [
            { text: "Cancel", style: "cancel", onPress: () => resolve(false) },
            { text: "Sign in", onPress: () => resolve(true) },
          ],
        );
      });
      if (!proceed) return;
      setRecoveringDeviceId(device.id);
      try {
        const result = await deviceCtx.recoverDeviceAuth(device);
        if (!result?.ok && !result?.alreadyHealthy) {
          Alert.alert(
            "Sign-in did not finish",
            result?.error || "Yaver could not sign this machine in. Check that it is reachable and try again.",
          );
        }
      } catch (err: any) {
        Alert.alert("Sign-in failed", err?.message || "Yaver could not sign this machine in.");
      } finally {
        setRecoveringDeviceId(null);
      }
    },
    [deviceCtx],
  );

  const recoverManagedMachineAuth = React.useCallback(
    async (machine: ManagedCloudMachineSummary) => {
      const host = String(machine.hostname || machine.serverIp || "").trim();
      // A box whose session expired never registers, so its cloud row has NO
      // deviceId — and requiring one deadlocked the only escape: it can't
      // register until it's signed in, and we refused to sign it in until it
      // registered. "Wait a few seconds for the wake state to refresh" could
      // never come true. The device id was only ever a label; recovery reaches
      // the agent by host, and the machine id is a stable stand-in.
      const deviceId = String(machine.deviceId || "").trim() || `managed:${machine.id}`;
      if (!host) {
        Alert.alert(
          "Can't sign this box in yet",
          "This box hasn't reported an address yet, so there's nothing to sign in to. Give the wake a few seconds and try again.",
        );
        return;
      }
      const synthetic: Device = {
        id: deviceId,
        name: managedMachineName(machine),
        host,
        port: 18080,
        online: true,
        lastSeen: Date.now(),
        os: "linux",
        runners: [],
        needsAuth: true,
        hosting: "yaver-hosted",
        managed: true,
        machineId: machine.id,
        machineStatus: machine.status,
        lanIps: machine.serverIp ? [machine.serverIp] : undefined,
      };
      setRecoveringMachineId(machine.id);
      try {
        const result = await deviceCtx.recoverDeviceAuth(synthetic);
        if (result?.ok || result?.alreadyHealthy) {
          await refreshManagedMachines();
          if (machine.status === "paused" || machine.status === "suspended") {
            await wakeManagedMachine(machine.id);
          }
        } else {
          Alert.alert("Sign-in did not finish", result?.error || "Yaver could not start remote sign-in for this box.");
        }
      } finally {
        setRecoveringMachineId(null);
      }
    },
    [deviceCtx, refreshManagedMachines, wakeManagedMachine],
  );

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="fullScreen" onRequestClose={onClose}>
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader
          title={switchSuccess ? "Connected" : switching ? "Switching" : "Remote Box"}
          backLabel="Cancel"
          onBack={onClose}
          style={{ paddingTop: Math.max(insets.top, 12) + 6 }}
        />

        {switching ? (
          <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 24 }}>
            {switchSuccess ? (
              <>
                <View
                  style={{
                    width: 64,
                    height: 64,
                    borderRadius: 32,
                    backgroundColor: c.success + "22",
                    alignItems: "center",
                    justifyContent: "center",
                    marginBottom: 16,
                  }}
                >
                  <Text style={{ color: c.success, fontSize: 34, fontWeight: "800", marginTop: -2 }}>{"✓"}</Text>
                </View>
                <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700", marginBottom: 4 }}>
                  Connected
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center" }}>
                  Now using {switchSuccess}
                </Text>
              </>
            ) : switchError ? (
              <>
                <Text style={{ color: c.warn, fontSize: 17, fontWeight: "700", marginBottom: 8 }}>
                  Couldn't switch
                </Text>
                <View
                  style={{
                    alignSelf: "stretch",
                    maxHeight: 220,
                    backgroundColor: c.bgCard,
                    borderWidth: 1,
                    borderColor: c.border,
                    borderRadius: 10,
                    padding: 12,
                    marginBottom: 20,
                  }}
                >
                  <ScrollView>
                    <Text
                      selectable
                      style={{ color: c.textMuted, fontSize: 12, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", lineHeight: 18 }}
                    >
                      {switchError}
                    </Text>
                  </ScrollView>
                </View>
                <View style={{ flexDirection: "row", gap: 12 }}>
                  <Pressable
                    onPress={() => {
                      setSwitchError(null);
                      setSwitching(false);
                      setProbeStage(null);
                    }}
                    style={({ pressed }) => ({
                      borderWidth: 1,
                      borderColor: c.border,
                      paddingVertical: 12,
                      paddingHorizontal: 20,
                      borderRadius: 10,
                      opacity: pressed ? 0.7 : 1,
                    })}
                  >
                    <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Back to list</Text>
                  </Pressable>
                  <Pressable
                    onPress={() => { void handleContinue(); }}
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
                </View>
              </>
            ) : (
              <>
                <ActivityIndicator color={c.accent} size="large" />
                <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 14 }}>
                  {probeStage || `Connecting to ${pickedDevice?.name || "remote box"}…`}
                </Text>
              </>
            )}
          </View>
        ) : (
          <ScrollView
            style={{ flex: 1 }}
            contentContainerStyle={[{ padding: 16, paddingBottom: 32 }, tabletContent]}
          >
            {/* Title + subtitle only make sense when there IS a list to choose
                from. With an empty roster they framed a void: a heading that
                promised a choice, a subtitle pointing at a Confirm bar that
                could never confirm. Empty → the EmptyState below carries its
                own title and the ONE action that unblocks the user. */}
            {eligibleDevices.length > 0 ? (
              <>
                <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700", marginBottom: 4 }}>
                  Choose remote box
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 20 }}>
                  Select the machine Yaver should use for app builds, live reload, and project tools. Confirm at the bottom when you're ready.
                </Text>
              </>
            ) : null}
            {eligibleDevices.length === 0 ? (
              // NOTE: deliberately bare EmptyState, NOT NoMachineEmpty —
              // NoMachineEmpty renders this very modal, so using it here
              // would recurse.
              <EmptyState
                icon="desktop-outline"
                title="No remote boxes ready"
                body={
                  sleepingMachines.length > 0
                    ? "Wake one of the sleeping machines below, or pair a new one on the Devices tab."
                    : "Devices handles pairing, auth recovery, and deep diagnostics. Come back once a machine shows as live."
                }
                action={{
                  label: "Open Devices",
                  onPress: () => {
                    onClose();
                    router.push("/(tabs)/devices" as any);
                  },
                }}
              />
            ) : (
              eligibleDevices.map((device) => {
                const ping = pingByDevice[device.id];
                const codingStatus = codingStatusByDevice[device.id];
                const codingRunners = codingStatus ? sortedCodingRunners(codingStatus.runners) : [];
                const readyCodingRunners = codingRunners.filter((r) => r.ready);
                const selected = pickedDeviceId === device.id;
                const agentVer = (device.agentVersion || "").trim();
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
                const probe = offlineProbe[device.id];
                const reachableNow = !!probe?.ok;
                const lastSeenTs = (device as any).lastSeen ?? (device as any).lastHeartbeat ?? 0;
                // A healthy agent heartbeats ~every 30s. If Convex still flags a
                // box "online" but it hasn't beat in >2min, it's very likely
                // unreachable (stale flag / dropped tunnel) — warn instead of a
                // misleading "Online · tap to select" that then fails on switch.
                const staleOnline =
                  device.online &&
                  !connectedSet.has(device.id) &&
                  !reachableNow &&
                  lastSeenTs > 0 &&
                  Date.now() - lastSeenTs > 120_000;
                const isDown = !connectedSet.has(device.id) && !device.online && !reachableNow;
                // Box heartbeats ("online") but its agent reports no live relay
                // tunnel — so it's only reachable from its own LAN, not off-LAN
                // via relay. Say so honestly instead of a bare "Online" that
                // 502s on cellular.
                const noRelayPath =
                  device.online &&
                  !connectedSet.has(device.id) &&
                  !reachableNow &&
                  (device as any).relayConnected === false;
                // Agent is up but running in bootstrap mode (no valid token) —
                // typically a box that restarted after its token left disk. It
                // is reachable and recoverable, so offer sign-in rather than a
                // "tap to select" that would connect to a daemon with no auth.
                const needsSignIn = !!device.needsAuth && !connectedSet.has(device.id);
                const statusLine = connectedSet.has(device.id)
                  ? ping && ping.ok
                    ? `Connected · ${ping.rttMs}ms`
                    : ping && !ping.ok
                      ? "Connected (pool) · ping failed"
                      : "Connected · pinging…"
                  : needsSignIn
                    ? recoveringDeviceId === device.id
                      ? "Signing in…"
                      : device.online || reachableNow
                        ? "Needs sign-in · tap to sign this machine in"
                        : `Needs sign-in · ${lastSeenLabel((device as any).lastSeen)}`
                  : staleOnline
                    ? `${lastSeenLabel(lastSeenTs).replace(/^last seen/, "Last seen")} · may be unreachable`
                    : noRelayPath
                      ? "Online · LAN-only (no relay path)"
                    : device.online
                      ? "Online · tap to select"
                      : reachableNow
                        ? `Reachable · ${probe?.line ?? ""} · tap to select`
                        : `Down · ${lastSeenLabel((device as any).lastSeen)}`;
                const roleLabel =
                  device.id === primaryDeviceId
                    ? "Primary"
                    : device.id === secondaryDeviceId
                      ? "Secondary"
                      : null;
                return (
                  <Pressable
                    key={device.id}
                    onPress={() => {
                      if (needsSignIn && (device.online || reachableNow)) {
                        void signInDevice(device);
                        return;
                      }
                      setPickedDeviceId(device.id);
                    }}
                    // Long-press a device → quick actions (Disconnect). Tearing
                    // down the client for this device frees its relay tunnel +
                    // stream slots without leaving the picker.
                    onLongPress={() => {
                      const connected = connectionManager.clientFor(device.id).isConnected;
                      Alert.alert(
                        device.name,
                        device.alias ? `@${device.alias}` : undefined,
                        [
                          {
                            text: connected ? "Disconnect" : "Disconnect (not connected)",
                            style: "destructive",
                            onPress: () => connectionManager.disconnect(device.id),
                          },
                          { text: "Cancel", style: "cancel" },
                        ],
                      );
                    }}
                    delayLongPress={350}
                    style={({ pressed }) => ({
                      marginBottom: 10,
                      padding: 14,
                      borderRadius: 10,
                      borderWidth: selected ? 1.5 : 1,
                      borderColor: selected ? c.accent : c.border,
                      backgroundColor: c.bgCard,
                      opacity: pressed ? 0.85 : 1,
                    })}
                  >
                    <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                      <View style={{ flex: 1, paddingRight: 12 }}>
                        <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }} numberOfLines={1}>
                          {device.name}
                          {device.alias ? <Text style={{ color: c.textMuted, fontWeight: "400" }}>  @{device.alias}</Text> : null}
                        </Text>
                        {roleLabel ? (
                          <View
                            style={{
                              alignSelf: "flex-start",
                              marginTop: 6,
                              paddingHorizontal: 8,
                              paddingVertical: 3,
                              borderRadius: 999,
                              borderWidth: 1,
                              borderColor: c.accent + "44",
                              backgroundColor: c.accent + "16",
                            }}
                          >
                            <Text style={{ color: c.accent, fontSize: 10, fontWeight: "700" }}>{roleLabel}</Text>
                          </View>
                        ) : null}
                        <Text
                          style={{ color: isDown || staleOnline || noRelayPath ? c.warn : c.textMuted, fontSize: 11, marginTop: 4 }}
                        >
                          {statusLine}
                          {activeDevice?.id === device.id ? " · Focused" : ""}
                          {versionSuffix && !outdated ? versionSuffix : ""}
                        </Text>
                        {isDown ? (
                          <Pressable
                            onPress={(e) => {
                              // Probe reachability without triggering the row's
                              // switch (offline boxes often have a stale Convex
                              // flag but are actually reachable over relay).
                              (e as any)?.stopPropagation?.();
                              if (!probe?.busy) void probeOffline(device);
                            }}
                            style={({ pressed }) => ({
                              alignSelf: "flex-start",
                              marginTop: 8,
                              paddingHorizontal: 12,
                              paddingVertical: 6,
                              borderRadius: 8,
                              borderWidth: 1,
                              borderColor: c.accent,
                              backgroundColor: pressed ? c.accent + "22" : "transparent",
                              flexDirection: "row",
                              alignItems: "center",
                              opacity: probe?.busy ? 0.5 : 1,
                            })}
                          >
                            {probe?.busy ? <ActivityIndicator size="small" color={c.accent} /> : null}
                            <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700", marginLeft: probe?.busy ? 6 : 0 }}>
                              {probe?.busy ? "Pinging…" : probe ? "Ping again" : "Ping"}
                            </Text>
                          </Pressable>
                        ) : null}
                        {probe && !probe.ok && !probe.busy ? (
                          <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 4 }}>
                            {probe.line} — make sure it's powered on and running the agent
                          </Text>
                        ) : null}
                        {codingStatus === undefined ? (
                          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                            Checking coding agents…
                          </Text>
                        ) : readyCodingRunners.length > 0 ? (
                          <Text style={{ color: c.success, fontSize: 11, marginTop: 4, fontWeight: "600" }} numberOfLines={1}>
                            {readyCodingRunners.map((r) => `${runnerDisplayName(r.id)} ready`).join(" · ")}
                            {codingStatus?.path ? ` · ${codingStatus.path}` : ""}
                          </Text>
                        ) : codingRunners.length > 0 ? (
                          // "Claude Code auth needed" used to be dead text: it named
                          // the blocker and then abandoned you. The remote-OAuth flow
                          // already existed (RunnerAuthModal) but was buried in the
                          // device-details sheet, which nobody opens while picking a
                          // machine. Put the fix where the problem is stated.
                          <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginTop: 4 }}>
                            <Text style={{ color: c.warn, fontSize: 11, fontWeight: "600", flexShrink: 1 }} numberOfLines={1}>
                              {codingRunners.map((r) => {
                                if (!r.installed) return `${runnerDisplayName(r.id)} not installed`;
                                if (!r.authConfigured) return `${runnerDisplayName(r.id)} auth needed`;
                                return `${runnerDisplayName(r.id)} not ready`;
                              }).join(" · ")}
                            </Text>
                            {(() => {
                              // Browser OAuth only covers claude/codex; opencode
                              // authenticates through provider config, and offering
                              // it a "Sign in" button that opens an OAuth page the
                              // user cannot complete is worse than offering nothing.
                              const signInTarget = codingRunners.find(
                                (r) =>
                                  r.installed &&
                                  !r.authConfigured &&
                                  ["claude", "claude-code", "codex"].includes(r.id),
                              );
                              if (!signInTarget) return null;
                              return (
                                <Pressable
                                  onPress={(e: any) => {
                                    // Don't let the tap fall through to the row, which
                                    // would select the box and close the picker out
                                    // from under the auth sheet.
                                    e?.stopPropagation?.();
                                    setAuthTarget({
                                      deviceId: device.id,
                                      deviceName: device.name || device.id,
                                      runner: signInTarget.id,
                                    });
                                  }}
                                  hitSlop={6}
                                  style={{
                                    paddingHorizontal: 8,
                                    paddingVertical: 3,
                                    borderRadius: 6,
                                    backgroundColor: "#f59e0b22",
                                    borderWidth: 1,
                                    borderColor: "#f59e0b66",
                                  }}
                                >
                                  <Text style={{ color: "#f59e0b", fontSize: 11, fontWeight: "700" }}>
                                    Sign in →
                                  </Text>
                                </Pressable>
                              );
                            })()}
                          </View>
                        ) : (
                          <Text style={{ color: c.warn, fontSize: 11, marginTop: 4, fontWeight: "600" }} numberOfLines={1}>
                            {codingStatus?.error || "No coding agents ready"}
                          </Text>
                        )}
                        {outdated ? (
                          <Text style={{ color: c.warn, fontSize: 11, marginTop: 2, fontWeight: "600" }}>
                            yaver {agentVer} · {distance} version{distance === 1 ? "" : "s"} behind {latestCliVersion}
                          </Text>
                        ) : null}
                      </View>
                      <Text style={{ color: selected ? c.accent : isDown || staleOnline ? c.warn : c.textMuted, fontSize: 12, fontWeight: "700" }}>
                        {selected
                          ? "SELECTED"
                          : connectedSet.has(device.id)
                            ? "CONNECTED"
                            : isDown
                              ? "DOWN"
                              : staleOnline
                                ? "STALE"
                                : "LIVE"}
                      </Text>
                    </View>
                  </Pressable>
                );
              })
            )}

            {/* The outcome blocks below are deliberately OUTSIDE the row list.
                A wake ends by removing its row from this list — the box becomes
                a device, or (on failure) can be filtered out — so anything
                rendered only on the row disappears at exactly the moment it has
                something to say. That is the bug the user filmed: tap Wake, the
                row vanishes, nothing is ever explained. */}
            {sleepingMachines.length > 0 || managedLastFailure || managedJustWoke ? (
              <View style={{ marginTop: 8 }}>
                <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700", marginBottom: 2 }}>
                  Sleeping machines
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 12 }}>
                  Yaver-managed boxes parked to stop their meter. Wake one to use it as a remote box.
                </Text>

                {managedJustWoke ? (
                  <View
                    style={{
                      marginBottom: 10,
                      padding: 12,
                      borderRadius: 10,
                      borderWidth: 1,
                      borderColor: c.success,
                      backgroundColor: c.success + "14",
                    }}
                  >
                    <Text style={{ color: c.success, fontSize: 13, fontWeight: "700" }}>
                      {managedMachineName(managedJustWoke)} is awake
                    </Text>
                    <Text style={{ color: c.textSecondary, fontSize: 11, marginTop: 4 }}>
                      It moved out of Sleeping and is listed above as a machine you can pick.
                    </Text>
                  </View>
                ) : null}

                {managedLastFailure && !sleepingMachines.some((m) => m.id === managedLastFailure.machineId) ? (
                  <View
                    style={{
                      marginBottom: 10,
                      padding: 12,
                      borderRadius: 10,
                      borderWidth: 1,
                      borderColor: c.warn,
                      backgroundColor: c.warn + "14",
                    }}
                  >
                    <Text style={{ color: c.warn, fontSize: 13, fontWeight: "700" }}>Wake failed</Text>
                    <Text style={{ color: c.textSecondary, fontSize: 11, marginTop: 4 }}>
                      {managedLastFailure.message}
                    </Text>
                  </View>
                ) : null}

                {sleepingMachines.map((machine) => (
                  <SleepingMachineRow
                    key={machine.id}
                    c={c}
                    machine={machine}
                    waking={managedWakingId === machine.id}
                    error={managedWakeErrors[machine.id]}
                    signingIn={recoveringMachineId === machine.id}
                    onWake={() => { void wakeManagedMachine(machine.id); }}
                    onSignIn={() => { void recoverManagedMachineAuth(machine); }}
                  />
                ))}
              </View>
            ) : null}

            {/* No eligible boxes → no Confirm bar. A dead "Pick a machine to
                continue" button under an empty list is an action that cannot
                work in this state. */}
            {eligibleDevices.length > 0 ? (
              <>
                <View style={{ height: 16 }} />
                <Pressable
                  onPress={() => { void handleContinue(); }}
                  disabled={!pickedDevice}
                  style={({ pressed }) => ({
                    backgroundColor: !pickedDevice ? c.border : c.accent,
                    paddingVertical: 14,
                    borderRadius: 10,
                    alignItems: "center",
                    opacity: pressed ? 0.85 : 1,
                  })}
                >
                  <Text style={{ color: !pickedDevice ? c.textMuted : "#000", fontWeight: "700" }}>
                    {!pickedDevice
                      ? "Pick a machine to continue"
                      : pickedDeviceIsCurrent && pickedDeviceIsConnected
                        ? "Keep using this machine"
                        : pickedDeviceIsCurrent
                          ? "Reconnect to this machine"
                          : "Use selected machine"}
                  </Text>
                </Pressable>
              </>
            ) : null}
          </ScrollView>
        )}
      </View>

      {/* Remote runner sign-in, driven from the row that reported the problem.
          On success, drop this device's cached coding status so the row re-probes
          and flips from "auth needed" to "ready" without the user re-opening the
          picker — the fix should be visible where the complaint was. */}
      <RunnerAuthModal
        visible={!!authTarget}
        runner={authTarget?.runner || ""}
        deviceName={authTarget?.deviceName || ""}
        target={authTarget?.deviceId}
        onClose={() => setAuthTarget(null)}
        onCompleted={() => {
          const id = authTarget?.deviceId;
          setAuthTarget(null);
          if (!id) return;
          setCodingStatusByDevice((prev) => {
            const next = { ...prev };
            delete next[id];
            return next;
          });
        }}
      />
    </Modal>
  );
}
