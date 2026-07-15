import type { Metadata } from "next";
import { Inter } from "next/font/google";
import Script from "next/script";
import "./globals.css";
import Header from "@/components/Header";
import Footer from "@/components/Footer";
import ThemeProvider from "@/components/ThemeProvider";
import ChatWidget from "@/components/ChatWidget";

const inter = Inter({
  subsets: ["latin"],
  variable: "--font-inter",
});

export const metadata: Metadata = {
  title: "Yaver \u2014 Your Machine Is Your Cloud",
  description:
    "Create full-stack projects, run local backends, test on iOS, Android, watch, TV, car, and AR/VR surfaces, and deploy from your own machine. Free, open source, P2P encrypted.",
  keywords: [
    "developer control plane",
    "local development platform",
    "Convex local dashboard",
    "Supabase local",
    "deploy from phone",
    "AR VR developer tools",
    "watch app developer tools",
    "TV app developer tools",
    "car app developer tools",
    "vibe coding",
    "your machine is your cloud",
    "Claude Code",
    "Ollama",
    "MCP server",
  ],
  icons: {
    icon: [
      { url: "/favicon.ico", sizes: "48x48" },
      { url: "/icon-192.png", sizes: "192x192", type: "image/png" },
      { url: "/icon-512.png", sizes: "512x512", type: "image/png" },
    ],
    apple: [{ url: "/apple-touch-icon.png", sizes: "180x180" }],
  },
  openGraph: {
    title: "Yaver \u2014 Your Machine Is Your Cloud",
    description:
      "Control your dev machines from phone, web, watch, TV, car, and AR/VR surfaces. Create projects, run local backends, test on real devices, deploy. Free forever.",
    url: "https://yaver.io",
    siteName: "Yaver",
    type: "website",
    images: [{ url: "/og-image.png", width: 1200, height: 630 }],
  },
  twitter: {
    card: "summary_large_image",
    title: "Yaver \u2014 Your Machine Is Your Cloud",
    description:
      "Control your dev machines from phone, web, watch, TV, car, and AR/VR surfaces. Create projects, run local backends, test on real devices, deploy. Free forever.",
    images: ["/og-image.png"],
  },
  metadataBase: new URL("https://yaver.io"),
  manifest: "/manifest.webmanifest",
  alternates: {
    canonical: "https://yaver.io",
    // llms.txt is the agent-facing install guide. Declaring it as an
    // alternate representation lets AI crawlers (Claude, ChatGPT, …)
    // find a deterministic, human-free install recipe
    // instead of scraping React markup.
    types: {
      "text/plain": [{ url: "/llms.txt", title: "Yaver AI-agent install guide (llms.txt)" }],
      "application/json": [
        { url: "/.well-known/mcp/server.json", title: "Yaver MCP Registry server.json" },
        { url: "/.well-known/mcp/server-card.json", title: "Yaver MCP server card" },
        { url: "/.well-known/mcp.json", title: "Yaver MCP server card" },
      ],
    },
  },
  other: {
    // Explicit pointer for agents that look at arbitrary <meta> tags.
    "ai:install-guide": "https://yaver.io/llms.txt",
    "ai:install-command": "npm install -g yaver-cli && yaver auth",
    "mcp:server": "io.github.kivanccakmak/yaver",
    "mcp:manifest": "https://yaver.io/.well-known/mcp/server.json",
    "mcp:config": "https://yaver.io/.mcp.json",
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        {/*
          Apply the saved theme before React hydrates so the page doesn't
          flash the default palette first. Dark stays the factory default
          for visitors who've never toggled; only explicit "light" opts
          out. Mirrors ThemeProvider's localStorage contract.
        */}
        <script
          dangerouslySetInnerHTML={{
            __html: `
              try {
                var t = localStorage.getItem('theme');
                if (t !== 'light') document.documentElement.classList.add('dark');
              } catch (e) {
                document.documentElement.classList.add('dark');
              }
              try {
                if (new URLSearchParams(window.location.search).get('embed') === 'mobile') {
                  document.documentElement.classList.add('yaver-mobile-embed');
                }
              } catch (e) {}
            `,
          }}
        />
        <Script
          src="https://www.googletagmanager.com/gtag/js?id=G-K7JHRJKPQB"
          strategy="afterInteractive"
        />
        <Script id="gtag-init" strategy="afterInteractive">
          {`
            window.dataLayer = window.dataLayer || [];
            function gtag(){dataLayer.push(arguments);}
            gtag('js', new Date());
            gtag('config', 'G-K7JHRJKPQB');
          `}
        </Script>
      </head>
      <body className={`${inter.variable} font-sans`}>
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{
            __html: JSON.stringify({
              "@context": "https://schema.org",
              "@type": "SoftwareApplication",
              name: "Yaver",
              applicationCategory: "DeveloperApplication",
              operatingSystem: "macOS, Windows, Linux, iOS, Android",
              description:
                "Open-source P2P tool that lets developers use any AI coding agent from their mobile device, connecting directly to development machines.",
              url: "https://yaver.io",
              downloadUrl: "https://yaver.io/download",
              installUrl: "https://yaver.io/llms.txt",
              softwareHelp: "https://yaver.io/llms.txt",
              offers: { "@type": "Offer", price: "0", priceCurrency: "USD" },
              author: {
                "@type": "Organization",
                name: "Yaver",
                url: "https://yaver.io",
              },
            }),
          }}
        />
        <ThemeProvider>
          <div className="flex min-h-screen flex-col">
            <Header />
            <main className="flex-1">{children}</main>
            <Footer />
          </div>
          <ChatWidget />
        </ThemeProvider>
      </body>
    </html>
  );
}
