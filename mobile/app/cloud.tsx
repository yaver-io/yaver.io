import React from "react";
import { ScrollView, StyleSheet, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";

import { AppScreenHeader } from "../src/components/AppScreenHeader";
import CloudProvidersSection from "../src/components/CloudProvidersSection";
import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";

export default function CloudScreen() {
  const c = useColors();
  const { token } = useAuth();
  const router = useRouter();
  const leaveCloud = () => {
    if (router.canGoBack()) router.back();
    else router.replace("/(tabs)/more" as any);
  };

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <AppScreenHeader title="Bring your own cloud" onBack={leaveCloud} />
      <ScrollView contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
        <View style={styles.column}>
          <CloudProvidersSection c={c} token={token} initialOpen hideHeader />
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  content: { flexGrow: 1, padding: 16, alignItems: "center" },
  column: { width: "100%", maxWidth: 760 },
});
