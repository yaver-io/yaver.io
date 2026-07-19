# Autorun store — local SQLite persistence, dependency graph, deploy leases, verified-only "done"

Status: **spec, not yet implemented.** This document is what a fresh autorun
should build against. Everything below is a decision, not an option — where the
task file gave a preference, the reasoning is written down so a later reader can
tell whether the constraint still holds.

Companion to `AUTORUN_TASK_GRAPH.md` (why a run must stop being atomic) and
`AUTORUN_COLLECTIVE_SYNC_AUDIT.md` (may two runs touch the same code?). Those
answer *scheduling* questions; this one answers *state* questions:

> When an autorun says an item is done, is that a claim or a fact?
> And: when a run is deploying / building / touching a file, does another run
> on the same box know, and back off?

Today the answers are "a claim" and "no, not reliably." Status lives in git
refs, tmux pane titles, and free-text markdown. There is no local record of
*what was tried, how, against which commit*, and no cheap way to ask "which
items unblock now that #47 is actually validated?" There is also no shared
truth about "who is deploying to TestFlight right now?" — which is not
theoretical: **on 2026-07-19 during the run this document is being written
for, two concurrent autoruns hit `xcodebuild -exportArchive` against the same
`.xcarchive` in the same clone, twelve minutes apart, and both attempted to
upload to App Store Connect.** TestFlight caps at ~15–20 uploads/app/day with
no rollback; a race like that is one clean way to burn a whole day of the
quota on the same build number.

This store makes both questions answerable in one place.

---

## 1. Evaluation — SQLite, not Redis

Both were on the table. SQLite wins on every axis that matches the product.

| Axis | SQLite (embedded) | Redis (server) |
|---|---|---|
| Deployment shape | one file, opened by the process that uses it | second process to babysit, port, config, restart, auth |
| Fit with `npm install -g yaver-cli` | perfect — the agent already opens ~10 SQLite files | regression — a whole new install/uninstall/upgrade path per platform |
| Fit with scale-to-zero (Hetzner rule) | perfect — nothing to leave running | regression — always-on server, exactly the dimension the product sells against |
| Crash safety | WAL is crash-safe, atomic transactions | crash-safe with RDB+AOF, tuning required |
| Graph queries (recursive CTE) | native | client-side traversal or Lua |
| Backup | copy one file | dump/restore machinery |
| Cross-machine sync | not needed here — the store is per-device | not needed here |
| Concurrency (this workload) | one writer at a time, many readers, WAL fine at ≤100 QPS | designed for higher, but the workload doesn't need it |
| Cross-process lease semantics | `INSERT … ON CONFLICT` + `BEGIN IMMEDIATE` is a native mutex across every process that opens the file | native (SETNX / Lua) but requires the extra process |

The Convex privacy contract already forbids task inputs, task outputs, and
absolute filesystem paths leaving the device (`convex_privacy_test.go`). This
store carries exactly those forbidden fields — so it MUST stay local. A
network-attached Redis fights that constraint even if it's on `localhost`.

### 1.1 Driver — pure Go `modernc.org/sqlite`, not cgo `mattn/go-sqlite3`

`desktop/agent/go.mod` already imports `modernc.org/sqlite v1.48.1` and every
existing SQLite user in the agent (`backend_sql.go`, `email.go`,
`error_tracker.go`, `log_search.go`, `metrics_history.go`, `phone_backend.go`,
`uptime_alerts.go`, `threshold_alerts.go`, `tier_c_audit_pitr_ha.go`) opens it
via `sql.Open("sqlite", …)`. The release pipeline cross-compiles darwin+linux
on x64+arm64 with `CGO_ENABLED=0` (`Dockerfile.yaver-cloud`, `cloud_deploy.go`);
`mattn/go-sqlite3` would break that. **Use `modernc.org/sqlite`.** Do not add a
cgo dependency.

Open string convention (copy from `email.go:816`):

```go
sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
```

---

## 2. Where the store lives

```
~/.yaver/autoruns.db
~/.yaver/autoruns.db-wal
~/.yaver/autoruns.db-shm
```

Reuse `yaverDir()` from `discovery.go:83` (creates `~/.yaver` if missing). Do
not put the DB anywhere else — the privacy contract exists because Convex is
where cross-device state goes, and this state must not.

**Never sync any row or column of this DB to Convex.** Every column in
`autoruns.task`, `autorun_items.description`, `item_attempts.command`,
`item_attempts.output_excerpt`, `item_events.note`, `deploy_leases.holder`,
`code_locks.path` is exactly the kind of field
`fieldsWeForbidInAnyConvexPayload` blocks. Add a sync-boundary test if you find
yourself passing rows out of the agent process — see §12.

**Every autorun tmux session on this box, from every user, opens the same
file.** That is the design: the whole point of leases is that they only work
when everyone agrees on where the truth is. If a call site wants to fake it in
a test, pass a per-test DB path — do NOT introduce a global "mock store" mode.

---

## 3. Core schema — autoruns and items

Verbatim DDL. `PRAGMA foreign_keys=ON` for the connection.

### 3.1 `autoruns`

```sql
CREATE TABLE autoruns (
  id           TEXT PRIMARY KEY,             -- ULID
  task         TEXT NOT NULL,                -- path or slug of the task file
  runner       TEXT NOT NULL,                -- 'claude' | 'codex' | 'opencode' | 'glm' | ...
  gate         TEXT,                         -- exact --gate command string
  branch       TEXT,                         -- git branch the run works on
  workdir      TEXT,                         -- absolute path to the checkout
  tmux_session TEXT,                         -- tmux session name (for cross-surface UI)
  parent_autorun_id TEXT REFERENCES autoruns(id) ON DELETE SET NULL, -- when a run forks another
  status       TEXT NOT NULL,                -- 'active' | 'succeeded' | 'failed' | 'aborted' | 'paused' | 'merged'
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  finished_at  INTEGER,                      -- NULL until terminal
  merged_at    INTEGER,                      -- when the run's branch merged into main (see §6)
  merged_sha   TEXT                          -- the resulting main-branch commit
);

CREATE INDEX autoruns_status_idx ON autoruns(status);
CREATE INDEX autoruns_branch_idx ON autoruns(branch);
CREATE INDEX autoruns_workdir_idx ON autoruns(workdir);
CREATE INDEX autoruns_created_at_idx ON autoruns(created_at);
```

