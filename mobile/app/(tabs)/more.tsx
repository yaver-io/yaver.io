import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { WebView } from "react-native-webview";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";

const TUTORIALS = [
  { label: "Always-on Setup", icon: "\u{1F50C}", desc: "Auto-boot, systemd, run forever", url: "https://yaver.io/manuals/auto-boot" },
  { label: "Self-host Relay", icon: "\u{1F310}", desc: "Your own relay server with Docker", url: "https://yaver.io/manuals/relay-setup" },
  { label: "Local LLM", icon: "\u{1F9E0}", desc: "Ollama, Qwen, zero API keys", url: "https://yaver.io/manuals/local-llm" },
  { label: "Voice AI", icon: "\u{1F3A4}", desc: "PersonaPlex, Whisper, speech-to-code", url: "https://yaver.io/manuals/voice-ai" },
  { label: "Feedback SDK", icon: "\u{1F41B}", desc: "Visual bug reports from your app", url: "https://yaver.io/manuals/feedback-loop" },
  { label: "CLI Setup", icon: "\u2699", desc: "Install, auth, configure agents", url: "https://yaver.io/manuals/cli-setup" },
  { label: "Integrations", icon: "\u{1F517}", desc: "MCP, Claude Desktop, Cursor", url: "https://yaver.io/manuals/integrations" },
];

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

// ── Health Monitor types ───────────────────────────────────────────

interface HealthTarget {
  id: string;
  url: string;
  label?: string;
  status?: string;
  statusCode?: number;
  responseMs?: number;
  uptimePercent?: number;
  lastChecked?: string;
  history?: { status: string; responseMs: number; time: string }[];
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
      setChecks(detectedChecks);
      setResults(existingResults);
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
      Alert.alert("Error", e instanceof Error ? e.message : "Failed to run check");
    }
  }, []);

  const handleRunAll = useCallback(async () => {
    try {
      const available = checks.filter((ch) => ch.available);
      setRunningTypes(new Set(available.map((ch) => ch.type)));
      await quicClient.runAllQualityChecks();
    } catch (e) {
      setRunningTypes(new Set());
      Alert.alert("Error", e instanceof Error ? e.message : "Failed to run checks");
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

function HealthMonitorSection({ c }: { c: ReturnType<typeof useColors> }) {
  const [targets, setTargets] = useState<HealthTarget[]>([]);
  const [loading, setLoading] = useState(true);
  const [addingUrl, setAddingUrl] = useState(false);
  const [newUrl, setNewUrl] = useState("");
  const [newLabel, setNewLabel] = useState("");
  const [expandedTarget, setExpandedTarget] = useState<string | null>(null);
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
      Alert.alert("Error", e instanceof Error ? e.message : "Failed to add target");
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
    try {
      await quicClient.checkHealthTarget(id);
      loadTargets();
    } catch {
      // silent
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
    <View style={{ paddingHorizontal: 14, paddingBottom: 8 }}>
      {/* Add URL button / form */}
      {addingUrl ? (
        <View style={{ marginBottom: 10, gap: 6 }}>
          <TextInput
            style={[s.textInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="https://example.com/health"
            placeholderTextColor={c.textMuted}
            value={newUrl}
            onChangeText={setNewUrl}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
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
          style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, marginBottom: 10 }]}
          onPress={() => setAddingUrl(true)}
        >
          <Text style={[s.actionBtnText, { color: c.textPrimary }]}>+ Add URL</Text>
        </Pressable>
      )}

      {targets.length === 0 && !addingUrl && (
        <Text style={{ color: c.textMuted, fontSize: 13, paddingVertical: 4 }}>
          No health targets configured.
        </Text>
      )}

      {/* Targets list */}
      {targets.map((t) => {
        const isUp = t.status === "up" || t.statusCode === 200;
        const statusDot = isUp ? "\u25CF" : "\u25CF";
        const dotColor = t.status ? (isUp ? "#22c55e" : "#ef4444") : c.textMuted;

        return (
          <View key={t.id}>
            <Pressable
              style={[s.resultRow, { borderBottomColor: c.border }]}
              onPress={() => setExpandedTarget(expandedTarget === t.id ? null : t.id)}
              onLongPress={() => handleRemove(t.id, t.label || t.url)}
            >
              <Text style={{ color: dotColor, fontSize: 14, width: 24 }}>{statusDot}</Text>
              <View style={{ flex: 1 }}>
                <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "500" }} numberOfLines={1}>
                  {t.label || t.url}
                </Text>
                {t.label && (
                  <Text style={{ color: c.textMuted, fontSize: 11 }} numberOfLines={1}>{t.url}</Text>
                )}
              </View>
              {t.responseMs != null && (
                <Text style={{ color: c.textMuted, fontSize: 12 }}>{t.responseMs}ms</Text>
              )}
              {t.uptimePercent != null && (
                <Text style={{ color: c.textMuted, fontSize: 12, marginLeft: 6 }}>
                  {t.uptimePercent.toFixed(1)}%
                </Text>
              )}
              <Text style={{ color: c.textMuted, fontSize: 14, marginLeft: 8 }}>
                {expandedTarget === t.id ? "\u2304" : "\u203A"}
              </Text>
            </Pressable>
            {expandedTarget === t.id && (
              <View style={{ paddingLeft: 24, paddingVertical: 6, gap: 4 }}>
                <Pressable
                  style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, alignSelf: "flex-start" }]}
                  onPress={() => handleCheck(t.id)}
                >
                  <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Check Now</Text>
                </Pressable>
                {t.history && t.history.length > 0 && (
                  <View style={{ marginTop: 4 }}>
                    {t.history.slice(0, 10).map((h, i) => (
                      <View key={i} style={{ flexDirection: "row", gap: 8, paddingVertical: 2 }}>
                        <Text style={{ color: h.status === "up" ? "#22c55e" : "#ef4444", fontSize: 11, width: 16 }}>
                          {h.status === "up" ? "\u25CF" : "\u25CF"}
                        </Text>
                        <Text style={{ color: c.textMuted, fontSize: 11 }}>{h.responseMs}ms</Text>
                        <Text style={{ color: c.textMuted, fontSize: 11 }}>{h.time}</Text>
                      </View>
                    ))}
                  </View>
                )}
                <Pressable onPress={() => handleRemove(t.id, t.label || t.url)}>
                  <Text style={{ color: "#ef4444", fontSize: 13, marginTop: 4 }}>Remove</Text>
                </Pressable>
              </View>
            )}
          </View>
        );
      })}
    </View>
  );
}

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
      Alert.alert("Error", e instanceof Error ? e.message : `Failed: ${label}`);
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
      Alert.alert("Error", e instanceof Error ? e.message : "Failed to save credential");
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
            Alert.alert("Error", e instanceof Error ? e.message : "Failed to remove");
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

