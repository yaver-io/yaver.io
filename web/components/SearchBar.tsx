"use client";

import { useState, useEffect, useRef, useCallback } from "react";
import { useRouter } from "next/navigation";

// Static search index — all searchable pages
const SEARCH_INDEX = [
  { path: "/", title: "Home", tags: "p2p encrypted ai agent mobile desktop cli" },
  { path: "/download", title: "Download", tags: "install mac windows linux brew scoop homebrew" },
  { path: "/pricing", title: "Deployment Options", tags: "relay self-host local machine cloud preview" },
  { path: "/docs", title: "Documentation", tags: "docs guide api reference" },
  { path: "/docs/developers", title: "Developer Guide", tags: "sdk api architecture build test contribute" },
  { path: "/docs/mcp", title: "MCP Protocol", tags: "mcp model context protocol tools json-rpc" },
  { path: "/docs/self-hosting", title: "Self-Hosting", tags: "self-host relay docker vps deploy" },
  { path: "/docs/contributing", title: "Contributing", tags: "contribute pull request pr code style" },
  { path: "/faq", title: "FAQ", tags: "questions help troubleshoot" },
  { path: "/manuals", title: "Manuals", tags: "setup guide tutorial how-to" },
  { path: "/blog", title: "Blog", tags: "news releases announcements product updates" },
  { path: "/blog/yaver-pi-image", title: "Raspberry Pi 5 Dev-Node Image", tags: "raspberry pi image arm64 download dev node codex claude opencode" },
  { path: "/blog/opencode-providers-and-ollama", title: "OpenCode Providers and Ollama", tags: "opencode byok anthropic openai openrouter glm zhipu zai ollama local free" },
  { path: "/manuals/cli-setup", title: "CLI Setup", tags: "cli install auth serve connect terminal" },
  { path: "/manuals/relay-setup", title: "Relay Setup", tags: "relay server deploy docker nginx ssl" },
  { path: "/manuals/auto-boot", title: "Auto-Boot", tags: "launchd systemd auto-start boot service" },
  { path: "/manuals/raspberry-pi", title: "Raspberry Pi", tags: "raspberry pi arm64 image home server dev node pair mobile" },
  { path: "/manuals/integrations", title: "Integrations", tags: "telegram discord slack webhook mcp notifications session transfer" },
  { path: "/privacy", title: "Privacy Policy", tags: "privacy data policy gdpr" },
  { path: "/terms", title: "Terms of Service", tags: "terms conditions legal" },
  // Feature-specific entries
  { path: "/manuals/integrations#notifications", title: "Telegram Bot Setup", tags: "telegram bot notification chat two-way bidirectional" },
  { path: "/manuals/integrations#notifications", title: "Discord Webhook Setup", tags: "discord webhook notification alert" },
  { path: "/manuals/integrations#notifications", title: "Slack Webhook Setup", tags: "slack webhook notification alert" },
  { path: "/manuals/integrations#webhooks", title: "CI/CD Webhooks", tags: "webhook github actions gitlab ci cd trigger" },
  { path: "/manuals/integrations#mcp-tools", title: "MCP Tools", tags: "mcp tools search files git exec screenshot system" },
  { path: "/manuals/integrations#session-transfer", title: "Session Transfer", tags: "session transfer move migrate claude codex opencode" },
  { path: "/docs/developers#sdk", title: "SDKs", tags: "sdk go python javascript typescript flutter dart npm pip pub.dev" },
  { path: "/manuals/relay-setup", title: "Self-Host Relay", tags: "self-host relay install docker one-line script curl" },
  { path: "/docs/developers#session-transfer", title: "Session Transfer API", tags: "session export import transfer api endpoint" },
  { path: "/pricing", title: "Self-Hosted Relay", tags: "relay server docker hetzner self-host" },
  { path: "/integrations", title: "Integrations", tags: "integrations chat ai agents developer tools ci cd sdk connectivity" },
];

function fuzzyMatch(query: string, text: string): boolean {
  const q = query.toLowerCase();
  const t = text.toLowerCase();
  if (t.includes(q)) return true;
  // Simple fuzzy: all chars of query appear in order in text
  let qi = 0;
  for (let i = 0; i < t.length && qi < q.length; i++) {
    if (t[i] === q[qi]) qi++;
  }
  return qi === q.length;
}

export default function SearchBar() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const router = useRouter();

  // Cmd+K shortcut
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setOpen(true);
      }
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  useEffect(() => {
    if (open) {
      setTimeout(() => inputRef.current?.focus(), 50);
      setQuery("");
      setSelectedIndex(0);
    }
  }, [open]);

  const results = query.length > 0
    ? SEARCH_INDEX.filter((item) =>
        fuzzyMatch(query, item.title + " " + item.tags)
      ).slice(0, 8)
    : [];

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIndex((i) => Math.min(i + 1, results.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === "Enter" && results[selectedIndex]) {
      router.push(results[selectedIndex].path);
      setOpen(false);
    }
  }, [results, selectedIndex, router]);

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="flex items-center gap-2 rounded-lg border border-surface-800 bg-surface-900/50 px-3 py-1.5 text-xs text-surface-500 transition-colors hover:border-surface-600 hover:text-surface-300"
      >
        <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" />
        </svg>
        Search...
        <kbd className="ml-2 rounded border border-surface-700 bg-surface-800 px-1.5 py-0.5 text-[10px] font-medium text-surface-500">&#8984;K</kbd>
      </button>
    );
  }

  return (
    <div className="fixed inset-0 z-[200] flex items-start justify-center pt-[20vh]" onClick={() => setOpen(false)}>
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <div
        className="relative w-full max-w-lg rounded-xl border border-surface-700 bg-[#1a1d27] shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center border-b border-surface-800 px-4">
          <svg className="h-4 w-4 shrink-0 text-surface-500" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" />
          </svg>
          <input
            ref={inputRef}
            type="text"
            value={query}
            onChange={(e) => { setQuery(e.target.value); setSelectedIndex(0); }}
            onKeyDown={handleKeyDown}
            placeholder="Search docs, manuals, features..."
            className="flex-1 bg-transparent px-3 py-4 text-sm text-surface-100 outline-none placeholder:text-surface-600"
          />
          <kbd className="rounded border border-surface-700 bg-surface-800 px-1.5 py-0.5 text-[10px] text-surface-500">ESC</kbd>
        </div>

        {results.length > 0 && (
          <ul className="max-h-80 overflow-y-auto p-2">
            {results.map((item, i) => (
              <li key={item.path + item.title}>
                <button
                  onClick={() => { router.push(item.path); setOpen(false); }}
                  className={`flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm transition-colors ${
                    i === selectedIndex ? "bg-[#6366f1]/20 text-surface-100" : "text-surface-400 hover:bg-surface-800/50"
                  }`}
                >
                  <svg className="h-4 w-4 shrink-0 text-surface-600" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
                  </svg>
                  <div>
                    <div className="font-medium">{item.title}</div>
                    <div className="text-xs text-surface-600">{item.path}</div>
                  </div>
                </button>
              </li>
            ))}
          </ul>
        )}

        {query.length > 0 && results.length === 0 && (
          <div className="px-4 py-8 text-center text-sm text-surface-600">
            No results for &ldquo;{query}&rdquo;
          </div>
        )}

        {query.length === 0 && (
          <div className="px-4 py-6 text-center text-xs text-surface-600">
            Type to search docs, manuals, integrations...
          </div>
        )}
      </div>
    </div>
  );
}
