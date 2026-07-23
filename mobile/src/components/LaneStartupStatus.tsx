// LaneStartupStatus — "is this alive?" for every preview lane.
//
// Every lane makes the user wait on something slow the phone cannot see:
//
//   Browser (RN + Flutter)  first web compile, 10s–3min
//   Hermes                  bundle build on the box
//   WebRTC                  simulator/emulator boot, ICE, first frame
//
// Each grew its own ad-hoc spinner, and all of them had the same defect: a
// spinner that never changes reads as HUNG. Reported from TestFlight 465 on a
// Flutter compile — "taking too much time, feels stuck", "I'm not sure whether
// it's going to load or not, I don't feel secure at all".
//
// The honest answer to that is not a nicer spinner, it is two numbers:
//
//   elapsed        — proves time is passing, and how much
//   last output    — proves the BOX is still talking
//
// Together they separate SLOW from STUCK, which is the only thing the user
// actually wants to know. Silence is stated outright rather than left to be
// inferred, because inferring is what makes a long-but-healthy compile feel
// broken.
//
// Log policy: this is a ROLLING TAIL, not a transcript. During startup nobody
// reads scrollback — they want the newest line. Full history belongs in the
// failure panel, where it is evidence. Keep `maxLines` small.

import React from "react";
import { Text, View, StyleSheet } from "react-native";
import { describeLaneProgress } from "../lib/laneProgress";

export { LANE_STALL_SECONDS, formatElapsed, describeLaneProgress } from "../lib/laneProgress";

export type LaneStartupStatusProps = {
  /** When this lane's startup began. Null renders nothing. */
  startedAt: number | null;
  /** When the agent last emitted anything. Null = nothing heard yet. */
  lastOutputAt: number | null;
  /** Re-render clock, passed in so the parent owns the single interval. */
  now: number;
  /** Newest-last log lines. Only the last `maxLines` are shown. */
  lines?: string[];
  maxLines?: number;
  mutedColor: string;
  /** Amber, used only when the lane has gone quiet. */
  warnColor?: string;
  /** Optional extra hint, e.g. what the user can do about a stall. */
  stallHint?: string;
};

export default function LaneStartupStatus({
  startedAt, lastOutputAt, now, lines, maxLines = 4,
  mutedColor, warnColor = "#f59e0b", stallHint,
}: LaneStartupStatusProps) {
  const progress = describeLaneProgress({ startedAt, lastOutputAt, now });
  if (!progress) return null;
  const tail = (lines ?? []).slice(-maxLines);

  return (
    <View style={styles.wrap}>
      <Text style={[styles.progress, { color: progress.stalled ? warnColor : mutedColor }]}>
        {progress.text}
        {progress.stalled && stallHint ? ` — ${stallHint}` : ""}
      </Text>
      {tail.length > 0 && (
        <View style={styles.tail}>
          {tail.map((ln, i) => (
            <Text
              key={`${i}-${ln.slice(0, 24)}`}
              numberOfLines={1}
              ellipsizeMode="tail"
              // Newest line full-strength, older ones fade — the eye lands on
              // what is happening NOW without the block turning into a wall.
              style={[styles.line, { color: mutedColor, opacity: i === tail.length - 1 ? 1 : 0.45 }]}
            >
              {ln}
            </Text>
          ))}
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: { marginTop: 8, alignSelf: "stretch", paddingHorizontal: 16 },
  progress: { fontSize: 12, textAlign: "center" },
  tail: { marginTop: 8 },
  line: { fontSize: 10, fontFamily: "Menlo", textAlign: "center" },
});
