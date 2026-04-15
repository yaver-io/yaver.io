// Hybrid tab — mobile UI for yaver's planner+implementer mode.
//
// Mirrors web/app/hybrid/page.tsx in scope: pick a planner and a
// local implementer (aider + Qwen), type a feature prompt, and either
// preview the plan or plan+run in one shot. Tapping "Plan & Run" will
// block for minutes — Qwen 14B on a laptop is ~20 tok/s — so we keep
// a big idle spinner and avoid queuing multiple requests.
//
// Route is not listed in the tab bar (same pattern as autodev) —
// hidden by default and linked from the More tab.

import React, { useState } from "react";
import {
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import {
  quicClient,
  type HybridPlanResult,
  type HybridReport,
  type HybridRunRequest,
} from "../../src/lib/quic";

const DEFAULT: HybridRunRequest = {
  planner: "claude",
  implementer: "aider-ollama",
  model: "ollama_chat/qwen2.5-coder:14b",
  workDir: "",
  prompt: "",
  maxSubtasks: 15,
  timeoutSec: 1800,
};

export default function HybridScreen() {
  const c = useColors();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [req, setReq] = useState<HybridRunRequest>(DEFAULT);
  const [plan, setPlan] = useState<HybridPlanResult | null>(null);
  const [report, setReport] = useState<HybridReport | null>(null);
  const [busy, setBusy] = useState<"idle" | "planning" | "running">("idle");
  const [err, setErr] = useState<string | null>(null);

  const canSubmit = connected && busy === "idle" && req.prompt.trim() && req.workDir.trim();

  async function plan_() {
    setErr(null);
    setPlan(null);
    setReport(null);
    setBusy("planning");
    try {
      setPlan(await quicClient.hybridPlan(req));
    } catch (e: any) {
      setErr(e?.message ?? String(e));
    } finally {
      setBusy("idle");
    }
  }

  async function run_() {
    setErr(null);
    setReport(null);
    setBusy("running");
    try {
      setReport(await quicClient.hybridRun(req));
    } catch (e: any) {
      setErr(e?.message ?? String(e));
    } finally {
      setBusy("idle");
    }
  }

  return (
    <SafeAreaView style={[styles.root, { backgroundColor: c.bg }]}>
      <ScrollView contentContainerStyle={styles.scroll} keyboardShouldPersistTaps="handled">
        <Text style={[styles.title, { color: c.textPrimary }]}>Hybrid Mode</Text>
        <Text style={[styles.subtitle, { color: c.textMuted }]}>
          Claude plans → Qwen (local, free) implements. ~15–30× cheaper than pure
          Claude Code on feature loops.
        </Text>

        {!connected && (
          <View style={[styles.warnBox, { borderColor: c.warn }]}>
            <Text style={{ color: c.warn, fontSize: 13 }}>
              Not connected to a desktop agent — pick one from the Devices tab first.
            </Text>
          </View>
        )}

        <Row label="Planner" value={req.planner ?? ""} onChange={(v) => setReq({ ...req, planner: v })} c={c} />
        <Row label="Implementer" value={req.implementer ?? ""} onChange={(v) => setReq({ ...req, implementer: v })} c={c} />
        <Row label="Model" value={req.model ?? ""} onChange={(v) => setReq({ ...req, model: v })} c={c} />
        <Row
          label="Work dir (absolute path on the agent)"
          value={req.workDir}
          onChange={(v) => setReq({ ...req, workDir: v })}
          placeholder="/Users/you/projects/my-app"
          c={c}
        />

        <Text style={[styles.fieldLabel, { color: c.textMuted }]}>Feature prompt</Text>
        <TextInput
          style={[styles.textarea, { color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border }]}
          multiline
          value={req.prompt}
          placeholder="Add a Convex mutation createPortfolio(name, startingCashUsd)…"
          placeholderTextColor={c.textMuted}
          onChangeText={(t) => setReq({ ...req, prompt: t })}
        />

        <View style={styles.btnRow}>
          <TouchableOpacity
            style={[styles.btn, { backgroundColor: c.accent, opacity: canSubmit ? 1 : 0.4 }]}
            disabled={!canSubmit}
            onPress={plan_}
          >
            <Text style={styles.btnLabel}>{busy === "planning" ? "Planning…" : "Plan"}</Text>
          </TouchableOpacity>
          <TouchableOpacity
            style={[styles.btn, { backgroundColor: c.accentDim, opacity: canSubmit ? 1 : 0.4 }]}
            disabled={!canSubmit}
            onPress={run_}
          >
            <Text style={styles.btnLabel}>{busy === "running" ? "Running…" : "Plan & Run"}</Text>
          </TouchableOpacity>
        </View>

        {busy !== "idle" && (
          <View style={styles.busy}>
            <ActivityIndicator />
            <Text style={{ color: c.textMuted, marginTop: 8, fontSize: 12 }}>
              {busy === "running" ? "Qwen is writing code — this can take several minutes." : "Planner is thinking…"}
            </Text>
          </View>
        )}

        {err && (
          <View style={[styles.errBox, { borderColor: c.error }]}>
            <Text style={{ color: c.error, fontSize: 13 }}>{err}</Text>
          </View>
        )}

        {plan && !report && (
          <View style={styles.section}>
            <Text style={[styles.sectionH, { color: c.textPrimary }]}>Plan ({plan.subtasks.length} subtasks)</Text>
            {plan.subtasks.map((st, i) => (
              <View key={i} style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={[styles.cardTitle, { color: c.textPrimary }]}>{i + 1}. {st.title}</Text>
                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
                  Files: {st.files.join(", ")}
                </Text>
                <Text style={{ color: c.textPrimary, fontSize: 12, marginTop: 6 }}>{st.prompt}</Text>
              </View>
            ))}
          </View>
        )}

        {report && (
          <View style={styles.section}>
            <Text style={[styles.sectionH, { color: c.textPrimary }]}>
              Results — {report.ok ? "all green" : `${report.failedSteps} failed`}
            </Text>
            {report.results.map((r, i) => (
              <View key={i} style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={[styles.cardTitle, { color: r.status === "ok" ? c.success : c.error }]}>
                  {i + 1}. [{r.status}] {r.subtask.title}
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
                  {(r.durationMs / 1000).toFixed(1)}s
                </Text>
                {r.error ? (
                  <Text style={{ color: c.error, fontSize: 11, marginTop: 4 }}>{r.error}</Text>
                ) : null}
              </View>
            ))}
          </View>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

function Row({
  label,
  value,
  onChange,
  placeholder,
  c,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  c: ReturnType<typeof useColors>;
}) {
  return (
    <View style={{ marginTop: 12 }}>
      <Text style={[styles.fieldLabel, { color: c.textMuted }]}>{label}</Text>
      <TextInput
        style={[styles.input, { color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border }]}
        value={value}
        placeholder={placeholder}
        placeholderTextColor={c.textMuted}
        onChangeText={onChange}
        autoCapitalize="none"
        autoCorrect={false}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  scroll: { padding: 16, paddingBottom: 40 },
  title: { fontSize: 22, fontWeight: "700" },
  subtitle: { fontSize: 13, marginTop: 6, lineHeight: 18 },
  fieldLabel: { fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 4 },
  input: { borderWidth: 1, borderRadius: 6, paddingHorizontal: 10, paddingVertical: 8, fontSize: 13 },
  textarea: {
    borderWidth: 1, borderRadius: 6, paddingHorizontal: 10, paddingVertical: 8,
    fontSize: 13, minHeight: 120, marginTop: 12, textAlignVertical: "top",
  },
  btnRow: { flexDirection: "row", gap: 10, marginTop: 16 },
  btn: { flex: 1, paddingVertical: 12, borderRadius: 6, alignItems: "center" },
  btnLabel: { color: "#fff", fontWeight: "600", fontSize: 14 },
  busy: { marginTop: 16, alignItems: "center" },
  warnBox: { borderWidth: 1, borderRadius: 6, padding: 10, marginTop: 12 },
  errBox: { borderWidth: 1, borderRadius: 6, padding: 10, marginTop: 12 },
  section: { marginTop: 20 },
  sectionH: { fontSize: 16, fontWeight: "600", marginBottom: 10 },
  card: { borderWidth: 1, borderRadius: 6, padding: 10, marginBottom: 8 },
  cardTitle: { fontSize: 13, fontWeight: "600" },
});
