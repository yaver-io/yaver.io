"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useRef, useState, useEffect } from "react";
import { useAuth } from "@/lib/use-auth";

// Canonical definitional one-liner — picked up by AI search
// (ChatGPT, Claude) and SEO as the answer to "what is Yaver?". Framed
// around the factual wedge (self-hosted on-device RN dev client, no
// third-party dev-portal gate) rather than specific competitor names,
// per LEGAL_SAFETY.md §2 (trademark) and §3 (comparative claims).
const LANDING_TAGLINE =
  "Yaver is an open-source, mobile-first feedback and AI-debugging SDK for app teams. Install one SDK in your app, capture user context from real phones, and hand incidents to Claude Code, Codex, Aider, or local models without giving up self-hosting.";

// Option B (Phone-first BaaS) is the YC-application framing, not the
// launch hero. When closer to the 2026-05-04 YC submission, swap
// LANDING_TAGLINE + the hero H1/subline to Option B (see LICENSING.md
// blog post and the competitive audit in the repo for the full pitch).
// Keeping it out of the page for now so the Show HN + viral launch has
// a single narrow wedge.

const LANDING_FAQ: ReadonlyArray<{ q: string; a: string }> = [
  // ── What is it ──────────────────────────────────────────────────────
  {
    q: "What is Yaver?",
    a: "An open-source, mobile-first developer toolkit centered on your own workstation. Drive your coding agent, trigger Hermes reloads on your own phone, capture feedback from your dev build, and keep everything running on your own machine — no hosted middleman. Works with any terminal agent: Claude Code, Codex, Aider, Goose, OpenCode, or a local model runner of your choice.",
  },
  {
    q: "Who is Yaver for?",
    a: "An individual developer running Yaver on their own hardware — laptop, Mac mini, Linux box, or VPS — using their own phone as a remote control. Kick off agents, watch runs, reload bundles, iterate on a dev build from the couch. Sharing with trusted teammates (see below) is supported as a secondary capability.",
  },
  {
    q: "How is this different from AWS or Vercel?",
    a: "Yaver starts one step earlier. The first backend tier can live in the mobile app itself, then move to your own machine. AWS and Vercel start with cloud infrastructure; Yaver starts with the phone and your own hardware.",
  },
  {
    q: "Can I use this for web apps, not just mobile?",
    a: "Yes. The current deepest workflow is mobile-first, especially React-based apps, but Yaver also has SDKs and integrations for web, Flutter, and Unity paths. The landing story starts with mobile because that is where user-context capture and reproduction pain are sharpest.",
  },

  // ── Push to Device (Section 6) ──────────────────────────────────────
  {
    q: "Do I really push apps to my phone without a developer portal or upload limits?",
    a: "Yes for React Native, and that Hermes path stays exactly the same. The Yaver mobile app is a self-hosted native container with 40+ pre-installed React Native modules. yaver-cli compiles your project's JavaScript to Hermes bytecode and pushes it directly to the container over WiFi, 4G, or your relay. No third-party developer-portal gate, no daily upload quotas, no cloud build minutes. Your binary stays on your machine. Flutter and fully native apps (Swift/Kotlin) are second-class compared to React Native, but on the same LAN Yaver can still trigger development builds and deliver them straight to your phone while you work from your laptop, even if the phone is elsewhere on your local network.",
  },
  {
    q: "Does push-to-device support the React Native New Architecture (TurboModules, Fabric)?",
    a: "Yes. Bundles load through a real native bridge built with ExpoReactNativeFactory + RCTAppDependencyProvider, so TurboModules, Fabric, JSI, and TurboModuleRegistry.getEnforcing() all work. RN 0.81+ apps run the same way they would in a production Xcode build — never a WebView.",
  },
  {
    q: "Can I hot-reload my app on a real phone while editing on my laptop?",
    a: "Yes. yaver dev start runs Metro on your machine; the mobile app proxies through the agent and reloads on save. Native code changes flow through Build + Deploy (one button to TestFlight / Play Store internal track). The Feedback SDK adds a floating feedback/debug button inside any embedding app, and the Yaver mobile app is the operator surface for triage when you are away from your desk.",
  },

  // ── Phone-first backend (Section 4) ─────────────────────────────────
  {
    q: "Can I build a backend without provisioning a server first?",
    a: "Yes. Phone-first projects let you scaffold a SQLite-backed backend with schema, auth personas, seed data, and CRUD routes from the mobile app or web dashboard. The whole project lives in ~/.yaver/phone-projects/<slug>/ until you decide to move it. No cloud account, no server, no DNS.",
  },
  {
    q: "Can I move my project to my own machine or to the cloud later?",
    a: "Yes. The same portable bundle promotes to your own hardware (Mac, Linux, WSL, Pi, or VPS) or to one of 19 hosted targets — Convex, Supabase, Postgres/Neon, Turso, and others. Each switch takes a snapshot first and supports rollback for 7 days. Standard Docker + Postgres at the bottom — export anytime.",
  },

  // ── Dashboard (Section 5) ───────────────────────────────────────────
  {
    q: "Can I browse my database from my phone?",
    a: "Yes. Convex Dashboard, Supabase Studio, Drizzle Studio, PocketBase Admin, and pgweb are tunneled through your relay so you can browse tables, run queries, and watch logs from your phone or any browser — even when the database is on localhost.",
  },

  // ── AI agents (Section 8) ───────────────────────────────────────────
  {
    q: "Which AI coding agents work with Yaver?",
    a: "Anything that runs in a terminal: Claude Code, Codex, Aider, Ollama, Goose, OpenCode, Amp, Continue, or any tmux session. Switch agents per task or set a default.",
  },
  {
    q: "Can I use my own LLM keys, Claude Max plan, or a local model?",
    a: "Yes. The agent shells out to whichever CLI you have installed, so it uses whatever auth or subscription you have already set up. Hybrid mode pairs a frontier planner (Claude or Codex) with a local Aider+Ollama implementer — typically 80–95% cheaper on long feature loops, fully air-gapped if you want.",
  },

  // ── Sharing / cost / privacy ────────────────────────────────────────
  {
    q: "Can I share my own machine with teammates?",
    a: "Yes. `yaver guests invite <email>` grants scoped P2P access to a teammate (max 5, invitations expire in 2 days, no shell / vault / session access). `yaver serve --multi-user --team <id>` gives each team member an isolated workspace on the same box. Sharing is meant for people you already trust — and if the way you use that access could touch a third party's platform rules (App Store, Play Store, SSO provider, model license, employer policy, etc.), checking compliance is your responsibility.",
  },
  {
    q: "Do I need a powerful machine?",
    a: "Any modern Mac or Linux machine works. 8 GB RAM is enough; 16 GB is comfortable once you have a few projects open at once. Yaver compiles Hermes bytecode on the fly when you push an app to your phone, which needs Node.js, Go, and a C/C++ toolchain — all installed automatically on first use. A $5/month Hetzner or DigitalOcean VPS works fine as a headless dev box you drive from your phone; disk is the only thing to size up (20+ GB for node_modules, Xcode-style caches, and hermesc build output).",
  },
  {
    q: "Is my code safe?",
    a: "Your code never leaves your machine. All traffic is P2P encrypted (QUIC+TLS). The relay is a dumb pipe — it forwards bytes but cannot read your data. Self-host the relay if you want zero third-party servers involved. Convex stores only sign-in identity, peer discovery, and audit summaries — never task input, output, files, vault values, or absolute paths (enforced by tests in the repo).",
  },
  {
    q: "Does Yaver manage my servers?",
    a: "No. The default path is your phone sandbox first, then your own machine, dev box, or VPS. The only managed offering is a $10/month shared relay so you do not have to run one yourself. Everything else is self-host.",
  },
  {
    q: "Is this really free?",
    a: "Yes for the local-first path: mobile app, CLI, agent, web dashboard, SDKs, and self-hosted relay. The repo uses a split license (FSL-1.1 core that auto-converts to Apache-2.0 two years per release, Apache-2.0 SDKs from day one). The optional managed relay is $10/month.",
  },
  {
    q: "What if I already use Vercel or Supabase Cloud?",
    a: "Great — keep using them for production. Use Yaver for local development at $0, then deploy to your cloud provider when ready. Yaver bridges your local stack and your production stack.",
  },

  // ── License ─────────────────────────────────────────────────────────
  {
    q: "What license is Yaver under?",
    a: "Split license. Core (agent, relay, backend, web, mobile app, desktop app) is FSL-1.1-Apache-2.0 — free for any non-competing use; each release auto-transitions to Apache-2.0 two years after publication. Client SDKs and CLIs are Apache-2.0 from day one. See LICENSING.md.",
  },
  {
    q: "Can I use Yaver in a commercial closed-source app?",
    a: "Yes. Using yaver-cli to build your app is a permitted use. Embedding the Feedback SDK, BlackBox, or push client is Apache-2.0 — your app stays under whatever license you choose.",
  },
  {
    q: "What triggers the FSL restriction?",
    a: "Hosting a commercial service that competes with Yaver's managed offering. Self-hosting, modifying for your own team, research, and consulting are all permitted uses. Each release becomes Apache-2.0 after 2 years and the restriction lifts for that version.",
  },
  {
    q: "I'm at a company that doesn't allow non-OSI licenses. Can I still use Yaver?",
    a: "Usually yes — most enterprise AGPL/GPL bans don't cover Fair-code. The SDKs you actually embed are Apache-2.0 anyway. If your legal team needs the core under different terms, a commercial license is available — email kivanc.cakmak@simkab.com.",
  },
  {
    q: "Can I fork Yaver?",
    a: "Yes. SDK forks (Apache-2.0) are unrestricted. Core forks (FSL) can be modified and self-hosted freely; they just can't be sold as a Yaver-competing service for 2 years per release.",
  },
];

const LANDING_HOWTO_STEPS: ReadonlyArray<{ name: string; text: string; url?: string }> = [
  {
    name: "Install the Yaver CLI",
    text: "Run npm install -g yaver-cli (or the curl install.sh on a box without Node).",
    url: "https://yaver.io/download",
  },
  {
    name: "Install the Yaver mobile app",
    text: "Download the Yaver mobile app from the App Store or Google Play and sign in with Apple, Google, or Microsoft SSO.",
    url: "https://yaver.io/download",
  },
  {
    name: "Embed the SDK and start the feedback loop",
    text: "In your existing app, install the Feedback SDK and pair it with Yaver. For React Native projects, yaver push compiles your JS to Hermes bytecode, validates the bundle, and pushes it to the Yaver app on a paired device. Once paired, app context and feedback incidents flow straight to your machine and your coding agent.",
    url: "https://yaver.io/manuals/cli-setup",
  },
];

