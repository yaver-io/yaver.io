/**
 * carSurfaceIntent.ts — driving-safe personal assistant routing for car voice.
 *
 * This sits BEFORE the coding-agent dispatch. If the driver asks for meetings
 * or mail, call the provider-neutral Yaver ops verbs directly. If it is not a
 * car-surface intent, return handled:false and let carVoiceCoding dispatch it as
 * a coding task.
 */

export type CarSurfaceIntentKind =
  | "meeting_next"
  | "meeting_join_next"
  | "mail_unread"
  | "mail_send"
  | "git_prs"
  | "git_issues"
  | "git_connect"
  | "git_ci_status"
  | "media_open"
  | "maps_open";

export interface CarSurfaceIntent {
  kind: CarSurfaceIntentKind;
  payload: Record<string, unknown>;
  confirmRequired?: boolean;
  prompt?: string;
}

export interface CarSurfaceResult {
  handled: boolean;
  spoken: string;
  intent?: CarSurfaceIntent;
  raw?: unknown;
}

export type CarSurfaceOps = (
  verb: string,
  payload: Record<string, unknown>,
) => Promise<unknown>;

export function classifyCarSurfaceIntent(
  text: string,
): CarSurfaceIntent | null {
  const clean = text.trim();
  const t = ` ${clean.toLowerCase()} `;
  if (!clean) return null;

  const provider = providerFromText(t);
  const mediaProvider = mediaProviderFromText(t);
  const mediaish =
    mediaProvider !== "auto" ||
    /\b(youtube|twitch|kick|vimeo|spotify|apple music|livestream|live stream|stream|video|music)\b/.test(
      t,
    );
  if (mediaish && /\b(open|watch|play|search|find|show)\b/.test(t)) {
    const query = mediaQueryFromText(clean);
    if (query || firstURL(clean)) {
      return {
        kind: "media_open",
        payload: {
          provider: mediaProvider,
          query,
          url: firstURL(clean),
          live: /\b(live|livestream|live stream)\b/.test(t),
          open: true,
          openMode: "browser",
          surface: "car",
        },
      };
    }
  }

  const mapsProvider = mapsProviderFromText(t);
  const mapsish =
    mapsProvider !== "auto" ||
    /\b(map|maps|traffic|directions|navigate|route|commute|waze)\b/.test(t);
  if (mapsish && /\b(open|show|check|find|navigate|route|directions|traffic)\b/.test(t)) {
    const destination = mapsDestinationFromText(clean);
    const query = mapsQueryFromText(clean);
    if (destination || query) {
      return {
        kind: "maps_open",
        payload: {
          provider: mapsProvider,
          destination,
          query: destination ? "" : query,
          traffic: /\b(traffic|busy|congestion|jam|commute)\b/.test(t),
          open: true,
          openMode: "browser",
          surface: "car",
        },
      };
    }
  }

  const meetingish =
    /\b(meeting|call|standup|sync|teams|google meet|meet|zoom)\b/.test(t);
  if (meetingish && /\b(join|open|start|dial in|dial)\b/.test(t)) {
    return {
      kind: "meeting_join_next",
      payload: {
        provider,
        open: true,
        openMode: "browser",
        surface: "car",
        withinHours: 24,
      },
    };
  }
  if (
    meetingish &&
    /\b(next|upcoming|when|what time|what's|what is|show|check)\b/.test(t)
  ) {
    return {
      kind: "meeting_next",
      payload: { provider, withinHours: 24 },
    };
  }

  if (
    /\b(email|emails|mail|inbox|gmail|outlook)\b/.test(t) &&
    /\b(read|check|any|new|unread|incoming|came in|what)\b/.test(t)
  ) {
    return {
      kind: "mail_unread",
      payload: {
        provider: mailProviderFromText(t),
        limit: 5,
        onlyPersonal: true,
      },
    };
  }

  if (/\b(send|email|mail)\b/.test(t) && /\b(email|mail)\b/.test(t)) {
    const addr = firstEmailAddress(clean);
    if (!addr) {
      return {
        kind: "mail_send",
        payload: {},
        confirmRequired: true,
        prompt:
          "I need a specific email address. I'll leave this for your phone.",
      };
    }
    const body = mailBodyFromText(clean);
    return {
      kind: "mail_send",
      payload: {
        to: [addr],
        subject: "Quick update",
        body,
        execute: false,
        surface: "car",
      },
      confirmRequired: true,
      prompt: `Draft email to ${addr}: ${body}. Say confirm on your phone to send.`,
    };
  }

  const gitish =
    /\b(github|gitlab|git|pull request|pull requests|pr|prs|merge request|merge requests|mr|mrs|issue|issues|ci|pipeline|pipelines|actions|workflow|workflows|oauth|authorize|authenticate)\b/.test(
      t,
    );
  if (gitish) {
    const provider = gitProviderFromText(t);
    if (
      /\b(connect|sign in|signin|log in|login|authorize|auth|authenticate|oauth|onboard|setup|set up)\b/.test(
        t,
      )
    ) {
      return {
        kind: "git_connect",
        payload: { provider, surface: "car" },
      };
    }
    if (
      /\b(ci|pipeline|pipelines|actions|workflow|workflows|build|builds)\b/.test(
        t,
      ) &&
      /\b(check|status|what|show|read|list|any|latest)\b/.test(t)
    ) {
      return {
        kind: "git_ci_status",
        payload: { provider, surface: "car" },
      };
    }
    if (
      /\b(pull request|pull requests|pr|prs|merge request|merge requests|mr|mrs)\b/.test(
        t,
      ) &&
      /\b(check|show|read|list|any|open|status|what)\b/.test(t)
    ) {
      return {
        kind: "git_prs",
        payload: { provider, state: "open", limit: 5, surface: "car" },
      };
    }
    if (
      /\b(issue|issues)\b/.test(t) &&
      /\b(check|show|read|list|any|open|status|what)\b/.test(t)
    ) {
      return {
        kind: "git_issues",
        payload: { provider, state: "open", limit: 5, surface: "car" },
      };
    }
  }

  return null;
}

export async function executeCarSurfaceIntent(
  text: string,
  ops: CarSurfaceOps,
): Promise<CarSurfaceResult> {
  const intent = classifyCarSurfaceIntent(text);
  if (!intent) return { handled: false, spoken: "" };

  if (
    intent.confirmRequired &&
    intent.kind === "mail_send" &&
    Object.keys(intent.payload).length === 0
  ) {
    return {
      handled: true,
      spoken: intent.prompt || "I need more detail.",
      intent,
    };
  }

  const verb = intent.kind;
  const raw = await ops(verb, intent.payload);
  return {
    handled: true,
    spoken: spokenForCarIntent(intent, raw),
    intent,
    raw,
  };
}

export function spokenForCarIntent(
  intent: CarSurfaceIntent,
  raw: unknown,
): string {
  const data = unwrapOpsLike(raw) as any;
  switch (intent.kind) {
    case "meeting_next": {
      const title = data?.title || "your next meeting";
      const when = timeForSpeech(data?.startsAt);
      return when
        ? `Next meeting: ${title} at ${when}.`
        : `Next meeting: ${title}.`;
    }
    case "meeting_join_next": {
      const title = data?.title || "your next meeting";
      if (data?.opened) return `Opening ${title}.`;
      if (data?.openUrl || data?.joinUrl)
        return `I found ${title}; open it from your phone when safe.`;
      return `I found ${title}, but there is no join link.`;
    }
    case "mail_unread": {
      if (typeof data?.spoken === "string" && data.spoken.trim())
        return data.spoken;
      const count = Number(data?.count || 0);
      if (!count) return "No recent personal email.";
      const first = data?.messages?.[0];
      return first?.subject
        ? `${count} emails. Latest: ${first.subject}.`
        : `${count} recent emails.`;
    }
    case "mail_send": {
      if (data?.dryRun)
        return intent.prompt || "Draft ready on your phone for confirmation.";
      if (data?.sent) return "Email sent.";
      return "Email is ready for review.";
    }
    case "git_prs":
    case "git_issues":
    case "git_ci_status": {
      if (typeof data?.spoken === "string" && data.spoken.trim())
        return data.spoken;
      const count = Number(data?.count || 0);
      if (intent.kind === "git_ci_status")
        return count
          ? `${count} recent CI entries.`
          : "No recent CI entries returned.";
      if (intent.kind === "git_prs")
        return count
          ? `${count} open pull requests.`
          : "No open pull requests.";
      return count ? `${count} open issues.` : "No open issues.";
    }
    case "git_connect": {
      if (typeof data?.spoken === "string" && data.spoken.trim())
        return data.spoken;
      if (typeof data?.error === "string" && data.error.trim())
        return `Could not start git authorization: ${data.error}`;
      const provider = gitProviderLabel(data?.provider || intent.payload.provider);
      const code = data?.user_code || data?.userCode || data?.code;
      const url = data?.verification_uri || data?.verificationUri || data?.openUrl;
      if (code && url)
        return `${provider} authorization started. Code ${spellCodeForSpeech(
          String(code),
        )}. Open ${shortHostForSpeech(String(url))} on your phone.`;
      if (code)
        return `${provider} authorization started. Code ${spellCodeForSpeech(
          String(code),
        )}. Check your phone for the link.`;
      return `${provider} authorization started. Check your phone for the browser approval.`;
    }
    case "media_open":
    case "maps_open": {
      if (typeof data?.spoken === "string" && data.spoken.trim())
        return data.spoken;
      if (data?.opened && data?.provider) return `Opening ${data.provider}.`;
      return "Opening on the runtime.";
    }
    default:
      return "Done.";
  }
}

function unwrapOpsLike(raw: unknown): unknown {
  const r = raw as any;
  if (r && typeof r === "object" && "initial" in r) return r.initial;
  return raw;
}

function providerFromText(t: string): string {
  if (/\b(teams|microsoft|office|o365|outlook)\b/.test(t)) return "teams";
  if (/\b(google meet|meet|gmail|google)\b/.test(t)) return "google";
  if (/\bzoom\b/.test(t)) return "zoom";
  return "auto";
}

function mailProviderFromText(t: string): string {
  if (/\b(gmail|google)\b/.test(t)) return "gmail";
  if (/\b(outlook|office|o365|microsoft)\b/.test(t)) return "o365";
  return "auto";
}

function gitProviderFromText(t: string): string {
  if (/\bgitlab|merge request|merge requests|mr|mrs\b/.test(t)) return "gitlab";
  if (/\bgithub|pull request|pull requests|pr|prs|actions\b/.test(t))
    return "github";
  return "github";
}

function gitProviderLabel(value: unknown): string {
  const s = String(value || "").toLowerCase();
  if (s === "gitlab") return "GitLab";
  return "GitHub";
}

function spellCodeForSpeech(code: string): string {
  return code
    .replace(/[^A-Z0-9-]/gi, "")
    .split("")
    .join(" ");
}

function shortHostForSpeech(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}

function mediaProviderFromText(t: string): string {
  if (/\byoutube|yt\b/.test(t)) return "youtube";
  if (/\btwitch\b/.test(t)) return "twitch";
  if (/\bkick\b/.test(t)) return "kick";
  if (/\bvimeo\b/.test(t)) return "vimeo";
  if (/\bspotify\b/.test(t)) return "spotify";
  if (/\bapple music\b/.test(t)) return "apple_music";
  return "auto";
}

function mapsProviderFromText(t: string): string {
  if (/\byandex\b/.test(t)) return "yandex";
  if (/\bwaze\b/.test(t)) return "waze";
  if (/\bapple maps?\b/.test(t)) return "apple";
  if (/\bgoogle maps?|maps\b/.test(t)) return "google";
  return "auto";
}

function firstURL(s: string): string {
  const m = s.match(/https:\/\/[^\s]+/i);
  return m ? m[0] : "";
}

function mediaQueryFromText(s: string): string {
  return s
    .replace(/https:\/\/[^\s]+/gi, " ")
    .replace(/\b(open|watch|play|search|find|show|on|in)\b/gi, " ")
    .replace(/\b(youtube|yt|twitch|kick|vimeo|spotify|apple music)\b/gi, " ")
    .replace(/\b(livestream|live stream|stream|video|music|channel)\b/gi, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function mapsDestinationFromText(s: string): string {
  const m = s.match(/\b(?:to|towards|navigate to|directions to)\s+(.+)$/i);
  if (!m) return "";
  return mapsQueryFromText(m[1]);
}

function mapsQueryFromText(s: string): string {
  return s
    .replace(/\b(open|show|check|find|navigate|route|directions|traffic|map|maps|on|in)\b/gi, " ")
    .replace(/\b(google|yandex|apple|waze)\b/gi, " ")
    .replace(/\b(busy|congestion|jam|commute)\b/gi, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function firstEmailAddress(s: string): string {
  const m = s.match(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/i);
  return m ? m[0] : "";
}

function mailBodyFromText(s: string): string {
  const cleaned = s
    .replace(/\b(send|email|mail)\b/gi, " ")
    .replace(/\bto\s+[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi, " ")
    .replace(/\s+/g, " ")
    .trim();
  return cleaned || "Running a few minutes late.";
}

function timeForSpeech(value: unknown): string {
  if (typeof value !== "string" || !value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString(undefined, {
    hour: "numeric",
    minute: "2-digit",
  });
}
