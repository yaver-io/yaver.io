import { urlHost } from "../../src/lib/urlHost";
import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Linking,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import * as Clipboard from "expo-clipboard";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import { useFocusEffect, useRouter, useLocalSearchParams } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import PairQrScanner from "../../src/components/PairQrScanner";
import SegmentedCodeInput from "../../src/components/SegmentedCodeInput";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import { quicClient, type HealthMonitorTarget, type MachineInfo } from "../../src/lib/quic";
import { useAuth } from "../../src/context/AuthContext";
import { getUserSettings } from "../../src/lib/auth";

// Optional-tool ids that are owner-only (experimental robotics / lab hardware).
// Hidden from non-owners regardless of the optional-tool preference.
const OWNER_ONLY_MORE_TOOLS = new Set<string>([
  "robot-cell",
  "printer",
  "circuit",
  "screw-cell",
]);
import { fetchPairInfo, submitPair, parsePairUrl } from "../../src/lib/pairDevice";
import { beaconListener, type DiscoveredDevice } from "../../src/lib/beacon";
import { isOptionalMoreToolEnabled, normalizeOptionalMoreTools, type OptionalMoreToolId } from "../../src/lib/moreOptionalTools";

// ── Quality Gates types ────────────────────────────────────────────

interface QualityCheck {
  type: string;
  available: boolean;
  command: string;
  framework: string;
}

interface QualityResult {
  id: string;
  type: string;
  status: string;
  duration?: number;
  output?: string;
  passed?: boolean;
  exitCode?: number;
  startedAt?: string;
}

// ── Git types ──────────────────────────────────────────────────────

interface GitStatusInfo {
  branch: string;
  ahead: number;
  behind: number;
  clean: boolean;
  staged: any[];
  modified: any[];
  untracked: any[];
}

interface GitCommitInfo {
  hash: string;
  shortHash: string;
  message: string;
  author: string;
  date: string;
}

// ── Quality Gates Section ──────────────────────────────────────────

function QualityGatesSection({ c }: { c: ReturnType<typeof useColors> }) {
  const [checks, setChecks] = useState<QualityCheck[]>([]);
  const [results, setResults] = useState<QualityResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [runningTypes, setRunningTypes] = useState<Set<string>>(new Set());
  const [expandedResult, setExpandedResult] = useState<string | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadData = useCallback(async () => {
    try {
      const [detectedChecks, existingResults] = await Promise.all([
        quicClient.detectQualityChecks(),
        quicClient.getQualityResults(),
      ]);
      setChecks(detectedChecks || []);
      setResults(existingResults || []);
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  // Poll results when checks are running
  useEffect(() => {
    if (runningTypes.size > 0) {
      pollRef.current = setInterval(async () => {
        try {
          const r = await quicClient.getQualityResults();
          setResults(r);
          // Clear running state for completed checks
          const stillRunning = new Set<string>();
          for (const type of runningTypes) {
            const result = r.find((res: QualityResult) => res.type === type);
            if (result && (result.status === "running" || result.status === "queued")) {
              stillRunning.add(type);
            }
          }
          setRunningTypes(stillRunning);
        } catch {
          // silent
        }
      }, 3000);
    }
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [runningTypes]);

  const handleRunCheck = useCallback(async (type: string) => {
    try {
      setRunningTypes((prev) => new Set(prev).add(type));
      await quicClient.runQualityCheck(type);
    } catch (e) {
      setRunningTypes((prev) => {
        const next = new Set(prev);
        next.delete(type);
        return next;
      });
      Alert.alert("Couldn't Run Check", `Yaver couldn't run this quality check. Check your connection and try again.\n\n${e instanceof Error ? e.message : "Failed to run check"}`);
    }
  }, []);

  const handleRunAll = useCallback(async () => {
    try {
      const available = checks.filter((ch) => ch.available);
      setRunningTypes(new Set(available.map((ch) => ch.type)));
      await quicClient.runAllQualityChecks();
    } catch (e) {
      setRunningTypes(new Set());
      Alert.alert("Couldn't Run Checks", `Yaver couldn't run the quality checks. Check your connection and try again.\n\n${e instanceof Error ? e.message : "Failed to run checks"}`);
    }
  }, [checks]);

  if (loading) {
    return (
      <View style={{ padding: 16, alignItems: "center" }}>
        <ActivityIndicator color={c.accent} />
      </View>
    );
  }

  const availableChecks = checks.filter((ch) => ch.available);
  const typeLabels: Record<string, string> = {
    test: "Test",
    lint: "Lint",
    typecheck: "TypeCheck",
    format: "Format",
  };

  return (
    <View style={{ paddingHorizontal: 14, paddingBottom: 8 }}>
      {/* Action buttons */}
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginBottom: 10 }}>
        {availableChecks.length > 1 && (
          <Pressable
            style={[s.actionBtn, { backgroundColor: c.accent }]}
            onPress={handleRunAll}
          >
            <Text style={[s.actionBtnText, { color: "#fff" }]}>Run All</Text>
          </Pressable>
        )}
        {availableChecks.map((ch) => (
          <Pressable
            key={ch.type}
            style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border }]}
            onPress={() => handleRunCheck(ch.type)}
            disabled={runningTypes.has(ch.type)}
          >
            {runningTypes.has(ch.type) ? (
              <ActivityIndicator size="small" color={c.accent} />
            ) : (
              <Text style={[s.actionBtnText, { color: c.textPrimary }]}>
                {typeLabels[ch.type] || ch.type}
              </Text>
            )}
          </Pressable>
        ))}
      </View>

      {availableChecks.length === 0 && (
        <Text style={{ color: c.textMuted, fontSize: 13, paddingVertical: 4 }}>
          No quality checks detected for this project.
        </Text>
      )}

      {/* Results */}
      {results.slice(0, 10).map((r) => {
        const passed = r.status === "passed" || (r.exitCode === 0 && r.status === "completed");
        const isRunning = r.status === "running" || r.status === "queued";
        const statusIcon = isRunning ? "\u25CB" : passed ? "\u2713" : "\u2717";
        const statusColor = isRunning ? c.textMuted : passed ? "#22c55e" : "#ef4444";

        return (
          <View key={r.id}>
            <Pressable
              style={[s.resultRow, { borderBottomColor: c.border }]}
              onPress={() => setExpandedResult(expandedResult === r.id ? null : r.id)}
            >
              <Text style={{ color: statusColor, fontSize: 16, fontWeight: "700", width: 24 }}>
                {statusIcon}
              </Text>
              <Text style={{ color: c.textPrimary, fontSize: 14, flex: 1, fontWeight: "500" }}>
                {typeLabels[r.type] || r.type}
              </Text>
              {r.duration != null && (
                <Text style={{ color: c.textMuted, fontSize: 12 }}>
                  {(r.duration / 1000).toFixed(1)}s
                </Text>
              )}
              <Text style={{ color: c.textMuted, fontSize: 14, marginLeft: 8 }}>
                {expandedResult === r.id ? "\u2304" : "\u203A"}
              </Text>
            </Pressable>
            {expandedResult === r.id && r.output && (
              <ScrollView
                style={[s.outputBox, { backgroundColor: c.bg, borderColor: c.border }]}
                nestedScrollEnabled
              >
                <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "Courier" }}>
                  {r.output}
                </Text>
              </ScrollView>
            )}
          </View>
        );
      })}
    </View>
  );
}

// ── Health Monitor Section ─────────────────────────────────────────

const HEALTH_STATUS_COLORS: Record<string, string> = {
  up: "#22c55e",
  down: "#ef4444",
  unknown: "#a1a1aa",
};

function formatHealthTime(time: string) {
  try {
    const diff = Date.now() - new Date(time).getTime();
    if (diff < 60_000) return "just now";
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
    return `${Math.floor(diff / 86_400_000)}d ago`;
  } catch {
    return time;
  }
}

