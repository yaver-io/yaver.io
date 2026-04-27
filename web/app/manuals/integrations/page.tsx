import Link from "next/link";

export default function IntegrationsManual() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <Link
          href="/manuals"
          className="mb-8 inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to Manuals
        </Link>

        <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
          Integrations guide
        </h1>
        <p className="mb-12 text-sm leading-relaxed text-surface-400">
          Set up notifications, CI/CD webhooks, MCP tools, and session
          transfer. All integrations connect to your Yaver agent over P2P
          encrypted connections &mdash; no data touches third-party servers
          unless you configure a notification channel.
        </p>

        {/* Table of Contents */}
        <nav className="mb-12 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
          <h2 className="mb-3 text-sm font-semibold text-surface-200">
            On this page
          </h2>
          <ul className="space-y-1.5 text-sm text-surface-400">
            <li>
              <a href="#notifications" className="hover:text-surface-100">
                1. Notifications (Telegram / Discord / Slack)
              </a>
            </li>
            <li>
              <a href="#cicd-webhooks" className="hover:text-surface-100">
                2. CI/CD Webhooks
              </a>
            </li>
            <li>
              <a href="#mcp-tools" className="hover:text-surface-100">
                3. MCP Tools
              </a>
            </li>
            <li>
              <a href="#session-transfer" className="hover:text-surface-100">
                4. Session Transfer
              </a>
            </li>
          </ul>
        </nav>

        {/* ─── Notifications ─── */}
        <section id="notifications" className="mb-16">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            1. Notifications
          </h2>
          <p className="mb-6 text-sm leading-relaxed text-surface-400">
            Get notified when tasks complete, fail, or need input. Configure
            one or more channels in your Yaver agent config.
          </p>

          {/* Telegram */}
          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Telegram
            </h3>
            <ol className="space-y-3 text-sm text-surface-400">
              <li className="flex gap-3">
                <span className="shrink-0 font-mono text-surface-500">1.</span>
                <span>
                  Open Telegram and search for{" "}
                  <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-300">
                    @BotFather
                  </code>
                  . Send{" "}
                  <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-300">
                    /newbot
                  </code>{" "}
                  and follow the prompts to create a bot. Copy the API token.
                </span>
              </li>
              <li className="flex gap-3">
                <span className="shrink-0 font-mono text-surface-500">2.</span>
                <span>
                  Get your chat ID: send a message to your bot, then visit{" "}
                  <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-300 break-all">
                    https://api.telegram.org/bot&lt;TOKEN&gt;/getUpdates
                  </code>{" "}
                  and find{" "}
                  <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-300">
                    chat.id
                  </code>{" "}
                  in the response.
                </span>
              </li>
              <li className="flex gap-3">
                <span className="shrink-0 font-mono text-surface-500">3.</span>
                <span>Configure in Yaver:</span>
              </li>
            </ol>
            <pre className="mt-3 rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`yaver notify add telegram \\
  --bot-token "123456:ABC-DEF..." \\
  --chat-id "987654321"`}</code>
            </pre>
          </div>

          {/* Discord */}
          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Discord
            </h3>
            <ol className="space-y-3 text-sm text-surface-400">
              <li className="flex gap-3">
                <span className="shrink-0 font-mono text-surface-500">1.</span>
                <span>
                  In your Discord server, go to{" "}
                  <strong className="text-surface-300">
                    Channel Settings &rarr; Integrations &rarr; Webhooks
                  </strong>
                  . Click <strong className="text-surface-300">New Webhook</strong>,
                  name it, and copy the webhook URL.
                </span>
              </li>
              <li className="flex gap-3">
                <span className="shrink-0 font-mono text-surface-500">2.</span>
                <span>Configure in Yaver:</span>
              </li>
            </ol>
            <pre className="mt-3 rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`yaver notify add discord \\
  --webhook-url "https://discord.com/api/webhooks/..."`}</code>
            </pre>
          </div>

          {/* Slack */}
          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Slack
            </h3>
            <ol className="space-y-3 text-sm text-surface-400">
              <li className="flex gap-3">
                <span className="shrink-0 font-mono text-surface-500">1.</span>
                <span>
                  Go to{" "}
                  <a
                    href="https://api.slack.com/apps"
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
                  >
                    api.slack.com/apps
                  </a>
                  . Create a new app (or use an existing one). Under{" "}
                  <strong className="text-surface-300">
                    Incoming Webhooks
                  </strong>
                  , activate and create a webhook URL for your channel.
                </span>
              </li>
              <li className="flex gap-3">
                <span className="shrink-0 font-mono text-surface-500">2.</span>
                <span>Configure in Yaver:</span>
              </li>
            </ol>
            <pre className="mt-3 rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`yaver notify add slack \\
  --webhook-url "https://hooks.slack.com/services/T.../B.../xxx"`}</code>
            </pre>
          </div>

          <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-2 text-sm font-semibold text-surface-200">
              Managing notifications
            </h3>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`yaver notify list           # list configured channels
