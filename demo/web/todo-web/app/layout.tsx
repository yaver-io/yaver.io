import type { Metadata } from "next";
import { FeedbackBoot } from "./feedback-boot";
import "./globals.css";

export const metadata: Metadata = {
  title: "Todo Web — Yaver Feedback Showcase",
  description: "Minimal Next.js Todo app showcasing the Yaver Feedback web SDK.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        {children}
        <FeedbackBoot />
      </body>
    </html>
  );
}