`workdir` and `task` may contain absolute paths (which is exactly why this
table can never be synced anywhere). `branch` + `workdir` are how the store
links a run to the git state it is producing — see §6.

### 3.2 `autorun_items`

One row per unit of work in a run.

```sql
CREATE TABLE autorun_items (
  id            TEXT PRIMARY KEY,             -- ULID
  autorun_id    TEXT NOT NULL REFERENCES autoruns(id) ON DELETE CASCADE,
  description   TEXT NOT NULL,
  stage         TEXT NOT NULL,                -- see §4 state machine
  ordering      INTEGER NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  runner        TEXT,                         -- which runner picked this up
  runner_seat   TEXT,                         -- machine/tmux-session
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  abandoned_reason TEXT,
  CHECK (stage IN ('pending','blocked','started','finished','testing','test_failed','validated','abandoned')),
  CHECK ((stage != 'abandoned') OR (abandoned_reason IS NOT NULL))
);

CREATE INDEX autorun_items_autorun_id_idx ON autorun_items(autorun_id);
CREATE INDEX autorun_items_stage_idx ON autorun_items(stage);
```

`ordering` is a caller-supplied sort hint, not a priority — it does not affect
dependency satisfaction. Dependencies gate eligibility (§3.3), `ordering`
breaks ties among items that are simultaneously eligible.

### 3.3 `item_edges` — item→item dependencies

```sql
CREATE TABLE item_edges (
  from_item TEXT NOT NULL REFERENCES autorun_items(id) ON DELETE CASCADE,
  to_item   TEXT NOT NULL REFERENCES autorun_items(id) ON DELETE CASCADE,
  PRIMARY KEY (from_item, to_item)
);
CREATE INDEX item_edges_to_idx ON item_edges(to_item);
```

**Semantics:** an edge `A→B` means "B depends on A" — B is only eligible when
A is `validated`. `finished` is NOT enough (see §4). This is the whole point:
downstream work cannot unblock on unverified upstream.

**Cycle detection is a WRITE-TIME requirement.** At insert time, before
committing the edge, run a recursive CTE from `to_item` and refuse if
`from_item` is reachable:

```sql
WITH RECURSIVE reach(id) AS (
  SELECT to_item FROM item_edges WHERE from_item = ?      -- new edge's from
  UNION
  SELECT ie.to_item FROM item_edges ie JOIN reach r ON ie.from_item = r.id
)
SELECT 1 FROM reach WHERE id = ? LIMIT 1;                 -- new edge's from
```

If that returns a row, the edge would close a cycle — reject with a clear
error naming both endpoints. Do this INSIDE the same transaction as the
insert; do not "check then insert" (a race would let two adds close a cycle).

### 3.4 `item_attempts` — every try, with evidence

```sql
CREATE TABLE item_attempts (
  id                TEXT PRIMARY KEY,
  item_id           TEXT NOT NULL REFERENCES autorun_items(id) ON DELETE CASCADE,
  n                 INTEGER NOT NULL,           -- 1-based attempt number for this item
  stage             TEXT NOT NULL,
  started_at        INTEGER NOT NULL,
  ended_at          INTEGER,
  exit_code         INTEGER,
  summary           TEXT,
  failure_output    TEXT,

  -- HOW this attempt was verified. method='none' is invalid for a validated
  -- item — see §5. Enforced at write time.
  method            TEXT NOT NULL DEFAULT 'none',
  command           TEXT,
  output_excerpt    TEXT,
  output_bytes      INTEGER,
  tests_passed      INTEGER,
  tests_failed      INTEGER,
  artifact          TEXT,                       -- build number, IPA path, commit SHA, deploy id
  commit_sha        TEXT,                       -- git HEAD in workdir when verify started
  verified_at       INTEGER,

  UNIQUE (item_id, n),
  CHECK (method IN ('unit_test','integration_test','build','lint','manual','deploy_smoke','none'))
);

CREATE INDEX item_attempts_item_id_idx ON item_attempts(item_id);
```

`commit_sha` is captured explicitly so `stale_validation` (§5.2) does not
depend on parsing `artifact`. `artifact` is a free-form string used by
whatever generated it; `commit_sha` is machine-checked.

### 3.5 `item_events` — append-only audit log

```sql
CREATE TABLE item_events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  item_id     TEXT NOT NULL REFERENCES autorun_items(id) ON DELETE CASCADE,
  from_stage  TEXT,
  to_stage    TEXT NOT NULL,
  at          INTEGER NOT NULL,
  actor       TEXT,
  note        TEXT
);
CREATE INDEX item_events_item_id_idx ON item_events(item_id);
CREATE INDEX item_events_at_idx ON item_events(at);
```

**Append-only.** Never `UPDATE item_events`, never `DELETE` except via the
cascade when the parent item is removed. Every stage change writes a row.

Cycle-time per stage is derived: `SELECT to_stage, at FROM item_events WHERE
item_id=? ORDER BY at` and diff. Do not add a "duration" column — it would be
derived and can drift.

### 3.6 Migrations

