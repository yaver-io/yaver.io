// app/repo-coding.tsx — code a real GitHub repo FULLY ON THIS PHONE, no remote
// box. The honest on-iOS engine is "Yaver Agent · GLM": Yaver's own agentic loop
// (read→grep→edit→git) running in Hermes, driving the GLM cloud API. The real
// claude/codex/opencode CLIs can't run on iOS (no Node/exec/JIT) — those need a
// machine; this path doesn't.
//
// Flow: add a GLM key (the engine) + optional GitHub token (private repos / push)
// → clone a repo onto the phone (isomorphic-git over expo-file-system) → ask the
// agent to make a change → it edits the whole tree (incl. convex/) with a git
// checkpoint wrapped around the run → commit/push from the embedded git panel.
//
// Everything here is on-device. Only Convex (the backend) is remote — and the
// agent edits convex/*.ts locally; deploying those is the one step that still
// needs a machine (Convex's CLI is Node), surfaced honestly below.

import React, { useCallback, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useFocusEffect, useLocalSearchParams, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import http from "isomorphic-git/http/web";

import { useColors } from "../src/context/ThemeContext";
import { AppBackButton } from "../src/components/AppBackButton";
import SandboxGitPanel from "../src/components/SandboxGitPanel";
import { LOCAL_KEYS, deleteLocalSecret, getLocalSecret, saveLocalSecret } from "../src/lib/auth";
import { hasGitHubToken, saveGitHubToken, gitHubNetFromStore } from "../src/lib/githubAuthStore";
import { looksLikeGitHubToken } from "../src/lib/githubAuth";
import { cloneGitRepoToPhone } from "../src/lib/cloneToPhone";
import { listPhoneProjects, type PhoneProject } from "../src/lib/phoneProjects";
import { runAgenticCoding, gitContextForSlug } from "../src/lib/codingAgent/codingAgentRun";
import { repoSandboxForSlug } from "../src/lib/codingAgent/repoSandbox";
import { loadCodingConfig } from "../src/lib/codingAgent/sandboxBinding";
import { isRepo, revertTo } from "../src/lib/codingAgent/sandboxGit";
import type { CodingAgentProgress } from "../src/lib/codingAgent/runner";

const SFMG_DEFAULT = "kivanccakmak/sfmg";

export default function RepoCodingScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const params = useLocalSearchParams<{ slug?: string }>();

  const [hasGlm, setHasGlm] = useState(false);
  const [hasGh, setHasGh] = useState(false);
  const [glmDraft, setGlmDraft] = useState("");
  const [ghDraft, setGhDraft] = useState("");
  const [savingKey, setSavingKey] = useState<"glm" | "gh" | null>(null);

  const [repoInput, setRepoInput] = useState(SFMG_DEFAULT);
  const [cloning, setCloning] = useState(false);

  const [projects, setProjects] = useState<PhoneProject[]>([]);
  const [repoSlugs, setRepoSlugs] = useState<Set<string>>(new Set());
  const [selected, setSelected] = useState<string | null>(params.slug ?? null);

  const [prompt, setPrompt] = useState("");
  const [running, setRunning] = useState(false);
  const [logLines, setLogLines] = useState<string[]>([]);
  const [beforeOid, setBeforeOid] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const reload = useCallback(async () => {
    const [glm, gh, list] = await Promise.all([
      getLocalSecret(LOCAL_KEYS.glmApiKey),
      hasGitHubToken(),
      listPhoneProjects().catch(() => [] as PhoneProject[]),
    ]);
    setHasGlm(!!glm?.trim());
    setHasGh(gh);
    setProjects(list);
    // Which phone projects actually have a git repo (so we mark them + enable the agent).
    const repos = new Set<string>();
    await Promise.all(
      list.map(async (p) => {
        try {
          if (await isRepo(gitContextForSlug(p.slug))) repos.add(p.slug);
        } catch {
          /* ignore */
        }
      }),
    );
    setRepoSlugs(repos);
  }, []);

  useFocusEffect(
    useCallback(() => {
      void reload();
    }, [reload]),
  );

  const saveGlm = useCallback(async () => {
    const v = glmDraft.trim();
    setSavingKey("glm");
    try {
      if (v) await saveLocalSecret(LOCAL_KEYS.glmApiKey, v);
      else await deleteLocalSecret(LOCAL_KEYS.glmApiKey);
      setGlmDraft("");
      await reload();
    } finally {
      setSavingKey(null);
    }
  }, [glmDraft, reload]);

  const saveGh = useCallback(async () => {
    const v = ghDraft.trim();
    if (v && !looksLikeGitHubToken(v)) {
      Alert.alert("GitHub", "That doesn't look like a GitHub token (ghp_… / github_pat_…).");
      return;
    }
    setSavingKey("gh");
    try {
      if (v) await saveGitHubToken(v);
      else await deleteLocalSecret(LOCAL_KEYS.githubToken);
      setGhDraft("");
      await reload();
    } finally {
      setSavingKey(null);
    }
  }, [ghDraft, reload]);

  const doClone = useCallback(async () => {
    const input = repoInput.trim();
    if (!input) return;
    setCloning(true);
    try {
      const res = await cloneGitRepoToPhone(input);
      await reload();
      setSelected(res.slug);
      Alert.alert(
        "Cloned",
        `${input} is on your phone${res.anonymous ? " (public clone — add a GitHub token to push)" : ""}.`,
      );
    } catch (e: any) {
      Alert.alert("Clone failed", e?.message ?? String(e));
    } finally {
      setCloning(false);
    }
  }, [repoInput, reload]);

  const appendLog = useCallback((line: string) => {
    setLogLines((prev) => [...prev.slice(-200), line]);
  }, []);

  const onProgress = useCallback(
    (e: CodingAgentProgress) => {
      if (e.kind === "tool_call") {
        const { name, args, error, denied } = e.call;
        const a = args as any;
        const target = a?.path ?? a?.pattern ?? a?.branch ?? a?.message ?? "";
        const mark = denied ? "⊘" : error || (e.call.result as any)?.error ? "✗" : "✓";
        appendLog(`${mark} ${name}${target ? ` ${String(target).slice(0, 60)}` : ""}`);
      } else if (e.kind === "model_text" && e.text.trim()) {
        appendLog(`💬 ${e.text.trim().slice(0, 200)}`);
      }
    },
    [appendLog],
  );

  const runAgent = useCallback(async () => {
    const slug = selected;
    const p = prompt.trim();
    if (!slug || !p) return;
    const config = await loadCodingConfig();
    if (!config) {
      Alert.alert(
        "Add a GLM key",
        "The Yaver Agent needs a GLM API key — or turn on Yaver-managed mode (uses your credit balance, no key needed).",
      );
      return;
    }
    const net = (await gitHubNetFromStore(http)) ?? undefined; // lets the agent git_push if a token is set
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setRunning(true);
    setLogLines([`▶ ${p}`]);
    setBeforeOid(null);
    try {
      const run = await runAgenticCoding({
        slug,
        prompt: p,
        config,
        net,
        sandbox: repoSandboxForSlug(slug), // whole repo (convex/, app.json, …), not just src/
        onProgress,
        signal: ctrl.signal,
      });
      setBeforeOid(run.before);
      const r = run.result;
      const changed = r.mutatedPaths.length;
      appendLog(
        `■ done · ${r.steps} step${r.steps === 1 ? "" : "s"} · ${changed} file${changed === 1 ? "" : "s"} changed` +
          (r.hitMaxSteps ? " · hit step cap" : ""),
      );
      if (r.finalText.trim()) appendLog(`\n${r.finalText.trim()}`);
      await reload();
    } catch (e: any) {
      if (e?.name === "AbortError") appendLog("⏹ stopped");
      else appendLog(`✗ ${e?.message ?? String(e)}`);
    } finally {
      setRunning(false);
      abortRef.current = null;
    }
  }, [selected, prompt, onProgress, appendLog, reload]);

  const stop = useCallback(() => abortRef.current?.abort(), []);

  const revert = useCallback(async () => {
    if (!selected || !beforeOid) return;
    try {
      await revertTo(gitContextForSlug(selected), beforeOid);
      appendLog("↩ reverted to the pre-run checkpoint");
      setBeforeOid(null);
      await reload();
    } catch (e: any) {
      Alert.alert("Revert failed", e?.message ?? String(e));
    }
  }, [selected, beforeOid, appendLog, reload]);

  const gitProjects = projects.filter((p) => repoSlugs.has(p.slug));
  const canRunAgent = !!selected && repoSlugs.has(selected) && hasGlm && !running;

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <View style={{ paddingTop: insets.top + 8 }}>
        <View style={styles.header}>
          <AppBackButton onPress={() => router.back()} />
          <View style={{ marginLeft: 8, flex: 1 }}>
            <Text style={[styles.h1, { color: c.textPrimary }]}>Code on this phone</Text>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>
              Yaver Agent · GLM — runs fully on your iPhone, no remote box
            </Text>
          </View>
        </View>
      </View>

      <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: insets.bottom + 48 }}>
        {/* Setup */}
        <Text style={[styles.section, { color: c.textSecondary }]}>SETUP</Text>
        <View style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          <KeyRow
            label="GLM API key"
            sub="The engine. The agent reads/edits code through GLM."
            saved={hasGlm}
            value={glmDraft}
            onChange={setGlmDraft}
            onSave={saveGlm}
            saving={savingKey === "glm"}
            c={c}
          />
          <View style={{ height: 10 }} />
          <KeyRow
            label="GitHub token"
            sub="For private repos and pushing. Optional for public clones."
            saved={hasGh}
            value={ghDraft}
            onChange={setGhDraft}
            onSave={saveGh}
            saving={savingKey === "gh"}
            c={c}
          />
        </View>

        {/* Clone */}
        <Text style={[styles.section, { color: c.textSecondary, marginTop: 18 }]}>CLONE A REPO</Text>
        <View style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          <View style={{ flexDirection: "row" }}>
            <TextInput
              value={repoInput}
              onChangeText={setRepoInput}
              placeholder="owner/repo"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              style={[styles.input, { flex: 1, color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            />
            <Pressable
              onPress={doClone}
              disabled={cloning}
              style={[styles.saveBtn, { backgroundColor: c.accent, marginLeft: 8 }]}
            >
              {cloning ? <ActivityIndicator color={c.bg} /> : <Text style={{ color: c.bg, fontWeight: "600" }}>Clone</Text>}
            </Pressable>
          </View>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8, lineHeight: 16 }}>
            Clones onto this phone via on-device git (shallow). Default is sfmg.
          </Text>
        </View>

        {/* Projects */}
        <Text style={[styles.section, { color: c.textSecondary, marginTop: 18 }]}>YOUR REPOS ON THIS PHONE</Text>
        {gitProjects.length === 0 ? (
          <Text style={{ color: c.textMuted, fontSize: 13, paddingHorizontal: 4 }}>
            No cloned repos yet. Clone one above to start.
          </Text>
        ) : (
          gitProjects.map((p) => {
            const sel = p.slug === selected;
            return (
              <Pressable
                key={p.slug}
                onPress={() => setSelected(p.slug)}
                style={[styles.projRow, { borderColor: sel ? c.accent : c.border, backgroundColor: c.bgCard }]}
              >
                <View style={[styles.radio, { borderColor: sel ? c.accent : c.border }]}>
                  {sel ? <View style={[styles.radioDot, { backgroundColor: c.accent }]} /> : null}
                </View>
                <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{p.name}</Text>
              </Pressable>
            );
          })
        )}

        {/* Agent */}
        {selected && repoSlugs.has(selected) ? (
          <>
            <Text style={[styles.section, { color: c.textSecondary, marginTop: 18 }]}>
              YAVER AGENT · GLM
            </Text>
            <View style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}>
              <TextInput
                value={prompt}
                onChangeText={setPrompt}
                placeholder="Describe a change — e.g. “add a Settings screen with a dark-mode toggle”"
                placeholderTextColor={c.textMuted}
                multiline
                style={[styles.prompt, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
              />
              <View style={{ flexDirection: "row", marginTop: 8 }}>
                <Pressable
                  onPress={runAgent}
                  disabled={!canRunAgent || !prompt.trim()}
                  style={[
                    styles.runBtn,
                    { backgroundColor: c.accent, opacity: canRunAgent && prompt.trim() ? 1 : 0.5 },
                  ]}
                >
                  {running ? (
                    <ActivityIndicator color={c.bg} />
                  ) : (
                    <Text style={{ color: c.bg, fontWeight: "700" }}>Run on this phone</Text>
                  )}
                </Pressable>
                {running ? (
                  <Pressable onPress={stop} style={[styles.stopBtn, { borderColor: c.border, marginLeft: 8 }]}>
                    <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Stop</Text>
                  </Pressable>
                ) : beforeOid ? (
                  <Pressable onPress={revert} style={[styles.stopBtn, { borderColor: c.border, marginLeft: 8 }]}>
                    <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Revert</Text>
                  </Pressable>
                ) : null}
              </View>
              {!hasGlm ? (
                <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 8 }}>
                  Add a GLM key above to enable the agent.
                </Text>
              ) : null}

              {logLines.length > 0 ? (
                <View style={[styles.log, { borderColor: c.border, backgroundColor: c.bg }]}>
                  {logLines.map((l, i) => (
                    <Text key={i} style={[styles.logLine, { color: c.textSecondary }]}>
                      {l}
                    </Text>
                  ))}
                </View>
              ) : null}
            </View>

            {/* Commit / push */}
            <Text style={[styles.section, { color: c.textSecondary, marginTop: 18 }]}>COMMIT & PUSH</Text>
            <SandboxGitPanel slug={selected} onChanged={() => void reload()} />
          </>
        ) : null}

        <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 18, lineHeight: 16 }}>
          The real claude / codex / opencode CLIs can't run on iOS — they need a machine. This agent is
          Yaver's own loop on GLM, doing the same kind of work fully on-device. Building/running the app and
          deploying Convex still need a machine.
        </Text>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function KeyRow({
  label,
  sub,
  saved,
  value,
  onChange,
  onSave,
  saving,
  c,
}: {
  label: string;
  sub: string;
  saved: boolean;
  value: string;
  onChange: (t: string) => void;
  onSave: () => void;
  saving: boolean;
  c: ReturnType<typeof useColors>;
}) {
  return (
    <View>
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{label}</Text>
        <Text style={{ color: saved ? "#4caf50" : c.textMuted, fontSize: 12 }}>{saved ? "saved" : "not set"}</Text>
      </View>
      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>{sub}</Text>
      <View style={{ flexDirection: "row", marginTop: 8 }}>
        <TextInput
          value={value}
          onChangeText={onChange}
          placeholder={saved ? "Replace (blank + Save to remove)" : "Paste key"}
          placeholderTextColor={c.textMuted}
          autoCapitalize="none"
          autoCorrect={false}
          secureTextEntry
          style={[styles.input, { flex: 1, color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
        />
        <Pressable onPress={onSave} style={[styles.saveBtn, { backgroundColor: c.accent, marginLeft: 8 }]}>
          {saving ? <ActivityIndicator color={c.bg} /> : <Text style={{ color: c.bg, fontWeight: "600" }}>Save</Text>}
        </Pressable>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  header: { flexDirection: "row", alignItems: "center", paddingHorizontal: 12, paddingBottom: 10 },
  h1: { fontSize: 18, fontWeight: "700" },
  section: { fontSize: 11, fontWeight: "700", letterSpacing: 0.5, marginBottom: 8 },
  card: { borderWidth: 1, borderRadius: 10, padding: 12 },
  input: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8, fontSize: 13 },
  saveBtn: { borderRadius: 8, paddingHorizontal: 16, alignItems: "center", justifyContent: "center" },
  projRow: {
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
    marginBottom: 8,
  },
  radio: { width: 20, height: 20, borderRadius: 10, borderWidth: 2, marginRight: 12, alignItems: "center", justifyContent: "center" },
  radioDot: { width: 10, height: 10, borderRadius: 5 },
  prompt: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 10, fontSize: 14, minHeight: 72, textAlignVertical: "top" },
  runBtn: { flex: 1, borderRadius: 8, paddingVertical: 12, alignItems: "center", justifyContent: "center" },
  stopBtn: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 18, alignItems: "center", justifyContent: "center" },
  log: { marginTop: 12, borderWidth: 1, borderRadius: 8, padding: 10, gap: 3 },
  logLine: { fontSize: 12, fontFamily: "Menlo" },
});
