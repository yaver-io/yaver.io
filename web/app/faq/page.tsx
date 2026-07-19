"use client";

import Link from "next/link";
import { useState } from "react";

const faqs = [
  {
    category: "Getting Started",
    items: [
      {
        q: "Who is Yaver for?",
        a: "An individual developer running Yaver on their own machine. Your laptop, your Mac mini, your Linux box, your VPS — paired with your own phone as a remote control. Sharing the machine with a trusted teammate (for example, to let them hit your local Ollama + Qwen cluster) is supported as a secondary capability, but Yaver is not a hosted multi-tenant service and should not be treated as one.",
      },
      {
        q: "What AI agents does Yaver work with?",
        a: "Anything that runs in a terminal. Claude Code, Codex CLI, OpenCode, Goose, Amp, Aider, Ollama, Qwen, Continue, or any custom command. Run local models with Ollama for zero-cost, fully private AI coding. Switch agents per task or set a default with `yaver set-runner <name>`.",
      },
      {
        q: "Do I need API keys?",
        a: "Self-hosted Yaver does not need Yaver API keys. Your chosen coding agent may need its own login, subscription, or API key. Optional Yaver Cloud is web-billed managed infrastructure for a saved workspace and private relay; the mobile app controls existing machines and does not sell cloud access inside the app.",
      },
      {
        q: "Don't some agents already have remote access?",
        a: "Yes — Claude Code has a remote control feature (code.claude.com), and OpenAI Codex runs in the cloud. Yaver is useful when you want a single interface across multiple agents, when you use local models that have no cloud option, or when you want full control over your infrastructure.",
      },
      {
        q: "Does Yaver auto-start when my PC boots?",
        a: "Yes. During installation, Yaver registers itself as a system service. On macOS it uses a LaunchAgent, on Linux a systemd user service, and on Windows a startup entry. After a reboot, `yaver serve` starts automatically. You can disable this with `yaver config set auto-start false`.",
      },
      {
        q: "Do I need to re-authenticate after a reboot?",
        a: "No. Once you run `yaver auth` the first time, your session is saved locally. It persists across reboots indefinitely.",
      },
    ],
  },
  {
    category: "Networking",
    items: [
      {
        q: "Do I need a relay server?",
        a: "Only if your phone and dev machine aren't on the same network. On the same WiFi, Yaver finds your machine automatically via LAN broadcast. For remote access, start with Yaver's free shared relay. Relay Pro gives you a private managed relay and higher limits.",
      },
      {
        q: "Can I use Yaver with a VPN?",
        a: "Yes. Yaver operates at the application layer, so it can coexist with an existing private network. You do not need to set one up for normal Yaver use.",
      },
      {
        q: "What happens if my connection fails?",
        a: "Yaver tries direct connection first, then falls back to relay servers in priority order. If a relay goes down, traffic routes through remaining relays. The CLI reconnects with exponential backoff (up to 30s). Network changes (WiFi to cellular) trigger an automatic reconnect — no manual intervention.",
      },
    ],
  },
  {
    category: "Relay",
    items: [
      {
        q: "Which relay should I use?",
        a: "Start with Free Relay for light personal use. Upgrade to Relay Pro when Yaver becomes part of daily work and you need private managed capacity with higher limits.",
      },
      {
        q: "Can I run everything locally with no cloud?",
        a: "Yes for local development: run the agent on your own machine and use local models through Ollama if you want. Yaver's hosted coordination plane handles sign-in, discovery, billing, and relay presence for the public app; your source and runner sessions stay on the machine doing the work.",
      },
    ],
  },
  {
    category: "Privacy & Security",
    items: [
      {
        q: "Is my code safe?",
        a: "Yaver connects your phone to your dev machine over the best available Yaver transport. CLI-to-relay uses QUIC (TLS encrypted), mobile-to-relay uses HTTPS, and the relay is password-protected and forwards bytes without inspecting them. On LAN, the beacon uses a SHA-256 token fingerprint so only your devices can discover each other. No code, tasks, or output are stored on Yaver servers. All of this is open source — read the code yourself.",
      },
      {
        q: "What is the privacy model?",
        a: "Zero-knowledge. All code, prompts, and outputs flow P2P between your devices. The backend only handles OAuth sign-in and device discovery — it never sees your data. The website is just for registration and account management, not a control plane. Even if the auth backend were compromised, your code would be safe because it never passes through it.",
      },
      {
        q: "How does authentication work?",
        a: "You sign in via OAuth (Apple, Google, or Microsoft). Both the CLI and mobile app receive a session token from Convex. This token authenticates all API requests and device registration. The relay server has a separate shared password that prevents unauthorized agents from connecting. On LAN, the UDP beacon includes a fingerprint derived from your user ID (first 8 hex chars of SHA-256), so only devices signed in to the same account will discover each other.",
      },
      {
        q: "What encryption is used?",
        a: "It depends on the connection path. CLI-to-relay uses QUIC with TLS. Mobile-to-relay uses HTTPS with TLS certificates. Direct LAN uses HTTP on your local network, where traffic stays on your WiFi. The relay is a pass-through transport.",
      },
      {
        q: "Where are my relay credentials stored?",
        a: "You choose. By default, relay server URL and password are stored locally on each device (AsyncStorage on mobile, config.json on CLI). You can optionally enable cloud sync to store them in your Convex account so they sync across devices. The web dashboard always stores to your account. If privacy is a concern, use local-only storage and configure each device separately.",
      },
    ],
  },
  {
    category: "CLI & Usage",
    items: [
      {
        q: "Does the CLI auto-update?",
        a: "Optionally. Enable with `yaver config set auto-update true`. Otherwise update manually with `npm install -g yaver-cli@latest`.",
      },
      {
        q: "Can I use Yaver without the mobile app?",
        a: "Yes. Run `yaver connect` from any terminal to connect to your remote dev machine. Laptop to desktop, server to server, SSH session to home machine — same connection strategy, same agent support. The mobile app is just one way to interact with your agent.",
      },
      {
        q: "What is the website for?",
        a: "The yaver.io website is only for initial registration and basic account management — signing in via OAuth, viewing your registered devices, and managing your account. It is not a control plane. All actual interaction with your AI agents happens from the CLI (`yaver serve`, `yaver connect`) and the mobile app.",
      },
      {
        q: "Can I run multiple agents per machine?",
        a: "Yes. Each `yaver serve` instance manages its own tmux sessions. You can run different AI agents side by side and switch between them from the mobile app.",
      },
      {
        q: "Can I use Yaver on a headless server?",
        a: "Yes. Three auth paths for headless machines: (1) `yaver auth --headless` runs the OAuth device-code flow — prints a QR code, you scan it with your phone camera, sign in with Apple/Google/Microsoft, and the headless machine polls and receives the token. (2) `yaver auth pair` starts a P2P pairing window, prints a short code + QR, and waits for another signed-in machine to forward its token over the relay — no second OAuth dance. On the source machine run `yaver auth send <CODE> <URL>`. (3) `yaver auth --token <token>` if you already have a token from another device. Combined with auto-boot, a Mac mini or Linux server becomes a persistent AI dev machine you control from your phone — no browser ever runs on the headless box.",
      },
      {
        q: "My Mac mini is upstairs and I'm on my laptop — how do I sign in without clicking through Apple OAuth on the Mac mini?",
        a: "SSH into the Mac mini and run `yaver auth pair`. It prints a QR code + 6-character code. On your laptop (already signed in), run `yaver auth send <CODE> <url-from-the-QR>` or open the Yaver mobile app → More → Pair device → scan the QR. Your laptop's token flows over the existing P2P relay to the Mac mini in one shot. No second OAuth required. Works identically for Apple, Google, and Microsoft sign-in because the token the source is forwarding is already session-level, not provider-level.",
      },
    ],
  },
  {
    category: "Build, Test & Deploy",
    items: [
      {
        q: "Can I build mobile apps from my phone?",
        a: "Yes. `yaver build flutter apk`, `yaver build gradle apk`, `yaver build xcode ipa`, `yaver build rn android` — all run on your dev machine. The artifact (APK, IPA, AAB) transfers P2P to your phone. On Android, tap to install directly. On iOS, use TestFlight or OTA install via relay. Flutter, React Native, Expo, native Android/iOS, or any custom build command.",
      },
      {
        q: "How does the React Native developer preview compare to a Flutter or fully native preview?",
        a: "React Native is the deepest path: Yaver compiles your project's JavaScript to Hermes bytecode and previews it in the Yaver developer-preview container — same shape Expo Go and expo-dev-client use, scoped to your own paired devices. Flutter and fully native apps (Swift/Kotlin) preview through their normal Debug build paths driven from your machine; on the same LAN Yaver can trigger those builds on your dev machine and deliver them to your phone via the standard Xcode / adb install path.",
      },
      {
        q: "How does hot reload work remotely?",
        a: "`yaver debug flutter` starts Flutter's debug server on your dev machine and creates a P2P tunnel. Your phone connects to localhost:9100 through the tunnel — Flutter's hot reload works exactly as if you were sitting at your desk. Same for React Native (Metro on :8081). Latency is ~50ms through relay, ~5ms on LAN.",
      },
      {
        q: "Can I run tests from my phone?",
        a: "`yaver test unit` auto-detects your test framework and runs it. Supported: Flutter, Jest, Vitest, pytest, Go test, Cargo test, XCTest, Espresso, Playwright, Cypress, Maestro. For Android tests, Yaver auto-boots an emulator if none is running. For iOS, it boots a simulator. Results stream to your phone with pass/fail counts.",
      },
      {
        q: "How does the full pipeline work?",
        a: "`yaver pipeline --test --deploy p2p` does everything: auto-detects your platform, builds the artifact, runs tests, and if they pass, makes it available for P2P download to your phone. One command. If tests fail, the pipeline stops. You can also deploy to TestFlight (`--deploy testflight`), Play Store (`--deploy playstore`), or trigger GitHub Actions (`--deploy github`).",
      },
      {
        q: "What about TestFlight and Play Store?",
        a: "`yaver build push testflight` uploads your IPA directly. `yaver build push playstore` uploads your AAB to the internal testing track. Credentials (App Store Connect API key, Google Play service account) are stored in the P2P encrypted vault — never on our servers. You can also use `yaver deploy --ci github` to trigger GitHub Actions, or `--ci gitlab` for GitLab CI.",
      },
      {
        q: "Why not just use GitHub Actions or GitLab CI?",
        a: "You can — Yaver supports both. But P2P builds are free and instant. GitHub Actions gives you 2,000 free minutes/month then charges $0.008/min. GitLab CI gives 400 free minutes then charges. Yaver P2P: unlimited, free, forever. Your dev machine does the work. No cloud compute needed. Use CI when you want it, skip it when you don't.",
      },
      {
        q: "Does Yaver support Expo?",
        a: "Yes. `yaver build expo-android` and `yaver build expo-ios` for Expo managed workflow projects. For bare workflow, use the standard Flutter/RN/Gradle/Xcode build commands.",
      },
      {
        q: "What is repo switching?",
        a: "`yaver repo list` shows all git repos discovered on your machine. `yaver repo switch my-app` changes the agent's working directory to that project. Auto-discovers repos under ~/. No manual path typing, no GitHub/GitLab integration needed. Works with any git repo.",
      },
    ],
  },
  {
    category: "Feedback & Visual Bug Reports",
    items: [
      {
        q: "What is the visual feedback loop?",
        a: "After deploying a build to your phone, you test it and find bugs. Record your screen and narrate what you see — 'this button is broken', 'the layout overlaps'. Send the report to your AI agent via P2P. The agent sees the screen recording, reads your voice transcript, sees the screenshots and timeline, and fixes the bugs. Rebuild. Repeat. Like pair programming, but the AI watches over your shoulder.",
      },
      {
        q: "What are the three feedback modes?",
        a: "Full Interactive (Live): your screen and voice stream to the agent in real-time. The agent's vision model detects bugs proactively and hot-reloads fixes as you speak. Say 'make this bigger' and it happens. Semi Interactive (Narrated): screen and voice stream live, agent comments on what it sees, but doesn't auto-fix. Say 'fix it now' for specific issues or 'keep in mind for later'. Post Mode (Batch): record everything offline. No streaming. Compress and upload when done. Agent processes the full session afterwards. Best for slow connections or detailed QA.",
      },
      {
        q: "What are agent commentary levels?",
        a: "A scale from 0-10 controlling how proactive the agent is. Level 0: silent, only responds when asked. Level 3: notes crashes and critical errors. Level 5: comments on obvious UI issues. Level 7: comments on layout, performance, accessibility. Level 10: comments on everything it sees. Like adjusting how chatty your pair programmer is.",
      },
      {
        q: "Can I give voice commands while testing?",
        a: "Yes. In Full Interactive mode, say things like 'make this button bigger', 'fix this layout', 'run the tests', or 'push to TestFlight'. The agent hears your voice (via on-device speech-to-text), creates a task, and executes it. Hot reload pushes the changes to your phone in seconds.",
      },
      {
        q: "How does the agent see my screen?",
        a: "On iOS, ReplayKit captures the screen (system permission required once). On Android, MediaProjection API captures the screen. On web, the browser's getDisplayMedia API is used. The video is compressed (H.264/VP9, ~2-5 MB per minute at 720p) before transfer. In live mode, frames stream in real-time. In batch mode, the full recording is compressed and uploaded at the end.",
      },
      {
        q: "Is my screen recording data safe?",
        a: "Yes. Screen recordings, voice, and screenshots transfer directly to your dev machine via P2P (same encrypted channel as everything else in Yaver). Nothing passes through our servers. The relay is a pass-through that can't read the data. Recordings are stored on your dev machine under ~/.yaver/feedback/ and you can delete them anytime with `yaver feedback delete <id>`.",
      },
      {
        q: "What does `yaver feedback fix` do?",
        a: "It takes a feedback report (screen recording, voice transcript, screenshots, timeline) and generates a structured prompt for the AI agent. The prompt includes: device info, timeline of events with timestamps, voice transcript, screenshot references, and crash logs. The agent reads this and creates a fix. No manual bug report writing — your recording becomes the bug report.",
      },
    ],
  },
  {
    category: "Feedback SDKs",
    items: [
      {
        q: "What are the feedback SDKs?",
        a: "Open-source libraries you embed in your app during development. Available for React Native (yaver-feedback-react-native), Flutter (yaver_feedback on pub.dev), and Web (@yaver/feedback-web). They add shake-to-report, screen recording, voice annotation, and P2P upload to your app — all disabled automatically in production builds.",
      },
      {
        q: "Do I need the Yaver mobile app if I use the SDK?",
        a: "No. The feedback SDK is self-contained. It includes device discovery (auto-finds your Yaver agent on the LAN), a connection UI, screen recording, voice annotation, and P2P upload. Your app connects directly to your dev machine without the Yaver mobile app as a middleman. The SDK is a mini Yaver client embedded in your app.",
      },
      {
        q: "How does device discovery work in the SDK?",
        a: "The SDK automatically scans your local network for Yaver agents (common IPs on 192.168.x.x, 10.0.x.x, port 18080). It checks the /health endpoint with a 2-second timeout. Once found, the connection is cached. You can also enter the URL manually. Works on WiFi. For remote connections, use a relay URL.",
      },
      {
        q: "How do I install the feedback SDK?",
        a: "Web: `npm install @yaver/feedback-web` then `YaverFeedback.init({ trigger: 'floating-button' })`. React Native: `npm install yaver-feedback-react-native` then `YaverFeedback.init({ trigger: 'shake' })`. Flutter: add `yaver_feedback` to pubspec.yaml then `YaverFeedback.init(FeedbackConfig(trigger: FeedbackTrigger.shake))`. All SDKs auto-discover your dev machine.",
      },
      {
        q: "Is the SDK safe for production?",
        a: "The SDK auto-disables in production. On React Native, it checks `__DEV__`. On web, it checks `process.env.NODE_ENV`. On Flutter, it checks `kDebugMode`. You can also explicitly set `enabled: false`. When disabled, the SDK has zero runtime cost — no network requests, no UI, no screen capture. The floating button and shake detector don't activate.",
      },
      {
        q: "What triggers are available?",
        a: "React Native: shake phone, floating button, or manual (`YaverFeedback.startReport()`). Flutter: same three options. Web: floating button (corner of screen), keyboard shortcut (Ctrl+Shift+F by default), or manual. You can also use the ConnectionScreen/ConnectionWidget component in your dev settings page for full control.",
      },
      {
        q: "Can I develop my own app using the feedback SDK?",
        a: "That's exactly what it's for. Install the SDK in your app, run your dev build on your phone, and use the feedback loop to iterate: code → build → test on device → record bugs → AI fixes → hot reload. The SDK connects to your Yaver agent running on your dev machine. Yaver itself uses its own feedback SDK for development (dogfooding).",
      },
      {
        q: "What data does the SDK capture?",
        a: "Screen recording (ReplayKit on iOS, MediaProjection on Android, getDisplayMedia on web), microphone audio for voice narration, screenshots with annotations, touch/click events, console errors (web), device info (platform, model, OS version, screen size), and a timestamped timeline of all events. Everything is compressed before upload.",
      },
      {
        q: "Can I use the SDK for web development too?",
        a: "Yes. @yaver/feedback-web works with any web framework (React, Vue, Svelte, vanilla JS). Screen recording uses the browser's getDisplayMedia API. Hot reload works natively through your dev server (webpack, Vite, etc.) — Yaver tunnels the dev server port to your phone's browser. Say 'fix this' while looking at your web app and the AI pushes a fix via HMR.",
      },
    ],
  },
  {
    category: "Sharing",
    items: [
      {
        q: "Can I share my own machine with teammates?",
        a: "Yes. `yaver guests invite <email>` grants scoped P2P access to a teammate (max 5, invitations expire in 2 days, no shell / vault / session access). `yaver serve --multi-user --team <id>` gives each team member an isolated workspace on the same box. Useful for pointing a teammate's laptop or phone at your home workstation — for example, at a local model runner you host, or a dev build you're iterating on.",
      },
      {
        q: "Anything I should know before sharing?",
        a: "If the way you use shared access could touch a third party's rules — App Store / Play Store policies, SSO provider TOS, model or dataset licenses, an employer or client agreement, export controls, anything like that — checking compliance is your responsibility. Yaver publishes source code and SDKs; it does not audit how you use them or guarantee that any particular pattern is approved by any particular provider.",
      },
    ],
  },
];

