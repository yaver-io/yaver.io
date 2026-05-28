import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-cloud-launch-anywhere";
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
    tags: ["Yaver", "Cloud", "Architecture", "Dev environments", "cloud-init"],
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
    "yaver launch architecture",
    "device-code authorize",
    "cloud-init pending-auth",
    "Hetzner snapshot constraint",
    "AWS AMI public",
    "GCP custom image public",
  ],
};

export default function CloudLaunchAnywherePage() {
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
            Yaver cloud launch: anywhere, in five steps
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            <code>yaver launch hetzner</code>, <code>yaver launch aws</code>,{" "}
            <code>yaver launch gcp</code>, <code>yaver launch ssh user@box</code>, the
            browser portal at yaver.io/launch — all five entry points end up running the
            same five-step chain. The architecture, and why the Hetzner branch works without
            ever having a public Yaver snapshot.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">The five steps</h2>
            <ol className="list-decimal space-y-3 pl-6 text-surface-400">
              <li>
                <strong>Mint a device-code.</strong> The launching device POSTs to{" "}
                <code>/auth/device-code</code> on the Yaver Convex deployment. It gets back a
                6-character user-code, a longer device-code, and a 15-minute expiry.
              </li>
              <li>
                <strong>Pre-authorize it.</strong> Same device POSTs to{" "}
                <code>/auth/device-code/authorize</code> with the user-code, signed with its
                own bearer token. Server-side, the code is now bound to the launching
                user&apos;s account. The new box can redeem it for a real session token
                without any browser interaction.
              </li>
              <li>
                <strong>Provision the box.</strong> Provider-specific:{" "}
                <code>hcloud server create</code>, <code>aws ec2 run-instances</code>,{" "}
                <code>gcloud compute instances create</code>, or for SSH adoption, an{" "}
                <code>ssh</code> into a box you already own. The provisioner injects a{" "}
                <code>#cloud-config</code> YAML whose <code>write_files</code> block drops
                the device-code into <code>/etc/yaver/pending-auth.json</code>.
              </li>
              <li>
                <strong>First-boot consumes the code.</strong> The agent&apos;s firstboot
                script runs <code>yaver auth --headless --background-wait</code>, which sees
                the pending file, polls <code>/auth/device-code/poll</code>, gets a session
                token under the same user, writes <code>config.json</code>, and starts{" "}
                <code>yaver-agent.service</code>. By the time the script exits, the box is
                heartbeating to Convex as one of your devices.
              </li>
              <li>
                <strong>Mirror runner credentials.</strong> The launching device polls Convex
                until the new device row appears online, then PushMirrorToPeer&apos;s your
                <code>~/.claude/.credentials.json</code>, codex token store, and opencode
                config across the encrypted runner-auth-mirror channel. The new box can now
                run <code>claude</code>, <code>codex</code>, and <code>opencode</code> on
                your existing Max Pro / ChatGPT Plus subscription with zero re-OAuth.
              </li>
            </ol>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Why Hetzner works without a public snapshot
            </h2>
            <p>
              AWS AMIs and GCP custom images can be flipped to world-launchable with one API
              call (<code>aws ec2 modify-image-attribute</code>, or{" "}
              <code>gcloud compute images add-iam-policy-binding allAuthenticatedUsers</code>).
              Hetzner snapshots can&apos;t — they&apos;re always scoped to the project that
              created them. The first attempt at <code>yaver launch hetzner</code> tries to
              use a snapshot from <code>cloud-images.json</code>; if there isn&apos;t one for
              your Hetzner project, the launcher falls back to vanilla{" "}
              <code>ubuntu-24.04</code> and embeds an extra cloud-init step that{" "}
              <code>npm install -g yaver-cli</code> on first boot.
            </p>
            <p className="mt-4">
              First boot is ~3 minutes slower than the snapshot path (apt + npm install), but
              post-boot is identical: same pending-auth consumption, same registration, same
              runner mirror. The CI workflow still builds a snapshot for our own project
              (used by the managed-cloud SKU), but it&apos;s no longer the only path.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              SSH adoption is the same chain, minus step 3
            </h2>
            <p>
              <code>yaver launch ssh user@host</code> ships the same pending-auth.json over
              an SSH connection, runs npm install in a heredoc, drops a detached{" "}
              <code>tmux</code> session running <code>yaver auth --headless</code> followed
              by <code>yaver serve</code>. The agent comes online the same way; the mirror
              step is unchanged. This is the path that lets you turn any cheap VPS, NAS, or
              home Linux box into a Yaver node without rebuilding it.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              The browser portal is just a UI for steps 1+2
            </h2>
            <p>
              <Link className="underline hover:text-surface-100" href="/launch">
                yaver.io/launch
              </Link>{" "}
              runs steps 1 and 2 client-side (the page calls Convex directly), then emits a
              provider-native deep link with the resulting user-code baked in. AWS:
              CloudFormation Launch Stack URL. GCP: Deployment Manager URL. Hetzner: Cloud
              Console with the cloud-init template referenced. The provider&apos;s UI handles
              step 3, the box handles steps 4+5 on its own once it boots. The portal is what
              lets someone with a phone, a tablet, or a borrowed laptop launch a Yaver box
              without ever installing the CLI.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Where to read the code</h2>
            <p>It&apos;s all open on GitHub:</p>
            <ul className="mt-3 list-disc space-y-1 pl-6 text-surface-400">
              <li>
                <code>desktop/agent/launch_cmd.go</code> — the dispatcher and shared helpers
                (manifest fetch, authorize-self, cloud-init build, runner-mirror loop)
              </li>
              <li>
                <code>desktop/agent/launch_hetzner.go</code> + <code>launch_aws.go</code> +{" "}
                <code>launch_gcp.go</code> + <code>launch_ssh.go</code> — provider-specific
                step 3
              </li>
              <li>
                <code>cloud-image/</code> — the rootfs overlay (cloud-init seeds, systemd
                units, firstboot script that consumes pending-auth.json)
              </li>
              <li>
                <code>cloud-images.json</code> — the manifest of published image IDs per
                provider+region+arch
              </li>
              <li>
                <code>scripts/build-cloud-image.sh</code> — the unified Hetzner/AWS/GCP image
                builder; can run locally with your provider creds, or in CI via
                .github/workflows/release-cloud-image.yml
              </li>
            </ul>
          </section>
        </div>
      </article>
    </div>
  );
}