function HealthMonitorSection({ c }: { c: ReturnType<typeof useColors> }) {
  const [targets, setTargets] = useState<HealthMonitorTarget[]>([]);
  const [loading, setLoading] = useState(true);
  const [addingUrl, setAddingUrl] = useState(false);
  const [newUrl, setNewUrl] = useState("");
  const [newLabel, setNewLabel] = useState("");
  const [expandedTarget, setExpandedTarget] = useState<string | null>(null);
  const [checkingId, setCheckingId] = useState<string | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadTargets = useCallback(async () => {
    try {
      const t = await quicClient.getHealthTargets();
      setTargets(t);
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadTargets();
    pollRef.current = setInterval(loadTargets, 30000);
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [loadTargets]);

  const handleAdd = useCallback(async () => {
    if (!newUrl.trim()) return;
    try {
      await quicClient.addHealthTarget(newUrl.trim(), newLabel.trim() || undefined);
      setNewUrl("");
      setNewLabel("");
      setAddingUrl(false);
      loadTargets();
    } catch (e) {
      Alert.alert("Couldn't Add Target", `Yaver couldn't add this health target. Check your connection and try again.\n\n${e instanceof Error ? e.message : "Failed to add target"}`);
    }
  }, [newUrl, newLabel, loadTargets]);

  const handleRemove = useCallback((id: string, label?: string) => {
    Alert.alert("Remove Target", `Remove ${label || "this target"}?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Remove",
        style: "destructive",
        onPress: async () => {
          try {
            await quicClient.removeHealthTarget(id);
            loadTargets();
          } catch {
            // silent
          }
        },
      },
    ]);
  }, [loadTargets]);

  const handleCheck = useCallback(async (id: string) => {
    setCheckingId(id);
    try {
      await quicClient.checkHealthTarget(id);
      await loadTargets();
    } catch {
      // silent
    } finally {
      setCheckingId(null);
    }
  }, [loadTargets]);

  if (loading) {
    return (
      <View style={{ padding: 16, alignItems: "center" }}>
        <ActivityIndicator color={c.accent} />
      </View>
    );
  }

  return (
    <View style={{ paddingHorizontal: 14, paddingBottom: 8, gap: 10 }}>
      {/* Add URL form / button */}
      {addingUrl ? (
        <View style={[hs.addForm, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="https://example.com/health"
            placeholderTextColor={c.textMuted}
            value={newUrl}
            onChangeText={setNewUrl}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
            autoFocus
          />
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="Label (optional)"
            placeholderTextColor={c.textMuted}
            value={newLabel}
            onChangeText={setNewLabel}
          />
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.accent, flex: 1 }]}
              onPress={handleAdd}
            >
              <Text style={[s.actionBtnText, { color: "#fff" }]}>Add</Text>
            </Pressable>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, flex: 1 }]}
              onPress={() => { setAddingUrl(false); setNewUrl(""); setNewLabel(""); }}
            >
              <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Cancel</Text>
            </Pressable>
          </View>
        </View>
      ) : (
        <Pressable
          style={[hs.addBtn, { backgroundColor: c.bgCard, borderColor: c.border }]}
          onPress={() => setAddingUrl(true)}
        >
          <Text style={{ color: c.accent, fontSize: 18, fontWeight: "300" }}>+</Text>
          <Text style={{ color: c.textMuted, fontSize: 13 }}>Add URL to monitor</Text>
        </Pressable>
      )}

      {targets.length === 0 && !addingUrl && (
        <View style={{ paddingVertical: 20, alignItems: "center" }}>
          <Text style={{ color: c.textMuted, fontSize: 13 }}>
            No health targets yet. Add a URL to start monitoring.
          </Text>
        </View>
      )}

      {/* Target cards — task-card style */}
      {targets.map((t) => {
        const statusKey =
          t.status === "warning"
            ? "warning"
            : t.status === "up" || t.statusCode === 200
            ? "up"
            : t.status === "down"
            ? "down"
            : t.status || "unknown";
        const isUp = statusKey === "up";
        const statusColor = HEALTH_STATUS_COLORS[statusKey] || HEALTH_STATUS_COLORS.unknown;
        const isExpanded = expandedTarget === t.id;
        const isChecking = checkingId === t.id;

        return (
          <Pressable
            key={t.id}
            style={[hs.targetCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => setExpandedTarget(isExpanded ? null : t.id)}
            onLongPress={() => handleRemove(t.id, t.label || t.url)}
          >
            {/* Header row — badges */}
            <View style={hs.targetHeader}>
              <View style={[hs.statusBadge, { backgroundColor: statusColor + "22" }]}>
                <Text style={[hs.statusText, { color: statusColor }]}>{statusKey}</Text>
              </View>
              {t.statusCode != null && (
                <View style={[hs.statusBadge, { backgroundColor: (isUp ? "#22c55e" : "#ef4444") + "22" }]}>
                  <Text style={[hs.statusText, { color: isUp ? "#22c55e" : "#ef4444" }]}>{t.statusCode}</Text>
                </View>
              )}
              {t.responseMs != null && (
                <View style={[hs.statusBadge, { backgroundColor: "#6366f122" }]}>
                  <Text style={[hs.statusText, { color: "#6366f1" }]}>{t.responseMs}ms</Text>
                </View>
              )}
              {isChecking && <ActivityIndicator size="small" color={c.accent} />}
            </View>

            {/* Title — label or URL */}
            <Text style={[hs.targetTitle, { color: c.textPrimary }]} numberOfLines={1}>
              {t.label || t.url}
            </Text>
            {t.label && (
              <Text style={[hs.targetUrl, { color: c.textMuted }]} numberOfLines={1}>{t.url}</Text>
            )}

            {/* Uptime bar */}
            {t.uptimePercent != null && (
              <View style={hs.uptimeRow}>
                <View style={[hs.uptimeBarBg, { backgroundColor: c.border }]}>
                  <View
                    style={[hs.uptimeBarFill, {
                      width: `${Math.min(t.uptimePercent, 100)}%`,
                      backgroundColor: t.uptimePercent >= 99 ? "#22c55e" : t.uptimePercent >= 95 ? "#f59e0b" : "#ef4444",
                    }]}
                  />
                </View>
                <Text style={[hs.uptimeText, { color: c.textMuted }]}>
                  {t.uptimePercent.toFixed(1)}% uptime
                </Text>
              </View>
            )}

            {/* Timestamp */}
            {t.lastChecked && (
              <Text style={[hs.targetTimestamp, { color: c.textMuted }]}>
                checked {formatHealthTime(t.lastChecked)}
              </Text>
            )}

            {/* Expanded details */}
            {isExpanded && (
              <View style={[hs.expandedSection, { borderTopColor: c.border }]}>
                {/* Action buttons */}
                <View style={{ flexDirection: "row", gap: 8, marginBottom: 8 }}>
                  <Pressable
                    style={[s.actionBtn, { backgroundColor: c.accent, flex: 1 }]}
                    onPress={() => handleCheck(t.id)}
                    disabled={isChecking}
                  >
                    {isChecking ? (
                      <ActivityIndicator size="small" color="#fff" />
                    ) : (
                      <Text style={[s.actionBtnText, { color: "#fff" }]}>Check Now</Text>
                    )}
                  </Pressable>
                  <Pressable
                    style={[s.actionBtn, { backgroundColor: "#ef444422", flex: 1 }]}
                    onPress={() => handleRemove(t.id, t.label || t.url)}
                  >
                    <Text style={[s.actionBtnText, { color: "#ef4444" }]}>Remove</Text>
                  </Pressable>
                </View>

                {/* History */}
                {t.history && t.history.length > 0 && (
                  <View style={{ gap: 2 }}>
                    <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600", marginBottom: 4 }}>
                      Recent Checks
                    </Text>
                    {t.history.slice(0, 10).map((h, i) => {
                      const hColor =
                        h.status === "warning"
                          ? "#f59e0b"
                          : h.status === "up"
                          ? "#22c55e"
                          : "#ef4444";
                      return (
                        <View key={i} style={hs.historyRow}>
                          <View style={[hs.historyDot, { backgroundColor: hColor }]} />
                          <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }}>
                            {h.responseMs}ms
                          </Text>
                          <Text style={{ color: c.textMuted, fontSize: 11 }}>
                            {formatHealthTime(h.time)}
                          </Text>
                        </View>
                      );
                    })}
                  </View>
                )}
              </View>
            )}
          </Pressable>
        );
      })}
    </View>
  );
}

const hs = StyleSheet.create({
  addForm: {
    borderRadius: 12,
    borderWidth: 1,
    padding: 14,
    gap: 8,
  },
  addBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    borderRadius: 12,
    borderWidth: 1,
    borderStyle: "dashed",
    padding: 14,
    justifyContent: "center",
  },
  targetCard: {
    borderRadius: 12,
    padding: 16,
    borderWidth: 1,
  },
  targetHeader: {
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 8,
    gap: 8,
  },
  statusBadge: {
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 6,
  },
  statusText: {
    fontSize: 12,
    fontWeight: "600",
    textTransform: "uppercase",
  },
  targetTitle: {
    fontSize: 16,
    fontWeight: "600",
  },
  targetUrl: {
    fontSize: 12,
    marginTop: 2,
    fontFamily: "monospace",
  },
  uptimeRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    marginTop: 10,
  },
  uptimeBarBg: {
    flex: 1,
    height: 4,
    borderRadius: 2,
    overflow: "hidden",
  },
  uptimeBarFill: {
    height: "100%",
    borderRadius: 2,
  },
  uptimeText: {
    fontSize: 11,
    fontWeight: "500",
    minWidth: 80,
    textAlign: "right",
  },
  targetTimestamp: {
    fontSize: 11,
    marginTop: 8,
  },
  expandedSection: {
    marginTop: 12,
    paddingTop: 12,
    borderTopWidth: StyleSheet.hairlineWidth,
  },
  historyRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingVertical: 3,
  },
  historyDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
  },
});

// ── Git Section ────────────────────────────────────────────────────

function GitSection({ c }: { c: ReturnType<typeof useColors> }) {
  const [status, setStatus] = useState<GitStatusInfo | null>(null);
  const [commits, setCommits] = useState<GitCommitInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [commitMsg, setCommitMsg] = useState("");
  const [showCommitInput, setShowCommitInput] = useState(false);
  const [actionLoading, setActionLoading] = useState<string | null>(null);

  const loadGitData = useCallback(async () => {
    try {
      const [s, log] = await Promise.all([
        quicClient.gitStatus(),
        quicClient.gitLog(undefined, 10),
      ]);
      setStatus(s);
      setCommits(log);
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadGitData();
  }, [loadGitData]);

  const doAction = useCallback(async (label: string, action: () => Promise<any>) => {
    setActionLoading(label);
    try {
      await action();
      await loadGitData();
    } catch (e) {
      Alert.alert(`${label} Failed`, `Yaver couldn't ${label.toLowerCase()} on your dev machine. Check your connection and try again.\n\n${e instanceof Error ? e.message : `Failed: ${label}`}`);
    } finally {
      setActionLoading(null);
    }
  }, [loadGitData]);

  const handlePull = useCallback(() => doAction("Pull", () => quicClient.gitPull()), [doAction]);
  const handleStash = useCallback(() => doAction("Stash", () => quicClient.gitStash()), [doAction]);

  const handlePush = useCallback(() => {
    Alert.alert("Push", "Push commits to remote?", [
      { text: "Cancel", style: "cancel" },
      { text: "Push", onPress: () => doAction("Push", () => quicClient.gitPush()) },
    ]);
  }, [doAction]);

  const handleCommit = useCallback(async () => {
    if (!commitMsg.trim()) return;
    setActionLoading("Commit");
    try {
      await quicClient.gitCommit(commitMsg.trim());
      setCommitMsg("");
      setShowCommitInput(false);
      await loadGitData();
    } catch (e) {
      Alert.alert("Error", e instanceof Error ? e.message : "Failed to commit");
    } finally {
      setActionLoading(null);
    }
  }, [commitMsg, loadGitData]);

  if (loading) {
    return (
      <View style={{ padding: 16, alignItems: "center" }}>
        <ActivityIndicator color={c.accent} />
      </View>
    );
  }

  if (!status) {
    return (
      <View style={{ padding: 14 }}>
        <Text style={{ color: c.textMuted, fontSize: 13 }}>Not a git repository.</Text>
      </View>
    );
  }

  const changedFiles = [
    ...status.staged.map((f: any) => ({ ...f, area: "S" })),
    ...status.modified.map((f: any) => ({ ...f, area: "M" })),
    ...status.untracked.map((f: any) => ({ ...f, area: "?" })),
  ];

  const statusIcons: Record<string, string> = {
    modified: "M",
    added: "A",
    deleted: "D",
    renamed: "R",
    untracked: "?",
  };

  return (
    <View style={{ paddingHorizontal: 14, paddingBottom: 8 }}>
      {/* Branch + status */}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 8 }}>
        <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>
          {"\u2387"} {status.branch}
        </Text>
        <Text style={{ color: status.clean ? "#22c55e" : "#f59e0b", fontSize: 12 }}>
          {status.clean ? "\u2713 clean" : "\u25CF dirty"}
        </Text>
        {status.ahead > 0 && (
          <Text style={{ color: c.textMuted, fontSize: 12 }}>{"\u2191"}{status.ahead}</Text>
        )}
        {status.behind > 0 && (
          <Text style={{ color: c.textMuted, fontSize: 12 }}>{"\u2193"}{status.behind}</Text>
        )}
      </View>

      {/* Action buttons */}
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginBottom: 10 }}>
        {(["Pull", "Push", "Stash", "Commit"] as const).map((label) => {
          const handlers: Record<string, () => void> = {
            Pull: handlePull,
            Push: handlePush,
            Stash: handleStash,
            Commit: () => setShowCommitInput(!showCommitInput),
          };
          const isLoading = actionLoading === label;
          return (
            <Pressable
              key={label}
              style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border }]}
              onPress={handlers[label]}
              disabled={isLoading}
            >
              {isLoading ? (
                <ActivityIndicator size="small" color={c.accent} />
              ) : (
                <Text style={[s.actionBtnText, { color: c.textPrimary }]}>{label}</Text>
              )}
            </Pressable>
          );
        })}
      </View>

      {/* Commit input */}
      {showCommitInput && (
        <View style={{ marginBottom: 10, gap: 6 }}>
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="Commit message..."
            placeholderTextColor={c.textMuted}
            value={commitMsg}
            onChangeText={setCommitMsg}
            multiline
          />
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.accent, flex: 1 }]}
              onPress={handleCommit}
              disabled={!commitMsg.trim() || actionLoading === "Commit"}
            >
              {actionLoading === "Commit" ? (
                <ActivityIndicator size="small" color="#fff" />
              ) : (
                <Text style={[s.actionBtnText, { color: "#fff" }]}>Commit All</Text>
              )}
            </Pressable>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, flex: 1 }]}
              onPress={() => { setShowCommitInput(false); setCommitMsg(""); }}
            >
              <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Cancel</Text>
            </Pressable>
          </View>
        </View>
      )}

      {/* Changed files */}
      {changedFiles.length > 0 && (
        <View style={{ marginBottom: 8 }}>
          <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600", marginBottom: 4 }}>
            Changed Files ({changedFiles.length})
          </Text>
          {changedFiles.slice(0, 20).map((f: any, i: number) => {
            const fileStatus = f.status || (f.area === "?" ? "untracked" : "modified");
            const icon = statusIcons[fileStatus] || f.area || "M";
            const iconColor = icon === "A" ? "#22c55e" : icon === "D" ? "#ef4444" : icon === "?" ? c.textMuted : "#f59e0b";
            return (
              <View key={i} style={{ flexDirection: "row", gap: 8, paddingVertical: 2 }}>
                <Text style={{ color: iconColor, fontSize: 12, fontFamily: "Courier", width: 16 }}>{icon}</Text>
                <Text style={{ color: c.textPrimary, fontSize: 12, fontFamily: "Courier", flex: 1 }} numberOfLines={1}>
                  {f.path || f.file || f.name || "unknown"}
                </Text>
              </View>
            );
          })}
          {changedFiles.length > 20 && (
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
              +{changedFiles.length - 20} more
            </Text>
          )}
        </View>
      )}

      {/* Recent commits */}
      {commits.length > 0 && (
        <View>
          <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600", marginBottom: 4 }}>
            Recent Commits
          </Text>
          {commits.map((cm) => (
            <View key={cm.hash} style={{ flexDirection: "row", gap: 8, paddingVertical: 3 }}>
              <Text style={{ color: c.accent, fontSize: 11, fontFamily: "Courier", width: 56 }}>
                {cm.shortHash}
              </Text>
              <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }} numberOfLines={1}>
                {cm.message}
              </Text>
            </View>
          ))}
        </View>
      )}
    </View>
  );
}

// ── Repo Sync Section ────────────────────────────────────────────

interface RepoInfoItem {
  name: string;
  path: string;
  branch: string;
  remote: string;
  lastCommit: string;
  dirty: boolean;
}

interface CredentialHost {
  host: string;
  username: string;
  hasToken: boolean;
}

