/**
 * carSurfaceIntent.ts — driving-safe personal assistant routing for car voice.
 *
 * This sits BEFORE the coding-agent dispatch. If the driver asks for meetings
 * or mail, call the provider-neutral Yaver ops verbs directly. If it is not a
 * car-surface intent, return handled:false and let carVoiceCoding dispatch it as
 * a coding task.
 */

export type CarSurfaceIntentKind =
  "meeting_next" | "meeting_join_next" | "mail_unread" | "mail_send";

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
