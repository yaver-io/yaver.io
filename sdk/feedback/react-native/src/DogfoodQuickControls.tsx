import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  Animated,
  DeviceEventEmitter,
  Keyboard,
  Modal,
  PanResponder,
  Pressable,
  StyleSheet,
  Text,
  View,
  useWindowDimensions,
} from 'react-native';
import { YaverFeedback, type DogfoodControlTriggerState } from './YaverFeedback';
import {
  getDogfoodControlPosition,
  getDogfoodEntryIconHidden,
  getDogfoodRuntimeSelection,
  setDogfoodControlPosition,
  type DogfoodControlEdge,
} from './preferences';
import type { DogfoodUsageMode } from './dogfoodPolicy';

const FALLBACK_SIZE = 36;
const DOCK_VISIBLE = 21;
const SAFE_TOP = 64;
const SAFE_BOTTOM = 92;
const EMPTY_STATE: DogfoodControlTriggerState = {
  configured: false,
  authorized: false,
  gestureSupported: false,
  gestureEnabled: false,
  fallbackVisible: false,
  presentation: 'auto',
  onboardingSeen: false,
  reason: 'not-configured',
};

function preferenceScope(state: DogfoodControlTriggerState): string | undefined {
  return state.appId && state.installationId ? `${state.appId}:${state.installationId}` : undefined;
}

/**
 * The standalone SDK's only persistent chrome. An authorized tester sees this
 * edge-docked Y by default until they explicitly hide it in Dogfood Settings.
 * Both Y and the optional three-finger gesture open the same compact card.
 */
