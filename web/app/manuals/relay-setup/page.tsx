import Link from "next/link";

export default function RelaySetupManual() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <Link
          href="/manuals"
          className="mb-8 inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to Manuals
        </Link>

        <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
          Relay server setup guide
        </h1>
        <p className="mb-12 text-sm leading-relaxed text-surface-400">
          Deploy your own relay server so your phone can reach your dev machine
          from anywhere. This guide covers quick testing setups, production
          deployments with HTTPS, client configuration, and ongoing maintenance.
        </p>

        {/* What is the Relay Server? */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            What is the relay server?
          </h2>
          <p className="mb-4 text-sm leading-relaxed text-surface-400">
            The relay is a lightweight pass-through proxy that lets your phone
            reach your dev machine when they&apos;re on different networks. Your
            dev machine opens an outbound QUIC tunnel to the relay; your phone
            sends short-lived HTTP requests to the same relay. The relay
            forwards traffic between them &mdash; it never stores any data.
          </p>
          <div className="mb-6 rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              When you need a relay
            </h3>
            <ul className="space-y-2 text-sm text-surface-400">
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>Phone and dev machine are on different networks (home vs. office, cellular vs. WiFi)</span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>Dev machine is behind NAT or a firewall that blocks inbound connections</span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>You want your machine reachable from anywhere without port forwarding</span>
              </li>
            </ul>
          </div>
          <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              When you do NOT need a relay
            </h3>
            <ul className="space-y-2 text-sm text-surface-400">
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>
                  <strong className="text-surface-300">Same WiFi network</strong> &mdash; Yaver
                  discovers your machine automatically via LAN beacon. Zero setup.
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>
                  <strong className="text-surface-300">Tailscale installed</strong> &mdash; Connect
                  directly over your tailnet. Run{" "}
                  <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver serve --no-relay</code>.
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">&#8226;</span>
                <span>
                  <strong className="text-surface-300">Cloudflare Tunnel</strong> &mdash; Routes
                  through HTTPS, no relay or VPS needed. See the{" "}
                  <Link href="/docs/self-hosting#cloudflare" className="text-surface-300 underline underline-offset-2 hover:text-surface-100">
                    self-hosting guide
                  </Link>.
                </span>
              </li>
            </ul>
          </div>
        </section>

        {/* Requirements */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Requirements
          </h2>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-6 font-medium text-surface-300">What</th>
                  <th className="pb-3 font-medium text-surface-300">Details</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">VPS with public IP</td>
                  <td className="py-3">Any provider: Hetzner, DigitalOcean, AWS Lightsail, Linode, Vultr, etc.</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">Specs</td>
                  <td className="py-3">1 vCPU, 512 MB RAM minimum. The relay is very lightweight.</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">Docker</td>
                  <td className="py-3">Docker Engine + Docker Compose plugin installed.</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 text-surface-300">Ports</td>
                  <td className="py-3">TCP 443 (HTTPS), UDP 4433 (QUIC), TCP 8443 (HTTP fallback).</td>
                </tr>
                <tr>
                  <td className="py-3 pr-6 text-surface-300">Domain name</td>
                  <td className="py-3">Optional but recommended for HTTPS. A bare IP works for testing.</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        {/* Quick Setup */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Quick setup (Docker, no HTTPS)
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            Get a relay running in under a minute. Good for testing and
            development. The QUIC tunnel between the CLI agent and relay is
            always encrypted regardless of the HTTP layer. Pick whichever
            installation method suits you:
          </p>

          {/* Option A: sparse clone */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Option A: Sparse git clone (recommended)
          </h3>
          <p className="mb-3 text-xs text-surface-500">
            Clone only the relay directory &mdash; no need to download the entire repo.
          </p>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Clone only the relay directory</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  git clone --depth 1 --filter=blob:none --sparse https://github.com/kivanccakmak/yaver.git /opt/yaver-relay
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">cd /opt/yaver-relay</span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">git sparse-checkout set relay</span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">cd relay</span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Set password and start</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  echo &quot;RELAY_PASSWORD=your-secret-here&quot; &gt; .env
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  docker compose up -d
                </span>
              </div>
            </div>
          </div>

          {/* Option B: no git */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Option B: Docker without git
          </h3>
          <p className="mb-3 text-xs text-surface-500">
            Download just the Dockerfile and docker-compose.yml &mdash; no git required.
          </p>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Download just the relay files</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  {"mkdir -p /opt/yaver-relay && cd /opt/yaver-relay"}
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  curl -sL https://raw.githubusercontent.com/kivanccakmak/yaver/main/relay/Dockerfile -o Dockerfile
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  curl -sL https://raw.githubusercontent.com/kivanccakmak/yaver/main/relay/docker-compose.yml -o docker-compose.yml
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Set password and start</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  echo &quot;RELAY_PASSWORD=your-secret-here&quot; &gt; .env
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  docker compose up -d
                </span>
              </div>
            </div>
          </div>

          {/* Option C: build from source */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Option C: Build from source (no Docker)
          </h3>
          <p className="mb-3 text-xs text-surface-500">
            Requires Go installed on the server.
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  git clone --depth 1 --filter=blob:none --sparse https://github.com/kivanccakmak/yaver.git
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd yaver &amp;&amp; git sparse-checkout set relay &amp;&amp; cd relay
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  go build -o yaver-relay .
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  ./yaver-relay serve --password your-secret
                </span>
              </div>
            </div>
          </div>

          {/* Verify */}
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">verify</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Verify it&apos;s running</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  {"curl http://<your-vps-ip>:8443/health"}
                </span>
              </div>
              <div className="pl-2 text-green-400/80">
                {"{ \"status\": \"ok\" }"}
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            This works for testing, but mobile apps in production typically
            require HTTPS. See the next section for a full production setup.
          </p>
        </section>

        {/* Production Setup */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Production setup (Docker + nginx + Let&apos;s Encrypt)
          </h2>
          <p className="mb-6 text-sm text-surface-400">
            A complete production deployment with HTTPS, automatic certificate
            renewal, and proper firewall configuration.
          </p>

          {/* Step 1 */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 1: Point DNS to your VPS
          </h3>
          <p className="mb-4 text-sm text-surface-400">
            Create an A record in your DNS provider pointing to your VPS IP address.
          </p>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">DNS</span>
            </div>
            <div className="terminal-body text-[13px]">
              <pre className="text-surface-300">
{`Type: A
Name: relay.yourdomain.com
Value: <your-vps-ip>
TTL: 300`}
              </pre>
            </div>
          </div>

          {/* Step 2 */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 2: SSH into your VPS and install prerequisites
          </h3>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  ssh root@your-vps-ip
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Install Docker, nginx, and certbot</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  # install Docker, nginx, and certbot with your server&apos;s standard package flow
                </span>
              </div>
            </div>
          </div>

          {/* Step 3 */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 3: Get an SSL certificate
          </h3>
          <p className="mb-4 text-sm text-surface-400">
            Stop nginx temporarily so certbot can bind to port 80 for the
            certificate challenge.
          </p>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  systemctl stop nginx
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  certbot certonly --standalone -d relay.yourdomain.com --non-interactive --agree-tos -m you@email.com
                </span>
              </div>
              <div className="pl-2 text-green-400/80">
                Certificate saved to /etc/letsencrypt/live/relay.yourdomain.com/fullchain.pem
              </div>
            </div>
          </div>

          {/* Step 4 */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 4: Configure nginx
          </h3>
          <p className="mb-4 text-sm text-surface-400">
            Create an nginx config that terminates SSL and proxies to the relay&apos;s
            HTTP port. The long timeouts are important for streaming responses.
          </p>
          <div className="terminal mb-6">
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
    listen 443 ssl;
    server_name relay.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/relay.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/relay.yourdomain.com/privkey.pem;

    # Recommended SSL settings
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    location / {
        proxy_pass http://127.0.0.1:8443;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Long timeouts for SSE / streaming
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
        proxy_buffering off;
    }
}

# Redirect HTTP to HTTPS
server {
    listen 80;
    server_name relay.yourdomain.com;
    return 301 https://$host$request_uri;
}`}
              </pre>
            </div>
          </div>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Enable the site and start nginx</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  ln -s /etc/nginx/sites-available/relay /etc/nginx/sites-enabled/
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  nginx -t &amp;&amp; systemctl enable nginx &amp;&amp; systemctl start nginx
                </span>
              </div>
            </div>
          </div>

          {/* Step 5 */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 5: Clone and start the relay
          </h3>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Sparse clone &mdash; only downloads the relay directory</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  git clone --depth 1 --filter=blob:none --sparse https://github.com/kivanccakmak/yaver.git /opt/yaver-relay
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd /opt/yaver-relay &amp;&amp; git sparse-checkout set relay &amp;&amp; cd relay
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  echo &quot;RELAY_PASSWORD=your-secret-here&quot; &gt; .env
                </span>
              </div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  docker compose up -d
                </span>
              </div>
            </div>
          </div>

          {/* Step 6 */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 6: Open firewall ports
          </h3>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># HTTPS for mobile app connections</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">ufw allow 443/tcp</span>
              </div>
              <div className="text-surface-500"># QUIC for CLI agent tunnels</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">ufw allow 4433/udp</span>
              </div>
              <div className="text-surface-500"># HTTP for Let&apos;s Encrypt renewal</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">ufw allow 80/tcp</span>
              </div>
            </div>
          </div>

          {/* Step 7 */}
          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Step 7: Verify
          </h3>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  curl https://relay.yourdomain.com/health
                </span>
              </div>
              <div className="pl-2 text-green-400/80">
                {"{ \"status\": \"ok\" }"}
              </div>
            </div>
          </div>
        </section>

        {/* Automated Setup Script */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Automated setup script
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            If you prefer a one-liner, the deploy script handles everything:
            copies the relay binary, sets up systemd or Docker, and starts the
            service.
          </p>
          <div className="terminal mb-4">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Deploy via Docker (recommended)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd yaver/relay &amp;&amp; ./deploy/up.sh &lt;server-ip&gt; --docker
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Deploy via binary + systemd</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd yaver/relay &amp;&amp; ./deploy/up.sh &lt;server-ip&gt;
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Stop relay</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd yaver/relay &amp;&amp; ./deploy/down.sh &lt;server-ip&gt;
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Stop + purge everything</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd yaver/relay &amp;&amp; ./deploy/down.sh &lt;server-ip&gt; --purge
                </span>
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            The script SSHs into the server as root, copies the relay binary
            (or builds via Docker), configures a systemd service, and starts it.
            You still need to set up nginx and certbot manually for HTTPS.
          </p>
        </section>

        {/* Configure Clients */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Configure clients
          </h2>
          <p className="mb-6 text-sm text-surface-400">
            Point the CLI and mobile app to your relay server.
          </p>

          <div className="space-y-4">
            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <h3 className="mb-3 text-sm font-medium text-surface-200">
                CLI
              </h3>
              <div className="terminal mb-3">
                <div className="terminal-header">
                  <div className="terminal-dot bg-[#ff5f57]" />
                  <div className="terminal-dot bg-[#febc2e]" />
                  <div className="terminal-dot bg-[#28c840]" />
                  <span className="ml-3 text-xs text-surface-500">terminal</span>
                </div>
                <div className="terminal-body space-y-2 text-[13px]">
                  <div className="text-surface-500"># Add your relay server</div>
                  <div>
                    <span className="text-surface-400">$</span>{" "}
                    <span className="text-surface-200 select-all">
                      yaver relay add https://relay.yourdomain.com --password your-secret
                    </span>
                  </div>
                  <div className="h-px bg-surface-800/60" />
                  <div className="text-surface-500"># Test the connection</div>
                  <div>
                    <span className="text-surface-400">$</span>{" "}
                    <span className="text-surface-200 select-all">yaver relay test</span>
                  </div>
                  <div className="pl-2 text-green-400/80">
                    Relay relay.yourdomain.com: OK (52ms)
                  </div>
                </div>
              </div>
              <p className="text-xs text-surface-500">
                Or manually edit{" "}
                <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">~/.yaver/config.json</code>:
              </p>
              <div className="terminal mt-3">
                <div className="terminal-body text-[13px]">
                  <pre className="text-surface-300">
{`{
  "relay_servers": [
    {
      "id": "my-relay",
      "quic_addr": "<your-vps-ip>:4433",
      "http_url": "https://relay.yourdomain.com"
    }
  ],
  "relay_password": "your-secret-here"
}`}
                  </pre>
                </div>
              </div>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <h3 className="mb-2 text-sm font-medium text-surface-200">
                Mobile app
              </h3>
              <p className="text-sm text-surface-400">
                Open the Yaver app &rarr; Settings &rarr; Relay Servers &rarr;
                Add &rarr; Enter your relay URL and password.
              </p>
            </div>
          </div>
        </section>

        {/* Monitoring & Maintenance */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Monitoring &amp; maintenance
          </h2>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Health check</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  curl https://relay.yourdomain.com/health
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># View active tunnels</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  curl https://relay.yourdomain.com/tunnels
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># View relay logs</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  docker logs -f yaver-relay
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Update to latest version</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  cd /opt/yaver-relay &amp;&amp; git pull &amp;&amp; cd relay &amp;&amp; docker compose up -d --build
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Verify SSL renewal works</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  certbot renew --dry-run
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Check server resources</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  df -h / &amp;&amp; free -h &amp;&amp; docker ps
                </span>
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            Certbot auto-renews certificates via a systemd timer. No manual
            intervention needed, but it&apos;s good practice to verify with{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">certbot renew --dry-run</code>{" "}
            after initial setup.
          </p>
        </section>

        {/* Changing the Relay Password */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Changing the relay password
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            The relay password can be changed at any time without restarting the
            server. Changes take effect immediately &mdash; all new connections
            and proxy requests will require the new password.
          </p>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Option A: Via the API
          </h3>
          <p className="mb-3 text-xs text-surface-500">
            Call the admin endpoint from anywhere &mdash; the mobile app, web
            dashboard, or a simple curl command all use this under the hood.
          </p>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div className="text-surface-500"># Change password (provide current password if one is set)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  {"curl -X POST https://relay.example.com/admin/set-password \\"}
                </span>
              </div>
              <div className="pl-4">
                <span className="text-surface-200 select-all">
                  {"-H \"Content-Type: application/json\" \\"}
                </span>
              </div>
              <div className="pl-4">
                <span className="text-surface-200 select-all">
                  {"-d '{\"current_password\":\"old\",\"password\":\"new\"}'"}
                </span>
              </div>
              <div className="pl-2 text-green-400/80">
                {"{ \"ok\": true, \"message\": \"Password updated\" }"}
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># First-time setup (no current password needed)</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  {"curl -X POST https://relay.example.com/admin/set-password \\"}
                </span>
              </div>
              <div className="pl-4">
                <span className="text-surface-200 select-all">
                  {"-H \"Content-Type: application/json\" \\"}
                </span>
              </div>
              <div className="pl-4">
                <span className="text-surface-200 select-all">
                  {"-d '{\"password\":\"my-secret\"}'"}
                </span>
              </div>
              <div className="h-px bg-surface-800/60" />
              <div className="text-surface-500"># Check if password is set</div>
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  curl https://relay.example.com/admin/status
                </span>
              </div>
              <div className="pl-2 text-green-400/80">
                {"{ \"ok\": true, \"password_set\": true, \"tunnels\": 2, \"uptime\": \"4h32m10s\" }"}
              </div>
            </div>
          </div>

          <h3 className="mb-2 mt-6 text-sm font-semibold text-surface-200">
            Option B: Via the CLI
          </h3>
          <div className="terminal mb-6">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-3 text-xs text-surface-500">terminal</span>
            </div>
            <div className="terminal-body space-y-2 text-[13px]">
              <div>
                <span className="text-surface-400">$</span>{" "}
                <span className="text-surface-200 select-all">
                  yaver-relay set-password new-secret-here
                </span>
              </div>
            </div>
          </div>
          <p className="text-xs text-surface-500">
            The CLI command writes the password to a file and updates the{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">.env</code> file,
            but requires a restart to take effect. The API method is preferred
            because it updates the running server immediately and also persists to
            a{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">.relay-password</code>{" "}
            file for the next restart. Password priority on startup: CLI flag &gt;{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">RELAY_PASSWORD</code>{" "}
            env var &gt;{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">.relay-password</code>{" "}
            file.
          </p>
        </section>

        {/* ATS (App Transport Security) */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            App Transport Security (iOS HTTPS requirement)
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            iOS enforces App Transport Security (ATS), which requires HTTPS with
            a valid TLS certificate for all network connections. Your relay
            server must have HTTPS configured or the mobile app will refuse to
            connect on iOS. Android is more lenient but also recommends HTTPS.
          </p>
          <div className="mb-6 rounded-lg border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="mb-3 text-sm font-semibold text-surface-200">
              Options for HTTPS
            </h3>
            <ul className="space-y-3 text-sm text-surface-400">
              <li className="flex gap-3">
                <span className="text-surface-500">1.</span>
                <span>
                  <strong className="text-surface-300">Domain + Let&apos;s Encrypt</strong>{" "}
                  (recommended) &mdash; Free, auto-renewing certificates. Covered
                  in the production setup section above.
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">2.</span>
                <span>
                  <strong className="text-surface-300">Tailscale</strong>{" "}
                  (easiest) &mdash; HTTPS via MagicDNS with zero certificate
                  management. If you just want connectivity without running your
                  own relay, use Tailscale instead.
                </span>
              </li>
              <li className="flex gap-3">
                <span className="text-surface-500">3.</span>
                <span>
                  <strong className="text-surface-300">Cloudflare Proxy</strong>{" "}
                  (free tier) &mdash; Point your domain through Cloudflare and
                  get HTTPS automatically.
                </span>
              </li>
            </ul>
          </div>
          <p className="text-sm text-surface-400">
            For the simplest setup with no certificate management at all, use
            Tailscale instead of a relay server. Your phone and dev machine join
            the same tailnet and connect directly &mdash; no relay, no
            certificates, no VPS needed.
          </p>
        </section>

        {/* Docker Configuration Reference */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Docker configuration reference
          </h2>
          <p className="mb-4 text-sm text-surface-400">
            The relay ships with a{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">docker-compose.yml</code>{" "}
            in the <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">relay/</code> directory.
            Here are the environment variables you can configure in the{" "}
            <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">.env</code> file:
          </p>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-6 font-medium text-surface-300">Variable</th>
                  <th className="pb-3 pr-6 font-medium text-surface-300">Default</th>
                  <th className="pb-3 font-medium text-surface-300">Description</th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 font-mono text-xs text-surface-300">RELAY_PASSWORD</td>
                  <td className="py-3 pr-6">(required)</td>
                  <td className="py-3">Shared secret between relay, CLI agents, and mobile apps. Must match on all sides.</td>
                </tr>
                <tr className="border-b border-surface-800/60">
                  <td className="py-3 pr-6 font-mono text-xs text-surface-300">QUIC_PORT</td>
                  <td className="py-3 pr-6">4433</td>
                  <td className="py-3">UDP port for QUIC tunnels from CLI agents.</td>
                </tr>
                <tr>
                  <td className="py-3 pr-6 font-mono text-xs text-surface-300">HTTP_PORT</td>
                  <td className="py-3 pr-6">8443</td>
                  <td className="py-3">TCP port for HTTP proxy. Nginx reverse-proxies to this port.</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        {/* Troubleshooting */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Troubleshooting
          </h2>
          <div className="space-y-4">
            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <code className="text-sm font-semibold text-surface-100">Health check fails</code>
              <ul className="mt-2 space-y-2 text-sm text-surface-400">
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Check Docker is running:{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">docker ps</code>
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Check port bindings:{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">docker port yaver-relay</code>
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Check relay logs:{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">docker logs yaver-relay</code>
                  </span>
                </li>
              </ul>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <code className="text-sm font-semibold text-surface-100">Mobile can&apos;t connect</code>
              <ul className="mt-2 space-y-2 text-sm text-surface-400">
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>Verify the HTTPS certificate is valid and not expired</span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Check nginx config:{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">nginx -t</code>
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Ensure TCP 443 is open:{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">ufw status</code>
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Confirm{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">proxy_buffering off</code>{" "}
                    is set in nginx (required for streaming)
                  </span>
                </li>
              </ul>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <code className="text-sm font-semibold text-surface-100">CLI agent can&apos;t register</code>
              <ul className="mt-2 space-y-2 text-sm text-surface-400">
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Check the relay password matches on both sides (CLI config and relay{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">.env</code>)
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Ensure UDP 4433 is open:{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">ufw allow 4433/udp</code>
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Run{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">yaver logs -f</code>{" "}
                    on the CLI side to see connection errors
                  </span>
                </li>
              </ul>
            </div>

            <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-4">
              <code className="text-sm font-semibold text-surface-100">Connection drops</code>
              <ul className="mt-2 space-y-2 text-sm text-surface-400">
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Check relay logs for errors:{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">docker logs -f yaver-relay</code>
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    Verify nginx{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">proxy_read_timeout</code>{" "}
                    is set to{" "}
                    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-400">86400s</code>
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    If behind a cloud load balancer, set its idle timeout to at least 86400s
                  </span>
                </li>
                <li className="flex gap-3">
                  <span className="text-surface-500">&#8226;</span>
                  <span>
                    The CLI reconnects automatically with exponential backoff (1s, 2s, 4s, ... max 30s)
                  </span>
                </li>
              </ul>
            </div>
          </div>
        </section>

        {/* Footer */}
        <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-6">
          <h3 className="mb-2 text-sm font-semibold text-surface-200">
            Need more?
          </h3>
          <p className="text-sm text-surface-400">
            See the{" "}
            <Link href="/docs/self-hosting" className="text-surface-300 underline underline-offset-2 hover:text-surface-100">
              self-hosting guide
            </Link>{" "}
            for other networking options (Tailscale, Cloudflare Tunnel), or open an issue on{" "}
            <a
              href="https://github.com/kivanccakmak/yaver/issues"
              target="_blank"
              rel="noopener noreferrer"
              className="text-surface-300 underline underline-offset-2 hover:text-surface-100"
            >
              GitHub
            </a>.
          </p>
        </div>

        <div className="mt-12 flex items-center justify-between">
          <Link
            href="/manuals"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            &larr; All manuals
          </Link>
          <Link
            href="/manuals/cli-setup"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            CLI setup guide &rarr;
          </Link>
        </div>
      </div>
    </div>
  );
}
