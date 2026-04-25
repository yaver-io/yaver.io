import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "ai-iot-fix-architecture";
const post = postBySlug(POST_SLUG)!;
const POST_URL = `https://yaver.io/blog/${POST_SLUG}`;

export const metadata: Metadata = {
  title: `${post.title} — Yaver Blog`,
  description: post.description,
  alternates: { canonical: POST_URL },
  openGraph: {
    title: post.title,
    description: post.description,
    url: POST_URL,
    siteName: "Yaver",
    type: "article",
    publishedTime: post.date,
    authors: ["Yaver"],
    tags: ["IoT", "AI", "c-agent", "LLM", "Mobile Orchestrator", "Embedded Systems"],
    images: [{ url: "/og-image.png", width: 1200, height: 630 }],
  },
  twitter: {
    card: "summary_large_image",
    title: post.title,
    description: post.description,
    images: ["/og-image.png"],
  },
};

const articleLd = {
  "@context": "https://schema.org",
  "@type": "BlogPosting",
  headline: post.title,
  description: post.description,
  datePublished: post.date,
  dateModified: post.date,
  url: POST_URL,
  mainEntityOfPage: POST_URL,
  image: "https://yaver.io/og-image.png",
  author: { "@type": "Organization", name: "Yaver", url: "https://yaver.io" },
  publisher: {
    "@type": "Organization",
    name: "Yaver",
    url: "https://yaver.io",
    logo: { "@type": "ImageObject", url: "https://yaver.io/icon-512.png" },
  },
};

export default function AIIoTFixArchitectureBlogPage() {
  return (
    <div className="px-6 py-20">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(articleLd) }}
      />
      <article className="mx-auto max-w-3xl space-y-8 text-sm leading-7 text-surface-300">
        <Link href="/blog" className="inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50">
          &larr; Back to Blog
        </Link>

        <header className="space-y-4">
          <time dateTime={post.date} className="text-xs uppercase tracking-[0.2em] text-surface-500">
            {post.date}
          </time>
          <h1 className="text-3xl font-bold text-surface-50 md:text-4xl">
            Designing an AI-to-IoT Fix Loop: Mobile Orchestrator, Cloud Brain, and c-agent
          </h1>
          <p className="text-surface-400">
            The goal is not another dashboard full of prewritten probes. The goal is a system that can
            understand a live device problem, write the right diagnostic code for that incident, run it
            safely on the device, and converge toward a fix while a human stays in control.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The motivating fix case</h2>
          <p>
            Start with a concrete example: a Klipper printer suddenly starts under-extruding after
            a board swap. A static tool catalog is not enough. One incident needs Moonraker state,
            another needs Wi-Fi health, another needs a kernel log, another needs a heater PID
            history or a board-specific config diff.
          </p>
          <p className="mt-4">
            That is why the architecture is built around <strong>per-incident code generation</strong>.
            The brain should be able to author a small bounded probe for this case, sign it, ship it,
            run it, inspect the result, and then decide whether to ask a follow-up question, run
            another probe, or propose a fix.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The four actors</h2>
          <pre className="overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`operator on phone
  -> mobile orchestrator
  -> cloud brain
  -> LLM coordinator + retrieval + build/sign pipeline
  -> c-agent runtime on device
  -> built-in probe / wasm module / bounded fix
  -> result stream back to phone`}
          </pre>
          <p className="mt-4">
            Each actor has a narrow job:
          </p>
          <ul className="mt-3 list-disc space-y-2 pl-6 text-surface-400">
            <li><strong>Mobile orchestrator:</strong> operator UI, approvals, incident history, and live status.</li>
            <li><strong>Cloud brain:</strong> session coordinator, incident memory, routing, and audit.</li>
            <li><strong>LLM coordinator:</strong> reasoning layer that decides what probe or fix should run next.</li>
            <li><strong>c-agent runtime:</strong> small device-side execution layer that verifies, binds capabilities, runs, and reports.</li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Why the phone matters</h2>
          <p>
            The phone is not the place that executes diagnostics. It is the <strong>operator
            surface</strong>. That distinction matters.
          </p>
          <p className="mt-4">
            The mobile app is where the human says &ldquo;the printer is jamming after 20 minutes&rdquo;,
            sees what the system is doing, approves risky actions, and receives the final explanation.
            In the higher-risk flows, the phone is also the trust surface that signs off on reboot,
            rollback, or config mutation.
          </p>
          <p className="mt-4">
            That makes the phone analogous to a field-tech console, not a transport gimmick.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">What the cloud brain actually does</h2>
          <p>
            The cloud brain is the durable coordinator. It owns the incident graph, remembers prior
            iterations, keeps the audit trail, and decides whether the next step is retrieval,
            another probe, a fix proposal, or a stop condition.
          </p>
          <p className="mt-4">
            It is also where module artifacts get built and signed. The device should not compile
            untrusted code locally. The brain produces an immutable artifact, signs it, and the
            device runtime either accepts or rejects it.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The LLM is not the runtime</h2>
          <p>
            A common mistake in AI systems is to let the model collapse into the whole stack. Here,
            the LLM is only the <strong>reasoning layer</strong>.
          </p>
          <p className="mt-4">
            It reads the incident, prior telemetry, retrieved patterns, and device capabilities. It
            then emits a plan such as:
          </p>
          <pre className="overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`1. run wifi_client_count
