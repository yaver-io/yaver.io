/**
 * YaverGlass — single component for all translucent surfaces in Yaver.
 *
 * Renders the right material per platform/iOS-version:
 *   iOS 26+:  expo-glass-effect (true Apple Liquid Glass with refraction)
 *   iOS 18-25: expo-blur BlurView (frosted glass fallback)
 *   Android:  solid Material 3 surface — Google's design lead publicly
 *             refused the Liquid Glass copyjob, embrace divergence
 *             (see project_spatial_constraints_2026)
 *   Web:      backdrop-filter via CSS
 *
 * Also honors AccessibilityInfo.isReduceTransparencyEnabled — when on,
 * we drop to solid fills everywhere (Apple's own apps do this).
 *
 * Runtime feature detection — expo-glass-effect is dynamic-required so
 * the bundle compiles + runs even when the package isn't installed
 * (i.e. on RN 0.79 before the bump to 0.80+). The wrapper auto-
 * upgrades once you `npm install expo-glass-effect`.
 *
 * Usage:
 *   <YaverGlass style={{ borderRadius: 32 }} variant="regular">
 *     <YourContent />
 *   </YaverGlass>
 */

import React, { useEffect, useState } from "react";
import { AccessibilityInfo, Platform, StyleSheet, View, ViewStyle } from "react-native";
import { BlurView } from "expo-blur";
import { useColors } from "../context/ThemeContext";

export type GlassVariant = "regular" | "clear" | "tinted";
export type GlassShape = "default" | "capsule" | "circle";

export interface YaverGlassProps {
  /** "regular" matches Apple's default; "clear" is more transparent;
   *  "tinted" picks up the parent's background color subtly. */
  variant?: GlassVariant;
  /** Hint to native renderers — `.capsule` and `.circle` get special
   *  Liquid Glass refraction shapes on iOS 26. Has no effect on Android
   *  / web. */
  shape?: GlassShape;
  /** Tint color overrides — affects iOS BlurView fallback + Android
   *  Material surface. Hex string. */
  tint?: string;
  /** Override the base BlurView intensity (0-100). Default 80. */
  blurIntensity?: number;
  /** Force solid fallback (skip blur/glass). Useful for benchmarking
   *  or when iPhone-13-era perf is a concern. */
  forceSolid?: boolean;
  style?: ViewStyle;
  children?: React.ReactNode;
}

// Try to load expo-glass-effect once at module init. If it isn't
// installed (current state on RN 0.79), this stays null and we fall
// back to BlurView. After the RN+Xcode bump + `npm install
// expo-glass-effect`, this picks it up automatically — no code
// change needed.
let GlassEffectModule: any = null;
try {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  GlassEffectModule = require("expo-glass-effect");
} catch {
  GlassEffectModule = null;
}

// Apple's Liquid Glass only ships on iOS 26+ AND requires the
// expo-glass-effect NATIVE module to be linked into the binary. The JS
// package can be present (require succeeds, `GlassView` is exported) while
// the native view manager is absent — e.g. the pod was never installed
// (current state: expo-glass-effect is in package.json but not in
// Podfile.lock). Rendering <GlassView> then throws RN's "Unimplemented
// component: ViewManagerAdapter_ExpoGlassEffect_GlassView".
//
// expo-glass-effect exposes `isGlassEffectAPIAvailable()` precisely for
// this — it returns false from the JS stub when native isn't linked, and
// true (from the native override) once it is. Gate on it so we fall back
// to BlurView until the native side actually ships. Wrapped in try/catch
// because calling into a missing native module can throw.
function supportsLiquidGlass(): boolean {
  if (Platform.OS !== "ios") return false;
  if (!GlassEffectModule?.GlassView) return false;
  const ver = typeof Platform.Version === "string" ? parseInt(Platform.Version, 10) : Platform.Version;
  if (typeof ver !== "number" || ver < 26) return false;
  try {
    const apiOk = GlassEffectModule.isGlassEffectAPIAvailable?.();
    const glassOk = GlassEffectModule.isLiquidGlassAvailable?.();
    // Require an explicit true from the runtime probe. undefined/false
    // (native not linked) → fall back. Either probe being true is enough.
    return apiOk === true || glassOk === true;
  } catch {
    return false;
  }
}

