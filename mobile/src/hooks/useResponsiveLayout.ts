import { useMemo } from "react";
import { Platform, useWindowDimensions } from "react-native";
import { breakpoints, layoutTokens } from "../theme/tokens";

export type LayoutClass = "phone" | "tablet-portrait" | "tablet-landscape";

export type ResponsiveLayout = {
  width: number;
  height: number;
  isLandscape: boolean;
  isTablet: boolean;
  isPhone: boolean;
  layoutClass: LayoutClass;
  paneCount: 1 | 2 | 3;
  contentMaxWidth: {
    narrow: number;
    regular: number;
    wide: number;
    full: number;
  };
  gutter: number;
  gridCols: (kind: keyof typeof layoutTokens.gridCols) => number;
  dialog: typeof layoutTokens.dialog;
  rail: typeof layoutTokens.rail;
};

// useResponsiveLayout — single source of truth for tablet/phone
// layout decisions. Reads window size live (rotates with the device)
// and classifies into one of three shells. Keep this hook cheap; it
// runs on every layout change for every consumer.
export function useResponsiveLayout(): ResponsiveLayout {
  const { width, height } = useWindowDimensions();

  return useMemo(() => {
    const isLandscape = width > height;
    // Tablet detection by short-edge: catches portrait iPads (768)
    // and Android tablets while excluding phones in landscape.
    const shortEdge = Math.min(width, height);
    const isTablet = shortEdge >= breakpoints.tablet;
    const isPhone = !isTablet;

    let layoutClass: LayoutClass;
    if (!isTablet) {
      layoutClass = "phone";
    } else if (isLandscape || width >= breakpoints.tabletLandscape) {
      layoutClass = "tablet-landscape";
    } else {
      layoutClass = "tablet-portrait";
    }

    let paneCount: 1 | 2 | 3 = 1;
    if (layoutClass === "tablet-landscape") {
      paneCount = width >= layoutTokens.pane.threeColMinWidth ? 3 : 2;
    }

    const gutter =
      layoutClass === "phone"
        ? layoutTokens.gutter.phone
        : layoutClass === "tablet-portrait"
        ? layoutTokens.gutter.tabletPortrait
        : layoutTokens.gutter.tabletLandscape;

    const gridCols = (kind: keyof typeof layoutTokens.gridCols): number => {
      const cfg = layoutTokens.gridCols[kind];
      if (layoutClass === "phone") return cfg.phone;
      if (layoutClass === "tablet-portrait") return cfg.tabletPortrait;
      return cfg.tabletLandscape;
    };

    return {
      width,
      height,
      isLandscape,
      isTablet,
      isPhone,
      layoutClass,
      paneCount,
      contentMaxWidth: layoutTokens.contentMaxWidth,
      gutter,
      gridCols,
      dialog: layoutTokens.dialog,
      rail: layoutTokens.rail,
    };
  }, [width, height]);
}

// Convenience: clamp a percent-style maxWidth (like chat bubbles)
// so that on tablet widths the bubble doesn't blow out to 800pt.
export function clampedBubbleWidth(
  containerWidth: number,
  pct: number,
  cap = 640,
): number {
  return Math.min(containerWidth * pct, cap);
}

// Convenience: detect if the runtime is an iPad. expo-device
// covers it but adds an import; we infer cheaply from short-edge.
export function isIpad(width: number, height: number): boolean {
  return Platform.OS === "ios" && Math.min(width, height) >= breakpoints.tablet;
}
