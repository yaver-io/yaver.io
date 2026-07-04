import React from "react";
import { ScrollView, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { GitProviderSection } from "./more";

export default function GitProvidersScreen() {
  const c = useColors();
  const tabletContent = useTabletContentStyle("regular");
  const router = useRouter();
  const insets = useSafeAreaInsets();
  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Git Providers" onBack={() => router.navigate("/(tabs)/more" as any)} style={{ paddingTop: insets.top + 12 }} />
      <ScrollView contentContainerStyle={[{ paddingBottom: 32 }, tabletContent]}>
        <GitProviderSection c={c} />
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingBottom: 12,
    borderBottomWidth: 1,
  },
});
