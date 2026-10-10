import React, { useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import { resolveStudioDefaults, studioLanesFor, type StudioLane, type StudioRunner } from "../src/lib/studioWorkspace";

type Project = { name: string; path: string; framework?: string; surfaces?: string[] };

export default function StudioConfigScreen() {
  const colors = useColors();
  const router = useRouter();
  const { devices, activeDevice, selectDevice, connectionStatus } = useDevice();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectPath, setProjectPath] = useState("");
  const [lane, setLane] = useState<StudioLane>("device");
  const [runner, setRunner] = useState<StudioRunner>("shell");
  const [voiceInputEnabled, setVoiceInputEnabled] = useState(false);
  const [voiceOutputEnabled, setVoiceOutputEnabled] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const project = projects.find((item) => item.path === projectPath) || projects[0];
  const lanes = useMemo(() => studioLanesFor(project?.framework, project?.surfaces), [project]);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setLoadError("");
    Promise.all([
      quicClient.listProjects(true),
      AsyncStorage.getItem("yaver:studio:active"),
    ]).then(async ([list, activeRaw]) => {
      if (!alive) return;
      const mapped = (list || []).map((item) => ({ name: item.name, path: item.path, framework: item.framework, surfaces: item.surfaces })).filter((item) => item.name && item.path);
      let active: { projectPath?: string } | null = null;
      try { active = activeRaw ? JSON.parse(activeRaw) : null; } catch {}
      const selected = mapped.find((item) => item.path === active?.projectPath) || mapped[0];
      setProjects(mapped);
      setProjectPath(selected?.path || "");
      if (selected) {
        const raw = await AsyncStorage.getItem(`yaver:studio:last:${selected.path}`).catch(() => null);
        let saved = null;
        try { saved = raw ? JSON.parse(raw) : null; } catch {}
        const defaults = resolveStudioDefaults(saved, selected.framework, selected.surfaces);
        setLane(defaults.lane);
        setRunner(defaults.runner);
        setVoiceInputEnabled(defaults.voiceInputEnabled);
        setVoiceOutputEnabled(defaults.voiceOutputEnabled);
      }
    }).catch((error) => {
      if (!alive) return;
      setProjects([]);
      setProjectPath("");
      setLoadError(error instanceof Error ? error.message : "The selected machine is not connected.");
    }).finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [activeDevice?.id]);

  const chooseProject = async (next: Project) => {
    setProjectPath(next.path);
    const raw = await AsyncStorage.getItem(`yaver:studio:last:${next.path}`).catch(() => null);
    let saved = null;
    try { saved = raw ? JSON.parse(raw) : null; } catch {}
    const defaults = resolveStudioDefaults(saved, next.framework, next.surfaces);
    setLane(defaults.lane);
    setRunner(defaults.runner);
    setVoiceInputEnabled(defaults.voiceInputEnabled);
    setVoiceOutputEnabled(defaults.voiceOutputEnabled);
  };

  const save = async () => {
    if (!project) return;
    const defaults = resolveStudioDefaults({ lane, runner, splitRatio: 0.3, tmuxSession: "yaver-studio", voiceInputEnabled, voiceOutputEnabled }, project.framework, project.surfaces);
    await Promise.all([
      AsyncStorage.setItem(`yaver:studio:last:${project.path}`, JSON.stringify(defaults)),
      AsyncStorage.setItem("yaver:studio:active", JSON.stringify({ projectPath: project.path, deviceId: activeDevice?.id || "" })),
    ]);
    router.replace("/vibe-studio" as any);
  };

  return (
    <View style={[styles.root, { backgroundColor: colors.bg }]}>
      <AppScreenHeader title="Studio Configuration" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={styles.content}>
        <Text style={[styles.note, { color: colors.textMuted }]}>Studio stays clean. These defaults control the 30% lane and 70% SSH workspace.</Text>
        <Section title="Machine" colors={colors}>
          <View style={styles.choices}>{devices.map((device) => <Choice key={device.id} label={device.alias ? `@${device.alias}` : device.name} selected={activeDevice?.id === device.id} onPress={() => void selectDevice(device)} colors={colors} />)}</View>
        </Section>
        <Section title="Project" colors={colors}>
          {loading ? <ActivityIndicator color={colors.accent} /> : loadError ? (
            <Text style={[styles.note, { color: colors.textMuted }]}>Connect or re-select a machine to load its projects.</Text>
          ) : <View style={styles.choices}>{projects.map((item) => <Choice key={item.path} label={item.name} selected={project?.path === item.path} onPress={() => void chooseProject(item)} colors={colors} />)}</View>}
        </Section>
        <Section title="Lane" colors={colors}>
          <View style={styles.choices}>{lanes.map((item) => <Choice key={item} label={item === "live" ? "Runtime" : item} selected={lane === item} onPress={() => setLane(item)} colors={colors} />)}</View>
        </Section>
        <Section title="Runner inside SSH" colors={colors}>
          <View style={styles.choices}>{(["shell", "codex", "claude", "opencode"] as StudioRunner[]).map((item) => <Choice key={item} label={item === "shell" ? "Shell / tmux" : item} selected={runner === item} onPress={() => setRunner(item)} colors={colors} />)}</View>
        </Section>
        <Section title="Voice" colors={colors}>
          <View style={styles.choices}>
            <Choice label="STT input" selected={voiceInputEnabled} onPress={() => setVoiceInputEnabled((value) => !value)} colors={colors} />
            <Choice label="TTS output" selected={voiceOutputEnabled} onPress={() => setVoiceOutputEnabled((value) => !value)} colors={colors} />
          </View>
        </Section>
        <View style={[styles.fixed, { borderColor: colors.border }]}><Text style={{ color: colors.textPrimary, fontWeight: "700" }}>Layout · Lane 30% / SSH 70%</Text></View>
        <Pressable disabled={!project || connectionStatus !== "connected"} onPress={() => void save()} style={[styles.save, { backgroundColor: colors.accent, opacity: project && connectionStatus === "connected" ? 1 : 0.45 }]}><Text style={styles.saveText}>Save and open Studio</Text></Pressable>
      </ScrollView>
    </View>
  );
}

