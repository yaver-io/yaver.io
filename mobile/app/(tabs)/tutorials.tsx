// Tutorials — moved out of the bottom-card Modal in more.tsx into a
// proper hidden tab route, so opening Tutorials from the More page
// uses the same push-style navigation as Quality Gates and the rest of
// the More-tab destinations (Todos, Settings, Pair Device, etc.). The
// previous version popped a slide-up Modal which felt like a sheet,
// inconsistent with every other Developer-Tools entry.

import React, { useState } from "react";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { WebView } from "react-native-webview";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";

const TUTORIALS = [
  { label: "Always-on Setup", icon: "↗", desc: "Auto-boot, systemd, run forever", url: "https://yaver.io/manuals/auto-boot?embed=mobile" },
  { label: "Self-host Relay", icon: "⊕", desc: "Your own relay server with Docker", url: "https://yaver.io/manuals/relay-setup?embed=mobile" },
  { label: "Local LLM", icon: "◇", desc: "Ollama, Qwen, zero API keys", url: "https://yaver.io/manuals/local-llm?embed=mobile" },
  { label: "Voice AI", icon: "•", desc: "PersonaPlex, Whisper, speech-to-code", url: "https://yaver.io/manuals/voice-ai?embed=mobile" },
  { label: "Feedback SDK", icon: "○", desc: "Visual bug reports from your app", url: "https://yaver.io/manuals/feedback-loop?embed=mobile" },
  { label: "CLI Setup", icon: "⚙", desc: "Install, auth, configure agents", url: "https://yaver.io/manuals/cli-setup?embed=mobile" },
  { label: "Integrations", icon: "←", desc: "MCP, Claude Desktop, Cursor", url: "https://yaver.io/manuals/integrations?embed=mobile" },
];

export default function TutorialsScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const [openUrl, setOpenUrl] = useState<string | null>(null);

  if (openUrl) {
    const tutorial = TUTORIALS.find((t) => t.url === openUrl);
    return (
      <View style={[s.safe, { backgroundColor: c.bg }]}>
        <AppScreenHeader
          title={tutorial?.label ?? "Tutorial"}
          onBack={() => setOpenUrl(null)}
          style={{ paddingTop: insets.top + 12 }}
        />
        <WebView
          source={{ uri: openUrl }}
          style={{ flex: 1, backgroundColor: c.bg }}
          javaScriptEnabled
          domStorageEnabled
        />
      </View>
    );
  }

  return (
    <View style={[s.safe, { backgroundColor: c.bg }]}>
      <AppScreenHeader
        title="Tutorials"
        onBack={() => router.navigate("/(tabs)/more" as any)}
        style={{ paddingTop: insets.top + 12 }}
      />
      <ScrollView contentContainerStyle={s.list}>
        {TUTORIALS.map((t) => (
          <Pressable
            key={t.label}
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => setOpenUrl(t.url)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{t.icon}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>{t.label}</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                {t.desc}
              </Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"›"}</Text>
          </Pressable>
        ))}
      </ScrollView>
    </View>
  );
}

const s = StyleSheet.create({
  safe: { flex: 1 },
  list: { padding: 16, paddingTop: 12, paddingBottom: 28, gap: 10 },
  card: {
    flexDirection: "row",
    alignItems: "center",
    gap: 14,
    padding: 16,
    borderRadius: 16,
    borderWidth: 1,
  },
  icon: {
    fontSize: 18,
    width: 24,
    textAlign: "center",
  },
  label: {
    fontSize: 15,
    fontWeight: "600",
  },
  desc: {
    fontSize: 12,
    marginTop: 4,
    lineHeight: 16,
  },
});
