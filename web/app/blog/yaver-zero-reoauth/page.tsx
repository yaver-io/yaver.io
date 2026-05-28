import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-zero-reoauth";
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
    tags: ["Yaver", "Claude Code", "Codex", "OpenCode", "OAuth", "Credential mirror"],
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
    "Claude Code OAuth across machines",
    "Codex token mirror",
    "OpenCode credential sync",
    "subscription auth without re-OAuth",
    "Max Pro ChatGPT Plus single subscription",
  ],
};

export default function ZeroReoauthPage() {
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
            Zero re-OAuth: how a fresh Yaver box arrives signed in to Claude Code, Codex,
            and OpenCode
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            New cloud boxes need new credentials. Everywhere else, that&apos;s a 10-minute
            chore of opening browser tabs and re-OAuthing every coding agent on the new
            machine. Yaver mirrors them across. Same Max Pro / ChatGPT Plus / OpenCode plan,
            no double-billing, no re-sign-in. The trick is two existing primitives wired
            together.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">The pain</h2>
            <p>
              Every coding agent CLI uses a subscription token kept in a per-user file:
              Claude Code at <code>~/.claude/.credentials.json</code>, Codex under{" "}
              <code>~/.config/codex/</code>, OpenCode under <code>~/.config/opencode/</code>.
              These tokens are bound to your account, not the device. Same token works
              everywhere; getting it onto a new device is the hard part.
            </p>
            <p className="mt-4">
              The default story on a fresh Linux box: SSH in, run{" "}
              <code>claude login</code>, watch it print a URL, copy it, open it on your
              phone, sign in, copy the resulting code back, paste it, repeat for codex,
              repeat for opencode. With a flaky connection, easily 10 minutes lost — and
              still doesn&apos;t scale when you spin up multiple boxes.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              The Yaver primitives that solve it
            </h2>
            <p className="mb-3">Two surfaces that already shipped for other reasons:</p>
            <h3 className="mt-4 mb-2 text-base font-semibold text-surface-100">
              1. Device-code pre-authorization
            </h3>
            <p>
              Convex&apos;s <code>/auth/device-code/authorize</code> endpoint accepts a
              user-code from any signed-in device and binds it to that user&apos;s account
              server-side. Originally written for the &ldquo;phone authorizes the Mac&rdquo;
              UX. Reused here: the launching machine mints a code AND authorizes it as
              itself, then ships the code to the new box. The new box redeems the (already
              authorized) code via the standard poll endpoint and writes its own{" "}
              <code>config.json</code> — no browser involved.
            </p>
            <h3 className="mt-4 mb-2 text-base font-semibold text-surface-100">
              2. Runner credential mirror
            </h3>
            <p>
              <code>runner_auth_mirror</code> is the agent-to-agent transfer for runner
              credential files. Source side reads the local{" "}
              <code>~/.claude/.credentials.json</code>, base64-encodes the bytes, signs
              with the owner&apos;s bearer token, POSTs to the target agent&apos;s{" "}
              <code>/runner/auth/mirror/accept</code>. Target writes the file under its own
              <code> ~/.claude/</code> (and equivalent paths for codex + opencode). The
              channel is end-to-end the Yaver auth boundary; the credential file never
              touches our servers.
            </p>
            <p className="mt-4">
              Originally shipped for the glass-OAuth feature where the mobile app drives a
              Mac without a second sign-in. Same primitive works for any new device the user
              owns.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Chained for launch</h2>
            <p>
              <code>yaver launch hetzner</code> (or aws / gcp / ssh) runs these steps in
              order:
            </p>
            <ol className="mt-3 list-decimal space-y-2 pl-6 text-surface-400">
              <li>Mint a device-code. Authorize it as yourself.</li>
              <li>
                Embed the code in <code>/etc/yaver/pending-auth.json</code> via cloud-init.
              </li>
              <li>Provision the box. Wait for first boot.</li>
              <li>Box redeems the code → it&apos;s now one of your devices.</li>
              <li>
                For each runner (claude / codex / opencode), if the launching device has
                the credential locally, push it to the new box via mirror.
              </li>
            </ol>
            <p className="mt-4">
              From the user&apos;s perspective: <code>yaver launch hetzner</code>, wait 90
              seconds, SSH in, run <code>claude</code>. Already signed in. Same for codex.
              Same for opencode. Same Max Pro plan, same ChatGPT Plus seat, same OpenCode
              GLM API key — no extra subscriptions, no double-billing, no re-OAuth.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              What we never do
            </h2>
            <ul className="list-disc space-y-1 pl-6 text-surface-400">
              <li>
                <strong>No API-key fallback.</strong> If you don&apos;t have a local
                subscription token (because you&apos;ve never run <code>claude login</code>{" "}
                locally), the mirror just skips that runner. You can sign in later from any
                device; the next mirror picks it up. We never propose{" "}
                <code>ANTHROPIC_API_KEY</code> or <code>OPENAI_API_KEY</code> as an
                alternative — that would silently double your bill.
              </li>
              <li>
                <strong>No credential storage on our servers.</strong> Convex sees identity
                + device registry, not credential bytes. The mirror is a direct
                agent-to-agent call.
              </li>
              <li>
                <strong>No headless / unattended runners.</strong> The CLI we spawn on the
                new box is the same interactive TUI you&apos;d use locally; usage stays on
                your subscription. The agent never invokes{" "}
                <code>claude -p &lt;prompt&gt;</code> or{" "}
                <code>codex --headless</code> on your behalf — that path uses Anthropic /
                OpenAI&apos;s API billing instead of your subscription, and would surprise
                you with a separate charge.
              </li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              When the mirror doesn&apos;t fire
            </h2>
            <p>
              If <code>yaver launch …</code> succeeds but a runner ends up unauthenticated
              (e.g. you launched from a device that hadn&apos;t signed into Codex), you can
              repair it later:
            </p>
            <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`yaver runner-auth mirror --to <new-box-name> --runner codex`}
            </pre>
            <p className="mt-3">
              The mirror is idempotent and can be re-run from any device that has the
              relevant local credential. The new box doesn&apos;t need to be repaved.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
