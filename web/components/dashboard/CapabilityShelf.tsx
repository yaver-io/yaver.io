"use client";

// The "build cockpit" capability shelf — the normie's seat for the
// à-la-carte managed services (docs/yaver-normie-concierge-fair-
// metering.md). His terminal Claude Code / Codex writes the code and the
// Yaver MCP does the infra work; THIS is where the human sees the money,
// turns capabilities on one at a time, and reads an HONEST per-capability
// burn breakdown.
//
// Fairness contract (doc §6): a calm headline balance, the per-layer
// breakdown one tap away, every capability independently opt-in, and a
// "run it yourself → free" BYO exit always visible. We charge for toil
// removed, never for ignorance.
//
// Data: GET/POST /managed/services, GET /managed/cockpit, GET
// /managed/burn (http.ts). Add-credit reuses the existing OpenAI-style
// credit-pack checkout (/billing/credits/*), Apple-safe web top-up.

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

type ServiceKey = "reload" | "backend" | "web" | "agentBox" | "inference" | "publish" | "studio";

type Capability = {
  key: ServiceKey;
  icon: string;
  title: string;
  blurb: string;
  priceHint: string;
  // The infra noun we're hiding — shown only as a tiny "replaces …" tag
  // so a curious user can connect the dots, never as the headline.
  replaces: string;
  meterKind: "compute" | "backend" | "web" | "inference" | "publish" | "studio";
};

// Ladder order: cheapest hook first → hero last. This is the order the
// guided journey walks him through (doc §4).
const CAPABILITIES: Capability[] = [
  {
    key: "reload",
    icon: "📱",
    title: "See it on my phone",
    blurb: "Compile and push your app to your phone instantly. No Xcode, no cables.",
    priceHint: "~$0.01 / preview",
    replaces: "Xcode + Hermes build",
    meterKind: "compute",
  },
  {
    key: "backend",
    icon: "🧠",
    title: "App backend (data, auth, APIs)",
    blurb: "Turn on your app's brain. We deploy and run it for you.",
    priceHint: "metered, ~2× cost",
    replaces: "Convex",
    meterKind: "backend",
  },
  {
    key: "web",
    icon: "🌐",
    title: "Website for my app",
    blurb: "Give your app a public web address. We host and deploy it.",
    priceHint: "metered, ~2× cost",
    replaces: "Cloudflare",
    meterKind: "web",
  },
  {
    key: "agentBox",
    icon: "💻",
    title: "Always-on coding computer",
    blurb: "Run Claude Code / Codex in the cloud and drive it from anywhere. Use your own AI key (free) or Yaver's.",
    priceHint: "~$0.08 / hr",
    replaces: "Hetzner box",
    meterKind: "compute",
  },
  {
    key: "inference",
    icon: "✨",
    title: "Yaver AI (no key needed)",
    blurb: "Don't have an AI key? Route through Yaver's gateway. If you have a Claude/OpenAI key, keep using it — it's cheaper.",
    priceHint: "metered, ~1.5× cost",
    replaces: "GLM / OpenRouter",
    meterKind: "inference",
  },
  {
    key: "publish",
    icon: "🚀",
    title: "Publish to the App Store / Play",
    blurb: "We build, sign, screenshot, and submit it. The part you can't do without a Mac.",
    priceHint: "per release",
    replaces: "Mac + Apple/Google",
    meterKind: "publish",
  },
  {
    key: "studio",
    icon: "🎬",
    title: "Store Studio (screenshots & videos)",
    blurb: "Generate App Store / Play screenshots, preview videos, and permission-justification videos for your app — no Mac, no Fastlane, no spare device.",
    priceHint: "metered per run · free on your own box",
    replaces: "Fastlane + a screenshot SaaS + a device",
    meterKind: "studio",
  },
];

const KIND_LABEL: Record<string, string> = {
  compute: "Computer / reload",
  backend: "Backend",
  web: "Website",
  inference: "Yaver AI",
  publish: "Publishing",
};

function dollars(cents: number): string {
  return `$${(Math.max(0, cents) / 100).toFixed(2)}`;
}

type Cockpit = {
  balanceCents: number;
  currency: string;
  enabled: Record<string, boolean>;
  anyEnabled: boolean;
  estPerDayCents: number;
  estDaysLeft: number | null;
  lowBalance: boolean;
  empty: boolean;
};

type BurnRow = {
  kind: string;
  chargedCents: number;
  providerCostCents: number;
  dryRunCents: number;
};
type Burn = {
  rows: BurnRow[];
  totalChargedCents: number;
  totalDryRunCents: number;
  realChargedCents: number;
  days: number;
};

