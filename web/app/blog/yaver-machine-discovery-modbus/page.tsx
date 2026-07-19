import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "yaver-machine-discovery-modbus";
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
    tags: ["Modbus", "PLC", "machine discovery", "Industry 4.0", "wire harness", "Yaver"],
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
  keywords: ["Modbus TCP", "Modbus RTU", "PLC reverse engineering", "register map", "machine telemetry", "wire harness machine"],
};

export default function YaverMachineDiscoveryBlogPage() {
  return (
    <div className="px-6 py-20">
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(articleLd) }} />
      <article className="mx-auto max-w-3xl">
        <Link href="/blog" className="mb-8 inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50">
          &larr; Back to Blog
        </Link>

        <div className="mb-10">
          <time dateTime={post.date} className="text-xs uppercase tracking-[0.2em] text-surface-500">
            {post.date}
          </time>
          <h1 className="mt-3 text-3xl font-bold text-surface-50 md:text-4xl">
            Machine discovery: reverse-engineering a PLC over Modbus with AI
          </h1>
          <p className="mt-4 text-sm leading-7 text-surface-400">
            Most shop-floor machines speak Modbus, but nobody has the register map. Yaver&apos;s
            machine engine taps the bus, watches the numbers move, and lets an AI infer what every
            register actually means — turning an opaque controller into a typed, queryable device
            you can read, verify, and (carefully) write.
          </p>
        </div>

        <div className="space-y-8 text-sm leading-7 text-surface-300">
          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">The problem</h2>
            <p>
              A wire-harness cut/strip/crimp machine has a PLC with hundreds of holding registers.
              Setpoints, live values, counters, alarms — all integers at numeric addresses. The
              vendor docs are thin or gone, and a register that reads <code>5000</code> could be a
              cut length of 1250&nbsp;mm at a 0.25 scale, a speed, or a piece counter. Connecting
              such a machine to MES/ERP normally means a paid integrator and weeks of guesswork.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Turn it on</h2>
            <p>
              Run the agent with the machine engine enabled on a box that can reach the PLC (a
              laptop on the line, or a Raspberry Pi appliance next to the machine):
            </p>
            <pre className="mt-2 overflow-x-auto rounded-lg bg-surface-900 p-3 text-xs text-surface-300">
{`yaver serve --machine`}
            </pre>
            <p className="mt-4">
              <code>machine_status</code> reports what the box can do: Modbus-TCP works everywhere;
              passive serial-bus sniffing (Modbus-RTU) is supported on Linux.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">1 — Observe the bus</h2>
            <p>Two ways to collect evidence, both non-invasive:</p>
            <ul className="mt-3 list-disc space-y-1 pl-6 text-surface-400">
              <li>
                <strong>Passive RTU sniff</strong> — <code>machine_sniff_start</code> with a serial
                <code> device</code> (e.g. <code>/dev/ttyUSB0</code>) taps the bus <em>read-only</em>
                and builds a candidate schematic from the traffic it sees. No hardware? Open a manual
                session and replay a capture into it with <code>machine_feed</code> (hex bytes).
              </li>
              <li>
                <strong>Active TCP read-scan</strong> — <code>machine_scan_registers</code> reads a
                contiguous range over Modbus-TCP (<code>fc 3</code>=holding, <code>4</code>=input),
                read-only, recording presence and values.
              </li>
            </ul>
            <p className="mt-4">
              <code>machine_sniff_status</code> snapshots the live candidate schematic without
              stopping; <code>machine_sniff_stop</code> returns the final one.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">2 — Let the AI label the registers</h2>
            <p>
              <code>machine_understand</code> pipes the candidate schematic — plus, crucially, any
              <em> ground-truth labels</em> from the job that was running (e.g.
              <code> {"{lengthMm:1250, qty:500, stripL:6}"}</code>) — to a vision/LLM model. It returns
              a labelled map: for each register a human <code>name</code> (cut_length, strip_left,
              quantity, speed, crimp_height, alarm_word, piece_counter…), the engineering
              <code> unit</code>, a numeric <code>scale</code> (observed × scale = engineering value),
              a <code>kind</code> (setpoint / live / counter / alarm), and a confidence.
            </p>
            <p className="mt-4">
              The ground-truth anchor is what makes it reliable: if a label says
              <code> lengthMm=1250</code> and a setpoint register reads <code>5000</code>, the model
              infers <code>scale 0.25</code> and names it <code>cut_length</code>. The AI runs through
              Yaver&apos;s normal provider chain — cloud-brain first, with env/local (Ollama) fallback —
              so it can run fully on-prem.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">3 — Read, verify, and (carefully) write</h2>
            <ul className="list-disc space-y-1 pl-6 text-surface-400">
              <li><code>machine_read</code> — read specific registers over Modbus-TCP for current values / verification.</li>
              <li>
                <code>machine_write</code> — write one holding register, then <strong>read it back to
                verify</strong>. It is <strong>range-clamped</strong> (you pass min/max; out-of-range is
                refused), <strong>high-risk-gated</strong> (<code>allowHighRisk=true</code> only after
                upstream approval), and <strong>safety functions are never network-writable</strong>.
              </li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">4 — Sync the device to your system of record</h2>
            <p>
              <code>machine_sync</code> pushes a device heartbeat plus the learned schematic (and
              optional telemetry samples) over org-secret machine-edge routes, where it&apos;s
              stored as a typed device with a manual and a telemetry stream. The opaque controller is
              now a first-class machine in your system of record — discovered, not hand-mapped.
            </p>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">Why it&apos;s safe by construction</h2>
            <ul className="list-disc space-y-1 pl-6 text-surface-400">
              <li>Discovery is read-only: sniffing is passive; scanning only reads.</li>
              <li>Every write is verified by read-back and clamped to a caller-supplied range.</li>
              <li>Safety registers are excluded from network writes entirely.</li>
              <li>The whole flow runs on a box <em>you</em> own, on <em>your</em> network, with AI that can be local.</li>
            </ul>
          </section>

          <section>
            <h2 className="mb-3 text-xl font-semibold text-surface-100">The bigger picture</h2>
            <p>
              This is the same Yaver idea applied to hardware: meet the legacy system where it is,
              learn it with AI, and bridge it forward without a rip-and-replace. A Pi appliance on the
              line, a few minutes of sniffing against a known job, and a machine that&apos;s spoken
              nothing but raw integers for a decade becomes something your software can actually read.
            </p>
          </section>
        </div>
      </article>
    </div>
  );
}
