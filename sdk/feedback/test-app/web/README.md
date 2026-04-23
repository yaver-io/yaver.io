# Web Feedback SDK Dummy React Harness

This package is a small React host app used to exercise the web Feedback SDK in a more realistic way than import-only smoke tests.

It covers a simple dogfood scenario:

- Render a dummy web UI.
- Change the UI tone from steady to vibing through an Ollama Qwen-style action.
- Send feedback through `YaverFeedback.upload(...)`.
- Verify the uploaded metadata includes transcript, project, and candidate-lane fields.

Run locally:

```bash
npm install
npm run test:ci
```
