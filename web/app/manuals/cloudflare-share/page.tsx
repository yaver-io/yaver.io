import Link from "next/link";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Cloudflare Tunnel + Box Sharing — Yaver Manual",
  description:
    "Set up Yaver behind Cloudflare Tunnel for a single always-on box, then share that box safely through guest access or host-share sessions.",
  alternates: { canonical: "https://yaver.io/manuals/cloudflare-share" },
  openGraph: {
    title: "Cloudflare Tunnel + Box Sharing — Yaver Manual",
    description:
      "Single-user Cloudflare Tunnel setup, guest sharing, host-share sessions, machine scoping, project scoping, and security caveats.",
    url: "https://yaver.io/manuals/cloudflare-share",
    siteName: "Yaver",
    type: "article",
  },
  twitter: {
    card: "summary_large_image",
    title: "Cloudflare Tunnel + Box Sharing — Yaver Manual",
    description:
      "Set up a Yaver box behind Cloudflare Tunnel, then share it safely.",
  },
};

export default function CloudflareShareManualPage() {
  return (
    <div className="px-6 py-20">
      <article className="mx-auto max-w-3xl space-y-8 text-sm leading-7 text-surface-300">
        <Link href="/manuals" className="inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50">
          &larr; Back to Manuals
        </Link>

        <header className="space-y-4">
          <h1 className="text-3xl font-bold text-surface-50 md:text-4xl">
            Cloudflare Tunnel + Box Sharing
          </h1>
          <p className="text-surface-400">
            The clean setup is: one host installs Yaver on the box, puts that box behind a
            Cloudflare Tunnel, then shares the box through Yaver. Cloudflare is the network
            path. Yaver is still the identity and authorization layer.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">1. Single-user setup</h2>
          <p>
            For one user with one phone and one primary box, the easiest path is the built-in
            Cloudflare wizard:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`yaver auth
yaver serve
yaver tunnel cloudflare wizard`}
          </pre>
          <p className="mt-4">
            Under the hood, the wizard runs <code>cloudflared tunnel login</code>, creates a
            named tunnel, writes ingress to <code>http://127.0.0.1:18080</code>, creates the
            DNS route, and saves the resulting public URL into Yaver&apos;s local config.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">2. How guest traffic works</h2>
          <p>The path for a guest request over the host&apos;s Cloudflare setup is:</p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`guest app
  -> https://host-tunnel.example.com/...
  -> Cloudflare edge
  -> cloudflared on host
  -> Yaver agent on 127.0.0.1:18080
  -> Yaver auth + guest policy checks
  -> requested endpoint`}
          </pre>
          <p className="mt-4">
            The guest does <strong>not</strong> need a Cloudflare account. They only need a
            Yaver account. Their requests still carry a Yaver bearer token, and the host agent
            validates that token against Convex before allowing guest-scoped endpoints.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">3. Sharing some boxes vs all boxes</h2>
          <p>
            If you want to share only one host, scope the invitation at creation time:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`yaver guests invite teammate@example.com --scope=full --machines=mac-mini --projects=yaver`}
          </pre>
          <p className="mt-4">
            If you share multiple devices under one account, do not assume one account-level
            tunnel URL is correct for all of them. Yaver intentionally only reuses an account
            tunnel hint automatically when the guest-visible share resolves to exactly one host
            box.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">4. Sharing some repos vs all repos</h2>
          <p>
            Classic guest mode is for bounded host usage: tasks, feedback, selected projects,
            selected machines, selected runners. It is not raw shell access.
          </p>
          <p className="mt-4">
            If you need a more session-oriented workflow around a specific repo/workspace, use
            host-share:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`yaver host-share prepare
yaver host-share create --projects yaver --session-ttl-min 480 --idle-timeout-min 30
yaver host-share join <invite-code>
yaver host-share attach-repo --session <session-id> --path ~/code/yaver
yaver host-share sync-repo --session <session-id> --to-host
yaver host-share sync-repo --session <session-id> --from-host
yaver host-share end <session-id>`}
          </pre>
          <p className="mt-4">
            That keeps the collaboration tied to a brokered session instead of turning a guest
            grant into a general-purpose owner bypass.
          </p>
          <p className="mt-4">
            In that flow, the guest keeps the canonical repo on their own device. Yaver mirrors
            that repo into the host&apos;s borrowed workspace, so host-installed Codex and other
            tools can work there without giving the guest general ownership of the host box.
          </p>
          <p className="mt-4">
            If <code>attach-repo</code> cannot find the repo, run <code>yaver repo refresh</code> on
            the guest machine first so the local root is discoverable.
          </p>
          <p className="mt-4">
            Hosts can also bound or stop access explicitly: use session TTL and idle timeout when
            creating the invite, then use <code>yaver host-share end &lt;session-id&gt;</code> to cut
            off a live session immediately.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">5. Security notes</h2>
          <ul className="list-disc space-y-2 pl-6 text-surface-400">
            <li>Do not hand Cloudflare Access service-token secrets to guests. Keep them host-local.</li>
            <li>Use <code>feedback-only</code> for end users and testers. Use <code>full</code> only for trusted collaborators.</li>
            <li>Use machine scoping and project scoping together whenever possible.</li>
            <li>Prefer <code>yaver serve --containerize-guests</code> or guest <code>isolation=true</code> on shared infra.</li>
            <li>Leave raw tunnel forwarding off unless a session genuinely needs it.</li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">6. Best-fit rule</h2>
          <p>
            Use Cloudflare Tunnel when you have one stable box and want a better HTTPS path than
            the shared relay. Use Yaver guest access or host-share to decide <em>who</em> may use
            that box and <em>what</em> they may touch.
          </p>
        </section>
      </article>
    </div>
  );
}
