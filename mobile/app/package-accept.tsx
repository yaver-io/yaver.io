// package-accept.tsx — the RUNNER's consent screen for a shared Task Package.
// Reachable via deep link (/package-accept?code=ABCD1234) or by pasting a code.
// Shows exactly what the package will do (domains, schedule, will-NOT list,
// data shared), the runner sets wifi/charging constraints, and Accept
// materializes the scoped grant. Decline does nothing. See
// docs/yaver-task-packages.md (runner consent).

import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Switch, Text, TextInput, View } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import {
  lookupSharedPackage,
  acceptSharedPackage,
  type SharedAllocation,
} from "../src/lib/packageShareClient";

export default function PackageAcceptScreen() {
  const c = useColors();
  const router = useRouter();
  const params = useLocalSearchParams<{ code?: string }>();

  const [code, setCode] = useState((params.code as string) || "");
  const [alloc, setAlloc] = useState<SharedAllocation | null>(null);
  const [wifiOnly, setWifiOnly] = useState(true);
  const [chargingOnly, setChargingOnly] = useState(false);
  const [loading, setLoading] = useState(false);
  const [accepted, setAccepted] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const lookup = useCallback(async (theCode: string) => {
    if (!theCode.trim()) return;
    setLoading(true);
    setErr(null);
    setAlloc(null);
    try {
      const a = await lookupSharedPackage(theCode);
      setAlloc(a);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (params.code) void lookup(params.code as string);
  }, [params.code, lookup]);

  async function accept() {
    setLoading(true);
    setErr(null);
    try {
      await acceptSharedPackage(code, { wifiOnly, chargingOnly });
      setAccepted(true);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  const row = (label: string, value: string) => (
    <View style={{ flexDirection: "row", justifyContent: "space-between", marginTop: 6 }}>
      <Text style={{ color: c.textMuted, fontSize: 13 }}>{label}</Text>
      <Text style={{ color: c.textPrimary, fontSize: 13, flexShrink: 1, textAlign: "right" }}>{value}</Text>
    </View>
  );

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Accept a shared task" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16, gap: 12 }}>
        {!alloc && (
          <View style={[styles.card, { borderColor: c.border }]}>
            <Text style={{ color: c.textMuted, fontSize: 12, textTransform: "uppercase", letterSpacing: 0.5 }}>
              Invite code
            </Text>
            <TextInput
              value={code}
              onChangeText={setCode}
              autoCapitalize="characters"
              placeholder="ABCD1234"
              placeholderTextColor={c.textMuted}
              style={{ color: c.textPrimary, borderColor: c.border, borderWidth: 1, borderRadius: 10, padding: 10, marginTop: 8, letterSpacing: 2 }}
            />
            <Pressable
              style={[styles.btn, { borderColor: c.accent, marginTop: 10, alignSelf: "flex-start" }]}
              disabled={loading || !code.trim()}
              onPress={() => void lookup(code)}
            >
              <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>Look up</Text>
            </Pressable>
          </View>
        )}

        {loading && <ActivityIndicator color={c.accent} />}
        {err && <Text style={{ color: "#f87171", fontSize: 13 }}>{err}</Text>}

        {alloc && !accepted && (
          <View style={[styles.card, { borderColor: c.border }]}>
            <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700" }}>{alloc.packageName}</Text>
            <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 2 }}>
              {alloc.kind}
              {alloc.tier === "acting" ? " · acting (can take actions)" : " · read-only"}
            </Text>

            {alloc.consentSummary ? (
              <Text style={{ color: c.textPrimary, fontSize: 14, marginTop: 12 }}>{alloc.consentSummary}</Text>
            ) : null}

            <View style={{ marginTop: 12 }}>
              {alloc.domains?.length > 0 && row("Fetches from", alloc.domains.join(", "))}
              {alloc.schedule ? row("How often", alloc.schedule) : null}
              {alloc.dataShown?.length > 0 && row("Data shared", alloc.dataShown.join(", "))}
              {row("Runs on", alloc.target)}
            </View>

            {alloc.willNot?.length > 0 && (
              <View style={{ marginTop: 12 }}>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>It will NOT:</Text>
                {alloc.willNot.map((w, i) => (
                  <Text key={i} style={{ color: "#34d399", fontSize: 13, marginTop: 2 }}>
                    ✓ never {w}
                  </Text>
                ))}
              </View>
            )}

            <View style={{ marginTop: 14, gap: 8 }}>
              <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                <Text style={{ color: c.textPrimary, fontSize: 13 }}>Only on Wi-Fi</Text>
                <Switch value={wifiOnly} onValueChange={setWifiOnly} />
              </View>
              <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                <Text style={{ color: c.textPrimary, fontSize: 13 }}>Only while charging</Text>
                <Switch value={chargingOnly} onValueChange={setChargingOnly} />
              </View>
            </View>

            <View style={{ flexDirection: "row", gap: 10, marginTop: 16 }}>
              <Pressable style={[styles.btn, { borderColor: c.border }]} onPress={() => router.back()}>
                <Text style={{ color: c.textMuted, fontSize: 14 }}>Decline</Text>
              </Pressable>
              <Pressable
                style={[styles.btn, { backgroundColor: c.accent, borderColor: c.accent }]}
                disabled={loading}
                onPress={() => void accept()}
              >
                <Text style={{ color: "#fff", fontSize: 14, fontWeight: "700" }}>Accept & start helping</Text>
              </Pressable>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 10 }}>
              No accounts, no logins. Results go to the person who shared this. You can stop anytime.
            </Text>
          </View>
        )}

        {accepted && (
          <View style={[styles.card, { borderColor: "#34d39955" }]}>
            <Text style={{ color: "#34d399", fontSize: 16, fontWeight: "700" }}>✓ Accepted</Text>
            <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 6 }}>
              “{alloc?.packageName}” is now shared with your device. Open Task Packages to run it or turn on the
              periodic runner.
            </Text>
            <Pressable
              style={[styles.btn, { borderColor: c.accent, marginTop: 12, alignSelf: "flex-start" }]}
              onPress={() => router.replace("/packages")}
            >
              <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>Open Task Packages</Text>
            </Pressable>
          </View>
        )}
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  card: { borderWidth: 1, borderRadius: 14, padding: 16 },
  btn: { borderWidth: 1, borderRadius: 12, paddingHorizontal: 16, paddingVertical: 8 },
});
