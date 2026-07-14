// App Store screenshots screen — first-class "tap, walk away" surface for
// `yaver shots`. Auto-generates App Store screenshots on a simulator on
// the user's Mac (Engine 1) and optionally sets metadata + submits for
// review. Mirrors publish.tsx's queue-and-poll idiom (publishJobs), but
// shots-focused: locale, a submit toggle, an explainer, and shots-aware
// status labels.
//
// Pairs with backend/convex/publishJobs.ts (targets ["shots"] /
// ["shots-submit"], no schema change) + desktop/agent/publish_worker.go
// (runShotsTargetForJob). Privacy: only app NAME + targets cross the wire.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import EmptyState from "../../src/components/EmptyState";
import { useAuth } from "../../src/context/AuthContext";
import { useColors } from "../../src/context/ThemeContext";
import { CONVEX_SITE_URL } from "../../src/_core/constants";

type FarmDevice = {
  deviceId: string;
  name?: string;
  alias?: string;
  platform?: string;
  isOnline?: boolean;
  publishCapabilities?: string[];
};

type ShotsJob = {
  jobId: string;
  app: string;
  targets: string[];
  status: string;
  message?: string;
  result?: { target: string; ok: boolean }[];
  createdAt: number;
};

// A small, friendly locale set — the App Store localizations most solo
// devs ship first. Free text covers the rest.
const LOCALES = ["en-US", "en-GB", "tr", "de-DE", "es-ES", "fr-FR"];

