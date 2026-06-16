// EV Stations — find nearby charging stations through one of your Yaver boxes.
// The box runs the ev_charging DISCOVERY verb (OpenChargeMap, keyless-friendly)
// over the mesh; this phone supplies its GPS location and the filters. Defaults
// are Turkey-first CCS2 (Togg T10X / MG ZS EV) so the screen is useful out of
// the box. Pick a network (Trugo/ZES/Eşarj/…), a minimum power, a radius, then
// tap Navigate to hand a station off to Maps.
//
// DISCOVERY ONLY — this never starts or stops a charge session (that's a
// proprietary OCPP concern, not in the open agent). For QR-scan provider
// handoff see ev-charging.tsx; this screen is the map/discovery side, calling
// the ev_charging / ev_networks / ev_connector_types verbs. Transport mirrors
// the circuit/printer cells: LAN-first, relay fallback, your bearer.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Linking,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import * as Location from "expo-location";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  evChargingClient,
  getEvDeviceId,
  setEvDeviceId,
  type EVNetwork,
  type EVStation,
  type EVTarget,
} from "../src/lib/evChargingClient";
import {
  EV_DEFAULTS,
  chargeSpeedLabel,
  classifyPower,
  connectorSummary,
  formatDistance,
  formatPower,
  navUrl,
  stationSubtitle,
  stationTitle,
} from "../src/lib/evChargingFormat";

const SPEED_COLORS: Record<string, string> = {
  ultra: "#a855f7",
  fast: "#22c55e",
  slow: "#f59e0b",
  unknown: "#6b7280",
};

const RADII = [10, 25, 50, 100];
const MIN_POWERS = [0, 50, 120, 150];

