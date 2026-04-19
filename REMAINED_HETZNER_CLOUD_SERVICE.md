# Yaver Cloud — remaining tasks before the managed tier is live

Everything in this file is a human task. Once you finish it, the
LemonSqueezy checkout + Hetzner provisioning + custom-domain flow
that is already in the codebase will run end-to-end. Nothing here
needs more code; each item is either a credential you generate at a
provider, an env var you paste into Convex, or a button you press
in the LemonSqueezy dashboard.

Work top-to-bottom. Every section ends with a one-line verify step
so you can tell it's wired correctly without running the full flow.

---

## 0. What's already done (so you don't re-do it)

- Convex schema + functions (`userDomains` table, `cloudMachines.provision`
  real Hetzner call, `provisionRelay` custom-domain binding, HMAC webhook
  verification, env-var compat for `LEMON_SQUEEZY_*` AND `LEMONSQUEEZY_*`).
- Agent LemonSqueezy client + sandbox flag (`LSStatus.IsSandbox`).
- `cloud/docker-compose.yml` boots with `YAVER_CLOUD_TENANT=1` so
  auth rejections include a hint and `YAVER_CLOUD_CHECKOUT_URL`.
- Mobile `pushPhoneProject` detects 402 (subscription required) and
  surfaces a message — **never opens checkout in-app** (Apple rule).
- Web `/dashboard` gained a **Domains** tab driven by `userDomains`.
- DNS provider adapter (`dns_provider.go`) with Cloudflare + manual
  fallback so users with Namecheap/Porkbun/GoDaddy get paste-able
  instructions instead of an error.
- Custom-domain TLS is fully automated on every provisioned cloud
  machine: a `yaver-tls.timer` systemd unit ticks every 5 min, pulls
  pending `userDomains` rows via `/machine/pending-tls`, runs
  `certbot --nginx`, and POSTs the result back. See §7.1.

Verify: `cd backend && npx convex env list | grep -i lemon` should
show `LEMON_SQUEEZY_API_KEY` and `LEMON_SQUEEZY_STORE_ID`. If those
lines are there, your Convex deploy includes slice 1-4.

---

## 1. LemonSqueezy — test-mode credentials

The current `LEMON_SQUEEZY_API_KEY` in Convex is expired (the store
probe returned `401 Your API key has expired`). Rotate it and add
the missing variant + webhook-secret vars.

### 1.1 Create (or confirm) a test-mode store

1. Log in to <https://app.lemonsqueezy.com/>.
2. Top-right store switcher → **Test mode** toggle ON. Create a
   store if one doesn't exist. Note the store ID (URL slug) — on
   this account it's currently `156373`.
3. **Products → New product → "Yaver Cloud"**. Add one variant at
   whatever price (start at $9/mo). Note the **variant ID** (visible
   in the URL of the variant page, e.g.
   `/stores/156373/products/12345/variants/67890` → `67890`).

### 1.2 Rotate the API key

1. **Settings → API → Create API key** (type = test-mode).
2. Copy it once — LS never shows it again.
3. Set it in Convex:
   ```bash
   cd backend
   npx convex env set LEMONSQUEEZY_API_KEY "lsk_test_xxx..."
   npx convex env set LEMONSQUEEZY_STORE_ID "156373"
   npx convex env set LEMONSQUEEZY_YAVER_CLOUD_VARIANT_ID "67890"
   npx convex env set LEMONSQUEEZY_SANDBOX "true"
   ```
   (The code accepts both `LEMONSQUEEZY_*` and `LEMON_SQUEEZY_*`; the
   former is the LS-docs spelling and should be preferred for new vars.)

### 1.3 Create the webhook

1. **Settings → Webhooks → Add endpoint**.
2. Callback URL:
   ```
   https://shocking-echidna-394.eu-west-1.convex.site/webhooks/lemonsqueezy
   ```
3. Signing secret: click **Generate**, then paste into Convex:
   ```bash
   npx convex env set LEMONSQUEEZY_WEBHOOK_SECRET "whsec_xxx..."
   ```
