import Image from "next/image";
import Link from "next/link";
import {
  DOWNLOAD_SLUGS,
  fetchDownloadFallbacks,
  fetchDownloads,
  findDownload,
  formatFileSize,
  type DownloadSlug,
} from "@/lib/downloads";

const directArtifacts = [
  {
    title: "Linux x64 tarball",
    description: "Raw amd64 agent binary tarball for Linux hosts.",
    slug: "linux-tarball-amd64" as const,
    installHint: "tar xzf yaver-*-linux-amd64.tar.gz && sudo mv yaver-linux-amd64 /usr/local/bin/yaver",
  },
  {
    title: "Linux ARM64 tarball",
    description: "Raw arm64 agent binary tarball for Linux hosts.",
    slug: "linux-tarball-arm64" as const,
    installHint: "tar xzf yaver-*-linux-arm64.tar.gz && sudo mv yaver-linux-arm64 /usr/local/bin/yaver",
  },
  {
    title: "macOS Apple Silicon tarball",
    description: "Direct agent binary archive for Apple Silicon Macs.",
    slug: "macos-arm64" as const,
    installHint: "tar xzf yaver-*-darwin-arm64.tar.gz && sudo mv yaver-darwin-arm64 /usr/local/bin/yaver",
  },
  {
    title: "macOS Intel tarball",
    description: "Direct agent binary archive for Intel Macs.",
    slug: "macos-x64" as const,
    installHint: "tar xzf yaver-*-darwin-amd64.tar.gz && sudo mv yaver-darwin-amd64 /usr/local/bin/yaver",
  },
];

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

