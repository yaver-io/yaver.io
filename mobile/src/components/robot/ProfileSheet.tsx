// ProfileSheet — pick the robot profile (Ender-3 Cartesian / Cartesian +
// screwdriver / screwdriver-only). The choice is saved encrypted in the edge
// vault and decides which controls the app shows. docs/yaver-robot-teach-motor-multicam.md §2b.
import React from "react";
import { Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { ProfileOption } from "../../lib/robotClient";

const OK = "#22c55e";

export function ProfileSheet({
  c,
  current,
  profiles,
  onSelect,
  busy,
}: {
  c: any;
  current?: string;
  profiles: ProfileOption[];
  onSelect: (kind: string) => void;
  busy?: boolean;
}) {
  const card = { backgroundColor: c.bgCard ?? c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 16, padding: 14 } as const;
  return (
    <View style={card}>
      <Text style={{ color: c.tabInactive, fontSize: 12, marginBottom: 8, fontWeight: "700" }}>ROBOT PROFILE</Text>
      {profiles.map((p) => {
        const sel = p.kind === current;
        return (
          <Pressable key={p.kind} onPress={() => !busy && onSelect(p.kind)} style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 10, borderBottomColor: c.borderSubtle, borderBottomWidth: 1, opacity: busy ? 0.6 : 1 }}>
            <View style={{ flex: 1, paddingRight: 10 }}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{p.label}</Text>
              <Text style={{ color: c.tabInactive, fontSize: 12, marginTop: 1 }}>{p.desc}</Text>
              <Text style={{ color: c.tabInactive, fontSize: 11, marginTop: 3 }}>{p.modules.join(" · ")}</Text>
            </View>
            {sel && <Ionicons name="checkmark-circle" size={22} color={OK} />}
          </Pressable>
        );
      })}
    </View>
  );
}
