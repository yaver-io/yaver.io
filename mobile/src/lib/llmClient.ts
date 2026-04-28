// llmClient.ts — phone-side BYOK LLM client. Slice 3 of the
// phone-first dev stack (docs/phone-first-dev-stack.md).
//
// What this gives the editor screen: a way to ask Claude / Codex /
// any future provider to edit the project's `src/` tree without
// going through a desktop agent. The phone holds the API key in
// expo-secure-store, sends a tool-use request to the provider, and
// receives a structured EditPlan it can preview, apply, or reject.
//
// Provider implementations (currently just Anthropic) live in
// sibling files (llmAnthropic.ts). They satisfy the LlmProvider
// interface defined here. OpenAI / xAI / etc. plug in the same way.
//
// This file is pure logic + types — no native deps, no expo, no
// fetch import. The provider supplies its own fetchImpl so the
// same code path runs in production RN, mobile-headless tests, and
// any other host.

import type { PhoneSchema } from "./phoneProjects";

/** A single edit the LLM proposes. The phone applies the plan via
 *  applyEditPlan() against a SandboxSource-shaped target. */
export interface FileEdit {
  action: "create" | "update" | "delete";
  /** Posix-relative path inside the project's `src/` tree. The
   *  source store re-validates this on every write/delete, so a
   *  malicious or buggy LLM response can't escape the project root.
   *  Validation happens at apply time, not at parse time, because
   *  we want to *preview* the plan even when one entry is dodgy. */
  path: string;
  /** Required for create/update. Ignored for delete. */
  content?: string;
  /** Optional per-edit reason for the diff viewer. */
  reason?: string;
}

/** EditPlan is what every provider returns. Shape is provider-
 *  agnostic so callers don't have to switch on the model. */
export interface EditPlan {
  /** The model's overall reasoning. Shown in the diff viewer
   *  before the user applies. Plain text, no markdown formatting
   *  promised. */
  rationale: string;
  edits: FileEdit[];
  /** Diagnostic counters. Optional — providers fill what they know. */
  inputTokens?: number;
  outputTokens?: number;
  /** Provider-specific opaque debug payload. Don't log it; some
   *  providers echo the system prompt back. */
  debug?: unknown;
}

/** Snapshot of a single file passed in the request. */
export interface FileSnapshot {
  path: string;
  content: string;
}

export interface EditFilesRequest {
  /** Natural-language instruction from the user. */
  prompt: string;
  /** All files the model can read AND modify. The phone caller is
   *  responsible for keeping the size sane — early projects will be
   *  a few KB total, well within any modern model's context window. */
  files: FileSnapshot[];
  /** Optional context the model uses to produce more relevant
   *  edits. Free-form; providers stuff this into the system prompt. */
  framework?: string;
  /** The phone-project's backend schema, so a model can wire CRUD
   *  calls correctly without inventing tables. */
  schema?: PhoneSchema;
  /** Cap the request's wall-clock duration. Providers honour this
   *  via AbortController. Default 60s. */
  timeoutMs?: number;
}

/** The contract every LLM provider satisfies. Stateless — every call
 *  is independent. */
export interface LlmProvider {
  /** Stable label so the UI can render which model produced an
   *  edit plan (e.g. "claude-opus-4-7"). */
  readonly model: string;
  /** Slug-safe provider id. */
  readonly id: string;
  editFiles(req: EditFilesRequest): Promise<EditPlan>;
}

/** ApplyTarget is the subset of the source store applyEditPlan
 *  needs. Tests pass a mock; production passes phoneSandboxSourceDefault. */
export interface ApplyTarget {
  writeSourceFile(slug: string, relPath: string, content: string): Promise<void>;
  deleteSourceFile(slug: string, relPath: string): Promise<void>;
}

export interface ApplyEditPlanResult {
  applied: FileEdit[];
  /** Per-edit failures: the LLM proposed something the source store
   *  rejected. Most often UnsafeSourcePathError on a bad path, or a
   *  delete with no `path`. The applied edits ARE persisted; this
   *  is a partial-success contract. */
  skipped: Array<{ edit: FileEdit; reason: string }>;
}

/** applyEditPlan walks the plan and persists each edit through the
 *  given ApplyTarget. It is intentionally lenient on per-edit
 *  failures so the user sees as much progress as possible — a
 *  single bogus path does not cancel the rest. */
export async function applyEditPlan(
  slug: string,
  plan: EditPlan,
  target: ApplyTarget,
): Promise<ApplyEditPlanResult> {
  const applied: FileEdit[] = [];
  const skipped: Array<{ edit: FileEdit; reason: string }> = [];
  for (const edit of plan.edits) {
    try {
      switch (edit.action) {
        case "create":
        case "update": {
          if (typeof edit.content !== "string") {
            skipped.push({ edit, reason: `${edit.action} edit missing 'content'` });
            continue;
          }
          await target.writeSourceFile(slug, edit.path, edit.content);
          applied.push(edit);
          break;
        }
        case "delete": {
          await target.deleteSourceFile(slug, edit.path);
          applied.push(edit);
          break;
        }
        default: {
          skipped.push({ edit, reason: `unknown action ${JSON.stringify(edit.action)}` });
        }
      }
    } catch (e: any) {
      skipped.push({ edit, reason: String(e?.message ?? e) });
    }
  }
  return { applied, skipped };
}

/** Tight token-budget guard: refuse before sending if the inlined
 *  files would obviously blow past a sane request size. Each
 *  provider can lift the cap; this is the conservative floor we
 *  enforce in the shared layer.
 *
 *  Returns the total content size. Throws if it exceeds maxBytes
 *  (default 200 KB — fits comfortably under every modern model's
 *  context with room left for the prompt and the response).
 */
export function assertRequestSize(req: EditFilesRequest, maxBytes = 200_000): number {
  // TextEncoder is available in modern RN (Hermes) AND every Node /
  // Bun host. Using Buffer would tie us to Node only.
  const encoder = new TextEncoder();
  let total = 0;
  for (const f of req.files) {
    total += encoder.encode(f.content).byteLength;
  }
  total += encoder.encode(req.prompt).byteLength;
  if (total > maxBytes) {
    throw new Error(
      `EditFilesRequest exceeds ${maxBytes} bytes (got ${total}). Trim files before retrying.`,
    );
  }
  return total;
}

/** Renders the edit plan to a single human-readable diff blob.
 *  Useful for the editor-screen "Preview" panel: shows rationale,
 *  then per-file action + size delta. Pure formatter, no I/O. */
export function formatEditPlan(plan: EditPlan): string {
  const lines: string[] = [];
  if (plan.rationale.trim()) {
    lines.push(plan.rationale.trim());
    lines.push("");
  }
  for (const edit of plan.edits) {
    const size = edit.action === "delete" ? "-" : `${(edit.content ?? "").length}b`;
    lines.push(`${edit.action.toUpperCase()} ${edit.path} (${size})`);
    if (edit.reason) lines.push(`  ${edit.reason}`);
  }
  if (plan.inputTokens != null || plan.outputTokens != null) {
    lines.push("");
    lines.push(`tokens: ${plan.inputTokens ?? "?"} in, ${plan.outputTokens ?? "?"} out`);
  }
  return lines.join("\n");
}