function GitProviderSection({ c }: { c: ReturnType<typeof useColors> }) {
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

  const loadProviders = useCallback(async () => {
    try {
      const baseUrl = (quicClient as any).baseUrl;
      const headers = (quicClient as any).authHeaders;
      const res = await fetch(`${baseUrl}/git/provider/status`, { headers });
      const data = await res.json();
      if (data.ok) setProviders(data.providers || []);
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { loadProviders(); }, [loadProviders]);

  // Auto-detect: ask the CLI to find tokens from gh/glab CLI, env vars, etc.
  const handleAutoDetect = useCallback(async () => {
    setDetecting(true);
    try {
      const baseUrl = (quicClient as any).baseUrl;
      const headers = (quicClient as any).authHeaders;
      const res = await fetch(`${baseUrl}/git/provider/detect`, { headers });
      const data = await res.json();
      if (data.ok && data.providers?.length > 0) {
        await loadProviders();
        const names = data.providers.map((p: any) => `${p.provider}: ${p.username}`).join("\n");
        Alert.alert("Found", `Detected from your dev machine:\n${names}`);
      } else {
        Alert.alert(
          "No credentials found",
          "Your dev machine doesn't have gh CLI or GitLab CLI logged in.\n\nInstall gh CLI and run 'gh auth login', or enter a token manually below.",
          [
            { text: "OK" },
            { text: "Enter GitHub token", onPress: () => setShowManualSetup("github") },
            { text: "Enter GitLab token", onPress: () => setShowManualSetup("gitlab") },
          ],
        );
      }
    } catch (e) {
      Alert.alert("Error", e instanceof Error ? e.message : "Detection failed");
    } finally {
      setDetecting(false);
    }
  }, [loadProviders]);

  // Manual token entry (fallback when auto-detect fails)
  const handleManualSetup = useCallback(async (provider: "github" | "gitlab") => {
    if (!token.trim()) return;
    setDetecting(true);
    try {
      const baseUrl = (quicClient as any).baseUrl;
      const headers = { ...(quicClient as any).authHeaders, "Content-Type": "application/json" };
      const res = await fetch(`${baseUrl}/git/provider/setup`, {
        method: "POST", headers,
        body: JSON.stringify({ provider, token: token.trim() }),
      });
      const data = await res.json();
      if (data.ok) {
        Alert.alert("Connected", `Signed in as ${data.username}`);
        setToken("");
        setShowManualSetup(null);
        await loadProviders();
      } else {
        Alert.alert("Error", data.error || "Setup failed");
      }
    } catch (e) {
      Alert.alert("Error", e instanceof Error ? e.message : "Setup failed");
    } finally {
      setDetecting(false);
    }
  }, [token, loadProviders]);

  const handleRemove = useCallback((providerHost: string) => {
    Alert.alert("Disconnect", `Remove ${providerHost}?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Remove", style: "destructive", onPress: async () => {
          try {
            const baseUrl = (quicClient as any).baseUrl;
            const headers = (quicClient as any).authHeaders;
            await fetch(`${baseUrl}/git/provider/${encodeURIComponent(providerHost)}`, {
              method: "DELETE", headers,
            });
            await loadProviders();
          } catch {}
        },
      },
    ]);
  }, [loadProviders]);

  const handleBrowseRepos = useCallback(async (providerHost: string) => {
    if (showRepos === providerHost) { setShowRepos(null); return; }
    setShowRepos(providerHost);
    setReposLoading(true);
    setRepoSearch("");
    try {
      const baseUrl = (quicClient as any).baseUrl;
      const headers = (quicClient as any).authHeaders;
      const res = await fetch(`${baseUrl}/git/provider/repos?host=${encodeURIComponent(providerHost)}&per_page=50`, { headers });
      const data = await res.json();
      if (data.ok) setRepos(data.repos || []);
    } catch {
      Alert.alert("Error", "Failed to load repos");
    } finally {
      setReposLoading(false);
    }
  }, [showRepos]);

  const handleClone = useCallback(async (repo: any) => {
    setCloning(repo.fullName);
    try {
      const baseUrl = (quicClient as any).baseUrl;
      const headers = { ...(quicClient as any).authHeaders, "Content-Type": "application/json" };
      const res = await fetch(`${baseUrl}/repos/clone`, {
        method: "POST", headers,
        body: JSON.stringify({ url: repo.sshUrl || repo.cloneUrl }),
      });
      const data = await res.json();
      if (data.ok) {
        const meta = data.metadata;
        Alert.alert(
          data.alreadyExisted ? "Already Cloned" : "Cloned",
          `${repo.fullName}\n${data.path}${meta?.framework ? `\nFramework: ${meta.framework}` : ""}${meta?.languages ? `\nLanguages: ${meta.languages.join(", ")}` : ""}`,
        );
      } else {
        Alert.alert("Clone Failed", data.error || "Unknown error");
      }
    } catch (e) {
      Alert.alert("Error", e instanceof Error ? e.message : "Clone failed");
    } finally {
      setCloning(null);
    }
  }, []);

  const filteredRepos = repoSearch.trim()
    ? repos.filter((r: any) =>
        r.name.toLowerCase().includes(repoSearch.toLowerCase()) ||
        r.fullName.toLowerCase().includes(repoSearch.toLowerCase()))
    : repos;

  if (loading) {
    return <View style={{ padding: 16, alignItems: "center" }}><ActivityIndicator color={c.accent} /></View>;
  }

  return (
    <View style={{ paddingHorizontal: 14, paddingBottom: 12 }}>
      {/* Privacy notice */}
      <View style={{ backgroundColor: c.accent + "11", borderRadius: 8, padding: 10, marginBottom: 12, borderWidth: 1, borderColor: c.accent + "22" }}>
        <Text style={{ color: c.textSecondary, fontSize: 12, lineHeight: 17 }}>
          All credentials are stored locally on your device and agent. Never sent to Yaver servers. Your code stays private — P2P only.
        </Text>
      </View>

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
                <ScrollView style={{ maxHeight: 300 }}>
                  {filteredRepos.map((repo: any) => (
                    <Pressable
                      key={repo.fullName}
                      style={{ flexDirection: "row", alignItems: "center", paddingHorizontal: 12, paddingVertical: 8, borderBottomWidth: 1, borderBottomColor: c.border, gap: 8 }}
                      onPress={() => handleClone(repo)}
                      disabled={cloning === repo.fullName}
                    >
                      <View style={{ flex: 1 }}>
                        <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                          <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>{repo.name}</Text>
                          {repo.private && (
                            <View style={{ backgroundColor: "#f59e0b22", borderRadius: 3, paddingHorizontal: 4, paddingVertical: 1 }}>
                              <Text style={{ color: "#f59e0b", fontSize: 9, fontWeight: "600" }}>private</Text>
                            </View>
                          )}
                          {repo.language && (
                            <Text style={{ color: c.textMuted, fontSize: 10 }}>{repo.language}</Text>
                          )}
                        </View>
                        {repo.description ? (
                          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 1 }} numberOfLines={1}>{repo.description}</Text>
                        ) : null}
                      </View>
                      {cloning === repo.fullName ? (
                        <ActivityIndicator size="small" color={c.accent} />
                      ) : (
                        <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Clone</Text>
                      )}
                    </Pressable>
                  ))}
                </ScrollView>
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

      {/* Manual token entry (fallback) */}
      {showManualSetup && (
        <View style={{ marginTop: 8, backgroundColor: c.bgCard, borderRadius: 10, borderWidth: 1, borderColor: c.border, padding: 14, gap: 10 }}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>
            {showManualSetup === "github" ? "GitHub Token" : "GitLab Token"}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 17 }}>
            {showManualSetup === "github"
              ? "Create a token at github.com/settings/tokens with 'repo' scope."
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
              {detecting ? <ActivityIndicator size="small" color="#fff" /> : <Text style={[s.actionBtnText, { color: "#fff" }]}>Connect</Text>}
            </Pressable>
            <Pressable
              style={[s.actionBtn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, flex: 1 }]}
              onPress={() => { setShowManualSetup(null); setToken(""); }}
            >
              <Text style={[s.actionBtnText, { color: c.textPrimary }]}>Cancel</Text>
            </Pressable>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 10, textAlign: "center" }}>
            Stored locally on your dev machine. Never sent to Yaver servers.
          </Text>
        </View>
      )}
    </View>
  );
}

// ── Main Screen ────────────────────────────────────────────────────

export default function MoreScreen() {
  const c = useColors();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [showTutorials, setShowTutorials] = useState(false);
  const [tutorialUrl, setTutorialUrl] = useState<string | null>(null);

  // Expandable section state
  const [expandedSection, setExpandedSection] = useState<string | null>(null);

  const insets = useSafeAreaInsets();
  const handleTodos = useCallback(() => router.navigate("/(tabs)/todos" as any), [router]);
  const handleSettings = useCallback(() => router.navigate("/(tabs)/settings" as any), [router]);
  const handleTutorials = useCallback(() => setShowTutorials(true), []);

  const toggleSection = useCallback((section: string) => {
    setExpandedSection((prev) => (prev === section ? null : section));
  }, []);

  return (
    <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <ScrollView contentContainerStyle={s.list}>
        {/* Quality Gates */}
        {connected && (
          <View>
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={() => toggleSection("quality")}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\u2714"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Quality Gates</Text>
                <Text style={[s.desc, { color: c.textMuted }]}>Run tests, lint, typecheck, format</Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>
                {expandedSection === "quality" ? "\u2304" : "\u203A"}
              </Text>
            </Pressable>
            {expandedSection === "quality" && <QualityGatesSection c={c} />}
          </View>
        )}

        {/* Health Monitor */}
        {connected && (
          <View>
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={() => toggleSection("health")}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\u2661"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Health Monitor</Text>
                <Text style={[s.desc, { color: c.textMuted }]}>Monitor production URLs</Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>
                {expandedSection === "health" ? "\u2304" : "\u203A"}
              </Text>
            </Pressable>
            {expandedSection === "health" && <HealthMonitorSection c={c} />}
          </View>
        )}

        {/* Git Providers */}
        {connected && (
          <View>
            <Pressable
              style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={() => toggleSection("git-providers")}
            >
              <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F511}"}</Text>
              <View style={{ flex: 1 }}>
                <Text style={[s.label, { color: c.textPrimary }]}>Git Providers</Text>
                <Text style={[s.desc, { color: c.textMuted }]}>GitHub / GitLab — browse repos, clone to machine</Text>
              </View>
              <Text style={{ color: c.textMuted, fontSize: 16 }}>
                {expandedSection === "git-providers" ? "\u2304" : "\u203A"}
              </Text>
            </Pressable>
            {expandedSection === "git-providers" && <GitProviderSection c={c} />}
          </View>
        )}

        {/* Existing cards */}
        <Pressable style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={handleTodos}>
          <Text style={[s.icon, { color: c.textMuted }]}>{"\u2610"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>Todos</Text>
            <Text style={[s.desc, { color: c.textMuted }]}>Task queue — Run All for sleep mode</Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
        </Pressable>
        <Pressable style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={handleTutorials}>
          <Text style={[s.icon, { color: c.textMuted }]}>{"\u{1F4DA}"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>Tutorials</Text>
            <Text style={[s.desc, { color: c.textMuted }]}>Guides for setup, deploy, voice AI</Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
        </Pressable>
        <Pressable style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={handleSettings}>
          <Text style={[s.icon, { color: c.textMuted }]}>{"\u2699"}</Text>
          <View style={{ flex: 1 }}>
            <Text style={[s.label, { color: c.textPrimary }]}>Settings</Text>
            <Text style={[s.desc, { color: c.textMuted }]}>Theme, speech, preferences</Text>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
        </Pressable>
      </ScrollView>

      {/* Tutorials list modal */}
      <Modal visible={showTutorials && !tutorialUrl} animationType="slide">
        <View style={[s.safe, { backgroundColor: c.bg }]}>
          <View style={[s.modalHeader, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
            <Pressable onPress={() => setShowTutorials(false)} style={{ paddingVertical: 8 }}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
            </Pressable>
            <Text style={[s.modalTitle, { color: c.textPrimary }]}>Tutorials</Text>
            <View style={{ width: 50 }} />
          </View>
          <ScrollView contentContainerStyle={s.list}>
            {TUTORIALS.map((t) => (
              <Pressable
                key={t.label}
                style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
                onPress={() => setTutorialUrl(t.url)}
              >
                <Text style={[s.icon, { color: c.textMuted }]}>{t.icon}</Text>
                <View style={{ flex: 1 }}>
                  <Text style={[s.label, { color: c.textPrimary }]}>{t.label}</Text>
                  <Text style={[s.desc, { color: c.textMuted }]}>{t.desc}</Text>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 16 }}>{"\u203A"}</Text>
              </Pressable>
            ))}
          </ScrollView>
        </View>
      </Modal>

      {/* Tutorial content WebView */}
      <Modal visible={!!tutorialUrl} animationType="slide">
        <View style={[s.safe, { backgroundColor: c.bg }]}>
          <View style={[s.modalHeader, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
            <Pressable onPress={() => setTutorialUrl(null)} style={{ paddingVertical: 8 }}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
            </Pressable>
            <Text style={[s.modalTitle, { color: c.textPrimary }]}>
              {TUTORIALS.find(t => t.url === tutorialUrl)?.label ?? "Tutorial"}
            </Text>
            <View style={{ width: 40 }} />
          </View>
          {tutorialUrl && (
            <WebView
              source={{ uri: tutorialUrl }}
              style={{ flex: 1, backgroundColor: c.bg }}
              javaScriptEnabled
              domStorageEnabled
            />
          )}
        </View>
      </Modal>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  safe: { flex: 1 },
  list: { padding: 16, gap: 8 },
  card: {
    flexDirection: "row",
    alignItems: "center",
    padding: 14,
    borderRadius: 10,
    borderWidth: 1,
    gap: 12,
  },
  icon: { fontSize: 22 },
  label: { fontSize: 15, fontWeight: "600" },
  desc: { fontSize: 12, marginTop: 2 },
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
  textInput: {
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 14,
  },
});
