import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "stt-tts-voice-local-byok";
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
    tags: ["Yaver", "STT", "TTS", "Voice", "Whisper", "Deepgram", "Cartesia", "P2P", "Vault", "BYOK"],
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
    "local speech to text",
    "on-device Whisper",
    "text to speech agent",
    "BYOK voice",
    "Deepgram Flux",
    "Cartesia Sonic",
    "P2P API key vault",
    "voice coding agent",
  ],
};

export default function STTTTSVoiceBlogPage() {
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
            Voice in Yaver: local STT/TTS by default, bring-your-own cloud, keys
            in the vault
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            You should be able to talk to your coding agent without signing up
            for a speech API or shipping your microphone audio to a vendor. So
            Yaver&apos;s voice stack starts free and offline: on-device Whisper
            for speech-to-text, your OS voice for text-to-speech. When you want
            a faster or nicer cloud engine, you bring your own key — and that
            key lives in your encrypted <code>yaver vault</code> and travels
            P2P between your own machines, never through our servers.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Two halves: STT and TTS
            </h2>
            <p>
              <strong className="text-surface-100">STT</strong> (speech-to-text)
              turns what you say into a prompt for the agent.{" "}
              <strong className="text-surface-100">TTS</strong> (text-to-speech)
              reads the agent&apos;s reply back to you. They&apos;re
              independent — you can run STT alone (dictate prompts, read
              replies on screen), TTS alone (type prompts, hear answers in the
              car or on glasses), both, or neither (plain typing in a terminal).
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Local is the default, and it&apos;s free
            </h2>
            <p className="mb-3">
              When you install the CLI, Yaver provisions a free voice stack in
              the background — ffmpeg for mic capture, <code>whisper.cpp</code>,
              and a small ggml model under <code>~/.yaver/models/</code>. No API
              key, no cost, fully offline once installed. Opt out with{" "}
              <code>YAVER_SKIP_POSTINSTALL_VOICE=1</code>.
            </p>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`# Check / install the local voice deps yourself
yaver voice deps --install

# Live mic transcription to stdout (local Whisper)
yaver voice listen

# Also speak finals back with the free local OS voice
yaver voice listen --tts

# See what's wired and which providers are ready
yaver voice status`}
            </pre>
            <p className="mt-4">
              Per surface, the free path is: <strong className="text-surface-100">Go
              agent / CLI</strong> → <code>whisper.cpp</code> + the host{" "}
              <code>say</code>/<code>espeak</code> voice;{" "}
              <strong className="text-surface-100">mobile</strong> → bundled{" "}
              <code>whisper.rn</code> + the iOS/Android system voice;{" "}
              <strong className="text-surface-100">web</strong> → on-device
              Whisper, falling back to the browser voice for readback.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Bring your own cloud engine (optional)
            </h2>
            <p className="mb-3">
              Local Whisper is great, but sometimes you want lower latency or
              end-of-turn detection. You can switch any surface to a cloud
              engine: <strong className="text-surface-100">OpenAI</strong>,{" "}
              <strong className="text-surface-100">Deepgram Flux</strong> (STT),
              or <strong className="text-surface-100">Cartesia Sonic</strong>{" "}
              (TTS). These are bring-your-own-key — Yaver never resells speech
              capacity.
            </p>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`# One-key Deepgram (Flux STT + Aura-2 TTS):
yaver voice setup deepgram --deepgram-api-key dg_...

# Or Deepgram Flux for STT + Cartesia Sonic for TTS:
yaver voice setup deepgram-cartesia \\
  --deepgram-api-key dg_... --cartesia-api-key ck_...

# Or OpenAI for both:
yaver voice setup openai --openai-api-key sk-...`}
            </pre>
            <p className="mt-4">
              On the <strong className="text-surface-100">web dashboard</strong>,
              the same choices live in Preferences → Voice: pick the speech
              provider and TTS provider, and the UI tells you exactly where the
              key goes (the vault, never Convex). On{" "}
              <strong className="text-surface-100">mobile</strong>, the Voice
              screen does the same.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Keys live in the vault and flow P2P
            </h2>
            <p className="mb-3">
              This is the important part. A cloud speech key is a secret, and
              Yaver treats it like every other secret: it goes into your
              encrypted <code>yaver vault</code>, scoped to a project, and it
              syncs <em>peer-to-peer</em> between your own devices over the same
              transport Yaver uses for tasks and terminals. Convex only ever
              holds the <em>preference</em> (which provider you picked, whether
              TTS is on) — never the key, never your audio, never a transcript.
            </p>
            <pre className="overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`# Store the key in the vault, scoped to a "voice" project
yaver vault add DEEPGRAM_API_KEY --project voice --value dg_...
yaver vault add CARTESIA_API_KEY --project voice --value ck_...

# Bring it to another of your machines (P2P, not via our servers)
yaver vault sync`}
            </pre>
            <p className="mt-4">
              So a key you add on your laptop is available to the Mac mini or
              cloud box that actually runs the transcription, without you ever
              pasting it twice or uploading it to a hosted store. Same vault,
              same{" "}
              <Link href="/blog/yaver-p2p-vault" className="text-surface-100 underline decoration-surface-600 underline-offset-2 hover:decoration-surface-300">
                P2P sync
              </Link>
              , now for voice.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              The agent now knows if you&apos;re on voice
            </h2>
            <p className="mb-3">
              A coding answer you read on a desktop and one a phone reads aloud
              should not be shaped the same. So the client tells the agent its
              voice state when it sends a task, and the agent adapts its reply:
            </p>
            <ul className="ml-5 list-disc space-y-1.5">
              <li>
                <strong className="text-surface-100">TTS on</strong> → the agent
                keeps the spoken headline short and budgeted (≈280 chars by
                default), with the detail following on screen.
              </li>
              <li>
                <strong className="text-surface-100">STT on</strong> → if it
                needs input, it ends with one short, spoken-friendly question
                instead of a wall of options.
              </li>
              <li>
                <strong className="text-surface-100">CLI, neither</strong> →
                plain text with full detail and ANSI color; no voice shaping.
              </li>
            </ul>
            <p className="mt-4">
              Mobile and web advertise this automatically (the device that&apos;s
              actually rendering decides), and a CLI client can opt in with an{" "}
              <code>X-Yaver-Voice: stt,tts</code> hint. The default — a bare
              terminal — stays exactly as it was: text in, text out.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">
              Privacy, in one line
            </h2>
            <p>
              Your microphone audio and transcripts stay on your devices and the
              agent you own; cloud STT/TTS only happens with a key you supplied,
              against the vendor you chose. Convex stores a provider name and a
              toggle, nothing more — the same contract Yaver enforces for every
              other secret.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
