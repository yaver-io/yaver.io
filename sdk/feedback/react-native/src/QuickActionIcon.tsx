import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  Animated,
  DeviceEventEmitter,
  Dimensions,
  NativeModules,
  PanResponder,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { YaverFeedback } from './YaverFeedback';
import {
  getQuickIconColorPreset,
  getQuickIconDisabled,
  QUICK_ICON_COLOR_PRESETS,
  setQuickIconColorPreset,
  setQuickIconDisabled,
} from './preferences';

// Mirror the suppression rule used by YaverFeedback + ShakeDetector:
// when loaded through Yaver's super-host Hermes bundle, the host owns
// shake + reload UX. We must not render a second action surface.
function isRunningInsideYaverHost(): boolean {
  try {
    return !!(NativeModules as any)?.YaverInfo;
  } catch {
    return false;
  }
}

const DEFAULT_SIZE = 44;
const DEFAULT_BACKGROUND_COLOR = '#ff6b2c';
const DEFAULT_LABEL_COLOR = '#111111';
const DEFAULT_BORDER_COLOR = 'rgba(255,255,255,0.92)';
const DEFAULT_SHADOW_COLOR = '#000000';
const LONG_PRESS_MS = 550;

export interface QuickActionIconProps {
  /** Deprecated alias for `backgroundColor`. */
  color?: string;
  /** Override the background from FeedbackConfig.quickIconBackgroundColor. */
  backgroundColor?: string;
  /** Override the label color from FeedbackConfig.quickIconForegroundColor. */
  foregroundColor?: string;
  /** Override the border color from FeedbackConfig.quickIconBorderColor. */
  borderColor?: string;
  /** Override the shadow color from FeedbackConfig.quickIconShadowColor. */
  shadowColor?: string;
  /** Override the initial position from FeedbackConfig.quickIconInitialPosition. */
  initialPosition?: { x: number; y: number };
  /** Override the icon diameter. Default 44. */
  size?: number;
}

/**
 * Small tap-to-open icon for the Yaver Feedback SDK.
 *
 * Default UX:
 *  - **Tap** opens the feedback modal (same as shake).
 *  - **Long-press** (~550ms) opens a menu with "Open feedback" and
 *    "Hide icon". Hiding is persisted to AsyncStorage so the user's
 *    decision survives app relaunches.
 *  - **Drag** repositions the icon.
 *
 * Shake always keeps working independently — even when the icon is
 * hidden the user can still shake to open feedback.
 *
 * Visibility is controlled by `FeedbackConfig.quickIcon`:
 *  - `'auto'` (default) → `'after-shake'` on iOS/Android, `'off'` on web.
 *  - `'always'` → visible from first render.
 *  - `'after-shake'` → hidden until `yaverFeedback:firstShake` fires.
 *  - `'off'` → never rendered.
 *
 * Suppressed entirely when the SDK is loaded inside Yaver's super-host
 * (the Yaver mobile app owns the shake gesture + overlay in that case).
 */