function DebugConsolePreview() {
  const [panelOpen, setPanelOpen] = useState(true);
  const [btnPos, setBtnPos] = useState({ x: 0, y: 0 });
  const [dragging, setDragging] = useState(false);
  const dragRef = useRef<{ startX: number; startY: number; origX: number; origY: number } | null>(null);
  const [activeTab, setActiveTab] = useState("Home");
  const [selectedCategory, setSelectedCategory] = useState("All");
  const [selectedProduct, setSelectedProduct] = useState<string | null>(null);
  const [searchText, setSearchText] = useState("");
  const [cartItems, setCartItems] = useState<string[]>(["Salmon Bento", "Chicken Bento"]);
  const [input, setInput] = useState("");
  const outputRef = useRef<HTMLDivElement>(null);
  const allProducts = [
    { name: "Veggie Bento", price: "$129", color: "from-indigo-500/20 to-blue-500/20", cat: "Veggie", crashed: true },
    { name: "Salmon Bento", price: "$89", color: "from-rose-500/20 to-pink-500/20", cat: "Fish", crashed: false },
    { name: "Chicken Bento", price: "$249", color: "from-emerald-500/20 to-teal-500/20", cat: "Meat", crashed: false },
    { name: "Sunglasses", price: "$65", color: "from-amber-500/20 to-orange-500/20", cat: "Accessories", crashed: false },
  ];
  const filteredProducts = selectedCategory === "All" ? allProducts : allProducts.filter(p => p.cat === selectedCategory);
  const homeMessages = [
    { from: "agent", text: "\u26A0 error caught by SDK:" },
    { from: "agent", text: 'TypeError: Cannot read "price"' },
    { from: "agent", text: "at ProductCard (ProductList.tsx:34)" },
    { from: "agent", text: "screen: Home > Veggie Bento" },
    { from: "user", text: "> fix this crash" },
    { from: "agent", text: "task f82c started..." },
    { from: "agent", text: "reading ProductList.tsx..." },
    { from: "agent", text: "fix: added null check for price" },
    { from: "agent", text: "done. tap Reload to see the fix." },
  ];
  const [messages, setMessages] = useState(homeMessages);

  const [streaming, setStreaming] = useState(false);

  const addMessages = (...msgs: { from: string; text: string }[]) => {
    setMessages((prev) => [...prev.slice(-20), ...msgs]);
    setTimeout(() => { outputRef.current?.scrollTo({ top: outputRef.current.scrollHeight, behavior: "smooth" }); }, 50);
  };

  // Simulate streamed agent responses with delays + optional side effects (tab switching etc.)
  const simulateAgent = (steps: { from: string; text: string; delay: number; action?: () => void }[]) => {
    if (streaming) return;
    setStreaming(true);
    if (steps.length > 0) { addMessages(steps[0]); steps[0].action?.(); }
    let i = 1;
    const next = () => {
      if (i >= steps.length) { setStreaming(false); return; }
      const step = steps[i];
      i++;
      setTimeout(() => {
        addMessages(step);
        step.action?.();
        next();
      }, step.delay);
    };
    next();
  };

  return (
    <div className="mt-10 overflow-hidden rounded-2xl border border-surface-800/60 bg-surface-900/30 p-6 sm:p-8">
      {/* Two-column: description left, phone right */}
      <div className="flex flex-col items-center gap-8 lg:flex-row lg:items-center lg:gap-10">
        {/* Left — text */}
        <div className="flex-1">
          <span className="mb-3 inline-block rounded-full bg-[#6366f1]/15 px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-[#a5b4fc]">Feedback SDK</span>
          <h3 className="mb-3 text-xl font-bold text-surface-50 sm:text-2xl">
            Debug console inside your app
          </h3>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            Drop a single <code className="rounded bg-surface-800 px-1.5 py-0.5 text-xs text-[#a5b4fc]">&lt;FloatingButton /&gt;</code> component into your app. Gate it behind your developer user ID — your users never see it.
          </p>
          <p className="mb-6 text-xs leading-relaxed text-surface-500">
            Behind the console is your AI coding agent — Claude Code, Codex, Aider, Ollama, or any LLM you run. Messages go P2P to your dev machine, the agent writes code, and you see the result. No cloud middleman.
          </p>

          <div className="space-y-4">
            <div className="flex items-start gap-3">
              <div className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-surface-800 text-sm text-surface-300">&gt;</div>
              <div>
                <p className="text-sm font-medium text-surface-200">Message back &amp; forth</p>
                <p className="text-xs text-surface-500">Send tasks, see agent responses in real-time</p>
              </div>
            </div>
            <div className="flex items-start gap-3">
              <div className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-[#fbbf24]/10 text-sm text-[#fbbf24]">{"\u21BB"}</div>
              <div>
                <p className="text-sm font-medium text-surface-200">Hot Reload</p>
                <p className="text-xs text-surface-500">One tap to refresh after agent fixes code</p>
              </div>
            </div>
            <div className="flex items-start gap-3">
              <div className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-[#60a5fa]/10 text-sm text-[#60a5fa]">{"\u2692"}</div>
              <div>
                <p className="text-sm font-medium text-surface-200">Build + Deploy</p>
                <p className="text-xs text-surface-500">One button builds iOS + Android, uploads to TestFlight &amp; Play Store. Configurable: one platform, both, or build-only without deploy.</p>
              </div>
            </div>
            <div className="flex items-start gap-3">
              <div className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-[#f87171]/10 text-sm">{"\uD83D\uDC1B"}</div>
              <div>
                <p className="text-sm font-medium text-surface-200">Report Bug</p>
                <p className="text-xs text-surface-500">Screenshots the app (hides the SDK overlay), sends to agent. AI analyzes the UI and fixes the bug.</p>
              </div>
            </div>
            <div className="flex items-start gap-3">
              <div className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-[#a78bfa]/10 text-sm text-[#a78bfa]">{"\u25B6"}</div>
              <div>
                <p className="text-sm font-medium text-surface-200">Autonomous Test Loop</p>
                <p className="text-xs text-surface-500">Agent reads codebase, navigates every screen on device or emulator, catches crashes, fixes them, hot reloads, and repeats. Fix report shows all changes — staged, never committed.</p>
              </div>
            </div>
          </div>

        </div>

        {/* Right — phone mockup */}
        <div className="shrink-0 lg:self-center">
          <div className="relative overflow-hidden rounded-[2.8rem] border-[3px] border-surface-300/40 bg-white shadow-2xl shadow-black/30" style={{ width: 340 }}>
            {/* Dynamic Island */}
            <div className="relative z-20 mx-auto mt-2 h-[26px] w-[100px] rounded-full bg-black" />

            {/* Status bar */}
            <div className="flex items-center justify-between px-8 pb-1.5 pt-1">
              <span className="text-xs font-semibold text-gray-800">9:41</span>
              <div className="flex items-center gap-2">
                {/* Cell signal */}
                <svg className="h-3 w-4 text-gray-800" viewBox="0 0 20 14" fill="currentColor">
                  <rect x="0" y="10" width="3" height="4" rx="0.5"/>
                  <rect x="5" y="7" width="3" height="7" rx="0.5"/>
                  <rect x="10" y="4" width="3" height="10" rx="0.5"/>
                  <rect x="15" y="0" width="3" height="14" rx="0.5"/>
                </svg>
                {/* WiFi */}
                <svg className="h-3 w-4 text-gray-800" viewBox="0 0 24 24" fill="currentColor"><path d="M1 9l2 2c4.97-4.97 13.03-4.97 18 0l2-2C16.93 2.93 7.08 2.93 1 9zm8 8l3 3 3-3a4.237 4.237 0 00-6 0zm-4-4l2 2a7.074 7.074 0 0110 0l2-2C15.14 9.14 8.87 9.14 5 13z"/></svg>
                {/* Battery */}
                <svg className="h-3 w-6 text-gray-800" viewBox="0 0 28 13" fill="currentColor">
                  <rect x="0" y="0.5" width="24" height="12" rx="2.5" stroke="currentColor" strokeWidth="1" fill="none"/>
                  <rect x="25" y="3.5" width="2.5" height="5" rx="1"/>
                  <rect x="2" y="2.5" width="20" height="8" rx="1.5" fill="#34d399"/>
                </svg>
              </div>
            </div>

            {/* App content */}
            <div className="relative px-4 pb-12" style={{ minHeight: 600 }}>
              {/* === Tab Content === */}
              {activeTab === "Home" && !selectedProduct && (
                <>
                  {/* App nav */}
                  <div className="mb-3 flex items-center justify-between">
                    <h4 className="text-base font-bold text-gray-900">BentoApp</h4>
                    <div className="flex items-center gap-2.5">
                      <button className="flex h-8 w-8 items-center justify-center rounded-full bg-gray-100" onClick={() => setActiveTab("Search")}>
                        <svg className="h-4 w-4 text-gray-500" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" /></svg>
                      </button>
                      <div className="h-8 w-8 rounded-full bg-gradient-to-br from-indigo-400 to-purple-500" />
                    </div>
                  </div>
                  {/* Category pills */}
                  <div className="mb-3 flex gap-1.5">
                    {["All", "Veggie", "Fish", "Meat", "Dessert"].map((c) => (
                      <button key={c} onClick={() => setSelectedCategory(c)} className={`rounded-full px-3 py-1.5 text-[11px] font-medium transition-colors ${selectedCategory === c ? "bg-gray-900 text-white" : "bg-gray-100 text-gray-500 hover:bg-gray-200"}`}>{c}</button>
                    ))}
                  </div>
                  {/* Product grid */}
                  <div className="grid grid-cols-2 gap-2.5">
                    {filteredProducts.map((p) => (
                      <button
                        key={p.name}
                        className={`rounded-xl p-2 text-left transition-all active:scale-95 ${p.crashed ? "border border-red-400/40 bg-red-50" : "bg-gray-50 hover:bg-gray-100"}`}
                        onClick={() => { if (!p.crashed) setSelectedProduct(p.name); }}
                      >
                        <div className={`mb-2 flex h-20 items-center justify-center rounded-lg bg-gradient-to-br ${p.color}`}>
                          {p.crashed && <div className="rounded-md bg-red-500/20 px-2 py-1"><p className="text-[9px] font-bold text-red-500">CRASH</p></div>}
                        </div>
                        <p className={`text-[11px] font-medium ${p.crashed ? "text-red-600" : "text-gray-800"}`}>{p.name}</p>
                        {p.crashed
                          ? <p className="text-[9px] text-red-400">TypeError: null price</p>
                          : <p className="text-xs font-semibold text-gray-900">{p.price}</p>
                        }
                      </button>
                    ))}
                  </div>
                </>
              )}

              {/* Product detail view */}
              {activeTab === "Home" && selectedProduct && (
                <div className="pt-1">
                  <button onClick={() => setSelectedProduct(null)} className="mb-3 flex items-center gap-1 text-[12px] text-gray-500">
                    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" /></svg>
                    Back
                  </button>
                  {(() => { const p = allProducts.find(x => x.name === selectedProduct); if (!p) return null; return (
                    <>
                      <div className={`mb-4 h-36 rounded-2xl bg-gradient-to-br ${p.color}`} />
                      <h4 className="text-lg font-bold text-gray-900">{p.name}</h4>
                      <p className="mb-1 text-xs text-gray-400">{p.cat}</p>
                      <p className="mb-4 text-xl font-bold text-gray-900">{p.price}</p>
                      <p className="mb-5 text-[11px] leading-relaxed text-gray-500">
                        Premium quality {p.name.toLowerCase()} crafted with attention to detail.
                      </p>
                      <div className="flex gap-3">
                        <button
                          onClick={() => { if (!cartItems.includes(p.name)) setCartItems([...cartItems, p.name]); }}
                          className={`flex-1 rounded-xl py-3 text-center text-[13px] font-semibold transition-colors ${cartItems.includes(p.name) ? "bg-gray-100 text-gray-400" : "bg-gray-900 text-white"}`}
                        >
                          {cartItems.includes(p.name) ? "In Cart" : "Add to Cart"}
                        </button>
                        <button className="rounded-xl border border-gray-200 px-4 py-3">
                          <svg className="h-5 w-5 text-gray-400" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" d="M21 8.25c0-2.485-2.099-4.5-4.688-4.5-1.935 0-3.597 1.126-4.312 2.733-.715-1.607-2.377-2.733-4.313-2.733C5.1 3.75 3 5.765 3 8.25c0 7.22 9 12 9 12s9-4.78 9-12z" /></svg>
                        </button>
                      </div>
                    </>
                  ); })()}
                </div>
              )}

              {activeTab === "Search" && (
                <div className="pt-2">
                  <div className="mb-4 flex items-center gap-2 rounded-xl bg-gray-100 px-4 py-2.5">
                    <svg className="h-4 w-4 text-gray-400" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" /></svg>
                    <input
                      type="text"
                      value={searchText}
                      onChange={(e) => setSearchText(e.target.value)}
                      placeholder="Search products..."
                      className="flex-1 bg-transparent text-xs text-gray-800 placeholder:text-gray-400 focus:outline-none"
                    />
                    {searchText && <button onClick={() => setSearchText("")} className="text-[10px] text-gray-400">{"\u2715"}</button>}
                  </div>
                  {searchText ? (
                    <div>
                      <p className="mb-3 text-[11px] text-gray-400">{allProducts.filter(p => p.name.toLowerCase().includes(searchText.toLowerCase())).length} results</p>
                      {allProducts.filter(p => p.name.toLowerCase().includes(searchText.toLowerCase())).map((p) => (
                        <button key={p.name} onClick={() => { setSelectedProduct(p.name); setActiveTab("Home"); }} className="mb-2 flex w-full items-center gap-3 rounded-xl bg-gray-50 p-3 text-left transition-colors hover:bg-gray-100">
                          <div className={`h-12 w-12 shrink-0 rounded-lg bg-gradient-to-br ${p.color}`} />
                          <div className="flex-1">
                            <p className="text-[12px] font-medium text-gray-800">{p.name}</p>
                            <p className="text-[11px] text-gray-400">{p.cat}</p>
                          </div>
                          <p className="text-[12px] font-semibold text-gray-900">{p.price}</p>
                        </button>
                      ))}
                    </div>
                  ) : (
                    <>
                      <p className="mb-3 text-[11px] font-semibold text-gray-400">Recent</p>
                      {["running shoes", "leather wallet", "wireless earbuds"].map((q) => (
                        <button key={q} onClick={() => setSearchText(q)} className="flex w-full items-center gap-3 border-b border-gray-100 py-3 text-left">
                          <svg className="h-4 w-4 text-gray-300" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
                          <p className="text-[12px] text-gray-600">{q}</p>
                        </button>
                      ))}
                      <p className="mb-3 mt-6 text-[11px] font-semibold text-gray-400">Trending</p>
                      <div className="flex flex-wrap gap-2">
                        {["Nike", "Apple Watch", "Ray-Ban", "Adidas", "Gucci"].map((t) => (
                          <button key={t} onClick={() => setSearchText(t.toLowerCase())} className="rounded-full bg-gray-100 px-3 py-1.5 text-[10px] text-gray-600 transition-colors hover:bg-gray-200">{t}</button>
                        ))}
                      </div>
                    </>
                  )}
                </div>
              )}

              {activeTab === "Cart" && (
                <div className="pt-2">
                  <h4 className="mb-4 text-base font-bold text-gray-900">Cart ({cartItems.length})</h4>
                  {cartItems.length === 0 ? (
                    <div className="py-16 text-center">
                      <p className="mb-2 text-2xl">{"\uD83D\uDED2"}</p>
                      <p className="text-[13px] text-gray-400">Your cart is empty</p>
                      <button onClick={() => setActiveTab("Home")} className="mt-3 text-[12px] text-gray-500">Browse products</button>
                    </div>
                  ) : (
                    <>
                      {cartItems.map((itemName) => {
                        const p = allProducts.find(x => x.name === itemName);
                        if (!p) return null;
                        return (
                          <div key={itemName} className="mb-2 flex items-center gap-3 rounded-xl bg-gray-50 p-3">
                            <div className={`h-14 w-14 shrink-0 rounded-lg bg-gradient-to-br ${p.color}`} />
                            <div className="flex-1">
                              <p className="text-[12px] font-medium text-gray-800">{p.name}</p>
                              <p className="text-[11px] text-gray-400">Qty: 1</p>
                              <p className="mt-0.5 text-[12px] font-semibold text-gray-900">{p.price}</p>
                            </div>
                            <button onClick={() => setCartItems(cartItems.filter(x => x !== itemName))} className="rounded-lg p-2 text-gray-300 transition-colors hover:text-red-400">
                              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" d="M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0" /></svg>
                            </button>
                          </div>
                        );
                      })}
                      <div className="mt-2 flex items-center justify-between rounded-xl bg-gray-50 px-4 py-3">
                        <p className="text-xs text-gray-400">Total</p>
                        <p className="text-sm font-bold text-gray-900">
                          ${cartItems.reduce((sum, name) => { const p = allProducts.find(x => x.name === name); return sum + (p ? parseFloat(p.price.slice(1)) : 0); }, 0).toFixed(0)}
                        </p>
                      </div>
                      <button className="mt-3 w-full rounded-xl bg-gray-900 py-3 text-center transition-colors hover:bg-gray-800 active:scale-[0.98]">
                        <p className="text-[13px] font-semibold text-white">Checkout</p>
                      </button>
                    </>
                  )}
                </div>
              )}

              {activeTab === "Profile" && (
                <div className="pt-2">
                  <div className="mb-5 flex items-center gap-4">
                    <div className="h-14 w-14 rounded-full bg-gradient-to-br from-indigo-400 to-purple-500" />
                    <div>
                      <p className="text-[14px] font-bold text-gray-900">Jane Developer</p>
                      <p className="text-[11px] text-gray-400">jane@acme.dev</p>
                    </div>
                  </div>
                  {["Orders", "Wishlist", "Addresses", "Payment Methods", "Settings"].map((item) => (
                    <div key={item} className="flex items-center justify-between border-b border-gray-100 py-3.5">
                      <p className="text-[12px] text-gray-600">{item}</p>
                      <svg className="h-4 w-4 text-gray-300" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" /></svg>
                    </div>
                  ))}
                  <div className="mt-4 rounded-xl border border-gray-200 py-3 text-center">
                    <p className="text-[12px] text-gray-400">Sign Out</p>
                  </div>
                </div>
              )}

              {/* Tab bar */}
              <div className="absolute bottom-0 left-0 right-0 flex items-center justify-around border-t border-gray-100 bg-white/95 px-4 py-3 backdrop-blur">
                {[
                  { icon: "M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6", label: "Home" },
                  { icon: "M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z", label: "Search" },
                  { icon: "M15.75 10.5V6a3.75 3.75 0 10-7.5 0v4.5m11.356-1.993l1.263 12c.07.665-.45 1.243-1.119 1.243H4.25a1.125 1.125 0 01-1.12-1.243l1.264-12A1.125 1.125 0 015.513 7.5h12.974c.576 0 1.059.435 1.119 1.007zM8.625 10.5a.375.375 0 11-.75 0 .375.375 0 01.75 0zm7.5 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z", label: "Cart" },
                  { icon: "M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.501 20.118a7.5 7.5 0 0114.998 0A17.933 17.933 0 0112 21.75c-2.676 0-5.216-.584-7.499-1.632z", label: "Profile" },
                ].map((t) => (
                  <button
                    key={t.label}
                    className="flex flex-col items-center gap-1"
                    onClick={() => {
                      setActiveTab(t.label);
                      if (t.label === "Home") {
                        setMessages(homeMessages);
                      } else {
                        setMessages([]);
                        setInput("");
                      }
                    }}
                  >
                    <svg className={`h-5 w-5 ${activeTab === t.label ? "text-gray-900" : "text-gray-300"}`} fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" d={t.icon} /></svg>
                    <span className={`text-[10px] ${activeTab === t.label ? "font-semibold text-gray-900" : "text-gray-300"}`}>{t.label}</span>
                  </button>
                ))}
              </div>

              {/* ── Yaver floating button (draggable) ── */}
              <div
                className="absolute z-30"
                style={{ left: 12 + btnPos.x, top: 220 + btnPos.y, cursor: dragging ? "grabbing" : "grab", touchAction: "none" }}
                onPointerDown={(e) => {
                  dragRef.current = { startX: e.clientX, startY: e.clientY, origX: btnPos.x, origY: btnPos.y };
                  setDragging(true);
                  (e.target as HTMLElement).setPointerCapture(e.pointerId);
                }}
                onPointerMove={(e) => {
                  if (!dragRef.current) return;
                  const dx = e.clientX - dragRef.current.startX;
                  const dy = e.clientY - dragRef.current.startY;
                  setBtnPos({ x: dragRef.current.origX + dx, y: dragRef.current.origY + dy });
                }}
                onPointerUp={(e) => {
                  const d = dragRef.current;
                  const moved = d ? Math.abs(e.clientX - d.startX) + Math.abs(e.clientY - d.startY) : 0;
                  dragRef.current = null;
                  setDragging(false);
                  if (moved < 5) setPanelOpen((v) => !v);
                }}
              >
                <div className="relative flex h-10 w-10 items-center justify-center rounded-xl shadow-lg transition-transform hover:scale-110 active:scale-95" style={{ background: "linear-gradient(135deg, #1a1a1a, #333, #555, #333, #1a1a1a)", backgroundSize: "300% 300%", animation: "yaver-btn-glow 3s ease infinite", boxShadow: "0 4px 12px rgba(0,0,0,0.5)" }}>
                  <span className="pointer-events-none text-base font-extrabold italic text-white drop-shadow-md">{panelOpen ? "\u2715" : "y"}</span>
                  <div className="pointer-events-none absolute -right-[2px] -top-[2px] h-2 w-2 rounded-full border-[1.5px] border-[#0a0a0a] bg-[#22c55e]" />
                </div>

                {/* Debug panel — stopPropagation prevents pointer events from toggling the panel */}
                {panelOpen && (
                  <div
                    className="absolute left-0 top-12 z-20 w-[255px] overflow-hidden rounded-xl border border-[#6366f1]/30 bg-[#0a0a0a] shadow-2xl shadow-black/80"
                    onPointerDown={(e) => e.stopPropagation()}
                    onPointerUp={(e) => e.stopPropagation()}
                    onClick={(e) => e.stopPropagation()}
                  >
                    {/* Header */}
                    <div className="flex items-center gap-1.5 px-3 py-2">
                      <div className="h-1.5 w-1.5 rounded-full bg-[#22c55e]" />
                      <span className="flex-1 font-mono text-[9px] font-bold uppercase tracking-widest text-[#6366f1]">YAVER DEBUG</span>
                      <span className="font-mono text-[8px] text-surface-600">live</span>
                      <button onClick={() => setPanelOpen(false)} className="ml-1 rounded px-1 py-0.5 text-[10px] text-surface-500 hover:bg-surface-800 hover:text-surface-300">{"\u2197"}</button>
                      <button onClick={() => setPanelOpen(false)} className="rounded px-1 py-0.5 text-[10px] text-surface-500 hover:bg-surface-800 hover:text-surface-300">{"\u2715"}</button>
                    </div>

                    {/* Output */}
                    <div ref={outputRef} className="max-h-[90px] overflow-y-auto bg-[#111] px-3 py-2 font-mono text-[10px] leading-4">
                      {messages.length > 0 ? messages.map((msg, i) => (
                        <div key={i} className={msg.from === "user" ? "text-surface-500" : msg.text.startsWith("\u26A0") || msg.text.startsWith("TypeError") ? "text-[#f87171]" : "text-[#22c55e]"}>
                          {msg.text}
                        </div>
                      )) : (
                        <div className="text-surface-600">connected. type a message or use actions.</div>
                      )}
                    </div>

                    {/* Input */}
                    <div className="flex items-center gap-1.5 border-t border-surface-800/60 px-3 py-1.5">
                      <span className="font-mono text-xs font-bold text-[#6366f1]">&gt;</span>
                      <input
                        type="text"
                        value={input}
                        onChange={(e) => setInput(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" && input.trim()) {
                            const msg = input;
                            setInput("");
                            simulateAgent([
                              { from: "user", text: `> ${msg}`, delay: 0 },
                              { from: "agent", text: `task ${Math.random().toString(36).slice(2, 6)} started...`, delay: 500 },
                              { from: "agent", text: "reading codebase...", delay: 800 },
                              { from: "agent", text: `applying fix for: ${msg.slice(0, 30)}`, delay: 1200 },
                              { from: "agent", text: "1 file changed, 3 insertions(+)", delay: 1000 },
                              { from: "agent", text: "done.", delay: 600 },
                            ]);
                          }
                        }}
                        placeholder="tell the agent..."
                        className="flex-1 bg-transparent font-mono text-[10px] text-surface-200 placeholder:text-surface-600 focus:outline-none"
                      />
                      <button
                        onClick={() => {
                          if (input.trim()) {
                            const msg = input;
                            setInput("");
                            simulateAgent([
                              { from: "user", text: `> ${msg}`, delay: 0 },
                              { from: "agent", text: `task ${Math.random().toString(36).slice(2, 6)} started...`, delay: 500 },
                              { from: "agent", text: "reading codebase...", delay: 800 },
                              { from: "agent", text: `applying fix for: ${msg.slice(0, 30)}`, delay: 1200 },
                              { from: "agent", text: "1 file changed, 3 insertions(+)", delay: 1000 },
                              { from: "agent", text: "done.", delay: 600 },
                            ]);
                          }
                        }}
                        className={`rounded-md bg-[#6366f1] px-2 py-1 font-mono text-[9px] font-bold text-white transition-colors hover:bg-[#5558e6] ${streaming ? "pointer-events-none opacity-50" : ""}`}
                      >
                        run
                      </button>
                    </div>

                    {/* Action cards row 1 — Reload | Build | Report Bug */}
                    <div className="grid grid-cols-3 gap-1.5 border-t border-surface-800/60 px-2.5 py-1.5">
                      <button
                        onClick={() => simulateAgent([
                          { from: "user", text: "> reload", delay: 0 },
                          { from: "agent", text: "sending reload signal...", delay: 400 },
                          { from: "agent", text: "metro bundler: rebuild", delay: 800 },
                          { from: "agent", text: "bundle complete (0.3s)", delay: 1200 },
                          { from: "agent", text: "done.", delay: 500 },
                        ])}
                        className={`flex flex-col items-center gap-0.5 rounded-lg border border-surface-800 bg-[#111] py-1.5 transition-colors hover:border-[#fbbf24]/40 hover:bg-[#fbbf24]/5 ${streaming ? "pointer-events-none opacity-50" : ""}`}
                      >
                        <span className="text-sm text-[#fbbf24]">{"\u21BB"}</span>
                        <span className="font-mono text-[6px] font-semibold text-surface-400">Hot Reload</span>
                      </button>
                      <button
                        onClick={() => simulateAgent([
                          { from: "user", text: "> build + deploy", delay: 0 },
                          { from: "agent", text: "task d4f2 started...", delay: 500 },
                          { from: "agent", text: "[iOS] archive + TestFlight...", delay: 1200 },
                          { from: "agent", text: "[iOS] build 65 uploaded.", delay: 2000 },
                          { from: "agent", text: "[Android] bundleRelease...", delay: 1500 },
                          { from: "agent", text: "[Android] AAB uploaded.", delay: 1800 },
                          { from: "agent", text: "both deployed. done.", delay: 600 },
                        ])}
                        className={`flex flex-col items-center gap-0.5 rounded-lg border border-surface-800 bg-[#111] py-1.5 transition-colors hover:border-[#60a5fa]/40 hover:bg-[#60a5fa]/5 ${streaming ? "pointer-events-none opacity-50" : ""}`}
                      >
                        <span className="text-sm text-[#60a5fa]">{"\u2692"}</span>
                        <span className="font-mono text-[7px] font-semibold text-surface-400">Build</span>
                      </button>
                      <button
                        onClick={() => simulateAgent([
                          { from: "user", text: "> report bug", delay: 0 },
                          { from: "agent", text: "capturing screenshot...", delay: 400 },
                          { from: "agent", text: "screenshot sent to agent", delay: 600 },
                          { from: "agent", text: "analyzing UI state...", delay: 800 },
                          { from: "agent", text: "found: null price on card 1", delay: 1200 },
                          { from: "agent", text: "fix: ProductList.tsx:34", delay: 800 },
                          { from: "agent", text: "added null check. done.", delay: 700 },
                        ])}
                        className={`flex flex-col items-center gap-0.5 rounded-lg border border-surface-800 bg-[#111] py-1.5 transition-colors hover:border-[#f87171]/40 hover:bg-[#f87171]/5 ${streaming ? "pointer-events-none opacity-50" : ""}`}
                      >
                        <span className="text-[11px]">{"\uD83D\uDC1B"}</span>
                        <span className="font-mono text-[6px] font-semibold text-surface-400">Report Bug</span>
                      </button>
                    </div>

                    {/* Action cards row 2 — Test App | Reset */}
                    <div className="grid grid-cols-2 gap-1.5 border-t border-surface-800/40 px-2.5 py-1.5">
                      <button
                        onClick={() => simulateAgent([
                          { from: "user", text: "> test app", delay: 0 },
                          { from: "agent", text: "reading src/ for context...", delay: 500 },
                          { from: "agent", text: "found 4 screens, 6 components", delay: 900 },
                          // Home tab
                          { from: "agent", text: "[Home] navigating...", delay: 600, action: () => setActiveTab("Home") },
                          { from: "agent", text: "[Home] scrolling products...", delay: 600 },
                          { from: "agent", text: "[Home] tap Salmon Bento...", delay: 500, action: () => setSelectedProduct("Salmon Bento") },
                          { from: "agent", text: "[Home] detail loaded. back.", delay: 700, action: () => setSelectedProduct(null) },
                          { from: "agent", text: "[Home] tap Veggie Bento...", delay: 500 },
                          { from: "agent", text: "\u26A0 TypeError: price is null", delay: 800 },
                          { from: "agent", text: "fix: ProductCard.tsx:34", delay: 600 },
                          { from: "agent", text: "  price?.toFixed(2) ?? 'N/A'", delay: 400 },
                          { from: "agent", text: "hot reload \u2713 crash gone", delay: 800 },
                          // Search tab
                          { from: "agent", text: "[Search] navigating...", delay: 500, action: () => setActiveTab("Search") },
                          { from: "agent", text: "[Search] typing 'shoes'...", delay: 500, action: () => setSearchText("shoes") },
                          { from: "agent", text: "[Search] 1 result found. ok", delay: 700 },
                          { from: "agent", text: "[Search] clear. typing 'bag'...", delay: 500, action: () => setSearchText("bag") },
                          { from: "agent", text: "[Search] 1 result. tapping...", delay: 600, action: () => { setSearchText(""); setActiveTab("Home"); setSelectedProduct("Salmon Bento"); } },
                          { from: "agent", text: "[Search] detail ok. back.", delay: 600, action: () => { setSelectedProduct(null); setActiveTab("Search"); setSearchText(""); } },
                          // Cart tab
                          { from: "agent", text: "[Cart] navigating...", delay: 500, action: () => setActiveTab("Cart") },
                          { from: "agent", text: "[Cart] 2 items. total $338.", delay: 600 },
                          { from: "agent", text: "[Cart] removing item...", delay: 500, action: () => setCartItems(["Chicken Bento"]) },
                          { from: "agent", text: "[Cart] 1 item. total $249.", delay: 600 },
                          { from: "agent", text: "[Cart] tap checkout... ok", delay: 500, action: () => setCartItems(["Salmon Bento", "Chicken Bento"]) },
                          // Profile tab
                          { from: "agent", text: "[Profile] navigating...", delay: 500, action: () => setActiveTab("Profile") },
                          { from: "agent", text: "[Profile] user info ok", delay: 500 },
                          { from: "agent", text: "[Profile] tap Orders... ok", delay: 400 },
                          { from: "agent", text: "[Profile] tap Settings... ok", delay: 400 },
                          { from: "agent", text: "[Profile] tap Sign Out...", delay: 500 },
                          // Report
                          { from: "agent", text: "--- test complete ---", delay: 600, action: () => setActiveTab("Home") },
                          { from: "agent", text: "4 screens tested", delay: 300 },
                          { from: "agent", text: "1 bug found, 1 fix applied", delay: 300 },
                          { from: "agent", text: "ProductCard.tsx:34 (null price)", delay: 300 },
                          { from: "agent", text: "changes staged, not committed", delay: 300 },
                        ])}
                        className={`flex flex-col items-center gap-0.5 rounded-lg border border-surface-800 bg-[#111] py-1.5 transition-colors hover:border-[#a78bfa]/40 hover:bg-[#a78bfa]/5 ${streaming ? "pointer-events-none opacity-50" : ""}`}
                      >
                        <span className="text-sm text-[#a78bfa]">{"\u25B6"}</span>
                        <span className="font-mono text-[7px] font-semibold text-surface-400">Test App</span>
                      </button>
                      <button
                        onClick={() => {
                          setMessages(homeMessages);
                          setActiveTab("Home");
                          setSelectedProduct(null);
                          setSelectedCategory("All");
                          setSearchText("");
                          setCartItems(["Salmon Bento", "Chicken Bento"]);
                          setPanelOpen(true);
                        }}
                        className="flex flex-col items-center gap-0.5 rounded-lg border border-surface-800 bg-[#111] py-1.5 transition-colors hover:bg-surface-800"
                      >
                        <span className="text-sm text-surface-500">{"\u21BA"}</span>
                        <span className="font-mono text-[7px] font-semibold text-surface-500">Reset</span>
                      </button>
                    </div>
                  </div>
                )}
              </div>
            </div>

            {/* Home indicator */}
            <div className="mx-auto mb-2 h-[5px] w-32 rounded-full bg-gray-200" />
          </div>
        </div>
      </div>
    </div>
  );
}

// Video assets live on GitHub Releases, not Vercel/Cloudflare — keeps the
// web/ bundle tiny and the 2.3 MB Bento reel out of our CDN bill.
const VIDEO_CDN = "https://github.com/kivanccakmak/yaver.io/releases/download";

const DEMO_TABS = [
  {
    id: "push-fix",
    label: "Push to Phone",
    icon: "\uD83D\uDCF1",
    desc: "yaver push — send your RN project to a paired iPhone in seconds. Shake to report a crash. AI fixes it. Hot reload.",
    header: "SFMG on iPhone + Claude editing splash color on the Mac",
    video: `${VIDEO_CDN}/yaver-hosting-demo-v1/yaver-hosting-demo.mp4`,
  },
  {
    id: "full-loop",
    label: "Full Loop",
    icon: "\uD83D\uDD04",
    desc: "Create a project, browse your database, vibe code a feature \u2014 all from your phone.",
    header: "Yaver scaffolds Bento + Bento running live on iPhone 16 Pro sim",
    video: `${VIDEO_CDN}/bento-demo-v1/bento-demo.mp4`,
  },
  {
    id: "auto-test",
    label: "Auto Test",
    icon: "\uD83E\uDD16",
    desc: "Agent navigates every screen, finds 2 crashes, fixes both, produces a report.",
    header: "Agent autonomously navigating Bento \u2014 phone + terminal",
    video: null,
  },
];

function DemoSection() {
  const [activeDemo, setActiveDemo] = useState("push-fix");
  const demo = DEMO_TABS.find((d) => d.id === activeDemo)!;

  return (
    <section className="border-t border-surface-800/60 px-6 py-20">
      <div className="mx-auto max-w-5xl">
        {/* Tab bar */}
        <div className="mb-6 flex items-center justify-center gap-2">
          {DEMO_TABS.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setActiveDemo(tab.id)}
              className={`flex items-center gap-2 rounded-xl px-4 py-2.5 text-sm font-medium transition-all ${
                activeDemo === tab.id
                  ? "bg-[#6366f1] text-white shadow-lg shadow-indigo-500/20"
                  : "bg-surface-900 text-surface-400 hover:bg-surface-800 hover:text-surface-200"
              }`}
            >
              <span className="text-base">{tab.icon}</span>
              <span>{tab.label}</span>
            </button>
          ))}
        </div>

        {/* Description */}
        <p className="mb-4 text-center text-sm text-surface-400">{demo.desc}</p>

        {/* Video frame */}
        <div className="overflow-hidden rounded-2xl border border-surface-800 bg-surface-950">
          {/* Video header bar */}
          <div className="flex items-center gap-2 border-b border-surface-800/60 bg-surface-900/50 px-4 py-2">
            <div className="flex gap-1.5">
              <div className="h-3 w-3 rounded-full bg-[#ff5f57]" />
              <div className="h-3 w-3 rounded-full bg-[#febc2e]" />
              <div className="h-3 w-3 rounded-full bg-[#28c840]" />
            </div>
            <span className="flex-1 text-center text-xs text-surface-500">{demo.header}</span>
          </div>

          {/* Video */}
          {demo.video ? (
            <video
              key={demo.id}
              src={demo.video}
              className="w-full bg-[#050508]"
              autoPlay
              muted
              loop
              playsInline
              preload="metadata"
              controls
            />
          ) : (
            <div className="flex aspect-video items-center justify-center bg-[#050508]">
              <div className="px-6 text-center">
                <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full border border-surface-700 bg-surface-900">
                  <svg className="ml-1 h-7 w-7 text-surface-400" fill="currentColor" viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg>
                </div>
                <p className="mb-1 text-sm font-medium text-surface-300">Coming soon</p>
                <p className="mx-auto max-w-xs text-xs text-surface-500">
                  Shooting this cut next \u2014 Bento running through the shake-to-report / auto-test flows.
                </p>
              </div>
            </div>
          )}
        </div>

      </div>
    </section>
  );
}

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

