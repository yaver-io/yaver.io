"use client";

// /j/[code] — Yaver Support Link landing page (docs/mesh-support-link.md).
// A friend opens this from a link the supporter shared. It shows WHO is
// inviting and exactly what installing will do, then gives one inspectable
// install command (no piped shell by default — security T2). The actual access
// grant happens on the friend's machine behind a consent prompt in `yaver join`.

import { use, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

type InviteInfo = {
  valid: boolean;
  status?: string;
  offerTerminal?: boolean;
  offerDesktopControl?: boolean;
  defaultTtlHours?: number;
  label?: string;
  inviter?: { name: string; email?: string; avatarUrl?: string } | null;
};

function detectOS(): "mac" | "linux" | "windows" | "unknown" {
  if (typeof navigator === "undefined") return "unknown";
  const p = `${navigator.platform} ${navigator.userAgent}`.toLowerCase();
  if (p.includes("mac")) return "mac";
  if (p.includes("win")) return "windows";
  if (p.includes("linux")) return "linux";
  return "unknown";
}

export default function SupportJoinPage({ params }: { params: Promise<{ code: string }> }) {
  const { code } = use(params);
  const [info, setInfo] = useState<InviteInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [copied, setCopied] = useState(false);
  const os = detectOS();

  useEffect(() => {
    fetch(`${CONVEX_URL}/support/invite/info?code=${encodeURIComponent(code)}`)
      .then((r) => r.json())
      .then(setInfo)
      .catch(() => setInfo({ valid: false }))
      .finally(() => setLoading(false));
  }, [code]);

  const installCmd = `npm install -g yaver-cli && yaver join ${code}`;
  const copy = () => {
    navigator.clipboard?.writeText(installCmd);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  if (loading) {
    return <Centered><p className="text-surface-400">Loading…</p></Centered>;
  }
  if (!info || !info.valid) {
    return (
      <Centered>
        <h1 className="text-2xl font-semibold text-surface-100">Link expired</h1>
        <p className="mt-2 max-w-md text-surface-400">
          This support link is no longer valid{info?.status ? ` (${info.status})` : ""}. Ask the
          person who sent it for a fresh link.
        </p>
      </Centered>
    );
  }

  const inviter = info.inviter?.name ?? "Someone";

  return (
    <Centered>
      <div className="w-full max-w-lg space-y-6">
        <div className="flex items-center gap-3">
          {info.inviter?.avatarUrl ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={info.inviter.avatarUrl} alt="" className="h-12 w-12 rounded-full" />
          ) : (
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-emerald-500/20 text-emerald-700 dark:text-emerald-300">
              {inviter.charAt(0).toUpperCase()}
            </div>
          )}
          <div>
            <h1 className="text-xl font-semibold text-surface-100">{inviter} wants to help you</h1>
            {info.inviter?.email && <p className="text-xs text-surface-500">{info.inviter.email}</p>}
          </div>
        </div>

        <p className="text-surface-300">
          Yaver lets {inviter} securely connect to this computer to help you — like screen-sharing,
          but you stay in control. Installing takes ~1 minute. <strong>Nothing happens until you
          approve it</strong> on your own screen.
        </p>

        <div className="rounded-2xl border border-surface-800 bg-surface-900/70 p-4">
          <p className="mb-2 text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">
            1 · Install &amp; connect
          </p>
          {os === "windows" && (
            <p className="mb-2 text-xs text-amber-700 dark:text-amber-200/90">
              On Windows, open <strong>WSL2</strong> (Ubuntu) and run this inside it.
            </p>
          )}
          <div className="flex items-center gap-2 rounded-xl border border-surface-800 bg-surface-950 p-3">
            <code className="flex-1 break-all text-xs text-emerald-700 dark:text-emerald-300">{installCmd}</code>
            <button
              onClick={copy}
              className="shrink-0 rounded-lg border border-surface-700 px-3 py-1 text-xs text-surface-200 hover:bg-surface-800"
            >
              {copied ? "Copied" : "Copy"}
            </button>
          </div>
          <p className="mt-2 text-[11px] text-surface-500">
            This only installs the open-source Yaver CLI and starts the join flow — you can read
            exactly what it does at github.com/kivanccakmak/yaver.io.
          </p>
        </div>

        <div className="rounded-2xl border border-surface-800 bg-surface-900/70 p-4">
          <p className="mb-2 text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">
            2 · Approve access
          </p>
          <p className="text-sm text-surface-300">
            Yaver will ask you to sign in (1 tap) and then show a consent screen. By default
            {inviter} can only <strong>see status and read files</strong>. You choose whether to
            also allow:
          </p>
          <ul className="mt-2 space-y-1 text-sm text-surface-400">
            <li>• Running commands / an AI agent {info.offerTerminal ? "(offered)" : "(not offered)"}</li>
            <li>• Controlling your screen + keyboard {info.offerDesktopControl ? "(offered)" : "(not offered)"}</li>
          </ul>
          <p className="mt-2 text-[11px] text-surface-500">
            Access {info.defaultTtlHours ? `defaults to ${info.defaultTtlHours}h` : "is time-limited"} and
            you can cut it instantly anytime with <code className="text-surface-300">yaver support deny-all</code>.
          </p>
        </div>
      </div>
    </Centered>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <main className="flex min-h-screen flex-col items-center justify-center bg-surface-950 px-4 py-12 text-center">
      {children}
    </main>
  );
}
