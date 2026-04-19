import type { Metadata } from "next";
import Link from "next/link";

export const metadata: Metadata = {
  title: "Licensing — Yaver",
  description:
    "Yaver uses a split-license model. Server & infrastructure components are AGPL-3.0-only; client SDKs and CLIs are Apache-2.0. Embed Yaver in closed-source commercial apps without copyleft contamination.",
  alternates: { canonical: "https://yaver.io/licensing" },
  openGraph: {
    title: "Licensing — Yaver",
    description:
      "Split-license model. AGPL-3.0 core + Apache-2.0 client SDKs.",
    url: "https://yaver.io/licensing",
    siteName: "Yaver",
    type: "article",
  },
  twitter: {
    card: "summary_large_image",
    title: "Licensing — Yaver",
    description:
      "AGPL-3.0-only core + Apache-2.0 client SDKs. Embed Yaver in closed-source apps freely.",
  },
};

const agplComponents: Array<{ path: string; what: string }> = [
  { path: "desktop/agent/", what: "The Go agent (yaver serve, QUIC server, MCP server, task runner)" },
  { path: "relay/", what: "QUIC relay server for NAT traversal" },
  { path: "backend/", what: "Convex backend (auth + peer discovery + platform config)" },
  { path: "web/", what: "yaver.io web app (landing, dashboards, MCP console)" },
  { path: "mobile/", what: "Yaver mobile app" },
  { path: "desktop/app/", what: "Electron desktop app" },
  { path: "desktop/installer/", what: "Electron installer" },
  { path: "pi-image/", what: "Raspberry Pi image infrastructure" },
];

const apacheComponents: Array<{ path: string; what: string; pkg: string }> = [
  { path: "cli/", what: "yaver-cli push-to-device CLI", pkg: "yaver-cli on npm" },
  { path: "sdk/js/", what: "Programmatic JS/TS SDK", pkg: "yaver-sdk on npm" },
  { path: "sdk/feedback/react-native/", what: "React Native Feedback SDK — FloatingButton, YaverFeedback, BlackBox", pkg: "yaver-feedback-react-native on npm" },
  { path: "sdk/feedback/web/", what: "Web Feedback SDK", pkg: "@yaver/feedback-web on npm" },
  { path: "sdk/feedback/flutter/", what: "Flutter Feedback SDK", pkg: "yaver_feedback on pub.dev" },
  { path: "sdk/flutter/", what: "Flutter programmatic SDK", pkg: "yaver on pub.dev" },
  { path: "sdk/python/", what: "Python programmatic SDK", pkg: "yaver on PyPI" },
  { path: "sdk/go/yaver/", what: "Go programmatic SDK", pkg: "imported via go get" },
  { path: "sdk/go/clib/", what: "C shared library (libyaver.so / .dylib)", pkg: "built per-platform" },
  { path: "sdk/errors-js/", what: "yaver-errors error-reporting client", pkg: "yaver-errors on npm" },
];

