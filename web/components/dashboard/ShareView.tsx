"use client";

import { useState } from "react";

// Viral share surface. Given a deploy URL (or any public project URL), builds
// share intents for Twitter / Bluesky / Reddit / LinkedIn / copy, and shows
// the Built-with-Yaver badge snippet the user can embed.

export default function ShareView() {
  const [url, setUrl] = useState("");
  const [name, setName] = useState("");

  const message = name
    ? `Just shipped ${name} — built on my own hardware with Yaver (free, open, your machine is your cloud). ${url}`
    : url
      ? `Just shipped a new project with Yaver. ${url}`
      : "I'm building with Yaver — free, open, your machine is your cloud.";
  const plain = encodeURIComponent(message);
  const urlOnly = encodeURIComponent(url || "https://yaver.io");

  const intents = [
    { label: "𝕏 (Twitter)", href: `https://twitter.com/intent/tweet?text=${plain}` },
    { label: "Bluesky", href: `https://bsky.app/intent/compose?text=${plain}` },
    { label: "Reddit", href: `https://reddit.com/submit?title=${encodeURIComponent(name || "Built with Yaver")}&url=${urlOnly}` },
    { label: "LinkedIn", href: `https://www.linkedin.com/sharing/share-offsite/?url=${urlOnly}` },
    { label: "Hacker News", href: `https://news.ycombinator.com/submitlink?u=${urlOnly}&t=${encodeURIComponent(name || "Built with Yaver")}` },
  ];

  const badgeHTML = `<a href="https://yaver.io"><img src="https://yaver.io/badge" alt="Built with Yaver" height="28"/></a>`;
  const badgeHTMLLight = `<a href="https://yaver.io"><img src="https://yaver.io/badge?theme=light" alt="Built with Yaver" height="28"/></a>`;

  async function copy(text: string) {
    try { await navigator.clipboard.writeText(text); alert("Copied"); } catch {}
  }

  return (
    <div className="space-y-5">
      <div>
        <h2 className="text-xl font-semibold text-surface-100">Share what you built</h2>
        <p className="text-sm text-surface-500">Yaver is free and open. The way it grows is when you tell someone.</p>
      </div>

      <div className="space-y-2">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Project name (optional)"
          className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm" />
        <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://myapp.com"
          className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      </div>

      <div>
        <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">Share to</h3>
        <div className="flex flex-wrap gap-2">
          {intents.map((i) => (
            <a key={i.label} href={i.href} target="_blank" rel="noreferrer"
              className="px-3 py-2 text-sm rounded-lg bg-indigo-500/20 text-indigo-300 hover:bg-indigo-500/30 border border-indigo-500/30">
              {i.label}
            </a>
          ))}
          <button onClick={() => copy(message)} className="px-3 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">
            Copy text
          </button>
        </div>
      </div>

      <div>
        <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">"Built with Yaver" badge</h3>
        <p className="text-xs text-surface-500 mb-2">Drop the snippet into your footer. Free forever — no account, no tracking.</p>
        <div className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-3">
          <img src="/badge" alt="Built with Yaver" />
          <button onClick={() => copy(badgeHTML)} className="ml-auto text-xs text-indigo-400 hover:text-indigo-300">Copy dark</button>
          <button onClick={() => copy(badgeHTMLLight)} className="text-xs text-indigo-400 hover:text-indigo-300">Copy light</button>
        </div>
        <pre className="mt-2 text-[10px] text-surface-500 font-mono bg-surface-900/50 border border-surface-800 rounded p-2 overflow-auto">{badgeHTML}</pre>
      </div>

      <div className="rounded-lg border border-indigo-500/40 bg-indigo-500/5 p-4 text-sm">
        <div className="font-semibold text-indigo-300 mb-1">Tell a friend</div>
        <p className="text-surface-300">Yaver is free for solo builders and always will be. The more people use it, the more features we can ship. Share your invite:</p>
        <div className="mt-3 flex gap-2">
          <input readOnly value="https://yaver.io" className="flex-1 rounded border border-surface-700 bg-surface-900 px-3 py-1.5 text-sm font-mono" />
          <button onClick={() => copy("https://yaver.io")} className="px-3 py-1.5 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">Copy</button>
        </div>
      </div>
    </div>
  );
}
