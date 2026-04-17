import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Pressable,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useColors } from "../../src/context/ThemeContext";
import { AppBackButton } from "../../src/components/AppBackButton";
import {
  CFRecord,
  CFRecordInput,
  CFZone,
  createCloudflareRecord,
  deleteCloudflareRecord,
  listCloudflareRecords,
  listCloudflareZones,
  verifyCloudflareToken,
} from "../../src/lib/phoneProjects";

// Per-project "Custom Domain" screen. Pastes a Cloudflare API token (Zone:
// DNS:Edit scope), lists the user's zones, lets them CNAME <sub>.<zone>
// to cloud.yaver.io (or any custom target) in one tap. No persistence on
// the agent — token stays on this phone (AsyncStorage) and travels via
// the X-CF-Token header per-request. See desktop/agent/cloudflare_dns.go.

const TOKEN_KEY = "yaver.cloudflare.token";
const DEFAULT_TARGET = "cloud.yaver.io";

export default function PhoneDNSScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");

  const [token, setToken] = useState("");
  const [tokenValid, setTokenValid] = useState<boolean | null>(null);
  const [tokenMsg, setTokenMsg] = useState<string | null>(null);
  const [verifying, setVerifying] = useState(false);

  const [zones, setZones] = useState<CFZone[]>([]);
  const [loadingZones, setLoadingZones] = useState(false);

  const [zoneId, setZoneId] = useState<string | null>(null);
  const [records, setRecords] = useState<CFRecord[]>([]);
  const [loadingRecords, setLoadingRecords] = useState(false);

  const [subdomain, setSubdomain] = useState("");
  const [target, setTarget] = useState(DEFAULT_TARGET);
  const [proxied, setProxied] = useState(true);
  const [recordType, setRecordType] = useState<"CNAME" | "A" | "TXT">("CNAME");
  const [creating, setCreating] = useState(false);

  // Hydrate token from device storage on mount.
  useEffect(() => {
    AsyncStorage.getItem(TOKEN_KEY).then((t) => {
      if (t) setToken(t);
    }).catch(() => {});
  }, []);

  const selectedZone = zones.find((z) => z.id === zoneId) ?? null;

  const verifyAndLoad = useCallback(async () => {
    if (!token.trim()) return;
    setVerifying(true);
    setTokenValid(null);
    setTokenMsg(null);
    try {
      const st = await verifyCloudflareToken(token.trim());
      setTokenValid(st?.valid ?? false);
      setTokenMsg(st?.message ?? null);
      if (st?.valid) {
        await AsyncStorage.setItem(TOKEN_KEY, token.trim()).catch(() => {});
        setLoadingZones(true);
        try {
          const zs = await listCloudflareZones(token.trim());
          setZones(zs);
          if (zs.length === 1) setZoneId(zs[0].id);
        } finally {
          setLoadingZones(false);
        }
      }
    } catch (e: any) {
      setTokenValid(false);
      setTokenMsg(e?.message ?? "verify failed");
    } finally {
      setVerifying(false);
    }
  }, [token]);

  const loadRecords = useCallback(async () => {
    if (!token || !zoneId) return;
    setLoadingRecords(true);
    try {
      const r = await listCloudflareRecords(token, zoneId);
      setRecords(r);
    } catch (e: any) {
      Alert.alert("Couldn't load records", e?.message ?? String(e));
    } finally {
      setLoadingRecords(false);
    }
  }, [token, zoneId]);

  useEffect(() => {
    void loadRecords();
  }, [loadRecords]);

  async function pointToTarget() {
    if (!selectedZone) {
      Alert.alert("Pick a zone first");
      return;
    }
    const sub = subdomain.trim();
    const name = sub ? `${sub}.${selectedZone.name}` : selectedZone.name;
    if (!target.trim()) {
      Alert.alert("Target required", "e.g. cloud.yaver.io");
      return;
    }
    const input: CFRecordInput = {
      type: recordType,
      name,
      content: target.trim(),
      proxied: recordType === "CNAME" || recordType === "A" ? proxied : false,
      ttl: 1,
      comment: `yaver phone-project ${slugStr}`,
    };
    setCreating(true);
    try {
      const rec = await createCloudflareRecord(token, selectedZone.id, input);
      Alert.alert(
        "Record created",
        `${rec.type} ${rec.name} → ${rec.content}\n\nDNS will propagate in a few seconds. Yaver Cloud's Caddy picks up new hostnames automatically.`,
      );
      setSubdomain("");
      await loadRecords();
    } catch (e: any) {
      Alert.alert("Create failed", e?.message ?? String(e));
    } finally {
      setCreating(false);
    }
  }

  async function removeRecord(rec: CFRecord) {
    if (!selectedZone) return;
    Alert.alert(`Delete ${rec.name}?`, `${rec.type} → ${rec.content}`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          const ok = await deleteCloudflareRecord(token, selectedZone.id, rec.id);
          if (!ok) Alert.alert("Delete failed");
          await loadRecords();
        },
      },
    ]);
  }

  const statusColor = tokenValid === true ? (c.success ?? "#22c55e") : tokenValid === false ? "#ff6b6b" : c.textMuted;

  return (
    <ScrollView
      style={{ backgroundColor: c.bg }}
      contentContainerStyle={{ paddingTop: insets.top + 8, paddingBottom: 60 + insets.bottom }}
    >
      <View style={{ paddingHorizontal: 16 }}>
        <AppBackButton onPress={() => router.back()} style={{ marginBottom: 8 }} />
        <Text style={[styles.h1, { color: c.textPrimary }]}>Custom Domain</Text>
        <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 4 }}>
          Point a domain at <Text style={{ color: c.textPrimary }}>{slugStr}</Text> via
          Cloudflare. Create a Cloudflare API token with <Text style={{ color: c.textPrimary }}>Zone → DNS → Edit</Text> on the
          zone you want to manage. Yaver never stores the token — it travels with
          your request and lives in device storage on this phone.
        </Text>

        <Pressable
          onPress={() => Linking.openURL("https://dash.cloudflare.com/profile/api-tokens").catch(() => undefined)}
          style={[styles.openBtn, { borderColor: c.accent, marginTop: 12 }]}
        >
          <Text style={{ color: c.accent, fontWeight: "600", fontSize: 13 }}>
            Open Cloudflare → API Tokens ↗
          </Text>
        </Pressable>

        <Text style={[styles.label, { color: c.textMuted, marginTop: 16 }]}>API token</Text>
        <TextInput
          value={token}
          onChangeText={(v) => {
            setToken(v);
            setTokenValid(null);
          }}
          placeholder="Paste scoped Cloudflare token"
          placeholderTextColor={c.textMuted}
          autoCapitalize="none"
          autoCorrect={false}
          secureTextEntry
          style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
        />
        <Pressable
          onPress={verifyAndLoad}
          disabled={!token.trim() || verifying}
          style={[
            styles.verifyBtn,
            { backgroundColor: c.accent, opacity: !token.trim() || verifying ? 0.6 : 1 },
          ]}
        >
          {verifying ? (
            <ActivityIndicator color={c.bg} />
          ) : (
            <Text style={{ color: c.bg, fontWeight: "600" }}>Verify & load zones</Text>
          )}
        </Pressable>
        {tokenValid !== null ? (
          <Text style={{ color: statusColor, fontSize: 12, marginTop: 6 }}>
            {tokenValid ? "✓ Token active" : `✗ ${tokenMsg ?? "invalid"}`}
          </Text>
        ) : null}

        {zones.length > 0 ? (
          <View style={{ marginTop: 20 }}>
            <Text style={[styles.label, { color: c.textMuted }]}>Zone</Text>
            {loadingZones ? (
              <ActivityIndicator color={c.textMuted} />
            ) : (
              zones.map((z) => (
                <Pressable
                  key={z.id}
                  onPress={() => setZoneId(z.id)}
                  style={[
                    styles.zoneRow,
                    {
                      backgroundColor: zoneId === z.id ? c.accent + "22" : "transparent",
                      borderColor: zoneId === z.id ? c.accent : c.border,
                    },
                  ]}
                >
                  <Text style={{ color: c.textPrimary, fontWeight: "500" }}>{z.name}</Text>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    {z.status ?? "—"} · {z.id.slice(0, 8)}
                  </Text>
                </Pressable>
              ))
            )}
          </View>
        ) : null}

        {selectedZone ? (
          <View style={{ marginTop: 20 }}>
            <Text style={[styles.section, { color: c.textPrimary }]}>
              New record in {selectedZone.name}
            </Text>

            <View style={{ flexDirection: "row", gap: 8 }}>
              {(["CNAME", "A", "TXT"] as const).map((t) => (
                <Pressable
                  key={t}
                  onPress={() => setRecordType(t)}
                  style={[
                    styles.typeChip,
                    {
                      backgroundColor: recordType === t ? c.accent : c.bgCard,
                      borderColor: c.border,
                    },
                  ]}
                >
                  <Text style={{ color: recordType === t ? c.bg : c.textPrimary, fontSize: 12, fontWeight: "500" }}>
                    {t}
                  </Text>
                </Pressable>
              ))}
            </View>

            <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Subdomain</Text>
            <TextInput
              value={subdomain}
              onChangeText={setSubdomain}
              placeholder={`e.g. myapp (leave blank for ${selectedZone.name} itself)`}
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
            />
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }}>
              Full name: {subdomain ? `${subdomain}.${selectedZone.name}` : selectedZone.name}
            </Text>

            <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>
              {recordType === "CNAME" ? "Points to" : recordType === "A" ? "IP address" : "TXT content"}
            </Text>
            <TextInput
              value={target}
              onChangeText={setTarget}
              placeholder={recordType === "CNAME" ? "cloud.yaver.io" : recordType === "A" ? "1.2.3.4" : "verification string"}
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
            />

            {(recordType === "CNAME" || recordType === "A") ? (
              <View style={{ flexDirection: "row", alignItems: "center", marginTop: 10, justifyContent: "space-between" }}>
                <View style={{ flex: 1 }}>
                  <Text style={{ color: c.textPrimary, fontSize: 13 }}>Proxy through Cloudflare</Text>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    Orange cloud · caching + TLS; turn off for direct TLS via target host
                  </Text>
                </View>
                <Switch value={proxied} onValueChange={setProxied} />
              </View>
            ) : null}

            <Pressable
              onPress={pointToTarget}
              disabled={creating}
              style={[styles.createBtn, { backgroundColor: c.accent, opacity: creating ? 0.6 : 1 }]}
            >
              {creating ? (
                <ActivityIndicator color={c.bg} />
              ) : (
                <Text style={{ color: c.bg, fontWeight: "600" }}>
                  Create {recordType} record
                </Text>
              )}
            </Pressable>

            <Text style={[styles.section, { color: c.textPrimary, marginTop: 24 }]}>
              Existing records
            </Text>
            {loadingRecords ? (
              <ActivityIndicator color={c.textMuted} style={{ marginTop: 8 }} />
            ) : records.length === 0 ? (
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
                No records yet.
              </Text>
            ) : (
              records.map((r) => (
                <Pressable
                  key={r.id}
                  onLongPress={() => removeRecord(r)}
                  style={[styles.record, { backgroundColor: c.bgCard, borderColor: c.border }]}
                >
                  <Text style={{ color: c.textPrimary, fontWeight: "500", fontSize: 13 }}>
                    {r.type} {r.name}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 12 }}>
                    → {r.content}
                    {r.proxied ? "  ·  proxied" : ""}
                  </Text>
                </Pressable>
              ))
            )}
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
              Long-press a record to delete it.
            </Text>
          </View>
        ) : null}
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  h1: { fontSize: 24, fontWeight: "700" },
  label: { fontSize: 11, fontWeight: "500", marginBottom: 4, textTransform: "uppercase", letterSpacing: 0.5 },
  section: { fontSize: 13, fontWeight: "600", marginBottom: 8, textTransform: "uppercase", letterSpacing: 0.5 },
  input: { borderWidth: 1, borderRadius: 8, padding: 10, fontSize: 14 },
  openBtn: { borderWidth: 1, borderRadius: 8, paddingVertical: 10, alignItems: "center" },
  verifyBtn: { paddingVertical: 12, borderRadius: 8, alignItems: "center", marginTop: 10 },
  createBtn: { paddingVertical: 12, borderRadius: 8, alignItems: "center", marginTop: 14 },
  zoneRow: { borderWidth: 1, borderRadius: 8, padding: 10, marginTop: 6 },
  typeChip: { paddingHorizontal: 14, paddingVertical: 8, borderRadius: 16, borderWidth: 1 },
  record: { borderWidth: 1, borderRadius: 8, padding: 10, marginTop: 6 },
});
