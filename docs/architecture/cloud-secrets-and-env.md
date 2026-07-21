# Cloud Secrets & Environment Contract

Date: 2026-07-21
Scope: every credential and setting the Cloud Workspace / Relay Pro stack reads,
where it must live, and why.

**The repo is public. A hacker reads every tracked file.** So the test before
any key, cert or credential is committed is the one from `CLAUDE.md`:

> *If an attacker reads these exact bytes from the public repo, can they get in
> or forge?* **Yes → secret store. No (it only lets them verify) → code is fine.**

---

## 1. Where secrets are allowed to live

| Store | Use for | Reachable by |
|---|---|---|
| **Convex env** (`npx convex env set … --prod`) | everything the backend needs at runtime: provider credentials, relay password, Cloudflare token | Convex functions only |
| **GitHub Actions secrets** | anything CI needs to build/sign/publish | CI jobs only |
| **Local `~/.yaver` 0600 / `yaver vault`** | machine-local unlock secrets (keychain, sudo) | that machine only |

**Never:** a tracked file, a comment, a commit message, a log line, an error
string, a Convex *table*, or a client-visible API response.

> ⚠️ **Convex env ≠ a Convex table.** Env is a secret store. A table is *data* —
> anything that can read the DB can read it. Provider credentials go in env,
> never in a row.

---

## 2. Compute provider credentials

### Hetzner — **WIRED (production provider)**

| Variable | Private? | Notes |
|---|---|---|
| `HCLOUD_TOKEN` | 🔴 **PRIVATE** | Full read/write on the project. Leaking it lets an attacker create, delete and snapshot every box. |

```bash
cd backend && npx convex env set HCLOUD_TOKEN '<token>' --prod
```

Scope it to the **Yaver project only**. A token that can see other projects
violates the resource-boundary rule and widens every mistake.

### AWS

| Variable | Private? | Notes |
|---|---|---|
| `AWS_ACCESS_KEY_ID` | 🟡 identifier | Useless alone, but pairs with the secret. |
| `AWS_SECRET_ACCESS_KEY` | 🔴 **PRIVATE** | Signs every request. |
| `AWS_SESSION_TOKEN` | 🔴 **PRIVATE** | Optional, for temporary credentials. |
| `AWS_REGION`, `AWS_SUBNET_ID`, `AWS_SECURITY_GROUP_ID`, `AWS_KEY_NAME`, `AWS_DEFAULT_AMI_ID` | 🟢 config | Resource ids, not secrets. |

Use an IAM user/role scoped to EC2 only. **Prefer short-lived STS credentials**
once available — a long-lived access key in env is the weakest link here.

### GCP — credentials were the blocker, now fixed

| Variable | Private? | Notes |
|---|---|---|
| `GCP_SERVICE_ACCOUNT_JSON` | 🔴 **PRIVATE** | The whole SA key JSON (raw or base64). Contains `private_key` — it **signs** tokens, so it is the crown jewel. |
| `GCP_PROJECT_ID` | 🟢 config | Falls back to `project_id` inside the SA key. |
| `GCP_ZONE` | 🟢 config | |
| `GCP_ACCESS_TOKEN` | 🔴 private, **manual probing only** | Expires in ~1h and **cannot refresh**. Never production. |

```bash
npx convex env set GCP_SERVICE_ACCOUNT_JSON "$(cat sa.json | base64)" --prod
```

Grant the SA `roles/compute.instanceAdmin.v1` and nothing wider. `rm` the local
key file afterwards — a `sa.json` sitting in a repo directory is one careless
`git add -A` away from being public. (That is also why `CLAUDE.md` forbids
`git add -A` outright.)

### Azure — same story

| Variable | Private? | Notes |
|---|---|---|
| `AZURE_CLIENT_SECRET` | 🔴 **PRIVATE** | App-registration secret. |
| `AZURE_TENANT_ID`, `AZURE_CLIENT_ID` | 🟡 identifiers | Not secret alone; treat as config. |
| `AZURE_SUBSCRIPTION_ID`, `AZURE_RESOURCE_GROUP`, `AZURE_LOCATION`, `AZURE_NETWORK_INTERFACE_ID`, `AZURE_NETWORK_SECURITY_GROUP` | 🟢 config | |
| `AZURE_SSH_PUBLIC_KEY` | 🟢 **public key** | Safe in code by definition — it only lets someone *verify*. |
| `AZURE_BEARER_TOKEN` | 🔴 private, **manual probing only** | Expires, cannot refresh. |

Scope the app registration to the single resource group.

---

## 3. Non-provider secrets this stack touches

| Variable | Private? | Notes |
|---|---|---|
| `CF_API_TOKEN` | 🔴 **PRIVATE** | Cloudflare DNS. Scope to the one zone, DNS-edit only. |
| `CF_ZONE_ID` | 🟢 config | |
| `CRON_TRIGGER_SECRET` | 🔴 **PRIVATE** | Bearer for `/crons/run`. Leaking it lets anyone trigger metering/sweeps. |
| `GATEWAY_SHARED_SECRET` | 🔴 **PRIVATE** | Gateway ↔ Convex metering. `/gateway/meter` 500s without it — fail-closed, correct. |
| `LEMONSQUEEZY_*` | 🔴 **PRIVATE** | Billing API + webhook signing. |

