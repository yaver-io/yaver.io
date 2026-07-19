import Link from "next/link";

const options = [
  {
    title: "Free Relay",
    description:
      "Shared Yaver relay access for trying the product and light personal use. Fair limits keep the shared pool reliable.",
    action: "Start with the CLI",
    href: "/manuals/cli-setup",
  },
  {
    title: "Relay Pro",
    description:
      "A private managed relay for daily remote work, guest feedback sessions, and higher limits without running network infrastructure yourself.",
    action: "Open billing",
    href: "/dashboard?tab=billing",
  },
  {
    title: "Cloud Workspace",
    description:
      "Yaver-hosted compute with Relay Pro included. Use it when you want a saved development workspace, build machine, and deploy path without managing a box.",
    action: "Open cloud",
    href: "/dashboard?tab=cloud",
  },
];

export default function SelfHostingPage() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <Link
          href="/docs"
          className="mb-10 inline-flex text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to docs
        </Link>

        <header className="mb-12">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            Hosting and Relay Options
          </h1>
          <p className="text-sm leading-relaxed text-surface-400">
            Yaver is open source and remains compatible with custom network
            setups, but the normal user path is Yaver Relay. Free Relay is for
            trying the product, Relay Pro is the managed remote-access product,
            and Cloud Workspace adds Yaver-hosted compute.
          </p>
        </header>

        <section className="mb-12 grid gap-4 md:grid-cols-3">
          {options.map((option) => (
            <Link
              key={option.title}
              href={option.href}
              className="rounded-lg border border-surface-800 bg-surface-900/60 p-5 transition-colors hover:border-surface-600"
            >
              <h2 className="mb-3 text-base font-semibold text-surface-100">
                {option.title}
              </h2>
              <p className="mb-4 text-sm leading-relaxed text-surface-400">
                {option.description}
              </p>
              <span className="text-xs font-medium text-surface-200">
                {option.action} &rarr;
              </span>
            </Link>
          ))}
        </section>

        <section className="mb-10 rounded-lg border border-surface-800 bg-surface-900/60 p-6">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            What stays open
          </h2>
          <p className="text-sm leading-relaxed text-surface-400">
            The repository still contains the relay, agent, SDKs, and backend
            code. Advanced operators can inspect and adapt those components from
            source. Public product docs no longer walk normal users through
            third-party network or relay alternatives because Yaver Relay is the
            supported path we can make reliable.
          </p>
        </section>

        <section className="rounded-lg border border-surface-800 bg-surface-900/60 p-6">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Recommended setup
          </h2>
          <ol className="space-y-3 text-sm leading-relaxed text-surface-400">
            <li>
              <span className="font-medium text-surface-200">1.</span> Install
              Yaver CLI and sign in on your development machine.
            </li>
            <li>
              <span className="font-medium text-surface-200">2.</span> Start
              with Free Relay to confirm your phone, dashboard, and machine can
              connect.
            </li>
            <li>
              <span className="font-medium text-surface-200">3.</span> Upgrade
              to Relay Pro when Yaver becomes part of daily work, or choose
              Cloud Workspace when you also want Yaver-hosted compute.
            </li>
          </ol>
        </section>
      </div>
    </div>
  );
}
