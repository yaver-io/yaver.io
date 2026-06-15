// Data Collection · Vantages — Yaver as a general user-directed data-collection
// layer. See this runtime's egress identity (the IP/geo a source sees), lend or
// borrow egress between YOUR OWN machines (peer-egress), inspect per-vantage
// source health + blocks, and view the cross-vantage diff for a source.
// Transport mirrors the circuit/arm cells: LAN-first, relay fallback, your
// bearer. Collected data stays on the box (local store) — never on Convex.
import React, { useCallback, useEffect, useRef, useState } from "react";
import { Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  dataCollectionClient,
  getDataCollectionDeviceId,
  setDataCollectionDeviceId,
  type DataCollectionTarget,
  type Egress,
  type EgressProxyPolicy,
  type HealthRow,
  type VantageCompare,
} from "../src/lib/dataCollectionClient";

function stateColor(c: any, state?: string): string {
  if (!state) return c.textMuted;
  if (state === "healthy") return c.success;
  if (state.startsWith("blocked_") || state === "rate_limited") return c.error;
  return c.warn;
}

export default function DataCollectionScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];
  const styles = makeStyles(c);

  const [deviceId, setDeviceId] = useState("");
  const [egress, setEgress] = useState<Egress | null>(null);
  const [policy, setPolicy] = useState<EgressProxyPolicy | null>(null);
  const [health, setHealth] = useState<HealthRow[]>([]);
  const [blocked, setBlocked] = useState<HealthRow[]>([]);
  const [sourceId, setSourceId] = useState("");
  const [dataset, setDataset] = useState("");
  const [compare, setCompare] = useState<VantageCompare | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const liveRef = useRef(true);

  const target = useCallback((): DataCollectionTarget | undefined => {
    if (!deviceId) return undefined;
    const d = devices?.find((x) => x.id === deviceId || x.deviceId === deviceId);
    return { id: deviceId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  useEffect(() => {
    getDataCollectionDeviceId().then((id) => id && setDeviceId(id));
    return () => {
      liveRef.current = false;
    };
  }, []);

  const refresh = useCallback(async () => {
    const t = target();
    if (!t) return;
    setBusy(true);
    setErr(null);
    setMsg(null);
    const eg = await dataCollectionClient.runtimeEgress(t);
    if (eg?.egress) setEgress(eg.egress);
    const st = await dataCollectionClient.egressProxyStatus(t);
    if (st?.policy) setPolicy(st.policy);
    const h = await dataCollectionClient.sourceHealth(t);
    if (Array.isArray(h?.health)) setHealth(h.health);
    const b = await dataCollectionClient.blockList(t);
    if (Array.isArray(b?.blocked)) setBlocked(b.blocked);
    setBusy(false);
  }, [target]);

  useEffect(() => {
    if (deviceId) {
      setDataCollectionDeviceId(deviceId);
      refresh();
    }
  }, [deviceId, refresh]);

  const toggleLending = useCallback(async () => {
    const t = target();
    if (!t) return;
    setBusy(true);
    setErr(null);
    const next = !(policy?.enabled ?? false);
    const r = await dataCollectionClient.egressProxySet(t, { enabled: next });
    setBusy(false);
    if ((r as any)?.ok === false) {
      setErr((r as any).error || "could not update egress policy");
      return;
    }
    if (r?.policy) setPolicy(r.policy);
    setMsg(next ? "Egress lending enabled (owner-only, opt-in)" : "Egress lending disabled");
  }, [target, policy]);

  const doCompare = useCallback(async () => {
    const t = target();
    if (!t) return;
    if (!sourceId.trim()) {
      setErr("enter a sourceId to compare");
      return;
    }
    setBusy(true);
    setErr(null);
    setMsg(null);
    const r = await dataCollectionClient.vantageCompare(t, sourceId.trim(), dataset.trim() || undefined);
    setBusy(false);
    if ((r as any)?.ok === false) {
      setErr((r as any).error || "compare failed");
      return;
    }
    setCompare(r);
  }, [target, sourceId, dataset]);

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Data Collection · Vantages" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 48 }}>
        {/* device picker */}
        <Text style={styles.label}>Runtime (device)</Text>
        <View style={styles.row}>
          {(devices || []).map((d: any) => {
            const id = d.id || d.deviceId;
            const on = id === deviceId;
            return (
              <Pressable key={id} onPress={() => setDeviceId(id)} style={[styles.chip, on && { backgroundColor: c.accent, borderColor: c.accent }]}>
                <Text style={{ color: on ? c.textInverse : c.textSecondary, fontSize: 13 }}>{d.name || id}</Text>
              </Pressable>
            );
          })}
        </View>
        <Pressable onPress={refresh} disabled={!deviceId || busy} style={[styles.btn, { marginTop: 10, opacity: !deviceId || busy ? 0.4 : 1 }]}>
          <Text style={{ color: c.textPrimary, fontSize: 13 }}>{busy ? "Refreshing…" : "Refresh"}</Text>
        </Pressable>

        {err ? <Text style={[styles.muted, { color: c.error, marginTop: 12 }]}>{err}</Text> : null}
        {!err && msg ? <Text style={[styles.muted, { marginTop: 12 }]}>{msg}</Text> : null}

        {/* egress identity */}
        {egress ? (
          <View style={styles.card}>
            <Text style={styles.cardTitle}>Egress identity</Text>
            <Text style={styles.mono}>IP: {egress.ip || "—"}</Text>
            <Text style={styles.mono}>Geo: {[egress.region, egress.country].filter(Boolean).join(" / ") || "—"}</Text>
            <Text style={styles.mono}>ASN: {egress.asn || "—"}</Text>
            <Text style={styles.mono}>Stable: {egress.stableKnown ? (egress.stable ? "yes" : "no") : "unknown"}</Text>
          </View>
        ) : null}

        {/* egress lending */}
        {policy ? (
          <View style={styles.card}>
            <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
              <View style={{ flex: 1, paddingRight: 10 }}>
                <Text style={styles.cardTitle}>Egress lending</Text>
                <Text style={styles.muted}>Lend this box&apos;s IP to your own other devices. Opt-in, owner-only, never an open proxy.</Text>
              </View>
              <Pressable onPress={toggleLending} disabled={busy} style={[styles.btn, policy.enabled && { backgroundColor: c.success, borderColor: c.success }]}>
                <Text style={{ color: policy.enabled ? c.textInverse : c.textPrimary, fontSize: 13 }}>{policy.enabled ? "Enabled" : "Disabled"}</Text>
              </Pressable>
            </View>
          </View>
        ) : null}

        {/* cross-vantage compare */}
        <View style={styles.card}>
          <Text style={styles.cardTitle}>Cross-vantage compare</Text>
          <TextInput
            value={sourceId}
            onChangeText={setSourceId}
            placeholder="sourceId"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            style={styles.input}
          />
          <TextInput
            value={dataset}
            onChangeText={setDataset}
            placeholder="dataset (optional)"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            style={[styles.input, { marginTop: 8 }]}
          />
          <Pressable onPress={doCompare} disabled={!deviceId || busy} style={[styles.btn, { marginTop: 10, backgroundColor: c.accent, borderColor: c.accent, opacity: !deviceId || busy ? 0.4 : 1 }]}>
            <Text style={{ color: c.textInverse, fontSize: 13 }}>Compare</Text>
          </Pressable>
          {compare?.vantages?.length ? (
            <View style={{ marginTop: 12 }}>
              {compare.vantages.map((v) => (
                <View key={v.vantageId} style={styles.vantageRow}>
                  <Text style={[styles.mono, { fontWeight: "700" }]}>{v.vantageId}</Text>
                  <Text style={styles.muted}>{[v.egressGeo, v.egressCountry, v.egressIp].filter(Boolean).join(" ") || "—"}</Text>
                  <Text style={[styles.mono, { color: stateColor(c, v.state) }]}>{v.state || "—"}</Text>
                  {(compare.fields || []).map((f) => (
                    <Text key={f} style={styles.mono}>
                      {f}: {v.values && v.values[f] !== undefined ? String(v.values[f]) : "—"}
                    </Text>
                  ))}
                </View>
              ))}
            </View>
          ) : (
            <Text style={[styles.muted, { marginTop: 8 }]}>No comparison loaded.</Text>
          )}
        </View>

        {/* per-vantage health */}
        <View style={styles.card}>
          <Text style={styles.cardTitle}>Source health (per vantage)</Text>
          {health.length ? (
            health.map((h) => (
              <View key={`${h.sourceId}|${h.vantageId}`} style={styles.healthRow}>
                <Text style={[styles.mono, { flex: 1 }]} numberOfLines={1}>
                  {h.sourceId} · {h.vantageId}
                </Text>
                <Text style={[styles.mono, { color: stateColor(c, h.state) }]}>{h.state}</Text>
                <Text style={styles.muted}>
                  {" "}
                  {h.geoBlockCount24h ?? 0}/{h.ipBlockCount24h ?? 0}/{h.rateLimitCount24h ?? 0}
                </Text>
              </View>
            ))
          ) : (
            <Text style={styles.muted}>No health rows yet.</Text>
          )}
        </View>

        {/* blocks */}
        {blocked.length ? (
          <View style={[styles.card, { borderColor: c.error }]}>
            <Text style={[styles.cardTitle, { color: c.error }]}>Blocked vantages</Text>
            <Text style={[styles.muted, { marginBottom: 6 }]}>Recorded as findings — Yaver does not rotate IPs to route around a block.</Text>
            {blocked.map((b) => (
              <Text key={`${b.sourceId}|${b.vantageId}`} style={[styles.mono, { color: stateColor(c, b.state) }]}>
                {b.sourceId} · {b.vantageId} · {b.state}
              </Text>
            ))}
          </View>
        ) : null}
      </ScrollView>
    </View>
  );
}

function makeStyles(c: any) {
  return StyleSheet.create({
    label: { color: c.textMuted, fontSize: 13, fontWeight: "600", marginBottom: 6, marginTop: 4 },
    muted: { color: c.textMuted, fontSize: 13 },
    mono: { color: c.textPrimary, fontSize: 12, fontFamily: "monospace" },
    row: { flexDirection: "row", flexWrap: "wrap", alignItems: "center", gap: 8 },
    chip: { borderWidth: 1, borderColor: c.border, borderRadius: 18, paddingHorizontal: 14, paddingVertical: 7, marginRight: 8, backgroundColor: c.bgCard },
    btn: { borderRadius: 8, paddingHorizontal: 14, paddingVertical: 8, backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, alignSelf: "flex-start" },
    input: { color: c.textPrimary, borderWidth: 1, borderColor: c.border, borderRadius: 8, paddingHorizontal: 12, paddingVertical: 8, backgroundColor: c.bgInput, fontSize: 13 },
    card: { backgroundColor: c.bgCard, borderRadius: 10, borderWidth: 1, borderColor: c.border, padding: 12, marginTop: 14 },
    cardTitle: { color: c.textPrimary, fontWeight: "700", fontSize: 14, marginBottom: 8 },
    vantageRow: { borderTopWidth: 1, borderTopColor: c.border, paddingTop: 8, marginTop: 8 },
    healthRow: { flexDirection: "row", alignItems: "center", paddingVertical: 4 },
  });
}
