// SandboxAiPanel.tsx — the "Ask AI" panel for the phone-sandbox code editor.
// Wires the coding-backend selection (on-device model OR BYO-key cloud model)
// into a generate → preview → apply loop against the local src/ tree.
//
// Backend choice is optional and user-controlled: the chip shows what will run
// (resolved from the saved preference + availability) and taps through to the
// /sandbox-ai chooser. Every backend returns the same EditPlan, previewed with
// formatEditPlan and applied with applyEditPlan — the source store re-validates
// every path so a bad LLM response can't escape the project root.

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

interface Props {
  slug: string;
  /** The file currently open in the editor — becomes the on-device edit target. */
  openPath: string | null;
  /** Called after edits are applied so the editor can refresh its tree/buffer. */
  onApplied: (paths: string[]) => void | Promise<void>;
}

type Phase = "idle" | "generating" | "preview" | "applying";

export function SandboxAiPanel({ slug, openPath, onApplied }: Props) {
  const c = useColors();
  const router = useRouter();

  const [expanded, setExpanded] = useState(false);
  const [active, setActive] = useState<ActiveBackendResult | null>(null);
  const [instruction, setInstruction] = useState("");
  const [phase, setPhase] = useState<Phase>("idle");
  const [plan, setPlan] = useState<EditPlan | null>(null);
  const [error, setError] = useState<string | null>(null);

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
      // Gather the src/ tree as snapshots the model can read + edit.
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

  // Collapsed: a single bar that opens the panel.
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

      {active?.fellBackFrom ? (
        <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 6 }}>
          {backendMeta(active.fellBackFrom).label} isn't set up — using {backendLabel}.
        </Text>
      ) : null}

      {phase === "preview" && plan ? (
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
          {error ? (
            <Text style={{ color: "#ff6b6b", fontSize: 12, marginTop: 6 }}>{error}</Text>
          ) : null}
          <Pressable
            onPress={generate}
            disabled={phase === "generating" || !instruction.trim()}
            style={[
              styles.btnPrimary,
              {
                backgroundColor: instruction.trim() ? c.accent : c.bgCard,
                borderColor: c.border,
                marginTop: 8,
              },
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
      )}
    </View>
  );
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
