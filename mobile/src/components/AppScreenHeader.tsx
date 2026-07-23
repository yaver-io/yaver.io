import React from "react";
import { StyleProp, StyleSheet, Text, View, ViewStyle } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../context/ThemeContext";
import { AppBackButton } from "./AppBackButton";

// AppScreenHeader is the canonical app bar for every screen the user
// reaches via the More menu (Settings, Files, Builds, Healthmon, etc.)
// — i.e. every (tabs)/* screen that sets headerShown: false in the tab
// layout because it brings its own header. It mirrors the react-
// navigation default Header used by the visible tabs (Hot Reload,
// Tasks, Projects, Devices, More) so the bar shape stays identical
// across the app.
export function AppScreenHeader({
  title,
  onBack,
  backLabel = "Back",
  right,
  style,
}: {
  title: string;
  onBack: () => void;
  backLabel?: string;
  right?: React.ReactNode;
  style?: StyleProp<ViewStyle>;
}) {
  const c = useColors();
  const insets = useSafeAreaInsets();

  return (
    <View
      style={[
        styles.header,
        {
          backgroundColor: c.bg,
          borderBottomColor: c.border,
          paddingTop: insets.top + 8,
        },
        style,
      ]}
    >
      <View style={styles.sideLeft}>
        <AppBackButton onPress={onBack} label={backLabel} />
      </View>
      <Text style={[styles.title, { color: c.textPrimary }]} numberOfLines={1} ellipsizeMode="tail">
        {title}
      </Text>
      <View style={styles.rightSlot}>{right}</View>
    </View>
  );
}

const styles = StyleSheet.create({
  header: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 12,
    paddingBottom: 10,
    borderBottomWidth: 1,
    gap: 8,
  },
  // Left/right side slots reserve symmetric space so the flexed title stays
  // visually centred and NEVER collides with the Back button or the right
  // actions (a long "project / subdir" title used to overrun both).
  sideLeft: {
    flexShrink: 0,
  },
  title: {
    flex: 1,
    fontSize: 17,
    fontWeight: "700",
    textAlign: "center",
  },
  rightSlot: {
    flexShrink: 0,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "flex-end",
  },
});
