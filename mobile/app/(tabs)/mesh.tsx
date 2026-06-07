// mesh.tsx — Yaver Mesh home (mobile), Tailscale-style. Leads with a Connect
// hero for THIS phone, then a grouped + searchable node list, with Access rules
// and Sharing demoted to footer entries. Replaces the flat admin scroll that
// used to live in network.tsx. See docs/yaver-mesh-mobile-tailscale-ui-design.md.

import { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Pressable, RefreshControl, ScrollView, Text, TextInput, View } from "react-native";
import { router } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useAuth } from "../../src/context/AuthContext";
import { CONVEX_SITE_URL } from "../../src/_core/constants";
import { useMesh } from "../../src/lib/useMesh";
import {
  ensureMeshKeyPair,
  isMeshTunnelSupported,
  meshDeviceIdFromPubKey,
  meshTunnelDown,
  meshTunnelStatus,
  meshTunnelUp,
  type MeshTunnelStatus,
} from "../../src/lib/yaverMesh";
import { nodeLabel, type MeshPeer } from "../../src/lib/meshTypes";
import { ConnectHero } from "../../src/components/mesh/ConnectHero";
import { MeshNodeRow } from "../../src/components/mesh/MeshNodeRow";
import { ChevronRightIcon, SearchIcon } from "../../src/components/mesh/MeshIcons";

