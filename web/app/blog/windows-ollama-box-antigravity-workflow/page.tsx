import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "windows-ollama-box-antigravity-workflow";
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
    tags: ["Windows", "Ollama", "Tailscale", "Antigravity", "Continue", "OpenCode"],
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

export default function WindowsOllamaBoxAntigravityWorkflowPage() {
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
          <time dateTime={post.date} className="text-xs uppercase tracking-[0.2em] text-surface-500">
            {post.date}
          </time>
          <h1 className="text-3xl font-bold text-surface-50 md:text-4xl">
            A Clean Antigravity Workflow with a Windows Ollama Box
          </h1>
          <p className="text-surface-400">
            The right way to use a stronger Windows machine for local coding models is to make it
            a boring, stable LLM server and keep the MacBook as the development surface. The
            editor should stay light. The box should stay awake. Tailscale should remove network
            drama. And the editor should use Continue for Ollama, not Antigravity&apos;s native model
            picker.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The mistake people make first</h2>
          <p>
            The native Antigravity model selector looks like the obvious place to wire in a remote
            Ollama box. It is not. That dropdown is for Antigravity&apos;s own cloud-backed models.
          </p>
          <p className="mt-4">
            The working setup is Antigravity as the editor shell and Continue inside it as the
            model client. That is where the Windows-hosted Qwen models show up.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The split that actually works</h2>
          <pre className="overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`MacBook
  -> Antigravity for editing
  -> Continue inside the editor
  -> OpenCode in Terminal when you want an agent loop
  -> Tailscale to reach the Windows box

Windows box
  -> OpenSSH for management
  -> Ollama on port 11434
  -> qwen2.5-coder model ladder
  -> always-on power settings
  -> startup tasks for serve + model pulls`}
          </pre>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The two remote variants we actually used</h2>
          <p>
            There are two practical remote variants for the MacBook.
          </p>
          <ul className="mt-4 list-disc space-y-2 pl-6 text-surface-400">
            <li>
              Tailscale default:
              <code className="mx-1 rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                http://carrotbytepc.tailc32088.ts.net:11434
              </code>
            </li>
            <li>
              LAN fallback:
              <code className="mx-1 rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
                http://192.168.1.104:11434
              </code>
            </li>
          </ul>
          <p className="mt-4">
            In the validated setup, Tailscale was the version that actually answered from the Mac.
            The LAN path remained a fallback because raw access to
            <code className="mx-1 rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              192.168.1.104:11434
            </code>
            still timed out.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Make the Windows box operational first</h2>
          <p>
            The machine is only useful if it behaves like infrastructure. That means OpenSSH
            enabled, Tailscale signed in, sleep disabled, hibernation disabled, and Ollama exposed
            on a stable private address instead of a random LAN IP that breaks the moment someone
            leaves home Wi-Fi.
          </p>
          <p className="mt-4">
            The practical endpoint becomes a Tailscale name like{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              http://carrotbytepc.tailc32088.ts.net:11434
            </code>
            , with the OpenAI-compatible path on{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              /v1
            </code>
            .
          </p>
          <p className="mt-4">
            In the validated setup, that Tailscale endpoint returned model data from the MacBook.
            The raw LAN endpoint still timed out, so Tailscale was the right default.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The Windows settings that matter</h2>
          <ul className="list-disc space-y-2 pl-6 text-surface-400">
            <li>OpenSSH server enabled and set to automatic startup</li>
            <li>Tailscale enabled so the box is reachable off-LAN</li>
            <li>Ollama bound to <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">0.0.0.0:11434</code></li>
            <li>Firewall rule for port <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">11434</code> restricted to Tailscale address space</li>
            <li>Sleep, disk sleep, and hibernate disabled so the box stays available</li>
            <li>Startup task for <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">ollama serve</code></li>
            <li>Background task for sequential model pulls</li>
          </ul>
          <p className="mt-4">
            This is the difference between “a machine that worked once” and a box you can depend on
            when you are away from the desk.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Use a model ladder, not one giant default</h2>
          <p>
            On a 32 GB machine, the practical setup is a ladder:
          </p>
          <ul className="mt-4 list-disc space-y-2 pl-6 text-surface-400">
            <li><code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">qwen2.5-coder:1.5b</code> for smoke tests and quick replies</li>
            <li><code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">qwen2.5-coder:7b</code> for faster day-to-day coding</li>
            <li><code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">qwen2.5-coder:14b</code> as the serious default for React and larger edits</li>
          </ul>
          <p className="mt-4">
            Keep only one model loaded at a time. That is the sane use of a 32 GB box. Install more
            than one model, but do not pretend all of them should stay resident in memory.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Keep Antigravity clean</h2>
          <p>
            Antigravity should stay the editing surface, not become a junk drawer of half-working
            transport hacks. The useful pattern is:
          </p>
          <ol className="mt-4 list-decimal space-y-2 pl-6 text-surface-400">
            <li>Use Continue inside Antigravity for in-editor chat, edit, and context-aware changes.</li>
            <li>Point Continue at the remote Ollama endpoint over Tailscale.</li>
            <li>Make sure Continue&apos;s YAML includes the required top-level <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">version</code> field.</li>
            <li>Ignore Antigravity&apos;s internal localhost ports when reading logs unless the editor itself is crashing.</li>
            <li>Use OpenCode in Terminal for heavier agent loops or session-based coding.</li>
            <li>Keep a few named workflows instead of retyping model flags every time.</li>
          </ol>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The config bug that made the setup look broken</h2>
          <p>
            In the verified setup, Continue initially showed no models even though the Windows
            Ollama box was reachable over Tailscale. The actual failure was schema validation.
          </p>
          <p className="mt-4">
            The active Continue build required:
          </p>
          <pre className="mt-4 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`name: Windows Remote Ollama (Tailscale)
version: "1.0.0"
models:
  - name: Qwen 14B Windows Tailscale
    provider: ollama
    model: qwen2.5-coder:14b
    apiBase: http://carrotbytepc.tailc32088.ts.net:11434
    roles: [chat, edit, apply]`}
          </pre>
          <p className="mt-4">
            Without the top-level <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">version</code>,
            Continue rejected the file and surfaced an empty model picker. That was the real bug,
            not the remote endpoint.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The actual config files</h2>
          <p>
            The practical setup only became reliable when the config files matched the real tools
            in use instead of a generic “OpenAI-compatible endpoint” idea.
          </p>
          <p className="mt-4">
            That also meant using the correct file format for each tool. In this stack, Continue
            used YAML and sometimes TypeScript, while OpenCode used JSON. Saving the Continue
            config as TOML would not have worked.
          </p>
          <p className="mt-4">
            For Continue inside Antigravity or Cursor on the Mac, the active file was YAML:
          </p>
          <pre className="mt-4 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`~/.continue/config.yaml

name: Windows Remote Ollama (Tailscale)
version: "1.0.0"
models:
  - name: Qwen 14B Windows Tailscale
    provider: ollama
    model: qwen2.5-coder:14b
    apiBase: http://carrotbytepc.tailc32088.ts.net:11434
    roles:
      - chat
      - edit
      - apply`}
          </pre>
          <p className="mt-4">
            On that Mac, Continue also had a repo-style TypeScript config path present, so the same
            models were injected there too:
          </p>
          <pre className="mt-4 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`~/.continue/config.ts

export function modifyConfig(config: Config): Config {
  return {
    ...config,
    models: [
      {
        name: "Qwen 14B Windows Tailscale",
        provider: "ollama",
        model: "qwen2.5-coder:14b",
        apiBase: "http://carrotbytepc.tailc32088.ts.net:11434",
        roles: ["chat", "edit", "apply"],
      },
    ],
  };
}`}
          </pre>
          <p className="mt-4">
            For OpenCode on the Mac, the endpoint lived in JSON:
          </p>
          <pre className="mt-4 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`~/.config/opencode/opencode.json

{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "ollama": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Ollama",
      "options": {
        "baseURL": "http://carrotbytepc.tailc32088.ts.net:11434/v1"
      },
      "models": {
        "qwen2.5-coder:14b": {
          "name": "qwen2.5-coder:14b"
        }
      }
    }
  }
}`}
          </pre>
          <p className="mt-4">
            And on the Windows machine itself, the local version stayed pointed at
            <code className="mx-1 rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              http://127.0.0.1:11434
            </code>
            because that box was both the editor host and the Ollama host.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The editor-side workflow that feels normal</h2>
          <p>
            On the MacBook, the clean version is to have one remote endpoint file, one Continue
            config, one OpenCode config, and a few launchers:
          </p>
          <pre className="mt-4 overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`REMOTE_OLLAMA_BASE_URL=http://carrotbytepc.tailc32088.ts.net:11434