export const DogfoodQuickControls: React.FC<{ suppressed?: boolean }> = ({ suppressed = false }) => {
  const { width, height } = useWindowDimensions();
  const orientation = width > height ? 'landscape' : 'portrait';
  const defaultPosition = useMemo(() => ({
    x: Math.max(width - DOCK_VISIBLE, 0),
    y: Math.max(Math.round(height * 0.45), SAFE_TOP),
  }), [height, width]);
  const pan = useRef(new Animated.ValueXY(defaultPosition)).current;
  const opacity = useRef(new Animated.Value(0.72)).current;
  const lastPosition = useRef(defaultPosition);
  const dragStart = useRef(defaultPosition);
  const dragged = useRef(false);
  const fadeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const scopeRef = useRef<string | undefined>(undefined);
  const [dockEdge, setDockEdge] = useState<DogfoodControlEdge>('right');
  const [keyboardVisible, setKeyboardVisible] = useState(false);
  const [state, setState] = useState<DogfoodControlTriggerState>(EMPTY_STATE);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<'reload' | 'chat' | 'settings' | 'hide' | 'exit' | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [usageMode, setUsageMode] = useState<DogfoodUsageMode>('reload-and-chat');
  const [entryIconHidden, setEntryIconHidden] = useState(false);

  const scheduleFade = useCallback(() => {
    if (fadeTimer.current) clearTimeout(fadeTimer.current);
    fadeTimer.current = setTimeout(() => {
      Animated.timing(opacity, { toValue: 0.42, duration: 260, useNativeDriver: true }).start();
    }, 1800);
  }, [opacity]);

  const wakeControl = useCallback(() => {
    if (fadeTimer.current) clearTimeout(fadeTimer.current);
    Animated.timing(opacity, { toValue: 1, duration: 100, useNativeDriver: true }).start();
  }, [opacity]);

  const refresh = useCallback(async () => {
    try {
      const next = await YaverFeedback.syncDogfoodControlGesture();
      scopeRef.current = preferenceScope(next);
      setState(next);
      if (next.authorized) {
        const [mode, hidden] = await Promise.all([
          YaverFeedback.getDogfoodUsageMode(),
          getDogfoodEntryIconHidden(preferenceScope(next)),
        ]);
        setUsageMode(mode);
        setEntryIconHidden(hidden);
      }
    } catch {
      setState(EMPTY_STATE);
    }
  }, []);

  const openCard = useCallback(() => {
    setMessage(null);
    setOpen(true);
    if (!state.onboardingSeen) {
      void YaverFeedback.setDogfoodControlPresentation(state.presentation).then(setState).catch(() => {});
    }
  }, [state.onboardingSeen, state.presentation]);

  useEffect(() => {
    void refresh();
    const trigger = DeviceEventEmitter.addListener('yaverDogfoodControlGesture', () => {
      openCard();
    });
    const capability = DeviceEventEmitter.addListener('yaverDogfoodControlCapability', () => {
      void refresh();
    });
    // Enrollment approval and Exit Dogfood both change SDK mode while this
    // component remains mounted. Refresh React state immediately; native
    // setEnabled() alone cannot make the fallback Y appear or disappear.
    const mode = DeviceEventEmitter.addListener('yaverFeedback:dogfoodChanged', () => {
      void refresh();
    });
    const usage = DeviceEventEmitter.addListener('yaverFeedback:dogfoodUsageModeChanged', (payload) => {
      if (payload?.mode === 'chat-only' || payload?.mode === 'reload-only' || payload?.mode === 'reload-and-chat') {
        setUsageMode(payload.mode);
      }
    });
    const openUsage = DeviceEventEmitter.addListener('yaverFeedback:dogfoodUsageRequested', () => {
      openCard();
      void refresh();
    });
    const icon = DeviceEventEmitter.addListener('yaverFeedback:dogfoodEntryIconChanged', (payload) => {
      if (typeof payload?.visible !== 'boolean') return;
      if (!payload.scope || payload.scope === scopeRef.current) setEntryIconHidden(!payload.visible);
    });
    return () => {
      trigger.remove();
      capability.remove();
      mode.remove();
      usage.remove();
      openUsage.remove();
      icon.remove();
    };
  }, [openCard, refresh]);

  useEffect(() => {
    const show = Keyboard.addListener('keyboardDidShow', () => setKeyboardVisible(true));
    const hide = Keyboard.addListener('keyboardDidHide', () => setKeyboardVisible(false));
    return () => { show.remove(); hide.remove(); };
  }, []);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const saved = await getDogfoodControlPosition(orientation, preferenceScope(state));
      if (cancelled) return;
      const edge = saved?.edge || 'right';
      const minY = SAFE_TOP;
      const maxY = Math.max(minY, height - FALLBACK_SIZE - SAFE_BOTTOM);
      const y = saved
        ? minY + (maxY - minY) * saved.yRatio
        : Math.max(minY, Math.min(maxY, Math.round(height * 0.45)));
      const next = { x: edge === 'left' ? -FALLBACK_SIZE + DOCK_VISIBLE : width - DOCK_VISIBLE, y };
      setDockEdge(edge);
      lastPosition.current = next;
      pan.setValue(next);
      scheduleFade();
    })();
    return () => { cancelled = true; };
  }, [height, orientation, pan, scheduleFade, state.appId, state.installationId, width]);

  useEffect(() => () => {
    if (fadeTimer.current) clearTimeout(fadeTimer.current);
  }, []);

  const panResponder = useMemo(() => PanResponder.create({
    onStartShouldSetPanResponder: () => true,
    onMoveShouldSetPanResponder: (_, gesture) => Math.abs(gesture.dx) > 3 || Math.abs(gesture.dy) > 3,
    onPanResponderGrant: () => {
      wakeControl();
      dragged.current = false;
      dragStart.current = lastPosition.current;
      pan.setOffset(lastPosition.current);
      pan.setValue({ x: 0, y: 0 });
    },
    onPanResponderMove: (_, gesture) => {
      if (Math.abs(gesture.dx) > 3 || Math.abs(gesture.dy) > 3) dragged.current = true;
      Animated.event([null, { dx: pan.x, dy: pan.y }], { useNativeDriver: false })(_, gesture);
    },
    onPanResponderRelease: (_, gesture) => {
      pan.flattenOffset();
      const minY = SAFE_TOP;
      const maxY = Math.max(minY, height - FALLBACK_SIZE - SAFE_BOTTOM);
      const rawX = dragStart.current.x + gesture.dx;
      const y = Math.max(minY, Math.min(maxY, dragStart.current.y + gesture.dy));
      const edge: DogfoodControlEdge = rawX + FALLBACK_SIZE / 2 < width / 2 ? 'left' : 'right';
      const x = edge === 'left' ? -FALLBACK_SIZE + DOCK_VISIBLE : width - DOCK_VISIBLE;
      const next = { x, y };
      const yRatio = maxY === minY ? 0.5 : (y - minY) / (maxY - minY);
      setDockEdge(edge);
      lastPosition.current = next;
      Animated.spring(pan, { toValue: next, useNativeDriver: false, friction: 7 }).start(scheduleFade);
      void setDogfoodControlPosition(orientation, { edge, yRatio }, preferenceScope(state));
    },
    onPanResponderTerminate: scheduleFade,
  }), [height, orientation, pan, scheduleFade, state.appId, state.installationId, wakeControl, width]);

  const fastReload = useCallback(async () => {
    if (busy) return;
    setBusy('reload');
    setMessage(null);
    try {
      const ack = await YaverFeedback.requestDogfoodFastReload();
      setMessage(ack || 'Fast Reload requested.');
      setTimeout(() => setOpen(false), 650);
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error);
      setMessage(detail.replace(/^DOGFOOD_AGENT_UPGRADE_REQUIRED:\s*/, ''));
    } finally {
      setBusy(null);
    }
  }, [busy]);

  const openChat = useCallback(async () => {
    if (busy) return;
    setBusy('chat');
    setMessage(null);
    setOpen(false);
    const dogfoodConfig = YaverFeedback.getConfig()?.dogfood as {
      projectName?: string;
      projectPath?: string;
      lane?: 'browser' | 'hermes' | 'webrtc';
      targetDeviceId?: string;
      runtimeSessionId?: string;
      renderBehavior?: 'manual' | 'auto-on-request';
    } | undefined;
    const configured = YaverFeedback.getActiveDogfoodRuntimeSelection()
      || (dogfoodConfig?.projectPath ? dogfoodConfig : null)
      || (state.appId ? await getDogfoodRuntimeSelection(state.appId) : null);
    if (state.authorized) {
      // This card is already the post-approval, live-runtime surface. Opening
      // its composer must be a synchronous UI handoff; repeating account and
      // checkout discovery here left the pressed control on “Opening…” while
      // the exact runtime was visibly alive underneath it.
      console.log(`[YaverFeedback] Opening Dogfood Chat${configured?.projectPath ? ` for ${configured.projectPath}` : ' with runtime recovery'}.`);
      DeviceEventEmitter.emit('yaverFeedback:dogfoodNewChatRequested', {
        selection: configured?.projectPath ? {
          projectName: configured.projectName,
          projectPath: configured.projectPath,
          lane: configured.lane || 'browser',
          targetDeviceId: configured.targetDeviceId,
          runtimeSessionId: configured.runtimeSessionId,
        } : undefined,
        renderBehavior: dogfoodConfig?.renderBehavior || 'manual',
      });
      setBusy(null);
      return;
    }
    const result = await YaverFeedback.openDogfoodChat();
    if (result.phase === 'denied' || result.phase === 'error') {
      setMessage(result.error || 'Dogfood access is not available on this installation.');
      setOpen(true);
    }
    setBusy(null);
  }, [busy, state.authorized]);

  const openSettings = useCallback(async () => {
    if (busy) return;
    setBusy('settings');
    setMessage(null);
    setOpen(false);
    const result = await YaverFeedback.openDogfood();
    if (result.phase === 'denied' || result.phase === 'error') {
      setMessage(result.error || 'Dogfood Settings are not available on this installation.');
      setOpen(true);
    }
    setBusy(null);
  }, [busy]);

  const hideEntry = useCallback(async () => {
    if (busy) return;
    setBusy('hide');
    setMessage(null);
    try {
      if (entryIconHidden) {
        await YaverFeedback.setDogfoodEntryIconVisible(true);
        setEntryIconHidden(false);
      } else {
        await YaverFeedback.setDogfoodEntryIconVisible(false);
        setEntryIconHidden(true);
        setOpen(false);
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(null);
    }
  }, [busy, entryIconHidden]);

  const exitDogfood = useCallback(() => {
    if (busy) return;
    Alert.alert(
      'Exit Dogfood?',
      'Return to the normal installed app. Your source changes and coding sessions stay untouched.',
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: 'Exit Dogfood',
          style: 'destructive',
          onPress: () => {
            setBusy('exit');
            void YaverFeedback.exitDogfoodMode()
              .then(() => setOpen(false))
              .catch((error) => setMessage(error instanceof Error ? error.message : String(error)))
              .finally(() => setBusy(null));
          },
        },
      ],
    );
  }, [busy]);

  if (!state.configured || !state.authorized) return null;

  return (
    <>
      {state.fallbackVisible && !entryIconHidden && !keyboardVisible && !open && !suppressed ? (
        <Animated.View pointerEvents="box-none" style={[StyleSheet.absoluteFill, styles.layer]}>
          <Animated.View
            {...panResponder.panHandlers}
            style={[
              styles.fallbackPosition,
              { opacity, transform: [{ translateX: pan.x }, { translateY: pan.y }] },
            ]}
          >
            <Pressable
              testID="yaver-dogfood-minimized-control"
              accessibilityRole="button"
              accessibilityLabel="Open Dogfood controls"
              hitSlop={10}
              onPressIn={wakeControl}
              onPress={() => {
                if (dragged.current) {
                  dragged.current = false;
                  return;
                }
                openCard();
              }}
              style={({ pressed }) => [styles.fallback, pressed && styles.pressed]}
            >
              <Text style={[
                styles.fallbackText,
                dockEdge === 'right' ? styles.rightDockText : styles.leftDockText,
              ]}>Y</Text>
            </Pressable>
          </Animated.View>
        </Animated.View>
      ) : null}

      <Modal visible={open} transparent animationType="fade" onRequestClose={() => setOpen(false)}>
        <Pressable style={styles.backdrop} onPress={() => setOpen(false)}>
          <Pressable style={styles.card} onPress={(event) => event.stopPropagation()}>
            <View style={styles.actions}>
              {usageMode !== 'reload-only' ? <Pressable
                testID="yaver-dogfood-chat"
                accessibilityRole="button"
                disabled={busy !== null}
                onPress={() => void openChat()}
                style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}
              >
                <Text style={styles.actionTitle}>{busy === 'chat' ? 'Opening…' : 'Chat'}</Text>
              </Pressable> : null}
              {usageMode !== 'chat-only' ? <Pressable
                testID="yaver-dogfood-fast-reload"
                accessibilityRole="button"
                disabled={busy !== null}
                onPress={() => void fastReload()}
                style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}
              >
                <Text style={styles.actionTitle}>{busy === 'reload' ? 'Reloading…' : 'Reload'}</Text>
              </Pressable> : null}
            </View>
            <View style={styles.utilityActions}>
              <Pressable
                testID="yaver-dogfood-settings"
                accessibilityRole="button"
                disabled={busy !== null}
                onPress={() => void openSettings()}
                style={({ pressed }) => [styles.utilityAction, pressed && styles.actionPressed]}
              >
                <Text style={styles.utilityText}>{busy === 'settings' ? 'Opening…' : 'Settings'}</Text>
              </Pressable>
              <Pressable
                testID="yaver-dogfood-hide"
                accessibilityRole="button"
                accessibilityLabel={entryIconHidden ? 'Show Y over this app' : 'Hide Y over this app'}
                disabled={busy !== null}
                onPress={() => void hideEntry()}
                style={({ pressed }) => [styles.utilityAction, pressed && styles.actionPressed]}
              >
                <Text style={styles.utilityText}>{busy === 'hide' ? 'Saving…' : entryIconHidden ? 'Show Y' : 'Hide Y'}</Text>
              </Pressable>
              <Pressable
                testID="yaver-dogfood-exit"
                accessibilityRole="button"
                disabled={busy !== null}
                onPress={exitDogfood}
                style={({ pressed }) => [styles.utilityAction, pressed && styles.actionPressed]}
              >
                <Text style={styles.exitText}>{busy === 'exit' ? 'Exiting…' : 'Exit'}</Text>
              </Pressable>
            </View>
            {message ? <Text style={styles.message}>{message}</Text> : null}
          </Pressable>
        </Pressable>
      </Modal>
    </>
  );
};