Ship an in-code migrations table:

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);
```

Apply migrations 1..N idempotently on open. See `phone_backend.go` for the
existing agent pattern. Never modify a migration after it ships — add a new
one.

---

## 4. State machine — the whole point

`finished` is **not** success. Only `validated` satisfies a dependency edge.
This is the schema-level version of the CLAUDE.md rule "a green build is not
evidence the code survived": it prevents a run converging on work that was
written but never checked.

### 4.1 Stages

| Stage | Meaning |
|---|---|
| `pending` | queued; may or may not have dependencies |
| `blocked` | dependencies unsatisfied OR an external blocker was recorded |
| `started` | a runner picked it up |
| `finished` | code written, but **not** yet verified — never treat as done |
| `testing` | verification in progress |
| `test_failed` | verification ran and failed |
| `validated` | verification passed; the ONLY terminal success state |
| `abandoned` | deliberately dropped, `abandoned_reason` required |

### 4.2 Allowed transitions

```
pending    → blocked         auto when an edge lands on it and the source isn't validated
pending    → started         a runner picks it up (edge sources all validated, or no edges)
blocked    → pending         auto when the last blocking edge's source reaches validated
blocked    → abandoned       user says "drop this"
started    → finished        code write complete, no verification yet
started    → test_failed     verification ran inline and failed
started    → abandoned       user aborts mid-work
finished   → testing         verification begins
finished   → abandoned       user aborts before verify
testing    → validated       verification passed (attempt row must have method != 'none' and exit_code = 0)
testing    → test_failed     verification failed (attempt row must carry the failure_output)
test_failed → testing        retry
test_failed → abandoned      give up
validated  → (terminal)      no outgoing transitions
abandoned  → (terminal)      no outgoing transitions
```

Reject every other transition at write time. `pending → validated`, `finished
→ validated`, `started → validated` are all illegal — the only way to reach
`validated` is via `testing → validated`, and only when a valid attempt row
exists (see §5).

### 4.3 Eligibility

An item is **eligible** (can move `pending → started`) iff every edge landing
on it has a `from_item` in stage `validated`. When an edge lands on a
non-validated item, put it in `blocked`. When the source reaches `validated`,
recompute all descendants and flip newly-eligible items back to `pending`.

Do the recomputation in a single transaction with the state change that caused
it, so a reader never sees a "validated with blocked dependent" moment.

---

## 5. Verification evidence

An item can only reach `validated` if the transition writes an attempt row
with:

- `method != 'none'`, and
- `exit_code = 0`, and
- `verified_at` populated, and
- `commit_sha` populated, and
- either `command` non-empty OR `method = 'manual'` (a human-checked item is
  the one case where "command" doesn't apply — record a `summary` instead).

**Enforce this in the write path, not by comment.** A single write helper
`RecordValidation(itemID, attemptID, VerificationEvidence)` should be the only
way to reach `validated`. Do not expose a lower-level "set stage=validated"
API.

### 5.1 Truncation policy

`output_excerpt` is the head + tail of stdout+stderr, capped at **4 KB head +
4 KB tail** (8 KB max). Store `output_bytes` as the total unclipped size so a
consumer knows truncation happened. Never store more — a store that carries
whole build logs is one power cycle from being where the log rotation lives.

### 5.2 Stale-validation flag

Every attempt row carries `commit_sha` (§3.4). When reading an item, compute
a derived flag:

```
stale_validation = (
  item.stage == 'validated'
  AND latest_attempt.commit_sha != HEAD(workdir)
)
```

Do not silently trust old results. `yaver autorun status` and every ops read
should return this flag. When a run merges into main and `workdir` HEAD moves
forward, every `validated` item whose evidence predates the merge is now
stale — that is the point.

### 5.3 Forbidden fields (mirror the Convex privacy contract, even locally)

The DB is local-only, but the discipline is the same because a bug in a
future export path would leak these:

- Do not put vault values in any column.
- Do not put raw tokens / API-key plaintext in any column.
- Do not put file contents in `output_excerpt`.
- Do not put user identifiable secrets in `note` / `summary` / `holder`.
- Do not put customer LAN IPs.

Add a test (`autorun_store_privacy_test.go`) that enumerates forbidden keys
and greps the schema + a sample INSERT for them. Fail closed.

---

## 6. Coordination tables — the race the store exists to fix

This section is why the store is not just a "task tracker." Today two autorun
tmux sessions on the same box can both try to deploy to TestFlight, both
`git checkout main` and rebase, both edit `mobile/ios/Info.plist` — and
nothing on this machine tells them to stop. The store solves this with a
handful of lease tables. All of them use the same primitive: a single row
per resource, uniquely-keyed, with `holder` (autorun_id) and expiry.

The primitive:

```sql
-- Acquire (fails cleanly if already held and not expired):
BEGIN IMMEDIATE;
INSERT INTO <lease_table> (key, holder, acquired_at, expires_at, purpose)
VALUES (?, ?, ?, ?, ?);
COMMIT;
-- (INSERT fails on PK conflict; caller then reads the row to see who holds it.)
```

`BEGIN IMMEDIATE` upgrades the lock immediately so two concurrent acquires
don't both pass the "does the row exist?" read. `expires_at` is enforced by
readers: rows whose `expires_at < now()` are treated as absent (a sweeper
`DELETE`s them, but do not rely on the sweeper — the read has to check).

Every lease has a `refresh(holder, key)` that pushes `expires_at` forward.
Every long-running holder MUST heartbeat every ~30s. A holder that dies loses
its lease within TTL and another autorun can pick it up.

### 6.1 `deploy_leases` — one deploy per target at a time

```sql
CREATE TABLE deploy_leases (
  target        TEXT PRIMARY KEY,             -- 'testflight' | 'playstore' | 'convex' | 'cloudflare-web' | ...
  autorun_id    TEXT NOT NULL REFERENCES autoruns(id) ON DELETE CASCADE,
  item_id       TEXT REFERENCES autorun_items(id) ON DELETE SET NULL,
  holder        TEXT NOT NULL,                -- tmux session or process descriptor — human-readable, never a secret
  workdir       TEXT NOT NULL,                -- checkout the deploy is running from
  branch        TEXT,                         -- git branch being shipped
  build_number  TEXT,                         -- CFBundleVersion / versionCode / commit SHA — see §7
  stage         TEXT NOT NULL,                -- 'archiving' | 'exporting' | 'uploading' | 'submitting' | 'finished' | 'failed'
  started_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL,             -- default: started_at + 60 min (typical TestFlight archive+upload)
  ended_at      INTEGER,                      -- NULL while active
  outcome       TEXT,                         -- 'success' | 'failure' | 'quota_exceeded' | 'aborted' — only when ended_at is set
  CHECK (stage IN ('archiving','exporting','uploading','submitting','finished','failed'))
);
```

**Rules:**

1. `deploy-testflight.sh`, `deploy-playstore.sh`, and every other deploy
   script MUST call the store to acquire a lease *before* the first
   destructive step (bumping CFBundleVersion, deleting an archive, uploading).
   The wrapper is a tiny CLI: `yaver autorun deploy-lease acquire testflight
   --workdir $PWD --branch $BRANCH --build $BUILD --autorun $ID` returns exit
   0 on success and exit 3 on "held by …". The deploy script exits 3 too,
   with the message the store returned.
2. **The exit-3 pathway is where the fix lands.** Today's race
   (2×`xcodebuild -exportArchive`) exists because nothing gates step 1 of the
   deploy on step 0 of the sibling. With the lease, the second script exits
   3 in the first second and the operator sees "TestFlight is being deployed
   by autorun <id> from workdir <path>, since <ts>. Wait for it or abort it
   with `yaver autorun deploy-lease abort testflight`."
3. The lease is *per-target*, not per-machine. A `cloudflare-web` deploy and a
   `testflight` deploy can run concurrently.
4. `stage` progresses as the deploy runs; the wrapper writes it. `finished`
   and `failed` are terminal (set `ended_at` + `outcome` at that point).
5. On heartbeat failure (holder crashes / SSH drops / tmux killed) the lease
   expires; another autorun can take it. **Do not delete other holders'
   leases from code.** The `abort` verb is user-facing and requires a confirm.
6. The record of a completed deploy stays in the row (with `ended_at` set) so
   `yaver autorun deploy-history testflight` can list past deploys with build
   numbers and outcomes. This is why the row is not `DELETE`d at end.

**One-clean-flag rule:** the deploy scripts MUST be idempotent with respect
to acquiring the lease — a re-run of the same wrapper after a crash should
see its own lease and continue, not "held by me, cannot acquire." The
`acquire` verb takes `--autorun` and matches on it: same autorun_id + same
target = same lease.

### 6.2 `build_leases` — one build per artefact target at a time

Same shape as `deploy_leases`, but for the *build* half of the deploy (an
xcodebuild archive that hasn't shipped yet, a gradle bundleRelease, a
`convex deploy` compile). Separated so that a machine can be building for
target X while deploying to target Y.

```sql
CREATE TABLE build_leases (
  target        TEXT PRIMARY KEY,             -- 'ios-archive' | 'android-bundle' | 'convex-compile' | ...
  autorun_id    TEXT NOT NULL REFERENCES autoruns(id) ON DELETE CASCADE,
  item_id       TEXT REFERENCES autorun_items(id) ON DELETE SET NULL,
  holder        TEXT NOT NULL,
  workdir       TEXT NOT NULL,
  branch        TEXT,
  stage         TEXT NOT NULL,                -- 'compiling' | 'linking' | 'archiving' | 'finished' | 'failed'
  started_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL,
  ended_at      INTEGER,
  outcome       TEXT
);
```

Why separate from `deploy_leases`: a build outcome is `finished` when there
is an artefact on disk; a deploy outcome is `finished` when the artefact left
the machine. Conflating them means the store can't answer "the archive
exists, the upload failed, is there anything to retry?"

### 6.3 `code_locks` — path-level "someone is actively touching this"

An agent should not edit `mobile/ios/Info.plist` while another agent's build
of `mobile/` is halfway through `expo prebuild`. Track file/tree ownership
with a soft lock:

```sql
CREATE TABLE code_locks (
  path          TEXT PRIMARY KEY,             -- absolute path, normalised (no trailing slash, no symlink resolution)
  autorun_id    TEXT NOT NULL REFERENCES autoruns(id) ON DELETE CASCADE,
  item_id       TEXT REFERENCES autorun_items(id) ON DELETE SET NULL,
  holder        TEXT NOT NULL,
  purpose       TEXT NOT NULL,                -- 'edit' | 'build' | 'deploy'
  started_at    INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL
);
CREATE INDEX code_locks_autorun_idx ON code_locks(autorun_id);
```

**Query surface for a would-be editor:**

```sql
-- "Is any prefix of my target path locked by someone else?"
SELECT * FROM code_locks
WHERE ? LIKE path || '/%' OR path = ?  -- ancestor or exact
   OR path LIKE ? || '/%'              -- descendant
   AND expires_at > ?
   AND autorun_id != ?;                -- not me
