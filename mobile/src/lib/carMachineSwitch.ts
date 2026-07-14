/**
 * carMachineSwitch.ts — pick the target machine BY VOICE.
 *
 * Why this has to exist: on CarPlay there is no picker. Apple's voice-based-
 * conversation category forbids showing text or lists in response to a query,
 * so the driver cannot be handed a menu of boxes to tap — the phone screen's
 * device picker simply isn't reachable from the car. "Switch to pokayoke" is
 * therefore the ONLY way to retarget a turn while driving, which makes this a
 * load-bearing part of the CarPlay surface rather than a convenience.
 *
 * Pure + dependency-free so it can be unit-tested with `npx tsx`, matching the
 * house style of carVoiceConfirm.ts / carSurfaceIntent.ts.
 *
 * Matching is deliberately forgiving. STT mangles machine names constantly —
 * "pokayoke" comes back as "poka yoke", "poke a yoke", "pokayoka". A driver who
 * has to enunciate a hostname three times is a driver who is not watching the
 * road. We accept a loose match and SPEAK BACK the machine we chose, so a wrong
 * guess is caught by ear rather than silently running work on the wrong box.
 */

export interface MachineLike {
  /** Stable id (deviceId). */
  id: string;
  /** Whatever the user sees: nickname, alias, hostname. */
  name: string;
  /** Optional extra names to match against (alias, hostname). */
  aliases?: string[];
}

export interface MachineSwitchRequest {
  /** The spoken machine name, as heard. */
  spokenName: string;
}

/**
 * Detect "switch to X" / "use X" / "run this on X" — and the Turkish forms,
 * since the driver is in Turkey and will code-switch mid-sentence.
 *
 * Returns null when the utterance isn't a machine switch, so the caller falls
 * through to the normal coding/ops dispatch.
 */
export function classifyMachineSwitch(text: string): MachineSwitchRequest | null {
  const clean = (text || "").trim();
  if (!clean) return null;

  const patterns: RegExp[] = [
    // English: "switch to pokayoke", "use the mac mini", "run it on primary",
    // "connect to my box", "talk to pokayoke"
    /\b(?:switch|change|move|connect|talk|point)\s+(?:it\s+|this\s+)?(?:to|over to)\s+(.+)$/i,
    /\b(?:use|select|pick|choose)\s+(?:the\s+|my\s+)?(.+)$/i,
    /\b(?:run|do)\s+(?:it|this|that)\s+on\s+(.+)$/i,
    // Turkish: "pokayoke'ye geç", "mac mini'yi kullan".
    // No \b after the verb — JS word boundaries are ASCII-only, so "geç" would
    // never match (ç is not a \w character). Anchor on end-of-string instead.
    /^(.+?)['’]?(?:ye|ya|e|a)\s+ge[çc]\s*$/i,
    /^(.+?)['’]?(?:yi|yı|i|ı)\s+kullan\s*$/i,
  ];

  for (const re of patterns) {
    const m = clean.match(re);
    if (!m) continue;
    const spokenName = cleanupSpokenName(m[1]);
    if (spokenName) return { spokenName };
  }
  return null;
}

/**
 * Resolve a spoken name to an actual machine. Returns null when nothing is a
 * plausible match — the caller must then SAY it didn't find it rather than
 * silently dispatching to whatever box happened to be selected.
 */
export function matchMachine(
  spokenName: string,
  machines: MachineLike[],
): MachineLike | null {
  const target = normalize(spokenName);
  if (!target || machines.length === 0) return null;

  let best: { m: MachineLike; score: number } | null = null;
  for (const m of machines) {
    const candidates = [m.name, ...(m.aliases || [])].filter(Boolean);
    for (const c of candidates) {
      const score = similarity(target, normalize(c));
      if (score > 0 && (!best || score > best.score)) best = { m, score };
    }
  }
  // 0.6 is the floor for "close enough to act on". Below that we'd rather admit
  // we didn't catch it than run a build on the wrong machine.
  if (!best || best.score < 0.6) return null;
  return best.m;
}

/** What the car says back. Always names the machine, so a misheard pick is caught by ear. */
export function spokenForMachineSwitch(
  machine: MachineLike | null,
  spokenName: string,
): string {
  if (!machine) {
    return `I couldn't find a machine called ${spokenName}. Say the name again, or pick it on your phone.`;
  }
  return `Switched to ${machine.name}.`;
}

// ── internals ────────────────────────────────────────────────────────────────

/** Strip filler the STT tends to append: "pokayoke please", "pokayoke box". */
function cleanupSpokenName(raw: string): string {
  return (raw || "")
    .replace(/\b(please|now|thanks|thank you|box|machine|computer|server|l[üu]tfen)\b/gi, " ")
    .replace(/[.,!?]+$/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/** Lowercase, strip everything that isn't a letter or digit. "Mac Mini" → "macmini". */
function normalize(s: string): string {
  return (s || "")
    .toLowerCase()
    .replace(/[^a-z0-9çğıöşü]+/gi, "");
}

/**
 * Similarity in [0,1]. Exact and containment score highest; otherwise fall back
 * to a character-bigram Dice coefficient, which handles the way STT garbles a
 * name ("pokayoka" vs "pokayoke") far better than an edit-distance threshold.
 */
function similarity(a: string, b: string): number {
  if (!a || !b) return 0;
  if (a === b) return 1;
  if (a.includes(b) || b.includes(a)) return 0.9;

  const bigrams = (s: string): string[] => {
    const out: string[] = [];
    for (let i = 0; i < s.length - 1; i++) out.push(s.slice(i, i + 2));
    return out;
  };
  const A = bigrams(a);
  const B = bigrams(b);
  if (A.length === 0 || B.length === 0) return 0;

  const pool = [...B];
  let hits = 0;
  for (const g of A) {
    const idx = pool.indexOf(g);
    if (idx >= 0) {
      hits++;
      pool.splice(idx, 1);
    }
  }
  return (2 * hits) / (A.length + B.length);
}
