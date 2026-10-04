# Yaver Gateway Tombstone

## Status

This Worker is intentionally body-blind and returns `410 Gone` for the legacy
hosted inference API. Yaver-operated infrastructure must not receive prompts,
model responses, or provider credentials. AI calls run directly from a trusted
endpoint or behind an E2EE-authorized user-owned agent.

## Quick start

```bash
cd gateway
npm install
npm run typecheck

npm run deploy
```
