import Link from "next/link";

function Terminal({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="terminal mb-4">
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

function MacCmd({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <span className="text-surface-400">$</span>{" "}
      <span className="text-surface-200 select-all">{children}</span>
    </div>
  );
}

function WinCmd({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <span className="text-surface-400">&gt;</span>{" "}
      <span className="text-surface-200 select-all">{children}</span>
    </div>
  );
}

export default function WindowsSshCodingBoxManual() {
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
          MacBook to Windows AI box over SSH
        </h1>
        <p className="mb-12 text-sm leading-relaxed text-surface-400">
          Turn a 32 GB Windows machine into an always-on coding box you can reach from macOS over
          SSH, keep stable over Tailscale, and use for Ollama, OpenCode, and editor-driven remote
          coding. This is the practical setup for a stronger desk machine backing a lighter laptop.
        </p>

        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">Target topology</h2>
          <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <pre className="overflow-x-auto text-xs text-surface-300">
{`MacBook
  -> SSH
  -> Windows PC
     -> Ollama
     -> qwen2.5-coder:14b
     -> OpenCode / terminal agents
  -> reachable over Tailscale
  -> stays awake on AC power`}
            </pre>
          </div>
        </section>

        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">1. Baseline the Windows host</h2>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            Install Windows OpenSSH Server, confirm the service is running, and use SSH keys from
            the Mac instead of relying on password auth. If the Windows account is an administrator,
            OpenSSH may read keys from
            <code className="mx-1 rounded bg-surface-900 px-1.5 py-0.5 text-surface-300">C:\ProgramData\ssh\administrators_authorized_keys</code>
            instead of the user profile&apos;s key file.
          </p>
          <Terminal title="windows powershell">
            <WinCmd>Start-Service sshd</WinCmd>
            <WinCmd>Set-Service -Name sshd -StartupType Automatic</WinCmd>
            <WinCmd>Get-Service sshd</WinCmd>
          </Terminal>
          <Terminal title="macos terminal">
            <MacCmd>ssh user@192.168.1.104</MacCmd>
          </Terminal>
        </section>

        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">2. Make the box stay up</h2>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            A remote coding box should not go to sleep in the middle of a model pull or an agent
            run. On AC power, disable the normal sleep timers and hibernation.
          </p>
          <Terminal title="windows powershell">
            <WinCmd>powercfg /change monitor-timeout-ac 0</WinCmd>
            <WinCmd>powercfg /change standby-timeout-ac 0</WinCmd>
            <WinCmd>powercfg /change disk-timeout-ac 0</WinCmd>
            <WinCmd>powercfg /change hibernate-timeout-ac 0</WinCmd>
            <WinCmd>powercfg /hibernate off</WinCmd>
          </Terminal>
          <p className="text-sm leading-relaxed text-surface-400">
            Keep the machine on AC. If this is a laptop and you want it closed on a shelf, also
            change the lid-close action separately in Windows power settings.
          </p>
        </section>

        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">3. Use Tailscale for the stable path</h2>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            Tailscale gives the box a stable private address and MagicDNS name. That matters once
            the MacBook is off the local LAN or the Windows box gets a new DHCP lease.
          </p>
          <Terminal title="windows powershell">
            <WinCmd>tailscale ip -4</WinCmd>
            <WinCmd>tailscale status</WinCmd>
          </Terminal>
          <Terminal title="macos terminal">
            <MacCmd>ssh user@carrotbytepc.tailc32088.ts.net</MacCmd>
            <MacCmd>ssh user@100.88.81.42</MacCmd>
          </Terminal>
          <p className="text-sm leading-relaxed text-surface-400">
            The Tailscale path does not replace OpenSSH. It gives OpenSSH a better network.
          </p>
        </section>

        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">4. Install Ollama on Windows</h2>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            For a 32 GB Windows machine, start with
            <code className="mx-1 rounded bg-surface-900 px-1.5 py-0.5 text-surface-300">qwen2.5-coder:14b</code>.
            It is a better everyday trade than forcing a larger model onto the box.
          </p>
          <Terminal title="windows powershell">
            <WinCmd>winget install -e --id Ollama.Ollama --accept-source-agreements --accept-package-agreements --silent</WinCmd>
            <WinCmd>ollama pull qwen2.5-coder:14b</WinCmd>
            <WinCmd>ollama run qwen2.5-coder:14b</WinCmd>
          </Terminal>
        </section>

        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">5. Install OpenCode</h2>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            If you want a terminal-native coding agent on the Windows box, install Node LTS and
            then OpenCode.
          </p>
          <Terminal title="windows powershell">
            <WinCmd>winget install -e --id OpenJS.NodeJS.LTS --accept-source-agreements --accept-package-agreements --silent</WinCmd>
            <WinCmd>npm install -g opencode-ai</WinCmd>
          </Terminal>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            Then point OpenCode at Ollama with a config like this:
          </p>
          <div className="rounded-lg bg-surface-900 p-4">
            <pre className="overflow-x-auto text-xs text-surface-300">
{`{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "ollama": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Ollama",
      "options": {
        "baseURL": "http://127.0.0.1:11434/v1"
      },
      "models": {
        "qwen2.5-coder:14b": {
          "name": "qwen2.5-coder:14b"
        }
      }
    }
  }
}`}
            </pre>
          </div>
        </section>

        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">6. Add a clean Mac SSH alias</h2>
          <Terminal title="~/.ssh/config">
            <div className="text-surface-200">
              <pre className="overflow-x-auto whitespace-pre-wrap text-xs">
{`Host carrotbytepc
  HostName carrotbytepc.tailc32088.ts.net
  User user
  IdentityFile ~/.ssh/id_ed25519`}
              </pre>
            </div>
          </Terminal>
          <Terminal title="macos terminal">
            <MacCmd>ssh carrotbytepc</MacCmd>
          </Terminal>
        </section>

        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">7. Daily workflow from the MacBook</h2>
          <Terminal title="macos terminal">
            <MacCmd>ssh carrotbytepc</MacCmd>
            <MacCmd>cd path\\to\\repo</MacCmd>
            <MacCmd>opencode</MacCmd>
          </Terminal>
          <p className="text-sm leading-relaxed text-surface-400">
            If you are using Antigravity or another editor with SSH-based remote workflows, reuse
            the same host alias and Windows box. Keep the transport layer simple: the box should be
            reachable from plain Terminal first, then from the editor.
          </p>
        </section>

        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">Operational checklist</h2>
          <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <ul className="space-y-2 text-sm text-surface-400">
              <li>OpenSSH Server installed and on automatic startup</li>
              <li>Tailscale signed in and showing a stable tailnet IP</li>
              <li>sleep and hibernate disabled on AC</li>
              <li>Ollama installed with <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-300">qwen2.5-coder:14b</code></li>
              <li>Node LTS + OpenCode installed if you want a terminal agent</li>
              <li>Mac SSH alias configured for the Tailscale hostname</li>
            </ul>
          </div>
        </section>
      </div>
    </div>
  );
}
