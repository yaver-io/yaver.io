import { useMemo } from "react";
import { ViewStyle } from "react-native";
import { useResponsiveLayout } from "./useResponsiveLayout";

// useTabletContentStyle — drop-in addition to a ScrollView /
// FlatList contentContainerStyle. Returns an empty object on
// phones (zero behaviour change) and a centered max-width clamp
// on tablets so existing screens get reading-line-friendly
// columns without any structural rewrite.
//
// Usage:
//   const tabletContent = useTabletContentStyle("regular");
//   <ScrollView contentContainerStyle={[styles.scroll, tabletContent]} ... />
export function useTabletContentStyle(
  width: "narrow" | "regular" | "wide" | "full" = "regular",
): ViewStyle {
  const layout = useResponsiveLayout();
  return useMemo(() => {
    if (layout.layoutClass === "phone") return {};
    const max = layout.contentMaxWidth[width];
    if (!isFinite(max)) return {};
    return {
      maxWidth: max,
      alignSelf: "center",
      width: "100%",
    };
  }, [layout.layoutClass, layout.contentMaxWidth, width]);
}
