import Image from "next/image";
import Link from "next/link";

function DownloadButton({
  href,
  primary,
  children,
}: {
  href: string;
  primary?: boolean;
  children: React.ReactNode;
}) {
  const className = primary
    ? "inline-flex items-center justify-center rounded-xl bg-surface-50 px-4 py-2.5 text-sm font-semibold text-surface-950 transition hover:bg-surface-100"
    : "inline-flex items-center justify-center rounded-xl border border-surface-700 px-4 py-2.5 text-sm font-semibold text-surface-200 transition hover:border-surface-500 hover:text-surface-50";

  return (
    <a href={href} className={className}>
      {children}
    </a>
  );
}

function CommandCard({ label, commands }: { label: string; commands: string[] }) {
  return (
    <div className="rounded-2xl border border-surface-800 bg-surface-900/70 p-5">
      <p className="mb-3 text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
        {label}
      </p>
      <div className="rounded-xl bg-surface-950 p-4 font-mono text-[13px] text-surface-300">
        {commands.map((command) => (
          <div key={command} className="mb-2 last:mb-0">
            <span className="text-surface-500">$</span>{" "}
            <span className="select-all">{command}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

export default function DownloadPage() {
  return (
    <div className="px-6 py-16 md:py-20">
      <div className="mx-auto max-w-4xl">
        <section className="relative overflow-hidden rounded-[2rem] border border-surface-800 bg-surface-900 px-6 py-10 md:px-10 md:py-12">
          <div className="absolute inset-0 bg-[radial-gradient(circle_at_top_right,rgba(255,255,255,0.10),transparent_32%),radial-gradient(circle_at_bottom_left,rgba(255,255,255,0.06),transparent_28%)]" />
          <div className="relative">
            <div className="mb-4 inline-flex items-center gap-3 rounded-full border border-surface-700 bg-surface-950/70 px-4 py-2">
              <Image
                src="/icon-192.png"
                alt="Yaver logo"
                width={28}
                height={28}
                className="rounded-md"
              />
              <span className="text-xs font-semibold uppercase tracking-[0.24em] text-surface-400">
                Install Yaver
              </span>
            </div>
            <h1 className="max-w-3xl text-4xl font-bold tracking-tight text-surface-50 md:text-5xl">
              One install path. npm.
            </h1>
            <p className="mt-4 max-w-2xl text-sm leading-6 text-surface-400 md:text-base">
              Yaver ships exclusively through <code>npm install -g yaver-cli</code> on every supported
              platform: macOS (Apple Silicon and Intel), Linux (x64 and arm64 — Raspberry Pi, AWS Graviton,
              ARM VPSes, etc.), and Windows via WSL2. The npm package detects your platform and
              downloads the matching, signed and notarized agent binary into <code>~/.yaver/bin/</code>.
            </p>
            <p className="mt-3 max-w-2xl text-sm leading-6 text-surface-500">
              Why one path: a single <code>yaver</code> command, owned by the user who runs it (no
              system-user split, no <code>/root/.yaver</code> vs <code>/home/yaver/.yaver</code> drift),
              auto-updates with <code>npm install -g yaver-cli@latest</code>, and avoids multiple
              competing install channels on the same machine. Legacy packaging paths are removed.
            </p>
            <p className="mt-3 max-w-2xl text-sm leading-6 text-surface-500">
              Install Node.js 18+ however you normally manage Node. That does not change Yaver
              distribution: <code>yaver-cli</code> is npm-only, and upgrades must also happen through
              npm so one machine does not accumulate multiple competing <code>yaver</code> binaries.
            </p>
          </div>
        </section>

        <section className="mt-10 rounded-2xl border border-surface-800 bg-surface-900 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
            One-shot install
          </p>
          <h2 className="mt-2 text-2xl font-semibold text-surface-50">
            Install with npm
          </h2>
          <p className="mt-3 max-w-2xl text-sm leading-6 text-surface-400">
            Requires Node.js 18+. The <code>npm install -g</code> step installs a tiny shim into your
            global npm <code>bin/</code> dir; first run downloads the matching agent binary from
            GitHub Releases into <code>~/.yaver/bin/&lt;version&gt;/</code> and verifies its signature.
          </p>
          <div className="mt-6 grid gap-4 md:grid-cols-2">
            <CommandCard
              label="Install"
              commands={[
                "npm install -g yaver-cli",
                "yaver auth",
                "yaver serve",
              ]}
            />
            <CommandCard
              label="Update"
              commands={[
                "npm install -g yaver-cli@latest",
                "yaver --version",
              ]}
            />
          </div>
        </section>

        <section className="mt-10 grid gap-5 md:grid-cols-2">
          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <h2 className="text-lg font-semibold text-surface-50">macOS</h2>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              Apple Silicon and Intel. Binary is Developer ID signed and notarized — no Gatekeeper
              prompts. Once Node.js 18+ is present, install Yaver with npm.
            </p>
            <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[13px] text-surface-300">
              <div><span className="text-surface-500">$</span> <span className="select-all">npm install -g yaver-cli</span></div>
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <h2 className="text-lg font-semibold text-surface-50">Linux (x64 and arm64)</h2>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              Ubuntu, Debian, Raspberry Pi 4/5, AWS Graviton, Oracle Cloud ARM, ARM VPSes, etc.
              Once Node.js 18+ is present, install and update Yaver only with npm.
            </p>
            <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[13px] text-surface-300">
              <div><span className="text-surface-500">$</span> <span className="select-all">npm install -g yaver-cli</span></div>
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <h2 className="text-lg font-semibold text-surface-50">Raspberry Pi image</h2>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              The Pi image is just a convenience base image for a headless dev node. It does not change
              distribution. After boot, install <code>yaver-cli</code> with npm on the Pi itself, and
              update it later with <code>npm install -g yaver-cli@latest</code> only.
            </p>
            <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[13px] text-surface-300">
              <div className="mb-2 text-surface-500"># on the Pi after first boot:</div>
              <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">npm install -g yaver-cli</span></div>
              <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">yaver auth --headless</span></div>
              <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">yaver serve --install-systemd</span></div>
              <div><span className="text-surface-500">$</span> <span className="select-all">npm install -g yaver-cli@latest</span></div>
            </div>
            <div className="mt-4 flex flex-wrap gap-3">
              <DownloadButton href="/manuals/raspberry-pi" primary>
                Raspberry Pi manual
              </DownloadButton>
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <h2 className="text-lg font-semibold text-surface-50">Windows (via WSL2)</h2>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              Native Windows is not supported. Run Yaver inside WSL2; <code>yaver auth</code> hands
              browser sign-in off to Windows automatically. Once Node.js 18+ is present in WSL2,
              install Yaver with npm.
            </p>
            <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[13px] text-surface-300">
              <div className="mb-2 text-surface-500"># inside WSL2:</div>
              <div><span className="text-surface-500">$</span> <span className="select-all">npm install -g yaver-cli</span></div>
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <h2 className="text-lg font-semibold text-surface-50">Headless / SSH-only</h2>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              Pi, VPS, remote Linux box: install via npm, then sign in via short code. No browser on the
              target machine required. Upgrades are still npm-only here too.
            </p>
            <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
              <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">sudo npm install -g yaver-cli</span></div>
              <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">yaver auth --headless</span></div>
              <div className="mb-1 text-surface-500">Go to https://yaver.io/auth/device?code=XXXX-YYYY</div>
              <div><span className="text-surface-500">$</span> <span className="select-all">yaver serve --install-systemd  # survives reboots</span></div>
            </div>
          </div>
        </section>

        <section className="mt-10 grid gap-5 md:grid-cols-2">
          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <div className="flex items-center gap-2">
              <h2 className="text-lg font-semibold text-surface-50">Android app</h2>
              <span className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-400">
                APK &middot; QR install
              </span>
            </div>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              Scan the QR from your phone or tap through to download the latest signed APK. No Node,
              no CLI &mdash; the mobile app is the native container that runs your projects and drives
              your agents. Also on Google Play.
            </p>
            <div className="mt-5 flex flex-wrap gap-3">
              <DownloadButton href="https://download.yaver.io" primary>
                Open QR install page
              </DownloadButton>
              <a
                href="https://download.yaver.io/latest.apk"
                className="inline-flex items-center justify-center rounded-xl border border-surface-700 px-4 py-2.5 text-sm font-semibold text-surface-200 transition hover:border-surface-500 hover:text-surface-50"
              >
                Download APK directly
              </a>
              <a
                href="https://play.google.com/store/apps/details?id=io.yaver.mobile"
                className="inline-flex items-center justify-center rounded-xl border border-surface-700 px-4 py-2.5 text-sm font-semibold text-surface-200 transition hover:border-surface-500 hover:text-surface-50"
              >
                Google Play
              </a>
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <h2 className="text-lg font-semibold text-surface-50">iOS app</h2>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              iPhone and iPad ship through the App Store &mdash; Apple does not allow public APK-style
              sideloading. Install from the store, then sign in with the same account as your desktop
              agent.
            </p>
            <div className="mt-5 flex flex-wrap gap-3">
              <DownloadButton href="https://apps.apple.com/us/app/yaver-io/id6760467669" primary>
                App Store
              </DownloadButton>
            </div>
          </div>
        </section>

        <section className="mt-10 rounded-2xl border border-surface-800 bg-surface-900 p-6">
          <h2 className="text-lg font-semibold text-surface-50">Sign-in providers</h2>
          <p className="mt-3 text-sm leading-6 text-surface-400">
            <code>yaver auth</code> opens the browser to sign in with any of the providers below. Linking
            multiple identities to the same account is supported on web and mobile.
          </p>
          <div className="mt-3 flex flex-wrap gap-2">
            {[
              { name: "Google (Gmail)", emoji: "\u{1F4E7}" },
              { name: "Apple", emoji: "" },
              { name: "Microsoft / O365", emoji: "\u{1F5C2}" },
              { name: "GitHub", emoji: "\u{1F431}" },
              { name: "GitLab", emoji: "\u{1F98A}" },
              { name: "Discord", emoji: "\u{1F3AE}" },
              { name: "Slack", emoji: "\u{1F4AC}" },
              { name: "Email / password", emoji: "✉" },
            ].map((p) => (
              <span
                key={p.name}
                className="inline-flex items-center gap-1.5 rounded-full border border-surface-700 bg-surface-950 px-3 py-1 text-[12px] text-surface-300"
              >
                <span>{p.emoji}</span>
                {p.name}
              </span>
            ))}
          </div>
          <p className="mt-4 text-xs text-surface-500">
            See{" "}
            <Link href="/manuals/account-linking" className="underline hover:text-surface-300">
              account linking
            </Link>{" "}
            for merging accounts you made by accident.
          </p>
        </section>

        <section className="mt-10 rounded-2xl border border-surface-800 bg-surface-900 p-6">
          <h2 className="text-lg font-semibold text-surface-50">
            Use from Claude Code, Codex, or opencode (MCP)
          </h2>
          <p className="mt-3 text-sm leading-6 text-surface-400">
            Yaver ships an MCP server. No global install needed &mdash; <code>npx</code> pulls it on
            first run. Register it once, then ask the agent to call <code>yaver_lazy_setup</code>; it
            surfaces the sign-in link and pairs your phone from inside the chat.
          </p>
          <div className="mt-5 space-y-2 rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="text-surface-500"># Claude Code</div>
            <div><span className="text-surface-500">$</span> <span className="select-all">claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp</span></div>
            <div className="mt-2 text-surface-500"># Codex</div>
            <div><span className="text-surface-500">$</span> <span className="select-all">codex mcp add yaver -- npx -y yaver-cli yaver-mcp</span></div>
            <div className="mt-2 text-surface-500"># opencode</div>
            <div><span className="text-surface-500">$</span> <span className="select-all">npm install -g yaver-cli && yaver mcp setup opencode</span></div>
          </div>
          <p className="mt-4 text-xs text-surface-500">
            Already installed globally? <code>yaver mcp setup claude-code</code> writes the same entry,
            and <code>yaver auth</code> auto-registers every installed runner on first sign-in.
            Published to the official MCP registry as <code>io.github.kivanccakmak/yaver</code>. Full
            tool list and HTTP/remote setup:{" "}
            <Link href="/docs/mcp" className="underline hover:text-surface-300">
              MCP guide
            </Link>
            .
          </p>
        </section>

        <section className="mt-10 rounded-2xl border border-surface-800 bg-surface-900 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
            Notes
          </p>
          <div className="mt-4 space-y-3 text-sm leading-6 text-surface-400">
            <p>
              The npm package downloads the agent binary from GitHub Releases on first run and caches
              it under <code>~/.yaver/bin/&lt;version&gt;/</code>. <code>npm install -g yaver-cli@latest</code>
              ships the new shim; the next <code>yaver</code> invocation downloads the matching binary.
            </p>
            <p>
              Older install channels are unsupported. They will not receive future updates and should
              not be used.
            </p>
            <p>
              The rule is simple: install Node however you want, but install and upgrade
              <code> yaver-cli </code>
              only with npm. That keeps one active Yaver binary path per machine.
            </p>
          </div>
          <div className="mt-5 flex flex-wrap gap-3">
            <DownloadButton href="/manuals/cli-setup" primary>
              CLI setup
            </DownloadButton>
            <Link
              href="/manuals/relay-setup"
              className="inline-flex items-center justify-center rounded-xl border border-surface-700 px-4 py-2.5 text-sm font-semibold text-surface-200 transition hover:border-surface-500 hover:text-surface-50"
            >
              Relay setup
            </Link>
          </div>
        </section>
      </div>
    </div>
  );
}
