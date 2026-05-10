import React from "react";
import { StyleProp, View, ViewStyle } from "react-native";
import { useColors } from "../../context/ThemeContext";
import { ResponsiveContent } from "./ResponsiveContent";

// ScreenScaffold — wraps a screen body with theme background and
// (optionally) a tablet content cap. Use this on screens that are
// fundamentally single-column with no split-pane: settings sub-pages,
// status views, simple lists.
export function ScreenScaffold({
  children,
  contentWidth = "regular",
  unbounded = false,
  style,
}: {
  children: React.ReactNode;
  contentWidth?: "narrow" | "regular" | "wide" | "full";
  unbounded?: boolean;
  style?: StyleProp<ViewStyle>;
}) {
  const c = useColors();
  if (unbounded) {
    return <View style={[{ flex: 1, backgroundColor: c.bg }, style]}>{children}</View>;
  }
  return (
    <View style={[{ flex: 1, backgroundColor: c.bg }, style]}>
      <ResponsiveContent width={contentWidth}>{children}</ResponsiveContent>
    </View>
  );
}
