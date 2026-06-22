// Publish — the single mobile store surface: Setup · Permissions · Listing
// (one screen, no clutter). All three read the agent endpoints (/stores,
// /capabilities, /listing) over the YAVER mesh. For everything only a human
// can do (identity, payment, review) it opens the exact official page.
import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Linking, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";

type Section = "setup" | "permissions" | "listing";

type StoreTask = {
  id: string; platform: "apple" | "google" | "both"; title: string; summary: string;
  automation: "auto" | "assisted" | "manual"; routeUrl?: string; steps?: string[];
  yaverCmd?: string; status: "done" | "todo" | "action" | "blocked" | "unknown";
};
type ManifestPlan = {
  capabilities: { id: string; title: string; detected: boolean }[];
  iosPlistUsage: Record<string, string>;
  iosEntitlements: string[]; androidPermissions: string[]; consoleForms: string[];
};
type StoreListing = {
  appName: string; bundleId: string; packageName: string; version: string; description: string;
  privacy: { category: string; purposes: string[]; usedForTracking: boolean }[];
  consoleForms: string[];
};

const STATUS_GLYPH: Record<StoreTask["status"], string> = {
  done: "✓", todo: "○", action: "◆", blocked: "⋯", unknown: "·",
};
const GROUPS: { key: StoreTask["platform"]; label: string }[] = [
  { key: "apple", label: "APPLE" }, { key: "google", label: "GOOGLE" }, { key: "both", label: "CROSS-PLATFORM" },
];

