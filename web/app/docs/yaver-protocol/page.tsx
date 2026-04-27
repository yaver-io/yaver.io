import Link from "next/link";

export const metadata = {
  title: "Yaver Protocol v1 — Yaver",
  description:
    "Producer/consumer messaging between agent and dashboard/mobile. Real progress, snapshots, liveness contract.",
};

export default function YaverProtocolPage() {
  return (
    <div className="mx-auto max-w-3xl px-6 py-16">
      <div className="mb-8">
        <Link href="/docs" className="text-[12px] text-surface-500 hover:text-surface-300">
          ← All docs
        </Link>
      </div>
      <h1 className="mb-2 text-3xl font-bold text-surface-50">Yaver Protocol v1</h1>
      <p className="mb-8 text-sm text-surface-400">
        The producer/consumer messaging contract between the Go agent and the dashboard /
        mobile app — built so the user <strong>never feels disconnected</strong>, even
        during slow compiles.
      </p>

      <Section title="Three guarantees">
        <ul className="list-disc space-y-2 pl-6 text-[14px] leading-relaxed text-surface-300">
          <li>
            <strong>Real progress, not fake.</strong> The agent regex-parses Metro / Expo
            / Hermesc stdout (e.g. <Code>Bundling 67% (1247/2390)</Code>) into structured
            <Code>progress</Code> events with <Code>pct</Code>, <Code>done</Code>,{" "}
            <Code>total</Code>, <Code>currentFile</Code>, <Code>etaMs</Code>, and a{" "}
            <Code>progressSrc</Code> of <Code>exact</Code> / <Code>heuristic</Code> /{" "}
            <Code>unknown</Code>. Consumer renders an indeterminate spinner for{" "}
            <Code>unknown</Code> — never fakes a percentage.
          </li>
          <li>
            <strong>Snapshots every 5 seconds.</strong> A reconnecting consumer reads ONE{" "}
            <Code>snapshot</Code> event and is fully caught up. No replay storm.
          </li>
          <li>
            <strong>Liveness contract decoupled from compile state.</strong> Channel
            health is its own indicator: <Code>live</Code> / <Code>syncing</Code> /{" "}
            <Code>reconnecting</Code> / <Code>lost</Code>. Slow compiles never look like
            "lost" connections.
          </li>
        </ul>
      </Section>

      <Section title="Topics">
        <ul className="list-disc space-y-1 pl-6 text-[14px] text-surface-300">
          <li><Code>dev/start</Code> — Metro / Expo / Vite / Next dev server lifecycle</li>
          <li><Code>webview/build</Code> — Expo Web sibling for browser preview</li>
          <li><Code>hermes/compile</Code> — hermesc on the agent (per <Code>/dev/build-native</Code>)</li>
          <li><Code>bundle/push</Code> — yaver-cli pushing a bundle to phone</li>
        </ul>
      </Section>

      <Section title="Phases">
        <p className="mb-3 text-[14px] text-surface-300">
          Each topic owns its phase machine. Consumer renders the phase label; the agent
          owns the state diagram and transition validation.
        </p>
        <table className="w-full text-[12px] text-surface-400">
          <thead className="text-left text-surface-500">
            <tr>
              <th className="py-2 pr-4">Topic</th>
              <th className="py-2">Phase order</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-surface-800">
            <tr>
              <td className="py-2 pr-4 font-mono">dev/start</td>
              <td className="py-2">queued → installing_deps → starting → metro_bundling → listening → idle ⇄ metro_bundling</td>
            </tr>
            <tr>
              <td className="py-2 pr-4 font-mono">webview/build</td>
              <td className="py-2">queued → preparing → web_bundling → listening → ready</td>
            </tr>
            <tr>
              <td className="py-2 pr-4 font-mono">hermes/compile</td>
              <td className="py-2">queued → metro_bundling → hermesc_compiling → validating → ready</td>
            </tr>
            <tr>
              <td className="py-2 pr-4 font-mono">bundle/push</td>
              <td className="py-2">queued → uploading → validating → bridge_reload → ready</td>
            </tr>
          </tbody>
        </table>
      </Section>

      <Section title="Progress payload">
        <pre className="overflow-x-auto rounded-md border border-surface-800 bg-surface-950 p-4 font-mono text-[12px] text-surface-300">{`type Progress = {
  type: "progress";
  topic: string;          // see Topics
  phase: string;          // current phase
  pct: number;            // 0..100, REAL number from compiler stdout
  done?: number;          // e.g. 1247 modules
  total?: number;         // e.g. 2390 modules
  unit?: string;          // "modules" | "bytes" | "files" | "tasks"
  currentFile?: string;   // e.g. "node_modules/expo-router/build/Route.js"
  progressSrc:            // critically: tells UI whether pct is REAL
    | "exact"             //   parsed from "Bundling 67% (1247/2390)"
    | "heuristic"         //   estimated from rate
    | "unknown";          //   indeterminate spinner, NOT a fake bar
  etaMs?: number;         // est remaining millis (only when src=="exact")
};`}</pre>
      </Section>

      <Section title="Snapshot payload">
        <p className="mb-3 text-[14px] text-surface-300">
          Emitted every 5 seconds while a dev server is running. The consumer's source of
          truth — even if every <Code>progress</Code> delta were dropped, the next
          snapshot fully restores the UI.
        </p>
        <pre className="overflow-x-auto rounded-md border border-surface-800 bg-surface-950 p-4 font-mono text-[12px] text-surface-300">{`type Snapshot = {
  type: "snapshot";
  snapshot: {
    generatedAt: string;
    running: boolean;
    framework: string;
    port: number;
    webPort: number;
    workDir: string;
    uptimeSec: number;
    idleSec: number;
    pid: number;
    pidAlive: boolean;
    phases: { [topic: string]: string };
    progress?: Progress;
    webProgress?: Progress;
    recentLogs: string[];   // last 8 stdout/stderr lines
    beatNumber: number;
  };
};`}</pre>
      </Section>

      <Section title="Liveness contract">
        <table className="w-full text-[12px] text-surface-400">
          <thead className="text-left text-surface-500">
            <tr>
              <th className="py-2 pr-4">Time since last byte</th>
              <th className="py-2 pr-4">UI label</th>
              <th className="py-2">Animation</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-surface-800">
            <tr>
              <td className="py-2 pr-4 font-mono">&lt; 6 s</td>
              <td className="py-2 pr-4">channel: live (green dot)</td>
              <td className="py-2">normal</td>
            </tr>
            <tr>
              <td className="py-2 pr-4 font-mono">6–15 s</td>
              <td className="py-2 pr-4">channel: syncing… (amber)</td>
              <td className="py-2">ping animation</td>
            </tr>
            <tr>
              <td className="py-2 pr-4 font-mono">15–60 s</td>
              <td className="py-2 pr-4">channel: reconnecting…</td>
              <td className="py-2">spinner + auto-reconnect</td>
            </tr>
            <tr>
              <td className="py-2 pr-4 font-mono">&gt; 60 s</td>
              <td className="py-2 pr-4">channel: lost — Reconnect &amp; Fix</td>
              <td className="py-2">manual button</td>
            </tr>
          </tbody>
        </table>
      </Section>

      <Section title="Implementation pointers">
        <ul className="list-disc space-y-2 pl-6 text-[14px] text-surface-300">
          <li>
            <Code>desktop/agent/devserver_progress.go</Code> — regex parsers for Metro
            / Expo / hermesc stdout.
          </li>
          <li>
            <Code>desktop/agent/devserver.go</Code> — <Code>DevServerEvent</Code> struct,{" "}
            <Code>DevServerSnapshot</Code>, heartbeat + snapshot loop.
          </li>
          <li>
            <Code>web/components/dashboard/PreviewPane.tsx</Code> — consumer rendering of{" "}
            <Code>topicProgress</Code> + <Code>connectionHealth</Code> chip.
          </li>
          <li>
            <Code>mobile/src/components/DevPreview.tsx</Code> — same consumer for the
            mobile app.
          </li>
        </ul>
      </Section>

      <Section title="Future">
        <ul className="list-disc space-y-1 pl-6 text-[14px] text-surface-300">
          <li>
            <strong>v2</strong>: replace JSON-over-SSE with CBOR-framed multiplexed QUIC
            stream. Move blackbox + bundle-push + feedback onto one envelope.
          </li>
          <li>
            <strong>v3</strong>: bidirectional control plane — consumer can SUBSCRIBE /
            PAUSE per-topic without HTTP roundtrips.
          </li>
          <li>
            <strong>v4</strong>: cross-language client libs (Swift, Kotlin, Rust).
          </li>
        </ul>
      </Section>

      <p className="mt-12 text-[12px] text-surface-500">
        Full reference:{" "}
        <a
          href="https://github.com/kivanccakmak/yaver.io/blob/main/docs/yaver-protocol.md"
          className="text-sky-400 hover:underline"
        >
          docs/yaver-protocol.md
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

function Code({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-surface-900 px-1.5 py-0.5 font-mono text-[12px] text-amber-200">
      {children}
    </code>
  );
}
