import type { MetadataRoute } from "next";
import { publicBlogPosts } from "@/lib/blog";

const SITE = "https://yaver.io";

type StaticRoute = {
  path: string;
  changeFrequency: MetadataRoute.Sitemap[number]["changeFrequency"];
  priority: number;
};

const staticRoutes: StaticRoute[] = [
  { path: "", changeFrequency: "weekly", priority: 1.0 },
  { path: "/download", changeFrequency: "weekly", priority: 0.9 },
  { path: "/integrations", changeFrequency: "monthly", priority: 0.8 },
  { path: "/apps", changeFrequency: "weekly", priority: 0.8 },
  { path: "/games", changeFrequency: "weekly", priority: 0.8 },
  { path: "/blog", changeFrequency: "weekly", priority: 0.8 },
  { path: "/docs", changeFrequency: "weekly", priority: 0.8 },
  { path: "/docs/developers", changeFrequency: "weekly", priority: 0.8 },
  { path: "/docs/mcp", changeFrequency: "weekly", priority: 0.7 },
  { path: "/docs/self-hosting", changeFrequency: "monthly", priority: 0.7 },
  { path: "/docs/contributing", changeFrequency: "monthly", priority: 0.6 },
  { path: "/docs/feedback-sdk", changeFrequency: "monthly", priority: 0.6 },
  { path: "/faq", changeFrequency: "monthly", priority: 0.7 },
  { path: "/manuals", changeFrequency: "monthly", priority: 0.7 },
  { path: "/manuals/cli-setup", changeFrequency: "monthly", priority: 0.6 },
  { path: "/manuals/relay-setup", changeFrequency: "monthly", priority: 0.6 },
  { path: "/manuals/raspberry-pi", changeFrequency: "monthly", priority: 0.6 },
  { path: "/manuals/auto-boot", changeFrequency: "monthly", priority: 0.5 },
  { path: "/manuals/voice-ai", changeFrequency: "monthly", priority: 0.5 },
  { path: "/manuals/feedback-loop", changeFrequency: "monthly", priority: 0.5 },
  { path: "/manuals/code-from-beach", changeFrequency: "monthly", priority: 0.5 },
  { path: "/manuals/integrations", changeFrequency: "monthly", priority: 0.6 },
  { path: "/support", changeFrequency: "monthly", priority: 0.4 },
  { path: "/licensing", changeFrequency: "monthly", priority: 0.6 },
  { path: "/privacy", changeFrequency: "yearly", priority: 0.3 },
  { path: "/terms", changeFrequency: "yearly", priority: 0.3 },
];

export default function sitemap(): MetadataRoute.Sitemap {
  const now = new Date();
  const entries: MetadataRoute.Sitemap = staticRoutes.map((r) => ({
    url: `${SITE}${r.path}`,
    lastModified: now,
    changeFrequency: r.changeFrequency,
    priority: r.priority,
  }));
  for (const post of publicBlogPosts) {
    entries.push({
      url: `${SITE}/blog/${post.slug}`,
      lastModified: new Date(post.date),
      changeFrequency: "monthly",
      priority: 0.7,
    });
  }
  return entries;
}
