export type BlogPost = {
  slug: string;
  title: string;
  date: string;
  description: string;
};

export const POSTS_PER_PAGE = 10;

export const blogPosts: BlogPost[] = [
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
