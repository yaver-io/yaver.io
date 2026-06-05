import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-sdk-developer-guide";
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
    tags: ["SDK", "React Native", "Developer Guide", "Coding Agents", "OAuth", "P2P"],
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

const code = (s: string) => (
  <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">{s}</code>
);

function Block({ children }: { children: string }) {
  return (
    <pre className="overflow-x-auto rounded-xl border border-surface-800 bg-surface-900 p-4 text-[12px] leading-6 text-surface-200">
      <code>{children}</code>
    </pre>
  );
}

export default function YaverSdkDeveloperGuideBlogPage() {
  return (
    <div className="px-6 py-20">
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(articleLd) }} />
      <article className="mx-auto max-w-3xl space-y-8 text-sm leading-7 text-surface-300">
        <Link href="/blog" className="inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50">
          &larr; Back to Blog
        </Link>

        <header className="space-y-4">
          <time dateTime={post.date} className="text-xs uppercase tracking-[0.2em] text-surface-500">
            {post.date}
          </time>
          <h1 className="text-3xl font-bold text-surface-50 md:text-4xl">
            Embed Yaver: using <span className="text-surface-200">yaver-sdk</span> as a library
          </h1>
          <p className="text-surface-400">
            Yaver isn&apos;t just an app — it&apos;s a library. The same connectivity that powers
            Yaver&apos;s own phone and web clients ships as {code("yaver-sdk")}: a single dependency
            that lets <em>your</em> app run Claude Code, Codex, or OpenCode on a remote machine,
            authenticate the runners, and stream their output. It works in React Native, the
            browser, and Node — and the heavy lifting (OAuth, the transport ladder, task
            streaming) stays inside the SDK and the Go agent, not in your app. This is the
            developer guide.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The mental model: broker on the server, connect on the client</h2>
          <p>
            There are exactly two roles. The <strong className="text-surface-100">server</strong> holds
            your Yaver <em>account secret</em> and mints a short-lived, least-privilege token plus a
            connection bundle for one device. The <strong className="text-surface-100">client</strong> takes
            that bundle and connects <em>directly</em> to the agent — over LAN, an HTTPS tunnel, or the
            P2P relay, whichever wins a health race. The account secret never reaches the client.
          </p>
          <Block>{`client → POST /your-backend/yaver-session   (server brokers)
       ← { deviceId, device, relay, tunnelUrl, token }
client → connectHandle(bundle, { getToken })  →  AgentSession
       → agent (direct / tunnel / relay)  →  runner (claude / codex / opencode)`}</Block>
          <p className="mt-3">
            Install it once — no peer dependencies, no native modules:
          </p>
          <Block>{`npm install yaver-sdk
# React Native: it's pure JS (fetch + AbortController + TextDecoder). No pod install.`}</Block>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Server side: mint a scoped token (YaverApp)</h2>
          <p>
            On your backend, wrap your account credentials in {code("YaverApp")} and hand the client a
            session handle. The handle is opaque — your client never learns about relays or Convex.
          </p>
          <Block>{`// your-backend/yaver-session route (Node / edge)
import { YaverApp } from "yaver-sdk";

const app = new YaverApp({
  accountToken: process.env.YAVER_ACCOUNT_TOKEN!,   // server-only secret
  convexUrl: process.env.YAVER_CONVEX_URL,           // optional override
});

export async function POST(req) {
  const { deviceId } = await req.json();
  const handle = await app.sessionHandle(deviceId);  // coords + relay bundle
  const { token } = await app.mintClientToken({       // scoped, short-lived
    label: "myapp-client",
    ttlMs: 12 * 60 * 60 * 1000,
  });
  return Response.json({ ...handle, token });
}`}</Block>
          <p className="mt-3">
            That&apos;s the whole server. Authn/authz around the route is yours; the SDK takes care of
            the device discovery and the relay handshake.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Client side: connect and stream (connectHandle)</h2>
          <p>
            Pass the bundle straight into {code("connectHandle")}. The {code("getToken")} hook is the
            important bit on mobile: when a backgrounded session&apos;s token is rejected (the agent
            bearer rotates on restart), the SDK transparently re-brokers and retries — your stream
            doesn&apos;t die.
          </p>
          <Block>{`import { connectHandle } from "yaver-sdk";

async function openSession() {
  const bundle = await fetch("/your-backend/yaver-session", {
    method: "POST",
    body: JSON.stringify({ deviceId: MY_DEVICE_ID }),
  }).then((r) => r.json());

  return connectHandle(bundle, {
    // re-mint a scoped token on 401/403 — self-healing long sessions
    getToken: async () =>
      (await fetch("/your-backend/yaver-session", { method: "POST",
        body: JSON.stringify({ deviceId: MY_DEVICE_ID }) }).then((r) => r.json())).token,
  });
}

// one chat turn, streamed (SSE with a polling fallback, both built in)
async function* runChat(prompt: string, runner = "codex") {
  const session = await openSession();
  const task = await session.createTask(prompt, { runner, source: "myapp" });
  yield* session.streamOutput(task.id);   // yields incremental text
}`}</Block>
          <p className="mt-3">
            {code("AgentSession")} also gives you {code("getTask")}, {code("stopTask")}, and
            {" "}{code("status()")} (reachable / account-linked / which runners are ready) so you can
            gate your UI without knowing any internals.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Authenticating the runners (the two OAuth levels)</h2>
          <p>
            A runner only does work when it&apos;s logged in to <em>its</em> provider. The agent owns
            the real OAuth dance; the SDK&apos;s {code("YaverClient")} just drives it. There are two
            levels: the <strong className="text-surface-100">account link</strong> (is the box tied to
            your Yaver account) and the <strong className="text-surface-100">runner OAuth</strong> (is
            Claude Code / Codex / OpenCode signed in).
          </p>
          <Block>{`import { YaverClient } from "yaver-sdk";
const c = new YaverClient(agentBaseURL, token);

// what's installed + authed + which models
const cap = await c.getCapability();
// cap.runners[], cap.needs.runnerAuth = ["claude", ...]

// start a runner OAuth — returns { openUrl, code? }
const sess = await c.runnerAuthStart("claude");   // claude = paste-code
//          or c.runnerAuthStart("codex")          // codex  = device-auth

// open sess.openUrl in an in-app browser, then either:
await c.runnerAuthSubmitCode(sess.id, pastedCode); // claude paste flow
// (codex device-auth needs no paste — the agent polls; just call status)
const done = await c.runnerAuthStatus(sess.id);    // poll until "completed"`}</Block>
          <p className="mt-3">
            For headless / BYOK setups (an Anthropic or OpenAI key, an OpenRouter key, or local
            Ollama) use {code("runnerAuthSetup")} instead — and pass {code("setupMCP: true")} to wire
            your MCP servers into the runner in the same call:
          </p>
          <Block>{`await c.runnerAuthSetup("opencode", {
  setupMCP: true,
  installIfMissing: true,
  // anthropicApiKey / openaiApiKey / glmApiKey / ... as needed
});`}</Block>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">In-app browser auth (React Native)</h2>
          <p>
            The runner OAuth flows are <em>paste-code</em> (Claude) or <em>device-code</em> (Codex), so
            you don&apos;t need a redirect scheme — you just need to render the page. Open
            {" "}{code("openUrl")} in an in-app browser so the user never leaves your app; the agent
            polls for completion in the background.
          </p>
          <Block>{`import * as WebBrowser from "expo-web-browser";

async function authRunner(runner) {
  const sess = await c.runnerAuthStart(runner);
  if (sess.openUrl) {
    await WebBrowser.openBrowserAsync(sess.openUrl, { enableBarCollapsing: true });
  }
  // claude: show a "paste code" field -> c.runnerAuthSubmitCode(sess.id, code)
  // codex:  show sess.code on screen  -> user types it on the device page
  // then poll c.runnerAuthStatus(sess.id) until "completed"
}`}</Block>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Why client-direct beats proxying</h2>
          <ul className="space-y-3 list-disc pl-5">
            <li>
              <strong className="text-surface-100">No server in the hot path.</strong> Your backend is
              touched once (mint + resolve). Streaming runs client ↔ relay ↔ agent, so serverless
              time limits never truncate a long agent run.
            </li>
            <li>
              <strong className="text-surface-100">The agent stays private.</strong> It dials <em>out</em> to
              the relay; nothing is publicly reachable. The relay returns permissive CORS, so even a
              browser tab can connect directly.
            </li>
            <li>
              <strong className="text-surface-100">Self-healing tokens.</strong> {code("getToken")} re-mints
              on rejection, so a phone that was backgrounded for an hour resumes mid-stream instead of
              throwing a 401.
            </li>
            <li>
              <strong className="text-surface-100">One protocol, one place.</strong> Account link, runner
              OAuth, transport ladder, and task streaming all live in the SDK and the Go agent. When the
              agent updates, the SDK&apos;s tolerant readers absorb it — your app code doesn&apos;t change.
            </li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The full export surface</h2>
          <p>
            Everything you need is named off the package root:
          </p>
          <ul className="space-y-2 list-disc pl-5">
            <li>{code("YaverApp")} / {code("YaverBroker")} — server: session handles + scoped tokens.</li>
            <li>{code("connect")} / {code("connectHandle")} / {code("pickTransport")} / {code("AgentSession")} — client transport + tasks.</li>
            <li>{code("YaverClient")} — direct agent HTTP client: runners, OAuth, exec, tasks, {code("getCapability()")}.</li>
            <li>{code("YaverConvexClient")} — device discovery + settings.</li>
            <li>{code("YaverPolicyClient")} / {code("selectRunner")} — the runtime resolver (the &ldquo;OpenRouter of coding agents&rdquo; spine).</li>
            <li>{code("CompanionClient")} — crons + workers for serverless projects.</li>
            <li>{code("transcribe")} — speech-to-text helper for voice input.</li>
          </ul>
          <p className="mt-3">
            Talos — the manufacturing platform — is the first external consumer: its phone app embeds
            {" "}{code("yaver-sdk")} so a factory operator can ask a remote Codex/Claude-Code agent
            real ERP questions, with the curated Talos MCP wired into the runner. That&apos;s the whole
            point of shipping Yaver as a library: your product gets a remote coding/agent brain, and
            you never reimplement the plumbing.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Get the box first</h2>
          <p>
            The SDK needs an agent to talk to. Spin one up with{" "}
            <Link href="/blog/yaver-cloud-image" className="text-surface-100 underline decoration-dotted underline-offset-4 hover:text-surface-50">
              the Yaver cloud image
            </Link>{" "}
            (a dev box pre-signed-in to your coding agents), or adopt an existing machine with{" "}
            <Link href="/blog/yaver-cloud-launch-anywhere" className="text-surface-100 underline decoration-dotted underline-offset-4 hover:text-surface-50">
              yaver launch
            </Link>
            . Then point {code("yaver-sdk")} at its device ID and you&apos;re live.
          </p>
        </section>
      </article>
    </div>
  );
}