```

An editor that gets a hit should NOT edit; it should surface "waiting on
autorun <id> — <purpose> since <ts>" and re-check on a schedule. **The lock
is advisory,** not enforced by the OS: nothing stops a user from
`vim`-editing the file. That is fine; the goal is to keep autoruns from
stepping on each other, not to build a mandatory filesystem lock.

**Coarseness rule:** builds/deploys lock the whole project subtree
(`mobile/`, `web/`, `desktop/agent/`, `backend/convex/`), not individual
files. Edits lock the files they touch. A build-lock over `mobile/` refuses
an edit-lock over `mobile/ios/Info.plist`; two edit-locks over different
files in `mobile/` coexist.

### 6.4 `branch_leases` — one autorun per branch at a time

Two autoruns pushing to the same branch is one force-push away from
destroying work. The `main` branch is special (see §7) but every autorun
branch has a lease.

```sql
CREATE TABLE branch_leases (
  branch        TEXT PRIMARY KEY,             -- 'autorun/deploy-and-harden/claude', ...
  autorun_id    TEXT NOT NULL REFERENCES autoruns(id) ON DELETE CASCADE,
  workdir       TEXT NOT NULL,
  holder        TEXT NOT NULL,
  acquired_at   INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL
);
```

The branch lease is what makes `yaver autorun start --branch X` cleanly
refuse a second start on the same branch.

### 6.5 The `main`-branch merge lease

`main` is not just another branch; it is the convergence point every autorun
should eventually reach (§7). Only one autorun can be in the middle of the
merge/rebase/push sequence at a time.

Reuse `branch_leases` with `branch='main'` — no extra table. The convention:

- Grab the `main` lease only for the *duration of the merge*, not the whole
  autorun.
- Rebase → run gate → push, all inside the lease.
- Release immediately after push.
- TTL is short (5 minutes) so a stuck merge doesn't block the box.

---

## 7. Convergence to `main` — the eventual half of "verified"

Autoruns don't ship by staying on their branch. The eventual model is:

1. Autorun works on `autorun/<slot>/<runner>` (a branch lease per §6.4).
2. Items reach `validated` (§4, §5) via real verification.
3. When the run's overall status is a success, the store's own convergence
   worker (or a user-triggered `yaver autorun land <id>`) does the following
   inside the `main` branch-lease (§6.5):

   ```
   git fetch github main
   git checkout main
   git pull --ff-only github main
   git merge --no-ff autorun/<slot>/<runner>
   <run the same --gate>              # sanity check on merged state
   git push github main
   ```

4. On success, set `autoruns.status = 'merged'`, `autoruns.merged_at = now`,
   `autoruns.merged_sha = <resulting main SHA>`.
5. Release the branch lease + main lease.

If the gate fails on `main` (a conflict passed textually but broke the
merged state), the merge is aborted (`git merge --abort`), the autorun's
status stays `succeeded` (its own work was valid), and the store records a
`main_merge_failures` row for that autorun so the user can see the diff.

`main_merge_failures`:

```sql
CREATE TABLE main_merge_failures (
  autorun_id  TEXT NOT NULL REFERENCES autoruns(id) ON DELETE CASCADE,
  attempted_at INTEGER NOT NULL,
  reason      TEXT NOT NULL,                  -- 'conflict' | 'gate_failed' | 'push_rejected' | 'lease_lost'
  detail      TEXT,                           -- head+tail of the failure output, truncated to 4 KB (§5.1)
  PRIMARY KEY (autorun_id, attempted_at)
);
```

**Non-negotiable rules for the convergence worker:**

- Never `--force`. Never `push --force-with-lease`. A rejected push means
  someone else landed; refetch and retry.
- Never touch anyone else's commits. The immutability rule from CLAUDE.md
  applies at the store level too — the merge is `--no-ff` so a revert points
  at the merge, not the incorporated commits.
- Never skip the gate. If the caller passes `--skip-gate`, refuse — this is
  the difference between "validated on my branch" and "validated on main."
- Never merge while any of the run's items are `test_failed` or `blocked`.
  The convergence CLI refuses.

---

## 8. Recap clips and highlight reels — "what happened while I was away"

Autoruns run for hours. Users watch the tape after. Today the recap is a
tmux `capture-pane` blob and whatever the runner wrote in markdown; the
`recap_speak` ops verb (commit 5ad3e8f87 in this repo's history) reads
those out over TTS. The store adds structured highlights so the mobile UI
can show a football-match-alike reel of *what actually mattered* in the run
— a validated item, a `test_failed`, a lease conflict, a deploy that shipped
— each linked to a short capture (screenlog frame range, PTY excerpt,
image, audio).

The store OWNS the metadata; the media stays on disk under
`~/.yaver/recaps/<autorun_id>/<clip_id>.<ext>`. The DB is not a blob store.

### 8.1 `recap_clips` — atomic highlight units

```sql
CREATE TABLE recap_clips (
  id            TEXT PRIMARY KEY,             -- ULID
  autorun_id    TEXT NOT NULL REFERENCES autoruns(id) ON DELETE CASCADE,
  item_id       TEXT REFERENCES autorun_items(id) ON DELETE SET NULL,
  attempt_id    TEXT REFERENCES item_attempts(id) ON DELETE SET NULL,
  kind          TEXT NOT NULL,                -- 'validation' | 'test_failed' | 'deploy_success' | 'deploy_failure' | 'lease_conflict' | 'merge' | 'abandon' | 'note' | 'ambient'
  weight        INTEGER NOT NULL DEFAULT 50,  -- 0..100; the highlight-ranker uses this to pick top N for a reel
  title         TEXT NOT NULL,                -- one-line human title ('build 448 uploaded', 'follow-up test flaked')
  summary       TEXT,                         -- 1-3 sentences of context
  actor         TEXT,                         -- 'runner:claude', 'user', 'system:auto-block'
  started_at    INTEGER NOT NULL,             -- clip range start (screenlog frames / PTY seek / video timestamp)
  ended_at      INTEGER NOT NULL,             -- clip range end
  produced_at   INTEGER NOT NULL,             -- when the clip was written to disk

  -- Media pointers. All paths are RELATIVE to ~/.yaver/recaps/<autorun_id>/,
  -- so a machine that copied the whole recap folder to a phone still resolves.
  media_kind    TEXT NOT NULL,                -- 'video' | 'image' | 'pty' | 'audio' | 'multi'
  media_path    TEXT,                         -- primary media file (relative to recap dir)
  poster_path   TEXT,                         -- optional thumbnail/still (relative to recap dir)
  duration_ms   INTEGER,                      -- media length; 0 for stills

  -- Where the clip came from — the same producers the agent already runs
  -- for screenlog, PTY, ambient capture. Never a new capture engine.
  source        TEXT NOT NULL,                -- 'screenlog' | 'pty' | 'camera' | 'appletv_capture' | 'mobile_view' | 'imported'
  source_ref    TEXT,                         -- opaque back-reference (screenlog session id, tmux session name, remote-desktop stream id)

  CHECK (kind IN ('validation','test_failed','deploy_success','deploy_failure','lease_conflict','merge','abandon','note','ambient')),
  CHECK (media_kind IN ('video','image','pty','audio','multi')),
  CHECK (source IN ('screenlog','pty','camera','appletv_capture','mobile_view','imported')),
  CHECK (weight BETWEEN 0 AND 100)
);