function Section({ title, colors, children }: { title: string; colors: ReturnType<typeof useColors>; children: React.ReactNode }) {
  return <View style={[styles.section, { borderColor: colors.border, backgroundColor: colors.bgCard }]}><Text style={[styles.label, { color: colors.textMuted }]}>{title.toUpperCase()}</Text>{children}</View>;
}
function Choice({ label, selected, onPress, colors }: { label: string; selected: boolean; onPress: () => void; colors: ReturnType<typeof useColors> }) {
  return <Pressable onPress={onPress} style={[styles.choice, { borderColor: selected ? colors.accent : colors.border, backgroundColor: selected ? colors.accentSoft : colors.bg }]}><Text style={{ color: selected ? colors.accent : colors.textSecondary, fontWeight: "700" }}>{label}</Text></Pressable>;
}
const styles = StyleSheet.create({ root: { flex: 1 }, content: { width: "100%", maxWidth: 720, alignSelf: "center", padding: 20, gap: 12 }, note: { fontSize: 13, lineHeight: 19 }, section: { borderWidth: 1, borderRadius: 12, padding: 12, gap: 10 }, label: { fontSize: 9, fontWeight: "900", letterSpacing: 1 }, choices: { flexDirection: "row", flexWrap: "wrap", gap: 8 }, choice: { borderWidth: 1, borderRadius: 9, paddingHorizontal: 11, paddingVertical: 9 }, fixed: { borderWidth: 1, borderRadius: 12, padding: 14 }, save: { minHeight: 48, borderRadius: 12, alignItems: "center", justifyContent: "center" }, saveText: { color: "#fff", fontWeight: "800" } });