function RepoSyncSection({ c }: { c: ReturnType<typeof useColors> }) {
  const [repos, setRepos] = useState<RepoInfoItem[]>([]);
  const [creds, setCreds] = useState<CredentialHost[]>([]);
  const [loading, setLoading] = useState(true);
  const [actionLoading, setActionLoading] = useState<string | null>(null);

  // Clone form
  const [showClone, setShowClone] = useState(false);
  const [cloneUrl, setCloneUrl] = useState("");
  const [cloneDir, setCloneDir] = useState("");
  const [cloneBranch, setCloneBranch] = useState("");

  // Credential form
  const [showAddCred, setShowAddCred] = useState(false);
  const [credHost, setCredHost] = useState("");
  const [credToken, setCredToken] = useState("");
  const [credUsername, setCredUsername] = useState("");

  // Expanded repo
  const [expandedRepo, setExpandedRepo] = useState<string | null>(null);

  const loadData = useCallback(async () => {
    try {
      const [repoList, credList] = await Promise.all([
        quicClient.listRepos(),
        quicClient.listRepoCredentials(),
      ]);
      setRepos(repoList);
      setCreds(credList);
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const handleClone = useCallback(async () => {
    if (!cloneUrl.trim()) return;
    setActionLoading("Clone");
    try {
      const result = await quicClient.cloneRepo(
        cloneUrl.trim(),
        cloneDir.trim() || undefined,
        cloneBranch.trim() || undefined,
      );
      Alert.alert("Cloned", `Repository cloned to:\n${result.path}`);
      setCloneUrl("");
      setCloneDir("");
      setCloneBranch("");
      setShowClone(false);
      await loadData();
    } catch (e) {
      Alert.alert("Clone Failed", e instanceof Error ? e.message : "Unknown error");
    } finally {
      setActionLoading(null);
    }
  }, [cloneUrl, cloneDir, cloneBranch, loadData]);

  const handlePull = useCallback(async (workDir: string) => {
    setActionLoading(`pull-${workDir}`);
    try {
      const result = await quicClient.pullRepo(workDir);
      Alert.alert("Pulled", result.output || "Already up to date.");
      await loadData();
    } catch (e) {
      Alert.alert("Pull Failed", e instanceof Error ? e.message : "Unknown error");
    } finally {
      setActionLoading(null);
    }
  }, [loadData]);

  const handleDeleteRepo = useCallback((repo: RepoInfoItem) => {
    Alert.alert(
      "Delete Remote Repo",
      `Delete ${repo.name} from the connected machine?\n\nThis removes the source code directory on that machine.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: async () => {
            setActionLoading(`delete-${repo.path}`);
            try {
              await quicClient.deleteRepo(repo.path);
              if (expandedRepo === repo.path) setExpandedRepo(null);
              await loadData();
            } catch (e) {
              Alert.alert("Delete Failed", e instanceof Error ? e.message : "Unknown error");
            } finally {
              setActionLoading(null);
            }
          },
        },
      ],
    );
  }, [expandedRepo, loadData]);

  const handleWorkspaceBootstrap = useCallback(async (repo: RepoInfoItem) => {
    setActionLoading(`workspace-${repo.path}`);
    try {
      const started = await quicClient.startExec("yaver workspace init --scaffold", {
        workDir: repo.path,
        timeout: 10 * 60_000,
      });
      const exec = await quicClient.waitForExec(started.execId, { timeoutMs: 10 * 60_000, pollMs: 1000 });
      if (exec.exitCode && exec.exitCode !== 0) {
        throw new Error(exec.stderr || exec.stdout || "workspace bootstrap failed");
      }
      Alert.alert(
        "Workspace Ready",
        exec.stdout?.trim() || `Scaffolded and initialized workspace in ${repo.name}.`,
      );
      await loadData();
    } catch (e) {
      Alert.alert("Workspace Bootstrap Failed", e instanceof Error ? e.message : "Unknown error");
    } finally {
      setActionLoading(null);
    }
  }, [loadData]);

  const handleAddCred = useCallback(async () => {
    if (!credHost.trim() || !credToken.trim()) return;
    setActionLoading("AddCred");
    try {
      await quicClient.setRepoCredential(
        credHost.trim(),
        credToken.trim(),
        credUsername.trim() || undefined,
      );
      setCredHost("");
      setCredToken("");
      setCredUsername("");
      setShowAddCred(false);
      await loadData();
    } catch (e) {
      Alert.alert("Couldn't Save Credential", `Yaver couldn't save the credential to your dev machine. Check your connection and try again.\n\n${e instanceof Error ? e.message : "Failed to save credential"}`);
    } finally {
      setActionLoading(null);
    }
  }, [credHost, credToken, credUsername, loadData]);

  const handleRemoveCred = useCallback((host: string) => {
    Alert.alert("Remove Credential", `Remove token for ${host}?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Remove",
        style: "destructive",
        onPress: async () => {
          try {
            await quicClient.removeRepoCredential(host);
            await loadData();
          } catch (e) {
            Alert.alert("Couldn't Remove Credential", `Yaver couldn't remove the credential. Check your connection and try again.\n\n${e instanceof Error ? e.message : "Failed to remove"}`);
          }
        },
      },
    ]);
  }, [loadData]);

  if (loading) {
    return (
      <View style={{ padding: 16, alignItems: "center" }}>
        <ActivityIndicator color={c.accent} />
      </View>
    );
  }

  return (
    <View style={{ paddingHorizontal: 14, paddingBottom: 8 }}>
      {/* Action buttons */}
      <View style={{ flexDirection: "row", gap: 8, marginBottom: 10 }}>
        <Pressable
          style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border }]}
          onPress={() => setShowClone(!showClone)}
        >
          <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Clone Repo</Text>
        </Pressable>
        <Pressable
          style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border }]}
          onPress={() => setShowAddCred(!showAddCred)}
        >
          <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Add Token</Text>
        </Pressable>
        <Pressable
          style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border }]}
          onPress={loadData}
        >
          <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Refresh</Text>
        </Pressable>
      </View>

      {/* Clone form */}
      {showClone && (
        <View style={{ marginBottom: 10, gap: 6 }}>
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="https://github.com/user/repo.git"
            placeholderTextColor={c.textMuted}
            value={cloneUrl}
            onChangeText={setCloneUrl}
            autoCapitalize="none"
            autoCorrect={false}
          />
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="Directory (optional, default ~/Projects)"
            placeholderTextColor={c.textMuted}
            value={cloneDir}
            onChangeText={setCloneDir}
            autoCapitalize="none"
            autoCorrect={false}
          />
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="Branch (optional)"
            placeholderTextColor={c.textMuted}
            value={cloneBranch}
            onChangeText={setCloneBranch}
            autoCapitalize="none"
            autoCorrect={false}
          />
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.accent, flex: 1 }]}
              onPress={handleClone}
              disabled={!cloneUrl.trim() || actionLoading === "Clone"}
            >
              {actionLoading === "Clone" ? (
                <ActivityIndicator size="small" color="#fff" />
              ) : (
                <Text style={[s.actionBtnText, { color: "#fff" }]}>Clone</Text>
              )}
            </Pressable>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, flex: 1 }]}
              onPress={() => { setShowClone(false); setCloneUrl(""); setCloneDir(""); setCloneBranch(""); }}
            >
              <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Cancel</Text>
            </Pressable>
          </View>
        </View>
      )}

      {/* Add credential form */}
      {showAddCred && (
        <View style={{ marginBottom: 10, gap: 6 }}>
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="Host (e.g. github.com)"
            placeholderTextColor={c.textMuted}
            value={credHost}
            onChangeText={setCredHost}
            autoCapitalize="none"
            autoCorrect={false}
          />
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="Personal Access Token"
            placeholderTextColor={c.textMuted}
            value={credToken}
            onChangeText={setCredToken}
            autoCapitalize="none"
            autoCorrect={false}
            secureTextEntry
          />
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="Username (optional)"
            placeholderTextColor={c.textMuted}
            value={credUsername}
            onChangeText={setCredUsername}
            autoCapitalize="none"
            autoCorrect={false}
          />
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.accent, flex: 1 }]}
              onPress={handleAddCred}
              disabled={!credHost.trim() || !credToken.trim() || actionLoading === "AddCred"}
            >
              {actionLoading === "AddCred" ? (
                <ActivityIndicator size="small" color="#fff" />
              ) : (
                <Text style={[s.actionBtnText, { color: "#fff" }]}>Save</Text>
              )}
            </Pressable>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, flex: 1 }]}
              onPress={() => { setShowAddCred(false); setCredHost(""); setCredToken(""); setCredUsername(""); }}
            >
              <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Cancel</Text>
            </Pressable>
          </View>
        </View>
      )}

      {/* Credentials list */}
      {creds.length > 0 && (
        <View style={{ marginBottom: 10 }}>
          <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600", marginBottom: 4 }}>
            Credentials ({creds.length})
          </Text>
          {creds.map((cr) => (
            <Pressable
              key={cr.host}
              style={[s.resultRow, { borderBottomColor: c.border }]}
              onLongPress={() => handleRemoveCred(cr.host)}
            >
              <Text style={{ color: "#22c55e", fontSize: 14, width: 20 }}>{"\u2713"}</Text>
              <Text style={{ color: c.textPrimary, fontSize: 13, flex: 1 }}>{cr.host}</Text>
              {cr.username ? (
                <Text style={{ color: c.textMuted, fontSize: 11 }}>{cr.username}</Text>
              ) : null}
            </Pressable>
          ))}
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
            Long press to remove
          </Text>
        </View>
      )}

      {/* Repos list */}
      {repos.length > 0 ? (
        <View>
          <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600", marginBottom: 4 }}>
            Repos ({repos.length})
          </Text>
          {repos.map((repo) => (
            <View key={repo.path}>
              <Pressable
                style={[s.resultRow, { borderBottomColor: c.border }]}
                onPress={() => setExpandedRepo(expandedRepo === repo.path ? null : repo.path)}
              >
                <Text style={{ color: repo.dirty ? "#f59e0b" : "#22c55e", fontSize: 14, width: 20 }}>
                  {repo.dirty ? "\u25CF" : "\u25CF"}
                </Text>
                <View style={{ flex: 1 }}>
                  <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "500" }}>{repo.name}</Text>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>{repo.branch}</Text>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 14 }}>
                  {expandedRepo === repo.path ? "\u2304" : "\u203A"}
                </Text>
              </Pressable>
              {expandedRepo === repo.path && (
                <View style={{ paddingLeft: 24, paddingVertical: 6, gap: 4 }}>
                  <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "Courier" }} numberOfLines={1}>
                    {repo.path}
                  </Text>
                  {repo.remote ? (
                    <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "Courier" }} numberOfLines={1}>
                      {repo.remote}
                    </Text>
                  ) : null}
                  {repo.lastCommit ? (
                    <Text style={{ color: c.textPrimary, fontSize: 12, marginTop: 2 }} numberOfLines={2}>
                      {repo.lastCommit}
                    </Text>
                  ) : null}
                  <Pressable
                    style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, alignSelf: "flex-start", marginTop: 4 }]}
                    onPress={() => handlePull(repo.path)}
                    disabled={actionLoading === `pull-${repo.path}`}
                  >
                    {actionLoading === `pull-${repo.path}` ? (
                      <ActivityIndicator size="small" color={c.accent} />
                    ) : (
                      <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Pull</Text>
                    )}
                  </Pressable>
                  <View style={{ flexDirection: "row", gap: 8, marginTop: 6, flexWrap: "wrap" }}>
                    <Pressable
                      style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border }]}
                      onPress={() => handleWorkspaceBootstrap(repo)}
                      disabled={actionLoading === `workspace-${repo.path}`}
                    >
                      {actionLoading === `workspace-${repo.path}` ? (
                        <ActivityIndicator size="small" color={c.accent} />
                      ) : (
                        <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Workspace Init</Text>
                      )}
                    </Pressable>
                    <Pressable
                      style={[s.actionBtn, { backgroundColor: "#7f1d1d", borderWidth: 1, borderColor: "#991b1b" }]}
                      onPress={() => handleDeleteRepo(repo)}
                      disabled={actionLoading === `delete-${repo.path}`}
                    >
                      {actionLoading === `delete-${repo.path}` ? (
                        <ActivityIndicator size="small" color="#fff" />
                      ) : (
                        <Text style={[s.actionBtnText, { color: "#fff" }]}>Delete Remote</Text>
                      )}
                    </Pressable>
                  </View>
                </View>
              )}
            </View>
          ))}
        </View>
      ) : (
        <Text style={{ color: c.textMuted, fontSize: 13, paddingVertical: 4 }}>
          No repos found. Clone one or check ~/Projects.
        </Text>
      )}
    </View>
  );
}

// ── Git Provider Section ────────────────────────────────────────────