export function CapabilityShelf({ token }: { token: string | null }) {
  const [cockpit, setCockpit] = useState<Cockpit | null>(null);
  const [burn, setBurn] = useState<Burn | null>(null);
  const [showBreakdown, setShowBreakdown] = useState(false);
  const [busy, setBusy] = useState<ServiceKey | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [addingCredit, setAddingCredit] = useState(false);

  const headers = useCallback(
    () => ({ Authorization: `Bearer ${token}`, "Content-Type": "application/json" }),
    [token],
  );

  const refresh = useCallback(async () => {
    if (!token) return;
    try {
      const [cRes, bRes] = await Promise.all([
        fetch(`${CONVEX_URL}/managed/cockpit`, { headers: { Authorization: `Bearer ${token}` } }),
        fetch(`${CONVEX_URL}/managed/burn`, { headers: { Authorization: `Bearer ${token}` } }),
      ]);
      const c = await cRes.json().catch(() => ({}));
      const b = await bRes.json().catch(() => ({}));
      if (c?.ok) setCockpit(c as Cockpit);
      if (b?.ok) setBurn(b as Burn);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load");
    }
  }, [token]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const toggle = useCallback(
    async (key: ServiceKey, next: boolean) => {
      if (!token) return;
      setBusy(key);
      setError(null);
      // Optimistic — the switch should feel instant.
      setCockpit((prev) =>
        prev ? { ...prev, enabled: { ...prev.enabled, [key]: next } } : prev,
      );
      try {
        const res = await fetch(`${CONVEX_URL}/managed/services`, {
          method: "POST",
          headers: headers(),
          body: JSON.stringify({ service: key, enabled: next }),
        });
        const data = await res.json().catch(() => ({}));
        if (!data?.ok) throw new Error(data?.error || "Toggle failed");
        await refresh();
      } catch (e) {
        setError(e instanceof Error ? e.message : "Toggle failed");
        await refresh(); // re-sync truth on failure
      } finally {
        setBusy(null);
      }
    },
    [token, headers, refresh],
  );

  // Add credit — OpenAI-style web top-up (Apple-safe; app only spends).
  // Reuses the existing credit-pack checkout; picks the mid pack as a
  // sensible default and opens the hosted checkout in a new tab.
  const addCredit = useCallback(async () => {
    if (!token) return;
    setAddingCredit(true);
    setError(null);
    try {
      const pRes = await fetch(`${CONVEX_URL}/billing/credits/packs`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      const p = await pRes.json().catch(() => ({}));
      const packs: Array<{ id: string; cents: number }> = Array.isArray(p?.packs) ? p.packs : [];
      if (packs.length === 0) {
        setError("Credit packs aren't configured yet.");
        return;
      }
      // Default to the pack nearest $25, else the first.
      const pick =
        packs.find((x) => x.cents === 2500) ??
        packs.slice().sort((a, b) => Math.abs(a.cents - 2500) - Math.abs(b.cents - 2500))[0];
      const cRes = await fetch(`${CONVEX_URL}/billing/credits/checkout`, {
        method: "POST",
        headers: headers(),
        body: JSON.stringify({ packId: pick.id }),
      });
      const c = await cRes.json().catch(() => ({}));
      const url = c?.url || c?.checkoutUrl;
      if (url) window.open(url, "_blank", "noopener");
      else setError(c?.error || "Couldn't start checkout.");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't start checkout.");
    } finally {
      setAddingCredit(false);
    }
  }, [token, headers]);

  if (!token) {
    return (
      <div className="text-xs text-surface-500">Sign in to manage your app's capabilities.</div>
    );
  }

  const balance = cockpit?.balanceCents ?? 0;
  const daysLeft = cockpit?.estDaysLeft ?? null;

  // Guided journey (doc §4): walk him up the core ladder one rung at a
  // time. We only nudge the four CORE rungs (reload → backend → web →
  // publish); agentBox + inference are optional power/convenience tiers
  // he reaches for himself. The nudge is informational + one-tap, never
  // auto-enabling (enabling spends money — stays deliberate, doc §6).
  const JOURNEY: { key: ServiceKey; cta: string; why: string }[] = [
    { key: "reload", cta: "See it on your phone", why: "the fastest way to know it works — costs about a penny" },
    { key: "backend", cta: "Add your app's backend", why: "when your app needs to store data or sign users in" },
    { key: "web", cta: "Put it on the web", why: "give your app a public address people can open" },
    { key: "publish", cta: "Publish to the stores", why: "the part you can't do without a Mac — we handle it" },
  ];
  const nextStep = cockpit ? JOURNEY.find((s) => cockpit.enabled?.[s.key] !== true) : undefined;

  return (
    <div className="space-y-4">
      {/* Header: calm headline balance + honest runway. */}
      <div className="rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
        <div className="flex items-end justify-between gap-4">
          <div>
            <div className="text-xs uppercase tracking-wide text-surface-500">Balance</div>
            <div className="mt-0.5 text-2xl font-semibold text-surface-100">{dollars(balance)}</div>
            <div className="mt-0.5 text-xs text-surface-500">
              {cockpit?.estPerDayCents
                ? `≈ ${dollars(cockpit.estPerDayCents)}/day` +
                  (daysLeft !== null ? ` · ~${daysLeft} day${daysLeft === 1 ? "" : "s"} left at your pace` : "")
                : "No spend yet"}
            </div>
          </div>
          <button
            onClick={addCredit}
            disabled={addingCredit}
            className="shrink-0 rounded-lg bg-emerald-600 px-3 py-2 text-sm font-medium text-white hover:bg-emerald-500 disabled:opacity-50"
          >
            {addingCredit ? "Opening…" : "Add credit →"}
          </button>
        </div>
        {cockpit?.empty && (
          <div className="mt-3 rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-500">
            Your balance is empty. Add credit to enable a capability — your first preview costs about a penny.
          </div>
        )}
        {cockpit && !cockpit.empty && cockpit.lowBalance && (
          <div className="mt-3 rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-500">
            Running low — about {daysLeft} day{daysLeft === 1 ? "" : "s"} left at your current pace.
          </div>
        )}
      </div>

      {/* Guided journey — the single next rung, one tap to enable. */}
      {nextStep && (
        <div className="flex items-center justify-between gap-3 rounded-xl border border-indigo-500/30 bg-indigo-500/10 p-3">
          <div className="min-w-0">
            <div className="text-xs font-medium text-indigo-700 dark:text-indigo-300">Next: {nextStep.cta}</div>
            <div className="mt-0.5 text-[11px] text-surface-400">{nextStep.why}</div>
          </div>
          <button
            onClick={() => toggle(nextStep.key, true)}
            disabled={busy === nextStep.key}
            className="shrink-0 rounded-lg bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          >
            {busy === nextStep.key ? "…" : "Turn on"}
          </button>
        </div>
      )}

      {error && (
        <div className="rounded-lg bg-rose-500/10 px-3 py-2 text-xs text-rose-400">{error}</div>
      )}

      {/* The à-la-carte shelf — one card per capability, independent. */}
      <div className="space-y-2">
        <div className="text-xs uppercase tracking-wide text-surface-500">Your app's capabilities</div>
        {CAPABILITIES.map((cap) => {
          const on = cockpit?.enabled?.[cap.key] === true;
          const isBusy = busy === cap.key;
          return (
            <div
              key={cap.key}
              className="flex items-start gap-3 rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]"
            >
              <div className="mt-0.5 text-xl leading-none">{cap.icon}</div>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium text-surface-100">{cap.title}</span>
                  <span className="rounded bg-surface-700/40 px-1.5 py-0.5 text-[10px] text-surface-400">
                    replaces {cap.replaces}
                  </span>
                </div>
                <p className="mt-0.5 text-xs text-surface-500">{cap.blurb}</p>
                <div className="mt-1 text-[11px] text-surface-500">{cap.priceHint}</div>
              </div>
              <button
                onClick={() => toggle(cap.key, !on)}
                disabled={isBusy}
                aria-pressed={on}
                className={
                  "relative mt-1 inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 " +
                  (on ? "bg-emerald-600" : "bg-surface-600")
                }
                title={on ? "On — turn off" : "Off — turn on"}
              >
                <span
                  className={
                    "inline-block h-5 w-5 transform rounded-full bg-white transition-transform " +
                    (on ? "translate-x-5" : "translate-x-0.5")
                  }
                />
              </button>
            </div>
          );
        })}
        <p className="px-1 pt-1 text-[11px] text-surface-500">
          Each is independent — turn on only what you need. Prefer to run it yourself?{" "}
          <a
            href="https://yaver.io/docs/byo"
            target="_blank"
            rel="noopener noreferrer"
            className="text-emerald-500 hover:underline"
          >
            Connect your own cloud → free
          </a>
          .
        </p>
      </div>

      {/* Honest per-capability breakdown — one tap away (doc §6 rule 4). */}
      {burn && burn.rows.length > 0 && (
        <div className="rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
          <button
            onClick={() => setShowBreakdown((s) => !s)}
            className="flex w-full items-center justify-between text-left"
          >
            <span className="text-xs uppercase tracking-wide text-surface-500">
              Where it went · last {burn.days} days
            </span>
            <span className="text-xs text-surface-400">
              {dollars(burn.totalChargedCents)} {showBreakdown ? "▴" : "▾"}
            </span>
          </button>
          {showBreakdown && (
            <div className="mt-3 space-y-1.5">
              {burn.rows.map((r) => (
                <div key={r.kind} className="flex items-center justify-between text-xs">
                  <span className="text-surface-300">{KIND_LABEL[r.kind] ?? r.kind}</span>
                  <span className="text-surface-400">
                    {dollars(r.chargedCents)}
                    {r.dryRunCents > 0 && (
                      <span className="ml-1 text-[10px] text-surface-500">(simulated)</span>
                    )}
                  </span>
                </div>
              ))}
              {burn.totalDryRunCents > 0 && (
                <p className="pt-1 text-[10px] text-surface-500">
                  "Simulated" = preview pricing during launch; not charged for real yet.
                </p>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export default CapabilityShelf;
