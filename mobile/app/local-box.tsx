// local-box.tsx — Settings → "This phone as a Linux box" (Android only).
//
// The one screen that makes the on-device sandbox reachable. It drives the
// YaverSandbox native module (SandboxService.kt) through sandboxControl.ts:
//   1. shows what shipped in the APK (agent binary, proot) + what's installed,
//   2. downloads + verifies the Alpine rootfs (RootfsInstaller),
//   3. starts/stops the foreground agent on 127.0.0.1:18080 and wires it into
//      connectionManager as the "This phone" box.
//
// Each capability gates the next: agent present → proot present → rootfs
// installed → running. We surface exactly which step is missing rather than a
// dead button, because the blockers are real (a build without the jniLibs
// payload, or a rootfs that hasn't been published yet).

import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Switch, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Stack, useRouter } from "expo-router";
import { Ionicons } from "@expo/vector-icons";

import { useColors } from "../src/context/ThemeContext";
import { useAuth } from "../src/context/AuthContext";
import {
  isSandboxSupported,
  sandboxStatus,
  installRootfs,
  onInstallProgress,
  openFactoryResetSettings,
  startHomeHostSandbox,
  startSandbox,
  stopSandbox,
  type SandboxNativeStatus,
} from "../src/lib/sandboxControl";
import { ROOTFS_MANIFEST, ROOTFS_PUBLISHED } from "../src/lib/sandboxRootfsManifest";

type Busy = "idle" | "installing" | "starting" | "hosting" | "stopping";

