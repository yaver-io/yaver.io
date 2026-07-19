"use client";

// DeviceDeployCapabilities — "what can this machine actually ship?", rendered
// from the device row rather than by reaching the box.
//
// The point of this component is that it is NOT a platform guess. The old
// signal, `publishCapabilities`, is a runtime.GOOS switch: every Mac claims iOS
// whether or not Xcode is installed, a signing identity exists, or the keychain
// can be unlocked from a non-GUI session. On 2026-07-19 the mac mini was picked
// as a deploy box while it had no npm credential at all and no google-auth for
// Play uploads — neither was visible anywhere until a deploy tried and failed.
//
// These lists come from the agent running the toolchain. That makes AGE part of
// the meaning: the probe refreshes about every 6 hours, so a green pill is
// "true as of then", never "true now". Rendering the timestamp is mandatory —
// a stale green presented as live is precisely the failure this replaces.

import { Badge } from "@/components/ui/Badge";

/** Human labels. Unknown targets fall through to their raw name rather than
 *  being dropped: a target the agent knows and this map doesn't must still be
 *  visible, or the UI silently under-reports what a box can do. */
const TARGET_LABELS: Record<string, string> = {
  npm: "npm",
  testflight: "TestFlight",
  playstore: "Play internal",
  "playstore-production": "Play production",
  convex: "Convex",
  "convex-selfhosted": "Convex (self-hosted)",
  cloudflare: "Cloudflare",
  vercel: "Vercel",
  netlify: "Netlify",
  firebase: "Firebase",
  fly: "Fly",
  pages: "Pages",
  railway: "Railway",
  "supabase-db": "Supabase DB",
  "supabase-functions": "Supabase Fns",
};

function label(target: string): string {
  return TARGET_LABELS[target] ?? target;
}

/** Beyond this the probe is old enough that it should be called out rather
 *  than quietly shown. Two refresh intervals — one missed cycle is normal
 *  (the box was asleep); two means it stopped reporting. */
const STALE_AFTER_MS = 12 * 60 * 60 * 1000;

export function formatProbeAge(at?: string): { text: string; stale: boolean } | null {
  if (!at) return null;
  const t = Date.parse(at);
  if (Number.isNaN(t)) return null;
  const ms = Date.now() - t;
  const stale = ms > STALE_AFTER_MS;
  const mins = Math.floor(ms / 60000);
  if (mins < 1) return { text: "just now", stale };
  if (mins < 60) return { text: `${mins}m ago`, stale };
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return { text: `${hrs}h ago`, stale };
  return { text: `${Math.floor(hrs / 24)}d ago`, stale };
}

export function DeviceDeployCapabilities({
  ready,
  blocked,
  probedAt,
  compact = false,
}: {
  ready?: string[];
  blocked?: string[];
  probedAt?: string;
  compact?: boolean;
}) {
  const hasReady = (ready?.length ?? 0) > 0;
  const hasBlocked = (blocked?.length ?? 0) > 0;
  const age = formatProbeAge(probedAt);

  // Never probed is a distinct state from "probed and can ship nothing", and
  // conflating them would be the same lie in a new place. Say which it is.
  if (!hasReady && !hasBlocked) {
    return (
      <div className="text-xs text-muted-fg">
        Deploy capability not reported yet — needs agent 1.99.319+ and one probe cycle.
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-1.5">
        {(ready ?? []).map((t) => (
          <Badge key={t} tone="success" variant="soft">
            {label(t)}
          </Badge>
        ))}
        {!compact &&
          (blocked ?? []).map((t) => (
            <Badge key={t} tone="muted" variant="outline">
              {label(t)}
            </Badge>
          ))}
        {compact && hasBlocked ? (
          <span className="text-xs text-muted-fg">+{blocked!.length} blocked</span>
        ) : null}
      </div>
      {age ? (
        <div className={`text-xs ${age.stale ? "text-warning-fg" : "text-muted-fg"}`}>
          {age.stale ? "⚠ probed " : "probed "}
          {age.text}
          {age.stale ? " — may be out of date" : ""}
        </div>
      ) : null}
    </div>
  );
}
