import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-p2p-vault";
const post = postBySlug(POST_SLUG)!;
const POST_URL = `https://yaver.io/blog/${POST_SLUG}`;

export const metadata: Metadata = {
  title: `${post.title} - Yaver Blog`,
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
    tags: ["Yaver", "P2P", "Vault", "Secrets", "Deploys", "Cloudflare", "TestFlight"],
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
    "Yaver vault",
    "P2P secret sync",
    "encrypted local vault",
    "deploy credentials",
    "API key sync",
    "peer to peer developer tools",
  ],
};

export default function YaverP2PVaultBlogPage() {
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
            Yaver P2P Vault: secrets that follow your machines without touching our servers
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            Most deploy systems make you choose between pasting secrets into every box or
            uploading them to a hosted vault. Yaver takes a narrower path: secrets live in an
            encrypted vault on your machines, and when you ask for sync they move directly
            from one owned device to another over the same P2P transport Yaver already uses
            for tasks, builds, and terminals.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">The everyday flow</h2>
            <p className="mb-3">
              Add secrets once, scoped to the project that needs them:
            </p>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`yaver vault add CLOUDFLARE_API_TOKEN --project web --value <token>
yaver vault add CLOUDFLARE_ACCOUNT_ID --project web --value <account-id>

yaver vault add APP_STORE_KEY_ID --project mobile --value <key-id>
yaver vault add APP_STORE_KEY_ISSUER --project mobile --value <issuer-uuid>
yaver vault add APP_STORE_KEY_P8 --project mobile --value "$(cat AuthKey_ABC123.p8)"`}
            </pre>
            <p className="mt-4">
              Then source those values into a deploy without committing a <code>.env</code>{" "}
              file:
            </p>
            <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`eval "$(yaver vault env --project web)"
./scripts/deploy-web.sh

# Or run one command with the project vault injected:
yaver vault exec --project mobile -- ./scripts/deploy-testflight.sh`}
            </pre>
            <p className="mt-4">
              Project entries override global entries with the same name, so a global{" "}
              <code>OPENAI_API_KEY</code> can exist while <code>mobile/OPENAI_API_KEY</code>{" "}
              stays different. <code>yaver vault list --project '*'</code> shows names and
              metadata, never values. <code>yaver vault get NAME --project web</code> prints
              the value only when you explicitly ask for that one secret.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Sync to another machine
            </h2>
            <p>
              When you bring up a new dev box, Raspberry Pi, Mac mini, or managed cloud
              runner, sign it into the same Yaver account and run:
            </p>
            <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`yaver vault sync

# Or sync with one known peer:
yaver vault sync --from primary`}
            </pre>
            <p className="mt-4">
              Without <code>--from</code>, Yaver asks Convex for the list of devices on your
              account, skips the current machine, and attempts a peer sync with each one. The
              secret payload does not go to Convex. Convex is only the address book: device
              IDs, online status, and the relay information needed to find your own machines.
            </p>
            <p className="mt-4">
              The same operation is exposed to the dashboard and mobile app as{" "}
              <code>/vault/peer-sync</code>, so a UI can say "try syncing from this peer"
              without shelling out to a terminal.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              What is on disk
            </h2>
            <p>
              The vault file is <code>~/.yaver/vault.enc</code>. Current Yaver builds write
              the v2 format: a small magic header, a nonce, and a NaCl secretbox ciphertext.
              The plaintext inside that ciphertext is a sorted JSON array of entries:
              name, project, category, value, notes, created time, updated time, writer
              device ID, and a deleted flag.
            </p>
            <p className="mt-4">
              The encryption key is a per-machine 32-byte master key stored as{" "}
              <code>~/.yaver/master.key</code> with mode <code>0600</code>. On macOS, Yaver
              also mirrors that key into Keychain. A sidecar <code>master.key.meta</code>{" "}
              records the Yaver user ID, and the vault refuses to open for a different user
              on the same machine.
            </p>
            <p className="mt-4">
              Older vaults used Argon2id over an auth-token-derived passphrase. The current
              opener still understands that legacy file, decrypts it, and migrates it to the
              master-key-backed v2 format. That matters because auth tokens rotate; your
              deploy secrets should not become unreadable because an OAuth session refreshed.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              How peer sync works
            </h2>
            <p>
              Vault sync is explicit anti-entropy between two owner-authenticated agents:
            </p>
            <ol className="mt-3 list-decimal space-y-2 pl-6 text-surface-400">
              <li>
                The caller builds a digest of local entries: project, name, updated time,
                and whether the entry is deleted. No values are in the digest.
              </li>
              <li>
                The caller POSTs that digest to the peer&apos;s <code>/vault/sync</code>{" "}
                endpoint. The peer returns full entries only for revisions that are newer
                than the caller&apos;s digest or absent from it.
              </li>
              <li>
                The caller applies those entries locally with last-writer-wins by{" "}
                <code>updated_at</code>.
              </li>
              <li>
                The caller GETs the peer&apos;s <code>/vault/digest</code>, computes which
                local entries the peer is missing or stale on, then POSTs them to{" "}
                <code>/vault/push</code>.
              </li>
              <li>
                The peer accepts entries whose <code>updated_at</code> is newer and rejects
                entries it already has at the same or newer revision.
              </li>
            </ol>
            <p className="mt-4">
              Deletes are tombstones: <code>Deleted=true</code> with a fresh updated time
              and no value. They propagate like any other revision, then get garbage
              collected after 30 days. If two machines edit the same <code>(project, name)</code>{" "}
              around the same time, the newer timestamp wins and the sync report surfaces
              <code>superseded-local</code> or <code>rejected</code> counts so you know a
              conflict happened.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              The transport boundary
            </h2>
            <p>
              A vault sync call uses the same remote-agent resolver as other Yaver
              peer-to-peer operations. It resolves the target device from your device list,
              tries direct LAN and public endpoints first, and falls back to the configured
              relay route when direct access is not available. The relay is pass-through; it
              is not a secret store.
            </p>
            <p className="mt-4">
              Every <code>/vault/*</code> endpoint is behind owner authentication and rate
              limiting. Guest, support, and SDK tokens are not allowed to open vault values.
              MCP tools that read or write vault entries are local-only on purpose: you can
              sync the vault with a peer, but an arbitrary remote tool call cannot ask a
              different machine to reveal a secret value on its behalf.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              What never goes to Convex
            </h2>
            <p>
              Convex stores identity, sessions, device rows, relay config, and product
              bookkeeping. It does not store vault values, deploy tokens, raw API keys,
              terminal output, task prompts, source files, or absolute paths. The agent has
              tests that scan Convex-bound payloads for forbidden fields such as{" "}
              <code>vaultValue</code>, <code>secret</code>, <code>token</code>,{" "}
              <code>stdout</code>, <code>output</code>, and home-directory paths.
            </p>
            <p className="mt-4">
              That is the core Yaver contract: our backend can help your devices find each
              other, but the sensitive development material stays on your machines and moves
              peer-to-peer only when you ask it to.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
