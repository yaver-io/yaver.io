import Link from "next/link";
import type { Metadata } from "next";
import { blogPosts, paginate, POSTS_PER_PAGE } from "@/lib/blog";

export const metadata: Metadata = {
  title: "Blog — Yaver",
  description:
    "Product releases, architecture notes, and major Yaver updates. Yaver is a P2P tool that lets developers run Claude Code, OpenAI Codex, or OpenCode (BYOK Anthropic / OpenAI / OpenRouter / GLM, or local Ollama) from their phone, connecting directly to their development machines.",
  alternates: { canonical: "https://yaver.io/blog" },
  openGraph: {
    title: "Blog — Yaver",
    description:
      "Product releases, architecture notes, and major Yaver updates.",
    url: "https://yaver.io/blog",
    siteName: "Yaver",
    type: "website",
  },
  twitter: {
    card: "summary_large_image",
    title: "Blog — Yaver",
    description: "Product releases, architecture notes, and major Yaver updates.",
  },
};

type SearchParams = Promise<{ page?: string }>;

export default async function BlogIndexPage({
  searchParams,
}: {
  searchParams: SearchParams;
}) {
  const params = await searchParams;
  const requested = Number.parseInt(params?.page ?? "1", 10) || 1;
  const { posts, page, totalPages } = paginate(blogPosts, requested);

  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <div className="mb-14">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">Blog</h1>
          <p className="text-sm leading-relaxed text-surface-500">
            Product releases, architecture notes, and major Yaver updates.
          </p>
        </div>

        <div className="space-y-4">
          {posts.map((post) => (
            <Link
              key={post.slug}
              href={`/blog/${post.slug}`}
              className="block rounded-2xl border border-surface-800 bg-surface-900 p-6 transition-colors hover:border-surface-600"
            >
              <time
                dateTime={post.date}
                className="text-xs uppercase tracking-[0.2em] text-surface-500"
              >
                {post.date}
              </time>
              <h2 className="mt-2 text-xl font-semibold text-surface-50">{post.title}</h2>
              <p className="mt-3 text-sm leading-6 text-surface-400">{post.description}</p>
            </Link>
          ))}
        </div>

        {totalPages > 1 && (
          <nav
            aria-label="Blog pagination"
            className="mt-10 flex items-center justify-between text-sm text-surface-400"
          >
            {page > 1 ? (
              <Link
                href={page - 1 === 1 ? "/blog" : `/blog?page=${page - 1}`}
                className="rounded-lg border border-surface-800 px-4 py-2 transition-colors hover:border-surface-600 hover:text-surface-50"
                rel="prev"
              >
                &larr; Newer
              </Link>
            ) : (
              <span />
            )}
            <span className="text-xs uppercase tracking-[0.2em] text-surface-500">
              Page {page} of {totalPages}
            </span>
            {page < totalPages ? (
              <Link
                href={`/blog?page=${page + 1}`}
                className="rounded-lg border border-surface-800 px-4 py-2 transition-colors hover:border-surface-600 hover:text-surface-50"
                rel="next"
              >
                Older &rarr;
              </Link>
            ) : (
              <span />
            )}
          </nav>
        )}

        <p className="sr-only">
          {blogPosts.length} posts total, {POSTS_PER_PAGE} per page.
        </p>
      </div>
    </div>
  );
}