export default function StoresScreen() {
  const router = useRouter();
  const c = useColors();
  const { activeDevice, primaryDeviceId } = useDevice();
  const deviceId = activeDevice?.id || primaryDeviceId || "";
  const [section, setSection] = useState<Section>("setup");
  const [stores, setStores] = useState<StoreTask[] | null>(null);
  const [caps, setCaps] = useState<ManifestPlan | null>(null);
  const [listing, setListing] = useState<StoreListing | null>(null);
  const [readiness, setReadiness] = useState<{ ready: boolean; blockers: string[] } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  const load = useCallback(async () => {
    setErr(null);
    if (!deviceId) {
      setErr("No device connected.");
      setStores([]);
      return;
    }
    const get = async (path: string) => {
      const res = await quicClient.agentRequest(deviceId, path, undefined, 15000);
      return res.json().catch(() => null);
    };
    try {
      const [s, cp, l, rd] = await Promise.all([get("/stores"), get("/capabilities"), get("/listing"), get("/publish/status")]);
      setStores(Array.isArray(s?.tasks) ? s.tasks : []);
      setCaps(cp);
      setListing(l);
      setReadiness(rd && typeof rd.ready === "boolean" ? rd : null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setStores([]);
    }
  }, [deviceId]);

  useEffect(() => { void load(); }, [load]);

  const statusColor = (s: StoreTask["status"]) =>
    s === "done" ? c.success : s === "action" ? c.warn : s === "todo" ? c.accent : c.textMuted;

  return (
    <View style={[styles.root, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Publish to the stores" onBack={() => router.back()} />
      <View style={[styles.tabs, { backgroundColor: c.bgCard }]}>
        {(["setup", "permissions", "listing"] as Section[]).map((sx) => (
          <Pressable key={sx} onPress={() => setSection(sx)}
            style={[styles.tab, section === sx && { backgroundColor: c.accentSoft }]}>
            <Text style={{ color: section === sx ? c.accent : c.textMuted, fontWeight: "600", fontSize: 13, textTransform: "capitalize" }}>{sx}</Text>
          </Pressable>
        ))}
      </View>

      <ScrollView contentContainerStyle={styles.scroll}>
        {readiness ? (
          <View style={[styles.banner, { backgroundColor: readiness.ready ? c.successBg : c.warnBg, borderColor: readiness.ready ? c.successBorder : c.warnBorder }]}>
            <Text style={{ color: readiness.ready ? c.success : c.warn, fontWeight: "700", fontSize: 13 }}>
              {readiness.ready ? "✓ Ready to submit" : `✗ ${readiness.blockers.length} blocker(s) before you can ship`}
            </Text>
            {!readiness.ready ? readiness.blockers.map((b, i) => (
              <Text key={i} style={{ color: c.warn, fontSize: 11, marginTop: 2 }}>• {b}</Text>
            )) : null}
          </View>
        ) : null}
        {err ? (
          <View style={[styles.banner, { backgroundColor: c.warnBg, borderColor: c.warnBorder }]}>
            <Text style={{ color: c.warn, fontSize: 12 }}>{err}</Text>
          </View>
        ) : null}

        {section === "setup" ? (
          stores === null ? <ActivityIndicator color={c.accent} style={{ marginTop: 24 }} /> :
          GROUPS.map((g) => {
            const rows = (stores || []).filter((t) => t.platform === g.key);
            if (!rows.length) return null;
            return (
              <View key={g.key} style={{ marginBottom: 16 }}>
                <Text style={[styles.groupLabel, { color: c.textMuted }]}>{g.label}</Text>
                {rows.map((t) => {
                  const open = !!expanded[t.id];
                  return (
                    <View key={t.id} style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                      <Pressable onPress={() => setExpanded((m) => ({ ...m, [t.id]: !open }))} style={styles.cardHead}>
                        <Text style={[styles.glyph, { color: statusColor(t.status) }]}>{STATUS_GLYPH[t.status] ?? "·"}</Text>
                        <View style={{ flex: 1 }}>
                          <Text style={[styles.title, { color: c.textPrimary }]}>{t.title}</Text>
                          <Text style={[styles.sub, { color: c.textMuted }]} numberOfLines={open ? undefined : 2}>{t.summary}</Text>
                        </View>
                      </Pressable>
                      {open ? (
                        <View style={[styles.detail, { borderTopColor: c.border }]}>
                          {(t.steps || []).map((s, i) => <Text key={i} style={[styles.step, { color: c.textSecondary }]}>{i + 1}. {s}</Text>)}
                          {t.yaverCmd ? <Text style={[styles.cmd, { color: c.textPrimary, backgroundColor: c.bgInput }]}>{t.yaverCmd}</Text> : null}
                          {t.routeUrl ? (
                            <Pressable onPress={() => Linking.openURL(t.routeUrl!)} style={[styles.openBtn, { backgroundColor: c.accentSoft, borderColor: c.accent }]}>
                              <Text style={{ color: c.accent, fontWeight: "600", fontSize: 13 }}>Open the official page ↗</Text>
                            </Pressable>
                          ) : null}
                        </View>
                      ) : null}
                    </View>
                  );
                })}
              </View>
            );
          })
        ) : section === "permissions" ? (
          caps === null ? <ActivityIndicator color={c.accent} style={{ marginTop: 24 }} /> : (
            <View>
              <Text style={[styles.groupLabel, { color: c.textMuted }]}>DETECTED FROM YOUR CODE</Text>
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, flexDirection: "row", flexWrap: "wrap", gap: 6, padding: 12 }]}>
                {caps.capabilities.filter((x) => x.detected).map((x) => (
                  <Text key={x.id} style={[styles.chip, { backgroundColor: c.accentSoft, color: c.accent }]}>{x.title}</Text>
                ))}
                {caps.capabilities.filter((x) => x.detected).length === 0 ? (
                  <Text style={{ color: c.textMuted, fontSize: 13 }}>No permission-bearing SDKs found.</Text>
                ) : null}
              </View>
              {caps.androidPermissions?.length ? (
                <>
                  <Text style={[styles.groupLabel, { color: c.textMuted }]}>ANDROID PERMISSIONS</Text>
                  <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 12 }]}>
                    {caps.androidPermissions.map((p) => <Text key={p} style={[styles.mono, { color: c.textSecondary }]}>{p}</Text>)}
                  </View>
                </>
              ) : null}
              {caps.consoleForms?.length ? <ConsoleForms forms={caps.consoleForms} c={c} /> : null}
            </View>
          )
        ) : (
          listing === null ? <ActivityIndicator color={c.accent} style={{ marginTop: 24 }} /> : (
            <View>
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 12 }]}>
                <Text style={[styles.kv, { color: c.textMuted }]}>App: <Text style={{ color: c.textPrimary }}>{listing.appName || "—"}</Text></Text>
                <Text style={[styles.kv, { color: c.textMuted }]}>iOS: <Text style={{ color: c.textPrimary }}>{listing.bundleId || "—"}</Text></Text>
                <Text style={[styles.kv, { color: c.textMuted }]}>Android: <Text style={{ color: c.textPrimary }}>{listing.packageName || "—"}</Text></Text>
                <Text style={[styles.sub, { color: c.textMuted, marginTop: 8 }]}>{listing.description}</Text>
              </View>
              <Text style={[styles.groupLabel, { color: c.textMuted }]}>PRIVACY / DATA SAFETY (FROM CODE)</Text>
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 12 }]}>
                {listing.privacy.length === 0 ? (
                  <Text style={{ color: c.textMuted, fontSize: 13 }}>No data collected.</Text>
                ) : listing.privacy.map((d, i) => (
                  <Text key={i} style={[styles.kv, { color: c.textSecondary }]}>
                    {d.category} — {d.purposes.join(", ")}{d.usedForTracking ? " · tracking" : ""}
                  </Text>
                ))}
              </View>
              {listing.consoleForms?.length ? <ConsoleForms forms={listing.consoleForms} c={c} /> : null}
            </View>
          )
        )}
      </ScrollView>
    </View>
  );
}

