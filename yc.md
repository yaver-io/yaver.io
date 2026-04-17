# YC Application Sprint — Yaver

**Deadline:** May 4, 2026, 5pm PT (hard stop; application closes 8pm PT).
**Today:** April 17, 2026. **17 days.**
**Decision date:** June 5, 2026.

## The One-Line Pitch

> Yaver is the **backend that moves with you** — build it on your phone, grow it
> onto your own Mac, lift it to our cloud. Same code, same data, no migration.

Everything in the application, the demo, and the HN launch must ladder to that sentence. If a feature doesn't support it, cut it from the pitch (not from the repo).

## The Backend Continuum (the core insight)

One runtime, three tiers — user picks how far to scale, never rewrites:

| Tier | Where it runs | When | Price |
|---|---|---|---|
| **Phone** | SQLite in the Yaver mobile app | first CRUD, prototyping, offline demos | free |
| **Your dev machine** | `yaver serve` on the user's Mac / Mac mini / Pi / Linux box | real-device testing, self-hosted staging, privacy-sensitive workloads | free (their HW) |
| **Yaver Cloud** | Managed Hetzner tier (CPU / GPU / multi-user) | production traffic, teams, zero-ops | $10–$449/mo |

Same portable manifest (schema + auth personas + seed + storage) is materialized at each tier. No driver swap, no schema rewrite, no "export and redeploy" ceremony. That continuity is the product.

**Supabase/Convex/Postgres/Neon/Turso/Firebase** are **escape hatches, not promotion targets.** We ship one-click export to all 19 for lock-in-free trust signaling — but the headline path is Yaver end-to-end.

## The Wedge Demo (2 minutes, shot on a phone screen)

1. Open Yaver on iPhone.
2. Voice/text prompt: *"todo app with login"*.
3. AI scaffolds RN app + Yaver mini-backend (SQLite, on-phone) in seconds.
4. App runs on phone — login works, todos save, all local.
5. Tap **Grow → Your Dev Machine**. Same manifest materializes on the user's Mac via `yaver serve`. Phone now talks to the Mac-hosted backend over P2P. Zero data loss.
6. Tap **Grow → Yaver Cloud**. Same manifest provisions on a managed box. Shareable URL.
7. (Optional fallback slide) **Export to Supabase** — same manifest, escape hatch proves no lock-in.

If this runs end-to-end without a hitch, the application is close to guaranteed a first-round read. If it doesn't, nothing else in the application will save it.

## What Gets Cut From the Pitch

Keep in the repo, remove from the YC narrative:

- Mobile worker fleet (retention hook, mention in one line)
- Guest access
- Voice AI providers
- Hybrid mode / planner-implementer layering
- Session handoff
- Container sandbox
- Browser automation
- Distributed inference
- Support sessions
- LLM serving (**never mention — this kills deals**)

CLAUDE.md is 3× too big for the pitch. It stays in the repo — but the application, demo video, and landing page talk about one thing.

## 17-Day Calendar

### Week 1 — Build the Wedge (Apr 17–23)

| Date | Ship | Done when |
|---|---|---|
| Apr 17 (Fri) | Scope + `remained.md` for mini-backend MVP (collections, CRUD, auth personas, seed data). | Checklist exists, autodev kicks first item. |
| Apr 18 (Sat) | Mini-backend runtime in Yaver mobile app — SQLite + schema DSL + query/mutation API. Local-only. | Phone app can define a collection and CRUD it. |
| Apr 19 (Sun) | Mini-backend persistence + fixtures. Portable project manifest (schema.json). | Project manifest round-trips import/export on phone. |
| Apr 20 (Mon) | "Create project from phone" flow: prompt → agent scaffolds RN + mini-backend on user's Mac. | Voice/text prompt on phone produces a running RN project on the dev Mac. |
| Apr 21 (Tue) | Deploy toggle UI: `[Your Dev Machine]` / `[Yaver Cloud]`. Dev-machine path = push-to-device. Cloud path = stub returning fake URL. | UI ships; dev-machine branch actually works. |
| Apr 22 (Wed) | Promote flow: one-tap export phone → user's dev machine (tar + git init + push via agent). | `yaver projects promote <id>` works from mobile. |
| Apr 23 (Thu) | Dogfood: build a real app (todo or habit tracker) end-to-end from phone. Fix every friction. | You built it from your phone only, no MacBook touch. |

### Week 2 — Polish + Proof (Apr 24–30)

