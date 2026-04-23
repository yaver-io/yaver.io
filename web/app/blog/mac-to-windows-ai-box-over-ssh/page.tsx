import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "mac-to-windows-ai-box-over-ssh";
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
    tags: ["Windows", "SSH", "Tailscale", "Ollama", "OpenCode"],
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
};

export default function MacToWindowsAiBoxBlogPage() {
  return (
    <div className="px-6 py-20">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(articleLd) }}
      />
      <article className="mx-auto max-w-3xl space-y-8 text-sm leading-7 text-surface-300">
        <Link href="/blog" className="inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50">
          &larr; Back to Blog
        </Link>

        <header className="space-y-4">
          <time dateTime={post.date} className="text-xs uppercase tracking-[0.2em] text-surface-500">
            {post.date}
          </time>
          <h1 className="text-3xl font-bold text-surface-50 md:text-4xl">
            Turning a Windows PC into a Remote AI Coding Box for a MacBook
          </h1>
          <p className="text-surface-400">
            This is the practical version of the setup many solo developers actually want:
            keep a stronger Windows machine on the desk, reach it from a MacBook over SSH,
            keep it awake with sane power settings, and run local coding models there instead of
            on the laptop.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The shape of the setup</h2>
          <pre className="overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`MacBook
  -> ssh user@carrotbytepc.tailc32088.ts.net
  -> Windows box on the same tailnet
  -> Ollama + qwen2.5-coder:14b
  -> OpenCode / terminal agents / editor workflow
  -> always-on machine for long-running coding tasks`}
          </pre>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Why Windows here can make sense</h2>
          <p>
            Plenty of developers already have a Windows gaming or workstation box with more RAM,
            more disk, and a GPU that is better suited to local models than the travel laptop.
            The MacBook becomes the thin client. The Windows machine becomes the always-on worker.
          </p>
          <p className="mt-4">
            That is especially useful if the laptop is a portable control surface and the heavier
            tasks are local model inference, long-running agent sessions, builds, or indexing.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The non-negotiable first step: reliable SSH</h2>
          <p>
            Before touching Ollama or any coding agent, the box needs a stable remote entry point.
            That means enabling the Windows OpenSSH server, using key-based auth from macOS,
            and making sure the Windows account is not blocked by blank-password remote login rules.
          </p>
          <p className="mt-4">
            In practice, that usually means either setting a password temporarily or installing the
            Mac&apos;s public key into the Windows OpenSSH key location. If the Windows account is in the
            local Administrators group, that often means
            <code className="mx-1 rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">C:\ProgramData\ssh\administrators_authorized_keys</code>
            instead of the user profile&apos;s own
            <code className="mx-1 rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">authorized_keys</code>.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Then make the box boring</h2>
          <p>
            The right remote dev machine is boring. It should not sleep in the middle of a model
            pull, hibernate during an agent run, or turn off in the middle of the day because
            nobody touched the keyboard.
          </p>
          <ul className="mt-4 list-disc space-y-2 pl-6 text-surface-400">
            <li>set sleep timeout to never while on AC</li>
            <li>disable hibernation if the machine is meant to stay available</li>
            <li>keep the SSH service on automatic startup</li>
            <li>use Tailscale for a stable private address instead of depending on LAN IP drift</li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Model choice matters more than bragging rights</h2>
          <p>
            A 32 GB Windows machine is enough to do useful local coding work, but that does not
            mean it should immediately jump to the biggest model in the list.
          </p>
          <p className="mt-4">
            For a general-purpose coding box, <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">qwen2.5-coder:14b</code>
            is the conservative recommendation. It is big enough to be useful, small enough to fit
            comfortably on a 32 GB machine, and much less annoying than forcing a larger model into
            constant paging or half-broken GPU offload.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Where Tailscale fits</h2>
          <p>
            Tailscale does not replace Windows OpenSSH. It gives the SSH path a stable private
            network and a hostname that still works when the machine leaves the local LAN, the DHCP
            lease changes, or the MacBook is somewhere else entirely.
          </p>
          <p className="mt-4">
            So the practical target becomes:
          </p>
          <pre className="mt-4 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`ssh user@carrotbytepc.tailc32088.ts.net
# or
ssh user@100.88.81.42`}
          </pre>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">What about Antigravity?</h2>
          <p>
            The useful mental model is simple: first make the SSH path good. Then let the editor
            reuse that path if it supports SSH-based remote development in your environment.
          </p>
          <p className="mt-4">
            That means the machine can already be productive from plain macOS Terminal on day one,
            and any Antigravity-style remote workflow becomes an editor layer on top of the same
            host instead of a separate transport problem.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The result</h2>
          <p>
            A MacBook on the couch, a Windows box on the desk, SSH between them, Tailscale for
            stable reachability, Ollama for local models, and a machine that stays awake long
            enough to be worth relying on. That is not flashy infrastructure. It is just a solid
            remote coding box.
          </p>
          <p className="mt-4 text-surface-400">
            The step-by-step version lives in the{" "}
            <Link href="/manuals/windows-ssh-coding-box" className="underline hover:text-surface-200">
              manuals section
            </Link>
            , and the shorter operational summary lives in the{" "}
            <Link href="/docs/developers" className="underline hover:text-surface-200">
              developer docs
            </Link>
            .
          </p>
        </section>
      </article>
    </div>
  );
}
