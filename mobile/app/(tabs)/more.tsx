import React from "react";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
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
});
