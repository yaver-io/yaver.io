// network.tsx — Yaver Mesh console (mobile). Phase 6: console/status only — the
// phone shows the mesh topology and edits access rules, but does NOT itself
// carry VPN traffic yet (the on-device PacketTunnel is a later phase). Mesh data
// flows through the Convex /mesh/* HTTP routes using the session token.

import { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  Share,
  Text,
  TextInput,
  View,
} from "react-native";
import * as Clipboard from "expo-clipboard";
import { useColors } from "../../src/context/ThemeContext";
import { useAuth } from "../../src/context/AuthContext";
import { CONVEX_SITE_URL } from "../../src/_core/constants";
import {
  isMeshTunnelSupported,
  meshTunnelDown,
  meshTunnelStatus,
  meshTunnelUp,
  type MeshTunnelStatus,
} from "../../src/lib/yaverMesh";

type SupportConn = {
  grantId: string;
  deviceId: string | null;
  counterpartName: string;
  allowDesktopControl: boolean;
  expiresAt: number | null;
};

type MeshPeer = {
  deviceId: string;
  alias?: string;
  meshIPv4?: string;
  online?: boolean;
  isExitNode?: boolean;
  accessScope?: "owner" | "shared" | "peer";
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

// Bridging a Tailnet = advertising Tailscale's CGNAT block as a mesh route on
// a node sitting on BOTH networks. Mesh peer /32s + the 100.96/12 overlay are
// longer-prefix, so they still win — only real Tailnet hosts route through the
// bridge. Lets mesh peers reach a Tailnet without Tailscale on every node.
const TAILSCALE_BRIDGE_CIDR = "100.64.0.0/10";

export default function NetworkScreen() {
  const c = useColors();
  const { token } = useAuth();
  const [peers, setPeers] = useState<MeshPeer[]>([]);
  const [rules, setRules] = useState<ACLRule[]>([]);
  const [supportLink, setSupportLink] = useState<string | null>(null);
  const [supporting, setSupporting] = useState<SupportConn[]>([]);
  const [supportedBy, setSupportedBy] = useState<SupportConn[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // On-device tunnel (Phase 7) — only meaningful on a build that bundled the
  // native NetworkExtension/VpnService. Absent everywhere else → tunnel stays
  // null and the card renders a "coming in a native update" hint.
  const tunnelSupported = isMeshTunnelSupported();
  const [tunnel, setTunnel] = useState<MeshTunnelStatus | null>(null);
  const [tunnelBusy, setTunnelBusy] = useState(false);

  useEffect(() => {
    if (!tunnelSupported) return;
    void meshTunnelStatus().then(setTunnel);
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
      if (next.state === "error" && next.error) setError(next.error);
      void load();
    } finally {
      setTunnelBusy(false);
    }
  }, [token, tunnel, tunnelBusy]);

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
      const cRes = await fetch(`${CONVEX_SITE_URL}/support/connections`, { headers });
      if (cRes.ok) {
        const cJson = await cRes.json();
        setSupporting(cJson.supporting ?? []);
        setSupportedBy(cJson.supportedBy ?? []);
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

  const createSupportLink = useCallback(
    async (offerTerminal: boolean, offerDesktopControl: boolean) => {
      if (!token) return;
      try {
        const res = await fetch(`${CONVEX_SITE_URL}/support/invite`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: JSON.stringify({ offerTerminal, offerDesktopControl }),
        });
        if (!res.ok) throw new Error(`invite: HTTP ${res.status}`);
        const json = await res.json();
        const url = `https://yaver.io/j/${json.code}`;
        setSupportLink(url);
        // Open the native share sheet (WhatsApp, Messages, Mail, …).
        await Share.share({
          message: `Let me help you on your computer with Yaver — open this to connect: ${url}`,
        });
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [token]
  );

  const revokeSupport = useCallback(
    async (grantId: string) => {
      if (!token) return;
      try {
        await fetch(`${CONVEX_SITE_URL}/support/grant/revoke`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: JSON.stringify({ grantId }),
        });
        void load();
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
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
          it gets a stable overlay IP every other node can reach. This phone manages the mesh{tunnelSupported ? " and can join it directly below." : "; on-device tunneling arrives in a later update."}
        </Text>
      </View>

      {/* This phone — on-device tunnel (Phase 7). Real toggle on extension
          builds; a graceful hint otherwise so the card always renders. */}
      <View style={{ borderRadius: 16, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, padding: 14, gap: 10 }}>
        <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
          <Text style={{ fontSize: 15, fontWeight: "700", color: c.textPrimary }}>This phone</Text>
          {tunnelSupported ? (
            <View
              style={{
                width: 8,
                height: 8,
                borderRadius: 4,
                backgroundColor: tunnel?.state === "connected" ? "#34d399" : c.textMuted,
              }}
            />
          ) : null}
        </View>
        {tunnelSupported ? (
          <>
            <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 17 }}>
              {tunnel?.state === "connected"
                ? `Connected · ${tunnel.meshIPv4 ?? "overlay IP pending"}`
                : "Join the mesh from this phone — get a 100.96 overlay IP so ssh/HTTP to any peer works directly."}
            </Text>
            <Pressable
              onPress={() => void toggleTunnel()}
              disabled={tunnelBusy}
              style={{
                borderRadius: 999,
                paddingVertical: 10,
                alignItems: "center",
                opacity: tunnelBusy ? 0.5 : 1,
                backgroundColor: tunnel?.state === "connected" ? "#ef444415" : "#34d39915",
                borderWidth: 1,
                borderColor: tunnel?.state === "connected" ? "#ef444455" : "#34d39955",
              }}
            >
              <Text style={{ color: tunnel?.state === "connected" ? "#fca5a5" : "#34d399", fontSize: 14, fontWeight: "700" }}>
                {tunnelBusy ? "…" : tunnel?.state === "connected" ? "Disconnect" : "Connect to mesh"}
              </Text>
            </Pressable>
          </>
        ) : (
          <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 17 }}>
            On-device tunneling (this phone carrying mesh traffic with its own overlay IP) arrives
            in a native update. For now this phone manages the mesh; your desktops and servers form
            the data plane.
          </Text>
        )}
      </View>

      {error ? (
        <View style={{ borderRadius: 14, borderWidth: 1, borderColor: "#ef444455", backgroundColor: "#ef444415", padding: 12 }}>
          <Text style={{ color: "#fca5a5", fontSize: 13 }}>{error}</Text>
        </View>
      ) : null}

      {/* Support a friend — generate a link and share it (WhatsApp, Messages, …) */}
      <View style={{ borderRadius: 16, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, padding: 14, gap: 10 }}>
        <Text style={{ fontSize: 15, fontWeight: "700", color: c.textPrimary }}>Support a friend</Text>
        <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 17 }}>
          Send a link. Your friend installs Yaver, approves access, and their computer joins your
          mesh so you can help them. Default = view + files; they opt into more on their own screen.
        </Text>
        <View style={{ flexDirection: "row", gap: 8 }}>
          <Pressable
            onPress={() => void createSupportLink(false, false)}
            style={{ flex: 1, borderRadius: 999, paddingVertical: 9, alignItems: "center", borderWidth: 1, borderColor: "#34d39955", backgroundColor: "#34d39915" }}
          >
            <Text style={{ color: "#34d399", fontSize: 13, fontWeight: "600" }}>View-only link</Text>
          </Pressable>
          <Pressable
            onPress={() => void createSupportLink(true, true)}
            style={{ flex: 1, borderRadius: 999, paddingVertical: 9, alignItems: "center", borderWidth: 1, borderColor: "#fcd34d55", backgroundColor: "#fcd34d15" }}
          >
            <Text style={{ color: "#fcd34d", fontSize: 13, fontWeight: "600" }}>Full-support link</Text>
          </Pressable>
        </View>
        {supportLink ? (
          <View style={{ gap: 8 }}>
            <Text selectable style={{ color: "#34d399", fontSize: 12, fontFamily: "Menlo" }}>{supportLink}</Text>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Pressable
                onPress={async () => {
                  await Clipboard.setStringAsync(supportLink);
                  Alert.alert("Copied", "Support link copied to clipboard.");
                }}
                style={{ borderRadius: 999, paddingHorizontal: 14, paddingVertical: 6, borderWidth: 1, borderColor: c.border }}
              >
                <Text style={{ color: c.textPrimary, fontSize: 12 }}>Copy</Text>
              </Pressable>
              <Pressable
                onPress={() => Share.share({ message: `Open this to let me help you on your computer: ${supportLink}` })}
                style={{ borderRadius: 999, paddingHorizontal: 14, paddingVertical: 6, borderWidth: 1, borderColor: c.border }}
              >
                <Text style={{ color: c.textPrimary, fontSize: 12 }}>Share…</Text>
              </Pressable>
            </View>
          </View>
        ) : null}
        {supporting.length > 0 ? (
          <View style={{ gap: 4, borderTopWidth: 1, borderTopColor: c.border, paddingTop: 8 }}>
            <Text style={{ fontSize: 11, color: c.textMuted }}>YOU CAN SUPPORT</Text>
            {supporting.map((s) => (
              <View key={s.grantId} style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                <Text style={{ flex: 1, color: c.textPrimary, fontSize: 13 }}>
                  {s.counterpartName}{s.allowDesktopControl ? "  · desktop" : ""}
                  {s.expiresAt ? "  · time-boxed" : "  · until revoked"}
                </Text>
                <Pressable onPress={() => void revokeSupport(s.grantId)}>
                  <Text style={{ color: c.textMuted, fontSize: 12 }}>end</Text>
                </Pressable>
              </View>
            ))}
          </View>
        ) : null}
        {supportedBy.length > 0 ? (
          <View style={{ gap: 4, borderTopWidth: 1, borderTopColor: c.border, paddingTop: 8 }}>
            <Text style={{ fontSize: 11, color: "#fca5a5" }}>WHO CAN ACCESS YOUR MACHINES</Text>
            {supportedBy.map((s) => (
              <View key={s.grantId} style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                <Text style={{ flex: 1, color: c.textPrimary, fontSize: 13 }}>{s.counterpartName}</Text>
                <Pressable onPress={() => void revokeSupport(s.grantId)} style={{ borderRadius: 6, borderWidth: 1, borderColor: "#ef444455", paddingHorizontal: 8, paddingVertical: 2 }}>
                  <Text style={{ color: "#fca5a5", fontSize: 12 }}>revoke</Text>
                </Pressable>
              </View>
            ))}
          </View>
        ) : null}
      </View>

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
            const isOwner = p.accessScope === "owner";
            const advertisingExit = p.isExitNode || p.wantExitNode;
            const currentRoutes = p.wantRoutes ?? (p.advertisedRoutes ?? []).filter((r) => r !== "0.0.0.0/0");
            const bridgingTailnet = currentRoutes.includes(TAILSCALE_BRIDGE_CIDR);
            const toggleTailnetBridge = () => {
              const next = bridgingTailnet
                ? currentRoutes.filter((r) => r !== TAILSCALE_BRIDGE_CIDR)
                : [...currentRoutes, TAILSCALE_BRIDGE_CIDR];
              void saveNodeConfig(p.deviceId, { wantRoutes: next });
            };
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
                      defaultValue={currentRoutes.filter((r) => r !== TAILSCALE_BRIDGE_CIDR).join(", ")}
                      onEndEditing={(e) => {
                        const typed = e.nativeEvent.text.split(",").map((s) => s.trim()).filter(Boolean);
                        // Preserve the Tailnet-bridge route (it has its own toggle).
                        const next = bridgingTailnet ? [...typed, TAILSCALE_BRIDGE_CIDR] : typed;
                        void saveNodeConfig(p.deviceId, { wantRoutes: next });
                      }}
                      placeholder="routes 10.0.0.0/24"
                      placeholderTextColor={c.textMuted}
                      style={{ flex: 1, minWidth: 120, color: c.textPrimary, borderWidth: 1, borderColor: c.border, borderRadius: 8, paddingHorizontal: 8, paddingVertical: 4, fontSize: 11 }}
                    />
                    <Pressable
                      onPress={toggleTailnetBridge}
                      style={{
                        borderRadius: 999,
                        paddingHorizontal: 10,
                        paddingVertical: 4,
                        backgroundColor: bridgingTailnet ? "#22d3ee22" : c.bg,
                        borderWidth: 1,
                        borderColor: bridgingTailnet ? "#22d3ee55" : c.border,
                      }}
                    >
                      <Text style={{ color: bridgingTailnet ? "#22d3ee" : c.textMuted, fontSize: 11 }}>
                        {bridgingTailnet ? "✓ Tailnet" : "bridge Tailnet"}
                      </Text>
                    </Pressable>
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
