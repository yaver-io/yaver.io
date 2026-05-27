/**
 * YaverSheet — drop-in Liquid Glass replacement for `react-native`'s
 * <Modal>. Use this for any bottom sheet / centered modal that should
 * pick up the Liquid Glass material on iOS 26+, BlurView on older
 * iOS, and a solid Material 3 surface on Android.
 *
 * Usage:
 *   <YaverSheet visible={open} onClose={() => setOpen(false)}>
 *     <YourSheetContent />
 *   </YaverSheet>
 *
 * Behavior:
 *   - Backdrop fades in (semi-opaque), tap-to-dismiss
 *   - Sheet slides up from the bottom on phones, scales in on tablet
 *   - Auto-respects safe-area insets
 *   - Reduce Transparency drops to solid fills (handled by YaverGlass)
 */

import React, { useEffect, useRef } from "react";
import {
  Animated,
  Dimensions,
  Easing,
  Keyboard,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  View,
  ViewStyle,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { YaverGlass, type GlassVariant } from "./YaverGlass";

export interface YaverSheetProps {
  visible: boolean;
  onClose: () => void;
  /** Sheet height as a percentage of screen height (0.0 - 1.0) on
   *  phones. Tablets ignore this and center at intrinsic size. */
  heightFraction?: number;
  /** Glass variant — "regular" (default) or "clear" (more transparent). */
  variant?: GlassVariant;
  /** Tint applied beneath the glass material. */
  tint?: string;
  /** Hide the drag handle bar at the top. */
  hideHandle?: boolean;
  /** Layout shape — "bottom" slides up from bottom; "center" centers. */
  presentation?: "bottom" | "center";
  /** Disable backdrop tap-to-dismiss. */
  blockDismiss?: boolean;
  /** Extra style for the sheet container. */
  style?: ViewStyle;
  children: React.ReactNode;
}

export function YaverSheet({
  visible,
  onClose,
  heightFraction = 0.7,
  variant = "regular",
  tint,
  hideHandle = false,
  presentation = "bottom",
  blockDismiss = false,
  style,
  children,
}: YaverSheetProps): React.JSX.Element {
  const insets = useSafeAreaInsets();
  const { height: screenH, width: screenW } = Dimensions.get("window");
  const isTablet = screenW >= 768;
  const effective = presentation === "center" || isTablet ? "center" : "bottom";

  const sheetH = effective === "center"
    ? Math.min(screenH * 0.8, 720)
    : Math.max(360, screenH * heightFraction);

  // Slide / scale animations
  const slideAnim = useRef(new Animated.Value(0)).current;
  useEffect(() => {
    Animated.timing(slideAnim, {
      toValue: visible ? 1 : 0,
      duration: 240,
      easing: Easing.out(Easing.cubic),
      useNativeDriver: true,
    }).start();
    if (visible) Keyboard.dismiss();
  }, [visible, slideAnim]);

  const transform =
    effective === "bottom"
      ? [{ translateY: slideAnim.interpolate({ inputRange: [0, 1], outputRange: [sheetH, 0] }) }]
      : [{ scale: slideAnim.interpolate({ inputRange: [0, 1], outputRange: [0.95, 1] }) }];

  const containerStyle: ViewStyle = effective === "bottom"
    ? {
        position: "absolute",
        left: 0, right: 0, bottom: 0,
        height: sheetH + insets.bottom,
        paddingBottom: insets.bottom,
      }
    : {
        position: "absolute",
        alignSelf: "center",
        top: (screenH - sheetH) / 2,
        width: Math.min(screenW * 0.92, 560),
        height: sheetH,
      };

  return (
    <Modal
      visible={visible}
      transparent
      animationType="none"
      statusBarTranslucent
      onRequestClose={onClose}
    >
      {/* Backdrop */}
      <Animated.View style={[StyleSheet.absoluteFill, { backgroundColor: "rgba(0,0,0,0.45)", opacity: slideAnim }]}>
        <Pressable
          style={StyleSheet.absoluteFill}
          onPress={blockDismiss ? undefined : onClose}
          accessibilityRole="button"
          accessibilityLabel="Close sheet"
        />
      </Animated.View>

      {/* Sheet */}
      <Animated.View style={[containerStyle, { transform, opacity: slideAnim }]}>
        <YaverGlass
          variant={variant}
          tint={tint}
          style={{
            flex: 1,
            borderTopLeftRadius: effective === "bottom" ? 20 : 16,
            borderTopRightRadius: effective === "bottom" ? 20 : 16,
            borderBottomLeftRadius: effective === "center" ? 16 : 0,
            borderBottomRightRadius: effective === "center" ? 16 : 0,
            ...style,
          }}
        >
          {!hideHandle && effective === "bottom" && (
            <View style={styles.handleWrap}>
              <View style={styles.handle} />
            </View>
          )}
          <View style={{ flex: 1, paddingHorizontal: 4 }}>{children}</View>
        </YaverGlass>
      </Animated.View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  handleWrap: {
    alignItems: "center",
    paddingTop: 8,
    paddingBottom: 4,
  },
  handle: {
    width: 40,
    height: 5,
    borderRadius: 3,
    backgroundColor: "rgba(255,255,255,0.35)",
  },
});

export default YaverSheet;
