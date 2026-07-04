// mesh.tsx — Yaver Mesh home (mobile), Tailscale-style. Leads with a Connect
// hero for THIS phone, then lists EVERY machine in the account (not just nodes
// already on the mesh) with its mesh on/off state and a one-tap "Enable mesh"
// that — back-to-back over P2P — stages an agent self-update and brings mesh up
// on the box. Access rules + Sharing stay as footer entries. See
// docs/yaver-mesh-mobile-tailscale-ui-design.md.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Pressable, RefreshControl, ScrollView, Text, TextInput, View } from "react-native";
import { router } from "expo-router";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { useAuth } from "../../src/context/AuthContext";
import { Device, useDevice } from "../../src/context/DeviceContext";
import { CONVEX_SITE_URL } from "../../src/_core/constants";
import { useMesh } from "../../src/lib/useMesh";
import {
  enableMeshOnDevice,
  meshEnablePhaseLabel,
  meshStatusForDevice,
  type MeshDeviceStatus,
  type MeshEnablePhase,
} from "../../src/lib/meshControl";
import { connectionManager } from "../../src/lib/connectionManager";
import {
  ensureMeshKeyPair,
  isMeshTunnelSupported,
  meshDeviceIdFromPubKey,
  meshTunnelDown,
  meshTunnelStatus,
  meshTunnelUp,
  type MeshTunnelStatus,
} from "../../src/lib/yaverMesh";
import { nodeLabel } from "../../src/lib/meshTypes";
import { ConnectHero } from "../../src/components/mesh/ConnectHero";
import { MeshEnableProgress, type MeshEnableProgressInfo } from "../../src/components/mesh/MeshEnableProgress";
import { MeshMachineRow } from "../../src/components/mesh/MeshMachineRow";
import { ChevronRightIcon, SearchIcon } from "../../src/components/mesh/MeshIcons";

type EnableAllState = { done: number; total: number; current: string; phase?: MeshEnablePhase } | null;

// Device IDs the phone has already tried to auto-enable onto the mesh, so a box
// (or an unreachable one) isn't re-attempted on every launch. Persisted.
const AUTO_ENABLE_KEY = "mesh:autoEnableAttempted:v1";

