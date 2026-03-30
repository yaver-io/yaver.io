"use client";

import Link from "next/link";
import { useRef, useState } from "react";

const CONVEX_SITE_URL =
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://shocking-echidna-394.eu-west-1.convex.site";

function WaitlistButton({ plan }: { plan: string }) {
  const [email, setEmail] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [loading, setLoading] = useState(false);
  const [showInput, setShowInput] = useState(false);

  const handleSubmit = async () => {
    if (!email.includes("@")) return;
    setLoading(true);
    try {
      await fetch(`${CONVEX_SITE_URL}/dev/log`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          source: "web", level: "info", tag: "waitlist",
          message: `Waitlist signup: ${plan}`,
          data: JSON.stringify({ email, plan, timestamp: new Date().toISOString() }),
        }),
      });
    } catch { /* best effort */ }
    setSubmitted(true);
    setLoading(false);
  };

  if (submitted) {
    return (
      <div className="block w-full rounded-lg border border-[#22c55e]/40 bg-[#22c55e]/10 py-2.5 text-center text-sm font-medium text-[#22c55e]">
        You&apos;re on the list!
      </div>
    );
  }

  if (!showInput) {
    return (
      <button
        onClick={() => setShowInput(true)}
        className="block w-full rounded-lg border border-surface-700 bg-surface-800/50 py-2.5 text-center text-sm font-medium text-surface-300 transition-colors hover:bg-surface-800 hover:text-surface-100"
      >
        Join Waitlist
      </button>
    );
  }

  return (
    <div className="space-y-2">
      <input
        type="email" placeholder="your@email.com" value={email}
        onChange={(e) => setEmail(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") handleSubmit();
          if (e.key === "Escape") setShowInput(false);
        }}
        onBlur={() => { if (!email) setShowInput(false); }}
        className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 placeholder:text-surface-600 focus:border-[#6366f1] focus:outline-none"
        autoFocus
      />
      <div className="flex gap-2">
        <button
          onClick={handleSubmit} disabled={loading || !email.includes("@")}
          className="flex-1 rounded-lg bg-[#6366f1] px-4 py-2 text-sm font-medium text-white hover:bg-[#5558e6] disabled:opacity-50"
        >
          {loading ? "..." : "Go"}
        </button>
        <button
          onClick={() => setShowInput(false)}
          className="rounded-lg border border-surface-700 px-3 py-2 text-sm text-surface-500 hover:text-surface-200"
        >
          {"\u2715"}
        </button>
      </div>
    </div>
  );
}

