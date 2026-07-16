# Remote closed-loop web smoke

Drives the **deployed** yaver.io dashboard with a real browser from a remote
box, and asserts what a `curl` structurally cannot.

## Why a browser, and why remote

`curl https://yaver.io/dashboard` returns 200 and tells you almost nothing: the
dashboard is a client route, so its JS chunk is **lazy-loaded** and never appears
in the SSR HTML. Only a JS-executing browser resolves which chunk to fetch. That
is the gap this catches — a deploy that is green in CI but serving a stale build.

It runs on a remote box because that is also the honest vantage: it exercises
the public URL over the real network, not a dev server on localhost.

## The secret constraint (read before extending this)

This test is **unauthenticated on purpose**. CLAUDE.md: *"No secrets ever live
there"* for the disposable Hetzner box — so it must NEVER carry a real session
token. Do not "improve" it by injecting `yaver_auth_token` into localStorage.

That bound shapes the assertions: the version chip only renders inside the
authed dashboard, so this greps the **loaded JS chunks** for the build's version
instead of reading the DOM. Anything needing auth belongs in a local run, or
behind a scoped throwaway token — never here.

## What it asserts

- the app boots (non-empty body, real title)
- it reaches the auth gate rather than crashing or rendering blank
- the deployed build carries the expected version
- **no stale version is baked in** — the drift bug this was written for:
  `web/package.json` sat at 1.1.154 while `versions.json` said 1.1.158, so the
  sidebar advertised a build four releases old and a current deploy looked stale

## Run

```bash
# one-time, on the remote box (chromium + chromedriver already present there)
ssh root@<box> 'python3 -m venv /tmp/seltest && /tmp/seltest/bin/pip install selenium'

scp e2e/remote/web_smoke.py root@<box>:/tmp/
ssh root@<box> '/tmp/seltest/bin/python /tmp/web_smoke.py 1.1.159'
```

Exits non-zero on failure. Pass the expected version as argv[1]; it defaults to
the version current when this was written, so always pass it explicitly in CI.

A screenshot lands at `/tmp/yaver-dashboard.png` on the box for eyeballing.
