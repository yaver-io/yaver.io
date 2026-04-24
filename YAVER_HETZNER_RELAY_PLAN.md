# Hetzner-as-relay-hub plan (pick up tomorrow)

Status: draft
Date: 2026-04-24

## Problem (tonight)

Prod Convex (`perceptive-minnow-557`) had an empty `platformConfig.relay_servers`
row. That's why the web dashboard showed "Relays configured: 0" and nothing
could connect — a browser on `https://yaver.io` cannot fetch `http://<LAN-IP>:18080`
(mixed content), so the relay path is the only usable transport from the web.
The dev deployment (`shocking-echidna-394`) had the row populated; prod did
not.

**Fix applied:** copied the existing relay row (`public.yaver.io`,
`46.224.110.38:4433`) to prod via
`npx convex run --prod platformConfig:set`. Web should now see one relay
after a hard refresh. Re-auth button should light up. Re-connect should
succeed if agents are registered with that relay.

That was the symptom. The root cause (two Convex deployments drifted) is
the real issue: every time we bring up a fresh Convex deployment, we have
to re-seed `relay_servers` on it or it silently breaks the web.

## What you actually asked for: Hetzner as the relay hub

> "make [a plan] for all machines I have to connect to them from
> Hetzner if possible"

Replace `public.yaver.io` (external) with a Yaver-owned relay running on
Hetzner dedicated hardware, so:

- Every agent on your personal machines (`simkab-Vostro-3888`,
  `192.168.1.105`, `CarrotBytePC`, `Ofis2`, etc.) opens an outbound QUIC
  tunnel to Hetzner.
- The web dashboard + mobile app hit the Hetzner relay over HTTPS.
- The relay pass-throughs requests to the right agent by deviceId.
- Nothing flows through any third-party box.

## Not the test-ephemeral box

Do **not** put this on `yaver-test-ephemeral`. That box is labeled
"disposable" in CLAUDE.md and gets snapshot/recreated. A production
relay wants a separate, long-lived machine.

Recommended shape: a cheap dedicated Hetzner VPS (CX22, €4.51/mo,
2 vCPU / 4 GB, Helsinki or Nuremberg), public IPv4, systemd-managed,
behind `relay.yaver.io` DNS.

## Bring-up checklist (for tomorrow)

1. **Provision the VPS.**
   ```bash
   cd ci/hcloud
   ./create-server.sh --name yaver-relay-prod-1 --type cx22 --location hel1
   ```
   (if you want EU; `nbg1` for Nuremberg.) Write down the IP.

2. **Point DNS.** Cloudflare → yaver.io zone → add `relay.yaver.io
   A <vps-ip>`. Proxy mode: DNS-only (not orange-cloud) — the relay
   speaks QUIC on UDP/4433, which Cloudflare can't proxy.

3. **Deploy the relay via Docker** (from the repo):
   ```bash
   export RELAY_SERVER_IP=<vps-ip>
   export RELAY_DOMAIN=relay.yaver.io
   export RELAY_PASSWORD=$(openssl rand -hex 16)
   ./relay/deploy/up.sh $RELAY_SERVER_IP --docker \
     --domain $RELAY_DOMAIN \
     --password $RELAY_PASSWORD
   ```
   This stands up `relay` + `caddy` (for the HTTPS frontend),
   issues a Let's Encrypt cert for `relay.yaver.io`, opens
   `udp/4433` + `tcp/443`, enables the systemd unit.

4. **Verify health:**
   ```bash
   curl -I https://relay.yaver.io/health
   # expect 200, body {"ok":true}
   ```

5. **Update prod Convex `platformConfig.relay_servers`:**
   ```bash
   cd backend
   RELAY_JSON=$(cat <<EOF
   [{"id":"hetzner-eu","quicAddr":"<vps-ip>:4433","httpUrl":"https://relay.yaver.io","region":"eu","priority":1,"label":"Hetzner EU","password":"$RELAY_PASSWORD"}]
   EOF
   )
   npx convex run --prod platformConfig:set \
     "{\"key\":\"relay_servers\",\"value\":$(jq -Rs . <<< "$RELAY_JSON")}"
   ```
   Both web and mobile pick this up on next launch.

6. **Re-register all agents.** On each of your boxes:
   ```bash
   yaver relay add https://relay.yaver.io --password $RELAY_PASSWORD
   yaver relay set-default https://relay.yaver.io
   systemctl --user restart yaver   # or just `yaver serve` restart
   ```
   Or, if you'd rather flip the whole user account to the new managed
   relay and let each agent pick up from Convex:
   ```bash
   yaver managed set relay true
   ```

7. **Decommission `public.yaver.io`** (or keep it as a fallback
   `priority: 2`). Running two relays is the cheap HA story — if
   Hetzner reboots, the public one stays up.

8. **Close `relay` access** (so only agents with the relay password can
   register) — already default; just confirm the systemd unit sets
   `--password $RELAY_PASSWORD`.

## Hardening (not tomorrow, but before this is your only path)

- **Relay HA.** Two relays in different regions (Helsinki + Nuremberg
  or Helsinki + Falkenstein). Keep the priority in platformConfig sorted.
- **Auto-register.** Agent should re-register itself with the configured
  relay on every boot so a new relay URL doesn't require manual agent
  touch.
- **Monitor.** Add `relay.yaver.io/health` to a Cloudflare uptime monitor
  (already in `web/components/dashboard/OpsView.tsx`'s uptime feature
  set).
- **Rotate the password.** When you rotate, update Convex
  platformConfig; all agents pick up the new password from their next
  heartbeat (see `desktop/agent/relay.go` — it already pulls relay
  config from Convex).

## Why this solves "agent up but I can't connect from web"

Today:
- You sit in a cafe / on 5G → HTTPS web on yaver.io can't hit
  `http://10.0.0.30:18080` (mixed content).
- The only way in is through a relay. If the relay isn't registered
  or isn't in platformConfig, the web has nowhere to go.
- The re-auth button I shipped tonight literally requires a relay too,
  because it POSTs `/auth/recover` through the same transport.

Tomorrow (with Hetzner relay):
- Your agents on every personal machine hold an outbound QUIC tunnel
  to Hetzner. No inbound ports needed on your home router.
- Web on yaver.io hits `https://relay.yaver.io/d/<deviceId>/...` → Caddy
  terminates TLS → relay forwards through the QUIC tunnel to the
  agent → response comes back.
- Re-auth via `/auth/recover {mode:"direct"}` works through that same
  path. So "agent up but I can't connect" is fixable with a single
  click from any browser.

## One-command morning starter

```bash
# from repo root
./scripts/bring-up-hetzner-relay.sh  # TBD — write this as the first task tomorrow
```

That wrapper should:

1. Provision the VPS (step 1).
2. Prompt for DNS confirmation (step 2 is manual in Cloudflare).
3. Run `relay/deploy/up.sh` (step 3).
4. `npx convex run --prod platformConfig:set` (step 5).
5. Print the per-agent `yaver relay add` command for you to copy onto
   each box.

Writing that script is the first 30-minute block of tomorrow.
