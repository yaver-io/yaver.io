import React from 'react';
import { Modal, Pressable, StyleSheet, Text, View } from 'react-native';
import type { DogfoodUsageMode } from './dogfoodPolicy';

export type DogfoodControlBusy = 'reload' | 'chat' | 'settings' | 'hide' | 'exit' | null;

export interface DogfoodControlMenuProps {
  visible: boolean;
  usageMode: DogfoodUsageMode;
  busy?: DogfoodControlBusy;
  message?: string | null;
  hideLabel?: string;
  onChat: () => void;
  onReload: () => void;
  onSettings: () => void;
  onExit: () => void;
  onHide?: () => void;
  onDismiss: () => void;
}

/** Shared Y-menu contract for SDK hosts and Yaver's own dogfood runtime. */
export const DogfoodControlMenu: React.FC<DogfoodControlMenuProps> = ({
  visible,
  usageMode,
  busy = null,
  message,
  hideLabel,
  onChat,
  onReload,
  onSettings,
  onExit,
  onHide,
  onDismiss,
}) => (
  <Modal visible={visible} transparent animationType="fade" onRequestClose={onDismiss}>
    <Pressable style={styles.backdrop} onPress={onDismiss}>
      <Pressable style={styles.card} onPress={(event) => event.stopPropagation()}>
        <View style={styles.actions}>
          {usageMode !== 'reload-only' ? <Pressable
            testID="yaver-dogfood-chat"
            accessibilityRole="button"
            disabled={busy !== null}
            onPress={onChat}
            style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}
          ><Text style={styles.actionTitle}>{busy === 'chat' ? 'Opening…' : 'Chat'}</Text></Pressable> : null}
          {usageMode !== 'chat-only' ? <Pressable
            testID="yaver-dogfood-fast-reload"
            accessibilityRole="button"
            disabled={busy !== null}
            onPress={onReload}
            style={({ pressed }) => [styles.action, pressed && styles.actionPressed]}
          ><Text style={styles.actionTitle}>{busy === 'reload' ? 'Reloading…' : 'Reload'}</Text></Pressable> : null}
        </View>
        <View style={styles.utilityActions}>
          <Pressable testID="yaver-dogfood-settings" accessibilityRole="button" disabled={busy !== null} onPress={onSettings} style={({ pressed }) => [styles.utilityAction, pressed && styles.actionPressed]}>
            <Text style={styles.utilityText}>{busy === 'settings' ? 'Opening…' : 'Settings'}</Text>
          </Pressable>
          {onHide ? <Pressable testID="yaver-dogfood-hide" accessibilityRole="button" accessibilityLabel={hideLabel} disabled={busy !== null} onPress={onHide} style={({ pressed }) => [styles.utilityAction, pressed && styles.actionPressed]}>
            <Text style={styles.utilityText}>{busy === 'hide' ? 'Saving…' : hideLabel || 'Hide Y'}</Text>
          </Pressable> : null}
          <Pressable testID="yaver-dogfood-exit" accessibilityRole="button" disabled={busy !== null} onPress={onExit} style={({ pressed }) => [styles.utilityAction, pressed && styles.actionPressed]}>
            <Text style={styles.exitText}>{busy === 'exit' ? 'Exiting…' : 'Exit'}</Text>
          </Pressable>
        </View>
        {message ? <Text style={styles.message}>{message}</Text> : null}
      </Pressable>
    </Pressable>
  </Modal>
);

const styles = StyleSheet.create({
  backdrop: { flex: 1, justifyContent: 'center', alignItems: 'center', padding: 24, backgroundColor: 'rgba(2,6,23,0.28)' },
  card: { width: '100%', maxWidth: 280, borderRadius: 16, padding: 10, backgroundColor: '#111827', shadowColor: '#000', shadowOpacity: 0.3, shadowRadius: 16, shadowOffset: { width: 0, height: 8 }, elevation: 12 },
  actions: { flexDirection: 'row', gap: 10 },
  utilityActions: { flexDirection: 'row', gap: 8, marginTop: 8 },
  utilityAction: { flex: 1, minHeight: 36, borderRadius: 10, alignItems: 'center', justifyContent: 'center' },
  utilityText: { color: '#c7d2fe', fontSize: 12, fontWeight: '700' },
  exitText: { color: '#fca5a5', fontSize: 12, fontWeight: '700' },
  action: { flex: 1, minHeight: 52, borderRadius: 12, paddingHorizontal: 12, alignItems: 'center', justifyContent: 'center', backgroundColor: '#1f2937', borderWidth: StyleSheet.hairlineWidth, borderColor: '#4b5563' },
  actionPressed: { backgroundColor: '#374151' },
  actionTitle: { color: '#f9fafb', fontSize: 15, fontWeight: '700' },
  message: { color: '#fdba74', fontSize: 12, lineHeight: 17, marginTop: 12 },
});
