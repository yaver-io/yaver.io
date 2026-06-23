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

type Section = "setup" | "permissions" | "listing" | "testers";

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

// TestersSection — first-class TestFlight / Play tester + build management,
// driven by the agent's store_* ops verbs (appstoreconnect.go / playpublish_api.go).
// apple manages individual beta testers; google manages the track's Google
// Groups + release rollout (Play has no per-email tester API — see the note).
function TestersSection({ listing, project }: { listing: StoreListing | null; project?: string }) {
  const [store, setStore] = useState<"apple" | "google">("apple");
  const [creds, setCreds] = useState<any>(null);
  const [groups, setGroups] = useState<any[]>([]);
  const [testers, setTesters] = useState<any[]>([]);
  const [builds, setBuilds] = useState<any[]>([]);
  const [googleGroups, setGoogleGroups] = useState<string[]>([]);
  const [note, setNote] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const [groupEmail, setGroupEmail] = useState("");

  const bundleId = listing?.bundleId || "";
  const packageName = listing?.packageName || "";

  const base = useCallback(
    () => ({ store, project: project || undefined, bundleId, packageName, track: "internal" }),
    [store, project, bundleId, packageName],
  );

  const load = useCallback(async () => {
    setMsg(null);
    setNote("");
    try {
      const c = new AgentClient();
      const cr = await c.callOps("store_credentials_status", { project: project || undefined });
      setCreds(cr.initial);
      if (store === "apple") {
        const [g, t, b] = await Promise.all([
          c.callOps("store_group_list", base()),
          c.callOps("store_tester_list", base()),
          c.callOps("store_build_list", base()),
        ]);
        setGroups(g.initial?.groups || []);
        setTesters(t.initial?.testers || []);
        setBuilds(b.initial?.builds || []);
        setGoogleGroups([]);
      } else {
        const [t, b] = await Promise.all([
          c.callOps("store_tester_list", base()),
          c.callOps("store_build_list", base()),
        ]);
        setGoogleGroups(t.initial?.googleGroups || []);
        setNote(t.initial?.note || "");
        setBuilds((b.initial?.releases || []).map((r: any) => ({ version: (r.versionCodes || []).join(", "), processingState: r.status })));
        setGroups([]);
        setTesters([]);
      }
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    }
  }, [store, base, project]);

  useEffect(() => {
    void load();
  }, [load]);

  const run = async (verb: string, extra: Record<string, unknown>, okMsg: string) => {
    setBusy(true);
    setMsg(null);
    try {
      const c = new AgentClient();
      const r = await c.callOps(verb, { ...base(), ...extra });
      if (r.ok === false) setMsg(r.error || "failed");
      else {
        setMsg(okMsg);
        await load();
      }
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const configured = store === "apple" ? creds?.apple?.configured : creds?.google?.configured;

  return (
    <div className="space-y-4">
      <div className="flex gap-1 rounded-lg bg-slate-100 p-1 dark:bg-surface-900">
        {(["apple", "google"] as const).map((s) => (
          <button
            key={s}
            onClick={() => setStore(s)}
            className={`flex-1 rounded-md px-3 py-1.5 text-sm font-semibold transition-colors ${
              store === s ? "bg-white text-slate-800 shadow-sm dark:bg-surface-700 dark:text-surface-50" : "text-slate-500 dark:text-surface-400"
            }`}
          >
            {s === "apple" ? "TestFlight" : "Play internal"}
          </button>
        ))}
      </div>

      {creds && !configured ? (
        <Card>
          <p className="text-sm text-amber-700 dark:text-amber-300">
            {store === "apple" ? "App Store Connect" : "Google Play"} credentials not configured for this project.
          </p>
          <p className="mt-1 text-xs text-slate-500 dark:text-surface-400">
            {store === "apple"
              ? "Add APP_STORE_KEY_PATH / _ID / _ISSUER to the vault (yaver vault add …)."
              : "Add PLAY_STORE_KEY_FILE (service-account JSON path) to the vault."}
          </p>
          {(store === "apple" ? creds?.apple?.detail : creds?.google?.detail) ? (
            <p className="mt-1 text-[11px] text-slate-400">{store === "apple" ? creds.apple.detail : creds.google.detail}</p>
          ) : null}
        </Card>
      ) : null}

      {msg ? (
        <p className="rounded-md border border-sky-500/30 bg-sky-500/5 px-2.5 py-1.5 text-xs text-sky-700 dark:text-sky-300">{msg}</p>
      ) : null}

      {/* Builds */}
      <Card>
        <div className="mb-2 flex items-center justify-between">
          <div className="text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
            {store === "apple" ? "TestFlight builds" : "Internal track releases"}
          </div>
          <button
            disabled={busy}
            onClick={() => run("store_release_promote", store === "apple" ? {} : { status: "completed" }, store === "apple" ? "Latest build assigned to the default group." : "Internal release rolled out to testers.")}
            className="rounded-lg border border-violet-500/50 bg-violet-500/10 px-3 py-1 text-xs font-semibold text-violet-700 hover:bg-violet-500/20 disabled:opacity-50 dark:text-violet-300"
          >
            {store === "apple" ? "Assign latest → group" : "Roll out to testers"}
          </button>
        </div>
        {builds.length === 0 ? (
          <p className="text-sm text-slate-500 dark:text-surface-400">No builds.</p>
        ) : (
          <div className="space-y-1 text-xs">
            {builds.slice(0, 8).map((b, i) => (
              <div key={i} className="flex items-center justify-between">
                <span className="font-mono text-slate-700 dark:text-surface-200">{b.version}</span>
                <span className="text-slate-500 dark:text-surface-400">{b.processingState || ""}{b.expired ? " · expired" : ""}</span>
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* Testers / groups */}
      {store === "apple" ? (
        <Card>
          <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
            Beta testers ({testers.length}) · {groups.length} group(s)
          </div>
          <div className="mb-3 flex gap-2">
            <input
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="tester@email.com"
              className="flex-1 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-surface-700 dark:bg-surface-900"
            />
            <button
              disabled={busy || !email}
              onClick={() => run("store_tester_invite", { email }, `Invited ${email}.`)}
              className="rounded-lg border border-emerald-500/50 bg-emerald-500/10 px-3 py-1 text-sm font-semibold text-emerald-700 hover:bg-emerald-500/20 disabled:opacity-50 dark:text-emerald-300"
            >
              Invite
            </button>
          </div>
          <div className="space-y-1 text-xs">
            {testers.slice(0, 30).map((t, i) => (
              <div key={i} className="flex items-center justify-between gap-2">
                <span className="truncate text-slate-700 dark:text-surface-200">{t.email}</span>
                <span className="flex items-center gap-2">
                  <span className="text-slate-400">{t.state}</span>
                  <button onClick={() => run("store_tester_remove", { email: t.email }, `Removed ${t.email}.`)} className="text-rose-500 hover:underline">remove</button>
                </span>
              </div>
            ))}
          </div>
        </Card>
      ) : (
        <Card>
          <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-surface-400">
            Track Google Groups ({googleGroups.length})
          </div>
          {note ? <p className="mb-2 text-[11px] text-slate-500 dark:text-surface-400">{note}</p> : null}
          <div className="mb-3 flex gap-2">
            <input
              value={groupEmail}
              onChange={(e) => setGroupEmail(e.target.value)}
              placeholder="testers@yourdomain.com (a Google Group)"
              className="flex-1 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-surface-700 dark:bg-surface-900"
            />
            <button
              disabled={busy || !groupEmail}
              onClick={() => run("store_tester_invite", { groupEmail }, `Bound ${groupEmail} to the internal track.`)}
              className="rounded-lg border border-emerald-500/50 bg-emerald-500/10 px-3 py-1 text-sm font-semibold text-emerald-700 hover:bg-emerald-500/20 disabled:opacity-50 dark:text-emerald-300"
            >
              Bind
            </button>
          </div>
          <div className="space-y-1 text-xs">
            {googleGroups.map((g, i) => (
              <div key={i} className="flex items-center justify-between gap-2">
                <span className="truncate text-slate-700 dark:text-surface-200">{g}</span>
                <button onClick={() => run("store_tester_remove", { groupEmail: g }, `Unbound ${g}.`)} className="text-rose-500 hover:underline">unbind</button>
              </div>
            ))}
          </div>
        </Card>
      )}
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
    { key: "testers", label: "Testers" },
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
      ) : section === "testers" ? (
        <TestersSection listing={listing} project={path} />
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
