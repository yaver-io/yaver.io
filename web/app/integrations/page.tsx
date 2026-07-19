"use client";

import Link from "next/link";

interface Integration {
  name: string;
  description: string;
  status: "Built-in" | "Configure in settings";
  docsLink: string;
}

interface Category {
  title: string;
  subtitle: string;
  icon: React.ReactNode;
  integrations: Integration[];
}

const CATEGORIES: Category[] = [
  {
    title: "Chat Providers",
    subtitle: "Bidirectional notifications and commands",
    icon: (
      <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M8.625 12a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H8.25m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0H12m4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm0 0h-.375M21 12c0 4.556-4.03 8.25-9 8.25a9.764 9.764 0 01-2.555-.337A5.972 5.972 0 015.41 20.97a5.969 5.969 0 01-.474-.065 4.48 4.48 0 00.978-2.025c.09-.457-.133-.901-.467-1.226C3.93 16.178 3 14.189 3 12c0-4.556 4.03-8.25 9-8.25s9 3.694 9 8.25z" />
      </svg>
    ),
    integrations: [
      {
        name: "Telegram",
        description: "Two-way bot: get task notifications and send commands from Telegram",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#notifications",
      },
      {
        name: "Discord",
        description: "Webhook notifications for task completion and agent status changes",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#notifications",
      },
      {
        name: "Slack",
        description: "Webhook notifications for task updates and agent alerts",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#notifications",
      },
      {
        name: "Microsoft Teams",
        description: "Incoming webhook notifications for task and agent events",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#notifications",
      },
    ],
  },
  {
    title: "AI Agents",
    subtitle: "Supported agent runners",
    icon: (
      <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 8.715L18 9.75l-.259-1.035a3.375 3.375 0 00-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 002.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 002.455 2.456L21.75 6l-1.036.259a3.375 3.375 0 00-2.455 2.456zM16.894 20.567L16.5 21.75l-.394-1.183a2.25 2.25 0 00-1.423-1.423L13.5 18.75l1.183-.394a2.25 2.25 0 001.423-1.423l.394-1.183.394 1.183a2.25 2.25 0 001.423 1.423l1.183.394-1.183.394a2.25 2.25 0 00-1.423 1.423z" />
      </svg>
    ),
    integrations: [
      {
        name: "Claude Code",
        description: "Anthropic's CLI agent for code generation and editing",
        status: "Built-in",
        docsLink: "/manuals/cli-setup",
      },
      {
        name: "Codex",
        description: "OpenAI's coding agent, runs in tmux via Yaver",
        status: "Built-in",
        docsLink: "/manuals/cli-setup",
      },
      {
        name: "Aider",
        description: "Open-source AI pair programming tool",
        status: "Built-in",
        docsLink: "/manuals/cli-setup",
      },
      {
        name: "Ollama",
        description: "Run local LLMs on your own hardware, no API keys needed",
        status: "Built-in",
        docsLink: "/manuals/local-llm",
      },
      {
        name: "Goose",
        description: "Block's open-source AI developer agent",
        status: "Built-in",
        docsLink: "/manuals/cli-setup",
      },
      {
        name: "Amp",
        description: "Sourcegraph's AI coding agent",
        status: "Built-in",
        docsLink: "/manuals/cli-setup",
      },
      {
        name: "OpenCode",
        description: "Open-source terminal AI code editor",
        status: "Built-in",
        docsLink: "/manuals/cli-setup",
      },
    ],
  },
  {
    title: "Developer Tools",
    subtitle: "MCP-based tools available to agents",
    icon: (
      <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M11.42 15.17l-5.1-5.1m0 0L11.42 4.97m-5.1 5.1h13.36M4.5 19.5h15" />
      </svg>
    ),
    integrations: [
      {
        name: "File Search",
        description: "Search project files by name, pattern, or content",
        status: "Built-in",
        docsLink: "/docs/mcp",
      },
      {
        name: "Git Operations",
        description: "Status, diff, log, and branch operations via MCP",
        status: "Built-in",
        docsLink: "/docs/mcp",
      },
      {
        name: "Screen Capture",
        description: "Capture screenshots of the desktop for visual context",
        status: "Built-in",
        docsLink: "/docs/mcp",
      },
      {
        name: "System Monitor",
        description: "CPU, memory, disk usage, and process monitoring",
        status: "Built-in",
        docsLink: "/docs/mcp",
      },
      {
        name: "Exec",
        description: "Run shell commands with output streaming",
        status: "Built-in",
        docsLink: "/docs/mcp",
      },
    ],
  },
  {
    title: "CI/CD",
    subtitle: "Trigger builds and get pipeline notifications",
    icon: (
      <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182M2.985 19.644l3.181-3.183" />
      </svg>
    ),
    integrations: [
      {
        name: "GitHub Actions",
        description: "Trigger workflows and receive build status notifications",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#webhooks",
      },
      {
        name: "GitLab CI",
        description: "Pipeline triggers and merge request notifications",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#webhooks",
      },
      {
        name: "Generic Webhooks",
        description: "Send task events to any HTTP endpoint",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#webhooks",
      },
    ],
  },
  {
    title: "Issue Tracking & Alerting",
    subtitle: "Create issues and trigger alerts on task completion",
    icon: (
      <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75v-.7V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0" />
      </svg>
    ),
    integrations: [
      {
        name: "Linear",
        description: "Auto-create issues in Linear when tasks complete or fail",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#notifications",
      },
      {
        name: "Jira",
        description: "Create Jira tickets from task completion events",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#notifications",
      },
      {
        name: "PagerDuty",
        description: "Trigger PagerDuty incidents on task failures",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#notifications",
      },
      {
        name: "Opsgenie",
        description: "Send alerts to Opsgenie when tasks fail",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#notifications",
      },
      {
        name: "Email",
        description: "Email notifications via Office 365 or Gmail",
        status: "Configure in settings",
        docsLink: "/manuals/integrations#notifications",
      },
    ],
  },
  {
    title: "SDKs",
    subtitle: "Embed Yaver in your own apps",
    icon: (
      <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M17.25 6.75L22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3l-4.5 16.5" />
      </svg>
    ),
    integrations: [
      {
        name: "Go",
        description: "Import directly from GitHub via go get",
        status: "Built-in",
        docsLink: "/docs/developers#sdk",
      },
      {
        name: "Python",
        description: "Install from PyPI: pip install yaver",
        status: "Built-in",
        docsLink: "/docs/developers#sdk",
      },
      {
        name: "JavaScript / TypeScript",
        description: "Install from npm: npm install yaver-sdk",
        status: "Built-in",
        docsLink: "/docs/developers#sdk",
      },
      {
        name: "Flutter / Dart",
        description: "Dart package for mobile and desktop Flutter apps",
        status: "Built-in",
        docsLink: "/docs/developers#sdk",
      },
      {
        name: "C / C++",
        description: "Shared library (.so / .dylib) for native integration",
        status: "Built-in",
        docsLink: "/docs/developers#sdk",
      },
    ],
  },
  {
    title: "Connectivity",
    subtitle: "Transport and networking options",
    icon: (
      <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M12.75 3.03v.568c0 .334.148.65.405.864a11.04 11.04 0 012.649 2.648c.213.258.53.407.864.407H17.5M3.75 21h16.5M5.625 4.5H3.75a1.125 1.125 0 00-1.125 1.125v15.75M16.5 3.75v1.875c0 .621.504 1.125 1.125 1.125h1.875m-1.875-3H8.25m8.25 0v3.375c0 .621.504 1.125 1.125 1.125H21M8.25 3.75H5.625m2.625 0v3.375c0 .621.504 1.125 1.125 1.125H12" />
      </svg>
    ),
    integrations: [
      {
        name: "Direct LAN",
        description: "Auto-discovered via UDP beacon on the same network (~5ms latency)",
        status: "Built-in",
        docsLink: "/docs",
      },
      {
        name: "QUIC Relay",
        description: "Application-layer relay for NAT traversal, self-hostable via Docker",
        status: "Built-in",
        docsLink: "/manuals/relay-setup",
      },
    ],
  },
];

