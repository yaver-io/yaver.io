export interface ImportedConversationBrief {
  sourceLabel: string;
  sourceUrl?: string;
  title?: string;
  suggestedName?: string;
  prompt: string;
  charCount: number;
}

const SOURCE_PATTERNS: Array<{ re: RegExp; label: string }> = [
  { re: /https?:\/\/claude\.ai\/share\/[^\s)]+/i, label: "Claude share" },
  { re: /https?:\/\/chatgpt\.com\/share\/[^\s)]+/i, label: "ChatGPT share" },
  { re: /https?:\/\/chat\.openai\.com\/share\/[^\s)]+/i, label: "ChatGPT share" },
  { re: /https?:\/\/chatgpt\.com\/c\/[^\s)]+/i, label: "ChatGPT thread" },
  { re: /https?:\/\/github\.com\/[^\s)]+/i, label: "GitHub link" },
];

const NOISE_PREFIXES = [
  "shared by",
  "this is a copy",
  "tip:",
  "model:",
  "directory:",
  "$ codex",
  "gpt-",
  "claude",
  "chatgpt",
  "codex",
  "11:31",
  "12:33",
  "13:15",
  "resimli kontrol yapma yontemi",
];

function cleanLine(line: string): string {
  return line
    .replace(/[│╭╰─]+/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function findSource(raw: string): { label: string; url?: string } {
  for (const item of SOURCE_PATTERNS) {
    const match = raw.match(item.re);
    if (match?.[0]) {
      return { label: item.label, url: match[0] };
    }
  }
  return { label: "Pasted conversation" };
}

function looksUsefulTitle(line: string): boolean {
  if (!line) return false;
  const lower = line.toLowerCase();
  if (NOISE_PREFIXES.some((prefix) => lower.startsWith(prefix))) return false;
  if (line.length < 5 || line.length > 88) return false;
  if (/^(https?:\/\/|\$|#)/.test(line)) return false;
  if (!/[a-zA-Z]/.test(line)) return false;
  if (/[{}[\];]/.test(line)) return false;
  return true;
}

function titleCaseWord(word: string): string {
  if (!word) return "";
  return word.slice(0, 1).toUpperCase() + word.slice(1).toLowerCase();
}

function suggestName(title?: string): string | undefined {
  if (!title) return undefined;
  const cleaned = title
    .replace(/^(how to|build|create|make)\s+/i, "")
    .replace(/[^\p{L}\p{N}\s-]/gu, " ")
    .replace(/\s+/g, " ")
    .trim();
  if (!cleaned) return undefined;
  const words = cleaned.split(" ").filter(Boolean).slice(0, 4);
  if (!words.length) return undefined;
  return words.map(titleCaseWord).join(" ");
}

function detectTitle(raw: string): string | undefined {
  const lines = raw
    .split(/\r?\n/g)
    .map(cleanLine)
    .filter(Boolean);
  for (const line of lines.slice(0, 24)) {
    if (looksUsefulTitle(line)) return line;
  }
  return undefined;
}

function trimConversation(raw: string): string {
  return raw.trim().slice(0, 20000);
}

export function buildImportedConversationBrief(raw: string): ImportedConversationBrief {
  const body = trimConversation(raw);
  const source = findSource(body);
  const title = detectTitle(body);
  const suggestedName = suggestName(title);
  const prompt = [
    "Use this imported AI conversation as the original product brief for a Yaver project.",
    "Assume the user may be non-technical, imprecise, or unfamiliar with software architecture words.",
    "Your first job is to understand what they are trying to achieve in plain product terms before deciding the implementation.",
    `Source: ${source.label}${source.url ? ` (${source.url})` : ""}.`,
    title ? `Detected title: ${title}.` : "",
    "Before writing any build plan, identify where internet research, API documentation lookup, or platform verification would be needed to make the request technically real.",
    "If the thread references existing products, APIs, share links, platforms, MCP flows, app-store constraints, or external services, treat those as research targets and fill in the missing technical path.",
    "Infer the first shippable scope even if the conversation is exploratory or messy.",
    "Prefer a mobile-first full-stack app, but include web UI and MCP or console surfaces when the thread asks for them.",
    "Preserve explicit constraints, requested integrations, rollout notes, and wording that changes the product direction.",
    "If the thread mixes examples with requirements, treat examples as inspiration and keep the concrete ask as the source of truth.",
    "Translate vague asks like 'make it work like this' into concrete product requirements, technical components, and likely data flows.",
    "Produce a richer implementation brief that explains what must exist technically: user input flow, import/parsing flow, storage, backend jobs, web/mobile UI, MCP or console hooks, and rollout constraints.",
    "When something cannot be derived directly from the conversation, make the smallest reasonable product assumption and keep it practical.",
    "",
    "Imported conversation:",
    body,
  ]
    .filter(Boolean)
    .join("\n");
  return {
    sourceLabel: source.label,
    sourceUrl: source.url,
    title,
    suggestedName,
    prompt,
    charCount: body.length,
  };
}

export function mergeImportedConversationPrompt(basePrompt: string, importedConversation: string): string {
  const trimmedBase = basePrompt.trim();
  const trimmedConversation = importedConversation.trim();
  if (!trimmedConversation) return trimmedBase;
  const imported = buildImportedConversationBrief(trimmedConversation).prompt;
  if (!trimmedBase) return imported;
  if (trimmedBase.includes(imported)) return trimmedBase;
  return `${trimmedBase}\n\n${imported}`;
}