export default function LocalBoxScreen() {
  const c = useColors();
  const router = useRouter();
  const { token } = useAuth();

  const supported = isSandboxSupported();
  const [status, setStatus] = useState<SandboxNativeStatus | null>(null);
  const [busy, setBusy] = useState<Busy>("idle");
  const [progress, setProgress] = useState<{ phase: string; pct: number } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [resetChecks, setResetChecks] = useState({
    backedUp: false,
    signedOut: false,
    understandsErase: false,
  });

  const refresh = useCallback(async () => {
    setStatus(await sandboxStatus());
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const onInstall = useCallback(async () => {
    setError(null);
    setBusy("installing");
    setProgress({ phase: "starting", pct: 0 });
    const unsub = onInstallProgress((p) => {
      const pct = p.total > 0 ? Math.min(1, p.bytes / p.total) : 0;
      setProgress({ phase: p.phase, pct });
    });
    try {
      const ok = await installRootfs(
        ROOTFS_MANIFEST.url,
        ROOTFS_MANIFEST.sha256,
        ROOTFS_MANIFEST.version,
      );
      if (!ok) setError("Rootfs install failed (sha mismatch or download error). See logcat YaverRootfs.");
      await refresh();
    } catch (e: any) {
      setError(e?.message ?? "Rootfs install failed.");
    } finally {
      unsub();
      setBusy("idle");
      setProgress(null);
    }
  }, [refresh]);

  const onStart = useCallback(async () => {
    if (!token) {
      setError("Sign in first — the on-device agent authenticates as you.");
      return;
    }
    setError(null);
    setBusy("starting");
    try {
      await startSandbox(token);
      await refresh();
    } catch (e: any) {
      setError(e?.message ?? "Failed to start the on-device agent.");
    } finally {
      setBusy("idle");
    }
  }, [token, refresh]);

  const onHomeHostToggle = useCallback(async (enabled: boolean) => {
    if (enabled && !token) {
      setError("Sign in first — the on-device agent authenticates as you.");
      return;
    }
    setError(null);
    setBusy(enabled ? "hosting" : "stopping");
    try {
      if (enabled) {
        await startHomeHostSandbox(token!);
      } else {
        await stopSandbox();
      }
      await refresh();
    } catch (e: any) {
      setError(e?.message ?? (enabled ? "Failed to host on this phone." : "Failed to stop hosting."));
    } finally {
      setBusy("idle");
    }
  }, [token, refresh]);

  const onStop = useCallback(async () => {
    setError(null);
    setBusy("stopping");
    try {
      await stopSandbox();
      await refresh();
    } finally {
      setBusy("idle");
    }
  }, [refresh]);

  const onOpenResetSettings = useCallback(async () => {
    setError(null);
    try {
      await openFactoryResetSettings();
    } catch (e: any) {
      setError(e?.message ?? "Could not open Android reset settings.");
    }
  }, []);

  // ── Unsupported (iOS / web / build without the jniLibs payload) ──────────
  if (!supported) {
    return (
      <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <Stack.Screen options={{ title: "This phone as a box" }} />
        <Pressable
          onPress={() => (router.canGoBack() ? router.back() : router.replace("/(tabs)"))}
          hitSlop={12}
          style={{ flexDirection: "row", alignItems: "center", marginTop: 12, marginHorizontal: 16, alignSelf: "flex-start" }}
          accessibilityRole="button"
          accessibilityLabel="Back"
        >
          <Ionicons name="chevron-back" size={22} color={c.textSecondary} />
          <Text style={{ color: c.textSecondary, fontSize: 16, marginLeft: 2, fontWeight: "600" }}>Back</Text>
        </Pressable>
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, margin: 16 }]}>
          <Text style={[styles.h, { color: c.textPrimary }]}>Not available on this device</Text>
          <Text style={[styles.p, { color: c.textMuted }]}>
            Running the phone itself as a Linux coding box needs the Android
            on-device agent, which only ships in Android builds that include the
            sandbox payload. iOS can&apos;t host an on-device compiler/sandbox —
            pair a machine instead.
          </Text>
        </View>
      </SafeAreaView>
    );
  }

  const agentPresent = !!status?.agentPresent;
  const prootPresent = !!status?.prootPresent;
  const rootfsInstalled = !!status?.rootfsInstalled;
  const running = !!status?.running;
  const homeHostMode = !!status?.homeHostMode;
  const relayOnly = !!status?.relayOnly;
  const batteryPercent = typeof status?.batteryPercent === "number" && status.batteryPercent >= 0
    ? `${status.batteryPercent}%`
    : "unknown";
  const charging = !!status?.charging;

  const canInstall = agentPresent && prootPresent && ROOTFS_PUBLISHED && busy === "idle";
  const canStart = agentPresent && rootfsInstalled && !running && busy === "idle";
  const canToggleHomeHost = agentPresent && busy === "idle";
  const resetReady = resetChecks.backedUp && resetChecks.signedOut && resetChecks.understandsErase;

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <Stack.Screen options={{ title: "This phone as a box" }} />
      <ScrollView contentContainerStyle={{ padding: 16, gap: 12 }}>
        <Pressable
          onPress={() => (router.canGoBack() ? router.back() : router.replace("/(tabs)"))}
          hitSlop={12}
          style={{ flexDirection: "row", alignItems: "center", marginBottom: 12, alignSelf: "flex-start" }}
          accessibilityRole="button"
          accessibilityLabel="Back"
        >
          <Ionicons name="chevron-back" size={22} color={c.textSecondary} />
          <Text style={{ color: c.textSecondary, fontSize: 16, marginLeft: 2, fontWeight: "600" }}>Back</Text>
        </Pressable>
        <Text style={[styles.p, { color: c.textMuted }]}>
          Turn this phone into its own Linux coding box: a proot Alpine userland
          (node · git · ripgrep · hermesc + claude/codex/opencode) hosting the
          Yaver agent on 127.0.0.1. The terminal, runner toggles and Hermes
          reload then drive it exactly like a remote machine — no box needed.
        </Text>

        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <View style={styles.toggleRow}>
            <View style={{ flex: 1 }}>
              <Text style={[styles.h, { color: c.textPrimary }]}>Host my assistant on this phone</Text>
              <Text style={[styles.p, { color: c.textMuted }]}>
                Starts the native agent for your signed-in account with direct listeners bound to loopback.
              </Text>
            </View>
            {busy === "hosting" || busy === "stopping" ? (
              <ActivityIndicator size="small" color={c.accent} />
            ) : (
              <Switch
                value={homeHostMode && running}
                disabled={!canToggleHomeHost}
                onValueChange={onHomeHostToggle}
                trackColor={{ false: c.border, true: c.accent }}
                thumbColor="#fff"
              />
            )}
          </View>
          <Sep c={c} />
          <StepRow c={c} label="Relay-only inbound" ok={homeHostMode && relayOnly}
            hint={homeHostMode && relayOnly ? "direct HTTP/TLS listeners are loopback-only" : "off"} />
          <Sep c={c} />
          <StepRow c={c} label="Power" ok={charging}
            hint={`${batteryPercent}${charging ? " · charging" : " · not charging"}`} />
        </View>

        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.h, { color: c.textPrimary }]}>Prepare this phone for colo</Text>
          <Text style={[styles.p, { color: c.textMuted }]}>
            This path erases the phone, including apps. Use home hosting above when you want to keep apps and data.
          </Text>
          <CheckRow
            c={c}
            checked={resetChecks.backedUp}
            label="Back up anything you need"
            onPress={() => setResetChecks((v) => ({ ...v, backedUp: !v.backedUp }))}
          />
          <CheckRow
            c={c}
            checked={resetChecks.signedOut}
            label="Sign out of Google and app accounts"
            onPress={() => setResetChecks((v) => ({ ...v, signedOut: !v.signedOut }))}
          />
          <CheckRow
            c={c}
            checked={resetChecks.understandsErase}
            label="I understand factory reset erases apps and data"
            onPress={() => setResetChecks((v) => ({ ...v, understandsErase: !v.understandsErase }))}
          />
          <Btn
            c={c}
            label="Open Android reset settings"
            disabled={!resetReady}
            onPress={onOpenResetSettings}
          />
        </View>

        {/* Capability ladder */}
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <StepRow c={c} label="Agent binary" ok={agentPresent}
            hint={agentPresent ? status?.version ? `running ${status.version}` : "shipped" : "not in this build — rebuild with the sandbox payload"} />
          <Sep c={c} />
          <StepRow c={c} label="proot" ok={prootPresent}
            hint={prootPresent ? "shipped" : "not bundled — runners can't be isolated yet"} />
          <Sep c={c} />
          <StepRow c={c} label="Linux rootfs" ok={rootfsInstalled}
            hint={rootfsInstalled ? `installed (${status?.version ?? ROOTFS_MANIFEST.version})` : ROOTFS_PUBLISHED ? "not installed" : "not hosted yet"} />
          <Sep c={c} />
          <StepRow c={c} label="Agent running" ok={running}
            hint={running ? "on 127.0.0.1:18080" : "stopped"} />
        </View>

        {/* Blocker banners */}
        {!agentPresent && (
          <Banner c={c} tone="error"
            text="This build shipped without the on-device agent. Rebuild via scripts/deploy-playstore.sh (it runs build-android-sandbox.sh) and reinstall." />
        )}
        {agentPresent && !prootPresent && (
          <Banner c={c} tone="warn"
            text="proot isn't bundled, so coding runners can't run on-device yet. Rebuild with PROOT_SRC or YAVER_PROOT_URL set so build-android-sandbox.sh includes it." />
        )}
        {agentPresent && prootPresent && !ROOTFS_PUBLISHED && !rootfsInstalled && (
          <Banner c={c} tone="warn"
            text="The Linux rootfs hasn't been published yet. Run scripts/build-android-rootfs-alpine-arm64.sh then scripts/publish-android-rootfs.sh, and flip ROOTFS_PUBLISHED in sandboxRootfsManifest.ts." />
        )}

        {error && <Banner c={c} tone="error" text={error} />}

        {/* Install rootfs */}
        {!rootfsInstalled && (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.h, { color: c.textPrimary }]}>Install Linux rootfs</Text>
            <Text style={[styles.p, { color: c.textMuted }]}>
              Downloads + verifies ~{(ROOTFS_MANIFEST.sizeBytes / 1e6).toFixed(0)} MB once
              ({ROOTFS_MANIFEST.version}). Wi-Fi recommended.
            </Text>
            {busy === "installing" && progress && (
              <View style={{ marginTop: 8 }}>
                <View style={[styles.bar, { backgroundColor: c.borderSubtle }]}>
                  <View style={[styles.barFill, { backgroundColor: c.accent, width: `${Math.round(progress.pct * 100)}%` }]} />
                </View>
                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                  {progress.phase} · {Math.round(progress.pct * 100)}%
                </Text>
              </View>
            )}
            <Btn c={c} label={busy === "installing" ? "Installing…" : "Install"}
              disabled={!canInstall} busy={busy === "installing"} onPress={onInstall} />
          </View>
        )}

        {/* Start / Stop */}
        {rootfsInstalled && (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.h, { color: c.textPrimary }]}>
              {running ? "On-device box is running" : "Start the on-device box"}
            </Text>
            <Text style={[styles.p, { color: c.textMuted }]}>
              {running
                ? "Select \"This phone\" in the box picker to open a terminal or run a coding agent against it."
                : "Starts the foreground agent and registers this phone as a box."}
            </Text>
            {running && !homeHostMode ? (
              <Btn c={c} label={busy === "stopping" ? "Stopping…" : "Stop"} tone="error"
                disabled={busy !== "idle"} busy={busy === "stopping"} onPress={onStop} />
            ) : running && homeHostMode ? (
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 10 }}>
                Turn off home hosting above before starting the coding sandbox mode.
              </Text>
            ) : (
              <Btn c={c} label={busy === "starting" ? "Starting…" : "Start"}
                disabled={!canStart} busy={busy === "starting"} onPress={onStart} />
            )}
          </View>
        )}

        <Pressable onPress={refresh} style={{ alignSelf: "center", padding: 8 }}>
          <Text style={{ color: c.accent, fontSize: 13 }}>Refresh status</Text>
        </Pressable>
      </ScrollView>
    </SafeAreaView>
  );
}

