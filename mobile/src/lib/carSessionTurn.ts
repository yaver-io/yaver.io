/**
 * carSessionTurn.ts — the LIVE-SESSION dispatch path for the car surface.
 *
 * docs/yaver-car-surface.md §7 build order #2: "point carReplyDispatch.ts at
 * /runner/session/turn." The old path (dispatchAndSummarize) spawns a NEW task
 * via /watch/turn; this one drives the session the user already has running —
 * "keep developing this" means the ubuntu session, not a fresh task.
 *
 * The session endpoint (POST /runner/session/turn, shipped 2026-07-10) is
 * synchronous: it sends the prompt, waits `waitMs`, reads the pane, and returns.
 * There is no task to poll. The response carries:
 *   - `pane`: plain text (ANSI stripped) for the TV/phone to render
 *   - `awaitingChoice`: true when the pane is showing a menu
 *   - `options[]`: the menu options when awaitingChoice
 *
 * For the CAR, the awaitingChoice handling is voice-only (the driver can't read
 * a screen): speak the options as a question, map the driver's spoken reply
 * ("yes"/"one"/"1") to a choice digit, and loop. Menus chain and renumber —
 * never assume option 1 means yes (docs/yaver-car-surface.md §4.1).
 *
 * Pure + dependency-injected so it unit-tests under `npx tsx` with no RN.
 */

// ── Types (mirror desktop/agent/runner_session_turn.go) ──────────────

export interface SessionTurnResult {
  ok: boolean;
  session: string;
  runner?: string;
  /** "prompt" or "choice" — what we actually delivered. */
  sent?: string;
  /** True when the pane is (still) on a menu. The caller must answer with
   *  `choice` before any prompt will be accepted. */
  awaitingChoice: boolean;
  options?: string[];
  /** Plain text pane tail (ANSI stripped). Enough for a TV to render and
   *  for a car to summarize. */
  pane?: string;
  error?: string;
}

/** The session-turn dep. Injectable so tests can mock the endpoint. */
export type SessionTurnDep = (
  text: string | null,
  choice: string | null,
) => Promise<SessionTurnResult>;

// ── Choice parsing ───────────────────────────────────────────────────

/**
 * Map a spoken word/phrase to a bare option digit the session endpoint accepts
 * (`isTmuxChoiceAnswer` = `^\s*\d{1,2}\s*$`). Returns null when the utterance
 * doesn't look like a choice.
 *
 * Handles: bare digits ("1"), number words ("one", "two"), and common
 * yes/no/confirm/cancel phrases a driver would naturally say.
 */
export function parseSpokenChoice(text: string): string | null {
  const t = text.trim().toLowerCase().replace(/[.!]+$/, "");
  if (!t) return null;

  // Bare digit(s).
  if (/^\d{1,2}$/.test(t)) return t;

  // Number words.
  const words: Record<string, string> = {
    one: "1", two: "2", three: "3", four: "4", five: "5",
    six: "6", seven: "7", eight: "8", nine: "9",
    first: "1", second: "2", third: "3",
    // Common menu shapes: yes/confirm → 1, no/cancel → 2.
    yes: "1", yeah: "1", yep: "1", confirm: "1", confirmed: "1",
    no: "2", nope: "2", cancel: "2", exit: "2",
  };
  return words[t] ?? null;
}

// ── Pane summarization (mirrors watch_risk.go::summarizeForWatch) ────

const CODE_PATTERN = /[{}<>;=]|```|\b(function|const|class|def|import|return)\b|\/\w+\//;
const SENTENCE_PATTERN = /^(.{1,160}?[.!?])(\s|$)/;

/**
 * Pull the first short, status-shaped clause out of a pane tail. Refuses
 * code/markup/path-dump lines (the car must never speak code). Clamps to
 * READBACK_MAX_CHARS (200) — the same ceiling the car loop uses.
 */
export function summarizeSessionPane(pane: string): string {
  if (!pane) return "Done.";
  const lines = pane.split(/\r?\n/);
  for (const raw of lines) {
    const line = raw.trim();
    if (!line) continue;
    if (CODE_PATTERN.test(line)) continue;
    // First sentence only.
    const m = line.match(SENTENCE_PATTERN);
    const clause = (m ? m[1] : line).replace(/[#*`_~]/g, "").trim();
    if (clause) return clampSentence(clause);
  }
  return "Done.";
}

function clampSentence(s: string): string {
  const t = s.replace(/\s+/g, " ").trim();
  if (t.length <= 200) return t;
  return t.slice(0, 199).trimEnd() + "…";
}

// ── Core: drive a live session turn ──────────────────────────────────

export interface CarSessionTurnResult {
  /** The one-sentence line to speak over car audio. */
  spoken: string;
  /** The session name (for display). */
  session?: string;
  /** True when the pane is showing a menu — the driver needs to answer. */
  awaitingChoice: boolean;
  /** The options when awaitingChoice (for the caller to speak/show). */
  options: string[];
  /** The full pane (for the phone screen / TV). */
  pane: string;
  error?: string;
}

/**
 * Drive one turn of the live coding session from a car reply.
 *
 * Flow:
 *   - If there's a pending choice (awaitingChoice) and the text looks like a
 *     number/yes/no → send as {choice}.
 *   - Otherwise → send as {text} (a prompt).
 *   - Map the response:
 *     awaitingChoice: true  → speak the options as a question
 *     awaitingChoice: false → speak a one-sentence summary of the pane
 *     error                → speak the error
 *
 * Never throws — errors become a spoken line so the car always says something.
 */
export async function dispatchSessionTurn(
  text: string,
  sessionTurn: SessionTurnDep,
  pendingChoice: boolean,
): Promise<CarSessionTurnResult> {
  const clean = text.trim();
  if (!clean) {
    return {
      spoken: "I didn't catch that.",
      awaitingChoice: false,
      options: [],
      pane: "",
    };
  }

  // If we were awaiting a choice, try to map the utterance to a digit.
  let choice: string | null = null;
  let prompt: string | null = clean;
  if (pendingChoice) {
    choice = parseSpokenChoice(clean);
    if (choice) prompt = null;
  }

  let result: SessionTurnResult;
  try {
    result = await sessionTurn(prompt, choice);
  } catch (e) {
    return {
      spoken: "I couldn't reach your box.",
      awaitingChoice: false,
      options: [],
      pane: "",
      error: e instanceof Error ? e.message : String(e),
    };
  }

  if (result.awaitingChoice) {
    const options = result.options ?? [];
    const spoken = options.length > 0
      ? `Choose: ${options.slice(0, 4).map((o) => o.trim().slice(0, 60)).join(". ")}.`
      : "Pick an option.";
    return {
      spoken,
      session: result.session,
      awaitingChoice: true,
      options,
      pane: result.pane ?? "",
    };
  }

  if (!result.ok || (result.error && !result.pane)) {
    return {
      spoken: result.error || "Something went wrong.",
      session: result.session,
      awaitingChoice: false,
      options: [],
      pane: result.pane ?? "",
      error: result.error,
    };
  }

  // Not awaiting a choice → summarize the pane to one sentence.
  return {
    spoken: summarizeSessionPane(result.pane ?? ""),
    session: result.session,
    awaitingChoice: false,
    options: [],
    pane: result.pane ?? "",
  };
}
