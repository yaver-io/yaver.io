export type BlogPost = {
  slug: string;
  title: string;
  date: string;
  description: string;
};

export const POSTS_PER_PAGE = 10;

export const blogPosts: BlogPost[] = [
  {
    slug: "ai-iot-fix-architecture",
    title: "Designing an AI-to-IoT Fix Loop: Mobile Orchestrator, Cloud Brain, and c-agent",
    date: "2026-04-25",
    description:
      "The architecture behind Yaver's IoT troubleshooting direction: a phone as the operator surface, a cloud brain that plans and signs work, and a small c-agent runtime on the device that executes bounded diagnostics and fixes.",
  },
  {
    slug: "remote-opencode-on-windows-ollama-via-yaver",
    title:
      "Driving a Windows Ollama Box Remotely with Yaver and opencode",
    date: "2026-04-24",
    description:
      "A ground-truth, developer-docs walkthrough for setting up a Windows box with Ollama + Qwen, exposing it over Tailscale, wrapping it with opencode, and letting Yaver's Go agent manage providers, models, and API keys — all reachable from CLI, the Yaver mobile app, or your own product's settings page.",
  },
  {
    slug: "windows-ollama-box-antigravity-workflow",
    title: "A Clean Antigravity Workflow with a Windows Ollama Box",
    date: "2026-04-23",
    description:
      "How to keep a Windows machine always-on as a self-hosted LLM box, expose Ollama safely over Tailscale, and drive it from a MacBook through Antigravity, Continue, and OpenCode without turning the editor setup into a mess.",
  },
  {
    slug: "mac-to-windows-ai-box-over-ssh",
    title: "Turning a Windows PC into a Remote AI Coding Box for a MacBook",
    date: "2026-04-23",
    description:
      "A practical setup for using a 32 GB Windows machine as the always-on coding box: OpenSSH from macOS, Tailscale for stable reachability, Ollama with a Qwen coder model, and a terminal workflow that also fits Antigravity-style remote editing.",
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