CREATE INDEX recap_clips_autorun_idx ON recap_clips(autorun_id);
CREATE INDEX recap_clips_item_idx ON recap_clips(item_id);
CREATE INDEX recap_clips_started_idx ON recap_clips(started_at);
CREATE INDEX recap_clips_kind_idx ON recap_clips(kind);
```

**Rules for producers:**

- Every `RecordValidation` write should also `INSERT` a `kind='validation'`
  clip if a screenlog session or PTY buffer covers the verification window
  (the producer already knows both `started_at` and `verified_at` — clip range
  is that window ± a small pad). This is the store's own hook; the runner
  doesn't have to remember.
- Every `deploy_leases` lease that terminates in `outcome='success'` or
  `outcome='failure'` writes a `deploy_success`/`deploy_failure` clip whose
  range is the whole lease. The wrapper deploy scripts (§6.1) do this at
  release time.
- Every rejected lease acquire writes a `lease_conflict` clip. Weight defaults
  to 60 — a race is instructive.
- Every `test_failed` transition writes a `test_failed` clip with the attempt's
  `failure_output` referenced through `attempt_id` (do NOT duplicate the
  failure text into the clip's `summary` — the attempt row already has it).
- Every `main`-branch merge (§7) writes a `merge` clip with the resulting
  `merged_sha` in `source_ref`.
- `note` and `ambient` are the escape hatches: a runner explicitly logs a
  highlight (a discovery, a wrong-turn, a UI screenshot the user asked for),
  and the ambient capture (screenlog running the whole time) periodically
  emits low-weight clips so the reel isn't only bright moments.

### 8.2 `recap_reels` — user-shareable assemblies

A reel is the football-highlights: N clips picked and ordered, plus optional
narration.

```sql
CREATE TABLE recap_reels (
  id            TEXT PRIMARY KEY,
  autorun_id    TEXT REFERENCES autoruns(id) ON DELETE SET NULL,  -- reels can span multiple runs; NULL when curated
  title         TEXT NOT NULL,
  subtitle      TEXT,                         -- 'today's run: 4 validated, 1 flake, TestFlight build 448'
  created_at    INTEGER NOT NULL,
  duration_ms   INTEGER,                      -- final assembled length; 0 until produced
  audience      TEXT NOT NULL DEFAULT 'self', -- 'self' | 'team' | 'public'  (only 'self' by default — content is user-owned)
  status        TEXT NOT NULL,                -- 'draft' | 'ready' | 'played'
  narration_path TEXT,                        -- optional TTS voiceover, relative to ~/.yaver/recaps/reels/<reel_id>/
  poster_path   TEXT,                         -- reel-level cover image
  CHECK (audience IN ('self','team','public')),
  CHECK (status IN ('draft','ready','played'))
);

