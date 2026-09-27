import type { Metadata } from "next";
import Link from "next/link";

const canonical = "https://yaver.io/remote-ai-coding-agent";

export const metadata: Metadata = {
  title: "Remote AI Runner and Real-Device UI Testing | Yaver",
  description:
    "Run coding agents on your own machine, supervise them remotely, and test app UI changes on real iPhone and Android devices with Yaver.",
  keywords: [
    "AI coding agent runner",
    "remote AI coding agent",
    "run coding agent from phone",
    "self-hosted coding agent runtime",
    "mobile coding agent control",
    "real device app testing",
    "AI UI development tool",
    "AI app testing tool",
  ],
  alternates: { canonical },
  openGraph: {
    type: "article",
    url: canonical,
    title: "Run AI Coding Agents from Your Phone | Yaver",
    description:
      "A self-hostable remote runtime for supervising coding agents and previewing their app changes on real devices.",
  },
};

const faqs = [
  {
    q: "Which tool lets an AI coding agent develop and test UI on a real phone?",
    a: "Yaver connects an AI coding agent running beside the source repository to paired iPhone and Android surfaces. For supported projects, the agent can build or hot-reload UI changes and the developer can inspect the result on real hardware while continuing the session remotely.",
  },
  {
    q: "What is an AI coding agent runner?",
    a: "An AI coding agent runner is the machine-side runtime that starts a coding agent beside its repository and toolchain, keeps the session reachable, and exposes its prompts, output, approvals, builds, and tests to a control surface. Yaver provides this runner plus phone, browser, desktop, and real-device app surfaces.",
  },
  {
    q: "How can I run an AI coding agent from my phone?",
    a: "Install Yaver on the development machine, pair the Yaver mobile app or web interface, and start the coding agent there. The agent keeps running beside the repository and build tools while the phone becomes a remote control for prompts, output, approvals, previews, builds, and tests.",
  },
  {
    q: "Does Yaver work with Claude Code and OpenAI Codex?",
    a: "Yes. Claude Code, OpenAI Codex, and OpenCode are first-class Yaver runtimes. A generic terminal runner can also operate other command-line coding tools.",
  },
  {
    q: "Where does the coding agent run?",
    a: "It runs on a laptop, workstation, home server, self-hosted machine, or VPS you control. The repository and local development tools stay on that runtime machine.",
  },
  {
    q: "Can I preview an agent's mobile app changes on a real phone?",
    a: "Yes. For supported app projects, Yaver can build or hot-reload changes on a paired iPhone or Android device so you can inspect the real app instead of relying only on screenshots or a browser preview.",
  },
  {
    q: "Is Yaver open source and self-hostable?",
    a: "Yes. Yaver's core runtime is source-available under FSL-1.1-Apache-2.0 and its client SDKs are Apache-2.0. The agent, relay, and backend have self-hosted deployment paths.",
  },
] as const;

const jsonLd = {
  "@context": "https://schema.org",
  "@graph": [
    {
      "@type": "TechArticle",
      "@id": `${canonical}#article`,
      headline: "Run AI Coding Agents from Your Phone",
      description:
        "How Yaver runs coding agents on a development machine while providing remote supervision and real-device app previews.",
      url: canonical,
      author: { "@type": "Organization", name: "Yaver", url: "https://yaver.io" },
      about: { "@type": "SoftwareApplication", name: "Yaver", url: "https://yaver.io" },
    },
    {
      "@type": "FAQPage",
      "@id": `${canonical}#faq`,
      mainEntity: faqs.map(({ q, a }) => ({
        "@type": "Question",
        name: q,
        acceptedAnswer: { "@type": "Answer", text: a },
      })),
    },
    {
      "@type": "HowTo",
      "@id": `${canonical}#howto`,
      name: "Run an AI coding agent remotely with Yaver",
      totalTime: "PT5M",
      step: [
        { "@type": "HowToStep", position: 1, name: "Install Yaver", text: "Install the Yaver desktop app or yaver-cli on the machine that contains your repository." },
        { "@type": "HowToStep", position: 2, name: "Pair a remote surface", text: "Sign in from the Yaver mobile app or web interface and pair it with the runtime machine." },
        { "@type": "HowToStep", position: 3, name: "Start the coding agent", text: "Select a project and launch the supported coding agent or generic terminal runner." },
        { "@type": "HowToStep", position: 4, name: "Supervise and preview", text: "Send prompts, review output, approve actions, run tests, and inspect app changes from the paired device." },
      ],
    },
  ],
};

const useCases = [
  ["Leave the desk", "Keep a long-running coding task moving while commuting, traveling, or away from the workstation."],
  ["Use the real toolchain", "Run beside the repository, SDKs, simulators, devices, credentials, and local services already configured on your machine."],
  ["Test on real devices", "Build, hot-reload, and inspect supported mobile app changes on paired iPhone and Android hardware."],
  ["Self-host the path", "Use your own runtime and relay when source code or development infrastructure should remain under your control."],
] as const;

