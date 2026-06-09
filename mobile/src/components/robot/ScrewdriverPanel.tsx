// ScrewdriverPanel — drive the screwdriver motor itself: exact rotation (turns
// at rpm, CW/CCW) + optional raw GPIO. Used by the full cell AND standalone in
// the "screwdriver only" profile (no XYZ). All e-stop gated on the edge.
import React, { useState } from "react";
import { Pressable, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";

const OK = "#22c55e";

export function ScrewdriverPanel({
  c,
  disabled,
  hasTool,
  hasGpio,
  toolOn,
  onTool,
  onRotate,
  onGpio,
  onScrewHome,
}: {
  c: any;
  disabled?: boolean;
  hasTool?: boolean;
  hasGpio?: boolean;
  toolOn?: boolean;
  onTool?: (on: boolean) => void;
  onRotate: (turns: number, rpm: number, ccw: boolean) => void;
  onGpio?: (pin: number, value: number) => void;
  onScrewHome?: (pecks?: number) => void;
}) {
  const [turns, setTurns] = useState(1);
  const [rpm, setRpm] = useState(300);
  const [gpioOpen, setGpioOpen] = useState(false);
  const [pin, setPin] = useState("6");
  const [val, setVal] = useState("255");
  const card = { backgroundColor: c.bgCard ?? c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 16, padding: 14 } as const;

  const Chip = ({ label, on, onPress }: { label: string; on: boolean; onPress: () => void }) => (
    <Pressable onPress={onPress} disabled={disabled} style={{ paddingHorizontal: 12, paddingVertical: 7, borderRadius: 10, backgroundColor: on ? c.accent : "transparent", borderColor: c.borderSubtle, borderWidth: 1, opacity: disabled ? 0.5 : 1 }}>
      <Text style={{ color: on ? "#fff" : c.textPrimary, fontWeight: "700" }}>{label}</Text>
    </Pressable>
  );

  return (
    <View style={[card, { gap: 12 }]}>
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ color: c.textPrimary, fontWeight: "800", fontSize: 16 }}>Screwdriver</Text>
        {hasTool && onTool && (
          <Pressable onPress={() => onTool(!toolOn)} disabled={disabled} style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <View style={{ width: 9, height: 9, borderRadius: 5, backgroundColor: toolOn ? OK : c.tabInactive }} />
            <Text style={{ color: toolOn ? OK : c.tabInactive, fontWeight: "700" }}>{toolOn ? "ON" : "Off"}</Text>
          </Pressable>
        )}
      </View>

      {/* turns */}
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ color: c.tabInactive, fontSize: 12 }}>TURNS</Text>
        <View style={{ flexDirection: "row", gap: 8 }}>
          {[0.5, 1, 2, 5].map((t) => <Chip key={t} label={`${t}`} on={turns === t} onPress={() => setTurns(t)} />)}
        </View>
      </View>
      {/* rpm */}
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ color: c.tabInactive, fontSize: 12 }}>RPM</Text>
        <View style={{ flexDirection: "row", gap: 8 }}>
          {[100, 300, 600].map((r) => <Chip key={r} label={`${r}`} on={rpm === r} onPress={() => setRpm(r)} />)}
        </View>
      </View>

      {/* direction → rotate */}
      <View style={{ flexDirection: "row", gap: 10 }}>
        <Pressable onPress={() => onRotate(turns, rpm, false)} disabled={disabled} style={{ flex: 1, flexDirection: "row", gap: 6, alignItems: "center", justifyContent: "center", backgroundColor: c.accent, borderRadius: 12, paddingVertical: 14, opacity: disabled ? 0.5 : 1 }}>
          <Ionicons name="reload" size={18} color="#fff" />
          <Text style={{ color: "#fff", fontWeight: "800" }}>Drive ⟳ {turns}</Text>
        </Pressable>
        <Pressable onPress={() => onRotate(turns, rpm, true)} disabled={disabled} style={{ flex: 1, flexDirection: "row", gap: 6, alignItems: "center", justifyContent: "center", borderColor: c.accent, borderWidth: 1, borderRadius: 12, paddingVertical: 14, opacity: disabled ? 0.5 : 1 }}>
          <Ionicons name="reload-outline" size={18} color={c.accent} style={{ transform: [{ scaleX: -1 }] }} />
          <Text style={{ color: c.accent, fontWeight: "800" }}>Loosen ⟲ {turns}</Text>
        </Pressable>
      </View>

      {/* düz (slotted) — spin while creeping Z to catch the slot (yuva), then drive to torque */}
      {onScrewHome && (
        <View style={{ borderTopColor: c.borderSubtle, borderTopWidth: 1, paddingTop: 10, gap: 8 }}>
          <Text style={{ color: c.tabInactive, fontSize: 12, fontWeight: "700" }}>DÜZ (SLOTTED) — find slot then drive</Text>
          <View style={{ flexDirection: "row", gap: 10 }}>
            <Pressable onPress={() => onScrewHome(1)} disabled={disabled} style={{ flex: 1, flexDirection: "row", gap: 6, alignItems: "center", justifyContent: "center", backgroundColor: c.accent, borderRadius: 12, paddingVertical: 14, opacity: disabled ? 0.5 : 1 }}>
              <Ionicons name="locate" size={18} color="#fff" />
              <Text style={{ color: "#fff", fontWeight: "800" }}>Slot home</Text>
            </Pressable>
            <Pressable onPress={() => onScrewHome(3)} disabled={disabled} style={{ flex: 1, flexDirection: "row", gap: 6, alignItems: "center", justifyContent: "center", borderColor: c.accent, borderWidth: 1, borderRadius: 12, paddingVertical: 14, opacity: disabled ? 0.5 : 1 }}>
              <Ionicons name="repeat" size={18} color={c.accent} />
              <Text style={{ color: c.accent, fontWeight: "800" }}>Pecked ×3</Text>
            </Pressable>
          </View>
          <Text style={{ color: c.tabInactive, fontSize: 11 }}>Torque sensor confirms it caught (yakaladı) — seated at target.</Text>
        </View>
      )}

      {/* advanced GPIO */}
      {hasGpio && onGpio && (
        <View style={{ borderTopColor: c.borderSubtle, borderTopWidth: 1, paddingTop: 10 }}>
          <Pressable onPress={() => setGpioOpen((o) => !o)} style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <Ionicons name={gpioOpen ? "chevron-down" : "chevron-forward"} size={16} color={c.tabInactive} />
            <Text style={{ color: c.tabInactive, fontSize: 12, fontWeight: "700" }}>GPIO (M42) — driver enable / relay / dir pin</Text>
          </Pressable>
          {gpioOpen && (
            <View style={{ flexDirection: "row", gap: 8, alignItems: "center", marginTop: 10 }}>
              <Field c={c} label="pin" value={pin} onChange={setPin} />
              <Field c={c} label="value" value={val} onChange={setVal} />
              <Pressable onPress={() => onGpio(parseInt(pin, 10) || 0, parseInt(val, 10) || 0)} disabled={disabled} style={{ backgroundColor: c.accent + "22", borderColor: c.accent, borderWidth: 1, borderRadius: 10, paddingHorizontal: 14, paddingVertical: 10, opacity: disabled ? 0.5 : 1 }}>
                <Text style={{ color: c.accent, fontWeight: "700" }}>Set</Text>
              </Pressable>
            </View>
          )}
        </View>
      )}
    </View>
  );
}

function Field({ c, label, value, onChange }: { c: any; label: string; value: string; onChange: (v: string) => void }) {
  return (
    <View style={{ flex: 1 }}>
      <Text style={{ color: c.tabInactive, fontSize: 10 }}>{label}</Text>
      <TextInput value={value} onChangeText={onChange} keyboardType="number-pad" style={{ color: c.textPrimary, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8, marginTop: 2 }} placeholderTextColor={c.tabInactive} />
    </View>
  );
}