2. query klipper_status for heater + motion objects
3. if heater drift is high, author a focused PID-inspection probe
4. if config mismatch is likely, propose a bounded config fix
5. require mobile approval before applying`}
          </pre>
          <p className="mt-4">
            The runtime still enforces the boundary. The model can propose. It does not get to
            skip verification, capability binding, budgets, or approval gates.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Why c-agent exists</h2>
          <p>
            The c-agent is the smallest trustworthy surface on the device. It is where the architecture
            becomes practical.
          </p>
          <ul className="mt-3 list-disc space-y-2 pl-6 text-surface-400">
            <li>It speaks a small framed protocol.</li>
            <li>It understands signed module delivery.</li>
            <li>It exposes only declared capabilities.</li>
            <li>It runs built-in probes today and is designed to run wasm modules next.</li>
            <li>It can stream partial output and return structured final results.</li>
          </ul>
          <p className="mt-4">
            That gives the system a stable execution substrate across wildly different device classes:
            printers, routers, drones, CNC controllers, and robotics hosts.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Design hardware and firmware to be fixable</h2>
          <p>
            There is also a hardware-design implication here: if you want AI-assisted repair to work,
            subsystems cannot be built with an &ldquo;any dependency failure means process crash&rdquo; mindset.
          </p>
          <p className="mt-4">
            Replaceable components should be able to enter a <strong>bounded stuck or degraded mode</strong>,
            preserve enough state to be resumed, and wait for a new module or dependency implementation
            to be inserted. That is a much better substrate than taking down the full controller every
            time one subsystem wedges.
          </p>
          <p className="mt-4">
            In practice that means designing components so they can:
          </p>
          <ul className="mt-3 list-disc space-y-2 pl-6 text-surface-400">
            <li>quiesce instead of aborting the host process</li>
            <li>stop initiating new work while in-flight work drains or is safely cancelled</li>
            <li>surface a clear &ldquo;dependency unavailable / waiting for replacement&rdquo; state</li>
            <li>resume once a replacement module is loaded and validated</li>
          </ul>
          <p className="mt-4">
            That is exactly why the c-agent host model includes quiesce, pause, resume, queued invokes,
            and replace semantics. AI-fixable hardware needs <strong>hot-swappable failure boundaries</strong>,
            not global crashes.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The real architecture claim</h2>
          <p>
            The interesting claim is not &ldquo;LLMs can analyze logs.&rdquo; Everyone can do that. The real
            claim is:
          </p>
          <pre className="overflow-x-auto rounded-lg bg-surface-900 p-4 text-xs text-surface-300">
{`human reports issue on phone
  -> brain plans
  -> LLM writes a bounded probe or fix
  -> build/sign pipeline produces immutable artifact
  -> c-agent verifies and runs it
  -> telemetry streams back
  -> brain refines
  -> phone approves high-risk actions
  -> device converges on a fix`}
          </pre>
          <p className="mt-4">
            That is the loop we are designing Yaver&apos;s IoT lane around.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Why start with repair, not general automation</h2>
          <p>
            Repair has the cleanest wedge. Users already feel pain, already pay for help, and already
            tolerate a guided human-in-the-loop workflow. A static control panel is not enough for
            the long tail of failure cases, but a bounded iterative loop can be.
          </p>
          <p className="mt-4">
            That is why Klipper, OpenWrt, PX4, and similar surfaces are attractive. They are open,
            technical, Linux-heavy, and full of incidents where one more custom probe changes the
            outcome.
          </p>
        </section>
      </article>
    </div>
  );
}