export default function EvStationsScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice() as any;
  const devices = (deviceCtx?.devices as any[]) || [];

  const [deviceId, setDeviceId] = useState("");
  const [coords, setCoords] = useState<{ lat: number; lon: number } | null>(null);
  const [locating, setLocating] = useState(false);
  const [locError, setLocError] = useState<string | null>(null);

  // Filters — seed from EV_DEFAULTS (CCS2 / TR / 25 km).
  const [connectorType, setConnectorType] = useState<string>(EV_DEFAULTS.connectorType);
  const [country, setCountry] = useState<string>(EV_DEFAULTS.country);
  const [network, setNetwork] = useState<string>("");
  const [radius, setRadius] = useState<number>(EV_DEFAULTS.radiusKM);
  const [minPower, setMinPower] = useState<number>(EV_DEFAULTS.minPowerKW);

  const [networks, setNetworks] = useState<EVNetwork[]>([]);
  const [stations, setStations] = useState<EVStation[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const live = useRef(true);
  useEffect(() => () => { live.current = false; }, []);

  const target = useCallback((): EVTarget | undefined => {
    if (!deviceId) return undefined;
    const d = devices.find((x) => x.id === deviceId || x.deviceId === deviceId);
    return { id: deviceId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  // Restore the last-used box.
  useEffect(() => {
    getEvDeviceId().then((id) => id && setDeviceId(id));
  }, []);

  // Best-effort GPS on mount; user can retry with the "Locate" button.
  const locate = useCallback(async () => {
    setLocating(true);
    setLocError(null);
    try {
      const { status } = await Location.requestForegroundPermissionsAsync();
      if (status !== "granted") {
        setLocError("Location permission denied — grant it to find stations near you.");
        return;
      }
      const pos = await Location.getCurrentPositionAsync({ accuracy: Location.Accuracy.Balanced });
      if (!live.current) return;
      setCoords({ lat: pos.coords.latitude, lon: pos.coords.longitude });
    } catch (e: any) {
      setLocError(e?.message || "couldn't get your location");
    } finally {
      if (live.current) setLocating(false);
    }
  }, []);

  useEffect(() => { void locate(); }, [locate]);

  // Load the curated network directory for the chosen country (for the chips).
  useEffect(() => {
    const t = target();
    if (!t) return;
    let cancelled = false;
    evChargingClient
      .networks(t, country)
      .then((r) => { if (!cancelled && r?.networks) setNetworks(r.networks); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [deviceId, country, target]);

  const search = useCallback(async () => {
    const t = target();
    if (!t) { setErr("Pick a device first."); return; }
    if (!coords) { setErr("Waiting for your location — tap Locate."); return; }
    setBusy(true);
    setErr(null);
    setMsg(null);
    try {
      const r = await evChargingClient.charging(t, {
        lat: coords.lat,
        lon: coords.lon,
        radius,
        connector_type: connectorType || undefined,
        network: network || undefined,
        country: country || undefined,
        min_power_kw: minPower || undefined,
      });
      if (!live.current) return;
      if (r?.blocked) {
        setErr(r.detail || "OpenChargeMap returned a block. Try again later.");
        setStations([]);
      } else if (r?.error) {
        setErr(r.error);
        setStations([]);
      } else {
        const list = r?.stations || [];
        setStations(list);
        setMsg(`${list.length} station${list.length === 1 ? "" : "s"} within ${r?.radius_km ?? radius} km${r?.keyless ? " · keyless" : ""}`);
      }
    } catch (e: any) {
      if (live.current) setErr(e?.message || "search failed");
    } finally {
      if (live.current) setBusy(false);
    }
  }, [target, coords, radius, connectorType, network, country, minPower]);

  const pickDevice = useCallback((id: string) => {
    setDeviceId(id);
    setEvDeviceId(id).catch(() => {});
    setStations([]);
    setMsg(null);
  }, []);

  const navigateTo = useCallback((st: EVStation) => {
    const url = navUrl(st);
    if (url) Linking.openURL(url).catch(() => {});
  }, []);

  const onlineDevices = useMemo(() => devices.filter((d) => d.online !== false), [devices]);

  return (
    <View style={[s.root, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="EV Stations" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 14, paddingBottom: 40, gap: 12 }}>
        {/* Device picker */}
        <View>
          <Text style={[s.section, { color: c.textMuted }]}>Device</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
            {(onlineDevices.length ? onlineDevices : devices).map((d) => {
              const active = d.id === deviceId;
              return (
                <Pressable
                  key={d.id}
                  onPress={() => pickDevice(d.id)}
                  style={[s.chip, { borderColor: active ? c.accent : c.border, backgroundColor: active ? `${c.accent}22` : c.bgCard }]}
                >
                  <Text style={{ color: active ? c.accent : c.textPrimary, fontSize: 13, fontWeight: active ? "700" : "500" }}>
                    {d.alias ? `@${d.alias}` : d.name}
                  </Text>
                </Pressable>
              );
            })}
            {devices.length === 0 && <Text style={{ color: c.textMuted, fontSize: 13 }}>No devices online.</Text>}
          </View>
        </View>

        {/* Location */}
        <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            <Text style={{ flex: 1, color: c.textPrimary, fontSize: 13 }}>
              {coords
                ? `📍 ${coords.lat.toFixed(4)}, ${coords.lon.toFixed(4)}`
                : locating
                ? "Locating…"
                : "Location not set"}
            </Text>
            <Pressable onPress={locate} disabled={locating} style={[s.smallBtn, { borderColor: c.border }]}>
              {locating ? <ActivityIndicator size="small" color={c.accent} /> : <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Locate</Text>}
            </Pressable>
          </View>
          {locError && <Text style={{ color: "#f59e0b", fontSize: 11, marginTop: 6 }}>{locError}</Text>}
        </View>

        {/* Connector */}
        <View>
          <Text style={[s.section, { color: c.textMuted }]}>Connector (default CCS2 — Togg / MG ZS EV)</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
            {["ccs2", "type2", "ccs1", "chademo", "nacs", ""].map((id) => {
              const active = connectorType === id;
              const label = id === "" ? "Any" : id.toUpperCase();
              return (
                <Pressable key={id || "any"} onPress={() => setConnectorType(id)} style={[s.chip, { borderColor: active ? c.accent : c.border, backgroundColor: active ? `${c.accent}22` : c.bgCard }]}>
                  <Text style={{ color: active ? c.accent : c.textPrimary, fontSize: 12, fontWeight: active ? "700" : "500" }}>{label}</Text>
                </Pressable>
              );
            })}
          </View>
        </View>

        {/* Network */}
        <View>
          <Text style={[s.section, { color: c.textMuted }]}>Network</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
            <Pressable onPress={() => setNetwork("")} style={[s.chip, { borderColor: network === "" ? c.accent : c.border, backgroundColor: network === "" ? `${c.accent}22` : c.bgCard }]}>
              <Text style={{ color: network === "" ? c.accent : c.textPrimary, fontSize: 12, fontWeight: network === "" ? "700" : "500" }}>Any</Text>
            </Pressable>
            {networks.map((n) => {
              const active = network === n.id;
              return (
                <Pressable key={n.id} onPress={() => setNetwork(active ? "" : n.id)} style={[s.chip, { borderColor: active ? c.accent : c.border, backgroundColor: active ? `${c.accent}22` : c.bgCard }]}>
                  <Text style={{ color: active ? c.accent : c.textPrimary, fontSize: 12, fontWeight: active ? "700" : "500" }}>{n.name}</Text>
                </Pressable>
              );
            })}
          </View>
        </View>

        {/* Country */}
        <View style={{ flexDirection: "row", gap: 10 }}>
          <View style={{ flex: 1 }}>
            <Text style={[s.section, { color: c.textMuted }]}>Country</Text>
            <TextInput
              value={country}
              onChangeText={(t) => setCountry(t.trim().toLowerCase())}
              placeholder="tr"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgCard }]}
            />
          </View>
        </View>

        {/* Radius */}
        <View>
          <Text style={[s.section, { color: c.textMuted }]}>Radius</Text>
          <View style={{ flexDirection: "row", gap: 8 }}>
            {RADII.map((r) => {
              const active = radius === r;
              return (
                <Pressable key={r} onPress={() => setRadius(r)} style={[s.chip, { borderColor: active ? c.accent : c.border, backgroundColor: active ? `${c.accent}22` : c.bgCard }]}>
                  <Text style={{ color: active ? c.accent : c.textPrimary, fontSize: 12, fontWeight: active ? "700" : "500" }}>{r} km</Text>
                </Pressable>
              );
            })}
          </View>
        </View>

        {/* Min power */}
        <View>
          <Text style={[s.section, { color: c.textMuted }]}>Min power</Text>
          <View style={{ flexDirection: "row", gap: 8 }}>
            {MIN_POWERS.map((p) => {
              const active = minPower === p;
              return (
                <Pressable key={p} onPress={() => setMinPower(p)} style={[s.chip, { borderColor: active ? c.accent : c.border, backgroundColor: active ? `${c.accent}22` : c.bgCard }]}>
                  <Text style={{ color: active ? c.accent : c.textPrimary, fontSize: 12, fontWeight: active ? "700" : "500" }}>{p === 0 ? "Any" : `${p}+ kW`}</Text>
                </Pressable>
              );
            })}
          </View>
        </View>

        {/* Search */}
        <Pressable
          onPress={search}
          disabled={busy || !deviceId || !coords}
          style={[s.searchBtn, { backgroundColor: c.accent, opacity: busy || !deviceId || !coords ? 0.5 : 1 }]}
        >
          {busy ? <ActivityIndicator color="#fff" /> : <Text style={{ color: "#fff", fontSize: 15, fontWeight: "700" }}>Find stations</Text>}
        </Pressable>

        {err && <Text style={{ color: "#f59e0b", fontSize: 12 }}>{err}</Text>}
        {msg && !err && <Text style={{ color: c.textMuted, fontSize: 12 }}>{msg}</Text>}

        {/* Results */}
        {stations.map((st, i) => {
          const speed = classifyPower(st.max_power_kw);
          const sc = SPEED_COLORS[speed];
          const dist = formatDistance(st.distance_km);
          const conns = connectorSummary(st.connectors);
          return (
            <View key={`${st.lat},${st.lon},${i}`} style={[s.station, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 4 }}>
                <View style={[s.speedBadge, { backgroundColor: `${sc}22` }]}>
                  <Text style={{ color: sc, fontSize: 10, fontWeight: "700" }}>{chargeSpeedLabel(speed)}</Text>
                </View>
                {st.max_power_kw ? <Text style={{ color: sc, fontSize: 11, fontWeight: "600" }}>{formatPower(st.max_power_kw)}</Text> : null}
                {dist ? <Text style={{ color: c.textMuted, fontSize: 11, marginLeft: "auto" }}>{dist}</Text> : null}
              </View>
              <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }} numberOfLines={1}>{stationTitle(st)}</Text>
              {stationSubtitle(st) ? <Text style={{ color: c.textMuted, fontSize: 12 }} numberOfLines={1}>{stationSubtitle(st)}</Text> : null}
              {conns ? <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }} numberOfLines={2}>{conns}</Text> : null}
              <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginTop: 8 }}>
                {st.status_hint ? <Text style={{ color: c.textMuted, fontSize: 11, flex: 1 }} numberOfLines={1}>{st.status_hint}</Text> : <View style={{ flex: 1 }} />}
                <Pressable onPress={() => navigateTo(st)} style={[s.navBtn, { backgroundColor: c.accent }]}>
                  <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>Navigate ›</Text>
                </Pressable>
              </View>
            </View>
          );
        })}

        {!busy && stations.length === 0 && !err && (
          <Text style={{ color: c.textMuted, fontSize: 12, textAlign: "center", marginTop: 12 }}>
            Set your filters and tap Find stations.
          </Text>
        )}
      </ScrollView>
    </View>
  );
}

const s = StyleSheet.create({
  root: { flex: 1 },
  section: { fontSize: 12, fontWeight: "600", marginBottom: 6 },
  chip: { borderWidth: 1, borderRadius: 16, paddingHorizontal: 12, paddingVertical: 7 },
  card: { borderWidth: 1, borderRadius: 12, padding: 12 },
  input: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8, fontSize: 14 },
  smallBtn: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 12, paddingVertical: 7 },
  searchBtn: { borderRadius: 10, paddingVertical: 13, alignItems: "center", marginTop: 4 },
  station: { borderWidth: 1, borderRadius: 12, padding: 12 },
  speedBadge: { paddingHorizontal: 8, paddingVertical: 3, borderRadius: 6 },
  navBtn: { borderRadius: 8, paddingHorizontal: 12, paddingVertical: 7 },
});
