import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "unity-feedback-sdk-self-hosted-iteration";
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
    tags: ["Unity", "Game Development", "Self-Hosted", "Feedback SDK"],
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

export default function UnityFeedbackSdkBlogPage() {
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
            Yaver for Unity, Explained Simply
          </h1>
          <p className="text-surface-400">
            If you are not a Unity expert, the short version is simple: Yaver is trying to make the
            loop from “I found a bug in my game” to “the fix is tested and running again” much
            shorter, while keeping the heavy work on machines you control.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">What Yaver is not doing</h2>
          <p>
            Yaver is not pretending Unity works exactly like React Native. It is not promising
            magical live code patching for every Unity game on every platform.
          </p>
          <p className="mt-4">
            That would sound nice, but it would also be misleading. Unity projects differ a lot,
            especially between mobile and desktop, Mono and IL2CPP, and game-specific build setups.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">What Yaver is doing</h2>
          <p>
            Yaver is building a Unity package that lives inside the game project and talks to a
            Yaver agent running on a machine the developer or studio owns.
          </p>
          <ul className="mt-4 list-disc space-y-2 pl-6 text-surface-400">
            <li>in-game feedback overlay</li>
            <li>screenshots, logs, and crash capture</li>
            <li>black-box history of what happened before the bug</li>
            <li>remote “vibing” tasks</li>
            <li>content refresh and scene reload hooks</li>
            <li>Unity tests on the agent machine</li>
            <li>desktop builds and relaunches</li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Why this matters for a solo developer</h2>
          <p>
            Imagine you are away from your desk and testing your game on a phone or a laptop.
            You hit a bug. Instead of sending yourself a vague note, the game can already know:
          </p>
          <ul className="mt-4 list-disc space-y-2 pl-6 text-surface-400">
            <li>what scene you were in</li>
            <li>what the logs said</li>
            <li>what the screen looked like</li>
            <li>what crash or exception happened</li>
          </ul>
          <p className="mt-4">
            That goes back to your own machine. The agent can keep working there, run tests,
            build again, relaunch a desktop player, or prepare a mobile redeploy.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Why this matters for a studio</h2>
          <p>
            A studio can use the same package, but point it at a stronger shared machine, a rented
            GPU box, or a private runner with a local model. That can reduce outside AI spend and
            keep more of the workflow on infrastructure the team already controls.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The main idea in one sentence</h2>
          <p>
            Yaver for Unity is not about copying Hermes. It is about giving Unity developers a
            self-hosted feedback and iteration loop that feels fast, honest, and useful.
          </p>
        </section>
      </article>
    </div>
  );
}