CREATE TABLE recap_reel_clips (
  reel_id       TEXT NOT NULL REFERENCES recap_reels(id) ON DELETE CASCADE,
  clip_id       TEXT NOT NULL REFERENCES recap_clips(id) ON DELETE CASCADE,
  ordering      INTEGER NOT NULL,             -- 0-based, contiguous
  trim_start_ms INTEGER NOT NULL DEFAULT 0,   -- optional inner trim of the clip
  trim_end_ms   INTEGER,                      -- NULL = clip's own end
  caption       TEXT,                         -- one-line overlay text
  PRIMARY KEY (reel_id, ordering)
);
CREATE INDEX recap_reel_clips_clip_idx ON recap_reel_clips(clip_id);
```

**Assembly is a job, not a DB write.** The store records the plan
(`recap_reels` + `recap_reel_clips`); a producer (`recap_render.go`, a
follow-up file) concatenates media, runs the TTS narration if any, writes
`<reel_id>/reel.mp4`, and flips `status='ready'`. The reel *plan* is
recoverable and re-renderable; the reel *file* is a cache.

### 8.3 Ranker rules for the default "today" reel

The mobile UI's default "watch what happened" reel is auto-assembled:

- Pick clips in the autorun's timeline order (`ORDER BY started_at`).
- Score by `weight + kind_bonus` where `kind_bonus` boosts `validation`,
  `deploy_success`, `deploy_failure`, `merge`; suppresses long `ambient`
  runs.
- Cap total duration at 90 s by default (user override); trim longest clips
  first (`trim_end_ms`).
- Always include the last event (whatever it is) — the tape must end where
  the run ended, not on the last "interesting" moment.
- Skip clips whose media file is missing on disk. Do not silently substitute.

### 8.4 Rendered artefacts and privacy

- Rendered `reel.mp4` and `narration.wav` live under
  `~/.yaver/recaps/reels/<reel_id>/`. Never in the DB. Never in Convex.
- `audience='team'` and `'public'` are opt-in states — the DB has the column
  so the mobile UI can offer sharing, but the producer must never upload
  unless the user explicitly flipped it. Default is `'self'`.
- Anything captured from a paired remote-desktop / Apple TV / camera stream
  inherits the same consent as the source (see `remote_runtime.go` /
  `appletv.go`). The store does not re-authorise; it only records what was
  captured with existing consent.
- Do not put user-identifiable strings into `title` / `caption` beyond what
  the run already produced. A reel is a memory aid, not a scraping target.

### 8.5 Wire surfaces

The reel/clip model is what makes these features possible; each is a UI/agent
task, not a store task, but the store is the source of truth:

- **Mobile Tasks screen** — every autorun row has a "▶︎ recap" affordance
  when a `recap_reels` row exists for it in status `ready`.
- **Mobile autorun detail** — the item list shows a small clip thumbnail per
  item if a `kind='validation'` / `'test_failed'` clip exists for it.
- **Web autorun view** — the same detail, plus a scrubber timeline built from
  `recap_clips.started_at/ended_at`.
- **`recap_speak` ops verb** (already exists) — pulls titles + summaries in
  timeline order and TTSes them; no change needed beyond reading from the
  store instead of tmux capture.
- **`recap_render`** (new) — assembles a reel from a plan.

### 8.6 API surface additions

Add to §10.1 HTTP:
```
POST   /autoruns/{id}/clips                  add a clip (used by producers)
GET    /autoruns/{id}/clips                  list clips, filterable by kind
GET    /clips/{id}                           one clip with resolved media path
POST   /reels                                create a reel (with optional clip plan)
GET    /reels                                list reels
GET    /reels/{id}                           reel + ordered clips + rendered artefact status
POST   /reels/{id}/render                    kick the assembly job
DELETE /reels/{id}                           deletes the plan; cascade the rendered file
```

Add to §10.2 MCP ops:
```
recap_clip_add          insert a clip
recap_clips             list clips for an autorun/item
recap_reel_create       create a reel plan
recap_reel_render       kick the render
recap_reels             list reels
```

Add to §10.3 CLI:
```
yaver autorun recap [--autorun <id>] [--reel <reel_id>] [--play]
    show clips or reels; --play streams to the default surface via the
    existing recap_speak / mobile pane.
