import Link from "next/link";

export const metadata = {
  title: "Full On-Prem Free Stack — Yaver + Ollama + GLM-4.7-Flash + Aider",
  description:
    "Run a complete AI coding setup for $0/month. Step-by-step guide with SWE-bench analysis, hardware requirements, and everything you need to know about local LLMs.",
};

export default function FreeOnPremManual() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <Link
          href="/manuals"
          className="mb-8 inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to Manuals
        </Link>

        <div className="mb-4 inline-flex items-center rounded-full border border-green-500/20 bg-green-500/10 px-3 py-1 text-xs font-medium text-green-400">
          $0/month &middot; Fully on-prem &middot; No API keys
        </div>

        <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
          Full On-Prem Free Stack
        </h1>
        <p className="mb-6 text-base leading-relaxed text-surface-400">
          Yaver + Ollama + GLM-4.7-Flash + Aider &mdash; a complete AI coding
          setup that runs entirely on your hardware. No cloud, no API keys, no
          recurring costs. Control it from your phone.
        </p>

        {/* Table of contents */}
        <nav className="mb-12 rounded-lg border border-surface-800 bg-surface-900/50 p-5">
          <h2 className="mb-3 text-sm font-semibold text-surface-200">
            In this guide
          </h2>
          <ol className="space-y-1.5 text-sm text-surface-400">
            <li>
              <a href="#the-stack" className="hover:text-surface-200">
                1. The stack
              </a>
            </li>
            <li>
              <a href="#swe-analysis" className="hover:text-surface-200">
                2. SWE-bench analysis &mdash; how good is it?
              </a>
            </li>
            <li>
              <a href="#hardware" className="hover:text-surface-200">
                3. Hardware requirements
              </a>
            </li>
            <li>
              <a href="#concepts" className="hover:text-surface-200">
                4. Key concepts (SWE-bench, Flash, quantization)
              </a>
            </li>
            <li>
              <a href="#setup" className="hover:text-surface-200">
                5. Step-by-step setup
              </a>
            </li>
            <li>
              <a href="#licenses" className="hover:text-surface-200">
                6. Licenses &amp; open-source status
              </a>
            </li>
          </ol>
        </nav>

        {/* ─── 1. The Stack ─── */}
        <section id="the-stack" className="mb-16">
          <h2 className="mb-4 text-xl font-semibold text-surface-100">
            1. The stack
          </h2>
          <p className="mb-6 text-sm leading-relaxed text-surface-400">
            Four free, open-source tools that together give you a fully local AI
            coding assistant you can control from your phone:
          </p>

          <div className="space-y-3">
            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold text-surface-100">
                  Ollama
                </h3>
                <span className="rounded-full bg-green-500/10 px-2 py-0.5 text-[10px] font-medium text-green-400">
                  MIT
                </span>
              </div>
              <p className="mt-1 text-sm text-surface-400">
                Local LLM runtime. Downloads and runs open-weight models on your
                machine. Handles model management, quantization, and GPU
                acceleration automatically.
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold text-surface-100">
                  GLM-4.7-Flash
                </h3>
                <span className="rounded-full bg-green-500/10 px-2 py-0.5 text-[10px] font-medium text-green-400">
                  MIT
                </span>
              </div>
              <p className="mt-1 text-sm text-surface-400">
                The AI model itself. A 30B-parameter Mixture-of-Experts (MoE) model from
                Zhipu AI that only activates 3B parameters per token &mdash;
                making it fast and memory-efficient while scoring{" "}
                <strong className="text-surface-200">
                  59.2% on SWE-bench Verified
                </strong>
                , the strongest in its size class.
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold text-surface-100">
                  Aider
                </h3>
                <span className="rounded-full bg-green-500/10 px-2 py-0.5 text-[10px] font-medium text-green-400">
                  Apache 2.0
                </span>
              </div>
              <p className="mt-1 text-sm text-surface-400">
                AI pair programming tool that connects to the model, understands
                your codebase, makes git-aware edits, and runs in a terminal.
                Works with any LLM backend including Ollama.
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold text-surface-100">
                  Yaver CLI
                </h3>
                <span className="rounded-full bg-green-500/10 px-2 py-0.5 text-[10px] font-medium text-green-400">
                  AGPL-3.0
                </span>
              </div>
              <p className="mt-1 text-sm text-surface-400">
                Makes the whole setup controllable from your phone. Runs Aider
                in a tmux session and streams output to the Yaver mobile app.
                Peer-to-peer &mdash; no middleman servers.
              </p>
            </div>
          </div>

          {/* Architecture diagram */}
          <div className="mt-6 rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <pre className="text-xs leading-relaxed text-surface-400 overflow-x-auto">
              {`┌──────────────┐        ┌──────────────┐
│  Your Phone  │───────▶│  Your PC     │
│  Yaver App   │  WiFi  │              │
└──────────────┘  or    │  Yaver CLI   │
                  VPN   │    ↓         │
                        │  Aider       │
                        │    ↓         │
                        │  Ollama      │
                        │    ↓         │
                        │  GLM-4.7-    │
                        │  Flash       │
                        └──────────────┘
                        All on your machine.
                        Nothing leaves your network.`}
            </pre>
          </div>
        </section>

        {/* ─── 2. SWE-bench Analysis ─── */}
        <section id="swe-analysis" className="mb-16">
          <h2 className="mb-4 text-xl font-semibold text-surface-100">
            2. SWE-bench analysis &mdash; how good is it?
          </h2>

          <div className="mb-6 rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-2 text-sm font-semibold text-surface-200">
              What is SWE-bench?
            </h3>
            <p className="text-sm leading-relaxed text-surface-400">
              <strong className="text-surface-200">SWE-bench</strong> (Software
              Engineering Benchmark) is the industry-standard benchmark for
              measuring how well AI models can solve{" "}
              <em>real software engineering tasks</em>. It consists of 2,294
              real GitHub issues from popular Python repositories (Django,
              scikit-learn, Flask, etc.). Each task requires the model to read
              the issue, understand the codebase, and produce a working patch
              that passes the project&apos;s test suite.
            </p>
            <p className="mt-3 text-sm leading-relaxed text-surface-400">
              <strong className="text-surface-200">SWE-bench Verified</strong>{" "}
              is a human-validated subset of 500 problems, curated to remove
              ambiguous or unfair tasks. It&apos;s considered the gold standard
              for comparing coding AI.
            </p>
            <p className="mt-3 text-sm leading-relaxed text-surface-400">
              A score of <strong className="text-surface-200">59.2%</strong>{" "}
              means GLM-4.7-Flash can independently solve nearly 6 out of 10
              real software engineering issues &mdash; writing correct patches
              from scratch that pass all tests.
            </p>
          </div>

          <h3 className="mb-3 text-sm font-semibold text-surface-200">
            SWE-bench Verified scores &mdash; open-source models you can run
            locally
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Model
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Params
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Active
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    SWE-bench Verified
                  </th>
                  <th className="pb-3 font-medium text-surface-300">
                    Can run locally?
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-green-500/10 bg-green-500/5">
                  <td className="py-3 pr-4 font-medium text-green-400">
                    GLM-4.7-Flash
                  </td>
                  <td className="py-3 pr-4">30B MoE</td>
                  <td className="py-3 pr-4">3B</td>
                  <td className="py-3 pr-4 font-semibold text-green-400">
                    59.2%
                  </td>
                  <td className="py-3 text-green-400">
                    Yes &mdash; 19 GB (q4)
                  </td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">GLM-4.7</td>
                  <td className="py-3 pr-4">~230B MoE</td>
                  <td className="py-3 pr-4">~40B</td>
                  <td className="py-3 pr-4 font-semibold text-surface-200">
                    73.8%
                  </td>
                  <td className="py-3">Needs multi-GPU</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">
                    Qwen3-30B
                  </td>
                  <td className="py-3 pr-4">30B</td>
                  <td className="py-3 pr-4">30B</td>
                  <td className="py-3 pr-4">22.0%</td>
                  <td className="py-3">Yes &mdash; 20 GB (q4)</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">
                    DeepSeek-Coder-V2
                  </td>
                  <td className="py-3 pr-4">236B MoE</td>
                  <td className="py-3 pr-4">21B</td>
                  <td className="py-3 pr-4">18.2%</td>
                  <td className="py-3">16B variant &mdash; 10 GB (q4)</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">
                    Qwen2.5-Coder-32B
                  </td>
                  <td className="py-3 pr-4">32B</td>
                  <td className="py-3 pr-4">32B</td>
                  <td className="py-3 pr-4">~20%</td>
                  <td className="py-3">Yes &mdash; 20 GB (q4)</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4 text-surface-300">
                    CodeLlama-34B
                  </td>
                  <td className="py-3 pr-4">34B</td>
                  <td className="py-3 pr-4">34B</td>
                  <td className="py-3 pr-4">~5%</td>
                  <td className="py-3">Yes &mdash; 20 GB (q4)</td>
                </tr>
              </tbody>
            </table>
          </div>

          <p className="mt-4 text-xs text-surface-500">
            Scores are from SWE-bench Verified leaderboard. &ldquo;Active&rdquo;
            = parameters used per token (MoE models activate a subset).
            GLM-4.7-Flash is ~3x better than the next open model in its size
            class.
          </p>

          <h3 className="mb-3 mt-8 text-sm font-semibold text-surface-200">
            For context: top closed-source models
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Model
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    SWE-bench Verified
                  </th>
                  <th className="pb-3 font-medium text-surface-300">Cost</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">
                    Claude Opus 4 + tools
                  </td>
                  <td className="py-3 pr-4">72.0%</td>
                  <td className="py-3">$15/1M input tokens</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">GPT-4o</td>
                  <td className="py-3 pr-4">~33%</td>
                  <td className="py-3">$2.50/1M input tokens</td>
                </tr>
                <tr className="border-b border-green-500/10 bg-green-500/5">
                  <td className="py-3 pr-4 font-medium text-green-400">
                    GLM-4.7-Flash (local)
                  </td>
                  <td className="py-3 pr-4 font-semibold text-green-400">
                    59.2%
                  </td>
                  <td className="py-3 text-green-400">$0 (your hardware)</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div className="mt-6 rounded-lg border border-amber-500/20 bg-amber-500/5 p-4">
            <h4 className="mb-1 text-sm font-semibold text-amber-400">
              Important note on benchmarks
            </h4>
            <p className="text-sm leading-relaxed text-surface-400">
              SWE-bench scores depend heavily on the scaffolding (the tool that
              uses the model). Aider, the coding agent in this stack, uses its
              own prompting strategy which may produce different results than the
              benchmark&apos;s default setup. Real-world performance also depends
              on your codebase, language (SWE-bench is Python-only), and task
              complexity. These numbers are useful for relative comparison, not
              as absolute guarantees.
            </p>
          </div>
        </section>

        {/* ─── 3. Hardware Requirements ─── */}
        <section id="hardware" className="mb-16">
          <h2 className="mb-4 text-xl font-semibold text-surface-100">
            3. Hardware requirements
          </h2>

          <h3 className="mb-3 text-sm font-semibold text-surface-200">
            GLM-4.7-Flash quantization options
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Quantization
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Download size
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    RAM/VRAM needed
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Quality
                  </th>
                  <th className="pb-3 font-medium text-surface-300">
                    Recommendation
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-green-500/10 bg-green-500/5">
                  <td className="py-3 pr-4 font-medium text-green-400">
                    Q4_K_M (default)
                  </td>
                  <td className="py-3 pr-4">19 GB</td>
                  <td className="py-3 pr-4">~22 GB</td>
                  <td className="py-3 pr-4">Very good</td>
                  <td className="py-3 text-green-400">
                    Best for most PCs
                  </td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">Q8_0</td>
                  <td className="py-3 pr-4">32 GB</td>
                  <td className="py-3 pr-4">~36 GB</td>
                  <td className="py-3 pr-4">Excellent</td>
                  <td className="py-3">If you have 48+ GB RAM</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4 text-surface-300">BF16 (full)</td>
                  <td className="py-3 pr-4">60 GB</td>
                  <td className="py-3 pr-4">~64 GB</td>
                  <td className="py-3 pr-4">Perfect</td>
                  <td className="py-3">Multi-GPU or high-end workstation</td>
                </tr>
              </tbody>
            </table>
          </div>

          <h3 className="mb-3 mt-8 text-sm font-semibold text-surface-200">
            Can it run on your PC?
          </h3>
          <div className="space-y-3">
            <div className="rounded-lg border border-green-500/20 bg-green-500/5 p-4">
              <div className="flex items-center gap-2">
                <span className="text-green-400 text-lg">&#10003;</span>
                <h4 className="text-sm font-semibold text-green-400">
                  24 GB RAM &mdash; Yes, it works
                </h4>
              </div>
              <p className="mt-2 text-sm text-surface-400">
                The Q4_K_M quantization needs ~22 GB. With 24 GB of system RAM,
                this runs on CPU. It&apos;s slower than GPU (expect 5-15
                tokens/second vs. 30-60 on GPU), but it works. Close other apps
                to free memory. macOS with Apple Silicon (M1/M2/M3/M4) is
                particularly good here because unified memory means the GPU and
                CPU share the same RAM pool.
              </p>
            </div>

            <div className="rounded-lg border border-green-500/20 bg-green-500/5 p-4">
              <div className="flex items-center gap-2">
                <span className="text-green-400 text-lg">&#10003;</span>
                <h4 className="text-sm font-semibold text-green-400">
                  32 GB RAM &mdash; Comfortable
                </h4>
              </div>
              <p className="mt-2 text-sm text-surface-400">
                Q4_K_M runs smoothly with headroom for your IDE and browser. If
                you have a GPU with 24 GB VRAM (RTX 3090, RTX 4090, etc.), the
                model runs entirely on GPU for much faster inference.
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <div className="flex items-center gap-2">
                <span className="text-amber-400 text-lg">&#9888;</span>
                <h4 className="text-sm font-semibold text-amber-400">
                  16 GB RAM &mdash; Too tight for GLM-4.7-Flash
                </h4>
              </div>
              <p className="mt-2 text-sm text-surface-400">
                Consider smaller models instead:{" "}
                <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                  qwen2.5-coder:7b
                </code>{" "}
                (4.7 GB) or{" "}
                <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                  deepseek-coder-v2:16b
                </code>{" "}
                (8.9 GB). Lower SWE-bench scores, but they fit.
              </p>
            </div>
          </div>

          <h3 className="mb-3 mt-8 text-sm font-semibold text-surface-200">
            GPU vs. CPU performance
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Hardware
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Expected speed (Q4)
                  </th>
                  <th className="pb-3 font-medium text-surface-300">Notes</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">
                    RTX 4090 (24 GB VRAM)
                  </td>
                  <td className="py-3 pr-4">~40-60 tok/s</td>
                  <td className="py-3">Fits entirely in VRAM, fastest option</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">
                    RTX 3090 (24 GB VRAM)
                  </td>
                  <td className="py-3 pr-4">~30-45 tok/s</td>
                  <td className="py-3">Great performance, widely available used</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">
                    Apple M2/M3/M4 (24 GB unified)
                  </td>
                  <td className="py-3 pr-4">~20-35 tok/s</td>
                  <td className="py-3">Unified memory = GPU + CPU share RAM</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">
                    RTX 4060 (8 GB VRAM) + 32 GB RAM
                  </td>
                  <td className="py-3 pr-4">~15-25 tok/s</td>
                  <td className="py-3">
                    Partial offload: some layers GPU, rest CPU
                  </td>
                </tr>
                <tr>
                  <td className="py-3 pr-4 text-surface-300">
                    CPU only (24 GB RAM)
                  </td>
                  <td className="py-3 pr-4">~5-15 tok/s</td>
                  <td className="py-3">Slower but works. Good enough for coding tasks.</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p className="mt-3 text-xs text-surface-500">
            Token speed estimates are approximate and vary by CPU generation,
            RAM speed, and context length. For coding tasks, even 5 tok/s is
            usable &mdash; you&apos;re reading and reviewing output, not chatting
            in real-time.
          </p>
        </section>

        {/* ─── 4. Key Concepts ─── */}
        <section id="concepts" className="mb-16">
          <h2 className="mb-4 text-xl font-semibold text-surface-100">
            4. Key concepts
          </h2>

          <div className="space-y-6">
            <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
              <h3 className="mb-2 text-sm font-semibold text-surface-200">
                What is SWE-bench?
              </h3>
              <p className="text-sm leading-relaxed text-surface-400">
                <strong className="text-surface-200">
                  Software Engineering Benchmark
                </strong>{" "}
                &mdash; a test suite of 2,294 real bug reports and feature
                requests from popular open-source Python projects. The AI must
                read the issue, understand the codebase, and write a patch that
                passes all tests. It&apos;s the closest thing we have to
                measuring &ldquo;can this AI actually do my job?&rdquo;
              </p>
              <p className="mt-2 text-sm text-surface-400">
                <strong className="text-surface-200">SWE-bench Verified</strong>{" "}
                is a smaller, human-curated subset of 500 problems that removes
                ambiguous tasks. Most leaderboards use this version.
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
              <h3 className="mb-2 text-sm font-semibold text-surface-200">
                What does &ldquo;Flash&rdquo; mean?
              </h3>
              <p className="text-sm leading-relaxed text-surface-400">
                In AI model naming, <strong className="text-surface-200">&ldquo;Flash&rdquo;</strong>{" "}
                means a faster, lighter version of a model optimized for
                efficiency. GLM-4.7-Flash is the efficient version of GLM-4.7.
                It uses a{" "}
                <strong className="text-surface-200">
                  Mixture-of-Experts (MoE)
                </strong>{" "}
                architecture: the model has 30 billion total parameters, but
                only activates ~3 billion per token. This means:
              </p>
              <ul className="mt-2 space-y-1 text-sm text-surface-400">
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    <strong className="text-surface-200">Faster inference</strong>{" "}
                    &mdash; less computation per token
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    <strong className="text-surface-200">Less memory used during generation</strong>{" "}
                    &mdash; only 3B params are active
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    <strong className="text-surface-200">Full model still needs to be loaded</strong>{" "}
                    &mdash; all 30B params must be in memory, even though only 3B
                    fire at a time
                  </span>
                </li>
              </ul>
              <p className="mt-2 text-sm text-surface-400">
                Think of it like a large office building: all 30 floors exist
                (loaded in RAM), but only 3 floors have their lights on at any
                moment (active parameters).
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
              <h3 className="mb-2 text-sm font-semibold text-surface-200">
                What is quantization?
              </h3>
              <p className="text-sm leading-relaxed text-surface-400">
                <strong className="text-surface-200">Quantization</strong>{" "}
                reduces the precision of a model&apos;s numbers to make it
                smaller and faster. Neural networks store weights as numbers.
                The original model uses 16-bit floating point (BF16) &mdash;
                each number takes 2 bytes. Quantization converts these to
                lower-precision formats:
              </p>
              <div className="mt-3 overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-surface-800 text-left">
                      <th className="pb-2 pr-4 font-medium text-surface-300">
                        Format
                      </th>
                      <th className="pb-2 pr-4 font-medium text-surface-300">
                        Bits per weight
                      </th>
                      <th className="pb-2 pr-4 font-medium text-surface-300">
                        Size reduction
                      </th>
                      <th className="pb-2 font-medium text-surface-300">
                        Quality loss
                      </th>
                    </tr>
                  </thead>
                  <tbody className="text-surface-400">
                    <tr className="border-b border-surface-800/60">
                      <td className="py-2 pr-4 text-surface-300">BF16</td>
                      <td className="py-2 pr-4">16 bits</td>
                      <td className="py-2 pr-4">None (original)</td>
                      <td className="py-2">None</td>
                    </tr>
                    <tr className="border-b border-surface-800/60">
                      <td className="py-2 pr-4 text-surface-300">Q8_0</td>
                      <td className="py-2 pr-4">8 bits</td>
                      <td className="py-2 pr-4">~50%</td>
                      <td className="py-2">Negligible</td>
                    </tr>
                    <tr>
                      <td className="py-2 pr-4 text-surface-300">Q4_K_M</td>
                      <td className="py-2 pr-4">4 bits</td>
                      <td className="py-2 pr-4">~75%</td>
                      <td className="py-2">
                        Small &mdash; barely noticeable for coding
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p className="mt-3 text-sm text-surface-400">
                <strong className="text-surface-200">Q4_K_M</strong>{" "}
                (&ldquo;4-bit quantization, K-quant method, medium quality&rdquo;)
                is the sweet spot for most users. It&apos;s 75% smaller than the
                original while retaining excellent coding quality. That&apos;s
                how a 30B-parameter model fits in 19 GB.
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
              <h3 className="mb-2 text-sm font-semibold text-surface-200">
                What is Mixture of Experts (MoE)?
              </h3>
              <p className="text-sm leading-relaxed text-surface-400">
                Traditional AI models activate all their parameters for every
                token. <strong className="text-surface-200">MoE models</strong>{" "}
                split the network into &ldquo;expert&rdquo; sub-networks and a
                router that picks which experts to use for each token.
                GLM-4.7-Flash has many expert networks but only activates
                a few (totaling ~3B parameters) per token. This gives you the
                knowledge of a 30B model with the speed of a 3B model.
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
              <h3 className="mb-2 text-sm font-semibold text-surface-200">
                Open-source vs. open-weight vs. proprietary
              </h3>
              <p className="text-sm leading-relaxed text-surface-400">
                <strong className="text-surface-200">Permissive open-source</strong>{" "}
                (MIT, Apache 2.0): code and weights are free to use, modify,
                and redistribute for any purpose, including commercial.
                GLM-4.7-Flash, Ollama, and Aider are in this category. Yaver
                itself is <strong className="text-surface-200">copyleft open-source</strong>{" "}
                (AGPL-3.0-only) &mdash; same freedoms, but if you expose a
                modified Yaver as a network service you must publish your
                changes.
              </p>
              <p className="mt-2 text-sm text-surface-400">
                <strong className="text-surface-200">Open-weight</strong>: model
                weights are downloadable but the license may restrict commercial
                use or modification. Llama 3.1 is an example.
              </p>
              <p className="mt-2 text-sm text-surface-400">
                <strong className="text-surface-200">Proprietary</strong>: you
                can only access the model via a paid API. GPT-4o and Claude are
                examples.
              </p>
            </div>
          </div>
        </section>

        {/* ─── 5. Step-by-Step Setup ─── */}
        <section id="setup" className="mb-16">
          <h2 className="mb-4 text-xl font-semibold text-surface-100">
            5. Step-by-step setup
          </h2>
          <p className="mb-6 text-sm text-surface-400">
            From zero to a working on-prem AI coding assistant in about 15
            minutes (plus model download time).
          </p>

          {/* Step 1: Ollama */}
          <div className="mb-8">
            <div className="mb-3 flex items-center gap-3">
              <span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-300">
                1
              </span>
              <h3 className="text-base font-semibold text-surface-100">
                Install Ollama
              </h3>
            </div>

            <div className="space-y-4">
              <div>
                <h4 className="mb-2 text-sm font-medium text-surface-200">
                  macOS
                </h4>
                <div className="terminal">
                  <div className="terminal-header">
                    <div className="terminal-dot bg-[#ff5f57]" />
                    <div className="terminal-dot bg-[#febc2e]" />
                    <div className="terminal-dot bg-[#28c840]" />
                    <span className="ml-3 text-xs text-surface-500">
                      terminal
                    </span>
                  </div>
                  <div className="terminal-body text-[13px]">
                    <div>
                      <span className="text-surface-400">$</span>{" "}
                      <span className="text-surface-200 select-all">
                        brew install ollama
                      </span>
                    </div>
                  </div>
                </div>
              </div>

              <div>
                <h4 className="mb-2 text-sm font-medium text-surface-200">
                  Linux
                </h4>
                <div className="terminal">
                  <div className="terminal-header">
                    <div className="terminal-dot bg-[#ff5f57]" />
                    <div className="terminal-dot bg-[#febc2e]" />
                    <div className="terminal-dot bg-[#28c840]" />
                    <span className="ml-3 text-xs text-surface-500">
                      terminal
                    </span>
                  </div>
                  <div className="terminal-body text-[13px]">
                    <div>
                      <span className="text-surface-400">$</span>{" "}
                      <span className="text-surface-200 select-all">
                        curl -fsSL https://ollama.com/install.sh | sh
                      </span>
                    </div>
                  </div>
                </div>
              </div>

              <div>
                <h4 className="mb-2 text-sm font-medium text-surface-200">
                  Windows
                </h4>
                <p className="text-sm text-surface-400">
                  Download the installer from{" "}
                  <a
                    href="https://ollama.com/download/windows"
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
                  >
                    ollama.com/download/windows
                  </a>
                  . Run it &mdash; adds{" "}
                  <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                    ollama
                  </code>{" "}
                  to your PATH automatically.
                </p>
              </div>
            </div>
          </div>

          {/* Step 2: Pull GLM-4.7-Flash */}
          <div className="mb-8">
            <div className="mb-3 flex items-center gap-3">
              <span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-300">
                2
              </span>
              <h3 className="text-base font-semibold text-surface-100">
                Download GLM-4.7-Flash
              </h3>
            </div>

            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">terminal</span>
              </div>
              <div className="terminal-body space-y-3 text-[13px]">
                <div className="text-surface-500">
                  # Download the model (~19 GB, one-time)
                </div>
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    ollama pull glm-4.7-flash
                  </span>
                </div>
                <div className="pl-2 text-surface-400">
                  pulling manifest... done
                </div>
                <div className="pl-2 text-surface-400">
                  pulling 8a0e93837e63... 100% 19 GB
                </div>
                <div className="h-px bg-surface-800/60" />
                <div className="text-surface-500"># Verify it works</div>
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    ollama run glm-4.7-flash &quot;Write a Python function that
                    reverses a linked list&quot;
                  </span>
                </div>
                <div className="pl-2 text-green-400/80">
                  def reverse_linked_list(head):
                </div>
                <div className="pl-2 text-green-400/80">
                  &nbsp;&nbsp;&nbsp;&nbsp;prev, current = None, head
                </div>
                <div className="pl-2 text-green-400/80">
                  &nbsp;&nbsp;&nbsp;&nbsp;while current:
                </div>
                <div className="pl-2 text-green-400/80">
                  &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;current.next,
                  prev, current = prev, current, current.next
                </div>
                <div className="pl-2 text-green-400/80">
                  &nbsp;&nbsp;&nbsp;&nbsp;return prev
                </div>
              </div>
            </div>

            <div className="mt-4 rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <p className="text-sm text-surface-400">
                <strong className="text-surface-200">
                  If you have less than 24 GB RAM
                </strong>
                , use a smaller model instead:
              </p>
              <div className="terminal mt-3">
                <div className="terminal-header">
                  <div className="terminal-dot bg-[#ff5f57]" />
                  <div className="terminal-dot bg-[#febc2e]" />
                  <div className="terminal-dot bg-[#28c840]" />
                  <span className="ml-3 text-xs text-surface-500">
                    terminal
                  </span>
                </div>
                <div className="terminal-body space-y-2 text-[13px]">
                  <div>
                    <span className="text-surface-400">$</span>{" "}
                    <span className="text-surface-200 select-all">
                      ollama pull qwen2.5-coder:7b
                    </span>
                    <span className="ml-2 text-surface-500">
                      # 4.7 GB &mdash; fits in 8 GB RAM
                    </span>
                  </div>
                </div>
              </div>
            </div>
          </div>

          {/* Step 3: Install Aider */}
          <div className="mb-8">
            <div className="mb-3 flex items-center gap-3">
              <span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-300">
                3
              </span>
              <h3 className="text-base font-semibold text-surface-100">
                Install Aider
              </h3>
            </div>

            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">
                  terminal
                </span>
              </div>
              <div className="terminal-body space-y-3 text-[13px]">
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    pip install aider-chat
                  </span>
                </div>
                <div className="h-px bg-surface-800/60" />
                <div className="text-surface-500">
                  # Set Ollama as the backend
                </div>
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    export OLLAMA_API_BASE=http://localhost:11434
                  </span>
                </div>
                <div className="h-px bg-surface-800/60" />
                <div className="text-surface-500">
                  # Test it (in any git repo)
                </div>
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    aider --model ollama/glm-4.7-flash
                  </span>
                </div>
                <div className="pl-2 text-green-400/80">
                  Aider v0.x.x
                </div>
                <div className="pl-2 text-green-400/80">
                  Model: ollama/glm-4.7-flash with whole edit format
                </div>
              </div>
            </div>

            <div className="terminal mt-4">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">
                  windows (powershell)
                </span>
              </div>
              <div className="terminal-body space-y-3 text-[13px]">
                <div>
                  <span className="text-surface-400">&gt;</span>{" "}
                  <span className="text-surface-200 select-all">
                    pip install aider-chat
                  </span>
                </div>
                <div>
                  <span className="text-surface-400">&gt;</span>{" "}
                  <span className="text-surface-200 select-all">
                    $env:OLLAMA_API_BASE = &quot;http://localhost:11434&quot;
                  </span>
                </div>
                <div>
                  <span className="text-surface-400">&gt;</span>{" "}
                  <span className="text-surface-200 select-all">
                    aider --model ollama/glm-4.7-flash
                  </span>
                </div>
              </div>
            </div>
          </div>

          {/* Step 4: Install Yaver CLI */}
          <div className="mb-8">
            <div className="mb-3 flex items-center gap-3">
              <span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-300">
                4
              </span>
              <h3 className="text-base font-semibold text-surface-100">
                Install Yaver CLI
              </h3>
            </div>

            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">
                  macOS / linux
                </span>
              </div>
              <div className="terminal-body space-y-2 text-[13px]">
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    brew install kivanccakmak/yaver/yaver
                  </span>
                </div>
              </div>
            </div>

            <div className="terminal mt-3">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">
                  windows (powershell)
                </span>
              </div>
              <div className="terminal-body space-y-2 text-[13px]">
                <div>
                  <span className="text-surface-400">&gt;</span>{" "}
                  <span className="text-surface-200 select-all">
                    scoop bucket add yaver
                    https://github.com/kivanccakmak/scoop-yaver
                  </span>
                </div>
                <div>
                  <span className="text-surface-400">&gt;</span>{" "}
                  <span className="text-surface-200 select-all">
                    scoop install yaver
                  </span>
                </div>
              </div>
            </div>
          </div>

          {/* Step 5: Configure & start */}
          <div className="mb-8">
            <div className="mb-3 flex items-center gap-3">
              <span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-300">
                5
              </span>
              <h3 className="text-base font-semibold text-surface-100">
                Configure and start
              </h3>
            </div>

            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">terminal</span>
              </div>
              <div className="terminal-body space-y-3 text-[13px]">
                <div className="text-surface-500"># Sign in (opens browser)</div>
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    yaver auth
                  </span>
                </div>
                <div className="pl-2 text-green-400/80">
                  Signed in as you@gmail.com
                </div>
                <div className="h-px bg-surface-800/60" />
                <div className="text-surface-500">
                  # Set the runner to use Aider + GLM-4.7-Flash
                </div>
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    {`yaver set-runner custom "OLLAMA_API_BASE=http://localhost:11434 aider --model ollama/glm-4.7-flash {prompt}"`}
                  </span>
                </div>
                <div className="h-px bg-surface-800/60" />
                <div className="text-surface-500">
                  # Start (no relay needed for same-WiFi or Tailscale)
                </div>
                <div>
                  <span className="text-surface-400">$</span>{" "}
                  <span className="text-surface-200 select-all">
                    yaver serve --no-relay
                  </span>
                </div>
                <div className="pl-2 text-green-400/80">
                  Agent started on :18080
                </div>
                <div className="pl-2 text-green-400/80">
                  Ready. Waiting for tasks...
                </div>
              </div>
            </div>
          </div>

          {/* Step 6: Connect from phone */}
          <div className="mb-8">
            <div className="mb-3 flex items-center gap-3">
              <span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-300">
                6
              </span>
              <h3 className="text-base font-semibold text-surface-100">
                Connect from your phone
              </h3>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
              <ol className="space-y-2 text-sm text-surface-400">
                <li className="flex gap-3">
                  <span className="text-surface-300 font-medium">1.</span>
                  <span>
                    Download the Yaver app (App Store / Google Play &mdash; free)
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-300 font-medium">2.</span>
                  <span>Sign in with the same account</span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-300 font-medium">3.</span>
                  <span>
                    Your machine appears automatically (if on same WiFi) or via
                    Tailscale
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-300 font-medium">4.</span>
                  <span>
                    Send a coding task &mdash; it runs on your local
                    GLM-4.7-Flash model
                  </span>
                </li>
              </ol>
            </div>

            <p className="mt-4 text-sm text-surface-400">
              For remote access outside your WiFi, set up{" "}
              <Link
                href="/manuals/local-llm#set-up-tailscale"
                className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
              >
                Tailscale
              </Link>{" "}
              (free for personal use) or{" "}
              <Link
                href="/manuals/relay-setup"
                className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
              >
                self-host a relay server
              </Link>
              .
            </p>
          </div>

          {/* Done */}
          <div className="rounded-lg border border-green-500/20 bg-green-500/5 p-5">
            <h3 className="mb-2 text-sm font-semibold text-green-400">
              That&apos;s it. You&apos;re running a fully on-prem AI coding
              setup for $0/month.
            </h3>
            <p className="text-sm text-surface-400">
              No data leaves your machine. No API keys. No subscriptions. The
              model runs on your hardware, the connection is peer-to-peer, and
              every piece of software is open source.
            </p>
          </div>
        </section>

        {/* ─── 6. Licenses ─── */}
        <section id="licenses" className="mb-16">
          <h2 className="mb-4 text-xl font-semibold text-surface-100">
            6. Licenses &amp; open-source status
          </h2>

          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Component
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    License
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-300">
                    Commercial use
                  </th>
                  <th className="pb-3 font-medium text-surface-300">Cost</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">Ollama</td>
                  <td className="py-3 pr-4">MIT</td>
                  <td className="py-3 pr-4 text-green-400">Yes</td>
                  <td className="py-3">Free</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">GLM-4.7-Flash</td>
                  <td className="py-3 pr-4">MIT</td>
                  <td className="py-3 pr-4 text-green-400">Yes</td>
                  <td className="py-3">Free</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">Aider</td>
                  <td className="py-3 pr-4">Apache 2.0</td>
                  <td className="py-3 pr-4 text-green-400">Yes</td>
                  <td className="py-3">Free</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">Yaver CLI</td>
                  <td className="py-3 pr-4">AGPL-3.0</td>
                  <td className="py-3 pr-4 text-green-400">Yes</td>
                  <td className="py-3">Free</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">Yaver Mobile</td>
                  <td className="py-3 pr-4">AGPL-3.0</td>
                  <td className="py-3 pr-4 text-green-400">Yes</td>
                  <td className="py-3">Free</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-4 text-surface-300">
                    Tailscale (optional)
                  </td>
                  <td className="py-3 pr-4">
                    BSD 3-Clause (client)
                  </td>
                  <td className="py-3 pr-4 text-green-400">Yes</td>
                  <td className="py-3">
                    Free for personal (100 devices)
                  </td>
                </tr>
                <tr>
                  <td className="py-3 pr-4 font-semibold text-surface-100">
                    Total
                  </td>
                  <td className="py-3 pr-4" />
                  <td className="py-3 pr-4" />
                  <td className="py-3 font-semibold text-green-400">
                    $0/month
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <p className="mt-4 text-sm text-surface-400">
            Every component is open-source: MIT / Apache 2.0 for the dependencies
            (Ollama, GLM-4.7-Flash, Aider), and AGPL-3.0-only for Yaver itself.
            You can self-host, modify, and redistribute the whole stack. If you
            offer Yaver as a network service to third parties, AGPL-3.0 requires
            you to publish your modifications &mdash; personal and internal team
            use have no such obligation.
          </p>
        </section>

        {/* Footer links */}
        <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-6">
          <h3 className="mb-2 text-sm font-semibold text-surface-200">
            Related guides
          </h3>
          <p className="text-sm text-surface-400">
            <Link
              href="/manuals/local-llm"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              Zero-cost local AI coding setup
            </Link>{" "}
            &mdash; more model options (Qwen, CodeLlama, DeepSeek) and
            Tailscale setup details.
            <br />
            <Link
              href="/manuals/cli-setup"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              CLI setup guide
            </Link>{" "}
            &mdash; all Yaver commands and configuration options.
            <br />
            <Link
              href="/manuals/relay-setup"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              Relay setup guide
            </Link>{" "}
            &mdash; self-host a relay server for access from anywhere.
            <br />
            <Link
              href="/manuals/auto-boot"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              Auto-boot guide
            </Link>{" "}
            &mdash; auto-start after power outages.
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
            href="/manuals/local-llm"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            Local LLM guide &rarr;
          </Link>
        </div>
      </div>
    </div>
  );
}
