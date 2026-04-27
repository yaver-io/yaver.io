import Link from "next/link";

const manuals = [
  {
    title: "Code from the Beach \u2014 Remote Build, Test & Deploy",
    description:
      "Develop from your phone, build on your machine, test automatically, and deploy to your phone, TestFlight, or Play Store \u2014 all over encrypted P2P connections.",
    href: "/manuals/code-from-beach",
    tags: ["Build", "Test", "Deploy", "Flutter", "Mobile"],
    featured: true,
  },
  {
    title: "Visual Feedback Loop \u2014 Bug Reports from Your Phone",
    description:
      "Record your screen, narrate the bug, send it to your AI agent \u2014 and get a fix without typing a line of code. Screen recordings, voice notes, and device info flow P2P to your dev machine where the agent turns them into tasks.",
    href: "/manuals/feedback-loop",
    tags: ["Feedback", "Testing", "SDK", "Screen Recording"],
    featured: true,
  },
  {
    title: "Raspberry Pi \u2014 Plug-and-Play Yaver Home Server",
    description:
      "Turn a Pi 4 or Pi 5 into an always-on Yaver target. Hardware picks, headless OAuth, systemd auto-start, power-on after outage, and every WiFi/HDMI/Bluetooth power-save knob to disable.",
    href: "/manuals/raspberry-pi",
    tags: ["Raspberry Pi", "Linux", "Always-on", "ARM64"],
    featured: true,
  },
  {
    title: "CLI setup & usage guide",
    description:
      "Install the Yaver CLI, sign in, choose your AI agent, and learn the most useful commands.",
    href: "/manuals/cli-setup",
    tags: ["macOS", "Linux", "Windows"],
  },
  {
    title: "Relay server setup",
    description:
      "Deploy your own relay server so your phone can reach your dev machine from anywhere. Covers Docker setup, HTTPS with Let's Encrypt, client configuration, and maintenance.",
    href: "/manuals/relay-setup",
    tags: ["Docker", "VPS", "nginx"],
  },
  {
    title: "Cloudflare Tunnel + Box Sharing",
    description:
      "Use Cloudflare Tunnel for a single always-on box, then share that box safely through Yaver guest access or host-share sessions. Covers machine scoping, project scoping, and the security caveats.",
    href: "/manuals/cloudflare-share",
    tags: ["Cloudflare", "Guests", "Sharing", "Security"],
  },
  {
    title: "Integrations guide",
    description:
      "Set up notifications (Telegram, Discord, Slack), CI/CD webhooks (GitHub Actions, GitLab CI), MCP tools, and session transfer between machines.",
    href: "/manuals/integrations",
    tags: ["Notifications", "Webhooks", "MCP", "Session Transfer"],
  },
  {
    title: "Local voice AI with PersonaPlex",
    description:
      "Run NVIDIA PersonaPlex 7B on Apple Silicon for real-time speech-to-speech \u2014 full-duplex, no cloud, no API keys. 4-bit quantized via MLX, fits in 5.3 GB.",
    href: "/manuals/voice-ai",
    tags: ["Voice", "PersonaPlex", "MLX", "Apple Silicon"],
  },
  {
    title: "Headless Dev Machine — Set Up Once, Code Forever",
    description:
      "Turn any machine into a permanent AI development server. Systemd service, OAuth that survives reboots, auto-updates, project discovery — your always-on dev companion from your pocket.",
    href: "/manuals/auto-boot",
    tags: ["systemd", "macOS", "Linux", "headless"],
  },
  {
    title: "MacBook to Windows AI Box over SSH",
    description:
      "Use a Windows machine as the always-on coding box behind a MacBook: OpenSSH, Tailscale, Ollama with Qwen, OpenCode, and the power settings that keep it alive.",
    href: "/manuals/windows-ssh-coding-box",
    tags: ["Windows", "SSH", "Tailscale", "Ollama"],
  },
];

export default function ManualsPage() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <div className="mb-16 text-center">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            Manuals
          </h1>
          <p className="text-sm text-surface-500">
            Step-by-step guides for getting the most out of Yaver.
          </p>
        </div>

        <div className="space-y-4">
          {manuals.map((manual) => (
            <Link
              key={manual.href}
              href={manual.href}
              className={`card block transition-colors hover:border-surface-600 ${
                manual.featured
                  ? "border-green-500/20 bg-green-500/5"
                  : ""
              }`}
            >
              {manual.featured && (
                <span className="mb-3 inline-block rounded-full border border-green-500/20 bg-green-500/10 px-2.5 py-0.5 text-[11px] font-medium text-green-400">
                  Recommended
                </span>
              )}
              <h2 className="mb-2 text-base font-semibold text-surface-100">
                {manual.title}
              </h2>
              <p className="mb-3 text-sm leading-relaxed text-surface-400">
                {manual.description}
              </p>
              <div className="flex flex-wrap gap-2">
                {manual.tags.map((tag) => (
                  <span
                    key={tag}
                    className={`rounded-full px-2.5 py-0.5 text-[11px] font-medium ${
                      manual.featured
                        ? "bg-green-500/10 text-green-400"
                        : "bg-surface-800 text-surface-400"
                    }`}
                  >
                    {tag}
                  </span>
                ))}
              </div>
            </Link>
          ))}
        </div>

        <div className="mt-12 text-center">
          <Link href="/" className="text-xs text-surface-500 hover:text-surface-50">
            Back to home
          </Link>
        </div>
      </div>
    </div>
  );
}
