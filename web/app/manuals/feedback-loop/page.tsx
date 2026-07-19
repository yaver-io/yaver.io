import Link from "next/link";

export default function FeedbackLoopManual() {
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
          Visual Feedback Loop &mdash; Bug Reports from Your Phone
        </h1>
        <p className="mb-12 text-sm leading-relaxed text-surface-400">
          Record what you see, narrate the bug, send it to your AI agent
          &mdash; and get a fix without typing a single line of code. Screen
          recordings, voice notes, screenshots, and device info flow P2P to
          your dev machine where the agent turns them into actionable tasks.
        </p>

        {/* Overview */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Overview
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            The feedback loop closes the gap between &quot;I found a
            bug&quot; and &quot;it&apos;s fixed.&quot; Instead of writing up
            a ticket, switching to a laptop, and reproducing the issue
            &mdash; just record it on your phone and let the AI agent handle
            the rest.
          </p>
          <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <div className="space-y-3 text-sm text-surface-400">
              <div className="flex items-center gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  1
                </span>
                <span>
                  <strong className="text-surface-300">Code</strong> &mdash;
                  AI agent writes or modifies code on your dev machine
                </span>
              </div>
              <div className="flex items-center gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  2
                </span>
                <span>
                  <strong className="text-surface-300">Build</strong> &mdash;
                  build runs remotely, artifact sent to your phone (P2P)
                </span>
              </div>
              <div className="flex items-center gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  3
                </span>
                <span>
                  <strong className="text-surface-300">Test</strong> &mdash;
                  you use the app on your phone, find something off
                </span>
              </div>
              <div className="flex items-center gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  4
                </span>
                <span>
                  <strong className="text-surface-300">Report</strong>{" "}
                  &mdash; record the bug (screen + voice), send to agent
                </span>
              </div>
              <div className="flex items-center gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  5
                </span>
                <span>
                  <strong className="text-surface-300">AI fixes</strong>{" "}
                  &mdash; agent analyzes recording, generates a fix, commits
                </span>
              </div>
              <div className="flex items-center gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  6
                </span>
                <span>
                  <strong className="text-surface-300">Rebuild</strong>{" "}
                  &mdash; new build sent to your phone, verify the fix
                </span>
              </div>
            </div>
          </div>
        </section>

        {/* Approach A: Record from Yaver App */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Approach A: Record from the Yaver app
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            The Yaver mobile app has built-in screen recording and voice
            annotation. No SDK integration required &mdash; works with any
            app on your phone.
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">
                yaver app
              </span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div className="text-surface-500">
                # 1. Open Yaver app, tap the feedback button
              </div>
              <div className="pl-2 text-surface-400">
                Recording started... speak to annotate.
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500">
                # 2. Use the app you&apos;re testing normally
              </div>
              <div className="pl-2 text-surface-400">
                &quot;The login button doesn&apos;t respond on this
                screen...&quot;
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500">
                # 3. Tap stop &mdash; bundle uploads to your agent via P2P
              </div>
              <div className="pl-2 text-green-400/80">
                Feedback sent. Task created: fix-login-button (#fb-k8m2x)
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            The recording, voice transcript, screenshots, and device info
            are bundled and sent to your agent over Yaver&apos;s P2P
            connection. Nothing touches third-party servers.
          </p>
        </section>

        {/* Approach B: Embed Feedback SDK */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Approach B: Embed the Feedback SDK
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Add the Yaver feedback SDK directly to your app under
            development. Shake your phone (or press a hotkey) to trigger a
            bug report &mdash; the SDK captures the screen, collects device
            info, and sends everything to your agent automatically.
          </p>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            React Native
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">
                  npm install yaver-feedback-react-native
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># In your App.tsx</div>
              <div className="pl-2 text-surface-300">
                {`import { YaverFeedback } from 'yaver-feedback-react-native';`}
              </div>
              <div className="pl-2 text-surface-400">
                {`// Wrap your app root`}
              </div>
              <div className="pl-2 text-surface-300">
                {`<YaverFeedback shakeToReport>`}
              </div>
              <div className="pl-2 text-surface-300">
                {`  <App />`}
              </div>
              <div className="pl-2 text-surface-300">
                {`</YaverFeedback>`}
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Flutter
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">
                  flutter pub add yaver_feedback
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># In your main.dart</div>
              <div className="pl-2 text-surface-300">
                {`YaverFeedback.init(shakeToReport: true);`}
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Web
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">
                  npm install @yaver/feedback-web
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># In your entry point</div>
              <div className="pl-2 text-surface-300">
                {`import { initFeedback } from '@yaver/feedback-web';`}
              </div>
              <div className="pl-2 text-surface-300">
                {`initFeedback({ hotkey: 'ctrl+shift+f' });`}
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            The SDK auto-discovers your dev machine on the local network
            using Yaver&apos;s LAN beacon. If you&apos;re on a different
            network, it falls back to the relay server automatically.
          </p>
        </section>

        {/* Three Feedback Modes */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Three feedback modes
          </h2>

          <div className="space-y-4">
            <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
              <h3 className="mb-2 text-sm font-semibold text-surface-200">
                Live mode
              </h3>
              <p className="text-sm text-surface-400">
                Your AI agent watches your screen in real time via a
                continuous stream. It uses a vision model to detect issues as
                they happen and can comment proactively &mdash; &quot;I see
                the layout is broken on this screen, want me to fix
                it?&quot; Best for iterating on UI with an agent that can see
                what you see.
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
              <h3 className="mb-2 text-sm font-semibold text-surface-200">
                Narrated mode
              </h3>
              <p className="text-sm text-surface-400">
                Record your screen and narrate as you go &mdash; &quot;this
                button should be blue, not red&quot; or &quot;the animation
                stutters here.&quot; When you stop recording, the bundle
                (video + transcript + screenshots + device info) is sent to
                the agent. Best for async bug reports when you want to
                capture context without waiting for the agent to respond.
              </p>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
              <h3 className="mb-2 text-sm font-semibold text-surface-200">
                Batch mode
              </h3>
              <p className="text-sm text-surface-400">
                Captures everything during a testing session &mdash; all
                screens visited, taps, scrolls, network requests, crashes
                &mdash; and dumps it as a structured timeline. No narration
                needed. The agent analyzes the full session to find issues
                you might have missed. Best for QA sessions where you want
                comprehensive coverage.
              </p>
            </div>
          </div>
        </section>

        {/* Agent Commentary Levels */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Agent commentary levels
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Control how much the agent talks back during live feedback
            sessions. Set the level from 0 (silent) to 10 (comments on
            everything).
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">
                  yaver feedback --level 5
                </span>
                <span className="ml-2 text-surface-500">
                  # Suggests fixes when issues detected
                </span>
              </div>
            </div>
          </div>
          <div className="overflow-hidden rounded-lg border border-surface-800">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 bg-surface-900/50">
                  <th className="px-4 py-2.5 text-left font-medium text-surface-300">
                    Level
                  </th>
                  <th className="px-4 py-2.5 text-left font-medium text-surface-300">
                    Behavior
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="px-4 py-2 font-mono text-surface-300">0</td>
                  <td className="px-4 py-2">
                    Silent &mdash; receives feedback, no comments
                  </td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="px-4 py-2 font-mono text-surface-300">
                    1&ndash;3
                  </td>
                  <td className="px-4 py-2">
                    Acknowledges feedback, confirms receipt
                  </td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="px-4 py-2 font-mono text-surface-300">
                    4&ndash;5
                  </td>
                  <td className="px-4 py-2">
                    Suggests fixes when issues are detected
                  </td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="px-4 py-2 font-mono text-surface-300">
                    6&ndash;8
                  </td>
                  <td className="px-4 py-2">
                    Comments on layout, performance, and UX patterns
                  </td>
                </tr>
                <tr>
                  <td className="px-4 py-2 font-mono text-surface-300">
                    9&ndash;10
                  </td>
                  <td className="px-4 py-2">
                    Proactive &mdash; detects bugs via vision model before
                    you mention them
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        {/* Voice-Driven Live Coding */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Voice-driven live coding
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            In live mode, speak naturally while testing and the agent makes
            changes in real time. Say &quot;make this button bigger&quot;
            while looking at the screen &mdash; the agent sees the same
            screen via the video stream, identifies the button, modifies the
            code, and hot reload pushes the change to your phone. No context
            switching. No typing.
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">
                live session
              </span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div className="text-surface-500">
                # Start live feedback with voice
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">
                  yaver feedback --mode live --level 5
                </span>
              </div>
              <div className="pl-2 text-surface-400">
                Live session started. Agent is watching your screen.
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="pl-2 text-blue-400/80">
                You: &quot;Make this button bigger and change the color to
                blue&quot;
              </div>
              <div className="pl-2 text-surface-400">
                Agent: Updating button styles in LoginScreen.tsx...
              </div>
              <div className="pl-2 text-green-400/80">
                Hot reload (412ms) &mdash; check your screen.
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="pl-2 text-blue-400/80">
                You: &quot;The spacing between these cards is too
                tight&quot;
              </div>
              <div className="pl-2 text-surface-400">
                Agent: Increasing gap from 8px to 16px in
                CardList.tsx...
              </div>
              <div className="pl-2 text-green-400/80">
                Hot reload (287ms) &mdash; check your screen.
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            Voice-driven coding works best with Flutter hot reload or React
            Native fast refresh. The agent correlates your speech with
            the current screen frame to understand exactly what you&apos;re
            referring to.
          </p>
        </section>

        {/* Device Discovery */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Device discovery
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            The feedback SDK and the Yaver app both use the same discovery
            mechanism to find your dev machine. On the same Wi-Fi network,
            discovery is instant via LAN beacon. On different networks, the
            relay server handles the connection.
          </p>
          <ul className="space-y-2 text-sm text-surface-400">
            <li className="flex gap-3">
              <span className="text-surface-500">&#8226;</span>
              <span>
                <strong className="text-surface-300">Same network</strong>{" "}
                &mdash; LAN beacon auto-discovers your dev machine in under
                3 seconds. Zero config.
              </span>
            </li>
            <li className="flex gap-3">
              <span className="text-surface-500">&#8226;</span>
              <span>
                <strong className="text-surface-300">
                  Different network
                </strong>{" "}
                &mdash; feedback routes through the relay server
                automatically. Works from 5G, hotel Wi-Fi, anywhere.
              </span>
            </li>
          </ul>
        </section>

        {/* CLI Commands */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            CLI commands
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Manage feedback reports from the terminal. Every report sent from
            the app or SDK is stored locally on your dev machine and
            accessible via the CLI.
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-3 text-[13px]">
              <div className="text-surface-500"># List all feedback reports</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">yaver feedback list</span>
              </div>
              <div className="pl-2 text-surface-400">
                {`  #fb-k8m2x  narrated  "Login button not responding"    2m ago\n  #fb-a3j9p  live      "Card layout spacing"            18m ago\n  #fb-r7w1z  batch     "Full QA session"                1h ago`}
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500">
                # View details of a specific report
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">
                  yaver feedback show fb-k8m2x
                </span>
              </div>
              <div className="pl-2 text-surface-400">
                {`  Mode:        narrated\n  Duration:    47s\n  Device:      iPhone 15 Pro / iOS 18.2\n  Video:       recording.mp4 (12.4 MB)\n  Screenshots: 3\n  Timeline:\n    0:05  voice  "Opening the login screen"\n    0:12  voice  "Tapping the login button — nothing happens"\n    0:18  screenshot  login_screen.jpg\n    0:31  voice  "Tried again, still no response"`}
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500">
                # Create an AI task from a feedback report
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200">
                  yaver feedback fix fb-k8m2x
                </span>
              </div>
              <div className="pl-2 text-surface-400">
                Analyzing feedback... extracting bug description from video
                + transcript.
              </div>
              <div className="pl-2 text-green-400/80">
                Task created: &quot;Fix login button tap handler on
                LoginScreen&quot; (#task-m4x2)
              </div>
              <div className="pl-2 text-surface-400">
                Agent is working on the fix...
              </div>
            </div>
          </div>
        </section>

        {/* How the AI Agent Processes Feedback */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            How the AI agent processes feedback
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            When a feedback bundle arrives at your agent, it goes through a
            multi-step analysis pipeline to turn raw recordings into
            actionable code changes.
          </p>
          <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <div className="space-y-4 text-sm text-surface-400">
              <div className="flex gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  1
                </span>
                <div>
                  <strong className="text-surface-300">
                    Receive bundle
                  </strong>
                  <p className="mt-1">
                    Agent receives the multipart upload: screen recording
                    (MP4), voice transcript, annotated screenshots, device
                    info (platform, model, OS version), and a timestamped
                    event timeline.
                  </p>
                </div>
              </div>
              <div className="flex gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  2
                </span>
                <div>
                  <strong className="text-surface-300">
                    Extract key frames
                  </strong>
                  <p className="mt-1">
                    The video is sampled at key moments (voice annotations,
                    tap events, screen transitions) to produce a set of
                    representative screenshots with timestamps.
                  </p>
                </div>
              </div>
              <div className="flex gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  3
                </span>
                <div>
                  <strong className="text-surface-300">
                    Generate fix prompt
                  </strong>
                  <p className="mt-1">
                    The agent constructs a detailed prompt combining the
                    transcript, screenshots, device context, and the
                    relevant source files. The vision model maps UI elements
                    in screenshots to components in your codebase.
                  </p>
                </div>
              </div>
              <div className="flex gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  4
                </span>
                <div>
                  <strong className="text-surface-300">Create task</strong>
                  <p className="mt-1">
                    A new task is created with the generated prompt. The AI
                    coding agent (Claude Code, Aider, Codex, etc.) receives
                    the task and begins working on the fix.
                  </p>
                </div>
              </div>
              <div className="flex gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-200">
                  5
                </span>
                <div>
                  <strong className="text-surface-300">
                    Rebuild and verify
                  </strong>
                  <p className="mt-1">
                    Once the fix is committed, the agent triggers a rebuild.
                    The new artifact is sent to your phone so you can verify
                    the fix immediately.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </section>

        <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-6">
          <h3 className="mb-2 text-sm font-semibold text-surface-200">
            Related guides
          </h3>
          <p className="text-sm text-surface-400">
            See the{" "}
            <Link
              href="/manuals/code-from-beach"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              Build, Test &amp; Deploy guide
            </Link>{" "}
            for remote builds and P2P artifact delivery, or the{" "}
            <Link
              href="/manuals/cli-setup"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              CLI setup guide
            </Link>{" "}
            to get started with the Yaver agent.
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
            href="/manuals/code-from-beach"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            Build, Test &amp; Deploy &rarr;
          </Link>
        </div>
      </div>
    </div>
  );
}
