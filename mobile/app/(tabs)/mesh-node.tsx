// mesh-node.tsx — Yaver Mesh node detail. The depth layer the old flat screen
// lacked: copyable addresses, connection telemetry, and the node-role PROVIDER
// controls (serve as exit node / advertise gateway routes / bridge a Tailnet).
// The CONSUMER choice (route this node through an exit node) opens the picker.

import { useMemo } from "react";
import { ActivityIndicator, Pressable, ScrollView, Switch, Text, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useMesh } from "../../src/lib/useMesh";
import {
  TAILSCALE_BRIDGE_CIDR,
  effectiveRoutes,
  nodeLabel,
  type MeshPeer,
} from "../../src/lib/meshTypes";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { CopyableAddress } from "../../src/components/mesh/CopyableAddress";
import { CidrChips } from "../../src/components/mesh/CidrChips";
import { ChevronRightIcon, ExitNodeIcon, GatewayIcon } from "../../src/components/mesh/MeshIcons";

function relTime(ts?: number): string | null {
  if (!ts) return null;
  const secs = Math.max(0, Math.floor((Date.now() - ts) / 1000));
  if (secs < 60) return "just now";
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}

export default function MeshNodeScreen() {
  const c = useColors();
  const { deviceId } = useLocalSearchParams<{ deviceId?: string }>();
  const mesh = useMesh();

  const peer = useMemo(
    () => mesh.peers.find((p) => p.deviceId === deviceId),
    [mesh.peers, deviceId]
  );

  if (mesh.loading && !peer) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader title="Node" onBack={() => router.navigate("/(tabs)/more" as any)} />
        <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
          <ActivityIndicator color={c.textMuted} />
        </View>
      </View>
    );
  }
  if (!peer) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader title="Node" onBack={() => router.navigate("/(tabs)/more" as any)} />
        <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 24 }}>
          <Text style={{ color: c.textMuted, fontSize: 14 }}>Node not found. Pull to refresh on the mesh screen.</Text>
        </View>
      </View>
    );
  }

  const isOwner = peer.accessScope === "owner" || peer.accessScope === undefined;
  const routes = effectiveRoutes(peer).filter((r) => r !== TAILSCALE_BRIDGE_CIDR);
  const bridgingTailnet = (peer.wantRoutes ?? peer.advertisedRoutes ?? []).includes(TAILSCALE_BRIDGE_CIDR);

  const setRoutes = (next: string[]) => {
    const withBridge = bridgingTailnet ? [...next, TAILSCALE_BRIDGE_CIDR] : next;
    void mesh.saveNodeConfig(peer.deviceId, { wantRoutes: withBridge });
  };
  const toggleBridge = () => {
    const base = effectiveRoutes(peer).filter((r) => r !== TAILSCALE_BRIDGE_CIDR);
    void mesh.saveNodeConfig(peer.deviceId, {
      wantRoutes: bridgingTailnet ? base : [...base, TAILSCALE_BRIDGE_CIDR],
    });
  };

  const usingExit = peer.wantUseExitNode
    ? nodeLabel(mesh.peers.find((p) => p.deviceId === peer.wantUseExitNode) ?? ({ deviceId: peer.wantUseExitNode } as MeshPeer))
    : "None";

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Node" onBack={() => router.navigate("/(tabs)/more" as any)} />
      <ScrollView style={{ flex: 1, backgroundColor: c.bg }} contentContainerStyle={{ padding: 16, gap: 16 }}>
      {/* Identity */}
      <View style={{ gap: 8 }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
          <View style={{ width: 10, height: 10, borderRadius: 5, backgroundColor: peer.online ? "#34d399" : c.textMuted }} />
          <Text style={{ fontSize: 22, fontWeight: "700", color: c.textPrimary, flex: 1 }} numberOfLines={1}>
            {nodeLabel(peer)}
          </Text>
        </View>
        <Text style={{ fontSize: 12, color: c.textMuted }}>
          {peer.online ? "Online" : "Offline"}
          {peer.accessScope === "shared" ? " · shared with you" : ""}
          {peer.os ? ` · ${peer.os}` : ""}
          {peer.clientVersion ? ` · v${peer.clientVersion}` : ""}
        </Text>
      </View>

      {/* Addresses */}
      <Card title="ADDRESSES">
        {peer.meshIPv4 ? <CopyableAddress label="Overlay" value={peer.meshIPv4} /> : null}
        {peer.meshIPv6 ? <CopyableAddress label="IPv6" value={peer.meshIPv6} tint="#22d3ee" /> : null}
        {peer.magicDns ? <CopyableAddress label="DNS" value={peer.magicDns} tint="#c4b5fd" /> : null}
        {!peer.meshIPv4 && !peer.magicDns ? (
          <Text style={{ color: c.textMuted, fontSize: 13 }}>No overlay address yet.</Text>
        ) : null}
      </Card>

      {/* Connection */}
      {(peer.connectionType || peer.lastHandshake) ? (
        <Card title="CONNECTION">
          {peer.connectionType ? (
            <Row k="Path" v={peer.connectionType === "relay" ? "Relayed (DERP)" : "Direct"} />
          ) : null}
          {relTime(peer.lastHandshake) ? <Row k="Last seen" v={relTime(peer.lastHandshake)!} /> : null}
        </Card>
      ) : null}

      {/* Provider axis — owner only */}
      {isOwner ? (
        <Card title="THIS NODE SERVES AS">
          <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
            <ExitNodeIcon size={18} color={peer.wantExitNode ? "#fcd34d" : c.textMuted} />
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>Exit node</Text>
              <Text style={{ color: c.textMuted, fontSize: 12 }}>Route peers' full internet traffic through this node.</Text>
            </View>
            <Switch
              value={!!peer.wantExitNode}
              onValueChange={(v) => void mesh.saveNodeConfig(peer.deviceId, { wantExitNode: v })}
            />
          </View>

          <View style={{ height: 1, backgroundColor: c.border, marginVertical: 12 }} />

          <View style={{ flexDirection: "row", alignItems: "center", gap: 10, marginBottom: 8 }}>
            <GatewayIcon size={18} color={routes.length || bridgingTailnet ? "#22d3ee" : c.textMuted} />
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>Gateway routes</Text>
              <Text style={{ color: c.textMuted, fontSize: 12 }}>Advertise LAN subnets so peers reach networks behind this node (a.k.a. subnet router).</Text>
            </View>
          </View>
          <CidrChips routes={routes} onChange={setRoutes} />

          <Pressable
            onPress={toggleBridge}
            style={{
              marginTop: 10,
              borderRadius: 999,
              paddingHorizontal: 12,
              paddingVertical: 6,
              alignSelf: "flex-start",
              borderWidth: 1,
              borderColor: bridgingTailnet ? "#22d3ee55" : c.border,
              backgroundColor: bridgingTailnet ? "#22d3ee1f" : c.bg,
            }}
          >
            <Text style={{ color: bridgingTailnet ? "#22d3ee" : c.textMuted, fontSize: 12, fontWeight: "600" }}>
              {bridgingTailnet ? "✓ Bridging Tailnet (100.64/10)" : "Bridge my Tailnet"}
            </Text>
          </Pressable>
        </Card>
      ) : null}

      {/* Consumer axis — route this node through an exit node */}
      {isOwner ? (
        <Pressable
          onPress={() => router.navigate({ pathname: "/(tabs)/mesh-exit", params: { deviceId: peer.deviceId } } as any)}
          style={{ flexDirection: "row", alignItems: "center", gap: 10, borderRadius: 14, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, padding: 14 }}
        >
          <ExitNodeIcon size={18} color={peer.wantUseExitNode ? "#fcd34d" : c.textMuted} />
          <Text style={{ flex: 1, color: c.textPrimary, fontSize: 14 }}>Route through exit node</Text>
          <Text style={{ color: c.textMuted, fontSize: 13 }}>{usingExit}</Text>
          <ChevronRightIcon size={16} color={c.textMuted} />
        </Pressable>
      ) : null}
      </ScrollView>
    </View>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  const c = useColors();
  return (
    <View style={{ gap: 10 }}>
      <Text style={{ fontSize: 12, fontWeight: "700", letterSpacing: 1.2, color: c.textMuted }}>{title}</Text>
      <View style={{ borderRadius: 14, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, padding: 14 }}>
        {children}
      </View>
    </View>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  const c = useColors();
  return (
    <View style={{ flexDirection: "row", justifyContent: "space-between", paddingVertical: 2 }}>
      <Text style={{ color: c.textMuted, fontSize: 13 }}>{k}</Text>
      <Text style={{ color: c.textPrimary, fontSize: 13 }}>{v}</Text>
    </View>
  );
}
