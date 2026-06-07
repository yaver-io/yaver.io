// SandboxAiPanel.tsx — the "Ask AI" panel for the phone-sandbox code editor.
// Two modes:
//   • Quick edit — single-shot generate → preview → apply (the original flow;
//     every backend returns one EditPlan we preview with formatEditPlan and
//     apply with applyEditPlan).
//   • Agent — the opencode-style iterative loop (codingAgent/runner) that reads,
//     greps, edits, and uses git on-device until the task is done. Each run is
//     wrapped in a git checkpoint (codingAgent/sandboxGit) so it can be reverted
//     in one tap. GLM by default (the cheap BYO path). See
//     docs/agentic-coding-sandbox.md.
//
// The source store re-validates every path so a bad model response can't escape
// the project root.

import React, { useCallback, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useFocusEffect, useRouter } from "expo-router";

import { useColors } from "../context/ThemeContext";
import {
  listSourceFiles,
  readSourceFile,
  writeSourceFile,
  deleteSourceFile,
} from "../lib/phoneSandboxSourceDefault";
import { getLocalPhoneProjectMeta } from "../lib/phoneSandboxLocal";
import { applyEditPlan, formatEditPlan, type EditPlan, type FileSnapshot } from "../lib/llmClient";
import { backendMeta } from "../lib/codingBackend";
import {
  makeProvider,
  resolveActiveBackend,
  type ActiveBackendResult,
} from "../lib/codingBackendStore";
// Agentic loop + on-device git.
import { runCodingAgent, type CodingAgentResult, type CodingToolCall } from "../lib/codingAgent/runner";
import { CODING_TOOLS } from "../lib/codingAgent/sandboxTools";
import { makeGitTools } from "../lib/codingAgent/gitTools";
import { sandboxForSlug, gitForSlug, gitNetForSlug, loadGlmCodingConfig } from "../lib/codingAgent/sandboxBinding";
import {
  ensureRepo,
  checkpointBefore,
  checkpointAfter,
  revertTo,
  type SandboxGitOptions,
} from "../lib/codingAgent/sandboxGit";

interface Props {
  slug: string;
  /** The file currently open in the editor — becomes the on-device edit target. */
  openPath: string | null;
  /** Called after edits are applied so the editor can refresh its tree/buffer. */
  onApplied: (paths: string[]) => void | Promise<void>;
}

type Mode = "quick" | "agent";
type Phase = "idle" | "generating" | "preview" | "applying";
type AgentPhase = "idle" | "running" | "done";

