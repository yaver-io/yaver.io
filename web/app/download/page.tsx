import Image from "next/image";
import Link from "next/link";
import {
  DOWNLOAD_SLUGS,
  fetchClientConfig,
  fetchDownloads,
  findDownload,
  formatFileSize,
} from "@/lib/downloads";

const GITHUB_RELEASE = "https://github.com/kivanccakmak/yaver.io/releases/latest";
const APT_REPO = "https://raw.githubusercontent.com/kivanccakmak/apt-yaver/main";

const linuxArtifacts = [
  {
    title: "Linux ARM64 AppImage",
    description: "Portable desktop app for ARM64 Linux. No package manager required.",
    slug: "linux-appimage-arm64" as const,
    fallbackLabel: "arm64",
    installHint: "chmod +x Yaver-arm64.AppImage && ./Yaver-arm64.AppImage",
  },
  {
    title: "Linux ARM64 .deb",
    description: "Native Debian and Ubuntu package for ARM64 Linux machines.",
    slug: "linux-deb-arm64" as const,
    fallbackLabel: "arm64",
    installHint: "sudo dpkg -i yaver_*_arm64.deb  # or: sudo apt install ./yaver_*_arm64.deb",
  },
  {
    title: "Linux ARM64 .rpm",
    description: "Fedora, RHEL, openSUSE — CLI package for aarch64 machines.",
    slug: "linux-rpm-arm64" as const,
    fallbackLabel: "arm64",
    installHint: "sudo rpm -i yaver_*_aarch64.rpm  # or: sudo dnf install ./yaver_*_aarch64.rpm",
  },
  {
    title: "Linux ARM64 tarball",
    description: "Raw binary tarball for any ARM64 Linux. Unzip and move to PATH.",
    slug: "linux-tarball-arm64" as const,
    fallbackLabel: "arm64",
    installHint: "tar xzf yaver-*-linux-arm64.tar.gz && sudo mv yaver-linux-arm64 /usr/local/bin/yaver",
  },
  {
    title: "Linux x64 AppImage",
    description: "Portable desktop app. No package manager required.",
    slug: "linux-appimage-amd64" as const,
    fallbackLabel: "x64 fallback",
    installHint: "chmod +x Yaver-amd64.AppImage && ./Yaver-amd64.AppImage",
  },
  {
    title: "Linux x64 .deb",
    description: "Native Debian and Ubuntu package for direct install.",
    slug: "linux-deb-amd64" as const,
    fallbackLabel: "x64 fallback",
    installHint: "sudo dpkg -i yaver_*_amd64.deb  # or: sudo apt install ./yaver_*_amd64.deb",
  },
  {
    title: "Linux x64 .rpm",
    description: "Fedora, RHEL, openSUSE — CLI package for x86_64 machines.",
    slug: "linux-rpm-amd64" as const,
    fallbackLabel: "x64 fallback",
    installHint: "sudo rpm -i yaver_*_x86_64.rpm  # or: sudo dnf install ./yaver_*_x86_64.rpm",
  },
  {
    title: "Linux x64 tarball",
    description: "Raw binary tarball for any x86_64 Linux. Unzip and move to PATH.",
    slug: "linux-tarball-amd64" as const,
    fallbackLabel: "x64 fallback",
    installHint: "tar xzf yaver-*-linux-amd64.tar.gz && sudo mv yaver-linux-amd64 /usr/local/bin/yaver",
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
  const [downloadsResult, configResult] = await Promise.allSettled([
    fetchDownloads(),
    fetchClientConfig(),
  ]);
  const downloads = downloadsResult.status === "fulfilled" ? downloadsResult.value : [];
  const config = configResult.status === "fulfilled" ? configResult.value : {};
  const cliVersion = config.cliVersion;
  const armAppImage = findDownload(downloads, DOWNLOAD_SLUGS["linux-appimage-arm64"]);
  const x64AppImage = findDownload(downloads, DOWNLOAD_SLUGS["linux-appimage-amd64"]);
  const armDeb = findDownload(downloads, DOWNLOAD_SLUGS["linux-deb-arm64"]);
  const x64Deb = findDownload(downloads, DOWNLOAD_SLUGS["linux-deb-amd64"]);
  const appImageSlug = armAppImage ? "linux-appimage-arm64" : "linux-appimage-amd64";
  const appImageName = armAppImage ? "Yaver-arm64.AppImage" : "Yaver-amd64.AppImage";
  const debSlug = armDeb ? "linux-deb-arm64" : "linux-deb-amd64";
  const debName = armDeb ? "yaver-arm64.deb" : "yaver-amd64.deb";
  const commandBlocks = [
    {
      label: "Fastest start (npm bootstrap)",
      commands: [
        "npm install -g yaver-cli",
        "yaver auth",
        "yaver serve",
        "# same install also supports: yaver push",
      ],
    },
    {
      label: "apt (Debian / Ubuntu)",
      commands: [
        "curl -fsSL https://raw.githubusercontent.com/kivanccakmak/apt-yaver/main/KEY.gpg | sudo gpg --dearmor -o /usr/share/keyrings/yaver.gpg",
        'echo "deb [signed-by=/usr/share/keyrings/yaver.gpg] https://raw.githubusercontent.com/kivanccakmak/apt-yaver/main ./ stable main" | sudo tee /etc/apt/sources.list.d/yaver.list',
        "sudo apt update && sudo apt install yaver",
      ],
    },
    {
      label: "AppImage quick start",
      commands: [
        `curl -L https://yaver.io/download/${appImageSlug} -o ${appImageName}`,
        `chmod +x ${appImageName}`,
        `./${appImageName}`,
      ],
    },
    {
      label: "Native CLI on Linux",
      commands: [
        "brew install kivanccakmak/yaver/yaver",
        "yaver auth",
        "yaver serve",
      ],
    },
    {
      label: "One-liner (any Linux, auto-detect arch)",
      commands: [
        "curl -fsSL https://yaver.io/install.sh | sh",
        "yaver auth && yaver serve",
      ],
    },
    {
      label: "dnf / rpm (Fedora / RHEL / openSUSE)",
      commands: [
        `curl -L https://yaver.io/download/linux-rpm-amd64 -o yaver.rpm`,
        "sudo dnf install ./yaver.rpm  # or: sudo rpm -i yaver.rpm",
      ],
    },
    {
      label: "dpkg (offline .deb install)",
      commands: [
        `curl -L https://yaver.io/download/${debSlug} -o ${debName}`,
        `sudo dpkg -i ${debName}`,
        "sudo apt-get install -f  # resolve any missing deps",
      ],
    },
  ];

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
                Fastest start: `npm install -g yaver-cli`. It installs the `yaver` command for both
                the Go agent and third-party React Native push-to-device. Native package-manager and
                direct-download paths are still available for Linux, macOS, or WSL.
              </p>
              <p className="mt-3 max-w-2xl text-sm leading-6 text-surface-500">
                Use package-manager installs when you want upgrades. Use direct artifacts when you just need a file fast.
              </p>
              <div className="mt-6 flex flex-wrap gap-3">
                <DownloadButton href={`/download/${debSlug}`} primary>
                  Linux
                </DownloadButton>
                <DownloadButton href="/download/macos-arm64">
                  macOS
                </DownloadButton>
                <DownloadButton href="#wsl">
                  WSL
                </DownloadButton>
              </div>
            </div>

            <div className="rounded-[1.5rem] border border-surface-800 bg-surface-950/80 p-5">
              <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
                Main paths
              </p>
              <div className="mt-4 space-y-4">
                <div className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">Fastest start</p>
                  <p className="mt-1 text-sm text-surface-400">
                    Use `npm install -g yaver-cli` when you want one install that covers `yaver serve`
                    and `yaver push`.
                  </p>
                </div>
                <div className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">Linux</p>
                  <p className="mt-1 text-sm text-surface-400">
                    Use `apt`, the install script, or a direct artifact when Yaver runs on the machine itself.
                  </p>
                </div>
                <div className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">macOS</p>
                  <p className="mt-1 text-sm text-surface-400">
                    Homebrew is the fast path when you want the Go agent on a Mac.
                  </p>
                </div>
                <div id="wsl" className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">WSL</p>
                  <p className="mt-1 text-sm text-surface-400">
                    Use the CLI inside WSL. Authenticate through Windows and load Hermes builds into Yaver on your phone.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section className="mt-10 grid gap-5 md:grid-cols-3">
          {linuxArtifacts.map((artifact) => {
            const resolved = findDownload(downloads, DOWNLOAD_SLUGS[artifact.slug]);
            const size = resolved ? formatFileSize(resolved.size) : null;
            return (
              <div key={artifact.slug} className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
                <div className="flex items-center justify-between gap-4">
                  <h2 className="text-lg font-semibold text-surface-50">{artifact.title}</h2>
                  <span className="rounded-full border border-surface-700 px-2.5 py-1 text-[11px] font-medium text-surface-400">
                    {resolved?.version ? `v${resolved.version}` : artifact.fallbackLabel}
                  </span>
                </div>
                <p className="mt-3 text-sm leading-6 text-surface-400">{artifact.description}</p>
                <p className="mt-3 text-xs text-surface-500">
                  {size ? `${size} • ` : ""}
                  {resolved?.filename ?? "Redirects to latest release artifact"}
                </p>
                <div className="mt-5 flex flex-wrap gap-3">
                  <DownloadButton href={`/download/${artifact.slug}`} primary>
                    Direct download
                  </DownloadButton>
                  {resolved?.url ? (
                    <DownloadButton href={resolved.url}>Storage URL</DownloadButton>
                  ) : (
                    <DownloadButton href={GITHUB_RELEASE}>Release page</DownloadButton>
                  )}
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

        <section className="mt-10 grid gap-5 lg:grid-cols-[1.15fr_0.85fr]">
          <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
            <div className="flex items-center justify-between gap-4">
              <div>
                <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
                  apt repository
                </p>
                <h2 className="mt-2 text-2xl font-semibold text-surface-50">
                  Install Yaver with `apt`
                </h2>
              </div>
              {cliVersion ? (
                <span className="rounded-full border border-surface-700 px-3 py-1 text-xs font-medium text-surface-400">
                  CLI v{cliVersion}
                </span>
              ) : null}
            </div>
            <p className="mt-3 max-w-2xl text-sm leading-6 text-surface-400">
              This path is for Debian and Ubuntu machines where you want `sudo apt install yaver`
              and normal package upgrades later.
            </p>
            {armDeb || x64Deb ? null : (
              <p className="mt-3 text-sm leading-6 text-amber-300">
                Direct `.deb` downloads are not published right now, so the button above falls back to the release page.
              </p>
            )}
            <div className="mt-6 space-y-4">
              <CommandCard key={commandBlocks[0].label} label={commandBlocks[0].label} commands={commandBlocks[0].commands} />
              <CommandCard key={commandBlocks[5].label} label={commandBlocks[5].label} commands={commandBlocks[5].commands} />
              <CommandCard key={commandBlocks[4].label} label={commandBlocks[4].label} commands={commandBlocks[4].commands} />
              <CommandCard key={commandBlocks[1].label} label={commandBlocks[1].label} commands={commandBlocks[1].commands} />
            </div>
            <p className="mt-4 text-xs text-surface-500">
              Repo source:{" "}
              <a
                href={APT_REPO}
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
              >
                {APT_REPO}
              </a>
            </p>
          </div>

          <div className="space-y-5">
            <CommandCard label={commandBlocks[3].label} commands={commandBlocks[3].commands} />
            <CommandCard
              label="CLI on macOS"
              commands={[
                "brew install kivanccakmak/yaver/yaver",
                "yaver auth",
                "yaver serve",
              ]}
            />

            <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
              <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
                WSL
              </p>
              <h2 className="mt-2 text-xl font-semibold text-surface-50">
                Use Yaver from Windows Subsystem for Linux
              </h2>
              <p className="mt-3 text-sm leading-6 text-surface-400">
                Run the Go agent inside WSL, let `yaver auth` hand browser sign-in off to Windows,
                and use the Yaver mobile app on your phone for Hermes reload.
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
                  <span>Build Hermes in WSL, then load it into Yaver on your phone.</span>
                </div>
              </div>
              <p className="mt-4 text-xs leading-6 text-surface-500">
                WSL is for the agent and CLI path. Use the Windows browser for auth and the mobile app
                as the runtime container on the phone.
              </p>
            </div>

            <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
              <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
                Notes
              </p>
              <div className="mt-4 space-y-3 text-sm leading-6 text-surface-400">
                <p>
                  If one Linux artifact fails on your machine, try the other packaging format first.
                  AppImage is usually the least fragile across distros.
                </p>
                <p>
                  If you only need the agent binary and not the desktop shell, the CLI install path is
                  lighter than the Electron app.
                </p>
                <p>
                  On Windows Subsystem for Linux, use the CLI path inside WSL rather than the Linux
                  desktop AppImage. `yaver auth` now hands browser sign-in off to Windows when possible.
                </p>
                <p>
                  Static docs and direct links now point at stable public routes under{" "}
                  <code>/download/...</code>, not one-off storage URLs.
                </p>
              </div>
            </div>

            <div className="rounded-2xl border border-surface-800 bg-surface-900 p-6">
              <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
                Other links
              </p>
              <p className="mt-3 text-sm leading-6 text-surface-400">
                Linux, macOS, and WSL are the main paths here. Keep the rest secondary.
              </p>
              <div className="mt-4 flex flex-wrap gap-3">
                <DownloadButton href="/download/macos-arm64">macOS</DownloadButton>
                <DownloadButton href={`/download/${debSlug}`}>Linux</DownloadButton>
                <Link
                  href="/manuals/cli-setup"
                  className="inline-flex items-center justify-center rounded-xl border border-surface-700 px-4 py-2.5 text-sm font-semibold text-surface-200 transition hover:border-surface-500 hover:text-surface-50"
                >
                  WSL / CLI setup
                </Link>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}
