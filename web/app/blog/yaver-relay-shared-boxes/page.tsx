import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-relay-shared-boxes";
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
    tags: ["Yaver Relay", "Guest Access", "Remote Dev", "Security"],
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

export default function YaverRelaySharedBoxesBlogPage() {
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
            Yaver Relay, Shared Boxes, and Yaver&apos;s Real Trust Boundary
          </h1>
          <p className="text-surface-400">
            The interesting part of &ldquo;Relay + Yaver&rdquo; is not the network hop. It&apos;s where the
            trust boundary actually lives once a host starts sharing a box with other people.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Relay is the pipe, not the principal</h2>
          <p>
            A host can put a Yaver box behind Yaver Relay and get a stable encrypted path from
            anywhere. That helps the owner on their own phone, and it can also help invited
            guests.
          </p>
          <p className="mt-4">
            But the guest is not authorized by the relay transport itself. The guest is
            authenticating to Yaver, and the target agent still enforces Yaver policy.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The real request path</h2>
          <pre className="overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`guest app
  -> Yaver Relay
  -> host's enrolled Yaver agent
  -> Yaver agent on 127.0.0.1:18080
  -> bearer validation + guest policy + project/machine checks
  -> endpoint handler`}
          </pre>
          <p className="mt-4">
            That means a guest does not need to configure networking. They need a Yaver account,
            an invitation from the host, and a permission scope that allows the requested action.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Where setups get fragile</h2>
          <ul className="list-disc space-y-2 pl-6 text-surface-400">
            <li>One account-level relay hint can be correct for one box and wrong for another box on the same host account.</li>
            <li>Relay credentials and session secrets are host-local; copying them into guest-visible metadata would be a mistake.</li>
            <li>Sharing &ldquo;all devices&rdquo; and &ldquo;all projects&rdquo; at once makes transport and policy harder to reason about.</li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The safer rule Yaver uses</h2>
          <p>
            Yaver can reuse a host relay hint for a shared device, but only when that shared
            view resolves to exactly one host box. That avoids guessing a transport hint for the
            wrong machine in multi-box setups.
          </p>
          <p className="mt-4">
            Permissions are still enforced separately through guest scope, allowed projects,
            machine scope, runner restrictions, and optional container isolation.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Practical guidance</h2>
          <ul className="list-disc space-y-2 pl-6 text-surface-400">
            <li>Use Yaver Relay for one stable remote path to an always-on box.</li>
            <li>Use guest access for bounded machine sharing.</li>
            <li>Use host-share for deeper repo/workspace sessions.</li>
            <li>Keep relay credentials and machine tokens on the host side only.</li>
            <li>Prefer machine-scoped and project-scoped shares over broad host-wide grants.</li>
          </ul>
        </section>
      </article>
    </div>
  );
}
