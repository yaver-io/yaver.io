import React from "react";
import { Linking, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";

export default function MoreScreen() {
  const c = useColors();
  const router = useRouter();

  const items = [
    { label: "Todos", icon: "\u2610", desc: "Local task lists by project", route: "/(tabs)/todos" },
    { label: "Settings", icon: "\u2699", desc: "Theme, speech, preferences", route: "/(tabs)/settings" },
  ];

  const tutorials = [
    { label: "Always-on Setup", icon: "\u{1F50C}", desc: "Auto-boot, systemd, run forever", url: "https://yaver.io/manuals/auto-boot" },
    { label: "Self-host Relay", icon: "\u{1F310}", desc: "Your own relay server with Docker", url: "https://yaver.io/manuals/relay-setup" },
    { label: "Local LLM", icon: "\u{1F9E0}", desc: "Ollama, Qwen, zero API keys", url: "https://yaver.io/manuals/local-llm" },
    { label: "Voice AI", icon: "\u{1F3A4}", desc: "PersonaPlex, Whisper, speech-to-code", url: "https://yaver.io/manuals/voice-ai" },
    { label: "Feedback SDK", icon: "\u{1F41B}", desc: "Visual bug reports from your app", url: "https://yaver.io/manuals/feedback-loop" },
  ];

  return (
    <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <ScrollView contentContainerStyle={s.list}>
        {items.map((item) => (
          <Pressable
            key={item.label}
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.push(item.route as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{item.icon}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>{item.label}</Text>
              <Text style={[s.desc, { color: c.textMuted }]}>{item.desc}</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        ))}

        <Text style={[s.sectionTitle, { color: c.textMuted }]}>Tutorials</Text>
        {tutorials.map((t) => (
          <Pressable
            key={t.label}
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => Linking.openURL(t.url)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{t.icon}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>{t.label}</Text>
              <Text style={[s.desc, { color: c.textMuted }]}>{t.desc}</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>{"\u2197"}</Text>
          </Pressable>
        ))}
      </ScrollView>
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
  sectionTitle: { fontSize: 11, fontWeight: "600", textTransform: "uppercase" as const, letterSpacing: 1, marginTop: 16, marginBottom: 4 },
});
