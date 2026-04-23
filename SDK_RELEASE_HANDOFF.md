# SDK_RELEASE_HANDOFF

## What Was Released

Published successfully:

- `yaver-cli` to npm at `1.99.2`
- `yaver-sdk` to npm at `0.2.2`
- `yaver-feedback-react-native` to npm at `0.5.3`
- `yaver-feedback-web` to npm at `0.1.2`
- `yaver` to PyPI at `0.2.2`

Release infrastructure changes landed:

- added `yaver sdk add ...` and `yaver feedback setup` command surface
- updated docs to treat `npm install -g yaver-cli` as the umbrella install point
- patched `.github/workflows/release-sdk.yml` to:
  - support `workflow_dispatch`
  - support selective publish inputs
  - publish the core Flutter package at `sdk/flutter`
  - restore pub.dev credentials from `PUB_CREDENTIALS_JSON`

## What Was Not Released

Not published:

- `sdk/flutter` package `yaver` to pub.dev (`0.2.1`)
- `sdk/feedback/flutter` package `yaver_feedback` to pub.dev (`0.1.2`)

Reason:

- GitHub Actions secret `PUB_CREDENTIALS_JSON` was not set
- both Flutter jobs failed before publish during credentials restore

## Exact Failure

Workflow:

- SDK publish run: `24631393474`

Failed jobs:

- `publish-feedback-flutter`
- `publish-flutter-core`

Failure point from logs:

```text
PUB_CREDENTIALS_JSON secret is not set
Process completed with exit code 1.
```

So this was not a bad credentials-file format error. The secret was simply absent for the workflow run.

## GitHub Runs

Relevant runs:

- CLI release run: `24631390084`
- SDK publish run: `24631393474`

CLI release run status when checked:

- npm publish succeeded
- MCP registry publish succeeded
- remaining packaging jobs were still running, but the important umbrella install publish had already landed

## Versions Bumped

Changed for release:

- `cli/package.json` → `1.99.2`
- `versions.json` CLI → `1.99.2`
- `sdk/js/package.json` → `0.2.2`
- `sdk/python/pyproject.toml` → `0.2.2`
- `sdk/flutter/pubspec.yaml` → `0.2.1`
- `sdk/feedback/react-native/package.json` → `0.5.3`
- `sdk/feedback/web/package.json` → `0.1.2`
- `sdk/feedback/flutter/pubspec.yaml` → `0.1.2`

## Metadata / Positioning Changes

Adjusted package metadata and README copy to position Yaver more broadly than a narrow "remote coding tool":

- "local-first agent runtime"
- "developer workflows"
- reduced overemphasis on just remote coding / vibe coding in package descriptions

## Important Package Naming Fix

Found and corrected a mismatch:

- actual published web feedback package is `yaver-feedback-web`
- some docs previously referenced `@yaver/feedback-web`

Docs and package-facing references were updated toward `yaver-feedback-web`.

## Commit and Tag

Release prep commit:

- `a7698de3` — `Release umbrella install and SDK packages`

Tag pushed:

- `cli/v1.99.2`

## What Claude Code Should Do Next

### 1. Add pub.dev credentials secret

GitHub secret required:

- `PUB_CREDENTIALS_JSON`

Value:

- full contents of local `~/.pub-cache/credentials.json`

### 2. Re-run only Flutter jobs

Use the `release-sdk.yml` workflow via `workflow_dispatch` and set:

- `publish_flutter_core=true`
- `publish_feedback_flutter=true`
- all other publish inputs `false`

That avoids duplicate-version failures on npm/PyPI packages already published.

### 3. Verify pub.dev live versions after rerun

Expected targets:

- `yaver` → `0.2.1`
- `yaver_feedback` → `0.1.2`

## How I Tried

1. Checked live registry versions first so I would not blindly trigger duplicate publishes.
2. Found npm/PyPI were already at prior current versions, so I bumped versions before release.
3. Added the new umbrella CLI/install changes and SDK injection commands.
4. Committed only release-relevant files, leaving unrelated dirty worktree changes alone.
5. Pushed `main`.
6. Tagged and pushed `cli/v1.99.2` to trigger CLI release.
7. Manually dispatched `release-sdk.yml`.
8. Watched both runs with `gh run watch`.
9. Pulled failed Flutter job logs with `gh run view ... --log`.
10. Confirmed failure was missing `PUB_CREDENTIALS_JSON`, not malformed credentials.

## Commands I Used

Representative commands used during the release:

```bash
git push origin main
git tag cli/v1.99.2 a7698de3
git push origin cli/v1.99.2

gh workflow run release-sdk.yml --ref main
gh run watch 24631393474 --exit-status
gh run watch 24631390084 --exit-status
gh run view 24631393474 --job 72019302866 --log

npm view yaver-cli version
npm view yaver-sdk version
npm view yaver-feedback-react-native version
npm view yaver-feedback-web version
python3 -m pip index versions yaver
```

## Bottom Line

Yes, deployment/publishing partially happened.

Succeeded:

- npm umbrella CLI
- npm SDK packages
- PyPI package

Blocked:

- both pub.dev publishes due to missing `PUB_CREDENTIALS_JSON`
