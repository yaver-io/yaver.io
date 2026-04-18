import Link from "next/link";

const posts = [
  {
    title: "Announcing the Yaver Raspberry Pi 5 Dev-Node Image",
    href: "/blog/yaver-pi-image",
    date: "2026-04-18",
    description:
      "A prebuilt ARM64 image for Raspberry Pi 5 that turns a Pi into a headless Yaver developer node with an economic hybrid AI stack.",
  },
];

export default function BlogIndexPage() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <div className="mb-14">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            Blog
          </h1>
          <p className="text-sm leading-relaxed text-surface-500">
            Product releases, architecture notes, and major Yaver updates.
          </p>
        </div>

        <div className="space-y-4">
          {posts.map((post) => (
            <Link
              key={post.href}
              href={post.href}
              className="block rounded-2xl border border-surface-800 bg-surface-900 p-6 transition-colors hover:border-surface-600"
            >
              <p className="text-xs uppercase tracking-[0.2em] text-surface-500">{post.date}</p>
              <h2 className="mt-2 text-xl font-semibold text-surface-50">{post.title}</h2>
              <p className="mt-3 text-sm leading-6 text-surface-400">{post.description}</p>
            </Link>
          ))}
        </div>
      </div>
    </div>
  );
}
