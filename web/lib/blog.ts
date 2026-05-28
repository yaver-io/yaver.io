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
    slug: "cloudflare-tunnels-shared-boxes",
    title: "Cloudflare Tunnels, Shared Boxes, and Yaver's Real Trust Boundary",
    date: "2026-04-22",
    description:
      "How a host can put one Yaver box behind Cloudflare Tunnel, share that box with guests, and keep Yaver as the actual authorization boundary.",
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
