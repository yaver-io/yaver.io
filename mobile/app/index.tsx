import { Redirect } from "expo-router";
import React from "react";
import { ActivityIndicator, Platform, StyleSheet, View } from "react-native";
import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";

export default function IndexScreen() {
  const { isAuthenticated, isLoading, surveyCompleted } = useAuth();
  const c = useColors();

  if (isLoading) {
    return (
      <View style={[styles.container, { backgroundColor: c.bg }]}>
        <ActivityIndicator size="large" color={c.accent} />
      </View>
    );
  }

  if (isAuthenticated) {
    if (!surveyCompleted) {
      return <Redirect href="/survey" />;
    }
    // On a TV the phone tab bar is cramped and not focus-friendly on a remote —
    // route to the 10-foot lean-back launcher instead (app/tv-home.tsx).
    if (Platform.isTV) {
      return <Redirect href="/tv-home" />;
    }
    return <Redirect href="/(tabs)/tasks" />;
  }

  // On a TV (Apple TV / Google TV) typing credentials is painful — use the
  // QR / device-code flow instead of the browser-OAuth login screen.
  if (Platform.isTV) {
    return <Redirect href="/tv-signin" />;
  }

  return <Redirect href="/login" />;
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
  },
});
