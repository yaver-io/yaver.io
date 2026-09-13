import React, { useEffect, useRef } from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import type { DogfoodUsageMode } from './dogfoodPolicy';
import { DogfoodLaunchingWidget } from './DogfoodSessionUi';

export interface DogfoodNativeMenuProps {
  active: boolean;
  busy?: boolean;
  status?: string;
  /** Yaver supplies its checkout-aware launch control; normal apps can use onLaunch. */
  launchContent?: React.ReactNode;
  onLaunch?: () => void;
  onReload: () => void;
  onExit: () => void;
  onOpenTasks: () => void;
  onOpenSettings: () => void;
  usageMode?: DogfoodUsageMode;
  issue?: string;
  onFixIssue?: () => void;
  colors?: {
    card?: string;
    border?: string;
    text?: string;
    muted?: string;
    accent?: string;
    danger?: string;
  };
}

/** Shared native Dogfood home used by Yaver and SDK host apps. */
export const DogfoodNativeMenu: React.FC<DogfoodNativeMenuProps> = ({
  active,
  busy = false,
  status,
  launchContent,
  onLaunch,
  onReload,
  onExit,
  onOpenTasks,
  onOpenSettings,
  usageMode = 'reload-and-chat',
  issue,
  onFixIssue,
  colors,
}) => {
  const autoLaunchRef = useRef(false);

  useEffect(() => {
    if (active) {
      autoLaunchRef.current = false;
      return;
    }
    if (busy || !onLaunch || autoLaunchRef.current || launchContent) return;
    autoLaunchRef.current = true;
    onLaunch();
  }, [active, busy, launchContent, onLaunch]);

  return <View style={styles.root}>
    {active ? (
      <View style={[styles.runtimeCard, { backgroundColor: colors?.card, borderColor: colors?.border }]} accessibilityLabel="Dogfood is active">
        <View style={styles.runtimeCopy}>
          <Text style={[styles.eyebrow, { color: colors?.accent }]}>DOGFOOD ACTIVE</Text>
          <Text style={[styles.title, { color: colors?.text }]}>Test the live app</Text>
          <Text style={[styles.detail, { color: colors?.muted }]}>{status || 'Your app stays open while Yaver prepares updates.'}</Text>
        </View>
        <Pressable
          testID="dogfood-native-reload"
          accessibilityRole="button"
          disabled={busy}
          onPress={onReload}
          style={({ pressed }) => [styles.primary, { backgroundColor: colors?.accent }, (pressed || busy) && styles.pressed]}
        ><Text style={styles.primaryText}>{busy ? 'Working…' : 'Reload Dogfood'}</Text></Pressable>
        <Pressable
          testID="dogfood-native-exit"
          accessibilityRole="button"
          onPress={onExit}
          style={({ pressed }) => [styles.exit, pressed && styles.pressed]}
        ><Text style={[styles.exitText, { color: colors?.danger }]}>{busy ? 'Stop Dogfood' : 'Exit Dogfood'}</Text></Pressable>
      </View>
    ) : launchContent || (
      <View style={[styles.runtimeCard, { backgroundColor: colors?.card, borderColor: colors?.border }]} accessibilityLabel="Dogfood is inactive">
        <View style={styles.runtimeCopy}>
          <Text style={[styles.eyebrow, { color: colors?.accent }]}>NATIVE APP</Text>
          <Text style={[styles.title, { color: colors?.text }]}>Dogfood this app</Text>
          <Text style={[styles.detail, { color: colors?.muted }]}>Launch the configured checkout without replacing the app you installed.</Text>
        </View>
        <DogfoodLaunchingWidget
          message={busy ? 'Preparing Dogfood…' : 'Launching Dogfood…'}
          detail="The configured checkout opens automatically."
          colors={{ background: colors?.card, border: colors?.border, text: colors?.text, muted: colors?.muted, accent: colors?.accent }}
        />
      </View>
    )}

    {issue ? <View style={[styles.issue, { backgroundColor: colors?.card, borderColor: colors?.danger || colors?.border }]}>
      <View style={styles.rowCopy}>
        <Text style={[styles.rowTitle, { color: colors?.text }]}>The running app needs attention</Text>
        <Text style={[styles.rowDetail, { color: colors?.muted }]}>{issue}</Text>
      </View>
      {onFixIssue ? <Pressable accessibilityRole="button" accessibilityLabel="Fix Dogfood issue in Tasks" onPress={onFixIssue} style={({ pressed }) => [styles.issueAction, { backgroundColor: colors?.accent }, pressed && styles.pressed]}>
        <Text style={styles.primaryText}>Fix in Tasks</Text>
      </Pressable> : null}
    </View> : null}

    {usageMode !== 'reload-only' ? <Pressable accessibilityRole="button" accessibilityLabel="Open Dogfood tasks" onPress={onOpenTasks} style={({ pressed }) => [styles.row, { backgroundColor: colors?.card, borderColor: colors?.border }, pressed && styles.pressed]}>
      <View style={styles.rowCopy}><Text style={[styles.rowTitle, { color: colors?.text }]}>Tasks</Text><Text style={[styles.rowDetail, { color: colors?.muted }]}>Vibe, follow live work, and continue sessions</Text></View>
      <Text style={[styles.chevron, { color: colors?.muted }]}>›</Text>
    </Pressable> : null}
    <Pressable accessibilityRole="button" accessibilityLabel="Open Dogfood settings" onPress={onOpenSettings} style={({ pressed }) => [styles.row, { backgroundColor: colors?.card, borderColor: colors?.border }, pressed && styles.pressed]}>
      <View style={styles.rowCopy}><Text style={[styles.rowTitle, { color: colors?.text }]}>Settings</Text><Text style={[styles.rowDetail, { color: colors?.muted }]}>Box, runner, checkout, lane, and Y icon</Text></View>
      <Text style={[styles.chevron, { color: colors?.muted }]}>›</Text>
    </Pressable>
  </View>;
};

