# Runtime Families

This directory is the beginning of the single-Yaver-shell / multi-host-family split.

Current state:

- `family-a/` mirrors the default Yaver host contract.
- `family-b/` is a pilot slot pinned to the current `sfmg` runtime contract so the
  runtime-family selector can be exercised end to end on the Hetzner ephemeral path.

Important constraint:

- Today both family manifests still point at the same compiled native dependency graph.
- The selector and shell/family plumbing are real.
- A truly distinct Family B native runtime still requires a second pinned mobile host
  dependency graph and build pipeline.

The source of truth for the active shell contract is still `mobile/sdk-manifest.json`.
The family manifests here are repo scaffolding for the eventual shell-plus-families split.
