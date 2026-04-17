import React from "react";
import { Pressable, StyleProp, StyleSheet, Text, TextStyle, ViewStyle } from "react-native";
import { useColors } from "../context/ThemeContext";

export function AppBackButton({
  onPress,
  label = "Back",
  style,
  textStyle,
}: {
  onPress: () => void;
  label?: string;
  style?: StyleProp<ViewStyle>;
  textStyle?: StyleProp<TextStyle>;
}) {
  const c = useColors();

  return (
    <Pressable onPress={onPress} style={[styles.button, style]} hitSlop={8}>
      <Text style={[styles.label, { color: c.accent }, textStyle]}>{"\u2039"} {label}</Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  button: {
    paddingVertical: 8,
  },
  label: {
    fontSize: 15,
    fontWeight: "600",
  },
});