function StepRow({ c, label, ok, hint }: { c: any; label: string; ok: boolean; hint?: string }) {
  return (
    <View style={styles.stepRow}>
      <View style={[styles.dot, { backgroundColor: ok ? c.success : c.borderSubtle, borderColor: ok ? c.success : c.border }]} />
      <View style={{ flex: 1 }}>
        <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>{label}</Text>
        {!!hint && <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 1 }}>{hint}</Text>}
      </View>
      <Text style={{ color: ok ? c.success : c.textMuted, fontSize: 12, fontWeight: "700" }}>{ok ? "OK" : "—"}</Text>
    </View>
  );
}

function Sep({ c }: { c: any }) {
  return <View style={[styles.sep, { backgroundColor: c.borderSubtle }]} />;
}

function CheckRow({ c, checked, label, onPress }: { c: any; checked: boolean; label: string; onPress: () => void }) {
  return (
    <Pressable onPress={onPress} style={styles.checkRow}>
      <View style={[styles.checkBox, {
        borderColor: checked ? c.accent : c.border,
        backgroundColor: checked ? c.accent : "transparent",
      }]}>
        {checked && <Text style={styles.checkMark}>✓</Text>}
      </View>
      <Text style={{ color: c.textPrimary, flex: 1, fontSize: 13 }}>{label}</Text>
    </Pressable>
  );
}

