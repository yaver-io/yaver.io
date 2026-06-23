// Store Testers — Yaver as the single management layer for app-store BETA
// testers + builds, for a third-party dev's OWN app. apple = TestFlight beta
// testers/groups/builds; google = Play internal track Google-Groups + rollout.
// Backed by the store_* MCP ops verbs on the box (LAN-first, relay fallback,
// your bearer). Credentials live in the box vault — never on Convex. Mirrors
// the circuit/printer cell transport.
import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  storeTestersClient,
  getStoreDeviceId,
  setStoreDeviceId,
  type ASCBetaTester,
  type ASCBuild,
  type PlayRelease,
  type Store,
  type StoreTarget,
} from "../src/lib/storeTestersClient";

const ACCENT = "#4f9cf9";

export default function StoreTestersScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];

  const [deviceId, setDeviceId] = useState("");
  const [store, setStore] = useState<Store>("apple");
  const [bundleId, setBundleId] = useState("");
  const [packageName, setPackageName] = useState("");
  const [creds, setCreds] = useState<any>(null);
  const [testers, setTesters] = useState<ASCBetaTester[]>([]);
  const [googleGroups, setGoogleGroups] = useState<string[]>([]);
  const [note, setNote] = useState("");
  const [builds, setBuilds] = useState<Array<{ version: string; state?: string; expired?: boolean }>>([]);
  const [email, setEmail] = useState("");
  const [groupEmail, setGroupEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  const target = useCallback((): StoreTarget | undefined => {
    if (!deviceId) return undefined;
    const d = devices?.find((x) => x.id === deviceId || x.deviceId === deviceId);
    return { id: deviceId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  const ident = useCallback(
    () => ({ store, bundleId: bundleId.trim(), packageName: packageName.trim(), track: "internal" }),
    [store, bundleId, packageName],
  );

  useEffect(() => {
    getStoreDeviceId().then((v) => v && setDeviceId(v));
  }, []);

  const load = useCallback(async () => {
    const t = target();
    if (!t) return;
    setMsg(null);
    setNote("");
    try {
      const cr = await storeTestersClient.credentialsStatus(t);
      setCreds(cr);
      if (store === "apple") {
        if (!bundleId.trim()) return;
        const [tl, bl] = await Promise.all([
          storeTestersClient.testerList(t, ident()),
          storeTestersClient.buildList(t, ident()),
        ]);
        setTesters(tl.testers || []);
        setBuilds((bl.builds || []).map((b: ASCBuild) => ({ version: b.version, state: b.processingState, expired: b.expired })));
        setGoogleGroups([]);
      } else {
        if (!packageName.trim()) return;
        const [tl, bl] = await Promise.all([
          storeTestersClient.testerList(t, ident()),
          storeTestersClient.buildList(t, ident()),
        ]);
        setGoogleGroups(tl.googleGroups || []);
        setNote(tl.note || "");
        setBuilds((bl.releases || []).map((r: PlayRelease) => ({ version: (r.versionCodes || []).join(", "), state: r.status })));
        setTesters([]);
      }
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    }
  }, [target, store, bundleId, packageName, ident]);

  useEffect(() => {
    void load();
  }, [load]);

  const run = async (fn: () => Promise<any>, okMsg: string) => {
    const t = target();
    if (!t) {
      setMsg("pick a device first");
      return;
    }
    setBusy(true);
    setMsg(null);
    try {
      const r = await fn();
      if (r?.ok === false) setMsg(r.error || "failed");
      else {
        setMsg(okMsg);
        await load();
      }
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const configured = store === "apple" ? creds?.apple?.configured : creds?.google?.configured;

  const field = { backgroundColor: c.bgCard, color: c.textPrimary, borderColor: c.border };

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Store Testers" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16, gap: 12 }}>
        <Text style={{ color: c.textMuted, fontSize: 12 }}>
          Manage TestFlight / Play internal testers + builds for your own app, straight from the box.
        </Text>

        <TextInput
          value={deviceId}
          onChangeText={(v) => {
            setDeviceId(v);
            void setStoreDeviceId(v);
          }}
          placeholder="device id"
          placeholderTextColor={c.textMuted}
          autoCapitalize="none"
          style={[styles.input, field]}
        />

        <View style={styles.row}>
          {(["apple", "google"] as const).map((s) => (
            <Pressable
              key={s}
              onPress={() => setStore(s)}
              style={[styles.tab, { borderColor: c.border }, store === s && { backgroundColor: ACCENT, borderColor: ACCENT }]}
            >
              <Text style={{ color: store === s ? "#fff" : c.textPrimary, fontWeight: "600", fontSize: 13 }}>
                {s === "apple" ? "TestFlight" : "Play internal"}
              </Text>
            </Pressable>
          ))}
        </View>

        {store === "apple" ? (
          <TextInput value={bundleId} onChangeText={setBundleId} placeholder="bundle id (com.acme.app)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[styles.input, field]} />
        ) : (
          <TextInput value={packageName} onChangeText={setPackageName} placeholder="package name (com.acme.app)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[styles.input, field]} />
        )}

        {creds && !configured ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={{ color: "#d97706", fontSize: 13 }}>
              {store === "apple" ? "App Store Connect" : "Google Play"} credentials not configured.
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
              {store === "apple" ? "Add APP_STORE_KEY_PATH/_ID/_ISSUER to the vault." : "Add PLAY_STORE_KEY_FILE to the vault."}
            </Text>
          </View>
        ) : null}

        {msg ? <Text style={{ color: ACCENT, fontSize: 12 }}>{msg}</Text> : null}

        {/* Builds */}
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <View style={styles.cardHead}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>{store === "apple" ? "TestFlight builds" : "Internal releases"}</Text>
            <Pressable
              disabled={busy}
              onPress={() => run(() => storeTestersClient.releasePromote(target()!, ident(), store === "google" ? { status: "completed" } : {}), store === "apple" ? "Assigned latest build to group." : "Rolled out to testers.")}
              style={[styles.btn, { borderColor: ACCENT }]}
            >
              <Text style={{ color: ACCENT, fontWeight: "600", fontSize: 12 }}>{store === "apple" ? "Assign latest" : "Roll out"}</Text>
            </Pressable>
          </View>
          {builds.length === 0 ? (
            <Text style={{ color: c.textMuted, fontSize: 13 }}>No builds.</Text>
          ) : (
            builds.slice(0, 8).map((b, i) => (
              <View key={i} style={styles.lineRow}>
                <Text style={{ color: c.textPrimary, fontSize: 12, fontFamily: "Menlo" }}>{b.version}</Text>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>{b.state || ""}{b.expired ? " · expired" : ""}</Text>
              </View>
            ))
          )}
        </View>

        {/* Testers / groups */}
        {store === "apple" ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary, marginBottom: 8 }]}>Beta testers ({testers.length})</Text>
            <View style={styles.row}>
              <TextInput value={email} onChangeText={setEmail} placeholder="tester@email.com" placeholderTextColor={c.textMuted} autoCapitalize="none" keyboardType="email-address" style={[styles.input, field, { flex: 1 }]} />
              <Pressable disabled={busy || !email} onPress={() => run(() => storeTestersClient.testerInvite(target()!, ident(), { email }), `Invited ${email}.`)} style={[styles.btn, { borderColor: "#10b981", opacity: !email ? 0.5 : 1 }]}>
                <Text style={{ color: "#10b981", fontWeight: "600", fontSize: 12 }}>Invite</Text>
              </Pressable>
            </View>
            {testers.slice(0, 30).map((t, i) => (
              <View key={i} style={styles.lineRow}>
                <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }} numberOfLines={1}>{t.email}</Text>
                <Text style={{ color: c.textMuted, fontSize: 11, marginRight: 8 }}>{t.state}</Text>
                <Pressable onPress={() => run(() => storeTestersClient.testerRemove(target()!, ident(), { email: t.email }), `Removed ${t.email}.`)}>
                  <Text style={{ color: "#f43f5e", fontSize: 12 }}>remove</Text>
                </Pressable>
              </View>
            ))}
          </View>
        ) : (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary, marginBottom: 8 }]}>Track Google Groups ({googleGroups.length})</Text>
            {note ? <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 8 }}>{note}</Text> : null}
            <View style={styles.row}>
              <TextInput value={groupEmail} onChangeText={setGroupEmail} placeholder="testers@yourdomain.com" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[styles.input, field, { flex: 1 }]} />
              <Pressable disabled={busy || !groupEmail} onPress={() => run(() => storeTestersClient.testerInvite(target()!, ident(), { groupEmail }), `Bound ${groupEmail}.`)} style={[styles.btn, { borderColor: "#10b981", opacity: !groupEmail ? 0.5 : 1 }]}>
                <Text style={{ color: "#10b981", fontWeight: "600", fontSize: 12 }}>Bind</Text>
              </Pressable>
            </View>
            {googleGroups.map((g, i) => (
              <View key={i} style={styles.lineRow}>
                <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }} numberOfLines={1}>{g}</Text>
                <Pressable onPress={() => run(() => storeTestersClient.testerRemove(target()!, ident(), { groupEmail: g }), `Unbound ${g}.`)}>
                  <Text style={{ color: "#f43f5e", fontSize: 12 }}>unbind</Text>
                </Pressable>
              </View>
            ))}
          </View>
        )}

        {busy ? <ActivityIndicator color={ACCENT} /> : null}
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  input: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8, fontSize: 14 },
  row: { flexDirection: "row", gap: 8, alignItems: "center" },
  tab: { flex: 1, borderWidth: 1, borderRadius: 8, paddingVertical: 8, alignItems: "center" },
  card: { borderWidth: 1, borderRadius: 12, padding: 12, gap: 6 },
  cardHead: { flexDirection: "row", justifyContent: "space-between", alignItems: "center" },
  cardTitle: { fontSize: 12, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.5 },
  btn: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 12, paddingVertical: 6 },
  lineRow: { flexDirection: "row", justifyContent: "space-between", alignItems: "center", paddingVertical: 3 },
});
