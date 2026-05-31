// onboarding-pair.tsx — post-survey "connect your computer" step.
//
// Why this screen exists: prod data (2026-05) showed every net-new organic
// signup completed mobile sign-in + the survey and then never paired a
// device — the activation step. The survey used to drop the user straight
// onto the Tasks tab, where pairing was buried under "Open Mobile Sandbox".
// This screen makes pairing the default next action while staying a SOFT
// nudge: a prominent "Skip for now" always escapes to the phone-only
// sandbox so we never block someone who genuinely wants mobile-only.
//
// It reuses the exact discovery + adopt machinery the Devices tab uses:
//   - beaconListener (LAN UDP 19837) surfaces a fresh `yaver serve` box
//   - adoptBootstrapDevice() pushes this phone's token to it
//   - a slow refreshDevices() poll catches boxes that register via
//     relay/Convex without ever appearing on the local beacon.
//
// Success = the user's Convex device list becomes non-empty (by either
// path). At that point we celebrate and send them to Tasks.

import { router } from "expo-router";
import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import * as Clipboard from "expo-clipboard";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { beaconListener, type DiscoveredDevice } from "../src/lib/beacon";
import { adoptBootstrapDevice } from "../src/lib/pairDevice";

const INSTALL_CMD = "npm install -g yaver-cli && yaver auth";

/** Per-user "they chose to skip pairing onboarding" flag. Checked by the
 *  Tasks/launch routing so we never re-show this screen after a skip. */
export function pairingSkippedKey(userId?: string): string {
  return userId ? `@yaver/u/${userId}/pairing_skipped` : "@yaver/pairing_skipped";
}