4. Enabled events (tick all 6):
   - `subscription_created`
   - `subscription_updated`
   - `subscription_cancelled`
   - `subscription_expired`
   - `subscription_resumed`
   - `subscription_payment_failed`

**Verify:** press **Test** next to the endpoint in LS. You should see
`200 OK`. If you get `401 Invalid signature`, the secret isn't
matching — re-paste carefully (no trailing whitespace). If you get
`500`, check `npx convex logs` in another terminal.

### 1.4 Set the optional checkout UX vars

These control what users see after paying — unset = reasonable
defaults, set = branded.

```bash
npx convex env set LEMONSQUEEZY_CHECKOUT_REDIRECT_URL "https://yaver.io/dashboard?welcome=cloud"
npx convex env set LEMONSQUEEZY_YAVER_CLOUD_RECEIPT_BUTTON_TEXT "Open Yaver"
npx convex env set NEXT_PUBLIC_BASE_URL "https://yaver.io"
```

---

## 2. Hetzner Cloud — API token

Needed for `cloudMachines.provision` (CPU/GPU dev boxes) and
`provisionRelay.provision` (managed relays).

1. Log in to <https://console.hetzner.cloud/>.
2. Pick (or create) a **new** project for Yaver Cloud production
   work. **Do not reuse** the existing shared project that hosts the
   Talos + yaver relay boxes — a rogue deprovision loop during
   development can otherwise delete those servers.
3. **Security → API tokens → Generate API token** with
   **Read & Write** permission. Copy.
4. Paste it into Convex:
   ```bash
   cd backend
   npx convex env set HCLOUD_TOKEN "your-hetzner-token"
   ```

**Verify:**
```bash
curl -fsSL -H "Authorization: Bearer $HCLOUD_TOKEN" \
  https://api.hetzner.cloud/v1/locations | jq '.locations[].name'
# should print "fsn1", "nbg1", "hel1", "ash", "hil", ...
```

---

## 3. Cloudflare — API token + zone ID (for the yaver.io zone)

Needed for auto-creating the `<shortId>.cloud.yaver.io` and
`<shortId>.relay.yaver.io` subdomains inside the yaver.io zone, and
for the `domain_setup` wizard in the agent.

### 3.1 Get the zone ID

1. Cloudflare Dashboard → pick the `yaver.io` zone.
2. Right sidebar → **Zone ID**. Copy.

### 3.2 Create a narrowly-scoped API token

1. <https://dash.cloudflare.com/profile/api-tokens> → **Create Token**.
2. Template: **Edit zone DNS**. Scope: include only the `yaver.io`
   zone. TTL: no expiry (or set a 90-day rotation reminder).
3. Copy the token.

### 3.3 Set them in Convex

```bash
cd backend
npx convex env set CF_API_TOKEN "your-cloudflare-token"
npx convex env set CF_ZONE_ID "your-zone-id"
```

**Verify:**
```bash
curl -fsSL -H "Authorization: Bearer $CF_API_TOKEN" \
  https://api.cloudflare.com/client/v4/zones/$CF_ZONE_ID | jq .success
# should print "true"
```

---

## 4. Deploy the `cloud.yaver.io` tenant box

This is the box `yaver phone push --to https://cloud.yaver.io`
and `pushPhoneProject({kind:"yaver-cloud"})` both target. It's a
SINGLE Hetzner server running `yaver serve` behind Caddy.

### 4.1 Get a fresh VPS

- Spin up a **new** Hetzner CAX11 (ARM, €4/mo) in the EU project
  from §2. **Do not reuse** the existing shared box — it runs
  Talos + your P2P relay and you don't want to collide.
- Note the public IPv4.

### 4.2 Point DNS

In Cloudflare, add an A record:
- Name: `cloud` (for `cloud.yaver.io`)
- Target: the new server IP
- Proxy: OFF (orange cloud grey) — Caddy needs direct reach for
  ACME HTTP-01 challenges.

