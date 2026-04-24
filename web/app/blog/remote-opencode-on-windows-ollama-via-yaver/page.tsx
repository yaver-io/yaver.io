import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "remote-opencode-on-windows-ollama-via-yaver";
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
      "Windows",
      "Ollama",
      "Qwen",
      "Tailscale",
      "OpenCode",
      "Yaver",
      "Remote Development",
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
};

export default function RemoteOpencodeWindowsOllamaViaYaverPage() {
  return (
    <div className="px-6 py-20">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(articleLd) }}
      />
      <article className="mx-auto max-w-3xl space-y-8 text-sm leading-7 text-surface-300">
        <Link
          href="/blog"
          className="inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to Blog
        </Link>

        <header className="space-y-4">
          <time
            dateTime={post.date}
            className="text-xs uppercase tracking-[0.2em] text-surface-500"
          >
            {post.date}
          </time>
          <h1 className="text-3xl font-bold text-surface-50 md:text-4xl">
            Driving a Windows Ollama Box Remotely with Yaver and opencode
          </h1>
          <p className="text-surface-400">
            A ground-truth walkthrough, written the same week we shipped it
            into a real product (CarrotBet). We put Qwen on a Windows tower,
            exposed it over Tailscale, wrapped it with opencode, and let
            Yaver supervise the whole thing from a MacBook — and, as of this
            week, from a phone. The setup stays boring so the app code can
            stay interesting.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">
            What you get at the end
          </h2>
          <p>
            A MacBook (or any laptop) where{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              opencode run
            </code>{" "}
            dispatches to a beefy Windows machine you never have to log into,
            with API keys and default models manageable from three places at
            once: the Yaver CLI, the Yaver mobile app, or an in-app settings
            page in your own product. Everything above the Windows box is
            just HTTP — same contract, same vault, same result.
          </p>
          <p className="mt-4">
            The stack looks like this:
          </p>
          <pre className="mt-4 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`Level 1 — your UIs                    Level 2 — Yaver Go agent               Runner
──────────────────                    ───────────────────────                ──────
• Yaver mobile app            ┐       • Lifecycle: install / upgrade        • opencode
• Yaver web / MCP console     ├──HTTP─►  / start / restart opencode          (TS on Bun)
• Your app's settings page    ┘       • Encrypted vault for API keys            │
                                      • runner-auth set opencode --*-api-key   │
                                      • Injects keys when spawning opencode    │
                                                                                 ▼
                                                                        ┌──────────────────┐
                                                                        │ Ollama on Windows │
                                                                        │ • qwen2.5-coder   │
                                                                        │ • glm / zai keys  │
                                                                        │ • reachable via   │
                                                                        │   Tailscale DNS   │
                                                                        └──────────────────┘`}
          </pre>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">
            Windows side — make the box a boring LLM server
          </h2>
          <p>
            The goal is to set it up once and forget it. Don't treat the
            Windows box like a dev machine; treat it like a toaster.
          </p>
          <ol className="mt-3 list-decimal space-y-3 pl-5">
            <li>
              <strong>Install Ollama</strong> from{" "}
              <Link
                href="https://ollama.com/download/windows"
                className="underline hover:text-surface-200"
              >
                ollama.com/download/windows
              </Link>
              . Let it autostart on login. This gives you a local server on{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                http://127.0.0.1:11434
              </code>
              .
            </li>
            <li>
              <strong>Pull a Qwen coder model</strong>. From PowerShell on
              the Windows box:
              <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`ollama pull qwen2.5-coder:14b
ollama pull qwen2.5-coder:7b
ollama pull qwen2.5-coder:1.5b`}
              </pre>
              Keep the 1.5b around — it is genuinely useful for cheap
              refactors, import fixes, and commit messages.
            </li>
            <li>
              <strong>Open the listener on the LAN.</strong> By default
              Ollama on Windows binds to loopback only. Set{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                OLLAMA_HOST=0.0.0.0:11434
              </code>{" "}
              as a system environment variable, restart the Ollama tray app,
              and confirm with a quick{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                netstat -an | findstr 11434
              </code>
              . You should see it listening on{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                0.0.0.0:11434
              </code>
              .
            </li>
            <li>
              <strong>Install Tailscale</strong> and sign in with the same
              tailnet you use on the Mac. Note the Windows host's Tailscale
              DNS name — something like{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                yourbox.tailnet-abc123.ts.net
              </code>
              . This replaces the LAN IP for any remote work so the endpoint
              stops changing when you move networks.
            </li>
            <li>
              <strong>Prevent sleep.</strong> On Windows: Settings → System →
              Power &amp; battery → Screen and sleep → set both to Never
              while plugged in. Otherwise the box naps and the Mac's first
              request of the day eats a 20-second wake-up penalty.
            </li>
          </ol>
          <p className="mt-4">
            From the Mac, the proof that Windows is ready is one command:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`curl http://yourbox.tailnet-abc123.ts.net:11434/api/tags`}
          </pre>
          <p className="mt-3">
            If that returns a JSON list with your qwen models, the
            infrastructure is done. Everything else is wiring.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">
            Mac side — install opencode once, let Yaver own the keys
          </h2>
          <p>
            opencode is{" "}
            <a
              href="https://github.com/sst/opencode"
              className="underline hover:text-surface-200"
            >
              the open-source coding agent
            </a>{" "}
            we use as Yaver's default runner for everything that is not
            Claude Code or Codex. It boots a local HTTP server, exposes an
            OpenAPI contract, and accepts any compliant client. That's the
            property that makes it wrap cleanly: Yaver's mobile app, Yaver's
            Go agent, and your own product's settings page all speak the
            same API.
          </p>
          <p className="mt-4">
            Install it via Yaver — the runtime lands under{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              ~/.yaver/runtimes/node/bin/opencode
            </code>{" "}
            and Yaver pulls upgrades with the rest of its toolchain:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`yaver runner-auth setup opencode
yaver runner-auth status
# opencode  yes  yes  yes  /Users/you/.yaver/runtimes/node/bin/opencode (1.14.22)`}
          </pre>
          <p className="mt-4">
            Then point opencode at the remote Ollama endpoint. Edit{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              ~/.config/opencode/opencode.jsonc
            </code>
            :
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "ollama": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Remote Ollama (Tailscale)",
      "options": {
        "baseURL": "http://yourbox.tailnet-abc123.ts.net:11434/v1"
      },
      "models": {
        "qwen2.5-coder:14b": { "name": "qwen2.5-coder:14b" },
        "qwen2.5-coder:7b":  { "name": "qwen2.5-coder:7b"  },
        "qwen2.5-coder:1.5b":{ "name": "qwen2.5-coder:1.5b"}
      }
    }
  }
}`}
          </pre>
          <p className="mt-4">
            At this point{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              opencode models
            </code>{" "}
            lists your Windows models under{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              ollama/qwen2.5-coder:*
            </code>
            . If you also want Z.AI's hosted GLM family in the same picker,
            add the key through Yaver's vault once:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`yaver runner-auth set opencode \\
  --zai-api-key       $ZAI_KEY        \\
  --glm-api-key       $GLM_KEY        \\
  --openai-api-key    $OPENAI_KEY     \\
  --anthropic-api-key $ANTHROPIC_KEY`}
          </pre>
          <p className="mt-4">
            That writes into Yaver's encrypted vault (NaCl secretbox +
            Argon2id, at{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              ~/.yaver/vault.enc
            </code>
            ) and Yaver will inject the relevant key when it spawns opencode
            for a run. If the vault does not have a matching key, opencode
            falls back to its own{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              ~/.local/share/opencode/auth.json
            </code>{" "}
            — so{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              opencode providers login
            </code>{" "}
            still works if you prefer that route. The two stores are
            additive, not exclusive.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">
            The HTTP contract you can wrap from anywhere
          </h2>
          <p>
            opencode serves the{" "}
            <a
              href="https://github.com/sst/opencode/blob/dev/packages/sdk/openapi.json"
              className="underline hover:text-surface-200"
            >
              OpenAPI spec
            </a>{" "}
            over HTTP. For the purposes of this post, four endpoints are
            enough to build a real UI:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`GET    /provider              -> map of {providerID: {name, models, source, ...}}
