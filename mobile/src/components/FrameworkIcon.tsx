// Branded framework icons. Replaces the prior emoji map in
// mobile/app/(tabs)/{hotreload,apps}.tsx — emoji rendered fine for
// color but didn't say "Swift on Apple platforms" at a glance, and
// Kotlin was a literal purple square.
//
// Most icons come from @expo/vector-icons / MaterialCommunityIcons,
// but two glyphs ("flutter", "vercel") aren't in the bundled MCI font
// — they render as the question-mark replacement glyph at runtime.
// Those use react-native-svg with the official brand artwork instead.
//
// Color palette is the authoritative brand-guideline color for each
// framework, NOT the closest react-native-friendly approximation.
import React from "react";
import { MaterialCommunityIcons } from "@expo/vector-icons";
import Svg, { Path } from "react-native-svg";

type FrameworkID =
  | "expo"
  | "react-native"
  | "react"
  | "flutter"
  | "swift"
  | "kotlin"
  | "nextjs"
  | "vite";

interface MCIIconSpec {
  kind: "mci";
  name: keyof typeof MaterialCommunityIcons.glyphMap;
  color: string;
}
interface SvgIconSpec {
  kind: "svg";
  render: (size: number, color?: string) => React.ReactElement;
}
type IconSpec = MCIIconSpec | SvgIconSpec;

const FRAMEWORK_ICON_SPECS: Record<string, IconSpec> = {
  expo: { kind: "mci", name: "react", color: "#A78BFA" },
  "react-native": { kind: "mci", name: "react", color: "#61DAFB" },
  react: { kind: "mci", name: "react", color: "#61DAFB" },
  flutter: {
    kind: "svg",
    render: (size: number) => <FlutterLogo size={size} />,
  },
  swift: { kind: "mci", name: "language-swift", color: "#FA7343" },
  kotlin: { kind: "mci", name: "language-kotlin", color: "#7F52FF" },
  // MCI doesn't ship the Vercel triangle in this glyph set, but plain
  // `triangle` reads as Next.js's logo well enough at 22pt. Override
  // with #fafafa so it pops on the dark card background.
  nextjs: { kind: "mci", name: "triangle", color: "#FAFAFA" },
  vite: { kind: "mci", name: "lightning-bolt", color: "#FFC107" },
};

interface Props {
  framework: string | null | undefined;
  size?: number;
  /** Override color (only honored for MCI specs — SVG specs use brand
   *  colors). Pass when the row is disabled and should be muted. */
  color?: string;
}

export function FrameworkIcon({ framework, size = 22, color }: Props) {
  const id = String(framework || "").trim().toLowerCase();
  const spec = FRAMEWORK_ICON_SPECS[id];
  if (!spec) {
    return (
      <MaterialCommunityIcons
        name="play-circle-outline"
        size={size}
        color={color ?? "#94a3b8"}
      />
    );
  }
  if (spec.kind === "svg") {
    return spec.render(size, color);
  }
  return (
    <MaterialCommunityIcons
      name={spec.name}
      size={size}
      color={color ?? spec.color}
    />
  );
}

/** FlutterLogo — official Flutter brand artwork as an inline SVG.
 *  Two-tone: lighter blue (#42A5F5) for the leading face, darker blue
 *  (#0D47A1) for the shadow. ViewBox 256x317 mirrors the upstream
 *  asset; the size prop scales uniformly. */
function FlutterLogo({ size }: { size: number }) {
  return (
    <Svg width={size} height={size} viewBox="0 0 256 317">
      <Path fill="#42A5F5" d="M157.66 0L0 157.65l48.797 48.79L255.34 0z" />
      <Path
        fill="#42A5F5"
        d="M156.5 145.46L67.85 234.105l49.045 49.052 48.78-48.798L255.32 145.46z"
      />
      <Path
        fill="#0D47A1"
        d="M116.895 283.157l37.05 37.058 26.045-26.044-37.05-37.058z"
      />
      <Path
        fill="#42A5F5"
        d="M67.4 234.55l49.5-12.95 36.8 36.65-49.5 12.95z"
      />
      <Path fill="#0D47A1" d="M116.7 258.25l59.6-22.5-22.7 47.45z" />
    </Svg>
  );
}

/** Detects whether a framework string would render with our branded icon
 *  (vs the neutral fallback). Useful for feature gates that only want to
 *  surface the icon when it's recognisable. */
export function isKnownFramework(framework: string | null | undefined): framework is FrameworkID {
  const id = String(framework || "").trim().toLowerCase();
  return id in FRAMEWORK_ICON_SPECS;
}
