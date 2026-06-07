// SandboxGitPanel.tsx — the git surface for a phone-local sandbox project. Wraps
// the on-device git lib (codingAgent/sandboxGitOps over the gitFsExpo adapter):
// status + commit, branches, merge with conflict resolution, history, and push
// to GitHub. Pure logic lives in gitPanelModel.ts / sandboxGitOps.ts / githubAuth
// — this is presentation + orchestration only.

import React, { useCallback, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useFocusEffect } from "expo-router";
// isomorphic-git's browser/RN http client (used only when pushing).
import http from "isomorphic-git/http/web";

import { useColors } from "../context/ThemeContext";
import { gitForSlug } from "../lib/codingAgent/sandboxBinding";
import { ensureRepo, log, type CommitEntry, type SandboxGitOptions } from "../lib/codingAgent/sandboxGit";
import {
  currentBranch,
  listBranches,
  createBranch,
  switchBranch,
  diffStatus,
  mergeBranch,
  listConflicts,
  resolveConflict,
  completeMerge,
  addRemote,
  listRemotes,
  push,
  parseConflictRegions,
  resolveAllRegions,
  type FileDiff,
} from "../lib/codingAgent/sandboxGitOps";
import { groupChanges, statusSummary, suggestCommitMessage, pushability } from "../lib/gitPanelModel";
import { hasGitHubToken, saveGitHubToken, gitHubNetFromStore } from "../lib/githubAuthStore";
import { normalizeRepoUrl, looksLikeGitHubToken } from "../lib/githubAuth";

interface Props {
  slug: string;
  /** Called after any tree-changing op so the editor can refresh its buffers. */
  onChanged?: () => void;
}

interface MergeState {
  active: boolean;
  theirs?: string;
  theirsOid?: string;
  conflicts: string[];
}

