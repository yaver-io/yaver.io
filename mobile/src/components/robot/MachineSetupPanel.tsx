// MachineSetupPanel — configure a Fuju (or any external step/dir) 3-axis Cartesian
// from the app: steps/mm per axis (Yaver pushes M92 on connect so the rails move
// in real mm) + the work envelope (soft limits sized to the rails' travel). The
// Ender and a Fuju build are the same cell; only these numbers differ. Applies on
// the next agent restart (hardware config).
import React, { useState } from "react";
import { Pressable, Text, TextInput, View } from "react-native";

export function MachineSetupPanel({
  c,
  busy,
  stepsPerMm,
  envelope,
  onApply,
}: {
  c: any;
  busy?: boolean;
  stepsPerMm?: { x?: number; y?: number; z?: number };
  envelope?: { Xmax: number; Ymax: number; Zmax: number };
  onApply: (patch: { stepsPerMm: { x: number; y: number; z: number }; envelope: { Xmin: number; Xmax: number; Ymin: number; Ymax: number; Zmin: number; Zmax: number } }) => void;
}) {
  const [sx, setSx] = useState(String(stepsPerMm?.x ?? 80));
  const [sy, setSy] = useState(String(stepsPerMm?.y ?? 80));
  const [sz, setSz] = useState(String(stepsPerMm?.z ?? 400));
  const [ex, setEx] = useState(String(envelope?.Xmax ?? 220));
  const [ey, setEy] = useState(String(envelope?.Ymax ?? 220));
  const [ez, setEz] = useState(String(envelope?.Zmax ?? 250));
  const card = { backgroundColor: c.bgCard ?? c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 16, padding: 14 } as const;
  const n = (s: string) => parseFloat(s) || 0;

  return (
    <View style={[card, { gap: 12 }]}>
      <Text style={{ color: c.textPrimary, fontWeight: "800", fontSize: 16 }}>Machine setup (Fuju / external drivers)</Text>
      <Text style={{ color: c.tabInactive, fontSize: 12 }}>steps/mm per axis (rail lead × microstepping) — pushed as M92</Text>
      <View style={{ flexDirection: "row", gap: 8 }}>
        <Field c={c} label="X steps/mm" v={sx} set={setSx} />
        <Field c={c} label="Y steps/mm" v={sy} set={setSy} />
        <Field c={c} label="Z steps/mm" v={sz} set={setSz} />
      </View>
      <Text style={{ color: c.tabInactive, fontSize: 12 }}>work envelope (mm) — rail travel</Text>
      <View style={{ flexDirection: "row", gap: 8 }}>
        <Field c={c} label="X max" v={ex} set={setEx} />
        <Field c={c} label="Y max" v={ey} set={setEy} />
        <Field c={c} label="Z max" v={ez} set={setEz} />
      </View>
      <Pressable
        onPress={() =>
          onApply({
            stepsPerMm: { x: n(sx), y: n(sy), z: n(sz) },
            envelope: { Xmin: 0, Xmax: n(ex), Ymin: 0, Ymax: n(ey), Zmin: 0, Zmax: n(ez) },
          })
        }
        disabled={busy}
        style={{ backgroundColor: c.accent, borderRadius: 12, paddingVertical: 13, alignItems: "center", opacity: busy ? 0.5 : 1 }}
      >
        <Text style={{ color: "#fff", fontWeight: "800" }}>Apply (restart agent to take effect)</Text>
      </Pressable>
    </View>
  );
}

function Field({ c, label, v, set }: { c: any; label: string; v: string; set: (s: string) => void }) {
  return (
    <View style={{ flex: 1 }}>
      <Text style={{ color: c.tabInactive, fontSize: 10 }}>{label}</Text>
      <TextInput value={v} onChangeText={set} keyboardType="numbers-and-punctuation" style={{ color: c.textPrimary, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 8, paddingHorizontal: 8, paddingVertical: 8, marginTop: 2 }} />
    </View>
  );
}
