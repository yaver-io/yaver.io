import { router } from "expo-router";
import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";

import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useAuth } from "../src/context/AuthContext";
import { useDevice } from "../src/context/DeviceContext";
import { useColors } from "../src/context/ThemeContext";
import {
  enrollAccessDevice,
  enableHostedAccessChannel,
  listAccessBrokers,
  registerAccessBroker,
  requestYaverSignIn,
  type AccessBroker,
} from "../src/lib/accessChannel";

export default function AccessChannelScreen() {
  const c = useColors();
  const { token } = useAuth();
  const { devices } = useDevice();
  const [brokers, setBrokers] = useState<AccessBroker[]>([]);
  const [name, setName] = useState("My Hetzner access broker");
  const [endpoint, setEndpoint] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState("");

  const refresh = useCallback(async () => {
    if (!token) return;
    try {
      setBrokers(await listAccessBrokers(token));
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not load access brokers.");
    }
  }, [token]);

  useEffect(() => { void refresh(); }, [refresh]);

  const register = useCallback(async () => {
    if (!token || !endpoint.trim()) return;
    setBusy("register");
    setMessage("");
    try {
      await registerAccessBroker(token, { name, endpoint });
      setEndpoint("");
      setMessage("Broker registered to your Yaver account.");
      await refresh();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not register broker.");
    } finally {
      setBusy(null);
    }
  }, [endpoint, name, refresh, token]);

  const enableHosted = useCallback(async () => {
    if (!token) return;
    setBusy("hosted");
    setMessage("");
    try {
      await enableHostedAccessChannel(token);
      setMessage("Yaver Access is ready. No server setup or physical access is required.");
      await refresh();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not enable Yaver Access.");
    } finally {
      setBusy(null);
    }
  }, [refresh, token]);

  const hosted = brokers.find((broker) => broker.deployment === "yaver-hosted" && broker.enabled);
  const byo = brokers.filter((broker) => broker.deployment === "byo");

  const enroll = useCallback(async (broker: AccessBroker, deviceId: string) => {
    if (!token) return;
    const key = `${broker.brokerId}:${deviceId}`;
    setBusy(key);
    setMessage("");
    try {
      await enrollAccessDevice(token, broker.brokerId, deviceId);
      setMessage("Device enrolled. Its private signing key stayed on the device.");
      await refresh();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not enroll device.");
    } finally {
      setBusy(null);
    }
  }, [refresh, token]);

  const requestSignIn = useCallback(async (broker: AccessBroker, deviceId: string) => {
    if (!token) return;
    const key = `auth:${broker.brokerId}:${deviceId}`;
    setBusy(key);
    setMessage("");
    try {
      await requestYaverSignIn(token, broker.brokerId, deviceId);
      setMessage("The device accepted the recovery request. Approve its pending Yaver sign-in from your device list.");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not request Yaver sign-in.");
    } finally {
      setBusy(null);
    }
  }, [token]);

  return (
    <View style={[styles.fill, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Access Channel" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
        <Text style={[styles.lead, { color: c.textSecondary }]}>
          Securely reach headless devices for wake and authorization. Tokens, source code, video,
          terminal output, keystrokes, and credentials never travel through this control plane.
        </Text>

        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.title, { color: c.textPrimary }]}>Yaver Access</Text>
          <Text style={[styles.meta, { color: c.textMuted }]}>Cloudflare hibernating connection · no server to maintain</Text>
          {hosted ? (
            <Text style={{ color: c.accent, fontWeight: "700" }}>Ready</Text>
          ) : (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Enable hosted Yaver Access"
              disabled={busy === "hosted"}
              onPress={() => void enableHosted()}
              style={[styles.primary, { backgroundColor: c.accent }, busy === "hosted" && styles.disabled]}
            >
              {busy === "hosted" ? <ActivityIndicator color="#000" /> : <Text style={styles.primaryText}>Enable</Text>}
            </Pressable>
          )}
        </View>

        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.title, { color: c.textPrimary }]}>Use your own MQTT broker</Text>
          <Text style={[styles.meta, { color: c.textMuted }]}>Optional advanced adapter for your Hetzner or on-premise infrastructure</Text>
          <TextInput
            value={name}
            onChangeText={setName}
            placeholder="Broker name"
            placeholderTextColor={c.textMuted}
            style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <TextInput
            value={endpoint}
            onChangeText={setEndpoint}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
            placeholder="mqtts://access.example.com:8883"
            placeholderTextColor={c.textMuted}
            style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Register secure access broker"
            disabled={busy === "register" || !endpoint.trim()}
            onPress={() => void register()}
            style={[styles.primary, { backgroundColor: c.accent }, (busy === "register" || !endpoint.trim()) && styles.disabled]}
          >
            {busy === "register" ? <ActivityIndicator color="#000" /> : <Text style={styles.primaryText}>Register securely</Text>}
          </Pressable>
        </View>

        {[...(hosted ? [hosted] : []), ...byo].map((broker) => (
          <View key={broker.brokerId} style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.title, { color: c.textPrimary }]}>{broker.name}</Text>
            <Text style={[styles.meta, { color: c.textMuted }]}>{broker.transport === "websocket" ? "Hosted WebSocket" : broker.endpoint}</Text>
            <Text style={[styles.section, { color: c.textSecondary }]}>Enroll an owned device</Text>
            {devices.map((device) => {
              const key = `${broker.brokerId}:${device.id}`;
              const authKey = `auth:${key}`;
              const enrolled = broker.deviceIds.includes(device.id);
              return (
                <Pressable
                  key={device.id}
                  accessibilityRole="button"
                  accessibilityLabel={`Enroll ${device.name} in ${broker.name}`}
                  disabled={busy === key || busy === authKey}
                  onPress={() => enrolled && broker.transport === "websocket" ? void requestSignIn(broker, device.id) : void enroll(broker, device.id)}
                  style={[styles.device, { borderColor: c.border }]}
                >
                  <View style={styles.deviceText}>
                    <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{device.name}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>{device.os || "Yaver device"}</Text>
                  </View>
                  {busy === key || busy === authKey
                    ? <ActivityIndicator color={c.accent} />
                    : <Text style={{ color: c.accent }}>{enrolled && broker.transport === "websocket" ? "Request sign-in" : enrolled ? "Enrolled" : "Enroll"}</Text>}
                </Pressable>
              );
            })}
          </View>
        ))}

        {message ? <Text accessibilityLiveRegion="polite" style={[styles.message, { color: c.textSecondary }]}>{message}</Text> : null}
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  fill: { flex: 1 },
  content: { padding: 18, gap: 14, paddingBottom: 48 },
  lead: { fontSize: 14, lineHeight: 20 },
  card: { borderWidth: 1, borderRadius: 16, padding: 16, gap: 12 },
  title: { fontSize: 17, fontWeight: "700" },
  meta: { fontSize: 12 },
  section: { fontSize: 12, fontWeight: "700", marginTop: 4 },
  input: { borderWidth: 1, borderRadius: 11, paddingHorizontal: 12, paddingVertical: 11, fontSize: 14 },
  primary: { minHeight: 46, borderRadius: 11, alignItems: "center", justifyContent: "center" },
  primaryText: { color: "#000", fontWeight: "800" },
  disabled: { opacity: 0.45 },
  device: { minHeight: 50, borderTopWidth: StyleSheet.hairlineWidth, flexDirection: "row", alignItems: "center", gap: 12 },
  deviceText: { flex: 1 },
  message: { fontSize: 13, lineHeight: 18 },
});
