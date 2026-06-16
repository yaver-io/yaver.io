import { Ionicons } from "@expo/vector-icons";
import { CameraView, useCameraPermissions, type BarcodeScanningResult } from "expo-camera";
import * as Clipboard from "expo-clipboard";
import * as Linking from "expo-linking";
import { useRouter } from "expo-router";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useDevice } from "../src/context/DeviceContext";
import { useColors } from "../src/context/ThemeContext";
import { addEVEvent, approveEV, makeEVIntent, setEVRoute, setEVState } from "../src/lib/evCharging/intent";
import { EV_PROVIDERS, buildEVRouteOptions, providerForIntent, providerLabel, parseEVManualInput, parseEVQr } from "../src/lib/evCharging/providers";
import type { EVChargingIntent, EVProviderId, EVRouteKind } from "../src/lib/evCharging/types";
import { quicClient } from "../src/lib/quic";

function EVScanner({
  onScanned,
  onClose,
}: {
  onScanned: (raw: string) => void;
  onClose: () => void;
}) {
  const c = useColors();
  const [permission, requestPermission] = useCameraPermissions();
  const handledRef = useRef(false);

  const onBarcode = useCallback((result: BarcodeScanningResult) => {
    if (handledRef.current) return;
    const raw = result?.data?.trim();
    if (!raw) return;
    handledRef.current = true;
    onScanned(raw);
  }, [onScanned]);

  if (!permission) {
    return (
      <View style={[styles.fill, styles.center, { backgroundColor: c.bg }]}>
        <Text style={{ color: c.textMuted }}>Preparing camera...</Text>
      </View>
    );
  }

  if (!permission.granted) {
    return (
      <View style={[styles.fill, styles.center, { backgroundColor: c.bg, padding: 28 }]}>
        <Text style={[styles.scanTitle, { color: c.textPrimary }]}>Scan charger QR</Text>
        <Text style={[styles.scanBody, { color: c.textSecondary }]}>
          Yaver reads the QR locally, identifies the charging app, and waits for your approval before any provider handoff.
        </Text>
        <Pressable
          onPress={() => void requestPermission()}
          style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
        >
          <Text style={styles.primaryBtnText}>Allow camera</Text>
        </Pressable>
        <Pressable onPress={onClose} style={({ pressed }) => [styles.linkBtn, pressed && { opacity: 0.65 }]}>
          <Text style={[styles.linkText, { color: c.textSecondary }]}>Cancel</Text>
        </Pressable>
      </View>
    );
  }

  return (
    <View style={[styles.fill, { backgroundColor: "#000" }]}>
      <CameraView
        style={StyleSheet.absoluteFill}
        facing="back"
        barcodeScannerSettings={{ barcodeTypes: ["qr"] }}
        onBarcodeScanned={onBarcode}
      />
      <View style={styles.scannerOverlay} pointerEvents="box-none">
        <View style={styles.scannerHeader}>
          <Pressable onPress={onClose} style={styles.closeCircle}>
            <Ionicons name="close" size={26} color="#fff" />
          </Pressable>
        </View>
        <View style={styles.scannerCopy}>
          <Text style={styles.scannerTitle}>Charger QR</Text>
          <Text style={styles.scannerSubtitle}>Point at the socket QR. Yaver will classify it first.</Text>
        </View>
        <View style={styles.reticle} />
      </View>
    </View>
  );
}