function ConsoleForms({ forms, c }: { forms: string[]; c: ReturnType<typeof useColors> }) {
  return (
    <>
      <Text style={[styles.groupLabel, { color: c.warn }]}>YOU SUBMIT THESE (YAVER DRAFTS + ROUTES)</Text>
      <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, padding: 12 }]}>
        {forms.map((f, i) => <Text key={i} style={[styles.kv, { color: c.textSecondary }]}>◆ {f}</Text>)}
      </View>
    </>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  tabs: { flexDirection: "row", margin: 12, borderRadius: 10, padding: 4 },
  tab: { flex: 1, paddingVertical: 8, borderRadius: 8, alignItems: "center" },
  scroll: { paddingHorizontal: 16, paddingBottom: 48 },
  banner: { borderWidth: 1, borderRadius: 8, padding: 10, marginBottom: 12 },
  groupLabel: { fontSize: 11, fontWeight: "700", letterSpacing: 1, marginBottom: 8, marginTop: 8 },
  card: { borderWidth: 1, borderRadius: 12, marginBottom: 8, overflow: "hidden" },
  cardHead: { flexDirection: "row", alignItems: "flex-start", gap: 10, padding: 12 },
  glyph: { fontSize: 16, width: 18, textAlign: "center", marginTop: 1 },
  title: { fontSize: 14, fontWeight: "600" },
  sub: { fontSize: 12, lineHeight: 16, marginTop: 2 },
  detail: { borderTopWidth: 1, padding: 12, gap: 6 },
  step: { fontSize: 12, lineHeight: 17 },
  cmd: { fontSize: 11, fontFamily: "Menlo", padding: 8, borderRadius: 6, marginTop: 4 },
  openBtn: { borderWidth: 1, borderRadius: 8, paddingVertical: 9, alignItems: "center", marginTop: 6 },
  chip: { fontSize: 12, fontWeight: "500", paddingHorizontal: 8, paddingVertical: 3, borderRadius: 6, overflow: "hidden" },
  mono: { fontSize: 11, fontFamily: "Menlo", lineHeight: 17 },
  kv: { fontSize: 12, lineHeight: 18 },
});