export default function MeshHomeScreen() {
  const c = useColors();
  const tabletContent = useTabletContentStyle("regular");
  const { token } = useAuth();
  const mesh = useMesh();
  const { devices } = useDevice();
  const [query, setQuery] = useState("");

  const tunnelSupported = isMeshTunnelSupported();
  const [tunnel, setTunnel] = useState<MeshTunnelStatus | null>(null);
  const [tunnelBusy, setTunnelBusy] = useState(false);
  const [selfId, setSelfId] = useState<string | null>(null);

  // Per-device mesh state. Seeded from the Convex control plane (mesh.peers)
  // and refined by live GET /mesh/status probes against each online box.
  const [statusById, setStatusById] = useState<Record<string, MeshDeviceStatus>>({});
  const [busyIds, setBusyIds] = useState<Set<string>>(new Set());
  // Current enable phase per in-flight device, so each row narrates progress
  // ("Updating agent…" → "Bringing mesh up…") instead of a silent spinner.
  const [phaseById, setPhaseById] = useState<Record<string, MeshEnablePhase>>({});
  const [enableAll, setEnableAll] = useState<EnableAllState>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    if (!tunnelSupported) return;
    void meshTunnelStatus().then(setTunnel);
    void ensureMeshKeyPair().then((pk) => {
      if (pk) setSelfId(meshDeviceIdFromPubKey(pk));
    });
  }, [tunnelSupported]);

  // Real machines in the account — drop the "account" pseudo-device that
  // DeviceContext injects so it never renders as a row.
  const machines = useMemo(() => devices.filter((d) => d.id !== "account"), [devices]);

  const joinedById = useMemo(
    () => new Map(mesh.peers.map((p) => [p.deviceId, p] as const)),
    [mesh.peers]
  );

  const meshOnFor = useCallback(
    (d: Device) => statusById[d.id]?.enabled ?? joinedById.has(d.id),
    [statusById, joinedById]
  );
  const meshIpFor = useCallback(
    (d: Device) => statusById[d.id]?.meshIPv4 ?? joinedById.get(d.id)?.meshIPv4,
    [statusById, joinedById]
  );

  // Lazily probe /mesh/status for each online box. Keyed on the online-id set
  // so it re-runs when machines come/go online, not on every DeviceContext
  // re-render. A per-id TTL guards against re-probing the same box in a tight
  // window (e.g. after enabling, when mesh.reload churns the list).
  const probedRef = useRef<Map<string, number>>(new Map());
  const onlineKey = useMemo(
    () => machines.filter((d) => d.online).map((d) => d.id).sort().join(","),
    [machines]
  );
  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    (async () => {
      for (const d of machines.filter((m) => m.online)) {
        if (cancelled) return;
        const last = probedRef.current.get(d.id) ?? 0;
        if (Date.now() - last < 30_000) continue;
        const st = await meshStatusForDevice(d, token);
        probedRef.current.set(d.id, Date.now());
        if (cancelled) return;
        if (st) setStatusById((prev) => ({ ...prev, [d.id]: st }));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [onlineKey, token]);

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

  // Enable mesh on a single box: warm the P2P client, stage an agent update,
  // bring mesh up. Returns true on success so the enable-all loop can tally.
  const enableOne = useCallback(
    async (d: Device, onPhase?: (p: MeshEnablePhase) => void): Promise<boolean> => {
      if (!token) return false;
      setBusyIds((prev) => new Set(prev).add(d.id));
      setPhaseById((prev) => ({ ...prev, [d.id]: "updating" }));
      try {
        // Bring up a live transport FIRST so mesh calls ride QuicClient's own
        // direct-LAN-first policy instead of a relay-only URL (which 502s for a
        // box reachable on the same Wi-Fi but not parked on the relay).
        await connectionManager
          .ensureConnected(d.id, {
            host: d.host,
            port: d.port,
            token,
            lanIps: d.lanIps,
            connectionPreferences: d.connectionPreferences,
          })
          .catch(() => {}); // best-effort — enableMeshOnDevice still falls back
        const r = await enableMeshOnDevice(d, token, (p) => {
          setPhaseById((prev) => ({ ...prev, [d.id]: p }));
          onPhase?.(p);
        });
        setStatusById((prev) => ({ ...prev, [d.id]: { enabled: true, meshIPv4: r.meshIPv4 } }));
        probedRef.current.set(d.id, Date.now());
        setNotice(
          r.dataPlaneWarning
            ? `${d.name}: joined · data plane needs elevated privilege on the box`
            : `${d.name} on the mesh${r.stagedVersion ? ` · staged ${r.stagedVersion}` : ""}`
        );
        mesh.setError(null);
        return true;
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        mesh.setError(`${d.name}: ${msg === "unreachable" ? "couldn't reach the agent" : msg}`);
        return false;
      } finally {
        setBusyIds((prev) => {
          const n = new Set(prev);
          n.delete(d.id);
          return n;
        });
        setPhaseById((prev) => {
          const n = { ...prev };
          delete n[d.id];
          return n;
        });
      }
    },
    [token, mesh]
  );

  const handleEnableOne = useCallback(
    async (d: Device) => {
      const ok = await enableOne(d);
      if (ok) void mesh.reload();
    },
    [enableOne, mesh]
  );

  // Enable mesh on every online, not-yet-on, owned box — back-to-back. Sequential
  // (not parallel) to avoid hammering the relay + each box's self-heal at once.
  const enableAllOnline = useCallback(async () => {
    if (!token || enableAll) return;
    const targets = machines.filter((d) => d.online && !d.isGuest && !meshOnFor(d));
    if (targets.length === 0) {
      setNotice("All your online machines are already on the mesh.");
      return;
    }
    let enabled = 0;
    let failed = 0;
    for (let i = 0; i < targets.length; i++) {
      const d = targets[i];
      setEnableAll({ done: i, total: targets.length, current: d.name, phase: "updating" });
      const ok = await enableOne(d, (p) => setEnableAll((s) => (s ? { ...s, phase: p } : s)));
      ok ? enabled++ : failed++;
    }
    setEnableAll(null);
    setNotice(`${enabled} enabled${failed ? ` · ${failed} unreachable` : ""}`);
    void mesh.reload();
  }, [token, machines, meshOnFor, enableOne, enableAll, mesh]);

  // Auto-enable mesh on newly-seen, owned, online boxes — once each, quietly.
  // "Fresh install / a new machine comes online → it joins the overlay without
  // a manual tap." A persisted attempted-set guards against re-trying the same
  // box (or an unreachable one) on every launch; offline boxes are skipped by
  // the online filter and unreachable ones fail silently (still recorded).
  const autoEnableRanRef = useRef(false);
  useEffect(() => {
    if (!token || autoEnableRanRef.current) return;
    const candidates = machines.filter((d) => d.online && !d.isGuest && !meshOnFor(d));
    if (candidates.length === 0) return;
    autoEnableRanRef.current = true; // once per mount
    let cancelled = false;
    (async () => {
      let attempted: string[] = [];
      try {
        const raw = await AsyncStorage.getItem(AUTO_ENABLE_KEY);
        attempted = raw ? JSON.parse(raw) : [];
      } catch {}
      const todo = candidates.filter((d) => !attempted.includes(d.id));
      if (todo.length === 0 || cancelled) return;
      try {
        const next = Array.from(new Set([...attempted, ...todo.map((d) => d.id)]));
        await AsyncStorage.setItem(AUTO_ENABLE_KEY, JSON.stringify(next));
      } catch {}
      let joined = 0;
      for (const d of todo) {
        if (cancelled) return;
        try {
          await connectionManager
            .ensureConnected(d.id, {
              host: d.host,
              port: d.port,
              token,
              lanIps: d.lanIps,
              connectionPreferences: d.connectionPreferences,
            })
            .catch(() => {});
          const r = await enableMeshOnDevice(d, token);
          if (cancelled) return;
          setStatusById((prev) => ({ ...prev, [d.id]: { enabled: true, meshIPv4: r.meshIPv4 } }));
          probedRef.current.set(d.id, Date.now());
          joined++;
        } catch {
          // best-effort — unreachable box, leave it for a manual tap
        }
      }
      if (!cancelled && joined > 0) {
        setNotice(`Mesh auto-enabled on ${joined} new machine${joined === 1 ? "" : "s"}`);
        void mesh.reload();
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token, machines, meshOnFor, mesh]);

  const { mine, shared } = useMemo(() => {
    const q = query.trim().toLowerCase();
    const match = (d: Device) =>
      !q || d.name.toLowerCase().includes(q) || (meshIpFor(d) ?? "").includes(q);
    const visible = machines.filter(match);
    return {
      mine: visible.filter((d) => !d.isGuest),
      shared: visible.filter((d) => d.isGuest),
    };
  }, [machines, query, meshIpFor]);

  const anyEnableable = useMemo(
    () => machines.some((d) => d.online && !d.isGuest && !meshOnFor(d)),
    [machines, meshOnFor]
  );

  // Single source of truth for the animated step overlay: the fleet run wins
  // (it carries a count), else the lone in-flight single-tap enable.
  const progressInfo = useMemo<MeshEnableProgressInfo | null>(() => {
    if (enableAll) {
      return {
        title: `Enabling mesh on ${enableAll.total} machine${enableAll.total === 1 ? "" : "s"}`,
        subtitle: `Machine ${enableAll.done + 1} of ${enableAll.total} · ${enableAll.current}`,
        phase: enableAll.phase,
      };
    }
    const id = Object.keys(phaseById)[0];
    if (id) {
      const d = machines.find((m) => m.id === id);
      return { title: `Enabling ${d?.name ?? "machine"}`, subtitle: "On the mesh in a moment…", phase: phaseById[id] };
    }
    return null;
  }, [enableAll, phaseById, machines]);

  const openNode = (deviceId: string) =>
    router.navigate({ pathname: "/(tabs)/mesh-node", params: { deviceId } } as any);

  const openExitPicker = () => {
    if (!selfId) return;
    router.navigate({ pathname: "/(tabs)/mesh-exit", params: { deviceId: selfId } } as any);
  };

  const renderRow = (d: Device) => (
    <MeshMachineRow
      key={d.id}
      name={d.name}
      os={d.os}
      online={d.online}
      isGuest={d.isGuest}
      meshOn={meshOnFor(d)}
      meshIPv4={meshIpFor(d)}
      joinedPeer={joinedById.get(d.id)}
      busy={busyIds.has(d.id)}
      phase={phaseById[d.id]}
      onEnable={() => void handleEnableOne(d)}
      onOpen={() => openNode(d.id)}
    />
  );

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Yaver Mesh" onBack={() => router.navigate("/(tabs)/more" as any)} />
      <ScrollView
        style={{ flex: 1, backgroundColor: c.bg }}
        contentContainerStyle={[{ padding: 16, gap: 16 }, tabletContent]}
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

        {notice ? (
          <Pressable onPress={() => setNotice(null)}>
            <View style={{ borderRadius: 14, borderWidth: 1, borderColor: "#34d39955", backgroundColor: "#34d39912", padding: 12 }}>
              <Text style={{ color: "#34d399", fontSize: 13 }}>{notice}</Text>
            </View>
          </Pressable>
        ) : null}

        {/* Fleet enable: one tap brings every online box onto the mesh. */}
        {anyEnableable || enableAll ? (
          <Pressable
            onPress={() => void enableAllOnline()}
            disabled={!!enableAll}
            style={{
              flexDirection: "row",
              alignItems: "center",
              justifyContent: "center",
              gap: 10,
              borderRadius: 14,
              borderWidth: 1,
              borderColor: "#34d39955",
              backgroundColor: "#34d39912",
              padding: 14,
              opacity: enableAll ? 0.8 : 1,
            }}
          >
            {enableAll ? (
              <>
                <ActivityIndicator size="small" color="#34d399" />
                <Text style={{ color: "#34d399", fontSize: 14, fontWeight: "700" }} numberOfLines={1}>
                  {enableAll.done + 1}/{enableAll.total} · {enableAll.current} — {meshEnablePhaseLabel(enableAll.phase)}
                </Text>
              </>
            ) : (
              <Text style={{ color: "#34d399", fontSize: 14, fontWeight: "700" }}>Enable mesh on all machines</Text>
            )}
          </Pressable>
        ) : null}

        {machines.length > 6 ? (
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

        {mesh.loading && machines.length === 0 ? (
          <ActivityIndicator color={c.textMuted} />
        ) : machines.length === 0 ? (
          <View style={{ borderRadius: 14, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, padding: 16, gap: 6 }}>
            <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>No machines yet</Text>
            <Text style={{ color: c.textMuted, fontSize: 13, lineHeight: 18 }}>
              Install Yaver on a dev box with <Text style={{ fontFamily: "Menlo" }}>npm i -g yaver-cli</Text> and sign in.
              It shows up here — then tap <Text style={{ fontWeight: "700" }}>Enable mesh</Text> to give it a stable 100.96
              overlay IP every other node can reach (a Tailscale alternative across your fleet).
            </Text>
          </View>
        ) : (
          <>
            {mine.length > 0 ? (
              <MachineSection title="MY DEVICES" devices={mine} render={renderRow} />
            ) : null}
            {shared.length > 0 ? (
              <MachineSection title="SHARED WITH ME" devices={shared} render={renderRow} />
            ) : null}
          </>
        )}

        <View style={{ gap: 8, marginTop: 4 }}>
          <FooterLink label="Access rules" sub="Who can reach what — ACLs & device tags" onPress={() => router.navigate("/(tabs)/mesh-access" as any)} />
          <FooterLink label="Sharing" sub="Support a friend · who can access your machines" onPress={() => router.navigate("/(tabs)/mesh-share" as any)} />
        </View>
      </ScrollView>

      <MeshEnableProgress info={progressInfo} />
    </View>
  );
}

function MachineSection({
  title,
  devices,
  render,
}: {
  title: string;
  devices: Device[];
  render: (d: Device) => React.ReactNode;
}) {
  const c = useColors();
  return (
    <View style={{ gap: 8 }}>
      <Text style={{ fontSize: 12, fontWeight: "700", letterSpacing: 1.2, color: c.textMuted }}>{title}</Text>
      {devices.map(render)}
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
