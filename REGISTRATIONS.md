# Registrations checklist — things I can't do for you

Last updated 2026-05-27 by Claude. Each item below requires your accounts /
credit card / consent. I've prepped everything else; this is the residual
"you must do this yourself" list to ship the voice + glasses + spatial work.

Estimated total time: **~15 minutes** if you go in order.

## ⏱ Tier 1 — Required for voice loop today (5 min)

These two API keys unlock the entire voice surface (mobile orb + /spatial + MentraOS).

### 1.1 Deepgram (STT)

- [ ] Go to https://console.deepgram.com/signup
- [ ] Sign in with Google or GitHub (no credit card required for the $200 free credit)
- [ ] Console → **API Keys** → "Create a New API Key"
  - Name: `yaver-prod`
  - Scope: `Member` (default)
  - Project: default
- [ ] Copy the secret (starts with `da-...`)

**Paste into `~/.yaver/config.json`** under `voice.deepgram_api_key`.

Or run:
```bash
yaver vault add DEEPGRAM_API_KEY --project agent --value "da-XXX"
```

### 1.2 Cartesia (TTS)

- [ ] Go to https://play.cartesia.ai/keys
- [ ] Sign up with Google (free tier covers ~10k chars/day, enough for solo dogfooding)
- [ ] Create new API key
- [ ] Copy the secret

**Paste into `~/.yaver/config.json`** under `voice.cartesia_api_key`.

### 1.3 Run config setup

After both keys are in hand:

```bash
yaver voice setup        # prints the full config block to copy
# Paste into ~/.yaver/config.json
yaver serve              # restart agent
yaver voice status       # should show deepgram + cartesia ready
```

Voice on phone + /spatial works the moment both are live. **No other steps needed for Tier 1.**

---

## ⏱ Tier 2 — Required for glasses (5 min)

Only do this once you've borrowed the Android smart glasses your friend mentioned.

### 2.1 MentraOS Developer Console

- [ ] Go to https://console.mentra.glass
- [ ] Sign in (Google OAuth)
- [ ] Create new app:
  - Package name: `io.yaver.agent`
  - Display name: `Yaver`
  - Description: `Hands-free Claude Code on smart glasses`
  - Permissions: **microphone** (transcription), **display** (showTextWall)
- [ ] Copy the API key shown

**Paste into `mentra-miniapp/.env`** (created from `.env.example`):

```bash
cd /Users/kivanccakmak/Workspace/yaver.io/mentra-miniapp
cp .env.example .env
# Edit .env: fill in MENTRAOS_API_KEY=<your key>
# Also fill in YAVER_SDK_TOKEN — generate via:
yaver sdk token --scope feedback,voice
# Paste the printed token into .env as YAVER_SDK_TOKEN

bun install                # already done by Claude
bun run dev                # miniapp runs on :8080
```

### 2.2 Pair the borrowed glasses to your phone

- [ ] Install the **MentraOS** app on the Android phone you'll use with the glasses
- [ ] Pair glasses → MentraOS handles the BLE handshake
- [ ] In MentraOS app → MiniApps → install Yaver (will appear once registered above)

That's it for glasses. The miniapp does the rest end-to-end via voice.

---

## ⏱ Tier 3 — Optional / deferred (5 min)

### 3.1 WebXR Emulator (zero cost, lets you test the VR scene without Quest)

- [ ] Install in Chrome: https://chromewebstore.google.com/detail/immersive-web-emulator/cgffilbpcibhmcfbgggfhfolhkfbhmik
- [ ] Open https://yaver.io/spatial?agent=...&token=... (after web deploy)
- [ ] DevTools → WebXR tab → switch to "Meta Quest 3"
- [ ] Click "Enter VR" — full simulated headset, free

### 3.2 Meta Ray-Ban Display Wearables Toolkit (waitlist, slow)

- [ ] Apply at https://developers.meta.com/wearables (NOT the regular Meta dev portal)
- [ ] Approval is gated; expect 2-6 week wait
- [ ] Until you have access, this is **deferred** in task #10 — no impact on shipping

### 3.3 Apple visionOS distribution

- [ ] Already have an Apple Developer account (you've shipped TestFlight); same one works for visionOS
- [ ] Xcode 26 → File → New → Project → visionOS → App
  - Don't actually need to create a new project — task #8 handles this as an Expo visionOS target on the existing iOS bundle
- [ ] App Store Connect → existing Yaver app → add visionOS to Platforms
- [ ] No new registration. Just a target add.

---

## What you do NOT need to do

- **Quest Store submission** — for now, share the URL `/spatial?surface=quest` directly via Quest Browser bookmark. Listing in the Horizon Store can wait until post-launch.
- **OpenAI / Anthropic API keys** — per memory (`feedback_no_api_keys_subscription_only`), Yaver ALWAYS uses your local Max Pro / ChatGPT Plus subscription OAuth tokens, never API keys. No registration needed here; you already have those subscriptions.
- **GitHub / GitLab OAuth for Yaver** — already configured per CLAUDE.md secrets list.
- **Cloudflare API token** — already configured (`CLOUDFLARE_API_TOKEN` GH secret).
- **TestFlight / Play Store credentials** — already configured per CLAUDE.md "Secrets" section.

---

## Verification step

After Tier 1 + (optionally) Tier 2:

```bash
# Tier 1 — voice on phone
yaver serve
cd /Users/kivanccakmak/Workspace/yaver.io
yaver wireless push                       # to your iPhone
# In the running mobile app: Home tab → tap mic orb → say "list my tasks"

# Tier 2 — voice on glasses
cd mentra-miniapp && bun run dev          # miniapp on :8080
# On glasses: say "launch sfmg" → check that the agent receives the open_app command

# Tier 3 — VR
./scripts/deploy-web.sh                    # ship /spatial to yaver.io
# In Chrome with WebXR Emulator extension: navigate to /spatial → Enter VR
```

If any of these fail, the diagnostics live at:

```bash
yaver doctor                               # general agent health
yaver voice status                         # voice provider readiness
curl http://localhost:18080/voice/status   # raw HTTP probe
```

---

## Why a checklist instead of "Claude does it for me"

OAuth flows for Google / Meta / Apple / Anthropic / OpenAI legitimately
require you to be present:
- Captcha / 2FA
- Reading + agreeing to terms of service
- Entering credit card details (sometimes)
- Choosing privacy / scope grants

I can't fake those interactions in a way that wouldn't violate the
providers' terms (and your trust). Pre-prepping URLs + exact paste-back
fields is the fastest path I can offer — most of these are 30 seconds
each. The whole list above is ~15 minutes if you don't context-switch.