function DebugConsolePreview() {
  const [panelOpen, setPanelOpen] = useState(true);
  const [btnPos, setBtnPos] = useState({ x: 0, y: 0 });
  const [dragging, setDragging] = useState(false);
  const dragRef = useRef<{ startX: number; startY: number; origX: number; origY: number } | null>(null);
  const [activeTab, setActiveTab] = useState("Home");
  const [selectedCategory, setSelectedCategory] = useState("All");
  const [selectedProduct, setSelectedProduct] = useState<string | null>(null);
  const [searchText, setSearchText] = useState("");
  const [cartItems, setCartItems] = useState<string[]>(["Leather Bag", "Smart Watch"]);
  const [input, setInput] = useState("");
  const outputRef = useRef<HTMLDivElement>(null);
  const allProducts = [
    { name: "Running Shoe", price: "$129", color: "from-indigo-500/20 to-blue-500/20", cat: "Shoes", crashed: true },
    { name: "Leather Bag", price: "$89", color: "from-rose-500/20 to-pink-500/20", cat: "Bags", crashed: false },
    { name: "Smart Watch", price: "$249", color: "from-emerald-500/20 to-teal-500/20", cat: "Watches", crashed: false },
    { name: "Sunglasses", price: "$65", color: "from-amber-500/20 to-orange-500/20", cat: "Accessories", crashed: false },
  ];
  const filteredProducts = selectedCategory === "All" ? allProducts : allProducts.filter(p => p.cat === selectedCategory);
  const homeMessages = [
    { from: "agent", text: "\u26A0 error caught by SDK:" },
    { from: "agent", text: 'TypeError: Cannot read "price"' },
    { from: "agent", text: "at ProductCard (ProductList.tsx:34)" },
    { from: "agent", text: "screen: Home > Running Shoe" },
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
      <div className="flex flex-col items-center gap-8 lg:flex-row lg:items-start lg:gap-10">
        {/* Left — text */}
        <div className="flex-1 lg:pt-8">
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
        <div className="shrink-0">
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
                    <h4 className="text-base font-bold text-gray-900">Acme Store</h4>
                    <div className="flex items-center gap-2.5">
                      <button className="flex h-8 w-8 items-center justify-center rounded-full bg-gray-100" onClick={() => setActiveTab("Search")}>
                        <svg className="h-4 w-4 text-gray-500" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" /></svg>
                      </button>
                      <div className="h-8 w-8 rounded-full bg-gradient-to-br from-indigo-400 to-purple-500" />
                    </div>
                  </div>
                  {/* Category pills */}
                  <div className="mb-3 flex gap-1.5">
                    {["All", "Shoes", "Bags", "Watches"].map((c) => (
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
                          { from: "agent", text: "[Home] tap Leather Bag...", delay: 500, action: () => setSelectedProduct("Leather Bag") },
                          { from: "agent", text: "[Home] detail loaded. back.", delay: 700, action: () => setSelectedProduct(null) },
                          { from: "agent", text: "[Home] tap Running Shoe...", delay: 500 },
                          { from: "agent", text: "\u26A0 TypeError: price is null", delay: 800 },
                          { from: "agent", text: "fix: ProductCard.tsx:34", delay: 600 },
                          { from: "agent", text: "  price?.toFixed(2) ?? 'N/A'", delay: 400 },
                          { from: "agent", text: "hot reload \u2713 crash gone", delay: 800 },
                          // Search tab
                          { from: "agent", text: "[Search] navigating...", delay: 500, action: () => setActiveTab("Search") },
                          { from: "agent", text: "[Search] typing 'shoes'...", delay: 500, action: () => setSearchText("shoes") },
                          { from: "agent", text: "[Search] 1 result found. ok", delay: 700 },
                          { from: "agent", text: "[Search] clear. typing 'bag'...", delay: 500, action: () => setSearchText("bag") },
                          { from: "agent", text: "[Search] 1 result. tapping...", delay: 600, action: () => { setSearchText(""); setActiveTab("Home"); setSelectedProduct("Leather Bag"); } },
                          { from: "agent", text: "[Search] detail ok. back.", delay: 600, action: () => { setSelectedProduct(null); setActiveTab("Search"); setSearchText(""); } },
                          // Cart tab
                          { from: "agent", text: "[Cart] navigating...", delay: 500, action: () => setActiveTab("Cart") },
                          { from: "agent", text: "[Cart] 2 items. total $338.", delay: 600 },
                          { from: "agent", text: "[Cart] removing item...", delay: 500, action: () => setCartItems(["Smart Watch"]) },
                          { from: "agent", text: "[Cart] 1 item. total $249.", delay: 600 },
                          { from: "agent", text: "[Cart] tap checkout... ok", delay: 500, action: () => setCartItems(["Leather Bag", "Smart Watch"]) },
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
                          setCartItems(["Leather Bag", "Smart Watch"]);
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

const DEMO_TABS = [
  {
    id: "remote",
    label: "Remote Task",
    icon: "\u{1F4F1}",
    desc: "Send a task from your phone. Agent writes code on your machine.",
    video: "/demo-remote.mp4",
  },
  {
    id: "feedback",
    label: "Bug Fix Loop",
    icon: "\uD83D\uDC1B",
    desc: "Report a bug from the running app. Agent fixes it. Hot reload.",
    video: "/demo-feedback.mp4",
  },
  {
    id: "autotest",
    label: "Auto Test",
    icon: "\u25B6",
    desc: "Agent drives the app, finds bugs, fixes them, and verifies.",
    video: "/demo-autotest.mp4",
  },
];

function DemoVideo({ src }: { src: string }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [paused, setPaused] = useState(false);
  const toggle = () => {
    const v = videoRef.current;
    if (!v) return;
    if (v.paused) { v.play(); setPaused(false); }
    else { v.pause(); setPaused(true); }
  };
  return (
    <div className="group relative cursor-pointer" onClick={toggle}>
      <video
        ref={videoRef}
        className="w-full bg-[#050508]"
        autoPlay
        muted
        loop
        playsInline
        src={src}
      />
      {paused && (
        <div className="absolute inset-0 flex items-center justify-center">
          <div className="flex h-14 w-14 items-center justify-center rounded-full bg-white/20 backdrop-blur-sm">
            <svg className="ml-1 h-6 w-6 text-white" fill="currentColor" viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg>
          </div>
        </div>
      )}
    </div>
  );
}

function DemoSection() {
  const [activeDemo, setActiveDemo] = useState("remote");
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
            <span className="flex-1 text-center text-xs text-surface-500">
              {demo.id === "remote" && "Phone + Terminal — split screen"}
              {demo.id === "feedback" && "Running app + Feedback SDK + Terminal"}
              {demo.id === "autotest" && "Agent driving app autonomously"}
            </span>
          </div>

          {/* Video */}
          {demo.id === "remote" ? (
            <DemoVideo src="/demo.mp4" />
          ) : (
            <div className="flex aspect-video items-center justify-center bg-[#050508]">
              <div className="text-center">
                <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full border border-surface-700 bg-surface-900">
                  <svg className="h-7 w-7 text-surface-400" fill="currentColor" viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg>
                </div>
                <p className="mb-1 text-sm font-medium text-surface-300">Coming soon</p>
              </div>
            </div>
          )}
        </div>

        {/* Quick summary of all three */}
        <div className="mt-8 grid grid-cols-1 gap-3 sm:grid-cols-3">
          {DEMO_TABS.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setActiveDemo(tab.id)}
              className={`rounded-xl border p-4 text-left transition-all ${
                activeDemo === tab.id
                  ? "border-[#6366f1]/40 bg-[#6366f1]/5"
                  : "border-surface-800 bg-surface-900/30 hover:border-surface-700"
              }`}
            >
              <div className="mb-2 flex items-center gap-2">
                <span className="text-lg">{tab.icon}</span>
                <span className={`text-sm font-semibold ${activeDemo === tab.id ? "text-surface-100" : "text-surface-300"}`}>{tab.label}</span>
              </div>
              <p className="text-xs text-surface-500">{tab.desc}</p>
            </button>
          ))}
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
              <div className="text-surface-500"># Install</div>
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
  return (
    <>
      {/* ── Section 1: Hero ── */}
      <section className="px-6 pb-20 pt-20 md:pt-28">
        <div className="mx-auto max-w-4xl text-center">
          <div className="mb-6 inline-flex items-center rounded-full border border-surface-700 bg-surface-900 px-4 py-1.5 text-xs text-surface-400">
            <span className="mr-2 inline-block h-1.5 w-1.5 rounded-full bg-green-500/70" />
            MIT Licensed &middot; Free Forever
          </div>
          <h1 className="mb-6 text-4xl font-bold tracking-tight text-surface-50 sm:text-5xl md:text-6xl">
            Any agent. Your hardware.
            <br />
            Your phone.
          </h1>
          <p className="mx-auto max-w-2xl text-base leading-relaxed text-surface-400 md:text-lg">
            Ollama, Aider, Goose, Codex, or Claude Code &mdash; run them on your own machine, control them from anywhere.
            <br />
            Zero cloud. Zero API keys. Free forever.
          </p>
          <div className="mt-8 flex flex-col items-center justify-center gap-4 sm:flex-row">
            <a
              href="https://github.com/kivanccakmak/yaver.io"
              target="_blank"
              rel="noopener noreferrer"
              className="btn-primary inline-flex items-center gap-2 px-8 py-3 text-sm font-medium"
            >
              <svg className="h-4 w-4" fill="currentColor" viewBox="0 0 24 24"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.405.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/></svg>
              Star on GitHub
            </a>
            <a href="#waitlist" className="btn-secondary px-8 py-3 text-sm font-medium">
              Join Waitlist
            </a>
          </div>
          <div className="mx-auto mt-6 max-w-md">
            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
              </div>
              <div className="terminal-body space-y-1.5 text-[13px]">
                <div><span className="text-surface-400">$</span>{" "}<span className="text-surface-200 select-all">brew install kivanccakmak/yaver/yaver</span></div>
                <div><span className="text-surface-400">$</span>{" "}<span className="text-surface-200">yaver auth</span></div>
                <div><span className="text-surface-400">$</span>{" "}<span className="text-surface-200">yaver serve</span></div>
              </div>
            </div>
          </div>

          {/* App download row */}
          <div className="mx-auto mt-10 max-w-2xl">
            <div className="flex flex-col items-center gap-3 sm:flex-row sm:justify-center">
              <a href="https://testflight.apple.com/join/yaver" target="_blank" rel="noopener noreferrer"
                className="inline-flex items-center gap-2 rounded-lg bg-surface-800 px-4 py-2.5 text-xs font-medium text-surface-300 transition-colors hover:bg-surface-700">
                <svg className="h-4 w-4 shrink-0 text-surface-400" fill="currentColor" viewBox="0 0 24 24"><path d="M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.8-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.40 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z"/></svg>
                App Store &mdash; iOS
              </a>
              <a href="https://play.google.com/store/apps/details?id=io.yaver.mobile" target="_blank" rel="noopener noreferrer"
                className="inline-flex items-center gap-2 rounded-lg bg-surface-800 px-4 py-2.5 text-xs font-medium text-surface-300 transition-colors hover:bg-surface-700">
                <svg className="h-4 w-4 shrink-0 text-surface-400" fill="currentColor" viewBox="0 0 24 24"><path d="M17.523 2.238l-1.931 3.334c1.88.907 3.261 2.565 3.713 4.608H4.694c.452-2.043 1.833-3.701 3.714-4.608L6.477 2.238a.357.357 0 01.13-.487.357.357 0 01.487.13l1.962 3.389A8.97 8.97 0 0112 4.749c1.07 0 2.088.188 3.039.521l1.962-3.389a.357.357 0 01.487-.13.357.357 0 01.13.487h-.095zM9.5 7.5a.75.75 0 100-1.5.75.75 0 000 1.5zm5 0a.75.75 0 100-1.5.75.75 0 000 1.5zM4.5 11.68h15c.276 0 .5.224.5.5v7.5c0 1.401-1.119 2.5-2.5 2.5h-11C5.119 22.18 4 21.061 4 19.68v-7.5c0-.276.224-.5.5-.5z"/></svg>
                Google Play
              </a>
            </div>
            <p className="mt-4 text-xs text-surface-500">
              Sign in with Apple, Google, or Microsoft. Your machine appears automatically on your phone.
            </p>
          </div>
        </div>
      </section>

      {/* ── Section 2: Demo ── */}
      <DemoSection />

      {/* ── Section 3: Wait, it's free? ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl text-center">
          <h2 className="mb-4 text-2xl font-bold text-surface-50 md:text-3xl">
            Free forever. No catch.
          </h2>
          <p className="mb-12 text-sm text-surface-400">
            Self-host everything. Your code never leaves your machine.
          </p>

          <div className="mx-auto max-w-2xl overflow-hidden rounded-xl border border-surface-800">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-surface-800 bg-surface-900/50">
                  <th className="px-5 py-3 font-medium text-surface-300">Component</th>
                  <th className="px-5 py-3 font-medium text-surface-300">Runs on</th>
                  <th className="px-5 py-3 text-right font-medium text-surface-300">Cost</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-surface-800/60">
                <tr><td className="px-5 py-3 text-surface-200">Yaver CLI</td><td className="px-5 py-3 text-surface-400">Your dev machine</td><td className="px-5 py-3 text-right font-semibold text-green-400">$0</td></tr>
                <tr><td className="px-5 py-3 text-surface-200">Yaver App</td><td className="px-5 py-3 text-surface-400">Your phone</td><td className="px-5 py-3 text-right font-semibold text-green-400">$0</td></tr>
                <tr><td className="px-5 py-3 text-surface-200">Relay server</td><td className="px-5 py-3 text-surface-400">Your own VPS</td><td className="px-5 py-3 text-right font-semibold text-green-400">$0</td></tr>
                <tr><td className="px-5 py-3 text-surface-200">LLM (via Ollama)</td><td className="px-5 py-3 text-surface-400">Your GPU or CPU</td><td className="px-5 py-3 text-right font-semibold text-green-400">$0</td></tr>
              </tbody>
            </table>
          </div>

          <p className="mt-6 text-xs text-surface-500">
            No API keys required for local models. No telemetry. No vendor lock-in.
            <br />
            MIT licensed &mdash; fork it, run your own instance of everything.
          </p>
        </div>
      </section>

      {/* ── Built for Solo Founders ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Built for solo founders.
          </h2>
          <p className="mb-12 text-center text-sm text-surface-400">
            One person. One machine. No team. No cloud bill. Just the agent doing the work.
          </p>

          <div className="grid gap-4 md:grid-cols-3">
            {[
              {
                icon: "\u{1F9D1}\u200D\u{1F4BB}",
                label: "No team needed",
                copy: "Autonomous test loop, hot reload, one-tap deploy \u2014 all the things a team would do, running on your laptop.",
              },
              {
                icon: "\u{1F4B8}",
                label: "No cloud bill",
                copy: "Your hardware runs the LLM. Your VPS runs the relay. Total cost: what you already pay.",
              },
              {
                icon: "\u{1F319}",
                label: "Works while you sleep",
                copy: "Queue tasks from your phone, walk away. Your machine runs them. You review the diff in the morning.",
              },
            ].map((item) => (
              <div
                key={item.label}
                className="rounded-xl border border-surface-800 bg-surface-900/50 p-6"
              >
                <div className="mb-3 text-2xl">{item.icon}</div>
                <p className="text-sm font-semibold text-surface-200">{item.label}</p>
                <p className="mt-2 text-xs leading-relaxed text-surface-400">{item.copy}</p>
              </div>
            ))}
          </div>

          <div className="mt-10 text-center">
            <a
              href="https://github.com/kivanccakmak/yaver.io"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 rounded-lg border border-surface-700 bg-surface-800/50 px-5 py-2.5 text-sm font-medium text-surface-300 transition-colors hover:bg-surface-800 hover:text-surface-100"
            >
              <svg className="h-4 w-4" fill="currentColor" viewBox="0 0 24 24"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.405.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/></svg>
              Star it if you&apos;re building alone
            </a>
          </div>
        </div>
      </section>

      {/* ── Built for Monorepos ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Full-stack from your phone.
          </h2>
          <p className="mb-12 text-center text-sm text-surface-400">
            Yaver shines when your web frontend, mobile app, backend, and infrastructure all live in one repo.
          </p>

          <div className="grid gap-4 md:grid-cols-2">
            {[
              {
                icon: "\u21BB",
                color: "text-[#fbbf24] bg-[#fbbf24]/10",
                label: "Native hot reload on your phone",
                copy: "React Native apps run inside Yaver with full native access &mdash; camera, BLE, GPS, sensors. Your CLI pushes code, the app reloads instantly. Works over WiFi, 4G, or any network through the relay.",
              },
              {
                icon: "\uD83E\uDDEA",
                color: "text-[#a78bfa] bg-[#a78bfa]/10",
                label: "Test across your stack",
                copy: "Run backend + frontend + mobile tests in one tap. The agent figures out the test runner for each subdirectory and reports results together.",
              },
              {
                icon: "\uD83D\uDE80",
                color: "text-[#22c55e] bg-[#22c55e]/10",
                label: "Deploy everything from one place",
                copy: "Ship web/ to Vercel, mobile/ to TestFlight and Play Store, backend/ to Convex &mdash; all from one project card on your phone.",
              },
              {
                icon: "\u2699",
                color: "text-[#60a5fa] bg-[#60a5fa]/10",
                label: "Zero config detection",
                copy: "The agent reads package.json, pubspec.yaml, next.config.ts, vite.config.ts in each subdirectory. No manifest files, no project setup.",
              },
              {
                icon: "\uD83C\uDFAF",
                color: "text-[#f87171] bg-[#f87171]/10",
                label: "Perfect for Convex + Expo + Vercel",
                copy: "The most common solo-founder stack. One repo, three deploy targets, all managed from your phone while you walk the dog.",
              },
              {
                icon: "\uD83D\uDCE6",
                color: "text-surface-300 bg-surface-800",
                label: "Works with any structure",
                copy: "Monorepo with Turborepo, Nx, or just plain directories. Yaver scans recursively and finds every deployable piece.",
              },
            ].map((item) => (
              <div
                key={item.label}
                className="flex items-start gap-3 rounded-xl border border-surface-800 bg-surface-900/50 p-5"
              >
                <div className={`mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-sm ${item.color}`}>{item.icon}</div>
                <div>
                  <p className="text-sm font-semibold text-surface-200">{item.label}</p>
                  <p className="mt-1 text-xs leading-relaxed text-surface-400" dangerouslySetInnerHTML={{ __html: item.copy }} />
                </div>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ── Section: Browse your apps. Take action. ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            All your projects. One tap to ship.
          </h2>
          <p className="mx-auto mb-12 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            Your phone discovers every project on your dev machine over P2P. AI detects the framework, finds every deployable target, and gives you one-tap actions &mdash; hot reload, deploy, build.
          </p>

          {/* Flow visualization */}
          <div className="mx-auto mb-12 max-w-3xl">
            <div className="flex flex-wrap items-center justify-center gap-2 text-sm">
              {[
                { label: "Phone connects P2P", icon: "\uD83D\uDCF1" },
                { label: "Agent scans repos", icon: "\uD83D\uDD0D" },
                { label: "AI detects targets", icon: "\u2699" },
                { label: "You tap an action", icon: "\u25B6" },
                { label: "Deployed", icon: "\u2713" },
              ].map((step, i) => (
                <div key={step.label} className="flex items-center gap-2">
                  <div className="flex items-center gap-1.5 rounded-full border border-surface-700 bg-surface-900 px-3 py-1.5">
                    <span>{step.icon}</span>
                    <span className="text-surface-300">{step.label}</span>
                  </div>
                  {i < 4 && <span className="text-surface-600">&rarr;</span>}
                </div>
              ))}
            </div>
          </div>

          {/* What the agent detects */}
          <div className="mb-10 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {[
              { framework: "React Native / Expo", actions: "Native Hot Reload inside Yaver, Build iOS, Build Android", platforms: "Camera, BLE, GPS — full native access", icon: "\uD83D\uDCF1", color: "text-[#a78bfa] bg-[#a78bfa]/10" },
              { framework: "Next.js", actions: "Dev Server, Deploy", platforms: "Vercel", icon: "\u25B2", color: "text-surface-300 bg-surface-800" },
              { framework: "Vite", actions: "Dev Server, Deploy", platforms: "Vercel", icon: "\u26A1", color: "text-[#fbbf24] bg-[#fbbf24]/10" },
              { framework: "Convex", actions: "Deploy Backend", platforms: "Convex Cloud", icon: "\uD83E\uDDE0", color: "text-[#f87171] bg-[#f87171]/10" },
              { framework: "Supabase", actions: "Deploy Backend", platforms: "Supabase Cloud", icon: "\u26A1", color: "text-[#22c55e] bg-[#22c55e]/10" },
              { framework: "Docker", actions: "Run Container", platforms: "Any server", icon: "\uD83D\uDC33", color: "text-[#60a5fa] bg-[#60a5fa]/10" },
            ].map((f) => (
              <div key={f.framework} className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
                <div className="mb-2 flex items-center gap-2">
                  <div className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-sm ${f.color}`}>{f.icon}</div>
                  <span className="text-sm font-semibold text-surface-100">{f.framework}</span>
                </div>
                <p className="text-xs text-surface-400">{f.actions}</p>
                <p className="mt-1 text-[11px] text-surface-500">&rarr; {f.platforms}</p>
              </div>
            ))}
          </div>

          {/* Monorepo callout */}
          <div className="mx-auto max-w-3xl rounded-xl border border-[#6366f1]/20 bg-[#6366f1]/5 p-5">
            <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
              <div className="flex-1">
                <p className="text-sm font-medium text-surface-100">Monorepo-aware</p>
                <p className="mt-1 text-xs leading-relaxed text-surface-400">
                  One project, multiple targets. The agent scans subdirectories and finds every deployable piece &mdash; <code className="rounded bg-surface-800 px-1 py-0.5 text-[11px] text-surface-300">mobile/</code> gets Hot Reload + TestFlight, <code className="rounded bg-surface-800 px-1 py-0.5 text-[11px] text-surface-300">web/</code> gets Vercel deploy, <code className="rounded bg-surface-800 px-1 py-0.5 text-[11px] text-surface-300">backend/</code> gets Convex deploy. All from one project card on your phone.
                </p>
              </div>
              <div className="shrink-0 rounded-lg border border-surface-800 bg-surface-900/80 p-3">
                <div className="space-y-1.5 font-mono text-[11px]">
                  <div className="text-surface-500">my-app/</div>
                  <div className="flex items-center gap-2 pl-3"><span className="text-[#a78bfa]">mobile/</span> <span className="rounded bg-[#a78bfa]/10 px-1.5 py-0.5 text-[9px] text-[#a78bfa]">expo</span> <span className="rounded bg-[#22c55e]/10 px-1.5 py-0.5 text-[9px] text-[#22c55e]">testflight</span></div>
                  <div className="flex items-center gap-2 pl-3"><span className="text-surface-300">web/</span> <span className="rounded bg-surface-700 px-1.5 py-0.5 text-[9px] text-surface-400">nextjs</span> <span className="rounded bg-surface-700 px-1.5 py-0.5 text-[9px] text-surface-400">vercel</span></div>
                  <div className="flex items-center gap-2 pl-3"><span className="text-[#f87171]">backend/</span> <span className="rounded bg-[#f87171]/10 px-1.5 py-0.5 text-[9px] text-[#f87171]">convex</span></div>
                </div>
              </div>
            </div>
          </div>

          {/* Features grid */}
          <div className="mt-10 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <p className="text-sm font-semibold text-surface-200">Fuzzy search + tags</p>
              <p className="mt-1 text-xs text-surface-400">
                Search by name, path, or framework. Filter by tags: expo, nextjs, flutter, vercel, convex, docker. Projects discovered automatically &mdash; no config files.
              </p>
            </div>
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <p className="text-sm font-semibold text-surface-200">One-tap quick actions</p>
              <p className="mt-1 text-xs text-surface-400">
                Running app shows action buttons: Ship It (version bump + build + deploy + changelog), Polish UI (design pass + hot reload), Fix All Bugs (test suite + fix + reload).
              </p>
            </div>
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <p className="text-sm font-semibold text-surface-200">Clone repos from your phone</p>
              <p className="mt-1 text-xs text-surface-400">
                Yaver auto-detects GitHub and GitLab credentials on your dev machine. Browse your repos from the app and clone to a headless server &mdash; no SSH, no manual git setup.
              </p>
            </div>
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <p className="text-sm font-semibold text-surface-200">Zero config</p>
              <p className="mt-1 text-xs text-surface-400">
                No manifest. No project file. The agent reads package.json, pubspec.yaml, go.mod, Cargo.toml, Dockerfile &mdash; and figures out the rest.
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* ── Section 4: 60-Second Install ── */}
      <section id="features" className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-3xl">
          <h2 className="mb-12 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Get started
          </h2>

          <div className="space-y-6">
            {[
              { step: 1, label: "Install CLI", cmd: "brew install kivanccakmak/yaver/yaver", output: null },
              { step: 2, label: "Auth", cmd: "yaver auth", output: null },
              { step: 3, label: "Start", cmd: "yaver serve", output: "Ready. Waiting for tasks..." },
              { step: 4, label: "Connect your agent", cmd: "yaver mcp setup claude", output: "# Works with: aider, codex, ollama, goose, opencode, amp, or any tmux session" },
            ].map((s) => (
              <div key={s.step} className="flex items-start gap-4">
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[#6366f1]/10 text-sm font-bold text-[#6366f1]">
                  {s.step}
                </span>
                <div className="flex-1">
                  <p className="mb-2 text-sm font-medium text-surface-200">{s.label}</p>
                  <div className="terminal">
                    <div className="terminal-header">
                      <div className="terminal-dot bg-[#ff5f57]" />
                      <div className="terminal-dot bg-[#febc2e]" />
                      <div className="terminal-dot bg-[#28c840]" />
                    </div>
                    <div className="terminal-body text-[13px]">
                      <div>
                        <span className="text-surface-400">$</span>{" "}
                        <span className="text-surface-200 select-all">{s.cmd}</span>
                      </div>
                      {s.output && (
                        <div className="mt-1 pl-2 text-green-400/80">{s.output}</div>
                      )}
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>

          {/* All install methods */}
          <div className="mt-12">
            <p className="mb-4 text-center text-xs font-semibold uppercase tracking-wider text-surface-500">All installation methods</p>
            <div className="overflow-hidden rounded-xl border border-surface-800">
              <table className="w-full text-left text-sm">
                <tbody className="divide-y divide-surface-800/60">
                  {[
                    { method: "Homebrew", cmd: "brew install kivanccakmak/yaver/yaver", os: "macOS / Linux" },
                    { method: "AUR", cmd: "yay -S yaver", os: "Arch Linux" },
                    { method: "apt", cmd: "sudo apt install yaver", os: "Debian / Ubuntu" },
                    { method: "RPM", cmd: "sudo rpm -i yaver_latest_x86_64.rpm", os: "Fedora / RHEL" },
                    { method: "Nix", cmd: "nix run github:kivanccakmak/yaver.io", os: "NixOS" },
                    { method: "Docker", cmd: "docker run --rm kivanccakmak/yaver-cli version", os: "Any" },
                    { method: "curl", cmd: "curl -fsSL https://yaver.io/install.sh | sh", os: "macOS / Linux" },
                  ].map((row) => (
                    <tr key={row.method}>
                      <td className="whitespace-nowrap px-4 py-2.5 text-xs font-medium text-surface-200">{row.method}</td>
                      <td className="px-4 py-2.5"><code className="text-[11px] text-surface-400 select-all">{row.cmd}</code></td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-[11px] text-surface-500">{row.os}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="mt-3 text-center text-xs text-surface-500">
              Or download binaries from{" "}
              <a href="https://github.com/kivanccakmak/yaver.io/releases" target="_blank" rel="noopener noreferrer" className="text-surface-300 underline hover:text-surface-100">GitHub Releases</a>.
              {" "}Mobile app on the App Store and Google Play.
            </p>
          </div>
        </div>
      </section>

      {/* ── Section 4b: Always-on (systemd) ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-3xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Set it up once. It runs forever.
          </h2>
          <p className="mb-12 text-center text-sm text-surface-400">
            Install as a system service on any Linux machine. Survives reboots, auto-updates itself.
          </p>

          <div className="space-y-6">
            {[
              { step: 1, label: "Sign in (once — token persists across reboots)", cmd: "yaver auth", output: null },
              { step: 2, label: "Install as systemd service", cmd: "yaver serve --install-systemd", output: "Yaver agent installed as systemd user service.\nThe agent starts automatically on login and survives reboots." },
              { step: 3, label: "That's it. Manage with:", cmd: "systemctl --user status yaver    # status\njournalctl --user -u yaver -f    # live logs\nsystemctl --user restart yaver   # restart", output: null },
            ].map((s) => (
              <div key={s.step} className="flex items-start gap-4">
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[#22c55e]/10 text-sm font-bold text-[#22c55e]">
                  {s.step}
                </span>
                <div className="flex-1">
                  <p className="mb-2 text-sm font-medium text-surface-200">{s.label}</p>
                  <div className="terminal">
                    <div className="terminal-header">
                      <div className="terminal-dot bg-[#ff5f57]" />
                      <div className="terminal-dot bg-[#febc2e]" />
                      <div className="terminal-dot bg-[#28c840]" />
                    </div>
                    <div className="terminal-body text-[13px]">
                      {s.cmd.split("\n").map((line, i) => (
                        <div key={i}>
                          <span className="text-surface-400">$</span>{" "}
                          <span className="text-surface-200">{line}</span>
                        </div>
                      ))}
                      {s.output && (
                        <div className="mt-1 pl-2 text-green-400/80 whitespace-pre-line">{s.output}</div>
                      )}
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>

          <div className="mt-10 grid gap-4 md:grid-cols-3">
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <p className="text-sm font-semibold text-surface-200">Auth persists</p>
              <p className="mt-1 text-xs text-surface-400">
                OAuth sign-in (Google/Apple/Microsoft) saves a long-lived token to ~/.yaver/config.json. No re-auth after reboot.
              </p>
            </div>
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <p className="text-sm font-semibold text-surface-200">Auto-updates</p>
              <p className="mt-1 text-xs text-surface-400">
                Checks GitHub releases every 6 hours. Downloads new binary, restarts automatically via systemd.
              </p>
            </div>
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <p className="text-sm font-semibold text-surface-200">Wake-on-LAN</p>
              <p className="mt-1 text-xs text-surface-400">
                Enable{" "}
                <a href="https://wiki.archlinux.org/title/Wake-on-LAN" target="_blank" rel="noopener noreferrer" className="text-surface-300 underline hover:text-surface-100">Wake-on-LAN</a>
                {" "}or{" "}
                <a href="https://support.apple.com/guide/mac-help/wake-your-mac-mh11775/mac" target="_blank" rel="noopener noreferrer" className="text-surface-300 underline hover:text-surface-100">Power Nap (macOS)</a>
                {" "}to wake your machine remotely from your phone.
              </p>
            </div>
          </div>

          <p className="mt-6 text-center text-xs text-surface-500">
            Works on any Linux machine — Mac Mini, Raspberry Pi, cloud VPS, or your desktop.
            {" "}macOS users: <code className="text-surface-400">yaver serve</code> auto-forks to background. Use the{" "}
            <a href="https://github.com/kivanccakmak/yaver.io" target="_blank" rel="noopener noreferrer" className="text-surface-300 underline hover:text-surface-100">desktop installer</a>
            {" "}for login-item auto-start.
          </p>
        </div>
      </section>

      {/* ── Section 5: Works with every agent ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Not locked to any agent. Not locked to any cloud.
          </h2>
          <p className="mb-12 text-center text-sm text-surface-400">
            Anything that runs in a terminal. Switch agents per task or set a default.
          </p>

          <div className="mb-8 flex flex-wrap items-center justify-center gap-x-6 gap-y-3 text-sm font-medium text-surface-300">
            {["Ollama", "Aider", "Goose", "OpenCode", "Amp", "Continue", "OpenAI Codex", "Claude Code", "Any tmux session"].map((name) => (
              <span key={name}>{name}</span>
            ))}
          </div>

          <div className="rounded-xl border border-green-500/20 bg-green-500/5 p-6">
            <p className="text-sm font-medium text-surface-100">
              Run Llama, Qwen, DeepSeek, Mistral, or CodeGemma on your own hardware.
            </p>
            <p className="mt-2 text-sm text-green-400">
              Zero API keys. Zero cloud. Fully air-gapped if you want. Full remote control from your phone or any terminal.
            </p>
          </div>

        </div>
      </section>

      {/* ── Section 6: Feedback SDK ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            The AI debug loop that doesn&apos;t exist anywhere else.
          </h2>
          <p className="mx-auto mb-6 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            Shake your phone. The AI sees your screen, writes the fix, and hot reloads the app. No laptop. No Slack thread. No Loom video. No waiting.
          </p>

          {/* Visual step sequence */}
          <div className="mx-auto mb-12 max-w-3xl">
            <div className="flex flex-wrap items-center justify-center gap-2 text-sm">
              {[
                { label: "Shake", icon: "\uD83D\uDCF1" },
                { label: "Screenshot", icon: "\uD83D\uDCF8" },
                { label: "P2P to agent", icon: "\u2192" },
                { label: "Fix", icon: "\uD83D\uDD27" },
                { label: "Hot reload", icon: "\u21BB" },
                { label: "Done", icon: "\u2713" },
              ].map((step, i) => (
                <div key={step.label} className="flex items-center gap-2">
                  <div className="flex items-center gap-1.5 rounded-full border border-surface-700 bg-surface-900 px-3 py-1.5">
                    <span>{step.icon}</span>
                    <span className="text-surface-300">{step.label}</span>
                  </div>
                  {i < 5 && <span className="text-surface-600">&rarr;</span>}
                </div>
              ))}
            </div>
          </div>


          <div className="mb-10 grid grid-cols-1 gap-3 sm:grid-cols-2">
            {[
              { icon: ">", color: "text-surface-300 bg-surface-800", label: "Message back and forth", desc: "Send tasks, see agent responses in real-time" },
              { icon: "\u21BB", color: "text-[#fbbf24] bg-[#fbbf24]/10", label: "Native Hot Reload", desc: "React Native apps run inside Yaver with camera, BLE, GPS — hot reload over WiFi or 4G" },
              { icon: "\u2692", color: "text-[#60a5fa] bg-[#60a5fa]/10", label: "Build + Deploy", desc: "One button: iOS + Android to TestFlight + Play Store" },
              { icon: "\uD83D\uDC1B", color: "text-[#f87171] bg-[#f87171]/10", label: "Bug Report", desc: "Auto-screenshot (SDK overlay hidden), AI analyzes, pushes fix" },
              { icon: "\u25B6", color: "text-[#a78bfa] bg-[#a78bfa]/10", label: "Autonomous Test Loop", desc: "Agent reads codebase, navigates app on device/emulator, catches crashes, writes fixes, hot reloads, and repeats — no human in the loop" },
              { icon: "\u25CF", color: "text-[#22c55e] bg-[#22c55e]/10", label: "BlackBox", desc: "Streams logs, navigation, crashes like a flight recorder" },
              { icon: "\u2713", color: "text-[#34d399] bg-[#34d399]/10", label: "Fix Report", desc: "All fixes listed with diffs — staged, never committed — review and accept" },
              { icon: "\uD83D\uDD12", color: "text-[#818cf8] bg-[#818cf8]/10", label: "6-layer security", desc: "Scoped tokens, IP binding, HTTPS on LAN, rotation, device alerts" },
              { icon: "\u2717", color: "text-surface-500 bg-surface-800", label: "Auto-disabled in production", desc: "Your users never see it" },
            ].map((f) => (
              <div key={f.label} className="flex items-start gap-3 rounded-xl border border-surface-800 bg-surface-900/50 p-4">
                <div className={`mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-lg text-sm ${f.color}`}>{f.icon}</div>
                <div>
                  <p className="text-sm font-medium text-surface-200">{f.label}</p>
                  <p className="text-xs text-surface-500">{f.desc}</p>
                </div>
              </div>
            ))}
          </div>

          {/* DebugConsolePreview component */}
          <DebugConsolePreview />

          {/* Feedback SDK code blocks */}
          <div className="mt-10 rounded-xl border border-surface-800/60 bg-surface-900/50 p-5">
            <div className="mb-3 flex items-center gap-2">
              <span className="text-sm font-semibold text-surface-100">React Native</span>
              <span className="rounded-full bg-[#8b5cf6]/20 px-2 py-0.5 text-[10px] text-[#a78bfa]">feedback</span>
            </div>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto"><code>{`const isDev = __DEV__ && user?.id === 'YOUR_USER_ID';

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
          </div>

          <div className="mt-4 rounded-xl border border-surface-800/60 bg-surface-900/50 p-5">
            <div className="mb-3 flex items-center gap-2">
              <span className="text-sm font-semibold text-surface-100">Flutter</span>
              <span className="rounded-full bg-[#8b5cf6]/20 px-2 py-0.5 text-[10px] text-[#a78bfa]">feedback</span>
            </div>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto"><code>{`final isDev = kDebugMode && user?.id == 'YOUR_USER_ID';

if (isDev) {
  YaverFeedback.init(trigger: FeedbackTrigger.floatingButton);
  BlackBox.start();
  BlackBox.wrapPrint();
}

return MaterialApp(
  builder: (context, child) => Stack(children: [
    child!,
    if (isDev) const FloatingButton(),
  ]),
);`}</code></pre>
          </div>

          <div className="mt-4 rounded-xl border border-surface-800/60 bg-surface-900/50 p-5">
            <div className="mb-3 flex items-center gap-2">
              <span className="text-sm font-semibold text-surface-100">Web</span>
              <span className="rounded-full bg-[#8b5cf6]/20 px-2 py-0.5 text-[10px] text-[#a78bfa]">feedback</span>
            </div>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto"><code>{`import { YaverFeedback, BlackBox } from '@yaver/feedback-web';

if (isDev) {
  YaverFeedback.init({ trigger: 'floating-button' });
  BlackBox.start();
  BlackBox.wrapConsole();
}`}</code></pre>
          </div>

          <div className="mt-6 flex flex-wrap items-center justify-center gap-2 text-xs text-surface-500">
            <code className="rounded bg-surface-800 px-2 py-1 text-surface-300">npm install @yaver/feedback-react-native</code>
            <span>&middot;</span>
            <code className="rounded bg-surface-800 px-2 py-1 text-surface-300">flutter pub add yaver_feedback</code>
            <span>&middot;</span>
            <code className="rounded bg-surface-800 px-2 py-1 text-surface-300">npm install @yaver/feedback-web</code>
          </div>
          <p className="mt-3 text-center text-xs text-surface-500">
            Available for: React Native &middot; Flutter &middot; Web
          </p>
        </div>
      </section>

      {/* ── Section 7: How connections work ── */}
      <section id="how-it-works" className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Three layers. Fastest path wins. Always.
          </h2>
          <p className="mb-16 text-center text-sm text-surface-400">
            Three layers, tried in order. The fastest available path wins.
          </p>

          {/* Connection waterfall */}
          <div className="mx-auto max-w-3xl space-y-4">
            {/* Layer 1 */}
            <div className="relative rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <div className="flex items-start gap-4">
                <div className="flex flex-col items-center">
                  <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-green-500/10 text-sm font-bold text-green-400">
                    1
                  </span>
                  <div className="mt-2 h-full w-px bg-surface-800" />
                </div>
                <div className="flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="text-sm font-semibold text-surface-50">LAN Discovery</h3>
                    <span className="rounded-full bg-green-500/10 px-2 py-0.5 text-[11px] font-medium text-green-400">~5ms</span>
                    <span className="rounded-full bg-surface-800 px-2 py-0.5 text-[11px] font-medium text-surface-400">UDP broadcast</span>
                  </div>
                  <p className="mt-2 text-sm leading-relaxed text-surface-400">
                    UDP broadcast on same WiFi &mdash; zero config. Auth-aware: only your devices match, even on shared networks.
                  </p>
                </div>
              </div>
            </div>

            {/* Layer 2 */}
            <div className="relative rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <div className="flex items-start gap-4">
                <div className="flex flex-col items-center">
                  <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-blue-500/10 text-sm font-bold text-blue-400">
                    2
                  </span>
                  <div className="mt-2 h-full w-px bg-surface-800" />
                </div>
                <div className="flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="text-sm font-semibold text-surface-50">Direct Connection</h3>
                    <span className="rounded-full bg-blue-500/10 px-2 py-0.5 text-[11px] font-medium text-blue-400">~5ms</span>
                    <span className="rounded-full bg-surface-800 px-2 py-0.5 text-[11px] font-medium text-surface-400">HTTP</span>
                  </div>
                  <p className="mt-2 text-sm leading-relaxed text-surface-400">
                    HTTP to known IP from device registry. Works when both devices are on the same network.
                  </p>
                </div>
              </div>
            </div>

            {/* Layer 3 */}
            <div className="relative rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <div className="flex items-start gap-4">
                <div className="flex flex-col items-center">
                  <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-amber-500/10 text-sm font-bold text-amber-400">
                    3
                  </span>
                </div>
                <div className="flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="text-sm font-semibold text-surface-50">QUIC Relay</h3>
                    <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-400">~50ms</span>
                    <span className="rounded-full bg-surface-800 px-2 py-0.5 text-[11px] font-medium text-surface-400">QUIC</span>
                  </div>
                  <p className="mt-2 text-sm leading-relaxed text-surface-400">
                    NAT traversal &mdash; CLI connects outbound, no port forwarding needed. Relay is a dumb pipe.
                  </p>
                </div>
              </div>
            </div>
          </div>

          {/* Trust bullets */}
          <div className="mx-auto mt-10 max-w-3xl space-y-3">
            <div className="flex items-start gap-3">
              <span className="mt-0.5 text-green-400">&#8226;</span>
              <p className="text-sm text-surface-400">CLI connects <strong className="text-surface-200">outbound only</strong> &mdash; no port forwarding needed, no firewall changes</p>
            </div>
            <div className="flex items-start gap-3">
              <span className="mt-0.5 text-green-400">&#8226;</span>
              <p className="text-sm text-surface-400">Relay is a <strong className="text-surface-200">dumb pipe</strong> &mdash; open source, self-hostable, can&apos;t read your traffic</p>
            </div>
            <div className="flex items-start gap-3">
              <span className="mt-0.5 text-green-400">&#8226;</span>
              <p className="text-sm text-surface-400"><strong className="text-surface-200">WiFi &rarr; cellular &rarr; WiFi</strong> transitions are silent &mdash; no reconnect prompts, no dropped sessions</p>
            </div>
          </div>

          {/* Hard NAT */}
          <div className="mx-auto mt-8 max-w-3xl">
            <div className="card">
              <p className="text-sm leading-relaxed text-surface-400">
                Behind a strict firewall? Use <strong className="text-surface-200">Tailscale</strong> (WireGuard) or <strong className="text-surface-200">Cloudflare Tunnel</strong> (pure HTTPS).
                Both work as drop-in replacements for the relay.
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* ── Section 7: 473 MCP Tools ── */}
      <MCPIntegrationSection />

      {/* ── Section 8: Full Local Stack ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <div className="mb-4 text-center">
            <span className="inline-flex items-center rounded-full border border-green-500/20 bg-green-500/10 px-3 py-1 text-xs font-medium text-green-400">
              $0/month &middot; No API keys &middot; Nothing leaves your network
            </span>
          </div>
          <h2 className="mb-12 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            $0/month. No API keys. Nothing leaves your network.
          </h2>

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {[
              { name: "Ollama", role: "LLM runtime", detail: "Downloads and runs models locally" },
              { name: "GLM-4.7-Flash", role: "30B MoE model", detail: "59.2% SWE-bench Verified" },
              { name: "Aider", role: "Coding agent", detail: "Git-aware AI pair programming" },
              { name: "Yaver", role: "Mobile remote", detail: "Control it all from your phone" },
            ].map((item) => (
              <div key={item.name} className="rounded-xl border border-green-500/10 bg-green-500/5 px-4 py-4">
                <p className="text-sm font-semibold text-surface-100">{item.name}</p>
                <p className="text-xs text-green-400">{item.role}</p>
                <p className="mt-2 text-xs text-surface-400">{item.detail}</p>
              </div>
            ))}
          </div>

          <div className="mx-auto mt-8 max-w-3xl rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <p className="text-sm font-medium text-surface-200">Runs on a PC with 24 GB RAM</p>
                <p className="mt-1 text-xs text-surface-400">
                  Q4 quantization &mdash; 19 GB download, ~22 GB in memory.
                  GPU optional but faster. Apple Silicon + Linux supported.
                </p>
              </div>
              <Link href="/manuals/free-onprem" className="btn-primary shrink-0 px-6 py-2.5 text-sm text-center">
                Setup guide &amp; SWE analysis
              </Link>
            </div>
          </div>
        </div>
      </section>

      {/* ── Section 9: Real Workflows ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-12 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            How developers actually use it
          </h2>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="card">
              <h3 className="mb-2 text-sm font-semibold text-surface-100">Browse apps, deploy from the couch</h3>
              <p className="text-sm leading-relaxed text-surface-400">
                Open the Apps tab. All your projects are there &mdash; auto-discovered. Tap your app, hot reload to your phone, fix a bug with the Feedback SDK, deploy to Vercel. Laptop stays closed.
              </p>
            </div>
            <div className="card">
              <h3 className="mb-2 text-sm font-semibold text-surface-100">Ship on a commute</h3>
              <p className="text-sm leading-relaxed text-surface-400">
                Tap &ldquo;Ship It&rdquo; on the bus &mdash; version bump, build iOS + Android, upload to TestFlight + Play Store, deploy Convex backend. Review the changelog when you arrive.
              </p>
            </div>
            <div className="card">
              <h3 className="mb-2 text-sm font-semibold text-surface-100">Headless server</h3>
              <p className="text-sm leading-relaxed text-surface-400">
                <code className="rounded bg-surface-800 px-1.5 py-0.5 text-xs text-surface-300">yaver serve</code> on a Linux box or Raspberry Pi.
                Browse projects, trigger builds, deploy backends. No SSH session juggling.
              </p>
            </div>
            <div className="card">
              <h3 className="mb-2 text-sm font-semibold text-surface-100">Multi-machine</h3>
              <p className="text-sm leading-relaxed text-surface-400">
                Work laptop during the day. Home server at night.
                <code className="rounded bg-surface-800 px-1.5 py-0.5 text-xs text-surface-300">yaver session transfer</code> moves the agent context with you.
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* ── Section 10: Capabilities ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-12 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Everything you can do from your phone
          </h2>

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {[
              { name: "Browse & Deploy Apps", desc: "Auto-discover projects, one-tap deploy to Vercel, TestFlight, Play Store, Convex" },
              { name: "Native Hot Reload", desc: "Run React Native apps inside Yaver with full native access — camera, BLE, GPS. Hot reload over any network." },
              { name: "Smart Actions", desc: "AI detects frameworks — shows Ship It, Build, Deploy, Hot Reload per target" },
              { name: "Session Transfer", desc: "Move AI sessions between machines mid-task" },
              { name: "Remote Exec", desc: "Run agents on any dev machine" },
              { name: "Adopt Running Sessions", desc: "Start tmux anywhere, continue on phone" },
              { name: "Task Scheduling", desc: "Cron-like scheduling, GitHub/GitLab webhook triggers" },
              { name: "Git Operations", desc: "Status, diff, commit from mobile" },
              { name: "Notifications", desc: "Telegram, Discord, Slack" },
              { name: "CLI-to-CLI", desc: "yaver connect from any terminal, no phone needed" },
              { name: "Agent Chaining", desc: "Ollama writes, Claude Code reviews, Aider applies" },
              { name: "P2P Key Vault", desc: "API keys, SSH keys, signing certs synced phone to machine" },
            ].map((cap) => (
              <div key={cap.name} className="rounded-xl border border-surface-800 bg-surface-900/50 px-4 py-3">
                <p className="text-sm font-medium text-surface-200">{cap.name}</p>
                <p className="mt-1 text-xs text-surface-500">{cap.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ── Section 11: SDKs ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Yaver is a platform. Embed it in your own tools.
          </h2>
          <p className="mx-auto mb-16 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            SDKs for Go, Python, JavaScript/TypeScript, Flutter/Dart, and C/C++.
          </p>

          <div className="space-y-4">
            <div className="rounded-xl border border-surface-800/60 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="text-sm font-semibold text-surface-100">Go</span>
                <span className="rounded-full bg-surface-800 px-2 py-0.5 text-[10px] text-surface-400">Native</span>
              </div>
              <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto"><code>{`c := yaver.NewClient(url, token)
task, _ := c.CreateTask("Fix bug", nil)
for chunk := range c.StreamOutput(task.ID, 0) {
    fmt.Print(chunk)
}`}</code></pre>
            </div>
            <div className="rounded-xl border border-surface-800/60 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="text-sm font-semibold text-surface-100">Python</span>
                <span className="rounded-full bg-surface-800 px-2 py-0.5 text-[10px] text-surface-400">pip install</span>
              </div>
              <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto"><code>{`client = YaverClient(url, token)
task = client.create_task("Fix bug")
for chunk in client.stream_output(task["id"]):
    print(chunk, end="")`}</code></pre>
            </div>
            <div className="rounded-xl border border-surface-800/60 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="text-sm font-semibold text-surface-100">TypeScript</span>
                <span className="rounded-full bg-surface-800 px-2 py-0.5 text-[10px] text-surface-400">npm</span>
              </div>
              <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto"><code>{`const task = await c.createTask('Fix bug');
for await (const chunk of c.streamOutput(task.id)) {
  process.stdout.write(chunk);
}`}</code></pre>
            </div>
            <div className="rounded-xl border border-surface-800/60 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="text-sm font-semibold text-surface-100">Flutter / Dart</span>
                <span className="rounded-full bg-surface-800 px-2 py-0.5 text-[10px] text-surface-400">pub.dev</span>
              </div>
              <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto"><code>{`final task = await c.createTask('Fix bug');
await for (final chunk in c.streamOutput(task.id)) {
  stdout.write(chunk);
}`}</code></pre>
            </div>
          </div>

          <div className="mt-8 rounded-xl border border-surface-800/60 bg-surface-900/50 p-5">
            <div className="space-y-2">
              <div className="flex items-center gap-3">
                <span className="w-16 text-xs font-medium text-surface-400">npm</span>
                <code className="flex-1 rounded bg-surface-950 px-3 py-1.5 text-xs text-surface-300 select-all">npm install yaver-sdk</code>
              </div>
              <div className="flex items-center gap-3">
                <span className="w-16 text-xs font-medium text-surface-400">pip</span>
                <code className="flex-1 rounded bg-surface-950 px-3 py-1.5 text-xs text-surface-300 select-all">pip install yaver</code>
              </div>
              <div className="flex items-center gap-3">
                <span className="w-16 text-xs font-medium text-surface-400">Go</span>
                <code className="flex-1 rounded bg-surface-950 px-3 py-1.5 text-xs text-surface-300 select-all">go get github.com/kivanccakmak/yaver.io/sdk/go/yaver</code>
              </div>
              <div className="flex items-center gap-3">
                <span className="w-16 text-xs font-medium text-surface-400">Flutter</span>
                <code className="flex-1 rounded bg-surface-950 px-3 py-1.5 text-xs text-surface-300 select-all">flutter pub add yaver</code>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* ── Section 12: ACL — Agent Communication Layer ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Connect agents to each other.
          </h2>
          <p className="mx-auto mb-12 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            ACL (Agent Communication Layer) connects Yaver to other MCP servers &mdash; chain tools across Ollama, databases, filesystems, and any MCP-compatible service.
          </p>

          <div className="terminal">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">Agent Communication Layer</span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div className="text-surface-500"># Connect to local Ollama</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">yaver acl add ollama http://localhost:11434/mcp</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Connect to a filesystem MCP server (stdio)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">{`yaver acl add files --stdio "npx -y @modelcontextprotocol/server-filesystem /home"`}</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Connect to a remote database</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">yaver acl add mydb https://db.example.com/mcp --auth token123</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># List connected peers</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver acl list</span>
              </div>
              <div className="pl-2 text-green-400/80">{`ollama   http://localhost:11434/mcp   ● connected   3 tools`}</div>
              <div className="pl-2 text-green-400/80">{`files    stdio (npx server-fs)       ● connected   7 tools`}</div>
              <div className="pl-2 text-green-400/80">{`mydb     https://db.example.com/mcp  ● connected   4 tools`}</div>
            </div>
          </div>

          <div className="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-3">
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
              <p className="text-sm font-medium text-surface-200">Chain tools</p>
              <p className="mt-1 text-xs text-surface-500">Claude reads your code via Yaver, queries your DB via ACL, and writes the migration &mdash; one prompt.</p>
            </div>
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
              <p className="text-sm font-medium text-surface-200">Any MCP server</p>
              <p className="mt-1 text-xs text-surface-500">HTTP or stdio transport. Ollama, Supabase, filesystem, Postgres, custom servers &mdash; all work.</p>
            </div>
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-4">
              <p className="text-sm font-medium text-surface-200">MCP-accessible</p>
              <p className="mt-1 text-xs text-surface-500">ACL peers are exposed as MCP tools. Claude Desktop, Cursor, VS Code can call them through Yaver.</p>
            </div>
          </div>
        </div>
      </section>

      {/* ── Section 13: Voice ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Speak your tasks. Anywhere.
          </h2>
          <p className="mx-auto mb-12 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            Voice input works everywhere &mdash; mobile app, feedback SDK, CLI.
          </p>

          <div className="overflow-hidden rounded-xl border border-surface-800">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-surface-800 bg-surface-900/50">
                  <th className="px-4 py-3 font-medium text-surface-300">Provider</th>
                  <th className="px-4 py-3 font-medium text-surface-300">Cost</th>
                  <th className="px-4 py-3 font-medium text-surface-300">How</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-surface-800/60">
                <tr><td className="px-4 py-3 text-surface-200">Whisper tiny (on-device)</td><td className="px-4 py-3 text-green-400">Free</td><td className="px-4 py-3 text-surface-400">Runs on your phone, no internet</td></tr>
                <tr><td className="px-4 py-3 text-surface-200">OpenAI GPT-4o Mini</td><td className="px-4 py-3 text-surface-400">$0.003/min</td><td className="px-4 py-3 text-surface-400">Best accuracy for technical speech</td></tr>
                <tr><td className="px-4 py-3 text-surface-200">Deepgram Nova-2</td><td className="px-4 py-3 text-surface-400">$0.004/min</td><td className="px-4 py-3 text-surface-400">Real-time WebSocket streaming</td></tr>
                <tr><td className="px-4 py-3 text-surface-200">NVIDIA PersonaPlex 7B</td><td className="px-4 py-3 text-green-400">Free (needs GPU)</td><td className="px-4 py-3 text-surface-400">170ms full-duplex turn-taking</td></tr>
              </tbody>
            </table>
          </div>

          <p className="mt-6 text-center text-xs text-surface-500">
            Type <code className="rounded bg-surface-800 px-1">/voice</code> in <code className="rounded bg-surface-800 px-1">yaver connect</code> to use voice from any terminal.
            Voice input works everywhere &mdash; mobile app, feedback SDK, CLI.
          </p>
        </div>
      </section>

      {/* ── Section 14: Security ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Your code never leaves your machine.
            <br />
            <span className="text-surface-400">Here&apos;s how that actually works.</span>
          </h2>

          <div className="mt-12 space-y-4">
            {[
              { title: "P2P", desc: "Tasks, output, and code flow directly phone to machine. No server in the middle." },
              { title: "Transport", desc: "QUIC+TLS on relay path, HTTPS on mobile, WireGuard via Tailscale." },
              { title: "Relay", desc: "Open source dumb pipe. Self-host it. It can't read your traffic." },
              { title: "Auth backend (Convex)", desc: "Stores only: hostname, platform, IP. Never sees code or tasks." },
              { title: "LAN beacon", desc: "SHA-256 token fingerprint. Only your devices match on shared WiFi." },
              { title: "Fully local path", desc: "Ollama + Tailscale = zero third-party servers. Zero API keys. Ever." },
            ].map((item) => (
              <div key={item.title} className="flex items-start gap-4 rounded-xl border border-surface-800 bg-surface-900/50 p-4">
                <p className="w-36 shrink-0 text-sm font-semibold text-surface-200">{item.title}</p>
                <p className="text-sm leading-relaxed text-surface-400">{item.desc}</p>
              </div>
            ))}
          </div>

          {/* Command Sandbox */}
          <div className="mt-12">
            <h3 className="mb-4 text-center text-lg font-semibold text-surface-100">
              Command sandbox enabled by default
            </h3>
            <p className="mb-6 text-center text-sm text-surface-400">
              AI agents can only run what you allow. The sandbox blocks dangerous operations out of the box.
            </p>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {[
                { cat: "Filesystem destruction", ex: "rm -rf /, rm -rf ~" },
                { cat: "Encryption / ransomware", ex: "Bulk encryption of home or root" },
                { cat: "Privilege escalation", ex: "sudo, su, doas (unless allowed)" },
                { cat: "Disk manipulation", ex: "mkfs, fdisk, dd to block devices" },
                { cat: "Network exfiltration", ex: "curl|bash, piping sensitive files" },
                { cat: "System compromise", ex: "Overwriting /etc/passwd, disabling services" },
              ].map((b) => (
                <div key={b.cat} className="rounded-lg border border-surface-800/60 bg-surface-900/50 px-4 py-3">
                  <p className="text-xs font-medium text-red-400/80">{b.cat}</p>
                  <p className="mt-1 text-[11px] text-surface-500">{b.ex}</p>
                </div>
              ))}
            </div>
            <div className="mt-6 terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">~/.yaver/config.json</span>
              </div>
              <div className="terminal-body text-[12px]">
                <pre className="text-surface-300">{`{
  "sandbox": {
    "enabled": true,
    "allow_sudo": false,
    "blocked_commands": ["terraform destroy"],
    "allowed_paths": ["/home/user/projects"]
  }
}`}</pre>
              </div>
            </div>
          </div>

          <div className="mt-8 flex items-center justify-center gap-4">
            <a href="https://github.com/kivanccakmak/yaver.io" target="_blank" rel="noopener noreferrer"
              className="text-sm text-surface-300 underline underline-offset-2 hover:text-surface-100">
              Read the source &rarr;
            </a>
            <span className="text-xs text-surface-500">MIT licensed. Don&apos;t trust. Verify.</span>
          </div>
        </div>
      </section>

      {/* ── Section 15: Self-host the relay ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-12 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Run your own relay. Free. One command.
          </h2>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <div className="mb-3 flex items-center gap-2">
                <span className="rounded-full bg-green-500/10 px-2 py-0.5 text-[10px] font-medium text-green-400">recommended</span>
                <h4 className="text-sm font-semibold text-surface-100">One-command setup</h4>
              </div>
              <div className="terminal">
                <div className="terminal-header">
                  <div className="terminal-dot bg-[#ff5f57]" />
                  <div className="terminal-dot bg-[#febc2e]" />
                  <div className="terminal-dot bg-[#28c840]" />
                  <span className="ml-3 text-xs text-surface-500">your laptop</span>
                </div>
                <div className="terminal-body space-y-2 text-[13px]">
                  <div>
                    <span className="text-surface-400">$</span>{" "}
                    <span className="text-surface-200 select-all">
                      ./scripts/setup-relay.sh 1.2.3.4 relay.example.com --password secret
                    </span>
                  </div>
                  <div className="pl-2 text-green-400/80">Relay running at https://relay.example.com</div>
                </div>
              </div>
            </div>

            <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
              <h4 className="mb-3 text-sm font-semibold text-surface-100">Docker</h4>
              <div className="terminal">
                <div className="terminal-header">
                  <div className="terminal-dot bg-[#ff5f57]" />
                  <div className="terminal-dot bg-[#febc2e]" />
                  <div className="terminal-dot bg-[#28c840]" />
                  <span className="ml-3 text-xs text-surface-500">on your VPS</span>
                </div>
                <div className="terminal-body space-y-2 text-[13px]">
                  <div>
                    <span className="text-surface-400">$</span>{" "}
                    <span className="text-surface-200 select-all">cd relay &amp;&amp; RELAY_PASSWORD=secret docker compose up -d</span>
                  </div>
                  <div className="pl-2 text-green-400/80">{`{"status":"ok"}`}</div>
                </div>
              </div>
            </div>
          </div>
          <p className="mt-4 text-center text-xs text-surface-500">
            Or skip the relay entirely with{" "}
            <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver serve --no-relay</code>
            {" "}and Tailscale.{" "}
            <Link href="/docs/self-hosting" className="text-surface-300 underline hover:text-surface-100">
              Full self-hosting guide &rarr;
            </Link>
          </p>
        </div>
      </section>

      {/* ── Section 16: Waitlist ── */}
      <section id="waitlist" className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-2xl text-center">
          <h2 className="mb-4 text-2xl font-bold text-surface-50 md:text-3xl">
            Managed relay and cloud dev machines are coming.
            <br />
            Get early access.
          </h2>

          <div className="mx-auto mt-8 max-w-sm">
            <WaitlistButton plan="early-access" />
            <p className="mt-3 text-xs text-surface-500">No spam. One email when it ships.</p>
          </div>

          <div className="mt-12 space-y-4 text-left">
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 px-5 py-4">
              <p className="text-sm font-medium text-surface-200">Managed Relay</p>
              <p className="mt-1 text-xs text-surface-500">Zero-config P2P tunneling, no VPS needed</p>
            </div>
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 px-5 py-4">
              <p className="text-sm font-medium text-surface-200">Cloud CPU Machine</p>
              <p className="mt-1 text-xs text-surface-500">8 vCPU / 16 GB RAM, Expo + EAS pre-installed, build iOS without a Mac</p>
            </div>
            <div className="rounded-xl border border-surface-800 bg-surface-900/50 px-5 py-4">
              <p className="text-sm font-medium text-surface-200">Cloud GPU Machine</p>
              <p className="mt-1 text-xs text-surface-500">RTX 4000, full Ollama stack pre-loaded, no API keys ever</p>
            </div>
          </div>
        </div>
      </section>

      {/* ── Section 17: Open Source ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            MIT licensed. Fork it. Own your entire stack.
          </h2>
          <p className="mb-12 text-center text-sm text-surface-400">
            Every component is open source &mdash; you own your entire stack.
          </p>

          <div className="overflow-hidden rounded-xl border border-surface-800">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-surface-800 bg-surface-900/50">
                  <th className="px-5 py-3 font-medium text-surface-300">Component</th>
                  <th className="px-5 py-3 font-medium text-surface-300">Language</th>
                  <th className="px-5 py-3 font-medium text-surface-300">Description</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-surface-800/60">
                <tr><td className="px-5 py-3 text-surface-200">CLI Agent</td><td className="px-5 py-3 text-surface-400">Go</td><td className="px-5 py-3 text-surface-400">Runs on your dev machine, manages tmux sessions</td></tr>
                <tr><td className="px-5 py-3 text-surface-200">Mobile App</td><td className="px-5 py-3 text-surface-400">React Native</td><td className="px-5 py-3 text-surface-400">iOS + Android, send tasks, review output</td></tr>
                <tr><td className="px-5 py-3 text-surface-200">Relay Server</td><td className="px-5 py-3 text-surface-400">Go</td><td className="px-5 py-3 text-surface-400">QUIC relay, self-hostable via Docker</td></tr>
                <tr><td className="px-5 py-3 text-surface-200">Auth Bridge</td><td className="px-5 py-3 text-surface-400">Convex</td><td className="px-5 py-3 text-surface-400">OAuth + device discovery only, self-hostable</td></tr>
                <tr><td className="px-5 py-3 text-surface-200">Key Vault</td><td className="px-5 py-3 text-surface-400">P2P encrypted</td><td className="px-5 py-3 text-surface-400">NaCl secretbox, never touches our servers</td></tr>
              </tbody>
            </table>
          </div>

          <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <a href="https://github.com/kivanccakmak/yaver.io" target="_blank" rel="noopener noreferrer" className="btn-primary px-6 py-3 text-sm">
              GitHub
            </a>
            <Link href="/docs/contributing" className="btn-secondary px-6 py-3 text-sm">
              Contributing Guide
            </Link>
            <Link href="/docs/developers" className="btn-secondary px-6 py-3 text-sm">
              Developer Docs
            </Link>
          </div>
        </div>
      </section>

      {/* ── Section 18: Integrations ── */}
      <section id="integrations" className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <h2 className="mb-4 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Works with everything you use
          </h2>
          <p className="mx-auto mb-16 max-w-2xl text-center text-sm leading-relaxed text-surface-400">
            7 AI agents, 4 chat platforms, 5 SDKs, issue trackers, alerting, and every major transport layer.
            All data flows peer-to-peer &mdash; nothing stored on our servers.
          </p>

          <div className="grid grid-cols-1 gap-8 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
            {/* AI Agents */}
            <div className="rounded-xl border border-surface-800 bg-surface-900/40 p-5">
              <div className="mb-4 flex items-center gap-2">
                <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-violet-500/10 text-violet-400">
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09z" />
                  </svg>
                </div>
                <h3 className="text-sm font-semibold text-surface-100">AI Agents</h3>
              </div>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>Claude Code</li>
                <li>OpenAI Codex</li>
                <li>Aider</li>
                <li>Goose</li>
                <li>Ollama</li>
                <li>Amp</li>
                <li>OpenCode</li>
                <li className="text-surface-600">+ any custom CLI</li>
              </ul>
            </div>

            {/* Chat & Notifications */}
            <div className="rounded-xl border border-surface-800 bg-surface-900/40 p-5">
              <div className="mb-4 flex items-center gap-2">
                <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-500/10 text-blue-400">
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M8.625 12a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H8.25m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H12m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0h-.375M21 12c0 4.556-4.03 8.25-9 8.25a9.764 9.764 0 01-2.555-.337A5.972 5.972 0 015.41 20.97a5.969 5.969 0 01-.474-.065 4.48 4.48 0 00.978-2.025c.09-.457-.133-.901-.467-1.226C3.93 16.178 3 14.189 3 12c0-4.556 4.03-8.25 9-8.25s9 3.694 9 8.25z" />
                  </svg>
                </div>
                <h3 className="text-sm font-semibold text-surface-100">Chat &amp; Notifications</h3>
              </div>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>Telegram <span className="text-emerald-400/70 text-xs">(2-way)</span></li>
                <li>Discord</li>
                <li>Slack</li>
                <li>Teams</li>
              </ul>
            </div>

            {/* SDKs */}
            <div className="rounded-xl border border-surface-800 bg-surface-900/40 p-5">
              <div className="mb-4 flex items-center gap-2">
                <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-amber-500/10 text-amber-400">
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M17.25 6.75L22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3l-4.5 16.5" />
                  </svg>
                </div>
                <h3 className="text-sm font-semibold text-surface-100">SDKs</h3>
              </div>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>Go</li>
                <li>Python</li>
                <li>JavaScript / TypeScript</li>
                <li>Flutter / Dart</li>
                <li>C / C++</li>
              </ul>
            </div>

            {/* Connectivity */}
            <div className="rounded-xl border border-surface-800 bg-surface-900/40 p-5">
              <div className="mb-4 flex items-center gap-2">
                <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-green-500/10 text-green-400">
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M8.288 15.038a5.25 5.25 0 017.424 0M5.106 11.856c3.807-3.808 9.98-3.808 13.788 0M1.924 8.674c5.565-5.565 14.587-5.565 20.152 0M12.53 18.22l-.53.53-.53-.53a.75.75 0 011.06 0z" />
                  </svg>
                </div>
                <h3 className="text-sm font-semibold text-surface-100">Connectivity</h3>
              </div>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>Direct LAN <span className="text-surface-600 text-xs">(~5ms)</span></li>
                <li>QUIC Relay <span className="text-surface-600 text-xs">(self-host)</span></li>
                <li>Cloudflare Tunnel</li>
                <li>Tailscale</li>
              </ul>
            </div>

            {/* Developer Platforms */}
            <div className="rounded-xl border border-surface-800 bg-surface-900/40 p-5">
              <div className="mb-4 flex items-center gap-2">
                <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-orange-500/10 text-orange-400">
                  <svg className="h-4 w-4" fill="currentColor" viewBox="0 0 24 24">
                    <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.405.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/>
                  </svg>
                </div>
                <h3 className="text-sm font-semibold text-surface-100">Developer Platforms</h3>
              </div>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>GitHub</li>
                <li>GitLab</li>
              </ul>
            </div>

            {/* Dev Tools */}
            <div className="rounded-xl border border-surface-800 bg-surface-900/40 p-5">
              <div className="mb-4 flex items-center gap-2">
                <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-red-500/10 text-red-400">
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75v-.7V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0" />
                  </svg>
                </div>
                <h3 className="text-sm font-semibold text-surface-100">Dev Tools</h3>
              </div>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>Linear</li>
                <li>Jira</li>
                <li>PagerDuty</li>
                <li>Opsgenie</li>
                <li>Email</li>
              </ul>
            </div>
          </div>

          <div className="mt-10 text-center">
            <Link
              href="/integrations"
              className="inline-flex items-center gap-1.5 text-sm text-surface-400 transition-colors hover:text-[#6366f1]"
            >
              View all integrations
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
              </svg>
            </Link>
          </div>
        </div>
      </section>

      {/* ── Section 19: FAQ ── */}
      <section className="border-t border-surface-800/60 px-6 py-24">
        <div className="mx-auto max-w-3xl">
          <h2 className="mb-12 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            FAQ
          </h2>

          <div>
            <FAQItem
              question="What agents work with Yaver?"
              answer="Anything that runs in a terminal. Claude Code, Codex CLI, OpenCode, Goose, Amp, Aider, Ollama, Qwen, Continue, or whatever custom command you want. Run local models with Ollama for zero-cost, fully private AI coding. Switch agents per task or set a default."
            />
            <FAQItem
              question="How is this different from Claude Code Remote?"
              answer="Claude Code Remote is a hosted option by Anthropic for Claude Code. Yaver is agent-agnostic — it works with Claude Code, Ollama, Aider, Goose, Codex, or any tmux session. It runs everything on your own hardware. Both tools are useful depending on your setup."
            />
            <FAQItem
              question="Do I need API keys?"
              answer="Depends on the agent. Cloud agents like Claude Code or Codex need their own API keys or subscriptions. Local models via Ollama need nothing — just download the model and go. Yaver itself has no API keys and no paid tiers."
            />
            <FAQItem
              question="Do I need a relay server?"
              answer="It depends on your network setup. On the same WiFi, Yaver discovers your machine automatically via UDP LAN broadcast — no relay needed, connections are direct at ~5ms latency. When your phone is on cellular or a different network, you need a way to reach your machine: either a relay server (self-host with one Docker command), Tailscale (connect over your tailnet, DERP handles hard NAT), or Cloudflare Tunnel (pure TCP/HTTPS). The relay is a pass-through — it forwards encrypted bytes and cannot read your traffic."
            />
            <FAQItem
              question="Can I use Tailscale instead of a relay?"
              answer="Yes. If both devices are on your tailnet, Yaver connects directly via the Tailscale IP. No relay needed. Tailscale's DERP servers handle hard NAT cases automatically."
            />
            <FAQItem
              question="Is my code safe?"
              answer="Yaver connects your phone directly to your dev machine. CLI-to-relay uses QUIC (TLS encrypted), mobile-to-relay uses HTTPS. The relay is password-protected and can't inspect traffic — it just forwards bytes. On Tailscale, you get full WireGuard end-to-end encryption. On LAN, the beacon uses a SHA-256 token fingerprint so only your devices can discover each other. No code, tasks, or output ever reach any server. All of this is open source — read the code yourself."
            />
            <FAQItem
              question="Can I use Yaver without the mobile app?"
              answer="Yes. Run `yaver connect` from any terminal to connect to your remote dev machine. Laptop to desktop, server to server, SSH session to home machine — same connection strategy, same agent support. The mobile app is just one way to interact with your agent."
            />
            <FAQItem
              question="Is it actually free?"
              answer="Yes. MIT license, no paid tiers, no usage limits, no telemetry, no catch. If you find it useful, star the repo or contribute a patch."
            />
            <FAQItem
              question="What if I'm behind a strict corporate firewall?"
              answer="Yaver's relay uses QUIC, which runs over UDP. Some corporate firewalls block all UDP traffic, which would prevent the relay from working. In that case, you have two options: Tailscale (its DERP relay servers use HTTPS to punch through even the strictest firewalls, and it works with the Tailscale mobile app too), or Cloudflare Tunnel (pure TCP/HTTPS, works through any firewall that allows web browsing). Both options give you a direct connection to your machine without needing Yaver's relay at all."
            />
            <FAQItem
              question="Don't some agents already have remote access?"
              answer="Yes — Claude Code has a remote control feature, and OpenAI Codex runs in the cloud. Yaver is useful when you want a single interface across multiple agents, when you use local models that have no cloud option, or when you want full control over your infrastructure. It's agent-agnostic by design."
            />
            <FAQItem
              question="How does the Feedback SDK work?"
              answer="The Feedback SDK adds a floating debug button to your app during development. From it you can message your AI agent, trigger hot reload, build and deploy to TestFlight and Play Store, and report bugs with auto-screenshots. The SDK streams all logs, navigation events, and crashes to the agent like a flight recorder. When a bug is reported, the agent has full context and can write a fix immediately. It's gated behind your developer user ID so end users never see it."
            />
            <FAQItem
              question="How does voice input work?"
              answer="Yaver supports speech-to-text on both mobile and CLI. You can use the free on-device option (Whisper, runs entirely on your phone/machine) or bring your own API key for OpenAI, Deepgram, or AssemblyAI. On mobile, tap the mic button in the task modal. On CLI, type /voice in yaver connect. All transcription happens on your device or goes directly to the provider you choose — nothing passes through Yaver servers."
            />
            <FAQItem
              question="Can I embed Yaver in my own app?"
              answer="Yes — Yaver provides SDKs for Go, Python, and JavaScript/TypeScript. Import the package, point it at a running Yaver agent, and create tasks, stream output, or use speech-to-text from your code. A C shared library (.so/.dylib/.dll) is also available for C/C++ and any language with FFI support (Ruby, Rust, etc)."
            />
            <FAQItem
              question="How does the build pipeline work?"
              answer="Your dev machine builds the app (Flutter, Gradle, Xcode, React Native, or Expo). The artifact (APK, IPA, AAB) transfers P2P to your phone — free and instant. On Android, tap to install directly. On iOS, use TestFlight or OTA install via relay. You can also push to GitHub Actions or GitLab CI instead. No cloud CI minutes consumed for P2P builds."
            />
            <FAQItem
              question="What about TestFlight and Play Store?"
              answer="'yaver build push testflight' uploads your IPA directly to TestFlight. 'yaver build push playstore' uploads your AAB to Play Store internal testing. Credentials are stored in the P2P encrypted vault — never on our servers."
            />
            <FAQItem
              question="What is the visual feedback loop?"
              answer="After deploying a build to your phone, you test it and record bugs — screen recording + voice narration. The report goes back to your AI agent via P2P, which sees the recording, reads your transcript, and fixes the issues. Three modes: Live (agent watches in real-time and comments), Narrated (record + send), and Batch (full dump). You can also embed our feedback SDK (@yaver/feedback-web, @yaver/feedback-react-native, yaver_feedback) in your app for shake-to-report during development."
            />
            <FAQItem
              question="How does the Apps tab work?"
              answer="The agent scans your home directory for projects and detects their stack — React Native, Next.js, Vite, Convex, Supabase, Docker, etc. For React Native apps, you get native hot reload right inside the Yaver app with full device access (camera, BLE, GPS). For web apps, the dev server loads in a WebView. For backends, you get one-tap deploy. Works over WiFi or 4G through the relay. Zero config — no manifest files, no project setup."
            />
            <FAQItem
              question="Can I run tests from my phone?"
              answer="Yes. 'yaver test unit' auto-detects your test framework (Flutter, Jest, pytest, Go test, Cargo, XCTest, Espresso, Playwright, Cypress, Maestro) and runs it. You see pass/fail counts and failures on your phone. For the full pipeline: 'yaver pipeline --test --deploy p2p' builds, tests, and deploys in one command."
            />
            <FAQItem
              question="Can I hear responses read aloud?"
              answer="Yes — enable Text-to-Speech in Settings > Voice. It uses your device's built-in TTS engine (Apple TTS on iOS/macOS, espeak on Linux). You can also control response verbosity from 0 (just 'done') to 10 (full diffs and reasoning) so the AI adapts how much detail it gives."
            />
            <FAQItem
              question="How do I contribute?"
              answer="Fork the repo, hack on it, open a PR. Check the README for dev setup. Bug reports and feature ideas are welcome as GitHub issues."
            />
            <FAQItem
              question="How does the Key Vault work?"
              answer="Key Vault syncs API keys, SSH keys, and signing certificates between your phone and dev machine over encrypted P2P connections. Keys are encrypted at rest using NaCl secretbox with Argon2id key derivation. In transit, they're auth-gated — only your authenticated devices can request them. On mobile, keys are stored in the OS keychain (Keychain on iOS, Keystore on Android). On the CLI, they're stored in an encrypted file under ~/.yaver/. Nothing ever touches our servers."
            />
            <FAQItem
              question="What are the Cloud Dev Machines?"
              answer="Dedicated Linux dev environments provisioned just for you — not shared with anyone. CPU machines come with 8 vCPU, 16 GB RAM, 160 GB NVMe. GPU machines add a dedicated NVIDIA RTX 4000 with 20 GB VRAM, Ollama + Qwen 2.5 Coder 32B, and PersonaPlex 7B for voice AI. All tiers include Node.js, Python, Go, Rust, Docker, Expo CLI, EAS CLI. Coming soon — join the waitlist for early access."
            />
          </div>
        </div>
      </section>

      {/* ── Related Work ── */}
      <section className="border-t border-surface-800/60 px-6 py-20">
        <div className="mx-auto max-w-6xl">
          <h2 className="mb-2 text-xl font-bold text-surface-50 md:text-2xl">Related Work</h2>
          <p className="mb-3 text-sm text-surface-400">
            Projects and tools in the same problem space. Yaver is compatible with most of these and can be used alongside them.
          </p>
          <p className="mb-10 text-xs text-surface-500">
            <span className="inline-flex items-center gap-1"><span className="rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-emerald-400">OSS</span> = open-source software</span>
          </p>

          <div className="grid gap-10 md:grid-cols-2 lg:grid-cols-3">
            {/* AI Coding Agents */}
            <div>
              <p className="mb-3 text-xs font-semibold uppercase tracking-wider text-surface-500">AI Coding Agents</p>
              <ul className="space-y-2.5 text-sm">
                <li>
                  <a href="https://docs.anthropic.com/en/docs/claude-code" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Claude Code</a>
                  <span className="text-surface-500"> &mdash; Anthropic&apos;s agentic coding tool</span>
                </li>
                <li>
                  <a href="https://ollama.com" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Ollama</a>
                  <span className="ml-1.5 rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-emerald-400">OSS</span>
                  <span className="text-surface-500"> &mdash; run LLMs locally</span>
                </li>
                <li>
                  <a href="https://aider.chat" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Aider</a>
                  <span className="ml-1.5 rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-emerald-400">OSS</span>
                  <span className="text-surface-500"> &mdash; AI pair programming in the terminal</span>
                </li>
                <li>
                  <a href="https://github.com/openai/codex" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">OpenAI Codex CLI</a>
                  <span className="ml-1.5 rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-emerald-400">OSS</span>
                  <span className="text-surface-500"> &mdash; OpenAI&apos;s coding agent</span>
                </li>
                <li>
                  <a href="https://block.github.io/goose/" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Goose</a>
                  <span className="ml-1.5 rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-emerald-400">OSS</span>
                  <span className="text-surface-500"> &mdash; Block&apos;s autonomous coding agent</span>
                </li>
                <li>
                  <a href="https://ampcode.com" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Amp</a>
                  <span className="text-surface-500"> &mdash; Sourcegraph&apos;s agentic IDE</span>
                </li>
              </ul>
            </div>

            {/* Mobile Bug Reporting & Observability */}
            <div>
              <p className="mb-3 text-xs font-semibold uppercase tracking-wider text-surface-500">Mobile Bug Reporting &amp; Observability</p>
              <ul className="space-y-2.5 text-sm">
                <li>
                  <a href="https://instabug.com" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Instabug</a>
                  <span className="text-surface-500"> &mdash; in-app bug reporting &amp; crash analytics</span>
                </li>
                <li>
                  <a href="https://luciq.ai" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Luciq</a>
                  <span className="text-surface-500"> &mdash; agentic observability platform for mobile</span>
                </li>
                <li>
                  <a href="https://sentry.io" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Sentry</a>
                  <span className="ml-1.5 rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-emerald-400">OSS</span>
                  <span className="text-surface-500"> &mdash; error tracking &amp; performance monitoring</span>
                </li>
                <li>
                  <a href="https://www.bugsnag.com" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">BugSnag</a>
                  <span className="text-surface-500"> &mdash; application stability management</span>
                </li>
                <li>
                  <a href="https://www.shakebugs.com" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Shake</a>
                  <span className="text-surface-500"> &mdash; shake-to-report bug SDK</span>
                </li>
                <li>
                  <a href="https://firebase.google.com/products/crashlytics" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Firebase Crashlytics</a>
                  <span className="text-surface-500"> &mdash; Google&apos;s crash reporting</span>
                </li>
              </ul>
            </div>

            {/* Remote Dev & Connectivity */}
            <div>
              <p className="mb-3 text-xs font-semibold uppercase tracking-wider text-surface-500">Remote Dev &amp; Connectivity</p>
              <ul className="space-y-2.5 text-sm">
                <li>
                  <a href="https://tailscale.com" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Tailscale</a>
                  <span className="text-surface-500"> &mdash; WireGuard mesh VPN</span>
                </li>
                <li>
                  <a href="https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">Cloudflare Tunnel</a>
                  <span className="text-surface-500"> &mdash; HTTPS tunnel to localhost</span>
                </li>
                <li>
                  <a href="https://ngrok.com" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">ngrok</a>
                  <span className="text-surface-500"> &mdash; expose local servers publicly</span>
                </li>
                <li>
                  <a href="https://code.visualstudio.com/docs/remote/remote-overview" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">VS Code Remote</a>
                  <span className="text-surface-500"> &mdash; remote development via SSH</span>
                </li>
                <li>
                  <a href="https://www.wireguard.com" target="_blank" rel="noopener noreferrer" className="font-medium text-surface-300 hover:text-surface-50">WireGuard</a>
                  <span className="ml-1.5 rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-emerald-400">OSS</span>
                  <span className="text-surface-500"> &mdash; modern VPN protocol</span>
                </li>
              </ul>
            </div>
          </div>
        </div>
      </section>

      {/* ── Footer ── */}
      <footer className="border-t border-surface-800/60 px-6 py-12">
        <div className="mx-auto max-w-5xl">
          <div className="flex flex-col items-center justify-between gap-6 sm:flex-row">
            <p className="text-xs text-surface-500">
              &copy; 2026 SIMKAB ELEKTRIK &middot;{" "}
              <a href="mailto:kivanc.cakmak@simkab.com" className="hover:text-surface-300">kivanc.cakmak@simkab.com</a>
            </p>
            <div className="flex flex-wrap items-center gap-4 text-xs text-surface-500">
              <a href="#features" className="hover:text-surface-300">Features</a>
              <a href="#faq" className="hover:text-surface-300">FAQ</a>
              <a href="/docs" className="hover:text-surface-300">Docs</a>
              <a href="/docs/developers" className="hover:text-surface-300">Developers</a>
              <Link href="/download" className="hover:text-surface-300">Download</Link>
              <a href="#waitlist" className="hover:text-surface-300">Pricing</a>
              <a href="#integrations" className="hover:text-surface-300">Integrations</a>
            </div>
          </div>
          <p className="mt-6 text-center text-xs text-surface-600">
            MIT Licensed &middot; Free Forever &middot;{" "}
            <a href="https://github.com/kivanccakmak/yaver.io" target="_blank" rel="noopener noreferrer" className="hover:text-surface-300">Source Code</a>
          </p>
        </div>
      </footer>
    </>
  );
}
