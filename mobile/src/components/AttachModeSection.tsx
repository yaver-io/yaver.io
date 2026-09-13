// AttachModeSection.tsx — the contributor-facing More → Develop Yaver gate.
//
// Yaver rendering Yaver: React Native defaults to its browser lane for the
// quickest edit-refresh loop. Hermes and WebRTC remain explicit choices.
//
// ── Why this is a GATE and not just a switch ────────────────────────────────
//
// Turning the mode on with nothing connected used to mean landing somewhere
// broken with no route out. So enabling walks an ordered gate — box, runner,
// checkout — where every step reports what is wrong AND the action that fixes
// it. The policy is computeAttachGate() in attachMode.ts (pure + tested); this
// component only renders it and wires the buttons, so the rules stay auditable
// on any host.
//
// Readiness comes from computeBoxReadiness() (boxInit.ts), not a second
// opinion, so this panel and the box checklist cannot drift apart.

import { router } from "expo-router";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Linking, Modal, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { Ionicons } from "@expo/vector-icons";
import type { ThemeColors } from "../constants/colors";
import { useDevice } from "../context/DeviceContext";
import {
  computeAttachGate,
  computeNestingVerdict,
  ATTACH_SENTINEL_KEY,
  type AttachStep,
} from "../lib/attachMode";
import type { BoxReadiness } from "../lib/boxInit";
import { loadBoxReadiness } from "../lib/boxInitStore";
import { runBoxAction } from "../lib/boxInitStore";
import {
  getDogfoodSourceStatus,
  getDogfoodRunners,
  dogfoodNativeRuntimeAvailable,
  installDogfoodGit,
  installDogfoodSource,
  requestDogfoodFixWithAI,
  verifyYaverCheckout,
  browserShortcutDriverFor,
  type DogfoodSourceStatus,
} from "../lib/attachClient";
import {
  BrowserShortcutController,
  type BrowserShortcutSnapshot,
} from "../../../sdk/feedback/react-native/src/BrowserShortcut";
import { BrowserShortcutStatusRail } from "../../../sdk/feedback/react-native/src/BrowserShortcutStatusRail";
import {
  defaultDogfoodLane,
  dogfoodLanePlan,
  type DogfoodLane,
} from "../../../sdk/feedback/react-native/src/DogfoodRuntime";
import {
  DogfoodLanePicker,
} from "../../../sdk/feedback/react-native/src/DogfoodSessionUi";
import {
  getDogfoodUsageMode,
  getDogfoodStartBehavior,
  getDogfoodRenderBehavior,
  getDogfoodSessionBehavior,
  getPreferredDogfoodLane,
  setDogfoodUsageMode,
  setDogfoodStartBehavior,
  setDogfoodRenderBehavior,
  setDogfoodSessionBehavior,
  setPreferredDogfoodLane,
} from "../../../sdk/feedback/react-native/src/preferences";
import type {
  DogfoodRenderBehavior,
  DogfoodSessionBehavior,
  DogfoodStartBehavior,
  DogfoodUsageMode,
} from "../../../sdk/feedback/react-native/src/dogfoodPolicy";
import RunnerAuthModal from "./RunnerAuthModal";
import { OpenCodeConfigModal } from "./OpenCodeConfigModal";
import { dogfoodCheckoutPreferenceKey } from "../lib/dogfoodCheckoutPreference";

const YAVER_DOGFOOD_APP_ID = "io.yaver.mobile";
const YAVER_DOGFOOD_MODE_SCOPE = "io.yaver.mobile:native";
const browserShortcutOriginKey = (deviceId: string) => `@yaver/browser-shortcut-origin/${deviceId}/${YAVER_DOGFOOD_APP_ID}`;
type AttachPanelKey = AttachStep["key"];
const GIT_CONFIG_FAILURE_CODES = new Set([
  "DOGFOOD_GIT_AUTH_UNCONFIGURED",
  "DOGFOOD_GIT_CREDENTIALS_EMBEDDED",
  "DOGFOOD_GIT_FETCH_FAILED",
  "DOGFOOD_GIT_ORIGIN_MISSING",
  "DOGFOOD_GIT_UPSTREAM_MISSING",
  "DOGFOOD_SOURCE_MISSING",
]);

