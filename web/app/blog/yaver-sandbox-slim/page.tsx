import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-sandbox-slim";
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
    tags: ["Yaver", "Docker", "Distroless", "Claude Code", "Codex", "OpenCode", "Sandbox"],
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
    "yaver sandbox slim",
    "distroless docker",
    "Claude Code Docker image",
    "Codex Docker image",
    "OpenCode Docker image",
    "GHCR Docker Hub",
  ],
};

export default function YaverSandboxSlimPage() {
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
            yaver-sandbox-slim: a distroless Docker image with three coding agents pre-baked
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            <code>ghcr.io/kivanccakmak/yaver-sandbox-slim:latest</code> (and{" "}
            <code>yaver/sandbox-slim</code> on Docker Hub). 1.3 GB. Multi-arch. Built on{" "}
            <code>gcr.io/distroless/nodejs22-debian12</code> with Node, git, busybox, and
            the Claude Code / Codex / OpenCode CLIs. This post is the technical story —
            the multi-stage build, the busybox overlay, and the day we forgot{" "}
            <code>/usr/bin/env</code>.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Why slim</h2>
            <p>
              The original Yaver sandbox image (still around as <code>yaver-sandbox</code>)
              is ~2 GB. It ships Java, Ruby, Rust, Go, Python, build tools, and the three
              runners. Useful when the task is &ldquo;build the Android APK&rdquo; or
              &ldquo;cargo test the project,&rdquo; but overkill when the task is &ldquo;edit
              some TypeScript and run the project&apos;s own npm scripts.&rdquo;
            </p>
            <p className="mt-4">
              Slim is for the second case. We strip everything except: Node 22, git,
              busybox, the three coding runners, and the Yaver agent. ~1.3 GB instead of 2,
              and a much smaller cold-start cost when you&apos;re spinning up dozens of
              short-lived containerized tasks.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Use it</h2>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`# pull directly
docker pull ghcr.io/kivanccakmak/yaver-sandbox-slim:latest
# or via Docker Hub (shorter)
docker pull yaver/sandbox-slim:latest

# wire into Yaver's config.json for --containerize-* tasks
{
  "containerize_guests": true,
  "container_image": "ghcr.io/kivanccakmak/yaver-sandbox-slim:latest"
}`}
            </pre>
            <p className="mt-3">
              Or build it locally — the source Dockerfile is at{" "}
              <code>desktop/agent/Dockerfile.sandbox.slim</code>:
            </p>
            <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`yaver sandbox build --slim`}
            </pre>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              The three-stage build
            </h2>
            <p className="mb-3">
              Distroless doesn&apos;t have apt, doesn&apos;t have a shell, doesn&apos;t have
              <code> RUN</code> as a meaningful concept (no shell to run commands in). So
              every transform we want — installing runners, symlinking busybox applets,
              chowning workspaces — has to happen in a <em>build</em> stage, with the final
              stage being a single big <code>COPY --from=…</code>.
            </p>
            <ol className="list-decimal space-y-3 pl-6 text-surface-400">
              <li>
                <strong>Toolbox.</strong> <code>node:22-bookworm-slim</code> (same glibc as
                distroless/nodejs22-debian12 so binaries stay ABI-compatible). Apt-installs
                git, ca-certificates, tini, busybox-static. Npm-installs the three runners
                under <code>/opt/runners</code>. Then assembles a complete{" "}
                <code>/distroless-overlay</code> tree: git binary + git-core, busybox at{" "}
                <code>/bin/busybox</code> with symlinks for each applet we need, runners
                under <code>/opt/runners</code>, CLI shims under <code>/usr/local/bin</code>.
              </li>
              <li>
                <strong>Agent build.</strong> <code>golang:1.26-bookworm</code>. Cross-compiles
                the Yaver Go agent statically (<code>CGO_ENABLED=0</code>) and drops it at{" "}
                <code>/out/yaver</code>. Standard Go multi-stage pattern.
              </li>
              <li>
                <strong>Runtime.</strong>{" "}
                <code>gcr.io/distroless/nodejs22-debian12</code>. Three COPYs — the overlay
                tree, the agent binary, and ENV settings. ENTRYPOINT is <code>tini</code> so
                the runner subprocesses get reaped properly.
              </li>
            </ol>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              The dependency hunt
            </h2>
            <p>
              Distroless ships glibc, libssl, libstdc++, and the Node runtime — but not
              libpcre2, libcurl, or libpcre. git needs all three. The toolbox stage walks{" "}
              <code>ldd</code> across git, busybox, and tini, copies any missing libs, and
              lets the final COPY merge them with the distroless base. Belt and suspenders:
              extra libs that distroless already has just overwrite themselves with
              identical content.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              The day we forgot /usr/bin/env
            </h2>
            <p>
              First build, all three CLIs passed <code>--version</code> smoke tests except
              codex. The error was inscrutable:{" "}
              <code>[FATAL tini (7)] exec /usr/local/bin/codex failed: No such file or
              directory</code>. The symlink at <code>/usr/local/bin/codex</code> resolved
              fine, and the target Node script was right there. The actual problem was the
              shebang: <code>#!/usr/bin/env node</code>. Linux kernel exec resolves shebang
              paths literally, and we&apos;d put busybox-env at <code>/bin/env</code> but
              not <code>/usr/bin/env</code>. One symlink fix:
            </p>
            <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`for cmd in env sh bash; do
  ln -s /bin/busybox /distroless-overlay/usr/bin/$cmd
done`}
            </pre>
            <p className="mt-3">
              The same trick fixes any third-party Node CLI you might want to pip into the
              image later. Worth pre-empting.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Slim vs fat</h2>
            <table className="mt-3 w-full border-collapse text-left">
              <thead>
                <tr className="border-b border-surface-800 text-surface-100">
                  <th className="py-2 pr-4">When you want&hellip;</th>
                  <th className="py-2 pr-4">Use</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-900">
                  <td className="py-2 pr-4">edit TS/JS, run the project&apos;s npm scripts</td>
                  <td className="py-2 pr-4"><code>yaver-sandbox-slim</code></td>
                </tr>
                <tr className="border-b border-surface-900">
                  <td className="py-2 pr-4">edit Python, run pytest</td>
                  <td className="py-2 pr-4"><code>yaver-sandbox</code> (fat, has Python)</td>
                </tr>
                <tr className="border-b border-surface-900">
                  <td className="py-2 pr-4">native Android Gradle build</td>
                  <td className="py-2 pr-4"><code>yaver-sandbox</code> (Java + Android SDK)</td>
                </tr>
                <tr className="border-b border-surface-900">
                  <td className="py-2 pr-4">cargo test</td>
                  <td className="py-2 pr-4"><code>yaver-sandbox</code> (Rust toolchain)</td>
                </tr>
                <tr>
                  <td className="py-2 pr-4">spin up 50 short-lived containers fast</td>
                  <td className="py-2 pr-4"><code>yaver-sandbox-slim</code></td>
                </tr>
              </tbody>
            </table>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Build it yourself</h2>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`# local multi-arch build + push to your own GHCR + Docker Hub:
./scripts/release-sandbox-slim.sh --owner your-gh-username

# or single-arch for local-only Docker:
./scripts/release-sandbox-slim.sh --dry-run`}
            </pre>
            <p className="mt-3">
              The CI workflow at{" "}
              <code>.github/workflows/build-yaver-sandbox-slim.yml</code> does the same
              thing on every push to{" "}
              <code>desktop/agent/Dockerfile.sandbox.slim</code> or any agent Go source.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