PUT    /auth/{providerID}     body: {"type":"api","key":"..."}    set a key
DELETE /auth/{providerID}                                         clear a key
GET    /global/config         -> {model, ...}
PATCH  /global/config         body: {"model":"zai/glm-4.6"}       set default`}
          </pre>
          <p className="mt-4">
            That's the entire surface needed to let a user pick a provider,
            paste an API key, and select a default model. You can call it
            from a Tauri desktop app, a Next.js console, a mobile app, or
            literally{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              curl
            </code>{" "}
            — opencode doesn't care. Start a server with:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`opencode serve --port 4096 --hostname 127.0.0.1`}
          </pre>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">
            Yaver's job — the Go agent in the middle
          </h2>
          <p>
            Yaver is a two-level wrapper. Level 1 is the user-facing UI
            (mobile app, web console, your product's settings page). Level 2
            is the Yaver Go agent — the thing running as a daemon on your
            dev box that supervises the coding runner and holds the vault.
            Everything level 1 does round-trips through level 2, but the
            "does the key reach opencode" question is owned entirely by
            level 2 and the vault.
          </p>
          <p className="mt-4">
            The Go agent's opencode wrapping is explicit, not magic. From
            the CLI today:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`# Pick a runner for this task:
yaver agent run --runner opencode --model ollama/qwen2.5-coder:14b \\
  --work-dir ~/projects/myapp --prompt "fix the flaky e2e test in checkout"