function StatusBadge({ status }: { status: "Built-in" | "Configure in settings" }) {
  if (status === "Built-in") {
    return (
      <span className="inline-flex items-center rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-medium text-emerald-400 ring-1 ring-inset ring-emerald-500/20">
        Built-in
      </span>
    );
  }
  return (
    <span className="inline-flex items-center rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-400 ring-1 ring-inset ring-amber-500/20">
      Configure
    </span>
  );
}

export default function IntegrationsPage() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-5xl">
        {/* Hero */}
        <div className="mb-16 text-center">
          <h1 className="mb-4 text-4xl font-bold text-surface-50 md:text-5xl">
            Integrations
          </h1>
          <p className="mx-auto max-w-2xl text-base leading-relaxed text-surface-400">
            Connect Yaver with your favorite AI agents, chat platforms, developer
            tools, and CI/CD pipelines. All data flows peer-to-peer &mdash;
            nothing is stored on our servers.
          </p>
        </div>

        {/* Categories */}
        <div className="space-y-16">
          {CATEGORIES.map((category) => (
            <section key={category.title}>
              <div className="mb-6 flex items-center gap-3">
                <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-surface-800/80 text-surface-300">
                  {category.icon}
                </div>
                <div>
                  <h2 className="text-xl font-semibold text-surface-100">
                    {category.title}
                  </h2>
                  <p className="text-sm text-surface-500">{category.subtitle}</p>
                </div>
              </div>

              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                {category.integrations.map((integration) => (
                  <Link
                    key={integration.name}
                    href={integration.docsLink}
                    className="group rounded-xl border border-surface-800 bg-surface-900/40 p-5 transition-all hover:border-surface-600 hover:bg-surface-900/70"
                  >
                    <div className="mb-3 flex items-start justify-between">
                      <h3 className="font-medium text-surface-200 group-hover:text-surface-50">
                        {integration.name}
                      </h3>
                      <StatusBadge status={integration.status} />
                    </div>
                    <p className="text-sm leading-relaxed text-surface-500 group-hover:text-surface-400">
                      {integration.description}
                    </p>
                    <div className="mt-4 flex items-center gap-1 text-xs text-surface-600 group-hover:text-[#6366f1]">
                      View docs
                      <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
                      </svg>
                    </div>
                  </Link>
                ))}
              </div>
            </section>
          ))}
        </div>

        {/* CTA */}
        <div className="mt-20 rounded-xl border border-surface-800 bg-surface-900/30 p-8 text-center">
          <h2 className="mb-2 text-lg font-semibold text-surface-100">
            Missing an integration?
          </h2>
          <p className="mb-6 text-sm text-surface-500">
            Yaver is open source. Contributions are welcome, or open an issue on
            GitHub to request a new integration.
          </p>
          <div className="flex items-center justify-center gap-4">
            <Link
              href="https://github.com/kivanccakmak/yaver.io"
              target="_blank"
              rel="noopener noreferrer"
              className="btn-primary px-5 py-2 text-sm"
            >
              View on GitHub
            </Link>
            <Link
              href="/docs/contributing"
              className="rounded-lg border border-surface-700 px-5 py-2 text-sm text-surface-300 transition-colors hover:border-surface-500 hover:text-surface-100"
            >
              Contributing Guide
            </Link>
          </div>
        </div>
      </div>
    </div>
  );
}
