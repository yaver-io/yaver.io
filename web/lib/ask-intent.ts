// Ask-intent auto-detection for the web console — a TypeScript mirror of the
// agent's detectAskIntent (desktop/agent/ask_intent.go). When a user types a
// natural-language QUESTION into the console ("how do I test STT/TTS?", "where
// does auth get wired?") with no yaver verb/command in it, the intent is to
// understand, not to change anything, so the console routes it through ask
// mode (deep grounded analysis, file:line cites, explain-first with a confirm
// gate) instead of a work run.
//
// HIGH PRECISION by design: a false positive sends a genuine work instruction
// down the read-only explain path, which is annoying, so we only flip to ask
// when there is a clear question signal AND the line is not a command.
// Imperative work prose ("add a dark-mode toggle") stays a work task.
//
// Keep this in sync with desktop/agent/ask_intent.go.

const QUESTION_STARTERS = new Set([
  "how", "what", "whats", "what's", "why", "where", "when", "who", "which",
  "can", "could", "should", "would", "does", "do", "is", "are",
  "explain", "describe",
]);

const QUESTION_PHRASES = [
  "how do i", "how would i", "how can i", "how to", "how does",
  "what is", "what's the", "what are", "what does", "what happens",
  "where is", "where do", "where does", "why is", "why does", "why do",
  "is there", "are there", "can i", "tell me", "walk me through",
];

const YAVER_VERBS = new Set([
  "yaver", "ops", "create_task", "yaver_ask", "exec_command",
  "cloud_deploy", "cloud_destroy", "deploy_run", "git_push",
  "build_ios", "build_android", "native_build", "wire_push",
  "wireless_push", "convex_deploy", "cf_deploy", "switch_run",
  "vault", "serve",
]);

function mentionsYaverVerb(fields: string[]): boolean {
  for (const raw of fields) {
    const t = raw.toLowerCase().trim();
    if (!t) continue;
    if (YAVER_VERBS.has(t)) return true;
    if (YAVER_VERBS.has(t.replace(/-/g, "_"))) return true;
  }
  return false;
}

/** Returns true when a console line is a natural-language question (an "ask
 *  case") rather than a command / work instruction. Conservative: when in
 *  doubt, returns false. */
export function detectAskIntent(input: string): boolean {
  const line = (input || "").trim();
  if (!line) return false;

  // Explicit command sigils — never an ask.
  if ("/$!-".includes(line[0])) return false;

  const fields = line.split(/\s+/).filter(Boolean);
  if (fields.length < 2) return false; // single token = name / verb / command

  if (mentionsYaverVerb(fields)) return false;

  const lower = line.toLowerCase();

  if (lower.replace(/[ \t]+$/, "").endsWith("?")) return true;

  for (const p of QUESTION_PHRASES) {
    if (lower.startsWith(p)) return true;
  }

  if (QUESTION_STARTERS.has(fields[0].toLowerCase())) return true;

  return false;
}

const BREADTH_SIGNALS = [
  "architecture", "architect", "end to end", "end-to-end", "across",
  "data flow", "control flow", "lifecycle", "wired", "wire up", "wired up",
  "pipeline", "interact", "overall", "entire", "whole system", "trace",
  "why does", "why do", "what happens when", "difference between", "compare",
  "relationship", "big picture", "from scratch", "step by step", "all the ways",
];

/** Returns true when an ask-case question is broad/architectural enough to
 *  warrant escalating from a single read-only agent to a multi-agent graph
 *  (investigate → answer → verify). Mirror of detectAskBreadth in
 *  desktop/agent/ask_intent.go. */
export function detectAskBreadth(question: string): boolean {
  const q = (question || "").trim().toLowerCase();
  if (!q) return false;
  for (const sig of BREADTH_SIGNALS) {
    if (q.includes(sig)) return true;
  }
  return q.split(/\s+/).filter(Boolean).length >= 16;
}
