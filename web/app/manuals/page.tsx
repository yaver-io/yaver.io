import Link from "next/link";

const manuals = [
  {
    title: "Full On-Prem Free Stack",
    description:
      "Yaver + Ollama + GLM-4.7-Flash + Aider — a complete AI coding setup for $0/month. SWE-bench analysis, hardware requirements, step-by-step setup, and everything you need to know about local LLMs.",
    href: "/manuals/free-onprem",
    tags: ["Free", "On-Prem", "SWE-bench", "GLM-4.7-Flash"],
    featured: true,
  },
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
    title: "Zero-cost local AI coding setup",
    description:
      "Run AI coding agents entirely on your own hardware — no API keys, no cloud services, no recurring costs. Set up Ollama, a coding agent, and Tailscale for remote access.",
    href: "/manuals/local-llm",
    tags: ["Ollama", "Free", "Local"],
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
    title: "Auto-boot on power restore",
    description:
      "Configure your macOS, Linux, or desktop PC to automatically boot when power is restored after an outage — so Yaver CLI starts without manual intervention.",
    href: "/manuals/auto-boot",
    tags: ["macOS", "Linux", "BIOS"],
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
