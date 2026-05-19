"use client";

import Link from "next/link";

function Terminal({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="terminal">
      <div className="terminal-header">
        <div className="terminal-dot bg-[#ff5f57]" />
        <div className="terminal-dot bg-[#febc2e]" />
        <div className="terminal-dot bg-[#28c840]" />
        <span className="ml-3 text-xs text-surface-500">{title}</span>
      </div>
      <div className="terminal-body space-y-2 text-[13px]">{children}</div>
    </div>
  );
}

function Cmd({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <span className="text-surface-400">$</span>{" "}
      <span className="text-surface-200 select-all">{children}</span>
    </div>
  );
}

function Comment({ children }: { children: React.ReactNode }) {
  return <div className="text-surface-500">{children}</div>;
}

function Output({ children }: { children: React.ReactNode }) {
  return <div className="text-green-400/80 pl-2">{children}</div>;
}

function Divider() {
  return <div className="h-px bg-surface-800/60" />;
}

function SectionHeading({
  id,
  children,
}: {
  id: string;
  children: React.ReactNode;
}) {
  return (
    <h2
      id={id}
      className="mb-4 text-2xl font-bold text-surface-50 md:text-3xl"
    >
      {children}
    </h2>
  );
}

function SubHeading({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="mb-3 text-lg font-semibold text-surface-100">{children}</h3>
  );
}

function Prose({ children }: { children: React.ReactNode }) {
  return (
    <p className="mb-6 text-sm leading-relaxed text-surface-400">{children}</p>
  );
}

function InlineCode({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-300">
      {children}
    </code>
  );
}

function ToolCategory({
  title,
  tools,
}: {
  title: string;
  tools: { name: string; description: string }[];
}) {
  return (
    <div className="card">
      <h4 className="mb-3 text-sm font-medium text-surface-200">{title}</h4>
      <ul className="space-y-2 text-sm text-surface-400">
        {tools.map((tool) => (
          <li key={tool.name}>
            &bull; <InlineCode>{tool.name}</InlineCode> &mdash;{" "}
            {tool.description}
          </li>
        ))}
      </ul>
    </div>
  );
}

