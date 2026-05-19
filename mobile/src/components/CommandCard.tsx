// Foldable shell-command card (Claude-Code-mobile style): a tappable
// header showing `$ <command>` + a status badge; tap to expand the
// captured stdout/stderr. Driven by CommandCardModel
// (src/lib/commandEvents.ts) accumulated from the task SSE stream.
//
// Self-contained on purpose: own StyleSheet + useColors + expand state,
// so it can be dropped into the chat list without touching the
// existing render pipeline. Matches DebugSection conventions in
// app/(tabs)/tasks.tsx (Pressable toggle + conditional body, theme
// tokens, monoFamily).

import React, { useState } from "react";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { monoFamily, spacing } from "../theme/tokens";
import type { CommandCardModel } from "../lib/commandEvents";

const MAX_BODY_LINES = 400; // per-card cap; mirrors the screen's windowing intent

function statusMeta(
  m: CommandCardModel,
  c: ReturnType<typeof useColors>,
): { label: string; color: string } {
  switch (m.status) {
    case "running":
      return { label: "running…", color: c.textMuted };
    case "ok":
      return { label: "exit 0", color: c.success };
    case "error":
      return {
        label: `exit ${m.exitCode ?? "?"}`,
        color: c.error,
      };
    default:
      return { label: "done", color: c.textMuted };
  }
}

function trimBody(s: string): { text: string; truncated: boolean } {
  if (!s) return { text: "", truncated: false };
  const lines = s.split("\n");
  if (lines.length <= MAX_BODY_LINES) return { text: s, truncated: false };
  return {
    text: lines.slice(-MAX_BODY_LINES).join("\n"),
    truncated: true,
  };
}

export function CommandCard({ model }: { model: CommandCardModel }) {
  const c = useColors();
  const [expanded, setExpanded] = useState(false);
  const st = statusMeta(model, c);

  const out = trimBody(model.stdout);
  const err = trimBody(model.stderr);
  const hasBody = !!(model.stdout || model.stderr);
  const bodyTruncated = out.truncated || err.truncated || model.truncated;

  return (
    <View style={s.wrap}>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={`Command ${model.command}, ${st.label}`}
        onPress={() => setExpanded((v) => !v)}
        style={[s.header, { backgroundColor: c.bgCard, borderColor: c.border }]}
      >
        <Text style={[s.caret, { color: c.textMuted }]}>
          {expanded ? "▼" : "▶"}
        </Text>
        <Text style={[s.dollar, { color: c.textMuted }]}>$</Text>
        <Text
          numberOfLines={expanded ? undefined : 1}
          style={[s.cmd, { color: c.textPrimary }]}
        >
          {model.command}
        </Text>
        <Text style={[s.badge, { color: st.color }]}>
          {st.label}
          {model.durationMs ? ` · ${(model.durationMs / 1000).toFixed(1)}s` : ""}
        </Text>
      </Pressable>

      {expanded && (
        <View
          style={[
            s.body,
            { backgroundColor: c.bgCard, borderColor: c.border },
          ]}
        >
          {model.cwd ? (
            <Text style={[s.meta, { color: c.textMuted }]} numberOfLines={1}>
              cwd: {model.cwd}
            </Text>
          ) : null}
          {bodyTruncated ? (
            <Text style={[s.meta, { color: c.textMuted }]}>
              (output truncated — view full transcript in the chat)
            </Text>
          ) : null}
          {!hasBody ? (
            <Text style={[s.meta, { color: c.textMuted }]}>
              {model.status === "running"
                ? "waiting for output…"
                : "(no output captured)"}
            </Text>
          ) : (
            <ScrollView
              style={s.scroll}
              nestedScrollEnabled
              showsVerticalScrollIndicator
            >
              {out.text ? (
                <Text
                  selectable
                  style={[s.outText, { color: c.textSecondary }]}
                >
                  {out.text}
                </Text>
              ) : null}
              {err.text ? (
                <Text
                  selectable
                  style={[s.outText, { color: c.error }]}
                >
                  {err.text}
                </Text>
              ) : null}
            </ScrollView>
          )}
        </View>
      )}
    </View>
  );
}

const s = StyleSheet.create({
  wrap: { marginVertical: spacing.xs },
  header: {
    flexDirection: "row",
    alignItems: "center",
    borderWidth: StyleSheet.hairlineWidth,
    borderRadius: 8,
    paddingVertical: spacing.sm,
    paddingHorizontal: spacing.md,
    gap: spacing.sm,
  },
  caret: { fontSize: 11 },
  dollar: { fontFamily: monoFamily, fontSize: 13, fontWeight: "700" },
  cmd: { flex: 1, fontFamily: monoFamily, fontSize: 13 },
  badge: { fontFamily: monoFamily, fontSize: 11 },
  body: {
    borderWidth: StyleSheet.hairlineWidth,
    borderTopWidth: 0,
    borderBottomLeftRadius: 8,
    borderBottomRightRadius: 8,
    padding: spacing.md,
    gap: spacing.xs,
  },
  meta: { fontFamily: monoFamily, fontSize: 11 },
  scroll: { maxHeight: 320 },
  outText: { fontFamily: monoFamily, fontSize: 12, lineHeight: 17 },
  panelWrap: { marginTop: spacing.md },
  panelHeader: {
    borderWidth: StyleSheet.hairlineWidth,
    borderRadius: 8,
    paddingVertical: spacing.sm,
    paddingHorizontal: spacing.md,
    marginBottom: spacing.xs,
  },
  panelTitle: { fontFamily: monoFamily, fontSize: 12, fontWeight: "600" },
});

// Foldable "Commands" section: a header (count) that expands to the
// list of CommandCards for a task, newest-run last (chronological).
// Renders nothing when there are no commands, so it's safe to mount
// unconditionally in the chat footer.
export function CommandsPanel({
  models,
}: {
  models?: Record<string, CommandCardModel>;
}) {
  const c = useColors();
  const [open, setOpen] = useState(true);
  const list = Object.values(models || {}).sort(
    (a, b) => a.startedAt - b.startedAt,
  );
  if (list.length === 0) return null;
  const running = list.filter((m) => m.status === "running").length;
  return (
    <View style={s.panelWrap}>
      <Pressable
        accessibilityRole="button"
        onPress={() => setOpen((v) => !v)}
        style={[
          s.panelHeader,
          { backgroundColor: c.bgCard, borderColor: c.border },
        ]}
      >
        <Text style={[s.panelTitle, { color: c.textSecondary }]}>
          {open ? "▼" : "▶"} Commands ({list.length}
          {running ? ` · ${running} running` : ""})
        </Text>
      </Pressable>
      {open &&
        list.map((m) => <CommandCard key={m.id} model={m} />)}
    </View>
  );
}

export default CommandCard;
