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
  | "maps_open"
  | "storage_scan"
  | "storage_reclaim"
  | "proc_top"
  | "ev_charging";

/**
 * Ambient context the SCREEN supplies — the driver can't speak their own
 * coordinates. Optional so every existing call site keeps working.
 */
export interface CarSurfaceContext {
  lat?: number;
  lon?: number;
  /** Vehicle preset id from ev_connector_types (e.g. "togg_t10x"). */
  vehicle?: string;
}

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

  // ── EV charging ─────────────────────────────────────────────────────
  // The one CarPlay category Yaver can honestly claim, and useful on voice
  // today with no entitlement at all. Filters default to the driver's car
  // (Togg T10X → CCS2) rather than making them recite connector types.
  const chargeish =
    /\b(charge|charging|charger|chargers|şarj|sarj|plug|supercharger)\b/.test(t) ||
    (/\b(ev|battery)\b/.test(t) && /\b(low|empty|range|top up)\b/.test(t));
  if (
    chargeish &&
    // English + Turkish triggers — the driver is in Turkey and will code-switch.
    (/\b(where|find|nearest|closest|near|nearby|any|show|need|can i|should i|look for)\b/.test(
      t,
    ) ||
      /(nerede|nerde|en yakın|en yakin|bul|var mı|var mi|lazım|lazim|gerek)/.test(
        t,
      ))
  ) {
    const fast = /\b(fast|rapid|dc|quick)\b/.test(t) || /(hızlı|hizli)/.test(t);
    return {
      kind: "ev_charging",
      payload: {
        // lat/lon are filled in from CarSurfaceContext at execute time.
        radius: 40,
        country: "TR",
        connector_type: "ccs2",
        min_power_kw: fast ? 120 : 50,
        network: networkFromText(t),
        surface: "car",
      },
    };
  }

  // ── Remote machine health: disk + processes ────────────────────────
  // storage_scan / proc_top are read-only and answer in one sentence, so
  // they're ideal for a driver. storage_reclaim DELETES, so it never fires
  // straight from a transcript — executeCarSurfaceIntent turns it into a
  // scan + dry-run and hands back a plan to be confirmed.
  const storageish = /\b(disk|storage|space|cache|caches|derived ?data)\b/.test(t);
  const reclaimish =
    /\b(reclaim|purge|prune)\b/.test(t) ||
    (/\b(clean|clear|free|empty|delete)\b/.test(t) && storageish);
  if (reclaimish) {
    return {
      kind: "storage_reclaim",
      payload: { surface: "car" },
      confirmRequired: true,
    };
  }
  if (
    storageish &&
    /\b(how much|check|status|left|full|usage|used|remaining|what)\b/.test(t)
  ) {
    return { kind: "storage_scan", payload: { surface: "car" } };
  }

  const procish = /\b(process|processes|cpu|memory|ram|load)\b/.test(t);
  if (
    procish &&
    /\b(top|what'?s using|using|hogging|eating|highest|most|check|show|busy)\b/.test(
      t,
    )
  ) {
    return {
      kind: "proc_top",
      payload: {
        sort: /\b(memory|ram|mem)\b/.test(t) ? "mem" : "cpu",
        limit: 5,
        surface: "car",
      },
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
  ctx: CarSurfaceContext = {},
): Promise<CarSurfaceResult> {
  const intent = classifyCarSurfaceIntent(text);
  if (!intent) return { handled: false, spoken: "" };

  // The driver cannot speak coordinates — the screen supplies them.
  if (intent.kind === "ev_charging") {
    if (typeof ctx.lat !== "number" || typeof ctx.lon !== "number") {
      return {
        handled: true,
        spoken: "I need location access to find chargers near you.",
        intent,
      };
    }
    intent.payload = { ...intent.payload, lat: ctx.lat, lon: ctx.lon };
  }

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

  // storage_reclaim is destructive and needs target ids the driver cannot
  // possibly speak. Never dispatch it from a raw transcript: scan, then ask
  // the agent for a DRY RUN (no confirm → it returns the plan, not an error),
  // and hand the plan back for confirmation. Deleting happens only in
  // confirmStorageReclaim(), after an explicit yes.
  if (intent.kind === "storage_reclaim") {
    const scan = unwrapOpsLike(await ops("storage_scan", {})) as any;
    const ids = reclaimIdsFromScan(scan);
    if (!ids.length) {
      return {
        handled: true,
        spoken: "Nothing worth reclaiming — the caches are already clean.",
        intent,
        raw: scan,
      };
    }
    const plan = await ops("storage_reclaim", { ids });
    const planData = unwrapOpsLike(plan) as any;
    const freed = planData?.freed || formatBytesForSpeech(scan?.totalReclaimableBytes);
    return {
      handled: true,
      spoken: `I can free about ${freed} on ${
        scan?.hostname || "the box"
      }. Say confirm to reclaim it.`,
      intent: { ...intent, payload: { ...intent.payload, ids }, confirmRequired: true },
      raw: plan,
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

/**
 * Execute an approved reclaim. Called ONLY after the driver has explicitly
 * confirmed the plan returned by executeCarSurfaceIntent (carVoiceConfirm's
 * `storage` risk kind gates the transcript that got here).
 */
export async function confirmStorageReclaim(
  ids: string[],
  ops: CarSurfaceOps,
): Promise<string> {
  if (!ids.length) return "Nothing to reclaim.";
  const raw = await ops("storage_reclaim", { ids, confirm: true });
  const data = unwrapOpsLike(raw) as any;
  const freed = data?.freed || formatBytesForSpeech(data?.freedBytes);
  const after = data?.rootFreeGbAfter;
  return typeof after === "number"
    ? `Freed ${freed}. ${Math.round(after)} gigabytes free now.`
    : `Freed ${freed}.`;
}

/** Flatten a StorageScan's groups into the target ids storage_reclaim wants. */
function reclaimIdsFromScan(scan: any): string[] {
  const groups = Array.isArray(scan?.groups) ? scan.groups : [];
  const ids: string[] = [];
  for (const g of groups) {
    for (const target of g?.targets || []) {
      if (target?.id) ids.push(String(target.id));
    }
  }
  return ids;
}

function formatBytesForSpeech(bytes: unknown): string {
  const n = Number(bytes || 0);
  if (!n) return "nothing";
  const gb = n / 1024 ** 3;
  if (gb >= 1) return `${gb.toFixed(1)} gigabytes`;
  return `${Math.max(1, Math.round(n / 1024 ** 2))} megabytes`;
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
    case "storage_scan": {
      const root = (data?.filesystems || [])[0];
      const freeGb = Number(root?.freeGb ?? root?.freeGB ?? 0);
      const reclaimable = formatBytesForSpeech(data?.totalReclaimableBytes);
      const host = data?.hostname ? ` on ${data.hostname}` : "";
      const partial = data?.partial ? " That's a floor — the scan timed out." : "";
      if (freeGb) {
        return `${Math.round(
          freeGb,
        )} gigabytes free${host}, and about ${reclaimable} is reclaimable.${partial}`;
      }
      return `About ${reclaimable} is reclaimable${host}.${partial}`;
    }
    case "storage_reclaim": {
      // Reached only on the confirmed path; the dry-run sentence is built in
      // executeCarSurfaceIntent, which has the scan in hand.
      const freed = data?.freed || formatBytesForSpeech(data?.freedBytes);
      if (data?.dryRun) return `That would free ${freed}. Say confirm to do it.`;
      return `Freed ${freed}.`;
    }
    case "ev_charging": {
      // The collection layer reports a third-party block as a finding rather
      // than routing around it (CLAUDE.md do-no-harm). Say so plainly.
      if (data?.blocked) {
        return "Charger data is blocked right now — the provider needs an API key.";
      }
      const stations = Array.isArray(data?.stations)
        ? data.stations
        : Array.isArray(data)
          ? data
          : [];
      if (!stations.length) return "No matching chargers nearby.";
      const s = stations[0];
      const name = s?.name || s?.title || "a charger";
      const km = Number(s?.distance_km ?? s?.distanceKm ?? 0);
      const kw = Number(s?.max_power_kw ?? s?.maxPowerKw ?? 0);
      const dist = km ? ` ${km.toFixed(0)} kilometers away` : "";
      const power = kw ? `, ${Math.round(kw)} kilowatt` : "";
      const more =
        stations.length > 1 ? ` ${stations.length - 1} more nearby.` : "";
      return `Nearest is ${name}${dist}${power}.${more}`;
    }
    case "proc_top": {
      const procs = Array.isArray(data?.processes) ? data.processes : [];
      if (!procs.length) return "No processes returned.";
      const top = procs[0];
      const byMem = intent.payload?.sort === "mem";
      const metric = byMem
        ? `${Math.round(Number(top.rssMb || 0))} megabytes`
        : `${Number(top.cpuPct || 0).toFixed(0)} percent CPU`;
      return `Top is ${top.name} at ${metric}.`;
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

/** Charging-network filter, spoken naturally ("any Trugo chargers near me"). */
function networkFromText(t: string): string {
  if (/\btrugo|togg\b/.test(t)) return "trugo";
  if (/\bzes\b/.test(t)) return "zes";
  if (/\be[şs]arj\b/.test(t)) return "esarj";
  if (/\bsharz\b/.test(t)) return "sharz";
  if (/\bvoltrun\b/.test(t)) return "voltrun";
  if (/\btesla\b/.test(t)) return "tesla";
  return "";
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
