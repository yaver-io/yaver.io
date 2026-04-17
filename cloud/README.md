# Yaver Cloud — Hetzner tenant for phone-backend projects

Runs a single `yaver serve` instance behind Caddy TLS so the mobile app (or
`yaver phone push` from the CLI) can deploy a phone-backend project to
Yaver's managed infrastructure with one call.

The same binary the developer runs on their own Mac/Pi/Linux/VPS runs here
— the only difference is Caddy in front and a persistent Docker volume
underneath. That's the whole managed-cloud tier for MVP.

This is the managed version of the same phone-backend continuum:

`phone sandbox -> your dev machine / your own host -> Yaver Cloud`

If you do not want the managed tier, the same project can also land on your
own box by running `yaver serve` directly or by using the exported
`Dockerfile` + `docker-compose.yml` scaffold from `yaver phone export --containerize`.

## One-time setup on a fresh Hetzner VPS

```bash
# From your laptop (DNS for cloud.yaver.io must already point at the box):
scp cloud/deploy.sh root@HETZNER_IP:/tmp/
ssh root@HETZNER_IP bash /tmp/deploy.sh
```

Or pull-based:

```bash
ssh root@HETZNER_IP "curl -fsSL https://raw.githubusercontent.com/kivanccakmak/yaver.io/main/cloud/deploy.sh | bash"
```

The script:
1. Installs Docker if missing.
2. Clones the repo to `/opt/yaver-cloud`.
3. Copies `cloud/.env.example` → `/opt/yaver-cloud/cloud/.env` and generates a
   `CLOUD_OWNER_TOKEN` if one isn't already set.
4. `docker compose up -d --build`.

Grab the generated token from `/opt/yaver-cloud/cloud/.env` and store it
somewhere the mobile app / CLI can read it. Per-user tokens replace this
flat token post-MVP (tracked against the `sdkTokens` table).

## Push a phone project from a laptop

```bash
yaver phone push --to https://cloud.yaver.io my-todos
```

`/phone/projects/receive` materialises the bundle into
`/home/yaver/.yaver/phone-projects/<slug>/` inside the container (backed by
the `yaver-data` volume, so redeploys don't wipe tenant data).

If you want the target project to arrive with Docker helpers included in the
bundle as well:

```bash
yaver phone push --to https://cloud.yaver.io --include-data --containerize my-todos
```

## Push from the mobile app

`mobile/src/lib/phoneProjects.ts` exposes `pushPhoneProject(slug, target)`.
For the cloud target:

```ts
await pushPhoneProject("my-todos", { kind: "yaver-cloud" });
```

By default the mobile client hits `https://cloud.yaver.io`. Override via
`{ kind: "yaver-cloud", cloudBaseUrl: "https://other.host" }` when testing
against a staging box.

## Health check

```bash
curl https://cloud.yaver.io/health
# {"status":"ok",...}
```

## Operational notes

- Request body cap is 128 MB (`request_body max_size` in Caddyfile,
  `maxBundle` in `desktop/agent/phone_backend_http.go`). Keep them in sync.
- Data volume: `yaver-data` → `/home/yaver/.yaver`. Back up before every
  `docker compose up --build` to be safe; `tini` ensures a clean SIGTERM on
  container stop so SQLite writes flush.
- Log volume: `50 MB × 5 files` per container (`json-file` driver). Rotate
  externally if you keep the box long-term.
- Scale-out: not in MVP. One Hetzner box per ~50 projects. When that's too
  few, split tenants by `CLOUD_DOMAIN` into `eu.cloud.yaver.io`,
  `us.cloud.yaver.io`, etc., each running its own stack.

## Why Caddy and not Nginx + Certbot

Caddy handles Let's Encrypt issue/renew without a cron or any writable
certs dir in the repo. Its `reverse_proxy` default config is sane for our
case (tails the agent on 18080, preserves X-Forwarded-*, no manual timeouts
tuning needed for the upload path beyond the body cap).

## What's NOT here (yet)

- Per-user isolation beyond the agent's existing owner-auth. All projects
  on this box authenticate as the single `CLOUD_OWNER_TOKEN` in MVP.
- Billing hooks. Add before opening the cloud tier to the public.
- Project-level custom domains. Post-MVP; the slug-keyed URL
  (`cloud.yaver.io/phone/projects/browse?slug=...`) is enough for now.
