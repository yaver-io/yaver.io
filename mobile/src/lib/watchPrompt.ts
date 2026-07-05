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
  if (/\b(implement|build|make|code|edit|change|add|fix|wire|create|ship|deploy|redeploy|deployment|release|rollout|push|force|reset|delete|remove|destroy|prod|production)\b/.test(t)) {
    return "implementation";
  }
  if (/\b(browser|website|site|page|login|search|scrape|click|open|chrome|safari|playwright|selenium)\b/.test(t)) {
    return "browser-automation";
  }
  if (/\b(question|ask|check|status|what|why|how|can you|runtime|session|summari[sz]e)\b/.test(t)) {
    return "remote-runtime-question";
  }
  if (/\b(idea|remember|note|thought|feature idea|maybe|should we|what if)\b/.test(t)) {
    return "idea-capture";
  }
  return "idea-capture";
}

export function buildWatchPrompt(text: string): WatchPromptPlan {
  const original = text.trim();
  const mode = classifyWatchPrompt(original);
  const shared = [
    "This task came from a smartwatch while the user may be walking.",
    "Input may be short, noisy, and under-specified.",
    "Do not ask for long clarification on the watch.",
    "Return a watch-safe one-sentence spoken summary first; send details, code, logs, and decisions to phone/desktop.",
  ].join(" ");

  let instruction: string;
  switch (mode) {
    case "implementation":
      instruction = [
        "Treat this as permission to work on the remote runtime.",
        "If the app/repo is ambiguous, infer from current Yaver context; otherwise create a concise implementation plan and stop before destructive changes.",
        "For code changes, make focused edits, run the smallest useful check, and summarize outcome in one sentence for the watch.",
      ].join(" ");
      break;
    case "browser-automation":
      instruction = [
        "Treat this as a remote browser/runtime automation request.",
        "Use visible or auditable browser automation where possible.",
        "Stop for login, payment, CAPTCHA, consent, destructive actions, or private data exposure; hand off details to phone.",
        "For read-only browsing, run the session and summarize the result in one watch-safe sentence.",
      ].join(" ");
      break;
    case "remote-runtime-question":
      instruction = [
        "Treat this as a question for the remote runtime.",
        "Inspect the relevant app/session/log/status if available.",
        "Answer with a one-sentence watch summary and put detailed findings on the phone/desktop task output.",
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
