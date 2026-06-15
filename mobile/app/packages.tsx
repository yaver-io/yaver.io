import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Switch, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  HiddenPackageWebView,
  type HiddenExtractorHandle,
} from "../src/components/HiddenPackageWebView";
import { queueObservation, drainToAgent } from "../src/lib/onPhoneCollector";
import {
  packagesClient,
  getPackagesDeviceId,
  setPackagesDeviceId,
  type PackageTarget,
  type PackageRow,
  type PackageRunResult,
  type PackageCheckResult,
} from "../src/lib/packagesClient";
import {
  enableBackgroundRunner,
  disableBackgroundRunner,
  backgroundRunnerStatus,
} from "../src/lib/backgroundCollector";

export default function PackagesScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice() as any;
  const devices = (deviceCtx?.devices ?? []) as any[];

  const [deviceId, setDeviceId] = useState("");
  const [pkgs, setPkgs] = useState<PackageRow[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [run, setRun] = useState<PackageRunResult | null>(null);
  const [checks, setChecks] = useState<Record<string, PackageCheckResult>>({});
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [bgOn, setBgOn] = useState(false);
  const [bgName, setBgName] = useState("");
  const [webResult, setWebResult] = useState<{ name: string; status: string; fields: Record<string, string>; shipped: number } | null>(null);
  const extractorRef = useRef<HiddenExtractorHandle>(null);

  const target = useCallback((): PackageTarget | undefined => {
    if (!deviceId) return undefined;
    const d = devices.find((x) => x.id === deviceId || x.deviceId === deviceId);
    return { id: deviceId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  useEffect(() => {
    getPackagesDeviceId().then((id) => id && setDeviceId(id));
    backgroundRunnerStatus().then((s) => {
      setBgOn(!!s.name);
      setBgName(s.name);
    });
  }, []);

  const load = useCallback(async () => {
    const t = target();
    if (!t) return;
    setErr(null);
    try {
      const res = await packagesClient.list(t);
      if ((res as any)?.ok === false) throw new Error((res as any).error);
      setPkgs(res?.packages ?? []);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }, [target]);

  useEffect(() => {
    if (!deviceId) return;
    setPackagesDeviceId(deviceId);
    void load();
  }, [deviceId, load]);

  async function runOnce(name: string, confirm: boolean) {
    const t = target();
    if (!t) return;
    setBusy(true);
    setErr(null);
    setSelected(name);
    try {
      const res = await packagesClient.run(t, name, confirm);
      if ((res as any)?.ok === false) throw new Error((res as any).error);
      setRun(res?.run ?? null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function runCheck(name: string) {
    const t = target();
    if (!t) return;
    setBusy(true);
    setErr(null);
    setSelected(name);
    try {
      const res = await packagesClient.check(t, name);
      if ((res as any)?.ok === false) throw new Error((res as any).error);
      if (res?.check) setChecks((m) => ({ ...m, [name]: res.check }));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // runHere extracts the package's webview sources ON THIS PHONE (its own IP),
  // queues the observation, and ships it to the phone's local agent.
  async function runHere(name: string) {
    const t = target();
    if (!t) return;
    setBusy(true);
    setErr(null);
    setSelected(name);
    setWebResult(null);
    try {
      const detail = await packagesClient.get(t, name);
      const sources = detail?.package?.spec?.task?.sources ?? [];
      const dataset =
        detail?.package?.spec?.output?.dataset || detail?.package?.spec?.task?.dataset || name;
      const allFields: Record<string, string> = {};
      let status = "ok";
      for (const s of sources) {
        const render = s.render || "auto";
        if (render === "fetch") continue; // plain fetch is the agent's job
        const ex = s.extract || {};
        const selectors: Record<string, string> = {};
        for (const k of Object.keys(ex)) {
          if (ex[k]?.selector) selectors[k] = ex[k].selector;
        }
        if (Object.keys(selectors).length === 0) continue;
        const res = await extractorRef.current?.extract({ url: s.url, selectors });
        if (!res) continue;
        if (res.status !== "ok") {
          status = res.status;
          continue;
        }
        Object.assign(allFields, res.fields);
      }
      let shipped = 0;
      if (Object.keys(allFields).length > 0) {
        await queueObservation({ pkg: name, dataset, fields: allFields, at: Date.now() });
        shipped = await drainToAgent("127.0.0.1");
      }
      setWebResult({ name, status, fields: allFields, shipped });
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function toggleBackground(name: string, on: boolean) {
    try {
      if (on) {
        await enableBackgroundRunner(name);
        setBgOn(true);
        setBgName(name);
      } else {
        await disableBackgroundRunner();
        setBgOn(false);
        setBgName("");
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  const tone = (s: string) =>
    s === "ok" ? "#34d399" : s === "needs_confirmation" ? "#fbbf24" : s.startsWith("blocked") ? "#fb923c" : "#f87171";

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Task Packages" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16, gap: 12 }}>
        <Text style={[styles.label, { color: c.textMuted }]}>Device</Text>
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
          {devices.map((d) => {
            const id = d.id || d.deviceId;
            const on = id === deviceId;
            return (
              <Pressable
                key={id}
                onPress={() => setDeviceId(id)}
                style={[styles.chip, { borderColor: on ? c.accent : c.border, backgroundColor: on ? c.accent + "22" : "transparent" }]}
              >
                <Text style={{ color: c.textPrimary, fontSize: 13 }}>{d.name || id}</Text>
              </Pressable>
            );
          })}
        </View>

        {err && <Text style={{ color: "#f87171", fontSize: 13 }}>{err}</Text>}

        <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
          <Text style={[styles.label, { color: c.textMuted }]}>Packages</Text>
          <Pressable onPress={() => void load()}>
            <Text style={{ color: c.accent, fontSize: 13 }}>Refresh</Text>
          </Pressable>
        </View>

        {pkgs.length === 0 && <Text style={{ color: c.textMuted, fontSize: 13 }}>No packages on this device.</Text>}

        {pkgs.map((p) => (
          <View key={p.name} style={[styles.card, { borderColor: c.border }]}>
            <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{p.name}</Text>
              <Text style={{ color: c.textMuted, fontSize: 12 }}>
                {p.kind}
                {p.tier === "acting" ? " · acting" : ""}
              </Text>
            </View>
            <View style={{ flexDirection: "row", gap: 10, marginTop: 8, alignItems: "center" }}>
              <Pressable
                style={[styles.btn, { borderColor: c.border }]}
                disabled={busy}
                onPress={() => void runOnce(p.name, false)}
              >
                <Text style={{ color: c.textPrimary, fontSize: 13 }}>Run once</Text>
              </Pressable>
              {p.tier === "acting" && (
                <Pressable
                  style={[styles.btn, { borderColor: "#fbbf2455" }]}
                  disabled={busy}
                  onPress={() => void runOnce(p.name, true)}
                >
                  <Text style={{ color: "#fbbf24", fontSize: 13 }}>Run (confirm)</Text>
                </Pressable>
              )}
              <Pressable
                style={[styles.btn, { borderColor: "#38bdf855" }]}
                disabled={busy}
                onPress={() => void runCheck(p.name)}
              >
                <Text style={{ color: "#38bdf8", fontSize: 13 }}>Check</Text>
              </Pressable>
              <Pressable
                style={[styles.btn, { borderColor: "#a78bfa55" }]}
                disabled={busy}
                onPress={() => void runHere(p.name)}
              >
                <Text style={{ color: "#a78bfa", fontSize: 13 }}>Run here</Text>
              </Pressable>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 4, marginLeft: "auto" }}>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>periodic</Text>
                <Switch
                  value={bgOn && bgName === p.name}
                  onValueChange={(v) => void toggleBackground(p.name, v)}
                />
              </View>
            </View>

            {selected === p.name && busy && <ActivityIndicator style={{ marginTop: 8 }} color={c.accent} />}
            {selected === p.name && run && !busy && (
              <View style={{ marginTop: 8 }}>
                <Text style={{ color: tone(run.status), fontSize: 13, fontWeight: "600" }}>
                  {run.status}
                  {run.country ? ` · ${run.country}` : ""}
                  {run.observationId ? " · stored" : ""}
                </Text>
                {run.fields && Object.keys(run.fields).length > 0 && (
                  <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }} numberOfLines={6}>
                    {JSON.stringify(run.fields, null, 1)}
                  </Text>
                )}
                {run.mcpCalls && run.mcpCalls.length > 0 && (
                  <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
                    MCP: {run.mcpCalls.map((m: any) => String(m.name)).join(", ")}
                  </Text>
                )}
                {(run.notes ?? []).map((n, i) => (
                  <Text key={i} style={{ color: "#fbbf24", fontSize: 12, marginTop: 2 }}>
                    {n}
                  </Text>
                ))}
              </View>
            )}

            {checks[p.name] && (
              <View style={{ marginTop: 8 }}>
                <Text
                  style={{
                    fontSize: 13,
                    fontWeight: "600",
                    color:
                      checks[p.name].status === "pass"
                        ? "#34d399"
                        : checks[p.name].status === "warn"
                          ? "#fbbf24"
                          : "#f87171",
                  }}
                >
                  preflight: {checks[p.name].status}
                </Text>
                {(checks[p.name].findings ?? []).map((f, i) => (
                  <Text
                    key={i}
                    style={{
                      fontSize: 12,
                      marginTop: 2,
                      color: f.level === "fail" ? "#f87171" : f.level === "warn" ? "#fbbf24" : c.textMuted,
                    }}
                  >
                    {f.level === "fail" ? "✕" : f.level === "warn" ? "⚠" : "·"} {f.message}
                  </Text>
                ))}
              </View>
            )}

            {selected === p.name && webResult && webResult.name === p.name && !busy && (
              <View style={{ marginTop: 8 }}>
                <Text style={{ color: tone(webResult.status), fontSize: 13, fontWeight: "600" }}>
                  on-phone (WebView): {webResult.status}
                  {webResult.shipped > 0 ? ` · ${webResult.shipped} shipped` : ""}
                </Text>
                {Object.keys(webResult.fields).length > 0 ? (
                  <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }} numberOfLines={6}>
                    {JSON.stringify(webResult.fields, null, 1)}
                  </Text>
                ) : (
                  <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
                    no webview sources with selectors (this package uses fetch/MCP — use Run once)
                  </Text>
                )}
              </View>
            )}
          </View>
        ))}

        <HiddenPackageWebView ref={extractorRef} />

        {bgOn && (
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
            Background runner on for “{bgName}”. Android runs it ~every 15 min even when closed; iOS runs it
            when the system allows.
          </Text>
        )}
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  label: { fontSize: 12, textTransform: "uppercase", letterSpacing: 0.5 },
  chip: { borderWidth: 1, borderRadius: 999, paddingHorizontal: 12, paddingVertical: 6 },
  card: { borderWidth: 1, borderRadius: 14, padding: 12 },
  btn: { borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, paddingVertical: 6 },
});
