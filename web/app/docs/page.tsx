import Link from "next/link";

const docs = [
  {
    title: "Self-hosting guide",
    description:
      "Run your own relay server, use Tailscale, or Cloudflare Tunnel. Full control over your infrastructure.",
    href: "/docs/self-hosting",
    tags: ["Tailscale", "Docker", "Cloudflare"],
  },
  {
    title: "MCP integration",
    description:
      "Connect Yaver as an MCP server from Claude Desktop, Claude Web UI, or any MCP-compatible client. 473 built-in tools.",
    href: "/docs/mcp",
    tags: ["MCP", "Claude", "Tools"],
  },
  {
    title: "Developer docs",
    description:
      "Set up a development environment, build from source, and understand the architecture.",
    href: "/docs/developers",
    tags: ["Dev", "Build", "Architecture"],
  },
  {
    title: "Contributing guide",
    description:
      "How to contribute to Yaver — code, docs, bug reports, and feature requests.",
    href: "/docs/contributing",
    tags: ["OSS", "PR", "Issues"],
  },
  {
    title: "Feedback SDK",
    description:
      "Error capture, black box streaming, and visual bug reports with 6-layer security: scoped tokens, IP binding, HTTPS on LAN, rotation, and new-device alerts.",
    href: "/docs/feedback-sdk",
    tags: ["SDK", "React Native", "Flutter", "Security"],
  },
  {
    title: "Cloud machines",
    description:
      "Dedicated CPU ($49/mo) and GPU ($449/mo) dev machines with multi-user team access, shared GPU, and isolated workspaces.",
    href: "/docs/cloud-machines",
    tags: ["GPU", "Teams", "Multi-user"],
  },
];

const manualLinks = [
  {
    title: "Full On-Prem Free Stack",
    description: "Yaver + Ollama + GLM-4.7-Flash + Aider — $0/month AI coding with SWE-bench analysis.",
    href: "/manuals/free-onprem",
    featured: true,
  },
  {
    title: "CLI setup & usage",
    href: "/manuals/cli-setup",
  },
  {
    title: "Relay server setup",
    href: "/manuals/relay-setup",
  },
  {
    title: "Zero-cost local AI coding",
    href: "/manuals/local-llm",
  },
  {
    title: "Local voice AI with PersonaPlex",
    href: "/manuals/voice-ai",
  },
  {
    title: "Auto-boot on power restore",
    href: "/manuals/auto-boot",
  },
];

export default function DocsPage() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <div className="mb-16 text-center">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            Documentation
          </h1>
          <p className="text-sm text-surface-500">
            Guides, references, and manuals for Yaver.
          </p>
        </div>

        {/* Docs */}
        <h2 className="mb-4 text-lg font-semibold text-surface-100">Docs</h2>
        <div className="mb-12 space-y-4">
          {docs.map((doc) => (
            <Link
              key={doc.href}
              href={doc.href}
              className="card block transition-colors hover:border-surface-600"
            >
              <h3 className="mb-2 text-base font-semibold text-surface-100">
                {doc.title}
              </h3>
              <p className="mb-3 text-sm leading-relaxed text-surface-400">
                {doc.description}
              </p>
              <div className="flex gap-2">
                {doc.tags.map((tag) => (
                  <span
                    key={tag}
                    className="rounded-full bg-surface-800 px-2.5 py-0.5 text-[11px] font-medium text-surface-400"
                  >
                    {tag}
                  </span>
                ))}
              </div>
            </Link>
          ))}
        </div>

        {/* Manuals */}
        <h2 className="mb-4 text-lg font-semibold text-surface-100">
          Step-by-step manuals
        </h2>
        <div className="space-y-3">
          {manualLinks.map((manual) => (
            <Link
              key={manual.href}
              href={manual.href}
              className={`block rounded-xl border px-4 py-3 transition-colors hover:border-surface-600 ${
                manual.featured
                  ? "border-green-500/20 bg-green-500/5"
                  : "border-surface-800 bg-surface-900/50"
              }`}
            >
              <div className="flex items-center justify-between">
                <div>
                  {manual.featured && (
                    <span className="mb-1 inline-block rounded-full border border-green-500/20 bg-green-500/10 px-2 py-0.5 text-[10px] font-medium text-green-400">
                      Recommended
                    </span>
                  )}
                  <h3 className="text-sm font-medium text-surface-200">
                    {manual.title}
                  </h3>
                  {manual.description && (
                    <p className="mt-1 text-xs text-surface-400">
                      {manual.description}
                    </p>
                  )}
                </div>
                <span className="text-surface-600">&rarr;</span>
              </div>
            </Link>
          ))}
        </div>

        <div className="mt-12 text-center">
          <Link
            href="/"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            Back to home
          </Link>
        </div>
      </div>
    </div>
  );
}
