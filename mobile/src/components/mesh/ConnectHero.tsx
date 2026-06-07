// ConnectHero.tsx — the top-of-screen "this phone" connect control, Tailscale
// style. When the native tunnel extension is present it's a real Connect /
// Disconnect toggle; when absent it degrades to a calm "manage-only" state so
// the screen is honest and fully shippable BEFORE the Phase 7 native side lands
// (it lights up automatically once isMeshTunnelSupported() flips true).

import React from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { useColors } from "../../context/ThemeContext";
import type { MeshTunnelStatus } from "../../lib/yaverMesh";
import { ChevronRightIcon, ExitNodeIcon, MeshIcon } from "./MeshIcons";

export function ConnectHero({
  supported,
  tunnel,
  busy,
  onToggle,
  exitNodeName,
  onPressExitNode,
}: {
  supported: boolean;
  tunnel: MeshTunnelStatus | null;
  busy: boolean;
  onToggle: () => void;
  exitNodeName: string | null;
  onPressExitNode: () => void;
}) {
  const c = useColors();
  const connected = tunnel?.state === "connected";
  const connecting = tunnel?.state === "connecting";

  if (!supported) {
    return (
      <View
        style={{
          borderRadius: 18,
          borderWidth: 1,
          borderColor: c.border,
          backgroundColor: c.bgCard,
          padding: 16,
          gap: 8,
        }}
      >
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <MeshIcon size={18} color={c.textMuted} />
          <Text style={{ fontSize: 15, fontWeight: "700", color: c.textPrimary }}>Manage-only</Text>
        </View>
        <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 17 }}>
          This phone manages the mesh — topology, exit nodes and access rules. On-device tunneling
          (this phone carrying mesh traffic with its own 100.96 overlay IP) arrives in a native
          update. Your desktops and servers form the data plane today.
        </Text>
      </View>
    );
  }

  const accent = connected ? "#ef4444" : "#34d399";

  return (
    <View
      style={{
        borderRadius: 18,
        borderWidth: 1,
        borderColor: connected ? "#34d39955" : c.border,
        backgroundColor: c.bgCard,
        padding: 16,
        gap: 14,
      }}
    >
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <View style={{ gap: 3 }}>
          <Text style={{ fontSize: 15, fontWeight: "700", color: c.textPrimary }}>This phone</Text>
          <Text style={{ fontSize: 12, color: c.textMuted, fontFamily: "Menlo" }}>
            {connected
              ? `Connected · ${tunnel?.meshIPv4 ?? "overlay IP pending"}`
              : connecting
                ? "Connecting…"
                : "Not connected"}
          </Text>
        </View>
        <View
          style={{
            width: 10,
            height: 10,
            borderRadius: 5,
            backgroundColor: connected ? "#34d399" : c.textMuted,
          }}
        />
      </View>

      <Pressable
        onPress={onToggle}
        disabled={busy}
        style={{
          borderRadius: 999,
          paddingVertical: 13,
          alignItems: "center",
          opacity: busy ? 0.5 : 1,
          backgroundColor: `${accent}1a`,
          borderWidth: 1,
          borderColor: `${accent}66`,
        }}
      >
        {busy ? (
          <ActivityIndicator color={accent} />
        ) : (
          <Text style={{ color: accent, fontSize: 15, fontWeight: "700" }}>
            {connected ? "Disconnect" : "Connect to mesh"}
          </Text>
        )}
      </Pressable>

      {/* Exit-node row — only actionable while connected. */}
      <Pressable
        onPress={onPressExitNode}
        disabled={!connected}
        style={{
          flexDirection: "row",
          alignItems: "center",
          gap: 10,
          opacity: connected ? 1 : 0.45,
          borderTopWidth: 1,
          borderTopColor: c.border,
          paddingTop: 12,
        }}
      >
        <ExitNodeIcon size={18} color={exitNodeName ? "#fcd34d" : c.textMuted} />
        <Text style={{ flex: 1, color: c.textPrimary, fontSize: 14 }}>Exit node</Text>
        <Text style={{ color: c.textMuted, fontSize: 13 }}>{exitNodeName ?? "None"}</Text>
        <ChevronRightIcon size={16} color={c.textMuted} />
      </Pressable>
    </View>
  );
}
