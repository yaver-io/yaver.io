import React, { useEffect, useRef } from "react";
import { Animated, Easing, StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { typography } from "../theme/tokens";
import {
  PARK_STEPS,
  PHASE_META,
  WAKE_STEPS,
  type LifecyclePhase,
  type MachineLifecycleState,
} from "../lib/wakeMachine";

// WakeProgress — the shared "waking up / closing down" visual. One
// animated bar + a phase ladder + a live network line, driven entirely by
// useMachineLifecycle's derived state. Used by RemoteBoxBanner (compact,
// inline under the status row), the Connection screen (full), and
// car-voice. The watch / TV / CLI mirror this ladder in their own idiom.
//
// It renders only while a run is in flight or just settled — when the box
// is plainly asleep or plainly ready the host surface shows its own
// resting UI. Progress is monotonic (the hook guarantees it) so the bar
// only ever fills.

export interface WakeProgressProps {
  state: MachineLifecycleState;
  /** Compact = single bar + one status line (for the banner). Full = bar +
   *  labelled step ladder + network line (for Connection / car). */
  compact?: boolean;
}

const NETWORK_PHASES: LifecyclePhase[] = ["registering", "online", "ready"];

export default function WakeProgress({ state, compact }: WakeProgressProps) {
  const c = useColors();
  const { phase, meta, percent, direction, error } = state;

  const fill = useRef(new Animated.Value(percent)).current;
  const pulse = useRef(new Animated.Value(0)).current;

  useEffect(() => {
    Animated.timing(fill, {
      toValue: percent,
      duration: 550,
      easing: Easing.out(Easing.cubic),
      useNativeDriver: false,
    }).start();
  }, [percent, fill]);

  // Gentle breathing pulse on the active bar / current step.
  useEffect(() => {
    const active = direction !== null && phase !== "error";
    if (!active) {
      pulse.stopAnimation();
      pulse.setValue(0);
      return;
    }
    const loop = Animated.loop(
      Animated.sequence([
        Animated.timing(pulse, { toValue: 1, duration: 900, easing: Easing.inOut(Easing.ease), useNativeDriver: true }),
        Animated.timing(pulse, { toValue: 0, duration: 900, easing: Easing.inOut(Easing.ease), useNativeDriver: true }),
      ]),
    );
    loop.start();
    return () => loop.stop();
  }, [direction, phase, pulse]);

  if (!direction && phase !== "error") return null;

  const isPark = direction === "park" || meta.kind === "park";
  const steps = isPark ? PARK_STEPS : WAKE_STEPS;
  const barColor = phase === "error" ? c.error : NETWORK_PHASES.includes(phase) ? c.success : c.accent;
  const widthPct = fill.interpolate({ inputRange: [0, 100], outputRange: ["0%", "100%"] });
  const pulseOpacity = pulse.interpolate({ inputRange: [0, 1], outputRange: [0.55, 1] });

  return (
    <View style={styles.wrap}>
      {/* Primary status line */}
      <View style={styles.headRow}>
        <Animated.View style={[styles.leadDot, { backgroundColor: barColor, opacity: phase === "error" ? 1 : pulseOpacity }]} />
        <Text style={[styles.headText, { color: phase === "error" ? c.error : c.textPrimary }]} numberOfLines={1}>
          {error ?? meta.label}
        </Text>
        <Text style={[styles.pct, { color: c.textMuted }]}>{Math.round(percent)}%</Text>
      </View>

      {/* Progress bar */}
      <View style={[styles.track, { backgroundColor: c.bgInput }]}>
        <Animated.View style={[styles.fillBar, { width: widthPct, backgroundColor: barColor }]} />
      </View>

      {!compact ? (
        <>
          {/* Labelled step ladder */}
          <View style={styles.ladder}>
            {steps.map((sp) => {
              const done = percent >= PHASE_META[sp].percent && phase !== sp;
              const current = phase === sp;
              const stepColor = phase === "error"
                ? (current ? c.error : c.textMuted)
                : done
                  ? c.success
                  : current
                    ? c.accent
                    : c.textMuted;
              return (
                <View key={sp} style={styles.step}>
                  <Animated.View
                    style={[
                      styles.stepDot,
                      { borderColor: stepColor, backgroundColor: done ? c.success : "transparent" },
                      current ? { opacity: pulseOpacity } : null,
                    ]}
                  >
                    {done ? <Text style={styles.check}>✓</Text> : null}
                  </Animated.View>
                  <Text
                    style={[styles.stepLabel, { color: current ? c.textPrimary : done ? c.textSecondary : c.textMuted, fontWeight: current ? "700" : "500" }]}
                    numberOfLines={1}
                  >
                    {PHASE_META[sp].short}
                  </Text>
                </View>
              );
            })}
          </View>

          {/* Live network line — appears once the relay leg starts */}
          {NETWORK_PHASES.includes(phase) ? (
            <View style={styles.netRow}>
              <View style={[styles.netDot, { backgroundColor: c.success }]} />
              <Text style={[styles.netText, { color: c.textSecondary }]}>
                {phase === "ready" ? "Connected over the free relay" : "Relay link coming up — no re-auth needed"}
              </Text>
            </View>
          ) : phase === "error" ? null : (
            <Text style={[styles.hint, { color: c.textMuted }]}>
              {isPark
                ? "Snapshot is kept — the server is only removed once it's safely stored."
                : "Restoring your snapshot and booting — this can take several minutes."}
            </Text>
          )}

          {/* Honest explanation when a phase overruns — replaces a silently
              frozen bar (the old "stuck at 80%" with no idea why). */}
          {state.stallHint ? (
            <Text style={[styles.hint, { color: c.warn, marginTop: 4 }]}>{state.stallHint}</Text>
          ) : null}
        </>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: { gap: 8, paddingTop: 8 },
  headRow: { flexDirection: "row", alignItems: "center", gap: 8 },
  leadDot: { width: 8, height: 8, borderRadius: 4 },
  headText: { ...typography.caption, fontWeight: "600", flex: 1, minWidth: 0 },
  pct: { ...typography.caption, fontVariant: ["tabular-nums"], fontWeight: "700" },
  track: { height: 6, borderRadius: 3, overflow: "hidden" },
  fillBar: { height: 6, borderRadius: 3 },
  ladder: { flexDirection: "row", justifyContent: "space-between", marginTop: 2 },
  step: { flex: 1, alignItems: "center", gap: 4 },
  stepDot: {
    width: 16,
    height: 16,
    borderRadius: 8,
    borderWidth: 2,
    alignItems: "center",
    justifyContent: "center",
  },
  check: { color: "#fff", fontSize: 9, fontWeight: "900", lineHeight: 12 },
  stepLabel: { fontSize: 9.5, letterSpacing: 0.1, textAlign: "center" },
  netRow: { flexDirection: "row", alignItems: "center", gap: 6, marginTop: 2 },
  netDot: { width: 7, height: 7, borderRadius: 4 },
  netText: { ...typography.caption },
  hint: { ...typography.caption, marginTop: 2 },
});
