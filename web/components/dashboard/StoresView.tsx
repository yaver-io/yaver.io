"use client";

// StoresView — the single "Publish" surface. Three sections, one place (no
// tab-sprawl): Setup (the store-onboarding concierge), Permissions (Info.plist/
// entitlements/manifest inferred from code), and Listing (the canonical store
// listing + truthful privacy). All read the agent endpoints (/stores,
// /capabilities, /listing) so this component is the single source the dashboard
// AND the per-project sandbox "Ship" flow can render. For everything Yaver
// can't do (identity, payment, store review) it routes to the official page.

import { useCallback, useEffect, useState } from "react";
import {
  AgentClient,
  type StoreTask,
  type ManifestPlan,
  type StoreListing,
  type PublishReadiness,
} from "@/lib/agent-client";

type Section = "setup" | "permissions" | "listing";

const STATUS: Record<StoreTask["status"], { glyph: string; cls: string }> = {
  done: { glyph: "✓", cls: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300" },
  todo: { glyph: "○", cls: "bg-sky-500/15 text-sky-700 dark:text-sky-300" },
  action: { glyph: "◆", cls: "bg-amber-500/15 text-amber-700 dark:text-amber-300" },
  blocked: { glyph: "⋯", cls: "bg-slate-500/15 text-slate-500 dark:text-surface-400" },
  unknown: { glyph: "·", cls: "bg-slate-500/10 text-slate-400 dark:text-surface-500" },
};
const AUTOMATION: Record<StoreTask["automation"], { label: string; cls: string }> = {
  auto: { label: "Yaver does it", cls: "bg-violet-500/15 text-violet-700 dark:text-violet-300" },
  assisted: { label: "guided", cls: "bg-sky-500/15 text-sky-700 dark:text-sky-300" },
  manual: { label: "you (legal/payment)", cls: "bg-amber-500/15 text-amber-700 dark:text-amber-300" },
};
const GROUPS: { key: StoreTask["platform"]; label: string }[] = [
  { key: "apple", label: "Apple" },
  { key: "google", label: "Google" },
  { key: "both", label: "Cross-platform" },
];

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-slate-300 bg-white/60 p-4 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.6)]">
      {children}
    </div>
  );
}