function ResourceCardsSection() {
  const cards = [
    {
      eyebrow: "Manual",
      title: "MacBook to Windows AI Box over SSH",
      text: "OpenSSH, Tailscale, always-on power settings, Ollama with Qwen, and a clean remote coding loop for a stronger Windows box behind a MacBook.",
      href: "/manuals/windows-ssh-coding-box",
      cta: "Read setup guide",
    },
    {
      eyebrow: "Developer Docs",
      title: "Operational notes for the repo",
      text: "Short-form developer reference for the remote Windows box pattern, including transport, model sizing, and always-on behavior.",
      href: "/docs/developers#remote-windows-box",
      cta: "Open developer docs",
    },
  ];

  return (
    <section className="border-t border-surface-800/60 px-6 py-24">
      <div className="mx-auto max-w-5xl">
        <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
          Remote Box Guides
        </h2>
        <p className="mx-auto mb-10 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
          Running a stronger Windows machine as the always-on AI box behind a MacBook is a real
          path. These pages cover the setup and the developer-facing operational notes.
        </p>
        <div className="grid gap-4 md:grid-cols-2">
          {cards.map((card) => (
            <Link
              key={card.href}
              href={card.href}
              className="rounded-xl border border-surface-800 bg-surface-900/50 p-5 transition-colors hover:border-surface-600"
            >
              <p className="mb-3 text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">
                {card.eyebrow}
              </p>
              <h3 className="mb-3 text-lg font-semibold text-surface-100">{card.title}</h3>
              <p className="mb-5 text-sm leading-relaxed text-surface-400">{card.text}</p>
              <span className="text-sm font-medium text-[#a5b4fc]">{card.cta} &rarr;</span>
            </Link>
          ))}
        </div>
      </div>
    </section>
  );
}