export default function ShotsScreen() {
  const { token } = useAuth();
  const c = useColors();
  const router = useRouter();
  const [devices, setDevices] = useState<FarmDevice[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [app, setApp] = useState("");
  const [locale, setLocale] = useState("en-US");
  const [submit, setSubmit] = useState(false);
  const [jobs, setJobs] = useState<ShotsJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [queuing, setQueuing] = useState(false);
  const [note, setNote] = useState("");

  const authHeaders = useMemo(
    () => (token ? { Authorization: `Bearer ${token}` } : undefined),
    [token],
  );

  // iOS-capable farm nodes only — screenshots are an iOS simulator flow.
  const loadDevices = useCallback(async () => {
    if (!authHeaders) return;
    try {
      const r = await fetch(`${CONVEX_SITE_URL}/devices/list?_=${Date.now()}`, {
        headers: authHeaders,
      });
      const j = await r.json();
      const all: FarmDevice[] = j?.devices ?? j ?? [];
      const farm = all.filter((d) => (d.publishCapabilities ?? []).includes("ios"));
      setDevices(farm);
      setSelected((prev) =>
        prev && farm.some((d) => d.deviceId === prev) ? prev : farm[0]?.deviceId ?? "",
      );
    } catch {
      setNote("Couldn't load machines — check your connection.");
    } finally {
      setLoading(false);
    }
  }, [authHeaders]);

  // Only shots jobs belong on this screen.
  const loadJobs = useCallback(async () => {
    if (!authHeaders || !selected) return;
    try {
      const r = await fetch(
        `${CONVEX_SITE_URL}/publish-jobs/list?deviceId=${encodeURIComponent(selected)}&limit=20`,
        { headers: authHeaders },
      );
      const j = await r.json();
      const all: ShotsJob[] = j?.jobs ?? [];
      setJobs(all.filter((job) => job.targets.some((t) => t.startsWith("shots"))));
    } catch {
      /* transient — next poll retries */
    }
  }, [authHeaders, selected]);

  useEffect(() => {
    loadDevices();
  }, [loadDevices]);

  useEffect(() => {
    loadJobs();
    const t = setInterval(loadJobs, 8000);
    return () => clearInterval(t);
  }, [loadJobs]);

  const queue = useCallback(async () => {
    if (!authHeaders || !selected) return;
    const appName = app.trim();
    if (!appName) {
      setNote("Enter your app name first.");
      return;
    }
    setQueuing(true);
    setNote("");
    try {
      const r = await fetch(`${CONVEX_SITE_URL}/publish-jobs/queue`, {
        method: "POST",
        headers: { ...authHeaders, "Content-Type": "application/json" },
        body: JSON.stringify({
          deviceId: selected,
          app: appName,
          stack: "react-native-expo",
          targets: [submit ? "shots-submit" : "shots"],
          sourceSurface: "mobile",
        }),
      });
      const j = await r.json();
      if (!r.ok || !j?.ok) {
        setNote(j?.error || `Queue failed (HTTP ${r.status}).`);
      } else {
        setNote(
          j.deduped
            ? "Already running — joined the existing job."
            : "Queued. Close the app — it captures on your Mac.",
        );
        loadJobs();
      }
    } catch (e: any) {
      setNote(`Queue failed: ${e?.message ?? "network error"}`);
    } finally {
      setQueuing(false);
    }
  }, [authHeaders, selected, app, submit, loadJobs]);

  const styles = makeStyles(c);

  // Map raw publishJobs status → a shots-friendly label.
  const shotsStatus = (job: ShotsJob): { label: string; tone: string } => {
    const ok = (job.result ?? []).every((r) => r.ok) && (job.result?.length ?? 0) > 0;
    switch (job.status) {
      case "queued":
        return { label: "queued", tone: c.textMuted };
      case "claimed":
      case "running":
        return { label: "capturing…", tone: c.accent ?? "#58a6ff" };
      case "done":
        return job.targets.includes("shots-submit")
          ? { label: ok ? "submitted / staged" : "needs attention", tone: ok ? (c.success ?? "#3fb950") : (c.error ?? "#f85149") }
          : { label: ok ? "uploaded" : "failed", tone: ok ? (c.success ?? "#3fb950") : (c.error ?? "#f85149") };
      case "failed":
      case "expired":
        return { label: job.status, tone: c.error ?? "#f85149" };
      default:
        return { label: job.status, tone: c.textMuted };
    }
  };

  if (loading) {
    return (
      <SafeAreaView style={styles.center}>
        <ActivityIndicator color={c.accent} />
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={styles.root} edges={["top"]}>
      <FlatList
        data={jobs}
        keyExtractor={(j) => j.jobId}
        refreshControl={
          <RefreshControl
            refreshing={false}
            onRefresh={() => {
              loadDevices();
              loadJobs();
            }}
            tintColor={c.accent}
          />
        }
        ListHeaderComponent={
          <View>
            <Text style={styles.h1}>App Store Screenshots</Text>
            <Text style={styles.sub}>
              Yaver boots a simulator on your Mac, walks your app, captures
              every screen, and uploads them to App Store Connect. Turn on
              "Submit for review" to also set metadata and send it in. Tap and
              close the app — it runs on the Mac.
            </Text>

            {devices.length === 0 ? null : (
              <>
                <Text style={styles.label}>Machine</Text>
                {devices.map((d) => {
                  const on = d.deviceId === selected;
                  return (
                    <Pressable
                      key={d.deviceId}
                      onPress={() => setSelected(d.deviceId)}
                      style={[styles.row, on && styles.rowOn]}
                    >
                      <Text style={styles.rowName}>
                        {d.alias || d.name || d.deviceId.slice(0, 8)}
                      </Text>
                      <Text style={styles.rowMeta}>
                        {(d.publishCapabilities ?? []).join(" + ")}
                        {d.isOnline ? "" : "  · offline"}
                      </Text>
                    </Pressable>
                  );
                })}

                <Text style={styles.label}>App name</Text>
                <TextInput
                  value={app}
                  onChangeText={setApp}
                  placeholder="my-app"
                  placeholderTextColor={c.textMuted}
                  autoCapitalize="none"
                  autoCorrect={false}
                  style={styles.input}
                />

                <Text style={styles.label}>Locale</Text>
                <View style={styles.chips}>
                  {LOCALES.map((l) => (
                    <Pressable
                      key={l}
                      onPress={() => setLocale(l)}
                      style={[styles.chip, locale === l && styles.chipOn]}
                    >
                      <Text style={[styles.chipTxt, locale === l && styles.chipTxtOn]}>
                        {l}
                      </Text>
                    </Pressable>
                  ))}
                </View>

                <View style={styles.toggleRow}>
                  <View style={{ flex: 1 }}>
                    <Text style={styles.toggleLabel}>Submit for review</Text>
                    <Text style={styles.toggleSub}>
                      Also set metadata + attempt submit. If Apple gates on
                      compliance/pricing, it's left staged for one tap.
                    </Text>
                  </View>
                  <Switch
                    value={submit}
                    onValueChange={setSubmit}
                    trackColor={{ true: c.accent }}
                  />
                </View>

                <Pressable
                  onPress={queue}
                  disabled={queuing || !selected}
                  style={[styles.cta, (queuing || !selected) && styles.ctaOff]}
                >
                  {queuing ? (
                    <ActivityIndicator color="#fff" />
                  ) : (
                    <Text style={styles.ctaTxt}>
                      {submit
                        ? "Generate screenshots + submit"
                        : "Generate App Store screenshots"}
                    </Text>
                  )}
                </Pressable>

                {note ? <Text style={styles.note}>{note}</Text> : null}
                <Text style={styles.label}>Recent</Text>
              </>
            )}
          </View>
        }
        renderItem={({ item }) => {
          const st = shotsStatus(item);
          return (
            <View style={styles.job}>
              <View style={{ flex: 1 }}>
                <Text style={styles.jobApp}>
                  {item.app}
                  {item.targets.includes("shots-submit") ? "  · submit" : ""}
                </Text>
                {item.message ? (
                  <Text style={styles.jobMsg}>{item.message}</Text>
                ) : null}
              </View>
              <Text style={[styles.jobStatus, { color: st.tone }]}>{st.label}</Text>
            </View>
          );
        }}
        ListEmptyComponent={
          devices.length > 0 ? (
            <Text style={styles.empty}>No screenshot runs yet.</Text>
          ) : (
            // Zero iOS-capable nodes rendered a blank screen below the header.
            // The simulator lives on a Mac; that's the one thing to fix.
            <EmptyState
              icon="phone-portrait-outline"
              title="No Mac connected"
              body="Screenshots are captured on an iOS simulator. Run Yaver on a Mac, signed in as you, and it shows up here."
              action={{
                label: "Set up a machine",
                onPress: () => router.push("/(tabs)/devices" as any),
              }}
            />
          )
        }
        contentContainerStyle={styles.content}
      />
    </SafeAreaView>
  );
}

function makeStyles(c: any) {
  return StyleSheet.create({
    root: { flex: 1, backgroundColor: c.bg },
    center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: c.bg },
    content: { padding: 16, paddingBottom: 48 },
    h1: { fontSize: 24, fontWeight: "700", color: c.text },
    sub: { color: c.textMuted, marginTop: 6, marginBottom: 18, lineHeight: 19 },
    label: {
      color: c.textMuted,
      fontSize: 12,
      fontWeight: "600",
      textTransform: "uppercase",
      marginTop: 18,
      marginBottom: 8,
    },
    row: {
      backgroundColor: c.card,
      borderRadius: 10,
      padding: 14,
      marginBottom: 8,
      borderWidth: 1,
      borderColor: c.border,
    },
    rowOn: { borderColor: c.accent },
    rowName: { color: c.text, fontWeight: "600", fontSize: 15 },
    rowMeta: { color: c.textMuted, fontSize: 12, marginTop: 3 },
    input: {
      backgroundColor: c.card,
      borderRadius: 10,
      borderWidth: 1,
      borderColor: c.border,
      color: c.text,
      padding: 14,
      fontSize: 15,
    },
    chips: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
    chip: {
      paddingVertical: 8,
      paddingHorizontal: 14,
      borderRadius: 999,
      backgroundColor: c.card,
      borderWidth: 1,
      borderColor: c.border,
    },
    chipOn: { backgroundColor: c.accent, borderColor: c.accent },
    chipTxt: { color: c.text, fontWeight: "600", fontSize: 13 },
    chipTxtOn: { color: "#fff" },
    toggleRow: {
      flexDirection: "row",
      alignItems: "center",
      gap: 12,
      marginTop: 20,
      backgroundColor: c.card,
      borderWidth: 1,
      borderColor: c.border,
      borderRadius: 10,
      padding: 14,
    },
    toggleLabel: { color: c.text, fontWeight: "600", fontSize: 15 },
    toggleSub: { color: c.textMuted, fontSize: 12, marginTop: 3, lineHeight: 17 },
    cta: {
      backgroundColor: c.accent,
      borderRadius: 12,
      padding: 16,
      alignItems: "center",
      marginTop: 22,
    },
    ctaOff: { opacity: 0.5 },
    ctaTxt: { color: "#fff", fontWeight: "700", fontSize: 16 },
    note: { color: c.textMuted, marginTop: 12, lineHeight: 18 },
    job: {
      flexDirection: "row",
      alignItems: "center",
      backgroundColor: c.card,
      borderRadius: 10,
      padding: 14,
      marginBottom: 8,
      borderWidth: 1,
      borderColor: c.border,
    },
    jobApp: { color: c.text, fontWeight: "600", fontSize: 14 },
    jobMsg: { color: c.textMuted, fontSize: 12, marginTop: 3 },
    jobStatus: { fontWeight: "700", fontSize: 13 },
    empty: { color: c.textMuted, textAlign: "center", marginTop: 28, lineHeight: 19 },
  });
}