function Banner({ c, tone, text }: { c: any; tone: "error" | "warn"; text: string }) {
  const bg = tone === "error" ? c.errorBg : c.warnBg;
  const bd = tone === "error" ? c.errorBorder : c.warnBorder;
  const fg = tone === "error" ? c.error : c.warn;
  return (
    <View style={[styles.banner, { backgroundColor: bg, borderColor: bd }]}>
      <Text style={{ color: fg, fontSize: 12, lineHeight: 17 }}>{text}</Text>
    </View>
  );
}

function Btn({
  c, label, onPress, disabled, busy, tone,
}: { c: any; label: string; onPress: () => void; disabled?: boolean; busy?: boolean; tone?: "error" }) {
  const bg = disabled ? c.border : tone === "error" ? c.error : c.accent;
  return (
    <Pressable onPress={onPress} disabled={disabled}
      style={[styles.btn, { backgroundColor: bg, opacity: disabled ? 0.6 : 1 }]}>
      {busy && <ActivityIndicator size="small" color="#fff" style={{ marginRight: 8 }} />}
      <Text style={{ color: "#fff", fontWeight: "700", fontSize: 14 }}>{label}</Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  card: { borderWidth: 1, borderRadius: 12, padding: 14 },
  h: { fontSize: 15, fontWeight: "700", marginBottom: 4 },
  p: { fontSize: 12, lineHeight: 18 },
  stepRow: { flexDirection: "row", alignItems: "center", paddingVertical: 8, gap: 10 },
  toggleRow: { flexDirection: "row", alignItems: "center", gap: 12 },
  checkRow: { flexDirection: "row", alignItems: "center", gap: 10, paddingTop: 12 },
  checkBox: { width: 22, height: 22, borderRadius: 5, borderWidth: 1, alignItems: "center", justifyContent: "center" },
  checkMark: { color: "#fff", fontSize: 14, fontWeight: "800", lineHeight: 18 },
  dot: { width: 12, height: 12, borderRadius: 6, borderWidth: 1 },
  sep: { height: StyleSheet.hairlineWidth },
  banner: { borderWidth: 1, borderRadius: 10, padding: 12 },
  bar: { height: 6, borderRadius: 3, overflow: "hidden" },
  barFill: { height: 6, borderRadius: 3 },
  btn: { flexDirection: "row", alignItems: "center", justifyContent: "center", marginTop: 12, paddingVertical: 11, borderRadius: 8 },
});
