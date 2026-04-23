# Hetzner Shared Owner Runbook

Use this setup when one always-on Hetzner box should serve three roles at once:

- your own long-lived Yaver machine
- the persistent remote test host for GitHub CI guest/integration workflows
- a shared box that other authenticated Yaver users can reach without becoming the owner

This runbook matches the current Go agent architecture:

- one primary owner/admin token
- optional paired devices for the same owner
- optional multi-user sessions for other authenticated users
- guest/host-share sessions with host keys blocked by default

## Rules

- Do not uninstall the agent from the persistent Hetzner test box during normal CI use.
- Keep `kivanc.cakmak@icloud.com` as the primary owner on the machine.
- Store the Codex/OpenAI key in the Yaver vault, not in committed files.
- Treat CI and newly created users as additional users or guests, not replacement owners.
- Do not grant host API keys to CI/guest sessions unless you explicitly want them spending against your account.

## 1. Deploy or retune the machine

Use the Hetzner deploy helper in shared mode:

```bash
./scripts/deploy-yaver-agent-hetzner.sh \
  --host <your-hetzner-ip-or-hostname> \
  --user root \
  --keyfile ~/.ssh/hetzner_ci_ed25519 \
  --multi-user \
  --max-users 12 \
  --containerize-guests
```

Optional hardening:

```bash
./scripts/deploy-yaver-agent-hetzner.sh \
  --host <your-hetzner-ip-or-hostname> \
  --user root \
  --keyfile ~/.ssh/hetzner_ci_ed25519 \
  --multi-user \
  --max-users 12 \
  --containerize-guests \
  --allow-ips 127.0.0.0/8,<your-home-cidr>,<your-tailnet-cidr>
```

Notes:

- `--multi-user` enables per-user isolated sessions on the box.
- `--containerize-guests` is the safer default for shared or CI-driven guest tasks.
- `--allow-ips` is optional; use it only if your access paths are stable enough not to lock yourself out.

## 2. Authenticate the box as the owner

SSH into the machine and authenticate as `kivanc.cakmak@icloud.com`.

Headless OAuth:

```bash
sudo -u yaver -H yaver auth --headless
```

Or pair from an already signed-in machine:

```bash
sudo -u yaver -H yaver auth pair
```

Verify the agent comes back up under the same owner after restart:

```bash
sudo systemctl restart yaver-agent
sudo systemctl status yaver-agent --no-pager
```

## 3. Add your Codex key securely

Store the key in the Yaver vault on the box:

```bash
sudo -u yaver -H yaver vault add OPENAI_API_KEY --category api-key
```

Why this is the right place:

- the agent already checks `OPENAI_API_KEY` via host secret sources
- vault-backed secrets are available to the owner
- guest tasks do not inherit host API keys by default

If you want to inspect runner readiness:

```bash
curl -fsS http://127.0.0.1:18080/agent/runners \
  -H "Authorization: Bearer <owner-token>"
```

## 4. Pair your own secondary devices

For your own laptop/phone/tablet, use pairing instead of creating more owners:

```bash
sudo -u yaver -H yaver auth pair
sudo -u yaver -H yaver auth pair list
```

This keeps one primary owner while allowing your other signed-in devices to act on the same machine.

## 5. Make GitHub and GitLab seamless on the box

Run this once per provider from SSH on the Hetzner machine:

```bash
sudo -u yaver -H yaver repo auth setup github --token <github-pat>
sudo -u yaver -H yaver repo auth setup gitlab --token <gitlab-pat>
```

What this does:

- saves the provider token into `~/.yaver/git-credentials.json` for clone/pull/private repo access
- saves the same token into the Yaver vault as `github-token` or `gitlab-token` for deploy/CI helpers

If you want the box to prefer SSH for repo access too:

```bash
sudo -u yaver -H yaver repo auth setup github --token <github-pat> --ssh
sudo -u yaver -H yaver repo auth setup gitlab --token <gitlab-pat> --ssh
```

Audit what is configured:

```bash
sudo -u yaver -H yaver repo auth status
```

Remove a provider cleanly:

```bash
sudo -u yaver -H yaver repo auth remove github
sudo -u yaver -H yaver repo auth remove gitlab
```

## 6. Let CI and new users use the same machine safely

There are two recommended patterns.

### Pattern A: CI or external users as guests

Use this when GitHub Actions or another user should borrow the machine for constrained tasks.

- keep the Hetzner box as the host side
- let the remote caller join via guest/host-share
- keep host API keys blocked for those sessions
- prefer containerized guest execution

This is the safest default for the current persistent test host.

### Pattern B: multi-user sessions

Use this when another real Yaver user should get their own isolated session on the box.

- they authenticate with their own token
- the agent creates a per-user workspace/home slice
- they are still not the machine owner

This is useful for trusted collaborators or your own alternate accounts, but still should not get your host API keys by default.

## 7. What not to do

- Do not run `./scripts/deploy-yaver-agent-hetzner.sh --uninstall` on the persistent test box unless you intentionally want to remove the machine.
- Do not put `OPENAI_API_KEY` into tracked files, systemd unit text in the repo, or GitHub workflow YAML.
- Do not treat GitHub CI as an equal owner of the box.
- Do not give guest sessions host keys unless that is a deliberate spend decision.

## 8. Suggested operating model for this repo

- Persistent Hetzner box stays installed and owner-authenticated as `kivanc.cakmak@icloud.com`.
- GitHub remote workflows continue using it as the canonical host.
- GitHub-runner-originated checks act as guests against that host.
- Personal Codex use happens as the owner through vault-backed `OPENAI_API_KEY`.
- New external users go through guest or multi-user paths, not owner replacement.

## 9. Quick recovery checklist

If the machine drifts or auth expires:

```bash
ssh root@<host>
sudo systemctl status yaver-agent --no-pager
sudo journalctl -u yaver-agent -n 100 --no-pager
sudo -u yaver -H yaver auth --headless
sudo systemctl restart yaver-agent
```

If you only need to rotate the Codex key:

```bash
sudo -u yaver -H yaver vault add OPENAI_API_KEY --category api-key
sudo systemctl restart yaver-agent
```