export default function McpPage() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        {/* Back link */}
        <Link
          href="/"
          className="mb-12 inline-block text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to home
        </Link>

        {/* Header */}
        <div className="mb-16">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            MCP Integration Guide
          </h1>
          <p className="text-sm leading-relaxed text-surface-400">
            Model Context Protocol (MCP) lets AI agents connect to tools and
            data sources. Yaver implements MCP so AI agents like Claude can use
            your development machine as a tool server &mdash; giving them access
            to your filesystem, tasks, email, and more.
          </p>
        </div>

        {/* Table of contents */}
        <div className="mb-16 rounded-xl border border-surface-800 bg-surface-900 p-6">
          <h3 className="mb-4 text-sm font-semibold text-surface-200">
            On this page
          </h3>
          <nav className="space-y-2 text-sm">
            {[
              ["what-is-mcp", "What is MCP?"],
              ["installation", "Installation"],
              ["local-mcp", "Local MCP (stdio) \u2014 Claude Code, Codex, opencode"],
              ["network-mcp", "Network MCP (HTTP) \u2014 Remote Access"],
              ["available-tools", "Available Tools (470+)"],
              ["email-setup", "Email Setup"],
              ["acl", "ACL \u2014 Connecting to Other MCP Servers"],
              ["standalone-mcp", "Standalone MCP Server"],
              ["plugins", "Creating Plugins"],
              ["security", "Security"],
            ].map(([id, label]) => (
              <a
                key={id}
                href={`#${id}`}
                className="block text-surface-500 hover:text-surface-200"
              >
                {label}
              </a>
            ))}
          </nav>
        </div>

        {/* ─── Section 1: What is MCP? ─── */}
        <section className="mb-20">
          <SectionHeading id="what-is-mcp">What is MCP?</SectionHeading>
          <Prose>
            The Model Context Protocol (MCP) is an open standard that lets AI
            agents interact with tools, data sources, and services through a
            unified interface. Instead of building custom integrations for every
            tool, MCP provides a single protocol that any AI agent can speak.
          </Prose>
          <div className="card">
            <h4 className="mb-2 text-sm font-medium text-surface-200">
              Why Yaver + MCP?
            </h4>
            <p className="text-sm leading-relaxed text-surface-400">
              Yaver implements an MCP server that exposes your development
              machine as a tool server. This means any MCP-compatible AI agent
              &mdash; Claude Desktop, Claude Web UI, or others &mdash; can
              create tasks, read files, manage projects, send emails, and
              connect to other MCP servers through your Yaver agent. All
              operations run locally on your machine; nothing is sent to
              third-party servers.
            </p>
          </div>
        </section>

        {/* ─── Section 2: Installation ─── */}
        <section className="mb-20">
          <SectionHeading id="installation">Installation</SectionHeading>
          <Prose>
            Install the Yaver CLI from npm, then sign in once so the agent can
            auto-start and register itself with the local runner CLIs.
          </Prose>

          <div className="mb-8">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                No global install required for Claude Code and Codex
              </h4>
              <p className="mb-3 text-sm leading-relaxed text-surface-400">
                If the user is already inside a coding-agent terminal, register
                Yaver through <InlineCode>npx</InlineCode>, restart the agent
                session if needed, then call{" "}
                <InlineCode>yaver_lazy_setup</InlineCode>. For a brand-new app,
                call <InlineCode>project_self_host_create</InlineCode> after
                sign-in; it creates the self-hosted monorepo before any managed
                cloud upsell.
              </p>
              <Terminal title="agent-terminal">
                <Cmd>
                  claude mcp add --scope user yaver -- npx -y yaver-cli
                  yaver-mcp
                </Cmd>
                <Divider />
                <Cmd>codex mcp add yaver -- npx -y yaver-cli yaver-mcp</Cmd>
              </Terminal>
            </div>
          </div>

          <div className="mb-6 space-y-4">
            <div className="mb-8">
              <Terminal title="install">
                <Comment># macOS / Linux / WSL</Comment>
                <Cmd>npm install -g yaver-cli</Cmd>
                <Divider />
                <Comment># Upgrade later</Comment>
                <Cmd>npm install -g yaver-cli@latest</Cmd>
                <Divider />
                <Comment># Verify installation</Comment>
                <Cmd>yaver --version</Cmd>
              </Terminal>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Authenticate
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Before using MCP, sign in with your Yaver account. This is
                required for device registration and peer discovery.
              </p>
              <div className="mt-3">
                <Terminal title="auth">
                  <Cmd>yaver auth</Cmd>
                  <Output>
                    Opening browser for sign-in...
                  </Output>
                </Terminal>
              </div>
            </div>
          </div>
        </section>

        {/* ─── Section 3: Local MCP (stdio) ─── */}
        <section className="mb-20">
          <SectionHeading id="local-mcp">
            Local MCP (stdio) &mdash; for Claude Code, Codex &amp; opencode
          </SectionHeading>
          <Prose>
            The stdio transport runs Yaver as a local process that communicates
            over standard input/output. Yaver registers itself as an MCP server
            inside its three first-class runner CLIs &mdash; Claude Code, Codex,
            and opencode &mdash; so any of them can call Yaver tools directly.
          </Prose>

          <SubHeading>One-Command Setup</SubHeading>
          <Prose>
            If Yaver is already installed globally, use its setup helper:
          </Prose>

          <div className="mb-8">
            <Terminal title="terminal">
              <Cmd>yaver mcp setup claude-code</Cmd>
              <Output>Added Yaver to Claude Code user MCP config.</Output>
              <Divider />
              <Comment># Also: codex (Codex CLI), opencode (opencode)</Comment>
              <Cmd>yaver mcp setup codex</Cmd>
              <Cmd>yaver mcp setup opencode</Cmd>
            </Terminal>
          </div>

          <SubHeading>Manual Configuration</SubHeading>
          <Prose>
            Or run the equivalent commands directly. Each runner has its own
            config surface. The most portable JSON uses{" "}
            <InlineCode>npx</InlineCode> so the MCP server can bootstrap itself:
          </Prose>

          <div className="mb-8">
            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">
                  yaver MCP server entry
                </span>
              </div>
              <div className="terminal-body text-[13px] leading-relaxed">
                <pre className="text-surface-300">
                  {`{
  "mcpServers": {
    "yaver": {
      "command": "npx",
      "args": ["-y", "yaver-cli", "yaver-mcp"]
    }
  }
}`}
                </pre>
              </div>
            </div>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Where Yaver writes the entry
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; Claude Code:{" "}
                  <InlineCode>
                    claude mcp add --scope user yaver -- npx -y yaver-cli
                    yaver-mcp
                  </InlineCode>
                </li>
                <li>
                  &bull; Codex:{" "}
                  <InlineCode>
                    codex mcp add yaver -- npx -y yaver-cli yaver-mcp
                  </InlineCode>
                </li>
                <li>
                  &bull; opencode:{" "}
                  <InlineCode>
                    ~/.config/opencode/opencode.json &mdash; mcp.yaver
                  </InlineCode>
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                How it works
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                The runner CLI launches Yaver&apos;s MCP stdio process as a child
                process. The MCP protocol runs over stdin/stdout using JSON-RPC.
                No network ports are opened for local stdio mode &mdash;
                everything stays on the user&apos;s machine.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Discovery metadata
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Yaver publishes official registry metadata as{" "}
                <InlineCode>io.github.kivanccakmak/yaver</InlineCode> and also
                serves machine-readable discovery files at{" "}
                <InlineCode>/.well-known/mcp/server.json</InlineCode>,{" "}
                <InlineCode>/.well-known/mcp/server-card.json</InlineCode>,{" "}
                <InlineCode>/.well-known/mcp.json</InlineCode>, and{" "}
                <InlineCode>/.mcp.json</InlineCode>. Agents that do not know
                Yaver yet should fetch <InlineCode>/llms.txt</InlineCode> first.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                First Project Capture
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                The MCP-first product path is self-hosted first. After{" "}
                <InlineCode>yaver_lazy_setup</InlineCode> reports signed in,
                call <InlineCode>project_self_host_create</InlineCode>. It
                creates <InlineCode>apps/web</InlineCode>,{" "}
                <InlineCode>apps/landing</InlineCode>,{" "}
                <InlineCode>apps/mobile</InlineCode>,{" "}
                <InlineCode>backend/convex</InlineCode>, and{" "}
                <InlineCode>packages/shared</InlineCode>, plus Yaver local
                service config and mobile testing next steps. Use{" "}
                <InlineCode>yaver_managed_cloud_onboarding</InlineCode> only
                after the user explicitly wants hourly managed cloud.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Section 4: Network MCP (HTTP) ─── */}
        <section className="mb-20">
          <SectionHeading id="network-mcp">
            Network MCP (HTTP) &mdash; for Remote Access
          </SectionHeading>
          <Prose>
            The HTTP transport exposes the MCP server over the network, allowing
            remote AI agents (including the Claude Web UI) to connect to your
            development machine.
          </Prose>

          <div className="mb-8">
            <Terminal title="network-mcp">
              <Comment># Start MCP server in HTTP mode</Comment>
              <Cmd>yaver mcp --mode http --port 18090</Cmd>
              <Output>MCP HTTP server listening on :18090</Output>
              <Divider />
              <Comment># Test the endpoint</Comment>
              <Cmd>{"curl http://localhost:18090/mcp"}</Cmd>
            </Terminal>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Connect from any MCP client
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Point your MCP client to{" "}
                <InlineCode>http://your-machine:18090/mcp</InlineCode>. If
                connecting from outside your local network, combine with
                Tailscale, a relay server, or Cloudflare Tunnel for secure
                access. See the{" "}
                <Link
                  href="/docs/self-hosting"
                  className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
                >
                  Self-Hosting Guide
                </Link>{" "}
                for networking options.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                With Tailscale
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                If both devices are on your tailnet, simply use your Tailscale
                IP:{" "}
                <InlineCode>http://100.x.x.x:18090/mcp</InlineCode>. No
                additional configuration needed.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Section 5: Available Tools ─── */}
        <section className="mb-20">
          <SectionHeading id="available-tools">
            Available Tools (470+)
          </SectionHeading>
          <Prose>
            Yaver exposes 473 tools through MCP, organized by category. AI
            agents can discover and use these tools automatically. Yaver's
            three first-class runners (Claude Code, Codex, opencode) can
            manage tasks, adopt tmux sessions, configure relay servers, and
            more &mdash; programmatically.
          </Prose>

          <div className="space-y-4">
            <ToolCategory
              title="Task Management"
              tools={[
                { name: "create_task", description: "Create a new AI agent task" },
                { name: "list_tasks", description: "List all tasks with status" },
                { name: "get_task", description: "Get task details and output" },
                { name: "stop_task", description: "Stop a running task" },
                { name: "continue_task", description: "Continue a stopped task with new input" },
              ]}
            />

            <ToolCategory
              title="Tmux Session Adoption"
              tools={[
                { name: "tmux_list_sessions", description: "List all tmux sessions with agent detection (claude, codex, opencode) and relationship to Yaver" },
                { name: "tmux_adopt_session", description: "Adopt an existing tmux session as a Yaver task \u2014 output streams to mobile, input routes via send-keys" },
                { name: "tmux_detach_session", description: "Stop monitoring an adopted session (tmux session keeps running)" },
                { name: "tmux_send_input", description: "Send keyboard input to an adopted tmux session" },
              ]}
            />

            <ToolCategory
              title="Diagnostics & Status"
              tools={[
                { name: "yaver_doctor", description: "Full system health check \u2014 auth, agent, runners, relays, tunnels, network" },
                { name: "yaver_status", description: "Auth status, agent info, runner, relay/tunnel summary" },
                { name: "yaver_devices", description: "List all registered devices with online/offline status" },
                { name: "yaver_logs", description: "View last N lines of agent log" },
                { name: "yaver_clear_logs", description: "Clear the agent log file" },
                { name: "yaver_help", description: "Get help about Yaver features (tmux, relay, tunnel, mobile, mcp, runners, etc.)" },
                { name: "yaver_ping", description: "Verify agent is alive and measure RTT" },
                { name: "agent_shutdown", description: "Gracefully shut down the agent" },
              ]}
            />

            <ToolCategory
              title="System & Config"
              tools={[
                { name: "get_info", description: "Get agent info and version" },
                { name: "get_system_info", description: "Get OS, CPU, memory, and disk info" },
                { name: "get_config", description: "Get current agent configuration" },
                { name: "config_set", description: "Set config values (auto-start, auto-update)" },
                { name: "set_work_dir", description: "Set the working directory for tasks" },
                { name: "list_projects", description: "List available projects and directories" },
              ]}
            />

            <ToolCategory
              title="Runners"
              tools={[
                { name: "list_runners", description: "List available AI agent runners" },
                { name: "switch_runner", description: "Switch to a different runner (Claude, Codex, Aider, etc.)" },
              ]}
            />

            <ToolCategory
              title="Relay Servers"
              tools={[
                { name: "get_relay_config", description: "Get current relay server configuration" },
                { name: "add_relay_server", description: "Add a relay server" },
                { name: "remove_relay_server", description: "Remove a relay server" },
                { name: "relay_test", description: "Test relay server connectivity and latency" },
                { name: "relay_set_password", description: "Set the default relay password" },
                { name: "relay_clear_password", description: "Remove the default relay password" },
              ]}
            />

            <ToolCategory
              title="Cloudflare Tunnels"
              tools={[
                { name: "tunnel_list", description: "List configured Cloudflare Tunnels" },
                { name: "tunnel_add", description: "Add a Cloudflare Tunnel endpoint" },
                { name: "tunnel_remove", description: "Remove a tunnel by ID or URL" },
                { name: "tunnel_test", description: "Test tunnel connectivity and latency" },
              ]}
            />

            <ToolCategory
              title="Filesystem"
              tools={[
                { name: "read_file", description: "Read file contents" },
                { name: "write_file", description: "Write content to a file" },
                { name: "list_directory", description: "List files and directories" },
                { name: "search_files", description: "Search for files by name or content" },
              ]}
            />

            <ToolCategory
              title="Email"
              tools={[
                { name: "email_list_inbox", description: "List inbox messages" },
                { name: "email_get", description: "Get a specific email" },
                { name: "email_send", description: "Send an email" },
                { name: "email_sync", description: "Sync mailbox with server" },
                { name: "email_search", description: "Search emails by query" },
              ]}
            />

            <ToolCategory
              title="ACL (Peer MCP Servers)"
              tools={[
                { name: "acl_list_peers", description: "List connected MCP peer servers" },
                { name: "acl_add_peer", description: "Connect to another MCP server" },
                { name: "acl_remove_peer", description: "Disconnect from an MCP server" },
                { name: "acl_list_peer_tools", description: "List tools available from a peer" },
                { name: "acl_call_peer_tool", description: "Call a tool on a connected peer" },
                { name: "acl_health", description: "Check health of all connected peers" },
              ]}
            />
          </div>
        </section>

        {/* ─── Section 6: Email Setup ─── */}
        <section className="mb-20">
          <SectionHeading id="email-setup">Email Setup</SectionHeading>
          <Prose>
            Yaver&apos;s email tools let AI agents read and send emails on your
            behalf. Configure either Office 365 or Gmail as your email provider.
          </Prose>

          <SubHeading>Office 365</SubHeading>
          <div className="mb-8">
            <Terminal title="email-office365">
              <Cmd>yaver email setup</Cmd>
              <Output>Select email provider:</Output>
              <Output>1) Office 365</Output>
              <Output>2) Gmail</Output>
              <Comment># Select 1 for Office 365</Comment>
              <Comment># Enter Azure Tenant ID, Client ID, Client Secret, Sender Email</Comment>
            </Terminal>
          </div>

          <div className="mb-8 card">
            <h4 className="mb-2 text-sm font-medium text-surface-200">
              Azure App Registration
            </h4>
            <p className="text-sm leading-relaxed text-surface-400">
              You&apos;ll need an Azure App Registration with{" "}
              <InlineCode>Mail.ReadWrite</InlineCode> and{" "}
              <InlineCode>Mail.Send</InlineCode> permissions. See the{" "}
              <a
                href="https://learn.microsoft.com/en-us/graph/auth-register-app-v2"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
              >
                Microsoft Graph documentation
              </a>{" "}
              for setup instructions.
            </p>
          </div>

          <SubHeading>Gmail</SubHeading>
          <div className="mb-8">
            <Terminal title="email-gmail">
              <Cmd>yaver email setup</Cmd>
              <Output>Select email provider:</Output>
              <Output>1) Office 365</Output>
              <Output>2) Gmail</Output>
              <Comment># Select 2 for Gmail</Comment>
              <Comment># Enter Google Client ID, Client Secret, Refresh Token, Sender Email</Comment>
            </Terminal>
          </div>

          <div className="card">
            <h4 className="mb-2 text-sm font-medium text-surface-200">
              Google Cloud Console
            </h4>
            <p className="text-sm leading-relaxed text-surface-400">
              You&apos;ll need a Google Cloud project with the Gmail API
              enabled and OAuth 2.0 credentials. See the{" "}
              <a
                href="https://developers.google.com/gmail/api/quickstart/go"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
              >
                Gmail API documentation
              </a>{" "}
              for setup instructions.
            </p>
          </div>
        </section>

        {/* ─── Section 7: ACL ─── */}
        <section className="mb-20">
          <SectionHeading id="acl">
            ACL &mdash; Connecting to Other MCP Servers
          </SectionHeading>
          <Prose>
            Yaver can connect to other MCP servers as peers, giving your AI
            agent access to their tools. This lets you compose multiple MCP
            servers into a single unified tool surface.
          </Prose>

          <div className="mb-8">
            <Terminal title="acl-examples">
              <Comment># Connect to a custom MCP server (HTTP)</Comment>
              <Cmd>yaver acl add my-mcp-server http://localhost:8765/mcp</Cmd>
              <Divider />
              <Comment># Connect to a filesystem MCP server (stdio)</Comment>
              <Cmd>
                yaver acl add files --stdio &quot;npx -y
                @modelcontextprotocol/server-filesystem /home/user&quot;
              </Cmd>
              <Divider />
              <Comment># Connect to a remote database MCP server</Comment>
              <Cmd>
                yaver acl add mydb https://db.example.com/mcp --auth token123
              </Cmd>
              <Divider />
              <Comment># List connected peers</Comment>
              <Cmd>yaver acl list</Cmd>
              <Divider />
              <Comment># Check health of all peers</Comment>
              <Cmd>yaver acl health</Cmd>
            </Terminal>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                How it works
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                When you add a peer, Yaver connects to the MCP server and
                discovers its tools. These tools are then exposed through
                Yaver&apos;s own MCP interface with the{" "}
                <InlineCode>acl_call_peer_tool</InlineCode> function. AI agents
                see all peer tools alongside Yaver&apos;s built-in tools.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Transport modes
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; <InlineCode>http</InlineCode> &mdash; Connect to an
                  HTTP MCP server (default)
                </li>
                <li>
                  &bull; <InlineCode>--stdio</InlineCode> &mdash; Launch a
                  local process and communicate over stdin/stdout
                </li>
                <li>
                  &bull; <InlineCode>--auth</InlineCode> &mdash; Pass an
                  authentication token for protected servers
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Standalone MCP Server ─── */}
        <section className="mb-20">
          <SectionHeading id="standalone-mcp">Standalone MCP Server</SectionHeading>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            Yaver includes a standalone, open-source MCP server (<code className="text-surface-200">yaver-mcp</code>) that you can
            deploy locally, on a VPS, or use our managed hosting. It provides built-in tools and supports
            user-deployed plugins for custom functionality.
          </p>

          <h4 className="mb-2 mt-6 text-sm font-semibold text-surface-200">Quick start</h4>
          <Terminal title="terminal">
            <Comment># Run locally</Comment>
            <Cmd>cd mcp &amp;&amp; go build -o yaver-mcp . &amp;&amp; ./yaver-mcp serve --password secret</Cmd>
            <Output>MCP server listening on 0.0.0.0:18100</Output>
          </Terminal>

          <h4 className="mb-2 mt-6 text-sm font-semibold text-surface-200">Deploy to VPS</h4>
          <Terminal title="terminal">
            <Comment># Binary deploy (builds + copies + starts systemd service)</Comment>
            <Cmd>cd mcp &amp;&amp; ./deploy/up.sh your-server-ip</Cmd>
            <Output>MCP server running on your-server-ip:18100</Output>
          </Terminal>

          <Terminal title="terminal">
            <Comment># Docker deploy</Comment>
            <Cmd>cd mcp &amp;&amp; ./deploy/up.sh your-server-ip --docker</Cmd>
          </Terminal>

          <h4 className="mb-2 mt-6 text-sm font-semibold text-surface-200">Connect from your agent</h4>
          <Terminal title="terminal">
            <Comment># Add as ACL peer</Comment>
            <Cmd>yaver acl add mcp http://your-server:18100/mcp --auth secret</Cmd>
          </Terminal>

          <h4 className="mb-2 mt-6 text-sm font-semibold text-surface-200">Built-in tools</h4>
          <p className="mb-2 text-sm text-surface-400">
            The standalone server ships with 10 tools: <code className="text-surface-300">read_file</code>, <code className="text-surface-300">write_file</code>,{" "}
            <code className="text-surface-300">list_directory</code>, <code className="text-surface-300">search_files</code>, <code className="text-surface-300">search_content</code>,{" "}
            <code className="text-surface-300">exec_command</code>, <code className="text-surface-300">git_status</code>, <code className="text-surface-300">git_diff</code>,{" "}
            <code className="text-surface-300">system_info</code>, <code className="text-surface-300">web_fetch</code>.
          </p>

        </section>

        {/* ─── Creating Plugins ─── */}
        <section className="mb-20">
          <SectionHeading id="plugins">Creating Plugins</SectionHeading>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            Plugins are standalone MCP servers that communicate via stdio JSON-RPC. Write them in
            any language: Python, Go, Node.js, Rust, etc.
          </p>

          <h4 className="mb-2 mt-6 text-sm font-semibold text-surface-200">Plugin structure</h4>
          <Terminal title="my-plugin/">
            <div className="text-surface-200">
              <pre className="whitespace-pre-wrap">{`manifest.json    # Plugin metadata, tool definitions
main.py          # Your MCP server (any language)
requirements.txt # Optional dependencies`}</pre>
            </div>
          </Terminal>

          <h4 className="mb-2 mt-6 text-sm font-semibold text-surface-200">manifest.json</h4>
          <Terminal title="manifest.json">
            <div className="text-surface-200">
              <pre className="whitespace-pre-wrap text-[12px]">{`{
  "name": "my-plugin",
  "version": "1.0.0",
  "description": "My custom tools",
  "runtime": "python",
  "command": "main.py",
  "env": ["MY_API_KEY"],
  "tools": [
    {
      "name": "my_tool",
      "description": "Does something useful",
      "inputSchema": {
        "type": "object",
        "required": ["input"],
        "properties": {
          "input": { "type": "string" }
        }
      }
    }
  ]
}`}</pre>
            </div>
          </Terminal>

          <h4 className="mb-2 mt-6 text-sm font-semibold text-surface-200">Deploy</h4>
          <Terminal title="terminal">
            <Comment># Deploy to local MCP server</Comment>
            <Cmd>yaver mcp deploy ./my-plugin</Cmd>
            <Output>Deployed my-plugin (1 tools registered)</Output>
            <Cmd>yaver mcp list</Cmd>
            <Output>{`NAME                 VERSION    TOOLS    STATUS
my-plugin            1.0.0      1        healthy`}</Output>
          </Terminal>

          <Terminal title="terminal">
            <Comment># Deploy to remote server</Comment>
            <Cmd>yaver mcp deploy ./my-plugin --server https://mcp.example.com --password secret</Cmd>
          </Terminal>

          <p className="mt-4 text-sm text-surface-400">
            See <code className="text-surface-300">mcp/plugins/example-hello/</code> in the repo for a complete Python example.
          </p>
        </section>

        {/* ─── Section 8: Security ─── */}
        <section className="mb-20">
          <SectionHeading id="security">Security</SectionHeading>
          <Prose>
            Yaver includes a sandbox that restricts what AI agents can do on
            your machine. The sandbox is enabled by default and blocks
            dangerous operations.
          </Prose>

          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900 p-6">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Default protections
            </h3>
            <ul className="space-y-2 text-sm text-surface-400">
              <li>
                &bull; <strong className="text-surface-300">Filesystem destruction</strong>{" "}
                &mdash; blocks <InlineCode>rm -rf /</InlineCode> and similar
                destructive patterns
              </li>
              <li>
                &bull; <strong className="text-surface-300">Privilege escalation</strong>{" "}
                &mdash; blocks <InlineCode>sudo</InlineCode>,{" "}
                <InlineCode>su</InlineCode>, and setuid operations
              </li>
              <li>
                &bull; <strong className="text-surface-300">Disk manipulation</strong>{" "}
                &mdash; blocks <InlineCode>mkfs</InlineCode>,{" "}
                <InlineCode>fdisk</InlineCode>, and partition tools
              </li>
              <li>
                &bull; <strong className="text-surface-300">Network exfiltration</strong>{" "}
                &mdash; blocks common exfiltration patterns
              </li>
            </ul>
          </div>

          <SubHeading>Configuration</SubHeading>
          <Prose>
            Customize the sandbox in your Yaver config file.
          </Prose>

          <div className="mb-8">
            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">
                  ~/.yaver/config.json
                </span>
              </div>
              <div className="terminal-body text-[13px] leading-relaxed">
                <pre className="text-surface-300">
                  {`{
  "sandbox": {
    "enabled": true,
    "allow_sudo": false,
    "blocked_commands": ["terraform destroy"]
  }
}`}
                </pre>
              </div>
            </div>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Sandbox options
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; <InlineCode>enabled</InlineCode> &mdash; Enable or
                  disable the sandbox (default: <InlineCode>true</InlineCode>)
                </li>
                <li>
                  &bull; <InlineCode>allow_sudo</InlineCode> &mdash; Allow
                  sudo commands (default: <InlineCode>false</InlineCode>)
                </li>
                <li>
                  &bull; <InlineCode>blocked_commands</InlineCode> &mdash; Add
                  custom commands to block
                </li>
              </ul>
            </div>

            <div className="rounded-xl border border-yellow-500/20 bg-yellow-500/5 p-6">
              <h4 className="mb-2 text-sm font-medium text-yellow-400/80">
                Warning
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Disabling the sandbox or allowing sudo gives AI agents full
                access to your system. Only do this if you understand the
                risks and trust the AI agent you&apos;re using.
              </p>
            </div>
          </div>
        </section>

        {/* Bottom CTA */}
        <div className="rounded-xl border border-surface-800 bg-surface-900 p-6 text-center">
          <p className="mb-2 text-sm font-medium text-surface-200">
            Need help?
          </p>
          <p className="text-sm text-surface-400">
            Open an issue on{" "}
            <a
              href="https://github.com/kivanccakmak/yaver/issues"
              target="_blank"
              rel="noopener noreferrer"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              GitHub
            </a>{" "}
            or email{" "}
            <a
              href="mailto:kivanc.cakmak@simkab.com"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              kivanc.cakmak@simkab.com
            </a>
          </p>
        </div>

        {/* Back to home */}
        <div className="mt-8 text-center">
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