interface GitProviderInfo {
  host: string;
  provider: string;
  username: string;
  avatarUrl?: string;
  hasSsh: boolean;
  setupAt: string;
}

export function GitProviderSection({ c }: { c: ReturnType<typeof useColors> }) {
  const [providers, setProviders] = useState<GitProviderInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [detecting, setDetecting] = useState(false);
  const [repos, setRepos] = useState<any[]>([]);
  const [showRepos, setShowRepos] = useState<string | null>(null);
  const [reposLoading, setReposLoading] = useState(false);
  const [cloning, setCloning] = useState<string | null>(null);
  const [repoSearch, setRepoSearch] = useState("");
  // Fallback: manual token entry (only if auto-detect fails)
  const [showManualSetup, setShowManualSetup] = useState<"github" | "gitlab" | null>(null);
  const [token, setToken] = useState("");
  // Target = which machine receives git creds. null means "this machine"
  // (the agent the phone is currently connected to). Anything else routes
  // through /peer/<deviceId>/... which the agent forwards via QUIC/relay.
  const [targetDeviceId, setTargetDeviceId] = useState<string | null>(null);
  const [machines, setMachines] = useState<MachineInfo[]>([]);

  const targetMachine = targetDeviceId
    ? machines.find((m) => m.deviceId === targetDeviceId)
    : null;
  const targetLabel = targetMachine?.name || targetMachine?.deviceId || "This machine";

  // Build the URL for an agent endpoint, peer-proxying when a remote
  // target is selected. The agent's /peer/<id>/... handler re-signs +
  // forwards verbatim, so individual git/provider/* endpoints don't
  // need to know about peers.
  const endpointFor = useCallback(
    (path: string) => {
      const baseUrl = (quicClient as any).baseUrl;
      if (!targetDeviceId) return `${baseUrl}${path}`;
      return `${baseUrl}/peer/${encodeURIComponent(targetDeviceId)}${path}`;
    },
    [targetDeviceId],
  );

  const loadMachines = useCallback(async () => {
    try {
      const result = await quicClient.consoleMachines();
      setMachines(result.machines || []);
    } catch {
      // silent — without a machine list the picker just shows "This machine"
    }
  }, []);

  const loadProviders = useCallback(async () => {
    setLoading(true);
    try {
      const headers = (quicClient as any).authHeaders;
      const res = await fetch(endpointFor("/git/provider/status"), { headers });
      const data = await res.json();
      if (data.ok) setProviders(data.providers || []);
      else setProviders([]);
    } catch {
      setProviders([]);
    } finally {
      setLoading(false);
    }
  }, [endpointFor]);

  useEffect(() => { loadMachines(); }, [loadMachines]);

  // Re-load providers whenever the target changes; clear stale browse
  // state so we never show repos from a different machine.
  useEffect(() => {
    setShowRepos(null);
    setRepos([]);
    setRepoSearch("");
    loadProviders();
  }, [loadProviders]);

  // Auto-detect: ask the agent to find tokens from gh/glab CLI, env vars,
  // and ~/.git-credentials on the *target* machine. When the target is a
  // remote box, this runs on that box, not on the phone.
  const handleAutoDetect = useCallback(async () => {
    setDetecting(true);
    try {
      const headers = (quicClient as any).authHeaders;
      const res = await fetch(endpointFor("/git/provider/detect"), { headers });
      const data = await res.json();
      if (data.ok && data.providers?.length > 0) {
        await loadProviders();
        const names = data.providers.map((p: any) => `${p.provider}: ${p.username}`).join("\n");
        Alert.alert("Found", `Detected on ${targetLabel}:\n${names}`);
      } else {
        Alert.alert(
          "No credentials found",
          `${targetLabel} doesn't have gh CLI or GitLab CLI logged in.\n\nInstall gh CLI on that machine and run 'gh auth login', or enter a token manually below.`,
          [
            { text: "OK" },
            { text: "Enter GitHub token", onPress: () => setShowManualSetup("github") },
            { text: "Enter GitLab token", onPress: () => setShowManualSetup("gitlab") },
          ],
        );
      }
    } catch (e) {
      Alert.alert("Detection Failed", `Yaver couldn't detect Git credentials on ${targetLabel}. Check your connection and try again.\n\n${e instanceof Error ? e.message : "Detection failed"}`);
    } finally {
      setDetecting(false);
    }
  }, [loadProviders, endpointFor, targetLabel]);

  // Manual token entry (fallback when auto-detect fails)
  const handleManualSetup = useCallback(async (provider: "github" | "gitlab") => {
    if (!token.trim()) return;
    setDetecting(true);
    try {
      const headers = { ...(quicClient as any).authHeaders, "Content-Type": "application/json" };
      const res = await fetch(endpointFor("/git/provider/setup"), {
        method: "POST", headers,
        body: JSON.stringify({ provider, token: token.trim() }),
      });
      const data = await res.json();
      if (data.ok) {
        Alert.alert("Connected", `Signed in as ${data.username} on ${targetLabel}`);
        setToken("");
        setShowManualSetup(null);
        await loadProviders();
      } else {
        Alert.alert("Couldn't Connect", `Yaver couldn't sign in with that token. Double-check the token and try again.\n\n${data.error || "Setup failed"}`);
      }
    } catch (e) {
      Alert.alert("Couldn't Connect", `Yaver couldn't complete sign-in on ${targetLabel}. Check your connection and try again.\n\n${e instanceof Error ? e.message : "Setup failed"}`);
    } finally {
      setDetecting(false);
    }
  }, [token, loadProviders, endpointFor, targetLabel]);

  const handleRemove = useCallback((providerHost: string) => {
    Alert.alert("Disconnect", `Remove ${providerHost} from ${targetLabel}?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Remove", style: "destructive", onPress: async () => {
          try {
            const headers = (quicClient as any).authHeaders;
            await fetch(endpointFor(`/git/provider/${encodeURIComponent(providerHost)}`), {
              method: "DELETE", headers,
            });
            await loadProviders();
          } catch {}
        },
      },
    ]);
  }, [loadProviders, endpointFor, targetLabel]);

  const handleBrowseRepos = useCallback(async (providerHost: string) => {
    if (showRepos === providerHost) { setShowRepos(null); return; }
    setShowRepos(providerHost);
    setReposLoading(true);
    setRepoSearch("");
    try {
      const headers = (quicClient as any).authHeaders;
      // Server now loads all pages (cap 1000) in one shot — keep
      // per_page large so callers that pin to a single page still
      // get a useful slice.
      const res = await fetch(endpointFor(`/git/provider/repos?host=${encodeURIComponent(providerHost)}&per_page=100`), { headers });
      const data = await res.json();
      if (data.ok) setRepos(data.repos || []);
    } catch {
      Alert.alert("Couldn't Load Repos", "Yaver couldn't load your repositories. Check your connection and try again.");
    } finally {
      setReposLoading(false);
    }
  }, [showRepos, endpointFor]);

  const handleClone = useCallback(async (repo: any) => {
    setCloning(repo.fullName);
    try {
      const headers = { ...(quicClient as any).authHeaders, "Content-Type": "application/json" };
      const res = await fetch(endpointFor("/repos/clone"), {
        method: "POST", headers,
        body: JSON.stringify({ url: repo.sshUrl || repo.cloneUrl, autoInit: true }),
      });
      const data = await res.json();
      if (data.ok) {
        const meta = data.metadata;
        const stackType = meta?.stackType ? `\nType: ${meta.stackType}` : "";
        const ci = Array.isArray(meta?.ciProviders) && meta.ciProviders.length
          ? `\nCI: ${meta.ciProviders.join(", ")}`
          : "";
        const integrations = Array.isArray(meta?.integrations) && meta.integrations.length
          ? `\nIntegrations: ${meta.integrations.join(", ")}`
          : "";
        const coding =
          Array.isArray(meta?.topology?.codingRunsOn) && meta.topology.codingRunsOn.length
            ? `\nCoding: user choice (${meta.topology.codingRunsOn.join(" → ")})${Array.isArray(meta?.topology?.codingRunners) && meta.topology.codingRunners.length ? ` (${meta.topology.codingRunners.length} desktop runner${meta.topology.codingRunners.length === 1 ? "" : "s"} detected)` : ""}`
            : "";
        const backend =
          Array.isArray(meta?.topology?.backendRunsOn) && meta.topology.backendRunsOn.includes("phone")
            ? `\nBackend: Yaver continuum (phone → your hardware)`
            : "";
        const autoinit = data.autoinit?.started
          ? `\nAutoinit: started`
          : data.autoinit?.error
            ? `\nAutoinit: ${data.autoinit.error}`
            : "";
        Alert.alert(
          data.alreadyExisted ? "Already Cloned" : "Cloned",
          `${repo.fullName}\n${data.path}${meta?.framework ? `\nFramework: ${meta.framework}` : ""}${stackType}${meta?.languages ? `\nLanguages: ${meta.languages.join(", ")}` : ""}${ci}${integrations}${coding}${backend}${autoinit}`,
        );
      } else {
        Alert.alert("Clone Failed", data.error || "Unknown error");
      }
    } catch (e) {
      Alert.alert("Clone Failed", `Yaver couldn't clone ${repo.fullName}. Check your connection and try again.\n\n${e instanceof Error ? e.message : "Clone failed"}`);
    } finally {
      setCloning(null);
    }
  }, [endpointFor]);

  // Device Flow (RFC 8628). When non-null, an OAuth approval is in
  // flight on `targetDeviceId` — UI shows the user_code + verification
  // URL and polls the agent until the state moves out of pending.
  type DeviceFlowSession = {
    sessionId: string;
    provider: "github" | "gitlab";
    host: string;
    userCode: string;
    verificationUri: string;
    interval: number;
    state: "pending" | "done" | "error" | "expired" | "unknown";
    username?: string;
    error?: string;
    byoClient?: boolean;
  };
  const [deviceFlow, setDeviceFlow] = useState<DeviceFlowSession | null>(null);
  const [deviceFlowStarting, setDeviceFlowStarting] = useState<"github" | "gitlab" | null>(null);

  const handleStartDeviceFlow = useCallback(async (provider: "github" | "gitlab") => {
    setDeviceFlowStarting(provider);
    try {
      const headers = { ...(quicClient as any).authHeaders, "Content-Type": "application/json" };
      const res = await fetch(endpointFor("/git/provider/oauth/start"), {
        method: "POST",
        headers,
        body: JSON.stringify({ provider }),
      });
      const data = await res.json();
      if (!data.ok || !data.session_id || !data.user_code || !data.verification_uri) {
        Alert.alert(
          "Couldn't Start Sign-In",
          data.error || "The agent rejected the request. If you haven't registered an OAuth App yet, set vault entry github-oauth-client-id (project=oauth) on the target machine first.",
        );
        return;
      }
      setDeviceFlow({
        sessionId: data.session_id,
        provider,
        host: data.host || (provider === "github" ? "github.com" : "gitlab.com"),
        userCode: data.user_code,
        verificationUri: data.verification_uri,
        interval: data.interval || 5,
        state: "pending",
        byoClient: !!data.byo_client,
      });
    } catch (e) {
      Alert.alert("Couldn't Start Sign-In", `Yaver couldn't start the sign-in flow. Check your connection and try again.\n\n${e instanceof Error ? e.message : "Failed to start"}`);
    } finally {
      setDeviceFlowStarting(null);
    }
  }, [endpointFor]);

  // Poll the active Device Flow session at the agent-prescribed
  // interval until the state moves out of pending. Restarts cleanly
  // when the session id, target, or interval changes.
  useEffect(() => {
    if (!deviceFlow || deviceFlow.state !== "pending") return;
    let cancelled = false;
    const intervalMs = Math.max(2, deviceFlow.interval) * 1000;
    const timer = setInterval(async () => {
      if (cancelled) return;
      try {
        const headers = (quicClient as any).authHeaders;
        const url = endpointFor(`/git/provider/oauth/status?session=${encodeURIComponent(deviceFlow.sessionId)}`);
        const res = await fetch(url, { headers });
        const data = await res.json();
        if (cancelled) return;
        if (!data.state || data.state === "pending") return;
        setDeviceFlow((prev) =>
          prev && prev.sessionId === deviceFlow.sessionId
            ? { ...prev, state: data.state, username: data.username, error: data.error }
            : prev,
        );
        if (data.state === "done") {
          await loadProviders();
          Alert.alert("Connected", `Linked ${deviceFlow.provider} as ${data.username || "user"} on ${targetLabel}.`);
        } else if (data.state === "error" || data.state === "expired" || data.state === "unknown") {
          Alert.alert(
            "Sign-In Didn't Complete",
            data.error || "Sign-in didn't complete — the code expired or was denied. Try again.",
          );
        }
      } catch {
        // soft-fail; keep polling
      }
    }, intervalMs);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [deviceFlow?.sessionId, deviceFlow?.state, deviceFlow?.interval, endpointFor, loadProviders, targetLabel]);

  const filteredRepos = repoSearch.trim()
    ? repos.filter((r: any) =>
        r.name.toLowerCase().includes(repoSearch.toLowerCase()) ||
        r.fullName.toLowerCase().includes(repoSearch.toLowerCase()))
    : repos;

  if (loading) {
    return <View style={{ padding: 16, alignItems: "center" }}><ActivityIndicator color={c.accent} /></View>;
  }

  // Build the target chip list. "This machine" + every owned, online,
  // non-self peer. Offline peers filtered out because the peer-proxy
  // would just time out.
  const targetOptions: { id: string | null; label: string; sub?: string }[] = [
    { id: null, label: "This machine" },
    ...machines
      .filter((m) => m.isOnline && !m.isLocal)
      .map((m) => ({ id: m.deviceId, label: m.name || m.deviceId, sub: m.platform })),
  ];

  return (
    <View style={{ paddingHorizontal: 14, paddingBottom: 12 }}>
      {/* Target picker — choose which machine receives git creds */}
      {targetOptions.length > 1 && (
        <View style={{ marginBottom: 10 }}>
          <Text style={{ color: c.textMuted, fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 6 }}>
            Configure on
          </Text>
          <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8, paddingRight: 12 }}>
            {targetOptions.map((opt) => {
              const active = (opt.id || null) === (targetDeviceId || null);
              return (
                <Pressable
                  key={opt.id || "__local__"}
                  onPress={() => {
                    if ((opt.id || null) === (targetDeviceId || null)) return;
                    setTargetDeviceId(opt.id);
                    setProviders([]);
                    setShowManualSetup(null);
                    setToken("");
                    setDeviceFlow(null);
                  }}
                  style={{
                    paddingHorizontal: 12,
                    paddingVertical: 8,
                    borderRadius: 16,
                    backgroundColor: active ? c.accent : c.bgCard,
                    borderWidth: 1,
                    borderColor: active ? c.accent : c.border,
                  }}
                >
                  <Text style={{ color: active ? "#fff" : c.textPrimary, fontSize: 13, fontWeight: "600" }}>{opt.label}</Text>
                  {opt.sub ? (
                    <Text style={{ color: active ? "#ffffffaa" : c.textMuted, fontSize: 10, marginTop: 1 }}>{opt.sub}</Text>
                  ) : null}
                </Pressable>
              );
            })}
          </ScrollView>
        </View>
      )}

      {/* Privacy notice */}
      <View style={{ backgroundColor: c.accent + "11", borderRadius: 8, padding: 10, marginBottom: 12, borderWidth: 1, borderColor: c.accent + "22" }}>
        <Text style={{ color: c.textSecondary, fontSize: 12, lineHeight: 17 }}>
          Tokens stay on {targetLabel}'s vault and {targetDeviceId ? "are sent over QUIC/relay" : "never leave this device"}. Yaver servers never see your credentials or repo contents.
        </Text>
      </View>

      {/* Device Flow — Sign in with GitHub / GitLab. Each tap opens a
          provider Device Flow on the selected target machine; the agent
          polls until the user approves in any browser. */}
      {!deviceFlow && (
        <View style={{ flexDirection: "row", gap: 8, marginBottom: 10 }}>
          {(["github", "gitlab"] as const).map((p) => (
            <Pressable
              key={`oauth-${p}`}
              onPress={() => handleStartDeviceFlow(p)}
              disabled={deviceFlowStarting !== null}
              style={{
                flex: 1,
                flexDirection: "row",
                alignItems: "center",
                justifyContent: "center",
                gap: 8,
                paddingVertical: 12,
                borderRadius: 10,
                borderWidth: 1,
                borderColor: "#10b98166",
                backgroundColor: "#10b98122",
                opacity: deviceFlowStarting !== null ? 0.5 : 1,
              }}
            >
              {deviceFlowStarting === p ? (
                <ActivityIndicator size="small" color="#10b981" />
              ) : (
                <Text style={{ color: "#10b981", fontSize: 13, fontWeight: "700" }}>
                  Sign in with {p === "github" ? "GitHub" : "GitLab"}
                </Text>
              )}
            </Pressable>
          ))}
        </View>
      )}

      {/* Active Device Flow session card — large code + URL + state */}
      {deviceFlow && (
        <View
          style={{
            marginBottom: 10,
            padding: 14,
            borderRadius: 10,
            borderWidth: 1,
            borderColor:
              deviceFlow.state === "done"
                ? "#10b98166"
                : deviceFlow.state === "error" || deviceFlow.state === "expired"
                  ? "#ef444466"
                  : "#f59e0b66",
            backgroundColor:
              deviceFlow.state === "done"
                ? "#10b98122"
                : deviceFlow.state === "error" || deviceFlow.state === "expired"
                  ? "#ef444422"
                  : "#f59e0b22",
            gap: 10,
          }}
        >
          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
            <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.5 }}>
              {deviceFlow.provider === "github" ? "GitHub" : "GitLab"} · {deviceFlow.state}
              {deviceFlow.byoClient ? " · BYO" : ""}
            </Text>
            <Pressable onPress={() => setDeviceFlow(null)}>
              <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600" }}>Close</Text>
            </Pressable>
          </View>
          {deviceFlow.state === "pending" ? (
            <>
              <Text style={{ color: c.textSecondary, fontSize: 12 }}>Open this URL in any browser:</Text>
              <Pressable onPress={() => Linking.openURL(deviceFlow.verificationUri)}>
                <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>{deviceFlow.verificationUri}</Text>
              </Pressable>
              <Text style={{ color: c.textSecondary, fontSize: 12, marginTop: 4 }}>And enter this code:</Text>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
                <Text
                  selectable
                  style={{
                    color: c.textPrimary,
                    fontSize: 22,
                    fontWeight: "800",
                    letterSpacing: 4,
                    backgroundColor: c.bg,
                    paddingHorizontal: 12,
                    paddingVertical: 8,
                    borderRadius: 8,
                    borderWidth: 1,
                    borderColor: c.border,
                  }}
                >
                  {deviceFlow.userCode}
                </Text>
                <Pressable
                  onPress={async () => {
                    await Clipboard.setStringAsync(deviceFlow.userCode);
                    Alert.alert("Copied", "Code copied to clipboard");
                  }}
                  style={{
                    paddingHorizontal: 12,
                    paddingVertical: 8,
                    borderRadius: 8,
                    borderWidth: 1,
                    borderColor: c.border,
                    backgroundColor: c.bgCard,
                  }}
                >
                  <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "600" }}>Copy</Text>
                </Pressable>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
                Token will land on {targetLabel}'s vault. Polling every {deviceFlow.interval}s.
              </Text>
            </>
          ) : deviceFlow.state === "done" ? (
            <Text style={{ color: c.textPrimary, fontSize: 13 }}>
              ✓ Linked {deviceFlow.provider} as {deviceFlow.username || "user"} on {targetLabel}.
            </Text>
          ) : (
            <Text style={{ color: c.textPrimary, fontSize: 13 }}>
              {deviceFlow.error || `Sign-in didn't complete — the code expired or was denied. Tap Sign in again to retry.`}
            </Text>
          )}
        </View>
      )}

      {/* Connected providers */}
      {providers.map((p) => (
        <View key={p.host} style={{ marginBottom: 10, backgroundColor: c.bgCard, borderRadius: 10, borderWidth: 1, borderColor: c.border, overflow: "hidden" }}>
          <View style={{ flexDirection: "row", alignItems: "center", padding: 12, gap: 10 }}>
            <View style={{ width: 32, height: 32, borderRadius: 16, backgroundColor: p.provider === "github" ? "#24292e" : "#fc6d26", alignItems: "center", justifyContent: "center" }}>
              <Text style={{ color: "#fff", fontSize: 16, fontWeight: "700" }}>{p.provider === "github" ? "G" : "L"}</Text>
            </View>
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>{p.username}</Text>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                {p.host}{p.hasSsh ? " \u00B7 SSH" : " \u00B7 HTTPS"}
              </Text>
            </View>
            <Pressable onPress={() => handleBrowseRepos(p.host)}>
              <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>
                {showRepos === p.host ? "Hide" : "Repos"}
              </Text>
            </Pressable>
            <Pressable onPress={() => {
              // Re-open the same manual-setup form pre-targeted at this
              // provider. POST /git/provider/setup updates in place when
              // the host already exists, so the user can rotate to a
              // new PAT (e.g. one that can see private repos like sfmg)
              // without removing + re-adding from scratch.
              setToken("");
              setShowManualSetup(p.provider as "github" | "gitlab");
            }}>
              <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Update</Text>
            </Pressable>
            <Pressable onPress={() => handleRemove(p.host)}>
              <Text style={{ color: "#ef4444", fontSize: 12, fontWeight: "600" }}>Remove</Text>
            </Pressable>
          </View>

          {/* Repo browser */}
          {showRepos === p.host && (
            <View style={{ borderTopWidth: 1, borderTopColor: c.border }}>
              {/* Search */}
              <View style={{ flexDirection: "row", alignItems: "center", paddingHorizontal: 12, paddingVertical: 8, gap: 8 }}>
                <TextInput
                  style={{ flex: 1, fontSize: 13, color: c.textPrimary, backgroundColor: c.bg, borderRadius: 6, paddingHorizontal: 10, paddingVertical: 6, borderWidth: 1, borderColor: c.border }}
                  placeholder="Search repos..."
                  placeholderTextColor={c.textMuted}
                  value={repoSearch}
                  onChangeText={setRepoSearch}
                  autoCapitalize="none"
                  autoCorrect={false}
                />
              </View>

              {reposLoading ? (
                <View style={{ padding: 16, alignItems: "center" }}><ActivityIndicator color={c.accent} /></View>
              ) : filteredRepos.length === 0 ? (
                <Text style={{ color: c.textMuted, fontSize: 13, padding: 12 }}>No repos found.</Text>
              ) : (
                // Render the full list inline — the outer page already
                // scrolls. A nested ScrollView with maxHeight:300 used
                // to clip the list to ~5 cramped rows; letting it flow
                // gives each repo room to breathe and matches what the
                // user expects from a phone screen.
                <View>
                  {filteredRepos.map((repo: any) => (
                    <Pressable
                      key={repo.fullName}
                      style={{ flexDirection: "row", alignItems: "center", paddingHorizontal: 16, paddingVertical: 16, borderBottomWidth: 1, borderBottomColor: c.border, gap: 12 }}
                      onPress={() => handleClone(repo)}
                      disabled={cloning === repo.fullName}
                    >
                      <View style={{ flex: 1, gap: 6 }}>
                        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                          <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600" }}>{repo.name}</Text>
                          {repo.private && (
                            <View style={{ backgroundColor: "#f59e0b22", borderRadius: 4, paddingHorizontal: 6, paddingVertical: 2 }}>
                              <Text style={{ color: "#f59e0b", fontSize: 11, fontWeight: "600" }}>private</Text>
                            </View>
                          )}
                          {repo.language && (
                            <Text style={{ color: c.textMuted, fontSize: 12 }}>{repo.language}</Text>
                          )}
                        </View>
                        {repo.description ? (
                          <Text style={{ color: c.textMuted, fontSize: 13, lineHeight: 18 }} numberOfLines={2}>{repo.description}</Text>
                        ) : null}
                      </View>
                      {cloning === repo.fullName ? (
                        <ActivityIndicator size="small" color={c.accent} />
                      ) : (
                        <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>Clone</Text>
                      )}
                    </Pressable>
                  ))}
                </View>
              )}
            </View>
          )}
        </View>
      ))}

      {/* Auto-detect button */}
      {providers.length === 0 && (
        <Pressable
          style={{ flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 8, backgroundColor: c.accent, borderRadius: 10, paddingVertical: 12, marginBottom: 8, opacity: detecting ? 0.5 : 1 }}
          onPress={handleAutoDetect}
          disabled={detecting}
        >
          {detecting ? (
            <ActivityIndicator size="small" color="#fff" />
          ) : (
            <Text style={{ color: "#fff", fontSize: 13, fontWeight: "600" }}>Detect from Dev Machine</Text>
          )}
        </Pressable>
      )}

      {/* Manual token entry — also used to UPDATE an existing token */}
      {showManualSetup && (() => {
        const isUpdate = providers.some(p => p.provider === showManualSetup);
        const titleVerb = isUpdate ? "Update" : "Add";
        return (
        <View style={{ marginTop: 8, backgroundColor: c.bgCard, borderRadius: 10, borderWidth: 1, borderColor: c.border, padding: 14, gap: 10 }}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>
            {titleVerb} {showManualSetup === "github" ? "GitHub" : "GitLab"} Token
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 17 }}>
            {showManualSetup === "github"
              ? "Create a token at github.com/settings/tokens. For private repos: classic PAT with 'repo' scope, OR fine-grained with Contents+Metadata: Read on All repositories."
              : "Create a token at gitlab.com/-/user_settings/personal_access_tokens with 'api' scope."}
          </Text>
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="Personal Access Token"
            placeholderTextColor={c.textMuted}
            value={token}
            onChangeText={setToken}
            autoCapitalize="none"
            autoCorrect={false}
            secureTextEntry
          />
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.accent, flex: 1, opacity: (!token.trim() || detecting) ? 0.4 : 1 }]}
              onPress={() => handleManualSetup(showManualSetup)}
              disabled={!token.trim() || detecting}
            >
              {detecting ? <ActivityIndicator size="small" color="#fff" /> : <Text style={[s.actionBtnText, { color: "#fff" }]}>{titleVerb === "Update" ? "Save" : "Connect"}</Text>}
            </Pressable>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, flex: 1 }]}
              onPress={() => { setShowManualSetup(null); setToken(""); }}
            >
              <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Cancel</Text>
            </Pressable>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 10, textAlign: "center" }}>
            Stored on {targetLabel}'s vault. Never sent to Yaver servers.
          </Text>
        </View>
        );
      })()}
    </View>
  );
}