export default function OnboardingPairScreen() {
  const { user, token } = useAuth();
  const c = useColors();
  const { devices, refreshDevices } = useDevice();

  const [discovered, setDiscovered] = useState<DiscoveredDevice[]>([]);
  const [adoptingId, setAdoptingId] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  // Once the user's Convex device list is non-empty, pairing succeeded
  // (via LAN adopt, relay claim, or a CLI `yaver auth` that registered
  // directly). This is the single source of truth for success — the
  // beacon list is just a discovery hint.
  const paired = devices.length > 0;

  // --- discovery lifecycle -------------------------------------------------
  useEffect(() => {
    let cancelled = false;
    let unsub: (() => void) | undefined;

    (async () => {
      if (!user?.id) return;
      try {
        await beaconListener.setUserId(user.id);
        if (cancelled) return;
        // Seed the known-device whitelist so already-owned boxes match
        // the fingerprint path (bootstrap boxes bypass it anyway).
        beaconListener.setKnownDevices(devices.map((d) => d.id));
        unsub = beaconListener.onDiscovered((dev) => {
          setDiscovered((prev) => {
            const next = prev.filter((p) => p.deviceId !== dev.deviceId);
            next.push(dev);
            return next;
          });
        });
        // Surface anything already buffered before our listener attached.
        setDiscovered(beaconListener.getDevices());
        beaconListener.start();
      } catch {
        // Discovery is best-effort; the copyable command + poll still work.
      }
    })();

    return () => {
      cancelled = true;
      if (unsub) unsub();
      beaconListener.stop();
    };
    // user.id is stable for the screen's lifetime; devices intentionally
    // excluded so we don't tear down/rebuild the socket on every poll.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [user?.id]);

  // Slow poll so a box that registers via relay/Convex (never appearing on
  // the LAN beacon) still flips us into the success state.
  useEffect(() => {
    if (paired) return;
    const t = setInterval(() => {
      refreshDevices().catch(() => {});
    }, 5000);
    return () => clearInterval(t);
  }, [paired, refreshDevices]);

  // Keep the beacon's whitelist in sync as devices arrive.
  useEffect(() => {
    beaconListener.setKnownDevices(devices.map((d) => d.id));
  }, [devices]);

  const copyCmd = useCallback(async () => {
    try {
      await Clipboard.setStringAsync(INSTALL_CMD);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      // expo-clipboard rejects on web; ignore.
    }
  }, []);

  const handleAdopt = useCallback(
    async (dev: DiscoveredDevice) => {
      if (!token) {
        Alert.alert("Not signed in", "Sign into the Yaver app first, then try again.");
        return;
      }
      setAdoptingId(dev.deviceId);
      try {
        const res = await adoptBootstrapDevice(dev, token, user?.id);
        if (res.ok) {
          Alert.alert(
            "Connected",
            `Signed ${user?.email ?? "your account"} into ${res.host ?? dev.name ?? "the machine"}. It'll appear as online shortly.`,
          );
          setTimeout(() => refreshDevices().catch(() => {}), 2500);
          return;
        }
        if (res.needsManualPasskey) {
          Alert.alert(
            "Type the passkey",
            "This box hid its passkey. Open More → Pair a device and enter the 6-character code shown on the machine.",
          );
          return;
        }
        Alert.alert("Couldn't connect", res.error ?? "The machine rejected the request.");
      } finally {
        setAdoptingId(null);
      }
    },
    [token, user, refreshDevices],
  );

  const goToTasks = useCallback(() => {
    router.replace("/(tabs)/tasks");
  }, []);

  const skipForNow = useCallback(async () => {
    try {
      await AsyncStorage.setItem(pairingSkippedKey(user?.id), "1");
    } catch {
      // Non-fatal — worst case the Tasks empty-state nudges again.
    }
    router.replace("/(tabs)/tasks");
  }, [user?.id]);

  const bootstrapDevices = discovered.filter((d) => d.needsAuth);

  // --- success state -------------------------------------------------------
  if (paired) {
    const first = devices[0];
    return (
      <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
        <View style={styles.centered}>
          <View style={[styles.checkCircle, { backgroundColor: c.success + "22", borderColor: c.success }]}>
            <Text style={[styles.checkMark, { color: c.success }]}>✓</Text>
          </View>
          <Text style={[styles.title, { color: c.textPrimary }]}>Your computer is connected</Text>
          <Text style={[styles.subtitle, { color: c.textSecondary }]}>
            {first?.name ? `${first.name} is paired with your account.` : "Your machine is paired with your account."}
          </Text>
          <Pressable
            style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.8 }]}
            onPress={goToTasks}
          >
            <Text style={[styles.primaryBtnText, { color: "#fff" }]}>Start coding</Text>
          </Pressable>
        </View>
      </SafeAreaView>
    );
  }

  // --- waiting / discovery state ------------------------------------------
  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
      <ScrollView contentContainerStyle={styles.content} showsVerticalScrollIndicator={false}>
        <Text style={[styles.title, { color: c.textPrimary }]}>Connect your computer</Text>
        <Text style={[styles.subtitle, { color: c.textSecondary }]}>
          Yaver runs your AI coding agent on your own machine and drives it from this phone.
          One command links them.
        </Text>

        {/* Step 1 — install + sign in (copyable) */}
        <View style={[styles.cmdCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.cmdLabel, { color: c.textMuted }]}>RUN THIS ON YOUR COMPUTER</Text>
          <Text style={[styles.cmdText, { color: c.textPrimary }]} selectable>
            {INSTALL_CMD}
          </Text>
          <Pressable
            style={({ pressed }) => [styles.copyBtn, { borderColor: c.border }, pressed && { opacity: 0.7 }]}
            onPress={copyCmd}
          >
            <Text style={[styles.copyBtnText, { color: copied ? c.success : c.accent }]}>
              {copied ? "Copied ✓" : "Copy command"}
            </Text>
          </Pressable>
          <Text style={[styles.cmdHelper, { color: c.textMuted }]}>
            Keeps running after sign-in (it auto-starts on login). Prefer the foreground? Run{" "}
            <Text style={{ color: c.textSecondary }}>yaver serve</Text>.
          </Text>
        </View>

        {/* Live discovery */}
        {bootstrapDevices.length > 0 ? (
          <View style={styles.discoverBlock}>
            <Text style={[styles.sectionLabel, { color: c.textSecondary }]}>
              FOUND ON YOUR NETWORK
            </Text>
            {bootstrapDevices.map((dev) => {
              const busy = adoptingId === dev.deviceId;
              return (
                <Pressable
                  key={dev.deviceId}
                  style={({ pressed }) => [
                    styles.deviceRow,
                    { backgroundColor: c.bgCard, borderColor: c.accent },
                    pressed && { opacity: 0.8 },
                  ]}
                  onPress={() => !busy && handleAdopt(dev)}
                  disabled={busy}
                >
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.deviceName, { color: c.textPrimary }]}>{dev.name}</Text>
                    <Text style={[styles.deviceMeta, { color: c.textMuted }]}>
                      {dev.ip}:{dev.port} — tap to connect
                    </Text>
                  </View>
                  {busy ? (
                    <ActivityIndicator size="small" color={c.accent} />
                  ) : (
                    <Text style={[styles.connectWord, { color: c.accent }]}>Connect</Text>
                  )}
                </Pressable>
              );
            })}
          </View>
        ) : (
          <View style={styles.waitingRow}>
            <ActivityIndicator size="small" color={c.textMuted} />
            <Text style={[styles.waitingText, { color: c.textMuted }]}>
              Waiting for your computer…
            </Text>
          </View>
        )}
      </ScrollView>

      {/* Soft escape — always available */}
      <View style={[styles.footer, { borderTopColor: c.border }]}>
        <Pressable
          style={({ pressed }) => [styles.skipBtn, pressed && { opacity: 0.6 }]}
          onPress={skipForNow}
        >
          <Text style={[styles.skipText, { color: c.textSecondary }]}>Skip for now</Text>
        </Pressable>
        <Text style={[styles.skipHelper, { color: c.textMuted }]}>
          You can build from this phone with the Mobile Sandbox and pair a computer anytime.
        </Text>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  centered: { flex: 1, alignItems: "center", justifyContent: "center", paddingHorizontal: 32 },
  content: { paddingHorizontal: 24, paddingTop: 40, paddingBottom: 24 },
  title: { fontSize: 24, fontWeight: "700", textAlign: "center", letterSpacing: -0.4, marginBottom: 10 },
  subtitle: { fontSize: 15, lineHeight: 21, textAlign: "center", marginBottom: 28 },
  cmdCard: { borderWidth: 1, borderRadius: 16, padding: 18, marginBottom: 24 },
  cmdLabel: { fontSize: 11, fontWeight: "700", letterSpacing: 1, marginBottom: 10 },
  cmdText: { fontSize: 14, fontFamily: "Courier", fontWeight: "600", marginBottom: 14 },
  copyBtn: { borderWidth: 1, borderRadius: 10, paddingVertical: 11, alignItems: "center" },
  copyBtnText: { fontSize: 14, fontWeight: "600" },
  cmdHelper: { fontSize: 12, lineHeight: 17, marginTop: 14 },
  discoverBlock: { marginBottom: 8 },
  sectionLabel: { fontSize: 11, fontWeight: "700", letterSpacing: 1, marginBottom: 10 },
  deviceRow: {
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderRadius: 14,
    paddingVertical: 16,
    paddingHorizontal: 16,
    marginBottom: 10,
  },
  deviceName: { fontSize: 15, fontWeight: "600" },
  deviceMeta: { fontSize: 12, marginTop: 3 },
  connectWord: { fontSize: 14, fontWeight: "700" },
  waitingRow: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 10, paddingVertical: 18 },
  waitingText: { fontSize: 14 },
  footer: { borderTopWidth: 1, paddingHorizontal: 24, paddingTop: 16, paddingBottom: 8 },
  skipBtn: { alignItems: "center", paddingVertical: 6 },
  skipText: { fontSize: 15, fontWeight: "600" },
  skipHelper: { fontSize: 12, lineHeight: 17, textAlign: "center", marginTop: 6 },
  checkCircle: {
    width: 72,
    height: 72,
    borderRadius: 36,
    borderWidth: 2,
    alignItems: "center",
    justifyContent: "center",
    marginBottom: 24,
  },
  checkMark: { fontSize: 36, fontWeight: "700" },
  primaryBtn: {
    width: "100%",
    paddingVertical: 15,
    borderRadius: 12,
    alignItems: "center",
    justifyContent: "center",
    marginTop: 28,
  },
  primaryBtnText: { fontSize: 16, fontWeight: "700" },
});
