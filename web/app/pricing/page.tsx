"use client";

import Link from "next/link";
// Public catalog: the open-source runtime on hardware the customer controls,
// plus the $9/month managed Relay Pro connectivity layer. Yaver does not sell
// hosted compute or AI model usage in this release.

const PLANS = [
  {
    name: "Self-hosted",
    price: "$0",
    per: "forever",
    highlight: false,
    tagline: "Run Yaver on your own machines. Full runtime, no Yaver infrastructure.",
    items: [
      "npm install -g yaver-cli on your Mac, Linux box, or Pi",
      "Your files, secrets, and runner output stay on your hardware",
      "Connect over LAN, your own relay, or Tailscale",
      "All surfaces: phone, tablet, watch, TV, car, web",
      "Open source — FSL-1.1-Apache-2.0 core",
    ],
    ctaLabel: "Install free",
    ctaHref: "/download",
  },
  {
    name: "Relay Pro",
    price: "$9",
    per: "/mo",
    highlight: true,
    tagline: "Managed private connectivity to the machines you already own or rent.",
    items: [
      "Mac, Windows/WSL, Linux, VPS, or Pi",
      "Claude Code, Codex, OpenCode, and terminal agents",
      "Browser lane, Hermes/native previews, and Feedback SDK",
      "Every supported Yaver client surface—or your own app using the SDK",
      "Account- and device-scoped authentication on shared managed infrastructure",
    ],
    ctaLabel: "Get Relay Pro",
    ctaHref: "/auth",
  },
];

const PRICING_FAQ: ReadonlyArray<{ q: string; a: string }> = [
  {
    q: "What does 'bring your own AI account' mean?",
    a: "Yaver doesn't sell AI model access. You use your existing Claude Code, Codex, OpenCode, or other terminal agent account on your own machine. Relay Pro provides the remote connectivity and Yaver clients around it.",
  },
  {
    q: "Can I self-host instead of paying?",
    a: "Yes. Install the CLI on your own machine or VPS, then use LAN, your own relay, or another private network. Relay Pro is the optional managed connectivity layer.",
  },
  {
    q: "Do I need a cloud account or token to use Yaver?",
    a: "No. Yaver runs on a computer you control, including a VPS from any provider. Relay Pro itself is provisioned server-side and never asks for your provider credentials.",
  },
  {
    q: "Does Relay Pro include a cloud computer or AI subscription?",
    a: "No. Relay Pro is connectivity, not hosted compute or model usage. Your coding agent runs on your own computer or VPS with your own agent account.",
  },
  {
    q: "Is Relay Pro unlimited?",
    a: "No. Relay Pro includes a high daily transfer allowance with fair-use controls so one account cannot degrade a shared relay host. The dashboard shows usage, and Yaver will name the limit instead of silently slowing or billing overage.",
  },
  {
    q: "How do I pay?",
    a: "Checkout happens in the Yaver dashboard after you sign in. Subscriptions are billed through LemonSqueezy.",
  },
];

function FAQItem({ question, answer }: { question: string; answer: string }) {
  return (
    <div className="border-b border-surface-800/60 py-5">
      <p className="text-sm font-medium text-surface-100">{question}</p>
      <p className="mt-2 text-sm leading-relaxed text-surface-400">{answer}</p>
    </div>
  );
}