| Date | Ship | Done when |
|---|---|---|
| Apr 24 (Fri) | Yaver Cloud path deploys to **one Hetzner box**. Single staging target, no autoscale, no SLA. | Cloud-deploy button works in demo. |
| Apr 25 (Sat) | Recruit 3 beta users (RN devs via Twitter/Reddit/Bluesky DM). Watch them use it on a call. | 3 users, 3 recordings, written notes. |
| Apr 26 (Sun) | Fix top 5 crashes/confusions from beta feedback. No new features. | Beta users successfully ship one screen each. |
| Apr 27 (Mon) | Landing page rewrite: `yaver.io` → "Build mobile apps from your phone. Deploy to your Mac or our cloud." One CTA. | Live on yaver.io; old feature grid gone. |
| Apr 28 (Tue) | Pre-launch polish: demo video B-roll, HN title draft, first HN comment draft. | Dry-run the HN launch with a friend. |
| Apr 29 (Wed) | **HN launch**, 9am PT (peaks morning US time). Reply to every comment for 6 hours. | Post is live; you're at the keyboard until noon PT. |
| Apr 30 (Thu) | Buffer day. Something is broken. Also: measure — signups, projects created, projects deployed, paying users. | Real numbers in a doc, ready to paste into application. |

### Final Stretch — YC Application (May 1–4)

| Date | Ship | Done when |
|---|---|---|
| May 1 (Fri) | **1-minute product demo video.** Phone screen recording. No talking head, no intro music. Just the magic. 3 takes max. | Uploaded to YouTube (unlisted). |
| May 2 (Sat) | **1-minute founder video.** Phone camera, no script, why-you-why-this-why-now. 3 takes max. | Uploaded to YouTube (unlisted). |
| May 3 (Sun) | Write the application. See "Application Answers" below. | Every field drafted; partner (spouse/friend) reviewed. |
| May 4 (Mon) | **Submit by 5pm PT.** Leaves 3 hours for form bugs. | Confirmation email received. |

## HN Launch Playbook (Apr 29)

**Go / No-Go check on Apr 28 evening:**

- [ ] Demo runs end-to-end on a fresh phone in under 3 minutes.
- [ ] Landing page loads in <2s, has one CTA, no feature grid.
- [ ] Signup works; first project creation works; you've tested on a phone that has never opened Yaver before.
- [ ] `status.yaver.io` or at least an uptime pingdom on the Hetzner cloud box.
- [ ] Twitter/Bluesky thread drafted, ready to post 30 minutes after HN submission.

**If any box is unchecked Apr 28 → skip HN, go straight to application.** A broken Show HN is in the public record forever and the YC partner will find it.

**Title:** `Show HN: Yaver – Build and deploy mobile apps from your phone`
No adjectives, no emojis, no "I made a thing".

**Submission time:** 9am PT Tuesday (highest HN engagement window).

**First comment (you, within 2 minutes of posting):**

> I've been building this for [N months]. The core insight: most mobile dev loops assume you're at a MacBook with Xcode. I wanted to ship an app from my phone while on the train. So Yaver lets you prompt on your phone → AI writes the code on your Mac over P2P → you run it on your phone via Hermes bytecode push → deploy to your own dev box or our cloud.
>
> Demo: [YouTube link — the 60s product video]
> Repo: [GitHub]
> Would love feedback on: [one specific question]

**Rules for the next 6 hours:**

1. Reply to every top-level comment within 10 minutes. Every one.
2. Never argue. Acknowledge the critique, say what you'll do about it.
3. If someone says "why 30 features?" → "Scaffolding for the wedge. Next release cuts 60%." Then move on.
4. If a comment has a concrete bug → fix it live, reply with the commit hash. YC *loves* this.
5. No "thanks!" replies. Every reply should add information.

**Traction to capture for the YC application:**

- HN rank at peak
- Upvotes at 12h / 24h
- Signups in first 24h
- Projects created in first 24h
- First paying user's timestamp

## YC Application Answers — Draft Now, Polish May 3

### Describe what your company does in 50 characters or less.
> The backend that grows from your phone to cloud.

