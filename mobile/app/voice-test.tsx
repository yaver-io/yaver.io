/**
 * /voice-test — on-device mic + speaker test for STT and TTS.
 * Deep-link from anywhere: router.push("/voice-test").
 *
 * Exercises src/lib/speech.ts directly (the same paths the feedback voice
 * input + agent speech use), so it covers the FREE local engine
 * (whisper.rn / OS speech) and any API-key provider. This is the mobile
 * twin of the terminal `yaver voice test`.
 */

import React from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import VoiceTestPanel from "../src/components/VoiceTestPanel";

export default function VoiceTestScreen(): React.JSX.Element {
  const router = useRouter();
  return (
    <SafeAreaView style={styles.root} edges={["top", "left", "right"]}>
      <View style={styles.headerRow}>
        <Pressable onPress={() => router.back()} hitSlop={12}>
          <Ionicons name="chevron-back" size={24} color="#fff" />
        </Pressable>
        <Text style={styles.headerTitle}>Voice test</Text>
        <View style={{ width: 24 }} />
      </View>
      <VoiceTestPanel />
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: "#0a0a0a" },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
  },
  headerTitle: { color: "#fff", fontSize: 18, fontWeight: "600" },
});
