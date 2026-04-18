import Link from "next/link";

export default function YaverPiImageBlogPage() {
  return (
    <div className="px-6 py-20">
      <article className="mx-auto max-w-3xl">
        <Link
          href="/blog"
          className="mb-8 inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to Blog
        </Link>

        <div className="mb-10">
          <p className="text-xs uppercase tracking-[0.2em] text-surface-500">2026-04-18</p>
          <h1 className="mt-3 text-3xl font-bold text-surface-50 md:text-4xl">
            Announcing the Yaver Raspberry Pi 5 Dev-Node Image
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            We&apos;re adding a prebuilt ARM64 image for Raspberry Pi 5 that turns a Pi into a
            headless Yaver developer machine: pair it from your phone, install the economic
            hybrid stack, and use it as your personal always-on dev box.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Why this exists</h2>
            <p>
              A lot of solo developers want a cheap machine they control themselves. The real goal
              isn&apos;t just “run another box at home.” The goal is to spend fewer premium AI tokens
              without losing the quality benefits of Codex, Claude Code, or OpenCode.
            </p>
            <p className="mt-4">
              The Pi image is aimed at that exact workflow. Frontier runners do the expensive
              planning and review. Local runners do bounded edits, retries, and unit tests. Yaver
              acts as the dispatcher, verifier, and escalation layer between them.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">What the image targets</h2>
            <p>
              The primary target is a Raspberry Pi 5 with 16 GB RAM and 256 GB storage. That
              configuration is strong enough to run the Yaver agent full-time, keep repos and build
              caches on-disk, and support a practical local-model worker tier with Ollama.
            </p>
            <p className="mt-4">
              This is not positioned as a “run everything locally forever” machine. It is a
              personal remote dev node that works best when paired with premium planners and a
              bounded local worker model.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">What&apos;s in the first release</h2>
            <ul className="space-y-2 text-surface-400">
              <li>Headless Yaver agent bootstrap and pairing path</li>
              <li>Pi-focused install profile: <code>yaver install pi-dev-node</code></li>
              <li>Economic-stack groundwork: Ollama, Aider, OpenCode, GitHub CLI, uv</li>
              <li>Download plumbing through the public Convex-backed artifact pipeline</li>
              <li>Updated Raspberry Pi manual, download page, README, and developer docs</li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Where to start</h2>
            <p>
              The image is published from the same downloads pipeline as the other public Yaver
              artifacts. Start from the <Link className="underline hover:text-surface-100" href="/download#raspi">download page</Link>,
              then use the <Link className="underline hover:text-surface-100" href="/manuals/raspberry-pi">Raspberry Pi manual</Link> for
              setup, pairing, and always-on configuration.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
