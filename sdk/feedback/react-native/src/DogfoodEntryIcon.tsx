import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Animated, DeviceEventEmitter, PanResponder, Pressable, StyleSheet, Text, useWindowDimensions } from 'react-native';
import { getDogfoodEntryIconHidden, setDogfoodEntryIconHidden } from './preferences';

export interface DogfoodEntryIconProps {
  /** The icon is controlled by the host so Settings can hide or restore it. */
  hidden?: boolean;
  preferenceScope?: string;
  onPress: () => void;
  accessibilityLabel?: string;
}

/**
 * The only chrome Dogfood places over a running app. It never opens a modal,
 * captures focus, or replaces the host surface; tapping it hands navigation
 * back to the host's native Dogfood menu.
 */
export const DogfoodEntryIcon: React.FC<DogfoodEntryIconProps> = ({
  hidden,
  preferenceScope,
  onPress,
  accessibilityLabel = 'Open Dogfood',
}) => {
  const { width, height } = useWindowDimensions();
  const [preferenceHidden, setPreferenceHidden] = useState(false);
  const edgeInset = 12;
  const iconSize = 48;
  const rightEdge = Math.max(edgeInset, width - iconSize - edgeInset);
  const position = useRef(new Animated.ValueXY({ x: rightEdge, y: Math.max(height * 0.45, 64) })).current;
  const origin = useRef({ x: rightEdge, y: Math.max(height * 0.45, 64) });
  const moved = useRef(false);

  const clamp = (x: number, y: number) => ({
    x: Math.max(edgeInset, Math.min(rightEdge, x)),
    y: Math.max(64, Math.min(Math.max(64, height - 128), y)),
  });

  useEffect(() => {
    const next = clamp(origin.current.x, origin.current.y);
    origin.current = next;
    position.setValue(next);
  // Animated position is stable; viewport changes are the only trigger.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [height, width]);

  useEffect(() => {
    if (hidden !== undefined) return;
    let alive = true;
    void getDogfoodEntryIconHidden(preferenceScope).then((value) => { if (alive) setPreferenceHidden(value); }).catch(() => {});
    const sub = DeviceEventEmitter.addListener('yaverFeedback:dogfoodEntryIconChanged', (payload) => {
      if (typeof payload?.visible === 'boolean' && (!payload.scope || payload.scope === preferenceScope)) setPreferenceHidden(!payload.visible);
    });
    return () => { alive = false; sub.remove(); };
  }, [hidden, preferenceScope]);

  const responder = useMemo(() => PanResponder.create({
    onStartShouldSetPanResponder: () => true,
    onMoveShouldSetPanResponder: (_, gesture) => Math.abs(gesture.dx) > 3 || Math.abs(gesture.dy) > 3,
    onPanResponderGrant: () => {
      moved.current = false;
      position.setOffset(origin.current);
      position.setValue({ x: 0, y: 0 });
    },
    onPanResponderMove: (_, gesture) => {
      if (Math.abs(gesture.dx) > 3 || Math.abs(gesture.dy) > 3) moved.current = true;
      Animated.event([null, { dx: position.x, dy: position.y }], { useNativeDriver: false })(_, gesture);
    },
    onPanResponderRelease: (_, gesture) => {
      position.flattenOffset();
      const raw = clamp(origin.current.x + gesture.dx, origin.current.y + gesture.dy);
      const next = { x: raw.x + iconSize / 2 < width / 2 ? edgeInset : rightEdge, y: raw.y };
      origin.current = next;
      Animated.spring(position, { toValue: next, useNativeDriver: false, friction: 7 }).start();
    },
  }), [height, position, width]);

  if (hidden ?? preferenceHidden) return null;
  return (
    <Animated.View
      {...responder.panHandlers}
      pointerEvents="box-none"
      style={[styles.position, { transform: [{ translateX: position.x }, { translateY: position.y }] }]}
    >
      <Pressable
        testID="yaver-dogfood-entry"
        accessibilityRole="button"
        accessibilityLabel={accessibilityLabel}
        accessibilityHint="Opens the native Dogfood menu. Long press to hide this Y; restore it in Dogfood Settings."
        hitSlop={10}
        delayLongPress={650}
        onLongPress={() => {
          void setDogfoodEntryIconHidden(true, preferenceScope).then(() => {
            setPreferenceHidden(true);
            DeviceEventEmitter.emit('yaverFeedback:dogfoodEntryIconChanged', {
              visible: false,
              scope: preferenceScope,
            });
          });
        }}
        onPress={() => {
          if (moved.current) {
            moved.current = false;
            return;
          }
          onPress();
        }}
        style={({ pressed }) => [styles.icon, pressed && styles.pressed]}
      >
        <Text style={styles.label}>Y</Text>
      </Pressable>
    </Animated.View>
  );
};

const styles = StyleSheet.create({
  position: { position: 'absolute', left: 0, top: 0, zIndex: 9999, elevation: 9999 },
  icon: {
    width: 48,
    height: 48,
    borderRadius: 24,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: '#6f58f5',
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,.82)',
    shadowColor: '#000',
    shadowOpacity: 0.2,
    shadowRadius: 8,
    shadowOffset: { width: 0, height: 3 },
  },
  label: { color: '#fff', fontSize: 20, fontWeight: '900' },
  pressed: { opacity: 0.72 },
});
