import { Redirect } from "expo-router";
import React from "react";
import { ActivityIndicator, StyleSheet, View } from "react-native";
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
    return <Redirect href="/(tabs)/tasks" />;
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