function SetupTaskRow({ t }: { t: StoreTask }) {
  const [open, setOpen] = useState(false);
  const st = STATUS[t.status] ?? STATUS.unknown;
  const auto = AUTOMATION[t.automation];
  return (
    <div className="rounded-lg border border-slate-200 px-3 py-2.5 dark:border-surface-800">
      <button onClick={() => setOpen((v) => !v)} className="flex w-full items-start justify-between gap-3 text-left">
        <div className="flex min-w-0 items-start gap-2">
          <span className={`mt-0.5 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-xs ${st.cls}`}>{st.glyph}</span>
          <div className="min-w-0">
            <div className="font-medium text-slate-800 dark:text-surface-100">{t.title}</div>
            <div className="mt-0.5 line-clamp-2 text-xs text-slate-500 dark:text-surface-400">{t.summary}</div>
          </div>
        </div>
        <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-semibold ${auto.cls}`}>{auto.label}</span>
      </button>
      {open ? (
        <div className="mt-3 space-y-2 border-t border-slate-200 pt-3 text-xs dark:border-surface-800">
          {t.steps?.map((s, i) => (
            <div key={i} className="text-slate-600 dark:text-surface-300">{i + 1}. {s}</div>
          ))}
          {t.yaverCmd ? <code className="block overflow-x-auto rounded bg-slate-100 px-2 py-1 text-[11px] dark:bg-surface-900">{t.yaverCmd}</code> : null}
          {t.routeUrl ? (
            <a href={t.routeUrl} target="_blank" rel="noreferrer" className="inline-flex rounded-lg border border-sky-500/50 bg-sky-500/10 px-3 py-1.5 font-semibold text-sky-700 hover:bg-sky-500/20 dark:text-sky-300">
              Open the {t.platform === "apple" ? "Apple" : t.platform === "google" ? "Google" : "official"} page ↗
            </a>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function SetupSection({ tasks }: { tasks: StoreTask[] }) {
  return (
    <div className="space-y-4">
      {GROUPS.map((g) => {
        const rows = tasks.filter((t) => t.platform === g.key);
        if (!rows.length) return null;
        return (
          <Card key={g.key}>
            <div className="mb-3 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">{g.label}</div>
            <div className="space-y-2">{rows.map((t) => <SetupTaskRow key={t.id} t={t} />)}</div>
          </Card>
        );
      })}
    </div>
  );
}

function PermissionsSection({ plan }: { plan: ManifestPlan }) {
  const detected = plan.capabilities.filter((c) => c.detected);
  return (
    <div className="space-y-4">
      <Card>
        <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
          Detected from your code ({detected.length})
        </div>
        {detected.length === 0 ? (
          <p className="text-sm text-slate-500 dark:text-surface-400">No permission-bearing SDKs found.</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {detected.map((c) => (
              <span key={c.id} className="rounded bg-violet-500/15 px-2 py-0.5 text-xs font-medium text-violet-700 dark:text-violet-300">{c.title}</span>
            ))}
          </div>
        )}
      </Card>
      {Object.keys(plan.iosPlistUsage || {}).length > 0 ? (
        <Card>
          <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">iOS usage strings (AI refines)</div>
          <div className="space-y-2">
            {Object.entries(plan.iosPlistUsage).map(([k, v]) => (
              <div key={k} className="text-xs">
                <div className="font-mono text-slate-700 dark:text-surface-200">{k}</div>
                <div className="text-slate-500 dark:text-surface-400">{v}</div>
              </div>
            ))}
          </div>
        </Card>
      ) : null}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {plan.androidPermissions?.length ? (
          <Card>
            <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">Android permissions</div>
            <div className="space-y-1 text-xs font-mono text-slate-700 dark:text-surface-200">
              {plan.androidPermissions.map((p) => <div key={p}>{p}</div>)}
            </div>
          </Card>
        ) : null}
        {plan.iosEntitlements?.length ? (
          <Card>
            <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">iOS entitlements</div>
            <div className="space-y-1 text-xs font-mono text-slate-700 dark:text-surface-200">
              {plan.iosEntitlements.map((e) => <div key={e}>{e}</div>)}
            </div>
          </Card>
        ) : null}
      </div>
      {plan.consoleForms?.length ? <ConsoleForms forms={plan.consoleForms} /> : null}
    </div>
  );
}

function ListingSection({ listing }: { listing: StoreListing }) {
  return (
    <div className="space-y-4">
      <Card>
        <div className="grid grid-cols-2 gap-2 text-xs sm:grid-cols-4">
          {[["App", listing.appName], ["iOS", listing.bundleId], ["Android", listing.packageName], ["Version", listing.version]].map(([k, v]) => (
            <div key={k}>
              <div className="text-[10px] uppercase tracking-wider text-slate-400 dark:text-surface-500">{k}</div>
              <div className="truncate font-medium text-slate-700 dark:text-surface-200">{v || "—"}</div>
            </div>
          ))}
        </div>
        <div className="mt-3 border-t border-slate-200 pt-3 dark:border-surface-800">
          <div className="text-[10px] uppercase tracking-wider text-slate-400 dark:text-surface-500">Description (draft — AI refines)</div>
          <div className="mt-1 text-sm text-slate-700 dark:text-surface-200">{listing.description}</div>
        </div>
      </Card>
      <Card>
        <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
          Privacy / Data Safety (derived from code — must match behaviour)
        </div>
        {listing.privacy.length === 0 ? (
          <p className="text-sm text-slate-500 dark:text-surface-400">No data collected. Declare “No data collected”.</p>
        ) : (
          <div className="space-y-1.5">
            {listing.privacy.map((d, i) => (
              <div key={i} className="flex items-center justify-between gap-2 text-xs">
                <span className="font-medium text-slate-700 dark:text-surface-200">{d.category}</span>
                <span className="text-slate-500 dark:text-surface-400">
                  {d.purposes.join(", ")}{d.usedForTracking ? " · tracking" : ""}
                </span>
              </div>
            ))}
          </div>
        )}
      </Card>
      {listing.consoleForms?.length ? <ConsoleForms forms={listing.consoleForms} /> : null}
    </div>
  );
}

function ConsoleForms({ forms }: { forms: string[] }) {
  return (
    <Card>
      <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-amber-600 dark:text-amber-400">
        You submit these (legal / store review) — Yaver drafts + routes
      </div>
      <div className="space-y-1 text-xs text-slate-600 dark:text-surface-300">
        {forms.map((f, i) => <div key={i}>◆ {f}</div>)}
      </div>
    </Card>
  );
}

export default function StoresView({ token: _token, path }: { token?: string | null; path?: string }) {
  const [section, setSection] = useState<Section>("setup");
  const [stores, setStores] = useState<StoreTask[] | null>(null);
  const [caps, setCaps] = useState<ManifestPlan | null>(null);
  const [listing, setListing] = useState<StoreListing | null>(null);
  const [readiness, setReadiness] = useState<PublishReadiness | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    setErr(null);
    try {
      const c = new AgentClient();
      const [s, cp, l, rd] = await Promise.all([
        c.getStores(),
        c.getCapabilities(path),
        c.getListing(path),
        c.getPublishStatus(path),
      ]);
      setStores(s);
      setCaps(cp);
      setListing(l);
      setReadiness(rd);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setStores([]);
    }
  }, [path]);

  useEffect(() => {
    void load();
  }, [load]);

  const TABS: { key: Section; label: string }[] = [
    { key: "setup", label: "Setup" },
    { key: "permissions", label: "Permissions" },
    { key: "listing", label: "Listing" },
  ];

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-slate-800 dark:text-surface-100">Publish to the stores</h2>
        <p className="text-xs text-slate-500 dark:text-surface-400">
          Yaver derives your permissions + store listing from your code and does what it can; for the
          parts only you can do (identity, payment, review), it opens the exact official page.
        </p>
      </div>

      {readiness ? (
        <div
          className={`rounded-xl border px-4 py-3 ${
            readiness.ready
              ? "border-emerald-500/30 bg-emerald-500/5"
              : "border-amber-500/30 bg-amber-500/5"
          }`}
        >
          <div className={`text-sm font-semibold ${readiness.ready ? "text-emerald-700 dark:text-emerald-300" : "text-amber-700 dark:text-amber-300"}`}>
            {readiness.ready ? "✓ Ready to submit" : `✗ ${readiness.blockers.length} blocker${readiness.blockers.length === 1 ? "" : "s"} before you can ship`}
          </div>
          {!readiness.ready && readiness.blockers.length > 0 ? (
            <ul className="mt-1.5 space-y-0.5 text-xs text-amber-700/90 dark:text-amber-300/90">
              {readiness.blockers.map((b, i) => (
                <li key={i}>• {b}</li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      <div className="flex gap-1 rounded-lg bg-slate-100 p-1 dark:bg-surface-900">
        {TABS.map((t) => (
          <button
            key={t.key}
            onClick={() => setSection(t.key)}
            className={`flex-1 rounded-md px-3 py-1.5 text-sm font-semibold transition-colors ${
              section === t.key
                ? "bg-white text-slate-800 shadow-sm dark:bg-surface-700 dark:text-surface-50"
                : "text-slate-500 hover:text-slate-700 dark:text-surface-400"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {err ? (
        <p className="rounded-md border border-amber-500/30 bg-amber-500/5 px-2.5 py-1.5 text-xs text-amber-700 dark:text-amber-300">
          Couldn’t reach your device agent ({err}). Connect a device to see live status.
        </p>
      ) : null}

      {section === "setup" ? (
        stores === null ? <Loading /> : <SetupSection tasks={stores} />
      ) : section === "permissions" ? (
        caps === null ? <Loading /> : <PermissionsSection plan={caps} />
      ) : listing === null ? (
        <Loading />
      ) : (
        <ListingSection listing={listing} />
      )}
    </div>
  );
}

function Loading() {
  return <p className="text-sm text-slate-500 dark:text-surface-400">Loading…</p>;
}
