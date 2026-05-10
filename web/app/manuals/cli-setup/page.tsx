import Link from "next/link";

export default function CLISetupManual() {
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
          CLI setup &amp; usage guide
        </h1>
        <p className="mb-12 text-sm leading-relaxed text-surface-400">
          Install the Yaver CLI, sign in, and start sending tasks to your dev
          machine from your phone. This guide covers installation, authentication,
          running the agent, and the most useful commands.
        </p>

        {/* Installation */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Installation
          </h2>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Fastest start (npm bootstrap)
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  npm install -g yaver-cli
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver auth
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver serve
                </span>
              </div>
            </div>
          </div>
          <p className="mb-4 text-xs text-surface-500">
            This single install gives you the same <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver</code> command
            for both the Go agent workflow and third-party React Native push via <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver push</code>.
          </p>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Install from npm
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  npm install -g yaver-cli
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  npm install -g yaver-cli@latest
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver auth
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver auth --headless
                </span>
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            What happens next
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver auth
                </span>
              </div>
              <div>
                <span className="text-surface-400">#</span>{" "}
                <span className="text-surface-200 select-all">
                  starts the agent if needed and auto-registers Yaver MCP
                </span>
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Notes
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body text-[13px]">
              <div>
                <span className="text-surface-400">#</span>{" "}
                <span className="text-surface-200 select-all">
                  npm install -g yaver-cli is the supported install path on macOS, Linux, and WSL.
                </span>
              </div>
              <div>
                <span className="text-surface-400">#</span>{" "}
                <span className="text-surface-200 select-all">
                  Use the same command with @latest to upgrade an existing machine.
                </span>
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Supported method
          </h3>
          <div className="overflow-x-auto mb-4">
            <table className="w-full text-sm">
              <tbody className="text-surface-400">
                <tr>
                  <td className="py-2 pr-4 font-medium text-surface-300">npm</td>
                  <td className="py-2 font-mono text-xs">npm install -g yaver-cli</td>
                </tr>
              </tbody>
            </table>
          </div>

          <p className="text-xs text-surface-500">
            The npm package provides the <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver</code> command for both agent and
            push/dev flows, and it is the install path Yaver keeps current.
          </p>
        </section>

        {/* Shell completions */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Shell completions
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Enable tab completion for all commands and subcommands.
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Bash &mdash; add to ~/.bashrc</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">{`eval "$(yaver completion bash)"`}</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Zsh &mdash; add to ~/.zshrc</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">{`eval "$(yaver completion zsh)"`}</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Fish</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">yaver completion fish | source</span>
              </div>
            </div>
          </div>
        </section>

        {/* MCP setup */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            MCP &mdash; connect to coding runners
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Yaver exposes 473 tools via the Model Context Protocol. One command configures each first-class runner CLI:
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver mcp setup claude-code</span>
                <span className="ml-2 text-surface-500"># Claude Code</span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver mcp setup codex</span>
                <span className="ml-2 text-surface-500"># Codex CLI</span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver mcp setup opencode</span>
                <span className="ml-2 text-surface-500"># opencode</span>
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            See the full{" "}
            <Link href="/docs/mcp" className="text-surface-300 underline underline-offset-2 hover:text-surface-100">
              MCP documentation
            </Link>{" "}
            for available tools, network mode, ACL, and plugins.
          </p>
        </section>

        {/* Sign in & start */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Sign in &amp; start the agent
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Two commands to get going. You only need to sign in once — your
            session is saved locally and persists across reboots.
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div className="text-surface-500"># Sign in (opens your browser)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver auth</span>
              </div>
              <div className="pl-2 text-surface-500">Opening browser...</div>
              <div className="pl-2 text-green-400/80">Signed in as you@gmail.com</div>
              <div className="pl-2 text-green-400/80">Agent started and running in background.</div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># That&apos;s it. Your machine is reachable from your phone.</div>
              <div className="text-surface-500"># The agent starts automatically after sign-in.</div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver auth</code> handles
            everything: signs you in via Apple, Google, or Microsoft, registers your
            device, and starts the agent in the background. You&apos;re done.
          </p>
        </section>

        {/* Commands reference */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Commands
          </h2>
          <p className="mb-6 text-sm text-surface-400">
            Here are the commands you&apos;ll use most. Run{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver help</code> to see
            them all.
          </p>

          <div className="space-y-6">
            <CommandBlock
              name="yaver auth"
              description="Sign in and start the agent. Opens your browser for Apple, Google, or Microsoft sign-in. Only needed once — your session persists across reboots."
              example={null}
            />
            <CommandBlock
              name="yaver status"
              description="See if the agent is running, which account you're signed in as, and your connection status."
              example={`$ yaver status\n  Signed in as:  you@gmail.com\n  Agent:         running (pid 12345)\n  Relay:         connected (eu-hel)\n  Runner:        Claude Code`}
            />
            <CommandBlock
              name="yaver set-runner"
              description="Choose which AI agent runs your tasks. Supports Claude Code, OpenAI Codex, OpenCode, or any custom CLI command."
              example={`$ yaver set-runner claude      # Claude Code (default)\n$ yaver set-runner codex       # OpenAI Codex\n$ yaver set-runner opencode    # OpenCode (BYOK Anthropic / OpenAI / OpenRouter / GLM, or local Ollama)\n$ yaver set-runner custom "my-tool --auto {prompt}"`}
            />
            <CommandBlock
              name="yaver tmux"
              description="Discover and manage tmux sessions. Adopt existing sessions to control them from the mobile app — start Claude Code on your laptop, walk away, pick it up on your phone."
              example={`$ yaver tmux list                 # List all sessions with agent detection\n  my-claude       claude        unrelated     1 window(s)\n  dev-codex       codex         unrelated     2 window(s)\n\n$ yaver tmux adopt my-claude      # Adopt as a Yaver task\n  Adopted tmux session "my-claude" as task a1b2c3d4\n\n$ yaver tmux detach a1b2c3d4      # Stop monitoring (session keeps running)`}
            />
            <CommandBlock
              name="yaver devices"
              description="List all your registered devices. Useful when you have multiple dev machines."
              example={`$ yaver devices\n  1. MacBook Pro (this device)  — online\n  2. Linux Server               — online\n  3. Windows Desktop            — offline`}
            />
            <CommandBlock
              name="yaver connect"
              description="Connect to one of your other dev machines from the terminal. Like SSH, but through Yaver's encrypted P2P connection."
              example={`$ yaver connect\n$ yaver connect --device <device-id>`}
            />
            <CommandBlock
              name="yaver attach"
              description="Open an interactive terminal to see running tasks and type prompts directly — like Claude Code but connected remotely."
              example={`$ yaver attach`}
            />
            <CommandBlock
              name="yaver logs"
              description="View agent logs. Useful for debugging connection issues."
              example={`$ yaver logs           # last 50 lines\n$ yaver logs -f        # follow (live tail)\n$ yaver logs -n 200    # last 200 lines`}
            />
            <CommandBlock
              name="yaver stop"
              description="Stop the agent. It won't accept tasks until you start it again."
              example={null}
            />
            <CommandBlock
              name="yaver restart"
              description="Restart the agent. Useful after changing runners or if something seems stuck."
              example={null}
            />
          </div>
        </section>

        {/* Serve flags */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Advanced: serve options
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Most users never need these — <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver auth</code> starts
            the agent with sensible defaults. But if you want more control:
          </p>

          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Run in foreground with verbose logging</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver serve --debug</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Set a specific working directory for tasks</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver serve --work-dir ~/projects/my-app</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Disable relay (only accept direct connections)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver serve --no-relay</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Wait for existing Claude sessions to finish</div>
              <div className="text-surface-500"># before starting new tasks</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver serve --wait-for-session</span>
              </div>
            </div>
          </div>

          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-6 font-medium text-surface-300">Flag</th>
                  <th className="pb-3 font-medium text-surface-300">What it does</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 font-mono text-xs text-surface-300">--debug</td>
                  <td className="py-3">Run in foreground with verbose logging. Great for troubleshooting.</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 font-mono text-xs text-surface-300">--work-dir</td>
                  <td className="py-3">Set the directory where tasks run. Defaults to current directory.</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 font-mono text-xs text-surface-300">--port</td>
                  <td className="py-3">HTTP server port. Default: 18080.</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 font-mono text-xs text-surface-300">--no-relay</td>
                  <td className="py-3">Disable relay tunnels. Only accept direct connections on LAN.</td>
                </tr>
                <tr>
                  <td className="py-3 pr-6 font-mono text-xs text-surface-300">--wait-for-session</td>
                  <td className="py-3">Queue new tasks if another Claude Code session is already running.</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        {/* Update */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Updating Yaver
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Match the upgrade command to the path you installed with. All paths
            resolve to the same underlying Go agent binary from the latest
            GitHub release.
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># npm (all platforms)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  npm install -g yaver-cli
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Upgrade later</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  npm install -g yaver-cli@latest
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Authenticate and auto-start the agent</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver auth
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Headless boxes</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver auth --headless
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Verify — should print the version you just installed</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver version</span>
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            Built-in auto-updater: <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver serve</code> checks GitHub for new releases every 6 hours and hot-swaps the binary in place. Running Yaver as a <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">systemd</code> user service (Linux) or as a login item (macOS) means you generally do not need to upgrade by hand.
          </p>
        </section>

        {/* Cleanup */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Cleanup &amp; uninstall
          </h2>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Sign out (keeps the binary installed)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver signout</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Remove all local data (auth, tasks, logs)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver purge</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Full uninstall (stop agent + remove config)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver uninstall</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Then remove the npm package</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">npm uninstall -g yaver-cli</span>
              </div>
            </div>
          </div>
        </section>

        {/* Tips */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Tips
          </h2>
          <ul className="space-y-3 text-sm text-surface-400">
            <li className="flex gap-3">
              <span className="text-surface-500">&#8226;</span>
              <span>
                <strong className="text-surface-300">Headless machines</strong> — Use{" "}
                <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver auth --token &lt;token&gt;</code>{" "}
                if you can&apos;t open a browser. Get the token by signing in on another machine first.
              </span>
            </li>
            <li className="flex gap-3">
              <span className="text-surface-500">&#8226;</span>
              <span>
                <strong className="text-surface-300">Multiple machines</strong> — Install Yaver on
                as many machines as you want. They all show up in the mobile app. Pick which one
                to send tasks to.
              </span>
            </li>
            <li className="flex gap-3">
              <span className="text-surface-500">&#8226;</span>
              <span>
                <strong className="text-surface-300">Custom AI tools</strong> — Any CLI command
                that accepts a prompt works with Yaver. Use{" "}
                <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">{`yaver set-runner custom "my-tool {prompt}"`}</code>{" "}
                to bring your own.
              </span>
            </li>
            <li className="flex gap-3">
              <span className="text-surface-500">&#8226;</span>
              <span>
                <strong className="text-surface-300">Check logs first</strong> — If something
                doesn&apos;t work, run{" "}
                <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver logs -f</code>{" "}
                to see what&apos;s happening in real time.
              </span>
            </li>
          </ul>
        </section>

        <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-6">
          <h3 className="mb-2 text-sm font-semibold text-surface-200">
            Need more?
          </h3>
          <p className="text-sm text-surface-400">
            Check the{" "}
            <Link href="/faq" className="text-surface-300 underline underline-offset-2 hover:text-surface-100">
              FAQ
            </Link>{" "}
            for common questions, or the{" "}
            <Link href="/manuals/auto-boot" className="text-surface-300 underline underline-offset-2 hover:text-surface-100">
              auto-boot guide
            </Link>{" "}
            to make your machine fully autonomous after power outages.
          </p>
        </div>

        <div className="mt-12 flex items-center justify-between">
          <Link
            href="/manuals"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            &larr; All manuals
          </Link>
          <Link
            href="/manuals/auto-boot"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            Auto-boot guide &rarr;
          </Link>
        </div>
      </div>
    </div>
  );
}

function CommandBlock({
  name,
  description,
  example,
}: {
  name: string;
  description: string;
  example: string | null;
}) {
  return (
    <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
      <code className="text-sm font-semibold text-surface-100">{name}</code>
      <p className="mt-1 text-sm text-surface-400">{description}</p>
      {example && (
        <pre className="mt-3 overflow-x-auto rounded-md bg-surface-950 p-3 text-xs leading-relaxed text-surface-400">
          {example}
        </pre>
      )}
    </div>
  );
}