export default function SandboxGitPanel({ slug, onChanged }: Props) {
  const c = useColors();
  const [git] = useState<SandboxGitOptions>(() => gitForSlug(slug));

  const [busy, setBusy] = useState(false);
  const [branch, setBranch] = useState<string | null>(null);
  const [branches, setBranches] = useState<string[]>([]);
  const [changes, setChanges] = useState<FileDiff[]>([]);
  const [history, setHistory] = useState<CommitEntry[]>([]);
  const [message, setMessage] = useState("");
  const [newBranch, setNewBranch] = useState("");
  const [merge, setMerge] = useState<MergeState>({ active: false, conflicts: [] });

  const [repo, setRepo] = useState("");
  const [hasRemote, setHasRemote] = useState(false);
  const [hasToken, setHasToken] = useState(false);
  const [token, setToken] = useState("");
  const [pushing, setPushing] = useState(false);

  const reload = useCallback(async () => {
    await ensureRepo(git);
    const [br, brs, ch, hist, conflicts, remotes, tok] = await Promise.all([
      currentBranch(git),
      listBranches(git),
      diffStatus(git),
      log(git, 20),
      listConflicts(git),
      listRemotes(git),
      hasGitHubToken(),
    ]);
    setBranch(br);
    setBranches(brs);
    setChanges(ch);
    setHistory(hist);
    setMessage((m) => m || suggestCommitMessage(ch));
    setMerge((s) => ({ ...s, active: conflicts.length > 0 || s.active, conflicts }));
    setHasRemote(remotes.length > 0);
    setHasToken(tok);
    const origin = remotes.find((r) => r.remote === "origin");
    if (origin && !repo) setRepo(origin.url);
  }, [git, repo]);

  useFocusEffect(
    useCallback(() => {
      reload().catch((e) => console.warn("[git] reload", e?.message));
    }, [reload]),
  );

  const runOp = useCallback(
    async (fn: () => Promise<void>) => {
      setBusy(true);
      try {
        await fn();
        await reload();
        onChanged?.();
      } catch (e: any) {
        Alert.alert("Git", e?.message ?? String(e));
      } finally {
        setBusy(false);
      }
    },
    [reload, onChanged],
  );

  const doCommit = () =>
    runOp(async () => {
      const { commitAll } = await import("../lib/codingAgent/sandboxGit");
      const oid = await commitAll(git, message.trim() || "update");
      if (!oid) Alert.alert("Git", "Nothing to commit.");
      setMessage("");
    });

  const doCreateBranch = () =>
    runOp(async () => {
      const name = newBranch.trim();
      if (!name) return;
      await createBranch(git, name, { checkout: true });
      setNewBranch("");
    });

  const doSwitch = (name: string) => runOp(() => switchBranch(git, name));

  const doMerge = (theirs: string) =>
    runOp(async () => {
      const res = await mergeBranch(git, theirs);
      if (res.status === "conflict") {
        setMerge({ active: true, theirs, theirsOid: res.theirsOid, conflicts: res.conflicts ?? [] });
        Alert.alert("Merge conflict", `Resolve ${res.conflicts?.length ?? 0} file(s), then complete the merge.`);
      } else {
        setMerge({ active: false, conflicts: [] });
        Alert.alert("Merge", res.status === "already-merged" ? "Already up to date." : "Merged cleanly.");
      }
    });

  const resolvePick = (path: string, pick: "ours" | "theirs") =>
    runOp(async () => {
      const content = (await git.fs.promises.readFile(`${git.dir}/${path}`, { encoding: "utf8" })) as string;
      await resolveConflict(git, path, resolveAllRegions(content, pick));
    });

  const doCompleteMerge = () =>
    runOp(async () => {
      const remaining = await listConflicts(git);
      if (remaining.length) {
        Alert.alert("Git", `Still ${remaining.length} unresolved file(s).`);
        return;
      }
      await completeMerge(git, `merge ${merge.theirs ?? "branch"}`, { theirsOid: merge.theirsOid });
      setMerge({ active: false, conflicts: [] });
    });

  const doSaveToken = () =>
    runOp(async () => {
      const t = token.trim();
      if (!looksLikeGitHubToken(t)) {
        Alert.alert("GitHub", "That doesn't look like a GitHub token (ghp_… / github_pat_…).");
        return;
      }
      await saveGitHubToken(t);
      setToken("");
      setHasToken(true);
    });

  const doPush = async () => {
    setPushing(true);
    try {
      // Multi-provider (GitHub / GitLab / Bitbucket / self-hosted): set the
      // remote first so the host can be detected, then resolve a token for that
      // host from the stored git credentials (Git Accounts settings). GitHub
      // continues to work via the same token slot the inline field writes.
      if (repo.trim()) {
        const { normalizeGitUrl } = await import("../lib/gitProviderAuth");
        await addRemote(git, "origin", normalizeGitUrl(repo.trim()));
      }
      const { gitNetForSlug } = await import("../lib/codingAgent/sandboxBinding");
      const net = await gitNetForSlug(slug);
      if (!net) {
        Alert.alert("Git", "No token for this repo's host. Add one in Git Accounts (Sandbox AI → Source control).");
        return;
      }
      const res = await push(git, net, {});
      Alert.alert("Push", res.ok ? "Pushed." : `Push failed: ${res.error}`);
      await reload();
    } catch (e: any) {
      Alert.alert("Push", e?.message ?? String(e));
    } finally {
      setPushing(false);
    }
  };

  const grouped = groupChanges(changes);
  const pushGate = pushability({ hasToken, hasRemote: hasRemote || !!repo.trim(), busy: pushing });

  return (
    <ScrollView style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
      <View style={styles.headerRow}>
        <Text style={{ color: c.textPrimary, fontWeight: "700" }}>⎇ Git</Text>
        <Text style={{ color: c.textMuted, fontSize: 12 }}>{statusSummary(branch, changes)}</Text>
        {busy ? <ActivityIndicator size="small" color={c.accent} /> : null}
      </View>

      {/* Changes + commit */}
      {changes.length > 0 ? (
        <View style={styles.section}>
          {grouped.added.map((p) => (
            <Text key={p} style={[styles.file, { color: c.success }]}>+ {p}</Text>
          ))}
          {grouped.modified.map((p) => (
            <Text key={p} style={[styles.file, { color: c.textSecondary }]}>~ {p}</Text>
          ))}
          {grouped.deleted.map((p) => (
            <Text key={p} style={[styles.file, { color: c.error }]}>− {p}</Text>
          ))}
          <TextInput
            value={message}
            onChangeText={setMessage}
            placeholder="Commit message"
            placeholderTextColor={c.textMuted}
            style={[styles.input, { color: c.textPrimary, backgroundColor: c.bgInput, borderColor: c.border }]}
          />
          <Pressable onPress={doCommit} disabled={busy} style={[styles.btn, { backgroundColor: c.accent }]}>
            <Text style={styles.btnText}>Commit {changes.length}</Text>
          </Pressable>
        </View>
      ) : (
        <Text style={[styles.muted, { color: c.textMuted }]}>Working tree clean.</Text>
      )}

      {/* Branches */}
      <View style={styles.section}>
        <Text style={[styles.label, { color: c.textTertiary }]}>BRANCHES</Text>
        <View style={styles.chipRow}>
          {branches.map((b) => (
            <Pressable
              key={b}
              onPress={() => b !== branch && doSwitch(b)}
              style={[styles.chip, { borderColor: b === branch ? c.accent : c.border }]}
            >
              <Text style={{ color: b === branch ? c.accent : c.textSecondary, fontSize: 12 }}>
                {b === branch ? "● " : ""}
                {b}
              </Text>
            </Pressable>
          ))}
        </View>
        <View style={styles.inlineRow}>
          <TextInput
            value={newBranch}
            onChangeText={setNewBranch}
            placeholder="new-branch"
            placeholderTextColor={c.textMuted}
            style={[styles.input, styles.flex, { color: c.textPrimary, backgroundColor: c.bgInput, borderColor: c.border }]}
          />
          <Pressable onPress={doCreateBranch} disabled={busy} style={[styles.btnSmall, { borderColor: c.border }]}>
            <Text style={{ color: c.accent, fontSize: 12 }}>Create</Text>
          </Pressable>
        </View>
        {/* Merge: offer the other branches */}
        {branches.filter((b) => b !== branch).length > 0 ? (
          <View style={styles.chipRow}>
            {branches
              .filter((b) => b !== branch)
              .map((b) => (
                <Pressable key={b} onPress={() => doMerge(b)} disabled={busy} style={[styles.chip, { borderColor: c.border }]}>
                  <Text style={{ color: c.textSecondary, fontSize: 12 }}>Merge {b} →</Text>
                </Pressable>
              ))}
          </View>
        ) : null}
      </View>

      {/* Conflict resolver */}
      {merge.conflicts.length > 0 ? (
        <View style={[styles.section, styles.conflict, { backgroundColor: c.warnBg, borderColor: c.warnBorder }]}>
          <Text style={{ color: c.warn, fontWeight: "700" }}>Merge conflicts</Text>
          {merge.conflicts.map((p) => (
            <View key={p} style={styles.conflictFile}>
              <Text style={{ color: c.textPrimary, fontSize: 13 }}>{p}</Text>
              <View style={styles.inlineRow}>
                <Pressable onPress={() => resolvePick(p, "ours")} disabled={busy} style={[styles.btnSmall, { borderColor: c.border }]}>
                  <Text style={{ color: c.textSecondary, fontSize: 12 }}>Keep mine</Text>
                </Pressable>
                <Pressable onPress={() => resolvePick(p, "theirs")} disabled={busy} style={[styles.btnSmall, { borderColor: c.border }]}>
                  <Text style={{ color: c.textSecondary, fontSize: 12 }}>Keep theirs</Text>
                </Pressable>
              </View>
            </View>
          ))}
          <Pressable onPress={doCompleteMerge} disabled={busy} style={[styles.btn, { backgroundColor: c.accent }]}>
            <Text style={styles.btnText}>Complete merge</Text>
          </Pressable>
        </View>
      ) : null}

      {/* Push to GitHub */}
      <View style={styles.section}>
        <Text style={[styles.label, { color: c.textTertiary }]}>GITHUB</Text>
        {!hasToken ? (
          <View style={styles.inlineRow}>
            <TextInput
              value={token}
              onChangeText={setToken}
              placeholder="GitHub token (ghp_…)"
              placeholderTextColor={c.textMuted}
              secureTextEntry
              autoCapitalize="none"
              style={[styles.input, styles.flex, { color: c.textPrimary, backgroundColor: c.bgInput, borderColor: c.border }]}
            />
            <Pressable onPress={doSaveToken} disabled={busy} style={[styles.btnSmall, { borderColor: c.border }]}>
              <Text style={{ color: c.accent, fontSize: 12 }}>Save</Text>
            </Pressable>
          </View>
        ) : null}
        <View style={styles.inlineRow}>
          <TextInput
            value={repo}
            onChangeText={setRepo}
            placeholder="owner/repo"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            style={[styles.input, styles.flex, { color: c.textPrimary, backgroundColor: c.bgInput, borderColor: c.border }]}
          />
          <Pressable
            onPress={doPush}
            disabled={!pushGate.enabled}
            style={[styles.btnSmall, { borderColor: c.border, opacity: pushGate.enabled ? 1 : 0.5 }]}
          >
            {pushing ? <ActivityIndicator size="small" color={c.accent} /> : <Text style={{ color: c.accent, fontSize: 12 }}>Push</Text>}
          </Pressable>
        </View>
        <Text style={[styles.muted, { color: c.textMuted }]}>{pushGate.hint}</Text>
      </View>

      {/* History */}
      {history.length > 0 ? (
        <View style={styles.section}>
          <Text style={[styles.label, { color: c.textTertiary }]}>HISTORY</Text>
          {history.slice(0, 10).map((h) => (
            <Text key={h.oid} style={[styles.commit, { color: c.textSecondary }]} numberOfLines={1}>
              <Text style={{ color: c.textMuted }}>{h.oid.slice(0, 7)} </Text>
              {h.message.trim()}
            </Text>
          ))}
        </View>
      ) : null}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  panel: { borderWidth: 1, borderRadius: 14, padding: 12, maxHeight: 520 },
  headerRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 8, marginBottom: 8 },
  section: { marginTop: 10, gap: 6 },
  label: { fontSize: 11, fontWeight: "700", letterSpacing: 0.5 },
  file: { fontSize: 12, fontFamily: "Menlo" },
  muted: { fontSize: 12 },
  input: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8, fontSize: 13 },
  flex: { flex: 1 },
  inlineRow: { flexDirection: "row", alignItems: "center", gap: 8 },
  chipRow: { flexDirection: "row", flexWrap: "wrap", gap: 6 },
  chip: { borderWidth: 1, borderRadius: 999, paddingHorizontal: 10, paddingVertical: 5 },
  btn: { borderRadius: 8, paddingVertical: 10, alignItems: "center" },
  btnText: { color: "#fff", fontWeight: "700", fontSize: 13 },
  btnSmall: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 12, paddingVertical: 8, alignItems: "center", justifyContent: "center" },
  conflict: { borderWidth: 1, borderRadius: 10, padding: 10 },
  conflictFile: { gap: 4, marginVertical: 4 },
  commit: { fontSize: 12, fontFamily: "Menlo" },
});