export function SandboxAiPanel({ slug, openPath, onApplied }: Props) {
  const c = useColors();
  const router = useRouter();

  const [expanded, setExpanded] = useState(false);
  const [mode, setMode] = useState<Mode>("quick");
  const [active, setActive] = useState<ActiveBackendResult | null>(null);

  // Quick-edit state.
  const [instruction, setInstruction] = useState("");
  const [phase, setPhase] = useState<Phase>("idle");
  const [plan, setPlan] = useState<EditPlan | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Agent state.
  const [agentPrompt, setAgentPrompt] = useState("");
  const [agentPhase, setAgentPhase] = useState<AgentPhase>("idle");
  const [trace, setTrace] = useState<CodingToolCall[]>([]);
  const [agentRes, setAgentRes] = useState<CodingAgentResult | null>(null);
  const [agentErr, setAgentErr] = useState<string | null>(null);
  const [beforeOid, setBeforeOid] = useState<string | null>(null);
  const [gitNote, setGitNote] = useState<string | null>(null);
  const [reverting, setReverting] = useState(false);

  // Re-resolve the backend whenever the editor regains focus (the user may have
  // changed the preference or added a key on /sandbox-ai).
  useFocusEffect(
    useCallback(() => {
      let cancelled = false;
      resolveActiveBackend()
        .then((r) => {
          if (!cancelled) setActive(r);
        })
        .catch(() => {});
      return () => {
        cancelled = true;
      };
    }, []),
  );

  const backendLabel = (() => {
    if (!active) return "…";
    if (!active.id) return "Set up AI";
    const meta = backendMeta(active.id);
    return active.auto ? `Auto · ${meta.label}` : meta.label;
  })();

  // ── Quick edit (single-shot) ──────────────────────────────────────────

  const generate = useCallback(async () => {
    const prompt = instruction.trim();
    if (!prompt) return;
    setError(null);
    setPlan(null);

    const resolved = await resolveActiveBackend();
    setActive(resolved);
    if (!resolved.id) {
      router.push("/sandbox-ai");
      return;
    }

    setPhase("generating");
    try {
      const entries = (await listSourceFiles(slug)).filter((e) => !e.isDirectory);
      const files: FileSnapshot[] = [];
      for (const e of entries) {
        files.push({ path: e.path, content: await readSourceFile(slug, e.path) });
      }
      const meta = await getLocalPhoneProjectMeta(slug).catch(() => null);

      const provider = await makeProvider(resolved.id, {
        openPath: openPath ?? undefined,
        framework: "react-native",
      });
      if (!provider) {
        throw new Error("That AI backend isn't available anymore. Open AI settings to fix it.");
      }

      const result = await provider.editFiles({
        prompt,
        files,
        framework: "react-native",
        schema: meta?.schema ?? undefined,
      });
      setPlan(result);
      setPhase("preview");
    } catch (e: any) {
      setError(String(e?.message ?? e));
      setPhase("idle");
    }
  }, [instruction, slug, openPath, router]);

  const apply = useCallback(async () => {
    if (!plan) return;
    setPhase("applying");
    try {
      const res = await applyEditPlan(slug, plan, { writeSourceFile, deleteSourceFile });
      await onApplied(res.applied.map((e) => e.path));
      if (res.skipped.length > 0) {
        Alert.alert(
          "Some edits were skipped",
          res.skipped.map((s) => `${s.edit.path}: ${s.reason}`).join("\n"),
        );
      }
      setPlan(null);
      setInstruction("");
      setPhase("idle");
    } catch (e: any) {
      setError(String(e?.message ?? e));
      setPhase("preview");
    }
  }, [plan, slug, onApplied]);

  const discard = useCallback(() => {
    setPlan(null);
    setPhase("idle");
  }, []);

  // ── Agent (iterative loop + git checkpoint) ───────────────────────────

  const runAgent = useCallback(async () => {
    const prompt = agentPrompt.trim();
    if (!prompt) return;
    setAgentErr(null);
    setAgentRes(null);
    setTrace([]);
    setBeforeOid(null);
    setGitNote(null);

    // Agent mode runs on GLM (the cheap coding-plan path). One key powers this
    // and the quick-edit GLM backend.
    const config = await loadGlmCodingConfig();
    if (!config) {
      router.push("/sandbox-ai");
      return;
    }

    setAgentPhase("running");

    // Best-effort git checkpoint so the whole run is revertible. If git fails on
    // this device we still code — just without the safety net.
    let git: SandboxGitOptions | null = null;
    try {
      git = gitForSlug(slug);
      await ensureRepo(git);
      const oid = await checkpointBefore(git, prompt.slice(0, 60));
      setBeforeOid(oid);
    } catch {
      git = null;
      setGitNote("Version control unavailable on this device — this run can't be auto-reverted.");
    }

    // Resolve push creds for this project's remote (if connected). When present,
    // makeGitTools exposes git_push so the agent can push to GitHub/GitLab itself.
    const net = git ? await gitNetForSlug(slug).catch(() => null) : null;

    try {
      const res = await runCodingAgent({
        prompt,
        sandbox: sandboxForSlug(slug),
        config,
        // Full file tools + local git tools (commit/branch/diff/merge); git_push
        // too when a remote + token are configured (net).
        tools: git ? [...CODING_TOOLS, ...makeGitTools(git, net ?? undefined)] : [...CODING_TOOLS],
        onProgress: (e) => {
          if (e.kind === "tool_call") setTrace((t) => [...t, e.call]);
        },
      });
      if (git) {
        try {
          await checkpointAfter(git, prompt.slice(0, 60));
        } catch {
          /* checkpoint is best-effort */
        }
      }
      setAgentRes(res);
      setAgentPhase("done");
      await onApplied(res.mutatedPaths);
    } catch (e: any) {
      setAgentErr(String(e?.message ?? e));
      setAgentPhase("idle");
    }
  }, [agentPrompt, slug, onApplied, router]);

  const revertRun = useCallback(async () => {
    if (!beforeOid) return;
    setReverting(true);
    try {
      const git = gitForSlug(slug);
      await revertTo(git, beforeOid);
      await onApplied(agentRes?.mutatedPaths ?? []);
      setAgentRes(null);
      setTrace([]);
      setBeforeOid(null);
      setAgentPhase("idle");
    } catch (e: any) {
      setAgentErr(String(e?.message ?? e));
    } finally {
      setReverting(false);
    }
  }, [beforeOid, slug, onApplied, agentRes]);

  const newTask = useCallback(() => {
    setAgentRes(null);
    setTrace([]);
    setBeforeOid(null);
    setAgentErr(null);
    setAgentPrompt("");
    setAgentPhase("idle");
  }, []);

  // ── Collapsed bar ─────────────────────────────────────────────────────

  if (!expanded) {
    return (
      <Pressable
        onPress={() => setExpanded(true)}
        style={[styles.bar, { borderColor: c.border, backgroundColor: c.bgCard }]}
      >
        <Text style={{ color: c.accent, fontWeight: "600" }}>✨ Ask AI</Text>
        <Text style={{ color: c.textMuted, fontSize: 12 }}>{backendLabel}</Text>
      </Pressable>
    );
  }

  return (
    <View style={[styles.panel, { borderColor: c.border, backgroundColor: c.bgCard }]}>
      <View style={styles.headerRow}>
        <Text style={{ color: c.textPrimary, fontWeight: "700" }}>✨ Ask AI</Text>
        <View style={{ flexDirection: "row", alignItems: "center" }}>
          <Pressable
            onPress={() => router.push("/sandbox-ai")}
            style={[styles.chip, { borderColor: c.border }]}
          >
            <Text style={{ color: active?.id ? c.textSecondary : c.accent, fontSize: 12 }}>
              {backendLabel} ▾
            </Text>
          </Pressable>
          <Pressable onPress={() => setExpanded(false)} style={{ marginLeft: 8 }}>
            <Text style={{ color: c.textMuted, fontSize: 18 }}>×</Text>
          </Pressable>
        </View>
      </View>

      {/* Mode toggle */}
      <View style={[styles.segment, { borderColor: c.border }]}>
        {(["quick", "agent"] as const).map((m) => (
          <Pressable
            key={m}
            onPress={() => setMode(m)}
            style={[
              styles.segmentBtn,
              { backgroundColor: mode === m ? c.accent : "transparent" },
            ]}
          >
            <Text style={{ color: mode === m ? c.bg : c.textSecondary, fontWeight: "600", fontSize: 12 }}>
              {m === "quick" ? "Quick edit" : "Agent"}
            </Text>
          </Pressable>
        ))}
      </View>

      {mode === "quick" ? (
        // ── Quick-edit UI ──────────────────────────────────────────────
        phase === "preview" && plan ? (
          <>
            <ScrollView style={[styles.preview, { borderColor: c.border, backgroundColor: c.bg }]}>
              <Text style={{ color: c.textSecondary, fontFamily: "Menlo", fontSize: 12 }}>
                {formatEditPlan(plan)}
              </Text>
            </ScrollView>
            <View style={{ flexDirection: "row", marginTop: 8 }}>
              <Pressable
                onPress={apply}
                disabled={plan.edits.length === 0}
                style={[
                  styles.btnPrimary,
                  { backgroundColor: plan.edits.length ? c.accent : c.bgCard, borderColor: c.border, flex: 1 },
                ]}
              >
                <Text style={{ color: plan.edits.length ? c.bg : c.textMuted, fontWeight: "600" }}>
                  Apply {plan.edits.length} edit{plan.edits.length === 1 ? "" : "s"}
                </Text>
              </Pressable>
              <Pressable
                onPress={discard}
                style={[styles.btnSecondary, { borderColor: c.border, marginLeft: 8, flex: 1 }]}
              >
                <Text style={{ color: c.textPrimary, fontWeight: "500" }}>Discard</Text>
              </Pressable>
            </View>
          </>
        ) : phase === "applying" ? (
          <View style={styles.center}>
            <ActivityIndicator color={c.accent} />
            <Text style={{ color: c.textMuted, marginTop: 6, fontSize: 12 }}>Applying…</Text>
          </View>
        ) : (
          <>
            <TextInput
              value={instruction}
              onChangeText={setInstruction}
              multiline
              placeholder={
                openPath
                  ? `Describe a change to ${openPath} (or anything in the project)…`
                  : "Describe what to build or change…"
              }
              placeholderTextColor={c.textMuted}
              style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
              editable={phase !== "generating"}
            />
            {error ? <Text style={{ color: "#ff6b6b", fontSize: 12, marginTop: 6 }}>{error}</Text> : null}
            <Pressable
              onPress={generate}
              disabled={phase === "generating" || !instruction.trim()}
              style={[
                styles.btnPrimary,
                { backgroundColor: instruction.trim() ? c.accent : c.bgCard, borderColor: c.border, marginTop: 8 },
              ]}
            >
              {phase === "generating" ? (
                <ActivityIndicator color={c.bg} />
              ) : (
                <Text style={{ color: instruction.trim() ? c.bg : c.textMuted, fontWeight: "600" }}>
                  {active?.id ? "Generate" : "Set up AI to start"}
                </Text>
              )}
            </Pressable>
          </>
        )
      ) : (
        // ── Agent UI ────────────────────────────────────────────────────
        <>
          <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 6 }}>
            Reads, edits, and commits across the whole project until the task is done. GLM · revertible.
            Connect a GitHub/GitLab repo in the Git panel below to let it push too.
          </Text>


          {(agentPhase === "running" || trace.length > 0) && (
            <ScrollView style={[styles.preview, { borderColor: c.border, backgroundColor: c.bg }]}>
              {trace.map((call, i) => (
                <Text
                  key={i}
                  style={{ color: call.error ? "#ff6b6b" : c.textSecondary, fontFamily: "Menlo", fontSize: 11 }}
                >
                  {call.error ? "✗" : call.denied ? "⊘" : "✓"} {call.name} {argGloss(call)}
                </Text>
              ))}
              {agentRes ? (
                <Text style={{ color: c.textPrimary, fontSize: 12, marginTop: 8 }}>{agentRes.finalText}</Text>
              ) : null}
            </ScrollView>
          )}

          {gitNote ? <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>{gitNote}</Text> : null}
          {agentErr ? <Text style={{ color: "#ff6b6b", fontSize: 12, marginTop: 6 }}>{agentErr}</Text> : null}

          {agentPhase === "running" ? (
            <View style={styles.center}>
              <ActivityIndicator color={c.accent} />
              <Text style={{ color: c.textMuted, marginTop: 6, fontSize: 12 }}>
                {trace.length} step{trace.length === 1 ? "" : "s"}…
              </Text>
            </View>
          ) : agentPhase === "done" && agentRes ? (
            <>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>
                {agentRes.mutatedPaths.length} file{agentRes.mutatedPaths.length === 1 ? "" : "s"} changed ·{" "}
                {agentRes.inputTokens + agentRes.outputTokens} tokens
              </Text>
              <View style={{ flexDirection: "row", marginTop: 8 }}>
                {beforeOid ? (
                  <Pressable
                    onPress={revertRun}
                    disabled={reverting}
                    style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
                  >
                    {reverting ? (
                      <ActivityIndicator color={c.textPrimary} />
                    ) : (
                      <Text style={{ color: c.textPrimary, fontWeight: "500" }}>Revert this run</Text>
                    )}
                  </Pressable>
                ) : null}
                <Pressable
                  onPress={newTask}
                  style={[styles.btnPrimary, { backgroundColor: c.accent, borderColor: c.border, marginLeft: beforeOid ? 8 : 0, flex: 1 }]}
                >
                  <Text style={{ color: c.bg, fontWeight: "600" }}>New task</Text>
                </Pressable>
              </View>
            </>
          ) : (
            <>
              <TextInput
                value={agentPrompt}
                onChangeText={setAgentPrompt}
                multiline
                placeholder="Describe a task — e.g. 'add a settings screen with a dark-mode toggle wired to state'"
                placeholderTextColor={c.textMuted}
                style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
              />
              <Pressable
                onPress={runAgent}
                disabled={!agentPrompt.trim()}
                style={[
                  styles.btnPrimary,
                  { backgroundColor: agentPrompt.trim() ? c.accent : c.bgCard, borderColor: c.border, marginTop: 8 },
                ]}
              >
                <Text style={{ color: agentPrompt.trim() ? c.bg : c.textMuted, fontWeight: "600" }}>
                  Run agent
                </Text>
              </Pressable>
            </>
          )}
        </>
      )}
    </View>
  );
}