yaver notify test telegram  # send a test notification
yaver notify remove discord # remove a channel`}</code>
            </pre>
          </div>
        </section>

        {/* ─── CI/CD Webhooks ─── */}
        <section id="cicd-webhooks" className="mb-16">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            2. CI/CD Webhooks
          </h2>
          <p className="mb-6 text-sm leading-relaxed text-surface-400">
            Trigger Yaver tasks from your CI/CD pipelines. Your agent
            exposes a webhook endpoint that accepts task creation requests.
          </p>

          {/* GitHub Actions */}
          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              GitHub Actions
            </h3>
            <p className="mb-3 text-sm text-surface-400">
              Add a step to your workflow that sends a webhook to your Yaver
              agent:
            </p>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`# .github/workflows/notify-yaver.yml
- name: Trigger Yaver task
  run: |
    curl -X POST https://your-agent:18080/webhooks/trigger \\
      -H "Content-Type: application/json" \\
      -H "Authorization: Bearer \${{ secrets.YAVER_TOKEN }}" \\
      -H "X-Webhook-Secret: \${{ secrets.YAVER_WEBHOOK_SECRET }}" \\
      -d '{
        "prompt": "Review the latest changes in this PR",
        "runner": "claude",
        "metadata": {
          "repo": "\${{ github.repository }}",
          "sha": "\${{ github.sha }}"
        }
      }'`}</code>
            </pre>
          </div>

          {/* GitLab CI */}
          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              GitLab CI
            </h3>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`# .gitlab-ci.yml
trigger_yaver:
  stage: deploy
  script:
    - |
      curl -X POST https://your-agent:18080/webhooks/trigger \\
        -H "Content-Type: application/json" \\
        -H "Authorization: Bearer $YAVER_TOKEN" \\
        -H "X-Webhook-Secret: $YAVER_WEBHOOK_SECRET" \\
        -d '{
          "prompt": "Deploy completed. Run post-deploy checks.",
          "runner": "claude"
        }'`}</code>
            </pre>
          </div>

          {/* Generic */}
          <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Generic webhook
            </h3>
            <p className="mb-3 text-sm text-surface-400">
              Any system that can make HTTP POST requests can trigger Yaver
              tasks:
            </p>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`POST /webhooks/trigger HTTP/1.1
Host: your-agent:18080
Content-Type: application/json
Authorization: Bearer <token>
X-Webhook-Secret: <secret>

{
  "prompt": "Task description here",
  "runner": "claude",           // optional: claude, codex, opencode, custom
  "model": "sonnet",            // optional: sonnet, opus, haiku
  "customCommand": "my-script", // optional: for runner=custom
  "metadata": {}                // optional: passed through to task
}`}</code>
            </pre>
            <p className="mt-3 text-xs text-surface-500">
              Configure the webhook secret with{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                yaver webhook setup --secret &lt;your-secret&gt;
              </code>
            </p>
          </div>
        </section>

        {/* ─── MCP Tools ─── */}
        <section id="mcp-tools" className="mb-16">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            3. MCP Tools
          </h2>
          <p className="mb-6 text-sm leading-relaxed text-surface-400">
            Yaver exposes 473 MCP tools. Connect from Claude Desktop, Claude
            Code, or any MCP-compatible client to control your agent
            programmatically.
          </p>

          {/* Connect as MCP server */}
          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Connect Yaver as MCP server to Claude Code
            </h3>
            <p className="mb-3 text-sm text-surface-400">
              Add Yaver to your Claude Desktop config:
            </p>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`// claude_desktop_config.json
{
  "mcpServers": {
    "yaver": {
      "command": "yaver",
      "args": ["mcp"]
    }
  }
}`}</code>
            </pre>
            <p className="mt-3 text-sm text-surface-400">
              For remote access (e.g., from Claude Web UI):
            </p>
            <pre className="mt-2 rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`yaver mcp --mode http --port 18090`}</code>
            </pre>
          </div>

          {/* Available tools */}
          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Available MCP tools (selection)
            </h3>
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-surface-800 text-left">
                    <th className="pb-3 pr-6 font-medium text-surface-300">
                      Tool
                    </th>
                    <th className="pb-3 font-medium text-surface-300">
                      Description
                    </th>
                  </tr>
                </thead>
                <tbody className="text-surface-400">
                  <tr className="border-b border-surface-800/40">
                    <td className="py-2 pr-6 font-mono text-xs text-surface-300">
                      task_create
                    </td>
                    <td className="py-2">Create a new AI task</td>
                  </tr>
                  <tr className="border-b border-surface-800/40">
                    <td className="py-2 pr-6 font-mono text-xs text-surface-300">
                      task_list
                    </td>
                    <td className="py-2">List all tasks</td>
                  </tr>
                  <tr className="border-b border-surface-800/40">
                    <td className="py-2 pr-6 font-mono text-xs text-surface-300">
                      task_output
                    </td>
                    <td className="py-2">Get task output</td>
                  </tr>
                  <tr className="border-b border-surface-800/40">
                    <td className="py-2 pr-6 font-mono text-xs text-surface-300">
                      session_list
                    </td>
                    <td className="py-2">List tmux sessions</td>
                  </tr>
                  <tr className="border-b border-surface-800/40">
                    <td className="py-2 pr-6 font-mono text-xs text-surface-300">
                      session_transfer
                    </td>
                    <td className="py-2">
                      Transfer session to another machine
                    </td>
                  </tr>
                  <tr className="border-b border-surface-800/40">
                    <td className="py-2 pr-6 font-mono text-xs text-surface-300">
                      file_search
                    </td>
                    <td className="py-2">Search files in workspace</td>
                  </tr>
                  <tr className="border-b border-surface-800/40">
                    <td className="py-2 pr-6 font-mono text-xs text-surface-300">
                      git_status
                    </td>
                    <td className="py-2">Get git status of workspace</td>
                  </tr>
                  <tr className="border-b border-surface-800/40">
                    <td className="py-2 pr-6 font-mono text-xs text-surface-300">
                      notify_send
                    </td>
                    <td className="py-2">
                      Send notification via configured channel
                    </td>
                  </tr>
                  <tr>
                    <td className="py-2 pr-6 font-mono text-xs text-surface-300">
                      webhook_trigger
                    </td>
                    <td className="py-2">Trigger a webhook</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p className="mt-3 text-xs text-surface-500">
              Run{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                yaver mcp --list-tools
              </code>{" "}
              for the full list of 473 tools.
            </p>
          </div>

          {/* Configuration */}
          <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Configuration examples
            </h3>
            <p className="mb-3 text-sm text-surface-400">
              Chain Yaver with other MCP servers using the Agent Communication
              Layer (ACL):
            </p>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`# Add a custom MCP peer
yaver acl add my-mcp-server http://localhost:8765/mcp

# Add a remote database MCP server
yaver acl add db https://db-server:8080/mcp

# List connected MCP peers
yaver acl list`}</code>
            </pre>
          </div>
        </section>

        {/* ─── Session Transfer ─── */}
        <section id="session-transfer" className="mb-16">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            4. Session Transfer
          </h2>
          <p className="mb-6 text-sm leading-relaxed text-surface-400">
            Move AI agent sessions between machines. Start a Claude Code
            session on your laptop, transfer it to a headless server, and keep
            working from your phone.
          </p>

          {/* CLI usage */}
          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              CLI usage
            </h3>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`# List transferable sessions
yaver session list

# Transfer a session to another device
yaver session transfer abc12345 --to my-server

# Export a session as a bundle (for manual transfer)
yaver session export abc12345 --output bundle.json

# Import a session bundle on the target machine
yaver session import --input bundle.json`}</code>
            </pre>
          </div>

          {/* Mobile usage */}
          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Mobile usage
            </h3>
            <p className="text-sm text-surface-400">
              In the Yaver mobile app, navigate to a running session and tap{" "}
              <strong className="text-surface-300">Transfer</strong>. Select the
              target device from your device list. The session&apos;s conversation
              history, agent state, and workspace context are bundled and sent
              to the target machine over your P2P connection.
            </p>
          </div>

          {/* MCP usage */}
          <div className="rounded-xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              MCP usage (from within Claude Code)
            </h3>
            <p className="mb-3 text-sm text-surface-400">
              Session transfer is available as an MCP tool. From within Claude
              Code, simply ask:
            </p>
            <pre className="rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">
              <code>{`# Claude Code will use the session_transfer MCP tool automatically:
"Transfer this session to my server"
"Move this conversation to my-desktop"
"Export this session as a bundle"`}</code>
            </pre>
            <p className="mt-3 text-xs text-surface-500">
              The MCP tools{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                session_list
              </code>
              ,{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                session_transfer
              </code>
              ,{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                session_export
              </code>
              , and{" "}
              <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                session_import
              </code>{" "}
              are all available when Yaver is connected as an MCP server.
            </p>
          </div>
        </section>

        {/* API Endpoints Reference */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            API endpoints reference
          </h2>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Endpoint
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Method
                  </th>
                  <th className="pb-3 font-medium text-surface-300">
                    Description
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/40">
                  <td className="py-2 pr-4 font-mono text-xs text-surface-300">
                    /webhooks/trigger
                  </td>
                  <td className="py-2 pr-4">POST</td>
                  <td className="py-2">Trigger a task via webhook</td>
                </tr>
                <tr className="border-b border-surface-800/40">
                  <td className="py-2 pr-4 font-mono text-xs text-surface-300">
                    /session/list
                  </td>
                  <td className="py-2 pr-4">GET</td>
                  <td className="py-2">List transferable sessions</td>
                </tr>
                <tr className="border-b border-surface-800/40">
                  <td className="py-2 pr-4 font-mono text-xs text-surface-300">
                    /session/export
                  </td>
                  <td className="py-2 pr-4">POST</td>
                  <td className="py-2">Export session bundle</td>
                </tr>
                <tr>
                  <td className="py-2 pr-4 font-mono text-xs text-surface-300">
                    /session/import
                  </td>
                  <td className="py-2 pr-4">POST</td>
                  <td className="py-2">Import session bundle</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <div className="flex flex-col gap-3 sm:flex-row">
          <Link href="/docs/mcp" className="btn-primary px-6 py-3 text-sm">
            MCP Documentation
          </Link>
          <Link
            href="/manuals"
            className="btn-secondary px-6 py-3 text-sm"
          >
            All Manuals
          </Link>
        </div>
      </div>
    </div>
  );
}
