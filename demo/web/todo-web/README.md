# Todo Web — Yaver Feedback web SDK showcase

Minimal Next.js 14 (App Router) Todo app used to demo the Yaver
Feedback web SDK (`yaver-feedback-web`). The mobile counterpart is
`demo/mobile/todo-rn`.

## Quick start

```bash
cd demo/web/todo-web
npm install
npm run dev
# open http://localhost:3000
```

The Yaver `Y` floating button appears in the corner. First click prompts
for sign-in (popup OAuth: Apple / Google / GitHub / GitLab / Microsoft, or
email + password), then a device picker, then opens the feedback flow
streaming back to your Yaver agent.

## Files

- `app/layout.tsx` — root layout, mounts the SDK boot component.
- `app/feedback-boot.tsx` — client-only `YaverFeedback.init()`.
- `app/page.tsx` — single-page Todo screen.
- `app/globals.css` — dark palette matching `demo/mobile/todo-rn`.

## Why no auth, no backend?

The showcase is about the feedback flow, not Todo correctness. State
lives in `localStorage` so demos persist across hot reloads.
