# Antigravity + Continue + Windows Ollama runbook

This runbook captures the setup that was validated on April 23, 2026 for:

- a MacBook running Antigravity
- a Windows machine running Ollama and Qwen models
- Continue inside Antigravity using the Windows machine as the model backend
- Tailscale as the primary remote path
- LAN as an optional secondary path

## What works

The supported working shape is:

- Antigravity as the editor shell on macOS
- Continue extension inside Antigravity for model access
- Windows Ollama as the remote backend
- Tailscale hostname as the default Ollama endpoint

Important boundary:

- Antigravity's native model picker does not show custom Ollama models
- Qwen appears in Continue's model picker, not Antigravity's built-in Gemini/Claude/GPT-OSS picker

## Verified endpoints

Windows host:

- LAN IP: `192.168.1.50`
- Tailscale DNS: `your-box.tailnet.ts.net`

Preferred Ollama endpoint from the Mac:

- `http://your-box.tailnet.ts.net:11434`

Optional LAN endpoint:

- `http://192.168.1.50:11434`

The Tailscale path was verified from the Mac with:

```bash
curl http://your-box.tailnet.ts.net:11434/api/tags
```

This returned the Windows-hosted Ollama models.

## Windows-side model inventory

Verified models:

- `qwen2.5-coder:14b`
- `qwen2.5-coder:7b`
- `qwen2.5-coder:1.5b`

Recommended default:

- `qwen2.5-coder:14b`

## Mac Continue config that worked

The Continue build in Antigravity required a top-level `version` field.
Without it, Continue failed with:

```text
Failed to parse config: version: Required
```

Working `~/.continue/config.yaml`:

```yaml
name: Windows Remote Ollama (Tailscale)
version: "1.0.0"
models:
  - name: Qwen 14B Windows Tailscale
    provider: ollama
    model: qwen2.5-coder:14b
    apiBase: http://your-box.tailnet.ts.net:11434
    roles:
      - chat
      - edit
      - apply
  - name: Qwen 7B Windows Tailscale
    provider: ollama
    model: qwen2.5-coder:7b
    apiBase: http://your-box.tailnet.ts.net:11434
    roles:
      - chat
      - edit
      - apply
  - name: Qwen 1.5B Windows Tailscale
    provider: ollama
    model: qwen2.5-coder:1.5b
    apiBase: http://your-box.tailnet.ts.net:11434
    roles:
      - chat
      - edit
      - apply
  - name: Qwen 14B Windows LAN
    provider: ollama
    model: qwen2.5-coder:14b
    apiBase: http://192.168.1.50:11434
    roles:
      - chat
      - edit
      - apply
  - name: Qwen 7B Windows LAN
    provider: ollama
    model: qwen2.5-coder:7b
    apiBase: http://192.168.1.50:11434
    roles:
      - chat
      - edit
      - apply
  - name: Qwen 1.5B Windows LAN
    provider: ollama
    model: qwen2.5-coder:1.5b
    apiBase: http://192.168.1.50:11434
    roles:
      - chat
      - edit
      - apply
context:
  - provider: code
  - provider: docs
```

## Continue config.ts note

On this machine, `~/.continue/config.ts` also existed and was active.
To make the setup robust, the same Windows endpoints were also injected there.

That matters because some Continue installs behave like repo-style TypeScript config setups instead of YAML-only setups.

## Why `127.0.0.1` in logs was misleading

The user saw log lines like:

```text
connect ECONNREFUSED 127.0.0.1:49249
```

That was not Continue trying to hit local Ollama.

Those localhost ports were Antigravity's internal local services.
The actual Continue-side blocker was the invalid YAML schema, not the remote Ollama target.

## Tailscale vs LAN

Use Tailscale as the default:

- works away from home
- avoids DHCP drift
- was the path confirmed to return model data from the Mac

Use LAN as an optional manual selection:

- lower hop count on the same network
- only useful if Windows Ollama is reachable on `192.168.1.50:11434`

At validation time:

- Tailscale path worked
- LAN path still timed out from the Mac

So the correct default was Tailscale first, LAN second.

## What the user should click

In Antigravity on the Mac:

1. open Continue
2. do not use the native Antigravity cloud model dropdown
3. open Continue's own model selector
4. select `Qwen 14B Windows Tailscale`

## Windows local case

When using Antigravity or Cursor on the Windows machine itself, Continue should point to local Ollama:

```yaml
name: Windows Local Ollama
version: "1.0.0"
models:
  - name: Qwen 14B Windows Local
    provider: ollama
    model: qwen2.5-coder:14b
    apiBase: http://127.0.0.1:11434
    roles:
      - chat
      - edit
      - apply
```

## OpenCode notes

Mac OpenCode was configured to prefer:

1. LAN
2. Tailscale
3. local tunnel

At validation time it selected the Tailscale endpoint successfully.

Windows OpenCode was configured to use local Ollama via:

- `http://127.0.0.1:11434/v1`

## Fast troubleshooting

If Continue shows no models:

1. check `~/.continue/config.yaml`
2. make sure it includes `version: "1.0.0"`
3. restart Antigravity
4. inspect the latest Antigravity renderer log for `Failed to parse config`

Useful log path on macOS:

```text
~/Library/Application Support/Antigravity/logs
```

If the Tailscale endpoint should work but does not:

```bash
curl http://your-box.tailnet.ts.net:11434/api/tags
```

If that fails, the problem is network reachability or Windows Ollama exposure, not Continue.
