"use client";

import Link from "next/link";

function Terminal({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="terminal">
      <div className="terminal-header">
        <div className="terminal-dot bg-[#ff5f57]" />
        <div className="terminal-dot bg-[#febc2e]" />
        <div className="terminal-dot bg-[#28c840]" />
        <span className="ml-3 text-xs text-surface-500">{title}</span>
      </div>
      <div className="terminal-body space-y-2 text-[13px]">{children}</div>
    </div>
  );
}

function Cmd({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <span className="text-surface-400">$</span>{" "}
      <span className="text-surface-200 select-all">{children}</span>
    </div>
  );
}

function Comment({ children }: { children: React.ReactNode }) {
  return <div className="text-surface-500">{children}</div>;
}

function Output({ children }: { children: React.ReactNode }) {
  return <div className="text-green-400/80 pl-2">{children}</div>;
}

function Divider() {
  return <div className="h-px bg-surface-800/60" />;
}

function SectionHeading({
  id,
  children,
}: {
  id: string;
  children: React.ReactNode;
}) {
  return (
    <h2
      id={id}
      className="mb-4 text-2xl font-bold text-surface-50 md:text-3xl"
    >
      {children}
    </h2>
  );
}

function SubHeading({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="mb-3 text-lg font-semibold text-surface-100">{children}</h3>
  );
}

function Prose({ children }: { children: React.ReactNode }) {
  return (
    <p className="mb-6 text-sm leading-relaxed text-surface-400">{children}</p>
  );
}

function InlineCode({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-300">
      {children}
    </code>
  );
}

