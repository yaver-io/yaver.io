// ScrewCellScreen — screw-cell shop-floor analytics on the phone, from the SAME
// agent ops verbs the firmware pushes to (screw_cell_record via
// cell_runner.py --yaver) and the coding agent reads via the
// screw_cell_analytics MCP tool. Pick a device (RobotDevicePicker, honest
// presence), then KPIs + a daily fail-rate trend (react-native-svg) + the
// flagged production orders + worst blocks + recent runs. Runs live in the
// agent's vault, never on Convex. Mobile twin of the web ScrewCellView.
import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import Svg, { Line, Rect } from "react-native-svg";

import { useColors } from "../context/ThemeContext";
import { useDevice } from "../context/DeviceContext";
import { useAuth } from "../context/AuthContext";
import { RobotDevicePicker } from "./robot/RobotDevicePicker";
import { screwCellClient, type ScrewAnalytics, type ScrewTarget } from "../lib/screwCellClient";

const WINDOWS = [7, 30, 90] as const;

function rateColor(c: ReturnType<typeof useColors>, r: number): string {
  if (r <= 0) return c.success;
  if (r < 5) return c.warn;
  return c.error;
}

function fmtTime(t?: number): string {
  if (!t) return "—";
  const ms = t > 1e12 ? t : t * 1000;
  try {
    return new Date(ms).toLocaleDateString();
  } catch {
    return String(t);
  }
}

