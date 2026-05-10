import React from "react";
import { StyleProp, View, ViewStyle } from "react-native";
import { useColors } from "../../context/ThemeContext";
import { useResponsiveLayout } from "../../hooks/useResponsiveLayout";
import { layoutTokens } from "../../theme/tokens";

// SplitPane renders left+right (and optional far-right) panes on
// tablet-landscape. On phone and tablet-portrait it renders only
// the active pane (the consumer toggles which pane is active),
// preserving the existing single-pane navigation flow.
//
// Consumers pass `mode` to control behavior:
//   - "list-detail": list left, detail right; on small screens
//     show whichever pane the consumer marks active.
//   - "rail-detail": narrow rail left + wide detail right; rail
//     stays visible at all sizes (used for Settings sections).
//   - "three": list / detail / preview triple-pane; on
//     tablet-portrait collapses to two; on phone collapses to one.
export function SplitPane({
  list,
  detail,
  preview,
  active = "detail",
  mode = "list-detail",
  listWidth,
  railCollapsedWidth,
  style,
}: {
  list: React.ReactNode;
  detail: React.ReactNode;
  preview?: React.ReactNode;
  active?: "list" | "detail" | "preview";
  mode?: "list-detail" | "rail-detail" | "three";
  listWidth?: number;
  railCollapsedWidth?: number;
  style?: StyleProp<ViewStyle>;
}) {
  const layout = useResponsiveLayout();
  const c = useColors();

  // Phone — render only the active pane. The single-pane phone
  // experience never changes.
  if (layout.layoutClass === "phone") {
    return (
      <View style={[{ flex: 1, backgroundColor: c.bg }, style]}>
        {active === "list" && list}
        {active === "detail" && detail}
        {active === "preview" && (preview ?? detail)}
      </View>
    );
  }

  // Tablet portrait — list-detail collapses to one pane; rail-detail
  // keeps the narrow rail visible; three-pane drops the preview.
  if (layout.layoutClass === "tablet-portrait") {
    if (mode === "rail-detail") {
      const rw = railCollapsedWidth ?? layoutTokens.rail.width;
      return (
        <View style={[{ flex: 1, flexDirection: "row", backgroundColor: c.bg }, style]}>
          <View style={{ width: rw, borderRightWidth: 1, borderRightColor: c.border }}>
            {list}
          </View>
          <View style={{ flex: 1 }}>{detail}</View>
        </View>
      );
    }
    if (mode === "three") {
      const lw = listWidth ?? layoutTokens.pane.minListWidth;
      return (
        <View style={[{ flex: 1, flexDirection: "row", backgroundColor: c.bg }, style]}>
          <View
            style={{
              width: active === "preview" ? 0 : lw,
              borderRightWidth: active === "preview" ? 0 : 1,
              borderRightColor: c.border,
              overflow: "hidden",
            }}
          >
            {list}
          </View>
          <View style={{ flex: 1 }}>{active === "preview" ? (preview ?? detail) : detail}</View>
        </View>
      );
    }
    // list-detail on portrait: still single-pane (toggle).
    return (
      <View style={[{ flex: 1, backgroundColor: c.bg }, style]}>
        {active === "list" && list}
        {active !== "list" && detail}
      </View>
    );
  }

  // Tablet landscape — true split.
  const lw = listWidth ?? Math.max(layoutTokens.pane.minListWidth, Math.min(layoutTokens.pane.maxListWidth, layout.width * 0.32));

  if (mode === "three" && preview && layout.paneCount === 3) {
    const sideW = Math.max(layoutTokens.pane.minListWidth, Math.min(420, layout.width * 0.28));
    return (
      <View style={[{ flex: 1, flexDirection: "row", backgroundColor: c.bg }, style]}>
        <View style={{ width: sideW, borderRightWidth: 1, borderRightColor: c.border }}>{list}</View>
        <View style={{ flex: 1, borderRightWidth: 1, borderRightColor: c.border }}>{detail}</View>
        <View style={{ width: sideW, minWidth: layoutTokens.pane.detailMinWidth }}>{preview}</View>
      </View>
    );
  }

  return (
    <View style={[{ flex: 1, flexDirection: "row", backgroundColor: c.bg }, style]}>
      <View style={{ width: lw, borderRightWidth: 1, borderRightColor: c.border }}>{list}</View>
      <View style={{ flex: 1 }}>{mode === "three" && active === "preview" ? (preview ?? detail) : detail}</View>
    </View>
  );
}
