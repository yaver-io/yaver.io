// CalibrationPanel — user-assisted screwdriver setup for a Cartesian cell, the
// way the commercial desktop screw-fastening robots do it:
//   1) jog Z down SLOWLY while watching the camera until the bit just meets the
//      screw head → "Set engage height" (Z touch-off).
//   2) lift to a clear travel height → "Set safe height".
//   3) pick the seat torque for terminal blocks.
// All saved to the edge config; the teach program + drive-home then use them.
import React, { useState } from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";

const OK = "#22c55e";
const WARN = "#f59e0b";

const Z_STEPS = [0.1, 0.5, 1, 5];
const TORQUE_PRESETS = [50, 100, 200, 400, 600];

export function CalibrationPanel({
  c,
  disabled,
  homed,
  currentZ,
  companion,
  liveTorque,
  zEngage,
  zSafe,
  targetTorqueNmm,
  onJogZ,
  onSetEngage,
  onSetSafe,
  onSetTarget,
  onTestScrew,
}: {
  c: any;
  disabled?: boolean;
  homed?: boolean;
  currentZ?: number;
  companion?: boolean;
  liveTorque?: number | null;
  zEngage?: number;
  zSafe?: number;
  targetTorqueNmm?: number;
  onJogZ: (dist: number) => void;
  onSetEngage: () => void;
  onSetSafe: () => void;
  onSetTarget: (nmm: number) => void;
  onTestScrew: () => void;
}) {
  const [zStep, setZStep] = useState(0.5);
  const card = { backgroundColor: c.bgCard ?? c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 16, padding: 14 } as const;
  const seated = companion && liveTorque != null && targetTorqueNmm ? liveTorque >= targetTorqueNmm : false;

  const Chip = ({ label, on, onPress }: { label: string; on: boolean; onPress: () => void }) => (
    <Pressable onPress={onPress} disabled={disabled} style={{ paddingHorizontal: 11, paddingVertical: 6, borderRadius: 10, backgroundColor: on ? c.accent : "transparent", borderColor: c.borderSubtle, borderWidth: 1, opacity: disabled ? 0.5 : 1 }}>
      <Text style={{ color: on ? "#fff" : c.textPrimary, fontWeight: "700", fontSize: 13 }}>{label}</Text>
    </Pressable>
  );

  return (
    <View style={[card, { gap: 12 }]}>
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ color: c.textPrimary, fontWeight: "800", fontSize: 16 }}>Calibration</Text>
        <Text style={{ color: c.tabInactive, fontSize: 12 }}>Z {currentZ != null ? currentZ.toFixed(2) : "—"}mm</Text>
      </View>

      {!homed && <Text style={{ color: WARN, fontSize: 12 }}>Home first so Z is referenced.</Text>}

      {/* Z touch-off */}
      <Text style={{ color: c.tabInactive, fontSize: 12 }}>
        Lower Z slowly until the bit just meets the screw head (watch the camera), then set the engage height.
      </Text>
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <View style={{ flexDirection: "row", gap: 6 }}>
          {Z_STEPS.map((s) => <Chip key={s} label={`${s}`} on={zStep === s} onPress={() => setZStep(s)} />)}
        </View>
        <View style={{ flexDirection: "row", gap: 8 }}>
          <Pressable onPress={() => onJogZ(zStep)} disabled={disabled} style={{ width: 56, height: 44, borderRadius: 10, alignItems: "center", justifyContent: "center", borderColor: c.borderSubtle, borderWidth: 1, opacity: disabled ? 0.5 : 1 }}>
            <Ionicons name="arrow-up" size={18} color={c.textPrimary} />
          </Pressable>
          <Pressable onPress={() => onJogZ(-zStep)} disabled={disabled} style={{ width: 56, height: 44, borderRadius: 10, alignItems: "center", justifyContent: "center", backgroundColor: c.accent + "1A", borderColor: c.accent, borderWidth: 1, opacity: disabled ? 0.5 : 1 }}>
            <Ionicons name="arrow-down" size={18} color={c.accent} />
          </Pressable>
        </View>
      </View>

      <View style={{ flexDirection: "row", gap: 8 }}>
        <Pressable onPress={onSetEngage} disabled={disabled} style={{ flex: 1, alignItems: "center", borderColor: c.accent, borderWidth: 1, borderRadius: 10, paddingVertical: 10, opacity: disabled ? 0.5 : 1 }}>
          <Text style={{ color: c.accent, fontWeight: "700" }}>Set engage</Text>
          <Text style={{ color: c.tabInactive, fontSize: 11 }}>{zEngage ? `${zEngage.toFixed(2)}mm` : "unset"}</Text>
        </Pressable>
        <Pressable onPress={onSetSafe} disabled={disabled} style={{ flex: 1, alignItems: "center", borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 10, paddingVertical: 10, opacity: disabled ? 0.5 : 1 }}>
          <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Set safe</Text>
          <Text style={{ color: c.tabInactive, fontSize: 11 }}>{zSafe ? `${zSafe.toFixed(2)}mm` : "unset"}</Text>
        </Pressable>
      </View>

      {/* torque target */}
      <View style={{ borderTopColor: c.borderSubtle, borderTopWidth: 1, paddingTop: 12, gap: 8 }}>
        <Text style={{ color: c.tabInactive, fontSize: 12 }}>SEAT TORQUE (terminal blocks) — N·mm</Text>
        <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
          {TORQUE_PRESETS.map((t) => <Chip key={t} label={`${t}`} on={targetTorqueNmm === t} onPress={() => onSetTarget(t)} />)}
        </View>

        {/* live torque gauge */}
        {companion ? (
          <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginTop: 4 }}>
            <View>
              <Text style={{ color: c.tabInactive, fontSize: 11 }}>LIVE TORQUE</Text>
              <Text style={{ color: seated ? OK : c.textPrimary, fontSize: 24, fontWeight: "800" }}>
                {liveTorque != null ? liveTorque.toFixed(0) : "—"}
                <Text style={{ fontSize: 13, color: c.tabInactive }}> / {targetTorqueNmm || "—"} N·mm</Text>
              </Text>
            </View>
            <Pressable onPress={onTestScrew} disabled={disabled} style={{ flexDirection: "row", gap: 6, alignItems: "center", backgroundColor: c.accent, borderRadius: 12, paddingHorizontal: 16, paddingVertical: 12, opacity: disabled ? 0.5 : 1 }}>
              <Ionicons name="hardware-chip-outline" size={16} color="#fff" />
              <Text style={{ color: "#fff", fontWeight: "800" }}>Drive home (test)</Text>
            </Pressable>
          </View>
        ) : (
          <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginTop: 4 }}>
            <Text style={{ color: c.tabInactive, fontSize: 12, flex: 1 }}>No torque sensor — drive-home runs open-loop to the plunge floor. Add a companion for closed-loop seating.</Text>
            <Pressable onPress={onTestScrew} disabled={disabled} style={{ backgroundColor: c.accent, borderRadius: 12, paddingHorizontal: 14, paddingVertical: 10, opacity: disabled ? 0.5 : 1 }}>
              <Text style={{ color: "#fff", fontWeight: "800" }}>Drive home</Text>
            </Pressable>
          </View>
        )}
      </View>
    </View>
  );
}