export default function LicensingPage() {
  return (
    <div className="px-6 py-20">
      <article className="mx-auto max-w-3xl">
        <p className="text-xs uppercase tracking-[0.2em] text-surface-500">Licensing</p>
        <h1 className="mt-3 text-3xl font-bold text-surface-50 md:text-4xl">
          The Yaver split-license model
        </h1>
        <p className="mt-5 text-sm leading-7 text-surface-400">
          Yaver uses <strong className="text-surface-200">two licenses</strong>, applied per component by role — not by language
          or directory depth. The core is strongly copyleft to prevent hosted clones; everything your app imports is
          permissive so you can ship it in closed-source commercial applications.
        </p>

        <div className="mt-10 space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Rule of thumb</h2>
            <p>
              <em>Does your application import, bundle, or link to this code?</em>
            </p>
            <ul className="mt-3 list-disc space-y-1 pl-6 text-surface-400">
              <li>
                <strong className="text-surface-200">Yes</strong> — it lives inside your process or binary →{" "}
                <span className="rounded bg-emerald-500/10 px-2 py-0.5 text-xs font-medium text-emerald-400">Apache-2.0</span>
              </li>
              <li>
                <strong className="text-surface-200">No</strong> — it runs as a separate service your app talks to →{" "}
                <span className="rounded bg-indigo-500/10 px-2 py-0.5 text-xs font-medium text-indigo-300">AGPL-3.0-only</span>
              </li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Server & infrastructure — AGPL-3.0-only
            </h2>
            <p className="text-surface-400">
              If you modify any of these and run them as a network service, you must publish your modifications
              under AGPL-3.0-only. Running them unmodified — even commercially — imposes no obligation.
            </p>
            <div className="mt-4 overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-surface-800 text-left text-xs uppercase tracking-[0.12em] text-surface-500">
                    <th className="py-2 pr-4 font-medium">Path</th>
                    <th className="py-2 font-medium">What it is</th>
                  </tr>
                </thead>
                <tbody className="text-surface-400">
                  {agplComponents.map((c) => (
                    <tr key={c.path} className="border-b border-surface-800/60">
                      <td className="py-3 pr-4 font-mono text-xs text-surface-300">{c.path}</td>
                      <td className="py-3">{c.what}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Client SDKs & CLIs — Apache-2.0
            </h2>
            <p className="text-surface-400">
              Import, bundle, or invoke these from any application — closed-source commercial apps included. Your
              app does not become AGPL because you use Yaver.
            </p>
            <div className="mt-4 overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-surface-800 text-left text-xs uppercase tracking-[0.12em] text-surface-500">
                    <th className="py-2 pr-4 font-medium">Path</th>
                    <th className="py-2 pr-4 font-medium">What it is</th>
                    <th className="py-2 font-medium">Package</th>
                  </tr>
                </thead>
                <tbody className="text-surface-400">
                  {apacheComponents.map((c) => (
                    <tr key={c.path} className="border-b border-surface-800/60">
                      <td className="py-3 pr-4 font-mono text-xs text-surface-300">{c.path}</td>
                      <td className="py-3 pr-4">{c.what}</td>
                      <td className="py-3 text-surface-500">{c.pkg}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">What this means in practice</h2>
            <ul className="list-disc space-y-2 pl-6 text-surface-400">
              <li>
                <strong className="text-surface-200">Building an app with Yaver</strong> (running yaver-cli, pushing
                bundles, scaffolding): your app is yours. Any license. Closed-source commercial is fine.
              </li>
              <li>
                <strong className="text-surface-200">Embedding Yaver SDKs in your app</strong> (FloatingButton,
                BlackBox, push client, programmatic SDKs): Apache-2.0. Your app stays under whatever license you
                choose. No AGPL obligations.
              </li>
              <li>
                <strong className="text-surface-200">Forking Yaver and running a competing service</strong>:
                modifications to the AGPL-licensed components must be published.
              </li>
              <li>
                <strong className="text-surface-200">Self-hosting Yaver unmodified</strong> for your team or
                company: no obligations to release anything.
              </li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Commercial license</h2>
            <p className="text-surface-400">
              A commercial license is available for organizations that want to use the AGPL-licensed components
              without AGPL obligations — for example, bundling a modified agent into a closed-source product.
              Contact{" "}
              <a className="underline hover:text-surface-100" href="mailto:kivanc.cakmak@simkab.com">
                kivanc.cakmak@simkab.com
              </a>.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Contributions</h2>
            <p className="text-surface-400">
              By submitting a pull request, you agree to the terms in{" "}
              <a
                className="underline hover:text-surface-100"
                href="https://github.com/kivanccakmak/yaver.io/blob/main/CONTRIBUTING.md"
              >
                CONTRIBUTING.md
              </a>
              : your contribution is licensed under the same license as the file you modify, and you grant SIMKAB
              ELEKTRIK the right to relicense contributions as part of the dual-licensing program. We use the{" "}
              <a className="underline hover:text-surface-100" href="https://developercertificate.org/">
                Developer Certificate of Origin
              </a>{" "}
              — every commit must be <code className="rounded bg-surface-900 px-1 text-xs">Signed-off-by</code>{" "}
              (<code className="rounded bg-surface-900 px-1 text-xs">git commit -s</code>).
            </p>
          </section>

          <section className="rounded-xl border border-surface-800 bg-surface-900/40 p-5">
            <p className="text-xs text-surface-500">
              <strong className="text-surface-300">Disclaimer:</strong> this page is not legal advice. If your
              organization has specific licensing constraints, consult an open-source attorney. The split-license
              model used here is standard practice (MongoDB, Elastic, Grafana, Sentry, Plausible all use variants)
              but exact obligations depend on your use case.
            </p>
          </section>

          <section>
            <p className="text-surface-400">
              The canonical source is{" "}
              <a
                className="underline hover:text-surface-100"
                href="https://github.com/kivanccakmak/yaver.io/blob/main/LICENSING.md"
              >
                LICENSING.md
              </a>{" "}
              in the repo. Back to <Link className="underline hover:text-surface-100" href="/">yaver.io</Link>.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
