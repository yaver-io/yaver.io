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

function OptionCard({
  label,
  title,
  description,
  recommended,
}: {
  label: string;
  title: string;
  description: string;
  recommended?: boolean;
}) {
  return (
    <div className="card relative">
      {recommended && (
        <span className="absolute -top-2.5 right-4 rounded-full border border-surface-700 bg-surface-900 px-3 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-green-400/80">
          Recommended
        </span>
      )}
      <div className="mb-3 flex h-8 w-8 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-300">
        {label}
      </div>
      <h3 className="mb-2 text-sm font-semibold text-surface-50">{title}</h3>
      <p className="text-sm leading-relaxed text-surface-400">{description}</p>
    </div>
  );
}

export default function SelfHostingPage() {
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
            Self-Hosting Guide
          </h1>
          <p className="text-sm leading-relaxed text-surface-400">
            Yaver is fully self-hostable. Every Yaver component is open source
            under the AGPL-3.0-only license. Choose the networking option that
            fits your setup &mdash; from zero-config to fully custom infrastructure.
          </p>
        </div>

        {/* Table of contents */}
        <div className="mb-16 rounded-xl border border-surface-800 bg-surface-900 p-6">
          <h3 className="mb-4 text-sm font-semibold text-surface-200">
            On this page
          </h3>
          <nav className="space-y-2 text-sm">
            {[
              ["overview", "Overview"],
              ["tailscale", "Option A: Tailscale (Recommended)"],
              ["relay", "Option B: Relay Server with Docker"],
              ["cloudflare", "Option C: Cloudflare Tunnel"],
              ["local-llm", "Local LLM Setup"],
              ["architecture", "Architecture Reference"],
              ["troubleshooting", "Troubleshooting"],
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

        {/* ─── Section 1: Overview ─── */}
        <section className="mb-20">
          <SectionHeading id="overview">Overview</SectionHeading>
          <Prose>
            Yaver connects your phone to your dev machine. How that connection
            works depends on your network. Pick the option that matches your
            situation:
          </Prose>

          <div className="mb-8 grid grid-cols-1 gap-4 sm:grid-cols-2">
            <OptionCard
              label="A"
              title="Tailscale"
              description="Easiest setup, no infrastructure needed. Install Tailscale on both devices, done in 2 minutes. DERP handles hard NAT automatically."
              recommended
            />
            <OptionCard
              label="B"
              title="Relay Server"
              description="Full control over your infrastructure. Deploy a lightweight QUIC relay on any VPS. Works through NAT and firewalls."
            />
            <OptionCard
              label="C"
              title="Cloudflare Tunnel"
              description="Behind a corporate firewall that blocks UDP? Cloudflare Tunnel routes through HTTPS."
            />
            <OptionCard
              label="D"
              title="Direct / LAN"
              description="Same WiFi network? Yaver discovers your machine automatically via LAN beacon. No setup needed."
            />
          </div>

          {/* Decision tree */}
          <div className="rounded-xl border border-surface-800 bg-surface-900 p-6">
            <h3 className="mb-4 text-sm font-semibold text-surface-200">
              Quick decision tree
            </h3>
            <div className="space-y-3 text-sm">
              <div className="flex items-start gap-3">
                <span className="mt-0.5 shrink-0 text-surface-500">
                  &bull;
                </span>
                <p className="text-surface-400">
                  <span className="text-surface-200">Same network?</span>{" "}
                  &rarr; Direct connection, no setup needed
                </p>
              </div>
              <div className="flex items-start gap-3">
                <span className="mt-0.5 shrink-0 text-surface-500">
                  &bull;
                </span>
                <p className="text-surface-400">
                  <span className="text-surface-200">
                    Different networks?
                  </span>{" "}
                  &rarr; Tailscale (recommended) &mdash; 2 minutes, no
                  infrastructure
                </p>
              </div>
              <div className="flex items-start gap-3">
                <span className="mt-0.5 shrink-0 text-surface-500">
                  &bull;
                </span>
                <p className="text-surface-400">
                  <span className="text-surface-200">
                    Want full control over privacy?
                  </span>{" "}
                  &rarr; Self-hosted relay server
                </p>
              </div>
              <div className="flex items-start gap-3">
                <span className="mt-0.5 shrink-0 text-surface-500">
                  &bull;
                </span>
                <p className="text-surface-400">
                  <span className="text-surface-200">
                    Hard NAT / enterprise firewall?
                  </span>{" "}
                  &rarr; Cloudflare Tunnel or Tailscale with DERP
                </p>
              </div>
            </div>
            <p className="mt-4 text-sm text-surface-400 italic">
              If you&apos;re not sure, start with Tailscale &mdash; it takes 2
              minutes and you can always switch to a self-hosted relay later.
            </p>
          </div>
        </section>

        {/* ─── Section 2: Tailscale ─── */}
        <section className="mb-20">
          <SectionHeading id="tailscale">
            Option A: Tailscale (Recommended)
          </SectionHeading>
          <Prose>
            If you&apos;re not sure which option to pick, start here. Tailscale
            is the easiest way to connect your phone to your dev machine across
            networks. Install Tailscale on both devices, and Yaver connects
            directly over your tailnet. No relay server, no VPS, no ports to
            open.
          </Prose>

          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900 p-6">
            <h4 className="mb-2 text-sm font-medium text-surface-200">
              Is Tailscale open source?
            </h4>
            <p className="text-sm leading-relaxed text-surface-400">
              The Tailscale <strong className="text-surface-200">client</strong> is
              open source under the BSD 3-Clause license (<a
                href="https://github.com/tailscale/tailscale"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
              >github.com/tailscale/tailscale</a>).
              The <strong className="text-surface-200">coordination server</strong> (control
              plane) is proprietary &mdash; it handles key exchange, ACLs, and device
              management. If you want a fully open-source alternative, use{" "}
              <a
                href="https://github.com/juanfont/headscale"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
              >Headscale</a>{" "}
              &mdash; a community-built, self-hostable replacement for the Tailscale
              coordination server. Headscale + Tailscale client = fully open-source
              mesh VPN with no proprietary components.
            </p>
            <p className="mt-3 text-sm text-surface-400">
              Tailscale is <strong className="text-surface-200">free for personal
              use</strong> (up to 100 devices, 3 users). Paid plans start for teams.
            </p>
          </div>

          <div className="mb-8">
            <Terminal title="tailscale">
              <Comment># On your dev machine</Comment>
              <Cmd>tailscale up</Cmd>
              <Cmd>yaver serve --no-relay</Cmd>
              <Output>Listening on 100.x.x.x:18080 (Tailscale)</Output>
              <Divider />
              <Comment># On your phone</Comment>
              <Comment># Install Tailscale from App Store / Play Store</Comment>
              <Comment># Then connect to the Tailscale IP in Yaver app</Comment>
            </Terminal>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                How it works
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Tailscale creates a WireGuard mesh network between your devices.
                Your phone gets a stable{" "}
                <InlineCode>100.x.x.x</InlineCode> IP address that reaches your
                dev machine directly, regardless of physical network.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Tailscale DERP (automatic relay)
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Tailscale has its own relay infrastructure called DERP servers.
                If a direct WireGuard connection fails (hard NAT, symmetric
                NAT), Tailscale automatically falls back to DERP relays. No
                configuration needed &mdash; it just works. DERP servers are
                globally distributed and free for personal use.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                When to use Tailscale
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; You want the simplest setup with no infrastructure to manage
                </li>
                <li>&bull; You need the lowest possible latency (direct WireGuard
                  tunnel, ~5ms)
                </li>
                <li>
                  &bull; You&apos;re behind enterprise NAT that blocks UDP
                  (Tailscale DERP handles this)
                </li>
                <li>
                  &bull; You don&apos;t want to depend on a self-hosted relay
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Section 3: Relay Server ─── */}
        <section className="mb-20">
          <SectionHeading id="relay">
            Option B: Relay Server with Docker
          </SectionHeading>
          <Prose>
            The relay is a lightweight QUIC proxy that lets your phone reach your
            dev machine through NAT. It&apos;s pass-through only &mdash; no data
            is stored. Deploy it on any VPS with a public IP.
          </Prose>

          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900 p-6">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Requirements
            </h3>
            <ul className="space-y-2 text-sm text-surface-400">
              <li>
                &bull; VPS with public IP (Hetzner, DigitalOcean, AWS, Linode,
                etc.)
              </li>
              <li>&bull; 1 vCPU, 512 MB RAM minimum</li>
              <li>&bull; Docker installed</li>
              <li>
                &bull; Ports: TCP 443 (HTTPS), UDP 4433 (QUIC), TCP 8443 (HTTP
                fallback)
              </li>
            </ul>
          </div>

          {/* Automated setup script */}
          <div className="mb-8 rounded-xl border border-green-500/10 bg-green-500/5 p-6">
            <h4 className="mb-2 text-sm font-semibold text-green-400">
              One-command setup script
            </h4>
            <p className="mb-4 text-sm leading-relaxed text-surface-400">
              The repo includes{" "}
              <InlineCode>scripts/setup-relay.sh</InlineCode> that automates the
              entire production setup: Docker installation, nginx, Let&apos;s Encrypt
              SSL, firewall rules, relay deployment, and health checks. One command,
              done in under 2 minutes.
            </p>
            <Terminal title="automated setup">
              <Comment># Prerequisites: a VPS with SSH access + a DNS A record pointing to it</Comment>
              <Divider />
              <Comment># Production setup with HTTPS</Comment>
              <Cmd>./scripts/setup-relay.sh 1.2.3.4 relay.yourdomain.com --password your-secret</Cmd>
              <Divider />
              <Comment># Without a domain (testing / IP-only)</Comment>
              <Cmd>./scripts/setup-relay.sh 1.2.3.4 --no-domain --password your-secret</Cmd>
              <Divider />
              <Comment># Custom ports</Comment>
              <Cmd>./scripts/setup-relay.sh 1.2.3.4 relay.yourdomain.com --password secret --quic-port 5433 --http-port 9443</Cmd>
            </Terminal>
          </div>

          <div className="mb-8 rounded-xl border border-surface-800 bg-surface-900 p-6">
            <h4 className="mb-3 text-sm font-semibold text-surface-200">
              What the script does
            </h4>
            <ol className="space-y-2 text-sm text-surface-400">
              <li className="flex gap-3">
                <span className="text-surface-300 font-medium">1.</span>
                <span>
                  <strong className="text-surface-200">Pre-flight checks</strong> &mdash;
                  verifies SSH access to the server
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-300 font-medium">2.</span>
                <span>
                  <strong className="text-surface-200">Installs Docker</strong> &mdash;
                  skips if already installed
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-300 font-medium">3.</span>
                <span>
                  <strong className="text-surface-200">HTTPS setup</strong> &mdash;
                  installs nginx + certbot, obtains a Let&apos;s Encrypt certificate,
                  configures reverse proxy with SSE/streaming support, enables auto-renewal
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-300 font-medium">4.</span>
                <span>
                  <strong className="text-surface-200">Deploys relay</strong> &mdash;
                  sparse-clones the relay directory to <InlineCode>/opt/yaver-relay</InlineCode>,
                  writes <InlineCode>.env</InlineCode> with the password, builds and starts
                  the Docker container
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-300 font-medium">5.</span>
                <span>
                  <strong className="text-surface-200">Firewall</strong> &mdash;
                  opens TCP 443 (HTTPS), UDP 4433 (QUIC), and TCP 80 (HTTP redirect) via UFW
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-300 font-medium">6.</span>
                <span>
                  <strong className="text-surface-200">Health check</strong> &mdash;
                  verifies the relay responds, prints a summary with connection details
                  and useful commands (logs, restart, stop)
                </span>
              </li>
            </ol>
            <p className="mt-4 text-xs text-surface-500">
              Source: <a
                href="https://github.com/kivanccakmak/yaver/blob/main/scripts/setup-relay.sh"
                target="_blank"
                rel="noopener noreferrer"
                className="text-surface-400 underline underline-offset-2 hover:text-surface-200"
              >scripts/setup-relay.sh</a> &mdash;
              read it before running on your server.
            </p>
          </div>

          {/* Quick start */}
          <SubHeading>Quick Start (no HTTPS)</SubHeading>
          <Prose>
            For testing or development, you can run the relay without TLS on the
            HTTP side. The QUIC tunnel between the CLI agent and relay is always
            encrypted.
          </Prose>

          <div className="mb-8">
            <Terminal title="quick-start">
              <Comment># Sparse clone &mdash; only downloads the relay directory</Comment>
              <Cmd>git clone --depth 1 --filter=blob:none --sparse https://github.com/kivanccakmak/yaver.git /opt/yaver-relay</Cmd>
              <Cmd>cd /opt/yaver-relay &amp;&amp; git sparse-checkout set relay &amp;&amp; cd relay</Cmd>
              <Divider />
              <Comment># Set password and start</Comment>
              <Cmd>echo &quot;RELAY_PASSWORD=your-secret&quot; &gt; .env</Cmd>
              <Cmd>docker compose up -d</Cmd>
              <Divider />
              <Comment># Verify it&apos;s running</Comment>
              <Cmd>{"curl http://localhost:8443/health"}</Cmd>
              <Output>{"{ \"status\": \"ok\" }"}</Output>
            </Terminal>
          </div>

          {/* Production manual steps */}
          <SubHeading>Manual Production Setup</SubHeading>
          <Prose>
            If you prefer to run each step manually instead of using the setup script,
            here&apos;s the full procedure. Point your domain&apos;s DNS A record to
            your VPS IP first.
          </Prose>

          <div className="mb-6">
            <Terminal title="production-setup (manual)">
              <Comment># 1. Install dependencies</Comment>
              <Cmd># install nginx, certbot, and the nginx certbot plugin with your server&apos;s standard package flow</Cmd>
              <Divider />
              <Comment># 2. Get Let&apos;s Encrypt certificate</Comment>
              <Cmd>
                certbot certonly --standalone -d relay.yourdomain.com
              </Cmd>
              <Divider />
              <Comment># 3. Open firewall ports</Comment>
              <Cmd>ufw allow 443/tcp</Cmd>
              <Cmd>ufw allow 4433/udp</Cmd>
              <Divider />
              <Comment># 4. Start relay</Comment>
              <Cmd>cd yaver/relay</Cmd>
              <Cmd>RELAY_PASSWORD=your-secret docker compose up -d</Cmd>
              <Divider />
              <Comment># 5. Verify</Comment>
              <Cmd>curl https://relay.yourdomain.com/health</Cmd>
              <Output>{"{ \"status\": \"ok\" }"}</Output>
            </Terminal>
          </div>

          {/* Nginx config */}
          <div className="mb-8">
            <p className="mb-3 text-sm font-medium text-surface-300">
              Nginx configuration (auto-generated by the setup script, or create manually)
            </p>
            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">
                  /etc/nginx/sites-available/relay
                </span>
              </div>
              <div className="terminal-body text-[13px] leading-relaxed">
                <pre className="text-surface-300">
                  {`server {
    listen 443 ssl http2;
    server_name relay.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/relay.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/relay.yourdomain.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8443;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # SSE/streaming support
        proxy_set_header Connection '';
        proxy_buffering off;
        proxy_cache off;
        chunked_transfer_encoding on;

        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}

server {
    listen 80;
    server_name relay.yourdomain.com;
    return 301 https://$server_name$request_uri;
}`}
                </pre>
              </div>
            </div>
            <p className="mt-2 text-xs text-surface-500">
              Template source: <InlineCode>relay/deploy/nginx-relay.conf</InlineCode>
            </p>
          </div>

          {/* Configure clients */}
          <SubHeading>Configure Clients</SubHeading>
          <Prose>
            Point the CLI and mobile app to your relay server.
          </Prose>

          <div className="mb-6 space-y-4">
            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">
                CLI Configuration
              </h4>
              <p className="mb-3 text-xs text-surface-500">
                Add to <InlineCode>~/.yaver/config.json</InlineCode>
              </p>
              <div className="terminal">
                <div className="terminal-body text-[13px]">
                  <pre className="text-surface-300">
                    {`{
  "relay_servers": [
    {
      "id": "my-relay",
      "quic_addr": "1.2.3.4:4433",
      "http_url": "https://relay.yourdomain.com"
    }
  ],
  "relay_password": "your-secret"
}`}
                  </pre>
                </div>
              </div>
            </div>

            <div className="card">
              <h4 className="mb-3 text-sm font-medium text-surface-200">
                Mobile Configuration
              </h4>
              <p className="text-sm text-surface-400">
                Open the Yaver app &rarr; Settings &rarr; Relay Servers &rarr;
                Add your relay URL and password.
              </p>
            </div>
          </div>

          {/* Without HTTPS note */}
          <div className="rounded-xl border border-surface-800 bg-surface-900 p-6">
            <h4 className="mb-2 text-sm font-medium text-surface-200">
              Without HTTPS (testing only)
            </h4>
            <p className="text-sm leading-relaxed text-surface-400">
              You can use the relay over plain HTTP for local testing. Set{" "}
              <InlineCode>http_url</InlineCode> to{" "}
              <InlineCode>{"http://<ip>:8443"}</InlineCode>. The QUIC tunnel
              between the CLI agent and relay is always encrypted regardless of
              the HTTP layer.
            </p>
          </div>
        </section>

        {/* ─── Section 4: Cloudflare Tunnel ─── */}
        <section className="mb-20">
          <SectionHeading id="cloudflare">
            Option C: Cloudflare Tunnel
          </SectionHeading>
          <Prose>
            For corporate networks where even UDP is blocked, Cloudflare Tunnel
            routes everything over HTTPS. No open ports, no firewall changes, no
            VPS needed.
          </Prose>

          <div className="mb-8">
            <Terminal title="cloudflare-tunnel">
              <Comment># Install cloudflared</Comment>
              <Cmd>cloudflared --version</Cmd>
              <Divider />
              <Comment># Authenticate</Comment>
              <Cmd>cloudflared tunnel login</Cmd>
              <Divider />
              <Comment># Create a tunnel</Comment>
              <Cmd>cloudflared tunnel create yaver</Cmd>
              <Divider />
              <Comment># Route tunnel to your agent&apos;s HTTP port</Comment>
              <Cmd>
                cloudflared tunnel route dns yaver yaver.yourdomain.com
              </Cmd>
              <Divider />
              <Comment># Start tunnel</Comment>
              <Cmd>
                cloudflared tunnel --url http://localhost:18080 run yaver
              </Cmd>
              <Output>
                Tunnel connected: https://yaver.yourdomain.com
              </Output>
            </Terminal>
            <Prose>
              Install Cloudflare Tunnel from Cloudflare&apos;s official distribution first if{" "}
              <InlineCode>cloudflared</InlineCode> is not already installed.
            </Prose>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                How it works
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Cloudflared creates an outbound-only connection from your dev
                machine to Cloudflare&apos;s edge network. Your mobile app
                connects to the tunnel URL over HTTPS. No inbound ports, no
                firewall changes, no VPS required.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Trade-offs
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; Adds ~50-100ms latency compared to direct or relay
                  connections
                </li>
                <li>
                  &bull; Requires a Cloudflare account (free tier works)
                </li>
                <li>
                  &bull; Works through any firewall that allows HTTPS
                </li>
                <li>
                  &bull; Best for corporate / enterprise networks with strict
                  firewall rules
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Section 5: Local LLM ─── */}
        <section className="mb-20">
          <SectionHeading id="local-llm">
            Local LLM Setup (Complete Privacy)
          </SectionHeading>
          <Prose>
            Combine a local LLM with any networking option above for a fully
            self-hosted AI coding setup. Your code, your models, your
            infrastructure &mdash; nothing leaves your machines.
          </Prose>

          <div className="mb-8">
            <Terminal title="local-llm">
              <Comment># Install Ollama on the dev machine</Comment>
              <Cmd>curl -fsSL https://ollama.com/install.sh | sh</Cmd>
              <Divider />
              <Comment># Pull a coding model</Comment>
              <Cmd>ollama pull qwen2.5-coder</Cmd>
              <Divider />
              <Comment># Point OpenCode at the local Ollama (BYOK lane)</Comment>
              <Cmd>yaver code set byok ollama --base-url http://127.0.0.1:11434 --model qwen2.5-coder:14b</Cmd>
              <Divider />
              <Comment># Start serving (with Tailscale for zero cloud deps)</Comment>
              <Cmd>yaver serve --no-relay</Cmd>
              <Output>
                OpenCode → ollama (qwen2.5-coder:14b). Ready.
              </Output>
            </Terminal>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Zero cloud dependency
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                With Ollama + Tailscale (or LAN), you have a completely offline
                AI coding setup. No API keys, no cloud services, no subscription
                costs. Everything runs on your hardware.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Compatible model servers
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; <InlineCode>ollama</InlineCode> &mdash; Easiest setup,
                  great model library
                </li>
                <li>
                  &bull; <InlineCode>llama.cpp</InlineCode> &mdash; Maximum
                  performance, manual setup
                </li>
                <li>
                  &bull; <InlineCode>vLLM</InlineCode> &mdash; High-throughput
                  serving, GPU optimized
                </li>
                <li>
                  &bull; <InlineCode>text-generation-webui</InlineCode> &mdash;
                  Web UI + API server
                </li>
                <li>
                  &bull; Any OpenAI-compatible API server
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Section 6: Architecture ─── */}
        <section className="mb-20">
          <SectionHeading id="architecture">
            Architecture Reference
          </SectionHeading>
          <Prose>
            All networking options follow the same pattern: your phone connects
            to your dev machine through some transport, and the agent runs AI
            tools locally.
          </Prose>

          <div className="mb-8">
            <div className="terminal">
              <div className="terminal-header">
                <div className="terminal-dot bg-[#ff5f57]" />
                <div className="terminal-dot bg-[#febc2e]" />
                <div className="terminal-dot bg-[#28c840]" />
                <span className="ml-3 text-xs text-surface-500">
                  architecture
                </span>
              </div>
              <div className="terminal-body text-[13px] leading-relaxed">
                <pre className="text-surface-300">
                  {`Phone ──► [ Transport Layer ] ──► Dev Machine ──► AI Agent

Transport options:
  ├─ LAN Beacon    (same WiFi, ~5ms)
  ├─ Tailscale     (any network, ~5ms)
  ├─ QUIC Relay    (any network, ~50ms)
  └─ CF Tunnel     (corporate, ~100ms)

AI Agent options:
  ├─ Claude Code   (Anthropic API key)
  ├─ Codex CLI     (OpenAI API key)
  ├─ Aider         (any provider)
  ├─ Ollama        (local, no API key)
  └─ Custom        (any terminal command)`}
                </pre>
              </div>
            </div>
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                CLI Agent
              </h4>
              <ul className="space-y-1 text-sm text-surface-400">
                <li>
                  &bull; HTTP server on{" "}
                  <InlineCode>0.0.0.0:18080</InlineCode>
                </li>
                <li>
                  &bull; QUIC server on{" "}
                  <InlineCode>0.0.0.0:4433</InlineCode>
                </li>
                <li>&bull; All connections are outbound (no port forwarding)</li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Relay Server
              </h4>
              <ul className="space-y-1 text-sm text-surface-400">
                <li>
                  &bull; QUIC from CLI on{" "}
                  <InlineCode>4433/udp</InlineCode>
                </li>
                <li>
                  &bull; HTTP from mobile on{" "}
                  <InlineCode>8443/tcp</InlineCode>
                </li>
                <li>&bull; Pass-through proxy, stores nothing</li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Section 7: Troubleshooting ─── */}
        <section className="mb-20">
          <SectionHeading id="troubleshooting">Troubleshooting</SectionHeading>
          <Prose>
            Common issues and how to resolve them.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Can&apos;t connect to relay
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; Verify the relay is running:{" "}
                  <InlineCode>
                    curl https://relay.yourdomain.com/health
                  </InlineCode>
                </li>
                <li>
                  &bull; Check firewall: TCP 443 and UDP 4433 must be open
                </li>
                <li>
                  &bull; Verify DNS: A record must point to your VPS IP
                </li>
                <li>
                  &bull; Check password matches between CLI config and relay
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Connection drops frequently
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; Ensure the CLI agent is running:{" "}
                  <InlineCode>yaver status</InlineCode>
                </li>
                <li>
                  &bull; Check relay logs:{" "}
                  <InlineCode>docker logs -f yaver-relay</InlineCode>
                </li>
                <li>
                  &bull; Relay uses keep-alive. If behind a load balancer, set
                  idle timeout to 86400s
                </li>
                <li>
                  &bull; CLI reconnects with exponential backoff (1s &rarr; 2s
                  &rarr; 4s &rarr; max 30s)
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Slow connection
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; Try Tailscale for lowest latency (direct WireGuard,
                  ~5ms)
                </li>
                <li>
                  &bull; Deploy relay closer to your location (choose a nearby
                  region)
                </li>
                <li>
                  &bull; Check if you&apos;re on cellular &mdash; switch to WiFi
                  for LAN beacon discovery
                </li>
                <li>
                  &bull; Cloudflare Tunnel adds ~50-100ms; consider relay or
                  Tailscale for lower latency
                </li>
              </ul>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                LAN beacon not discovering devices
              </h4>
              <ul className="space-y-2 text-sm text-surface-400">
                <li>
                  &bull; Both devices must be on the same WiFi network
                </li>
                <li>
                  &bull; Some routers block UDP broadcast &mdash; try connecting
                  via Convex IP instead
                </li>
                <li>
                  &bull; Check that UDP port 19837 is not blocked by local
                  firewall
                </li>
                <li>
                  &bull; If LAN fails, Yaver automatically falls back to relay
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* ─── Self-Hosting the Backend ─── */}
        <section className="mb-20">
          <SectionHeading id="backend">Self-Hosting the Backend (Convex)</SectionHeading>
          <Prose>
            Yaver&apos;s backend uses Convex for auth and device discovery only &mdash; no task
            data ever touches it. You can run your own Convex instance to be fully
            independent of our infrastructure.
          </Prose>

          <div className="space-y-6">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Option A: Convex Cloud (free tier)
              </h4>
              <p className="mb-3 text-sm text-surface-400">
                The easiest option. Convex offers a generous free tier (1M function
                calls/month). No servers to manage.
              </p>
              <Terminal title="Convex Cloud">
                <Cmd>cd backend &amp;&amp; npm install</Cmd>
                <Cmd>npx convex dev</Cmd>
                <Comment># Follow prompts to create a free project</Comment>
                <div className="h-2" />
                <Comment># Seed with predefined data (runners, models, config)</Comment>
                <Cmd>npx convex run seed:all</Cmd>
              </Terminal>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Option B: Self-hosted Convex (Docker)
              </h4>
              <p className="mb-3 text-sm text-surface-400">
                Convex is open source. Run the entire backend on your own hardware.
              </p>
              <Terminal title="Self-Hosted Convex">
                <Cmd>git clone https://github.com/get-convex/convex-backend.git</Cmd>
                <Cmd>cd convex-backend &amp;&amp; just run-local-backend</Cmd>
                <div className="h-2" />
                <Comment># Point Yaver at your local Convex</Comment>
                <Cmd>cd /path/to/yaver/backend</Cmd>
                <Cmd>npx convex dev --url http://localhost:3210</Cmd>
                <Cmd>npx convex run seed:all</Cmd>
              </Terminal>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Configure clients
              </h4>
              <p className="text-sm text-surface-400">
                Point the CLI at your backend with{" "}
                <InlineCode>yaver config set convex_site_url https://your-project.convex.site</InlineCode>{" "}
                or <InlineCode>yaver serve --convex-url &lt;url&gt;</InlineCode>.
                For the mobile app, update <InlineCode>CONVEX_URL</InlineCode> in{" "}
                <InlineCode>mobile/src/lib/constants.ts</InlineCode>.
              </p>
            </div>
          </div>

          <div className="mt-6">
            <Prose>
              See the full{" "}
              <Link
                href="/docs/contributing"
                className="text-surface-200 underline underline-offset-2 hover:text-surface-50"
              >
                Contributing Guide
              </Link>{" "}
              for details on seed data, database schema, and adding new AI runners.
            </Prose>
          </div>
        </section>

        {/* Build, Test & Deploy Pipeline */}
        <section className="mb-12">
          <SectionHeading id="pipeline">
            Build, Test &amp; Deploy Pipeline
          </SectionHeading>
          <Prose>
            The <InlineCode>yaver build</InlineCode>, <InlineCode>yaver test</InlineCode>,{" "}
            <InlineCode>yaver deploy</InlineCode>, and <InlineCode>yaver pipeline</InlineCode>{" "}
            commands work with any self-hosted setup &mdash; no cloud CI required.
            Builds run entirely on your dev machine using your local toolchains
            (Flutter, Gradle, Xcode, React Native, Expo). Artifacts transfer P2P
            to your phone or get pushed to TestFlight / Play Store.
          </Prose>

          <div className="mb-8">
            <Terminal title="pipeline">
              <Comment># Build, test, and deploy in one command</Comment>
              <Cmd>yaver pipeline --test --deploy p2p</Cmd>
              <Divider />
              <Comment># Or run each step individually</Comment>
              <Cmd>yaver build flutter apk</Cmd>
              <Cmd>yaver test unit</Cmd>
              <Cmd>yaver deploy --target p2p</Cmd>
              <Divider />
              <Comment># Push to app stores</Comment>
              <Cmd>yaver build push testflight</Cmd>
              <Cmd>yaver build push playstore</Cmd>
            </Terminal>
          </div>

          <Prose>
            CI integration is optional. If you prefer cloud CI, use{" "}
            <InlineCode>yaver deploy --ci github</InlineCode> or{" "}
            <InlineCode>yaver deploy --ci gitlab</InlineCode> to trigger
            workflows remotely. But for most day-to-day development, P2P builds
            are faster and free.
          </Prose>
        </section>

        {/* Bottom CTA */}
        <div className="rounded-xl border border-surface-800 bg-surface-900 p-6 text-center">
          <p className="mb-2 text-sm font-medium text-surface-200">
            Need help?
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
