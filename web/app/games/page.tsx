import Link from "next/link";
import { CARROTBET_YAVER_APP, SFMG_YAVER_APP, YAVER_FIRST_PARTY_GAMES } from "@/lib/yaver-apps";

function Pill({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-900 px-2.5 py-1 text-xs text-surface-300">
      {children}
    </span>
  );
}

function ContractRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="border-t border-surface-800 py-3 first:border-t-0">
      <dt className="text-xs uppercase tracking-wide text-surface-500">{label}</dt>
      <dd className="mt-1 text-sm text-surface-200">{value}</dd>
    </div>
  );
}

export default function GamesPage() {
  const sfmg = SFMG_YAVER_APP;
  const carrotbet = CARROTBET_YAVER_APP;

  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-6xl">
        <div className="mb-12">
          <Link href="/" className="text-xs text-surface-500 hover:text-surface-50">
            &larr; Back to home
          </Link>
        </div>

        <section className="mb-14 grid gap-10 lg:grid-cols-[1.15fr_0.85fr] lg:items-end">
          <div>
            <p className="mb-3 text-sm font-medium text-brand">Yaver Games</p>
            <h1 className="max-w-3xl text-4xl font-bold leading-tight text-surface-50 md:text-5xl">
              First-party AI strategy games that run on Yaver identity, cloud, and remote runners.
            </h1>
            <p className="mt-5 max-w-2xl text-base leading-relaxed text-surface-400">
              SFMG is the first integration target: a free Yaver-hosted football manager and owner
              simulation with Yaver OAuth required from the first build. The same contract becomes
              the generic runtime for future strategy games and non-game simulations.
            </p>
          </div>

          <div className="rounded-xl border border-surface-800 bg-surface-900 p-5">
            <p className="text-sm font-semibold text-surface-100">Launch boundary</p>
            <dl className="mt-4">
              <ContractRow label="Catalog" value={`${YAVER_FIRST_PARTY_GAMES.length} first-party game plus Carrotbet as a developer-owned import target`} />
              <ContractRow label="Developer path" value="Mobile sandbox -> private repo -> deploy/private preview -> optional Yaver catalog review." />
              <ContractRow label="Billing" value="Free at launch; Yaver owns future IAP, Play Billing, and web entitlements." />
              <ContractRow label="Auth" value="Yaver OAuth/session required. No standalone SFMG account in the Yaver build." />
              <ContractRow label="Runtime" value="Server-authoritative commands, event log, AI intent parser, remote-runner testing." />
            </dl>
          </div>
        </section>

        <section className="grid gap-6 lg:grid-cols-[0.95fr_1.05fr]">
          <article className="rounded-xl border border-surface-800 bg-surface-900 p-6">
            <div className="mb-4 flex flex-wrap gap-2">
              {sfmg.categories.map((genre) => (
                <Pill key={genre}>{genre}</Pill>
              ))}
            </div>
            <h2 className="text-2xl font-bold text-surface-50">{sfmg.title}</h2>
            <p className="mt-2 text-sm leading-relaxed text-surface-400">{sfmg.subtitle}</p>

            <div className="mt-6 grid gap-3 sm:grid-cols-2">
              <div className="rounded-lg border border-surface-800 bg-surface-950 p-4">
                <p className="text-xs uppercase tracking-wide text-surface-500">Modes</p>
                <p className="mt-2 text-sm text-surface-200">Manager, owner, tactical match, AI assistant</p>
              </div>
              <div className="rounded-lg border border-surface-800 bg-surface-950 p-4">
                <p className="text-xs uppercase tracking-wide text-surface-500">Surfaces</p>
                <p className="mt-2 text-sm text-surface-200">Web, mobile, Apple TV, Android TV, runner</p>
              </div>
            </div>

            <h3 className="mt-8 text-sm font-semibold text-surface-100">Integration plan</h3>
            <ol className="mt-3 space-y-2 text-sm text-surface-400">
              {sfmg.launchPlan.map((item) => (
                <li key={item} className="flex gap-3">
                  <span className="mt-1 h-1.5 w-1.5 flex-none rounded-full bg-brand" />
                  <span>{item}</span>
                </li>
              ))}
            </ol>

            <dl className="mt-8">
              <ContractRow label="Owners" value={sfmg.developerWorkspace.namedOwners?.join(", ") ?? "Yaver"} />
              <ContractRow label="Source providers" value={sfmg.developerWorkspace.sourceProviders.join(", ")} />
              <ContractRow label="Dev compute" value={sfmg.developerWorkspace.cloudAllocation.notes} />
              <ContractRow label="Runners" value={`${sfmg.developerWorkspace.codingRunners.supported.join(", ")}; OpenCode/GLM ${sfmg.developerWorkspace.codingRunners.opencodeGlm}`} />
            </dl>
          </article>

          <article className="rounded-xl border border-surface-800 bg-surface-900 p-6">
            <h2 className="text-xl font-bold text-surface-50">Generic Yaver strategy-game contract</h2>
            <p className="mt-2 text-sm leading-relaxed text-surface-400">
              A Yaver-first game is not a hidden app. It is a reviewed manifest plus a deterministic
              command system that Yaver can run, test, bill, moderate, and render across devices.
            </p>

            <dl className="mt-6">
              <ContractRow label="Identity" value="Yaver OAuth is the account of record for saves, leagues, guests, and devices." />
              <ContractRow label="Input" value="Controller, touch, voice, and AI text all resolve to validated game commands." />
              <ContractRow label="State" value="Server reducers own production state; local preview is only for development." />
              <ContractRow label="AI" value="LLMs parse intent, advise, narrate, and test. Reducers still decide legal state." />
              <ContractRow label="Beyond games" value="The same pattern fits training sims, ops rooms, education, business simulators, and workflow copilots." />
            </dl>

            <div className="mt-6 rounded-lg border border-surface-800 bg-surface-950 p-4">
              <p className="text-xs uppercase tracking-wide text-surface-500">Command path</p>
              <p className="mt-2 text-sm text-surface-200">
                intent -&gt; command -&gt; validation -&gt; reducer -&gt; event log -&gt; snapshot -&gt; render
              </p>
            </div>
          </article>
        </section>

        <section className="mt-6 rounded-xl border border-surface-800 bg-surface-900 p-6">
          <div className="mb-4 flex flex-wrap gap-2">
            {carrotbet.categories.map((category) => (
              <Pill key={category}>{category}</Pill>
            ))}
          </div>
          <h2 className="text-2xl font-bold text-surface-50">{carrotbet.title}</h2>
          <p className="mt-2 max-w-3xl text-sm leading-relaxed text-surface-400">{carrotbet.subtitle}</p>

          <div className="mt-6 grid gap-3 md:grid-cols-2">
            {carrotbet.modules?.map((module) => (
              <div key={module.id} className="rounded-lg border border-surface-800 bg-surface-950 p-4">
                <p className="text-sm font-semibold text-surface-100">{module.title}</p>
                <p className="mt-2 text-xs text-surface-400">{module.notes}</p>
                <p className="mt-3 text-xs text-surface-500">
                  Multiplayer: {module.multiplayer} · TV: {module.tvOptimized}
                </p>
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  );
}
