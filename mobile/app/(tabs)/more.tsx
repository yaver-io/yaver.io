import React, { useState } from "react";
import { Modal, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { WebView } from "react-native-webview";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";

const TUTORIALS = [
  { label: "Always-on Setup", icon: "\u{1F50C}", desc: "Auto-boot, systemd, run forever", url: "https://yaver.io/manuals/auto-boot" },
  { label: "Self-host Relay", icon: "\u{1F310}", desc: "Your own relay server with Docker", url: "https://yaver.io/manuals/relay-setup" },
  { label: "Local LLM", icon: "\u{1F9E0}", desc: "Ollama, Qwen, zero API keys", url: "https://yaver.io/manuals/local-llm" },
  { label: "Voice AI", icon: "\u{1F3A4}", desc: "PersonaPlex, Whisper, speech-to-code", url: "https://yaver.io/manuals/voice-ai" },
  { label: "Feedback SDK", icon: "\u{1F41B}", desc: "Visual bug reports from your app", url: "https://yaver.io/manuals/feedback-loop" },
  { label: "CLI Setup", icon: "\u{2699}", desc: "Install, auth, configure agents", url: "https://yaver.io/manuals/cli-setup" },
  { label: "Integrations", icon: "\u{1F517}", desc: "MCP, Claude Desktop, Cursor", url: "https://yaver.io/manuals/integrations" },
];

export default function MoreScreen() {
  const c = useColors();
  const router = useRouter();
  const [showTutorials, setShowTutorials] = useState(false);
  const [tutorialUrl, setTutorialUrl] = useState<string | null>(null);

  const items = [
    { label: "Todos", icon: "\u2610", desc: "Task queue — Run All for sleep mode", route: "/(tabs)/todos" },
    { label: "Tutorials", icon: "\u{1F4DA}", desc: "Guides for setup, deploy, voice AI", action: () => setShowTutorials(true) },
    { label: "Settings", icon: "\u2699", desc: "Theme, speech, preferences", route: "/(tabs)/settings" },
  ];

  return (
    <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <ScrollView contentContainerStyle={s.list}>
        {items.map((item) => (
          <Pressable
            key={item.label}
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => item.action ? item.action() : item.route && router.push(item.route as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{item.icon}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>{item.label}</Text>
              <Text style={[s.desc, { color: c.textMuted }]}>{item.desc}</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        ))}
      </ScrollView>

      {/* Tutorials list modal */}
      <Modal visible={showTutorials && !tutorialUrl} animationType="slide">
        <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["top"]}>
          <View style={[s.modalHeader, { borderBottomColor: c.border }]}>
            <Pressable onPress={() => setShowTutorials(false)}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Back</Text>
            </Pressable>
            <Text style={[s.modalTitle, { color: c.textPrimary }]}>Tutorials</Text>
            <View style={{ width: 40 }} />
          </View>
          <ScrollView contentContainerStyle={s.list}>
            {TUTORIALS.map((t) => (
              <Pressable
                key={t.label}
                style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
                onPress={() => setTutorialUrl(t.url)}
              >
                <Text style={[s.icon, { color: c.textMuted }]}>{t.icon}</Text>
                <View style={{ flex: 1 }}>
                  <Text style={[s.label, { color: c.textPrimary }]}>{t.label}</Text>
                  <Text style={[s.desc, { color: c.textMuted }]}>{t.desc}</Text>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
              </Pressable>
            ))}
          </ScrollView>
        </SafeAreaView>
      </Modal>

      {/* Tutorial content WebView */}
      <Modal visible={!!tutorialUrl} animationType="slide">
        <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["top"]}>
          <View style={[s.modalHeader, { borderBottomColor: c.border }]}>
            <Pressable onPress={() => setTutorialUrl(null)}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Back</Text>
            </Pressable>
            <Text style={[s.modalTitle, { color: c.textPrimary }]}>
              {TUTORIALS.find(t => t.url === tutorialUrl)?.label ?? "Tutorial"}
            </Text>
            <View style={{ width: 40 }} />
          </View>
          {tutorialUrl && (
            <WebView
              source={{ uri: tutorialUrl }}
              style={{ flex: 1, backgroundColor: c.bg }}
              javaScriptEnabled
              domStorageEnabled
            />
          )}
        </SafeAreaView>
      </Modal>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  safe: { flex: 1 },
  list: { padding: 16, gap: 8 },
  card: {
    flexDirection: "row",
    alignItems: "center",
    padding: 14,
    borderRadius: 10,
    borderWidth: 1,
    gap: 12,
  },
  icon: { fontSize: 22 },
  label: { fontSize: 15, fontWeight: "600" },
  desc: { fontSize: 12, marginTop: 2 },
  modalHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: 1,
  },
  modalTitle: { fontSize: 17, fontWeight: "700" },
});
