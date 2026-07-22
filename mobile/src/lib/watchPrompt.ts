export type WatchPromptMode =
  | "idea-capture"
  | "browser-automation"
  | "remote-runtime-question"
  | "implementation";

export interface WatchPromptPlan {
  mode: WatchPromptMode;
  original: string;
  prompt: string;
}

export function classifyWatchPrompt(text: string): WatchPromptMode {
  const t = ` ${text.trim().toLowerCase()} `;
  if (/\b(idea|remember|note|thought|feature idea|maybe|should we|what if)\b/.test(t)) {
    return "idea-capture";
  }
  if (/\b(implement|build|make|code|edit|change|add|fix|wire|create|ship|deploy|redeploy|deployment|release|rollout|push|force|reset|delete|remove|destroy|prod|production)\b/.test(t)) {
    return "implementation";
  }
  if (/\b(browser|website|site|page|login|search|scrape|click|open|chrome|safari|playwright|selenium)\b/.test(t)) {
    return "browser-automation";
  }
  if (/\b(question|ask|check|status|what|why|how|can you|runtime|session|summari[sz]e)\b/.test(t)) {
    return "remote-runtime-question";
  }
  return "idea-capture";
}

export function buildWatchPrompt(text: string): WatchPromptPlan {
  const original = text.trim();
  const mode = classifyWatchPrompt(original);
  const shared = [
    "Surface: Apple Watch / smartwatch voice command.",
    "The user is invoking Yaver through STT on a tiny wrist screen and will hear the reply through TTS.",
    "Treat the transcript as an instruction to act, not as normal chat, unless it is clearly a question or idea.",
    "Input may be short, noisy, under-specified, or missing app names; infer from the active Yaver device, current repo, selected project, and recent session when that is safer than asking.",
    "Do not ask for long clarification on the watch; ask only for credentials, irreversible/destructive authorization, payment, CAPTCHA/consent, or truly ambiguous target selection.",
    "Optimize for an async wrist workflow: start useful work, keep detailed reasoning/logs/code on phone/desktop, and make the first/final watch reply one short spoken sentence.",
    "Never put code, diffs, secrets, tokens, file dumps, or long logs in the watch summary.",
  ].join(" ");

  let instruction: string;
  switch (mode) {
    case "implementation":
      instruction = [
        "Treat this as permission to work on the remote runtime after the watch risk gate has passed.",
        "Resolve ambiguous app/repo references from current Yaver context before asking; if multiple plausible targets remain, prepare a concise plan and hand the choice to phone.",
        "For code changes, make focused edits, run the smallest useful check, leave detailed results in the task output, and summarize the outcome in one sentence for the watch.",
      ].join(" ");
      break;
    case "browser-automation":
      instruction = [
        "Treat this as a remote browser/runtime automation request.",
        "Use visible or auditable browser automation where possible.",
        "Stop for login, payment, CAPTCHA, consent, destructive actions, or private data exposure; hand off the exact next step to phone.",
        "For read-only browsing, run the session and summarize the result in one watch-safe sentence.",
      ].join(" ");
      break;
    case "remote-runtime-question":
      instruction = [
        "Treat this as a question for the remote runtime.",
        "Inspect the relevant app/session/log/status if available.",
        "Answer with a one-sentence watch summary first and put detailed findings on the phone/desktop task output.",
      ].join(" ");
      break;
    case "idea-capture":
    default:
      instruction = [
        "Treat this as idea capture unless the user explicitly asked to implement now.",
        "Turn it into a concise product note: app/context guess, user problem, possible feature, acceptance criteria, and next implementation step.",
        "Do not edit code unless the transcript explicitly asks to build/change/fix/add.",
      ].join(" ");
      break;
  }

  return {
    mode,
    original,
    prompt: `${shared}\n\n${instruction}\n\nWatch transcript: ${original}`,
  };
}