/** Hook to track the system "Reduce Transparency" accessibility flag.
 *  Apple drops Liquid Glass entirely when it's on; we mirror that. */
function useReduceTransparency(): boolean {
  const [enabled, setEnabled] = useState(false);
  useEffect(() => {
    let cancelled = false;
    AccessibilityInfo.isReduceTransparencyEnabled?.()
      .then((v) => { if (!cancelled) setEnabled(!!v); })
      .catch(() => {});
    const sub = AccessibilityInfo.addEventListener?.("reduceTransparencyChanged", setEnabled);
    return () => {
      cancelled = true;
      sub?.remove?.();
    };
  }, []);
  return enabled;
}

export function YaverGlass({
  variant = "regular",
  shape = "default",
  tint,
  blurIntensity = 80,
  forceSolid = false,
  style,
  children,
}: YaverGlassProps): React.JSX.Element {
  const colors = useColors();
  const reduceTransparency = useReduceTransparency();

  const baseRadius = shape === "circle" ? 999 : shape === "capsule" ? 999 : 12;
  const wrap = StyleSheet.flatten([{ borderRadius: baseRadius, overflow: "hidden" as const }, style]);

  // 1. Reduce Transparency → solid fill everywhere
  if (reduceTransparency || forceSolid) {
    return (
      <View style={[wrap, { backgroundColor: tint ?? colors.bgCardElevated ?? colors.bgCard }]}>
        {children}
      </View>
    );
  }

  // 2. iOS 26+ with expo-glass-effect installed → true Liquid Glass
  if (supportsLiquidGlass()) {
    const GlassView = GlassEffectModule.GlassView as React.ComponentType<any>;
    // expo-glass-effect props match Apple's SwiftUI .glassEffect():
    //   variant: "regular" | "clear"
    //   isInteractive: bool (subtle scale on tap)
    //   tintColor: color
    return (
      <GlassView
        glassEffectStyle={variant === "clear" ? "clear" : "regular"}
        tintColor={tint}
        isInteractive
        style={wrap}
      >
        {children}
      </GlassView>
    );
  }

  // 3. iOS 18-25 or non-iOS Apple builds → expo-blur frosted glass
  if (Platform.OS === "ios") {
    return (
      <BlurView
        intensity={blurIntensity}
        tint="systemMaterial"
        style={wrap}
      >
        {tint && (
          <View pointerEvents="none" style={[StyleSheet.absoluteFill, { backgroundColor: tint, opacity: 0.18 }]} />
        )}
        {children}
      </BlurView>
    );
  }

  // 4. Android → solid Material 3 surface. Per research
  //    (project_spatial_constraints_2026) Google publicly rejected
  //    porting Liquid Glass to Android — Material 3 Expressive bets
  //    on bold color + opaque surfaces. Embrace it.
  if (Platform.OS === "android") {
    return (
      <View
        style={[
          wrap,
          {
            backgroundColor: tint ?? colors.bgCardElevated ?? colors.bgCard,
            elevation: 3,
          },
        ]}
      >
        {children}
      </View>
    );
  }

  // 5. Web fallback — backdrop-filter via inline style. RN Web
  //    propagates `style.backdropFilter` to the underlying div.
  //    The cast is intentional — RN's ViewStyle types don't ship
  //    web-only CSS properties; runtime acceptance is fine.
  const webStyle: ViewStyle = {
    backgroundColor: tint ?? "rgba(255,255,255,0.08)",
    ...({
      backdropFilter: "blur(20px) saturate(160%)",
      WebkitBackdropFilter: "blur(20px) saturate(160%)",
    } as Record<string, string>),
  };
  return (
    <View style={[wrap, webStyle]}>
      {children}
    </View>
  );
}

export default YaverGlass;
