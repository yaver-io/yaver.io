import type { NextConfig } from "next";
import { initOpenNextCloudflareForDev } from "@opennextjs/cloudflare";

if (process.env.NODE_ENV === "development") {
  initOpenNextCloudflareForDev();
}

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // Don't 308 `/foo/` → `/foo`. Cloudflare/Next.js's default trailing-
  // slash strip breaks the iframe bundle path because the
  // `<base href="/dev/web-bundle/">` and the URL-rebase scripts on
  // both relay and agent sides assume the trailing slash stays put.
  // Stripping it sends pathname into a state expo-router can't match
  // and the iframe renders blank/unmatched-route instead of the app.
  skipTrailingSlashRedirect: true,
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
  // Force application/json on the WebAuthn / passkey association
  // files. Apple's CDN cache and Google's Asset Links validator both
  // refuse to parse these unless served with the JSON content type;
  // Next.js's static-asset serve detects the empty extension on AASA
  // as application/octet-stream by default.
  async headers() {
    return [
      {
        source: "/.well-known/apple-app-site-association",
        headers: [{ key: "Content-Type", value: "application/json" }],
      },
      {
        source: "/.well-known/assetlinks.json",
        headers: [{ key: "Content-Type", value: "application/json" }],
      },
    ];
  },
};

export default nextConfig;
