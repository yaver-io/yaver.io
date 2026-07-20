import Link from "next/link";
import { YAVER_CATALOG_APPS } from "@/lib/yaver-apps";

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

export default function AppsPage() {
  const catalogApps = YAVER_CATALOG_APPS;
  const games = catalogApps.filter((app) => app.kind === "game");
  const surfaces = Array.from(new Set(catalogApps.flatMap((app) => app.surfaces)));

  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-6xl">
        <div className="mb-12">
          <Link href="/" className="text-xs text-surface-500 hover:text-surface-50">
            &larr; Back to home
          </Link>
        </div>

        <section className="mb-14 grid gap-10 lg:grid-cols-[1.1fr_0.9fr] lg:items-end">
          <div>
            <p className="mb-3 text-sm font-medium text-brand">Yaver Apps</p>
            <h1 className="max-w-3xl text-4xl font-bold leading-tight text-surface-50 md:text-5xl">
              Apps that can ship outside Yaver or join the Yaver catalog for identity, billing, MCP, cloud, and surfaces.
            </h1>
            <p className="mt-5 max-w-2xl text-base leading-relaxed text-surface-400">
              Games are the first vertical, but the contract is broader: Yaver-native apps expose commands,
              events, MCP tool packs, approval policy, billing rules, and surface support without forcing the
              developer to give up their own release path.
            </p>
          </div>

          <div className="rounded-xl border border-surface-800 bg-surface-900 p-5">
            <p className="text-sm font-semibold text-surface-100">Platform boundary</p>
            <dl className="mt-4">
              <ContractRow label="Catalog" value={`${catalogApps.length} seeded app contracts, including ${games.length} game contracts`} />
              <ContractRow label="External release" value="Allowed. Developers can ship their own app and pay only for Yaver services they keep using." />
              <ContractRow label="Yaver release" value="Optional reviewed catalog build with Yaver OAuth, Yaver billing, MCP packs, and revenue share." />
              <ContractRow label="Surfaces" value={surfaces.join(", ")} />
            </dl>
          </div>
        </section>

        <section className="grid gap-6 lg:grid-cols-2">
          {catalogApps.map((app) => (
            <article key={app.id} className="rounded-xl border border-surface-800 bg-surface-900 p-6">
              <div className="mb-4 flex flex-wrap gap-2">
                <Pill>{app.kind}</Pill>
                <Pill>{app.owner}</Pill>
                <Pill>{app.status}</Pill>
              </div>
              <h2 className="text-2xl font-bold text-surface-50">{app.title}</h2>
              <p className="mt-2 text-sm leading-relaxed text-surface-400">{app.subtitle}</p>

              <dl className="mt-6">
                <ContractRow label="Repo" value={app.repo ?? "Yaver first-party runtime"} />
                <ContractRow label="Source" value={app.developerWorkspace.sourceProviders.join(", ")} />
                <ContractRow label="Develop" value={app.developerWorkspace.lifecycle.join(" -> ")} />
                <ContractRow label="Deploy" value={app.developerWorkspace.deploymentTargets.filter((target) => target !== "catalog-release").join(", ")} />
                <ContractRow label="Billing" value={`${app.monetization.launchBilling}; catalog developer share ${app.monetization.revenueShare.defaultDeveloperShareBps / 100}% when applicable`} />
                <ContractRow label="MCP packs" value={app.mcp.toolPacks.join(", ")} />
                <ContractRow label="Surfaces" value={app.surfaces.join(", ")} />
              </dl>

              {app.modules && app.modules.length > 0 ? (
                <div className="mt-6">
                  <h3 className="text-sm font-semibold text-surface-100">Modules</h3>
                  <div className="mt-3 grid gap-3">
                    {app.modules.map((module) => (
                      <div key={module.id} className="rounded-lg border border-surface-800 bg-surface-950 p-4">
                        <div className="flex flex-wrap items-center gap-2">
                          <p className="text-sm font-semibold text-surface-100">{module.title}</p>
                          <Pill>{module.multiplayer}</Pill>
                          <Pill>TV {module.tvOptimized}</Pill>
                        </div>
                        <p className="mt-2 text-xs leading-relaxed text-surface-400">{module.notes}</p>
                      </div>
                    ))}
                  </div>
                </div>
              ) : null}
            </article>
          ))}
        </section>

        <section className="mt-10 rounded-xl border border-surface-800 bg-surface-900 p-6">
          <h2 className="text-xl font-bold text-surface-50">Revenue posture</h2>
          <p className="mt-3 max-w-4xl text-sm leading-relaxed text-surface-400">
            Yaver should not trap developers. A developer can start from Tasks on a self-hosted Yaver box
            or Yaver Managed Cloud, keep code in Yaver Git, GitHub, GitLab, self-hosted Git, or a local repo,
            deploy private builds, and leave with the project intact. Yaver monetizes cloud inference,
            managed runners, relay, feedback, MCP hosting, testing, release automation, and optional catalog distribution. The catalog build
            is where Yaver OAuth, Yaver billing, reviewed MCP packs, multi-surface placement, and revenue
            share apply.
          </p>
        </section>
      </div>
    </div>
  );
}