export default function PricingPage() {
  return (
    <>
      {/* ── Hero ── */}
      <section className="px-6 pb-12 pt-20 md:pt-28">
        <div className="mx-auto max-w-5xl text-center">
          <p className="mb-3 text-xs font-semibold uppercase tracking-[0.25em] text-surface-400">
            Pricing
          </p>
          <h1 className="mb-5 text-4xl font-bold leading-[1.02] tracking-tight text-surface-50 md:text-5xl">
            Self-host for free.
            <br className="hidden sm:block" />
            <span className="bg-gradient-to-r from-indigo-400 to-emerald-400 bg-clip-text text-transparent">
              Pay only when Yaver runs infrastructure for you.
            </span>
          </h1>
          <p className="mx-auto max-w-2xl text-sm leading-relaxed text-surface-300 md:text-base">
            The full remote AI runtime is free on your own machines. The paid
            option is Relay Pro: managed private connectivity to your own machine.
          </p>
        </div>
      </section>

      {/* ── Plan cards ── */}
      <section className="px-6 pb-16">
        <div className="mx-auto grid max-w-4xl gap-6 md:grid-cols-2">
          {PLANS.map((plan) => (
            <div
              key={plan.name}
              className={
                "flex flex-col rounded-xl border p-6 " +
                (plan.highlight
                  ? "border-emerald-500/40 bg-emerald-500/5"
                  : "border-surface-800 bg-surface-900/50")
              }
            >
              <p className="text-sm font-semibold text-surface-100">{plan.name}</p>
              <p className="mt-3 text-4xl font-bold text-surface-50">
                {plan.price}
                <span className="text-base font-normal text-surface-500">{plan.per}</span>
              </p>
              <p className="mt-3 text-xs leading-relaxed text-surface-400">{plan.tagline}</p>
              <ul className="mt-5 flex flex-1 flex-col gap-2.5">
                {plan.items.map((item) => (
                  <li key={item} className="flex items-start gap-2 text-xs leading-relaxed text-surface-400">
                    <span className="mt-0.5 shrink-0 text-emerald-500">{"\u2713"}</span>
                    <span>{item}</span>
                  </li>
                ))}
              </ul>
              <Link
                href={plan.ctaHref}
                className={
                  "mt-6 rounded-lg px-4 py-2.5 text-center text-sm font-medium transition-colors " +
                  (plan.highlight
                    ? "btn-primary"
                    : "border border-surface-700 bg-surface-900 text-surface-200 hover:border-surface-600 hover:text-surface-50")
                }
              >
                {plan.ctaLabel}
              </Link>
            </div>
          ))}
        </div>
        <p className="mx-auto mt-8 max-w-2xl text-center text-xs text-surface-600">
          Relay Pro uses shared managed infrastructure with account- and device-scoped authorization. It is private to your account, but it is not a dedicated server or end-to-end-encryption product.
        </p>
      </section>

      {/* ── Comparison ── */}
      <section className="border-t border-surface-800/60 px-6 py-16">
        <div className="mx-auto max-w-4xl">
          <h2 className="mb-8 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Which path is yours?
          </h2>
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-xs uppercase tracking-wider text-surface-500">
                  <th className="py-3 pr-4 font-medium">Feature</th>
                  <th className="py-3 pr-4 font-medium">Self-hosted</th>
                  <th className="py-3 font-medium">Relay Pro</th>
                </tr>
              </thead>
              <tbody className="text-surface-300">
                {[
                  ["Where the agent runs", "Your machine or VPS", "Your machine or VPS"],
                  ["Coding agents", "Your accounts", "Your accounts"],
                  ["Yaver clients and SDK", "Included", "Included"],
                  ["Remote relay", "Free shared / self-hosted", "Managed private lane"],
                  ["Monthly cost", "$0", "$9"],
                ].map((row) => (
                  <tr key={row[0]} className="border-b border-surface-800/40">
                    <td className="py-3 pr-4 font-medium text-surface-100">{row[0]}</td>
                    <td className="py-3 pr-4">{row[1]}</td>
                    <td className="py-3">{row[2]}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </section>

      {/* ── FAQ ── */}
      <section className="border-t border-surface-800/60 px-6 py-16">
        <div className="mx-auto max-w-3xl">
          <h2 className="mb-8 text-center text-2xl font-bold text-surface-50 md:text-3xl">
            Pricing FAQ
          </h2>
          {PRICING_FAQ.map(({ q, a }) => (
            <FAQItem key={q} question={q} answer={a} />
          ))}
        </div>
      </section>
    </>
  );
}
