// DiscoveryDiagnosticsPanel — the "why is discovery stuck?" modal.
//
// Replaces the old vague "the remote agent may be unreachable" callout
// with a live checklist that probes each layer (reachable → agent signed
// in → this phone authorized → AI-agent OAuth → file-access/scan) and,
// for anything that's not green, shows numbered fix-it steps plus an
// in-app action. Pure rendering — all logic lives in lib/discoveryDiagnostics.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useColors, useTheme } from "../context/ThemeContext";
import { monoFamily } from "../theme/tokens";
import {
  runDiscoveryDiagnostics,
  type CheckStatus,
  type DiagnosticAction,
  type DiagnosticCheck,
  type DiagnosticsProbe,
  type DiagnosticsReport,
} from "../lib/discoveryDiagnostics";

interface Props {
  visible: boolean;
  onClose: () => void;
  /** Device-pinned probe (baseUrl + headers + host). Null while we don't
   *  have a live client for the active device — panel shows a hint. */
  probe: DiagnosticsProbe | null;
  onOpenDevices: () => void;
  onRetryScan: () => void;
  onReauth: () => void;
  onRunnerAuth: () => void;
}

export default function DiscoveryDiagnosticsPanel({
  visible,
  onClose,
  probe,
  onOpenDevices,
  onRetryScan,
  onReauth,
  onRunnerAuth,
}: Props) {
  const c = useColors();
  const { isDark } = useTheme();
  const [checks, setChecks] = useState<DiagnosticCheck[]>([]);
  const [report, setReport] = useState<DiagnosticsReport | null>(null);
  const [running, setRunning] = useState(false);
  const runIdRef = useRef(0);

  const run = useCallback(async () => {
    if (!probe) return;
    const myRun = ++runIdRef.current;
    setRunning(true);
    setReport(null);
    setChecks([]);
    const result = await runDiscoveryDiagnostics(probe, (live) => {
      if (runIdRef.current === myRun) setChecks(live);
    });
    if (runIdRef.current === myRun) {
      setChecks(result.checks);
      setReport(result);
      setRunning(false);
    }
  }, [probe]);

  // Auto-run once when opened.
  useEffect(() => {
    if (visible && probe) void run();
    if (!visible) {
      runIdRef.current++; // cancel any in-flight callback updates
      setChecks([]);
      setReport(null);
      setRunning(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible]);

  const handleAction = useCallback(
    (action: DiagnosticAction) => {
      switch (action.kind) {
        case "openDevices":
          onClose();
          onOpenDevices();
          break;
        case "retryScan":
          onRetryScan();
          void run();
          break;
        case "reauth":
          onClose();
          onReauth();
          break;
        case "runnerAuth":
          onClose();
          onRunnerAuth();
          break;
      }
    },
    [onClose, onOpenDevices, onRetryScan, onReauth, onRunnerAuth, run],
  );

  const overallColor =
    report?.overall === "ok" ? c.success : report?.overall === "degraded" ? c.warn : c.error;

  return (
    <Modal visible={visible} animationType="slide" transparent onRequestClose={onClose}>
      <View style={[st.backdrop, { backgroundColor: "rgba(0,0,0,0.55)" }]}>
        <View style={[st.sheet, { backgroundColor: c.bgCardElevated, borderColor: c.borderSubtle }]}>
          {/* Header */}
          <View style={st.header}>
            <View style={{ flex: 1 }}>
              <Text style={[st.title, { color: c.textPrimary }]}>Discovery diagnostics</Text>
              <Text style={[st.subtitle, { color: c.textSecondary }]}>
                {probe?.host ? `Checking ${probe.host}` : "No active device"}
              </Text>
            </View>
            <Pressable onPress={onClose} hitSlop={10} style={[st.iconBtn, { backgroundColor: c.bgInput }]}>
              <Ionicons name="close" size={18} color={c.textSecondary} />
            </Pressable>
          </View>

          {/* Overall banner */}
          {report ? (
            <View
              style={[
                st.overall,
                {
                  backgroundColor:
                    report.overall === "ok" ? c.successBg : report.overall === "degraded" ? c.warnBg : c.errorBg,
                  borderColor:
                    report.overall === "ok" ? c.successBorder : report.overall === "degraded" ? c.warnBorder : c.errorBorder,
                },
              ]}
            >
              <Ionicons
                name={report.overall === "ok" ? "checkmark-circle" : report.overall === "degraded" ? "alert-circle" : "close-circle"}
                size={18}
                color={overallColor}
              />
              <Text style={[st.overallText, { color: overallColor }]}>{report.summary}</Text>
            </View>
          ) : (
            <View style={[st.overall, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
              <ActivityIndicator size="small" color={c.accent} />
              <Text style={[st.overallText, { color: c.textSecondary }]}>Running checks…</Text>
            </View>
          )}

          <ScrollView style={{ maxHeight: 460 }} contentContainerStyle={{ paddingBottom: 8 }}>
            {!probe ? (
              <Text style={[st.noProbe, { color: c.textSecondary }]}>
                No live connection to a device yet. Pick a device on the Devices tab, then run diagnostics.
              </Text>
            ) : (
              checks.map((check) => <CheckRow key={check.id} check={check} onAction={handleAction} />)
            )}
          </ScrollView>

          {/* Footer */}
          <View style={st.footer}>
            <Pressable
              onPress={() => void run()}
              disabled={running || !probe}
              style={[st.runBtn, { backgroundColor: c.accent, opacity: running || !probe ? 0.6 : 1 }]}
            >
              {running ? (
                <View style={st.row}>
                  <ActivityIndicator size="small" color={c.textInverse} />
                  <Text style={[st.runBtnText, { color: c.textInverse }]}>Running…</Text>
                </View>
              ) : (
                <Text style={[st.runBtnText, { color: c.textInverse }]}>Run again</Text>
              )}
            </Pressable>
          </View>
        </View>
      </View>
    </Modal>
  );
}

function CheckRow({
  check,
  onAction,
}: {
  check: DiagnosticCheck;
  onAction: (a: DiagnosticAction) => void;
}) {
  const c = useColors();
  const show = check.status === "fail" || check.status === "warn";
  return (
    <View style={[st.checkRow, { borderColor: c.borderSubtle }]}>
      <View style={st.checkHeader}>
        <StatusIcon status={check.status} />
        <View style={{ flex: 1 }}>
          <Text style={[st.checkLabel, { color: c.textPrimary }]}>{check.label}</Text>
          {check.detail ? (
            <Text style={[st.checkDetail, { color: c.textSecondary }]}>{check.detail}</Text>
          ) : null}
        </View>
      </View>

      {show && check.remediation && check.remediation.length > 0 ? (
        <View style={[st.fixBox, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
          {check.remediation.map((step, i) => (
            <View key={i} style={st.stepRow}>
              <Text style={[st.stepNum, { color: c.accent }]}>{i + 1}.</Text>
              <Text style={[st.stepText, { color: c.textSecondary }]}>{renderInlineCode(step, c)}</Text>
            </View>
          ))}
          {check.action ? (
            <Pressable
              onPress={() => onAction(check.action!)}
              style={[st.actionBtn, { backgroundColor: c.accentSoft, borderColor: c.accent + "55" }]}
            >
              <Text style={[st.actionBtnText, { color: c.accent }]}>{check.action.label} ›</Text>
            </Pressable>
          ) : null}
        </View>
      ) : null}
    </View>
  );
}

// Render `code spans` in a remediation step with a mono tint, without a
// markdown dep. Splits on backtick pairs.
function renderInlineCode(text: string, c: ReturnType<typeof useColors>): React.ReactNode {
  if (!text.includes("`")) return text;
  const parts = text.split("`");
  return parts.map((part, i) =>
    i % 2 === 1 ? (
      <Text key={i} style={{ fontFamily: monoFamily, color: c.textPrimary }}>
        {part}
      </Text>
    ) : (
      <Text key={i}>{part}</Text>
    ),
  );
}

function StatusIcon({ status }: { status: CheckStatus }) {
  const c = useColors();
  if (status === "running") return <ActivityIndicator size="small" color={c.accent} style={st.statusIcon} />;
  const map: Record<CheckStatus, { name: keyof typeof Ionicons.glyphMap; color: string }> = {
    pending: { name: "ellipse-outline", color: c.textMuted },
    running: { name: "ellipse-outline", color: c.accent },
    pass: { name: "checkmark-circle", color: c.success },
    warn: { name: "alert-circle", color: c.warn },
    fail: { name: "close-circle", color: c.error },
    skip: { name: "remove-circle-outline", color: c.textMuted },
  };
  const g = map[status];
  return <Ionicons name={g.name} size={20} color={g.color} style={st.statusIcon} />;
}

const st = StyleSheet.create({
  backdrop: { flex: 1, justifyContent: "flex-end" },
  sheet: {
    borderTopLeftRadius: 20,
    borderTopRightRadius: 20,
    borderWidth: 1,
    paddingHorizontal: 18,
    paddingTop: 16,
    paddingBottom: 28,
  },
  header: { flexDirection: "row", alignItems: "flex-start", marginBottom: 12 },
  title: { fontSize: 18, fontWeight: "700" },
  subtitle: { fontSize: 13, marginTop: 2 },
  iconBtn: { width: 32, height: 32, borderRadius: 16, alignItems: "center", justifyContent: "center" },
  overall: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderRadius: 10,
    borderWidth: 1,
    marginBottom: 12,
  },
  overallText: { fontSize: 12.5, flex: 1, lineHeight: 17, fontWeight: "600" },
  noProbe: { fontSize: 13, lineHeight: 19, paddingVertical: 12 },
  checkRow: { borderBottomWidth: StyleSheet.hairlineWidth, paddingVertical: 12 },
  checkHeader: { flexDirection: "row", gap: 10, alignItems: "flex-start" },
  statusIcon: { width: 22, marginTop: 1 },
  checkLabel: { fontSize: 14.5, fontWeight: "600" },
  checkDetail: { fontSize: 12.5, lineHeight: 18, marginTop: 2 },
  fixBox: { marginTop: 10, marginLeft: 32, padding: 12, borderRadius: 10, borderWidth: 1, gap: 8 },
  stepRow: { flexDirection: "row", gap: 8, alignItems: "flex-start" },
  stepNum: { fontSize: 13, fontWeight: "700", width: 16 },
  stepText: { fontSize: 13, lineHeight: 19, flex: 1 },
  actionBtn: {
    alignSelf: "flex-start",
    marginTop: 4,
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    borderWidth: 1,
  },
  actionBtnText: { fontSize: 13, fontWeight: "700" },
  footer: { marginTop: 14 },
  row: { flexDirection: "row", alignItems: "center", gap: 8 },
  runBtn: { borderRadius: 12, paddingVertical: 13, alignItems: "center" },
  runBtnText: { fontSize: 15, fontWeight: "700" },
});
