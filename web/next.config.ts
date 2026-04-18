import type { NextConfig } from "next";
import { initOpenNextCloudflareForDev } from "@opennextjs/cloudflare";

if (process.env.NODE_ENV === "development") {
  initOpenNextCloudflareForDev();
}

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // Short, speakable aliases that jump straight to the AI-facing
  // install guide. Lets a non-developer tell their coding agent
  // "go to yaver.io/for-agents" or "yaver.io/setup" without having
  // to remember the .txt suffix.
  async redirects() {
    return [
      { source: "/for-agents", destination: "/llms.txt", permanent: true },
      { source: "/setup", destination: "/llms.txt", permanent: true },
      { source: "/ai", destination: "/llms.txt", permanent: true },
    ];
  },
};

export default nextConfig;