export default async function DownloadPage() {
  const [downloadsResult, fallbacksResult] = await Promise.allSettled([
    fetchDownloads(),
    fetchDownloadFallbacks(),
  ]);
  const downloads = downloadsResult.status === "fulfilled" ? downloadsResult.value : [];
  const fallbacks = fallbacksResult.status === "fulfilled" ? fallbacksResult.value : {};

  function resolveArtifact(slug: DownloadSlug) {
    const storage = findDownload(downloads, DOWNLOAD_SLUGS[slug]);
    const fallback = fallbacks[slug];
    return {
      href: storage?.url ?? fallback?.href ?? "/download",
      filename: storage?.filename ?? fallback?.filename ?? "Open download page",
      size: storage?.size ?? fallback?.size ?? 0,
      version: storage?.version ?? fallback?.version,
      direct: Boolean(storage?.url) || Boolean(fallback?.direct),
    };
  }

  return (
    <div className="px-6 py-16 md:py-20">
      <div className="mx-auto max-w-6xl">
        <section className="relative overflow-hidden rounded-[2rem] border border-surface-800 bg-surface-900 px-6 py-10 md:px-10 md:py-12">
          <div className="absolute inset-0 bg-[radial-gradient(circle_at_top_right,rgba(255,255,255,0.10),transparent_32%),radial-gradient(circle_at_bottom_left,rgba(255,255,255,0.06),transparent_28%)]" />
          <div className="relative grid gap-8 md:grid-cols-[1.4fr_0.9fr] md:items-center">
            <div>
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
                Install the Yaver agent without guessing.
              </h1>
              <p className="mt-4 max-w-2xl text-sm leading-6 text-surface-400 md:text-base">
                The fastest path is the npm bootstrap, and the native package-manager routes are available
                too. I verified the current Linux flow on a fresh Ubuntu host with both
                <code> npm install -g yaver-cli</code> and <code>apt install yaver</code>.
              </p>
              <p className="mt-3 max-w-2xl text-sm leading-6 text-surface-500">
                Native Windows is not a main path here. Use WSL2 on Windows, and use the Yaver mobile app
                on your phone for the Hermes runtime container.
              </p>
              <div className="mt-6 flex flex-wrap gap-3">
                <DownloadButton href="#npm" primary>
                  npm bootstrap
                </DownloadButton>
                <DownloadButton href="#script">
                  Install script
                </DownloadButton>
                <DownloadButton href="#packages">
                  Package managers
                </DownloadButton>
                <DownloadButton href="#tarballs">
                  Direct tarballs
                </DownloadButton>
                <DownloadButton href="#wsl">
                  WSL2
                </DownloadButton>
              </div>
            </div>

            <div className="rounded-[1.5rem] border border-surface-800 bg-surface-950/80 p-5">
              <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
                Verified paths
              </p>
              <div className="mt-4 space-y-4">
                <div className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">npm bootstrap</p>
                  <p className="mt-1 text-sm text-surface-400">
                    Use <code>npm install -g yaver-cli</code>. It installs a single <code>yaver</code> command
                    that covers both agent startup and third-party React Native push flows.
                  </p>
                </div>
                <div className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">apt / brew / package managers</p>
                  <p className="mt-1 text-sm text-surface-400">
                    apt works from a CDN-backed repo, Homebrew stays native, and Scoop/Winget/Chocolatey remain
                    available as secondary package-manager surfaces.
                  </p>
                </div>
                <div id="wsl" className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">WSL2 only</p>
                  <p className="mt-1 text-sm text-surface-400">
                    Run Yaver inside WSL2, let <code>yaver auth</code> hand browser sign-in off to Windows,
                    and use the Yaver mobile app on the phone. The WSL path is Hermes bundle reload, not Xcode.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section id="npm" className="mt-10 grid gap-5 lg:grid-cols-[1.15fr_0.85fr]">
          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
              Recommended
            </p>
            <h2 className="mt-2 text-2xl font-semibold text-surface-50">
              Install with npm
            </h2>
            <p className="mt-3 max-w-2xl text-sm leading-6 text-surface-400">
              This is the shortest onboarding path now. It gives you a single <code>yaver</code> command for
              the Go agent plus the React Native push/injection flow.
            </p>
            <div className="mt-6 space-y-4">
              <CommandCard
                label="npm bootstrap"
                commands={[
                  "npm install -g yaver-cli",
                  "yaver auth",
                  "yaver serve",
                ]}
              />
            </div>
          </div>

          <div className="space-y-5">
            <div id="script" className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
              <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
                No npm required
              </p>
              <h2 className="mt-2 text-xl font-semibold text-surface-50">
                Install with the auto-detect script
              </h2>
              <p className="mt-3 text-sm leading-6 text-surface-400">
                Use the shell installer when you want the matching tarball and a local <code>yaver</code>
                binary without setting up npm first.
              </p>
              <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[13px] text-surface-300">
                <div className="mb-2">
                  <span className="text-surface-500">$</span>{" "}
                  <span className="select-all">curl -fsSL https://yaver.io/install.sh | sh</span>
                </div>
                <div>
                  <span className="text-surface-500">$</span>{" "}
                  <span className="select-all">yaver serve</span>
                </div>
              </div>
            </div>

            <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
              <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
                WSL2
              </p>
              <h2 className="mt-2 text-xl font-semibold text-surface-50">
                Use Yaver from Windows Subsystem for Linux
              </h2>
              <p className="mt-3 text-sm leading-6 text-surface-400">
                WSL2 is the supported Windows-hosted path. The right model is{" "}
                <code>WSL -&gt; Hermes bundle -&gt; Yaver mobile app</code>.
              </p>
              <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[13px] text-surface-300">
                <div className="mb-2">
                  <span className="text-surface-500">$</span>{" "}
                  <span className="select-all">curl -fsSL https://yaver.io/install.sh | sh</span>
                </div>
                <div className="mb-2">
                  <span className="text-surface-500">$</span>{" "}
                  <span className="select-all">yaver auth</span>
                </div>
                <div className="mb-2">
                  <span className="text-surface-500">$</span>{" "}
                  <span className="select-all">yaver serve</span>
                </div>
                <div>
                  <span className="text-surface-500">#</span>{" "}
                  <span>Open the project in Yaver on the phone. WSL uses the Hermes bundle path, not Xcode.</span>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section id="packages" className="mt-10 grid gap-5 md:grid-cols-2">
          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <h2 className="text-lg font-semibold text-surface-50">apt</h2>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              Debian and Ubuntu can install Yaver from the CDN-backed apt repository.
            </p>
            <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[13px] text-surface-300">
              <div className="mb-2">
                <span className="text-surface-500">$</span>{" "}
                <span className="select-all">{`echo "deb [arch=$(dpkg --print-architecture) trusted=yes] https://cdn.jsdelivr.net/gh/kivanccakmak/apt-yaver@main stable main" | sudo tee /etc/apt/sources.list.d/yaver.list`}</span>
              </div>
              <div className="mb-2">
                <span className="text-surface-500">$</span>{" "}
                <span className="select-all">sudo apt update</span>
              </div>
              <div>
                <span className="text-surface-500">$</span>{" "}
                <span className="select-all">sudo apt install yaver</span>
              </div>
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <h2 className="text-lg font-semibold text-surface-50">Other package managers</h2>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              WSL2 is still the real Windows-hosted dev path, but these package-manager entries are live too.
            </p>
            <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[13px] text-surface-300">
              <div className="mb-2">
                <span className="text-surface-500">&gt;</span>{" "}
                <span className="select-all">winget install Yaver.Yaver</span>
              </div>
              <div className="mb-2">
                <span className="text-surface-500">&gt;</span>{" "}
                <span className="select-all">choco install yaver</span>
              </div>
              <div>
                <span className="text-surface-500">&gt;</span>{" "}
                <span className="select-all">scoop bucket add yaver https://github.com/kivanccakmak/scoop-yaver && scoop install yaver</span>
              </div>
            </div>
          </div>
        </section>

        <section id="raspi" className="mt-10 rounded-2xl border border-surface-800 bg-surface-900 p-6">
          <div className="flex items-center justify-between gap-4">
            <h2 className="text-lg font-semibold text-surface-50">Raspberry Pi / ARM64 home server</h2>
            <span className="rounded-full border border-emerald-700/50 bg-emerald-950/40 px-2.5 py-1 text-[11px] font-medium text-emerald-300">
              always-on host
            </span>
          </div>
          <p className="mt-3 text-sm leading-6 text-surface-400">
            Run <code>yaver serve</code> on a Raspberry Pi 4 (4+ GB) or any arm64 Linux SBC so the phone has a
            24/7 target for Hermes bundle push, project hosting, and relay roaming. The Pi compiles
            React Native bundles natively (hermesc is arm64-capable) and serves them back to the
            Yaver mobile app — no Mac needed to iterate on JS.
          </p>
          <p className="mt-3 text-xs text-surface-500">
            First Metro bundle on a Pi 4: ~30–60s. Hot reloads: under 2s. Use <code>yaver install node</code>
            once if Node isn&apos;t already on the Pi; the agent auto-installs a sudo-free LTS into <code>~/.yaver/runtimes/node</code>.
            {" "}
            Full walkthrough (hardware, headless OAuth, power-on after outage, disabling
            WiFi/HDMI/Bluetooth power save):{" "}
            <Link href="/manuals/raspberry-pi" className="underline hover:text-surface-300">
              Raspberry Pi manual
            </Link>
            .
          </p>
          <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">curl -fsSL https://yaver.io/install.sh | sh</span></div>
            <div className="mb-2 text-surface-500"># or direct tarball (arm64):</div>
            <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">tar xzf yaver-*-linux-arm64.tar.gz && sudo mv yaver-linux-arm64 /usr/local/bin/yaver</span></div>
            <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">yaver auth</span></div>
            <div><span className="text-surface-500">$</span> <span className="select-all">yaver serve --install-systemd  # survives reboots</span></div>
          </div>
        </section>

        <section id="docker" className="mt-10 rounded-2xl border border-surface-800 bg-surface-900 p-6">
          <div className="flex items-center justify-between gap-4">
            <h2 className="text-lg font-semibold text-surface-50">Docker image (multi-arch)</h2>
            <span className="rounded-full border border-surface-700 px-2.5 py-1 text-[11px] font-medium text-surface-400">
              linux/amd64 · linux/arm64
            </span>
          </div>
          <p className="mt-3 text-sm leading-6 text-surface-400">
            Prebuilt image for x86-64, Apple Silicon (via Docker Desktop), and arm64 (Raspberry Pi,
            AWS Graviton, Oracle ARM free tier). Pulls on every arch without changing the tag.
          </p>
          <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-2 text-surface-500"># Docker Hub:</div>
            <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">docker pull kivanccakmak/yaver-cli:latest</span></div>
            <div className="mb-2 text-surface-500"># GitHub Container Registry (same image):</div>
            <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">docker pull ghcr.io/kivanccakmak/yaver.io/cli:latest</span></div>
            <div className="mb-2 text-surface-500"># Run the agent with your auth token mounted:</div>
            <div><span className="text-surface-500">$</span> <span className="select-all">docker run --rm -v ~/.yaver:/root/.yaver -p 18080:18080 kivanccakmak/yaver-cli serve</span></div>
          </div>
        </section>

        <section id="headless-auth" className="mt-10 rounded-2xl border border-surface-800 bg-surface-900 p-6">
          <div className="flex items-center justify-between gap-4">
            <h2 className="text-lg font-semibold text-surface-50">Headless sign-in</h2>
            <span className="rounded-full border border-indigo-700/50 bg-indigo-950/40 px-2.5 py-1 text-[11px] font-medium text-indigo-300">
              Pi · VPS · SSH-only
            </span>
          </div>
          <p className="mt-3 text-sm leading-6 text-surface-400">
            Every install surface supports <code>yaver auth --headless</code>: the agent prints a short
            code + URL, you open the URL on any device that has a browser (phone, laptop), sign in
            with any supported provider, and the Pi / VPS picks up the token automatically. No
            browser on the target machine required.
          </p>
          <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-2 text-surface-500"># on the Pi / VPS / Docker container:</div>
            <div className="mb-2"><span className="text-surface-500">$</span> <span className="select-all">yaver auth --headless</span></div>
            <div className="mb-1 text-surface-500">Go to https://yaver.io/auth/device?code=XXXX-YYYY</div>
            <div className="mb-2 text-surface-500">Approve there; this machine will pick up the token.</div>
            <div className="mb-2 text-surface-500"># waits and polls automatically — resumable across restarts</div>
          </div>
          <p className="mt-4 text-sm font-semibold text-surface-200">Sign-in providers</p>
          <div className="mt-3 flex flex-wrap gap-2">
            {[
              { name: "Google (Gmail)", emoji: "\u{1F4E7}" },
              { name: "Apple", emoji: "\uF8FF" },
              { name: "Microsoft / O365", emoji: "\u{1F5C2}" },
              { name: "GitHub", emoji: "\u{1F431}" },
              { name: "GitLab", emoji: "\u{1F98A}" },
              { name: "Discord", emoji: "\u{1F3AE}" },
              { name: "Slack", emoji: "\u{1F4AC}" },
              { name: "Email / password", emoji: "\u2709" },
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
            Linking multiple identities to the same account is supported on web and mobile — one Yaver
            account can hold any combination of these, and merging two accounts you made by accident
            is available too. See{" "}
            <Link href="/manuals/account-linking" className="underline hover:text-surface-300">
              account linking
            </Link>
            .
          </p>
        </section>

        <section id="tarballs" className="mt-10 grid gap-5 md:grid-cols-2">
          {directArtifacts.map((artifact) => {
            const resolved = resolveArtifact(artifact.slug);
            const size = formatFileSize(resolved.size);
            return (
              <div key={artifact.slug} className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
                <div className="flex items-center justify-between gap-4">
                  <h2 className="text-lg font-semibold text-surface-50">{artifact.title}</h2>
                  <span className="rounded-full border border-surface-700 px-2.5 py-1 text-[11px] font-medium text-surface-400">
                    {resolved.version ? `v${resolved.version}` : "tarball"}
                  </span>
                </div>
                <p className="mt-3 text-sm leading-6 text-surface-400">{artifact.description}</p>
                <p className="mt-3 text-xs text-surface-500">
                  {size ? `${size} • ` : ""}
                  {resolved.filename}
                </p>
                <div className="mt-5 flex flex-wrap gap-3">
                  <DownloadButton href={`/download/${artifact.slug}`} primary>
                    Stable route
                  </DownloadButton>
                  <DownloadButton href={resolved.href}>
                    {resolved.direct ? "Direct file" : "Download page"}
                  </DownloadButton>
                </div>
                <div className="mt-5 rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
                  <div>
                    <span className="text-surface-500">$</span>{" "}
                    <span className="select-all">{artifact.installHint}</span>
                  </div>
                </div>
              </div>
            );
          })}
        </section>

        <section className="mt-10 rounded-2xl border border-surface-800 bg-surface-900 p-6">
          <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
            Notes
          </p>
          <div className="mt-4 space-y-3 text-sm leading-6 text-surface-400">
            <p>
              The download routes above are restricted to exact agent tarballs so <code>/download/...</code> does not
              silently hand back HTML when a package file is missing.
            </p>
            <p>
              If you only need a Linux or macOS agent quickly, use the install script. If you need a specific file for
              packaging or mirroring, use the tarball routes.
            </p>
            <p>
              WSL2 is for the agent and CLI path. Use the Windows browser for auth and the Yaver mobile app on your
              phone as the runtime container.
            </p>
          </div>
          <div className="mt-5 flex flex-wrap gap-3">
            <Link
              href="/manuals/cli-setup"
              className="inline-flex items-center justify-center rounded-xl border border-surface-700 px-4 py-2.5 text-sm font-semibold text-surface-200 transition hover:border-surface-500 hover:text-surface-50"
            >
              CLI setup
            </Link>
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