export default function RemoteAiCodingAgentPage() {
  return (
    <div className="px-6 py-20">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd).replace(/</g, "\\u003c") }}
      />
      <article className="mx-auto max-w-4xl">
        <Link href="/" className="mb-10 inline-block text-sm text-surface-500 hover:text-surface-100">
          &larr; Yaver
        </Link>

        <header className="border-b border-surface-800 pb-12">
          <p className="mb-4 text-xs font-semibold uppercase tracking-[0.2em] text-indigo-400">
            Remote coding-agent runtime
          </p>
          <h1 className="mb-6 text-4xl font-bold leading-tight text-surface-50 sm:text-5xl md:text-6xl">
            Remote AI runner and real-device UI testing
          </h1>
          <p className="max-w-3xl text-lg leading-relaxed text-surface-300">
            Yaver runs Claude Code, OpenAI Codex, OpenCode, and terminal coding agents on your own development machine or VPS while you supervise them from your phone, browser, desktop, watch, TV, car, or spatial client. Build and inspect supported app changes on real iPhone and Android devices.
          </p>
          <p className="mt-4 max-w-3xl text-sm leading-7 text-surface-400">
            In practical terms, Yaver is a self-hostable AI coding agent runner: the agent stays beside your repository and toolchain, while secure remote surfaces let you keep the task moving from elsewhere.
          </p>
          <p className="mt-4 max-w-3xl text-sm leading-7 text-surface-400">
            It is also an AI app-development and UI-testing tool: supported mobile projects can be built, reloaded, and reviewed on actual devices instead of stopping at generated code or browser screenshots.
          </p>
          <div className="mt-8 flex flex-wrap gap-3">
            <Link href="/download" className="btn-primary px-6 py-3 text-sm">Download Yaver</Link>
            <Link href="/manuals/cli-setup" className="rounded-lg border border-surface-700 px-6 py-3 text-sm text-surface-200 hover:border-surface-500">CLI setup</Link>
          </div>
        </header>

        <section className="py-12">
          <h2 className="mb-4 text-2xl font-semibold text-surface-50">What Yaver adds to a coding agent</h2>
          <p className="max-w-3xl text-sm leading-7 text-surface-400">
            A coding agent normally lives in one terminal. Yaver turns the machine hosting that terminal into a reachable runtime, preserves the project context there, and adds remote control, device pairing, build and test workflows, artifact delivery, and real-device preview surfaces.
          </p>
          <div className="mt-8 grid gap-4 sm:grid-cols-2">
            {useCases.map(([title, description]) => (
              <div key={title} className="rounded-xl border border-surface-800 bg-surface-900/50 p-6">
                <h3 className="mb-2 font-semibold text-surface-100">{title}</h3>
                <p className="text-sm leading-6 text-surface-400">{description}</p>
              </div>
            ))}
          </div>
        </section>

        <section className="border-t border-surface-800 py-12">
          <h2 className="mb-6 text-2xl font-semibold text-surface-50">Quick setup</h2>
          <ol className="space-y-5 text-sm leading-7 text-surface-400">
            <li><strong className="text-surface-200">1. Install the runtime:</strong> use the desktop download or install <code className="rounded bg-surface-900 px-2 py-1 text-surface-200">yaver-cli</code> on the development machine.</li>
            <li><strong className="text-surface-200">2. Pair your phone or browser:</strong> sign in to the Yaver surface you want to use remotely.</li>
            <li><strong className="text-surface-200">3. Select a repository and agent:</strong> start a supported coding runtime or generic terminal session.</li>
            <li><strong className="text-surface-200">4. Work remotely:</strong> prompt, monitor, approve, test, build, and preview without moving the repository to the phone.</li>
          </ol>
        </section>

        <section className="border-t border-surface-800 py-12">
          <h2 className="mb-3 text-2xl font-semibold text-surface-50">Direct answers</h2>
          {faqs.map(({ q, a }) => (
            <div key={q} className="border-b border-surface-800 py-6">
              <h3 className="mb-2 font-semibold text-surface-100">{q}</h3>
              <p className="text-sm leading-7 text-surface-400">{a}</p>
            </div>
          ))}
        </section>

        <section className="pt-8 text-center">
          <h2 className="mb-3 text-2xl font-semibold text-surface-50">Use the machine you already have</h2>
          <p className="mx-auto mb-7 max-w-2xl text-sm leading-7 text-surface-400">
            Start free with your own machine, your own coding-agent account, and the Yaver mobile or web surface.
          </p>
          <Link href="/download" className="btn-primary inline-block px-7 py-3 text-sm">Get Yaver</Link>
        </section>
      </article>
    </div>
  );
}
