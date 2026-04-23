# HETZNER_KICK_OFF

Manual kickoff for a headless Hetzner Yaver box owned by `kivanc.cakmak@icloud.com`, with:

- Yaver owner auth
- Apple-linked Yaver login
- secure Codex key storage
- GitHub and GitLab integration
- mobile-first usage after bootstrap
- shared-box safety for guests and friends

This assumes:

- the server has no desktop UI
- you want to understand every step manually
- you want to use your phone as much as possible
- the box stays installed as a long-lived Yaver machine

## 1. Provision The Box

From your desktop:

```bash
./scripts/deploy-yaver-agent-hetzner.sh \
  --host <hetzner-ip> \
  --user root \
  --keyfile ~/.ssh/hetzner_ci_ed25519 \
  --multi-user \
  --max-users 12 \
  --containerize-guests
```

Then SSH in:

```bash
ssh root@<hetzner-ip>
systemctl status yaver-agent --no-pager
```

You want the agent installed and running. Do not uninstall it between experiments.

## 2. Authenticate The Box As Your Owner Account

On the Hetzner box:

```bash
sudo -u yaver -H yaver auth --headless
```

This prints:

- a URL
- a short code
- usually a QR in the terminal

Use either:

- your iPhone camera
- Yaver mobile app
- your desktop browser

Headless auth page:

- `https://yaver.io/auth/device`

Reference:

- [README.md](/Users/kivanccakmak/Workspace/yaver.io/README.md:299)

If you already have another machine signed into Yaver, you can use pairing instead:

```bash
sudo -u yaver -H yaver auth pair
```

Then either:

- scan the QR from Yaver mobile
- or run this from your already signed-in desktop:

```bash
yaver auth send <PAIR-CODE> <target-url-from-the-qr>
```

After auth:

```bash
sudo systemctl restart yaver-agent
sudo systemctl status yaver-agent --no-pager
```

## 3. Link Apple To The Same Yaver Account

This is not Apple OAuth on the Hetzner machine itself. The Hetzner machine only needs a Yaver auth token. Apple is just one of the sign-in methods linked to your Yaver account.

From your desktop terminal:

```bash
yaver account link apple
```

That prints:

- a browser URL
- a QR code

Open the URL on your Mac, or scan the QR with your phone.

After this, your Yaver account can be signed into using Apple too.

Reference:

- [desktop/agent/account_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/account_cmd.go:13)

## 4. Store Your Codex Key Securely

On the Hetzner box:

```bash
sudo -u yaver -H yaver vault add OPENAI_API_KEY --category api-key
```

This is the correct place for your owner-only Codex key.

Do not put it in:

- shell profiles
- committed files
- workflow YAML
- systemd unit text in the repo

## 5. Make GitHub And GitLab Seamless

You now have one command per provider.

On the Hetzner box:

```bash
sudo -u yaver -H yaver repo auth setup github --token <github-pat>
sudo -u yaver -H yaver repo auth setup gitlab --token <gitlab-pat>
```

What this does:

- stores clone/pull credentials in `~/.yaver/git-credentials.json`
- stores CI/deploy credentials in the vault as `github-token` and `gitlab-token`

If you want the box to also generate and register its own SSH key:

```bash
sudo -u yaver -H yaver repo auth setup github --token <github-pat> --ssh
sudo -u yaver -H yaver repo auth setup gitlab --token <gitlab-pat> --ssh
```

Audit the final state:

```bash
sudo -u yaver -H yaver repo auth status
```

Remove a provider later if needed:

```bash
sudo -u yaver -H yaver repo auth remove github
sudo -u yaver -H yaver repo auth remove gitlab
```

## 6. PAT Scopes To Create

Keep these minimal.

### GitHub PAT

For clone, pull, repo browsing, workflow triggering, and release upload on your own repos:

- classic PAT:
  - `repo`
  - `workflow`

Or fine-grained PAT:

- repository contents: read/write
- metadata: read
- actions: read/write

If you only want clone/pull and not workflow dispatch, you can omit workflow-related permission.

### GitLab PAT

Usually enough:

- `api`
- `read_repository`
- `write_repository`

If you only want git operations and not pipeline actions, you can narrow this later.

## 7. Mobile-First Usage After Bootstrap

After the box is authenticated and paired:

1. Open Yaver mobile.
2. Pair the device if needed via the QR from `yaver auth pair`.
3. Go to the connected machine.
4. Use Repo Sync to:
   - clone repos
   - pull repos
   - delete remote repos
   - add/remove repo tokens if needed
5. Start dev flows from the phone.
6. Use vibing / tasks / Hermes reload / backend export against that same machine.

Your intended model is:

- the Hetzner box holds the source trees
- the phone drives the workflows
- the owner key stays only on the box

## 8. Shared Machine Model

For friends and teammates:

- they can be added as guests or multi-user users
- the machine still has one owner: `kivanc.cakmak@icloud.com`
- guest sessions should not inherit your `OPENAI_API_KEY` by default

Recommended:

- your own work: owner mode
- collaborators: guest or multi-user mode
- containerized guest execution: enabled

## 9. Recovery

If auth drifts:

```bash
ssh root@<hetzner-ip>
sudo systemctl status yaver-agent --no-pager
sudo journalctl -u yaver-agent -n 100 --no-pager
sudo -u yaver -H yaver auth --headless
sudo systemctl restart yaver-agent
```

If only the Codex key needs rotation:

```bash
sudo -u yaver -H yaver vault add OPENAI_API_KEY --category api-key
sudo systemctl restart yaver-agent
```

If only GitHub or GitLab token needs rotation:

```bash
sudo -u yaver -H yaver repo auth setup github --token <new-github-pat>
sudo -u yaver -H yaver repo auth setup gitlab --token <new-gitlab-pat>
```

## 10. Minimal First Run Checklist

Run these in order:

```bash
ssh root@<hetzner-ip>
sudo -u yaver -H yaver auth --headless
sudo -u yaver -H yaver vault add OPENAI_API_KEY --category api-key
sudo -u yaver -H yaver repo auth setup github --token <github-pat>
sudo -u yaver -H yaver repo auth status
sudo -u yaver -H yaver auth pair
```

Then on your desktop:

```bash
yaver account link apple
```

Then on your phone:

- scan the QR
- pair the box
- open Repo Sync
- clone your repo
- start using the machine
