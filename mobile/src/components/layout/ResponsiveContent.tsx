import React from "react";
import { StyleProp, View, ViewStyle } from "react-native";
import { useResponsiveLayout, ResponsiveLayout } from "../../hooks/useResponsiveLayout";

type ContentWidth = "narrow" | "regular" | "wide" | "full";

// ResponsiveContent caps content width on tablets while letting
// phones go edge-to-edge. The wrapper centers itself in its
// parent and only injects a maxWidth when the device is in a
// tablet class — so phone layouts stay byte-identical.
//
// Use `width="regular"` for primary content lists/chat. Use
// `narrow` for forms. Use `wide` only when the screen has a real
// reason to be wide (feeds, multi-column cards). Use `full` to
// opt out (split-pane shells set `full` on each pane).
export function ResponsiveContent({
  children,
  width = "regular",
  style,
  paddingHorizontal,
  paddingTop,
  paddingBottom,
  centered = true,
}: {
  children: React.ReactNode;
  width?: ContentWidth;
  style?: StyleProp<ViewStyle>;
  paddingHorizontal?: number;
  paddingTop?: number;
  paddingBottom?: number;
  centered?: boolean;
}) {
  const layout = useResponsiveLayout();
  const max = layout.contentMaxWidth[width];
  const isPhone = layout.layoutClass === "phone";

  const padH = paddingHorizontal ?? layout.gutter;

  return (
    <View
      style={[
        {
          width: "100%",
          alignItems: centered ? "center" : "stretch",
          paddingTop,
          paddingBottom,
        },
        style,
      ]}
    >
      <View
        style={{
          width: "100%",
          maxWidth: isPhone ? undefined : max,
          paddingHorizontal: padH,
        }}
      >
        {children}
      </View>
    </View>
  );
}

// Hook variant for cases where consumers need just the maxWidth
// (for FlatList contentContainerStyle etc.).
export function useContentMaxWidth(width: ContentWidth = "regular"): number | undefined {
  const layout = useResponsiveLayout();
  if (layout.layoutClass === "phone") return undefined;
  return layout.contentMaxWidth[width];
}

export type { ResponsiveLayout };
