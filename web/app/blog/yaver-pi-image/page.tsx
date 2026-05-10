import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-pi-image";
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
    tags: ["Raspberry Pi", "Yaver", "AI coding", "Ollama", "Claude Code", "Codex"],
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
    logo: {
      "@type": "ImageObject",
      url: "https://yaver.io/icon-512.png",
    },
  },
  keywords: [
    "Raspberry Pi 5",
    "ARM64 image",
    "headless dev node",
    "AI coding agent",
    "Ollama",
    "Aider",
    "Claude Code",
    "Codex",
  ],
};

export default function YaverPiImageBlogPage() {
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
            Announcing the Yaver Raspberry Pi 5 Dev-Node Image
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            A prebuilt ARM64 image that turns a Raspberry Pi 5 into a headless Yaver developer
            node. Flash it, boot it, pair it from your phone. No SSH, no distro package juggling, no
            manual install — the Pi is ready for Codex, Claude Code, Aider, or a local Ollama
            worker the first time you boot it.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Why this exists</h2>
            <p>
              A lot of solo developers want a cheap always-on machine they control themselves.
              The real goal isn&apos;t just &ldquo;run another box at home&rdquo; — it&apos;s to spend fewer
              premium AI tokens without losing the quality benefits of Codex, Claude Code, or
              OpenCode.
            </p>
            <p className="mt-4">
              The Pi image is aimed at that workflow. Frontier runners do the expensive
              planning and review. Local runners (Ollama + Qwen / GLM via Aider) do bounded
              edits, retries, and unit tests. Yaver acts as the dispatcher, verifier, and
              escalation layer between them.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Target hardware</h2>
            <p>
              Raspberry Pi 5 — recommended <strong>16 GB RAM, 256 GB NVMe</strong>. That
              configuration is strong enough to:
            </p>
            <ul className="mt-3 list-disc space-y-1 pl-6 text-surface-400">
              <li>Run the Yaver agent full-time as a systemd service</li>
              <li>Keep multiple repos and their build caches on-disk</li>
              <li>Host a practical local-model worker tier with Ollama</li>
              <li>Act as a remote dev host for Codex, Claude Code, or Aider sessions</li>
            </ul>
            <p className="mt-4">
              The artifact is <code>yaver-pi5-devnode-arm64.img.xz</code>, built on the stock
              Ubuntu Server Raspberry Pi arm64 image with Yaver&apos;s rootfs overlay and a
              cloud-init one-shot bootstrap.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">What&apos;s in the image</h2>
            <p className="mb-3">Preinstalled via cloud-init, available before first boot:</p>
            <ul className="list-disc space-y-1 pl-6 text-surface-400">
              <li><strong>System:</strong> ca-certificates, curl, jq, tmux, unzip, xz-utils</li>
              <li><strong>Dev base:</strong> git, gh (GitHub CLI), ffmpeg</li>
              <li><strong>Python:</strong> python3, python3-pip, python3-venv</li>
              <li><strong>Containers:</strong> docker.io + docker-compose-v2</li>
              <li><strong>Yaver agent:</strong> <code>/usr/local/bin/yaver</code></li>
            </ul>
            <p className="mt-4 mb-3">
              On first boot, Yaver runs <code>yaver install pi-dev-node</code> which layers
              on the full development stack:
            </p>
            <ul className="list-disc space-y-1 pl-6 text-surface-400">
              <li><strong>uv</strong> (fast Python tooling)</li>
              <li><strong>Mobile toolchain</strong> (Node.js, JDK, Android tooling stubs)</li>
              <li><strong>Ollama</strong> (local model host)</li>
              <li><strong>Aider</strong> + <strong>OpenCode</strong> (alternate AI runners)</li>
              <li>
                <strong>TDD kit:</strong> pre-commit, pytest, ruff, vitest, eslint, prettier
              </li>
              <li>
                <strong>Backend-dev:</strong> sqlite3, vercel, convex, supabase, postgresql-client,
                postgresql, redis-tools, redis-server, mqtt-broker, mqtt-clients
              </li>
            </ul>
            <p className="mt-4">
              Optional extras you can layer on from the motd: <code>yaver install tailscale</code>,
              <code> yaver install cloudflared</code>, <code>yaver install hybrid</code>
              (planner + local implementer mode).
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Flash + first boot</h2>
            <ol className="list-decimal space-y-3 pl-6 text-surface-400">
              <li>
                Download <code>yaver-pi5-devnode-arm64.img.xz</code> from the
                <Link className="underline hover:text-surface-100" href="/download#raspi"> download page</Link>.
              </li>
              <li>
                Flash it with Raspberry Pi Imager or <code>dd</code>:
                <pre className="mt-2 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`xz -d yaver-pi5-devnode-arm64.img.xz
sudo dd if=yaver-pi5-devnode-arm64.img of=/dev/sdX bs=64M status=progress conv=fsync`}
                </pre>
              </li>
              <li>
                Before unmounting, edit <code>/boot/firmware/yaver-firstboot.env</code> if you
                want to change the auto-update cadence (defaults to <code>daily</code>; also
                accepts <code>weekly</code> or <code>off</code>).
              </li>
              <li>
                Plug in Ethernet (or flash <code>wpa_supplicant.conf</code> onto the boot
                partition for Wi-Fi), power it up, and wait ~2–3 minutes for the first-boot
                script to finish. Status:
                <pre className="mt-2 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`ssh root@<pi-ip>
journalctl -u yaver-pi-firstboot.service -f`}
                </pre>
              </li>
              <li>
                Open the Yaver app on your phone. The Pi broadcasts its LAN beacon on UDP
                19837 and appears automatically in your device list. Tap it to pair — Yaver
                handles auth via Apple / Google / Microsoft, no passwords.
              </li>
            </ol>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Always-on mode</h2>
            <p>
              The image ships with <code>yaver-agent.service</code> enabled. It runs
              <code> yaver serve --multi-user --port 18080 --work-dir /var/lib/yaver/workspaces</code>
              as root, restarts on failure, and is wired into <code>multi-user.target</code> so
              the Pi is immediately reachable after a power cycle.
            </p>
            <p className="mt-4">
              Auto-updates run from a systemd timer (<code>yaver-pi-auto-update.timer</code>)
              and refresh three tiers at once:
            </p>
            <ul className="mt-3 list-disc space-y-1 pl-6 text-surface-400">
              <li>The Yaver binary itself (from the latest GitHub release)</li>
              <li>apt-managed system and dev packages</li>
              <li>Selected npm / pip / npx tools — Vercel, Convex, Supabase, Aider, etc.</li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Who it&apos;s for</h2>
            <p>
              This is not positioned as a &ldquo;run everything locally forever&rdquo; machine. It is a
              personal remote dev node that works best when paired with premium planners and
              a bounded local worker model. If you already run Codex / Claude Code on your
              laptop and want a dedicated, always-on, cheap box that handles background
              autodev loops and keeps your repos off of someone else&apos;s cloud, this is for
              you.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Where to start</h2>
            <p>
              Start from the
              <Link className="underline hover:text-surface-100" href="/download#raspi"> download page</Link>, then
              follow the
              <Link className="underline hover:text-surface-100" href="/manuals/raspberry-pi"> Raspberry Pi manual</Link>
              for pairing, always-on configuration, and how to pick a runner stack for your
              Pi.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
