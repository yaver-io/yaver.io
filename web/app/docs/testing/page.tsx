import Link from "next/link";

export const metadata = {
  title: "Test Suite — Yaver Developer Docs",
  description:
    "Critical-path headless tests for hermes-reload, webview, SSE, and Yaver Protocol v1.",
};

export default function TestingPage() {
  return (
    <div className="mx-auto max-w-3xl px-6 py-16">
      <div className="mb-8">
        <Link href="/docs" className="text-[12px] text-surface-500 hover:text-surface-300">
          ← All docs
        </Link>
      </div>
      <h1 className="mb-2 text-3xl font-bold text-surface-50">Test Suite</h1>
      <p className="mb-8 text-sm text-surface-400">
        Headless tests against the live <Code>yaver-test-ephemeral</Code> remote Linux box
        prove the critical user flows end-to-end. Local tests run unit + integration
        on this machine.
      </p>

      <Section title="Critical-path headless tests">
        <p className="mb-3 text-[14px] text-surface-300">
          Every release tag fires these via GitHub Actions. They use configured
          remote-test SSH credentials to drive the test box.
        </p>
        <table className="w-full text-[12px] text-surface-400">
          <thead className="text-left text-surface-500">
            <tr>
              <th className="py-2 pr-4">Workflow</th>
              <th className="py-2">Verifies</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-surface-800">
            <Row name="Mobile Hermes Through Relay Smoke" what="Bundle compile + Hermes bytecode validation through public.yaver.io" />
            <Row name="Webview Through Relay Smoke" what="/dev/start → Metro listen → /dev/events SSE flow through relay" />
            <Row name="Restart sfmg dev + verify SSE" what="Stop → start → 10 s SSE sample on both localhost and relay" />
            <Row name="SSE With Bearer Header" what="Both ?token= query auth and Authorization header path through relay" />
            <Row name="Verify Yaver Protocol v1" what="Asserts snapshot + phase + progress events emitted by agent v1.99.67+" />
            <Row name="Feedback SDK Relay Smoke" what="/feedback upload + /blackbox/command-stream SSE handshake" />
          </tbody>
        </table>
      </Section>

      <Section title="Run a workflow manually">
        <pre className="overflow-x-auto rounded-md border border-surface-800 bg-surface-950 p-4 font-mono text-[12px] text-surface-300">{`gh workflow run 'Mobile Hermes Through Relay Smoke'
gh workflow run 'Webview Through Relay Smoke'
gh workflow run 'Verify Yaver Protocol v1'
gh workflow run 'Feedback SDK Relay Smoke'`}</pre>
      </Section>

      <Section title="Local test entry point">
        <pre className="overflow-x-auto rounded-md border border-surface-800 bg-surface-950 p-4 font-mono text-[12px] text-surface-300">{`./scripts/test-suite.sh                # everything that runs on this Mac
./scripts/test-suite.sh --unit         # Go unit tests only (~30 s)
./scripts/test-suite.sh --lan          # localhost direct connect (~1 min)
./scripts/test-suite.sh --relay        # local relay + agent task flow (~2 min)
./scripts/test-suite.sh --tailscale    # cross-machine via Tailscale
./scripts/test-suite.sh --cloudflare   # Cloudflare tunnel`}</pre>
        <p className="mt-3 text-[13px] text-surface-400">
          No credentials needed for <Code>--unit</Code>, <Code>--lan</Code>,{" "}
          <Code>--relay</Code>. Remote modes need <Code>REMOTE_SERVER_IP</Code> + SSH key
          via env vars or <Code>.env.test</Code> (gitignored).
        </p>
      </Section>

      <Section title="Per-component">
        <h3 className="mt-4 mb-2 text-base font-semibold text-surface-100">Agent (Go)</h3>
        <pre className="overflow-x-auto rounded-md border border-surface-800 bg-surface-950 p-4 font-mono text-[12px] text-surface-300">{`cd desktop/agent
go test ./...        # all unit tests
go vet ./...         # static checks`}</pre>
        <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">Web dashboard</h3>
        <pre className="overflow-x-auto rounded-md border border-surface-800 bg-surface-950 p-4 font-mono text-[12px] text-surface-300">{`cd web
npx tsc --noEmit
npm run build`}</pre>
        <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">Mobile app</h3>
        <pre className="overflow-x-auto rounded-md border border-surface-800 bg-surface-950 p-4 font-mono text-[12px] text-surface-300">{`cd mobile
npx tsc --noEmit`}</pre>
        <p className="mt-3 text-[13px] text-surface-400">
          Mobile builds for distribution use the local TestFlight + Play Store scripts
          (<Code>scripts/deploy-testflight.sh</Code>,{" "}
          <Code>scripts/deploy-playstore.sh</Code>) — never CI for iOS.
        </p>
        <h3 className="mt-6 mb-2 text-base font-semibold text-surface-100">Browser e2e (Playwright)</h3>
        <pre className="overflow-x-auto rounded-md border border-surface-800 bg-surface-950 p-4 font-mono text-[12px] text-surface-300">{`cd e2e
npm install
npx playwright install --with-deps chromium  # first run only
npm test`}</pre>
      </Section>

      <Section title="Testing the Yaver Protocol v1">
        <p className="text-[14px] text-surface-300">
          <Code>Verify Yaver Protocol v1</Code> SSHes into the test box, restarts the
          sfmg dev server, samples <Code>/dev/events</Code> for 25 s, and asserts:
        </p>
        <ul className="mt-2 list-disc space-y-1 pl-6 text-[13px] text-surface-300">
          <li><Code>snapshot</Code> events fire (≥ 1 in the window)</li>
          <li><Code>heartbeat</Code> events fire</li>
          <li><Code>phase</Code> and/or <Code>progress</Code> events fire when bundling</li>
        </ul>
        <p className="mt-3 text-[14px] text-surface-300">
          It pretty-prints the first <Code>progress</Code> event so you can visually
          confirm <Code>topic</Code> / <Code>pct</Code> / <Code>done</Code> /{" "}
          <Code>total</Code> / <Code>currentFile</Code> / <Code>progressSrc</Code> are
          populated. Full protocol reference at{" "}
          <Link href="/docs/yaver-protocol" className="text-sky-400 hover:underline">
            /docs/yaver-protocol
          </Link>.
        </p>
      </Section>

      <Section title="Adding a new test">
        <ol className="list-decimal space-y-2 pl-6 text-[13px] text-surface-300">
          <li>
            New workflow under <Code>.github/workflows/&lt;name&gt;.yml</Code> with{" "}
            <Code>on: workflow_dispatch</Code>.
          </li>
          <li>
            Use the SSH-key prep + <Code>scp</Code> + <Code>ssh</Code> pattern from any
            existing diag workflow.
          </li>
          <li>
            Render bash via <Code>cat &gt; /tmp/x.sh &lt;&lt;'BODY' ... BODY</Code> to
            avoid YAML heredoc collisions.
          </li>
          <li>
            Validate locally: <Code>python3 -c "import yaml; yaml.safe_load(open(...))"</Code>
            .
          </li>
          <li>
            Add it to{" "}
            <a
              href="https://github.com/kivanccakmak/yaver.io/blob/main/docs/testing.md"
              className="text-sky-400 hover:underline"
            >
              docs/testing.md
            </a>
            .
          </li>
        </ol>
      </Section>

      <p className="mt-12 text-[12px] text-surface-500">
        Full reference:{" "}
        <a
          href="https://github.com/kivanccakmak/yaver.io/blob/main/docs/testing.md"
          className="text-sky-400 hover:underline"
        >
          docs/testing.md
        </a>
      </p>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mb-10">
      <h2 className="mb-4 text-xl font-semibold text-surface-100">{title}</h2>
      {children}
    </section>
  );
}

function Row({ name, what }: { name: string; what: string }) {
  return (
    <tr>
      <td className="py-2 pr-4 font-mono">{name}</td>
      <td className="py-2">{what}</td>
    </tr>
  );
}

function Code({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-surface-900 px-1.5 py-0.5 font-mono text-[12px] text-amber-200">
      {children}
    </code>
  );
}
