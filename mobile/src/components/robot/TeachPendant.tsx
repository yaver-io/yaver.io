// TeachPendant — the "teach" half of teach-and-repeat. Arm Record, then every
// jog / home / screwdriver / rotate you do on the cell is captured as a Step.
// Name + save the sequence (stored encrypted on the edge), then Replay it —
// each step camera/encoder-verified on the agent. docs/yaver-robot-teach-motor-multicam.md.
import React, { useState } from "react";
import { Pressable, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { RobotProgram, RobotStep } from "../../lib/robotClient";

const DANGER = "#ef4444";
const OK = "#22c55e";

export function TeachPendant({
  c,
  recording,
  onToggleRecord,
  steps,
  onClear,
  onSave,
  programs,
  onPlay,
  onDelete,
  busy,
}: {
  c: any;
  recording: boolean;
  onToggleRecord: () => void;
  steps: RobotStep[];
  onClear: () => void;
  onSave: (name: string) => void;
  programs: RobotProgram[];
  onPlay: (name: string) => void;
  onDelete: (name: string) => void;
  busy?: boolean;
}) {
  const [name, setName] = useState("");
  const card = { backgroundColor: c.bgCard ?? c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 16, padding: 14 } as const;

  return (
    <View style={[card, { gap: 12 }]}>
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ color: c.textPrimary, fontWeight: "800", fontSize: 16 }}>Teach &amp; Repeat</Text>
        <Pressable onPress={onToggleRecord} style={{ flexDirection: "row", alignItems: "center", gap: 6, backgroundColor: recording ? DANGER + "22" : "transparent", borderColor: recording ? DANGER : c.borderSubtle, borderWidth: 1, borderRadius: 20, paddingHorizontal: 12, paddingVertical: 7 }}>
          <View style={{ width: 10, height: 10, borderRadius: 5, backgroundColor: recording ? DANGER : c.tabInactive }} />
          <Text style={{ color: recording ? DANGER : c.textPrimary, fontWeight: "700" }}>{recording ? "Recording" : "Record"}</Text>
        </Pressable>
      </View>

      {recording && (
        <Text style={{ color: c.tabInactive, fontSize: 12 }}>
          Jog / home / drive the screwdriver — each action is captured below.
        </Text>
      )}

      {/* live recorded steps */}
      {steps.length > 0 && (
        <View style={{ gap: 4 }}>
          {steps.map((s, i) => (
            <View key={i} style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <Text style={{ color: c.tabInactive, fontSize: 11, width: 18 }}>{i + 1}</Text>
              <Text style={{ color: c.textPrimary, fontSize: 13 }}>{describeStep(s)}</Text>
            </View>
          ))}
          <View style={{ flexDirection: "row", gap: 8, marginTop: 6 }}>
            <TextInput value={name} onChangeText={setName} placeholder="program name" placeholderTextColor={c.tabInactive} style={{ flex: 1, color: c.textPrimary, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 9 }} />
            <Pressable onPress={() => { if (name.trim()) { onSave(name.trim()); setName(""); } }} disabled={!name.trim() || busy} style={{ backgroundColor: OK, borderRadius: 10, paddingHorizontal: 16, justifyContent: "center", opacity: !name.trim() || busy ? 0.5 : 1 }}>
              <Text style={{ color: "#fff", fontWeight: "700" }}>Save</Text>
            </Pressable>
            <Pressable onPress={onClear} style={{ borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, justifyContent: "center" }}>
              <Ionicons name="trash-outline" size={18} color={c.tabInactive} />
            </Pressable>
          </View>
        </View>
      )}

      {/* saved programs */}
      {programs.length > 0 && (
        <View style={{ borderTopColor: c.borderSubtle, borderTopWidth: 1, paddingTop: 10, gap: 8 }}>
          <Text style={{ color: c.tabInactive, fontSize: 11, fontWeight: "700" }}>SAVED PROGRAMS</Text>
          {programs.map((p) => (
            <View key={p.name} style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
              <View>
                <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{p.name}</Text>
                <Text style={{ color: c.tabInactive, fontSize: 12 }}>{p.steps?.length ?? 0} steps</Text>
              </View>
              <View style={{ flexDirection: "row", gap: 6 }}>
                <Pressable onPress={() => onPlay(p.name)} disabled={busy} style={{ flexDirection: "row", gap: 4, alignItems: "center", backgroundColor: c.accent, borderRadius: 10, paddingHorizontal: 14, paddingVertical: 8, opacity: busy ? 0.5 : 1 }}>
                  <Ionicons name="play" size={14} color="#fff" />
                  <Text style={{ color: "#fff", fontWeight: "700" }}>Run</Text>
                </Pressable>
                <Pressable onPress={() => onDelete(p.name)} style={{ borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 10, paddingHorizontal: 10, justifyContent: "center" }}>
                  <Ionicons name="trash-outline" size={16} color={c.tabInactive} />
                </Pressable>
              </View>
            </View>
          ))}
        </View>
      )}

      {steps.length === 0 && programs.length === 0 && !recording && (
        <Text style={{ color: c.tabInactive, fontSize: 13 }}>Tap Record, then jog the cell to a screw point and drive the screwdriver. Save the sequence to replay it verified.</Text>
      )}
    </View>
  );
}

function describeStep(s: RobotStep): string {
  switch (s.type) {
    case "home": return "Home (G28)";
    case "jog": return `Jog ${s.axis} ${s.dist! >= 0 ? "+" : ""}${s.dist}mm`;
    case "move": return `Move${s.x != null ? ` X${s.x}` : ""}${s.y != null ? ` Y${s.y}` : ""}${s.z != null ? ` Z${s.z}` : ""}`;
    case "tool": return `Screwdriver ${s.on ? "ON" : "OFF"}`;
    case "rotate": return `Rotate ${s.ccw ? "⟲" : "⟳"} ${s.turns} @ ${s.rpm}rpm`;
    case "dwell": return `Wait ${s.ms}ms`;
    case "screw": return "Screw (torque-gated)";
    default: return s.type;
  }
}
