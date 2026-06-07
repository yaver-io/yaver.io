// MeshNodeRow.tsx — one scannable row in the mesh home node list. Tailscale
// style: online dot · name · overlay IP (mono) · role badges · chevron.

import React from "react";
import { Pressable, Text, View } from "react-native";
import { useColors } from "../../context/ThemeContext";
import {
  effectiveRoutes,
  isExitProvider,
  isGatewayProvider,
  nodeLabel,
  type MeshPeer,
} from "../../lib/meshTypes";
import { RoleBadge } from "./RoleBadge";
import { ChevronRightIcon, ExitNodeIcon, GatewayIcon } from "./MeshIcons";

export function MeshNodeRow({
  peer,
  isSelf,
  onPress,
}: {
  peer: MeshPeer;
  isSelf?: boolean;
  onPress: () => void;
}) {
  const c = useColors();
  const exit = isExitProvider(peer);
  const gateway = isGatewayProvider(peer);
  const routeCount = effectiveRoutes(peer).length;

  return (
    <Pressable
      onPress={onPress}
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
          backgroundColor: peer.online ? "#34d399" : c.textMuted,
        }}
      />
      <View style={{ flex: 1, gap: 3 }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
          <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 15 }} numberOfLines={1}>
            {nodeLabel(peer)}
          </Text>
          {isSelf ? <Text style={{ color: "#34d399", fontSize: 11, fontWeight: "600" }}>you</Text> : null}
        </View>
        <Text style={{ color: peer.meshIPv4 ? "#34d399" : c.textMuted, fontSize: 12, fontFamily: "Menlo" }}>
          {peer.meshIPv4 || "—"}
          {peer.connectionType ? `  · ${peer.connectionType === "relay" ? "Relayed" : "Direct"}` : ""}
        </Text>
        {(exit || gateway || peer.accessScope === "shared") ? (
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: 2 }}>
            {exit ? <RoleBadge kind="exit" label="Exit node" icon={<ExitNodeIcon size={12} color="#fcd34d" />} /> : null}
            {gateway ? (
              <RoleBadge kind="gateway" label={`Gateway · ${routeCount}`} icon={<GatewayIcon size={12} color="#22d3ee" />} />
            ) : null}
            {peer.accessScope === "shared" ? <RoleBadge kind="shared" label="shared" /> : null}
          </View>
        ) : null}
      </View>
      <ChevronRightIcon size={18} color={c.textMuted} />
    </Pressable>
  );
}
