import Link from "next/link";

export default function RelaySetupManual() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <Link
          href="/manuals"
          className="mb-8 inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to Manuals
        </Link>

        <header className="mb-12">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            Yaver Relay Setup
          </h1>
          <p className="text-sm leading-relaxed text-surface-400">
            Yaver Relay is the supported remote path for reaching your machine
            from the phone or web dashboard. It works through NAT because the
            Yaver agent opens an outbound connection; you do not need to expose
            ports or operate separate network infrastructure.
          </p>
        </header>

        <section className="mb-10 rounded-lg border border-surface-800 bg-surface-900/60 p-6">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Normal setup
          </h2>
          <div className="terminal">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="select-all text-surface-200">
                  npm install -g yaver-cli
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="select-all text-surface-200">yaver auth</span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="select-all text-surface-200">
                  yaver serve
                </span>
              </div>
            </div>
          </div>
          <p className="mt-4 text-sm leading-relaxed text-surface-400">
            After the agent is running, sign in on the web dashboard or mobile
            app and pick your machine. Free Relay is shared and limited. Relay
            Pro gives you a private managed relay and higher limits.
          </p>
        </section>

        <section className="mb-10 grid gap-4 md:grid-cols-2">
          <Link
            href="/dashboard?tab=billing"
            className="rounded-lg border border-surface-800 bg-surface-900/60 p-5 transition-colors hover:border-surface-600"
          >
            <h2 className="mb-2 text-base font-semibold text-surface-100">
              Relay Pro
            </h2>
            <p className="mb-4 text-sm leading-relaxed text-surface-400">
              Use Relay Pro for daily remote work, private relay capacity, guest
              sessions, and fewer shared-relay limits.
            </p>
            <span className="text-xs font-medium text-surface-200">
              Open billing &rarr;
            </span>
          </Link>

          <Link
            href="/dashboard?tab=cloud"
            className="rounded-lg border border-surface-800 bg-surface-900/60 p-5 transition-colors hover:border-surface-600"
          >
            <h2 className="mb-2 text-base font-semibold text-surface-100">
              Cloud Workspace
            </h2>
            <p className="mb-4 text-sm leading-relaxed text-surface-400">
              Choose Cloud Workspace when you also want a saved Yaver-hosted dev
              machine for builds, app previews, and deployment work.
            </p>
            <span className="text-xs font-medium text-surface-200">
              Open cloud &rarr;
            </span>
          </Link>
        </section>

        <section className="rounded-lg border border-surface-800 bg-surface-900/60 p-6">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Advanced operators
          </h2>
          <p className="text-sm leading-relaxed text-surface-400">
            The relay implementation is still in the open-source repository and
            remains compatible with custom infrastructure. The public manual
            does not provide a step-by-step third-party relay deployment path
            because the product-supported path is Yaver Relay.
          </p>
        </section>
      </div>
    </div>
  );
}
