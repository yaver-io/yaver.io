// MeshMachineRow.tsx — one row in the mesh home's "all machines" list. Unlike
// MeshNodeRow (which renders a node already ON the mesh), this row renders ANY
// machine in the account and surfaces its mesh state + the control to flip it:
//
//   • on the mesh   → green 100.96.x.x chip; tap the row → node detail.
//   • off + online  → "Enable mesh" button (stages an agent update, then up).
//   • offline       → status line carries the "power on to enable" hint; no
//                     right-side control (it used to collide with the badge).
//   • busy          → spinner + the live phase label (updating → bringing up).
//
// It deliberately reuses the visual grammar of MeshNodeRow (online dot · name ·
// mono overlay IP · chevron) so the list reads as one coherent surface.

import React from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { useColors } from "../../context/ThemeContext";
import { osGlyph, type MeshPeer } from "../../lib/meshTypes";
import { meshEnablePhaseLabel, type MeshEnablePhase } from "../../lib/meshControl";
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
  phase,
  onEnable,
  onOpen,
  onLeave,
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
  /** Which step the in-flight enable is on, so the row can narrate progress. */
  phase?: MeshEnablePhase;
  onEnable: () => void;
  /** Open the node detail screen — only meaningful when the box is a peer. */
  onOpen?: () => void;
  /**
   * Guest-only: drop my own access to this box's host. Bound to long-press
   * rather than a second right-side control, which the layout below
   * deliberately keeps to one element.
   */
  onLeave?: () => void;
}) {
  const c = useColors();
  const glyph = osGlyph(os);
  const ip = meshIPv4 || joinedPeer?.meshIPv4;
  const canOpen = meshOn && !!joinedPeer && !!onOpen;

  // One status line carries the whole left-column story so the right side stays
  // a single control. Offline folds the "power on" hint here (it used to live as
  // a second right-side label that collided with the "shared" badge).
  const statusText = meshOn
    ? ip || "on the mesh"
    : online
      ? "not on the mesh"
      : isGuest
        ? "offline · shared"
        : "offline · power on to enable";

  return (
    <Pressable
      onPress={canOpen ? onOpen : undefined}
      onLongPress={onLeave}
      // Stay pressable when the only available action is the long-press leave,
      // otherwise a shared box that isn't a mesh peer would be inert.
      disabled={!canOpen && !onLeave}
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
          <Text
            style={{ flexShrink: 1, color: c.textPrimary, fontWeight: "600", fontSize: 15 }}
            numberOfLines={1}
          >
            {glyph ? `${glyph} ` : ""}
            {name}
          </Text>
          {isGuest ? (
            <View
              style={{
                borderRadius: 6,
                paddingHorizontal: 6,
                paddingVertical: 1,
                backgroundColor: c.border,
              }}
            >
              <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 0.4 }}>
                SHARED
              </Text>
            </View>
          ) : null}
        </View>
        <Text
          style={{ color: meshOn && ip ? "#34d399" : c.textMuted, fontSize: 12, fontFamily: "Menlo" }}
          numberOfLines={1}
        >
          {statusText}
        </Text>
      </View>

      {/* Right-side control — exactly one element, so nothing collides with the
          left column. Offline boxes carry their hint in the status line above. */}
      {busy ? (
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
          <ActivityIndicator size="small" color="#34d399" />
          <Text style={{ color: "#34d399", fontSize: 12, fontWeight: "600" }}>
            {meshEnablePhaseLabel(phase)}
          </Text>
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
      ) : null}
    </Pressable>
  );
}