Quick task:
  opencode run --model ollama/qwen2.5-coder:1.5b "..."

Balanced task:
  opencode -m ollama/qwen2.5-coder:7b

Deep coding:
  opencode -m ollama/qwen2.5-coder:14b`}
          </pre>
          <p className="mt-4">
            That keeps the choice visible. Small task, small model. Real feature work, bigger model.
            The workflow stays explicit instead of burying everything behind one mystery button.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Prove the path with a real request</h2>
          <p>
            A setup is not finished when the config file exists. It is finished when the Mac can
            ask the Windows box for code and get a valid answer back over the private network.
          </p>
          <p className="mt-4">
            The minimal proof is a direct generate call or an{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              opencode run
            </code>{" "}
            invocation from the MacBook using the remote model. In this setup, the direct proof was
            a successful
            <code className="mx-1 rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-200">
              curl http://carrotbytepc.tailc32088.ts.net:11434/api/tags
            </code>
            from the MacBook. Once that works, the rest is just quality-of-life around the same
            endpoint.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">What this buys you</h2>
          <p>
            The MacBook stays quiet and portable. The Windows box handles the model weight. The
            endpoint is reachable on the same LAN or from somewhere else entirely through Tailscale.
            Antigravity stays useful because it is just the editor, not the infrastructure.
          </p>
          <p className="mt-4 text-surface-400">
            If you want the longer machine-side setup story, read{" "}
            <Link href="/blog/mac-to-windows-ai-box-over-ssh" className="underline hover:text-surface-200">
              Turning a Windows PC into a Remote AI Coding Box for a MacBook
            </Link>
            . For the step-by-step version, use the{" "}
            <Link href="/manuals/windows-ssh-coding-box" className="underline hover:text-surface-200">
              Windows SSH coding box manual
            </Link>
            .
          </p>
        </section>
      </article>
    </div>
  );
}
