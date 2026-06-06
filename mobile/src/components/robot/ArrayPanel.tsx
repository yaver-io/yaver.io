// ArrayPanel — lay out a strip/grid of klemens (terminal blocks) and generate the
// fastening program in one shot. Grid = a jig in the Cartesian work area (travel
// to each); Linear = a single rail indexes a strip past a fixed screwdriver.
// "Use current as origin" anchors the layout where you jogged to. The saved
// program then appears under Teach & Repeat to Run.
import React, { useState } from "react";
import { Pressable, Switch, Text, TextInput, View } from "react-native";
import type { ArrayParams } from "../../lib/robotClient";

export function ArrayPanel({ c, disabled, busy, onGenerate }: { c: any; disabled?: boolean; busy?: boolean; onGenerate: (p: ArrayParams) => void }) {
  const [mode, setMode] = useState<"grid" | "linear">("grid");
  const [name, setName] = useState("klemens-strip");
  const [cols, setCols] = useState("5");
  const [rows, setRows] = useState("2");
  const [pitchX, setPitchX] = useState("15");
  const [pitchY, setPitchY] = useState("15");
  const [axis, setAxis] = useState<"X" | "Y">("X");
  const [count, setCount] = useState("10");
  const [pitch, setPitch] = useState("8");
  const [torque, setTorque] = useState("400");
  const [capture, setCapture] = useState(true);
  const [home, setHome] = useState(true);
  const card = { backgroundColor: c.bgCard ?? c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 16, padding: 14 } as const;
  const n = (s: string) => parseFloat(s) || 0;

  const generate = () => {
    const base: ArrayParams = {
      name: name.trim() || "klemens-strip",
      mode,
      targetTorqueNmm: n(torque),
      home,
      captureOrigin: capture,
    };
    if (mode === "grid") onGenerate({ ...base, cols: n(cols), rows: n(rows), pitchX: n(pitchX), pitchY: n(pitchY), serpentine: true });
    else onGenerate({ ...base, axis, count: n(count), pitch: n(pitch) });
  };

  const Tab = ({ m, label }: { m: "grid" | "linear"; label: string }) => (
    <Pressable onPress={() => setMode(m)} style={{ flex: 1, alignItems: "center", paddingVertical: 8, borderRadius: 10, backgroundColor: mode === m ? c.accent : "transparent", borderColor: c.borderSubtle, borderWidth: 1 }}>
      <Text style={{ color: mode === m ? "#fff" : c.textPrimary, fontWeight: "700" }}>{label}</Text>
    </Pressable>
  );

  return (
    <View style={[card, { gap: 12 }]}>
      <Text style={{ color: c.textPrimary, fontWeight: "800", fontSize: 16 }}>Klemens array</Text>
      <View style={{ flexDirection: "row", gap: 8 }}>
        <Tab m="grid" label="Grid (jig)" />
        <Tab m="linear" label="Linear (rail)" />
      </View>

      {mode === "grid" ? (
        <View style={{ flexDirection: "row", gap: 8 }}>
          <Field c={c} label="cols" v={cols} set={setCols} />
          <Field c={c} label="rows" v={rows} set={setRows} />
          <Field c={c} label="pitchX" v={pitchX} set={setPitchX} />
          <Field c={c} label="pitchY" v={pitchY} set={setPitchY} />
        </View>
      ) : (
        <View style={{ flexDirection: "row", gap: 8, alignItems: "flex-end" }}>
          <View>
            <Text style={{ color: c.tabInactive, fontSize: 10 }}>axis</Text>
            <Pressable onPress={() => setAxis(axis === "X" ? "Y" : "X")} style={{ borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 8, paddingHorizontal: 14, paddingVertical: 9, marginTop: 2 }}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{axis}</Text>
            </Pressable>
          </View>
          <Field c={c} label="count" v={count} set={setCount} />
          <Field c={c} label="pitch" v={pitch} set={setPitch} />
        </View>
      )}

      <View style={{ flexDirection: "row", gap: 8, alignItems: "flex-end" }}>
        <Field c={c} label="torque N·mm" v={torque} set={setTorque} />
        <View style={{ flex: 1 }}>
          <Text style={{ color: c.tabInactive, fontSize: 10 }}>name</Text>
          <TextInput value={name} onChangeText={setName} style={{ color: c.textPrimary, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8, marginTop: 2 }} />
        </View>
      </View>

      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ color: c.textPrimary }}>Use current position as origin</Text>
        <Switch value={capture} onValueChange={setCapture} />
      </View>
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ color: c.textPrimary }}>Home first</Text>
        <Switch value={home} onValueChange={setHome} />
      </View>

      <Pressable onPress={generate} disabled={disabled || busy} style={{ backgroundColor: c.accent, borderRadius: 12, paddingVertical: 13, alignItems: "center", opacity: disabled || busy ? 0.5 : 1 }}>
        <Text style={{ color: "#fff", fontWeight: "800" }}>Generate program</Text>
      </Pressable>
      <Text style={{ color: c.tabInactive, fontSize: 11 }}>
        {capture ? "Jog the tool over the first klemens, then Generate. " : ""}The program appears under Teach &amp; Repeat to Run.
      </Text>
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