### What is your company going to make?
> Yaver is a backend-as-a-service whose first tier runs inside a mobile app. A solo developer prompts on their phone, the app scaffolds a React Native project with a SQLite-backed mini-backend that runs *on the phone itself*, and the app is usable in under a minute — no signup with a cloud vendor, no infra to provision. When the project outgrows the phone, the same portable manifest (schema, auth, seed, storage) is materialized on the developer's own Mac via our P2P agent, and from there onto our managed cloud tier — no code changes, no data migration, no vendor swap. The user picks how far up the continuum to go; Yaver is the only tier that spans all three. Open-source everywhere; revenue comes from the managed cloud tier ($10–$449/mo) and a real-device test-fleet feature for teams. We also ship one-click export to Supabase, Convex, Firebase, Postgres, Turso, and 14 other backends — positioned as escape hatches, not the product.

### Why did you pick this idea?
- Personal pain — answer specifically, not generically.
- Insight nobody else has — P2P + mobile-first + portable mini-backend.
- Why the timing is now — Hermes push works in 2025, voice-prompting is usable, RN ecosystem stable.

### What's new about what you're making?
> Two things. (1) The *first tier of the backend* runs inside the mobile app — the phone isn't a client of a backend, it *is* the backend at step zero, with real schema, auth, storage, and CRUD. No other BaaS starts on-device. (2) The backend is a continuum, not a destination — the same portable manifest scales from phone → the developer's own hardware → our managed cloud, with zero migration. Supabase, Convex, and Firebase all assume the backend is somewhere else from day one; we assume it's in your pocket and only leaves when you say so.

### Who are your competitors, and who might become competitors?
> Direct BaaS: Supabase, Firebase, Convex, Appwrite — all laptop-first, none start on the phone, none run on user hardware, none give a three-tier continuum. Adjacent: Expo / EAS (build infra, no data layer), Replit Mobile (browser-based, no real native), Cursor Mobile (chat-only, no dev loop). Long-term risk: Supabase or Convex could bolt on a phone runtime, but that cannibalizes their own cloud revenue and neither has the P2P agent or the native container for on-device RN execution. Firebase is Google, too big to pivot. Open-source + phone-first is the moat.

### How far along are you?
- Specific numbers from Apr 30 measurement day.
- "Launched on HN on Apr 29, hit #X, N signups, M projects created, P paying."
- "Ship daily; cut N features in last 2 weeks to focus."

### How long have each of you been working on this?
- Honest dates.

### Do you have revenue?
- Whatever it is on May 3, put it in. Even $5/mo from one user counts and is better than zero.

### Anything else we should know?
- The **backend continuum** (phone → user HW → Yaver cloud, one runtime) is the entire moat. Supabase/Convex/Firebase cannot copy the phone tier without cannibalizing their cloud revenue.
- The mobile-worker feature (spare phones as a real-device test fleet) is the retention hook for teams — one line, not a paragraph.
- Open-source on day one; you self-host everything for free. Managed cloud is the paid tier.
- Team-of-one ships daily; commit history is public.

## Application-Killers (Non-Negotiable)

1. **Never mention "LLM serving" or compete-with-Cursor framing.** It signals bad taste.
2. **Don't over-promise TAM.** YC partners can smell "everyone who codes" from a mile away. Your TAM is solo React Native devs — own it.
3. **Don't list 30 features.** The application asks what your company *does*, not what your repo contains.
4. **Don't apologize for being solo.** YC funds solo founders. Just be clear you ship.
5. **Submit by 5pm, not 8pm.** Form bugs happen. Buffer is non-optional.
6. **Do not frame Supabase/Convex/Firebase as promotion targets.** They are escape hatches — one-click exports for trust signaling. Lead with Yaver-native (phone → user HW → Yaver cloud); mentioning competitors as the *default* destination hands them the narrative.

## Post-Submission (May 4+)

- Don't stop shipping. YC partners re-check your GitHub after reading the application.
- Daily tweets with concrete progress — screenshots, not vibes.
- If invited to interview (mid-to-late May), prep with 2 mock interviews. YC interviews are 10 minutes and partners interrupt — practice answering the first sentence, not the whole pitch.

## Success Criteria

By May 4 at 5pm PT:

- [ ] Wedge demo runs end-to-end on a stranger's phone in under 3 minutes.
- [ ] At least 100 signups, 20 real projects, 3 paying users.
- [ ] HN launch happened (or was consciously skipped Apr 28 with written reason).
- [ ] Product video (60s) and founder video (60s) uploaded.
- [ ] Application submitted, confirmation email archived.
- [ ] Landing page is one-CTA, one-sentence pitch.
- [ ] CLAUDE.md pitch bloat unchanged (keep the repo, trim only the narrative).

The bar is not "done." The bar is "the wedge demo is undeniable, and the traction numbers prove it's not vaporware."