export default function MeshHomeScreen() {
  const c = useColors();
  const { token } = useAuth();
  const mesh = useMesh();
  const [query, setQuery] = useState("");

  const tunnelSupported = isMeshTunnelSupported();
  const [tunnel, setTunnel] = useState<MeshTunnelStatus | null>(null);
  const [tunnelBusy, setTunnelBusy] = useState(false);
  const [selfId, setSelfId] = useState<string | null>(null);

  useEffect(() => {
    if (!tunnelSupported) return;
    void meshTunnelStatus().then(setTunnel);
    void ensureMeshKeyPair().then((pk) => {
      if (pk) setSelfId(meshDeviceIdFromPubKey(pk));
    });
  }, [tunnelSupported]);

  const toggleTunnel = useCallback(async () => {
    if (!token || tunnelBusy) return;
    setTunnelBusy(true);
    try {
      const connected = tunnel?.state === "connected";
      const next = connected
        ? await meshTunnelDown()
        : await meshTunnelUp({ convexSiteUrl: CONVEX_SITE_URL, token });
      setTunnel(next);
      if (next.state === "error" && next.error) mesh.setError(next.error);
      void mesh.reload();
    } finally {
      setTunnelBusy(false);
    }
  }, [token, tunnel, tunnelBusy, mesh]);

  const selfPeer = useMemo(
    () => (selfId ? mesh.peers.find((p) => p.deviceId === selfId) : undefined),
    [mesh.peers, selfId]
  );
  const exitNodeName = useMemo(() => {
    const id = selfPeer?.wantUseExitNode;
    if (!id) return null;
    const ex = mesh.peers.find((p) => p.deviceId === id);
    return ex ? nodeLabel(ex) : id.slice(0, 8);
  }, [selfPeer, mesh.peers]);

  const { mine, shared } = useMemo(() => {
    const q = query.trim().toLowerCase();
    const match = (p: MeshPeer) =>
      !q || nodeLabel(p).toLowerCase().includes(q) || (p.meshIPv4 ?? "").includes(q);
    const visible = mesh.peers.filter(match);
    return {
      mine: visible.filter((p) => p.accessScope !== "shared" && p.accessScope !== "peer"),
      shared: visible.filter((p) => p.accessScope === "shared" || p.accessScope === "peer"),
    };
  }, [mesh.peers, query]);

  const openNode = (deviceId: string) =>
    router.navigate({ pathname: "/(tabs)/mesh-node", params: { deviceId } } as any);

  const openExitPicker = () => {
    if (!selfId) return;
    router.navigate({ pathname: "/(tabs)/mesh-exit", params: { deviceId: selfId } } as any);
  };

  return (
    <ScrollView
      style={{ flex: 1, backgroundColor: c.bg }}
      contentContainerStyle={{ padding: 16, gap: 16 }}
      refreshControl={<RefreshControl refreshing={mesh.loading} onRefresh={() => void mesh.reload()} tintColor={c.textMuted} />}
    >
      <ConnectHero
        supported={tunnelSupported}
        tunnel={tunnel}
        busy={tunnelBusy}
        onToggle={() => void toggleTunnel()}
        exitNodeName={exitNodeName}
        onPressExitNode={openExitPicker}
      />

      {mesh.error ? (
        <View style={{ borderRadius: 14, borderWidth: 1, borderColor: "#ef444455", backgroundColor: "#ef444415", padding: 12 }}>
          <Text style={{ color: "#fca5a5", fontSize: 13 }}>{mesh.error}</Text>
        </View>
      ) : null}

      {mesh.peers.length > 6 ? (
        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            gap: 8,
            borderRadius: 12,
            borderWidth: 1,
            borderColor: c.border,
            backgroundColor: c.bgCard,
            paddingHorizontal: 12,
          }}
        >
          <SearchIcon size={16} color={c.textMuted} />
          <TextInput
            value={query}
            onChangeText={setQuery}
            placeholder="Search machines"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            autoCorrect={false}
            style={{ flex: 1, color: c.textPrimary, paddingVertical: 10, fontSize: 14 }}
          />
        </View>
      ) : null}

      {mesh.loading && mesh.peers.length === 0 ? (
        <ActivityIndicator color={c.textMuted} />
      ) : mesh.peers.length === 0 ? (
        <View style={{ borderRadius: 14, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, padding: 16, gap: 6 }}>
          <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>No machines on your mesh yet</Text>
          <Text style={{ color: c.textMuted, fontSize: 13, lineHeight: 18 }}>
            Bring a device on with <Text style={{ fontFamily: "Menlo" }}>yaver mesh up</Text>. It gets a stable
            100.96 overlay IP every other node can reach — a Tailscale alternative across your fleet.
          </Text>
        </View>
      ) : (
        <>
          {mine.length > 0 ? (
            <NodeSection title="MY DEVICES" peers={mine} selfId={selfId} onOpen={openNode} />
          ) : null}
          {shared.length > 0 ? (
            <NodeSection title="SHARED WITH ME" peers={shared} selfId={selfId} onOpen={openNode} />
          ) : null}
        </>
      )}

      <View style={{ gap: 8, marginTop: 4 }}>
        <FooterLink label="Access rules" sub="Who can reach what — ACLs & device tags" onPress={() => router.navigate("/(tabs)/mesh-access" as any)} />
        <FooterLink label="Sharing" sub="Support a friend · who can access your machines" onPress={() => router.navigate("/(tabs)/mesh-share" as any)} />
      </View>
    </ScrollView>
  );
}

function NodeSection({
  title,
  peers,
  selfId,
  onOpen,
}: {
  title: string;
  peers: MeshPeer[];
  selfId: string | null;
  onOpen: (deviceId: string) => void;
}) {
  const c = useColors();
  return (
    <View style={{ gap: 8 }}>
      <Text style={{ fontSize: 12, fontWeight: "700", letterSpacing: 1.2, color: c.textMuted }}>{title}</Text>
      {peers.map((p) => (
        <MeshNodeRow key={p.deviceId} peer={p} isSelf={p.deviceId === selfId} onPress={() => onOpen(p.deviceId)} />
      ))}
    </View>
  );
}

function FooterLink({ label, sub, onPress }: { label: string; sub: string; onPress: () => void }) {
  const c = useColors();
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
        padding: 14,
      }}
    >
      <View style={{ flex: 1 }}>
        <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }}>{label}</Text>
        <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>{sub}</Text>
      </View>
      <ChevronRightIcon size={18} color={c.textMuted} />
    </Pressable>
  );
}
