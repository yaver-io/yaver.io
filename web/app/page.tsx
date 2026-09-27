"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState, useEffect } from "react";
import { useAuth } from "@/lib/use-auth";
import { GUI_DOWNLOADS } from "@/lib/versions";

// Canonical definitional one-liner — picked up by AI search
// (ChatGPT, Claude) and SEO as the answer to "what is Yaver?". Framed
// around the product, not the MCP protocol: Yaver is a real-device app
// development loop. Run your coding agent on your own machine or VPS and
// drive it from a native desktop app, your
// phone, tablet, watch, or the web. No comparative claims against named
// competitors, per LEGAL_SAFETY.md §2 (trademark) and §3.
const LANDING_TAGLINE =
  "Yaver is an open-source, self-hostable real-device app development loop. It runs Claude Code, Codex, and OpenCode on your own Mac, PC, Linux host, home server, or VPS and hot-reloads the real app on your iPhone or Android seconds after the agent edits it. Drive it from desktop, phone, tablet, watch, TV, car, spatial clients, the web, or your own app using Yaver's SDK.";

const SUPPORTED_SURFACES = ["iOS", "Android", "Web", "watchOS", "tvOS", "Wear OS", "CarPlay", "Android Auto"];

const LANDING_FAQ: ReadonlyArray<{ q: string; a: string }> = [
  {
    q: "What is Yaver?",
    a: "Yaver is an open-source real-device app development loop. Claude Code, Codex, OpenCode, or another terminal agent runs on your own laptop, workstation, home server, or VPS. Yaver adds secure remote control, browser lane, Hermes/native previews, feedback capture, and clients from phone and watch to TV and spatial surfaces.",
  },
  {
    q: "Where does the agent run?",
    a: "On a computer you control: a laptop, workstation, Mac mini, Linux box, home server, or VPS. Install Yaver there and use your own Claude Code, Codex, OpenCode, or terminal-agent account.",
  },
  {
    q: "Is it a WebView?",
    a: "No. For React Native and Expo projects, Yaver compiles your JavaScript to Hermes bytecode and loads it through a real native bridge, so TurboModules, Fabric, and JSI behave like a production build.",
  },
  {
    q: "Does my code leave my machine?",
    a: "Your source files, runner processes, and durable output stay on the machine doing the work. The hosted control plane stores account and device metadata. Managed relay traffic is encrypted in transit and forwarded without persistent body storage, but the HTTP relay process is inside the transport trust boundary. Use a direct path or self-host the relay when you need to control that boundary.",
  },
  {
    q: "How self-hosted is it?",
    a: "The CLI, agent, relay, and backend are self-hostable, and the client SDK can put Yaver inside your own app. Run your own relay or private network, or use the optional $9/month Relay Pro connectivity service. No first-party Yaver app is required.",
  },
  {
    q: "Which coding agents work?",
    a: "Claude Code, OpenAI Codex, and OpenCode are first-class. Anything that runs in a terminal can be driven through the generic runner, and OpenCode can route to Aider, Goose, local Ollama models, OpenRouter, and other providers.",
  },
  {
    q: "What license is it under?",
    a: "Core is FSL-1.1-Apache-2.0: free for non-competing use and auto-converts to Apache-2.0 two years after each release. Client SDKs are Apache-2.0 from day one.",
  },
];

const LANDING_HOWTO_STEPS: ReadonlyArray<{ name: string; text: string; url?: string }> = [
  {
    name: "Install Yaver",
    text: "Install the desktop app for macOS, Windows, or Linux, or run npm install -g yaver-cli, then yaver auth. That machine becomes your runtime.",
    url: "https://yaver.io/download",
  },
  {
    name: "Install a Yaver surface app",
    text: "Download Yaver for iOS or Android and sign in with the same account.",
    url: "https://yaver.io/download",
  },
  {
    name: "Build and hot-reload",
    text: "Ask the agent to build something. Yaver runs it on your machine or VPS, compiles the bundle, and hot-reloads it on your paired phone.",
    url: "https://yaver.io/manuals/cli-setup",
  },
];

