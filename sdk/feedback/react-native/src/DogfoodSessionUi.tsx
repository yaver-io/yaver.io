import React from 'react';
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from 'react-native';
import type {
  DogfoodFailure,
  DogfoodLane,
  DogfoodLaneOption,
  DogfoodLogLine,
  DogfoodPhase,
} from './DogfoodRuntime';

export type DogfoodStatusTone = 'ready' | 'attention' | 'blocked' | 'pending';

export interface DogfoodStatusStep {
  key: string;
  label: string;
  detail: string;
  tone: DogfoodStatusTone;
  actionLabel?: string;
  actionDisabled?: boolean;
  expanded?: boolean;
  onAction?: () => void;
}

export interface DogfoodUiColors {
  background: string;
  border: string;
  text: string;
  muted: string;
  accent: string;
  accentSoft: string;
  ready: string;
  attention: string;
  blocked: string;
  console: string;
}

const DEFAULT_COLORS: DogfoodUiColors = {
  background: '#111827',
  border: '#334155',
  text: '#f8fafc',
  muted: '#94a3b8',
  accent: '#818cf8',
  accentSoft: '#312e81',
  ready: '#22c55e',
  attention: '#f59e0b',
  blocked: '#ef4444',
  console: '#070b12',
};

function resolvedColors(colors?: Partial<DogfoodUiColors>): DogfoodUiColors {
  return { ...DEFAULT_COLORS, ...colors };
}

function toneColor(tone: DogfoodStatusTone, colors: DogfoodUiColors): string {
  if (tone === 'ready') return colors.ready;
  if (tone === 'blocked') return colors.blocked;
  if (tone === 'attention') return colors.attention;
  return colors.muted;
}

/** Shared readiness rail used by Yaver itself and every embedded SDK host. */
export const DogfoodStatusRail: React.FC<{
  steps: readonly DogfoodStatusStep[];
  colors?: Partial<DogfoodUiColors>;
}> = ({ steps, colors: colorOverrides }) => {
  const colors = resolvedColors(colorOverrides);
  return (
    <View style={styles.rail} accessibilityLabel="Dogfood session readiness">
      {steps.map((step) => {
        const tone = toneColor(step.tone, colors);
        return (
          <View key={step.key} style={styles.statusRow}>
            <View style={[styles.statusDot, { backgroundColor: tone }]} />
            <View style={styles.statusCopy}>
              <Text style={[styles.statusLabel, { color: colors.text }]}>{step.label}</Text>
              <Text style={[styles.statusDetail, { color: tone }]}>{step.detail}</Text>
            </View>
            {step.actionLabel && step.onAction ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={`${step.actionLabel} ${step.label}`}
                accessibilityState={{ expanded: step.expanded, disabled: step.actionDisabled }}
                disabled={step.actionDisabled}
                onPress={step.onAction}
                style={({ pressed }) => [
                  styles.statusAction,
                  { borderColor: colors.border, opacity: step.actionDisabled ? 0.5 : pressed ? 0.7 : 1 },
                ]}
              >
                <Text style={[styles.statusActionText, { color: colors.accent }]}>{step.actionLabel}</Text>
              </Pressable>
            ) : null}
          </View>
        );
      })}
    </View>
  );
};

/** One lane selector and one default policy across Yaver, SFMG, and Talos. */
export const DogfoodLanePicker: React.FC<{
  options: readonly DogfoodLaneOption[];
  selected: DogfoodLane;
  fallbackLane?: DogfoodLane;
  onSelect: (lane: DogfoodLane) => void;
  colors?: Partial<DogfoodUiColors>;
  showUnsupportedReasons?: boolean;
}> = ({ options, selected, fallbackLane, onSelect, colors: colorOverrides, showUnsupportedReasons = true }) => {
  const colors = resolvedColors(colorOverrides);
  return (
    <View accessibilityRole="radiogroup" accessibilityLabel="Dogfood runtime lane">
      <View style={styles.choiceRow}>
        {options.map((option) => {
          const active = selected === option.lane;
          return (
            <Pressable
              key={option.lane}
              disabled={!option.supported}
              onPress={() => onSelect(option.lane)}
              accessibilityRole="radio"
              accessibilityState={{ checked: active, disabled: !option.supported }}
              aria-checked={active}
              style={({ pressed }) => [
                styles.choice,
                {
                  borderColor: active ? colors.accent : colors.border,
                  backgroundColor: active ? colors.accentSoft : colors.background,
                  opacity: !option.supported ? 0.45 : pressed ? 0.72 : 1,
                },
              ]}
            >
              <View style={styles.choiceCopy}>
                <Text style={[styles.choiceText, { color: colors.text }, active && styles.choiceTextActive]}>
                  {option.label}
                  {active ? ' · selected' : fallbackLane === option.lane ? ' · automatic fallback' : option.default ? ' · default' : ''}
                </Text>
                <Text style={[styles.choiceDescription, { color: colors.muted }]}>{option.description}</Text>
              </View>
              <Text style={[styles.choiceMark, { color: active ? colors.accent : colors.muted }]}>{active ? '✓' : '›'}</Text>
            </Pressable>
          );
        })}
      </View>
      {showUnsupportedReasons ? options.filter((option) => !option.supported && option.reason).map((option) => (
        <Text key={`${option.lane}-reason`} style={[styles.reason, { color: colors.muted }]}>
          {option.label}: {option.reason}
        </Text>
      )) : null}
    </View>
  );
};

function runtimeTone(phase: DogfoodPhase, colors: DogfoodUiColors): string {
  if (phase === 'ready') return colors.ready;
  if (phase === 'failed') return colors.blocked;
  if (phase === 'idle' || phase === 'stopped') return colors.muted;
  return colors.attention;
}