# Or make opencode the default runner:
# edit ~/.yaver/config.json: "runner": "opencode"
yaver restart
yaver config show | grep runner`}
          </pre>
          <p className="mt-4">
            For the "default model, forever" part Yaver does not currently
            persist a model in its own config — the right place for that
            knob is opencode's global config, which Yaver reads through its
            runner when no explicit{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              --model
            </code>{" "}
            is passed:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`curl -XPATCH http://127.0.0.1:4096/global/config \\
  -H 'Content-Type: application/json' \\
  -d '{"model":"ollama/qwen2.5-coder:14b"}'`}
          </pre>
          <p className="mt-4">
            After that, every{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              yaver agent run --runner opencode
            </code>{" "}
            without an explicit model runs against Qwen on your Windows box.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">
            A real case study — wiring this into CarrotBet
          </h2>
          <p>
            We shipped this whole chain into a product (
            <a
              href="https://carrotbytes.xyz"
              className="underline hover:text-surface-200"
            >
              CarrotBet
            </a>
            ) this week as a sanity check. Carrotbytes.xyz's feedback
            widget lets a user file a bug; the bug becomes a task; the task
            runs on opencode; opencode uses Qwen on the dev box's Windows
            tower. For the settings side, CarrotBet didn't reinvent
            anything — it just talks to the same opencode HTTP endpoints we
            listed above, from both a web page and an Expo screen.
          </p>
          <p className="mt-4">
            The web page is a thin fetch-based client: probe{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              GET /provider
            </code>
            , render providers, accept an API key per row (→{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              PUT /auth/{"{providerID}"}
            </code>
            ), pick a default model (→{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              PATCH /global/config
            </code>
            ). When the server isn't running, it falls back to a CLI hint
            (<code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              yaver runner-auth set opencode --*-api-key
            </code>
            ) so nothing gets stuck. The mobile app has the identical
            screen, same endpoints, via{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              fetch
            </code>{" "}
            on React Native. No RN-specific SDK required.
          </p>
          <p className="mt-4">
            We also vendored the yaver-feedback-web tarball into the
            CarrotBet repo so a fresh clone never needs a sibling{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              yaver.io
            </code>{" "}
            checkout to run{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              npm install
            </code>
            . If you're shipping anything that depends on a feedback SDK
            that is not yet on npm, vendor it — the 60 KB tarball pays for
            itself the first time a collaborator clones on a new machine.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">
            The smallest working end-to-end test
          </h2>
          <p>
            Before you declare victory, run one request through the whole
            chain from the Mac:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`# 1. The Windows box is reachable
