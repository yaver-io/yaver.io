// localAgent/assistantName.ts — the spoken wake name for the on-device
// voice helper, the mobile mirror of desktop/agent/voice_control.go's
// assistantWakeWords. Single source of truth so "Yaver on my phone" and
// "Yaver on my Mac" answer to the same name.
//
// Default is "yaver". The user can rename it ("sam", "feyi", "kole", …) and
// "hey sam, deploy web" is then stripped down to "deploy web" before the
// remainder is routed, exactly like the desktop path. This is free today
// because the wake phrase is transcript *filtering* (any name works with
// zero training) — a future low-power hotword engine would need a trained
// keyword model, so keep this as the authoritative spoken name.
//
// PURE + RN-free (tsx-tested). Persistence (AsyncStorage) lives in the
// sibling adapter assistantNameStore.ts, kept out of this pure core so the
// barrel and the .mts tests never pull in a native dependency.

export const DEFAULT_ASSISTANT_NAME = "yaver";

/** Normalize a raw name: lowercased, trimmed, "" → the default. */
export function effectiveAssistantName(name: string | null | undefined): string {
  const n = (name ?? "").trim().toLowerCase();
  return n === "" ? DEFAULT_ASSISTANT_NAME : n;
}

/** Wake phrases stripped from the front of an utterance, for a given name.
 *  Mirrors assistantWakeWords in voice_control.go exactly. "please" is kept
 *  as a universal politeness filler regardless of name. */
export function assistantWakeWords(name: string | null | undefined): string[] {
  const n = effectiveAssistantName(name);
  return [`hey ${n}`, `ok ${n}`, `okay ${n}`, n, "please"];
}

/** Strip a leading wake phrase so "hey sam, status" and "status" route
 *  identically. Returns "" when the utterance is only the wake word. */
export function stripWakeWord(utterance: string, name: string | null | undefined): string {
  let t = utterance.trim().toLowerCase().replace(/[.!?,]+$/, "").trim();
  for (const w of assistantWakeWords(name)) {
    if (t === w) return "";
    if (t.startsWith(w + " ") || t.startsWith(w + ", ")) {
      t = t.slice(w.length).replace(/^,/, "").trim();
      break;
    }
  }
  return t.trim();
}

const COMMON_WORDS = new Set([
  "yes", "no", "ok", "okay", "stop", "go", "run", "the", "and",
  "hey", "please", "do", "it", "now", "up",
]);

/** Advisory (non-blocking) when a chosen name is prone to STT false triggers:
 *  too short to anchor on, or a common word. "" = looks fine. Mirrors
 *  assistantNameWarning in voice_control.go. */
export function assistantNameWarning(name: string | null | undefined): string {
  const n = (name ?? "").trim().toLowerCase();
  if (n === "" || n === DEFAULT_ASSISTANT_NAME) return "";
  if ([...n].length < 3) {
    return `"${n}" is very short — speech-to-text may mis-hear it. Prefer the "hey ${n}, …" form or a longer name.`;
  }
  if (COMMON_WORDS.has(n)) {
    return `"${n}" is a common word — it'll trigger during normal speech. Pick a more distinctive name.`;
  }
  return "";
}
