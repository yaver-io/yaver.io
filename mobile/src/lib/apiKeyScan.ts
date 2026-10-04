export type ScannedAPIKey = {
  apiKey: string;
  provider?: string;
};

// Accept a plain key, a small JSON handoff, or a yaver/provider URL. The value
// is returned in memory only; callers send it directly to the selected agent
// and must never put it in Convex, analytics, logs, or local storage.
export function parseScannedAPIKey(rawValue: string): ScannedAPIKey | null {
  const raw = String(rawValue || "").trim();
  if (!raw || raw.length > 16_384) return null;

  if (raw.startsWith("{")) {
    try {
      const parsed = JSON.parse(raw) as Record<string, unknown>;
      const apiKey = String(parsed.apiKey ?? parsed.key ?? "").trim();
      const provider = String(parsed.provider ?? "").trim().toLowerCase();
      return validKey(apiKey) ? { apiKey, ...(provider ? { provider } : {}) } : null;
    } catch {
      return null;
    }
  }

  try {
    const url = new URL(raw);
    const apiKey = String(url.searchParams.get("apiKey") || url.searchParams.get("key") || "").trim();
    const provider = String(url.searchParams.get("provider") || "").trim().toLowerCase();
    if (validKey(apiKey)) return { apiKey, ...(provider ? { provider } : {}) };
  } catch {
    // A provider key is commonly encoded as the entire QR payload.
  }

  return validKey(raw) ? { apiKey: raw } : null;
}

// OCR sees the surrounding desktop UI as well as the secret, and may wrap a
// long key across whitespace. Prefer an explicit "API key:" line, then score
// individual key-like tokens. We never guess/repair ambiguous OCR characters:
// the caller fills an editable field so the user can verify before saving.
export function parseRecognizedAPIKeyText(rawText: string): ScannedAPIKey | null {
  const raw = String(rawText || "").trim();
  if (!raw || raw.length > 64_000) return null;
  const direct = parseScannedAPIKey(raw);
  if (direct) return direct;

  const lines = raw.split(/[\r\n]+/).map((line) => line.trim()).filter(Boolean);
  for (const line of lines) {
    const labelled = line.match(/(?:api[ _-]?key|secret|token)\s*[:=]\s*(.+)$/i)?.[1];
    if (!labelled) continue;
    const compact = labelled.replace(/[\s"'`]+/g, "");
    if (validKey(compact)) return { apiKey: compact };
  }

  const candidates = raw.match(/[A-Za-z0-9][A-Za-z0-9._:+\/=\-]{11,16383}/g) || [];
  const plausible = candidates
    .filter(validKey)
    .filter((value) => /[A-Za-z]/.test(value) && /[0-9._:+\/=\-]/.test(value))
    .sort((left, right) => keyScore(right) - keyScore(left));
  return plausible[0] ? { apiKey: plausible[0] } : null;
}

// Hetzner Cloud API tokens are a single 64-byte ASCII value. Object Storage
// credentials are a different access-key/secret-key pair and intentionally do
// not pass this parser: S3 credentials cannot manage Cloud VPS resources.
export function parseHetznerCloudTokenText(rawText: string): ScannedAPIKey | null {
  const raw = String(rawText || "").trim();
  if (!raw || raw.length > 64_000) return null;

  const structured = parseScannedAPIKey(raw);
  if (structured && (!structured.provider || structured.provider === "hetzner") && isHetznerCloudToken(structured.apiKey)) {
    return { apiKey: structured.apiKey, provider: "hetzner" };
  }

  const lines = raw.split(/[\r\n]+/).map((line) => line.trim()).filter(Boolean);
  for (const line of lines) {
    const labelled = line.match(/(?:cloud[ _-]?)?(?:api[ _-]?)?token\s*[:=]\s*(.+)$/i)?.[1];
    if (!labelled) continue;
    const compact = labelled.replace(/[\s"'`]+/g, "");
    if (isHetznerCloudToken(compact)) return { apiKey: compact, provider: "hetzner" };
  }

  const exactCandidates = raw.match(/[A-Za-z0-9_-]{64}/g) || [];
  return exactCandidates.length === 1
    ? { apiKey: exactCandidates[0], provider: "hetzner" }
    : null;
}

function isHetznerCloudToken(value: string): boolean {
  return /^[A-Za-z0-9_-]{64}$/.test(value);
}

function keyScore(value: string): number {
  const knownPrefix = /^(?:sk-|ds-|gsk_|hf_|xai-|AIza|key-)/i.test(value) ? 10_000 : 0;
  return knownPrefix + Math.min(value.length, 4096);
}

function validKey(value: string): boolean {
  return value.length >= 12 && value.length <= 16_384 && !/[\r\n\t ]/.test(value);
}