### 4.3 Run the deploy script

```bash
scp cloud/deploy.sh root@NEW_IP:/tmp/
ssh root@NEW_IP bash /tmp/deploy.sh
# Or fully pull-based:
ssh root@NEW_IP "curl -fsSL https://raw.githubusercontent.com/kivanccakmak/yaver.io/main/cloud/deploy.sh | bash"
```

The script:
- Installs Docker if missing.
- Clones the repo to `/opt/yaver-cloud`.
- Generates a random `CLOUD_OWNER_TOKEN` into `/opt/yaver-cloud/cloud/.env`.
- `docker compose up -d --build` both `yaver-agent` and `caddy`.

### 4.4 Grab the token you'll hand to subscribers

```bash
ssh root@NEW_IP "grep CLOUD_OWNER_TOKEN /opt/yaver-cloud/cloud/.env"
```

Store it in your password manager. It's the single shared secret
that mobile/CLI clients use to push to the cloud tenant.

**Verify:** `curl https://cloud.yaver.io/health` → `{"ok":true,...}`.

---

## 5. Deploy the web dashboard (so the Domains tab is live)

```bash
./scripts/deploy-web.sh
```

The dashboard's **Domains** tab (new) calls the Convex
`userDomains` mutations/queries you deployed earlier.

**Verify:** sign in at <https://yaver.io/dashboard>, click
**Domains** in the tab bar — the "Add a domain" form should
render without errors.

---

## 6. First end-to-end test (test-mode, no real money)

1. Sign up a throwaway user via the web (or reuse your own).
2. Visit `/pricing` and click **Start Yaver Cloud**. LS opens a
   test-mode checkout. Fill with LS test card `4242 4242 4242 4242`,
   any future expiry, any CVC. Complete.
3. Convex receives the `subscription_created` webhook. The HMAC
   check passes (because you set `LEMONSQUEEZY_WEBHOOK_SECRET`).
4. `cloudMachines.create` fires, which schedules
   `cloudMachines.provision`, which:
   - Creates a Hetzner `cx42` (or `gex44` for GPU) in `fsn1`.
   - Adds an A record `<shortId>.cloud.yaver.io` → server IP.
   - Runs the cloud-init (yaver CLI + Node + Go + Rust + Docker +
     Ollama on GPU).
   - Health-checks the box 5 min later.
5. In the web dashboard's **Infra** tab, the machine appears
   (status transitions `provisioning` → `active`).

**Verify:**
```bash
ssh root@<new-hetzner-ip> "yaver --version"
# should print the current version; cloud-init installed it.
```

---

## 7. Custom domain on top of a newly-provisioned machine

Typical developer story: they just bought `myapp.com` at Porkbun
and want to point it at their Yaver Cloud machine.

