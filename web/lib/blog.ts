export type BlogPost = {
  slug: string;
  title: string;
  date: string;
  description: string;
  published?: boolean;
};

export const POSTS_PER_PAGE = 10;

export const blogPosts: BlogPost[] = [
  {
    slug: "mobile-beta-testing-apple-google",
    title: "Ship to testers: TestFlight & Google Play internal testing, end to end",
    date: "2026-06-24",
    published: true,
    description:
      "The field guide for getting a build into a tester's hands on both stores: the developer accounts you need (Apple Developer Program, Google Play Console), App Store Connect and Play Console roles, internal vs external testers, exactly how a tester downloads via TestFlight or a Play opt-in link, the API keys and service accounts that automate uploads, and Yaver's one-command path to push to both — deploy-testflight.sh and deploy-playstore.sh + upload-playstore.py. Plus how Yaver makes the tester/build lifecycle first-class for your own and third-party apps — store_* MCP tools, a web Testers tab, and a mobile screen — driven by the App Store Connect and Google Play Developer APIs, multi-tenant and managed-cloud-ready.",
  },
  {
    slug: "yaver-sdk-developer-guide",
    title: "Embed Yaver: using yaver-sdk as a library",
    date: "2026-06-05",
    published: true,
    description:
      "Yaver ships as a library, not just an app. yaver-sdk lets your own React Native, web, or Node app run Claude Code, Codex, or OpenCode on a remote machine — broker a scoped token on the server, connectHandle() on the client, stream the runner's output, and drive runner OAuth in an in-app browser. The full developer guide: the broker/connect model, self-healing token refresh, the two OAuth levels, and the complete export surface.",
  },
  {
    slug: "yaver-rustdesk-ghosting",
    title: "Ghost mode: drive any legacy desktop ERP through RustDesk, with AI",
    date: "2026-06-04",
    published: true,
    description:
      "Yaver's UI ghost operates a legacy app's own GUI the way a human clerk would — screenshot, locate with a vision model, click and type — across Windows, macOS, and Linux, plus headless web. The blackbox pattern: install only RustDesk on the customer's PC, drop in a pre-configured Yaver appliance, and let it read the data and write back through the app's screens. Includes the abstract remote-view layer (RustDesk/AnyDesk/VNC), DPI/retina-correct coordinates, a live MJPEG camera view, and the on-prem vs cloud-brain AI split.",
  },
  {
    slug: "yaver-machine-discovery-modbus",
    title: "Machine discovery: reverse-engineering a PLC over Modbus with AI",
    date: "2026-06-04",
    published: true,
    description:
      "Point Yaver at a wire-harness machine's PLC — sniff the Modbus-RTU bus read-only or read-scan it over Modbus-TCP — and let an AI infer what each holding register means (cut_length, strip_left, quantity, speed, alarm word…), its unit, and its scale, anchored on the job's ground-truth values. Then read/verify, optionally range-clamped write-back, and turn an opaque controller into a typed, queryable device.",
  },
  {
    slug: "stt-tts-voice-local-byok",
    title: "Voice in Yaver: local STT/TTS by default, bring-your-own cloud, keys in the vault",
    date: "2026-05-30",
    published: true,
    description:
      "How speech-to-text and text-to-speech work across the Go agent, the mobile app, and the web dashboard. Free on-device Whisper + OS voice out of the box, optional cloud engines (OpenAI, Deepgram Flux, Cartesia Sonic) configured per surface, and why API keys live in your encrypted `yaver vault` and flow P2P — never through Convex. Plus: the agent now knows whether the client has STT/TTS on, and shapes its replies for voice.",
  },
  {
    slug: "yaver-p2p-vault",
    title: "Yaver P2P Vault: secrets that follow your machines without touching our servers",
    date: "2026-05-29",
    published: true,
    description:
      "How to store API keys and deploy credentials in `yaver vault`, source them into builds, and sync them peer-to-peer across your own devices. The under-the-hood path: local encrypted vault.enc, owner-authenticated peer sync, digest/pull/push anti-entropy, tombstones, and why Convex never stores secret values.",
  },
  {
    slug: "yaver-cloud-image",
    title: "Yaver Cloud Image: a dev box on any provider, in 90 seconds",
    date: "2026-05-28",
    published: true,
    description:
      "Run one command. Get a Linux box on Hetzner, AWS, or GCP that's already signed in to your Yaver account, with claude-code, codex, and opencode authenticated from your existing devices. No tokens to copy, no second OAuth, no AMI hunting — this post is the install guide.",
  },
  {
    slug: "yaver-cloud-launch-anywhere",
    title: "Yaver cloud launch: anywhere, in five steps",
    date: "2026-05-28",
    published: true,
    description:
      "The architecture behind `yaver launch hetzner/aws/gcp/ssh` and the yaver.io/launch portal. The device-code authorize chain, why the Hetzner branch works without a public snapshot, and how SSH adoption reuses the same plumbing minus the provisioning step.",
  },
  {
    slug: "yaver-sandbox-slim",
    title: "yaver-sandbox-slim: a distroless Docker image with three coding agents",
    date: "2026-05-28",
    published: true,
    description:
      "ghcr.io/kivanccakmak/yaver-sandbox-slim — 1.3 GB, multi-arch, distroless/nodejs22 base with Node, git, busybox, Claude Code, Codex, and OpenCode pre-installed. The three-stage build, the dynamic-lib hunt, and the day we forgot /usr/bin/env.",
  },
  {
    slug: "yaver-zero-reoauth",
    title: "Zero re-OAuth: a fresh Yaver box arrives signed in to Claude Code, Codex, and OpenCode",
    date: "2026-05-28",
    published: true,
    description:
      "Spinning up a new Linux box usually means re-OAuth'ing every coding agent from scratch. Yaver mirrors the credentials your existing devices already have. Same Max Pro / ChatGPT Plus subscription, no double-billing. The two primitives — device-code pre-authorize + runner_auth_mirror — and how they chain.",
  },
  {
    slug: "yaver-install-script",
    title: "curl yaver.io/install | bash: what it actually does",
    date: "2026-05-28",
    published: true,
    description:
      "The 175-line install script that bridges from 'new machine, no Node' to 'npm install -g yaver-cli works'. Platform detection (macOS Homebrew, Debian NodeSource, RHEL dnf, nvm fallback), arch normalization, and the npm-prefix question. No telemetry, no binary tarballs, no background processes.",
  },
  {
    slug: "hermes-vs-webview-yaver-architecture",
    title: "Hermes Bytecode vs WebView: How Yaver Tests Native Apps Without an App Store Cycle",
    date: "2026-04-29",
    published: true,
    description:
      "How Yaver runs your in-progress React Native app on a real iPhone in 10 seconds — using Hermes bytecode for native frameworks and WebView for web frameworks. The architecture, what each path can and can't do, and where the limits come from (Apple, mostly).",
  },
  {
    slug: "opencode-providers-and-ollama",
    title: "OpenCode in Yaver: Bring Your Own Key, or Run Free on Ollama",
    date: "2026-04-28",
    description:
      "Yaver's chat picker now ships three coding agents: Claude Code, OpenAI Codex, and OpenCode. OpenCode is the BYOK lane — paste an Anthropic, OpenAI, OpenRouter, or GLM key and you're working. Or pick a local Ollama model and pay nothing.",
  },
  {
    slug: "ai-iot-fix-architecture",
    title: "AI-to-IoT Fix Loop: ...",
    date: "2026-04-25",
    published: false,
    description:
      "The architecture behind Yaver's IoT troubleshooting direction: a phone as the operator surface, a cloud brain that plans and signs work, and a small c-agent runtime on the device that executes bounded diagnostics and fixes.",
  },
  {
    slug: "unity-feedback-sdk-self-hosted-iteration",
    title: "Yaver for Unity, Explained Simply",
    date: "2026-04-23",
    description:
      "A plain-language explanation of what Yaver is building for Unity: feedback inside the game, self-hosted iteration, tests, builds, relaunches, and remote supervision on your own machines.",
  },
  {
    slug: "yaver-relay-shared-boxes",
    title: "Yaver Relay, Shared Boxes, and the Real Trust Boundary",
    date: "2026-04-22",
    description:
      "How a host can put one Yaver box behind Yaver Relay, share that box with guests, and keep Yaver as the actual authorization boundary.",
  },
  {
    slug: "yaver-pi-image",
    title: "Announcing the Yaver Raspberry Pi 5 Dev-Node Image",
    date: "2026-04-19",
    description:
      "A prebuilt ARM64 image for Raspberry Pi 5 that turns a Pi into a headless Yaver developer node — flash it, boot it, pair it from your phone. Includes the full dev stack, Ollama, and auto-updates.",
  },
];

export const publicBlogPosts = blogPosts.filter((post) => post.published !== false);

export function paginate(posts: BlogPost[], page: number) {
  const totalPages = Math.max(1, Math.ceil(posts.length / POSTS_PER_PAGE));
  const current = Math.min(Math.max(1, page), totalPages);
  const start = (current - 1) * POSTS_PER_PAGE;
  return {
    posts: posts.slice(start, start + POSTS_PER_PAGE),
    page: current,
    totalPages,
  };
}

export function postBySlug(slug: string): BlogPost | undefined {
  return blogPosts.find((p) => p.slug === slug);
}
