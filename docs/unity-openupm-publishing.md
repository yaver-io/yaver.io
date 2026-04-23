# Unity OpenUPM Publishing Prep

This is the practical checklist for making `io.yaver.feedback.unity` publishable through OpenUPM.

## Why OpenUPM first

OpenUPM is the cleanest public distribution path before Unity Asset Store UPM.

It works well when:

- the package already lives in Git
- the package is a normal UPM package
- you want easy install commands for external Unity developers
- you do not want to wait for Asset Store review before sharing the SDK

## Current state in this repo

Already done:

- package name uses `io.yaver.feedback.unity`
- `package.json` has name, version, description, keywords, docs, changelog
- `README.md` exists
- `CHANGELOG.md` exists
- `Documentation~/index.md` exists
- third-party notices file exists
- package validator script exists:
  - `node scripts/validate-unity-package.mjs`

## Still needed before OpenUPM

1. Stable public repo path
   - decide whether the package remains in this monorepo
   - or move/split to a dedicated Unity package repo

2. Clean public documentation URLs
   - point `documentationUrl` and `changelogUrl` to stable public URLs
   - avoid temporary or private repo paths

3. Versioning discipline
   - bump `sdk/feedback/unity/package.json` on each publishable change
   - keep changelog in sync

4. Public install instructions
   - add the final OpenUPM install command once the package is listed

5. Verified Unity version support statement
   - pick a tested Unity baseline
   - document what is actually verified

## Suggested OpenUPM release flow

1. Run package validator

```bash
node scripts/validate-unity-package.mjs
```

2. Run Unity package CI

- package EditMode tests
- sample EditMode tests
- sample PlayMode tests
- sample desktop/mobile builds

3. Bump package version

- `sdk/feedback/unity/package.json`
- `sdk/feedback/unity/CHANGELOG.md`

4. Tag a repo release

5. Publish or sync the package to OpenUPM

## Important reminder

The Unity package should remain separate from:

- Yaver CLI binaries
- agent executables
- platform build artifacts

The Unity package is the SDK/plugin.
The CLI/agent stays installed separately through `npm install -g yaver-cli`.

## Asset Store later

OpenUPM does not replace the Asset Store path.

Recommended sequence:

1. local/private UPM
2. OpenUPM
3. Asset Store UPM

That keeps the package easy to iterate on before the heavier marketplace path.