function MCPIntegrationSection() {
  const [mcpTab, setMcpTab] = useState<"stdio" | "http" | "cli">("stdio");

  return (
    <section className="border-t border-surface-800/60 px-6 py-24">
      <div className="mx-auto max-w-4xl">
        <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
          MCP Integration
        </h2>
        <p className="mx-auto mb-16 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
          Connect Yaver as an MCP server from Claude Desktop, Claude Web UI, or any MCP-compatible client.
        </p>

        {/* Tabs */}
        <div className="mb-6 flex items-center justify-center gap-2">
          {(
            [
              { key: "stdio", label: "Local (stdio)" },
              { key: "http", label: "Network (HTTP)" },
              { key: "cli", label: "CLI setup" },
            ] as const
          ).map((tab) => (
            <button
              key={tab.key}
              onClick={() => setMcpTab(tab.key)}
              className={`rounded-lg px-4 py-2 text-sm font-medium transition-colors ${
                mcpTab === tab.key
                  ? "bg-surface-800 text-surface-100"
                  : "text-surface-500 hover:text-surface-300"
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>

        {/* Tab content */}
        {mcpTab === "stdio" && (
          <div className="terminal">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">claude_desktop_config.json</span>
            </div>
            <div className="terminal-body text-[13px]">
              <pre className="text-surface-200 whitespace-pre-wrap">{`{
  "mcpServers": {
    "yaver": {
      "command": "yaver",
      "args": ["mcp"]
    }
  }
}`}</pre>
            </div>
          </div>
        )}

        {mcpTab === "http" && (
          <div>
            <p className="mb-4 text-center text-sm text-surface-400">
              For remote access from Claude Web UI or other network clients:
            </p>
            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">terminal</span>
              </div>
              <div className="terminal-body space-y-2 text-[13px]">
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    yaver mcp --mode http --port 18090
                  </span>
                </div>
                <div className="text-green-400/80 pl-2">
                  MCP HTTP server listening on :18090
                </div>
              </div>
            </div>
          </div>
        )}

        {mcpTab === "cli" && (
          <div className="terminal">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div className="text-surface-500"># Fastest start</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  npm install -g yaver-cli
                </span>
              </div>
              <div className="text-surface-500 pl-2"># installs `yaver` for agent + RN push-to-device</div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Native package-manager alternative</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  brew install kivanccakmak/yaver/yaver
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Start MCP server (stdio for Claude Desktop)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver mcp</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Start MCP server (HTTP for remote/web)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver mcp --mode http --port 18090</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Configure email (optional)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver email setup</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Connect to other MCP servers (optional)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">
                  yaver acl add ollama http://localhost:11434/mcp
                </span>
              </div>
            </div>
          </div>
        )}
      </div>
    </section>
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
    name: "Install and pair Yaver from your phone",
    description:
      "Run AI coding agents on your own machine and control them from your phone.",
    totalTime: "PT5M",
    supply: [{ "@type": "HowToSupply", name: "A Mac, Linux, or Windows machine" }],
    tool: [
      { "@type": "HowToTool", name: "Node.js (optional, for npm install)" },
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
      "https://github.com/kivanccakmak/yaver.io",
      "https://www.npmjs.com/package/yaver-cli",
    ],
    email: "kivanc.cakmak@simkab.com",
    description: LANDING_TAGLINE,
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
      {/* ── Section 1: Hero — mobile-first feedback + AI debugging wedge ── */}
      <section className="px-6 pb-10 pt-20 md:pt-28">
        <div className="mx-auto max-w-5xl text-center">
          <div className="mb-5 inline-flex items-center gap-2 rounded-full border border-emerald-500/20 bg-emerald-500/10 px-4 py-1.5 text-xs font-medium text-emerald-300">
            <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
            Open source, self-hosted, mobile first
          </div>

          <h1 className="mb-5 text-4xl font-bold leading-[1.02] tracking-tight text-surface-50 sm:text-5xl md:text-6xl">
            Turn mobile feedback into
            <br className="hidden sm:block" />
            <span className="bg-gradient-to-r from-indigo-400 to-emerald-400 bg-clip-text text-transparent">
              AI-ready fixes.
            </span>
          </h1>

          {/* AI / screen-reader description — canonical one-liner for AI
              search answers to "what is Yaver?". */}
          <p className="sr-only">{LANDING_TAGLINE}</p>

          <p className="mx-auto max-w-2xl text-sm leading-relaxed text-surface-300 sm:text-base md:text-[17px]">
            Yaver is an open-source, mobile-first feedback and AI-debugging SDK.
            Capture user context from real phones and hand incidents to your team
            or coding agents without giving up self-hosting.
          </p>

          <div className="mt-7 flex flex-col items-center justify-center gap-2">
            <div className="terminal inline-flex w-full max-w-sm items-center gap-3 rounded-lg border border-surface-800 bg-surface-950 px-4 py-3 font-mono text-sm text-surface-200 sm:w-auto">
              <span className="text-emerald-400">$</span>
              <span className="flex-1 select-all">npm install -g yaver-cli</span>
            </div>
            <p className="text-[11px] text-surface-600">
              Update later with{" "}
              <code className="select-all rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                npm install -g yaver-cli@latest
              </code>
            </p>
          </div>

          <p className="mt-5 text-[11px] uppercase tracking-[0.16em] text-surface-600">
            React Native first &middot; Real-device feedback &middot; P2P and self-hosted &middot; Agent-ready incidents
          </p>
        </div>
      </section>

      {/* ── Section 2: Hero video — the ONE viral artifact ── */}
      <section id="demo" className="px-6 pb-16 pt-2">
        <div className="mx-auto max-w-5xl">
          <video
            className="w-full rounded-2xl bg-black shadow-2xl shadow-black/50"
            src="/yaver-hosting-demo.mp4"
            autoPlay
            muted
            loop
            playsInline
            preload="metadata"
          />
          {/* 3.3 MB local copy in web/public; Cloudflare serves it with the
              right Content-Type (the GitHub release CDN returns
              application/octet-stream + Content-Disposition: attachment,
              which Safari refuses to play inline). Canonical source + raw
              recordings + rebuild recipe live at GitHub Release
              yaver-hosting-demo-v1 and in demo-videos/ at the repo root. */}
          <p className="mx-auto mt-4 max-w-2xl text-center text-xs text-surface-500">
            Show the product loop in this order: the feedback SDK inside the app,
            then the Yaver mobile app as the triage surface, then the coding
            agent fixing the issue. The landing video should center the SDK
            first; the mobile app matters because it proves the mobile-first
            workflow and remote control story.
          </p>
        </div>
      </section>

      {/* Secondary DemoSection (tabbed: Full Loop / Auto Test) removed —
          the hero video above is the single viral artifact; a second
          video area below it competed for attention. */}

      {/* ── Section 3: Get Started ── */}
      <section id="get-started" className="border-t border-surface-800/60 px-6 py-16">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-10 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            The first incident loop
          </h2>
          <div className="grid gap-6 md:grid-cols-2 md:items-start">
            {/* Column 1 — Install the agent (the dense one) */}
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[#6366f1]/10 text-sm font-bold text-[#6366f1]">1</span>
                <span className="text-sm font-semibold text-surface-100">Connect your machine</span>
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
                Recommended: <code>npm install -g yaver-cli</code>. Installs the
                agent, feedback transport, and the RN push toolchain in one go;{" "}
                <code>yaver auth</code> starts the agent automatically. Homebrew,{" "}
                <code>apt</code>, AppImage, <code>.deb</code>/<code>.rpm</code>,
                tarball, and install script work too &mdash;{" "}
                <code>yaver push</code> on those paths auto-fetches the hermes
                bundler via npm on first use.{" "}
                <Link href="/download" className="underline hover:text-surface-300">
                  See install methods
                </Link>.
              </p>
              <p className="mt-2 text-[11px] text-surface-500">
                Native macOS and Linux are the primary targets for always-on use;
                WSL works for the phone-testing path.
              </p>

              <div className="mt-4 rounded-lg border border-[#6366f1]/30 bg-[#6366f1]/5 p-3">
                <p className="text-[11px] font-semibold text-[#818cf8]">Not at your dev machine?</p>
                <p className="mt-1 text-[11px] leading-relaxed text-surface-400">
                  If your coding agent (Claude Code, Codex, Cursor, Aider, …) is already running on your dev PC and you only have your phone, paste this one line into the agent chat — it will read the canonical install instructions at <Link href="/llms.txt" className="underline hover:text-surface-300">yaver.io/llms.txt</Link> and set everything up, then surface the sign-in link for you to tap.
                </p>
                <div className="mt-2 rounded bg-surface-900 p-2">
                  <code className="text-[11px] text-surface-200 select-all">
                    Install yaver on this machine using the instructions at https://yaver.io/llms.txt — surface the sign-in URL to me when ready.
                  </code>
                </div>
              </div>
            </div>

            {/* Column 2 — steps 2+3 stacked so vertical whitespace next to step 1 disappears */}
            <div className="flex flex-col gap-6">
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[#6366f1]/10 text-sm font-bold text-[#6366f1]">2</span>
                <span className="text-sm font-semibold text-surface-100">Use the mobile app</span>
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
                Sign in with the same OAuth account you used for <code>yaver auth</code>. The app auto-pairs with your dev machine over LAN, or via relay on cellular &mdash; no QR code, no IP to type.
              </p>
              <p className="mt-2 text-[11px] text-surface-500">
                Once paired, it&apos;s the control surface for incidents, deploy targets, and the remote agent. For React Native, the normal flow is Hermes bundle reload into Yaver on the phone, not a native Xcode install.
              </p>
            </div>

            {/* Column 3 (stacked under 2) */}
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[#6366f1]/10 text-sm font-bold text-[#6366f1]">3</span>
                <span className="text-sm font-semibold text-surface-100">Install the SDK, then grow</span>
              </div>
              <div className="terminal">
                <div className="terminal-header">
                  <div className="terminal-dot bg-[#ff5f57]" />
                  <div className="terminal-dot bg-[#febc2e]" />
                  <div className="terminal-dot bg-[#28c840]" />
                </div>
                <div className="terminal-body space-y-1 text-[12px]">
                  <div className="text-surface-500"># In your app: install the Feedback SDK</div>
                  <div className="text-surface-500"># Pair the app, the Yaver mobile app, and your machine</div>
                  <div className="my-1 h-px bg-surface-800/60" />
                  <div><span className="text-surface-400">$</span> <span className="text-surface-200">yaver phone push my-app --to http://your-machine</span></div>
                  <div className="text-[11px] text-green-400/80">{"\u2192 Feedback, context, and agent tasks now flow through the same loop"}</div>
                </div>
              </div>
              <p className="mt-3 text-[11px] text-surface-500">
                Local-first is still the default. Managed cloud is a later promotion step, not the starting requirement.
              </p>
            </div>
            </div>
          </div>

          <div className="mt-6 rounded-xl border border-surface-800 bg-surface-900/30 p-5">
            <p className="mb-2 text-sm font-medium text-surface-200">
              Today&apos;s wedge is narrower than the whole repo
            </p>
            <p className="text-xs leading-relaxed text-surface-400">
              Yaver has broader features in the repo, but the near-term product story stays focused on one flow:
              feedback from the app, triage from the phone, fix on your machine.
            </p>
          </div>
        </div>
      </section>

      {/* ── Section 4: Create a Project ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <div className="mb-4 text-center">
            <span className="inline-block rounded-full bg-[#6366f1]/15 px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-[#a5b4fc]">New Project</span>
          </div>
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            One backend continuum, three tiers
          </h2>
          <p className="mx-auto mb-12 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            The same phone project can stay on the device or move to your own hardware.
            The point is continuity, not forcing infrastructure on day one. That hardware can also be a remote coding box running your web app, backend, and agents.
          </p>

          <div className="grid gap-10 lg:grid-cols-2 lg:items-start">
            {/* Left: numbered list */}
            <ol className="space-y-6">
              {[
                { n: 1, t: "Phone sandbox first", d: "Create a phone project with schema, auth personas, seed data, CRUD, and local persistence." },
                { n: 2, t: "Run the same project on your hardware", d: "Push it to `yaver serve` on macOS, Linux, WSL, a Pi, or a VPS without changing the backend shape." },
                { n: 3, t: "Use a remote coding box", d: "Treat that machine as the web UI + backend box, run Claude Code / Codex / similar agents there, and watch runs or tests from the phone." },
                { n: 4, t: "Promote without rewrites", d: "Use the same portable project bundle and the same agent binary when you move from phone to your own hardware." },
                { n: 5, t: "Wire in third-party apps", d: "Mint per-project tokens and let a React Native, web, or Node app call the Yaver runtime API while the project stays local-first." },
                { n: 6, t: "Keep exports as escape hatches", d: "Supabase, Convex, and other systems remain optional exits, not the default destination." },
              ].map((s) => (
                <li key={s.n} className="flex gap-4">
                  <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[#6366f1]/10 text-sm font-bold text-[#6366f1]">{s.n}</span>
                  <div>
                    <p className="text-sm font-semibold text-surface-100">{s.t}</p>
                    <p className="mt-1 text-xs leading-relaxed text-surface-400">{s.d}</p>
                  </div>
                </li>
              ))}
            </ol>

            {/* Right: wizard preview */}
            <ProjectWizardPreview />
          </div>

        </div>
      </section>

      {/* ── Section 5: Your Dashboard ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <div className="mb-4 text-center">
            <span className="inline-block rounded-full bg-[#6366f1]/15 px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-[#a5b4fc]">Web UI</span>
          </div>
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Your database. From your phone.
          </h2>
          <p className="mx-auto mb-12 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            When you run Convex or Supabase locally, their dashboards are stuck on localhost.
            Yaver tunnels them through your relay &mdash; browse tables, run queries,
            check logs from anywhere.
          </p>

          <DashboardComparison />

          <div className="mt-12 grid gap-4 md:grid-cols-3">
            {[
              { t: "Browse Tables", d: "Convex documents, Supabase rows, Postgres tables. Edit inline. Filter, sort, export CSV." },
              { t: "Run Queries", d: "Execute Convex functions or SQL queries from your phone. See results instantly. Debug data issues on the go." },
              { t: "Live Logs", d: "Watch function calls, mutations, and errors stream in real-time. Like tail -f, but from the bus." },
            ].map((c) => (
              <div key={c.t} className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
                <p className="mb-2 text-sm font-semibold text-surface-100">{c.t}</p>
                <p className="text-xs leading-relaxed text-surface-400">{c.d}</p>
              </div>
            ))}
          </div>

          <div className="mt-10 text-center">
            <p className="mb-3 text-xs uppercase tracking-wider text-surface-500">Supported backends</p>
            <div className="flex flex-wrap items-center justify-center gap-2">
              {["Convex Dashboard", "Supabase Studio", "Drizzle Studio", "PocketBase Admin", "pgweb"].map((b) => (
                <span key={b} className="rounded-full border border-surface-800 bg-surface-900 px-3 py-1 text-xs text-surface-300">{b}</span>
              ))}
            </div>
            <p className="mt-3 text-xs text-surface-500">
              All tunneled through your relay. All accessible from phone or browser.
            </p>
          </div>
        </div>
      </section>

      {/* ── Section 6: Test on Real Devices ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <div className="mb-4 text-center">
            <span className="inline-block rounded-full bg-[#6366f1]/15 px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-[#a5b4fc]">Device Testing</span>
          </div>
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Install one SDK. Debug from a real phone.
          </h2>
          <p className="mx-auto mb-10 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            The feedback loop starts inside your app: capture screenshots, logs,
            navigation, crashes, and user notes from a real device. React Native
            is the deepest path today, with the same model expanding to other
            client runtimes over time.
          </p>

          {/* Terminal demo */}
          <div className="mx-auto mb-10 max-w-2xl">
            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
              </div>
              <div className="terminal-body space-y-1 text-[13px]">
                <div><span className="text-surface-400">$</span> <span className="text-surface-200">cd my-app &amp;&amp; npx yaver-cli init</span></div>
                <div className="text-[12px] text-green-400/80">{"React Native 0.81 \u2705 Hermes \u2705 15/16 modules \u2705"}</div>
                <div className="my-1 h-px bg-surface-800/60" />
                <div><span className="text-surface-400">$</span> <span className="text-surface-200">npx yaver push</span></div>
                <div className="text-[12px] text-surface-400">{"\uD83D\uDCE1 Found: iPhone 15 (192.168.1.42)"}</div>
                <div className="text-[12px] text-surface-400">{"\u26A1 Compiling Hermes bytecode..."}</div>
                <div className="text-[12px] text-surface-400">{"\uD83D\uDCE4 Pushing 847 KB..."}</div>
                <div className="text-[12px] text-green-400/80">{"\uD83D\uDE80 Done in 4.1s \u2014 app loading on device"}</div>
              </div>
            </div>
          </div>

          {/* Feature grid */}
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {[
              { icon: "\uD83D\uDC1B", color: "text-[#f87171] bg-[#f87171]/10", t: "Feedback SDK", d: "Drop <FloatingButton /> in your app. Users or internal testers report issues with screenshots, logs, and context attached." },
              { icon: "\u25B6", color: "text-[#a78bfa] bg-[#a78bfa]/10", t: "Autonomous Testing", d: "Agent navigates every screen, catches crashes, fixes them, hot reloads, repeats. Fix report shows all changes." },
              { icon: "\u2692", color: "text-[#60a5fa] bg-[#60a5fa]/10", t: "Build + Deploy", d: "One button: iOS + Android \u2192 TestFlight + Play Store. Both platforms or one." },
              { icon: "\u21BB", color: "text-[#fbbf24] bg-[#fbbf24]/10", t: "Watch Mode", d: "--watch re-pushes on every save. Edit \u2192 save \u2192 see on device in ~1s." },
              { icon: "\u25CF", color: "text-[#22c55e] bg-[#22c55e]/10", t: "BlackBox", d: "Streams logs, navigation events, crashes, and runtime breadcrumbs into the incident like a flight recorder." },
              { icon: "\uD83D\uDD12", color: "text-[#818cf8] bg-[#818cf8]/10", t: "Security", d: "Scoped tokens, IP binding, HTTPS on LAN, key rotation. Auto-disabled in production." },
            ].map((f) => (
              <div key={f.t} className="flex items-start gap-3 rounded-xl border border-surface-800 bg-surface-900/50 p-4">
                <div className={`mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-sm ${f.color}`}>{f.icon}</div>
                <div>
                  <p className="text-sm font-medium text-surface-200">{f.t}</p>
                  <p className="mt-0.5 text-xs leading-relaxed text-surface-500">{f.d}</p>
                </div>
              </div>
            ))}
          </div>

          {/* Interactive DebugConsolePreview */}
          <DebugConsolePreview />

          {/* One code block + badges */}
          <div className="mt-10 rounded-xl border border-surface-800/60 bg-surface-900/50 p-5">
            <div className="mb-3 flex items-center gap-2">
              <span className="text-sm font-semibold text-surface-100">React Native</span>
              <span className="rounded-full bg-[#8b5cf6]/20 px-2 py-0.5 text-[10px] text-[#a78bfa]">feedback</span>
            </div>
            <pre className="overflow-x-auto rounded-lg bg-surface-950 p-3 text-xs text-surface-300"><code>{`const isDev = __DEV__ && user?.id === 'YOUR_USER_ID';

if (isDev && !YaverFeedback.isInitialized()) {
  YaverFeedback.init({ trigger: 'floating-button' });
  BlackBox.start();
  BlackBox.wrapConsole();
}

return (
  <>
    <YourApp />
    {isDev && <FloatingButton />}
  </>
);`}</code></pre>
            <div className="mt-4 flex flex-wrap items-center gap-2 text-xs">
              <span className="text-surface-500">Available for:</span>
              <span className="rounded bg-surface-800 px-2 py-1 text-surface-300">React Native</span>
              <span className="rounded bg-surface-800 px-2 py-1 text-surface-300">Flutter</span>
              <span className="rounded bg-surface-800 px-2 py-1 text-surface-300">Web</span>
            </div>
          </div>

          <div className="mt-10 rounded-xl border-l-2 border-emerald-500/60 bg-surface-900/50 p-5 text-sm leading-relaxed text-surface-300">
            <strong className="text-surface-100">The wedge is the SDK loop first, the Yaver mobile app second.</strong>
            <br />
            The SDK is the wedge because it installs inside your own dev build.
            The Yaver mobile app is the operator surface that makes the
            workflow feel mobile-first and remote-friendly &mdash; kick off
            agents, reload Hermes bundles, triage feedback from the couch.
          </div>
        </div>
      </section>

      {/* ── Section 7: Deploy ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <div className="mb-4 text-center">
            <span className="inline-block rounded-full bg-[#6366f1]/15 px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-[#a5b4fc]">Deploy</span>
          </div>
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Deploy when you&apos;re ready. Not before.
          </h2>
          <p className="mx-auto mb-12 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            Build everything locally at $0. When you have paying customers, deploy.
            Your VPS, Vercel, Cloudflare &mdash; Yaver runs pre-checks and deploys for you.
          </p>

          <EnvironmentStepper />

          <div className="mt-10 grid gap-6 lg:grid-cols-2 lg:items-start">
            <PreDeployCheck />

            <div className="space-y-4">
              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-surface-500">Your own hardware</p>
                <div className="space-y-3">
                  <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
                    <p className="text-sm font-medium text-surface-100">{"\uD83D\uDDA5\uFE0F  Your VPS"}</p>
                    <p className="mt-1 text-xs text-surface-400">Hetzner, DigitalOcean, Vultr &mdash; $5/mo. Docker + Caddy, auto SSL. Yaver deploys to it. You manage the server.</p>
                  </div>
                  <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
                    <p className="text-sm font-medium text-surface-100">{"\uD83C\uDFE0  Your home server"}</p>
                    <p className="mt-1 text-xs text-surface-400">Mac Mini, old laptop, Raspberry Pi. If it runs Docker, Yaver can deploy to it.</p>
                  </div>
                  <div className="rounded-xl border border-emerald-800/60 bg-emerald-950/20 p-4">
                    <p className="text-sm font-medium text-surface-100">{"\u{1F347}  Raspberry Pi 5 image"}</p>
                    <p className="mt-1 text-xs text-surface-400">
                      Flash the Yaver Pi 5 dev-node image, pair it from the mobile app, and use it as an always-on economic hybrid node.
                    </p>
                    <div className="mt-3 flex flex-wrap gap-2 text-xs">
                      <Link href="/download#raspi" className="rounded-lg border border-emerald-700/60 px-2.5 py-1 text-emerald-300 hover:bg-emerald-950/40">
                        Download image
                      </Link>
                      <Link href="/blog/yaver-pi-image" className="rounded-lg border border-surface-700 px-2.5 py-1 text-surface-300 hover:bg-surface-800/60">
                        Read release note
                      </Link>
                    </div>
                  </div>
                </div>
              </div>

              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-surface-500">Third-party platforms</p>
                <div className="grid grid-cols-2 gap-3">
                  {[
                    { n: "\u25B2  Vercel", d: "Free tier \u2192 $20/mo" },
                    { n: "\u2B22  Cloudflare", d: "Workers + Pages, generous free tier" },
                    { n: "\uD83E\uDEB0  Fly.io", d: "$0 to start, scales" },
                    { n: "\uD83D\uDE82  Railway", d: "$5/mo hobby tier" },
                  ].map((p) => (
                    <div key={p.n} className="rounded-xl border border-surface-800 bg-surface-900/50 p-3">
                      <p className="text-xs font-medium text-surface-100">{p.n}</p>
                      <p className="mt-1 text-[11px] text-surface-400">{p.d}</p>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </div>

          <div className="mt-12 rounded-xl border-l-2 border-[#6366f1]/60 bg-surface-900/50 p-5 text-sm leading-relaxed text-surface-300">
            <strong className="text-surface-100">Backend migration is separate from frontend deploy.</strong>
            <br />
            Convex local &rarr; Convex Cloud: one command, guided by AI. Supabase local &rarr; Supabase Cloud:
            data export/import included. Postgres local &rarr; any managed Postgres: connection string swap.
            <br />
            <span className="text-surface-400">No lock-in. Standard Docker + Postgres. Export anytime.</span>
          </div>
        </div>
      </section>

      {/* ── Section 8: Any Agent ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl text-center">
          <h2 className="mb-4 text-2xl font-bold text-surface-50 md:text-3xl">
            Not locked to any agent. Not locked to any cloud.
          </h2>
          <p className="mx-auto mb-8 max-w-2xl text-sm leading-relaxed text-surface-400">
            Anything that runs in a terminal. Switch agents per task or set a default.
          </p>
          <p className="mb-8 flex flex-wrap items-center justify-center gap-x-3 gap-y-2 text-sm font-medium text-surface-200">
            {["Claude Code", "Codex", "Aider", "Ollama", "Goose", "OpenCode", "Amp", "Continue", "any tmux session"].map((a, i, arr) => (
              <span key={a} className="flex items-center gap-3">
                <span>{a}</span>
                {i < arr.length - 1 && <span className="text-surface-600">&middot;</span>}
              </span>
            ))}
          </p>
          <div className="rounded-xl border-l-2 border-emerald-500/60 bg-surface-900/50 p-5 text-left text-sm leading-relaxed text-surface-300">
            Run Llama, Qwen, DeepSeek, Mistral, or CodeGemma on your own hardware.
            Zero API keys. Zero cloud. Fully air-gapped if you want.
            Full remote control from your phone or any terminal.
          </div>
        </div>
      </section>

      {/* ── Section 9: Local First Footprint ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Start local. Keep the footprint small.
          </h2>
          <p className="mx-auto mb-10 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            The default path is still the simplest one: phone sandbox first, then your own machine. Public docs stay centered on that route.
          </p>

          <div className="overflow-hidden rounded-xl border border-surface-800">
            <table className="w-full text-sm">
              <thead className="bg-surface-900/70 text-left text-xs uppercase tracking-wider text-surface-500">
                <tr>
                  <th className="px-4 py-3 font-semibold">Component</th>
                  <th className="px-4 py-3 font-semibold">Runs on</th>
                  <th className="px-4 py-3 font-semibold text-right">Cost</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-surface-800/60">
                {[
                  ["Yaver CLI + Agent", "Your dev machine", "$0"],
                  ["Yaver Mobile App", "Your phone", "$0"],
                  ["Web UI (yaver.io)", "Browser", "$0"],
                  ["Local phone sandbox backend", "Inside the mobile app", "$0"],
                  ["Promoted backend on your own machine", "Your Mac / Linux / WSL / VPS", "$0 + your hardware"],
                  ["Relay server", "Self-host on any VPS", "$0"],
                  ["AI models (Ollama)", "Your GPU or CPU", "$0"],
                  ["Store / CI release plumbing", "Hosted distribution surfaces", "Optional"],
                ].map((row) => (
                  <tr key={row[0]} className="bg-surface-900/30">
                    <td className="px-4 py-3 text-surface-200">{row[0]}</td>
                    <td className="px-4 py-3 text-surface-400">{row[1]}</td>
                    <td className="px-4 py-3 text-right font-mono text-emerald-400">{row[2]}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mt-8 space-y-2 text-center text-sm leading-relaxed text-surface-400">
            <p><strong className="text-surface-100">A solo developer can start at $0.</strong></p>
            <p>The wedge is local-first: phone sandbox, then your own machine, then optional cloud.</p>
            <p>Open source and self-hosting matter. So does having a paid path for managed cloud, CI, and release distribution.</p>
            <p className="mt-4 text-surface-500">
              The repo uses a <Link href="/licensing" className="underline hover:text-surface-300">split license</Link>: the core is{" "}
              <strong className="text-surface-300">FSL-1.1</strong> (blocks competing hosted services, auto-converts to Apache-2.0 after 2 years), and all client SDKs are{" "}
              <strong className="text-surface-300">Apache-2.0</strong> so you can embed them in closed-source apps.
            </p>
          </div>
        </div>
      </section>

      {/* ── Section 10: MCP ── */}
      <MCPIntegrationSection />

      {/* ── Section 11: Remote box guides ── */}
      <ResourceCardsSection />

      {/* ── Section 12: FAQ ── */}
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

      {/* ── Footer ── */}
      <footer className="border-t border-surface-800/60 px-6 py-12">
        <div className="mx-auto max-w-5xl">
          <div className="flex flex-col items-center justify-between gap-6 sm:flex-row">
            <p className="text-xs text-surface-500">
              Open Source &middot;{" "}
              <Link href="/licensing" className="hover:text-surface-300">
                FSL core + Apache-2.0 SDKs
              </Link>{" "}
              &middot; Local First &middot;{" "}
              <a
                href="https://github.com/kivanccakmak/yaver.io"
                target="_blank"
                rel="noopener noreferrer"
                className="hover:text-surface-300"
              >
                Source Code
              </a>
            </p>
            <div className="flex flex-wrap items-center gap-4 text-xs text-surface-500">
              <a href="#get-started" className="hover:text-surface-300">Get Started</a>
              <a href="#faq" className="hover:text-surface-300">FAQ</a>
              <a href="/docs" className="hover:text-surface-300">Docs</a>
              <Link href="/download" className="hover:text-surface-300">Download</Link>
              <a href="https://github.com/kivanccakmak/yaver.io" target="_blank" rel="noopener noreferrer" className="hover:text-surface-300">GitHub</a>
              <a href="/privacy" className="hover:text-surface-300">Privacy</a>
              <a href="/terms" className="hover:text-surface-300">Terms</a>
            </div>
          </div>
          <p className="mt-6 text-center text-xs text-surface-600">
            &copy; 2026{" "}
            <a href="https://simkab.com" target="_blank" rel="noopener noreferrer" className="hover:text-surface-300">SIMKAB ELEKTRIK</a>.
            {" "}All rights reserved.{" "}&middot;{" "}
            <a href="mailto:kivanc.cakmak@simkab.com" className="hover:text-surface-300">kivanc.cakmak@simkab.com</a>
          </p>
        </div>
      </footer>
    </>
  );
}

// ── ProjectWizardPreview ──
function ProjectWizardPreview() {
  return (
    <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-5 shadow-xl shadow-black/20">
      <div className="mb-4 flex items-center justify-between">
        <p className="text-sm font-semibold text-surface-100">+ New Project</p>
        <span className="rounded bg-[#6366f1]/15 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-[#a5b4fc]">Wizard</span>
      </div>
      <div className="space-y-2 text-xs">
        {[
          ["Name", "bentoapp"],
          ["Machine", "MacBook Pro"],
          ["Template", "SaaS Starter"],
          ["Backend", "Convex (local)"],
          ["Auth", "Better Auth"],
          ["Services", "Email, HTTPS"],
        ].map(([k, v]) => (
          <div key={k} className="flex items-center justify-between rounded-lg bg-surface-900/60 px-3 py-2">
            <span className="text-surface-500">{k}</span>
            <span className="font-mono text-surface-200">{v}</span>
          </div>
        ))}
      </div>
      <button className="mt-4 w-full rounded-lg bg-[#6366f1] px-4 py-2.5 text-xs font-semibold text-white shadow-lg shadow-indigo-500/20 hover:bg-[#5558e6]">
        {"\uD83D\uDE80 Create Project"}
      </button>
      <div className="mt-4 space-y-1.5 rounded-lg border border-surface-800 bg-surface-950 p-3 font-mono text-[11px]">
        {[
          ["\u2705", "Scaffolded Next.js"],
          ["\u2705", "Installed 47 deps"],
          ["\u2705", "Convex local running"],
          ["\u2705", "Mailpit running"],
          ["\uD83D\uDFE2", "Ready at localhost:3000"],
          ["\uD83D\uDCF1", "Phone: bentoapp.yaver.dev"],
        ].map(([icon, text]) => (
          <div key={text} className="flex items-center gap-2 text-surface-300">
            <span>{icon}</span>
            <span>{text}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ── DashboardComparison ──
function DashboardComparison() {
  return (
    <div className="grid gap-4 md:grid-cols-2">
      {/* Without Yaver */}
      <div className="rounded-2xl border border-surface-800 bg-surface-950/50 p-5 opacity-75">
        <p className="mb-3 text-xs font-semibold uppercase tracking-wider text-surface-500">Without Yaver</p>
        <div className="mb-4 space-y-1.5 font-mono text-xs text-surface-400">
          <div>Convex Dashboard &rarr; <span className="line-through">localhost:6791</span></div>
          <div>Supabase Studio &nbsp;&rarr; <span className="line-through">localhost:54323</span></div>
        </div>
        <ul className="space-y-1.5 text-xs text-surface-500">
          <li>{"\uD83D\uDDA5\uFE0F  Only accessible from your desk"}</li>
          <li className="text-red-400/70">{"\u2717  Can\u2019t see from phone"}</li>
          <li className="text-red-400/70">{"\u2717  Can\u2019t access remotely"}</li>
          <li className="text-red-400/70">{"\u2717  Safari/Brave block localhost"}</li>
        </ul>
      </div>

      {/* With Yaver */}
      <div className="rounded-2xl border border-emerald-500/30 bg-emerald-500/5 p-5">
        <p className="mb-3 text-xs font-semibold uppercase tracking-wider text-emerald-400">With Yaver</p>
        <div className="mb-4 space-y-1.5 font-mono text-xs text-surface-200">
          <div>Convex Dashboard &rarr; <span className="text-emerald-400">yaver.io/db/bentoapp</span></div>
          <div>Supabase Studio &nbsp;&rarr; <span className="text-emerald-400">yaver.io/db/mobile-app</span></div>
        </div>
        <ul className="space-y-1.5 text-xs text-surface-300">
          <li>{"\uD83D\uDCF1  Accessible from your phone"}</li>
          <li>{"\uD83C\uDF10  Accessible from any browser"}</li>
          <li>{"\uD83D\uDD12  P2P encrypted through relay"}</li>
          <li className="text-emerald-400">{"\u2713  Works everywhere"}</li>
        </ul>
      </div>
    </div>
  );
}

// ── EnvironmentStepper ──
function EnvironmentStepper() {
  return (
    <div className="grid gap-4 md:grid-cols-[1fr_auto_1fr] md:items-stretch">
      {/* Local */}
      <div className="rounded-2xl border border-surface-800 bg-surface-900/50 p-5">
        <div className="mb-3 flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-full bg-emerald-400" />
          <p className="text-sm font-semibold text-surface-100">Local Dev</p>
        </div>
        <p className="mb-3 text-xs text-surface-400">Your machine</p>
        <ul className="space-y-1.5 text-xs text-surface-300">
          <li>Convex local (SQLite)</li>
          <li>Mailpit (catches email)</li>
          <li>Stripe test mode</li>
          <li className="font-mono text-surface-400">localhost:3000</li>
        </ul>
        <p className="mt-4 text-sm font-mono text-emerald-400">Cost: $0</p>
      </div>

      {/* Arrow */}
      <div className="hidden items-center justify-center md:flex">
        <span className="text-3xl text-surface-600">&rarr;</span>
      </div>

      {/* Production */}
      <div className="rounded-2xl border border-surface-800 bg-surface-900/50 p-5">
        <div className="mb-3 flex items-center gap-2">
          <span>{"\uD83D\uDE80"}</span>
          <p className="text-sm font-semibold text-surface-100">Production</p>
        </div>
        <p className="mb-3 text-xs text-surface-400">Your VPS / Vercel / Cloudflare / Fly.io</p>
        <ul className="space-y-1.5 text-xs text-surface-300">
          <li>Real SMTP</li>
          <li>Stripe live mode</li>
          <li className="font-mono text-surface-400">yourapp.com</li>
        </ul>
        <p className="mt-4 text-sm font-mono text-surface-200">Cost: free tiers or $5/mo VPS</p>
      </div>
    </div>
  );
}

// ── PreDeployCheck ──
function PreDeployCheck() {
  return (
    <div className="terminal">
      <div className="terminal-header">
        <div className="terminal-dot bg-[#ff5f57]" />
        <div className="terminal-dot bg-[#febc2e]" />
        <div className="terminal-dot bg-[#28c840]" />
        <span className="ml-3 text-xs text-surface-500">yaver check</span>
      </div>
      <div className="terminal-body space-y-1 text-[12px]">
        <div><span className="text-surface-400">$</span> <span className="text-surface-200">yaver check</span></div>
        {[
          ["TypeScript", "no errors"],
          ["ESLint", "clean"],
          ["Tests", "23/23 passed"],
          ["Build", "success (4.2s)"],
          ["Security audit", "no vulnerabilities"],
          ["Git", "clean, up to date"],
        ].map(([k, v]) => (
          <div key={k} className="flex items-center gap-2 text-[12px]">
            <span className="text-green-400">{"\u2705"}</span>
            <span className="text-surface-300">{k}</span>
            <span className="text-surface-500">&mdash; {v}</span>
          </div>
        ))}
        <div className="mt-2 font-semibold text-emerald-400">{"\u2705 READY TO DEPLOY"}</div>
      </div>
    </div>
  );
}