/** Shared no-click handoff shown while a prepared runtime opens itself. */
export const DogfoodLaunchingWidget: React.FC<{
  message?: string;
  detail?: string;
  colors?: Partial<DogfoodUiColors>;
}> = ({ message = 'Launching Dogfood…', detail = 'The prepared app will open automatically.', colors: colorOverrides }) => {
  const colors = resolvedColors(colorOverrides);
  return (
    <View
      style={[styles.launching, { backgroundColor: colors.accentSoft, borderColor: colors.accent }]}
      accessibilityRole="progressbar"
      accessibilityLabel={message}
      accessibilityLiveRegion="polite"
    >
      <ActivityIndicator color={colors.accent} />
      <View style={styles.launchingCopy}>
        <Text style={[styles.launchingTitle, { color: colors.text }]}>{message}</Text>
        <Text style={[styles.launchingDetail, { color: colors.muted }]}>{detail}</Text>
      </View>
    </View>
  );
};

/** Shared second-stage live console. Browser lane deliberately names Browser
 * Logs; Hermes/WebRTC use the same lifecycle and failure/remedy treatment. */
export const DogfoodLiveConsole: React.FC<{
  lane: DogfoodLane;
  sourceLabel?: string;
  phase: DogfoodPhase;
  message: string;
  logs: readonly DogfoodLogLine[];
  failure?: DogfoodFailure;
  maxLines?: number;
  colors?: Partial<DogfoodUiColors>;
  renderText?: (text: string) => React.ReactNode;
}> = ({ lane, sourceLabel, phase, message, logs, failure, maxLines = 80, colors: colorOverrides, renderText }) => {
  const colors = resolvedColors(colorOverrides);
  const text = logs.slice(-maxLines).map((line) => line.text).join('\n');
  const title = lane === 'browser' ? 'Browser Logs' : lane === 'hermes' ? 'Hermes Logs' : 'WebRTC Logs';
  const accessibleTitle = sourceLabel ? `${title} · ${sourceLabel}` : title;
  return (
    <View style={[styles.console, { backgroundColor: colors.console, borderColor: colors.border }]} accessibilityLabel={accessibleTitle}>
      <View style={styles.consoleHeader}>
        <View style={[styles.statusDot, { backgroundColor: runtimeTone(phase, colors) }]} />
        <Text style={[styles.consoleTitle, { color: colors.text }]}>{title}</Text>
      </View>
      {sourceLabel ? <Text style={[styles.consoleSource, { color: colors.muted }]}>Source · {sourceLabel}</Text> : null}
      <Text style={[styles.consoleStatus, { color: colors.muted }]}>{message}</Text>
      {text ? (
        renderText ? renderText(text) : <Text selectable style={[styles.consoleText, { color: colors.text }]}>{text}</Text>
      ) : (
        <Text style={[styles.consoleEmpty, { color: colors.muted }]}>Waiting for the first line from the remote PC…</Text>
      )}
      {failure ? (
        <View style={[styles.failure, { borderColor: colors.blocked }]}>
          <Text style={[styles.failureText, { color: colors.text }]}>{failure.error}</Text>
          <Text style={[styles.failureRemedy, { color: colors.muted }]}>{failure.remedy}</Text>
        </View>
      ) : null}
    </View>
  );
};

const styles = StyleSheet.create({
  rail: { gap: 4 },
  statusRow: { minHeight: 46, flexDirection: 'row', alignItems: 'center', gap: 9 },
  statusDot: { width: 8, height: 8, borderRadius: 4 },
  statusCopy: { flex: 1 },
  statusLabel: { fontSize: 12, fontWeight: '700' },
  statusDetail: { fontSize: 11, lineHeight: 16, marginTop: 1 },
  statusAction: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 7 },
  statusActionText: { fontSize: 11, fontWeight: '700' },
  choiceRow: { gap: 8 },
  choice: { minHeight: 58, flexDirection: 'row', alignItems: 'center', borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 9 },
  choiceCopy: { flex: 1 },
  choiceText: { fontSize: 12, fontWeight: '500' },
  choiceTextActive: { fontWeight: '700' },
  choiceDescription: { fontSize: 10, lineHeight: 15, marginTop: 2 },
  choiceMark: { fontSize: 16, fontWeight: '800', marginLeft: 10 },
  reason: { fontSize: 10, lineHeight: 14, marginTop: 5 },
  launching: { width: '100%', minHeight: 64, flexDirection: 'row', alignItems: 'center', gap: 11, borderWidth: 1, borderRadius: 12, paddingHorizontal: 14, paddingVertical: 11 },
  launchingCopy: { flex: 1 },
  launchingTitle: { fontSize: 13, fontWeight: '800' },
  launchingDetail: { fontSize: 10, lineHeight: 15, marginTop: 2 },
  console: { width: '100%', maxHeight: 320, overflow: 'hidden', marginTop: 10, borderWidth: 1, borderRadius: 10, padding: 11, gap: 7 },
  consoleHeader: { flexDirection: 'row', alignItems: 'center', gap: 7 },
  consoleTitle: { fontSize: 12, fontWeight: '800' },
  consoleSource: { fontSize: 10, lineHeight: 14 },
  consoleStatus: { fontSize: 11, lineHeight: 16 },
  consoleText: { fontFamily: 'monospace', fontSize: 10, lineHeight: 15 },
  consoleEmpty: { fontSize: 10, fontStyle: 'italic' },
  failure: { borderWidth: 1, borderRadius: 8, padding: 9, gap: 4 },
  failureText: { fontSize: 11, fontWeight: '700' },
  failureRemedy: { fontSize: 10, lineHeight: 15 },
});
