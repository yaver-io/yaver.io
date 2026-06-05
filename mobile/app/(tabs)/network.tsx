// network.tsx — Yaver Mesh console (mobile). Phase 6: console/status only — the
// phone shows the mesh topology and edits access rules, but does NOT itself
// carry VPN traffic yet (the on-device PacketTunnel is a later phase). Mesh data
// flows through the Convex /mesh/* HTTP routes using the session token.

import { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  Text,
  TextInput,
  View,
} from "react-native";
import { useColors } from "../../src/context/ThemeContext";
import { useAuth } from "../../src/context/AuthContext";
import { CONVEX_SITE_URL } from "../../src/_core/constants";

type MeshPeer = {
  deviceId: string;
  alias?: string;
  meshIPv4?: string;
  online?: boolean;
  isExitNode?: boolean;
  accessScope?: "owner" | "shared";
  advertisedRoutes?: string[];
  wantExitNode?: boolean;
  wantUseExitNode?: string;
  wantRoutes?: string[];
};

type ACLRule = {
  srcType: "tag" | "device" | "user" | "any";
  src: string;
  dstType: "tag" | "device" | "user" | "any";
  dst: string;
  ports: string[];
  action: "accept" | "drop";
};

export default function NetworkScreen() {
  const c = useColors();
  const { token } = useAuth();
  const [peers, setPeers] = useState<MeshPeer[]>([]);
  const [rules, setRules] = useState<ACLRule[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setError(null);
    const headers = { Authorization: `Bearer ${token}` };
    try {
      const [pRes, aRes] = await Promise.all([
        fetch(`${CONVEX_SITE_URL}/mesh/peers`, { headers }),
        fetch(`${CONVEX_SITE_URL}/mesh/acls`, { headers }),
      ]);
      if (!pRes.ok) throw new Error(`peers: HTTP ${pRes.status}`);
      const pJson = await pRes.json();
      setPeers(pJson.peers ?? []);
      if (aRes.ok) {
        const aJson = await aRes.json();
        setRules(aJson.rules ?? []);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  const saveNodeConfig = useCallback(
    async (deviceId: string, patch: Partial<Pick<MeshPeer, "wantExitNode" | "wantUseExitNode" | "wantRoutes">> & { wantEnabled?: boolean }) => {
      if (!token) return;
      setPeers((prev) => prev.map((p) => (p.deviceId === deviceId ? { ...p, ...patch } : p)));
      try {
        await fetch(`${CONVEX_SITE_URL}/mesh/node/config`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: JSON.stringify({ deviceId, ...patch }),
        });
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        void load();
      }
    },
    [token, load]
  );

  const saveRules = useCallback(
    async (next: ACLRule[]) => {
      if (!token) return;
      setRules(next);
      try {
        await fetch(`${CONVEX_SITE_URL}/mesh/acls/set`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: JSON.stringify({ rules: next }),
        });
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [token]
  );

  return (
    <ScrollView
      style={{ flex: 1, backgroundColor: c.bg }}
      contentContainerStyle={{ padding: 16, gap: 16 }}
    >
      <View style={{ gap: 6 }}>
        <Text style={{ fontSize: 22, fontWeight: "700", color: c.textPrimary }}>Mesh Network</Text>
        <Text style={{ fontSize: 13, color: c.textMuted, lineHeight: 18 }}>
          Yaver Mesh is an optional WireGuard overlay — a Tailscale alternative across your
          fleet. Bring a machine on with <Text style={{ fontWeight: "600" }}>yaver mesh up</Text>;
          it gets a stable overlay IP every other node can reach. This phone manages the mesh;
          on-device tunneling arrives in a later update.
        </Text>
      </View>

      {error ? (
        <View style={{ borderRadius: 14, borderWidth: 1, borderColor: "#ef444455", backgroundColor: "#ef444415", padding: 12 }}>
          <Text style={{ color: "#fca5a5", fontSize: 13 }}>{error}</Text>
        </View>
      ) : null}

      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ fontSize: 12, fontWeight: "700", letterSpacing: 1.2, color: c.textMuted }}>
          MESH NODES
        </Text>
        <Pressable
          onPress={() => void load()}
          style={{ borderRadius: 999, borderWidth: 1, borderColor: c.border, paddingHorizontal: 12, paddingVertical: 4 }}
        >
          <Text style={{ color: c.textMuted, fontSize: 12 }}>Refresh</Text>
        </Pressable>
      </View>

      {loading ? (
        <ActivityIndicator color={c.textMuted} />
      ) : peers.length === 0 ? (
        <Text style={{ color: c.textMuted, fontSize: 13 }}>
          No mesh nodes yet. Run `yaver mesh up` on a device.
        </Text>
      ) : (
        <View style={{ gap: 8 }}>
          {peers.map((p) => {
            const isOwner = p.accessScope !== "shared";
            const advertisingExit = p.isExitNode || p.wantExitNode;
            const exitOptions = peers.filter((x) => x.deviceId !== p.deviceId && (x.isExitNode || x.wantExitNode));
            const usingName =
              p.wantUseExitNode
                ? peers.find((x) => x.deviceId === p.wantUseExitNode)?.alias || p.wantUseExitNode.slice(0, 8)
                : "none";
            const cycleExitNode = () => {
              const ids = ["", ...exitOptions.map((x) => x.deviceId)];
              const cur = ids.indexOf(p.wantUseExitNode || "");
              const next = ids[(cur + 1) % ids.length];
              void saveNodeConfig(p.deviceId, { wantUseExitNode: next });
            };
            return (
              <View
                key={p.deviceId}
                style={{
                  borderRadius: 14,
                  borderWidth: 1,
                  borderColor: c.border,
                  backgroundColor: c.bgCard,
                  padding: 12,
                  gap: 8,
                }}
              >
                <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
                  <View
                    style={{
                      width: 8,
                      height: 8,
                      borderRadius: 4,
                      backgroundColor: p.online ? "#34d399" : c.textMuted,
                    }}
                  />
                  <View style={{ flex: 1 }}>
                    <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{p.alias || p.deviceId}</Text>
                    <Text style={{ color: "#34d399", fontSize: 12, fontFamily: "Menlo" }}>{p.meshIPv4 || "—"}</Text>
                  </View>
                  {p.accessScope === "shared" ? <Text style={{ color: "#c4b5fd", fontSize: 11 }}>shared</Text> : null}
                  {advertisingExit ? <Text style={{ color: "#fcd34d", fontSize: 11 }}>exit</Text> : null}
                </View>

                {isOwner ? (
                  <View style={{ flexDirection: "row", flexWrap: "wrap", alignItems: "center", gap: 8, borderTopWidth: 1, borderTopColor: c.border, paddingTop: 8 }}>
                    <Pressable
                      onPress={() => void saveNodeConfig(p.deviceId, { wantExitNode: !p.wantExitNode })}
                      style={{
                        borderRadius: 999,
                        paddingHorizontal: 10,
                        paddingVertical: 4,
                        backgroundColor: p.wantExitNode ? "#fcd34d22" : c.bg,
                        borderWidth: 1,
                        borderColor: p.wantExitNode ? "#fcd34d55" : c.border,
                      }}
                    >
                      <Text style={{ color: p.wantExitNode ? "#fcd34d" : c.textMuted, fontSize: 11 }}>exit node</Text>
                    </Pressable>
                    {exitOptions.length > 0 ? (
                      <Pressable
                        onPress={cycleExitNode}
                        style={{ borderRadius: 999, paddingHorizontal: 10, paddingVertical: 4, borderWidth: 1, borderColor: c.border }}
                      >
                        <Text style={{ color: c.textMuted, fontSize: 11 }}>via: {usingName}</Text>
                      </Pressable>
                    ) : null}
                    <TextInput
                      defaultValue={(p.wantRoutes ?? (p.advertisedRoutes ?? []).filter((r) => r !== "0.0.0.0/0")).join(", ")}
                      onEndEditing={(e) => {
                        const next = e.nativeEvent.text.split(",").map((s) => s.trim()).filter(Boolean);
                        void saveNodeConfig(p.deviceId, { wantRoutes: next });
                      }}
                      placeholder="routes 10.0.0.0/24"
                      placeholderTextColor={c.textMuted}
                      style={{ flex: 1, minWidth: 120, color: c.textPrimary, borderWidth: 1, borderColor: c.border, borderRadius: 8, paddingHorizontal: 8, paddingVertical: 4, fontSize: 11 }}
                    />
                  </View>
                ) : null}
              </View>
            );
          })}
        </View>
      )}

      <Text style={{ fontSize: 12, fontWeight: "700", letterSpacing: 1.2, color: c.textMuted, marginTop: 4 }}>
        ACCESS RULES
      </Text>
      <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 17 }}>
        No rules = open mesh (every node reaches every node). Add a rule and everything not
        explicitly allowed is denied. Rules apply on every device, live.
      </Text>

      {rules.length === 0 ? (
        <Text style={{ color: c.textMuted, fontSize: 13 }}>No rules — open mesh (default allow).</Text>
      ) : (
        <View style={{ gap: 8 }}>
          {rules.map((r, i) => (
            <View
              key={i}
              style={{
                borderRadius: 14,
                borderWidth: 1,
                borderColor: c.border,
                backgroundColor: c.bgCard,
                padding: 12,
                gap: 8,
              }}
            >
              <Text style={{ color: c.textPrimary, fontSize: 13 }}>
                {describeEndpoint(r.srcType, r.src)} → {describeEndpoint(r.dstType, r.dst)}
              </Text>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>ports</Text>
                <TextInput
                  defaultValue={r.ports.join(",")}
                  onEndEditing={(e) => {
                    const ports = e.nativeEvent.text.split(",").map((s) => s.trim()).filter(Boolean);
                    void saveRules(rules.map((x, idx) => (idx === i ? { ...x, ports } : x)));
                  }}
                  placeholder="22,80-90,*"
                  placeholderTextColor={c.textMuted}
                  style={{
                    flex: 1,
                    color: c.textPrimary,
                    borderWidth: 1,
                    borderColor: c.border,
                    borderRadius: 8,
                    paddingHorizontal: 8,
                    paddingVertical: 4,
                    fontSize: 12,
                  }}
                />
                <Pressable
                  onPress={() =>
                    void saveRules(
                      rules.map((x, idx) =>
                        idx === i ? { ...x, action: x.action === "accept" ? "drop" : "accept" } : x
                      )
                    )
                  }
                  style={{
                    borderRadius: 8,
                    paddingHorizontal: 10,
                    paddingVertical: 4,
                    backgroundColor: r.action === "accept" ? "#34d39922" : "#ef444422",
                  }}
                >
                  <Text style={{ color: r.action === "accept" ? "#34d399" : "#fca5a5", fontSize: 12 }}>
                    {r.action}
                  </Text>
                </Pressable>
                <Pressable onPress={() => void saveRules(rules.filter((_, idx) => idx !== i))}>
                  <Text style={{ color: c.textMuted, fontSize: 16 }}>✕</Text>
                </Pressable>
              </View>
            </View>
          ))}
        </View>
      )}

      <Pressable
        onPress={() =>
          void saveRules([
            ...rules,
            { srcType: "any", src: "*", dstType: "any", dst: "*", ports: ["*"], action: "accept" },
          ])
        }
        style={{
          borderRadius: 999,
          borderWidth: 1,
          borderColor: "#34d39955",
          backgroundColor: "#34d39915",
          paddingVertical: 8,
          alignItems: "center",
        }}
      >
        <Text style={{ color: "#34d399", fontSize: 13, fontWeight: "600" }}>+ Add rule</Text>
      </Pressable>
    </ScrollView>
  );
}

function describeEndpoint(type: ACLRule["srcType"], val: string) {
  if (type === "any") return "any";
  if (type === "tag") return val.startsWith("tag:") ? val : `tag:${val}`;
  if (type === "device") return val.slice(0, 8);
  return val;
}