export function ScrewCellScreen() {
  const c = useColors();
  const { devices } = useDevice();
  const { token } = useAuth();

  const [deviceId, setDeviceId] = useState("");
  const [days, setDays] = useState<(typeof WINDOWS)[number]>(30);
  const [data, setData] = useState<ScrewAnalytics | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const targetFor = useCallback(
    (id: string): ScrewTarget | undefined => {
      const d: any = devices.find((x) => x.id === id);
      if (!d) return undefined;
      return { id: d.id, host: d.host, port: d.port, lanIps: d.lanIps };
    },
    [devices],
  );

  const refresh = useCallback(async () => {
    const t = targetFor(deviceId);
    if (!t) return;
    setBusy(true);
    setErr(null);
    const r: any = await screwCellClient.analytics(t, days);
    if (r?.ok === false) setErr(r.error || "failed");
    else if (r?.totals) setData(r as ScrewAnalytics);
    else setData(null);
    setBusy(false);
  }, [targetFor, deviceId, days]);

  useEffect(() => {
    if (deviceId) refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId, days]);

  const card = {
    backgroundColor: c.bgCard,
    borderColor: c.border,
    borderWidth: 1,
    borderRadius: 12,
    padding: 14,
    marginBottom: 12,
  } as const;

  const trend = data?.trend ?? [];
  const maxRate = Math.max(5, ...trend.map((t) => t.failRate));
  const CHART_W = 320;
  const CHART_H = 110;
  const barGap = trend.length > 0 ? CHART_W / trend.length : CHART_W;

  return (
    <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }} edges={["bottom"]}>
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        <Text style={{ color: c.textSecondary, fontSize: 13, marginBottom: 14 }}>
          Screw-cell shop-floor analytics — pick the device whose Yaver agent receives the runs.
        </Text>

        {/* Device picker */}
        <View style={card}>
          <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 6 }}>Device</Text>
          <RobotDevicePicker devices={devices} currentId={deviceId} token={token} onPick={setDeviceId} />
        </View>

        {!deviceId ? null : (
          <>
            {/* Window selector */}
            <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 12 }}>
              {WINDOWS.map((w) => (
                <Pressable
                  key={w}
                  onPress={() => setDays(w)}
                  style={{
                    paddingHorizontal: 14,
                    paddingVertical: 7,
                    borderRadius: 8,
                    backgroundColor: days === w ? c.accent : c.bgCard,
                    borderWidth: 1,
                    borderColor: days === w ? c.accent : c.border,
                  }}
                >
                  <Text style={{ color: days === w ? "#fff" : c.textSecondary, fontWeight: "600", fontSize: 13 }}>{w}d</Text>
                </Pressable>
              ))}
              <Pressable
                onPress={refresh}
                disabled={busy}
                style={{ marginLeft: "auto", paddingHorizontal: 14, paddingVertical: 7, borderRadius: 8, backgroundColor: c.accent, opacity: busy ? 0.5 : 1 }}
              >
                <Text style={{ color: "#fff", fontWeight: "700", fontSize: 13 }}>{busy ? "…" : "Yenile"}</Text>
              </Pressable>
            </View>

            {err ? <Text style={{ color: c.error, marginBottom: 10 }}>{err}</Text> : null}
            {busy && !data ? <ActivityIndicator color={c.accent} style={{ marginVertical: 20 }} /> : null}
            {!data && !busy ? <Text style={{ color: c.textMuted }}>No runs in this window yet.</Text> : null}

            {data ? (
              <>
                {/* KPI grid */}
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 10, marginBottom: 12 }}>
                  {[
                    { k: "Runs", v: data.totals.runs, col: c.textPrimary },
                    { k: "Screws", v: data.totals.screws, col: c.textPrimary },
                    { k: "Passed", v: data.totals.passed, col: c.success },
                    { k: "Failed", v: data.totals.failed, col: c.error },
                  ].map((m) => (
                    <View key={m.k} style={{ flexGrow: 1, flexBasis: "45%", backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 12, padding: 12 }}>
                      <Text style={{ color: c.textMuted, fontSize: 12 }}>{m.k}</Text>
                      <Text style={{ color: m.col, fontSize: 24, fontWeight: "700" }}>{m.v}</Text>
                    </View>
                  ))}
                </View>

                <View style={[card, { flexDirection: "row", alignItems: "baseline", justifyContent: "space-between" }]}>
                  <Text style={{ color: c.textSecondary, fontSize: 13 }}>Fail rate ({data.window?.days ?? days}d)</Text>
                  <Text style={{ color: rateColor(c, data.totals.failRate), fontSize: 30, fontWeight: "800" }}>{data.totals.failRate}%</Text>
                </View>

                {/* Daily fail-rate trend */}
                <View style={card}>
                  <Text style={{ color: c.textPrimary, fontWeight: "600", marginBottom: 8 }}>Daily fail rate</Text>
                  {trend.length === 0 ? (
                    <Text style={{ color: c.textMuted, fontSize: 12 }}>No daily data.</Text>
                  ) : (
                    <>
                      <Svg width="100%" height={CHART_H} viewBox={`0 0 ${CHART_W} ${CHART_H}`} preserveAspectRatio="none">
                        {[0, 0.5, 1].map((g) => (
                          <Line key={g} x1={0} x2={CHART_W} y1={6 + g * 90} y2={6 + g * 90} stroke={c.borderSubtle} strokeWidth={1} />
                        ))}
                        {trend.map((t, i) => {
                          const h = Math.max(2, (t.failRate / maxRate) * 90);
                          const w = Math.max(3, barGap * 0.6);
                          return <Rect key={t.date} x={i * barGap + (barGap - w) / 2} y={96 - h} width={w} height={h} rx={2} fill={rateColor(c, t.failRate)} />;
                        })}
                      </Svg>
                      <View style={{ flexDirection: "row", justifyContent: "space-between", marginTop: 2 }}>
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>{trend[0]?.date}</Text>
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>peak {maxRate}%</Text>
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>{trend[trend.length - 1]?.date}</Text>
                      </View>
                    </>
                  )}
                </View>

                {/* Flagged production orders */}
                <View style={card}>
                  <Text style={{ color: c.textPrimary, fontWeight: "600", marginBottom: 8 }}>
                    Flagged production orders{data.flaggedOrders.length > 0 ? ` (${data.flaggedOrders.length})` : ""}
                  </Text>
                  {data.flaggedOrders.length === 0 ? (
                    <Text style={{ color: c.success, fontSize: 12 }}>None flagged — every order passed clean. ✅</Text>
                  ) : (
                    data.flaggedOrders.map((o) => (
                      <View key={o.ficheno} style={{ borderTopColor: c.borderSubtle, borderTopWidth: 1, paddingVertical: 8, flexDirection: "row", justifyContent: "space-between" }}>
                        <View style={{ flex: 1 }}>
                          <Text style={{ color: c.textPrimary, fontWeight: "600", fontFamily: "Courier" }}>{o.ficheno}</Text>
                          <Text style={{ color: c.textMuted, fontSize: 11 }}>
                            {o.productId || "—"} · {o.flaggedBlocks}/{o.blocks} blocks · {fmtTime(o.lastAt)}
                          </Text>
                        </View>
                        <Text style={{ color: rateColor(c, o.failRate), fontWeight: "700" }}>{o.failRate}%</Text>
                      </View>
                    ))
                  )}
                </View>

                {/* Per-block worst-first */}
                <View style={card}>
                  <Text style={{ color: c.textPrimary, fontWeight: "600", marginBottom: 8 }}>Blocks — worst first</Text>
                  {data.byLabel.length === 0 ? (
                    <Text style={{ color: c.textMuted, fontSize: 12 }}>No labelled blocks.</Text>
                  ) : (
                    data.byLabel.map((b) => (
                      <View key={b.label} style={{ borderTopColor: c.borderSubtle, borderTopWidth: 1, paddingVertical: 7, flexDirection: "row", justifyContent: "space-between" }}>
                        <Text style={{ color: c.textPrimary, flex: 1 }} numberOfLines={1}>{b.label || "(unlabelled)"}</Text>
                        <Text style={{ color: c.textMuted, fontSize: 12, marginRight: 10 }}>{b.passed}/{b.screws}</Text>
                        <Text style={{ color: rateColor(c, b.failRate), fontWeight: "700" }}>{b.failRate}%</Text>
                      </View>
                    ))
                  )}
                </View>

                {/* Recent runs */}
                <View style={card}>
                  <Text style={{ color: c.textPrimary, fontWeight: "600", marginBottom: 8 }}>Recent runs</Text>
                  {data.recent.length === 0 ? (
                    <Text style={{ color: c.textMuted, fontSize: 12 }}>No recent runs.</Text>
                  ) : (
                    data.recent.map((r) => (
                      <View key={r.id} style={{ borderTopColor: c.borderSubtle, borderTopWidth: 1, paddingVertical: 7, flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                        <View style={{ flex: 1 }}>
                          <Text style={{ color: c.textSecondary }} numberOfLines={1}>{r.label || r.id}</Text>
                          {r.ficheno ? <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "Courier" }}>{r.ficheno}</Text> : null}
                        </View>
                        <Text style={{ color: c.textMuted, fontSize: 12, marginRight: 10 }}>{r.passed}/{r.screws}</Text>
                        <Text style={{ color: rateColor(c, r.failRate), fontWeight: "700" }}>{r.failRate}%</Text>
                      </View>
                    ))
                  )}
                </View>
              </>
            ) : null}
          </>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}
