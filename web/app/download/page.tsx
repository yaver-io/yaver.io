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
    title: "Linux AppImage",
    description: "Portable desktop app. No package manager required.",
    slug: "linux-appimage-amd64" as const,
    fallbackLabel: "amd64",
    installHint: "chmod +x Yaver-amd64.AppImage && ./Yaver-amd64.AppImage",
  },
  {
    title: "Linux .deb",
    description: "Native Debian and Ubuntu package for direct install.",
    slug: "linux-deb-amd64" as const,
    fallbackLabel: "amd64",
    installHint: "sudo apt install ./yaver-amd64.deb",
  },
  {
    title: "Linux ARM64 .deb",
    description: "Debian and Ubuntu package for ARM64 Linux machines.",
    slug: "linux-deb-arm64" as const,
    fallbackLabel: "arm64",
    installHint: "sudo apt install ./yaver-arm64.deb",
  },
];

const commandBlocks = [
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
      "curl -L https://yaver.io/download/linux-appimage-amd64 -o Yaver-amd64.AppImage",
      "chmod +x Yaver-amd64.AppImage",
      "./Yaver-amd64.AppImage",
    ],
  },
  {
    label: "CLI on Linux",
    commands: [
      "brew install kivanccakmak/yaver/yaver",
      "yaver auth",
      "yaver serve",
    ],
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
                  Linux installs that actually work
                </span>
              </div>
              <h1 className="max-w-3xl text-4xl font-bold tracking-tight text-surface-50 md:text-5xl">
                Download Yaver for Linux without guessing.
              </h1>
              <p className="mt-4 max-w-2xl text-sm leading-6 text-surface-400 md:text-base">
                Use the packaged desktop app, the portable AppImage, or the CLI.
                The direct buttons below resolve to storage-backed artifacts first,
                with GitHub releases as fallback.
              </p>
              <div className="mt-6 flex flex-wrap gap-3">
                <DownloadButton href="/download/linux-appimage-amd64" primary>
                  Download AppImage
                </DownloadButton>
                <DownloadButton href="/download/linux-deb-amd64">
                  Download .deb
                </DownloadButton>
                <DownloadButton href={GITHUB_RELEASE}>
                  GitHub Releases
                </DownloadButton>
              </div>
            </div>

            <div className="rounded-[1.5rem] border border-surface-800 bg-surface-950/80 p-5">
              <p className="text-xs font-semibold uppercase tracking-[0.2em] text-surface-500">
                Recommended path
              </p>
              <div className="mt-4 space-y-4">
                <div className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">Ubuntu / Debian</p>
                  <p className="mt-1 text-sm text-surface-400">
                    Use the apt repo if you want upgrades through your package manager.
                  </p>
                </div>
                <div className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">Any distro</p>
                  <p className="mt-1 text-sm text-surface-400">
                    Use the AppImage if you just want a single file and no system install.
                  </p>
                </div>
                <div className="rounded-xl border border-surface-800 bg-surface-900/80 p-4">
                  <p className="text-sm font-semibold text-surface-100">CLI only</p>
                  <p className="mt-1 text-sm text-surface-400">
                    Homebrew on Linux still works if you only need the `yaver` command.
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
            <div className="mt-6 space-y-4">
              {commandBlocks.slice(0, 2).map((block) => (
                <CommandCard key={block.label} label={block.label} commands={block.commands} />
              ))}
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
            <CommandCard label={commandBlocks[2].label} commands={commandBlocks[2].commands} />

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
                Need another platform
              </p>
              <p className="mt-3 text-sm leading-6 text-surface-400">
                macOS, Windows, mobile, and package-manager installs still live here too.
              </p>
              <div className="mt-4 flex flex-wrap gap-3">
                <DownloadButton href="/download/macos-arm64">macOS</DownloadButton>
                <DownloadButton href="/download/windows-x64">Windows</DownloadButton>
                <Link
                  href="/manuals/cli-setup"
                  className="inline-flex items-center justify-center rounded-xl border border-surface-700 px-4 py-2.5 text-sm font-semibold text-surface-200 transition hover:border-surface-500 hover:text-surface-50"
                >
                  CLI setup
                </Link>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}