> ⚠️ **Known gap, pre-existing:** `managedRelays.password` stores the relay
> password as plaintext **in a Convex table**, not in env. It is a shared secret
> the agent must fetch, so it cannot simply move — but it means DB read access
> implies relay access. Per the transport invariants the relay is *not* an
> authorization boundary (device keys are), so this is bounded rather than
> critical. It should still be hashed-at-rest or scoped before GA.

---

## 4. Behaviour flags (not secrets, but they gate real money)

| Variable | Default | Effect |
|---|---|---|
| `YAVER_CLOUD_PUBLIC` | unset ⇒ owner-only | Opens managed cloud beyond the owner allowlist. |
| `YAVER_CLOUD_METER_LIVE` | false ⇒ **dry-run** | Flips the wallet meter to real charges. |
| `YAVER_MANAGED_METER_LIVE` | false ⇒ **dry-run** | Managed usage metering. |
| `YAVER_CLOUD_IDLE_DISABLE` | unset ⇒ auto-park ON | Emergency brake for idle auto-park. |
| `YAVER_MANAGED_MACHINE_LIMIT` | 1 | Machines per user. |
| `YAVER_FORCE_COMPUTE_PROVIDER` | unset | **Operator only.** Forces a provider, bypassing eligibility. Loud + auditable. Never set in prod. |
| `YAVER_EGRESS_IP_DISABLE` | unset ⇒ enabled | Kill switch for stable egress reservation. |
| `YAVER_EGRESS_IP_RELEASE_DAYS` | 30 | Park age before a reserved IP is released. |
| `YAVER_EGRESS_IP_SWEEP_LIVE` | false ⇒ dry-run | Makes the release sweep actually release. |
| `YAVER_CLOUD_{STANDARD,HEAVY,BUILD,CPU,GPU}_TYPE` | see `hetznerServerType` | SKU override without a redeploy — useful when a type sells out. |

**Every money flag defaults to the safe side.** Keep it that way: a flag that
defaults to spending is a flag that will one day spend by accident.

---

## 5. Rules the code already enforces — do not regress them

1. **Credentials are read from `process.env` only.** No provider secret is ever
   passed from a client, stored in a table, or written to a file.
2. **No credential is ever logged.** Audited 2026-07-21: zero
   `console.*` calls interpolate a token, and zero error strings echo one. Error
   messages name the missing **variable**, never its value.
3. **Fail closed.** Missing credentials produce an explicit error and abort;
   nothing falls back to a default or a different account.
4. **Errors carry the remedy.** `"set GCP_SERVICE_ACCOUNT_JSON in Convex env"`
   beats `"missing config"` — a vague credential error costs whole sessions
   (see `errSecInternalComponent`, 2026-07-19).
5. **Public key material may live in code.** `AZURE_SSH_PUBLIC_KEY`, host-key
   fingerprints to pin, relay public identities — shipping a *verifier* is not a
   leak. That is the entire point of public-key crypto.

---

## 6. If a secret leaks

1. **Rotate first, investigate second.** Provider console → revoke → mint new →
   `npx convex env set … --prod`.
2. If it ever reached a **commit**: rotating is not enough —
   `git filter-repo --replace-text` before pushing, because the object stays in
   history and on every fork.
3. Check for damage the leak enables: for `HCLOUD_TOKEN`, list servers, volumes,
   snapshots and primary IPs (runbook §2.1) and look for anything you did not
   create.
4. Write the incident into the code, not just the commit message — a `doctor`
   probe or a preflight that would have caught it in ten seconds.

---

## 7. Setup checklist

```bash
cd backend

# Hetzner (production provider — the only one needed to run today)
npx convex env set HCLOUD_TOKEN '<token>' --prod
npx convex env set CF_API_TOKEN '<token>' --prod
npx convex env set CF_ZONE_ID   '<zone>'  --prod

# GCP (refreshable — never GCP_ACCESS_TOKEN in prod)
npx convex env set GCP_SERVICE_ACCOUNT_JSON "$(base64 < sa.json)" --prod
npx convex env set GCP_PROJECT_ID '<project>' --prod
npx convex env set GCP_ZONE       'europe-west4-a' --prod
rm sa.json

# Azure (refreshable — never AZURE_BEARER_TOKEN in prod)
npx convex env set AZURE_TENANT_ID     '<tenant>' --prod
npx convex env set AZURE_CLIENT_ID     '<app>'    --prod
npx convex env set AZURE_CLIENT_SECRET '<secret>' --prod
npx convex env set AZURE_SUBSCRIPTION_ID '<sub>'  --prod
npx convex env set AZURE_RESOURCE_GROUP  '<rg>'   --prod

# AWS
npx convex env set AWS_ACCESS_KEY_ID     '<id>'     --prod
npx convex env set AWS_SECRET_ACCESS_KEY '<secret>' --prod
npx convex env set AWS_REGION            'eu-north-1' --prod

# Verify names only — NEVER paste values into a shared terminal or a doc
npx convex env list --prod | cut -d= -f1
```

Setting a provider's credentials does **not** make it production-eligible.
`selectComputeProvider` still refuses any provider that cannot prove the
capability floor — see the runbook's go/no-go list.
