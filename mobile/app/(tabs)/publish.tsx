// Publish screen — the end-user "tap Publish, walk away" UI.
//
// Pairs with backend/convex/publishJobs.ts + the /publish-jobs/*
// httpActions + desktop/agent/publish_worker.go. It only ever talks
// to Convex (queue + poll) — never holds a build connection. The
// build runs on the chosen Mac-farm node on its own time; this screen
// just enqueues and watches status. Privacy: no path, no logs ever
// cross this wire (the Convex side enforces it).
//
// Idiom mirrors devices.tsx: useAuth() for the bearer token,
// CONVEX_SITE_URL for the backend, useColors/useTheme for styling.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
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

type PublishJob = {
  jobId: string;
  app: string;
  targets: string[];
  status: string;
  message?: string;
  result?: { target: string; ok: boolean }[];
  createdAt: number;
};

type StoreChoice =
  | "both"
  | "ios"
  | "android"
  | "tv"
  | "android-tv"
  | "tvos"
  | "android-auto"
  | "android-wear"
  | "watchos"
  | "carplay";

// Friendly store word → canonical /deploy/ship target IDs. Same map
// as the CLI façade (publish_ship.go) so both surfaces agree.
const STORE_TARGETS: Record<StoreChoice, string[]> = {
  ios: ["testflight"],
  android: ["playstore"],
  both: ["testflight", "playstore"],
  tv: ["android-tv", "tvos"],
  "android-tv": ["android-tv"],
  tvos: ["tvos"],
  "android-auto": ["playstore"],
  "android-wear": [],
  watchos: [],
  carplay: [],
};

const STORE_CHOICES: StoreChoice[] = [
  "both",
  "ios",
  "android",
  "tv",
  "android-tv",
  "tvos",
  "android-auto",
  "android-wear",
  "watchos",
  "carplay",
];

const STORE_META: Record<
  StoreChoice,
  { label: string; note: string; disabled?: boolean }
> = {
  both: {
    label: "Mobile both",
    note: "Queues TestFlight and Google Play.",
  },
  ios: {
    label: "App Store",
    note: "Queues iOS TestFlight on a Mac.",
  },
  android: {
    label: "Google Play",
    note: "Queues Android phone Play upload.",
  },
  tv: {
    label: "TV both",
    note: "Queues Android TV and Apple TV one after another.",
  },
  "android-tv": {
    label: "Android TV",
    note: "Verifies Leanback and uploads the Play AAB.",
  },
  tvos: {
    label: "Apple TV",
    note: "Queues tvOS archive/upload on a Mac.",
  },
  "android-auto": {
    label: "Android Auto",
    note: "Ships through the Android Play artifact.",
  },
  "android-wear": {
    label: "Wear OS",
    note: "Build lane exists; store upload is not one-tap yet.",
    disabled: true,
  },
  watchos: {
    label: "watchOS",
    note: "Simulator build works; upload needs companion/release lane wiring.",
    disabled: true,
  },
  carplay: {
    label: "CarPlay",
    note: "Needs Apple CarPlay entitlement and native template work first.",
    disabled: true,
  },
};

