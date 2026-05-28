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
    tags: ["Yaver", "Cloud", "Hetzner", "AWS", "GCP", "Dev environments", "AI coding"],
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
    "yaver launch",
    "Yaver cloud image",
    "Hetzner Yaver",
    "AWS Yaver",
    "GCP Yaver",
    "cloud dev environment one command",
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
            Yaver Cloud Image: a dev box on any provider, in 90 seconds
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            Run one command. Get a Linux box on Hetzner, AWS, or GCP that&apos;s already signed
            in to your Yaver account, with claude-code, codex, and opencode authenticated
            from your existing devices. No tokens to copy, no second OAuth, no AMI hunting.
            This post is the install guide.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">From zero</h2>
            <p className="mb-3">On any Mac, Linux, or WSL2:</p>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`curl -fsSL https://yaver.io/install | bash
yaver auth
yaver launch hetzner`}
            </pre>
            <p className="mt-4">
              That&apos;s the whole flow. <code>curl yaver.io/install</code> handles the Node
              install if you don&apos;t already have it, then <code>npm install -g yaver-cli</code>.
              <code> yaver auth</code> opens your browser once for OAuth. <code>yaver launch
              hetzner</code> provisions a box on your Hetzner account, waits ~90 seconds for
              first boot, and mirrors your runner credentials to it. SSH in and{" "}
              <code>claude</code>, <code>codex</code>, <code>opencode</code> are already
              signed in.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Pick your provider</h2>
            <p className="mb-4">
              Every command below uses <em>your</em> cloud account, billed to you directly.
              Yaver never sees your provider credentials.
            </p>

            <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">Hetzner</h3>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`export HCLOUD_TOKEN=hcloud_xxx     # from console.hetzner.cloud → Security
yaver launch hetzner               # cax21 (arm64, ~€4/mo) by default
yaver launch hetzner --arch amd64  # cpx21 (amd64) if you need x86_64`}
            </pre>
            <p className="mt-3">
              Cheapest by a wide margin. arm64 (cax21) is the default because Yaver runs
              great on ARM and saves you money. The box comes up in Helsinki by default —
              change with the Hetzner region flags or HCLOUD_LOCATION env var.
            </p>

            <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">AWS</h3>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`# uses your default aws CLI credentials
yaver launch aws --region us-east-1
yaver launch aws --region eu-central-1 --arch arm64`}
            </pre>
            <p className="mt-3">
              Provisions a t4g.small (arm64, Graviton) or t3.small (amd64) from the public
              Yaver AMI in your region. If a Yaver AMI isn&apos;t published for your region
              yet, the launcher tells you so — for now stick to us-east-1, us-west-2,
              eu-central-1, eu-west-1, ap-southeast-1, ap-northeast-1.
            </p>

            <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">Google Cloud</h3>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`# uses your default gcloud project + creds
yaver launch gcp
yaver launch gcp --arch amd64`}
            </pre>
            <p className="mt-3">
              t2a-standard-1 (arm64, Ampere) or e2-small (amd64) from the public Yaver custom
              image. Defaults to europe-west4-a; override with{" "}
              <code>YAVER_GCP_ZONE</code>.
            </p>

            <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">
              An existing box you already own
            </h3>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`yaver launch ssh user@your-nas
yaver launch ssh root@homelab.lan
yaver launch ssh user@cheap-vps.example.com`}
            </pre>
            <p className="mt-3">
              Works on anything you can SSH into: a Raspberry Pi at home, an old NAS, a
              Hetzner box you set up by hand last year, your friend&apos;s spare VPS. We
              install yaver-cli via npm, drop the same pre-authorized credentials,
              <code> tmux new-session -d -s yaver "yaver serve"</code>, and the box appears
              in your fleet just like a fresh provision.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Or without a terminal</h2>
            <p>
              If you don&apos;t want to install anything locally, go to{" "}
              <Link className="underline hover:text-surface-100" href="/launch">
                yaver.io/launch
              </Link>{" "}
              instead. Sign in, pick a provider, click the launch button. It drops you into
              your provider&apos;s native console (CloudFormation Launch Stack, GCP Deployment
              Manager, Hetzner Cloud Console) with our template + one-time code pre-filled.
              Click &ldquo;Create&rdquo;, wait ~2 minutes, the box appears in your Yaver
              fleet. Useful when you&apos;re on a borrowed laptop or driving Yaver from your
              phone.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">What you get</h2>
            <p className="mb-3">After <code>yaver launch …</code> returns:</p>
            <ul className="list-disc space-y-1 pl-6 text-surface-400">
              <li>
                A Linux box with the Yaver agent running on port 18080, registered as your
                device.
              </li>
              <li>
                <code>claude</code>, <code>codex</code>, and <code>opencode</code> pre-
                installed and signed in to <em>your</em> Anthropic / OpenAI / OpenRouter
                subscription via the same OAuth tokens you already have locally — no
                re-OAuth, no API-key fallback (
                <Link
                  className="underline hover:text-surface-100"
                  href="/blog/yaver-zero-reoauth"
                >
                  details
                </Link>
                ).
              </li>
              <li>
                Docker, Node, Python, Git, and the usual dev essentials. (For native Android
                or Cargo builds, use the fat sandbox image; for everything else the slim
                image is plenty — see the{" "}
                <Link
                  className="underline hover:text-surface-100"
                  href="/blog/yaver-sandbox-slim"
                >
                  sandbox post
                </Link>
                .)
              </li>
              <li>
                LAN beacon + relay-fallback networking so your phone, Mac, and the new box
                all reach each other regardless of NAT.
              </li>
              <li>SSH access via the key pair you used during provision.</li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Cleanup</h2>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`yaver devices                       # list everything
yaver ssh <box-name>                # ssh in
yaver cloud destroy <box-name>      # provider-aware teardown
                                    # (deletes the VM + decrements your bill)`}
            </pre>
            <p className="mt-3">
              Or use your provider&apos;s native UI — the Yaver tags on the resource make it
              easy to find. Boxes provisioned through <code>yaver launch</code> are tagged{" "}
              <code>managed-by=yaver-launch</code>.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Want the deep dive?</h2>
            <p>
              How the box claims itself on first boot without any browser interaction, how
              we work around Hetzner&apos;s no-public-snapshot constraint, and the device-
              code-authorize chain — written up at{" "}
              <Link
                className="underline hover:text-surface-100"
                href="/blog/yaver-cloud-launch-anywhere"
              >
                Yaver cloud launch: anywhere, in five steps
              </Link>
              .
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
