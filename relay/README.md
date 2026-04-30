# Yaver Relay Server

Application-layer P2P relay for Yaver. Enables mobile-to-desktop connectivity without Tailscale, VPN, or TUN/TAP — works through carrier-grade NAT, firewalls, and roaming.

## Architecture

1. The **mobile app** sends short-lived HTTP requests from Wi-Fi or 5G.
2. The **relay server** runs on your VPS with a public IP and forwards traffic only when needed.
3. The **desktop agent** keeps a persistent outbound QUIC connection, so no inbound port opening is required.
4. **Convex** is used only for auth and platform configuration.

### Connection flow

1. **Mobile app starts** → fetches relay server list from `GET /config` (public, no auth, cached 5 min)
2. **Desktop agent starts** → fetches relay servers from Convex, connects outbound to ALL relays via QUIC tunnels
3. **User selects a device** → mobile tries direct connection first (3s timeout), then each relay in priority order (5s timeout each)
4. **If a relay goes down** → traffic automatically routes through remaining relays — no user-visible failure

### How it solves NAT traversal & roaming

| Scenario | How it handles it |
|---|---|
| **Wi-Fi → 5G roaming** | Mobile makes short HTTP requests to relay — IP changes don't affect anything |
| **Carrier-grade NAT (CGNAT)** | Agent connects *outbound* to relay via QUIC — no inbound ports needed |
| **Both behind NAT** | Both sides connect outbound to relay — relay bridges them |
| **Dead zones / elevator** | Tunnel client auto-reconnects with exponential backoff (1s → 2s → 4s → 8s → max 30s) |
| **QUIC connection migration** | Agent's QUIC tunnel survives minor IP changes via connection ID |
| **VPN conflict (SurfShark etc.)** | No TUN/TAP — pure application-layer proxy, no VPN rights needed |

### Design decisions

- **No TUN/TAP**: Pure application-layer HTTP proxy. Won't conflict with existing VPNs.
- **No libp2p**: Lean implementation with just `quic-go` (~5 deps vs 100+).
- **Existing HTTP API preserved**: Mobile app just changes base URL from `http://tailscale-ip:18080/tasks` to `http://relay:8443/d/{deviceId}/tasks`.
- **QUIC for tunnel**: UDP-based, connection migration, efficient multiplexing.
- **Direct-first, relay-fallback**: Clients try direct connection first, fall back to relay automatically.
- **Multi-relay redundancy**: Multiple relay servers supported — if one goes down, traffic routes through others.
- **Platform-managed**: Relay config lives in Convex `platformConfig`, not per-device or per-user config.

### Remote Runtime compatibility

Yaver's native `Remote Runtime` lane is relay-compatible.

- `direct-webrtc` sessions still prefer direct host reachability when available.
- `relay-jpeg-poll` sessions deliberately use the relay's normal authenticated HTTP proxy path.
- The relay does not need TURN support for `relay-jpeg-poll`; it only needs to forward the standard remote-runtime endpoints such as:
  - `GET /d/{deviceId}/remote-runtime/capabilities`
  - `POST /d/{deviceId}/remote-runtime/sessions`
  - `POST /d/{deviceId}/remote-runtime/sessions/:id/control`
  - `POST /d/{deviceId}/remote-runtime/sessions/:id/command`
  - `GET /d/{deviceId}/remote-runtime/sessions/:id/frame`

That means the current relay is already suitable for the fallback transport used by Swift/Kotlin remote-runtime sessions. Full TURN-style media relay would still be a separate future feature for first-class cross-network WebRTC media.

## Multi-Relay Architecture

Relay servers are stored as a JSON array in Convex `platformConfig` under the key `relay_servers`:

```json
[
  {"id": "relay1", "quicAddr": "<your-ip>:4433", "httpUrl": "https://relay.yourdomain.com", "region": "eu", "priority": 1},
  {"id": "relay2", "quicAddr": "<your-ip>:4433", "httpUrl": "http://<your-ip>:8443", "region": "us", "priority": 2}
]
```

- **Desktop agent**: Fetches relay list from Convex on startup, connects outbound to ALL relays via QUIC tunnels
- **Mobile app**: Fetches relay list from `GET /config` on startup, tries each relay in priority order when connecting to a device
- **Failover**: If a relay goes down, agent reconnects with exponential backoff; mobile tries next relay in list
- **Adding a new relay**: Deploy relay binary/Docker to new server → add entry to `relay_servers` in Convex → clients pick it up automatically

### Managing relay servers in Convex

```bash
# View current relay servers
npx convex run platformConfig:get '{"key":"relay_servers"}'

# Set relay servers (replaces entire list)
npx convex run platformConfig:set '{"key":"relay_servers","value":"[{\"id\":\"relay1\",\"quicAddr\":\"<your-ip>:4433\",\"httpUrl\":\"https://relay.yourdomain.com\",\"region\":\"eu\",\"priority\":1}]"}'
```

## One-Line Install (any VPS)

Install a self-hosted relay on any Linux VPS with a single command:

```bash
curl -fsSL https://yaver.io/install-relay.sh | sudo bash -s -- \
  --domain relay.example.com \
  --password your-secret-password
```

This automatically:
1. Installs Docker
2. Pulls and runs the relay container
3. Sets up nginx reverse proxy
4. Gets Let's Encrypt SSL (auto-renewing)
5. Configures firewall
6. Sets up Watchtower for auto-updates

**Requirements:** Linux VPS with root access, domain pointing to the server's IP (A record), ports 80/443/4433 open.

