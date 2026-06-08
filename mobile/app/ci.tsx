// Self-hosted CI — configure a Yaver box as a GitHub/GitLab self-hosted runner
// from the phone, over the mesh. Register a repo → its existing workflows
// (runs-on: [self-hosted, yaver]) run on the box for $0 GitHub minutes. Shows
// the savings ledger and scaffolds deploy workflows (npm / TestFlight / Play
// internal). Same agent verbs as web. See docs/yaver-managed-cloud-ci-absorption.md.
import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  ciClient,
  getCIDeviceId,
  setCIDeviceId,
  type CIRegistration,
  type CISavings,
  type CITarget,
  type CIWorkflowTarget,
} from "../src/lib/ciClient";

const dollars = (cents: number) => `$${((cents || 0) / 100).toFixed(2)}`;

export default function CIScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];

  const [deviceId, setDeviceId] = useState("");
  const [regs, setRegs] = useState<CIRegistration[]>([]);
  const [savings, setSavings] = useState<CISavings | null>(null);
  const [wfTargets, setWfTargets] = useState<CIWorkflowTarget[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  const [provider, setProvider] = useState<"github" | "gitlab">("github");
  const [repo, setRepo] = useState("");
  const [scope, setScope] = useState<"repo" | "org">("repo");
  const [isolation, setIsolation] = useState<"container" | "host">("container");

  const [wfTarget, setWfTarget] = useState("test");
  const [preview, setPreview] = useState<{ path: string; content: string; secrets: string[] } | null>(null);

  const target = useCallback((): CITarget | undefined => {
    if (!deviceId) return undefined;
    const d = devices?.find((x) => x.id === deviceId || x.deviceId === deviceId);
    return { id: deviceId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  useEffect(() => {
    getCIDeviceId().then((id) => id && setDeviceId(id));
  }, []);

  const refresh = useCallback(async () => {
    const t = target();
    if (!t) return;
    try {
      const s = await ciClient.status(t);
      if (Array.isArray(s?.registrations)) setRegs(s.registrations);
      if (s?.savings) setSavings(s.savings);
    } catch (e: any) {
      setMsg(String(e?.message || e));
    }
  }, [target]);

  useEffect(() => {
    if (!deviceId) return;
    setCIDeviceId(deviceId);
    const t = target();
    if (!t) return;
    (async () => {
      await refresh();
      try {
        const wt = await ciClient.workflowTargets(t);
        if (Array.isArray(wt?.targets)) setWfTargets(wt.targets);
      } catch {}
    })();
  }, [deviceId]); // eslint-disable-line

  const register = async () => {
    const t = target();
    if (!t) return;
    if (!repo.trim()) {
      setMsg("enter owner/repo");
      return;
    }
    setBusy(true);
    setMsg(null);
    const r = await ciClient.register(t, { provider, target: repo.trim(), scope, isolation });
    setBusy(false);
    if (r?.ok === false) {
      setMsg(r.error || "register failed");
      return;
    }
    setMsg(`registered — runs-on: ${JSON.stringify(r?.runsOn || ["self-hosted", "yaver"])}`);
    setRepo("");
    refresh();
  };

  const remove = async (key: string) => {
    const t = target();
    if (!t) return;
    setBusy(true);
    await ciClient.remove(t, key);
    setBusy(false);
    refresh();
  };

  const scaffold = async (write: boolean) => {
    const t = target();
    if (!t) return;
    setBusy(true);
    setMsg(null);
    const r = await ciClient.scaffold(t, wfTarget, write);
    setBusy(false);
    if (r?.ok === false) {
      setMsg(r.error || "scaffold failed");
      if (r?.content) setPreview({ path: r.path, content: r.content, secrets: r.secrets || [] });
      return;
    }
    setPreview({ path: r.path, content: r.content, secrets: r.secrets || [] });
    if (write) setMsg(`wrote ${r.path}`);
  };

  // ---------- styles ----------
  const card = { backgroundColor: c.bgCard, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 12, padding: 14, marginBottom: 12 };
  const h = { color: c.textPrimary, fontSize: 15, fontWeight: "700" as const, marginBottom: 10 };
  const label = { color: c.textSecondary, fontSize: 12, marginBottom: 6 };
  const input = { backgroundColor: c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary, marginBottom: 10 };

  const pill = (active: boolean) => ({
    paddingVertical: 6,
    paddingHorizontal: 12,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: active ? c.accent : c.borderSubtle,
    backgroundColor: active ? c.accent : "transparent",
    marginRight: 8,
    marginBottom: 8,
  });
  const pillText = (active: boolean) => ({ color: active ? c.textInverse : c.textSecondary, fontSize: 12, fontWeight: "600" as const });

  const Toggle = ({ opts, value, onChange }: { opts: string[]; value: string; onChange: (v: any) => void }) => (
    <View style={{ flexDirection: "row", flexWrap: "wrap" }}>
      {opts.map((o) => (
        <Pressable key={o} onPress={() => onChange(o)} style={pill(value === o)}>
          <Text style={pillText(value === o)}>{o}</Text>
        </Pressable>
      ))}
    </View>
  );

  if (!deviceId) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader title="Self-hosted CI" onBack={() => router.back()} />
        <ScrollView contentContainerStyle={{ padding: 16 }}>
          <Text style={[h, { marginBottom: 14 }]}>Pick the box to run CI on</Text>
          {(devices || []).map((d) => (
            <Pressable key={d.id || d.deviceId} onPress={() => setDeviceId(d.id || d.deviceId)} style={[card, { flexDirection: "row", justifyContent: "space-between" }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{d.name || d.alias || d.id || d.deviceId}</Text>
              <Text style={{ color: c.textMuted }}>{d.online ? "online" : "offline"}</Text>
            </Pressable>
          ))}
          {(!devices || devices.length === 0) && <Text style={{ color: c.textMuted }}>No devices yet. Sign a box in first.</Text>}
        </ScrollView>
      </View>
    );
  }

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Self-hosted CI" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        <Pressable onPress={() => setDeviceId("")} style={{ marginBottom: 12 }}>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>← change box</Text>
        </Pressable>

        {savings && savings.runs > 0 && (
          <View style={[card, { borderColor: c.accent }]}>
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>
              {savings.runs} run{savings.runs === 1 ? "" : "s"} · saved {dollars(savings.savedCents)}
            </Text>
            <Text style={{ color: c.textSecondary, fontSize: 12, marginTop: 4 }}>
              GitHub would bill {dollars(savings.wouldHaveCostUpstreamCents)} · you paid {dollars(savings.chargedCents)}
            </Text>
          </View>
        )}

        {/* register */}
        <View style={card}>
          <Text style={h}>Register a repo</Text>
          <Text style={label}>Provider</Text>
          <Toggle opts={["github", "gitlab"]} value={provider} onChange={setProvider} />
          <Text style={label}>owner/repo (or org / project id)</Text>
          <TextInput style={input} value={repo} onChangeText={setRepo} placeholder="owner/repo" placeholderTextColor={c.textMuted} autoCapitalize="none" autoCorrect={false} />
          <Text style={label}>Scope</Text>
          <Toggle opts={["repo", "org"]} value={scope} onChange={setScope} />
          <Text style={label}>Isolation</Text>
          <Toggle opts={["container", "host"]} value={isolation} onChange={setIsolation} />
          <Pressable disabled={busy} onPress={register} style={{ backgroundColor: c.accent, padding: 12, borderRadius: 10, alignItems: "center", marginTop: 6, opacity: busy ? 0.5 : 1 }}>
            <Text style={{ color: c.textInverse, fontWeight: "700" }}>Register runner</Text>
          </Pressable>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Private repos only by default. Token minted per-job from the box&apos;s git creds.</Text>
        </View>

        {/* registrations */}
        <View style={card}>
          <Text style={h}>Registered runners</Text>
          {regs.length === 0 && <Text style={{ color: c.textMuted, fontSize: 13 }}>None yet.</Text>}
          {regs.map((r) => (
            <View key={r.key} style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", paddingVertical: 8, borderTopColor: c.borderSubtle, borderTopWidth: 1 }}>
              <View style={{ flex: 1 }}>
                <Text style={{ color: c.textPrimary, fontFamily: "monospace" }}>
                  {r.key} <Text style={{ color: r.live ? c.accent : c.textMuted }}>{r.live ? "● live" : "○ idle"}</Text>
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  {r.isolation} · {r.where}
                </Text>
              </View>
              <Pressable disabled={busy} onPress={() => remove(r.key)} style={{ borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 8, paddingVertical: 6, paddingHorizontal: 10 }}>
                <Text style={{ color: c.textSecondary, fontSize: 12 }}>Remove</Text>
              </Pressable>
            </View>
          ))}
        </View>

        {/* workflow scaffold */}
        <View style={card}>
          <Text style={h}>Scaffold a deploy workflow</Text>
          <Toggle opts={wfTargets.length ? wfTargets.map((t) => t.target) : ["test", "npm", "testflight", "play-internal"]} value={wfTarget} onChange={setWfTarget} />
          {wfTargets.find((t) => t.target === wfTarget)?.description ? (
            <Text style={{ color: c.textSecondary, fontSize: 12, marginBottom: 8 }}>{wfTargets.find((t) => t.target === wfTarget)?.description}</Text>
          ) : null}
          <View style={{ flexDirection: "row", gap: 10 }}>
            <Pressable disabled={busy} onPress={() => scaffold(false)} style={{ flex: 1, borderColor: c.borderSubtle, borderWidth: 1, padding: 10, borderRadius: 10, alignItems: "center" }}>
              <Text style={{ color: c.textSecondary, fontWeight: "600" }}>Preview</Text>
            </Pressable>
            <Pressable disabled={busy} onPress={() => scaffold(true)} style={{ flex: 1, backgroundColor: c.accent, padding: 10, borderRadius: 10, alignItems: "center", opacity: busy ? 0.5 : 1 }}>
              <Text style={{ color: c.textInverse, fontWeight: "700" }}>Write to repo</Text>
            </Pressable>
          </View>
          {preview && (
            <View style={{ marginTop: 10 }}>
              <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 4 }}>{preview.path}</Text>
              <ScrollView horizontal style={{ backgroundColor: c.bg, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 8, padding: 8, maxHeight: 220 }}>
                <Text style={{ color: c.textSecondary, fontSize: 11, fontFamily: "monospace" }}>{preview.content}</Text>
              </ScrollView>
              {preview.secrets.length > 0 && (
                <Text style={{ color: "#f59e0b", fontSize: 11, marginTop: 6 }}>Set secrets: {preview.secrets.join(", ")}</Text>
              )}
            </View>
          )}
        </View>

        {busy && <ActivityIndicator color={c.accent} style={{ marginVertical: 8 }} />}
        {msg && <Text style={{ color: c.textSecondary, fontSize: 13, marginTop: 4 }}>{msg}</Text>}
      </ScrollView>
    </View>
  );
}
