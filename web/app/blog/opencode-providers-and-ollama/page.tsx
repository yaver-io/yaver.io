import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "opencode-providers-and-ollama";
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
    tags: ["OpenCode", "BYOK", "Ollama", "Coding Agents", "Anthropic", "OpenRouter", "GLM"],
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

export default function OpenCodeProvidersAndOllamaBlogPage() {
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
            OpenCode in Yaver: Bring Your Own Key, or Run Free on Ollama
          </h1>
          <p className="text-surface-400">
            Yaver&apos;s chat picker now ships three coding agents end-to-end: Claude Code,
            OpenAI Codex, and OpenCode. The first two are vendor-shaped — you sign in once,
            you&apos;re done. OpenCode is the &ldquo;everything else&rdquo; lane: any API-compatible model
            you can point a key at, plus local Ollama for free.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Why three runners, not seven</h2>
          <p>
            We used to surface six runners in the chat picker (Claude, Codex, Aider, Aider+Qwen,
            Ollama, OpenCode). It looked like choice. It was actually clutter. Most users picked
            Claude or Codex, and the other four were either ways to point at a local model or
            ways to point at a third-party API — both of which OpenCode already does, with one
            consistent shape.
          </p>
          <p className="mt-3">
            So we cut the picker down to three first-class options. The other runners are still
            installable from the CLI and callable from MCP — they just don&apos;t take up real estate
            in the consumer surfaces. Local Ollama, in particular, is a much better fit as a
            <em> provider</em> inside OpenCode than as a top-level runner.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">What OpenCode actually is</h2>
          <p>
            <a href="https://opencode.ai" className="text-surface-100 underline decoration-dotted underline-offset-4 hover:text-surface-50" target="_blank" rel="noreferrer">OpenCode</a> is
            an open-source coding agent that works against any OpenAI-compatible chat-completions
            endpoint. That covers Anthropic, OpenAI, OpenRouter, Z.ai (GLM), Together, Groq,
            Fireworks, your own vLLM, and Ollama running on localhost. One CLI, one config file
            (<code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">opencode.json</code>),
            many backends.
          </p>
          <p className="mt-3">
            In Yaver, when you pick &ldquo;OpenCode&rdquo; in the chat picker we render an inline
            BYOK form: provider chips, model chips, and an API-key input. Picking a model and
            saving writes the key + base URL into the agent&apos;s <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">opencode.json</code>{" "}
            via a privileged owner-only endpoint. The key never leaves your dev machine.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The four BYOK lanes</h2>
          <ul className="space-y-3 list-disc pl-5">
            <li>
              <strong className="text-surface-100">Anthropic.</strong> Sonnet 4.6 / Opus 4.7 / Haiku 4.5
              against your own <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">ANTHROPIC_API_KEY</code>.
              Highest quality. Same models Claude Code uses, but billed against your Anthropic
              console instead of a Max plan — useful if you&apos;ve already burned through this week&apos;s
              Max budget on Opus and want to keep working on cheap Sonnet calls without switching
              accounts.
            </li>
            <li>
              <strong className="text-surface-100">OpenAI.</strong> GPT-5, GPT-5 Codex, GPT-5 Mini.
              Same models Codex uses, but the BYOK lane lets you swap to a cheaper variant per
              task (Codex always picks its own).
            </li>
            <li>
              <strong className="text-surface-100">OpenRouter.</strong> One key, hundreds of models —
              Claude, GPT, Llama 3.3 70B, DeepSeek V3, Qwen Coder 32B, and the long tail. This is
              the &ldquo;I just want to try things&rdquo; lane. Pay-as-you-go, you don&apos;t care which
              vendor invoices you. Default base URL is{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">https://openrouter.ai/api/v1</code>.
            </li>
            <li>
              <strong className="text-surface-100">GLM (Z.ai).</strong> Zhipu&apos;s coding plan ships
              GLM-4.6 and GLM-4.5 Air with a 128k context window at prices that are roughly an
              order of magnitude under Sonnet. Useful for long-context refactors or when you want
              to keep a planner-implementer loop running overnight without watching the meter.
            </li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The free lane: Ollama on localhost</h2>
          <p>
            Pick the <strong className="text-surface-100">Ollama (local, free)</strong> chip and
            there&apos;s no key field. Yaver writes a provider entry pointed at{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">http://127.0.0.1:11434</code>{" "}
            and OpenCode talks to whatever model you have pulled. The catalogue we surface by
            default is the practical coding shortlist:
          </p>
          <ul className="mt-3 space-y-2 list-disc pl-5">
            <li><code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">qwen2.5-coder:14b</code> — fits 24 GB RAM, the daily-driver pick.</li>
            <li><code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">qwen2.5-coder:7b</code> — fits 16 GB RAM, viable on a MacBook Air.</li>
            <li><code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">qwen2.5-coder:32b</code> — needs 48 GB+, noticeably stronger.</li>
            <li><code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">deepseek-coder-v2:16b</code> — strong on systems languages.</li>
            <li><code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">llama3.3:70b</code> — needs 64 GB+, generalist.</li>
          </ul>
          <p className="mt-3">
            Quality is a real step down from frontier models — don&apos;t expect Qwen 14B to plan a
            refactor across forty files the way Sonnet 4.6 will. But for &ldquo;rename this
            symbol everywhere,&rdquo; &ldquo;write the docstring,&rdquo; or &ldquo;sketch a unit
            test for this function,&rdquo; local Ollama is fast, free, and offline. It&apos;s also
            the right default when the dev machine is a Hetzner box you&apos;re paying for by the
            hour and you don&apos;t want to compound that with API spend.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">From the CLI</h2>
          <p>
            The web/mobile picker is a thin shell over <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">yaver code set byok</code>.
            Same operations, terminal-flavored:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg border border-surface-800 bg-surface-950 p-4 text-[12px] leading-6 text-surface-200">
{`# Anthropic
yaver code set byok anthropic --api-key sk-ant-... --model claude-sonnet-4-6

# OpenRouter — one key, many models
yaver code set byok openrouter \\
  --api-key sk-or-... \\
  --model anthropic/claude-sonnet-4.6

# GLM via Z.ai
yaver code set byok glm \\
  --api-key zai-... \\
  --base-url https://api.z.ai/api/coding/paas/v4 \\
  --model glm-4.6

# Local Ollama — no key
yaver code set byok ollama \\
  --base-url http://127.0.0.1:11434 \\
  --model qwen2.5-coder:14b
`}
          </pre>
          <p className="mt-3">
            The CLI writes the same{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">opencode.json</code>{" "}
            the picker writes. The agent only ever holds one config; the picker is one of three
            ways to edit it (the others being CLI and direct file edit).
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">When to reach for which</h2>
          <p>
            The shape that&apos;s emerged after a few months of using all three runners daily:
          </p>
          <ul className="mt-3 space-y-2 list-disc pl-5">
            <li>
              <strong className="text-surface-100">Claude Code</strong> for high-stakes commits —
              architecture changes, hairy refactors, anything where being right matters more than
              being cheap.
            </li>
            <li>
              <strong className="text-surface-100">OpenAI Codex</strong> for daily volume —
              ~4× fewer tokens per task than Claude on equivalent work. Best when your Max plan&apos;s
              weekly budget is the constraint.
            </li>
            <li>
              <strong className="text-surface-100">OpenCode + Ollama</strong> for chores at zero
              marginal cost — boilerplate, repetitive edits, things you&apos;d feel guilty paying a
              frontier model to do.
            </li>
            <li>
              <strong className="text-surface-100">OpenCode + OpenRouter</strong> for trying a new
              model before committing to it — Llama 3.3, Qwen Coder, the next thing.
            </li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Privacy, briefly</h2>
          <p>
            BYOK keys are written to the agent&apos;s{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">~/.yaver/</code>{" "}
            on your dev machine. They never touch Yaver&apos;s Convex backend, never go through the
            relay, and are not visible to guests on the same machine — the OpenCode config endpoint
            is owner-only, and guest scopes don&apos;t include it. If you want to rotate, repaste in
            the picker (or run{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">yaver code set byok &lt;provider&gt; --api-key …</code>{" "}
            again) — the new key overwrites the old one in place.
          </p>
        </section>

        <section>
          <p>
            Three runners. Five providers under OpenCode. One picker. Ship from your phone, pay
            for what you actually use, run anything else for free on the local box.
          </p>
        </section>
      </article>
    </div>
  );
}
