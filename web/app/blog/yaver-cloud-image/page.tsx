import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-cloud-image";
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
    tags: [
      "Yaver",
      "Cloud",
      "Hetzner",
      "AWS",
      "GCP",
      "AI coding",
      "Claude Code",
      "Codex",
      "OpenCode",
      "Dev environments",
    ],
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
    "cloud dev environment",
    "Hetzner cloud-init",
    "AWS CloudFormation",
    "GCP Deployment Manager",
    "Claude Code OAuth mirror",
    "device-code pre-authorize",
    "zero-friction launch",
  ],
};

export default function YaverCloudImageBlogPage() {
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
            Yaver Cloud Image: One Command for a Dev Box on Any Provider
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            <code>yaver launch hetzner</code> — and 90 seconds later you have a box that&apos;s
            already signed in to your Yaver account with claude-code, codex, and opencode
            authenticated. No copy-pasted tokens, no second OAuth on the new machine, no AMI
            hunting. This post walks through how the chain works and the four entry points
            we shipped: CLI, browser portal, SSH adoption, and raw artifacts.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">The problem</h2>
            <p>
              Spinning up a remote dev box should be a 90-second operation. In practice it&apos;s a
              25-minute project: pick a region, find an AMI, write cloud-init, SSH in, install
              Node, install your runners, copy your Anthropic / OpenAI subscription tokens
              over, fight some permissions, fight some path issues, give up, try again
              tomorrow.
            </p>
            <p className="mt-4">
              Yaver&apos;s wedge is that none of those steps should be visible to the user. We
              already mirror Claude Code, Codex, and OpenCode credentials between your devices
              (that&apos;s how the Yaver mobile app can drive a Mac without a second sign-in).
              Extending the same pattern to a brand-new cloud box is mostly plumbing — and the
              result is one command on your terminal, or one click on the browser portal.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">The chain</h2>
            <p className="mb-3">
              Every entry point ends up running the same five-step chain. The provider-specific
              bit is only step 3.
            </p>
            <ol className="list-decimal space-y-3 pl-6 text-surface-400">
              <li>
                <strong>Mint a device-code.</strong> The launching device calls Convex&apos;s
                <code> /auth/device-code</code> endpoint and gets back a fresh 6-character code
                that&apos;s valid for 15 minutes.
              </li>
              <li>
                <strong>Pre-authorize it as yourself.</strong> Same device calls{" "}
                <code>/auth/device-code/authorize</code> with the code and its own bearer
                token. The code is now bound to your user identity server-side — the new box
                can redeem it without any browser interaction.
              </li>
              <li>
                <strong>Provision a VM</strong> via the provider&apos;s API (
                <code>hcloud server create</code>, <code>aws ec2 run-instances</code>,{" "}
                <code>gcloud compute instances create</code>, or SSH for an existing Linux
                box). The cloud-init <code>user-data</code> embeds the device-code at{" "}
                <code>/etc/yaver/pending-auth.json</code>.
              </li>
              <li>
                <strong>First-boot consumes the code.</strong> The agent&apos;s firstboot script
                runs <code>yaver auth --headless --background-wait</code> which sees the
                pending file, polls Convex&apos;s <code>/poll</code> endpoint, gets a real session
                token under the same user, writes <code>config.json</code>, and starts{" "}
                <code>yaver-agent.service</code>.
              </li>
              <li>
                <strong>Mirror your runner credentials.</strong> The launching device finds
                the new box online (its heartbeat lands in Convex), and pushes
                <code> ~/.claude/.credentials.json</code>, the codex token store, and the
                opencode config across the encrypted{" "}
                <code>runner_auth_mirror</code> channel. The new box can now run{" "}
                <code>claude</code>, <code>codex</code>, and <code>opencode</code> on your
                existing Max Pro / ChatGPT Plus / OpenCode plan with zero re-OAuth.
              </li>
            </ol>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Four entry points</h2>
            <p className="mb-3">
              Same five-step chain, four ways to start it. Pick the one that matches where
              you&apos;re sitting.
            </p>

            <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">
              1. From your terminal
            </h3>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`yaver launch hetzner            # uses $HCLOUD_TOKEN
yaver launch aws --region us-east-1
yaver launch gcp                # uses gcloud default project
yaver launch ssh user@your-box  # adopt an existing Linux box`}
            </pre>
            <p className="mt-3">
              The provider arms all use the provider&apos;s own CLI under the hood (<code>hcloud</code>,
              <code> aws</code>, <code>gcloud</code>) and your existing credentials. Yaver
              never sees them. The SSH variant works on anything you can <code>ssh</code>{" "}
              into — Raspberry Pi at home, an old NAS, a Hetzner box you provisioned by
              hand last year, your friend&apos;s spare VPS.
            </p>

            <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">
              2. Browser portal (zero CLI required)
            </h3>
            <p>
              <Link className="underline hover:text-surface-100" href="/launch">
                yaver.io/launch
              </Link>{" "}
              shows a button per provider. Click &ldquo;AWS&rdquo; → you&apos;re dropped into the
              CloudFormation console with the template URL + your one-time code pre-filled.
              Click &ldquo;GCP&rdquo; → same thing but with Deployment Manager. Click
              &ldquo;Hetzner&rdquo; → cloud-init form deep-linked. No terminal, no
              <code> npm install</code>, no <code>brew</code>. Useful for PMs, designers, or
              anyone running Yaver from the mobile app and a laptop they don&apos;t want to set
              up.
            </p>

            <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">
              3. <code>curl yaver.io/install | bash</code>
            </h3>
            <p>
              The classic single-command installer. Autodetects platform (macOS, Linux,
              WSL2), installs Node 22 if it&apos;s missing (Homebrew, NodeSource, or per-user
              nvm fallback), then <code>npm install -g yaver-cli</code>. Once it&apos;s done you
              can run any of the <code>yaver launch …</code> commands above.
            </p>

            <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">
              4. Raw artifacts (KVM / Proxmox / Pi)
            </h3>
            <p>
              For hypervisor-based homelabs and embedded devices. The Raspberry Pi 5 image
              has been shipping for a while (
              <Link className="underline hover:text-surface-100" href="/blog/yaver-pi-image">
                announcement post
              </Link>
              ); qcow2 and ISO artifacts for KVM/Proxmox/Unraid are next. All built from the
              same{" "}
              <code>scripts/build-cloud-image.sh</code> pipeline + the{" "}
              <code>cloud-image/</code> rootfs overlay you can read in the repo.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Why the cloud-image works on Hetzner without a public snapshot
            </h2>
            <p>
              AWS AMIs and GCP custom images can be flipped to world-launchable with one API
              call. Hetzner snapshots can&apos;t — they&apos;re always scoped to the project that
              created them. We hit that constraint and ended up with a structurally better
              answer: when no public snapshot is published, <code>yaver launch hetzner</code>{" "}
              provisions vanilla Ubuntu 24.04 and lets cloud-init do the install. First boot
              is ~3 minutes slower (apt + <code>npm install -g yaver-cli</code>) but the
              post-boot behavior is identical — same pending-auth.json consumption, same
              registration, same runner mirror. The CI workflow still builds + tags a
              snapshot for the maintainer&apos;s project (used by the managed-cloud SKU and
              their own personal launches), but it&apos;s no longer the only path.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              The slim sandbox image
            </h2>
            <p>
              Same launch system also produces a distroless Docker sandbox image:
              <code> ghcr.io/kivanccakmak/yaver-sandbox-slim</code> (also on Docker Hub as{" "}
              <code>yaver/sandbox-slim</code>). Multi-arch, ~1.3 GB, built on{" "}
              <code>gcr.io/distroless/nodejs22-debian12</code>. Ships only Node, git,
              busybox, and the three coding runners — no Java / Ruby / Rust / Go, since
              tasks that need those should use the fat image. Useful for the{" "}
              <code>--containerize-guests</code> flag when the task is &ldquo;edit some code,
              run the project&apos;s own scripts&rdquo; and you want fast cold-start instead of a
              2 GB base.
            </p>
            <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`# from your config
{
  "containerize_guests": true,
  "container_image": "ghcr.io/kivanccakmak/yaver-sandbox-slim:latest"
}`}
            </pre>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Try it</h2>
            <p className="mb-3">From scratch on a machine that has nothing:</p>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`curl -fsSL https://yaver.io/install | bash
yaver auth
yaver launch hetzner    # or aws / gcp / ssh user@host`}
            </pre>
            <p className="mt-4">
              Or click your way through{" "}
              <Link className="underline hover:text-surface-100" href="/launch">
                yaver.io/launch
              </Link>{" "}
              and never open a terminal. If you want to read the code, everything lives in
              the open under{" "}
              <code>desktop/agent/launch_*.go</code>,{" "}
              <code>cloud-image/</code>, and <code>infra/</code> on GitHub.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