export default function PublishScreen() {
  const { token } = useAuth();
  const c = useColors();
  const [devices, setDevices] = useState<FarmDevice[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [store, setStore] = useState<StoreChoice>("both");
  const [app, setApp] = useState("");
  const [jobs, setJobs] = useState<PublishJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [queuing, setQueuing] = useState(false);
  const [note, setNote] = useState("");

  const authHeaders = useMemo(
    () => (token ? { Authorization: `Bearer ${token}` } : undefined),
    [token],
  );

  // Only devices that advertised a publish capability are farm nodes.
  const loadDevices = useCallback(async () => {
    if (!authHeaders) return;
    try {
      const r = await fetch(`${CONVEX_SITE_URL}/devices/list?_=${Date.now()}`, {
        headers: authHeaders,
      });
      const j = await r.json();
      const all: FarmDevice[] = j?.devices ?? j ?? [];
      const farm = all.filter((d) => (d.publishCapabilities?.length ?? 0) > 0);
      setDevices(farm);
      setSelected((prev) =>
        prev && farm.some((d) => d.deviceId === prev)
          ? prev
          : farm[0]?.deviceId ?? "",
      );
    } catch {
      setNote("Couldn't load devices — check your connection.");
    } finally {
      setLoading(false);
    }
  }, [authHeaders]);

  const loadJobs = useCallback(async () => {
    if (!authHeaders || !selected) return;
    try {
      const r = await fetch(
        `${CONVEX_SITE_URL}/publish-jobs/list?deviceId=${encodeURIComponent(
          selected,
        )}&limit=20`,
        { headers: authHeaders },
      );
      const j = await r.json();
      setJobs(j?.jobs ?? []);
    } catch {
      /* transient — next poll retries */
    }
  }, [authHeaders, selected]);

  useEffect(() => {
    loadDevices();
  }, [loadDevices]);

  // Poll job status while the screen is open — the "come back to a
  // green check" half. Cheap (one indexed query) so 8 s is fine.
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
    if (STORE_META[store].disabled || STORE_TARGETS[store].length === 0) {
      setNote(STORE_META[store].note);
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
          targets: STORE_TARGETS[store],
          sourceSurface: "mobile",
        }),
      });
      const j = await r.json();
      if (!r.ok || !j?.ok) {
        setNote(j?.error || `Queue failed (HTTP ${r.status}).`);
      } else {
        setNote(
          j.deduped
            ? "Already in flight — joined the existing build."
            : "Queued. You can close the app; it builds on your Mac.",
        );
        loadJobs();
      }
    } catch (e: any) {
      setNote(`Queue failed: ${e?.message ?? "network error"}`);
    } finally {
      setQueuing(false);
    }
  }, [authHeaders, selected, app, store, loadJobs]);

  const styles = makeStyles(c);

  const statusTone = (s: string) =>
    s === "done"
      ? c.success ?? "#3fb950"
      : s === "failed" || s === "expired"
        ? c.error ?? "#f85149"
        : c.accent ?? "#58a6ff";

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
            <Text style={styles.h1}>Publish</Text>
            <Text style={styles.sub}>
              Build & ship mobile, TV, and bundled car artifacts on your own Mac.
              Tap publish and close the app — it runs on the Mac.
            </Text>

            {devices.length === 0 ? (
              <Text style={styles.empty}>
                No publish-capable machine yet. Run `yaver serve` on a Mac
                (signed in as you) — it appears here automatically.
              </Text>
            ) : (
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

                <Text style={styles.label}>Target</Text>
                <View style={styles.segment}>
                  {STORE_CHOICES.map((s) => (
                    <Pressable
                      key={s}
                      disabled={STORE_META[s].disabled}
                      onPress={() => setStore(s)}
                      style={[
                        styles.seg,
                        store === s && styles.segOn,
                        STORE_META[s].disabled && styles.segDisabled,
                      ]}
                    >
                      <Text
                        style={[
                          styles.segTxt,
                          store === s && styles.segTxtOn,
                          STORE_META[s].disabled && styles.segTxtDisabled,
                        ]}
                      >
                        {STORE_META[s].label}
                      </Text>
                    </Pressable>
                  ))}
                </View>
                <Text style={styles.targetNote}>{STORE_META[store].note}</Text>

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

                <Pressable
                  onPress={queue}
                  disabled={queuing || !selected}
                  style={[
                    styles.cta,
                    (queuing || !selected) && styles.ctaOff,
                  ]}
                >
                  {queuing ? (
                    <ActivityIndicator color="#fff" />
                  ) : (
                    <Text style={styles.ctaTxt}>
                      Publish to {STORE_META[store].label}
                    </Text>
                  )}
                </Pressable>

                {note ? <Text style={styles.note}>{note}</Text> : null}
                <Text style={styles.label}>Recent</Text>
              </>
            )}
          </View>
        }
        renderItem={({ item }) => (
          <View style={styles.job}>
            <View style={{ flex: 1 }}>
              <Text style={styles.jobApp}>
                {item.app} → {item.targets.join(" + ")}
              </Text>
              {item.message ? (
                <Text style={styles.jobMsg}>{item.message}</Text>
              ) : null}
            </View>
            <Text
              style={[styles.jobStatus, { color: statusTone(item.status) }]}
            >
              {item.status}
            </Text>
          </View>
        )}
        ListEmptyComponent={
          devices.length > 0 ? (
            <Text style={styles.empty}>No publishes yet.</Text>
          ) : null
        }
        contentContainerStyle={styles.content}
      />
    </SafeAreaView>
  );
}

function makeStyles(c: any) {
  return StyleSheet.create({
    root: { flex: 1, backgroundColor: c.bg },
    center: {
      flex: 1,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.bg,
    },
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
    segment: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
    seg: {
      minWidth: "30%",
      paddingVertical: 12,
      paddingHorizontal: 10,
      borderRadius: 10,
      backgroundColor: c.card,
      borderWidth: 1,
      borderColor: c.border,
      alignItems: "center",
    },
    segOn: { backgroundColor: c.accent, borderColor: c.accent },
    segDisabled: { opacity: 0.45 },
    segTxt: { color: c.text, fontWeight: "600", fontSize: 13 },
    segTxtOn: { color: "#fff" },
    segTxtDisabled: { color: c.textMuted },
    targetNote: { color: c.textMuted, fontSize: 12, marginTop: 8 },
    input: {
      backgroundColor: c.card,
      borderRadius: 10,
      borderWidth: 1,
      borderColor: c.border,
      color: c.text,
      padding: 14,
      fontSize: 15,
    },
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
    jobStatus: { fontWeight: "700", fontSize: 13, textTransform: "uppercase" },
    empty: {
      color: c.textMuted,
      textAlign: "center",
      marginTop: 28,
      lineHeight: 19,
    },
  });
}
