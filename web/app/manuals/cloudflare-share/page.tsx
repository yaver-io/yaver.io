import { redirect } from "next/navigation";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Yaver Relay Setup — Yaver Manual",
  description:
    "Use Yaver Relay for remote access to your coding box or VPS. Free Relay is for light use, and Relay Pro provides managed private reachability for daily work.",
  alternates: { canonical: "https://yaver.io/manuals/relay-setup" },
  robots: { index: false, follow: true },
};

export default function LegacyCloudflareShareManualPage() {
  redirect("/manuals/relay-setup");
}
