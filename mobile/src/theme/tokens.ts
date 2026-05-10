export const spacing = {
  xs: 4,
  sm: 8,
  md: 12,
  lg: 16,
  xl: 20,
  xxl: 24,
  xxxl: 32,
} as const;

// Width breakpoints used by useResponsiveLayout. Anything below
// `tablet` is treated as a phone; tablet-portrait flips into
// landscape behaviour above `tabletLandscape`. Foldables that
// stretch into the 600-900pt band land in tablet-portrait, which
// is intentional — single-pane with grids feels right at that
// size.
export const breakpoints = {
  tablet: 600,
  tabletLandscape: 900,
  desktop: 1200,
} as const;

// Tablet content scaffolding. `contentMaxWidth.*` constrains
// reading-line lengths so wide displays don't stretch single
// columns edge-to-edge. `pane.*` defines two/three-pane shells.
export const layoutTokens = {
  contentMaxWidth: {
    narrow: 560,    // forms, settings rows, single chat column
    regular: 720,   // primary content (chat, lists, tasks)
    wide: 960,      // feeds with rich cards
    full: Number.POSITIVE_INFINITY,
  },
  pane: {
    minListWidth: 320,
    maxListWidth: 420,
    detailMinWidth: 480,
    threeColMinWidth: 1100,
    gap: 1,         // hairline divider between panes; cards inside add their own gutters
  },
  rail: {
    width: 88,           // collapsed icon rail
    expandedWidth: 240,  // when expanded with labels
  },
  // Modal scale presets. Used by AdaptiveDialog. On phone every
  // size collapses to bottom-sheet/full-screen; on tablet they
  // become centered cards.
  dialog: {
    compact: 320,        // confirm prompts
    form: 460,           // simple forms (auth, target picker)
    sheet: 600,          // pageSheet equivalent
    wide: 760,           // multi-column / preview dialogs
  },
  // Tablet content gutters — bigger than phone padding so cards
  // breathe on big screens.
  gutter: {
    phone: 14,
    tabletPortrait: 24,
    tabletLandscape: 32,
  },
  // Grid column counts by layout class. Used by FlatList numColumns.
  gridCols: {
    devices:  { phone: 1, tabletPortrait: 2, tabletLandscape: 2 },
    repos:    { phone: 1, tabletPortrait: 3, tabletLandscape: 4 },
    // Projects tab list (the cards under the search input). Capped at
    // 2 cols on every tablet shell so card titles + paths breathe —
    // reusing the `repos` token squeezed long monorepo names and
    // crowded the chevron against the right edge.
    projects: { phone: 1, tabletPortrait: 2, tabletLandscape: 2 },
    vibing:   { phone: 2, tabletPortrait: 3, tabletLandscape: 4 },
    metrics:  { phone: 2, tabletPortrait: 3, tabletLandscape: 4 },
  },
} as const;

export type ContentWidthKey = keyof typeof layoutTokens.contentMaxWidth;
export type DialogSizeKey = keyof typeof layoutTokens.dialog;

// SF Mono ships with iOS but isn't installed by name; Menlo is the
// system mono that picks SF Mono glyphs. On Android we fall back to
// the platform monospace family.
import { Platform } from "react-native";
export const monoFamily = Platform.OS === "ios" ? "Menlo" : "monospace";

export const typography = {
  navTitle: { fontSize: 17, fontWeight: "600" as const },
  cardTitle: { fontSize: 17, fontWeight: "600" as const },
  pageTitle: { fontSize: 28, fontWeight: "700" as const },
  body: { fontSize: 15, fontWeight: "400" as const },
  bodyStrong: { fontSize: 15, fontWeight: "600" as const },
  caption: { fontSize: 13, fontWeight: "400" as const },
  captionStrong: { fontSize: 13, fontWeight: "600" as const },
  badge: { fontSize: 11, fontWeight: "600" as const, letterSpacing: 0.4 },
  badgeSm: { fontSize: 10, fontWeight: "600" as const, letterSpacing: 0.4 },
  path: { fontSize: 13, fontWeight: "400" as const },
  // Mono variants for terminal-style content (file paths, command
  // strings, tool call names, error message bodies with flags). Keep
  // the mono surface narrow — see X2 typography rules in the
  // task-chat polish pass.
  monoBody: { fontSize: 14, fontFamily: monoFamily, fontWeight: "400" as const },
  monoCaption: { fontSize: 12, fontFamily: monoFamily, fontWeight: "400" as const },
} as const;

export const lightCardShadow = {
  shadowColor: "#000",
  shadowOffset: { width: 0, height: 1 },
  shadowOpacity: 0.04,
  shadowRadius: 8,
  elevation: 1,
} as const;

export const lightBrandShadow = {
  shadowColor: "#6E56F6",
  shadowOffset: { width: 0, height: 4 },
  shadowOpacity: 0.32,
  shadowRadius: 16,
  elevation: 6,
} as const;

export const lightTokens = {
  background: "#F7F7F9",
  surface: "#FFFFFF",
  surfaceElevated: "#FFFFFF",
  surfaceMuted: "#EFEFF3",
  textPrimary: "#0A0A0F",
  textSecondary: "#5A5A66",
  textTertiary: "#9A9AA5",
  textDisabled: "#C7C7D1",
  brandPrimary: "#6E56F6",
  brandPrimarySoft: "rgba(110, 86, 246, 0.12)",
  brandPrimaryHover: "#5B45E0",
  statusSuccess: "#16A34A",
  statusSuccessSoft: "rgba(22, 163, 74, 0.12)",
  statusInfo: "#2563EB",
  statusInfoSoft: "rgba(37, 99, 235, 0.12)",
  statusWarning: "#D97706",
  statusWarningSoft: "rgba(217, 119, 6, 0.12)",
  statusError: "#DC2626",
  statusErrorSoft: "rgba(220, 38, 38, 0.12)",
  statusNeutral: "#5A5A66",
  statusNeutralSoft: "rgba(90, 90, 102, 0.08)",
  border: "#E4E4E7",
  borderStrong: "#D4D4D8",
  divider: "#EFEFF3",
  shadowSm: "rgba(0, 0, 0, 0.06)",
  shadowMd: "rgba(0, 0, 0, 0.12)",
} as const;

export const darkTokens = {
  background: "#000000",
  surface: "#15151A",
  surfaceElevated: "#1F1F26",
  surfaceMuted: "#0E0E12",
  textPrimary: "#FFFFFF",
  textSecondary: "#A8A8B0",
  textTertiary: "#6E6E76",
  textDisabled: "#3F3F46",
  brandPrimary: "#7C66FF",
  brandPrimarySoft: "rgba(124, 102, 255, 0.16)",
  brandPrimaryHover: "#9080FF",
  statusSuccess: "#22C55E",
  statusSuccessSoft: "rgba(34, 197, 94, 0.14)",
  statusInfo: "#3B82F6",
  statusInfoSoft: "rgba(59, 130, 246, 0.14)",
  statusWarning: "#F59E0B",
  statusWarningSoft: "rgba(245, 158, 11, 0.14)",
  statusError: "#EF4444",
  statusErrorSoft: "rgba(239, 68, 68, 0.14)",
  statusNeutral: "#A8A8B0",
  statusNeutralSoft: "rgba(168, 168, 176, 0.10)",
  border: "#1F1F26",
  borderStrong: "#2D2D35",
  divider: "#1A1A20",
  shadowSm: "rgba(0, 0, 0, 0.4)",
  shadowMd: "rgba(0, 0, 0, 0.6)",
} as const;
