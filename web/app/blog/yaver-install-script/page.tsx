import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-install-script";
const post = postBySlug(POST_SLUG)!;
const POST_URL = `https://yaver.io/blog/${POST_SLUG}`;

export const metadata: Metadata = {
  title: `${post.title} — Yaver Blog`,
  description: post.description,
  alternates: { canonical: POST_URL },
  openGraph: {
    title: post.title,
    description: post.description,
    url: POST_URL,
    siteName: "Yaver",
    type: "article",
    publishedTime: post.date,
    authors: ["Yaver"],
    tags: ["Yaver", "Installer", "Node.js", "Homebrew", "NodeSource", "nvm"],
    images: [{ url: "/og-image.png", width: 1200, height: 630 }],
  },
  twitter: {
    card: "summary_large_image",
    title: post.title,
    description: post.description,
    images: ["/og-image.png"],
  },
};

const articleLd = {
  "@context": "https://schema.org",
  "@type": "BlogPosting",
  headline: post.title,
  description: post.description,
  datePublished: post.date,
  dateModified: post.date,
  url: POST_URL,
  mainEntityOfPage: POST_URL,
  image: "https://yaver.io/og-image.png",
  author: { "@type": "Organization", name: "Yaver", url: "https://yaver.io" },
  publisher: {
    "@type": "Organization",
    name: "Yaver",
    url: "https://yaver.io",
    logo: { "@type": "ImageObject", url: "https://yaver.io/icon-512.png" },
  },
  keywords: [
    "install yaver",
    "yaver install script",
    "curl yaver.io install",
    "yaver linux install",
    "yaver mac install",
    "yaver wsl install",
  ],
};

export default function YaverInstallScriptPage() {
  return (
    <div className="px-6 py-20">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(articleLd) }}
      />
      <article className="mx-auto max-w-3xl">
        <Link
          href="/blog"
          className="mb-8 inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to Blog
        </Link>

        <div className="mb-10">
          <time
            dateTime={post.date}
            className="text-xs uppercase tracking-[0.2em] text-surface-500"
          >
            {post.date}
          </time>
          <h1 className="mt-3 text-3xl font-bold text-surface-50 md:text-4xl">
            <code>curl yaver.io/install | bash</code>: what it actually does
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            One command, end-state is <code>yaver-cli</code> on your PATH. The script
            handles three things you&apos;d otherwise have to do yourself: detect platform +
            arch, ensure Node 18+ is installed (via the most idiomatic path for your OS),
            then <code>npm install -g yaver-cli</code>. Source is at{" "}
            <code>scripts/install.sh</code> in the repo.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Try it</h2>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`curl -fsSL https://yaver.io/install | bash`}
            </pre>
            <p className="mt-3">
              That&apos;s it. The script is idempotent — re-running upgrades both Node (if
              behind 18+) and <code>yaver-cli</code> in place.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Why a one-liner and not just &ldquo;npm install&rdquo;
            </h2>
            <p>
              <code>npm install -g yaver-cli</code> works perfectly — IF you already have
              Node 18+ on PATH. We can&apos;t assume that on a fresh Linux VPS, a fresh
              macOS laptop, or a WSL2 box that&apos;s never had a dev environment set up.
              The install script&apos;s job is to bridge from &ldquo;new machine&rdquo; to
              &ldquo;<code>npm install</code> works&rdquo; without making the user pick a
              Node installer.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Platform detection
            </h2>
            <p>Branches based on <code>uname</code>:</p>
            <ul className="mt-3 list-disc space-y-1 pl-6 text-surface-400">
              <li>
                <strong>macOS</strong> (Darwin) → Homebrew if present (<code>brew install
                node@22</code>), else nvm fallback.
              </li>
              <li>
                <strong>Debian / Ubuntu / WSL2</strong> (apt) → NodeSource setup script (
                <code>setup_22.x</code>), then <code>apt-get install nodejs</code>.
              </li>
              <li>
                <strong>Fedora / RHEL / CentOS</strong> (dnf, yum) → NodeSource RPM setup,
                then <code>dnf install nodejs</code>.
              </li>
              <li>
                <strong>Anything else</strong> → per-user nvm install under <code>~/.nvm</code>{" "}
                (no sudo, no system packages, never touches anything outside the user&apos;s
                home).
              </li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Arch detection
            </h2>
            <p>
              <code>uname -m</code> normalized to <code>amd64</code> or <code>arm64</code>.
              Anything else exits with a clear error — <code>yaver-cli</code>&apos;s npm
              postinstall fetches a per-platform binary from GitHub Releases, and we
              don&apos;t ship for armv6 / armv7 / mips / ppc.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              The npm-prefix question
            </h2>
            <p>
              <code>npm install -g</code> writes under{" "}
              <code>$(npm config get prefix)</code>. On Homebrew Node that&apos;s
              <code> /opt/homebrew</code> (writable by your user). On NodeSource Node
              that&apos;s <code>/usr</code> (needs sudo). The script tests writability and
              uses <code>sudo</code> only when necessary — never blanket sudos, never
              touches files you didn&apos;t expect.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              What it doesn&apos;t do
            </h2>
            <ul className="list-disc space-y-1 pl-6 text-surface-400">
              <li>
                <strong>No binary tarballs.</strong> The CLAUDE.md design doc is explicit:
                <code> npm install -g yaver-cli</code> is the only supported install path.
                The script ends in npm, not in <code>curl &gt; /usr/local/bin/yaver</code>.
                The old binary-tarball installer was removed in 1.99.124.
              </li>
              <li>
                <strong>No telemetry.</strong> The only network calls are: (1) downloading
                the script itself, (2) the package-manager calls you&apos;d run yourself if
                you weren&apos;t using this script.
              </li>
              <li>
                <strong>No background processes.</strong> The install ends and you control
                the next command. <code>yaver auth</code> is yours to run when you&apos;re
                ready.
              </li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">After the install</h2>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`yaver auth                    # one-time OAuth sign-in
yaver launch hetzner          # spin up a Yaver-ready cloud box
yaver launch ssh user@nas     # adopt an existing Linux box`}
            </pre>
            <p className="mt-3">
              See{" "}
              <Link className="underline hover:text-surface-100" href="/blog/yaver-cloud-image">
                the cloud-image tutorial
              </Link>{" "}
              for the full first-launch walkthrough.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Read the source</h2>
            <p>
              The script is small (~175 lines) and worth a look if you&apos;re wary of
              piping curl into bash on principle:
            </p>
            <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`curl -fsSL https://yaver.io/install   # the script itself, no piping`}
            </pre>
            <p className="mt-3">
              Or on GitHub at <code>scripts/install.sh</code>. Same content; we just copy
              it into <code>web/public/install</code> and <code>web/public/install.sh</code>
              so the canonical URLs both work.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
