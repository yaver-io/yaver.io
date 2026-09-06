// Last-mile protection for coding-agent diagnostics. Secrets must not appear
// in model progress, UI logs, or provider errors.
const TOKEN_PATTERNS: RegExp[] = [
  /\b(?:sk-ant-|sk-|ghp_|github_pat_|glpat-|xox[baprs]-)[A-Za-z0-9_\-]{12,}\b/g,
  /\bBearer\s+[A-Za-z0-9._~+\-/]+=*/gi,
  /\b(?:api[_-]?key|token|password|secret)\s*[:=]\s*['"]?[^\s,'"}]+/gi,
  /([?&](?:access_token|auth_token|token|api[_-]?key|code)=)[^&#\s]+/gi,
  /\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b/g,
];

export function redactSecrets(value: unknown, secrets: readonly string[] = []): string {
  let text = typeof value === "string" ? value : String(value ?? "");
  for (const secret of secrets) {
    const trimmed = secret.trim();
    if (trimmed.length >= 4) text = text.split(trimmed).join("[REDACTED]");
  }
  for (const pattern of TOKEN_PATTERNS) {
    pattern.lastIndex = 0;
    text = text.replace(pattern, (match) => {
      const separator = match.search(/[:=]/);
      return separator >= 0 ? `${match.slice(0, separator + 1)}[REDACTED]` : "[REDACTED]";
    });
  }
  return text;
}

export function redactValue(value: unknown, secrets: readonly string[] = []): unknown {
  if (typeof value === "string") return redactSecrets(value, secrets);
  if (Array.isArray(value)) return value.map((item) => redactValue(item, secrets));
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, redactValue(item, secrets)]));
  }
  return value;
}

export const redactProgressText = redactSecrets;