/** One-line gloss of a tool call's args for the live trace. */
function argGloss(call: CodingToolCall): string {
  const a = call.args as any;
  if (!a || typeof a !== "object") return "";
  if (typeof a.path === "string") return a.path;
  if (typeof a.pattern === "string") return `/${a.pattern}/`;
  if (typeof a.message === "string") return `"${a.message.slice(0, 32)}"`;
  if (typeof a.glob === "string") return a.glob;
  return "";
}

const styles = StyleSheet.create({
  bar: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    marginHorizontal: 12,
    marginBottom: 6,
  },
  panel: {
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
    marginHorizontal: 12,
    marginBottom: 6,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 8,
  },
  segment: {
    flexDirection: "row",
    borderWidth: 1,
    borderRadius: 8,
    padding: 2,
    marginBottom: 10,
  },
  segmentBtn: {
    flex: 1,
    alignItems: "center",
    paddingVertical: 6,
    borderRadius: 6,
  },
  chip: {
    borderWidth: 1,
    borderRadius: 14,
    paddingHorizontal: 10,
    paddingVertical: 4,
  },
  input: {
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    fontSize: 13,
    minHeight: 64,
    textAlignVertical: "top",
  },
  preview: {
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    maxHeight: 200,
  },
  btnPrimary: {
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderRadius: 8,
    borderWidth: 1,
    alignItems: "center",
  },
  btnSecondary: {
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderRadius: 8,
    borderWidth: 1,
    alignItems: "center",
  },
  center: { alignItems: "center", paddingVertical: 16 },
});
