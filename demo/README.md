# Demo Recording — Yaver Landing Page Video

## The App: Acme Store

A realistic React Native e-commerce app with intentionally missing input validation on the login form. During the demo, the AI agent will add the validation live.

**Key file:** `acme-store/src/components/LoginForm.tsx`
- Has email and password inputs
- Has a `// TODO: add input validation` comment
- No validation at all — empty fields go straight to the API

**Other files** (for realism — visible in the terminal when agent reads codebase):
- `src/screens/LoginScreen.tsx` — wraps LoginForm
- `src/screens/HomeScreen.tsx` — product grid (what you see after login)
- `src/components/ProductCard.tsx` — product display cards
- `src/lib/auth.ts` — auth service with login/logout

## Recording Setup

### What's on screen
- **Left**: Desktop terminal, fullscreen, dark theme, ~14pt monospace font
- **Right**: iPhone (physical or mirrored), showing Yaver app

Both visible simultaneously. The spatial relationship is the point.

### Terminal setup
```bash
cd ~/Workspace/yaver.io/demo/acme-store
yaver serve
# Leave this running — terminal shows "Ready. Waiting for tasks..."
```

### Agent setup (pick one)
```bash
# Option A: Aider + Ollama (fully local, $0, the headline story)
aider --model ollama/qwen2.5-coder:32b

# Option B: Aider + Claude (if Ollama not available)
aider --model claude-3.5-sonnet
```

## The Sequence (30-45 seconds)

### Beat 1: Phone sends task (5s)
1. Open Yaver app on phone
2. Type: `add input validation to the login form, email format and empty field checks`
3. Tap send

### Beat 2: Agent works (15-20s)
1. Terminal shows agent reading `src/components/LoginForm.tsx`
2. Agent starts writing code — actual validation logic appearing
3. Phone shows streaming output character by character

### Beat 3: Diff + Approve (5s)
1. Agent shows the diff in terminal
2. Phone shows the diff too
3. Tap Approve on phone

### Beat 4: Live result (5s)
1. File saved on desktop
2. (If feedback SDK demo) Switch to running app on phone — validation is live
3. Try submitting empty form — error messages appear

## The Money Frame

The split-screen moment where both phone and terminal show output updating simultaneously. This is the screenshot for HN/Reddit/Twitter.

## Recording Settings

- **Resolution**: 1920x1080 or higher
- **FPS**: 60fps
- **Terminal font**: 14pt, dark background (use existing terminal theme)
- **No cleanup**: Leave messy terminal output — authenticity beats polish
- **No voiceover, no music, no transitions**

## Post-Production

### Desktop recording
- macOS: QuickTime screen recording or OBS
- Crop to terminal window only

### iPhone recording
- Screen mirroring via QuickTime (Lightning/USB-C cable)
- Or: screen record on phone, AirDrop to Mac

### Merge
```bash
# Side-by-side merge with ffmpeg
ffmpeg -i desktop.mp4 -i phone.mp4 \
  -filter_complex "[0:v]scale=1280:720[left];[1:v]scale=640:720[right];[left][right]hstack" \
  -c:v libx264 -crf 23 -preset medium \
  demo.mp4

# Create looping GIF for Twitter/X (under 5MB)
ffmpeg -i demo.mp4 -vf "fps=15,scale=960:-1:flags=lanczos" -c:v gif \
  -loop 0 demo.gif
```

### For landing page
- MP4: autoplay, muted, loop
- Place in `web/public/demo.mp4`
- Update the demo section in `web/app/page.tsx` to use `<video>` instead of placeholder

## What the Agent Should Produce

The validation it adds to `LoginForm.tsx` should look like this (approximately):

```typescript
const handleSubmit = async () => {
  setError(null);

  // Validate email
  if (!email.trim()) {
    setError('Email is required');
    return;
  }
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    setError('Please enter a valid email address');
    return;
  }

  // Validate password
  if (!password) {
    setError('Password is required');
    return;
  }
  if (password.length < 6) {
    setError('Password must be at least 6 characters');
    return;
  }

  setLoading(true);
  // ... rest of handleSubmit
};
```

The agent writes this live. Don't pre-type it.
