import Link from "next/link";

export default function VoiceAIManual() {
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
          Local voice AI with PersonaPlex
        </h1>
        <p className="mb-12 text-sm leading-relaxed text-surface-400">
          Run NVIDIA PersonaPlex 7B locally on Apple Silicon for real-time
          speech-to-speech conversations &mdash; full-duplex, no cloud, no API
          keys. Pair it with Ollama + Yaver for a completely free, private voice
          + coding AI stack.
        </p>

        {/* What you get */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            What you get
          </h2>
          <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <ul className="space-y-2 text-sm text-surface-400">
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>
                  <strong className="text-surface-300">
                    PersonaPlex 7B (MLX)
                  </strong>{" "}
                  &mdash; NVIDIA&apos;s speech-to-speech model, ported to Apple
                  Silicon via MLX with 4-bit quantization (~5.3 GB)
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>
                  <strong className="text-surface-300">Full-duplex</strong>{" "}
                  &mdash; listens and speaks simultaneously, supports
                  interruptions, barge-ins, and natural turn-taking
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>
                  <strong className="text-surface-300">18 voice presets</strong>{" "}
                  &mdash; male and female voices with different styles
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>
                  <strong className="text-surface-300">
                    Role-based personas
                  </strong>{" "}
                  &mdash; text prompts control personality, knowledge, and
                  behavior
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>
                  <strong className="text-surface-300">Runs on any Mac</strong>{" "}
                  &mdash; M1/M2/M3/M4, 16 GB+ RAM recommended
                </span>
              </li>
            </ul>
          </div>
        </section>

        {/* Hardware requirements */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Hardware requirements
          </h2>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-6 font-medium text-surface-300">
                    Setup
                  </th>
                  <th className="pb-3 pr-6 font-medium text-surface-300">
                    RAM
                  </th>
                  <th className="pb-3 font-medium text-surface-300">Notes</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">
                    PersonaPlex only
                  </td>
                  <td className="py-3 pr-6">16 GB+</td>
                  <td className="py-3">~5.3 GB model + audio buffers</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">
                    PersonaPlex + Ollama 7B
                  </td>
                  <td className="py-3 pr-6">16 GB+</td>
                  <td className="py-3">Both fit comfortably</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">
                    PersonaPlex + Ollama 14B
                  </td>
                  <td className="py-3 pr-6">24 GB+</td>
                  <td className="py-3">~14.3 GB total, good headroom</td>
                </tr>
                <tr>
                  <td className="py-3 pr-6 text-surface-300">
                    PersonaPlex + Ollama 32B
                  </td>
                  <td className="py-3 pr-6">64 GB+</td>
                  <td className="py-3">
                    ~25 GB total, tight on 32 GB machines
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p className="mt-4 text-sm text-surface-400">
            For NVIDIA GPUs (A100, H100, RTX 4000+), use the{" "}
            <a
              href="https://github.com/NVIDIA/personaplex"
              target="_blank"
              rel="noopener noreferrer"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              official NVIDIA repo
            </a>{" "}
            with CUDA instead.
          </p>
        </section>

        {/* Prerequisites */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            1. Prerequisites
          </h2>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Python 3.12
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            PersonaPlex MLX requires Python &ge;3.10, &lt;3.13. Python 3.12 is
            recommended.
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
                <span className="text-surface-200 select-all">
                  brew install python@3.12
                </span>
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Hugging Face account
          </h3>
          <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <ul className="space-y-2 text-sm text-surface-400">
              <li className="flex gap-3">
                <span className="text-surface-300 font-medium">1.</span>
                <span>
                  Accept the model license at{" "}
                  <a
                    href="https://huggingface.co/nvidia/personaplex-7b-v1"
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
                  >
                    huggingface.co/nvidia/personaplex-7b-v1
                  </a>
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-300 font-medium">2.</span>
                <span>
                  Create an access token at{" "}
                  <a
                    href="https://huggingface.co/settings/tokens"
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
                  >
                    huggingface.co/settings/tokens
                  </a>
                </span>
              </li>
            </ul>
          </div>
        </section>

        {/* Install PersonaPlex */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            2. Install PersonaPlex MLX
          </h2>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500">
                # Clone the MLX port
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  git clone https://github.com/mu-hashmi/personaplex-mlx.git
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd personaplex-mlx
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500">
                # Create a virtual environment with Python 3.12
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  python3.12 -m venv .venv
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  source .venv/bin/activate
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Install</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  pip install -e .
                </span>
              </div>
            </div>
          </div>
        </section>

        {/* Set HF token */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            3. Set your Hugging Face token
          </h2>
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
                  export HF_TOKEN=hf_your_token_here
                </span>
              </div>
            </div>
          </div>
          <p className="text-sm text-surface-400">
            The model weights (~5.3 GB, 4-bit quantized) are downloaded
            automatically on first run. The original 16.7 GB PyTorch checkpoint
            is converted to MLX safetensors format.
          </p>
        </section>

        {/* Run it */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            4. Run PersonaPlex
          </h2>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Option A: Web mode (recommended first)
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            Launches a web UI at{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
              http://localhost:8998
            </code>{" "}
            with a mic button. Use headphones to avoid echo feedback.
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
                <span className="text-surface-200 select-all">
                  python -m personaplex_mlx.local_web -q 4 --voice NATF2
                  --text-prompt &quot;You enjoy having a good
                  conversation.&quot;
                </span>
              </div>
              <div className="pl-2 text-green-400/80">
                Server running at http://localhost:8998
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Option B: Terminal mode
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            Direct microphone input from your terminal. Requires headphones.
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
                <span className="text-surface-200 select-all">
                  python -m personaplex_mlx.local -q 4 --voice NATF2
                  --text-prompt &quot;You enjoy having a good
                  conversation.&quot;
                </span>
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Option C: Offline (WAV-to-WAV)
          </h3>
          <p className="mb-3 text-sm text-surface-400">
            Process a pre-recorded audio file. Useful for testing without a
            microphone.
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
                <span className="text-surface-200 select-all">
                  python -m personaplex_mlx.offline --voice NATF2
                  --text-prompt &quot;You are a wise and friendly
                  teacher.&quot; --input-wav input.wav --output-wav output.wav
                  --output-text output.json --seed 42424242
                </span>
              </div>
            </div>
          </div>
        </section>

        {/* Voice presets */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            5. Voice presets
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            PersonaPlex ships with 18 built-in voices. Use the{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
              --voice
            </code>{" "}
            flag to select one.
          </p>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-6 font-medium text-surface-300">
                    Category
                  </th>
                  <th className="pb-3 font-medium text-surface-300">
                    Voice IDs
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">
                    Natural female
                  </td>
                  <td className="py-3">
                    <code className="text-surface-300">NATF0</code>{" "}
                    <code className="text-surface-300">NATF1</code>{" "}
                    <code className="text-surface-300">NATF2</code>{" "}
                    <code className="text-surface-300">NATF3</code>
                  </td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">Natural male</td>
                  <td className="py-3">
                    <code className="text-surface-300">NATM0</code>{" "}
                    <code className="text-surface-300">NATM1</code>{" "}
                    <code className="text-surface-300">NATM2</code>{" "}
                    <code className="text-surface-300">NATM3</code>
                  </td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">
                    Varied female
                  </td>
                  <td className="py-3">
                    <code className="text-surface-300">VARF0</code>{" "}
                    <code className="text-surface-300">VARF1</code>{" "}
                    <code className="text-surface-300">VARF2</code>{" "}
                    <code className="text-surface-300">VARF3</code>{" "}
                    <code className="text-surface-300">VARF4</code>
                  </td>
                </tr>
                <tr>
                  <td className="py-3 pr-6 text-surface-300">Varied male</td>
                  <td className="py-3">
                    <code className="text-surface-300">VARM0</code>{" "}
                    <code className="text-surface-300">VARM1</code>{" "}
                    <code className="text-surface-300">VARM2</code>{" "}
                    <code className="text-surface-300">VARM3</code>{" "}
                    <code className="text-surface-300">VARM4</code>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        {/* Pair with Ollama + Yaver */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            6. Pair with Ollama + Yaver
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Run PersonaPlex alongside Ollama and Yaver for a fully local voice +
            coding AI stack. Both models fit in 24 GB of unified memory on Apple
            Silicon.
          </p>

          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500">
                # Install Ollama + a coding model
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  brew install ollama
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  ollama pull qwen2.5-coder:14b
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Start Yaver agent</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver serve
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500">
                # In another terminal, start PersonaPlex
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd personaplex-mlx &amp;&amp; source .venv/bin/activate
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  python -m personaplex_mlx.local_web -q 4 --voice NATF2
                  --text-prompt &quot;You are a helpful coding
                  assistant.&quot;
                </span>
              </div>
            </div>
          </div>

          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-6 font-medium text-surface-300">
                    Component
                  </th>
                  <th className="pb-3 pr-6 font-medium text-surface-300">
                    RAM
                  </th>
                  <th className="pb-3 font-medium text-surface-300">Cost</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">
                    PersonaPlex 7B (4-bit)
                  </td>
                  <td className="py-3 pr-6">~5.3 GB</td>
                  <td className="py-3">Free</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">
                    Qwen 2.5 Coder 14B
                  </td>
                  <td className="py-3 pr-6">~9 GB</td>
                  <td className="py-3">Free</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">
                    Yaver CLI
                  </td>
                  <td className="py-3 pr-6">~20 MB</td>
                  <td className="py-3">Free</td>
                </tr>
                <tr>
                  <td className="py-3 pr-6 font-semibold text-surface-100">
                    Total
                  </td>
                  <td className="py-3 pr-6 font-semibold text-surface-100">
                    ~14.3 GB
                  </td>
                  <td className="py-3 font-semibold text-green-400/80">
                    $0/month
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        {/* NVIDIA GPU alternative */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            7. NVIDIA GPU setup (alternative)
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            If you have an NVIDIA GPU with 20+ GB VRAM (A100, H100, RTX 4000+),
            use the official repo for best performance:
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
                <span className="text-surface-200 select-all">
                  git clone https://github.com/NVIDIA/personaplex.git
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd personaplex &amp;&amp; pip install -e .
                </span>
              </div>
            </div>
          </div>
          <p className="text-sm text-surface-400">
            See the{" "}
            <a
              href="https://github.com/NVIDIA/personaplex"
              target="_blank"
              rel="noopener noreferrer"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              NVIDIA PersonaPlex repo
            </a>{" "}
            and{" "}
            <a
              href="https://huggingface.co/nvidia/personaplex-7b-v1"
              target="_blank"
              rel="noopener noreferrer"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              model card
            </a>{" "}
            for full CUDA setup instructions.
          </p>
        </section>

        {/* Troubleshooting */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Troubleshooting
          </h2>
          <div className="space-y-4">
            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <h3 className="mb-2 text-sm font-medium text-surface-200">
                &quot;requires Python &ge;3.10, &lt;3.13&quot;
              </h3>
              <p className="text-sm text-surface-400">
                Install Python 3.12 via{" "}
                <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                  brew install python@3.12
                </code>{" "}
                and create the venv with{" "}
                <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                  python3.12 -m venv .venv
                </code>
                . Python 3.13+ is not yet supported.
              </p>
            </div>
            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <h3 className="mb-2 text-sm font-medium text-surface-200">
                Echo / audio feedback loop
              </h3>
              <p className="text-sm text-surface-400">
                The MLX client does not include echo cancellation. Use
                headphones to prevent the model from hearing its own output.
              </p>
            </div>
            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <h3 className="mb-2 text-sm font-medium text-surface-200">
                &quot;Access denied&quot; downloading model
              </h3>
              <p className="text-sm text-surface-400">
                Make sure you&apos;ve accepted the license at{" "}
                <a
                  href="https://huggingface.co/nvidia/personaplex-7b-v1"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
                >
                  huggingface.co/nvidia/personaplex-7b-v1
                </a>{" "}
                and set{" "}
                <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">
                  export HF_TOKEN=your_token
                </code>
                .
              </p>
            </div>
            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <h3 className="mb-2 text-sm font-medium text-surface-200">
                Slow first run
              </h3>
              <p className="text-sm text-surface-400">
                The first run downloads model assets from Hugging Face (~5.3 GB).
                Subsequent runs use the cached weights and start much faster.
              </p>
            </div>
          </div>
        </section>

        {/* Links */}
        <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-6">
          <h3 className="mb-2 text-sm font-semibold text-surface-200">
            Resources
          </h3>
          <ul className="space-y-2 text-sm text-surface-400">
            <li>
              <a
                href="https://huggingface.co/nvidia/personaplex-7b-v1"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
              >
                PersonaPlex model card
              </a>{" "}
              &mdash; NVIDIA&apos;s official model page
            </li>
            <li>
              <a
                href="https://huggingface.co/aufklarer/PersonaPlex-7B-MLX-4bit"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
              >
                PersonaPlex-7B-MLX-4bit
              </a>{" "}
              &mdash; 4-bit quantized MLX weights
            </li>
            <li>
              <a
                href="https://github.com/mu-hashmi/personaplex-mlx"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
              >
                personaplex-mlx
              </a>{" "}
              &mdash; MLX port source code
            </li>
            <li>
              <a
                href="https://github.com/NVIDIA/personaplex"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
              >
                NVIDIA/personaplex
              </a>{" "}
              &mdash; official CUDA implementation
            </li>
          </ul>
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
