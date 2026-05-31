// localAgent/resolver.ts — deterministic device resolver for the on-device
// voice helper.
//
// Whisper-tiny transcripts are noisy and users speak loosely ("my hetzner
// box", "the mac", "switch the linux one"). The voice router must NEVER act
// on a guessed device — so resolution is deterministic and ranked, and it
// returns an explicit `ambiguous` result the caller turns into a spoken
// "which one?" instead of picking blindly.
//
// PURE + RN-free: depends only on the DeviceRef shape below, so it unit-tests
// under tsx and carries zero React/native imports. The mobile app maps its
// DeviceContext devices → DeviceRef[] before calling.
//
// Ranking (first decisive hit wins):
//   1. special tokens: "primary" / "secondary" / "this phone"
//   2. exact alias (case-insensitive)              ← auto-seeded slugs live here
//   3. exact deviceId / id-prefix (>=6 chars)
//   4. exact name (case-insensitive)
//   5. token-overlap fuzzy over alias+name+platform keywords
// A tie at the fuzzy stage → ambiguous (ask the user), never a coin-flip.

export interface DeviceRef {
  deviceId: string;
  name: string;
  alias?: string;
  /** "macos" | "linux" | "windows" | "android" | "ios" | other */
  platform?: string;
  online?: boolean;
  isPrimary?: boolean;
  isSecondary?: boolean;
  isPhone?: boolean;
}

export type ResolveOutcome =
  | { kind: "resolved"; device: DeviceRef; matchedBy: ResolveMatch; score: number }
  | { kind: "ambiguous"; candidates: DeviceRef[]; query: string }
  | { kind: "none"; query: string };

export type ResolveMatch =
  | "primary"
  | "secondary"
  | "this-phone"
  | "alias"
  | "id"
  | "id-prefix"
  | "name"
  | "fuzzy";

// Filler words to strip from a spoken device reference before matching.
const STOPWORDS = new Set([
  "my", "the", "a", "an", "to", "on", "box", "machine", "device", "computer",
  "server", "one", "please", "switch", "connect", "go", "use", "set", "make",
  "primary", "main", "remote", "dev",
]);

// Map common spoken platform words to the platform enum so "the linux one"
// can match a device whose alias/name doesn't literally contain "linux".
const PLATFORM_WORDS: Record<string, string> = {
  mac: "macos",
  macos: "macos",
  macbook: "macos",
  imac: "macos",
  linux: "linux",
  ubuntu: "linux",
  debian: "linux",
  pi: "linux",
  raspberry: "linux",
  windows: "windows",
  pc: "windows",
  android: "android",
  iphone: "ios",
  ipad: "ios",
};

function normalize(s: string): string {
  return (s || "").toLowerCase().replace(/[^a-z0-9\s-]/g, " ").replace(/\s+/g, " ").trim();
}

function tokens(s: string): string[] {
  return normalize(s)
    .split(/[\s-]+/)
    .filter((w) => w && !STOPWORDS.has(w));
}

/**
 * Resolve a free-form (possibly spoken) device reference against the user's
 * devices. Deterministic and tie-safe.
 */
export function resolveDevice(query: string, devices: DeviceRef[]): ResolveOutcome {
  const raw = (query || "").trim();
  const norm = normalize(raw);
  if (!norm) return { kind: "none", query: raw };

  // 1. special tokens
  if (/\b(this phone|my phone)\b/.test(norm)) {
    const phone = devices.find((d) => d.isPhone);
    if (phone) return { kind: "resolved", device: phone, matchedBy: "this-phone", score: 1 };
  }
  if (/\b(primary|main)\b/.test(norm)) {
    const p = devices.find((d) => d.isPrimary);
    if (p) return { kind: "resolved", device: p, matchedBy: "primary", score: 1 };
  }
  if (/\bsecondary\b/.test(norm)) {
    const s = devices.find((d) => d.isSecondary);
    if (s) return { kind: "resolved", device: s, matchedBy: "secondary", score: 1 };
  }

  const qTokens = tokens(raw);

  // 2. exact alias (full normalized string OR any single token equals an alias)
  for (const d of devices) {
    const a = (d.alias || "").toLowerCase();
    if (a && (a === norm || qTokens.includes(a))) {
      return { kind: "resolved", device: d, matchedBy: "alias", score: 1 };
    }
  }

  // 3. exact deviceId / id-prefix
  const byId = devices.find((d) => d.deviceId.toLowerCase() === norm);
  if (byId) return { kind: "resolved", device: byId, matchedBy: "id", score: 1 };
  if (norm.length >= 6) {
    const pref = devices.filter((d) => d.deviceId.toLowerCase().startsWith(norm));
    if (pref.length === 1) return { kind: "resolved", device: pref[0], matchedBy: "id-prefix", score: 1 };
    if (pref.length > 1) return { kind: "ambiguous", candidates: pref, query: raw };
  }

  // 4. exact name
  const byName = devices.find((d) => d.name.toLowerCase() === norm);
  if (byName) return { kind: "resolved", device: byName, matchedBy: "name", score: 1 };

  // 5. fuzzy: score token overlap against alias + name + platform keyword.
  const scored = devices
    .map((d) => ({ d, score: fuzzyScore(qTokens, d) }))
    .filter((x) => x.score > 0)
    .sort((a, b) => b.score - a.score);

  if (scored.length === 0) return { kind: "none", query: raw };
  // Decisive winner: top score strictly beats the runner-up.
  if (scored.length === 1 || scored[0].score > scored[1].score) {
    return { kind: "resolved", device: scored[0].d, matchedBy: "fuzzy", score: scored[0].score };
  }
  // Tie → ask, don't guess. Return only the top-scoring tied candidates.
  const top = scored[0].score;
  return { kind: "ambiguous", candidates: scored.filter((x) => x.score === top).map((x) => x.d), query: raw };
}

function fuzzyScore(qTokens: string[], d: DeviceRef): number {
  if (qTokens.length === 0) return 0;
  const hay = new Set<string>();
  tokens(d.name).forEach((t) => hay.add(t));
  if (d.alias) tokens(d.alias).forEach((t) => hay.add(t));
  let score = 0;
  for (const q of qTokens) {
    if (hay.has(q)) {
      score += 2; // direct token hit on name/alias
      continue;
    }
    // platform-word match: "linux"/"mac"/etc → device.platform
    const plat = PLATFORM_WORDS[q];
    if (plat && d.platform === plat) score += 2;
    // partial substring hit (e.g. "hetz" → "hetzner")
    else if ([...hay].some((h) => h.includes(q) || q.includes(h))) score += 1;
  }
  // Tiny tie-breaker: prefer an online device when scores would otherwise tie.
  if (score > 0 && d.online) score += 0.1;
  return score;
}