export default function DevelopersPage() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        {/* Back link */}
        <Link
          href="/"
          className="mb-12 inline-block text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to home
        </Link>

        {/* Header */}
        <div className="mb-16">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            Developer Guide
          </h1>
          <p className="text-sm leading-relaxed text-surface-400">
            Everything you need to build, run, and contribute to Yaver. This
            guide covers project structure, build instructions, the backend API,
            data model, relay protocol, and testing.
          </p>
        </div>

        {/* Table of contents */}
        <div className="mb-16 rounded-xl border border-surface-800 bg-surface-900 p-6">
          <h3 className="mb-4 text-sm font-semibold text-surface-200">
            On this page
          </h3>
          <nav className="space-y-2 text-sm">
            {[
              ["project-structure", "Project Structure"],
              ["software-stack", "Software Stack"],
              ["network-stack", "Network Stack"],
              ["build-instructions", "Build Instructions"],
              ["backend-api", "Backend API Reference"],
              ["data-model", "What's Stored in Convex"],
              ["relay-protocol", "Relay Server Protocol"],
              ["relay-hot-reload", "Relay Hot-Reload & Health"],
              ["token-refresh", "Token Refresh & Re-Auth"],
              ["doctor", "System Health Check (yaver doctor)"],
              ["running-tests", "Running Tests"],
              ["integration-test-suite", "Integration Test Suite"],
              ["session-transfer", "Session Transfer"],
              ["pr-rules", "Pull Request Rules"],
              ["feedback-sdk", "Feedback SDK & Test Loop"],
              ["sdk-token-security", "SDK Token Security"],
              ["sdk", "SDK — Embed Yaver"],
              ["demo-app", "Demo App (AcmeStore)"],
              ["contributing", "Contributing"],
            ].map(([id, label]) => (
              <a
                key={id}
                href={`#${id}`}
                className="block text-surface-500 hover:text-surface-200"
              >
                {label}
              </a>
            ))}
          </nav>
        </div>

        {/* ─── Section 1: Project Structure ─── */}
        <section className="mb-20">
          <SectionHeading id="project-structure">
            Project Structure
          </SectionHeading>
          <Prose>
            Yaver is a monorepo with five main components. Each can be built and
            run independently.
          </Prose>

          <div className="mb-8">
            <Terminal title="project-structure">
              <pre className="text-surface-300">
                {`yaver/
├── desktop/agent/    # CLI agent (Go)
├── mobile/           # Mobile app (React Native / Expo)
├── relay/            # QUIC relay server (Go)
├── backend/          # Convex backend (auth + device registry)
├── web/              # Landing page + dashboard (Next.js)
├── scripts/          # Build & deploy scripts
└── keys/             # Private keys (gitignored)`}
              </pre>
            </Terminal>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                desktop/agent/
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                The Go CLI binary. Runs an HTTP server on{" "}
                <InlineCode>0.0.0.0:18080</InlineCode>, a QUIC server on{" "}
                <InlineCode>0.0.0.0:4433</InlineCode>, broadcasts LAN beacons
                on UDP <InlineCode>19837</InlineCode>, and manages AI runner
                processes (Claude Code, Codex, Aider, Ollama, etc.) via tmux.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                mobile/
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                React Native app for iOS and Android. Connects to the desktop
                agent directly over LAN or through the relay. Built with native
                tooling (xcodebuild / Gradle), not Expo CLI.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                relay/
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Lightweight QUIC relay server in Go. Pass-through proxy for NAT
                traversal &mdash; stores nothing. Deployed to Hetzner VPS via
                Docker.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                backend/
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Convex backend for auth (Google, Apple, Microsoft, email/password),
                device registry, platform config, and usage analytics. No task
                data or code is ever stored here.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                web/
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Next.js 15 landing page deployed on Vercel at{" "}
                <InlineCode>yaver.io</InlineCode>. Handles OAuth callbacks for
                desktop CLI auth flow.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Software Stack ─── */}
        <section className="mb-20">
          <SectionHeading id="software-stack">Software Stack</SectionHeading>
          <Prose>
            Yaver&apos;s software stack from high-level user-facing components down
            to low-level protocols and libraries.
          </Prose>

          <div className="mb-8">
            <Terminal title="stack-overview">
              <pre className="text-surface-300">
                {`┌───────────────────────────────────────────────────────┐
│                      User Layer                       │
│                                                       │
│  Mobile App          CLI Agent          Web Dashboard  │
│  (React Native)      (Go binary)        (Next.js)     │
│  iOS + Android       macOS/Linux/Win    Vercel         │
└──────────────┬─────────────┬─────────────┬────────────┘
               │             │             │
               ▼             ▼             ▼`}
              </pre>
            </Terminal>
          </div>

          {/* High Level */}
          <SubHeading>High Level &mdash; User-Facing Components</SubHeading>

          <div className="mb-6 space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Mobile App (React Native)
              </h4>
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>
                  Framework: Expo (prebuild, no EAS) + React Native
                </li>
                <li>
                  Navigation: <InlineCode>expo-router</InlineCode> (file-based)
                </li>
                <li>
                  State: React Context (<InlineCode>DeviceContext</InlineCode>,{" "}
                  <InlineCode>AuthContext</InlineCode>)
                </li>
                <li>
                  Storage: AsyncStorage (relay config, settings),{" "}
                  <InlineCode>SecureStore</InlineCode> (tokens)
                </li>
                <li>
                  Networking: <InlineCode>fetch</InlineCode> API for HTTP,{" "}
                  <InlineCode>react-native-udp</InlineCode> for LAN beacon
                </li>
                <li>
                  Build: xcodebuild (iOS), Gradle (Android) &mdash; always
                  native, never Expo Go
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                CLI Agent (Go)
              </h4>
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>Single binary, no runtime dependencies</li>
                <li>AI session management via tmux</li>
                <li>
                  HTTP server on port <InlineCode>18080</InlineCode> (task API,
                  health, status)
                </li>
                <li>
                  QUIC server on port <InlineCode>4433</InlineCode> (direct
                  connections)
                </li>
                <li>
                  UDP beacon broadcaster on port{" "}
                  <InlineCode>19837</InlineCode>
                </li>
                <li>
                  Config stored in{" "}
                  <InlineCode>~/.yaver/config.json</InlineCode>
                </li>
                <li>
                  Auth token from OAuth flow via local HTTP callback server
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Web Dashboard (Next.js)
              </h4>
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>Next.js 15 with App Router</li>
                <li>
                  Tailwind CSS with custom{" "}
                  <InlineCode>surface-*</InlineCode> color palette
                </li>
                <li>Client-side auth (tokens in localStorage + cookies)</li>
                <li>Deployed on Vercel (static + serverless API routes)</li>
                <li>
                  API routes handle OAuth flow (Google, Apple, Microsoft)
                </li>
              </ul>
            </div>
          </div>

          {/* Mid Level */}
          <SubHeading>Mid Level &mdash; Infrastructure Components</SubHeading>

          <div className="mb-6 space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Relay Server (Go)
              </h4>
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>Single binary, ~5MB</li>
                <li>
                  QUIC listener (<InlineCode>quic-go</InlineCode>) on UDP port{" "}
                  <InlineCode>4433</InlineCode> &mdash; accepts agent tunnels
                </li>
                <li>
                  HTTP server on TCP port <InlineCode>8443</InlineCode> &mdash;
                  accepts mobile/CLI client requests
                </li>
                <li>
                  In-memory device map (deviceID &rarr; QUIC connection)
                </li>
                <li>
                  Password authentication on both QUIC registration and HTTP
                  proxy
                </li>
                <li>
                  Self-signed TLS for QUIC (agents skip verification)
                </li>
                <li>
                  Production: nginx terminates HTTPS &rarr; proxy to HTTP 8443
                </li>
                <li>
                  Docker image based on <InlineCode>alpine:3.19</InlineCode>{" "}
                  (~15MB)
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Auth Backend (Convex)
              </h4>
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>Serverless backend-as-a-service (convex.dev)</li>
                <li>
                  Schema: <InlineCode>users</InlineCode>,{" "}
                  <InlineCode>sessions</InlineCode>,{" "}
                  <InlineCode>devices</InlineCode>,{" "}
                  <InlineCode>userSettings</InlineCode>,{" "}
                  <InlineCode>platformConfig</InlineCode>
                </li>
                <li>HTTP actions for OAuth callbacks</li>
                <li>
                  Mutations for writes (register device, update settings)
                </li>
                <li>Queries for reads (list devices, get config)</li>
                <li>Real-time subscriptions (not used currently)</li>
                <li>Deployed to Convex cloud (EU West)</li>
              </ul>
            </div>
          </div>

          {/* Low Level */}
          <SubHeading>Low Level &mdash; Libraries &amp; Protocols</SubHeading>

          <div className="mb-6 space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Go Dependencies
              </h4>
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>
                  <InlineCode>github.com/quic-go/quic-go</InlineCode> &mdash;
                  QUIC implementation (RFC 9000)
                </li>
                <li>
                  Standard library:{" "}
                  <InlineCode>crypto/tls</InlineCode>,{" "}
                  <InlineCode>net/http</InlineCode>,{" "}
                  <InlineCode>encoding/json</InlineCode>,{" "}
                  <InlineCode>os/exec</InlineCode>
                </li>
                <li>
                  No web framework &mdash; raw{" "}
                  <InlineCode>net/http</InlineCode> handlers
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                JavaScript / TypeScript Dependencies
              </h4>
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>React 19, Next.js 15, React Native</li>
                <li>
                  <InlineCode>jose</InlineCode> &mdash; JWT handling for Apple
                  OAuth
                </li>
                <li>
                  <InlineCode>convex</InlineCode> &mdash; Convex client SDK
                </li>
                <li>
                  <InlineCode>react-native-udp</InlineCode> &mdash; UDP socket
                  for LAN beacon
                </li>
                <li>
                  <InlineCode>expo-secure-store</InlineCode> &mdash; secure
                  token storage on mobile
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Protocols Used
              </h4>
              <ul className="space-y-1 text-sm leading-relaxed text-surface-400">
                <li>
                  QUIC (RFC 9000) &mdash; agent-to-relay tunnel, multiplexed
                  streams over UDP
                </li>
                <li>
                  HTTP/1.1 &mdash; mobile-to-relay proxy, CLI HTTP API
                </li>
                <li>
                  TLS 1.3 &mdash; transport encryption (QUIC and HTTPS)
                </li>
                <li>
                  UDP broadcast &mdash; LAN device discovery beacon
                </li>
                <li>
                  OAuth 2.0 / OpenID Connect &mdash; authentication (Apple,
                  Google, Microsoft)
                </li>
                <li>
                  JSON &mdash; all API payloads, config files, protocol
                  messages
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Network Stack ─── */}
        <section className="mb-20">
          <SectionHeading id="network-stack">Network Stack</SectionHeading>
          <Prose>
            How devices discover each other and establish connections, from
            high-level discovery down to transport protocols.
          </Prose>

          {/* Discovery Layer */}
          <SubHeading>Discovery Layer</SubHeading>
          <Prose>
            Three mechanisms are tried in order. The first successful connection
            wins.
          </Prose>

          <div className="mb-8">
            <Terminal title="connection-priority">
              <pre className="text-surface-300">
                {`┌──────────────────────────────────────────────────────────────────────┐
│                    CONNECTION PRIORITY                                │
│                                                                      │
│  1. LAN Beacon (direct)  ──  ~5ms   ── same WiFi, instant discovery  │
│  2. Convex IP (direct)   ──  ~5ms   ── known IP from device registry │
│  3. QUIC Relay (proxied) ──  ~50ms  ── roaming, NAT traversal        │
│                                                                      │
│  Silent roaming: transitions between layers are invisible to user    │
└──────────────────────────────────────────────────────────────────────┘`}
              </pre>
            </Terminal>
          </div>

          {/* LAN Beacon */}
          <div className="mb-8">
            <h4 className="mb-3 text-sm font-medium text-surface-200">
              1. LAN Beacon (UDP Broadcast)
            </h4>
            <Terminal title="lan-beacon-protocol">
              <pre className="text-surface-300">
                {`CLI Agent                                     Mobile App
    │                                                 │
    ├── Every 3s: UDP broadcast ──────────────────►   │
    │   dst: 255.255.255.255:19837                    │
    │   payload: {"v":1,"id":"dcbfdc50",              │
    │             "p":18080,"n":"MacBook",             │
    │             "th":"a1b2c3d4"}                     │
    │                                                 │
    │   Mobile matches:                               │
    │   1. beacon.id ∈ Convex device list             │
    │   2. beacon.th == SHA256(userId)[:8]             │
    │                                                 │
    │   If match → direct HTTP to beacon IP:port      │
    └─────────────────────────────────────────────────┘`}
              </pre>
            </Terminal>
            <div className="mt-3 space-y-1 text-sm text-surface-400">
              <p>
                Auth-aware: <InlineCode>th</InlineCode> field = first 8 hex
                chars of SHA-256(userId). Only same-user devices match, even on
                shared WiFi.
              </p>
              <p>
                Timeout: 10s without beacon &rarr; device marked as not local.
              </p>
              <p>
                Graceful degradation: if UDP socket fails &rarr; falls back to
                Convex.
              </p>
            </div>
          </div>

          {/* Convex Device Registry */}
          <div className="mb-8">
            <h4 className="mb-3 text-sm font-medium text-surface-200">
              2. Convex Device Registry (HTTP Polling)
            </h4>
            <Terminal title="convex-device-registry">
              <pre className="text-surface-300">
                {`CLI Agent                 Convex                  Mobile App
    │                         │                          │
    ├── POST /devices/register ──►│                      │
    │   {deviceId, hostname,      │                      │
    │    platform, localIP, port} │                      │
    │                             │                      │
    ├── POST /devices/heartbeat ──►│ (every 2min)        │
    │   {localIP, ...}            │                      │
    │                             │                      │
    │                             │◄── GET /devices ─────┤
    │                             │──► device list ──────►│
    │                             │                      │
    │   Mobile checks: isOnline && lastHeartbeat < 5min  │
    │   If private IP → try direct HTTP connection       │
    └────────────────────────────────────────────────────┘`}
              </pre>
            </Terminal>
          </div>

          {/* Relay */}
          <div className="mb-8">
            <h4 className="mb-3 text-sm font-medium text-surface-200">
              3. Relay Server (QUIC Tunnel + HTTP Proxy)
            </h4>
            <Terminal title="relay-tunnel-protocol">
              <pre className="text-surface-300">
                {`CLI Agent               Relay Server               Mobile App
    │                        │                             │
    │── QUIC connect ───────►│                             │
    │   (outbound, UDP 4433) │                             │
    │                        │                             │
    │── RegisterMsg ────────►│                             │
    │   {deviceId, token,    │                             │
    │    password}            │                             │
    │                        │◄── HTTP request ────────────┤
    │                        │    GET /d/{id}/health        │
    │                        │    X-Relay-Password: ..      │
    │                        │                             │
    │◄── QUIC stream ───────┤    (proxied)                 │
    │    TunnelRequest       │                             │
    │                        │                             │
    │── TunnelResponse ─────►│──── HTTP response ─────────►│
    └────────────────────────┘─────────────────────────────┘`}
              </pre>
            </Terminal>
          </div>

          {/* Transport Layer */}
          <SubHeading>Transport Layer</SubHeading>

          <div className="mb-8">
            <Terminal title="transport-paths">
              <pre className="text-surface-300">
                {`Direct connections (LAN / same network):
─────────────────────────────────────────
Mobile ──HTTP──► CLI Agent (:18080)
                 No encryption (local network)
                 Latency: ~5ms

Relay connections (different networks):
─────────────────────────────────────────
Mobile ──HTTPS──► nginx ──HTTP──► Relay (:8443)
                  TLS 1.3          │
                                   │ (in-memory proxy)
                                   │
CLI Agent ◄──QUIC──────────────── Relay (:4433)
              TLS 1.3 (self-signed)
              Latency: ~50ms`}
              </pre>
            </Terminal>
          </div>

          {/* Connection State Machine */}
          <SubHeading>Connection State Machine</SubHeading>

          <div className="mb-8">
            <Terminal title="connection-state-machine">
              <pre className="text-surface-300">
                {`           ┌──────────────┐
           │ DISCONNECTED │
           └──────┬───────┘
                  │ connect()
                  ▼
           ┌──────────────┐
           │  CONNECTING  │
           └──────┬───────┘
                  │
        ┌─────────┼──────────┐
        ▼         ▼          ▼
   ┌────────┐ ┌────────┐ ┌────────┐
   │  LAN   │ │   IP   │ │ RELAY  │
   │ beacon │ │ direct │ │        │
   │ probe  │ │  (2s)  │ │  try   │
   │  (2s)  │ │        │ │  each  │
   └───┬────┘ └───┬────┘ └───┬────┘
       │          │          │
       ▼          ▼          ▼
   ┌──────────────────────────────┐
   │          CONNECTED           │
   │     mode: direct | relay     │
   └──────────────┬───────────────┘
                  │ network change
                  │ or disconnect
                  ▼
   ┌──────────────────────────────┐
   │        RECONNECTING          │
   │     exponential backoff      │
   │     1s → 2s → 4s → 30s max  │
   └──────────────────────────────┘`}
              </pre>
            </Terminal>
          </div>

          {/* Protocol Messages */}
          <SubHeading>Protocol Messages</SubHeading>

          <div className="mb-6 space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                RegisterMsg (agent &rarr; relay, QUIC stream)
              </h4>
              <Terminal title="register-msg">
                <pre className="text-surface-300">
                  {`{
  "type": "register",
  "deviceId": "dcbfdc50-...",
  "token": "eyJhbG...",
  "password": "relay-secret"
}`}
                </pre>
              </Terminal>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                TunnelRequest (relay &rarr; agent, QUIC stream)
              </h4>
              <Terminal title="tunnel-request">
                <pre className="text-surface-300">
                  {`{
  "id": "req-uuid",
  "method": "GET",
  "path": "/health",
  "query": "",
  "headers": {"Authorization": "Bearer ..."},
  "body": []
}`}
                </pre>
              </Terminal>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                TunnelResponse (agent &rarr; relay, QUIC stream)
              </h4>
              <Terminal title="tunnel-response">
                <pre className="text-surface-300">
                  {`{
  "id": "req-uuid",
  "statusCode": 200,
  "headers": {"Content-Type": "application/json"},
  "body": [base64-encoded-bytes]
}`}
                </pre>
              </Terminal>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                LAN Beacon (CLI &rarr; broadcast, UDP)
              </h4>
              <Terminal title="lan-beacon-msg">
                <pre className="text-surface-300">
                  {`{"v":1,"id":"dcbfdc50","p":18080,"n":"MacBook-Air","th":"a1b2c3d4"}`}
                </pre>
              </Terminal>
            </div>
          </div>

          {/* SSE Streaming */}
          <SubHeading>SSE Streaming (Server-Sent Events)</SubHeading>
          <Prose>
            For real-time task output streaming, the relay supports SSE
            pass-through.
          </Prose>
          <div className="space-y-2 text-sm text-surface-400">
            <p>
              Path pattern: <InlineCode>/tasks/{"{id}"}/output</InlineCode> with{" "}
              <InlineCode>GET</InlineCode> method
            </p>
            <p>
              Relay detects SSE by path and switches to streaming mode
            </p>
            <p>
              QUIC stream stays open, relay flushes chunks as they arrive
            </p>
            <p>
              Timeout: 10 minutes (vs 30s for regular requests)
            </p>
          </div>
        </section>

        {/* ─── Build Instructions ─── */}
        <section className="mb-20">
          <SectionHeading id="build-instructions">
            Build Instructions
          </SectionHeading>
          <Prose>
            Each component builds independently. You only need to build the
            components you&apos;re working on.
          </Prose>

          {/* CLI */}
          <SubHeading>Build the CLI</SubHeading>
          <div className="mb-8">
            <Terminal title="cli-build">
              <Cmd>cd desktop/agent</Cmd>
              <Cmd>go build -o yaver .</Cmd>
              <Divider />
              <Comment># Cross-compile for other platforms</Comment>
              <Cmd>
                GOOS=linux GOARCH=amd64 go build -o yaver-linux-amd64 .
              </Cmd>
              <Cmd>
                GOOS=windows GOARCH=amd64 go build -o yaver-windows-amd64.exe .
              </Cmd>
              <Cmd>
                GOOS=darwin GOARCH=arm64 go build -o yaver-darwin-arm64 .
              </Cmd>
              <Divider />
              <Comment># Run from source during development</Comment>
              <Cmd>go run . serve</Cmd>
              <Output>Listening on 0.0.0.0:18080</Output>
            </Terminal>
          </div>

          {/* Mobile */}
          <SubHeading>Build the Mobile App</SubHeading>
          <div className="mb-4">
            <p className="mb-3 text-sm font-medium text-surface-300">iOS</p>
            <Terminal title="ios-build">
              <Cmd>cd mobile</Cmd>
              <Cmd>npx expo prebuild --platform ios</Cmd>
              <Cmd>cd ios &amp;&amp; pod install</Cmd>
              <Divider />
              <Comment># Build via Xcode or xcodebuild</Comment>
              <Cmd>
                xcodebuild -workspace Yaver.xcworkspace -scheme Yaver
                -configuration Release archive
              </Cmd>
            </Terminal>
          </div>
          <div className="mb-8">
            <p className="mb-3 text-sm font-medium text-surface-300">
              Android
            </p>
            <Terminal title="android-build">
              <Cmd>cd mobile</Cmd>
              <Cmd>npx expo prebuild --platform android</Cmd>
              <Cmd>cd android</Cmd>
              <Divider />
              <Comment># Requires Java 17 (Gradle 8.10 doesn&apos;t support Java 24)</Comment>
              <Cmd>
                {"JAVA_HOME=$(/usr/libexec/java_home -v 17) ./gradlew bundleRelease"}
              </Cmd>
              <Output>
                app/build/outputs/bundle/release/app-release.aab
              </Output>
            </Terminal>
          </div>

          {/* Relay */}
          <SubHeading>Build the Relay Server</SubHeading>
          <div className="mb-8">
            <Terminal title="relay-build">
              <Cmd>cd relay</Cmd>
              <Cmd>go build -o yaver-relay .</Cmd>
              <Divider />
              <Comment># Or use Docker</Comment>
              <Cmd>docker compose build</Cmd>
              <Cmd>docker compose up -d</Cmd>
            </Terminal>
          </div>

          {/* Website */}
          <SubHeading>Build the Website</SubHeading>
          <div className="mb-8">
            <Terminal title="web-build">
              <Cmd>cd web</Cmd>
              <Cmd>npm install</Cmd>
              <Cmd>npm run build</Cmd>
              <Divider />
              <Comment># Local dev server</Comment>
              <Cmd>npm run dev</Cmd>
              <Output>http://localhost:3000</Output>
            </Terminal>
          </div>

          {/* Convex */}
          <SubHeading>Deploy Your Own Convex Backend (Optional)</SubHeading>
          <Prose>
            Most contributors don&apos;t need their own backend &mdash; the
            hosted instance works fine. But if you want full control, you can
            deploy your own with a free Convex account.
          </Prose>
          <div className="mb-8">
            <Terminal title="convex-deploy">
              <Cmd>cd backend</Cmd>
              <Cmd>npm install</Cmd>
              <Cmd>npx convex dev</Cmd>
              <Output>Convex dev server running</Output>
            </Terminal>
          </div>

          <div className="rounded-xl border border-surface-800 bg-surface-900 p-6">
            <h4 className="mb-2 text-sm font-medium text-surface-200">
              Note on the Convex backend
            </h4>
            <p className="text-sm leading-relaxed text-surface-400">
              The backend handles OAuth and device discovery only. No task data,
              code, or AI output is stored. If you deploy your own, update the{" "}
              <InlineCode>CONVEX_SITE_URL</InlineCode> environment variable in
              the web and mobile projects to point to your instance.
            </p>
          </div>
        </section>

        {/* ─── Section 3: Backend API Reference ─── */}
        <section className="mb-20">
          <SectionHeading id="backend-api">
            Backend API Reference
          </SectionHeading>
          <Prose>
            The Convex backend exposes HTTP endpoints that the CLI and mobile
            app call. All authenticated endpoints require a{" "}
            <InlineCode>Authorization: Bearer &lt;token&gt;</InlineCode> header.
            The base URL is your Convex site URL (set via the{" "}
            <InlineCode>NEXT_PUBLIC_CONVEX_SITE_URL</InlineCode>{" "}
            environment variable).
          </Prose>

          {/* Auth endpoints */}
          <SubHeading>Authentication</SubHeading>
          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Method
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Path
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Auth
                  </th>
                  <th className="pb-3 font-medium text-surface-200">
                    Description
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/auth/signup</InlineCode>
                  </td>
                  <td className="py-3 pr-4">No</td>
                  <td className="py-3">Email/password signup</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/auth/login</InlineCode>
                  </td>
                  <td className="py-3 pr-4">No</td>
                  <td className="py-3">Email/password login</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/auth/apple-native</InlineCode>
                  </td>
                  <td className="py-3 pr-4">No</td>
                  <td className="py-3">
                    Native iOS Apple Sign-In (identity token)
                  </td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/auth/validate</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Validate bearer token, return user info</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/auth/update-profile</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Update user profile (name)</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/auth/delete-account</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Delete user account and all data</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/auth/upsert-user</InlineCode>
                  </td>
                  <td className="py-3 pr-4">No</td>
                  <td className="py-3">
                    Create or update user (called from web OAuth flow)
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          {/* Device endpoints */}
          <SubHeading>Devices</SubHeading>
          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Method
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Path
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Auth
                  </th>
                  <th className="pb-3 font-medium text-surface-200">
                    Description
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/register</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Register a desktop agent device</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/heartbeat</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">
                    Device heartbeat (every 2 min, includes runner info)
                  </td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/list</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">List user&apos;s registered devices</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/offline</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Mark device as offline</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/remove</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Remove a device from registry</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/runner-down</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Set runner down/up flag on a device</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/metrics</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Report CPU/RAM metrics (every 60s)</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/metrics</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">
                    Get metrics for a device (?deviceId=xxx)
                  </td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/event</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">
                    Record device event (crash, restart, etc.)
                  </td>
                </tr>
                <tr>
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/devices/events</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">
                    Get recent events (?deviceId=xxx)
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          {/* Settings & config endpoints */}
          <SubHeading>Settings &amp; Configuration</SubHeading>
          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Method
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Path
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Auth
                  </th>
                  <th className="pb-3 font-medium text-surface-200">
                    Description
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/settings</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Get user settings (runner, relay config)</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/settings</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Update user settings</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/config</InlineCode>
                  </td>
                  <td className="py-3 pr-4">No</td>
                  <td className="py-3">
                    Platform config (relay servers, runners, models)
                  </td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/runners</InlineCode>
                  </td>
                  <td className="py-3 pr-4">No</td>
                  <td className="py-3">List available AI runners</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/models</InlineCode>
                  </td>
                  <td className="py-3 pr-4">No</td>
                  <td className="py-3">List available AI models per runner</td>
                </tr>
              </tbody>
            </table>
          </div>

          {/* Usage & logging endpoints */}
          <SubHeading>Usage &amp; Logging</SubHeading>
          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Method
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Path
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Auth
                  </th>
                  <th className="pb-3 font-medium text-surface-200">
                    Description
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/usage/record</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">
                    Record runner usage when a task finishes
                  </td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/usage</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">
                    Usage summary with daily aggregation (?since=epoch)
                  </td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/survey/submit</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Token</td>
                  <td className="py-3">Submit developer survey</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>GET</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/downloads/list</InlineCode>
                  </td>
                  <td className="py-3 pr-4">No</td>
                  <td className="py-3">List all available CLI downloads</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/dev/log</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Optional</td>
                  <td className="py-3">Write a developer debug log</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4">
                    <InlineCode>POST</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>/mobile/log</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Optional</td>
                  <td className="py-3">Write a mobile stream debug log</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        {/* ─── Section 4: Data Model ─── */}
        <section className="mb-20">
          <SectionHeading id="data-model">
            What&apos;s Stored in Convex
          </SectionHeading>
          <Prose>
            Convex is purely for auth and device registry. No task data, no
            code, no AI output, no logs are stored. Everything AI-related flows
            peer-to-peer between mobile and desktop agent.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                users
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                User accounts. Fields: <InlineCode>email</InlineCode>,{" "}
                <InlineCode>fullName</InlineCode>,{" "}
                <InlineCode>provider</InlineCode> (google / apple / microsoft /
                email), <InlineCode>providerId</InlineCode>,{" "}
                <InlineCode>passwordHash</InlineCode> (email auth only),{" "}
                <InlineCode>avatarUrl</InlineCode>.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                sessions
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Auth sessions. Fields: <InlineCode>tokenHash</InlineCode>{" "}
                (SHA-256 of bearer token), <InlineCode>userId</InlineCode>,{" "}
                <InlineCode>expiresAt</InlineCode>. Tokens expire after 30 days.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                devices
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Registered desktop agents. Fields:{" "}
                <InlineCode>deviceId</InlineCode>,{" "}
                <InlineCode>name</InlineCode> (hostname),{" "}
                <InlineCode>platform</InlineCode> (macos / windows / linux),{" "}
                <InlineCode>quicHost</InlineCode>,{" "}
                <InlineCode>quicPort</InlineCode>,{" "}
                <InlineCode>isOnline</InlineCode>,{" "}
                <InlineCode>runners</InlineCode> (active AI processes),{" "}
                <InlineCode>lastHeartbeat</InlineCode>.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                userSettings
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Per-user preferences. Fields:{" "}
                <InlineCode>forceRelay</InlineCode>,{" "}
                <InlineCode>runnerId</InlineCode>,{" "}
                <InlineCode>customRunnerCommand</InlineCode>,{" "}
                <InlineCode>relayUrl</InlineCode> (custom relay),{" "}
                <InlineCode>relayPassword</InlineCode>.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                platformConfig
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Global config managed by admins. Key-value pairs including{" "}
                <InlineCode>relay_servers</InlineCode> (JSON array of relay
                endpoints), <InlineCode>cli_version</InlineCode>, and{" "}
                <InlineCode>mobile_version</InlineCode>.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                aiRunners &amp; aiModels
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Available AI runners (Claude Code, Codex, Aider, Ollama, etc.)
                and their supported models. Managed centrally, fetched by
                clients via <InlineCode>GET /config</InlineCode>.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                deviceMetrics &amp; deviceEvents
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Per-minute CPU/RAM metrics (last 1 hour kept) and device
                lifecycle events (crash, restart, OOM, started, stopped). Used
                for the monitoring dashboard.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                runnerUsage &amp; dailyTaskCounts
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Usage tracking: how long each AI runner ran per task, and daily
                task counts per user. Used for analytics.
              </p>
            </div>
          </div>

          <div className="mt-6 rounded-xl border border-surface-800 bg-surface-900 p-6">
            <h4 className="mb-2 text-sm font-medium text-surface-200">
              Privacy guarantee
            </h4>
            <p className="text-sm leading-relaxed text-surface-400">
              No task data, no source code, no AI output, no chat logs are ever
              stored in Convex or on the relay server. The backend is purely for
              auth, device discovery, and usage analytics. All AI interaction
              flows peer-to-peer between your phone and your dev machine.
            </p>
          </div>
        </section>

        {/* ─── Section 5: Relay Protocol ─── */}
        <section className="mb-20">
          <SectionHeading id="relay-protocol">
            Relay Server Protocol
          </SectionHeading>
          <Prose>
            The relay server is a pass-through QUIC proxy that enables NAT
            traversal. It stores nothing and sees encrypted traffic only.
          </Prose>

          <div className="mb-8">
            <Terminal title="relay-protocol">
              <pre className="text-surface-300">
                {`Desktop Agent                  Relay Server                   Mobile App
     │                              │                               │
     │── QUIC connect (outbound) ──►│                               │
     │── RegisterMsg ──────────────►│                               │
     │   { deviceId, token, pass }  │                               │
     │                              │◄── HTTP request ──────────────│
     │                              │    GET /d/{deviceId}/health    │
     │◄── forward via QUIC tunnel ──│                               │
     │── response ─────────────────►│── forward HTTP response ─────►│
     │                              │                               │`}
              </pre>
            </Terminal>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Agent &rarr; Relay (QUIC)
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                The CLI agent opens an outbound QUIC connection to the relay on
                port <InlineCode>4433/udp</InlineCode>. This is outbound-only,
                so it works behind NAT without any port forwarding. The agent
                sends a <InlineCode>RegisterMsg</InlineCode> with its{" "}
                <InlineCode>deviceId</InlineCode>, auth token, and relay
                password. The relay maps the device ID to this QUIC tunnel.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Mobile &rarr; Relay (HTTP)
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                The mobile app sends short-lived HTTP requests to{" "}
                <InlineCode>
                  {"https://<relay>/d/{deviceId}/<path>"}
                </InlineCode>
                . The relay looks up the QUIC tunnel for that device ID and
                forwards the request through the tunnel to the agent. The
                response comes back the same way.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Reconnection
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                If the QUIC tunnel drops, the agent reconnects with exponential
                backoff (1s &rarr; 2s &rarr; 4s &rarr; 8s &rarr; max 30s). The
                mobile app tries all configured relay servers in priority order
                with a 5-second timeout per server.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Connection priority
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; <span className="text-surface-200">LAN Beacon</span>{" "}
                  &mdash; direct HTTP, ~5ms (same WiFi)
                </li>
                <li>
                  &bull;{" "}
                  <span className="text-surface-200">Convex IP (direct)</span>{" "}
                  &mdash; direct HTTP, ~5ms (known IP from device registry)
                </li>
                <li>
                  &bull; <span className="text-surface-200">QUIC Relay</span>{" "}
                  &mdash; proxied, ~50ms (any network)
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Relay Hot-Reload & Health ─── */}
        <section className="mb-20">
          <SectionHeading id="relay-hot-reload">Relay Hot-Reload &amp; Health Monitoring</SectionHeading>
          <Prose>
            Relay servers can be added, removed, or updated while the agent is running &mdash; no restart needed.
            The CLI sends <InlineCode>SIGHUP</InlineCode> to the running agent, which reloads config instantly.
            A background poll every 30s serves as a safety net.
          </Prose>
          <Terminal title="relay management">
            <Cmd>yaver relay add https://relay.example.com --password secret --label &quot;My VPS&quot;</Cmd>
            <Output>Agent notified — relay will connect within seconds.</Output>
            <Cmd>yaver relay list</Cmd>
            <Cmd>yaver relay test</Cmd>
          </Terminal>
          <SubHeading>Health Monitoring</SubHeading>
          <Prose>
            The agent pings each relay&apos;s <InlineCode>/health</InlineCode> endpoint every 60 seconds.
            Results are cached in <InlineCode>~/.yaver/relay-health.json</InlineCode> and shown
            instantly in <InlineCode>yaver status</InlineCode> (no HTTP probes at display time).
            The mobile app also tracks relay health every 60s and uses it for automatic fallback.
          </Prose>
          <SubHeading>Mobile Auto-Fallback</SubHeading>
          <Prose>
            The mobile app runs a health heartbeat every 15 seconds. On 2 consecutive failures,
            it automatically tries relay servers even if <InlineCode>forceRelay</InlineCode> is off.
            This means: direct on WiFi, automatic relay failover when the path breaks &mdash; no user action needed.
          </Prose>
        </section>

        {/* ─── Token Refresh & Re-Auth ─── */}
        <section className="mb-20">
          <SectionHeading id="token-refresh">Token Refresh &amp; Re-Auth</SectionHeading>
          <Prose>
            Sessions last 30 days and auto-refresh across all layers:
          </Prose>
          <ul className="mb-6 space-y-2 text-sm text-surface-400">
            <li><strong className="text-surface-200">CLI agent:</strong> Refreshes token on startup + weekly. Detects 401 in heartbeat → attempts refresh → warns user if truly expired.</li>
            <li><strong className="text-surface-200">Mobile app:</strong> Refreshes on launch + every foreground resume. Auto-logouts if token is expired (forces re-login).</li>
            <li><strong className="text-surface-200">Backend:</strong> <InlineCode>POST /auth/refresh</InlineCode> extends session by 30 more days.</li>
          </ul>
          <Prose>
            Settings (relay servers, tunnels, preferences) are preserved across sign-out/sign-in
            on both CLI and mobile. Mobile settings are user-scoped &mdash; different accounts on the
            same device get isolated settings.
          </Prose>
        </section>

        {/* ─── System Health Check ─── */}
        <section className="mb-20">
          <SectionHeading id="doctor">System Health Check</SectionHeading>
          <Prose>
            <InlineCode>yaver doctor</InlineCode> performs a comprehensive system check similar
            to <InlineCode>flutter doctor</InlineCode>. It verifies auth, AI runners, relay
            servers, and network connectivity:
          </Prose>
          <Terminal title="yaver doctor">
            <Cmd>yaver doctor</Cmd>
            <Output>── Authentication ──</Output>
            <Output>  Auth token                     ✓ Present</Output>
            <Output>  Token validation               ✓ Valid</Output>
            <Output>── AI Runners ──</Output>
            <Output>  Claude Code (claude)           ✓ /usr/local/bin/claude (2.1.80)</Output>
            <Output>  Ollama (ollama)                ✓ /usr/local/bin/ollama (0.18.2)</Output>
            <Output>  Aider (aider)                  ! Not installed — pip install aider-chat</Output>
            <Output>── Relay Servers ──</Output>
            <Output>  Relay: My VPS                  ✓ OK (89ms, password set)</Output>
            <Output>Doctor summary: 12 passed, 3 warnings, 0 failures</Output>
          </Terminal>
        </section>

        {/* ─── Section 6: Running Tests ─── */}
        <section className="mb-20">
          <SectionHeading id="running-tests">Running Tests</SectionHeading>
          <Prose>
            Unit tests spin up real HTTP servers on random ports &mdash; no mocks,
            no external dependencies. Run them with a single command:
          </Prose>

          <div className="mb-8">
            <Terminal title="unit tests">
              <Cmd>cd desktop/agent &amp;&amp; go test -v ./...</Cmd>
              <Divider />
              <Output>--- PASS: TestHealth</Output>
              <Output>--- PASS: TestAuth</Output>
              <Output>--- PASS: TestCORS</Output>
              <Output>--- PASS: TestTaskCRUD</Output>
              <Output>--- PASS: TestAgentStatus</Output>
              <Output>--- PASS: TestPingPong</Output>
              <Output>--- PASS: TestShutdown</Output>
              <Output>--- PASS: TestServerClientIntegration</Output>
              <Output>--- PASS: TestMCPProtocol</Output>
              <Output>PASS</Output>
              <Divider />
              <Cmd>cd relay &amp;&amp; go test -v ./...</Cmd>
              <Output>PASS</Output>
            </Terminal>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                What&apos;s covered
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>&bull; Health check, auth, and CORS endpoints</li>
                <li>&bull; Task CRUD and agent status</li>
                <li>&bull; Ping/pong and graceful shutdown</li>
                <li>
                  &bull; Server-client integration: two agents on same
                  machine, verifies token isolation and task separation
                </li>
                <li>
                  &bull; MCP protocol: initialize + tools/list JSON-RPC (30 tools)
                </li>
                <li>&bull; Relay server registration and tunnel lifecycle</li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Integration Test Suite ─── */}
        <section className="mb-20">
          <SectionHeading id="integration-test-suite">Integration Test Suite</SectionHeading>
          <Prose>
            The full integration test suite verifies CLI-to-CLI connections across
            every transport mode &mdash; LAN, relay server (local + remote Docker + remote
            binary), Tailscale, and Cloudflare Tunnel. It also builds all
            components and validates MCP protocol compliance.
          </Prose>

          <div className="mb-8">
            <Terminal title="integration test suite">
              <Comment># Run everything</Comment>
              <Cmd>./scripts/test-suite.sh</Cmd>
              <Divider />
              <Comment># Or run specific sections</Comment>
              <Cmd>./scripts/test-suite.sh --unit</Cmd>
              <Cmd>./scripts/test-suite.sh --builds</Cmd>
              <Cmd>./scripts/test-suite.sh --lan</Cmd>
              <Cmd>./scripts/test-suite.sh --relay</Cmd>
              <Cmd>./scripts/test-suite.sh --relay-docker</Cmd>
              <Cmd>./scripts/test-suite.sh --relay-binary</Cmd>
              <Cmd>./scripts/test-suite.sh --tailscale</Cmd>
              <Cmd>./scripts/test-suite.sh --cloudflare</Cmd>
              <Divider />
              <Comment># Combine flags</Comment>
              <Cmd>./scripts/test-suite.sh --unit --lan --relay</Cmd>
            </Terminal>
          </div>

          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 text-surface-200">Flag</th>
                  <th className="pb-3 pr-4 text-surface-200">What it tests</th>
                  <th className="pb-3 text-surface-200">Requires</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4"><InlineCode>--unit</InlineCode></td>
                  <td className="py-3 pr-4">Go agent + relay unit tests</td>
                  <td className="py-3">Nothing</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4"><InlineCode>--builds</InlineCode></td>
                  <td className="py-3 pr-4">CLI, relay, web, backend typecheck, mobile typecheck, iOS, Android</td>
                  <td className="py-3">Node.js, Go, Xcode, Java 17</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4"><InlineCode>--lan</InlineCode></td>
                  <td className="py-3 pr-4">Auth rejection, task flow via direct HTTP, MCP protocol</td>
                  <td className="py-3">Nothing</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4"><InlineCode>--relay</InlineCode></td>
                  <td className="py-3 pr-4">Local relay + agent registration, proxy task flow, password rejection</td>
                  <td className="py-3">Nothing</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4"><InlineCode>--relay-docker</InlineCode></td>
                  <td className="py-3 pr-4">Deploy relay via Docker to remote server, test, teardown</td>
                  <td className="py-3">Remote server + SSH</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4"><InlineCode>--relay-binary</InlineCode></td>
                  <td className="py-3 pr-4">Deploy relay as native binary to remote server, test, teardown</td>
                  <td className="py-3">Remote server + SSH</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4"><InlineCode>--tailscale</InlineCode></td>
                  <td className="py-3 pr-4">Deploy agent to remote server, connect via Tailscale IPs</td>
                  <td className="py-3">Tailscale on both machines</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4"><InlineCode>--cloudflare</InlineCode></td>
                  <td className="py-3 pr-4">Quick tunnel + optional named tunnel with CF Access</td>
                  <td className="py-3"><InlineCode>cloudflared</InlineCode></td>
                </tr>
              </tbody>
            </table>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                No credentials needed
              </h4>
              <p className="text-sm text-surface-400">
                <InlineCode>--unit</InlineCode>, <InlineCode>--lan</InlineCode>,
                and <InlineCode>--relay</InlineCode> work out of the box. They spin up
                local processes and use the Convex dev backend for test account
                signup. Great for contributors who just want to verify their changes.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Remote server tests
              </h4>
              <p className="mb-3 text-sm text-surface-400">
                <InlineCode>--relay-docker</InlineCode>, <InlineCode>--relay-binary</InlineCode>,
                and <InlineCode>--tailscale</InlineCode> SSH into a remote Linux server
                (e.g. Hetzner VPS), deploy binaries, test cross-network connectivity,
                then tear everything down. Auto-detects CPU architecture (amd64 vs arm64).
              </p>
              <Terminal title="credentials setup">
                <Comment># Copy the template (gitignored)</Comment>
                <Cmd>cp .env.test.example .env.test</Cmd>
                <Divider />
                <Comment># Or keep credentials outside the repo</Comment>
                <Cmd>cp .env.test.example ../private/.env.test</Cmd>
              </Terminal>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                CI / GitHub Actions
              </h4>
              <p className="text-sm text-surface-400">
                The test suite runs automatically on pushes to <InlineCode>main</InlineCode> when
                CLI or relay code changes. It can also be triggered manually
                via <InlineCode>workflow_dispatch</InlineCode> from the Actions tab.
                Credentials are stored as GitHub Actions secrets.
                See <InlineCode>.github/workflows/test-suite.yml</InlineCode>.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Session Transfer ─── */}
        <section className="mb-20">
          <SectionHeading id="session-transfer">Session Transfer</SectionHeading>
          <Prose>
            Transfer AI agent sessions between machines. Move a Claude Code, Aider,
            Codex, Goose, or any other agent session from your laptop to a headless
            server &mdash; and keep working from your phone.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">CLI Usage</h4>
              <Terminal title="session transfer">
                <Cmd>yaver session list</Cmd>
                <Cmd>yaver session transfer abc12345 --to my-server</Cmd>
                <Cmd>yaver session export abc12345 --output bundle.json</Cmd>
                <Cmd>yaver session import --input bundle.json</Cmd>
              </Terminal>
            </div>

            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">MCP Support</h4>
              <Prose>
                Session transfer is also available as MCP tools &mdash; use it directly
                from within Claude Code without leaving your terminal.
              </Prose>
              <Terminal title="MCP usage">
                <Comment># From Claude Code, just ask:</Comment>
                <Output>&quot;Transfer this session to my server&quot;</Output>
                <Comment># Claude Code will use the session_transfer MCP tool automatically</Comment>
              </Terminal>
            </div>
          </div>
        </section>

        {/* ─── PR Rules ─── */}
        <section className="mb-20">
          <SectionHeading id="pr-rules">Pull Request Rules</SectionHeading>
          <Prose>
            All changes go through pull requests. The CI pipeline must pass before
            merging. Here&apos;s what happens when you open a PR:
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                CI checks (automated)
              </h4>
              <ol className="space-y-2 text-sm text-surface-400 list-decimal list-inside">
                <li>
                  <span className="text-surface-300">Change detection</span> &mdash;
                  only the components you touched are tested (CLI, relay, web, mobile, backend)
                </li>
                <li>
                  <span className="text-surface-300">Version check</span> &mdash;
                  if you changed a component, its version in <InlineCode>versions.json</InlineCode> must
                  be bumped
                </li>
                <li>
                  <span className="text-surface-300">Go tests</span> &mdash;
                  <InlineCode>go test ./...</InlineCode> for CLI agent and relay
                </li>
                <li>
                  <span className="text-surface-300">Go build</span> &mdash;
                  verifies the CLI compiles
                </li>
                <li>
                  <span className="text-surface-300">Web build</span> &mdash;
                  <InlineCode>npm run build</InlineCode> for the Next.js landing page
                </li>
                <li>
                  <span className="text-surface-300">Mobile typecheck</span> &mdash;
                  <InlineCode>tsc --noEmit</InlineCode> for React Native
                </li>
                <li>
                  <span className="text-surface-300">Backend typecheck</span> &mdash;
                  <InlineCode>npx convex typecheck</InlineCode> for Convex functions
                </li>
              </ol>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Before submitting
              </h4>
              <ol className="space-y-2 text-sm text-surface-400 list-decimal list-inside">
                <li>
                  Run <InlineCode>./scripts/test-suite.sh --unit --lan --relay</InlineCode> locally &mdash;
                  these catch most issues without needing remote infrastructure
                </li>
                <li>
                  If you changed builds, run <InlineCode>./scripts/test-suite.sh --builds</InlineCode> to
                  verify all components compile
                </li>
                <li>
                  Bump the version in <InlineCode>versions.json</InlineCode> for any
                  component you modified, then run <InlineCode>./scripts/sync-versions.sh</InlineCode>
                </li>
                <li>
                  Keep PRs focused &mdash; one feature or fix per PR
                </li>
              </ol>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Version bumping
              </h4>
              <p className="mb-3 text-sm text-surface-400">
                Every component has its own version in <InlineCode>versions.json</InlineCode>.
                CI enforces that changed components have their version bumped.
              </p>
              <Terminal title="version bump">
                <Comment># Edit versions.json, then sync everywhere</Comment>
                <Cmd>./scripts/sync-versions.sh</Cmd>
                <Divider />
                <Comment># This updates: mobile/app.json, Info.plist,</Comment>
                <Comment># project.pbxproj, build.gradle, etc.</Comment>
              </Terminal>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Release process
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; <span className="text-surface-200">Tags trigger releases</span>:
                  push <InlineCode>cli/v1.30.0</InlineCode> to build + publish CLI binaries,
                  update Homebrew/Scoop
                </li>
                <li>
                  &bull; <span className="text-surface-200">Tag format</span>:
                  <InlineCode>cli/vX.Y.Z</InlineCode>, <InlineCode>relay/vX.Y.Z</InlineCode>,
                  <InlineCode>mobile/vX.Y.Z</InlineCode>, <InlineCode>web/vX.Y.Z</InlineCode>
                </li>
                <li>
                  &bull; <span className="text-surface-200">Production deploys</span> require
                  manual approval in the GitHub environment
                </li>
                <li>
                  &bull; <span className="text-surface-200">Web deploys</span> are manual:
                  <InlineCode>./scripts/deploy-vercel.sh</InlineCode> (auto-deploy is disabled)
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── SDK ─── */}
        <section className="mb-20">
          <SectionHeading id="feedback-sdk">Feedback SDK &amp; Test Loop</SectionHeading>
          <Prose>
            The Feedback SDK turns your mobile app into a live testing tool that talks
            directly to the AI agent on your dev machine. Drop it into your React Native,
            Flutter, or web app and get a floating debug button that only you (the developer)
            can see. From it you can report bugs with auto-screenshots, send voice notes,
            trigger hot reload, and build/deploy to TestFlight or Play Store &mdash; all
            without leaving the app.
          </Prose>

          <SubHeading>The Loop</SubHeading>
          <Prose>
            The Feedback SDK creates a closed loop between your app and the AI agent:
            you use the app, hit a bug, tap the floating button to report it (with screenshot,
            voice, and error context attached), and the agent receives everything it needs to
            write a fix. Once the agent pushes the fix, you tap Hot Reload in the SDK
            to pull the latest code, verify the fix, and continue testing. No context switching,
            no copy-pasting stack traces, no typing bug reports.
          </Prose>

          <div className="mb-8">
            <Terminal title="feedback-loop">
              <div className="text-surface-300">{"1. Use your app → find a bug"}</div>
              <div className="text-surface-300">{"2. Tap floating button → report with screenshot + voice"}</div>
              <div className="text-surface-300">{"3. Agent receives: screenshot, error logs, BlackBox flight recorder, your description"}</div>
              <div className="text-surface-300">{"4. Agent writes a fix → pushes code"}</div>
              <div className="text-surface-300">{"5. Tap Hot Reload in SDK → app reloads with the fix"}</div>
              <div className="text-surface-300">{"6. Verify → continue testing → repeat"}</div>
            </Terminal>
          </div>

          <SubHeading>BlackBox (Flight Recorder)</SubHeading>
          <Prose>
            The SDK continuously streams all app events to the agent like a flight recorder:
            console logs, errors, navigation events, lifecycle changes, network requests,
            state changes, and render timings. The agent keeps the last 1000 events per device
            in a ring buffer. When a bug is reported, the agent has full context &mdash;
            not just the error, but everything that led up to it.
          </Prose>

          <SubHeading>Error Capture (No Conflicts)</SubHeading>
          <Prose>
            The SDK never hijacks global error handlers. It plays nicely with Sentry,
            Crashlytics, Bugsnag, or any other tool you already use.
            Use <InlineCode>wrapErrorHandler(existing)</InlineCode> for the handler chain
            or <InlineCode>attachError(err, metadata)</InlineCode> for manual capture
            in catch blocks. Fatal crashes auto-create fix tasks for the agent.
          </Prose>

          <SubHeading>Installation</SubHeading>
          <div className="space-y-4 mb-8">
            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">React Native</h4>
              <Terminal title="install">
                <Cmd>npm install @yaver/feedback-react-native</Cmd>
              </Terminal>
            </div>
            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">Flutter</h4>
              <Terminal title="install">
                <Cmd>flutter pub add yaver_feedback</Cmd>
              </Terminal>
            </div>
          </div>

          <Prose>
            See the full{" "}
            <Link
              href="/docs/feedback-sdk"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              Feedback SDK docs
            </Link>{" "}
            for quick start, API reference, agent integration, and configuration options.
          </Prose>
        </section>

        {/* ─── SDK Token Security ─── */}
        <section className="mb-20">
          <SectionHeading id="sdk-token-security">SDK Token Security</SectionHeading>
          <Prose>
            The Feedback SDK uses a dedicated token system with 6 defense-in-depth
            security layers. SDK tokens are independent from CLI session tokens
            &mdash; CLI reauth does not invalidate them, and they are scoped to
            only access feedback-related endpoints.
          </Prose>

          <SubHeading>Token Types</SubHeading>
          <div className="overflow-x-auto mb-6">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-surface-500">
                  <th className="pb-2">Type</th>
                  <th className="pb-2">Scope</th>
                  <th className="pb-2">Lifetime</th>
                  <th className="pb-2">Created by</th>
                </tr>
              </thead>
              <tbody className="text-surface-300">
                <tr className="border-t border-surface-800">
                  <td className="py-2">CLI session</td>
                  <td className="py-2">Full agent access</td>
                  <td className="py-2">1 year (auto-refreshed)</td>
                  <td className="py-2"><InlineCode>yaver auth</InlineCode></td>
                </tr>
                <tr className="border-t border-surface-800">
                  <td className="py-2">SDK token</td>
                  <td className="py-2">feedback, blackbox, voice, builds</td>
                  <td className="py-2">Custom (default 1 year)</td>
                  <td className="py-2"><InlineCode>yaver sdk-token create</InlineCode></td>
                </tr>
              </tbody>
            </table>
          </div>

          <SubHeading>Security Layers</SubHeading>
          <div className="space-y-4 mb-8">
            {[
              { n: "1", title: "Scope Restriction", desc: "SDK tokens only access /feedback, /blackbox/*, /voice/*, /builds. Cannot reach /tasks, /exec, /vault, /agent/*." },
              { n: "2", title: "IP Binding", desc: "Restrict tokens to specific CIDRs with --allowed-ips. Requests from non-matching IPs get 403." },
              { n: "3", title: "Agent IP Allowlist", desc: "yaver serve --allow-ips blocks all requests before auth. Only matching IPs reach the agent." },
              { n: "4", title: "Token Rotation", desc: "POST /sdk/token/rotate issues a new token. Old token has 5-minute grace period for in-flight requests." },
              { n: "5", title: "New Device Alerts", desc: "First-seen IPs trigger security events in Convex. Query via GET /security/events." },
              { n: "6", title: "HTTPS on LAN", desc: "Auto-generated self-signed TLS cert with IP SANs. Serves HTTPS on :18443. Fingerprint in /health and beacon." },
            ].map((layer) => (
              <div key={layer.n} className="card">
                <h4 className="mb-2 text-sm font-medium text-surface-200">
                  {layer.n}. {layer.title}
                </h4>
                <p className="text-sm leading-relaxed text-surface-400">
                  {layer.desc}
                </p>
              </div>
            ))}
          </div>

          <SubHeading>Auth Middleware</SubHeading>
          <Prose>
            The agent has two auth middlewares: <InlineCode>auth()</InlineCode>{" "}
            for full-access endpoints (rejects SDK tokens) and{" "}
            <InlineCode>authSDK()</InlineCode> for SDK-accessible endpoints
            (checks scope + IP binding). An outer{" "}
            <InlineCode>ipAllowlist()</InlineCode> middleware runs before both.
          </Prose>

          <Terminal title="CLI examples">
            <Comment># Create SDK token with defaults (1 year, all SDK scopes)</Comment>
            <Cmd>yaver sdk-token create --label &quot;AcmeStore dev&quot;</Cmd>
            <Divider />
            <Comment># Narrow scopes + IP binding + short expiry</Comment>
            <Cmd>yaver sdk-token create --scopes feedback,blackbox --allowed-ips 192.168.1.0/24 --expires 7d</Cmd>
            <Divider />
            <Comment># Agent IP allowlist</Comment>
            <Cmd>yaver serve --allow-ips 192.168.1.0/24,10.0.0.0/8</Cmd>
            <Divider />
            <Comment># Disable HTTPS</Comment>
            <Cmd>yaver serve --no-tls</Cmd>
          </Terminal>

          <SubHeading>Key Files</SubHeading>
          <div className="overflow-x-auto mb-6">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-surface-500">
                  <th className="pb-2">File</th>
                  <th className="pb-2">Purpose</th>
                </tr>
              </thead>
              <tbody className="text-surface-300">
                {[
                  ["backend/convex/schema.ts", "sdkTokens + securityEvents tables"],
                  ["backend/convex/auth.ts", "createSdkToken, validateSdkToken, rotateSdkToken"],
                  ["backend/convex/http.ts", "/sdk/token/*, /security/* endpoints"],
                  ["desktop/agent/httpserver.go", "auth(), authSDK(), ipAllowlist() middlewares"],
                  ["desktop/agent/auth.go", "ValidateSdkTokenFull(), CreateSdkToken()"],
                  ["desktop/agent/sdk_token.go", "yaver sdk-token CLI commands"],
                  ["desktop/agent/tls.go", "Self-signed TLS cert generation"],
                  ["desktop/agent/sdk_token_test.go", "25+ security tests"],
                ].map(([file, purpose]) => (
                  <tr key={file} className="border-t border-surface-800">
                    <td className="py-2"><InlineCode>{file}</InlineCode></td>
                    <td className="py-2 text-surface-400">{purpose}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <SubHeading>Tests</SubHeading>
          <Terminal title="Run security tests">
            <Comment># All 25+ security tests</Comment>
            <Cmd>cd desktop/agent &amp;&amp; go test -v -run &quot;TestSDK|TestIP|TestTLS|TestParse|TestPath&quot;</Cmd>
            <Divider />
            <Comment># TypeScript SDK token tests</Comment>
            <Cmd>cd sdk/feedback/react-native &amp;&amp; npm test -- SDKToken</Cmd>
          </Terminal>
        </section>

        {/* ─── SDK — Embed Yaver ─── */}
        <section className="mb-20">
          <SectionHeading id="sdk">SDK — Embed Yaver in Your App</SectionHeading>
          <Prose>
            Yaver provides embeddable SDKs so you can integrate P2P AI agent
            connectivity into your own applications. Available for Go, Python,
            JavaScript/TypeScript, Flutter/Dart, and C/C++ (via shared library).
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">Go</h4>
              <Terminal title="Go SDK">
                <Comment># Import the SDK</Comment>
                <div className="text-surface-300">{`import yaver "github.com/kivanccakmak/yaver.io/sdk/go/yaver"`}</div>
                <div className="text-surface-300 mt-2">{`client := yaver.NewClient("http://localhost:18080", token)`}</div>
                <div className="text-surface-300">{`task, _ := client.CreateTask("Fix the bug", nil)`}</div>
                <div className="text-surface-300">{`for chunk := range client.StreamOutput(task.ID, 0) {`}</div>
                <div className="text-surface-300">{`    fmt.Print(chunk)`}</div>
                <div className="text-surface-300">{`}`}</div>
              </Terminal>
              <p className="mt-3 text-xs text-surface-500">
                Also includes: AuthClient (token validation, device listing), Transcriber (STT), Config (load/save), Speak (TTS).
              </p>
            </div>

            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">Python</h4>
              <Terminal title="Python SDK">
                <Comment># pip install yaver (or copy sdk/python/yaver.py)</Comment>
                <div className="text-surface-300">{`from yaver import YaverClient`}</div>
                <div className="text-surface-300 mt-2">{`client = YaverClient("http://localhost:18080", token)`}</div>
                <div className="text-surface-300">{`task = client.create_task("Fix the bug")`}</div>
                <div className="text-surface-300">{`for chunk in client.stream_output(task["id"]):`}</div>
                <div className="text-surface-300">{`    print(chunk, end="")`}</div>
              </Terminal>
              <p className="mt-3 text-xs text-surface-500">
                Zero dependencies (stdlib only). HTTP mode works everywhere. Native mode uses ctypes + the C shared library for direct bindings.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">JavaScript / TypeScript</h4>
              <Terminal title="JS/TS SDK">
                <Comment># npm install yaver-sdk</Comment>
                <div className="text-surface-300">{`import { YaverClient } from 'yaver-sdk';`}</div>
                <div className="text-surface-300 mt-2">{`const client = new YaverClient('http://localhost:18080', token);`}</div>
                <div className="text-surface-300">{`const task = await client.createTask('Fix the bug');`}</div>
                <div className="text-surface-300">{`for await (const chunk of client.streamOutput(task.id)) {`}</div>
                <div className="text-surface-300">{`  process.stdout.write(chunk);`}</div>
                <div className="text-surface-300">{`}`}</div>
              </Terminal>
              <p className="mt-3 text-xs text-surface-500">
                Works in React Native, Node.js, and browsers. Full TypeScript types. Includes auth client and speech transcription.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">Flutter / Dart</h4>
              <Terminal title="Flutter SDK">
                <Comment># flutter pub add yaver</Comment>
                <div className="text-surface-300">{`import 'package:yaver/yaver.dart';`}</div>
                <div className="text-surface-300 mt-2">{`final client = YaverClient('http://localhost:18080', token);`}</div>
                <div className="text-surface-300">{`final task = await client.createTask('Fix the bug');`}</div>
                <div className="text-surface-300">{`await for (final chunk in client.streamOutput(task.id)) {`}</div>
                <div className="text-surface-300">{`  stdout.write(chunk);`}</div>
                <div className="text-surface-300">{`}`}</div>
              </Terminal>
              <p className="mt-3 text-xs text-surface-500">
                Works on iOS, Android, Web, and Desktop. Full type-safe models. Includes auth client, speech transcription, and image attachments.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">C / C++ (shared library)</h4>
              <Terminal title="Build & link">
                <Cmd>cd sdk/go/clib</Cmd>
                <Cmd>go build -buildmode=c-shared -o libyaver.so .</Cmd>
                <Comment># Generates libyaver.so + libyaver.h</Comment>
              </Terminal>
              <pre className="mt-3 rounded-lg bg-surface-950 p-3 text-xs text-surface-300 overflow-x-auto">{`#include "libyaver.h"
int client = YaverNewClient("http://localhost:18080", token);
char* result = YaverCreateTask(client, "Fix the bug", NULL);
// Parse result as JSON
YaverFreeString(result);
YaverFreeClient(client);`}</pre>
              <p className="mt-3 text-xs text-surface-500">
                Works with any language that supports C FFI: Rust (bindgen), Ruby (ffi gem), Java (JNI), C# (P/Invoke), etc.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">Testing SDKs</h4>
              <Terminal title="Test suite">
                <Cmd>./scripts/test-suite.sh --sdk</Cmd>
                <Output>✓ Go SDK tests passed</Output>
                <Output>✓ C shared library built</Output>
                <Output>✓ Python SDK tests passed</Output>
                <Output>✓ JS/TS SDK typecheck passed</Output>
                <Output>✓ JS/TS SDK built</Output>
                <Output>✓ Flutter/Dart SDK analysis passed</Output>
              </Terminal>
            </div>
          </div>
        </section>

        {/* ─── Demo App (AcmeStore) ─── */}
        <section className="mb-20">
          <SectionHeading id="demo-app">Demo App (AcmeStore)</SectionHeading>
          <Prose>
            The <InlineCode>demo/AcmeStore</InlineCode> directory contains a
            minimal React Native e-commerce app. It exists for one reason: to
            showcase the Feedback SDK integration in a real, runnable app that
            anyone can clone and try immediately.
          </Prose>

          <SubHeading>Why a demo app?</SubHeading>
          <Prose>
            The Feedback SDK is designed to be dropped into any React Native
            app during development. But reading docs about it is not the same
            as seeing it work. AcmeStore is a deliberately simple app (login,
            product list, cart) so the SDK integration stands out clearly
            without the noise of a real production codebase.
          </Prose>

          <SubHeading>What it demonstrates</SubHeading>
          <div className="mb-6 space-y-2 text-sm text-surface-400">
            <div className="flex items-start gap-2">
              <span className="mt-0.5 text-surface-500">1.</span>
              <span><strong className="text-surface-200">Floating debug button</strong> &mdash; the SDK adds a draggable button to the app. Tap it to open the debug panel, send messages to the AI agent, trigger hot reload, or report bugs with auto-screenshots.</span>
            </div>
            <div className="flex items-start gap-2">
              <span className="mt-0.5 text-surface-500">2.</span>
              <span><strong className="text-surface-200">Black box streaming</strong> &mdash; all logs, navigation events, errors, and crashes are streamed to the agent like a flight recorder. When a bug is reported, the agent already has full context.</span>
            </div>
            <div className="flex items-start gap-2">
              <span className="mt-0.5 text-surface-500">3.</span>
              <span><strong className="text-surface-200">Minimal integration code</strong> &mdash; the entire SDK setup is in <InlineCode>_layout.tsx</InlineCode>: init the SDK, start the black box, render the floating button. Three lines.</span>
            </div>
          </div>

          <SubHeading>Project structure</SubHeading>
          <div className="mb-6">
            <Terminal title="demo/AcmeStore">
              <pre className="text-surface-300">
                {`demo/AcmeStore/
├── app/
│   ├── _layout.tsx        # SDK init + FloatingButton
│   ├── index.tsx           # Home / product list
│   └── login.tsx           # Login screen
├── src/
│   ├── components/         # ProductCard, LoginForm
│   ├── context/            # AuthContext, CartContext
│   └── yaver-sdk/          # Vendored SDK source
│       ├── YaverFeedback.ts
│       ├── FloatingButton.tsx
│       ├── BlackBox.ts
│       ├── P2PClient.ts
│       └── Discovery.ts
└── app.json`}
              </pre>
            </Terminal>
          </div>

          <SubHeading>Running it</SubHeading>
          <div className="mb-6">
            <Terminal title="terminal">
              <Cmd>cd demo/AcmeStore</Cmd>
              <Cmd>npm install</Cmd>
              <Cmd>npx expo start</Cmd>
              <Comment># Scan QR with Expo Go, or run on simulator:</Comment>
              <Cmd>npx expo run:ios</Cmd>
            </Terminal>
          </div>

          <Prose>
            The SDK connects to your local Yaver agent automatically via LAN
            beacon discovery. Start <InlineCode>yaver serve</InlineCode> on
            your machine, open AcmeStore on your phone, and the debug button
            turns green when connected.
          </Prose>
        </section>

        {/* ─── Contributing ─── */}
        <section className="mb-20">
          <SectionHeading id="contributing">Contributing</SectionHeading>
          <Prose>
            Contributions are welcome. See the full{" "}
            <Link
              href="/docs/contributing"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              Contributing Guide
            </Link>{" "}
            for setup instructions, how to run your own Convex backend,
            seed data, CI/CD policy, and how to add new AI runners.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Quick start
              </h4>
              <ol className="space-y-2 text-sm text-surface-400 list-decimal list-inside">
                <li>Fork the repository</li>
                <li>Create a feature branch from <InlineCode>main</InlineCode></li>
                <li>Make your changes</li>
                <li>Bump version in <InlineCode>versions.json</InlineCode> and run <InlineCode>./scripts/sync-versions.sh</InlineCode></li>
                <li>
                  Run tests:{" "}
                  <InlineCode>./scripts/test-suite.sh --unit --lan --relay</InlineCode>
                </li>
                <li>Open a pull request against <InlineCode>main</InlineCode></li>
              </ol>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Conventions
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; <span className="text-surface-200">Go code</span>:{" "}
                  standard Go project layout, <InlineCode>gofmt</InlineCode>
                </li>
                <li>
                  &bull;{" "}
                  <span className="text-surface-200">TypeScript / React</span>:{" "}
                  functional components, hooks, no class components
                </li>
                <li>
                  &bull; <span className="text-surface-200">Convex</span>:{" "}
                  mutations for writes, queries for reads, HTTP actions for
                  OAuth callbacks
                </li>
                <li>
                  &bull; <span className="text-surface-200">Mobile</span>:{" "}
                  always native builds (xcodebuild for iOS, Gradle for Android),
                  never Expo CLI
                </li>
                <li>
                  &bull; <span className="text-surface-200">Tests</span>:{" "}
                  real servers on random ports, no mocks. If you add an endpoint,
                  add a test.
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Areas we&apos;d love help with
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>&bull; Additional AI runner integrations (Ollama, Qwen, LM Studio, etc.)</li>
                <li>&bull; Windows and Linux desktop installer improvements</li>
                <li>&bull; Relay server performance and benchmarking</li>
                <li>&bull; Documentation, tutorials, and example configurations</li>
                <li>&bull; Bug reports and test coverage improvements</li>
              </ul>
            </div>
          </div>
        </section>

        {/* Bottom CTA */}
        <div className="rounded-xl border border-surface-800 bg-surface-900 p-6 text-center">
          <p className="mb-2 text-sm font-medium text-surface-200">
            Questions or ideas?
          </p>
          <p className="text-sm text-surface-400">
            Open an issue on{" "}
            <a
              href="https://github.com/kivanccakmak/yaver/issues"
              target="_blank"
              rel="noopener noreferrer"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              GitHub
            </a>{" "}
            or email{" "}
            <a
              href="mailto:kivanc.cakmak@simkab.com"
              className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
            >
              kivanc.cakmak@simkab.com
            </a>
          </p>
        </div>

        {/* Back to home */}
        <div className="mt-8 text-center">
          <Link
            href="/"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            Back to home
          </Link>
        </div>
      </div>
    </div>
  );
}
