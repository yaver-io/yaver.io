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

export default function CloudMachinesPage() {
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
            Cloud Machines
          </h1>
          <p className="text-sm leading-relaxed text-surface-400">
            Dedicated cloud dev machines, accessible from your phone via Yaver.
            Shared CPU for general development, GPU for local AI inference and
            voice. No SSH keys, no port forwarding &mdash; authenticate with
            your existing Apple, Google, or Microsoft account.
          </p>
        </div>

        {/* Table of contents */}
        <div className="mb-16 rounded-xl border border-surface-800 bg-surface-900 p-6">
          <h3 className="mb-4 text-sm font-semibold text-surface-200">
            On this page
          </h3>
          <nav className="space-y-2 text-sm">
            {[
              ["overview", "Overview"],
              ["machine-specs", "Machine Specs"],
              ["getting-started", "Getting Started"],
              ["multi-user", "Multi-User / Team Access"],
              ["team-management", "Team Management"],
              ["auth-flow", "Auth Flow"],
              ["workspace-isolation", "Workspace Isolation"],
              ["self-hosting", "Self-Hosting"],
              ["api-reference", "API Reference"],
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

        {/* ─── Overview ─── */}
        <section className="mb-20">
          <SectionHeading id="overview">Overview</SectionHeading>
          <Prose>
            Yaver Cloud Machines are dedicated dev servers that appear as devices
            in your Yaver app. They run the Yaver agent, pre-configured with
            development tooling and (on GPU tier) a full local AI stack. Connect
            from your phone, send tasks, and get results &mdash; same workflow
            as a local machine, but always on and always reachable.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                CPU Machine &mdash; $49/mo
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                8 vCPU, 16 GB RAM, 160 GB NVMe. Pre-installed with Node.js,
                Python, Go, Rust, Docker, Expo CLI, EAS CLI, and the Yaver
                server. General-purpose development for teams that run AI agents
                in the cloud (Claude Code, Codex, Aider) without needing local
                GPU inference.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                GPU Machine &mdash; $449/mo
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                16 vCPU, 64 GB RAM, 320 GB NVMe, NVIDIA RTX 4000 with 20 GB
                VRAM. Everything in the CPU tier, plus Ollama with Qwen 2.5
                Coder 32B pre-loaded, PersonaPlex 7B for real-time voice AI,
                Whisper for speech-to-text, and the full CUDA toolkit. Run local
                models, voice conversations, and GPU-accelerated builds from
                your phone.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Machine Specs ─── */}
        <section className="mb-20">
          <SectionHeading id="machine-specs">Machine Specs</SectionHeading>
          <Prose>
            Both tiers are dedicated machines &mdash; no noisy neighbors, no
            shared CPU time. Resources are yours 24/7.
          </Prose>

          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-6 font-medium text-surface-200">Spec</th>
                  <th className="pb-3 pr-6 font-medium text-surface-200">CPU (cx42)</th>
                  <th className="pb-3 font-medium text-surface-200">GPU (gex44)</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-2.5 pr-6 text-surface-300">vCPU</td>
                  <td className="py-2.5 pr-6">8</td>
                  <td className="py-2.5">16</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-2.5 pr-6 text-surface-300">RAM</td>
                  <td className="py-2.5 pr-6">16 GB</td>
                  <td className="py-2.5">64 GB</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-2.5 pr-6 text-surface-300">Storage</td>
                  <td className="py-2.5 pr-6">160 GB NVMe</td>
                  <td className="py-2.5">320 GB NVMe</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-2.5 pr-6 text-surface-300">GPU</td>
                  <td className="py-2.5 pr-6">&mdash;</td>
                  <td className="py-2.5">NVIDIA RTX 4000, 20 GB VRAM</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-2.5 pr-6 text-surface-300">Price</td>
                  <td className="py-2.5 pr-6">$49/mo</td>
                  <td className="py-2.5">$449/mo</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-2.5 pr-6 text-surface-300">Dev tooling</td>
                  <td className="py-2.5 pr-6">Node.js, Python, Go, Rust, Docker, Expo CLI, EAS CLI</td>
                  <td className="py-2.5">Everything in CPU</td>
                </tr>
                <tr>
                  <td className="py-2.5 pr-6 text-surface-300">AI stack</td>
                  <td className="py-2.5 pr-6">&mdash;</td>
                  <td className="py-2.5">Ollama, Qwen 2.5 Coder 32B, PersonaPlex 7B, Whisper, CUDA toolkit</td>
                </tr>
              </tbody>
            </table>
          </div>

          <Prose>
            Both tiers include the Yaver server pre-installed and running. The
            machine registers itself with Convex on boot and appears in your
            device list automatically.
          </Prose>
        </section>

        {/* ─── Getting Started ─── */}
        <section className="mb-20">
          <SectionHeading id="getting-started">Getting Started</SectionHeading>
          <Prose>
            Subscribe to a machine tier from the Yaver app or web dashboard.
            Your machine provisions in approximately 10 minutes. Once ready, it
            appears in your device list &mdash; no setup required on your end.
          </Prose>

          <div className="mb-8">
            <Terminal title="provisioning">
              <Comment># 1. Subscribe from the app or web dashboard</Comment>
              <Comment># 2. Machine provisions (~10 min)</Comment>
              <Comment># 3. Machine appears in your Yaver device list</Comment>
              <Divider />
              <Comment># Verify from the CLI</Comment>
              <Cmd>yaver devices</Cmd>
              <Output>
                {`NAME                STATUS    TYPE    LOCATION
MacBook-Air         online    local   LAN
cloud-gpu-01        online    cloud   eu-central`}
              </Output>
              <Divider />
              <Comment># Connect and send a task</Comment>
              <Cmd>yaver connect cloud-gpu-01</Cmd>
              <Output>Connected to cloud-gpu-01 (GPU, 20 GB VRAM)</Output>
            </Terminal>
          </div>

          <Prose>
            From the mobile app, tap the machine in your device list to connect.
            Tasks, voice input, and feedback all work identically to a local
            machine &mdash; traffic flows through the relay if you are not on
            the same network.
          </Prose>
        </section>

        {/* ─── Multi-User / Team Access ─── */}
        <section className="mb-20">
          <SectionHeading id="multi-user">
            Multi-User / Team Access
          </SectionHeading>
          <Prose>
            The $449 GPU machine supports multiple users. A team admin creates a
            team, invites members by email, and each member authenticates with
            their own Apple, Google, or Microsoft account. No shared passwords,
            no SSH keys to manage.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Per-user isolation
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Each team member gets their own workspace directory, task queue,
                AI agent sessions, feedback reports, and black box streams.
                Users cannot see or interfere with each other&apos;s work.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Shared GPU resources
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Ollama, PersonaPlex, Whisper, Docker, and system-level tools are
                shared across all team members. GPU inference requests are queued
                &mdash; no manual resource management needed.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                OAuth-based auth
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Each member authenticates via their own identity provider. The
                machine validates tokens against Convex and checks team
                membership. No machine-level credentials to rotate or leak.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Team Management ─── */}
        <section className="mb-20">
          <SectionHeading id="team-management">
            Team Management
          </SectionHeading>
          <Prose>
            Teams can be managed from the mobile app, web dashboard, or CLI.
            The admin who creates the team can add and remove members.
          </Prose>

          <div className="mb-8">
            <Terminal title="team-management">
              <Comment># Create a team</Comment>
              <Cmd>yaver teams create --name &quot;backend-team&quot;</Cmd>
              <Output>Team created: backend-team (id: team_a1b2c3d4)</Output>
              <Divider />
              <Comment># Add a member by email</Comment>
              <Cmd>yaver teams add-member --team backend-team --email alice@company.com</Cmd>
              <Output>Invited alice@company.com to backend-team</Output>
              <Divider />
              <Comment># Remove a member</Comment>
              <Cmd>yaver teams remove-member --team backend-team --email alice@company.com</Cmd>
              <Output>Removed alice@company.com from backend-team</Output>
              <Divider />
              <Comment># List active sessions on a cloud machine</Comment>
              <Cmd>yaver sessions --device cloud-gpu-01</Cmd>
              <Output>
                {`USER                AGENT          STARTED         STATUS
alice@company.com   claude-code    2h ago          active
bob@company.com     opencode       15m ago         active
carol@company.com   codex          3h ago          idle`}
              </Output>
            </Terminal>
          </div>

          <Prose>
            The same operations are available via the REST API. See the API
            Reference section below for endpoint details.
          </Prose>

          <div className="mb-8">
            <Terminal title="team-api">
              <Comment># Add a member via API</Comment>
              <Cmd>{`curl -X POST https://cloud-gpu-01.yaver.io/teams/members \\
  -H "Authorization: Bearer $TOKEN" \\
  -d '{"teamId": "team_a1b2c3d4", "email": "alice@company.com"}'`}</Cmd>
              <Output>{`{"ok": true, "memberId": "usr_x7y8z9"}`}</Output>
              <Divider />
              <Comment># Remove a member via API</Comment>
              <Cmd>{`curl -X DELETE https://cloud-gpu-01.yaver.io/teams/members \\
  -H "Authorization: Bearer $TOKEN" \\
  -d '{"teamId": "team_a1b2c3d4", "email": "alice@company.com"}'`}</Cmd>
              <Output>{`{"ok": true}`}</Output>
            </Terminal>
          </div>
        </section>

        {/* ─── Auth Flow ─── */}
        <section className="mb-20">
          <SectionHeading id="auth-flow">Auth Flow</SectionHeading>
          <Prose>
            Cloud machines are headless &mdash; no browser, no desktop
            environment. OAuth authentication happens on the user&apos;s phone or
            laptop where a browser exists. The resulting token is sent to the
            machine and validated server-side.
          </Prose>

          <div className="mb-8">
            <Terminal title="auth-flow">
              <pre className="text-surface-300">
                {`User's phone/laptop          Convex              Cloud Machine
       │                          │                      │
       │  1. OAuth sign-in        │                      │
       │  (Apple/Google/MSFT)     │                      │
       │ ────────────────────────>│                      │
       │                          │                      │
       │  2. Token issued         │                      │
       │ <────────────────────────│                      │
       │                          │                      │
       │  3. Token sent to machine via Yaver protocol    │
       │ ───────────────────────────────────────────────>│
       │                          │                      │
       │                          │  4. Validate token   │
       │                          │<─────────────────────│
       │                          │                      │
       │                          │  5. Check team       │
       │                          │     membership       │
       │                          │─────────────────────>│
       │                          │                      │
       │                          │          6. Access granted
       │                          │          User dir created
       │                          │          /var/yaver/users/yaver-{id}/`}
              </pre>
            </Terminal>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                No browser on the machine
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                The cloud machine never opens a browser. OAuth is handled
                entirely on the client device. The machine only validates tokens
                against Convex.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Team membership validation
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                After token validation, the machine checks that the
                authenticated user belongs to the team that owns this machine.
                Unauthorized users are rejected even with a valid Yaver token.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Per-user directory
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                On first auth, the machine creates{" "}
                <InlineCode>{`/var/yaver/users/yaver-{id}/`}</InlineCode> for
                the user. All workspace files, task state, and session data live
                here.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Workspace Isolation ─── */}
        <section className="mb-20">
          <SectionHeading id="workspace-isolation">
            Workspace Isolation
          </SectionHeading>
          <Prose>
            Each authenticated user on a multi-user machine gets a fully
            isolated workspace. Users share the underlying hardware and
            system-level services, but all user-specific state is separated.
          </Prose>

          <div className="mb-8">
            <Terminal title="workspace-layout">
              <pre className="text-surface-300">
                {`/var/yaver/
├── users/
│   ├── yaver-alice-a1b2/       # Alice's workspace
│   │   ├── workspace/          # Project files
│   │   ├── tasks/              # Task queue
│   │   ├── feedback/           # Feedback reports
│   │   ├── sessions/           # AI agent sessions (tmux)
│   │   └── blackbox/           # Black box streams
│   │
│   └── yaver-bob-c3d4/         # Bob's workspace
│       ├── workspace/
│       ├── tasks/
│       ├── feedback/
│       ├── sessions/
│       └── blackbox/
│
└── shared/
    ├── ollama/                  # Shared Ollama (used as OpenCode's local provider)
    ├── personaplex/             # Shared PersonaPlex model
    └── whisper/                 # Shared Whisper model`}
              </pre>
            </Terminal>
          </div>

          <SubHeading>Per-user (isolated)</SubHeading>
          <div className="mb-6 space-y-4">
            <div className="card">
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>Workspace directory &mdash; project files, git repos, build artifacts</li>
                <li>Task queue &mdash; pending, active, and completed tasks</li>
                <li>Feedback reports &mdash; screenshots, voice recordings, annotations</li>
                <li>AI agent sessions &mdash; isolated tmux sessions per user</li>
                <li>Black box streams &mdash; terminal recordings and session logs</li>
              </ul>
            </div>
          </div>

          <SubHeading>Shared (system-level)</SubHeading>
          <div className="space-y-4">
            <div className="card">
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>GPU &mdash; Ollama, PersonaPlex, Whisper (inference requests are queued)</li>
                <li>Docker &mdash; shared daemon, per-user namespacing</li>
                <li>System tools &mdash; Node.js, Python, Go, Rust, compilers, CLIs</li>
                <li>Network &mdash; relay connection, beacon, Convex heartbeat</li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Self-Hosting ─── */}
        <section className="mb-20">
          <SectionHeading id="self-hosting">Self-Hosting</SectionHeading>
          <Prose>
            You can run multi-user mode on your own hardware. Any machine with
            the Yaver agent installed can serve multiple team members with the
            same isolation guarantees as a managed cloud machine.
          </Prose>

          <div className="mb-8">
            <Terminal title="self-hosted-multi-user">
              <Comment># Start the agent in multi-user mode</Comment>
              <Cmd>{`yaver serve --multi-user --team <teamId> --work-dir /var/yaver/workspaces`}</Cmd>
              <Output>
                {`Yaver agent started (multi-user mode)
Team: backend-team (team_a1b2c3d4)
Work dir: /var/yaver/workspaces
Listening on 0.0.0.0:18080 (HTTP), 0.0.0.0:4433 (QUIC)
Beacon broadcasting on UDP 19837`}
              </Output>
              <Divider />
              <Comment># Team members authenticate with their own accounts</Comment>
              <Comment># Each gets isolated workspace under /var/yaver/workspaces/yaver-{"{id}"}/</Comment>
            </Terminal>
          </div>

          <Prose>
            Requirements: the machine must be reachable by team members, either
            on the same LAN, via a relay server, or through Tailscale. The
            Yaver agent handles user isolation, session management, and auth
            validation &mdash; no additional setup needed beyond the{" "}
            <InlineCode>--multi-user</InlineCode> flag.
          </Prose>
        </section>

        {/* ─── API Reference ─── */}
        <section className="mb-20">
          <SectionHeading id="api-reference">API Reference</SectionHeading>
          <Prose>
            Key endpoints exposed by the Yaver agent when running in multi-user
            or cloud machine mode. All endpoints require a valid{" "}
            <InlineCode>Authorization: Bearer &lt;token&gt;</InlineCode> header.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Teams
              </h4>
              <div className="space-y-2 text-sm text-surface-400">
                <div>
                  <InlineCode>GET /teams</InlineCode> &mdash; List teams the
                  authenticated user belongs to.
                </div>
                <div>
                  <InlineCode>POST /teams/members</InlineCode> &mdash; Add a
                  member to a team. Body:{" "}
                  <InlineCode>{`{"teamId": "...", "email": "..."}`}</InlineCode>
                </div>
                <div>
                  <InlineCode>DELETE /teams/members</InlineCode> &mdash; Remove
                  a member from a team. Body:{" "}
                  <InlineCode>{`{"teamId": "...", "email": "..."}`}</InlineCode>
                </div>
                <div>
                  <InlineCode>POST /teams/validate</InlineCode> &mdash; Validate
                  that a token belongs to a team member. Used internally by the
                  agent on auth.
                </div>
              </div>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Machines
              </h4>
              <div className="space-y-2 text-sm text-surface-400">
                <div>
                  <InlineCode>GET /machines</InlineCode> &mdash; List cloud
                  machines associated with the authenticated user&apos;s teams.
                </div>
              </div>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Users
              </h4>
              <div className="space-y-2 text-sm text-surface-400">
                <div>
                  <InlineCode>GET /users</InlineCode> &mdash; List all users on
                  the current machine (admin only).
                </div>
                <div>
                  <InlineCode>GET /users/me</InlineCode> &mdash; Get the
                  authenticated user&apos;s profile and workspace path.
                </div>
              </div>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Sessions
              </h4>
              <div className="space-y-2 text-sm text-surface-400">
                <div>
                  <InlineCode>GET /sessions</InlineCode> &mdash; List active AI
                  agent sessions on the machine. Admin sees all sessions;
                  non-admin sees own sessions only.
                </div>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}