export const QuickActionIcon: React.FC<QuickActionIconProps> = ({
  color: colorProp,
  backgroundColor: backgroundColorProp,
  foregroundColor: foregroundColorProp,
  borderColor: borderColorProp,
  shadowColor: shadowColorProp,
  initialPosition: initialPositionProp,
  size = DEFAULT_SIZE,
}) => {
  const config = YaverFeedback.getConfig();

  const mode: 'always' | 'after-shake' | 'off' = (() => {
    const raw = config?.quickIcon ?? 'auto';
    if (raw === 'auto') {
      return Platform.OS === 'web' ? 'off' : 'after-shake';
    }
    return raw;
  })();

  const backgroundColor =
    backgroundColorProp ??
    colorProp ??
    config?.quickIconBackgroundColor ??
    config?.quickIconColor ??
    DEFAULT_BACKGROUND_COLOR;
  const foregroundColor =
    foregroundColorProp ??
    config?.quickIconForegroundColor ??
    DEFAULT_LABEL_COLOR;
  const borderColor =
    borderColorProp ??
    config?.quickIconBorderColor ??
    DEFAULT_BORDER_COLOR;
  const shadowColor =
    shadowColorProp ??
    config?.quickIconShadowColor ??
    DEFAULT_SHADOW_COLOR;

  const { width, height } = Dimensions.get('window');
  const defaultStart =
    initialPositionProp ??
    config?.quickIconInitialPosition ?? {
      x: Math.max(width - size - 14, 0),
      y: Math.max(Math.floor(height * 0.35), 80),
    };

  const pan = useRef(new Animated.ValueXY(defaultStart)).current;
  const lastPos = useRef(defaultStart);
  const dragStart = useRef<{ x: number; y: number } | null>(null);
  const didDrag = useRef(false);

  const [userDisabled, setUserDisabled] = useState<boolean | null>(null);
  const [colorPreset, setColorPreset] = useState<keyof typeof QUICK_ICON_COLOR_PRESETS | null>(null);
  const [shakenThisSession, setShakenThisSession] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [hostSuppressed] = useState<boolean>(() => isRunningInsideYaverHost());

  // Load the persisted disable flag once on mount. Until it resolves we
  // render nothing — a one-frame flash of the icon before hiding would
  // be worse than a tiny delayed appearance.
  useEffect(() => {
    let alive = true;
    getQuickIconDisabled().then((v) => {
      if (alive) setUserDisabled(v);
    });
    getQuickIconColorPreset().then((v) => {
      if (alive) setColorPreset(v);
    });
    return () => {
      alive = false;
    };
  }, []);

  // `after-shake` mode waits for the first shake before revealing
  // itself. YaverFeedback emits this event from its shake callback.
  useEffect(() => {
    const sub = DeviceEventEmitter.addListener(
      'yaverFeedback:firstShake',
      () => setShakenThisSession(true),
    );
    return () => sub.remove();
  }, []);

  // Programmatic control: host apps can call
  // `YaverFeedback.setQuickIconVisible(true)` to re-surface the icon
  // after the user hid it (e.g. from a settings screen).
  useEffect(() => {
    const showSub = DeviceEventEmitter.addListener(
      'yaverFeedback:quickIconShow',
      () => {
        setUserDisabled(false);
        void setQuickIconDisabled(false);
      },
    );
    const hideSub = DeviceEventEmitter.addListener(
      'yaverFeedback:quickIconHide',
      () => {
        setUserDisabled(true);
        void setQuickIconDisabled(true);
        setMenuOpen(false);
      },
    );
    const colorSub = DeviceEventEmitter.addListener(
      'yaverFeedback:quickIconColorChange',
      (next: { preset?: keyof typeof QUICK_ICON_COLOR_PRESETS | null }) => {
        const preset = next?.preset ?? null;
        setColorPreset(preset);
        void setQuickIconColorPreset(preset);
      },
    );
    return () => {
      showSub.remove();
      hideSub.remove();
      colorSub.remove();
    };
  }, []);

  const panResponder = useRef(
    PanResponder.create({
      onStartShouldSetPanResponder: () => true,
      onMoveShouldSetPanResponder: (_, g) =>
        Math.abs(g.dx) > 3 || Math.abs(g.dy) > 3,
      onPanResponderGrant: () => {
        didDrag.current = false;
        dragStart.current = { ...lastPos.current };
        pan.setOffset({ x: lastPos.current.x, y: lastPos.current.y });
        pan.setValue({ x: 0, y: 0 });
      },
      onPanResponderMove: (_, g) => {
        if (Math.abs(g.dx) > 3 || Math.abs(g.dy) > 3) {
          didDrag.current = true;
        }
        Animated.event([null, { dx: pan.x, dy: pan.y }], {
          useNativeDriver: false,
        })(_, g);
      },
      onPanResponderRelease: (_, g) => {
        pan.flattenOffset();
        const start = dragStart.current ?? lastPos.current;
        const maxX = Math.max(width - size, 0);
        const maxY = Math.max(height - size, 0);
        const nextX = Math.max(0, Math.min(maxX, start.x + g.dx));
        const nextY = Math.max(0, Math.min(maxY, start.y + g.dy));
        lastPos.current = { x: nextX, y: nextY };
        Animated.spring(pan, {
          toValue: { x: nextX, y: nextY },
          useNativeDriver: false,
          friction: 7,
        }).start();
      },
    }),
  ).current;

  const openFeedback = useCallback(() => {
    setMenuOpen(false);
    void YaverFeedback.startReport();
  }, []);

  const hideForever = useCallback(() => {
    setMenuOpen(false);
    setUserDisabled(true);
    void setQuickIconDisabled(true);
  }, []);

  if (hostSuppressed) return null;
  if (mode === 'off') return null;
  if (userDisabled === null) return null;
  if (userDisabled) return null;
  if (mode === 'after-shake' && !shakenThisSession) return null;
  if (!YaverFeedback.isEnabled()) return null;

  const presetColors = colorPreset ? QUICK_ICON_COLOR_PRESETS[colorPreset] : null;
  const visualSize = size;
  const radius = visualSize / 2;

  return (
    <Animated.View
      pointerEvents="box-none"
      style={[
        StyleSheet.absoluteFill,
        { zIndex: 9998 },
      ]}
    >
      <Animated.View
        {...panResponder.panHandlers}
        style={[
          styles.container,
          {
            transform: [{ translateX: pan.x }, { translateY: pan.y }],
          },
        ]}
      >
        <Pressable
          onPress={() => {
            if (didDrag.current) {
              didDrag.current = false;
              return;
            }
            openFeedback();
          }}
          onLongPress={() => {
            if (didDrag.current) return;
            setMenuOpen((m) => !m);
          }}
          delayLongPress={LONG_PRESS_MS}
          hitSlop={6}
          accessibilityRole="button"
          accessibilityLabel="Open Yaver feedback"
          style={({ pressed }) => [
            styles.icon,
            {
              width: visualSize,
              height: visualSize,
              borderRadius: radius,
              backgroundColor: presetColors?.backgroundColor ?? backgroundColor,
              borderColor: presetColors?.borderColor ?? borderColor,
              shadowColor: presetColors?.shadowColor ?? shadowColor,
              opacity: pressed ? 0.85 : 1,
            },
          ]}
        >
          <Text
            style={[
              styles.iconLabel,
              {
                color: presetColors?.foregroundColor ?? foregroundColor,
                fontSize: Math.round(visualSize * 0.5),
              },
            ]}
          >
            y
          </Text>
        </Pressable>
        {menuOpen ? (
          <View style={styles.menu}>
            <Pressable
              onPress={openFeedback}
              style={({ pressed }) => [
                styles.menuItem,
                pressed && styles.menuItemPressed,
              ]}
            >
              <Text style={styles.menuItemText}>Open feedback</Text>
            </Pressable>
            <View style={styles.menuDivider} />
            <Pressable
              onPress={hideForever}
              style={({ pressed }) => [
                styles.menuItem,
                pressed && styles.menuItemPressed,
              ]}
            >
              <Text style={[styles.menuItemText, styles.menuItemDanger]}>
                Hide icon
              </Text>
            </Pressable>
          </View>
        ) : null}
      </Animated.View>
    </Animated.View>
  );
};

const styles = StyleSheet.create({
  container: {
    position: 'absolute',
    top: 0,
    left: 0,
    alignItems: 'flex-start',
  },
  icon: {
    alignItems: 'center',
    justifyContent: 'center',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.34,
    shadowRadius: 6,
    elevation: 7,
    borderWidth: 2,
  },
  iconLabel: {
    fontWeight: '700',
    includeFontPadding: false,
  },
  menu: {
    marginTop: 6,
    minWidth: 150,
    backgroundColor: '#1f1f23',
    borderRadius: 10,
    paddingVertical: 4,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.3,
    shadowRadius: 6,
    elevation: 6,
  },
  menuItem: {
    paddingHorizontal: 14,
    paddingVertical: 10,
  },
  menuItemPressed: {
    backgroundColor: '#2a2a30',
  },
  menuItemText: {
    color: '#f4f4f5',
    fontSize: 14,
    fontWeight: '500',
  },
  menuItemDanger: {
    color: '#f97316',
  },
  menuDivider: {
    height: StyleSheet.hairlineWidth,
    backgroundColor: '#3f3f46',
    marginHorizontal: 8,
  },
});
