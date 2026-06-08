import type { Metadata } from "next";
import Link from "next/link";

export const metadata: Metadata = {
  title: "Licensing — Yaver",
  description:
    "Yaver uses a split-license model. Core components are FSL-1.1-Apache-2.0 (auto-converts to Apache-2.0 two years after each release). Client SDKs and CLIs are Apache-2.0 from day one.",
  alternates: { canonical: "https://yaver.io/licensing" },
  openGraph: {
    title: "Licensing — Yaver",
    description: "FSL-1.1 core + Apache-2.0 client SDKs.",
    url: "https://yaver.io/licensing",
    siteName: "Yaver",
    type: "article",
  },
  twitter: {
    card: "summary_large_image",
    title: "Licensing — Yaver",
    description:
      "FSL-1.1 core + Apache-2.0 client SDKs. Embed Yaver in closed-source apps freely.",
  },
};

const coreComponents: Array<{ path: string; what: string }> = [
  { path: "desktop/agent/", what: "Go agent (yaver serve, QUIC server, MCP server, task runner)" },
  { path: "relay/", what: "QUIC relay server for NAT traversal" },
  { path: "backend/", what: "Convex backend (auth + peer discovery + platform config)" },
  { path: "web/", what: "yaver.io web app (landing, dashboards, MCP console)" },
  { path: "mobile/", what: "Yaver mobile app" },
  { path: "desktop/app/", what: "Electron desktop app" },
  { path: "desktop/installer/", what: "Electron installer" },
  { path: "pi-image/", what: "Raspberry Pi image infrastructure" },
];

const sdkComponents: Array<{ path: string; what: string; pkg: string }> = [
  { path: "cli/", what: "yaver-cli push-to-device CLI", pkg: "yaver-cli on npm" },
  { path: "sdk/js/", what: "Programmatic JS/TS SDK", pkg: "yaver-sdk on npm" },
  { path: "sdk/feedback/react-native/", what: "React Native Feedback SDK", pkg: "yaver-feedback-react-native on npm" },
  { path: "sdk/feedback/web/", what: "Web Feedback SDK", pkg: "@yaver/feedback-web on npm" },
  { path: "sdk/feedback/flutter/", what: "Flutter Feedback SDK", pkg: "yaver_feedback on pub.dev" },
  { path: "sdk/flutter/", what: "Flutter programmatic SDK", pkg: "yaver on pub.dev" },
  { path: "sdk/python/", what: "Python programmatic SDK", pkg: "yaver on PyPI" },
  { path: "sdk/go/yaver/", what: "Go programmatic SDK", pkg: "go get" },
  { path: "sdk/go/clib/", what: "C shared library", pkg: "built per-platform" },
  { path: "sdk/errors-js/", what: "yaver-errors client", pkg: "yaver-errors on npm" },
];

