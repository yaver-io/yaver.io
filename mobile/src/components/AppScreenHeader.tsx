import React from "react";
import { StyleProp, StyleSheet, Text, View, ViewStyle } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../context/ThemeContext";
import { AppBackButton } from "./AppBackButton";

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
      <AppBackButton onPress={onBack} label={backLabel} />
      <Text style={[styles.title, { color: c.textPrimary }]} numberOfLines={1}>
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
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingBottom: 10,
    borderBottomWidth: 1,
  },
  title: {
    fontSize: 17,
    fontWeight: "700",
  },
  rightSlot: {
    minWidth: 50,
    alignItems: "flex-end",
  },
});
