// MeshMachineRow.tsx — one row in the mesh home's "all machines" list. Unlike
// MeshNodeRow (which renders a node already ON the mesh), this row renders ANY
// machine in the account and surfaces its mesh state + the control to flip it:
//
//   • on the mesh   → green 100.96.x.x chip; tap the row → node detail.
//   • off + online  → "Enable mesh" button (stages an agent update, then up).
//   • offline       → muted "Power on to enable"; control disabled.
//   • busy          → spinner.
//
// It deliberately reuses the visual grammar of MeshNodeRow (online dot · name ·
// mono overlay IP · chevron) so the list reads as one coherent surface.

import React from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { useColors } from "../../context/ThemeContext";
import { osGlyph, type MeshPeer } from "../../lib/meshTypes";
import { ChevronRightIcon } from "./MeshIcons";

export function MeshMachineRow({
  name,
  os,
  online,
  isGuest,
  meshOn,
  meshIPv4,
  joinedPeer,
  busy,
  onEnable,
  onOpen,
}: {
  name: string;
  os?: string;
  online: boolean;
  isGuest?: boolean;
  meshOn: boolean;
  meshIPv4?: string;
  /** The matching /mesh/peers entry, when the box is already a known node. */
  joinedPeer?: MeshPeer;
  busy?: boolean;
  onEnable: () => void;
  /** Open the node detail screen — only meaningful when the box is a peer. */
  onOpen?: () => void;
}) {
  const c = useColors();
  const glyph = osGlyph(os);
  const ip = meshIPv4 || joinedPeer?.meshIPv4;
  const canOpen = meshOn && !!joinedPeer && !!onOpen;

  return (
    <Pressable
      onPress={canOpen ? onOpen : undefined}
      disabled={!canOpen}
      style={{
        flexDirection: "row",
        alignItems: "center",
        gap: 10,
        borderRadius: 14,
        borderWidth: 1,
        borderColor: c.border,
        backgroundColor: c.bgCard,
        padding: 12,
      }}
    >
      <View
        style={{
          width: 8,
          height: 8,
          borderRadius: 4,
          backgroundColor: online ? "#34d399" : c.textMuted,
        }}
      />
      <View style={{ flex: 1, gap: 3 }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
          <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 15 }} numberOfLines={1}>
            {glyph ? `${glyph} ` : ""}
            {name}
          </Text>
          {isGuest ? <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600" }}>shared</Text> : null}
        </View>
        <Text style={{ color: meshOn && ip ? "#34d399" : c.textMuted, fontSize: 12, fontFamily: "Menlo" }}>
          {meshOn ? ip || "on the mesh" : online ? "not on the mesh" : "offline"}
        </Text>
      </View>

      {/* Right-side control. */}
      {busy ? (
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
          <ActivityIndicator size="small" color={c.textMuted} />
          <Text style={{ color: c.textMuted, fontSize: 12 }}>Enabling…</Text>
        </View>
      ) : meshOn ? (
        canOpen ? (
          <ChevronRightIcon size={18} color={c.textMuted} />
        ) : (
          <Text style={{ color: "#34d399", fontSize: 12, fontWeight: "700" }}>On</Text>
        )
      ) : online ? (
        <Pressable
          onPress={onEnable}
          hitSlop={8}
          style={{
            borderRadius: 10,
            borderWidth: 1,
            borderColor: "#34d39955",
            backgroundColor: "#34d39912",
            paddingVertical: 7,
            paddingHorizontal: 12,
          }}
        >
          <Text style={{ color: "#34d399", fontSize: 13, fontWeight: "700" }}>Enable mesh</Text>
        </Pressable>
      ) : (
        <Text style={{ color: c.textMuted, fontSize: 11 }}>Power on to enable</Text>
      )}
    </Pressable>
  );
}
