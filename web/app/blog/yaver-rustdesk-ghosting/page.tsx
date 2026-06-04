import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-rustdesk-ghosting";
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
    tags: ["RustDesk", "RPA", "computer use", "legacy ERP", "remote desktop", "Yaver"],
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
  keywords: ["RustDesk automation", "AI computer use", "legacy ERP integration", "RPA", "ghost mode", "remote desktop control"],
};

export default function YaverRustDeskGhostingBlogPage() {
  return (
    <div className="px-6 py-20">
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(articleLd) }} />
      <article className="mx-auto max-w-3xl">
        <Link href="/blog" className="mb-8 inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50">
          &larr; Back to Blog
        </Link>

        <div className="mb-10">
          <time dateTime={post.date} className="text-xs uppercase tracking-[0.2em] text-surface-500">
            {post.date}
          </time>
          <h1 className="mt-3 text-3xl font-bold text-surface-50 md:text-4xl">
            Ghost mode: drive any legacy desktop ERP through RustDesk, with AI
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            Some software has no API — or its write API costs more than the rest of the stack
            combined. Yaver&apos;s UI ghost operates the app&apos;s own screens the way a human clerk
            would: take a screenshot, ask a vision model where to act, then click and type. It works
            on the same machine or — via RustDesk — on a remote PC where you install nothing but a
            remote-desktop tool.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">The idea</h2>
            <p>
              A &ldquo;ghost&rdquo; is an AI that uses a GUI like a person. Yaver ships a cross-OS
              ghost engine — screen capture, mouse/keyboard injection, and an accessibility tree —
              for Windows, macOS, and Linux, plus a headless-browser path for web apps. On top sits a
              vision loop: screenshot → locate the target → act → verify. Reading stays cheap and
              direct (e.g. read-only SQL); only <em>writes</em> go through the GUI, so you never have
              to buy the app&apos;s expensive automation license.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">The blackbox pattern</h2>
            <p>
              The friction-killer for customers who won&apos;t let you touch their machines: install
              <strong> only RustDesk</strong> on the PC that runs the legacy app. Then drop in a
              pre-configured Yaver appliance (a Raspberry Pi is enough). The appliance:
            </p>
            <ul className="mt-3 list-disc space-y-1 pl-6 text-surface-400">
              <li>connects a RustDesk client to the PC — the app&apos;s screen is mirrored in a window;</li>
              <li>runs the ghost against that window: vision locates the field, input flows back through RustDesk to the PC;</li>
              <li>reads the underlying database read-only to <strong>verify</strong> every add / update / delete;</li>
              <li>keeps a metadata-only audit of what it did.</li>
            </ul>
            <p className="mt-4">
              Nothing of ours runs on the customer&apos;s PC. If they ever want out, they uninstall
              RustDesk and their system is untouched and fully current.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Remote-view as an abstraction</h2>
            <p>
              RustDesk is the default, not a hardcode. Yaver has a pluggable remote-view layer with a
              clean interface; <strong>RustDesk, AnyDesk, and VNC</strong> register out of the box and
              adding another is one struct. The agent manages the client lifecycle
              (<code>ghost_remote_connect</code> / <code>_status</code> / <code>_disconnect</code>),
              and exposes it over both MCP and a small HTTP surface so any UI can drive it.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Same PC or remote — the AI adapts</h2>
            <p>
              If the app runs on the same box as the ghost, it drives the local desktop directly. If
              it&apos;s on a remote PC, the ghost connects RustDesk first and operates the mirrored
              window — and the vision prompt is told it&apos;s looking at a remote view, so it ignores
              the RustDesk toolbar and acts on the app content. Same verbs either way; only
              &ldquo;connect first?&rdquo; differs.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Coordinates that actually land</h2>
            <p>
              Vision models output pixel coordinates, so the pixels have to be honest. Every prompt is
              told the exact screenshot dimensions. DPI is handled end to end: on a Retina Mac the
              capture is downscaled to logical points so a click maps 1:1; on Windows the agent goes
              DPI-aware so capture and cursor share one pixel space. The result is clicks that hit the
              field the model pointed at, not one scaled 2× off.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Watch it work — a live camera view</h2>
            <p>
              Operators don&apos;t trust a black box. The agent serves a live MJPEG stream
              (<code>/ghost/stream</code>) of the screen it&apos;s driving — a Bambu-printer-style
              camera view you can embed in a dashboard or phone. Frames are transient; nothing is
              stored unless you opt into a recording.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Local or cloud brain — your call</h2>
            <p>
              The grounding model runs through Yaver&apos;s normal provider chain: a configured runner
              (Claude Code / Codex / OpenRouter), a cloud key, or a local Ollama/vLLM model on the box
              — so a customer can keep everything on-prem. And when a customer&apos;s box has no AI at
              all, the brain runs in the cloud while the box only does the hands: it screenshots,
              sends the frame up, and applies the click that comes back.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Why this is the right shape</h2>
            <p>
              It&apos;s the Yaver thesis applied to software you can&apos;t change: meet the legacy
              system where it is, learn its screens with AI, write back verified, and keep it warm — no
              rip-and-replace, no API license, no agent on the customer&apos;s machine beyond a remote
              desktop they already trust.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