```

### 8.7 Tests to add

- Producer hooks: `RecordValidation` writes a `validation` clip when a
  screenlog session covers the window; no clip when there is no source.
- Deploy-lease terminals: acquire → heartbeat → release with success/failure
  outcomes writes exactly one clip each.
- Ranker: given a synthetic run with 20 mixed clips, the 90s cap trims the
  right clips first and always includes the final event.
- Media-missing: a reel with a clip whose file is deleted still renders (the
  clip is skipped, and the render logs it) — no silent substitution.
- Sharing default: creating a reel via the API without audience produces
  `'self'`.

---

## 9. Deploy status derived from the leases

Once a deploy runs through a lease (§6.1), the store is the truth about what
was shipped. Every read surface (§10) should be able to answer, per target:

- **Currently deploying?** (row in `deploy_leases` with `ended_at IS NULL`
  and `expires_at > now`)
- **Last successful build number**, per target (max `build_number` from
  `deploy_leases` rows where `outcome='success'`).
- **Last failure** — build number, timestamp, reason.
- **Uploads today** — count of `deploy_leases` rows for target 'testflight'
  or 'playstore' where `outcome IN ('success','failure')` and `started_at`
  is in the last 24h. Compare against the target's quota. TestFlight caps at
  ~15–20/app/day (a warning at 12, a hard "won't acquire" at 18 — driven by
  a per-target `quota_hint` in `buildTargets`).

The lease row is the record. There is no separate "deploys" table — one
lease = one deploy attempt. If a script wants to record a deploy that
happened outside the wrapper (a manual one from the shell), it can insert a
completed row directly via a dedicated verb; do NOT let it acquire+release
without heartbeating.

**Quota-blind is quota-out.** The `acquire` verb reads uploads-today before
returning success and refuses on a quota-exhausted target. The whole reason
this store exists for deploys is to make "wait a day" the answer, not
"upload the same broken build a 21st time."

---

## 10. Public API — HTTP + MCP ops + CLI

### 10.1 HTTP

All under the existing agent HTTP server (`httpserver.go`), auth-gated the
same way other agent endpoints are.

**Autoruns + items + edges + attempts + validation:**

```
POST   /autoruns
POST   /autoruns/{id}/items
POST   /autoruns/{id}/edges                (400 on cycle)
POST   /autoruns/{id}/items/{iid}/transition
POST   /autoruns/{id}/items/{iid}/attempts
PATCH  /autoruns/{id}/items/{iid}/attempts/{n}
POST   /autoruns/{id}/items/{iid}/validate  ← the ONE way to reach validated
GET    /autoruns
GET    /autoruns/{id}
GET    /autoruns/{id}/items/{iid}
DELETE /autoruns/{id}                        (only if status != active)
```

**Deploy + build + code + branch leases:**

```
POST   /leases/deploy/{target}/acquire       {autorun_id, workdir, branch, build_number}
POST   /leases/deploy/{target}/heartbeat     {autorun_id, stage}
POST   /leases/deploy/{target}/release       {autorun_id, outcome}
POST   /leases/deploy/{target}/abort         (user-invoked; requires confirm)
GET    /leases/deploy                        (current + recent history)
GET    /leases/deploy/{target}
GET    /leases/deploy/{target}/quota         (uploads today vs cap)

POST   /leases/build/{target}/acquire  … (same shape)
POST   /leases/code/acquire                  {path, purpose}   (returns 409 on conflict with detail)
POST   /leases/code/release
GET    /leases/code                          (all active locks)
POST   /leases/branch/{name}/acquire
POST   /leases/branch/{name}/release
GET    /leases/branch
```

**Convergence:**

```
POST   /autoruns/{id}/land                   drive merge → gate → push under main lease
GET    /autoruns/{id}/merge-failures
```

### 10.2 MCP ops verb

Add to the `ops` grand-tool (`ops_*.go`). Verbs, mirroring the HTTP surface,
sorted alphabetically:

```
autorun_abandon             mark abandoned with a reason
autorun_add_edges           add edges (400 on cycle)
autorun_add_items           add items
autorun_create              new autorun
autorun_deploy_history      past deploys per target, with build numbers + outcomes
autorun_deploy_lease        acquire / heartbeat / release / abort / status
autorun_build_lease         acquire / heartbeat / release / abort / status
autorun_code_lock           acquire / release / status
autorun_branch_lease        acquire / release / status
autorun_finish_attempt      finish an attempt
autorun_get                 autorun + item summary
autorun_item                one item, with attempts, events, blockers, stale_validation
autorun_land                drive merge → gate → push into main
autorun_list                list autoruns
autorun_start_attempt       record a new attempt
autorun_transition          move an item's stage
autorun_validate            the ONE way to reach validated (calls the same write helper)
```

Every verb's description must state the state-machine constraint it enforces.
The point of exposing this on MCP is that an agent asking "which item can I
work on next?" (or "is anyone deploying TestFlight right now?") gets the
answer without shelling out.

### 10.3 CLI

`yaver autorun` (existing subcommand in `autorun_cmd.go`) grows:

```
yaver autorun status [--autorun <id>] [--json]
    per item: stage, attempts, method+command+exit, artifact, commit_sha,
    stale_validation, blockers, holder-if-any

yaver autorun leases [--json]
    current deploy leases, build leases, code locks, branch leases

yaver autorun deploy-lease <target> <acquire|heartbeat|release|abort|status>
    the wrapper deploy scripts call. Exit 0 on acquire, exit 3 on held,
    exit 4 on quota-exhausted.

yaver autorun land <id>
    merge → gate → push under main lease. Refuses if any item is
    test_failed / blocked / stale.