// ── Main Screen ────────────────────────────────────────────────────

export default function MoreScreen() {
  const LEAN_MORE_SURFACE = true;
  const c = useColors();
  const router = useRouter();
  // "regular" caps at 720pt, which on a 1340pt landscape tablet
  // leaves ~310pt of empty whitespace each side — the More tab read
  // as a phone strip floating in a void. "wide" (960pt) keeps the
  // reading line bounded but uses the canvas. Phone behavior is
  // unaffected (the hook returns {} on phones).
  const tabletContent = useTabletContentStyle("wide");
  const { connectionStatus, activeDevice, refreshDevices } = useDevice();
  const { token, user, refreshUser } = useAuth();
  const connected = connectionStatus === "connected";
  const [moreOptionalTools, setMoreOptionalTools] = useState<OptionalMoreToolId[]>([]);
  // Owner-only experimental hardware cells stay hidden for non-owners even if
  // the optional-tool preference is on, matching the daemon gate
  // (mcp_owner_gate.go) and the web dashboard gate.
  const isOwner = user?.isOwner === true;
  const showOptionalTool = useCallback(
    (id: OptionalMoreToolId) => {
      if (!isOwner && OWNER_ONLY_MORE_TOOLS.has(id)) return false;
      return isOptionalMoreToolEnabled(moreOptionalTools, id);
    },
    [moreOptionalTools, isOwner],
  );

  // Tutorials lives in its own pushed screen now (mobile/app/(tabs)/tutorials.tsx)
  // so the open animation matches Quality Gates and every other More-tab
  // destination instead of sliding up as a bottom sheet. See `handleTutorials`
  // below — was setShowTutorials(true) into a Modal block, now it's a route push.

  // Pair device modal state
  const [showPair, setShowPair] = useState(false);
  const [pairCode, setPairCode] = useState("");
  const [pairUrl, setPairUrl] = useState("");
  const [pairBusy, setPairBusy] = useState(false);
  const [pairError, setPairError] = useState<string | null>(null);
  const [pairSuccess, setPairSuccess] = useState<string | null>(null);
  const [pairScannerOpen, setPairScannerOpen] = useState(false);
  const [pairManualOpen, setPairManualOpen] = useState(false);
  const [pairHost, setPairHost] = useState("");
  const [pairExpired, setPairExpired] = useState(false);
  const [pairReturnTarget, setPairReturnTarget] = useState("");
  const [bootstrapDevices, setBootstrapDevices] = useState<DiscoveredDevice[]>([]);

  // Expandable section state
  const [expandedSection, setExpandedSection] = useState<string | null>(null);

  const insets = useSafeAreaInsets();
  const layout = useResponsiveLayout();
  const handleDevices = useCallback(() => router.navigate("/(tabs)/devices" as any), [router]);
  const handleConnection = useCallback(() => router.navigate("/connection" as any), [router]);
  const handleMcpServers = useCallback(() => router.navigate("/mcp-servers" as any), [router]);
  const handleRobot = useCallback(() => router.navigate("/(tabs)/robot" as any), [router]);
  const handlePrinter = useCallback(() => router.navigate("/printer" as any), [router]);
  const handleCircuit = useCallback(() => router.navigate("/circuit" as any), [router]);
  const handleEvStations = useCallback(() => router.navigate("/ev-stations" as any), [router]);
  const handleCarVoice = useCallback(() => router.navigate("/car-voice-coding" as any), [router]);
  const handleVibe = useCallback(() => router.navigate("/vibe" as any), [router]);
  const handleVibeStudio = useCallback(() => router.navigate("/vibe-studio" as any), [router]);
  const handleDataCollection = useCallback(() => router.navigate("/data-collection" as any), [router]);
  const handleTwinMode = useCallback(() => router.navigate("/twin" as any), [router]);
  const handleScrewCell = useCallback(() => router.navigate("/screw-cell" as any), [router]);
  const handlePackages = useCallback(() => router.navigate("/packages" as any), [router]);
  const handleSettings = useCallback(() => router.navigate("/(tabs)/settings" as any), [router]);
  const handleTutorials = useCallback(() => router.navigate("/(tabs)/tutorials" as any), [router]);

  // Read ?pair=<url> on mount/route-change so a deep-linked pair URL
  // (handled at the root in _layout.tsx) opens this tab pre-filled.
  // The search-param contains the full canonical pair URL; we parse
  // it and apply it via the same applyPairUrl path used by paste.
  // Never auto-submits — the user always taps the explicit Pair button.
  const search = useLocalSearchParams<{ pair?: string; openPair?: string; returnTo?: string }>();
  const pairParam = typeof search.pair === "string" ? search.pair : "";
  const openPairParam = search.openPair === "1";
  const pairReturnTo = typeof search.returnTo === "string" ? search.returnTo : "";

  const refreshMoreOptionalTools = useCallback(() => {
    if (!token) {
      setMoreOptionalTools([]);
      return;
    }
    getUserSettings(token)
      .then((settings) => {
        setMoreOptionalTools(normalizeOptionalMoreTools(settings.moreOptionalTools));
      })
      .catch(() => {
        // Settings unreadable (offline / expired session) — keep the current
        // menu rather than blanking it. Relay credentials are handled in
        // DeviceContext, which surfaces the auth failure properly.
      });
  }, [token]);

  useEffect(() => {
    refreshMoreOptionalTools();
  }, [refreshMoreOptionalTools]);

  useFocusEffect(
    useCallback(() => {
      refreshMoreOptionalTools();
      // Owner entitlement is server-computed and can change while the app is
      // installed (allowlist repair, linked-account merge). Refresh it when
      // More becomes visible so Dogfood appears without a logout/reinstall.
      void refreshUser();
    }, [refreshMoreOptionalTools, refreshUser]),
  );

  const openPair = useCallback(() => {
    setPairReturnTarget("");
    setPairCode("");
    setPairError(null);
    setPairSuccess(null);
    setPairScannerOpen(false);
    setPairManualOpen(false);
    setPairHost("");
    setPairExpired(false);
    // Pre-fill with the currently active device's URL so that
    // re-pairing a known machine is one-tap. For a brand-new
    // headless box this will be empty — user types it in.
    if (activeDevice?.host && activeDevice?.port) {
      setPairUrl(`http://${urlHost(activeDevice.host)}:${activeDevice.port}`);
    } else {
      setPairUrl("");
    }
    // Seed bootstrap devices immediately so a box already on the
    // LAN shows up as a pickable row the instant the modal opens.
    setBootstrapDevices(beaconListener.getBootstrapDevices());
    setShowPair(true);
  }, [activeDevice]);

  // While the Pair modal is open, refresh the list of needs-auth
  // devices every 2 seconds. Beacons come in every 3s so two
  // polls are enough to catch a fresh box without UI jitter.
  useEffect(() => {
    if (!showPair) return;
    const iv = setInterval(() => {
      setBootstrapDevices(beaconListener.getBootstrapDevices());
    }, 2000);
    return () => clearInterval(iv);
  }, [showPair]);

  const pickBootstrapDevice = useCallback((dev: DiscoveredDevice) => {
    setPairError(null);
    setPairSuccess(null);
    setPairExpired(false);
    setPairHost(dev.name || dev.deviceId);
    setPairUrl(`http://${urlHost(dev.ip)}:${dev.port}`);
    if (dev.bootstrapPasskey) {
      setPairCode(dev.bootstrapPasskey);
    }
  }, []);

  // applyPairUrl handles a pasted canonical pair URL
  // (https://yaver.io/pair?sid=…&target=…&code=…). Splits it into the
  // existing passkey + target fields so the user still hits the same
  // explicit "Pair" button — never auto-submits a token from a paste.
  // Returns true when the input was recognised, so the input handler
  // can short-circuit instead of treating the URL as raw text.
  const applyPairUrl = useCallback((raw: string): boolean => {
    const payload = parsePairUrl(raw);
    if (!payload) return false;
    const expired = payload.exp != null && payload.exp * 1000 <= Date.now();
    const target = payload.target?.trim() ?? "";
    setPairExpired(expired);
    setPairHost(payload.host ?? "");
    if (payload.code || (payload.sid && payload.sid.length <= 6)) {
      // sid==code in Slice A; keep the field correct in case the
      // URL omitted the explicit code= parameter.
      setPairCode((payload.code || payload.sid).toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 6));
    } else {
      setPairCode("");
    }
    // Never retain a previous machine's URL when this QR omits one.
    // A stale target plus a fresh passkey is precisely the kind of mixed
    // state that can send a credential to the wrong listener.
    setPairUrl(target);
    setPairError(
      expired
        ? "This pairing QR has expired. Run `yaver auth pair` again to print a fresh one."
        : !target
          ? "This pairing link has no reachable machine address. Enter the machine URL below."
          : null,
    );
    setPairSuccess(null);
    return true;
  }, []);

  const handlePairQrScanned = useCallback((raw: string) => {
    const payload = parsePairUrl(raw);
    if (!payload || !applyPairUrl(raw)) {
      setPairError("That QR is not a Yaver pairing link.");
      return;
    }
    setPairScannerOpen(false);
    setPairManualOpen(!payload.target);
  }, [applyPairUrl]);

  // When the global Linking handler routes a pair URL into this tab
  // via ?pair=, open the pair modal and apply the URL once. The
  // router.setParams clear avoids re-opening on re-render.
  useEffect(() => {
    if (!pairParam) return;
    const payload = parsePairUrl(pairParam);
    if (payload && applyPairUrl(pairParam)) {
      setShowPair(true);
      setPairScannerOpen(false);
      setPairManualOpen(!payload.target);
      // Clear the param so navigating away + back doesn't re-trigger.
      router.setParams({ pair: undefined });
    }
  }, [pairParam, applyPairUrl, router]);

  // Focused setup routes (including Develop Yaver) reuse the canonical pair
  // sheet instead of growing another device-enrollment implementation.
  useEffect(() => {
    if (!openPairParam) return;
    openPair();
    setPairReturnTarget(pairReturnTo);
    router.setParams({ openPair: undefined, returnTo: undefined });
  }, [openPair, openPairParam, pairReturnTo, router]);

  const handlePairSubmit = useCallback(async () => {
    if (!token) {
      setPairError("Sign in on this phone first");
      return;
    }
    setPairBusy(true);
    setPairError(null);
    setPairSuccess(null);
    try {
      // First confirm the target is actually listening for a
      // pairing — avoids leaking the token to the wrong URL if
      // the user mistyped the host.
      const info = await fetchPairInfo(pairUrl);
      if (!info.ok) {
        setPairError(info.error ?? "Target is not in pairing mode");
        return;
      }
      const res = await submitPair({
        code: pairCode,
        targetUrl: pairUrl,
        token,
        userId: user?.id,
      });
      if (!res.ok) {
        setPairError(res.error ?? "Pairing failed");
        return;
      }
      setPairSuccess(`Paired with ${res.host ?? info.host ?? "target"}`);
      await refreshDevices().catch(() => {});
      if (pairReturnTarget === "dogfood") {
        setShowPair(false);
        setPairReturnTarget("");
        router.navigate("/(tabs)/dogfood" as any);
      }
    } finally {
      setPairBusy(false);
    }
  }, [pairCode, pairReturnTarget, pairUrl, refreshDevices, router, token, user]);

  const pairReady = pairCode.length === 6 && !!pairUrl.trim() && !pairExpired;

  const toggleSection = useCallback((section: string) => {
    setExpandedSection((prev) => (prev === section ? null : section));
  }, []);

  return (
    <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <ScrollView contentContainerStyle={[s.list, tabletContent]}>
        {/* No big page-title block here on purpose — every other tab
            (Tasks, Devices, Projects, Hot Reload) relies solely on the
            navigator header at the top of the screen for its title.
            The previous duplicate "More" + subtitle was inconsistent
            with that pattern. Keep the screen content starting with
            the hero card. */}

        <Pressable
          style={[
            s.heroCard,
            {
              backgroundColor: c.bgCard,
              borderColor: c.border,
              shadowColor: c.accent,
            },
          ]}
          onPress={() => router.navigate("/(tabs)/newproject" as any)}
        >
          <View style={[s.heroIconWrap, { backgroundColor: c.accent + "18", borderColor: c.accent + "35" }]}>
            <Text style={[s.heroIcon, { color: c.accent }]}>{"\u26A1"}</Text>
          </View>
          <View style={{ flex: 1 }}>
            <Text style={[s.eyebrow, { color: c.accent }]}>Start here</Text>
            <Text style={[s.heroLabel, { color: c.textPrimary }]}>Start Project</Text>
            <Text style={[s.heroDesc, { color: c.textMuted }]} numberOfLines={2}>
              Describe it; Yaver creates the RN/TS app, Serverless backend, git, and runner task.
            </Text>
          </View>
          <Text style={{ color: c.accent, fontSize: 20, fontWeight: "700" }}>{"\u203A"}</Text>
        </Pressable>

        <View style={s.quickGrid}>
          <Pressable
            style={[s.quickCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={openPair}
          >
            <Text style={[s.quickIcon, { color: c.textMuted }]}>{"\u2194"}</Text>
            <Text style={[s.quickLabel, { color: c.textPrimary }]}>Pair Machine</Text>
            <Text style={[s.quickDesc, { color: c.textMuted }]} numberOfLines={2}>Scan QR or enter code</Text>
          </Pressable>

          <Pressable
            style={[s.quickCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={handleDevices}
          >
            <Text style={[s.quickIcon, { color: c.textMuted }]}>{"\u25CF"}</Text>
            <Text style={[s.quickLabel, { color: c.textPrimary }]}>Devices</Text>
            <Text style={[s.quickDesc, { color: c.textMuted }]} numberOfLines={2}>
              Manage remote boxes
            </Text>
          </Pressable>
        </View>

        <Pressable
          style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
          onPress={handleConnection}
        >
          <Text style={[s.icon, { color: c.textMuted }]}>{"🌐"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>Connection &amp; Network</Text>
            <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Phone and runner diagnostics</Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
        </Pressable>

        {/* One native Dogfood destination owns launch/reload/exit, tasks, and settings. */}
        <Pressable
          style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
          onPress={() => router.navigate("/(tabs)/dogfood" as any)}
          accessibilityRole="button"
          accessibilityLabel="Develop Yaver with Yaver"
        >
          <Text style={[s.icon, { color: c.textMuted }]}>{"🐕"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>Dogfood</Text>
            <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Launch, reload, tasks, and settings</Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"›"}</Text>
        </Pressable>

        <Pressable
          style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
          onPress={handleSettings}
        >
          <Text style={[s.icon, { color: c.textMuted }]}>{"\u2699\ufe0f"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>Settings</Text>
            <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Preferences</Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
        </Pressable>

        {!LEAN_MORE_SURFACE ? (
          <>
        <Pressable
          style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
          onPress={handleConnection}
        >
          <Text style={[s.icon, { color: c.textMuted }]}>{"🌐"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>Connection / Network</Text>
            <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
              Internet, DNS, IP, WiFi, LAN {"·"} phone + runner diagnostics
            </Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"›"}</Text>
        </Pressable>

        <Pressable
          style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
          onPress={handleMcpServers}
        >
          <Text style={[s.icon, { color: c.textMuted }]}>{"🔌"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>MCP Servers</Text>
            <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
              Add your own or others' MCP servers {"·"} use their tools from Yaver
            </Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"›"}</Text>
        </Pressable>

        {/* A row in a list of rows, not a full-height hero.
            
            This was a centred EmptyState with an icon, a paragraph and two
            buttons, wedged between MCP Servers and the rest of the list — it broke the
            rhythm of the card list around it and pushed everything below the
            fold (2026-07-20). Pairing is one action; it gets one card, in the
            same shape as every other card here.

            The old phone-local project link is gone: new app development is
            remote-box-first, so offering local execution as a co-equal first
            move contradicts the product path. The route still exists for
            anything already using it. */}
        {!connected ? (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={openPair}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"🖥️"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Pair a machine</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                No machine connected {"·"} remote coding, builds, infra tools
              </Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"›"}</Text>
          </Pressable>
        ) : null}

        {LEAN_MORE_SURFACE ? (
          <>
            {connected ? (
              <Pressable
                style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
                onPress={handleDevices}
              >
                <Text style={[s.icon, { color: c.textMuted }]}>{"\u25CF"}</Text>
                <View style={{ flex: 1 }}>
                  <Text style={[s.label, { color: c.textPrimary }]}>Devices</Text>
                  <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                    Manage your remote boxes and pairing
                  </Text>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
              </Pressable>
            ) : null}

            {showOptionalTool("robot-cell") ? (
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handleRobot}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\ud83e\udd16"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Robot Cell</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"Jog the Ender-3 screwdriver robot \u2014 camera-validated moves"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
            </Pressable>
            ) : null}

            {showOptionalTool("printer") ? (
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handlePrinter}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\ud83d\udda8\ufe0f"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>3D Printer</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"Bambu Lab \u2014 discover, watch the chamber, control + remote CAD"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
            </Pressable>
            ) : null}

            {showOptionalTool("circuit") ? (
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handleCircuit}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\u26a1"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Circuit Simulator</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"SPICE/KiCad/EPLAN \u2014 simulate, ERC, view waveforms"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
            </Pressable>
            ) : null}

            {showOptionalTool("ev-stations") ? (
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handleEvStations}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\ud83d\udd0c"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>EV Stations</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"Find chargers nearby \u2014 CCS2 default, filter by network + power"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
            </Pressable>
            ) : null}

            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handleVibe}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"🎤"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Vibe</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"Talk to build — say “load me the app with Hermes” and keep vibing"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"›"}</Text>
            </Pressable>

            {/* Tablet Vibe Studio — landscape preview-LEFT / chat-RIGHT split.
                Only shown on tablets so the phone More menu stays untouched. */}
            {layout.isTablet ? (
              <Pressable
                style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
                onPress={handleVibeStudio}
              >
                <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F4F1}\u00b7\u{1F4AC}"}</Text>
                <View style={{ flex: 1 }}>
                  <Text style={[s.label, { color: c.textPrimary }]}>Vibe Studio</Text>
                  <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                    {"Split view — app preview beside chat. Landscape for the two-pane cockpit"}
                  </Text>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 16 }}>{"›"}</Text>
              </Pressable>
            ) : null}

            {showOptionalTool("car-voice") ? (
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handleCarVoice}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\ud83d\ude97"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Car Voice Coding</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"Speak a command from the car \u2014 status read back over Bluetooth"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
            </Pressable>
            ) : null}

            {showOptionalTool("data-collection") ? (
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handleDataCollection}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\ud83c\udf10"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Data Collection</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"Multi-vantage egress, source health, cross-vantage diff"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
            </Pressable>
            ) : null}

            {showOptionalTool("twin-mode") ? (
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handleTwinMode}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\u25A3"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Twin Mode</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"Run and record Android redroid and web flows on a remote dev machine"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
            </Pressable>
            ) : null}

            {showOptionalTool("task-packages") ? (
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handlePackages}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\ud83d\udce6"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Task Packages</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"Run and preflight-check portable task packages"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
            </Pressable>
            ) : null}

            {showOptionalTool("screw-cell") ? (
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={handleScrewCell}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\ud83d\udd29"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Screw Cell</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  {"Fail-rate trend, flagged orders, worst blocks, recent runs"}
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203a"}</Text>
            </Pressable>
            ) : null}

            <Pressable style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={handleTutorials}>
              <Text style={[s.icon, { color: c.textMuted }]}>{"\u2302"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Tutorials</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  Setup and remote-box guides
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
            </Pressable>

            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={() => router.navigate("/voice-test" as any)}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\u25CF"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Voice &amp; Speech Test</Text>
                <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>
                  Test STT + TTS {"\u2014"} on-device or your API key
                </Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
            </Pressable>
          </>
        ) : (
          <>
        {connected ? (
          <Pressable style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={handleTutorials}>
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2302"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Tutorials</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Setup and guides</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        ) : null}

        {/* Quality Gates — navigate to dedicated screen */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/qualitygates" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2714"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Quality Gates</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Tests and checks</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Local CI (yaver-test-sdk) */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/runs" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u25B6"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Local CI</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Run local test jobs</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Builds — artifact history + downloads */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/builds" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F4E6}"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Builds</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Artifacts and installs</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Publish — build + ship binaries to the App Store / Play */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/publish" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F680}"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Publish</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Ship to App Store / Play</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"›"}</Text>
          </Pressable>
        )}

        {/* App Store screenshots — auto-capture + submit for review */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/shots" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F4F8}"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>App Store Screenshots</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Auto-capture + submit for review</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"›"}</Text>
          </Pressable>
        )}

        {/* Monitor — errors + releases + uptime + events + flags */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/monitor" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2261"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Monitor</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Errors, uptime, releases</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Home — AWS-style overview dashboard */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/home" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\uD83C\uDFE0"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Home</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Overview</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Infra — managed machine workspace */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/infra" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\uD83D\uDEE0\uFE0F"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Infra</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Machine health and services</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Ops — deploy, backups, domains, uptime, secret rotate */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/ops" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\uD83D\uDE80"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Ops</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Deploy and backups</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        <Pressable
          style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
          onPress={() => router.navigate("/ev-charging" as any)}
        >
          <Text style={[s.icon, { color: c.textMuted }]}>{"\u26A1"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>EV Charging</Text>
            <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Scan QR, launch provider app</Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
        </Pressable>

        {/* Data browser — tables, query, schema, storage, jobs */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/data" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\uD83D\uDDC4\uFE0F"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Data</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Tables and queries</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Console — machines, containers, catalog */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/console" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\uD83D\uDCBB"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Console</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Machines and containers</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Terminal — native shell over WebSocket PTY */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/terminal" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2328\uFE0F"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Terminal</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Remote shell</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Health Monitor — navigate to dedicated screen */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/healthmon" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2661"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Health Monitor</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Check production URLs</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Schedules — cron / runAt / interval tasks on the agent */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/schedules" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u23F0"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Schedules</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Cron and timed jobs</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Accounts — cloud-provider credential vault on the host */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/accounts" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2601"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Accounts</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Cloud and payment accounts</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Storage — unified files + shared-storage + blobs */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/storage" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F4C2}"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Storage</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Project files and blobs</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Files (classic) — rich preview, kept for back-compat */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/files" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u25A1"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Files (classic)</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Classic file browser</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/shared-storage" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F5C4}"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Shared Storage</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>NAS and shared drives</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Vault — encrypted secrets stored on host (AES-GCM + Argon2id). */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/vault" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F512}"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Vault</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Encrypted secrets</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* API keys — labeled SDK tokens with usage tracking */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/apikeys" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F511}"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>API Keys</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>SDK tokens</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* New Project — fullstack wizard (web + mobile + backend + DNS + OAuth) */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/newproject" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2728"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>New Project</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Create a fullstack app</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/designmode" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u25A7"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Design Mode</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Import Figma and send to vibing</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Solo Stack — Forms + Newsletter + Job queue in one place */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/solostack" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2630"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Solo Stack</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Forms and jobs</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Studio — Clips, Chat, Invoices, Affiliates, A/B, Casts */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/studio" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u25CE"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Studio</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Clips, chat, invoices</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Mail — Gmail / O365 triage + AI-boosted replies */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/mail" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2709"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Mail</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Inbox and drafts</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        {/* Git Providers — dedicated screen for consistency */}
        {connected && (
          <Pressable
            style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => router.navigate("/(tabs)/gitproviders" as any)}
          >
            <Text style={[s.icon, { color: c.textMuted }]}>{"\u2387"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={[s.label, { color: c.textPrimary }]}>Git Providers</Text>
              <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Repos and clones</Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
          </Pressable>
        )}

        <Pressable style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={handleSettings}>
          <Text style={[s.icon, { color: c.textMuted }]}>{"\u2699"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>Settings</Text>
            <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>Theme and preferences</Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
        </Pressable>
          </>
        )}
          </>
        ) : null}
      </ScrollView>

      {/* Pair device modal */}
      <Modal
        visible={showPair}
        animationType="slide"
        transparent={!pairScannerOpen}
        onRequestClose={() => pairScannerOpen ? setPairScannerOpen(false) : setShowPair(false)}
      >
        {pairScannerOpen ? (
          <PairQrScanner onScanned={handlePairQrScanned} onClose={() => setPairScannerOpen(false)} />
        ) : (
          <KeyboardAvoidingView
            style={{ flex: 1 }}
            behavior={Platform.OS === "ios" ? "padding" : "height"}
          >
            <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.62)", justifyContent: "flex-end" }}>
              <View style={{ backgroundColor: c.bg, borderTopLeftRadius: 22, borderTopRightRadius: 22, maxHeight: "90%", overflow: "hidden" }}>
                <ScrollView
                  testID="pair-device-sheet"
                  keyboardShouldPersistTaps="handled"
                  keyboardDismissMode="interactive"
                  automaticallyAdjustKeyboardInsets
                  contentContainerStyle={{ padding: 20, paddingBottom: insets.bottom + 24 }}
                >
                  <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 18 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700" }}>Pair a machine</Text>
                    <Pressable onPress={() => setShowPair(false)} hitSlop={8} accessibilityRole="button" accessibilityLabel="Close pairing">
                      <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Close</Text>
                    </Pressable>
                  </View>

                  <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "700", letterSpacing: 0.7, textTransform: "uppercase" }}>On the machine</Text>
                  <View style={[s.pairCommand, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                    <View style={{ flex: 1, gap: 4 }}>
                      <Text selectable style={{ color: c.textPrimary, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 16, fontWeight: "700" }}>yaver auth pair</Text>
                      <Text style={{ color: c.textMuted, fontSize: 12 }}>Prints a short-lived QR and 6-character code</Text>
                    </View>
                    <Pressable
                      onPress={() => void Clipboard.setStringAsync("yaver auth pair")}
                      style={[s.pairCopyBtn, { borderColor: c.border }]}
                      accessibilityRole="button"
                      accessibilityLabel="Copy yaver auth pair command"
                    >
                      <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>Copy</Text>
                    </Pressable>
                  </View>

                  <Pressable
                    testID="pair-scan-qr"
                    onPress={() => setPairScannerOpen(true)}
                    style={({ pressed }) => [s.pairPrimaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
                    accessibilityRole="button"
                  >
                    <Text style={s.pairPrimaryIcon}>▣</Text>
                    <Text style={s.pairPrimaryText}>Scan QR</Text>
                  </Pressable>
                  <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 17, textAlign: "center", marginTop: 8 }}>
                    Scan it here, review the machine, then confirm pairing.
                  </Text>

                  {pairReady && !pairManualOpen && (
                    <View style={[s.pairReadyCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                      <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.6 }}>Ready to pair</Text>
                      <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700", marginTop: 4 }}>{pairHost || "Yaver machine"}</Text>
                      <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }} numberOfLines={1}>{pairUrl}</Text>
                      <Pressable
                        testID="pair-confirm"
                        onPress={handlePairSubmit}
                        disabled={pairBusy}
                        style={[s.pairConfirmBtn, { backgroundColor: c.accent, opacity: pairBusy ? 0.6 : 1 }]}
                      >
                        {pairBusy ? <ActivityIndicator color="#fff" /> : <Text style={s.pairPrimaryText}>Pair with this machine</Text>}
                      </Pressable>
                    </View>
                  )}

                  {bootstrapDevices.length > 0 && (
                    <View style={{ marginTop: 20 }}>
                      <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "700", marginBottom: 8 }}>
                        Nearby machines ({bootstrapDevices.length})
                      </Text>
                      {bootstrapDevices.map((d) => (
                        <Pressable
                          key={d.deviceId}
                          onPress={() => pickBootstrapDevice(d)}
                          style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border, marginBottom: 6 }]}
                        >
                          <Text style={[s.icon, { color: c.accent }]}>{"\u25CF"}</Text>
                          <View style={{ flex: 1 }}>
                            <Text style={[s.label, { color: c.textPrimary }]}>{d.name || d.deviceId}</Text>
                            <Text style={[s.desc, { color: c.textMuted }]} numberOfLines={1}>{d.ip}:{d.port} · ready to pair</Text>
                          </View>
                          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
                        </Pressable>
                      ))}
                    </View>
                  )}

                  <Pressable
                    testID="pair-manual-toggle"
                    onPress={() => setPairManualOpen((open) => !open)}
                    style={[s.pairSecondaryBtn, { borderColor: c.border, backgroundColor: c.bgCard }]}
                    accessibilityRole="button"
                    accessibilityState={{ expanded: pairManualOpen }}
                  >
                    <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>Enter code or link instead</Text>
                    <Text style={{ color: c.textMuted, fontSize: 18 }}>{pairManualOpen ? "⌃" : "⌄"}</Text>
                  </Pressable>

                  {pairManualOpen && (
                    <View testID="pair-manual-fields" style={{ marginTop: 12 }}>
                      <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 6 }}>6-character pairing code</Text>
                      <SegmentedCodeInput
                        testID="pair-segmented-code"
                        value={pairCode}
                        onChangeText={(t) => {
                          setPairCode(t);
                          setPairExpired(false);
                          setPairHost("");
                          setPairError(null);
                          setPairSuccess(null);
                        }}
                        length={6}
                        groupEvery={3}
                        accessibilityLabel="Six-character machine pairing code"
                      />

                      <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 6, marginTop: 14 }}>Machine URL or full yaver.io/pair link</Text>
                      <TextInput
                        value={pairUrl}
                        onChangeText={(t) => {
                          if (applyPairUrl(t)) return;
                          setPairUrl(t);
                          setPairExpired(false);
                          setPairHost("");
                          setPairError(null);
                          setPairSuccess(null);
                        }}
                        placeholder="http://192.168.1.20:18080"
                        placeholderTextColor={c.textMuted}
                        autoCapitalize="none"
                        autoCorrect={false}
                        spellCheck={false}
                        keyboardType="url"
                        returnKeyType="done"
                        style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgCard }]}
                      />
                      <Pressable
                        testID="pair-manual-confirm"
                        onPress={handlePairSubmit}
                        disabled={pairBusy || !pairReady}
                        style={[s.pairConfirmBtn, { backgroundColor: pairReady ? c.accent : c.bgCard, opacity: pairBusy ? 0.6 : 1 }]}
                      >
                        {pairBusy ? (
                          <ActivityIndicator color="#fff" />
                        ) : (
                          <Text style={[s.pairPrimaryText, { color: pairReady ? "#fff" : c.textMuted }]}>Pair with this machine</Text>
                        )}
                      </Pressable>
                    </View>
                  )}

                  {pairError && <Text accessibilityRole="alert" style={{ color: "#ef4444", fontSize: 13, lineHeight: 18, marginTop: 12 }}>{pairError}</Text>}
                  {pairSuccess && <Text accessibilityRole="alert" style={{ color: "#22c55e", fontSize: 13, lineHeight: 18, marginTop: 12 }}>{pairSuccess}</Text>}

                  {pairManualOpen && !pairReady && (
                    <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16, marginTop: 10 }}>
                      The URL is only needed for manual entry. A scanned QR already includes the best reachable address.
                    </Text>
                  )}
                </ScrollView>
              </View>
            </View>
          </KeyboardAvoidingView>
        )}
      </Modal>

      {/* Tutorials list + WebView were here as Modals; moved to a
          dedicated tab route at app/(tabs)/tutorials.tsx so the open
          animation matches Quality Gates and the rest of the More-tab
          destinations. */}
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  safe: { flex: 1 },
  list: { padding: 16, paddingTop: 12, paddingBottom: 28, gap: 10 },
  heroHeader: {
    marginBottom: 2,
    gap: 4,
  },
  pageTitle: {
    fontSize: 28,
    fontWeight: "700",
    letterSpacing: -0.4,
  },
  pageSubtitle: {
    fontSize: 13,
    lineHeight: 18,
  },
  quickGrid: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 10,
  },
  quickCard: {
    width: "48.5%",
    borderWidth: 1,
    borderRadius: 16,
    paddingHorizontal: 14,
    paddingVertical: 14,
    minHeight: 112,
  },
  quickIcon: {
    fontSize: 18,
    marginBottom: 14,
  },
  quickLabel: {
    fontSize: 15,
    fontWeight: "600",
  },
  quickDesc: {
    fontSize: 12,
    marginTop: 4,
    lineHeight: 16,
  },
  inlineSectionTitle: {
    marginTop: 8,
    marginBottom: 2,
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 0.8,
    textTransform: "uppercase",
  },
  sectionHeader: {
    gap: 3,
    marginTop: 12,
    marginBottom: 4,
  },
  sectionTitle: {
    fontSize: 17,
    fontWeight: "700",
  },
  sectionSubtitle: {
    fontSize: 12,
    lineHeight: 17,
  },
  card: {
    flexDirection: "row",
    alignItems: "center",
    padding: 14,
    borderRadius: 16,
    borderWidth: 1,
    gap: 12,
  },
  heroCard: {
    flexDirection: "row",
    alignItems: "center",
    padding: 18,
    borderRadius: 20,
    borderWidth: 1,
    gap: 14,
    shadowOpacity: 0.14,
    shadowRadius: 16,
    shadowOffset: { width: 0, height: 8 },
    elevation: 4,
    marginBottom: 2,
  },
  heroIconWrap: {
    width: 52,
    height: 52,
    borderRadius: 16,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
  },
  icon: {
    fontSize: 18,
    width: 34,
    height: 34,
    lineHeight: 34,
    textAlign: "center",
    borderRadius: 10,
    overflow: "hidden",
  },
  heroIcon: { fontSize: 24 },
  eyebrow: {
    fontSize: 11,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.6,
    marginBottom: 3,
  },
  heroLabel: { fontSize: 18, fontWeight: "700" },
  heroDesc: { fontSize: 13, marginTop: 4, lineHeight: 18 },
  label: { fontSize: 15, fontWeight: "600" },
  desc: { fontSize: 12, marginTop: 3 },
  modalHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: 1,
  },
  modalTitle: { fontSize: 17, fontWeight: "700" },
  actionBtn: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    alignItems: "center",
    justifyContent: "center",
    minWidth: 60,
  },
  actionBtnText: {
    fontSize: 13,
    fontWeight: "600",
  },
  cardBlock: {
    borderWidth: 1,
    borderRadius: 16,
    padding: 14,
    gap: 0,
  },
  inlineInput: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 9,
    fontSize: 13,
    minHeight: 38,
  },
  appSyncRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    borderTopWidth: StyleSheet.hairlineWidth,
    paddingTop: 8,
    marginTop: 8,
  },
  checkbox: {
    width: 24,
    height: 24,
    borderRadius: 6,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
  },
  resultRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingVertical: 8,
    borderBottomWidth: StyleSheet.hairlineWidth,
    gap: 8,
  },
  outputBox: {
    maxHeight: 200,
    borderWidth: 1,
    borderRadius: 6,
    padding: 8,
    marginVertical: 4,
  },
  pairCommand: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    borderWidth: 1,
    borderRadius: 14,
    padding: 14,
    marginTop: 8,
  },
  pairCopyBtn: {
    borderWidth: 1,
    borderRadius: 9,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  pairPrimaryBtn: {
    minHeight: 54,
    borderRadius: 14,
    marginTop: 18,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 10,
  },
  pairPrimaryIcon: { color: "#fff", fontSize: 19, fontWeight: "700" },
  pairPrimaryText: { color: "#fff", fontSize: 15, fontWeight: "700" },
  pairReadyCard: {
    borderWidth: 1,
    borderRadius: 14,
    padding: 14,
    marginTop: 18,
  },
  pairConfirmBtn: {
    minHeight: 48,
    borderRadius: 12,
    marginTop: 14,
    alignItems: "center",
    justifyContent: "center",
  },
  pairSecondaryBtn: {
    minHeight: 48,
    borderWidth: 1,
    borderRadius: 12,
    marginTop: 18,
    paddingHorizontal: 14,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  textInput: {
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 14,
  },
});
