import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { useDevice, type Device } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import {
  PhoneProject,
  PhonePushTarget,
  PhoneTemplate,
  createPhoneProject,
  createPhoneProjectAt,
  deletePhoneProject,
  listPhoneProjects,
  listPhoneTemplates,
} from "../src/lib/phoneProjects";

type StartMode = "this-device" | "dev-hw" | "yaver-cloud";

const YAVER_CLOUD_BASE = "https://cloud.yaver.io";

function pickDevMachines(all: Device[], currentId: string | undefined): Device[] {
  return all.filter(
    (d) =>
      d.online &&
      !d.isGuest &&
      !d.needsAuth &&
      d.id !== currentId &&
      d.deviceClass !== "edge-mobile",
  );
}

// Phone-first mini-backend list + inline wizard. See MOBILE_WORKER.md §213-419
// and desktop/agent/phone_backend.go.

export default function PhoneProjectsScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus, devices, activeDevice } = useDevice();
  const connected = connectionStatus === "connected";

  const [projects, setProjects] = useState<PhoneProject[]>([]);
  const [templates, setTemplates] = useState<PhoneTemplate[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [template, setTemplate] = useState("todos");
  const [creating, setCreating] = useState(false);

  // yc.md §Wedge Demo — user picks where the mini-backend lives at creation
  // time. "this-device" is the pragmatic default (lives on whichever agent
  // the phone is currently connected to; in practice that's the user's Mac).
  // The three-tier continuum per MOBILE_WORKER.md §Portability Contract:
  // phone → user's own hardware → Yaver managed cloud.
  const [startMode, setStartMode] = useState<StartMode>("this-device");
  const devMachines = useMemo(
    () => pickDevMachines(devices, activeDevice?.id),
    [devices, activeDevice?.id],
  );
  const [selectedDevMachineId, setSelectedDevMachineId] = useState<string | null>(null);
  useEffect(() => {
    if (!selectedDevMachineId && devMachines.length) {
      setSelectedDevMachineId(devMachines[0].id);
    }
  }, [devMachines, selectedDevMachineId]);
  const selectedDevMachine = useMemo(
    () => devMachines.find((d) => d.id === selectedDevMachineId) ?? null,
    [devMachines, selectedDevMachineId],
  );

  const load = useCallback(async () => {
    if (!connected) return;
    setErr(null);
    try {
      const [rows, tpls] = await Promise.all([
        listPhoneProjects(),
        templates.length ? Promise.resolve(templates) : listPhoneTemplates(),
      ]);
      setProjects(rows);
      if (!templates.length) setTemplates(tpls);
    } catch (e: any) {
      setErr(e?.message ?? "failed to load");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [connected, templates]);

  useEffect(() => {
    setLoading(true);
    void load();
  }, [load]);

  async function create() {
    if (!name.trim()) {
      Alert.alert("Phone Backend", "Project name is required");
      return;
    }
    setCreating(true);
    try {
      const spec = { name: name.trim(), template };
      let p: PhoneProject | null = null;
      let where = "";

      if (startMode === "this-device") {
        p = await createPhoneProject(spec);
        where = "on this device";
      } else if (startMode === "dev-hw") {
        if (!selectedDevMachine) {
          throw new Error("No dev machine online. Sign in with Yaver on your Mac/Pi/Linux.");
        }
        const relayHttpUrl = quicClient.activeRelayHttpUrl;
        if (!relayHttpUrl) {
          throw new Error(
            "This phone is connected directly to the current agent, not via relay. Cross-device create needs a relay route.",
          );
        }
        const target: PhonePushTarget = {
          kind: "dev-hw",
          deviceId: selectedDevMachine.id,
          relayHttpUrl,
        };
        p = await createPhoneProjectAt(target, spec);
        where = `on ${selectedDevMachine.name}`;
      } else {
        // yaver-cloud
        const target: PhonePushTarget = { kind: "yaver-cloud", cloudBaseUrl: YAVER_CLOUD_BASE };
        p = await createPhoneProjectAt(target, spec);
        where = "on Yaver Cloud";
      }

      if (!p) throw new Error("target returned no project");
      setName("");
      setShowForm(false);
      // Only refresh the local list — remote projects aren't in the user's
      // connected-agent list, so jumping straight to the detail screen for
      // a local project is the useful UX. For remote, show a toast.
      if (startMode === "this-device") {
        await load();
        router.navigate(`/phone-project/${p.slug}` as any);
      } else {
        Alert.alert(
          "Created",
          `"${p.name}" is now running ${where}. Slug: ${p.slug}. It'll show up here once you connect to that agent.`,
        );
        await load();
      }
    } catch (e: any) {
      Alert.alert("Phone Backend", e?.message ?? "failed to create");
    } finally {
      setCreating(false);
    }
  }

  function pickDevMachine() {
    if (!devMachines.length) {
      Alert.alert(
        "No dev machines online",
        "Install Yaver on your Mac / Pi / Linux and sign in with the same account.",
      );
      return;
    }
    Alert.alert("Pick a dev machine", "Where should this project live?", [
      ...devMachines.map((d) => ({
        text: `${d.name}${d.local ? " (LAN)" : ""}`,
        onPress: () => setSelectedDevMachineId(d.id),
      })),
      { text: "Cancel", style: "cancel" as const },
    ]);
  }

  async function remove(p: PhoneProject) {
    Alert.alert("Delete?", `Remove "${p.name}"? This deletes the SQLite file and manifest.`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          try {
            await deletePhoneProject(p.slug);
            await load();
          } catch (e: any) {
            Alert.alert("Phone Backend", e?.message ?? "failed to delete");
          }
        },
      },
    ]);
  }

  const header = useMemo(
    () => (
      <View style={{ paddingHorizontal: 16, paddingTop: 12 }}>
        <Text style={[styles.h1, { color: c.textPrimary }]}>Phone Backend</Text>
        <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
          Create a mini-backend on your phone. Tables, auth personas, and seed data
          live locally as a portable manifest — promote it to Convex, Supabase,
          Postgres, or Pi/VPS when you're ready.
        </Text>
        {!showForm ? (
          <Pressable
            onPress={() => setShowForm(true)}
            style={[styles.btn, { backgroundColor: c.accent, marginTop: 12 }]}
            disabled={!connected}
          >
            <Text style={[styles.btnText, { color: c.bg }]}>+ New phone project</Text>
          </Pressable>
        ) : (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 12 }]}>
            <Text style={[styles.label, { color: c.textMuted }]}>Project name</Text>
            <TextInput
              value={name}
              onChangeText={setName}
              placeholder="My app"
              placeholderTextColor={c.textMuted}
              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
            />
            <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Where should it live?</Text>
            {(
              [
                {
                  id: "this-device" as StartMode,
                  label: "This device",
                  sub: activeDevice?.name ? `→ ${activeDevice.name}` : "The agent you're currently connected to",
                },
                {
                  id: "dev-hw" as StartMode,
                  label: "Your Dev Machine",
                  sub: selectedDevMachine
                    ? `→ ${selectedDevMachine.name}${devMachines.length > 1 ? " (tap to change)" : ""}`
                    : devMachines.length
                      ? "Pick a paired Mac / Pi / Linux box"
                      : "No other devices paired yet",
                  disabled: devMachines.length === 0,
                  onLongPress: pickDevMachine,
                },
                {
                  id: "yaver-cloud" as StartMode,
                  label: "Yaver Cloud",
                  sub: YAVER_CLOUD_BASE.replace(/^https?:\/\//, "") + " · managed Hetzner tenant",
                },
              ] as const
            ).map((opt) => (
              <Pressable
                key={opt.id}
                onPress={() => {
                  if ((opt as any).disabled) {
                    (opt as any).onLongPress?.();
                    return;
                  }
                  setStartMode(opt.id);
                  if (opt.id === "dev-hw" && devMachines.length > 1) {
                    pickDevMachine();
                  }
                }}
                onLongPress={(opt as any).onLongPress}
                style={[
                  styles.templateRow,
                  {
                    backgroundColor: startMode === opt.id ? c.accent + "22" : "transparent",
                    borderColor: startMode === opt.id ? c.accent : c.border,
                    opacity: (opt as any).disabled ? 0.5 : 1,
                  },
                ]}
              >
                <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{opt.label}</Text>
                <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={2}>
                  {opt.sub}
                </Text>
              </Pressable>
            ))}

            <Text style={[styles.label, { color: c.textMuted, marginTop: 12 }]}>Template</Text>
            {templates.map((t) => (
              <Pressable
                key={t.id}
                onPress={() => setTemplate(t.id)}
                style={[
                  styles.templateRow,
                  {
                    backgroundColor: template === t.id ? c.accent + "22" : "transparent",
                    borderColor: template === t.id ? c.accent : c.border,
                  },
                ]}
              >
                <Text style={[styles.templateLabel, { color: c.textPrimary }]}>{t.label}</Text>
                <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={2}>
                  {t.description}
                </Text>
              </Pressable>
            ))}
            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable
                onPress={() => setShowForm(false)}
                style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
              >
                <Text style={[styles.btnText, { color: c.textPrimary }]}>Cancel</Text>
              </Pressable>
              <Pressable
                onPress={create}
                disabled={creating}
                style={[styles.btn, { backgroundColor: c.accent, flex: 1, opacity: creating ? 0.6 : 1 }]}
              >
                {creating ? (
                  <ActivityIndicator color={c.bg} />
                ) : (
                  <Text style={[styles.btnText, { color: c.bg }]}>Create</Text>
                )}
              </Pressable>
            </View>
          </View>
        )}
        {err ? (
          <Text style={[styles.muted, { color: "#ff6b6b", marginTop: 12 }]}>{err}</Text>
        ) : null}
      </View>
    ),
    [
      c,
      connected,
      creating,
      err,
      name,
      showForm,
      template,
      templates,
      startMode,
      activeDevice,
      selectedDevMachine,
      devMachines.length,
    ],
  );

  if (!connected) {
    return (
      <View style={[styles.empty, { backgroundColor: c.bg }]}>
        <Text style={{ color: c.textMuted }}>Connect to a device to use the phone backend.</Text>
      </View>
    );
  }

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <FlatList
        data={projects}
        keyExtractor={(p) => p.slug}
        contentContainerStyle={{ paddingBottom: 80 + insets.bottom, paddingTop: insets.top }}
        ListHeaderComponent={header}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={() => {
              setRefreshing(true);
              void load();
            }}
            tintColor={c.textMuted}
          />
        }
        ListEmptyComponent={
          loading ? (
            <ActivityIndicator style={{ marginTop: 32 }} color={c.textMuted} />
          ) : (
            <Text style={[styles.muted, { color: c.textMuted, textAlign: "center", marginTop: 32 }]}>
              No phone projects yet.
            </Text>
          )
        }
        renderItem={({ item }) => (
          <View style={{ paddingHorizontal: 16, paddingTop: 12 }}>
            <Pressable
              onPress={() => router.navigate(`/phone-project/${item.slug}` as any)}
              onLongPress={() => remove(item)}
              style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            >
              <Text style={[styles.projectName, { color: c.textPrimary }]}>{item.name}</Text>
              <Text style={[styles.muted, { color: c.textMuted }]} numberOfLines={1}>
                {item.slug}
                {item.template ? ` · ${item.template}` : ""}
              </Text>
              {item.stats ? (
                <Text style={[styles.stats, { color: c.textMuted }]}>
                  {item.stats.tableCount} table{item.stats.tableCount === 1 ? "" : "s"} · {item.stats.rowCount} row
                  {item.stats.rowCount === 1 ? "" : "s"} · {formatBytes(item.stats.dbBytes)}
                </Text>
              ) : null}
            </Pressable>
          </View>
        )}
      />
    </KeyboardAvoidingView>
  );
}

function formatBytes(n: number): string {
  if (!n) return "0 B";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

const styles = StyleSheet.create({
  h1: { fontSize: 24, fontWeight: "700" },
  muted: { fontSize: 13 },
  btn: { paddingVertical: 12, borderRadius: 8, alignItems: "center" },
  btnSecondary: {
    paddingVertical: 12,
    borderRadius: 8,
    alignItems: "center",
    borderWidth: 1,
  },
  btnText: { fontWeight: "600", fontSize: 15 },
  card: {
    borderWidth: 1,
    borderRadius: 10,
    padding: 14,
  },
  label: { fontSize: 12, fontWeight: "500", marginBottom: 4 },
  input: {
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    fontSize: 15,
  },
  templateRow: {
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    marginTop: 6,
  },
  templateLabel: { fontWeight: "600", fontSize: 14 },
  projectName: { fontSize: 17, fontWeight: "600" },
  stats: { fontSize: 12, marginTop: 6 },
  empty: { flex: 1, alignItems: "center", justifyContent: "center" },
});