```

Add a `--json` variant for every read command for MCP + web/mobile consumers.

---

## 11. What NOT to build

- No web/mobile surface **yet**. This spec is the store + agent-side API only.
  A UI on top is a separate change — the point of writing the API first is
  that mobile/web/tvOS all consume the same JSON.
- No Redis fallback. If SQLite doesn't fit some future use case, revisit; do
  not build both.
- No sync-to-Convex path. This DB is per-device by design (§2). If two boxes
  need shared autorun state, that is a separate design about shipping
  *validated* items forward, not the raw store.
- No "status" free-text column parallel to `stage`. Free-text is what the
  current markdown-based state is; the schema replaces that with the state
  machine.
- No "finished ⇒ validated auto-promotion." That would defeat the whole
  distinction. `finished` is a legitimate stage — code written, verify still
  owed — and the only way past it is through `testing`.
- No mandatory OS-level file lock. `code_locks` is advisory (§6.3).
- No `--force` in the convergence worker. Ever.
- No leaking rows to Convex or any other network destination. The store is
  local, forever.

---

## 12. Tests

Follow the repo's convention: real DB in a temp dir, no mocks (see
`desktop/agent/*_test.go` for examples — `phone_backend_test.go`,
`email_test.go` are the closest patterns).

Required coverage:

1. **Schema round-trip** — open a fresh DB, verify tables + indexes are
   present, verify all `CHECK` constraints reject bad inserts.
2. **State-machine legality** — for each illegal transition in §4.2, assert
   the write is rejected.
3. **Eligibility** — build `A → B → C`, assert C is `blocked` until A and B
   both validated; assert flip from `blocked → pending` happens atomically.
4. **Cycle rejection** — assert an insert that would close a cycle is rejected
   AND that the DB has no partial edge afterwards (transaction rolls back).
5. **Validation gate** — assert `RecordValidation` refuses `method='none'`,
   refuses `exit_code != 0`, refuses items whose latest attempt is
   `test_failed`.
6. **Stale-validation flag** — assert it goes false when the commit under
   `workdir` changes.
7. **Attempt history** — an item that fails 3 times then passes has 4 rows
   with `n=1..4`, exit codes `[1,1,1,0]`, and its `attempt_count = 4`.
8. **Deploy-lease race** — spawn two goroutines that both `acquire testflight`
   concurrently against the SAME DB file; assert one wins, the other gets a
   held-by-<other> error. Then expire the winner's lease and assert the
   loser can retry successfully.
9. **Deploy-lease quota** — insert 18 successful `testflight` leases in the
   last 24h; assert the 19th `acquire` returns `quota_exhausted`.
10. **Code-lock ancestor/descendant** — `code_locks` for `mobile/` refuses
    an acquire of `mobile/ios/Info.plist`; two acquires of unrelated files
    both succeed.
11. **Branch-lease** — two autoruns cannot both hold `autorun/foo`; either
    can hold `autorun/bar` while the other holds `autorun/foo`.
12. **Main-lease merge** — happy path merges cleanly; a gate failure aborts
    the merge and records `main_merge_failures`; the lease is released
    afterwards.
13. **Privacy** — greps schema + sample inserts for forbidden keys (§5.3).
14. **HTTP + MCP surface** — spin up the agent's real HTTP server on a
    random port (see `httpserver_test.go`), drive each endpoint, assert
    responses.
15. **CLI** — `yaver autorun deploy-lease acquire` exit codes 0/3/4 for
    ok/held/quota (drive the CLI subprocess against a temp-DB agent).

No mocking of `sql.DB`. No mocking of the filesystem. No mocking of
subprocess exits. The whole point of the repo's test convention is that
these tests fail the way production would.

---

## 13. Implementation order (for the next autorun)

1. Migration + schema (§3, §6) in `desktop/agent/autorun_store.go`. Open +
   apply migrations idempotently.
2. Write helpers for autoruns/items/attempts: `CreateAutorun`, `AddItem`,
   `AddEdge` (with cycle check), `Transition`, `StartAttempt`,
   `FinishAttempt`, `RecordValidation`, `Abandon`, `ListAutoruns`,
   `GetItem` (includes derived `stale_validation` + blockers).
3. Write helpers for leases: `AcquireDeployLease`, `HeartbeatDeployLease`,
   `ReleaseDeployLease`, `AbortDeployLease`, `DeployLeaseQuota`, and the
   parallel set for build/code/branch. Use `BEGIN IMMEDIATE` + `INSERT
   OR FAIL` for the acquire primitive; make it a shared helper.
4. Tests for §12.1–§12.13 first; then wire HTTP/MCP/CLI.
5. HTTP endpoints (§10.1) in a new file `autorun_store_http.go`, using the
   existing routing pattern.
6. Ops verbs (§10.2) in `ops_autorun_store.go` — verbs delegate to the same
   helpers, so the state-machine rules stay in one place.
7. CLI subcommands (§10.3) in the existing `autorun_cmd.go`.
8. Wrapper hooks in `scripts/deploy-testflight.sh`, `scripts/deploy-playstore.sh`,
   `scripts/deploy-web.sh` that acquire/heartbeat/release the deploy lease.
   This is where the concurrent-deploy race the store exists to fix actually
   gets fixed.
9. Convergence worker (§7) as `autorun_land.go` + `yaver autorun land`. Do
   not enable it by default — a per-autorun flag opts in for now.

Do NOT start with the UI. Do NOT add sync. Do NOT relax the transitions to
make a stubborn integration test pass — the strictness is the feature. Do
NOT skip the wrapper hooks: the whole point of the store is defeated if the
deploy scripts don't call it.

---

## 14. Non-goals worth naming

- **Not a task queue.** No workers, no dequeue semantics, no leases handed
  out on request. Runners drive items through the state machine explicitly;
  the store answers "what is the truth?" and "what can move now?", not
  "give me the next task."
- **Not a replacement for git.** The commit SHA lives in `commit_sha`, but
  the code is still in git. This DB carries *claims about* the code.
- **Not observability.** Metrics are somewhere else (`metrics_history.go`).
  This is a system-of-record, not a dashboard.
- **Not a distributed lock.** Every lease is scoped to one machine's DB
  file. If two machines both deploy the same app, they need a different
  mechanism — but they shouldn't; the whole product is "one box, many
  runners."
- **Not a CI orchestrator.** The store records outcomes; it does not launch
  builds. A runner still shells out to `xcodebuild`; the store just makes
  the shell-out safe from concurrent siblings.

---

## 15. Related work in this session

This session was scoped to four phases:

- Phase 1 (iOS TestFlight deploy) — attempted, deploy was cancelled by user
  before upload. The incident that prompted the coordination tables in §6
  came from Phase 1: two autoruns concurrent-deploying, one from this
  session and one from a sibling tmux, both reaching `xcodebuild
  -exportArchive` in the same clone. That is the failure mode §6.1 exists
  to prevent.
- Phase 2 (Android Play Store) — deferred.
- Phase 3 (cross-surface wiring for the build-doctor signing+disk probes,
  plus a mobile-side deploy-trigger button) — deferred to a follow-up
  session. The design intent for Phase 3 is that the deploy-trigger button
  in the mobile UI does `POST /leases/deploy/testflight/acquire` before
  anything else — so tapping "Deploy" from a phone is safe against a
  simultaneous CLI deploy on the paired Mac. **This is a load-bearing
  reason to land the store first.**
- Phase 4 (this document) — the store spec.
