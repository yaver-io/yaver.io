"use client";

// designChat.ts — the design-time twin of aiDraft.ts. Turns a natural-language
// layout request ("move quick-add below the list", "let users drag to reorder")
// into a structured DesignPatch[] via the GLM gateway. The host validates and
// applies it through the SAME applyDesign pipeline as a drag — so chat, drag, and
// inspector share one undo stack and one source of truth.
//
// This is the "yaver prompting" path. It mutates the spec, never the DOM, so it
// is inherently collision-free with the app's own drag (see the doc, §6.5/§9.5).

import type { PhoneNodeUi } from "@/lib/agent-client";
import { chatComplete } from "./gateway";

export type DesignPatch =
  | { op: "set"; nodeId: string; props: PhoneNodeUi }
  | { op: "move"; nodeId: string; beforeId: string | null }
  | { op: "enable"; nodeId: string; affordance: "reorder" | "swipe" };

// The curated core node ids the renderer stamps today. Patches outside this set
// are dropped (defensive — a bad draft can't address unknown widgets).
const KNOWN_NODES = ["nav", "title", "quickadd", "list"];

const SYSTEM_PROMPT = `You adjust the LAYOUT of a tiny app. Output ONLY a JSON array of patches — no prose, no markdown fences.
Widget node ids: nav (top tabs), title (header), quickadd (add-row controls), list (rows of records).
Patch shapes:
{"op":"set","nodeId":"quickadd","props":{"marginTop":16}}   // props keys: hidden(bool), marginTop(0-64 number px), title(string), reorderable(bool), swipeDelete(bool)
{"op":"move","nodeId":"quickadd","beforeId":"list"}         // move a node to just before another; beforeId null = move to the end
{"op":"enable","nodeId":"list","affordance":"reorder"}      // affordance: "reorder" or "swipe"
Rules: use ONLY the node ids above; keep marginTop within 0-64; output a strict JSON array only.`;

function extractArray(raw: string): string {
  const trimmed = raw.trim();
  const fence = trimmed.match(/```(?:json)?\s*([\s\S]*?)```/);
  if (fence) return fence[1].trim();
  const start = trimmed.indexOf("[");
  const end = trimmed.lastIndexOf("]");
  if (start >= 0 && end > start) return trimmed.slice(start, end + 1);
  return trimmed;
}

function sanitizeProps(raw: unknown): PhoneNodeUi {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const out: PhoneNodeUi = {};
  if (r.hidden === true) out.hidden = true;
  if (typeof r.marginTop === "number" && isFinite(r.marginTop)) out.marginTop = Math.max(0, Math.min(64, Math.round(r.marginTop)));
  if (typeof r.title === "string" && r.title.trim()) out.title = r.title.trim();
  if (r.reorderable === true) out.reorderable = true;
  if (r.swipeDelete === true) out.swipeDelete = true;
  return out;
}

function sanitize(raw: unknown): DesignPatch | null {
  const p = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const nodeId = typeof p.nodeId === "string" ? p.nodeId : "";
  if (!KNOWN_NODES.includes(nodeId)) return null;
  if (p.op === "set") {
    const props = sanitizeProps(p.props);
    return Object.keys(props).length ? { op: "set", nodeId, props } : null;
  }
  if (p.op === "move") {
    const beforeId = typeof p.beforeId === "string" && KNOWN_NODES.includes(p.beforeId) ? p.beforeId : null;
    return { op: "move", nodeId, beforeId };
  }
  if (p.op === "enable") {
    const aff = p.affordance === "reorder" || p.affordance === "swipe" ? p.affordance : null;
    return aff ? { op: "enable", nodeId, affordance: aff } : null;
  }
  return null;
}

/** Selection context for a scoped prompt — "change THIS", with a widget picked. */
export interface DesignContext {
  nodeId?: string;
  kind?: string;
}

/** Draft a layout change as a validated DesignPatch[]. Throws on auth/limit.
 * When `ctx` names a selected widget, the model resolves "this"/"it"/"the
 * selected" to that node — the select-then-prompt hybrid. */
export async function draftDesignPatch(
  prompt: string,
  token: string,
  ctx?: DesignContext,
  signal?: AbortSignal,
): Promise<DesignPatch[]> {
  const messages: { role: "system" | "user" | "assistant"; content: string }[] = [
    { role: "system", content: SYSTEM_PROMPT },
  ];
  if (ctx?.nodeId && KNOWN_NODES.includes(ctx.nodeId)) {
    messages.push({
      role: "system",
      content:
        `The user has SELECTED the "${ctx.nodeId}"` +
        (ctx.kind ? ` (${ctx.kind})` : "") +
        ` widget. Resolve "this", "it", "here", and "the selected one" to nodeId="${ctx.nodeId}". ` +
        `Target that node unless the request clearly names a different widget.`,
    });
  }
  messages.push({ role: "user", content: prompt });
  const content = await chatComplete({ token, signal, maxTokens: 512, messages });
  let parsed: unknown;
  try {
    parsed = JSON.parse(extractArray(content));
  } catch {
    throw new Error("AI returned an invalid layout change — try rephrasing.");
  }
  if (!Array.isArray(parsed)) throw new Error("AI did not return a layout patch.");
  const patches = parsed.map(sanitize).filter((p): p is DesignPatch => p !== null);
  if (!patches.length) throw new Error("No applicable change — name a widget like the quick-add or list.");
  return patches;
}