export default function EVChargingScreen() {
  const c = useColors();
  const router = useRouter();
  const { activeDevice, connectionStatus } = useDevice();
  const connected = Boolean(activeDevice && connectionStatus === "connected");

  const [scannerOpen, setScannerOpen] = useState(false);
  const [rawInput, setRawInput] = useState("");
  const [intent, setIntent] = useState<EVChargingIntent | null>(null);
  const [selectedRoute, setSelectedRoute] = useState<EVRouteKind | null>(null);
  const [manualProvider, setManualProvider] = useState<EVProviderId>("unknown");
  const [packageHint, setPackageHint] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [chargingStartedAt, setChargingStartedAt] = useState<number | null>(null);
  const [clock, setClock] = useState(Date.now());

  const provider = intent ? providerForIntent(intent) : null;
  const manualCode = intent?.chargerId || intent?.connectorId || intent?.stationId || intent?.socketLabel || intent?.rawQr || "";
  const runnerBrief = useMemo(() => {
    if (!intent) return "";
    return [
      `Provider: ${providerLabel(intent.provider)}`,
      `Android package hint: ${packageHint.trim() || provider?.androidPackageHints[0] || "unknown"}`,
      `Manual charger code: ${manualCode || "unknown"}`,
      intent.normalizedUrl ? `QR/link payload: ${intent.normalizedUrl}` : `Raw payload: ${intent.rawQr || manualCode || "unknown"}`,
      "Task: launch the provider app on Redroid and use manual station/socket-code entry if camera scan is unavailable.",
      "Stop at login, SMS/OTP, payment/card, and final start/stop confirmation. Ask the user; do not store secrets or codes.",
    ].join("\n");
  }, [intent, manualCode, packageHint, provider]);
  const routeOptions = useMemo(() => {
    if (!intent) return [];
    return buildEVRouteOptions(intent, {
      hasActiveYaverDevice: connected,
      hasRemoteAndroid: connected,
      hasProviderUrl: Boolean(intent.normalizedUrl),
    });
  }, [connected, intent]);

  useEffect(() => {
    if (!chargingStartedAt) return undefined;
    const id = setInterval(() => setClock(Date.now()), 1000);
    return () => clearInterval(id);
  }, [chargingStartedAt]);

  const elapsedLabel = useMemo(() => {
    if (!chargingStartedAt) return "00:00";
    const sec = Math.max(0, Math.floor((clock - chargingStartedAt) / 1000));
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = sec % 60;
    return h > 0
      ? `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`
      : `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  }, [chargingStartedAt, clock]);

  const classify = useCallback((raw: string) => {
    const parsed = parseEVQr(raw);
    if (!parsed) {
      Alert.alert("QR not readable", "Yaver could not read a charger code from that value.");
      return;
    }
    const next = makeEVIntent(raw, parsed);
    const adapter = providerForIntent(next);
    setIntent(next);
    setSelectedRoute(null);
    setChargingStartedAt(null);
    setPackageHint(adapter.androidPackageHints[0] ?? "");
    setScannerOpen(false);
  }, []);

  const classifyManual = useCallback(() => {
    const parsed = parseEVManualInput(rawInput, manualProvider);
    if (!parsed) {
      Alert.alert("Code needed", "Paste or type the QR payload, provider link, station code, socket code, or charger code.");
      return;
    }
    const next = makeEVIntent(rawInput.trim(), parsed);
    const adapter = providerForIntent(next);
    setIntent(addEVEvent(next, "manual_code_entered", "Manual QR/station code entered for provider handoff.", {
      provider: next.provider,
      code: next.chargerId || next.connectorId || next.stationId || next.socketLabel,
    }));
    setSelectedRoute(null);
    setChargingStartedAt(null);
    setPackageHint(adapter.androidPackageHints[0] ?? "");
    setScannerOpen(false);
  }, [manualProvider, rawInput]);

  const pickRoute = useCallback((kind: EVRouteKind) => {
    if (!intent) return;
    setSelectedRoute(kind);
    setIntent(setEVRoute(intent, kind));
  }, [intent]);

  const openProviderLink = useCallback(async () => {
    if (!intent?.normalizedUrl) return;
    setBusy("link");
    try {
      const ok = await Linking.canOpenURL(intent.normalizedUrl);
      if (!ok) {
        Alert.alert("Cannot open link", "This phone does not know how to open that provider URL.");
        return;
      }
      setIntent((cur) => cur ? addEVEvent(cur, "provider_deeplink", "Opened provider URL on this phone.") : cur);
      await Linking.openURL(intent.normalizedUrl);
    } catch (e) {
      Alert.alert("Could not open provider app", e instanceof Error ? e.message : "Unknown error");
    } finally {
      setBusy(null);
    }
  }, [intent]);

  const launchRemoteAndroid = useCallback(async () => {
    if (!activeDevice || !intent) return;
    const hint = packageHint.trim();
    if (!hint) {
      Alert.alert("Package hint needed", "Enter a package search hint, for example esarj, zes, trugo, or an exact Android package id.");
      return;
    }
    setBusy("android");
    try {
      const status = await quicClient.agentRequest(activeDevice.id, "/droid/status");
      const body = status.ok ? await status.json() : null;
      if (!body?.device) {
        Alert.alert("No Android device", "The selected Yaver machine does not report an attached Android device.");
        setIntent((cur) => cur ? addEVEvent(cur, "remote_android_unavailable", "No attached Android device was reported.") : cur);
        return;
      }
      const res = await quicClient.agentRequest(activeDevice.id, "/droid/launch", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ package: hint }),
      });
      const launch = res.ok ? await res.json() : null;
      if (!res.ok) {
        throw new Error(launch?.error || `HTTP ${res.status}`);
      }
      setIntent((cur) => cur ? addEVEvent(cur, "remote_android_launch", `Launched ${providerLabel(intent.provider)} on remote Android.`, { package: launch?.package || hint }) : cur);
      Alert.alert(
        "Remote Android opened",
        manualCode
          ? `Yaver opened the provider app. If camera scan is unavailable in Redroid, use the app's manual station/socket-code entry: ${manualCode}. Login, SMS, payment, and final start still need your visible approval.`
          : "Yaver opened the provider app. Login, SMS, payment, and final start still need your visible approval.",
        [{ text: "View Android", onPress: () => router.push("/droid-control" as any) }],
      );
    } catch (e) {
      Alert.alert("Could not launch app", e instanceof Error ? e.message : "Unknown error");
    } finally {
      setBusy(null);
    }
  }, [activeDevice, intent, manualCode, packageHint, router]);

  const copyRunnerBrief = useCallback(async () => {
    if (!runnerBrief) return;
    await Clipboard.setStringAsync(runnerBrief);
    setIntent((cur) => cur ? addEVEvent(cur, "runner_brief_copied", "Copied Redroid manual-code runner brief.") : cur);
  }, [runnerBrief]);

  const recordApproval = useCallback((kind: "login" | "otp" | "payment" | "start" | "stop") => {
    if (!intent) return;
    const labels: Record<typeof kind, string> = {
      login: "User approved provider login step.",
      otp: "User handled SMS/OTP in provider UI.",
      payment: "User approved provider payment/card step.",
      start: "User reached final start confirmation. Yaver did not start automatically.",
      stop: "User reached stop confirmation. Yaver did not stop automatically.",
    };
    setIntent(approveEV(intent, kind, labels[kind]));
  }, [intent]);

  const markCharging = useCallback(() => {
    if (!intent) return;
    const next = approveEV(intent, "start", "User confirmed charging started in provider UI.");
    setIntent(setEVState(next, "charging", "Charging marked active from Yaver."));
    setChargingStartedAt(Date.now());
  }, [intent]);

  const markStopped = useCallback(() => {
    if (!intent) return;
    const next = approveEV(intent, "stop", "User confirmed charging stopped in provider UI.");
    setIntent(setEVState(next, "complete", "Charging session marked complete from Yaver."));
    setChargingStartedAt(null);
  }, [intent]);

  if (scannerOpen) {
    return <EVScanner onScanned={classify} onClose={() => setScannerOpen(false)} />;
  }

  return (
    <SafeAreaView style={[styles.fill, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <AppScreenHeader title="EV Charging" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
        <View style={[styles.hero, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <View style={[styles.heroIcon, { backgroundColor: c.accent + "18" }]}>
            <Ionicons name="flash" size={28} color={c.accent} />
          </View>
          <View style={{ flex: 1 }}>
            <Text style={[styles.heroTitle, { color: c.textPrimary }]}>Scan or type, approve later</Text>
            <Text style={[styles.heroText, { color: c.textSecondary }]}>
              Yaver identifies the charger and supervises the real provider app. Redroid can use manual station codes when camera scan is unavailable.
            </Text>
          </View>
        </View>

        <View style={styles.actionsRow}>
          <Pressable
            onPress={() => setScannerOpen(true)}
            style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent, flex: 1 }, pressed && { opacity: 0.85 }]}
          >
            <Ionicons name="qr-code" size={18} color="#000" />
            <Text style={styles.primaryBtnText}>Scan QR</Text>
          </Pressable>
          <Pressable
            onPress={classifyManual}
            disabled={!rawInput.trim()}
            style={({ pressed }) => [
              styles.secondaryBtn,
              { borderColor: c.border, backgroundColor: c.bgCard, opacity: rawInput.trim() ? 1 : 0.45 },
              pressed && { opacity: 0.75 },
            ]}
          >
            <Text style={[styles.secondaryBtnText, { color: c.textPrimary }]}>Use code</Text>
          </Pressable>
        </View>

        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Manual QR / station code</Text>
          <Text style={[styles.routeDescription, { color: c.textMuted, marginTop: 6 }]}>
            Use this when Redroid cannot scan with a camera. Pick the provider if the code itself does not identify one.
          </Text>
          <View style={styles.providerGrid}>
            {EV_PROVIDERS.filter((p) => p.id !== "sarjtr").map((p) => {
              const selected = manualProvider === p.id;
              return (
                <Pressable
                  key={p.id}
                  onPress={() => setManualProvider(p.id)}
                  style={[styles.providerChip, {
                    borderColor: selected ? c.accent : c.border,
                    backgroundColor: selected ? c.accent + "18" : c.bg,
                  }]}
                >
                  <Text style={{ color: selected ? c.accent : c.textPrimary, fontSize: 12, fontWeight: "700" }}>{p.label}</Text>
                </Pressable>
              );
            })}
          </View>
        </View>

        <TextInput
          value={rawInput}
          onChangeText={setRawInput}
          placeholder="Paste QR payload, provider link, station code, socket code, or charger code"
          placeholderTextColor={c.textMuted}
          autoCapitalize="none"
          autoCorrect={false}
          multiline
          style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgCard }]}
        />

        {intent ? (
          <>
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <View style={styles.cardHeader}>
                <Text style={[styles.cardTitle, { color: c.textPrimary }]}>{providerLabel(intent.provider)}</Text>
                <Text style={[styles.badge, { color: c.accent, backgroundColor: c.accent + "18" }]}>{intent.state}</Text>
              </View>
              <InfoRow label="Station" value={intent.stationId || "-"} color={c.textMuted} valueColor={c.textPrimary} />
              <InfoRow label="Connector" value={intent.connectorId || intent.socketLabel || "-"} color={c.textMuted} valueColor={c.textPrimary} />
              <InfoRow label="Charger" value={intent.chargerId || "-"} color={c.textMuted} valueColor={c.textPrimary} />
              {intent.normalizedUrl ? (
                <Text style={[styles.urlText, { color: c.textMuted }]} numberOfLines={2}>{intent.normalizedUrl}</Text>
              ) : null}
            </View>

            <View style={[styles.sessionCard, { backgroundColor: intent.state === "charging" ? c.accent + "18" : c.bgCard, borderColor: intent.state === "charging" ? c.accent : c.border }]}>
              <View style={{ flex: 1 }}>
                <Text style={[styles.sessionLabel, { color: c.textMuted }]}>Session</Text>
                <Text style={[styles.sessionStatus, { color: c.textPrimary }]}>
                  {intent.state === "charging" ? "Charging" : intent.state === "complete" ? "Complete" : "Not charging"}
                </Text>
              </View>
              <Text style={[styles.timerText, { color: intent.state === "charging" ? c.accent : c.textMuted }]}>
                {elapsedLabel}
              </Text>
            </View>

            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Route</Text>
              <View style={{ gap: 8, marginTop: 10 }}>
                {routeOptions.map((option) => {
                  const selected = selectedRoute === option.kind || intent.route === option.kind;
                  return (
                    <Pressable
                      key={option.kind}
                      disabled={!option.available}
                      onPress={() => pickRoute(option.kind)}
                      style={[
                        styles.routeRow,
                        {
                          borderColor: selected ? c.accent : c.border,
                          backgroundColor: selected ? c.accent + "14" : c.bg,
                          opacity: option.available ? 1 : 0.5,
                        },
                      ]}
                    >
                      <View style={{ flex: 1 }}>
                        <Text style={[styles.routeLabel, { color: c.textPrimary }]}>{option.label}</Text>
                        <Text style={[styles.routeDescription, { color: c.textMuted }]}>
                          {option.available ? option.description : option.unavailableReason}
                        </Text>
                      </View>
                      <Ionicons name={selected ? "radio-button-on" : "radio-button-off"} size={20} color={selected ? c.accent : c.textMuted} />
                    </Pressable>
                  );
                })}
              </View>
            </View>

            {(selectedRoute === "provider_deeplink" || intent.route === "provider_deeplink") && (
              <Pressable
                onPress={openProviderLink}
                disabled={busy === "link" || !intent.normalizedUrl}
                style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
              >
                {busy === "link" ? <ActivityIndicator color="#000" /> : <Ionicons name="open-outline" size={18} color="#000" />}
                <Text style={styles.primaryBtnText}>Open provider app</Text>
              </Pressable>
            )}

            {(selectedRoute === "remote_android" || intent.route === "remote_android") && (
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Remote Android</Text>
                <Text style={[styles.routeDescription, { color: c.textMuted, marginTop: 6 }]}>
                  Launches the real provider app on the selected Yaver machine. Use manual station/socket entry if provider camera scan is unavailable. You still approve SMS, payment, start, and stop.
                </Text>
                <TextInput
                  value={packageHint}
                  onChangeText={setPackageHint}
                  placeholder="Package hint"
                  placeholderTextColor={c.textMuted}
                  autoCapitalize="none"
                  autoCorrect={false}
                  style={[styles.singleInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
                />
                <Pressable
                  onPress={launchRemoteAndroid}
                  disabled={busy === "android" || !connected}
                  style={({ pressed }) => [
                    styles.primaryBtn,
                    { backgroundColor: c.accent, opacity: connected ? 1 : 0.45, marginTop: 10 },
                    pressed && { opacity: 0.85 },
                  ]}
                >
                  {busy === "android" ? <ActivityIndicator color="#000" /> : <Ionicons name="logo-android" size={18} color="#000" />}
                  <Text style={styles.primaryBtnText}>Launch on Android</Text>
                </Pressable>
                {runnerBrief ? (
                  <Pressable
                    onPress={copyRunnerBrief}
                    style={({ pressed }) => [styles.secondaryBtn, { borderColor: c.border, backgroundColor: c.bg, marginTop: 10 }, pressed && { opacity: 0.75 }]}
                  >
                    <Ionicons name="clipboard-outline" size={18} color={c.textPrimary} />
                    <Text style={[styles.secondaryBtnText, { color: c.textPrimary }]}>Copy runner brief</Text>
                  </Pressable>
                ) : null}
              </View>
            )}

            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Approval notes</Text>
              <View style={styles.approvalGrid}>
                {(["login", "otp", "payment", "start", "stop"] as const).map((kind) => (
                  <Pressable
                    key={kind}
                    onPress={() => recordApproval(kind)}
                    style={[styles.approvalBtn, { borderColor: c.border, backgroundColor: c.bg }]}
                  >
                    <Text style={[styles.approvalText, { color: c.textPrimary }]}>{kind.toUpperCase()}</Text>
                  </Pressable>
                ))}
              </View>
            </View>

            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Charging state</Text>
              <Text style={[styles.routeDescription, { color: c.textMuted, marginTop: 6 }]}>
                Use these after the provider app confirms the session. Yaver records state only; it does not command the charger.
              </Text>
              <View style={styles.stateActions}>
                <Pressable
                  onPress={markCharging}
                  disabled={intent.state === "charging"}
                  style={({ pressed }) => [
                    styles.primaryBtn,
                    { backgroundColor: c.accent, flex: 1, opacity: intent.state === "charging" ? 0.45 : 1 },
                    pressed && { opacity: 0.85 },
                  ]}
                >
                  <Ionicons name="flash" size={18} color="#000" />
                  <Text style={styles.primaryBtnText}>Mark charging</Text>
                </Pressable>
                <Pressable
                  onPress={markStopped}
                  disabled={intent.state !== "charging"}
                  style={({ pressed }) => [
                    styles.secondaryBtn,
                    { borderColor: c.border, backgroundColor: c.bg, flex: 1, opacity: intent.state === "charging" ? 1 : 0.45 },
                    pressed && { opacity: 0.75 },
                  ]}
                >
                  <Text style={[styles.secondaryBtnText, { color: c.textPrimary }]}>Mark stopped</Text>
                </Pressable>
              </View>
            </View>

            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Event log</Text>
              {intent.events.slice().reverse().map((event, idx) => (
                <View key={`${event.at}-${idx}`} style={[styles.eventRow, { borderBottomColor: c.borderSubtle }]}>
                  <Text style={[styles.eventType, { color: c.accent }]}>{event.type}</Text>
                  <Text style={[styles.eventText, { color: c.textSecondary }]}>{event.message}</Text>
                </View>
              ))}
            </View>
          </>
        ) : (
          <View style={[styles.empty, { borderColor: c.border }]}>
            <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>No charger selected</Text>
            <Text style={[styles.emptyText, { color: c.textMuted }]}>
              Scan a ZES, Esarj, Trugo, En Yakıt, Voltrun, Sharz, Şarj@TR, or other charger QR. Unknown providers still get manual assist.
            </Text>
          </View>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

function InfoRow({ label, value, color, valueColor }: { label: string; value: string; color: string; valueColor: string }) {
  return (
    <View style={styles.infoRow}>
      <Text style={[styles.infoLabel, { color }]}>{label}</Text>
      <Text style={[styles.infoValue, { color: valueColor }]} numberOfLines={1}>{value}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  fill: { flex: 1 },
  center: { alignItems: "center", justifyContent: "center" },
  content: { padding: 16, gap: 14, paddingBottom: 36 },
  hero: { flexDirection: "row", gap: 12, borderWidth: 1, borderRadius: 8, padding: 14 },
  heroIcon: { width: 48, height: 48, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  heroTitle: { fontSize: 18, fontWeight: "700" },
  heroText: { fontSize: 13, lineHeight: 19, marginTop: 4 },
  actionsRow: { flexDirection: "row", gap: 10 },
  primaryBtn: { minHeight: 48, borderRadius: 8, paddingHorizontal: 16, alignItems: "center", justifyContent: "center", flexDirection: "row", gap: 8 },
  primaryBtnText: { color: "#000", fontSize: 15, fontWeight: "700" },
  secondaryBtn: { minHeight: 48, borderRadius: 8, paddingHorizontal: 16, alignItems: "center", justifyContent: "center", borderWidth: 1 },
  secondaryBtnText: { fontSize: 15, fontWeight: "700" },
  linkBtn: { paddingVertical: 10, paddingHorizontal: 12 },
  linkText: { fontSize: 14, fontWeight: "600" },
  input: { borderWidth: 1, borderRadius: 8, minHeight: 84, padding: 12, fontSize: 14, textAlignVertical: "top" },
  singleInput: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 12, paddingVertical: 10, fontSize: 14, marginTop: 12 },
  card: { borderWidth: 1, borderRadius: 8, padding: 14 },
  providerGrid: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 12 },
  providerChip: { borderWidth: 1, borderRadius: 999, paddingHorizontal: 10, paddingVertical: 8 },
  cardHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 10, marginBottom: 8 },
  cardTitle: { fontSize: 16, fontWeight: "700" },
  badge: { overflow: "hidden", borderRadius: 999, paddingHorizontal: 10, paddingVertical: 4, fontSize: 11, fontWeight: "700" },
  infoRow: { flexDirection: "row", justifyContent: "space-between", gap: 10, paddingVertical: 5 },
  infoLabel: { fontSize: 12, fontWeight: "600", textTransform: "uppercase" },
  infoValue: { fontSize: 13, fontWeight: "600", flex: 1, textAlign: "right" },
  urlText: { fontSize: 12, marginTop: 8 },
  sessionCard: { borderWidth: 1, borderRadius: 8, padding: 14, flexDirection: "row", alignItems: "center", gap: 12 },
  sessionLabel: { fontSize: 11, fontWeight: "700", textTransform: "uppercase" },
  sessionStatus: { fontSize: 20, fontWeight: "800", marginTop: 2 },
  timerText: { fontSize: 24, fontWeight: "800", fontVariant: ["tabular-nums"] },
  routeRow: { borderWidth: 1, borderRadius: 8, padding: 12, flexDirection: "row", gap: 10, alignItems: "center" },
  routeLabel: { fontSize: 14, fontWeight: "700" },
  routeDescription: { fontSize: 12, lineHeight: 17, marginTop: 3 },
  approvalGrid: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 10 },
  stateActions: { flexDirection: "row", gap: 10, marginTop: 12 },
  approvalBtn: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 12, paddingVertical: 10 },
  approvalText: { fontSize: 12, fontWeight: "700" },
  eventRow: { borderBottomWidth: StyleSheet.hairlineWidth, paddingVertical: 8 },
  eventType: { fontSize: 11, fontWeight: "700", textTransform: "uppercase" },
  eventText: { fontSize: 12, marginTop: 2 },
  empty: { borderWidth: 1, borderStyle: "dashed", borderRadius: 8, padding: 24, alignItems: "center" },
  emptyTitle: { fontSize: 16, fontWeight: "700" },
  emptyText: { fontSize: 13, lineHeight: 19, textAlign: "center", marginTop: 6 },
  scanTitle: { fontSize: 22, fontWeight: "700", textAlign: "center", marginBottom: 10 },
  scanBody: { fontSize: 14, lineHeight: 20, textAlign: "center", marginBottom: 22 },
  scannerOverlay: { ...StyleSheet.absoluteFillObject, alignItems: "center", justifyContent: "center" },
  scannerHeader: { position: "absolute", top: 52, right: 24 },
  closeCircle: { width: 56, height: 56, borderRadius: 28, backgroundColor: "rgba(0,0,0,0.55)", alignItems: "center", justifyContent: "center" },
  scannerCopy: { position: "absolute", top: 116, alignItems: "center", paddingHorizontal: 30 },
  scannerTitle: { color: "#fff", fontSize: 26, fontWeight: "800", textShadowColor: "rgba(0,0,0,0.6)", textShadowRadius: 8 },
  scannerSubtitle: { color: "#fff", fontSize: 15, lineHeight: 21, textAlign: "center", marginTop: 8, textShadowColor: "rgba(0,0,0,0.6)", textShadowRadius: 8 },
  reticle: { width: 230, height: 230, borderWidth: 4, borderColor: "rgba(255,255,255,0.95)", borderRadius: 28 },
});