const styles = StyleSheet.create({
  root: { gap: 12 },
  runtimeCard: { borderWidth: StyleSheet.hairlineWidth, borderColor: '#dddde7', borderRadius: 18, padding: 16, backgroundColor: '#fff', gap: 10 },
  runtimeCopy: { gap: 3 },
  eyebrow: { color: '#6f58f5', fontSize: 10, fontWeight: '900', letterSpacing: 0.7 },
  title: { color: '#18181e', fontSize: 19, fontWeight: '800' },
  detail: { color: '#71717d', fontSize: 12, lineHeight: 18 },
  primary: { minHeight: 48, borderRadius: 13, alignItems: 'center', justifyContent: 'center', backgroundColor: '#6f58f5' },
  primaryText: { color: '#fff', fontSize: 14, fontWeight: '800' },
  exit: { minHeight: 42, alignItems: 'center', justifyContent: 'center' },
  exitText: { color: '#b42318', fontSize: 13, fontWeight: '700' },
  row: { minHeight: 72, flexDirection: 'row', alignItems: 'center', borderWidth: StyleSheet.hairlineWidth, borderColor: '#dddde7', borderRadius: 16, paddingHorizontal: 16, backgroundColor: '#fff' },
  issue: { borderWidth: StyleSheet.hairlineWidth, borderColor: '#b42318', borderRadius: 16, padding: 16, gap: 10, backgroundColor: '#fff' },
  issueAction: { minHeight: 42, borderRadius: 12, alignItems: 'center', justifyContent: 'center', backgroundColor: '#6f58f5' },
  rowCopy: { flex: 1 },
  rowTitle: { color: '#202027', fontSize: 15, fontWeight: '800' },
  rowDetail: { color: '#787884', fontSize: 11, marginTop: 3 },
  chevron: { color: '#8d8d98', fontSize: 24 },
  pressed: { opacity: 0.7 },
});
