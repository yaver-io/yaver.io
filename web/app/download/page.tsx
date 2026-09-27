import Image from "next/image";
import Link from "next/link";
import { GUI_DOWNLOADS } from "@/lib/versions";

const card = "rounded-2xl border border-surface-800 bg-surface-900 p-6";
const secondaryButton =
  "inline-flex items-center justify-center rounded-xl border border-surface-700 px-4 py-2.5 text-sm font-semibold text-surface-200 transition hover:border-surface-500 hover:text-surface-50";
const primaryButton =
  "inline-flex items-center justify-center rounded-xl bg-surface-50 px-4 py-2.5 text-sm font-semibold text-surface-950 transition hover:bg-white";

function PlatformCard({
  icon,
  title,
  detail,
  href,
  action = "Download",
  children,
}: {
  icon: string;
  title: string;
  detail: string;
  href: string;
  action?: string;
  children?: React.ReactNode;
}) {
  return (
    <article className={`${card} flex min-h-64 flex-col`}>
      <div className="text-3xl" aria-hidden="true">{icon}</div>
      <h3 className="mt-5 text-xl font-semibold text-surface-50">{title}</h3>
      <p className="mt-2 text-sm leading-6 text-surface-400">{detail}</p>
      <div className="mt-5 flex flex-wrap gap-x-4 gap-y-2 text-xs text-surface-400">{children}</div>
      <a href={href} className={`${primaryButton} mt-auto pt-2`}>
        {action} →
      </a>
    </article>
  );
}

function CommandBlock({ commands }: { commands: string[] }) {
  return (
    <div className="rounded-xl border border-surface-800 bg-surface-950 p-4 font-mono text-[13px] leading-7 text-surface-300">
      {commands.map((command) => (
        <div key={command}>
          <span className="text-surface-600">$</span>{" "}
          <span className="select-all">{command}</span>
        </div>
      ))}
    </div>
  );
}

