"use client";

// Foldable shell-command card for the web dashboard: a clickable header
// showing `$ <command>` + a status badge; click to expand captured
// stdout/stderr. Driven by CommandCardModel (web/lib/command-events.ts)
// accumulated from the task SSE stream.
//
// Self-contained (own expand state + Tailwind), mirrors the
// SchedulesView state-toggle pattern and the surface/* + success/danger
// palette so it can be dropped into the task-stream render without a
// component-lib dependency.

import { useState } from "react";
import type { CommandCardModel } from "../../lib/command-events";

const MAX_BODY_LINES = 400;

function trimBody(s: string): { text: string; truncated: boolean } {
  if (!s) return { text: "", truncated: false };
  const lines = s.split("\n");
  if (lines.length <= MAX_BODY_LINES) return { text: s, truncated: false };
  return { text: lines.slice(-MAX_BODY_LINES).join("\n"), truncated: true };
}

function badge(m: CommandCardModel): { label: string; cls: string } {
  switch (m.status) {
    case "running":
      return { label: "running…", cls: "text-surface-400" };
    case "ok":
      return { label: "exit 0", cls: "text-success-fg" };
    case "error":
      return { label: `exit ${m.exitCode ?? "?"}`, cls: "text-danger-fg" };
    default:
      return { label: "done", cls: "text-surface-400" };
  }
}

export function CommandCard({ model }: { model: CommandCardModel }) {
  const [expanded, setExpanded] = useState(false);
  const b = badge(model);
  const out = trimBody(model.stdout);
  const err = trimBody(model.stderr);
  const hasBody = !!(model.stdout || model.stderr);
  const truncated = out.truncated || err.truncated || model.truncated;

  return (
    <div className="my-1.5">
      <button
        type="button"
        aria-label={`Command ${model.command}, ${b.label}`}
        onClick={() => setExpanded((v) => !v)}
        className="flex w-full items-center gap-2 rounded border border-surface-700 bg-surface-900 px-3 py-2 text-left hover:bg-surface-850"
      >
        <span className="text-[10px] text-surface-400">
          {expanded ? "▼" : "▶"}
        </span>
        <span className="font-mono text-sm font-bold text-surface-400">$</span>
        <span
          className={`flex-1 font-mono text-sm text-surface-100 ${
            expanded ? "whitespace-pre-wrap break-all" : "truncate"
          }`}
        >
          {model.command}
        </span>
        <span className={`font-mono text-[11px] ${b.cls}`}>
          {b.label}
          {model.durationMs
            ? ` · ${(model.durationMs / 1000).toFixed(1)}s`
            : ""}
        </span>
      </button>

      {expanded && (
        <div className="rounded-b border border-t-0 border-surface-700 bg-surface-950/40 px-3 py-2">
          {model.cwd && (
            <div className="truncate font-mono text-[11px] text-surface-400">
              cwd: {model.cwd}
            </div>
          )}
          {truncated && (
            <div className="font-mono text-[11px] text-surface-400">
              (output truncated — full transcript in the task stream)
            </div>
          )}
          {!hasBody ? (
            <div className="font-mono text-[11px] text-surface-400">
              {model.status === "running"
                ? "waiting for output…"
                : "(no output captured)"}
            </div>
          ) : (
            <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all font-mono text-xs leading-relaxed">
              {out.text && (
                <span className="text-surface-200">{out.text}</span>
              )}
              {err.text && (
                <span className="text-danger-fg">{err.text}</span>
              )}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

export default CommandCard;