export default function LicensingPage() {
  return (
    <div className="px-6 py-20">
      <article className="mx-auto max-w-3xl">
        <p className="text-xs uppercase tracking-[0.2em] text-surface-500">Licensing</p>
        <h1 className="mt-3 text-3xl font-bold text-surface-50 md:text-4xl">
          Split license: FSL core + Apache-2.0 SDKs
        </h1>
        <p className="mt-5 text-sm leading-7 text-surface-400">
          The core is copyleft-in-spirit (FSL, blocks competing hosted services
          for 2 years per release). Everything your own app imports is permissive
          (Apache-2.0) from day one.
        </p>

        <div className="mt-10 space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Rule of thumb</h2>
            <p>
              <em>Does your application import, bundle, or invoke this code?</em>
            </p>
            <ul className="mt-3 list-disc space-y-1 pl-6 text-surface-400">
              <li>
                <strong className="text-surface-200">Yes</strong> — it lives inside your process or binary →{" "}
                <span className="rounded bg-emerald-500/10 px-2 py-0.5 text-xs font-medium text-emerald-400">Apache-2.0</span>
              </li>
              <li>
                <strong className="text-surface-200">No</strong> — it runs as a network service →{" "}
                <span className="rounded bg-indigo-500/10 px-2 py-0.5 text-xs font-medium text-indigo-700 dark:text-indigo-300">FSL-1.1-Apache-2.0</span>
              </li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Core — FSL-1.1-Apache-2.0</h2>
            <p className="text-surface-400">
              Free for any non-competing use. The one restriction: you can&apos;t host a
              commercial product or service that substitutes for Yaver or offers
              substantially similar functionality. Each release auto-converts to
              Apache-2.0 two years after publication; the restriction lifts for
              that version then.
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
                  {coreComponents.map((c) => (
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
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Client SDKs & CLIs — Apache-2.0</h2>
            <p className="text-surface-400">
              Import, bundle, or invoke from anything — closed-source commercial
              apps included. Your app stays under whatever license you choose.
            </p>
            <div className="mt-4 overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-surface-800 text-left text-xs uppercase tracking-[0.12em] text-surface-500">
                    <th className="py-2 pr-4 font-medium">Path</th>
                    <th className="py-2 pr-4 font-medium">What</th>
                    <th className="py-2 font-medium">Package</th>
                  </tr>
                </thead>
                <tbody className="text-surface-400">
                  {sdkComponents.map((c) => (
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
            <h2 className="mb-3 text-xl font-semibold text-surface-100">In practice</h2>
            <ul className="list-disc space-y-2 pl-6 text-surface-400">
              <li>
                <strong className="text-surface-200">Using yaver-cli to build your app</strong>: permitted. Your app is yours.
              </li>
              <li>
                <strong className="text-surface-200">Embedding an SDK</strong>: Apache-2.0, no restrictions, ever.
              </li>
              <li>
                <strong className="text-surface-200">Self-hosting unmodified Yaver</strong> for your team or company:
                permitted, including commercially.
              </li>
              <li>
                <strong className="text-surface-200">Modifying the core for internal use</strong>: permitted.
              </li>
              <li>
                <strong className="text-surface-200">Running a paid SaaS that competes with Yaver Cloud</strong>: not
                permitted under FSL for 2 years per release; after that, the old version is Apache-2.0 and is fair game.
              </li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Commercial license</h2>
            <p className="text-surface-400">
              Need the core without the Competing Use restriction (for example,
              bundling a modified agent into a closed-source hosted product)?
              Email{" "}
              <a className="underline hover:text-surface-100" href="mailto:kivanc.cakmak@simkab.com">
                kivanc.cakmak@simkab.com
              </a>.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Contributions</h2>
            <p className="text-surface-400">
              See{" "}
              <a
                className="underline hover:text-surface-100"
                href="https://github.com/kivanccakmak/yaver.io/blob/main/CONTRIBUTING.md"
              >
                CONTRIBUTING.md
              </a>
              . Contributions use the file&apos;s own license; SIMKAB ELEKTRIK
              retains relicensing rights for the commercial tier. DCO sign-off
              required (<code className="rounded bg-surface-900 px-1 text-xs">git commit -s</code>).
            </p>
          </section>

          <section className="rounded-xl border border-surface-800 bg-surface-900/40 p-5">
            <p className="text-xs text-surface-500">
              <strong className="text-surface-300">Disclaimer:</strong> not legal
              advice. Consult an attorney for specific licensing constraints.
              Canonical text lives in{" "}
              <a
                className="underline hover:text-surface-100"
                href="https://github.com/kivanccakmak/yaver.io/blob/main/LICENSE"
              >
                LICENSE
              </a>{" "}
              and{" "}
              <a
                className="underline hover:text-surface-100"
                href="https://github.com/kivanccakmak/yaver.io/blob/main/LICENSING.md"
              >
                LICENSING.md
              </a>{" "}
              in the repo.
            </p>
          </section>

          <section>
            <p className="text-surface-400">
              Back to <Link className="underline hover:text-surface-100" href="/">yaver.io</Link>.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
