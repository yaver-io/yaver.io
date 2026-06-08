// App-Test Agent — run the in-repo flow corpus on a redroid surface and see the
// bugs the agent caught (and, in fix mode, fixed). Catch-only or fix; cold boot
// or a warm Yaver Base Image. Pick a device holding your app's repo, run, watch
// the live log, then read the report card.
import React, { useEffect, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { qaClient, type QAJob, type QAReport, type QATarget } from "../src/lib/qaClient";

const SEV_COLOR: Record<string, string> = {
  critical: "#ff5d5d",
  high: "#ff9f43",
  medium: "#ffd166",
  low: "#9aa7b4",
};
const OUTCOME_BADGE: Record<string, { label: string; color: string }> = {
  fixed: { label: "FIXED", color: "#2fbf71" },
  "attempted-unresolved": { label: "ATTEMPTED", color: "#ff9f43" },
  caught: { label: "CAUGHT", color: "#ff5d5d" },
};

export default function QAScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = ((deviceCtx as any).devices as any[]) || [];

  const [deviceId, setDeviceId] = useState("");
  const [pkg, setPkg] = useState("io.yaver.mobile");
  const [base, setBase] = useState("");
  const [mode, setMode] = useState<"catch" | "fix">("catch");
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<QAJob | null>(null);
  const [report, setReport] = useState<QAReport | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const target = (): QATarget | undefined => {
    const d = devices.find((x) => (x.deviceId || x.id) === deviceId);
    if (!d) return deviceId ? { id: deviceId } : undefined;
    return { id: deviceId, lanIps: d.lanIps || d.localIps, host: d.host };
  };

  // Poll while running, then pull the report.
  useEffect(() => {
    if (!job?.id || (job.state !== "running" && job.state !== "queued")) return;
    const t = target();
    if (!t) return;
    const iv = setInterval(async () => {
      const s = await qaClient.jobStatus(t, job.id);
      setJob(s);
      if (s.state === "completed") {
        const r = await qaClient.report(t, job.id!);
        if (!r?.error) setReport(r);
      }
    }, 3000);
    return () => clearInterval(iv);
  }, [job?.id, job?.state]);

  const run = async () => {
    const t = target();
    if (!t) {
      setErr("Pick a device that holds your app's repo first.");
      return;
    }
    setBusy(true);
    setErr(null);
    setReport(null);
    try {
      const j = await qaClient.run(t, { package: pkg, base: base || undefined, mode });
      if (j?.error) setErr(j.error);
      else setJob(j);
    } catch (e: any) {
      setErr(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  const label = { color: c.textMuted, fontSize: 12, marginTop: 14, marginBottom: 4 } as const;
  const input = {
    backgroundColor: c.bgCard,
    color: c.textPrimary,
    borderRadius: 8,
    padding: 10,
    borderWidth: 1,
    borderColor: c.border,
  } as const;

  const running = job?.state === "running" || job?.state === "queued";

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="App-Test Agent" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        <Text style={{ color: c.textMuted, fontSize: 13 }}>
          Drive your app through the yaver-tests/flows corpus on a redroid surface. The agent explores
          toward each goal; the oracle bank watches for red boxes, crashes, ANRs and blank screens.
        </Text>

        <Text style={label}>Device with your app's repo</Text>
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
          {devices.map((d) => {
            const id = d.deviceId || d.id;
            const on = id === deviceId;
            return (
              <Pressable
                key={id}
                onPress={() => setDeviceId(id)}
                style={{
                  backgroundColor: on ? c.accent : c.bgCard,
                  borderRadius: 8,
                  paddingVertical: 8,
                  paddingHorizontal: 12,
                  borderWidth: 1,
                  borderColor: c.border,
                }}
              >
                <Text style={{ color: on ? "#fff" : c.textPrimary, fontSize: 13 }}>{d.name || id}</Text>
                <Text style={{ color: on ? "#fff" : c.textMuted, fontSize: 10 }}>
                  {d.platform || ""} · {d.status || ""}
                </Text>
              </Pressable>
            );
          })}
        </View>

        <Text style={label}>App package</Text>
        <TextInput style={input} value={pkg} onChangeText={setPkg} autoCapitalize="none" placeholder="io.yaver.mobile" placeholderTextColor={c.textMuted} />

        <Text style={label}>Warm base version (optional — empty = cold boot)</Text>
        <TextInput style={input} value={base} onChangeText={setBase} autoCapitalize="none" placeholder="2026-06-09-1" placeholderTextColor={c.textMuted} />

        <Text style={label}>Mode</Text>
        <View style={{ flexDirection: "row", gap: 8 }}>
          {(["catch", "fix"] as const).map((m) => (
            <Pressable
              key={m}
              onPress={() => setMode(m)}
              style={{
                backgroundColor: m === mode ? c.accent : c.bgCard,
                borderRadius: 8,
                paddingVertical: 8,
                paddingHorizontal: 14,
                borderWidth: 1,
                borderColor: c.border,
              }}
            >
              <Text style={{ color: m === mode ? "#fff" : c.textPrimary, fontSize: 13 }}>
                {m === "catch" ? "Catch-only" : "Fix (draft)"}
              </Text>
            </Pressable>
          ))}
        </View>

        <Pressable
          onPress={run}
          disabled={busy || running}
          style={{ backgroundColor: c.accent, borderRadius: 10, padding: 14, alignItems: "center", marginTop: 18, opacity: busy || running ? 0.6 : 1 }}
        >
          {busy ? <ActivityIndicator color="#fff" /> : <Text style={{ color: "#fff", fontWeight: "600" }}>{running ? "Running…" : "Run app test"}</Text>}
        </Pressable>

        {err && (
          <View style={{ backgroundColor: "#3a1212", borderRadius: 8, padding: 12, marginTop: 16 }}>
            <Text style={{ color: "#ffb4b4", fontSize: 13 }}>{err}</Text>
          </View>
        )}

        {job && (
          <View style={{ marginTop: 18 }}>
            <Text style={{ color: c.textPrimary, fontWeight: "600", marginBottom: 6 }}>
              {job.state === "completed" ? "✓ " : running ? "● " : ""}
              {job.phase || job.state}
            </Text>
            <View style={{ backgroundColor: c.bgCard, borderRadius: 8, padding: 10, borderWidth: 1, borderColor: c.border }}>
              {(job.log || []).slice(-12).map((l, i) => (
                <Text key={i} style={{ color: c.textMuted, fontSize: 11, fontFamily: "Menlo" }}>{l}</Text>
              ))}
            </View>
          </View>
        )}

        {report && <ReportCard report={report} c={c} />}
      </ScrollView>
    </View>
  );
}

function ReportCard({ report, c }: { report: QAReport; c: any }) {
  return (
    <View style={{ marginTop: 20 }}>
      <View style={{ flexDirection: "row", gap: 10 }}>
        <Stat label="Caught" value={report.caught ?? 0} color="#ff9f43" c={c} />
        <Stat label="Fixed" value={report.fixed ?? 0} color="#2fbf71" c={c} />
        <Stat label="Flows" value={report.flows?.length ?? 0} color={c.accent} c={c} />
      </View>
      <Text style={{ color: report.passed ? "#2fbf71" : "#ff5d5d", fontWeight: "700", marginTop: 10 }}>
        {report.passed ? "PASS — no unresolved bugs" : `${(report.bugs || []).filter((b) => b.outcome !== "fixed").length} unresolved bug(s)`}
      </Text>

      {(report.bugs || []).map((b, i) => {
        const badge = OUTCOME_BADGE[b.outcome || "caught"];
        return (
          <View key={i} style={{ backgroundColor: c.bgCard, borderRadius: 8, padding: 12, marginTop: 10, borderLeftWidth: 3, borderLeftColor: SEV_COLOR[b.severity] || "#888" }}>
            <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
              <Text style={{ color: c.textPrimary, fontWeight: "600", flex: 1 }}>{b.title}</Text>
              <Text style={{ color: badge.color, fontSize: 10, fontWeight: "700" }}>{badge.label}</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
              {b.oracle} · {b.severity}
            </Text>
            {b.detail ? <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>{b.detail}</Text> : null}
            {b.fixSummary ? <Text style={{ color: "#2fbf71", fontSize: 11, marginTop: 4 }}>🔧 {b.fixSummary}</Text> : null}
          </View>
        );
      })}
    </View>
  );
}

function Stat({ label, value, color, c }: { label: string; value: number; color: string; c: any }) {
  return (
    <View style={{ flex: 1, backgroundColor: c.bgCard, borderRadius: 10, padding: 12, alignItems: "center", borderWidth: 1, borderColor: c.border }}>
      <Text style={{ color, fontSize: 24, fontWeight: "800" }}>{value}</Text>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>{label}</Text>
    </View>
  );
}
