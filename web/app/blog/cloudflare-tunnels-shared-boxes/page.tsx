import { redirect } from "next/navigation";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Yaver Relay, Shared Boxes, and the Real Trust Boundary — Yaver Blog",
  description:
    "How a host can put one Yaver box behind Yaver Relay, share that box with guests, and keep Yaver as the actual authorization boundary.",
  alternates: { canonical: "https://yaver.io/blog/yaver-relay-shared-boxes" },
  robots: { index: false, follow: true },
};

export default function LegacyCloudflareTunnelsSharedBoxesBlogPage() {
  redirect("/blog/yaver-relay-shared-boxes");
}