function FAQItem({ question, answer }: { question: string; answer: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="border-b border-surface-800/60">
      <button
        onClick={() => setOpen(!open)}
        className="flex w-full items-center justify-between py-5 text-left"
      >
        <span className="text-sm font-medium text-surface-100">{question}</span>
        <span className="ml-4 shrink-0 text-surface-500">{open ? "\u2212" : "+"}</span>
      </button>
      {open && (
        <p className="pb-5 text-sm leading-relaxed text-surface-400">{answer}</p>
      )}
    </div>
  );
}

export default function HomePage() {
  const { isAuthenticated, isLoading } = useAuth();
  const router = useRouter();

  // Signed-in users go straight to the dashboard. We redirect from
  // an effect instead of early-returning a spinner, because
  // early-returning turns this client page into an SEO black hole:
  // server-rendered HTML is just a spinner, so Google / ChatGPT /
  // Claude never see the hero, FAQPage JSON-LD, HowTo
  // JSON-LD, or any of the AI-search copy. Rendering the landing on
  // both server and client means signed-in users see a ~200 ms flash
  // of the landing during auth resolution before the effect fires —
  // an acceptable trade for real SEO.
  useEffect(() => {
    if (!isLoading && isAuthenticated) {
      router.replace("/dashboard");
    }
  }, [isLoading, isAuthenticated, router]);

  const faqLd = {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    mainEntity: LANDING_FAQ.map(({ q, a }) => ({
      "@type": "Question",
      name: q,
      acceptedAnswer: { "@type": "Answer", text: a },
    })),
  };

  const howToLd = {
    "@context": "https://schema.org",
    "@type": "HowTo",
    name: "Run an AI coding agent from your phone with Yaver",
    description:
      "Install the Yaver CLI on a machine, pair the phone app, and drive your coding agent remotely with hot-reload previews.",
    totalTime: "PT5M",
    supply: [{ "@type": "HowToSupply", name: "A Mac, Linux, or Windows machine" }],
    tool: [
      { "@type": "HowToTool", name: "The Yaver desktop app (macOS / Windows / Linux) or npm CLI" },
      { "@type": "HowToTool", name: "The Yaver mobile app" },
    ],
    step: LANDING_HOWTO_STEPS.map((s, i) => ({
      "@type": "HowToStep",
      position: i + 1,
      name: s.name,
      text: s.text,
      url: s.url,
    })),
  };

  const organizationLd = {
    "@context": "https://schema.org",
    "@type": "Organization",
    name: "Yaver",
    legalName: "SIMKAB ELEKTRIK",
    url: "https://yaver.io",
    logo: "https://yaver.io/icon-512.png",
    sameAs: [
      "https://github.com/yaver-io/yaver.io",
      "https://www.npmjs.com/package/yaver-cli",
    ],
    email: "kivanc.cakmak@simkab.com",
    founder: { "@type": "Person", name: "Kıvanç Çakmak" },
    description: LANDING_TAGLINE,
  };

  const pricingLd = {
    "@context": "https://schema.org",
    "@type": "OfferCatalog",
    name: "Yaver plans",
    itemListElement: [
      {
        "@type": "Offer",
        name: "Relay Pro",
        price: "9",
        priceCurrency: "USD",
        description: "Managed account-private relay connectivity to a machine or VPS you control.",
      },
    ],
  };

  return (
    <>
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(faqLd) }}
      />
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(howToLd) }}
      />
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(organizationLd) }}
      />
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(pricingLd) }}
      />
      {/* ── Section 1: Hero — product-first, real-device loop ── */}
      <section className="px-6 pb-10 pt-20 md:pt-28">
        <div className="mx-auto max-w-5xl text-center">
          <p className="mb-3 text-xs font-semibold uppercase tracking-[0.25em] text-surface-400">
            Vibe it, see it. Instantly on your real phone.
          </p>
          <div className="mb-5 inline-flex flex-wrap items-center justify-center gap-2">
            <span className="inline-flex items-center gap-2 rounded-full border border-emerald-500/20 bg-emerald-500/10 px-4 py-1.5 text-xs font-medium text-emerald-700 dark:text-emerald-300">
              <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
              Open source
            </span>
            <span className="inline-flex items-center gap-2 rounded-full border border-surface-700 bg-surface-900/70 px-4 py-1.5 text-xs font-medium text-surface-300">
              Self-hostable
            </span>
            <span className="inline-flex items-center gap-2 rounded-full border border-surface-700 bg-surface-900/70 px-4 py-1.5 text-xs font-medium text-surface-300">
              Runs on your machine
            </span>
          </div>

          <h1 className="mb-5 text-4xl font-bold leading-[1.02] tracking-tight text-surface-50 sm:text-5xl md:text-6xl">
            Yaver is an open-source
            <br className="hidden sm:block" />
            <span className="bg-gradient-to-r from-indigo-400 to-emerald-400 bg-clip-text text-transparent">
              real-device app loop.
            </span>
          </h1>

          {/* AI / screen-reader description — canonical one-liner for AI
              search answers to "what is Yaver?". */}
          <p className="sr-only">{LANDING_TAGLINE}</p>

          <p className="mx-auto max-w-2xl text-sm leading-relaxed text-surface-300 sm:text-base md:text-[17px]">
            Your coding agent builds on your own Mac, PC, Linux box, or VPS,
            and the app hot-reloads on your real iPhone or Android seconds
            later. Drive it from the desktop app, your phone, or the web.
            Bring your own AI account.
          </p>

          <div className="mx-auto mt-6 flex max-w-2xl flex-wrap items-center justify-center gap-2">
            {SUPPORTED_SURFACES.map((surface) => (
              <span
                key={surface}
                className="rounded-full border border-surface-800 bg-surface-900/70 px-3 py-1 text-xs font-medium text-surface-300"
              >
                {surface}
              </span>
            ))}
          </div>

          {/* Free runtime plus optional managed relay. */}
          <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Link
              href="/download"
              className="btn-primary px-6 py-3 text-sm"
            >
              Download Yaver — free
            </Link>
            <Link
              href="/pricing"
              className="rounded-lg border border-surface-700 bg-surface-900/70 px-6 py-3 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50"
            >
              Relay Pro — $9/mo &rarr;
            </Link>
          </div>

          <div className="mx-auto mt-10 w-full max-w-xl text-left">
            <p className="mb-2 text-center text-[11px] text-surface-500">
              One install turns any machine into a remote AI runtime:
            </p>
            <div className="space-y-1.5 rounded-lg border border-surface-800 bg-surface-950 px-4 py-3 font-mono text-[12px] leading-relaxed">
              <div className="text-surface-500"># on the machine or VPS that does the work:</div>
              <div className="text-surface-600">
                $ <span className="select-all break-all text-surface-200">npm install -g yaver-cli && yaver auth</span>
              </div>
              <div className="my-1.5 h-px bg-surface-800/60" />
              <div className="text-surface-500"># from your phone, or the desktop app:</div>
              <div className="text-surface-600">
                $ <span className="select-all break-all text-emerald-300">open Yaver — pick a machine, pick a project, ask for a change</span>
              </div>
            </div>
            <p className="mt-2 text-center text-[11px] text-surface-600">
              The agent edits, builds, and hot-reloads the app on your phone.{" "}
              <Link href="/docs" className="underline hover:text-surface-300">
                full docs &rarr;
              </Link>
            </p>
          </div>

        </div>
      </section>

      {/* ── Section 2: Hero video — the ONE viral artifact ──
          Mobile-only screen recording, 60s, sped from the raw 3.3-min
          capture. Shows the developer-tool loop entirely on-device:
          Projects tab → pick a project on your dev box → preview on
          your phone → shake → vibe-code → reload. */}
      <section id="demo" className="px-6 pb-16 pt-2">
        <div className="mx-auto max-w-3xl">
          <video
            className="w-full rounded-2xl bg-black shadow-2xl shadow-black/50"
            src="/yaver-vibe-reload.mp4"
            autoPlay
            muted
            loop
            playsInline
            preload="metadata"
          />
          <p className="mx-auto mt-4 max-w-2xl text-center text-sm text-surface-300">
            Open Yaver on your desktop or phone, pick a project from your
            machine or VPS, preview it on your phone, shake to
            vibe-code — fix a bug, ship a small feature, or tweak a style —
            and a fresh bundle lands in seconds. One screen, real device, no
            extra hardware.
          </p>
        </div>
      </section>

      {/* ── Section 2.5: Downloads — desktop app + mobile ── */}
      <section id="download" className="border-t border-surface-800/60 px-6 py-16">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-3 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Download Yaver
          </h2>
          <p className="mx-auto mb-10 max-w-2xl text-center text-sm text-surface-400">
            The native desktop app embeds the Yaver agent, so a fresh machine
            becomes both a development runtime and a control surface. Run on
            this machine, or explicitly target another paired box for coding,
            builds, and Browser Lane. The CLI turns headless machines into remote runtimes.
          </p>

          {/* Desktop app — the product shell */}
          <div className="mb-8 rounded-2xl border border-surface-800 bg-surface-900/50 p-6">
            <div className="mb-5 flex items-center justify-between gap-3">
              <div>
                <p className="text-base font-semibold text-surface-50">Yaver Desktop</p>
                <p className="mt-0.5 text-xs text-surface-500">
                  macOS · Windows · Linux — signed, self-contained, embeds the agent
                </p>
              </div>
              <Link
                href="/download"
                className="rounded-lg border border-surface-700 bg-surface-900 px-4 py-2 text-xs font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50"
              >
                All releases &rarr;
              </Link>
            </div>
            <div className="grid gap-3 sm:grid-cols-3">
              {[
                { name: "macOS (Apple Silicon)", href: GUI_DOWNLOADS.macArm64, note: "Signed + notarized DMG · arm64" },
                { name: "macOS (Intel)", href: GUI_DOWNLOADS.macX64, note: "Signed + notarized DMG · x64" },
                { name: "Windows", href: GUI_DOWNLOADS.winX64, note: "Signed installer · x64" },
                { name: "Linux", href: GUI_DOWNLOADS.linuxX64, note: "AppImage · x64" },
                { name: "Linux ARM", href: GUI_DOWNLOADS.linuxArm64, note: "AppImage · arm64" },
              ].map((d) => (
                <a
                  key={d.name}
                  href={d.href}
                  className="group flex items-center justify-between gap-3 rounded-xl border border-surface-800 bg-surface-950 px-4 py-3.5 transition-colors hover:border-emerald-500/40"
                >
                  <div>
                    <p className="text-sm font-semibold text-surface-100">{d.name}</p>
                    <p className="mt-0.5 text-[11px] text-surface-500">{d.note}</p>
                  </div>
                  <span className="text-lg text-emerald-500 transition-transform group-hover:translate-y-0.5">{"\u2193"}</span>
                </a>
              ))}
            </div>
            <p className="mt-4 text-[11px] text-surface-600">
              Linux also ships <code className="text-surface-400">.deb</code> (apt-get), <code className="text-surface-400">.rpm</code>, and <code className="text-surface-400">.tar.gz</code>. The desktop app works standalone on a fresh install — no npm or Node needed; runners provision on demand.
            </p>
          </div>

          {/* Mobile + CLI */}
          <div className="grid gap-4 md:grid-cols-3">
            <a href="https://apps.apple.com/us/app/yaver-io/id6760467669" target="_blank" rel="noopener noreferrer"
              className="rounded-xl border border-surface-800 bg-surface-900/50 p-4 transition-colors hover:border-surface-600">
              <p className="text-sm font-semibold text-surface-100">iPhone / iPad</p>
              <p className="mt-1 text-[11px] text-surface-500">App Store</p>
            </a>
            <a href="https://play.google.com/store/apps/details?id=io.yaver.mobile" target="_blank" rel="noopener noreferrer"
              className="rounded-xl border border-surface-800 bg-surface-900/50 p-4 transition-colors hover:border-surface-600">
              <p className="text-sm font-semibold text-surface-100">Android</p>
              <p className="mt-1 text-[11px] text-surface-500">Google Play</p>
            </a>
            <a href="/download"
              className="rounded-xl border border-surface-800 bg-surface-900/50 p-4 transition-colors hover:border-surface-600">
              <p className="text-sm font-semibold text-surface-100">CLI (npm)</p>
              <p className="mt-1 font-mono text-[11px] text-surface-500">npm install -g yaver-cli</p>
            </a>
          </div>
        </div>
      </section>

      {/* ── Section 3: How it works — two runtimes, one cockpit ── */}
      <section id="get-started" className="border-t border-surface-800/60 px-6 py-16">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-3 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            One cockpit, two runtimes
          </h2>
          <p className="mx-auto mb-10 max-w-2xl text-center text-sm text-surface-400">
            Run the agent where it fits your life — your own hardware for free,
            or a Yaver-managed workspace when you want it remote.
          </p>
          <div className="grid gap-6 md:grid-cols-2 md:items-start">
            {/* Column 1 — self-host path */}
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[#6366f1]/10 text-sm font-bold text-[#6366f1]">1</span>
                <span className="text-sm font-semibold text-surface-100">Self-host — free, your machines</span>
              </div>
              <div className="terminal">
                <div className="terminal-header">
                  <div className="terminal-dot bg-[#ff5f57]" />
                  <div className="terminal-dot bg-[#febc2e]" />
                  <div className="terminal-dot bg-[#28c840]" />
                </div>
                <div className="terminal-body space-y-1 text-[12px]">
                  <div><span className="text-surface-400">$</span> <span className="text-surface-200">npm install -g yaver-cli</span></div>
                  <div><span className="text-surface-400">$</span> <span className="text-surface-200">yaver auth</span></div>
                </div>
              </div>
              <p className="mt-3 text-[11px] text-surface-500">
                Your laptop, home server, or Pi becomes a runtime — driven
                from the Yaver desktop app, your phone, or the web. Builds,
                files, git state, prompts, and runner output stay on your
                hardware. Connect over LAN, your own relay, or Tailscale.{" "}
                <Link href="/download" className="underline hover:text-surface-300">
                  Install instructions
                </Link>.
              </p>
            </div>

            {/* Column 2 — optional managed relay path */}
            <div className="rounded-xl border border-emerald-500/20 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-emerald-500/10 text-sm font-bold text-emerald-500">2</span>
                <span className="text-sm font-semibold text-surface-100">Relay Pro — $9/mo, managed</span>
              </div>
              <div className="terminal">
                <div className="terminal-header">
                  <div className="terminal-dot bg-[#ff5f57]" />
                  <div className="terminal-dot bg-[#febc2e]" />
                  <div className="terminal-dot bg-[#28c840]" />
                </div>
                <div className="terminal-body space-y-1 text-[12px]">
                  <div className="text-surface-500"># keep using your own runtime:</div>
                  <div><span className="text-surface-400">$</span> <span className="text-surface-200">yaver serve</span></div>
                  <div className="text-[11px] text-green-400/80">{"\u2192 reachable through your managed private relay lane"}</div>
                </div>
              </div>
              <p className="mt-3 text-[11px] text-surface-500">
                Keep the repository and coding agent on your Mac, PC, Linux
                box, or VPS. Relay Pro makes that runtime reachable by every
                supported Yaver client and by apps using the Yaver SDK.{" "}
                <Link href="/pricing" className="underline hover:text-surface-300">
                  Pricing & details
                </Link>.
              </p>
            </div>

            {/* Column 3 — phone pairing + build/reload (spans both) */}
            <div className="flex flex-col gap-6 md:col-span-2">
              <div className="grid gap-6 md:grid-cols-2">
                <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
                  <div className="mb-3 flex items-center gap-2">
                    <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[#6366f1]/10 text-sm font-bold text-[#6366f1]">3</span>
                    <span className="text-sm font-semibold text-surface-100">Install a Yaver surface app</span>
                  </div>
                  <div className="mt-1 flex flex-col gap-2">
                    <a href="https://apps.apple.com/us/app/yaver-io/id6760467669" target="_blank" rel="noopener noreferrer"
                      className="inline-flex items-center gap-2 rounded-lg bg-surface-800 px-4 py-2.5 text-xs font-medium text-surface-300 transition-colors hover:bg-surface-700">
                      <svg className="h-4 w-4 shrink-0 text-surface-400" fill="currentColor" viewBox="0 0 24 24"><path d="M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.8-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.40 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z"/></svg>
                      iPhone download
                    </a>
                    <a href="https://play.google.com/store/apps/details?id=io.yaver.mobile" target="_blank" rel="noopener noreferrer"
                      className="inline-flex items-center gap-2 rounded-lg bg-surface-800 px-4 py-2.5 text-xs font-medium text-surface-300 transition-colors hover:bg-surface-700">
                      <svg className="h-4 w-4 shrink-0 text-surface-400" fill="currentColor" viewBox="0 0 24 24"><path d="M3 20.5V3.5c0-.35.2-.66.5-.85L13.5 12 3.5 21.35a1 1 0 01-.5-.85zm10.95-9l2.82-2.82 3.93 2.27c.7.4.7 1.38 0 1.78l-3.93 2.27-2.82-2.82L13.95 11.5zM4.5 2.66L14.2 12l-9.7 9.34L14.2 12 4.5 2.66z"/></svg>
                      Google Play
                    </a>
                  </div>
                  <p className="mt-3 text-[11px] text-surface-500">
                    Sign in with the same OAuth account you used for <code>yaver auth</code>. The app auto-pairs with your machine over LAN, or via relay on cellular &mdash; no QR code, no IP to type.
                  </p>
                </div>

                <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
                  <div className="mb-3 flex items-center gap-2">
                    <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[#6366f1]/10 text-sm font-bold text-[#6366f1]">4</span>
                    <span className="text-sm font-semibold text-surface-100">Build, reload, capture</span>
                  </div>
                  <div className="terminal">
                    <div className="terminal-header">
                      <div className="terminal-dot bg-[#ff5f57]" />
                      <div className="terminal-dot bg-[#febc2e]" />
                      <div className="terminal-dot bg-[#28c840]" />
                    </div>
                    <div className="terminal-body space-y-1 text-[12px]">
                      <div className="text-surface-500"># Ask the agent to build your app</div>
                      <div className="text-surface-500"># Yaver compiles and pushes the bundle</div>
                      <div className="my-1 h-px bg-surface-800/60" />
                      <div><span className="text-surface-400">$</span> <span className="text-surface-200">yaver push</span></div>
                      <div className="text-[11px] text-green-400/80">{"\u2192 Hermes bundle loaded on your paired phone"}</div>
                    </div>
                  </div>
                  <p className="mt-3 text-[11px] text-surface-500">
                    Shake the phone to send the screenshot, logs, and repro context back to the same coding agent.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* ── Section 3.5: Use from your coding agent (MCP) ── */}
      <section id="mcp" className="border-t border-surface-800/60 px-6 py-16">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-3 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Or use it from your coding agent
          </h2>
          <p className="mx-auto mb-8 max-w-2xl text-center text-sm text-surface-400">
            Yaver also exposes itself as an MCP server, so Claude Code, Codex,
            and OpenCode can drive the same real-device loop directly from the
            agent chat — build on your machine, hot-reload on your phone,
            shake to send repro context back.
          </p>
          <div className="mx-auto max-w-xl space-y-2 rounded-xl border border-surface-800 bg-surface-950 p-5 font-mono text-[12px] leading-relaxed">
            <div className="text-surface-500"># Claude Code:</div>
            <div><span className="text-surface-400">$</span> <span className="select-all text-surface-200">claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp</span></div>
            <div className="mt-2 text-surface-500"># Codex:</div>
            <div><span className="text-surface-400">$</span> <span className="select-all text-surface-200">codex mcp add yaver -- npx -y yaver-cli yaver-mcp</span></div>
            <div className="mt-2 text-surface-500"># OpenCode:</div>
            <div><span className="text-surface-400">$</span> <span className="select-all text-surface-200">npx -y -p yaver-cli yaver mcp setup opencode</span></div>
            <div className="mt-3 h-px bg-surface-800/60" />
            <div className="text-surface-500"># then, in the agent chat:</div>
            <div><span className="select-all text-emerald-300">call yaver_lazy_setup</span></div>
          </div>
          <p className="mt-3 text-center text-xs text-surface-600">
            <code>yaver_lazy_setup</code> surfaces the sign-in link and pairs your phone in-chat.{" "}
            <Link href="/docs/mcp" className="underline hover:text-surface-300">
              full MCP guide &rarr;
            </Link>
          </p>
          <p className="mt-4 text-center text-xs text-surface-600">
            Prefer the global CLI?{" "}
            <Link href="/download" className="underline hover:text-surface-300">
              npm install -g yaver-cli &rarr;
            </Link>
          </p>
        </div>
      </section>

      {/* ── Section 4: Pricing teaser ── */}
      <section id="pricing" className="border-t border-surface-800/60 px-6 py-20">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-3 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Simple pricing
          </h2>
          <p className="mx-auto mb-10 max-w-2xl text-center text-sm text-surface-400">
            Self-host for free. Pay only when you want Yaver to run infrastructure for you.
          </p>
          <div className="mx-auto grid max-w-4xl gap-6 md:grid-cols-2">
            {[
              {
                name: "Self-hosted",
                price: "$0",
                per: "forever",
                highlight: false,
                items: [
                  "npm install -g yaver-cli",
                  "Your machines, your relay, your data",
                  "Tailscale / LAN / self-hosted relay",
                  "All surfaces: phone, watch, TV, car, web",
                ],
                cta: { href: "/download", label: "Install free" },
              },
              {
                name: "Relay Pro",
                price: "$9",
                per: "/mo",
                highlight: true,
                items: [
                  "Managed private relay connectivity",
                  "Mac, PC, Linux box, VPS, or Pi",
                  "Browser lane, Hermes, Feedback SDK",
                  "Every Yaver surface and client SDK",
                ],
                cta: { href: "/pricing", label: "See details" },
              },
            ].map((plan) => (
              <div
                key={plan.name}
                className={
                  "flex flex-col rounded-xl border p-5 " +
                  (plan.highlight
                    ? "border-emerald-500/40 bg-emerald-500/5"
                    : "border-surface-800 bg-surface-900/50")
                }
              >
                <p className="text-sm font-semibold text-surface-100">{plan.name}</p>
                <p className="mt-2 text-3xl font-bold text-surface-50">
                  {plan.price}
                  <span className="text-sm font-normal text-surface-500">{plan.per}</span>
                </p>
                <ul className="mt-4 flex flex-1 flex-col gap-2">
                  {plan.items.map((item) => (
                    <li key={item} className="flex items-start gap-2 text-xs text-surface-400">
                      <span className="mt-0.5 text-emerald-500">{"\u2713"}</span>
                      {item}
                    </li>
                  ))}
                </ul>
                <Link
                  href={plan.cta.href}
                  className={
                    "mt-5 rounded-lg px-4 py-2.5 text-center text-sm font-medium transition-colors " +
                    (plan.highlight
                      ? "btn-primary"
                      : "border border-surface-700 bg-surface-900 text-surface-200 hover:border-surface-600 hover:text-surface-50")
                  }
                >
                  {plan.cta.label}
                </Link>
              </div>
            ))}
          </div>
          <p className="mt-8 text-center text-xs text-surface-600">
            Relay Pro is a private account-scoped lane on shared pass-through infrastructure, not a dedicated server or hosted coding machine.
          </p>
        </div>
      </section>

      {/* ── Section 5: Why this exists ── */}
      <section className="border-t border-surface-800/60 px-6 py-20">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-10 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Why this exists
          </h2>
          <div className="grid gap-4 md:grid-cols-3">
            {[
              {
                t: "Remote by default",
                d: "The agent runs where the work is — your machine or VPS — and you drive it from a phone, watch, TV, car, spatial client, the web, or your own app. Coding is no longer tied to a desk.",
              },
              {
                t: "Real phone loop",
                d: "The app runs on the device where mobile bugs actually happen: gestures, sensors, real network, and real performance. Hermes bytecode through a native bridge, not a WebView.",
              },
              {
                t: "Own machines, own keys",
                d: "Self-host for zero Yaver infrastructure: your files, your secrets, your relay, your Tailscale. Bring your own AI account on every path. No cloud token required, ever.",
              },
            ].map((item) => (
              <div key={item.t} className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
                <p className="text-sm font-semibold text-surface-100">{item.t}</p>
                <p className="mt-2 text-xs leading-relaxed text-surface-400">{item.d}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ── FAQ ── */}
      <section id="faq" className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-3xl">
          <h2 className="mb-10 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            FAQ
          </h2>
          <div>
            {LANDING_FAQ.map(({ q, a }) => (
              <FAQItem key={q} question={q} answer={a} />
            ))}
          </div>
        </div>
      </section>

    </>
  );
}