curl http://yourbox.tailnet-abc123.ts.net:11434/api/tags

# 2. opencode knows the model
opencode models | grep qwen

# 3. Yaver will invoke opencode with that model
yaver agent run --runner opencode \\
  --model ollama/qwen2.5-coder:7b \\
  --work-dir /tmp \\
  --prompt "Print a single-line greeting."`}
          </pre>
          <p className="mt-4">
            If step 3 comes back with a greeting, every layer is doing its
            job. Stop configuring; start shipping.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">
            Gotchas we hit
          </h2>
          <ul className="list-disc space-y-3 pl-5">
            <li>
              <strong>Ollama bound to loopback only.</strong> Even with{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                OLLAMA_HOST
              </code>{" "}
              set, you have to restart the Ollama tray app from the
              notification area — not reopen the app — or the env var is
              ignored. Check{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                netstat
              </code>
              .
            </li>
            <li>
              <strong>SSH not needed once Tailscale works.</strong> Tempting
              to keep SSH tunnels to localhost open. Don't. The Tailscale
              DNS name is stable across networks. SSH tunnels are a pile of
              terminal sessions waiting to die.
            </li>
            <li>
              <strong>Non-interactive shells missing PATH.</strong> When
              Yaver (or anything else) invokes commands over SSH without a
              login shell, Homebrew's{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                /opt/homebrew/bin
              </code>{" "}
              isn't on PATH and{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                npm
              </code>
              /
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                node
              </code>{" "}
              disappear. Put{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                eval "$(/opt/homebrew/bin/brew shellenv)"
              </code>{" "}
              in{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                ~/.zshenv
              </code>
              , not{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                ~/.zshrc
              </code>
              .
            </li>
            <li>
              <strong>Yaver vault re-locking.</strong> Rotating your Yaver
              auth token rotates the vault passphrase. If{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                yaver vault list
              </code>{" "}
              errors with "wrong passphrase or corrupted vault", set{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                YAVER_VAULT_PASSPHRASE
              </code>{" "}
              to the previous token and re-import.
            </li>
            <li>
              <strong>CORS for a browser-hosted settings page.</strong>{" "}
              opencode's{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                server.cors
              </code>{" "}
              must include your app's origin. For a prod site hitting an
              opencode server through a tunnel, add your domain to the
              CORS list or you'll chase 404s in devtools that are really
              preflight failures.
            </li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">
            Related reading
          </h2>
          <p>
            If you want the Windows-centric side of the story in more
            detail, start with{" "}
            <Link
              href="/blog/mac-to-windows-ai-box-over-ssh"
              className="underline hover:text-surface-200"
            >
              Turning a Windows PC into a Remote AI Coding Box for a
              MacBook
            </Link>
            . For the always-on LLM-server framing and the Continue-inside-
            Antigravity variation, see{" "}
            <Link
              href="/blog/windows-ollama-box-antigravity-workflow"
              className="underline hover:text-surface-200"
            >
              A Clean Antigravity Workflow with a Windows Ollama Box
            </Link>
            . And for the click-through version of this post, the matching
            manual is{" "}
            <Link
              href="/manuals/local-llm"
              className="underline hover:text-surface-200"
            >
              Local LLM
            </Link>
            .
          </p>
        </section>
      </article>
    </div>
  );
}
