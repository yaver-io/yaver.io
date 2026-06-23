# Store Tester & Build Management (TestFlight + Google Play)

First-class management of **beta testers, groups, builds and release rollout**
for App Store Connect (TestFlight) and Google Play, on behalf of **third-party
developers** who use Yaver — exposed through MCP, the web dashboard, and the
mobile app, and runnable from **any agent including a managed-cloud box**.

This is the lifecycle the upload scripts (`scripts/deploy-testflight.sh`,
`scripts/deploy-playstore.sh` + `scripts/upload-playstore.py`) can't reach:
those build + upload a binary; this manages *who can test it* and *whether a
build is delivered*.

Companion blog: `/blog/mobile-beta-testing-apple-google` (the human onboarding
guide — accounts, roles, how a tester downloads).

## Why it's multi-tenant by construction

Every verb takes a `project` (the customer's project slug). Credentials are
read from **that project's vault scope** — so dev B's managed-cloud box manages
dev B's app with dev B's keys, never Yaver's. The agent on the box holds the
keys; nothing store-credential-shaped ever goes to Convex (privacy contract).

```
phone / web / MCP  ──ops──▶  agent (local or managed cloud)
                                 │  resolves project-scoped creds from vault
                                 ▼
              App Store Connect API  /  Play Android Publisher API
```

## What's built

| Layer | File | Notes |
|---|---|---|
| Apple API client | `desktop/agent/appstoreconnect.go` | ES256 JWT (reuses `mintASCJWT`/`resolveAppleASCCreds` from the Store Studio); apps, beta groups, beta testers, builds, assign-build-to-group. Pure stdlib. |
| Google API client | `desktop/agent/playpublish_api.go` | SA→OAuth2 (reuses `resolveGoogleSA`/`getGoogleAccessToken`); edits, tracks, testers (Google Groups), release rollout. Pure stdlib. |
| MCP ops verbs | `desktop/agent/ops_store.go` | `store_*` verbs, auto-exposed via the `ops` grand-tool; owner-only. |
| Web UI | `web/components/dashboard/StoresView.tsx` | "Testers" tab → `callOps` on the connected agent. |
| Mobile UI | `mobile/app/store-testers.tsx` + `src/lib/storeTestersClient.ts` | reachable from **More → Store Testers**; LAN-first/relay transport. |
| Tests | `appstoreconnect_test.go`, `playpublish_api_test.go` | httptest servers (no mocks), per repo convention. |

## MCP verbs

All take `store: apple|google`, `project`, and `bundleId` (apple) /
`packageName` (google), `track` (google, default `internal`).

| Verb | Apple | Google |
|---|---|---|
| `store_credentials_status` | is the ASC key present? | is the SA JSON present? |
| `store_group_list` | TestFlight beta groups | track's Google Groups |
| `store_group_create` | create a beta group (+ public link) | n/a (Play uses external Groups) |
| `store_tester_list` | beta testers (+ state) | track's Google Groups (+ note) |
| `store_tester_invite` | create tester + add to group (Apple emails it) | bind a Google Group to the track |
| `store_tester_remove` | delete the beta tester | unbind a Google Group |
| `store_build_list` | TestFlight builds | track releases (version codes + status) |
| `store_release_promote` | assign latest build to a group | roll a draft out (`completed`) or stage (`inProgress` + `userFraction`) |

## The Apple / Google asymmetry (honest scope)

- **Apple** — the App Store Connect API fully manages **individual beta
  testers** and groups. `store_tester_invite` with an `email` creates the tester
  and Apple sends the invite.
- **Google** — the Play Developer API manages a track's **Google Groups** and
  **release rollout**, but **not** the per-email internal-tester list (that list
  is Play-Console-only). So `store_tester_invite` for google takes a
  `groupEmail` (a Google Group) and binds it to the track; adding individual
  people to that Group happens in Google Workspace, or per-email testers are
  added in the Console. The verbs say this in their responses rather than
  pretending.

## Credentials (vault keys, per project)

Reuses the Store Studio's keys (also the names in `~/.appstoreconnect/yaver.env`
and the CLAUDE.md vault commands):

- Apple: `APP_STORE_KEY_PATH` (the `.p8`), `APP_STORE_KEY_ID`,
  `APP_STORE_KEY_ISSUER`. (env fallback: `APP_STORE_KEY_*` / `APP_STORE_API_*`.)
- Google: `PLAY_STORE_KEY_FILE` (service-account JSON path). (env fallback:
  `PLAY_STORE_KEY_FILE`; default `keys/google-play-service-account.json`.)

## Real-world gotcha (Play app-content declarations)

A Play internal release can upload fine but **fail to roll out** until an
app-content declaration is completed in the Console — e.g. the Foreground
Service permissions declaration for apps using `FOREGROUND_SERVICE_*`. That's a
Console-only form; `store_release_promote` will surface the API's 403 verbatim
so the dev knows exactly which declaration to complete. (Observed on Yaver's own
app, build 267, 2026-06-24.)

## Not yet done (future)

- Beta App Review submission for external TestFlight groups via API.
- Reading per-tester *install/session* state (build beta detail) into the UI.
- Wiring `store_release_promote` into the `publish_*` farm flow so an automated
  ship can optionally roll out + invite a default group in one pass.
- Storing the `.p8` / SA JSON as inline vault content (no on-disk file) for
  fully stateless managed-cloud boxes (clients already accept it; surface a
  setup verb).
