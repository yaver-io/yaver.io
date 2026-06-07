// RoleBadge.tsx — the typed pill that renders a node's role/state consistently
// across the mesh home row, node detail, and exit-node picker. Colors match the
// existing mesh palette so nothing visually jumps when this lands alongside the
// old network.tsx hexes.

import React from "react";
import { Text, View } from "react-native";

export type RoleKind = "exit" | "gateway" | "shared" | "tag" | "tailnet" | "you" | "neutral";

// Mesh role palette (kept from the prior inline hexes in network.tsx):
//   exit = amber, gateway/tailnet = cyan, shared = violet.
export const ROLE_COLOR: Record<RoleKind, string> = {
  exit: "#fcd34d",
  gateway: "#22d3ee",
  tailnet: "#22d3ee",
  shared: "#c4b5fd",
  tag: "#94a3b8",
  you: "#34d399",
  neutral: "#94a3b8",
};

export function RoleBadge({
  kind,
  label,
  icon,
}: {
  kind: RoleKind;
  label: string;
  icon?: React.ReactNode;
}) {
  const color = ROLE_COLOR[kind];
  return (
    <View
      style={{
        flexDirection: "row",
        alignItems: "center",
        gap: 4,
        borderRadius: 999,
        paddingHorizontal: 8,
        paddingVertical: 2,
        backgroundColor: `${color}1f`,
        borderWidth: 1,
        borderColor: `${color}55`,
      }}
    >
      {icon}
      <Text style={{ color, fontSize: 11, fontWeight: "600" }}>{label}</Text>
    </View>
  );
}