Then add it to your Yaver CLI:
```bash
yaver relay add https://relay.example.com --password your-secret-password
```

The relay is a pass-through proxy — it never stores, reads, or logs your data. All connections are encrypted via QUIC (TLS 1.3).

## Quick Start (from source)

### Build

```bash
cd relay
go build -o yaver-relay .
```

### Run relay server locally

```bash
./yaver-relay serve --quic-port=4433 --http-port=8443
```

### Test locally

```bash
# Terminal 1: Start relay
./yaver-relay serve

# Terminal 2: Start agent (auto-fetches relays from Convex)
cd desktop/agent && go run . serve --debug

# Terminal 3: Test via relay
curl http://127.0.0.1:8443/d/<deviceId>/health
curl http://127.0.0.1:8443/tunnels
```

## Deploy to a VPS

### Docker deploy

```bash
# Build and deploy (sparse checkout — only clones relay/)
./deploy/up.sh <server-ip> --docker

# Or manually:
ssh root@<server-ip>
cd /opt/yaver-relay/relay
docker compose up -d --build
```

### Binary deploy (smaller footprint)

```bash
# Build + deploy + start systemd service
./deploy/up.sh <server-ip>

# Cross-compile for ARM64:
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o yaver-relay-linux-arm64 .
```

### Manual Docker

```bash
# Build
docker build -t yaver-relay .

# Run
docker run -d --name yaver-relay \
  --restart unless-stopped \
  -p 4433:4433/udp \
  -p 8443:8443/tcp \
  yaver-relay

# Logs
docker logs -f yaver-relay

# Health
curl http://localhost:8443/health
```

### Docker Compose

```bash
docker compose up -d --build
docker compose logs -f
docker compose down
```

### Stop relay

```bash
./deploy/down.sh <server-ip>             # Stop service
./deploy/down.sh <server-ip> --purge     # Stop + remove everything
```

## Operations

### Monitoring

```bash
# Docker logs
ssh root@<server-ip> docker logs -f yaver-relay

# Systemd logs (if binary deploy)
ssh root@<server-ip> journalctl -u yaver-relay -f
ssh root@<server-ip> journalctl -u yaver-relay --since today

# Health check
curl http://<server-ip>:8443/health

# Active tunnels
curl http://<server-ip>:8443/tunnels
```

### Systemd management (binary deploy)

```bash
systemctl start yaver-relay
systemctl stop yaver-relay
systemctl restart yaver-relay
systemctl status yaver-relay
systemctl enable yaver-relay    # auto-start on boot
```

### Adding a new relay server

1. Provision a VPS (any cloud — Hetzner, DigitalOcean, etc.)
2. Deploy relay binary or Docker to the new server
3. Open ports: 4433/udp (QUIC) + 8443/tcp (HTTP)
4. Verify health: `curl http://<new-ip>:8443/health`
5. Add to Convex `platformConfig`:
   ```bash
   cd backend
   npx convex run platformConfig:get '{"key":"relay_servers"}'
   # Copy existing JSON, append new entry, set:
   npx convex run platformConfig:set '{"key":"relay_servers","value":"[...existing...,{\"id\":\"new1\",\"quicAddr\":\"<ip>:4433\",\"httpUrl\":\"http://<ip>:8443\",\"region\":\"<region>\",\"priority\":2}]"}'
   ```
6. Agents and mobile clients will pick up the new relay automatically on next startup/refresh

### Removing a relay server

1. Remove the entry from `relay_servers` in Convex `platformConfig`
2. Wait for agents/mobile to refresh (agents reconnect on backoff, mobile fetches on startup)
3. Tear down the server: `./deploy/down.sh <ip> --purge`

### UDP buffer tuning (recommended)

For optimal QUIC performance, increase UDP buffer sizes on the relay server:

```bash
# /etc/sysctl.d/99-yaver-relay.conf
net.core.rmem_max=7500000
net.core.wmem_max=7500000
```

Apply: `sysctl --system`

## API Endpoints

| Endpoint | Description |
|---|---|
| `GET /health` | Relay health + active tunnel count |
| `GET /tunnels` | List connected agent tunnels (device IDs, uptime) |
| `ANY /d/{deviceId}/*` | Proxy request through tunnel to agent |

## Ports

| Port | Protocol | Direction | Purpose |
|---|---|---|---|
| 4433 | UDP (QUIC) | Inbound from agents | Agent tunnel connections |
| 8443 | TCP (HTTP) | Inbound from mobile | Mobile client proxy requests |

## Protocol

```
Agent ──QUIC──► Relay                    Mobile ──HTTP──► Relay
    │                                         │
    ├─ stream 0: RegisterMsg ──►              ├─ GET /d/{id}/health ──►
    │            ◄── RegisterResp             │
    │                                         │  Relay opens stream N on
    │  (connection stays alive)               │  agent's QUIC connection:
    │                                         │
    ├─ stream N: ◄── TunnelRequest            ├─ TunnelRequest ──► Agent
    │            TunnelResponse ──►           │  ◄── TunnelResponse
    │                                         │
    │  (QUIC keepalive every 20s)             ├─ HTTP response ◄──
```

## Future: Direct Connection Upgrade (Hole Punching)

The protocol includes `PeerInfo` messages for hole-punch coordination:

1. After both peers connect to relay, relay shares each peer's observed public address
2. Both attempt direct QUIC connection to each other
3. If direct connection succeeds → bypass relay, lower latency
4. If not (symmetric NAT) → relay continues proxying

Not implemented yet — the relay alone handles 100% of cases.