export default function FAQPage() {
  const [openFaq, setOpenFaq] = useState<string | null>(null);

  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <div className="mb-16 text-center">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            FAQ
          </h1>
          <p className="text-sm text-surface-500">
            Common questions about Yaver.
          </p>
        </div>

        <div className="space-y-10">
          {faqs.map((section) => (
            <div key={section.category}>
              <h2 className="mb-4 text-sm font-semibold uppercase tracking-wider text-surface-400">
                {section.category}
              </h2>
              <div className="space-y-1">
                {section.items.map((faq) => {
                  const key = `${section.category}-${faq.q}`;
                  const isOpen = openFaq === key;
                  return (
                    <div key={key} className="border-b border-surface-800">
                      <button
                        className="flex w-full items-center justify-between py-4 text-left text-sm font-medium text-surface-200 hover:text-surface-50"
                        onClick={() => setOpenFaq(isOpen ? null : key)}
                      >
                        {faq.q}
                        <span className="ml-4 text-surface-600">
                          {isOpen ? "\u2212" : "+"}
                        </span>
                      </button>
                      {isOpen && (
                        <p className="pb-4 text-sm leading-relaxed text-surface-500">
                          {faq.a}
                        </p>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </div>

        <div className="mt-12 rounded-lg border border-surface-800 bg-surface-900/50 p-6 text-center">
          <p className="text-sm text-surface-400">
            Found a bug or have a feature request?
          </p>
          <a
            href="https://github.com/kivanccakmak/yaver/issues"
            target="_blank"
            rel="noopener noreferrer"
            className="mt-2 inline-block text-sm font-medium text-surface-200 underline underline-offset-2 hover:text-surface-50"
          >
            Open a GitHub issue
          </a>
        </div>

        <div className="mt-8 text-center">
          <Link href="/" className="text-xs text-surface-500 hover:text-surface-50">
            Back to home
          </Link>
        </div>
      </div>
    </div>
  );
}
