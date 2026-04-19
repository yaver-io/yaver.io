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

export default function ContributingPage() {
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
            Contributing to Yaver
          </h1>
          <p className="text-sm leading-relaxed text-surface-400">
            Yaver is open source (AGPL-3.0-only). You can develop features, fix bugs, add
            new AI runner integrations, improve docs, and run your own backend
            instance. This guide covers everything you need to get started.
          </p>
        </div>

        {/* Table of contents */}
        <div className="mb-16 rounded-xl border border-surface-800 bg-surface-900 p-6">
          <h3 className="mb-4 text-sm font-semibold text-surface-200">
            On this page
          </h3>
          <ul className="space-y-2 text-sm text-surface-400">
            {[
              ["getting-started", "Getting Started"],
              ["running-your-own-backend", "Running Your Own Backend (Convex)"],
              ["seed-data", "Database Seed Data"],
              ["release-policy", "CI/CD & Release Policy"],
              ["development-workflow", "Development Workflow"],
              ["adding-runners", "Adding New AI Runners"],
            ].map(([id, label]) => (
              <li key={id}>
                <a
                  href={`#${id}`}
                  className="hover:text-surface-50 transition-colors"
                >
                  {label}
                </a>
              </li>
            ))}
          </ul>
        </div>

        {/* ─── Section 1: Getting Started ─── */}
        <section className="mb-20">
          <SectionHeading id="getting-started">Getting Started</SectionHeading>
          <Prose>
            Fork the repository, clone it, and you&apos;re ready to develop.
            Each component can be worked on independently.
          </Prose>

          <Terminal title="Setup">
            <Comment># Fork on GitHub, then:</Comment>
            <Cmd>git clone https://github.com/YOUR_USERNAME/yaver.git</Cmd>
            <Cmd>cd yaver</Cmd>
            <div className="h-2" />
            <Comment># Check current versions</Comment>
            <Cmd>cat versions.json</Cmd>
          </Terminal>

          <div className="mt-6 space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Project structure
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; <InlineCode>desktop/agent/</InlineCode> &mdash; Go CLI
                  agent (QUIC server, agent runner, tmux manager)
                </li>
                <li>
                  &bull; <InlineCode>mobile/</InlineCode> &mdash; React Native
                  app (iOS + Android)
                </li>
                <li>
                  &bull; <InlineCode>backend/</InlineCode> &mdash; Convex
                  backend (auth + peer discovery)
                </li>
                <li>
                  &bull; <InlineCode>relay/</InlineCode> &mdash; QUIC relay
                  server (Go, self-hostable)
                </li>
                <li>
                  &bull; <InlineCode>web/</InlineCode> &mdash; Next.js landing
                  page (Vercel)
                </li>
                <li>
                  &bull; <InlineCode>pi-image/</InlineCode> &mdash; Raspberry Pi
                  image overlay, first-boot config, and release assets
                </li>
                <li>
                  &bull; <InlineCode>versions.json</InlineCode> &mdash; Single
                  source of truth for all component versions
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Version management
              </h4>
              <p className="text-sm text-surface-400">
                Every PR that changes component code must bump the version in{" "}
                <InlineCode>versions.json</InlineCode>. CI enforces this. After
                editing <InlineCode>versions.json</InlineCode>, run{" "}
                <InlineCode>./scripts/sync-versions.sh</InlineCode> to propagate
                to all downstream files (Go consts, app.json, Info.plist,
                build.gradle, package.json). The Pi image has its own version
                key too: <InlineCode>piImage</InlineCode>.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Section 2: Running Your Own Backend ─── */}
        <section className="mb-20">
          <SectionHeading id="running-your-own-backend">
            Running Your Own Backend (Convex)
          </SectionHeading>
          <Prose>
            Yaver uses{" "}
            <a
              href="https://www.convex.dev"
              target="_blank"
              rel="noopener noreferrer"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              Convex
            </a>{" "}
            for auth and peer discovery only. No task data, code, or AI output
            ever touches the backend. To develop locally, you need your own
            Convex instance.
          </Prose>

          <SubHeading>Option 1: Convex Cloud (recommended for contributors)</SubHeading>
          <Prose>
            Convex offers a generous free tier (1M function calls/month). This is
            the easiest way to get started &mdash; no infrastructure to manage.
          </Prose>

          <Terminal title="Convex Cloud Setup">
            <Cmd>cd backend</Cmd>
            <Cmd>npm install</Cmd>
            <div className="h-2" />
            <Comment># This creates a free Convex project on first run</Comment>
            <Comment># Follow the prompts to sign in and create a project</Comment>
            <Cmd>npx convex dev</Cmd>
            <div className="h-2" />
            <Output>Convex functions ready! Visit https://dashboard.convex.dev</Output>
            <Output>Your deployment URL: https://your-project-123.convex.site</Output>
          </Terminal>

          <div className="mt-6 card">
            <h4 className="mb-2 text-sm font-medium text-surface-200">
              What <InlineCode>npx convex dev</InlineCode> does
            </h4>
            <ul className="space-y-2 text-sm text-surface-400">
              <li>&bull; Pushes the schema and functions to your Convex instance</li>
              <li>&bull; Watches for file changes and auto-deploys</li>
              <li>&bull; Creates <InlineCode>.env.local</InlineCode> with your deployment URL</li>
              <li>&bull; The dashboard at <InlineCode>dashboard.convex.dev</InlineCode> lets you inspect data and run mutations</li>
            </ul>
          </div>

          <div className="mt-8">
            <SubHeading>Option 2: Convex Self-Hosted (Docker)</SubHeading>
            <Prose>
              Convex is open source. You can run the entire backend on your own
              infrastructure using Docker. See the{" "}
              <a
                href="https://github.com/get-convex/convex-backend"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
              >
                Convex self-hosted repo
              </a>{" "}
              for setup instructions.
            </Prose>

            <Terminal title="Convex Self-Hosted">
              <Comment># Clone the Convex backend</Comment>
              <Cmd>git clone https://github.com/get-convex/convex-backend.git</Cmd>
              <Cmd>cd convex-backend</Cmd>
              <Cmd>just run-local-backend</Cmd>
              <div className="h-2" />
              <Comment># Then point Yaver&apos;s backend at your local instance</Comment>
              <Cmd>cd /path/to/yaver/backend</Cmd>
              <Cmd>npx convex dev --url http://localhost:3210</Cmd>
            </Terminal>
          </div>

          <div className="mt-8">
            <SubHeading>Seed the database</SubHeading>
            <Prose>
              After setting up Convex, seed it with predefined data (AI runners,
              models, default config). This is required for the app to work.
            </Prose>

            <Terminal title="Seed Data">
              <Comment># Seed everything at once (runners + models + default config)</Comment>
              <Cmd>npx convex run seed:all</Cmd>
              <Output>
                {`{ runners: { created: 4, updated: 0 }, models: { created: 6, updated: 0 }, config: { created: 6 } }`}
              </Output>
              <div className="h-2" />
              <Comment># Or seed individually</Comment>
              <Cmd>npx convex run aiRunners:seed</Cmd>
              <Cmd>npx convex run aiModels:seed</Cmd>
            </Terminal>
          </div>

          <div className="mt-8">
            <SubHeading>Point your clients at your backend</SubHeading>
            <Prose>
              Once Convex is running, configure the CLI and mobile app to use
              your instance instead of the production backend.
            </Prose>

            <Terminal title="Configure CLI">
              <Comment># Set your Convex URL in CLI config</Comment>
              <Cmd>yaver config set convex_site_url https://your-project-123.convex.site</Cmd>
              <div className="h-2" />
              <Comment># Or pass it as a flag</Comment>
              <Cmd>yaver serve --convex-url https://your-project-123.convex.site</Cmd>
            </Terminal>

            <div className="mt-4 card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Mobile app
              </h4>
              <p className="text-sm text-surface-400">
                Update the <InlineCode>CONVEX_URL</InlineCode> constant in{" "}
                <InlineCode>mobile/src/lib/constants.ts</InlineCode> to point at
                your Convex deployment URL. For the web landing page, update{" "}
                <InlineCode>web/lib/constants.ts</InlineCode>.
              </p>
            </div>
          </div>

          <div className="mt-8">
            <SubHeading>What lives in Convex vs what&apos;s P2P</SubHeading>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="card">
                <h4 className="mb-2 text-sm font-medium text-green-400">
                  Convex (auth + discovery)
                </h4>
                <ul className="space-y-1 text-sm text-surface-400">
                  <li>&bull; User accounts & sessions</li>
                  <li>&bull; Device registry (hostname, IP, online status)</li>
                  <li>&bull; AI runner & model definitions</li>
                  <li>&bull; Platform config (relay servers, versions)</li>
                  <li>&bull; Analytics (task counts, usage)</li>
                </ul>
              </div>
              <div className="card">
                <h4 className="mb-2 text-sm font-medium text-blue-400">
                  Peer-to-peer (never in Convex)
                </h4>
                <ul className="space-y-1 text-sm text-surface-400">
                  <li>&bull; Task prompts & AI output</li>
                  <li>&bull; Source code & files</li>
                  <li>&bull; Terminal sessions</li>
                  <li>&bull; Agent logs & stderr</li>
                  <li>&bull; Everything the AI agent sees or produces</li>
                </ul>
              </div>
            </div>
          </div>
        </section>

        {/* ─── Section 3: Seed Data ─── */}
        <section className="mb-20">
          <SectionHeading id="seed-data">Database Seed Data</SectionHeading>
          <Prose>
            The database has two categories of data: <strong className="text-surface-200">predefined seed data</strong>{" "}
            (shared across all instances) and <strong className="text-surface-200">user data</strong>{" "}
            (created at runtime). Understanding this distinction is important
            when contributing.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">
                Predefined seed data (shared)
              </h4>
              <p className="mb-3 text-sm text-surface-400">
                Defined as constants in code, applied via idempotent{" "}
                <InlineCode>seed</InlineCode> mutations. Safe to run any time
                &mdash; updates existing records, inserts missing ones.
              </p>
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left text-surface-400">
                      <th className="pb-2 pr-4 font-medium">Table</th>
                      <th className="pb-2 pr-4 font-medium">Source</th>
                      <th className="pb-2 font-medium">Content</th>
                    </tr>
                  </thead>
                  <tbody className="text-surface-400">
                    <tr className="border-t border-surface-800">
                      <td className="py-2 pr-4 text-surface-200">aiRunners</td>
                      <td className="py-2 pr-4">
                        <InlineCode>aiRunners.ts</InlineCode>
                      </td>
                      <td className="py-2">
                        Claude Code, Codex, Aider, Custom
                      </td>
                    </tr>
                    <tr className="border-t border-surface-800">
                      <td className="py-2 pr-4 text-surface-200">aiModels</td>
                      <td className="py-2 pr-4">
                        <InlineCode>aiModels.ts</InlineCode>
                      </td>
                      <td className="py-2">
                        Sonnet, Opus, Haiku, o3-mini, etc.
                      </td>
                    </tr>
                    <tr className="border-t border-surface-800">
                      <td className="py-2 pr-4 text-surface-200">
                        platformConfig
                      </td>
                      <td className="py-2 pr-4">
                        <InlineCode>seed.ts</InlineCode>
                      </td>
                      <td className="py-2">
                        Relay servers, version strings, defaults
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>

            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">
                User data (runtime only)
              </h4>
              <p className="mb-3 text-sm text-surface-400">
                Created when users sign up, register devices, and run tasks.
                Never seeded, never shared between instances.
              </p>
              <p className="text-sm text-surface-400">
                Tables:{" "}
                <InlineCode>users</InlineCode>,{" "}
                <InlineCode>sessions</InlineCode>,{" "}
                <InlineCode>devices</InlineCode>,{" "}
                <InlineCode>userSettings</InlineCode>,{" "}
                <InlineCode>runnerUsage</InlineCode>,{" "}
                <InlineCode>deviceMetrics</InlineCode>,{" "}
                <InlineCode>deviceEvents</InlineCode>,{" "}
                <InlineCode>dailyTaskCounts</InlineCode>,{" "}
                <InlineCode>developerLogs</InlineCode>,{" "}
                <InlineCode>mobileStreamLogs</InlineCode>
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Adding new seed data
              </h4>
              <p className="text-sm text-surface-400">
                To add a new AI runner (e.g., Ollama), edit the{" "}
                <InlineCode>PREDEFINED_RUNNERS</InlineCode> array in{" "}
                <InlineCode>backend/convex/aiRunners.ts</InlineCode>. To add
                new models, edit <InlineCode>PREDEFINED_MODELS</InlineCode> in{" "}
                <InlineCode>backend/convex/aiModels.ts</InlineCode>. Then run{" "}
                <InlineCode>npx convex run seed:all</InlineCode> to apply.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Section 4: CI/CD & Release Policy ─── */}
        <section className="mb-20">
          <SectionHeading id="release-policy">
            CI/CD &amp; Release Policy
          </SectionHeading>
          <Prose>
            Yaver uses GitHub Actions for CI and releases. Contributors can open
            PRs freely. Releases and deploys are restricted to maintainers.
          </Prose>

          <div className="space-y-4">
            <div className="card border-green-900/30">
              <h4 className="mb-2 text-sm font-medium text-green-400">
                What happens on every PR
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>&bull; Version bump check &mdash; CI fails if you changed component code without bumping <InlineCode>versions.json</InlineCode></li>
                <li>&bull; Go tests &mdash; <InlineCode>go test ./...</InlineCode> for CLI and relay</li>
                <li>&bull; Build verification &mdash; Go build, Next.js build, TypeScript typecheck</li>
                <li>&bull; All checks must pass before merge</li>
              </ul>
            </div>

            <div className="card border-yellow-900/30">
              <h4 className="mb-2 text-sm font-medium text-yellow-400">
                Maintainer-only (protected)
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; <strong className="text-surface-200">Releases</strong>{" "}
                  &mdash; triggered by git tags (<InlineCode>cli/v*</InlineCode>,{" "}
                  <InlineCode>mobile/v*</InlineCode>, etc.). Tag creation is
                  restricted to the repo owner.
                </li>
                <li>
                  &bull; <strong className="text-surface-200">Deploys</strong>{" "}
                  &mdash; Convex production, Vercel, TestFlight, Google Play.
                  All require the <InlineCode>production</InlineCode> GitHub
                  Environment with required reviewer approval.
                </li>
                <li>
                  &bull; <strong className="text-surface-200">Secrets</strong>{" "}
                  &mdash; Apple certificates, Android keystores, Convex deploy
                  keys, Vercel tokens. Never exposed to fork PRs.
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Release flow
              </h4>
              <ol className="space-y-2 text-sm text-surface-400 list-decimal list-inside">
                <li>Contributor opens PR with code changes + version bump</li>
                <li>CI runs checks (tests, build, version validation)</li>
                <li>Maintainer reviews and merges</li>
                <li>Maintainer pushes a release tag (e.g., <InlineCode>cli/v1.29.0</InlineCode>)</li>
                <li>GitHub Actions builds, packages, and publishes the release</li>
                <li>Version is updated in Convex for all clients to see</li>
              </ol>
            </div>
          </div>
        </section>

        {/* ─── Section 5: Development Workflow ─── */}
        <section className="mb-20">
          <SectionHeading id="development-workflow">
            Development Workflow
          </SectionHeading>
          <Prose>
            Each component can be developed independently. Here&apos;s how to
            run each one locally.
          </Prose>

          <div className="space-y-6">
            <div>
              <SubHeading>CLI Agent (Go)</SubHeading>
              <Terminal title="desktop/agent">
                <Cmd>cd desktop/agent</Cmd>
                <Cmd>go run . serve</Cmd>
                <div className="h-2" />
                <Comment># Run tests</Comment>
                <Cmd>go test -v ./...</Cmd>
                <div className="h-2" />
                <Comment># Build binary</Comment>
                <Cmd>go build -o yaver .</Cmd>
              </Terminal>
            </div>

            <div>
              <SubHeading>Backend (Convex)</SubHeading>
              <Terminal title="backend">
                <Cmd>cd backend</Cmd>
                <Cmd>npm install</Cmd>
                <Cmd>npx convex dev</Cmd>
                <div className="h-2" />
                <Comment># Seed with predefined data</Comment>
                <Cmd>npx convex run seed:all</Cmd>
                <div className="h-2" />
                <Comment># Deploy to production (maintainer only)</Comment>
                <Cmd>npx convex deploy --yes</Cmd>
              </Terminal>
            </div>

            <div>
              <SubHeading>Web (Next.js)</SubHeading>
              <Terminal title="web">
                <Cmd>cd web</Cmd>
                <Cmd>npm install</Cmd>
                <Cmd>npm run dev</Cmd>
                <Output>Local: http://localhost:3000</Output>
              </Terminal>
            </div>

            <div>
              <SubHeading>Relay Server (Go)</SubHeading>
              <Terminal title="relay">
                <Cmd>cd relay</Cmd>
                <Cmd>go run . serve --password your-secret</Cmd>
                <div className="h-2" />
                <Comment># Or with Docker</Comment>
                <Cmd>RELAY_PASSWORD=your-secret docker compose up -d</Cmd>
              </Terminal>
            </div>

            <div>
              <SubHeading>Mobile (React Native)</SubHeading>
              <Terminal title="mobile">
                <Cmd>cd mobile</Cmd>
                <Cmd>npm install</Cmd>
                <div className="h-2" />
                <Comment># iOS (requires macOS + Xcode)</Comment>
                <Cmd>cd ios &amp;&amp; pod install &amp;&amp; cd ..</Cmd>
                <Cmd>npx react-native run-ios</Cmd>
                <div className="h-2" />
                <Comment># Android (requires Android Studio + Java 17)</Comment>
                <Cmd>npx react-native run-android</Cmd>
              </Terminal>
            </div>
          </div>

          <div className="mt-8 card">
            <h4 className="mb-2 text-sm font-medium text-surface-200">
              PR requirements
            </h4>
            <ul className="space-y-2 text-sm text-surface-400">
              <li>&bull; Bump version in <InlineCode>versions.json</InlineCode> for changed components</li>
              <li>&bull; Run <InlineCode>./scripts/sync-versions.sh</InlineCode> to propagate</li>
              <li>&bull; All CI checks pass (tests, build, typecheck)</li>
              <li>&bull; Code review by maintainer</li>
            </ul>
          </div>
        </section>

        {/* ─── Section 6: Adding New Runners ─── */}
        <section className="mb-20">
          <SectionHeading id="adding-runners">
            Adding New AI Runners
          </SectionHeading>
          <Prose>
            Want to add support for a new AI agent? Here&apos;s how. A
            &ldquo;runner&rdquo; is any terminal command that accepts a prompt
            and produces output.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                1. Add the runner definition
              </h4>
              <p className="text-sm text-surface-400">
                Edit <InlineCode>backend/convex/aiRunners.ts</InlineCode> and
                add an entry to <InlineCode>PREDEFINED_RUNNERS</InlineCode>:
              </p>
              <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-950 p-4 text-xs text-surface-300">
{`{
  runnerId: "ollama",
  name: "Ollama",
  command: "ollama",
  args: JSON.stringify(["run", "codellama", "{prompt}"]),
  outputMode: "raw",
  resumeSupported: false,
  description: "Local LLM via Ollama",
  sortOrder: 4,
}`}
              </pre>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                2. Add models (optional)
              </h4>
              <p className="text-sm text-surface-400">
                If the runner supports multiple models, add them to{" "}
                <InlineCode>PREDEFINED_MODELS</InlineCode> in{" "}
                <InlineCode>backend/convex/aiModels.ts</InlineCode>:
              </p>
              <pre className="mt-3 overflow-x-auto rounded-lg bg-surface-950 p-4 text-xs text-surface-300">
{`{
  modelId: "codellama",
  runnerId: "ollama",
  name: "Code Llama",
  description: "Meta's code generation model",
  isDefault: true,
  sortOrder: 1,
}`}
              </pre>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                3. Seed and test
              </h4>
              <p className="text-sm text-surface-400">
                Run <InlineCode>npx convex run seed:all</InlineCode> to push
                your new runner to Convex. Then test it from the mobile app or CLI
                by selecting the new runner.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Runner fields reference
              </h4>
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left text-surface-400">
                      <th className="pb-2 pr-4 font-medium">Field</th>
                      <th className="pb-2 font-medium">Description</th>
                    </tr>
                  </thead>
                  <tbody className="text-surface-400">
                    {[
                      ["runnerId", "Unique identifier (lowercase, no spaces)"],
                      ["name", "Display name in the UI"],
                      ["command", "Terminal command to execute"],
                      ["args", "JSON array of arguments. {prompt} is replaced with user input"],
                      ["outputMode", "\"stream-json\" (structured) or \"raw\" (plain text)"],
                      ["resumeSupported", "Whether the agent can resume sessions"],
                      ["resumeArgs", "JSON array of args for resuming (if supported)"],
                      ["exitCommand", "Command to gracefully stop the agent"],
                      ["sortOrder", "Display order in the UI"],
                    ].map(([field, desc]) => (
                      <tr key={field} className="border-t border-surface-800">
                        <td className="py-2 pr-4 text-surface-200">
                          <InlineCode>{field}</InlineCode>
                        </td>
                        <td className="py-2">{desc}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </section>

        {/* Bottom CTA */}
        <div className="rounded-xl border border-surface-800 bg-surface-900 p-6 text-center">
          <p className="mb-2 text-sm font-medium text-surface-200">
            Ready to contribute?
          </p>
          <p className="text-sm text-surface-400">
            Fork the repo, pick an issue, and open a PR. Check the{" "}
            <Link
              href="/docs/developers"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              Developer Guide
            </Link>{" "}
            for architecture details, or open an issue on{" "}
            <a
              href="https://github.com/kivanccakmak/yaver/issues"
              target="_blank"
              rel="noopener noreferrer"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              GitHub
            </a>
            .
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
