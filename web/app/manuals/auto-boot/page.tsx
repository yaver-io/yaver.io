import Link from "next/link";

export default function AutoBootManual() {
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
          Headless Dev Machine — Set Up Once, Code Forever
        </h1>
        <p className="mb-4 text-sm leading-relaxed text-surface-400">
          Turn any machine into a permanent, headless AI development server.
          Mac Mini under your desk, Linux box in a closet, cloud VPS — set it up once, and
          it runs forever. Control everything from your phone.
        </p>
        <p className="mb-12 text-sm leading-relaxed text-surface-400">
          This guide covers the full stack: BIOS auto-power-on, OS auto-login,
          Yaver as a system service, OAuth that survives reboots, auto-updates,
          and project discovery.
        </p>

        {/* Why */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Your always-on dev companion, from your pocket
          </h2>
          <p className="text-sm leading-relaxed text-surface-400">
            As a solo founder, your dev machine is your most valuable asset. It has your codebase,
            your AI agents, your running services. With this setup, it becomes a permanent,
            always-available development server. Pull your phone out on the bus, at a cafe, or on
            the couch — your full dev environment is one tap away.
          </p>
          <p className="mt-3 text-sm leading-relaxed text-surface-400">
            The full chain is automated — no manual intervention, no SSH, no VPN:
          </p>
          <ol className="mt-4 space-y-2 text-sm text-surface-400">
            <li className="flex gap-3">
              <span className="shrink-0 font-mono text-surface-500">1.</span>
              Power restored &rarr; machine boots automatically (BIOS/firmware)
            </li>
            <li className="flex gap-3">
              <span className="shrink-0 font-mono text-surface-500">2.</span>
              OS starts &rarr; user session activates (auto-login or systemd lingering)
            </li>
            <li className="flex gap-3">
              <span className="shrink-0 font-mono text-surface-500">3.</span>
              Yaver starts as a system service &rarr; reconnects to relay servers using saved OAuth token
            </li>
            <li className="flex gap-3">
              <span className="shrink-0 font-mono text-surface-500">4.</span>
              Auto-discovers all your projects, connects to the AI agent of your choice
            </li>
            <li className="flex gap-3">
              <span className="shrink-0 font-mono text-surface-500">5.</span>
              You open Yaver on your phone &rarr; everything works as if you&apos;re sitting at your desk
            </li>
          </ol>
        </section>

        {/* Auth & Persistence */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            How OAuth survives reboots
          </h2>
          <p className="text-sm leading-relaxed text-surface-400">
            You only need to sign in once. <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver auth</code> opens
            a browser for Google, Apple, or Microsoft sign-in. After you authenticate, Yaver saves a
            long-lived session token to <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">~/.yaver/config.json</code>.
          </p>
          <p className="mt-3 text-sm leading-relaxed text-surface-400">
            This token persists across reboots. The systemd service (or LaunchAgent on macOS) reads
            it on startup. No re-authentication needed — ever. If you rotate your password on
            Google/Apple/Microsoft, the Yaver token remains valid (it&apos;s a separate session).
          </p>
          <div className="terminal mt-4 mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">one-time setup</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Sign in once (requires a browser — do this on initial setup)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">yaver auth</span>
              </div>
              <div className="text-green-400/80 pl-2">Signed in as kivanc@acme.com</div>
              <div className="text-green-400/80 pl-2">Token saved to ~/.yaver/config.json</div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># For headless machines without a browser, sign in on another machine</div>
              <div className="text-surface-500"># and copy ~/.yaver/config.json to the headless box</div>
            </div>
          </div>
        </section>

        {/* Auto-update */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Auto-updates — never touch the binary again
          </h2>
          <p className="text-sm leading-relaxed text-surface-400">
            The Yaver agent checks for new releases on GitHub every 6 hours. When a new version
            is found, it downloads the binary for your platform, replaces itself, and restarts.
            Under systemd, the service auto-restarts with the new binary thanks to{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">Restart=on-failure</code>.
          </p>
          <p className="mt-3 text-sm leading-relaxed text-surface-400">
            You ship a new feature from your laptop, and within 6 hours your Mac Mini in the closet
            has it too. Zero manual intervention.
          </p>
          <div className="terminal mt-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">auto-update config</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Enable auto-update (on by default)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">yaver config set auto-update true</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Or install via Homebrew and let brew handle updates</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">brew upgrade yaver</span>
              </div>
            </div>
          </div>
        </section>

        {/* macOS */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            macOS (Mac Mini, MacBook, Mac Studio)
          </h2>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 1: Auto-power-on after power failure
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            Apple Silicon Macs and Intel Macs with T2 chip support automatic restart
            after power loss.
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Enable auto-restart after power failure</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  sudo pmset -a autorestart 1
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Verify the setting</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">pmset -g</span>
              </div>
              <div className="text-surface-500 pl-2">autorestart&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;1</div>
            </div>
          </div>
          <p className="mb-2 text-xs text-surface-500">
            On older Intel Macs without T2, you can also use:{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
              System Settings &rarr; Energy Saver &rarr; &quot;Start up automatically after a power failure&quot;
            </code>
          </p>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 2: Auto-login (optional, for headless setups)
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            For LaunchAgents to run, macOS needs a logged-in user session. Enable
            auto-login for your user:
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Enable auto-login (will prompt for password)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  sudo defaults write /Library/Preferences/com.apple.loginwindow autoLoginUser &quot;yourusername&quot;
                </span>
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            Or go to <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">System Settings &rarr; Users &amp; Groups &rarr; Login Options &rarr; Automatic login</code>.
            Note: FileVault must be disabled for auto-login to work.
          </p>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 3: Yaver auto-start via LaunchAgent
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            Create a LaunchAgent plist so <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver serve</code> starts on login:
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Install the auto-start service</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver config set auto-start true
                </span>
              </div>
              <div className="text-green-400/80 pl-2">
                LaunchAgent installed: ~/Library/LaunchAgents/io.yaver.agent.plist
              </div>
              <div className="text-green-400/80 pl-2">
                Yaver will start automatically on login.
              </div>
            </div>
          </div>
          <p className="mb-3 text-xs text-surface-500">
            Under the hood, this creates a plist file at{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
              ~/Library/LaunchAgents/io.yaver.agent.plist
            </code>{" "}
            that runs <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver serve</code> with{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">KeepAlive: true</code> and{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">RunAtLoad: true</code>.
          </p>
          <p className="text-xs text-surface-500">
            If you prefer manual control, you can also create the plist yourself:
          </p>
          <div className="mt-3 overflow-x-auto rounded-lg border border-surface-800 bg-surface-900/50 p-4">
            <pre className="text-xs leading-relaxed text-surface-400">
{`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>io.yaver.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/homebrew/bin/yaver</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/yaver.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/yaver.err</string>
</dict>
</plist>`}
            </pre>
          </div>
        </section>

        {/* Linux */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Linux (Ubuntu, Debian, Fedora, etc.)
          </h2>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 1: Auto-power-on after power failure (BIOS/UEFI)
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            This is a firmware setting, not an OS setting. Reboot your machine
            and enter BIOS/UEFI setup (usually by pressing <kbd className="rounded border border-surface-700 bg-surface-800 px-1.5 py-0.5 text-xs text-surface-300">DEL</kbd>,{" "}
            <kbd className="rounded border border-surface-700 bg-surface-800 px-1.5 py-0.5 text-xs text-surface-300">F2</kbd>, or{" "}
            <kbd className="rounded border border-surface-700 bg-surface-800 px-1.5 py-0.5 text-xs text-surface-300">F12</kbd> during boot).
          </p>
          <p className="mb-3 text-sm text-surface-400">
            Look for one of these settings (exact name varies by manufacturer):
          </p>
          <ul className="mb-4 space-y-2 text-sm text-surface-400">
            <li className="flex gap-3">
              <span className="text-surface-500">&#8226;</span>
              <span><strong className="text-surface-300">Power</strong> &rarr; &quot;After Power Loss&quot; &rarr; set to <strong className="text-surface-300">&quot;Power On&quot;</strong></span>
            </li>
            <li className="flex gap-3">
              <span className="text-surface-500">&#8226;</span>
              <span><strong className="text-surface-300">Advanced</strong> &rarr; &quot;AC Power Recovery&quot; or &quot;Restore on AC Power Loss&quot; &rarr; <strong className="text-surface-300">&quot;Power On&quot;</strong></span>
            </li>
            <li className="flex gap-3">
              <span className="text-surface-500">&#8226;</span>
              <span><strong className="text-surface-300">BIOS Features</strong> &rarr; &quot;State After G3&quot; &rarr; <strong className="text-surface-300">&quot;S0 State&quot;</strong> (means &quot;power on&quot;)</span>
            </li>
          </ul>
          <p className="text-xs text-surface-500">
            Common manufacturers: Dell (&quot;AC Recovery&quot;), HP (&quot;After Power Loss&quot;), Lenovo (&quot;After Power Loss&quot;), ASUS (&quot;Restore AC Power Loss&quot;), Intel NUC (&quot;After Power Failure&quot;).
          </p>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 2: Auto-login (optional)
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            For headless servers, auto-login is usually not needed since systemd
            user services can run with lingering enabled. But if you want it:
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Enable lingering so user services start at boot</div>
              <div className="text-surface-500"># (no login session needed)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  sudo loginctl enable-linger $USER
                </span>
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 3: Yaver auto-start via systemd
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># One command — creates, enables, and starts the systemd service</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver serve --install-systemd
                </span>
              </div>
              <div className="text-green-400/80 pl-2">
                Created: ~/.config/systemd/user/yaver.service
              </div>
              <div className="text-green-400/80 pl-2">
                Yaver agent installed as systemd user service.
              </div>
              <div className="text-green-400/80 pl-2">
                The agent starts automatically on login and survives reboots.
              </div>
            </div>
          </div>
          <p className="mb-3 text-xs text-surface-500">
            The service runs from your home directory, discovers all your projects automatically,
            and auto-updates from GitHub releases. Service file at{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
              ~/.config/systemd/user/yaver.service
            </code>.
            If you prefer manual setup:
          </p>
          <div className="overflow-x-auto rounded-lg border border-surface-800 bg-surface-900/50 p-4">
            <pre className="text-xs leading-relaxed text-surface-400">
{`[Unit]
Description=Yaver Agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/yaver serve
Restart=always
RestartSec=5

[Install]
WantedBy=default.target`}
            </pre>
          </div>
          <div className="terminal mt-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Manual service management</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">systemctl --user enable yaver</span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">systemctl --user start yaver</span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">systemctl --user status yaver</span>
              </div>
            </div>
          </div>
        </section>

        {/* Desktop PC BIOS */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Windows
          </h2>
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 1: BIOS auto-power-on
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            Same as Linux — configure your BIOS/UEFI to power on after AC power
            loss. See the Linux section above for manufacturer-specific setting
            names.
          </p>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 2: Yaver auto-start
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">powershell</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Install the auto-start service</div>
              <div>
                <span className="text-surface-400">&gt;</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver config set auto-start true
                </span>
              </div>
              <div className="text-green-400/80 pl-2">
                Startup entry added to Windows Task Scheduler.
              </div>
              <div className="text-green-400/80 pl-2">
                Yaver will start automatically on login.
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            On Windows, <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver config set auto-start true</code> registers a
            Task Scheduler entry that runs <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver serve</code> at user logon.
            For auto-login, go to <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">netplwiz</code> and uncheck
            &quot;Users must enter a user name and password to use this computer&quot;.
          </p>
        </section>

        {/* Summary */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Summary checklist
          </h2>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-6 font-medium text-surface-300">Layer</th>
                  <th className="pb-3 pr-6 font-medium text-surface-300">macOS</th>
                  <th className="pb-3 pr-6 font-medium text-surface-300">Linux</th>
                  <th className="pb-3 font-medium text-surface-300">Windows</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 font-medium text-surface-300">Auto-power-on</td>
                  <td className="py-3 pr-6"><code className="text-xs">pmset autorestart 1</code></td>
                  <td className="py-3 pr-6">BIOS setting</td>
                  <td className="py-3">BIOS setting</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 font-medium text-surface-300">Auto-login</td>
                  <td className="py-3 pr-6">System Settings</td>
                  <td className="py-3 pr-6"><code className="text-xs">loginctl enable-linger</code></td>
                  <td className="py-3"><code className="text-xs">netplwiz</code></td>
                </tr>
                <tr>
                  <td className="py-3 pr-6 font-medium text-surface-300">Yaver auto-start</td>
                  <td className="py-3 pr-6">LaunchAgent</td>
                  <td className="py-3 pr-6">systemd user service</td>
                  <td className="py-3">Task Scheduler</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-6">
          <h3 className="mb-2 text-sm font-semibold text-surface-200">
            TL;DR — three commands, then forget about it
          </h3>
          <div className="terminal mt-3">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
            </div>
            <div className="terminal-body space-y-1 text-[13px]">
              <div><span className="text-surface-400">$</span> <span className="text-surface-200">brew install kivanccakmak/yaver/yaver</span></div>
              <div><span className="text-surface-400">$</span> <span className="text-surface-200">yaver auth</span></div>
              <div><span className="text-surface-400">$</span> <span className="text-surface-200">yaver serve --install-systemd</span>  <span className="text-surface-500"># or: yaver config set auto-start true</span></div>
            </div>
          </div>
          <p className="mt-4 text-sm text-surface-400">
            Combined with BIOS auto-power-on, your machine becomes a permanent AI development
            server that auto-updates, auto-discovers your projects, and is always reachable from
            your phone. Your committed dev companion, always in your pocket.
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
            href="/faq"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            FAQ &rarr;
          </Link>
        </div>
      </div>
    </div>
  );
}