export default function DownloadPage() {
  return (
    <main className="px-5 py-12 md:px-6 md:py-20">
      <div className="mx-auto max-w-5xl">
        <header className="relative overflow-hidden rounded-2xl border border-surface-800 bg-surface-900 px-6 py-7 md:px-8">
          <div className="absolute inset-0 bg-[radial-gradient(circle_at_top_right,rgba(124,92,255,0.22),transparent_38%),radial-gradient(circle_at_bottom_left,rgba(52,211,153,0.10),transparent_34%)]" />
          <div className="relative flex items-center gap-4">
            <Image src="/icon-192.png" alt="Yaver" width={44} height={44} className="rounded-xl" />
            <div>
              <p className="text-sm font-medium text-violet-300">Yaver</p>
              <h1 className="text-3xl font-semibold tracking-tight text-surface-50">Downloads</h1>
              <p className="mt-1 text-sm text-surface-400">Choose your platform.</p>
            </div>
          </div>
        </header>

        <section className="mt-12" aria-labelledby="desktop-downloads">
          <p className="text-xs font-semibold uppercase tracking-[0.22em] text-violet-400">1 · Desktop</p>
          <div className="mt-2 flex flex-wrap items-end justify-between gap-3">
            <div>
              <h2 id="desktop-downloads" className="text-3xl font-semibold text-surface-50">Get Yaver Desktop</h2>
              <p className="mt-2 text-sm text-surface-400">
                The newest signed desktop release includes the Yaver Go agent. Use this machine as the runtime, or control another paired box.
              </p>
            </div>
            <a href="https://github.com/yaver-io/yaver.io/releases" className="text-sm text-surface-400 underline hover:text-surface-50">
              All desktop releases
            </a>
          </div>

          <div className="mt-6 grid gap-4 md:grid-cols-3">
            <PlatformCard icon="" title="macOS" detail="Signed and notarized DMG for Apple Silicon." href={GUI_DOWNLOADS.macArm64}>
              <a href={GUI_DOWNLOADS.macX64} className="underline hover:text-surface-50">Intel DMG</a>
            </PlatformCard>
            <PlatformCard icon="⊞" title="Windows" detail="Signed, standard-user installer for Windows x64." href={GUI_DOWNLOADS.winX64} />
            <PlatformCard icon="🐧" title="Linux" detail="Ubuntu/Debian x64 package. Installs with apt and appears in your app launcher." href={GUI_DOWNLOADS.debX64} action="Download .deb">
              <a href={GUI_DOWNLOADS.linuxX64} className="underline hover:text-surface-50">x64 AppImage</a>
              <a href={GUI_DOWNLOADS.debArm64} className="underline hover:text-surface-50">arm64 .deb</a>
              <a href={GUI_DOWNLOADS.linuxArm64} className="underline hover:text-surface-50">arm64 AppImage</a>
              <a href={GUI_DOWNLOADS.rpmX64} className="underline hover:text-surface-50">x64 RPM</a>
              <a href={GUI_DOWNLOADS.rpmArm64} className="underline hover:text-surface-50">arm64 RPM</a>
            </PlatformCard>
          </div>
        </section>

        <section className="mt-12" aria-labelledby="mobile-downloads">
          <p className="text-xs font-semibold uppercase tracking-[0.22em] text-emerald-400">2 · Mobile</p>
          <h2 id="mobile-downloads" className="mt-2 text-3xl font-semibold text-surface-50">Add your phone or tablet</h2>
          <div className="mt-6 grid gap-4 md:grid-cols-2">
            <article className={card}>
              <div className="text-3xl" aria-hidden="true">🤖</div>
              <h3 className="mt-4 text-xl font-semibold text-surface-50">Android</h3>
              <p className="mt-2 text-sm leading-6 text-surface-400">Install the latest signed APK directly, scan a QR, or use Google Play.</p>
              <div className="mt-5 flex flex-wrap gap-3">
                <a href="https://download.yaver.io/latest.apk" className={primaryButton}>Download APK</a>
                <a href="https://download.yaver.io" className={secondaryButton}>Show QR</a>
                <a href="https://play.google.com/store/apps/details?id=io.yaver.mobile" className={secondaryButton}>Google Play</a>
              </div>
            </article>
            <article className={card}>
              <div className="text-3xl" aria-hidden="true">📱</div>
              <h3 className="mt-4 text-xl font-semibold text-surface-50">iPhone &amp; iPad</h3>
              <p className="mt-2 text-sm leading-6 text-surface-400">Install from the App Store, then sign in with the same Yaver account.</p>
              <a href="https://apps.apple.com/us/app/yaver-io/id6760467669" className={`${primaryButton} mt-5`}>Open App Store</a>
            </article>
          </div>
        </section>

        <section className={`${card} mt-12`} aria-labelledby="cli-install">
          <p className="text-xs font-semibold uppercase tracking-[0.22em] text-surface-500">3 · CLI or remote box</p>
          <div className="mt-2 grid gap-7 md:grid-cols-[1fr_1.05fr] md:items-center">
            <div>
              <h2 id="cli-install" className="text-2xl font-semibold text-surface-50">One command for the agent</h2>
              <p className="mt-3 text-sm leading-6 text-surface-400">
                Requires Node.js 18+. This installs the verified Go agent and, on a graphical desktop, the desktop GUI. Headless Linux stays CLI-only.
              </p>
              <p className="mt-3 text-sm leading-6 text-emerald-300">
                Already use OpenCode with DeepSeek? Yaver keeps your provider and model settings, adds only its MCP connection, and uses that existing runner.
              </p>
            </div>
            <CommandBlock commands={["npm install -g yaver-cli", "yaver auth", "yaver serve"]} />
          </div>
        </section>

        <section className="mt-6 grid gap-4 md:grid-cols-2">
          <article className={card}>
            <h2 className="text-xl font-semibold text-surface-50">SSH-only Linux / VPS</h2>
            <p className="mt-2 text-sm leading-6 text-surface-400">Pair without opening a browser on the server, then keep Yaver running after reboot.</p>
            <div className="mt-5">
              <CommandBlock commands={["npm install -g yaver-cli", "yaver auth --headless", "yaver serve --install-systemd"]} />
            </div>
          </article>
          <article className={card}>
            <h2 className="text-xl font-semibold text-surface-50">Runner integration</h2>
            <p className="mt-2 text-sm leading-6 text-surface-400">Yaver detects installed Claude Code, Codex, or OpenCode. To refresh one connection manually:</p>
            <div className="mt-5">
              <CommandBlock commands={["yaver mcp setup opencode"]} />
            </div>
            <Link href="/docs/mcp" className="mt-4 inline-block text-sm text-surface-300 underline hover:text-surface-50">MCP guide</Link>
          </article>
        </section>

        <section className={`${card} mt-12`} aria-labelledby="raspberry-pi">
          <div className="grid gap-7 md:grid-cols-[1fr_1.05fr] md:items-center">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.22em] text-surface-500">Raspberry Pi</p>
              <h2 id="raspberry-pi" className="mt-2 text-2xl font-semibold text-surface-50">Turn a Pi into a Yaver node</h2>
              <p className="mt-3 text-sm leading-6 text-surface-400">Use Raspberry Pi OS 64-bit on Pi 4/5, install Node.js 18+, then run the same headless setup.</p>
              <Link href="/manuals/raspberry-pi" className={`${secondaryButton} mt-5`}>Raspberry Pi guide</Link>
            </div>
            <CommandBlock commands={["npm install -g yaver-cli", "yaver auth --headless", "yaver serve --install-systemd"]} />
          </div>
        </section>
      </div>
    </main>
  );
}
