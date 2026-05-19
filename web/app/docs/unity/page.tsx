import Link from "next/link";

function SectionHeading({
  id,
  children,
}: {
  id: string;
  children: React.ReactNode;
}) {
  return (
    <h2 id={id} className="mb-4 text-2xl font-bold text-surface-50 md:text-3xl">
      {children}
    </h2>
  );
}

function Prose({ children }: { children: React.ReactNode }) {
  return <p className="mb-6 text-sm leading-relaxed text-surface-400">{children}</p>;
}

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

export default function UnityDocsPage() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <Link href="/docs" className="mb-12 inline-block text-xs text-surface-500 hover:text-surface-50">
          &larr; Back to docs
        </Link>

        <div className="mb-16">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">Unity</h1>
          <p className="text-sm leading-relaxed text-surface-400">
            Yaver for Unity is a self-hosted feedback and fast-iteration loop for mobile and desktop
            Unity projects. The game captures useful context inside the app, and your own machine
            does the heavy work.
          </p>
        </div>

        <div className="mb-16 rounded-xl border border-surface-800 bg-surface-900 p-6">
          <h3 className="mb-4 text-sm font-semibold text-surface-200">On this page</h3>
          <nav className="space-y-2 text-sm">
            {[
              ["setup", "Setup"],
              ["mobile", "Mobile Cases"],
              ["desktop", "Desktop Cases"],
              ["runners", "Self-Hosted Runner Patterns"],
              ["ci", "CI"],
              ["openupm", "OpenUPM Prep"],
              ["publishing", "Publishing"],
            ].map(([id, label]) => (
              <a key={id} href={`#${id}`} className="block text-surface-500 hover:text-surface-200">
                {label}
              </a>
            ))}
          </nav>
        </div>

        <section className="mb-20">
          <SectionHeading id="setup">Setup</SectionHeading>
          <Prose>
            Install Yaver once, inject the Unity package, then point the package at a machine you
            control.
          </Prose>
          <Terminal title="unity-setup">
            <Cmd>npm install -g yaver-cli</Cmd>
            <Cmd>yaver sdk add feedback --platform unity --dir /path/to/UnityProject</Cmd>
            <Cmd>yaver test unity --dir /path/to/UnityProject --mode EditMode</Cmd>
          </Terminal>
        </section>

        <section className="mb-20">
          <SectionHeading id="mobile">Mobile Cases</SectionHeading>
          <Prose>
            On mobile, Yaver is strongest for feedback, screenshots, crash context, content refresh,
            scene reload, and redeploy. That is the honest mobile loop.
          </Prose>
        </section>

        <section className="mb-20">
          <SectionHeading id="desktop">Desktop Cases</SectionHeading>
          <Prose>
            On desktop, Yaver gets stronger because the agent machine can run Unity tests, build a
            player, relaunch it, and keep going while the phone only watches the results.
          </Prose>
        </section>

        <section className="mb-20">
          <SectionHeading id="runners">Self-Hosted Runner Patterns</SectionHeading>
          <div className="space-y-4">
            {[
              "Solo developer: Unity project on your own desktop or laptop, reachable through relay, Tailscale, or a tunnel.",
              "Home build machine: a stronger desktop or Mac mini that keeps working while you are away.",
              "Studio runner: a shared workstation, remote Linux box, or GPU VPS with a private model and controlled costs.",
            ].map((item) => (
              <div key={item} className="rounded-lg border border-surface-800 bg-surface-900/50 p-4 text-sm text-surface-400">
                {item}
              </div>
            ))}
          </div>
        </section>

        <section className="mb-20">
          <SectionHeading id="ci">CI</SectionHeading>
          <Prose>
            The repo now has Unity package tests and sample-project CI with EditMode tests, PlayMode
            tests, desktop builds, and Android builds. Apple-specific build pipelines still need
            macOS or Unity Build Automation later.
          </Prose>
        </section>

        <section className="mb-20">
          <SectionHeading id="openupm">OpenUPM Prep</SectionHeading>
          <Prose>
            The current working checklist for OpenUPM preparation lives here:
            {" "}
            <a
              href="https://github.com/yaver-io/yaver.io/blob/main/docs/unity-openupm-publishing.md"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              docs/unity-openupm-publishing.md
            </a>
            .
          </Prose>
        </section>

        <section className="mb-20">
          <SectionHeading id="publishing">Publishing</SectionHeading>
          <Prose>
            The recommended path is: private/local UPM first, OpenUPM next, then Unity Asset Store
            UPM once the SDK is hardened. The Unity package should stay separate from the Yaver CLI
            and agent binaries.
          </Prose>
        </section>
      </div>
    </div>
  );
}
