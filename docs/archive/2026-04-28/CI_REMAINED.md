# CI Remained

Paused mid-setup. Pick up here when you want CI's `--cloudflare` test to flip from skip → pass and when you want to fill the remaining secrets for the release workflows.

## Session summary (what's done)

- **Git history scrubbed** (`git filter-repo`) — removed a leaked keystore password and an old Hetzner relay IP from every commit on `main`. Force-pushed. Backup branch: `backup-before-scrub`; tarball at `../yaver-git-backup-1776516110.tgz`.
- **22 GitHub secrets set** via `gh secret set`. See `CLAUDE.md § Secrets management` for the full list and the pattern (stdin input, never `--body` for sensitive values).
- **Anthropic API usage removed from CI** (`776a628f`) — deleted `autodev-e2e.yml`, pruned `claude:sonnet` matrix entry from `runner-integrations.yml`. No workflow reads `ANTHROPIC_API_KEY` anymore.
- **Cloudflare tunnel `yaver` exists** but has no connector (status: DOWN). Mac's cloudflared was uninstalled when we started the Hetzner migration.
- **Yaver agent running on `yaver-relay-free`** (authed as `kivanc.cakmak@icloud.com`, device `3f6e3ca6...`, listening on 18080).
- **`CF_AUTOMATION_TOKEN`** created as a Cloudflare API token and stored as a GitHub secret. Its resources (Account + Zone) were misconfigured in the dashboard — current value in local `.env.test` returns 401 "Invalid access token". Needs the form redone with all 4 permissions + both resource scopes attached.

## Remaining — required to make `--cloudflare` test go from skip → pass

1. **Fix `CF_AUTOMATION_TOKEN`** in Cloudflare dashboard. Delete the existing `yaver-ci-automation` token and create a fresh one with:
   - Permissions (4 rows):
     - Account → Cloudflare Tunnel → Edit
     - Account → Access: Apps and Policies → Edit
     - Account → Access: Service Tokens → Read
     - Zone → DNS → Edit
   - Account Resources → Include → `Kivanccakmak@gmail.com's Account`
   - Zone Resources → Include → specific zone → `yaver.io`
   - No IP allowlist (or this Mac's IP)
   - Paste into both `gh secret set CF_AUTOMATION_TOKEN` and local `.env.test`. Verify: `accounts: 1 success: True` and `zones: 1 success: True`.

2. **Install `cloudflared` on `yaver-relay-free`** with the tunnel connector token.
   - Dashboard → Networks → Tunnels → `yaver` → Add a connector → copy the `eyJh…` token (not the whole install command).
   - From this Mac, pipe it over SSH without echoing:
     ```bash
     set -a; source .env.test; set +a
     read -rs -p "Paste CF tunnel token: " T; echo
     # Public IP/hostname of yaver-relay-free lives in your local secrets; set it first:
     #   export PUBLIC_RELAY_HOST=<host-or-ip>   # or hardcode in ~/.ssh/config as yaver-relay
     ssh -i ~/.ssh/id_ed25519 "${REMOTE_SERVER_USER}@${PUBLIC_RELAY_HOST}" \
         TUNNEL_TOKEN="$T" bash -s <<'REMOTE'
     set -e
     curl -fsSL https://pkg.cloudflare.com/install.sh | bash >/dev/null
     apt-get install -y cloudflared >/dev/null
     cloudflared service install "$TUNNEL_TOKEN"
     systemctl enable --now cloudflared
     unset TUNNEL_TOKEN
     sleep 3
     echo "cloudflared: $(systemctl is-active cloudflared)"
     REMOTE
     unset T
     ```
   - Dashboard → Tunnels → `yaver` should flip to HEALTHY, one connector from the Hetzner box's public IP.

3. **Configure tunnel ingress** (yaver-cix.yaver.io → http://localhost:18080). Either via dashboard (Published application routes → add) or via API:
   ```
   PUT https://api.cloudflare.com/client/v4/accounts/{acct}/cfd_tunnel/{tunnel_id}/configurations
   { "config": { "ingress": [
       {"hostname": "yaver-cix.yaver.io", "service": "http://localhost:18080"},
       {"service": "http_status:404"}
   ] } }
   ```

4. **DNS**: CNAME `yaver-cix.yaver.io` → `{tunnel_id}.cfargotunnel.com` (proxied). Dashboard creates this automatically when the ingress is set, otherwise: `POST https://api.cloudflare.com/client/v4/zones/$CF_ZONE_ID/dns_records`.

5. **Access application** protecting `yaver-cix.yaver.io` — otherwise the hostname is open to the internet.
   - Dashboard → Access controls → Applications → Add application → Self-hosted → hostname `yaver-cix.yaver.io` → Policies → Action: **Service Auth** → Include: the existing service token (named in `CF_ACCESS_CLIENT_ID`).
   - Or via API: `POST /accounts/{acct}/access/apps` + `POST /accounts/{acct}/access/apps/{app_id}/policies`.

6. **Verify end-to-end** from the Mac:
   ```bash
   # without service-token headers → expect 302 to *.cloudflareaccess.com
   curl -I https://yaver-cix.yaver.io/health

   # with service-token headers → expect 200 from yaver
   curl -i https://yaver-cix.yaver.io/health \
     -H "CF-Access-Client-Id: <id>" \
     -H "CF-Access-Client-Secret: <secret>"
   ```
   Once both match, push a noop commit to `main` and watch the `test-suite` CI job — the Cloudflare section should show a PASS instead of a skip.

## Still-missing GitHub secrets (unrelated to Cloudflare)

These don't block today but block specific release paths:

| Secret | Blocks | Source |
|--------|--------|--------|
| `CONVEX_DEPLOY_KEY` | `release-mobile.yml`, `release-web.yml`, `release-relay.yml` | Convex dashboard → Settings → Deploy Keys → Generate Production |
| `APPLE_CERTIFICATE_P12` + `APPLE_CERTIFICATE_PASSWORD` | `release-mobile.yml` iOS signing | Keychain → iPhone/Apple Distribution cert → Export → base64 the `.p12`, remember the export password |
| `YAVER_CI_SSH_HOST_PRIMARY` + `_SECONDARY` + `_USER` + `_PORT` + `_PRIVATE_KEY` + `_KNOWN_HOSTS` | `remote-infra.yml` | `.env.test` already has the host/user/key; just `gh secret set` them |
| `YAVER_AGENT_URL` | `yaver-trigger-example.yml` | Only if you actually use that example workflow |

## Unrelated CI red — not blocked by CF setup

The `test-suite` job has been failing on every recent push with Go build errors in jobs that compile `github.com/yaver-io/agent`:

- `test-sdk-integration` — Go agent tests build failure
- `test-sdk-feedback` — same
- `test-phone-local-first` — same
- `build-ios`

These are caused by uncommitted work in the tree (`desktop/agent/mcp_remote_proxy.go` + `_test.go`, `cli/src/preuninstall.js`, `REMOTE_WORKER.md`). Either finish those changes and commit, or revert the work-in-progress to restore green. Independent from the Cloudflare tunnel setup.

## Decisions already made (keep these)

- Tunnel endpoint lives on Hetzner `yaver-relay-free`, not on the Mac. Mac sleeping would flap CI otherwise.
- API-only automation for future re-setups: the 4-permission token + a single script is the target — avoid dashboard clicks when you come back.
- `APPLE_CERTIFICATE_*` and `CONVEX_DEPLOY_KEY` handled manually from their respective dashboards; everything else already automated via `gh secret set` from `.env.test`.