export default function AttachModeSection({
  c,
  readiness,
  surface = "settings",
}: {
  c: ThemeColors;
  readiness?: BoxReadiness | null;
  surface?: "settings" | "usage";
}) {
  const {
    devices,
    activeDevice,
    connectionStatus,
    connectedDeviceIds,
    primaryDeviceId,
    codingMode,
    userDisconnected,
    autoConnecting,
    primaryRunnerByDevice,
    primaryModelByDevice,
    selectDevice,
    setPrimaryRunnerForDevice,
  } = useDevice();
  const activeConnected = !!activeDevice && (
    connectedDeviceIds.includes(activeDevice.id) || connectionStatus === "connected"
  );
  const primaryDevice = primaryDeviceId
    ? devices.find((device) => device.id === primaryDeviceId) || null
    : null;
  // A live explicit selection stays authoritative. When focus was lost during
  // app startup/navigation, Dogfood falls back to the account's configured
  // primary instead of rendering a dead "choose box" gate around a healthy
  // machine.
  const targetDevice = activeConnected ? activeDevice : primaryDevice || activeDevice;
  const targetConnected = !!targetDevice && (
    connectedDeviceIds.includes(targetDevice.id) ||
    (connectionStatus === "connected" && activeDevice?.id === targetDevice.id)
  );
  const [checkoutDir, setCheckoutDir] = useState("");
  const [checkoutDeviceId, setCheckoutDeviceId] = useState<string | null>(null);
  const [runner, setRunner] = useState("codex");
  const [runnerRows, setRunnerRows] = useState<Awaited<ReturnType<typeof getDogfoodRunners>>>([]);
  const [lane, setLane] = useState<DogfoodLane>("browser");
  const [laneHydrated, setLaneHydrated] = useState(false);
  const [usageMode, setUsageModeState] = useState<DogfoodUsageMode>("reload-only");
  const [startBehavior, setStartBehaviorState] = useState<DogfoodStartBehavior>("vibe-first");
  const [renderBehavior, setRenderBehaviorState] = useState<DogfoodRenderBehavior>("manual");
  const [sessionBehavior, setSessionBehaviorState] = useState<DogfoodSessionBehavior>("resume-last");
  const [nativeRuntimeAvailable, setNativeRuntimeAvailable] = useState(false);
  const [runnerSetupBusy, setRunnerSetupBusy] = useState(false);
  const [runnerSetupMessage, setRunnerSetupMessage] = useState<string | null>(null);
  const [runnerAuthFor, setRunnerAuthFor] = useState<"claude" | "codex" | null>(null);
  const [showOpenCodeConfig, setShowOpenCodeConfig] = useState(false);
  const [verified, setVerified] = useState<boolean | undefined>(undefined);
  const [verifying, setVerifying] = useState(false);
  const [failure, setFailure] = useState<{ code?: string; error: string; remedy?: string; fixPrompt?: string } | null>(null);
  const [fixing, setFixing] = useState(false);
  const [sourceStatus, setSourceStatus] = useState<DogfoodSourceStatus | null>(null);
  const [sourceBusy, setSourceBusy] = useState(false);
  const [sourceProgress, setSourceProgress] = useState<string | null>(null);
  const [measuredReadiness, setMeasuredReadiness] = useState<BoxReadiness | null>(readiness ?? null);
  const [connectingPrimary, setConnectingPrimary] = useState(false);
  const [mayOffer, setMayOffer] = useState(true);
  const [nestingReason, setNestingReason] = useState<string | undefined>();
  const [expandedStep, setExpandedStep] = useState<AttachPanelKey | null>(null);
  const shortcutController = useRef(new BrowserShortcutController());
  const primaryAutoConnectAttemptRef = useRef<string | null>(null);
  const [shortcutOrigin, setShortcutOrigin] = useState("");
  const [shortcutSnapshot, setShortcutSnapshot] = useState<BrowserShortcutSnapshot>({
    phase: "idle", progress: 0, message: "Ready to export after this machine and checkout are verified.",
  });

  // DeviceContext owns the normal app-wide sweep. This local handoff covers
  // the Dogfood entry race where the route mounts after that sweep has already
  // settled but before a focused client exists. Wait for the global sweep,
  // respect an explicit Disconnect / No remote box choice, and attempt the
  // configured primary only once per mount.
  useEffect(() => {
    if (
      targetConnected ||
      autoConnecting ||
      connectingPrimary ||
      userDisconnected ||
      codingMode !== "remote-preferred" ||
      !primaryDevice ||
      primaryAutoConnectAttemptRef.current === primaryDevice.id
    ) return;
    primaryAutoConnectAttemptRef.current = primaryDevice.id;
    setConnectingPrimary(true);
    setFailure(null);
    void selectDevice(primaryDevice, true)
      .finally(() => setConnectingPrimary(false));
  }, [autoConnecting, codingMode, connectingPrimary, primaryDevice, selectDevice, targetConnected, userDisconnected]);

  const persistCheckout = useCallback((deviceId: string | undefined, path: string) => {
    if (!deviceId) return;
    AsyncStorage.setItem(dogfoodCheckoutPreferenceKey(deviceId), path).catch(() => {});
  }, []);

  // Nesting guard: an already-attached instance must not offer to attach again.
  useEffect(() => {
    let alive = true;
    (async () => {
      let sentinel: string | null = null;
      try {
        sentinel = await AsyncStorage.getItem(ATTACH_SENTINEL_KEY);
      } catch {
        // absent is the normal host case
      }
      if (!alive) return;
      const v = computeNestingVerdict(sentinel);
      setMayOffer(v.mayOffer);
      setNestingReason(v.reason);
    })();
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    (async () => {
      try {
        const [savedLane, savedUsageMode, savedStart, savedRender, savedSession] = await Promise.all([
          getPreferredDogfoodLane(YAVER_DOGFOOD_APP_ID),
          getDogfoodUsageMode(YAVER_DOGFOOD_MODE_SCOPE),
          getDogfoodStartBehavior(YAVER_DOGFOOD_MODE_SCOPE),
          getDogfoodRenderBehavior(YAVER_DOGFOOD_MODE_SCOPE),
          getDogfoodSessionBehavior(YAVER_DOGFOOD_MODE_SCOPE),
        ]);
        if (savedLane) setLane(savedLane);
        if (savedUsageMode) setUsageModeState(savedUsageMode);
        if (savedStart) setStartBehaviorState(savedStart);
        if (savedRender) setRenderBehaviorState(savedRender);
        if (savedSession) setSessionBehaviorState(savedSession);
      } catch {
        // best-effort
      } finally {
        setLaneHydrated(true);
      }
    })();
  }, []);

  // Never carry an absolute checkout path from one box to another. Resolve a
  // separate remembered path per device; when absent, the agent discovery
  // effect below asks the selected box to find its own Yaver Git checkout.
  useEffect(() => {
    const deviceId = targetDevice?.id;
    if (!deviceId) {
      setCheckoutDeviceId(null);
      setCheckoutDir("");
      return;
    }
    let cancelled = false;
    // Mark this device as the checkout owner immediately. AsyncStorage is a
    // cache, not a prerequisite for asking the selected box where its source
    // lives; waiting for it here could suppress discovery indefinitely when
    // native storage was slow during a cold launch.
    setCheckoutDeviceId(deviceId);
    setCheckoutDir("");
    setSourceStatus(null);
    setVerified(undefined);
    void AsyncStorage.getItem(dogfoodCheckoutPreferenceKey(deviceId)).then((path) => {
      if (cancelled) return;
      setCheckoutDir(path || "");
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [targetDevice?.id]);

  useEffect(() => {
    shortcutController.current.cancel();
    setShortcutSnapshot({ phase: "idle", progress: 0, message: "Ready to export after this machine and checkout are verified." });
    const deviceId = targetDevice?.id;
    if (!deviceId) {
      setShortcutOrigin("");
      return;
    }
    let cancelled = false;
    setShortcutOrigin("");
    void AsyncStorage.getItem(browserShortcutOriginKey(deviceId)).then((saved) => {
      if (!cancelled) setShortcutOrigin(saved || "");
    }).catch(() => {});
    return () => { cancelled = true; shortcutController.current.cancel(); };
  }, [targetDevice?.id]);

  // Dogfood uses the same per-device runner preference as Tasks and Vibing.
  // Switching the connected/primary device therefore restores its runner
  // instead of maintaining a second Dogfood-only choice.
  useEffect(() => {
    if (!targetDevice?.id) return;
    setRunner(primaryRunnerByDevice[targetDevice.id] || "codex");
  }, [primaryRunnerByDevice, targetDevice?.id]);

  useEffect(() => {
    let cancelled = false;
    if (!targetDevice?.id || !targetConnected) {
      setRunnerRows([]);
      return;
    }
    void getDogfoodRunners(targetDevice.id)
      .then((rows) => {
        if (cancelled) return;
        setRunnerRows(rows);
        // A first-time user should inherit the box's proved working default,
        // not an arbitrary Codex placeholder. An explicit per-box choice is
        // never overwritten.
        if (!primaryRunnerByDevice[targetDevice.id]) {
          const ready = rows.find((row) => row.isDefault && (row.ready ?? row.installed))
            || rows.find((row) => row.ready ?? row.installed);
          if (ready) {
            const nextRunner = ready.id === "claude" ? "claude-code" : ready.id;
            setRunner(nextRunner);
            void setPrimaryRunnerForDevice(targetDevice.id, nextRunner, ready.models.find((model) => model.isDefault)?.id || null);
          }
        }
      })
      .catch(() => { if (!cancelled) setRunnerRows([]); });
    return () => { cancelled = true; };
  }, [primaryRunnerByDevice, setPrimaryRunnerForDevice, targetConnected, targetDevice?.id]);

  // Dogfood should be one action in the normal case. The Go
  // agent owns this answer because only it can inspect the box's source and
  // Git origin. A cached client-side repo list is not an operational check.
  useEffect(() => {
    // Lane/UI preferences are unrelated to source discovery. A slow native
    // preference read must never prevent the operational checkout probe.
    if (!targetConnected || !targetDevice?.id || checkoutDeviceId !== targetDevice.id) return;
    let cancelled = false;
    void (async () => {
      const requestedPath = checkoutDir.trim();
      const [requested, discovered] = await Promise.all([
        getDogfoodSourceStatus(targetDevice.id, requestedPath || undefined),
        requestedPath ? getDogfoodSourceStatus(targetDevice.id) : Promise.resolve(null),
      ]);
      let status = requested;
      if (discovered) {
        const candidates = [...(discovered.candidates || []), ...(requested.candidates || [])]
          .filter((candidate, index, rows) => rows.findIndex((row) => row.path === candidate.path) === index);
        if (!requested.ready && discovered.ready) {
          status = { ...discovered, candidates };
        } else {
          status = { ...requested, candidates };
        }
      }
      // Cached test/validation worktrees are commonly detached. Prefer the
      // agent's normal named checkout when it can prove one, so a stale local
      // preference does not strand Dogfood on a disposable clone.
      if (status.ready && status.branch === "HEAD" && discovered?.ready && discovered.branch && discovered.branch !== "HEAD") {
        status = { ...discovered, candidates: status.candidates };
      }
      if (cancelled) return;
      setSourceStatus(status);
      if (status.ready && status.path && status.path !== checkoutDir.trim()) {
        setCheckoutDir(status.path);
        persistCheckout(targetDevice.id, status.path);
      }
    })();
    return () => { cancelled = true; };
  // The explicit checkout is verified by the debounced effect below; this
  // effect is for initial agent-owned discovery only.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [checkoutDeviceId, persistCheckout, targetConnected, targetDevice?.id]);

  // Verification is the AGENT's answer, never a path guess here. Re-runs when
  // the directory or the box changes, and resets to "unknown" first so a stale
  // green can never stand in for the new answer.
  const verify = useCallback(
    async (dir: string) => {
      if (!targetDevice?.id || !targetConnected || checkoutDeviceId !== targetDevice.id || !dir.trim()) {
        setVerified(undefined);
        return;
      }
      setVerifying(true);
      setVerified(undefined);
      const [ok, status] = await Promise.all([
        verifyYaverCheckout(targetDevice.id, dir.trim()),
        getDogfoodSourceStatus(targetDevice.id, dir.trim()),
      ]);
      setSourceStatus(status);
      setVerified(ok);
      setVerifying(false);
    },
    [checkoutDeviceId, targetConnected, targetDevice?.id],
  );

  useEffect(() => {
    if (!checkoutDir.trim()) {
      setVerified(undefined);
      return;
    }
    const t = setTimeout(() => void verify(checkoutDir), 600);
    return () => clearTimeout(t);
  }, [checkoutDir, verify]);

  useEffect(() => {
    let cancelled = false;
    if (!targetDevice?.id || !targetConnected || verified !== true) {
      setNativeRuntimeAvailable(false);
      return;
    }
    void dogfoodNativeRuntimeAvailable(targetDevice.id, checkoutDir.trim())
      .then((available) => { if (!cancelled) setNativeRuntimeAvailable(available); });
    return () => { cancelled = true; };
  }, [checkoutDir, targetConnected, targetDevice?.id, verified]);

  const laneCapabilities = useMemo(() => ({
    nativeRuntimeAvailable,
    selfDevelopment: true,
  }), [nativeRuntimeAvailable]);
  const lanePolicy = useMemo(() => dogfoodLanePlan("expo", laneCapabilities, lane), [lane, laneCapabilities]);
  const laneOptions = lanePolicy.options;
  const normalizedRunner = runner === "claude-code" ? "claude" : runner;
  const selectedRunnerRow = runnerRows.find((row) => (row.id === "claude-code" ? "claude" : row.id) === normalizedRunner);
  const selectedModel = targetDevice?.id ? primaryModelByDevice[targetDevice.id] || "" : "";
  const checkoutCandidates = sourceStatus?.candidates || [];

  useEffect(() => {
    if (!laneOptions.some((option) => option.lane === lane && option.supported)) {
      setLane(defaultDogfoodLane("expo", laneCapabilities));
    }
  }, [lane, laneCapabilities, laneOptions]);

  // Readiness is an operational probe, not an optional decoration. The old
  // call site supplied no value, leaving the gate at "checking…" forever.
  useEffect(() => {
    let cancelled = false;
    if (!targetDevice?.id || !targetConnected) {
      setMeasuredReadiness(null);
      return;
    }
    void loadBoxReadiness(targetDevice.id)
      .then((next) => { if (!cancelled) setMeasuredReadiness(next); })
      .catch((err) => {
        if (!cancelled) {
          setMeasuredReadiness(null);
          setFailure({
            error: err instanceof Error ? err.message : String(err),
            remedy: "Reconnect the primary device, then try Dogfood mode again.",
          });
        }
      });
    return () => { cancelled = true; };
  }, [targetConnected, targetDevice?.id]);

  useEffect(() => {
    if (readiness !== undefined) setMeasuredReadiness(readiness ?? null);
  }, [readiness]);

  const gate = computeAttachGate({
    deviceId: targetConnected ? targetDevice?.id : null,
    deviceName: targetDevice?.name,
    readiness: measuredReadiness,
    runner,
    checkoutDir: checkoutDeviceId === targetDevice?.id ? checkoutDir.trim() || null : null,
    checkoutVerified: verifying ? undefined : verified,
  });
  const runnerCheckKey = runner === "claude-code" ? "claude" : runner;
  const runnerCheck = measuredReadiness?.checks.find((check) => check.key === runnerCheckKey);

  const configureRunner = useCallback(async () => {
    if (!targetDevice?.id || !runnerCheck || runnerCheck.status === "ok" || runnerSetupBusy) return;
    if (runnerCheck.status !== "missing") {
      if (runner === "opencode") setShowOpenCodeConfig(true);
      else setRunnerAuthFor(runner === "claude-code" ? "claude" : "codex");
      return;
    }
    setRunnerSetupBusy(true);
    setRunnerSetupMessage(`Installing ${runner}…`);
    setFailure(null);
    try {
      const result = await runBoxAction(targetDevice.id, runnerCheck.action);
      if (!result.ok) throw new Error(result.error || `Could not install ${runner}.`);
      const next = await loadBoxReadiness(targetDevice.id);
      setMeasuredReadiness(next);
      const nextCheck = next.checks.find((check) => check.key === runnerCheckKey);
      setRunnerSetupMessage(result.detail || "Runner installed");
      if (nextCheck?.status !== "ok") {
        if (runner === "opencode") setShowOpenCodeConfig(true);
        else setRunnerAuthFor(runner === "claude-code" ? "claude" : "codex");
      }
    } catch (error) {
      setFailure({
        code: "DOGFOOD_RUNNER_SETUP_FAILED",
        error: error instanceof Error ? error.message : String(error),
        remedy: `Retry ${runner} setup on ${targetDevice.name}.`,
      });
    } finally {
      setRunnerSetupBusy(false);
    }
  }, [runner, runnerCheck, runnerCheckKey, runnerSetupBusy, targetDevice?.id, targetDevice?.name]);

  const attach = useCallback(() => {
    if (!targetDevice?.id || !gate.canAttach || !laneHydrated) return;
    setFailure(null);
    router.push({
      pathname: "/dogfood-launch" as any,
      params: {
        workDir: checkoutDir.trim(),
        runner,
        lane,
        fallbackLane: lanePolicy.fallback,
        usageMode,
        startBehavior,
        renderBehavior,
        sessionBehavior,
        deviceId: targetDevice.id,
        deviceName: targetDevice.name,
      },
    } as any);
  }, [checkoutDir, gate.canAttach, lane, laneHydrated, lanePolicy.fallback, renderBehavior, runner, sessionBehavior, startBehavior, targetDevice?.id, targetDevice?.name, usageMode]);

  const shortcutBusy = ["checking", "building", "publishing", "verifying"].includes(shortcutSnapshot.phase);
  const shortcutCheckoutReady = targetConnected && checkoutDeviceId === targetDevice?.id && verified === true && !!checkoutDir.trim();
  const visibleShortcutSnapshot = useMemo<BrowserShortcutSnapshot>(() => {
    if (shortcutSnapshot.phase !== "idle") return shortcutSnapshot;
    if (!targetConnected) {
      return {
        phase: "blocked", activeStep: "connection", progress: 0,
        code: "BROWSER_SHORTCUT_BOX_OFFLINE",
        message: "Connect a machine before exporting.",
        remedy: "Choose or wake the Ubuntu, Mac, or Windows box that owns this checkout.",
      };
    }
    if (!shortcutCheckoutReady) {
      return {
        phase: "blocked", activeStep: "connection", progress: 0,
        code: "BROWSER_SHORTCUT_CHECKOUT_REQUIRED",
        message: "Verify the Yaver checkout on this machine first.",
        remedy: "Complete the Checkout row above; export will remain disabled until the agent proves it.",
      };
    }
    return shortcutSnapshot;
  }, [shortcutCheckoutReady, shortcutSnapshot, targetConnected]);

  const exportYaverShortcut = useCallback(async () => {
    if (!targetDevice?.id || !shortcutCheckoutReady || shortcutBusy) return;
    const driver = browserShortcutDriverFor(targetDevice.id);
    if (!driver) {
      setShortcutSnapshot({
        phase: "blocked", activeStep: "connection", progress: 0,
        code: "BROWSER_SHORTCUT_BOX_OFFLINE", message: "The selected machine is no longer connected.",
        remedy: "Reconnect it, then retry. No browser build was started.",
      });
      return;
    }
    const customOrigin = shortcutOrigin.trim();
    await shortcutController.current.run(driver, {
      appId: YAVER_DOGFOOD_APP_ID,
      projectPath: checkoutDir.trim().replace(/\/+$/, "") + "/mobile",
      ...(customOrigin ? { publicOrigin: customOrigin } : {}),
      brand: {
        displayName: "Yaver",
        shortName: "Yaver",
        themeColor: "#6C5CE7",
        backgroundColor: "#F8F8FB",
      },
      buildMode: "full",
    }, setShortcutSnapshot);
  }, [checkoutDir, shortcutBusy, shortcutCheckoutReady, shortcutOrigin, targetDevice?.id]);

  const runSourceFix = useCallback(async () => {
    if (!targetDevice?.id || sourceBusy) return;
    setSourceBusy(true);
    setFailure(null);
    setSourceProgress("Looking for a Yaver checkout…");
    try {
      let status = await getDogfoodSourceStatus(targetDevice.id);
      setSourceStatus(status);
      if (status.ready && status.path) {
        setCheckoutDir(status.path);
        persistCheckout(targetDevice.id, status.path);
        await verify(status.path);
        return;
      }

      if (status.code === "DOGFOOD_GIT_NOT_INSTALLED") {
        setSourceProgress("Installing Git…");
        const git = await installDogfoodGit(
          targetDevice.id,
          (line) => setSourceProgress(line.trim() || "Installing Git…"),
        );
        if (!git.ok) {
          setFailure({
            code: status.code,
            error: git.error || "Git installation did not complete.",
            remedy: "Open Git configuration on this box, or retry the install.",
          });
          return;
        }
        setSourceProgress("Looking for a Yaver checkout…");
        status = await getDogfoodSourceStatus(targetDevice.id);
        setSourceStatus(status);
        if (status.ready && status.path) {
          setCheckoutDir(status.path);
          persistCheckout(targetDevice.id, status.path);
          await verify(status.path);
          return;
        }
      }

      setSourceProgress("Cloning Yaver source…");
      const result = await installDogfoodSource(targetDevice.id, runner);
      if (!result.ok || !result.path) {
        setFailure({
          code: status.code || "DOGFOOD_SOURCE_CLONE_FAILED",
          error: result.error || "The source repair did not complete.",
          remedy: "Open Git configuration if GitHub access is required, then retry the clone.",
        });
        return;
      }

      const installed = await getDogfoodSourceStatus(targetDevice.id, result.path);
      setSourceStatus(installed);
      if (!installed.ready || !installed.path) {
        setFailure({
          code: installed.code,
          error: installed.message,
          remedy: installed.remedy || "Choose another Yaver checkout or open Git configuration.",
        });
        return;
      }
      setCheckoutDir(installed.path);
      persistCheckout(targetDevice.id, installed.path);
      await verify(installed.path);
    } catch (error) {
      setFailure({
        code: "DOGFOOD_SOURCE_FIX_FAILED",
        error: error instanceof Error ? error.message : String(error),
        remedy: "Reconnect the box, then retry. Open Git configuration if cloning still fails.",
      });
    } finally {
      setSourceBusy(false);
      setSourceProgress(null);
    }
  }, [persistCheckout, runner, sourceBusy, targetDevice?.id, verify]);

  const toggleStep = useCallback((step: AttachStep) => {
    // Runner and Checkout both live on the selected box. If that prerequisite
    // is missing, every row opens the same real box setup instead of revealing
    // controls that cannot work yet.
    const key = step.key !== "box" && (!targetDevice?.id || !targetConnected) ? "box" : step.key;
    setExpandedStep((current) => current === key ? null : key);
  }, [targetConnected, targetDevice?.id]);

  if (!mayOffer) {
    return (
      <View>
        <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 15 }}>Dogfood mode</Text>
        <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 6, lineHeight: 17 }}>
          {nestingReason}
        </Text>
      </View>
    );
  }

  if (surface === "usage") {
    return (
      <View>
        <View style={{ gap: 8, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, borderRadius: 16, padding: 16 }}>
          <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "800" }}>Dogfood</Text>
          <Text style={{ color: c.textSecondary, fontSize: 12, lineHeight: 18 }}>
            Launch opens the selected lane's live console before rendering the app.
          </Text>
          {gate.canAttach && laneHydrated ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Launch Dogfood"
              onPress={() => void attach()}
              style={({ pressed }) => ({ marginTop: 6, borderRadius: 12, backgroundColor: c.accent, paddingVertical: 13, alignItems: "center", opacity: pressed ? 0.75 : 1 })}
            >
              <Text style={{ color: "#fff", fontWeight: "800" }}>Launch Dogfood</Text>
            </Pressable>
          ) : (
            <Text style={{ color: c.warn, fontSize: 12, lineHeight: 17 }}>
              {!laneHydrated ? "Loading your Dogfood runtime choice…" : gate.nextStep?.detail || "Complete Dogfood Settings before launching."}
            </Text>
          )}
        </View>
      </View>
    );
  }

  return (
    <View>
      {/* One ordered setup contract: machine, runner, checkout, then runtime.
          The whole row is the route; inventories stay behind that row. */}
      <View accessibilityLabel="Dogfood session readiness" style={{ gap: 10 }}>
        {gate.steps.map((step) => {
          const busy = step.key === "checkout" && sourceBusy;
          const expanded = expandedStep === step.key;
          const tone = step.status === "ok" ? c.success : step.status === "blocked" ? c.error : c.warn;
          const icon = step.key === "box" ? "desktop-outline" : step.key === "runner" ? "sparkles-outline" : "folder-open-outline";
          const action = busy ? "Setting up…" : step.status === "ok" ? "Change" : "Set up";
          return (
            <Pressable
              key={step.key}
              accessibilityRole="button"
              accessibilityLabel={`${action} ${step.label}`}
              accessibilityState={{ expanded, busy }}
              disabled={busy}
              onPress={() => toggleStep(step)}
              style={({ pressed }) => ({
                minHeight: 76,
                flexDirection: "row",
                alignItems: "center",
                gap: 12,
                paddingHorizontal: 14,
                paddingVertical: 12,
                borderRadius: 16,
                borderWidth: 1,
                borderColor: expanded ? c.accent : c.border,
                backgroundColor: c.bgCard,
                opacity: busy ? 0.65 : pressed ? 0.78 : 1,
              })}
            >
              <View style={{ width: 44, height: 44, borderRadius: 14, alignItems: "center", justifyContent: "center", backgroundColor: `${tone}18` }}>
                {busy ? <ActivityIndicator size="small" color={c.accent} /> : <Ionicons name={icon as any} size={23} color={tone} />}
              </View>
              <View style={{ flex: 1 }}>
                <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>{step.label}</Text>
                <Text style={{ color: step.status === "ok" ? c.textSecondary : tone, fontSize: 12, lineHeight: 17, marginTop: 2 }} numberOfLines={2}>
                  {step.detail}
                </Text>
              </View>
              <View style={{ alignItems: "flex-end", gap: 4 }}>
                <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>{action}</Text>
                <Ionicons name={expanded ? "chevron-up" : "chevron-forward"} size={17} color={c.textMuted} />
              </View>
            </Pressable>
          );
        })}
        <View accessibilityLabel="Runtime lane choices" style={{ gap: 10, padding: 14, borderRadius: 16, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard }}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 12 }}>
            <View style={{ width: 44, height: 44, borderRadius: 14, alignItems: "center", justifyContent: "center", backgroundColor: `${c.success}18` }}>
              <Ionicons name="layers-outline" size={23} color={c.success} />
            </View>
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>Runtime lane</Text>
              <Text style={{ color: c.textSecondary, fontSize: 12, lineHeight: 17, marginTop: 2 }}>
                Choose where this working tree renders.
              </Text>
            </View>
          </View>
          <DogfoodLanePicker
            options={laneOptions}
            selected={lane}
            fallbackLane={lanePolicy.fallback}
            onSelect={(next: DogfoodLane) => {
              setLane(next);
              void setPreferredDogfoodLane(YAVER_DOGFOOD_APP_ID, next);
            }}
            colors={{
              background: c.bg, border: c.border, text: c.textPrimary, muted: c.textMuted,
              accent: c.accent, accentSoft: c.accentSoft, ready: c.success,
              attention: c.warn, blocked: c.error, console: "#0b0f14",
            }}
          />
        </View>
        <View style={{ minHeight: 76, gap: 9, paddingHorizontal: 14, paddingVertical: 12, borderRadius: 16, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard }}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>Dogfood UI</Text>
          <View style={{ flexDirection: "row", gap: 8 }}>
            {(["chat-only", "reload-only", "reload-and-chat"] as const).map((mode) => {
              const selected = usageMode === mode;
              return (
                <Pressable
                  key={mode}
                  accessibilityRole="radio"
                  accessibilityState={{ checked: selected }}
                  aria-checked={selected}
                  onPress={() => {
                    setUsageModeState(mode);
                    void setDogfoodUsageMode(mode, YAVER_DOGFOOD_MODE_SCOPE);
                  }}
                  style={{ flex: 1, borderRadius: 9, borderWidth: 1, borderColor: selected ? c.accent : c.border, backgroundColor: selected ? c.accentSoft : c.bg, padding: 9 }}
                >
                  <Text style={{ color: selected ? c.accent : c.textPrimary, fontSize: 12, fontWeight: "700" }}>
                    {mode === "chat-only" ? "Chat Only" : mode === "reload-only" ? "Reload Only" : "Reload + Chat"}
                  </Text>
                </Pressable>
              );
            })}
          </View>
          <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700", marginTop: 4 }}>Start</Text>
          <View style={{ flexDirection: "row", gap: 8 }}>
            {(["vibe-first", "render-on-open"] as const).map((value) => {
              const selected = startBehavior === value;
              return <Pressable key={value} accessibilityRole="radio" accessibilityState={{ checked: selected }} aria-checked={selected} onPress={() => { setStartBehaviorState(value); void setDogfoodStartBehavior(value, YAVER_DOGFOOD_MODE_SCOPE); }} style={{ flex: 1, borderRadius: 9, borderWidth: 1, borderColor: selected ? c.accent : c.border, backgroundColor: selected ? c.accentSoft : c.bg, padding: 9 }}><Text style={{ color: selected ? c.accent : c.textPrimary, fontSize: 12, fontWeight: "700" }}>{value === "vibe-first" ? "Vibe first" : "Render on open"}</Text></Pressable>;
            })}
          </View>
          <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700", marginTop: 4 }}>After UI updates</Text>
          <View style={{ flexDirection: "row", gap: 8 }}>
            {(["manual", "auto-on-request"] as const).map((value) => {
              const selected = renderBehavior === value;
              return <Pressable key={value} accessibilityRole="radio" accessibilityState={{ checked: selected }} aria-checked={selected} onPress={() => { setRenderBehaviorState(value); void setDogfoodRenderBehavior(value, YAVER_DOGFOOD_MODE_SCOPE); }} style={{ flex: 1, borderRadius: 9, borderWidth: 1, borderColor: selected ? c.accent : c.border, backgroundColor: selected ? c.accentSoft : c.bg, padding: 9 }}><Text style={{ color: selected ? c.accent : c.textPrimary, fontSize: 12, fontWeight: "700" }}>{value === "manual" ? "Tap Render" : "Auto-render"}</Text></Pressable>;
            })}
          </View>
          <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700", marginTop: 4 }}>Sessions</Text>
          <View style={{ flexDirection: "row", gap: 8 }}>
            {(["resume-last", "new-session"] as const).map((value) => {
              const selected = sessionBehavior === value;
              return <Pressable key={value} accessibilityRole="radio" accessibilityState={{ checked: selected }} aria-checked={selected} onPress={() => { setSessionBehaviorState(value); void setDogfoodSessionBehavior(value, YAVER_DOGFOOD_MODE_SCOPE); }} style={{ flex: 1, borderRadius: 9, borderWidth: 1, borderColor: selected ? c.accent : c.border, backgroundColor: selected ? c.accentSoft : c.bg, padding: 9 }}><Text style={{ color: selected ? c.accent : c.textPrimary, fontSize: 12, fontWeight: "700" }}>{value === "resume-last" ? "Resume newest" : "Start new"}</Text></Pressable>;
            })}
          </View>
        </View>
      </View>

      {expandedStep ? (
        <Modal
          transparent
          animationType="slide"
          visible
          onRequestClose={() => setExpandedStep(null)}
        >
          <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,.38)", justifyContent: "flex-end" }}>
            <Pressable
              style={{ flex: 1 }}
              onPress={() => setExpandedStep(null)}
              accessibilityRole="button"
              accessibilityLabel="Close Dogfood setting choices"
            />
            <ScrollView contentContainerStyle={{ borderTopLeftRadius: 22, borderTopRightRadius: 22, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, paddingHorizontal: 16, paddingTop: 12, paddingBottom: 24 }} style={{ maxHeight: "78%" }} keyboardShouldPersistTaps="handled">
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 4 }}>
                <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "800" }}>
                  {expandedStep === "box" ? "Choose box" : expandedStep === "runner" ? "Choose runner" : "Choose checkout"}
                </Text>
                <Pressable onPress={() => setExpandedStep(null)} accessibilityRole="button" accessibilityLabel="Close Dogfood setting choices" style={{ padding: 8 }}>
                  <Ionicons name="close" size={21} color={c.textMuted} />
                </Pressable>
              </View>
      {expandedStep === "box" ? (
        <View accessibilityLabel="Box choices" style={{ marginTop: 8, paddingLeft: 20 }}>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
            {devices.map((device) => {
              const selected = targetDevice?.id === device.id;
              const connected = connectedDeviceIds.includes(device.id);
              return (
                <Pressable
                  key={device.id}
                  disabled={connectingPrimary}
                  onPress={() => {
                    setConnectingPrimary(true);
                    setFailure(null);
                    void selectDevice(device)
                      .catch((err) => setFailure({
                        error: err instanceof Error ? err.message : String(err),
                        remedy: `Wake or repair ${device.name}, then retry.`,
                      }))
                      .finally(() => setConnectingPrimary(false));
                  }}
                  style={{
                    paddingHorizontal: 11,
                    paddingVertical: 8,
                    borderRadius: 8,
                    borderWidth: 1,
                    borderColor: selected ? c.accent : c.border,
                    backgroundColor: selected ? `${c.accent}22` : c.bg,
                    opacity: connected ? 1 : 0.65,
                  }}
                >
                  <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: selected ? "700" : "500" }}>
                    {device.name}{connected ? "" : " · offline"}
                  </Text>
                </Pressable>
              );
            })}
          </View>
          {!devices.length ? (
            <View style={{ alignItems: "flex-start", gap: 8 }}>
              <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 17 }}>
                Pair the Mac, Linux, or Windows machine that will run Yaver and your coding agent.
              </Text>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Pair a remote box"
                onPress={() => router.push({ pathname: "/(tabs)/more" as any, params: { openPair: "1", returnTo: "dogfood" } } as any)}
                style={{ flexDirection: "row", alignItems: "center", gap: 7, paddingHorizontal: 11, paddingVertical: 9, borderRadius: 9, backgroundColor: c.accent }}
              >
                <Ionicons name="link-outline" size={16} color="#fff" />
                <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>Pair remote box</Text>
              </Pressable>
            </View>
          ) : null}
        </View>
      ) : null}

      {expandedStep === "runner" ? (
        <View accessibilityLabel="Runner choices" style={{ marginTop: 8, paddingLeft: 20 }}>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
            {(["claude-code", "codex", "opencode"] as const).map((r) => {
              const on = runner === r;
              return (
                <Pressable
                  key={r}
                  onPress={() => {
                    setRunner(r);
                    if (targetDevice?.id) void setPrimaryRunnerForDevice(targetDevice.id, r, null);
                  }}
                  style={({ pressed }) => [
                    {
                      paddingHorizontal: 12,
                      paddingVertical: 8,
                      borderRadius: 8,
                      borderWidth: 1,
                      borderColor: on ? c.accent : c.border,
                      backgroundColor: on ? `${c.accent}22` : c.bg,
                    },
                    pressed && { opacity: 0.7 },
                  ]}
                >
                  <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: on ? "700" : "500" }}>{r}</Text>
                </Pressable>
              );
            })}
          </View>
          {selectedRunnerRow?.models?.length ? (
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 8 }}>
              {selectedRunnerRow.models.map((model) => {
                const on = selectedModel === model.id || (!selectedModel && model.isDefault);
                return (
                  <Pressable
                    key={model.id}
                    onPress={() => targetDevice?.id && void setPrimaryRunnerForDevice(targetDevice.id, runner, model.id)}
                    style={{ paddingHorizontal: 10, paddingVertical: 7, borderRadius: 8, borderWidth: 1, borderColor: on ? c.accent : c.border, backgroundColor: on ? `${c.accent}22` : c.bg }}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 11, fontWeight: on ? "700" : "500" }}>{model.name || model.id}</Text>
                  </Pressable>
                );
              })}
            </View>
          ) : null}
          {targetConnected && runnerCheck && runnerCheck.status !== "ok" ? (
            <Pressable
              disabled={runnerSetupBusy}
              onPress={() => void configureRunner()}
              style={{ marginTop: 8, alignSelf: "flex-start", paddingHorizontal: 11, paddingVertical: 8, borderRadius: 8, backgroundColor: c.accentSoft }}
            >
              <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>
                {runnerSetupBusy ? "Installing…" : runnerCheck.status === "missing" ? `Install ${runner}` : `Configure ${runner}`}
              </Text>
            </Pressable>
          ) : null}
          {runnerSetupMessage ? <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>{runnerSetupMessage}</Text> : null}

        </View>
      ) : null}

      {expandedStep === "checkout" ? (
        <View accessibilityLabel="Yaver checkout choices" style={{ marginTop: 8, paddingLeft: 20 }}>
          {sourceBusy ? (
            <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 8 }}>{sourceProgress || "Working…"}</Text>
          ) : null}
          <Pressable
            disabled={sourceBusy || !targetConnected}
            onPress={() => void runSourceFix()}
            accessibilityRole="button"
            accessibilityLabel="Find Yaver checkout on this box"
            style={({ pressed }) => ({ alignSelf: "flex-start", marginBottom: 10, paddingHorizontal: 11, paddingVertical: 8, borderRadius: 8, borderWidth: 1, borderColor: c.accent, opacity: sourceBusy || !targetConnected ? 0.5 : pressed ? 0.75 : 1 })}
          >
            <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>{sourceBusy ? "Finding checkout…" : "Find on this box"}</Text>
          </Pressable>
          {checkoutCandidates.length ? (
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginBottom: 10 }}>
              {checkoutCandidates.map((candidate) => {
                const selected = checkoutDir.trim() === candidate.path;
                return (
                  <Pressable
                    key={candidate.path}
                    onPress={() => {
                      setCheckoutDir(candidate.path);
                      persistCheckout(targetDevice?.id, candidate.path);
                      void verify(candidate.path);
                    }}
                    style={{ paddingHorizontal: 10, paddingVertical: 8, borderRadius: 8, borderWidth: 1, borderColor: selected ? c.accent : c.border, backgroundColor: selected ? `${c.accent}22` : c.bg }}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 11, fontWeight: selected ? "700" : "500" }}>{candidate.path}</Text>
                    {candidate.branch ? <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>{candidate.branch}</Text> : null}
                  </Pressable>
                );
              })}
            </View>
          ) : null}
          {sourceStatus && !sourceStatus.ready ? (
            <View style={{ borderWidth: 1, borderColor: c.warn, borderRadius: 10, padding: 10, marginBottom: 10 }}>
              <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "600" }}>{sourceStatus.message}</Text>
              {sourceStatus.remedy ? (
                <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16, marginTop: 4 }}>{sourceStatus.remedy}</Text>
              ) : null}
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 9 }}>
                <Pressable
                  disabled={sourceBusy}
                  onPress={() => void runSourceFix()}
                  style={{ paddingHorizontal: 11, paddingVertical: 8, borderRadius: 8, backgroundColor: c.accent }}
                >
                  <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>{sourceBusy ? "Working…" : "Try checkout fix"}</Text>
                </Pressable>
                {GIT_CONFIG_FAILURE_CODES.has(sourceStatus.code) && targetDevice?.id ? (
                  <Pressable
                    onPress={() => router.push({ pathname: "/(tabs)/settings" as any, params: { gitWizard: "1", deviceId: targetDevice.id } } as any)}
                    style={{ paddingHorizontal: 11, paddingVertical: 8, borderRadius: 8, borderWidth: 1, borderColor: c.accent }}
                  >
                    <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>Open Git configuration</Text>
                  </Pressable>
                ) : null}
              </View>
            </View>
          ) : null}
          <TextInput
            value={checkoutDir}
            onChangeText={(t) => {
              setCheckoutDir(t);
              persistCheckout(targetDevice?.id, t);
            }}
            accessibilityLabel="Yaver checkout path"
            placeholder="Path to yaver.io"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            autoCorrect={false}
            spellCheck={false}
            style={{
              color: c.textPrimary,
              borderColor: c.border,
              backgroundColor: c.bg,
              borderWidth: 1,
              borderRadius: 8,
              paddingHorizontal: 10,
              paddingVertical: 10,
              fontSize: 13,
            }}
          />
        </View>
      ) : null}

            </ScrollView>
          </View>
        </Modal>
      ) : null}

      {failure ? (
        <View
          style={{
            marginTop: 12,
            borderWidth: 1,
            borderColor: c.errorBorder,
            backgroundColor: c.errorBg,
            borderRadius: 10,
            padding: 10,
          }}
        >
          {failure.code ? <Text style={{ color: c.textMuted, fontSize: 10, marginBottom: 4 }}>{failure.code}</Text> : null}
          <Text style={{ color: c.textPrimary, fontSize: 12, lineHeight: 17 }}>{failure.error}</Text>
          {failure.remedy ? (
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6, lineHeight: 16 }}>
              {failure.remedy}
            </Text>
          ) : null}
          {failure.code && GIT_CONFIG_FAILURE_CODES.has(failure.code) && targetDevice?.id ? (
            <Pressable
              onPress={() => router.push({ pathname: "/(tabs)/settings" as any, params: { gitWizard: "1", deviceId: targetDevice.id } } as any)}
              style={{ marginTop: 10, alignSelf: "flex-start", borderWidth: 1, borderColor: c.accent, borderRadius: 8, paddingHorizontal: 11, paddingVertical: 8 }}
            >
              <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>Configure Git on this box</Text>
            </Pressable>
          ) : null}
          {failure.fixPrompt && targetDevice?.id && targetConnected ? (
            <Pressable
              disabled={fixing}
              onPress={() => {
                if (fixing || !failure.fixPrompt) return;
                setFixing(true);
                void requestDogfoodFixWithAI(targetDevice.id, checkoutDir.trim(), runner, failure.fixPrompt)
                  .then(({ taskId }) => setFailure({
                    error: `AI fix task ${taskId} started on ${targetDevice.name}.`,
                    remedy: "Follow it in Tasks. When it completes, return here and retry Dogfood mode.",
                  }))
                  .catch((err) => setFailure({
                    error: err instanceof Error ? err.message : String(err),
                    remedy: "Reconnect the primary device and retry Fix with AI.",
                    fixPrompt: failure.fixPrompt,
                  }))
                  .finally(() => setFixing(false));
              }}
              style={{ marginTop: 10, alignSelf: "flex-start", borderWidth: 1, borderColor: c.accent, backgroundColor: c.accentSoft, borderRadius: 8, paddingHorizontal: 11, paddingVertical: 8 }}
            >
              <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>
                {fixing ? "Starting AI fix…" : "Fix with AI"}
              </Text>
            </Pressable>
          ) : null}
        </View>
      ) : null}

      {/* The only non-configuration action appears after all three rows are
          operational. Incomplete setup therefore has no dead primary button. */}
      {gate.canAttach && laneHydrated ? (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Enter Dogfood mode"
          onPress={() => void attach()}
          style={({ pressed }) => ({
            marginTop: 16,
            paddingVertical: 14,
            borderRadius: 14,
            alignItems: "center",
            backgroundColor: c.accent,
            opacity: pressed ? 0.75 : 1,
            flexDirection: "row",
            justifyContent: "center",
            gap: 8,
          })}
        >
          <Ionicons name="play" size={17} color="#fff" />
          <Text style={{ color: "#fff", fontWeight: "700", fontSize: 14 }}>Enter Dogfood mode</Text>
        </Pressable>
      ) : null}

      <View
        accessibilityLabel="Export Yaver browser shortcut"
        style={{ marginTop: 14, gap: 10, borderWidth: 1, borderColor: c.border, borderRadius: 16, backgroundColor: c.bgCard, padding: 14 }}
      >
        <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
          <View style={{ width: 40, height: 40, borderRadius: 12, alignItems: "center", justifyContent: "center", backgroundColor: c.accentSoft }}>
            <Ionicons name="phone-portrait-outline" size={21} color={c.accent} />
          </View>
          <View style={{ flex: 1 }}>
            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "800" }}>Browser shortcut</Text>
            <Text style={{ color: c.textSecondary, fontSize: 11, lineHeight: 16, marginTop: 2 }}>
              Compile Yaver on this box, then install it from your phone browser.
            </Text>
          </View>
        </View>

        <TextInput
          value={shortcutOrigin}
          onChangeText={(value) => {
            setShortcutOrigin(value);
            if (targetDevice?.id) void AsyncStorage.setItem(browserShortcutOriginKey(targetDevice.id), value.trim()).catch(() => {});
          }}
          accessibilityLabel="Custom browser shortcut HTTPS origin"
          placeholder="Automatic isolated Yaver URL"
          placeholderTextColor={c.textMuted}
          keyboardType="url"
          autoCapitalize="none"
          autoCorrect={false}
          spellCheck={false}
          editable={!shortcutBusy}
          style={{ color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg, borderWidth: 1, borderRadius: 9, paddingHorizontal: 10, paddingVertical: 10, fontSize: 12, opacity: shortcutBusy ? 0.65 : 1 }}
        />
        <Text style={{ color: c.textMuted, fontSize: 10, lineHeight: 15 }}>
          Leave blank for a per-app Yaver HTTPS address. A machine IP, bearer token, or shared relay path is never embedded in the shortcut.
        </Text>

        <BrowserShortcutStatusRail
          snapshot={visibleShortcutSnapshot}
          colors={{
            background: c.bg, border: c.border, text: c.textPrimary, muted: c.textMuted,
            accent: c.accent, ready: c.success, blocked: c.error,
          }}
        />

        {shortcutSnapshot.phase === "ready" && shortcutSnapshot.release ? (
          <View style={{ gap: 8 }}>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Open exported Yaver shortcut"
              onPress={() => void Linking.openURL(shortcutSnapshot.release!.installUrl)}
              style={({ pressed }) => ({ borderRadius: 11, backgroundColor: c.accent, paddingVertical: 12, alignItems: "center", opacity: pressed ? 0.75 : 1 })}
            >
              <Text style={{ color: "#fff", fontWeight: "800", fontSize: 13 }}>Open and add to Home Screen</Text>
            </Pressable>
            <Text style={{ color: c.textMuted, fontSize: 10, lineHeight: 15 }}>
              In Safari or Chrome, use Share/Menu → Add to Home Screen. The installed shell can open offline; coding still needs a reachable Yaver machine.
            </Text>
          </View>
        ) : shortcutBusy ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Cancel browser shortcut export"
            onPress={() => {
              shortcutController.current.cancel();
              setShortcutSnapshot({ phase: "idle", progress: 0, message: "Browser shortcut export cancelled." });
            }}
            style={({ pressed }) => ({ borderRadius: 11, borderWidth: 1, borderColor: c.error, paddingVertical: 11, alignItems: "center", opacity: pressed ? 0.7 : 1 })}
          >
            <Text style={{ color: c.error, fontWeight: "700", fontSize: 12 }}>Cancel export</Text>
          </Pressable>
        ) : (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Export Yaver browser shortcut"
            accessibilityState={{ disabled: !shortcutCheckoutReady }}
            disabled={!shortcutCheckoutReady}
            onPress={() => void exportYaverShortcut()}
            style={({ pressed }) => ({ borderRadius: 11, borderWidth: 1, borderColor: c.accent, paddingVertical: 11, alignItems: "center", opacity: !shortcutCheckoutReady ? 0.45 : pressed ? 0.7 : 1 })}
          >
            <Text style={{ color: c.accent, fontWeight: "800", fontSize: 12 }}>Export installable Yaver</Text>
          </Pressable>
        )}
      </View>
      <RunnerAuthModal
        visible={runnerAuthFor !== null}
        runner={runnerAuthFor || "codex"}
        deviceName={targetDevice?.name || "primary device"}
        target={targetDevice?.id}
        onClose={() => setRunnerAuthFor(null)}
        onCompleted={() => {
          setRunnerAuthFor(null);
          if (targetDevice?.id) void loadBoxReadiness(targetDevice.id).then(setMeasuredReadiness);
        }}
      />
      <OpenCodeConfigModal
        visible={showOpenCodeConfig}
        target={targetDevice?.id}
        onClose={() => {
          setShowOpenCodeConfig(false);
          if (targetDevice?.id) void loadBoxReadiness(targetDevice.id).then(setMeasuredReadiness);
        }}
      />
    </View>
  );
}