const styles = StyleSheet.create({
  layer: { zIndex: 9997 },
  fallbackPosition: { position: 'absolute', left: 0, top: 0 },
  fallback: {
    width: FALLBACK_SIZE,
    height: FALLBACK_SIZE,
    borderRadius: FALLBACK_SIZE / 2,
    backgroundColor: '#6f58f5',
    borderWidth: 1.5,
    borderColor: 'rgba(255,255,255,0.92)',
    shadowColor: '#000',
    shadowOpacity: 0.28,
    shadowRadius: 5,
    shadowOffset: { width: 0, height: 2 },
    elevation: 6,
    overflow: 'hidden',
  },
  fallbackText: { position: 'absolute', top: 7, color: '#ffffff', fontSize: 17, lineHeight: 20, fontWeight: '900' },
  rightDockText: { left: 6 },
  leftDockText: { right: 6 },
  pressed: { opacity: 0.78 },
  backdrop: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    padding: 24,
    backgroundColor: 'rgba(2,6,23,0.28)',
  },
  card: {
    width: '100%',
    maxWidth: 280,
    borderRadius: 16,
    padding: 10,
    backgroundColor: '#111827',
    shadowColor: '#000',
    shadowOpacity: 0.3,
    shadowRadius: 16,
    shadowOffset: { width: 0, height: 8 },
    elevation: 12,
  },
  actions: { flexDirection: 'row', gap: 10 },
  utilityActions: { flexDirection: 'row', gap: 8, marginTop: 8 },
  utilityAction: { flex: 1, minHeight: 36, borderRadius: 10, alignItems: 'center', justifyContent: 'center' },
  utilityText: { color: '#c7d2fe', fontSize: 12, fontWeight: '700' },
  exitText: { color: '#fca5a5', fontSize: 12, fontWeight: '700' },
  action: {
    flex: 1,
    minHeight: 52,
    borderRadius: 12,
    paddingHorizontal: 12,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: '#1f2937',
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: '#4b5563',
  },
  actionPressed: { backgroundColor: '#374151' },
  actionTitle: { color: '#f9fafb', fontSize: 15, fontWeight: '700' },
  message: { color: '#fdba74', fontSize: 12, lineHeight: 17, marginTop: 12 },
});