1. Web dashboard → **Domains** tab.
2. **Add a domain** form:
   - Domain: `myapp.com`
   - Target: Cloud Machine
   - Machine: pick the one provisioned in §6
   - DNS at: Manual (or Cloudflare if they've moved nameservers)
3. Click **Generate DNS instructions**. The table shows:
   - `TXT _yaver-verify.myapp.com = yaver-verify-<token>`
   - `A myapp.com = <serverIp>` (or CNAME to `<shortId>.cloud.yaver.io`)
4. User pastes both at Porkbun's DNS UI.
5. Web dashboard → row for `myapp.com` → **Verify**. The Convex
   `userDomains.verify` action queries DoH and flips the row to
   `verified` when both records resolve. Retry every 1-2 min while
   DNS propagates.

### 7.1 TLS for the custom domain — fully automatic

Every provisioned cloud machine ships with a systemd timer
(`yaver-tls.timer`, 5-min cadence) that runs
`/usr/local/bin/yaver-tls-reconciler`. On each tick it:

1. `GET /machine/pending-tls?machineId=...` with its
   long-lived `X-Machine-Token` (provisioned into
   `/etc/yaver/machine.json`; only the hash is stored in Convex as
   `cloudMachines.machineTokenHash`).
2. For each `verified` `userDomains` row routed at this machine:
   - Writes an HTTP-only nginx server block in
     `/etc/nginx/sites-available/<domain>`.
   - `nginx -t && systemctl reload nginx` so port 80 accepts the
     domain (needed for the ACME HTTP-01 challenge).
   - Runs `certbot --nginx -d <domain> --non-interactive …`. Certbot
     upgrades the server block to HTTPS and installs its own renew
     timer.
3. POSTs `/machine/tls-issued` (on success) or `/machine/tls-error`
   (on failure). The row flips to `active` or `error` respectively,
   so the web dashboard's **Domains** tab reflects reality without
   anyone SSHing.

**Verify after first custom domain:** on the machine,
`systemctl status yaver-tls.timer` shows `active (waiting)`;
`journalctl -u yaver-tls.service` shows the reconciler's
`[yaver-tls] issuing cert for myapp.com` line; `curl -I
https://myapp.com/health` returns 200 with a valid Let's Encrypt
cert.

---

## 8. Summary — env vars in one place

Paste-ready (replace the `<...>` values):

```bash
cd backend

# LemonSqueezy
npx convex env set LEMONSQUEEZY_API_KEY            "<lsk_test_...>"
npx convex env set LEMONSQUEEZY_STORE_ID           "<store-id>"
npx convex env set LEMONSQUEEZY_YAVER_CLOUD_VARIANT_ID "<variant-id>"
npx convex env set LEMONSQUEEZY_WEBHOOK_SECRET     "<whsec_...>"
npx convex env set LEMONSQUEEZY_SANDBOX            "true"
npx convex env set LEMONSQUEEZY_CHECKOUT_REDIRECT_URL "https://yaver.io/dashboard?welcome=cloud"
npx convex env set LEMONSQUEEZY_YAVER_CLOUD_RECEIPT_BUTTON_TEXT "Open Yaver"
npx convex env set NEXT_PUBLIC_BASE_URL            "https://yaver.io"

# Hetzner
npx convex env set HCLOUD_TOKEN                    "<hetzner-rw-token>"

# Cloudflare (for yaver.io zone only)
npx convex env set CF_API_TOKEN                    "<cf-edit-dns-token>"
npx convex env set CF_ZONE_ID                      "<yaver-io-zone-id>"
```

Verify all set: `npx convex env list | sort`.

---

## 9. Rotation / break-glass

- **LS webhook secret rotation:** generate a new secret in LS,
  `npx convex env set LEMONSQUEEZY_WEBHOOK_SECRET "new"`, then
  paste the new secret into the LS webhook UI. No downtime if
  done in that order.
- **HCLOUD_TOKEN rotation:** same pattern — new token in Hetzner,
  set in Convex. Kill the old token after confirming a successful
  webhook-triggered provision.
- **If webhook signatures start failing:** temporarily unset
  `LEMONSQUEEZY_WEBHOOK_SECRET` (the agent logs a warning and
  accepts requests unverified) while you diagnose. Re-set it
  immediately afterward.

---

## 10. What the feature does NOT do yet (known follow-ups)

- Namecheap / Porkbun / Route53 API adapters — the abstraction in
  `desktop/agent/dns_provider.go` is in place, but only Cloudflare
  and manual are wired today.
- Mobile-originated checkout (intentionally off — Apple commission
  risk). The mobile app surfaces a "finish setup on yaver.io"
  message instead.
- Per-user `CLOUD_OWNER_TOKEN` isolation. The managed tenant
  today uses a single shared secret; moving to per-subscriber
  tokens is a separate migration.
- Wildcard TLS (`*.myapp.com`). Current reconciler issues
  single-name certs via HTTP-01. Wildcards need DNS-01, which
  means a scoped API token for the user's DNS provider — a
  separate UX work item on the **Domains** tab.

These are captured for a future session — none block the first
paying-customer flow.
