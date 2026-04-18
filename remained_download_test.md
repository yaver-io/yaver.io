# Remained Download Test

Date: 2026-04-18

## What I Verified

I tested the public install/download paths on isolated Hetzner VMs:

- `yaver-install-test-20260418` — Ubuntu 24.04 x86_64
- `yaver-install-test-arm-20260418` — Ubuntu 24.04 arm64

I did not touch or remove any existing Hetzner servers.

## Passing

- `curl -fsSL https://yaver.io/install.sh | sh`
  - passed on x86_64
  - passed on arm64
- Stable tarball routes are the only routes left on the landing page
  - `/download/linux-tarball-amd64`
  - `/download/linux-tarball-arm64`
  - `/download/macos-arm64`
  - `/download/macos-x64`

## Failing Or Removed

- `npm install -g yaver-cli`
  - was failing because the npm package assumed a matching GitHub release tag and filename layout
  - fixed in local code by resolving release assets from GitHub release metadata instead of guessing filenames
  - requires publishing a new npm release to fix the public install path
- `apt`
  - landing-page command was malformed
  - the repo hosted at `apt-yaver` also has inconsistent metadata (`InRelease` vs `Packages.gz` hash/size mismatch)
  - GitHub Pages endpoint also redirects to `https://kivanccakmak.com/apt-yaver/...`, which did not resolve on the test VM
- `.deb`, `.rpm`, AppImage`
  - multiple public routes returned HTML fallback pages or installer packages instead of a clean agent install
  - removed from the landing page until the release artifacts are fixed

## Code Changes In This Branch

- Restrict landing-page recommendations to methods that passed
- Restrict `/download/[slug]` fallbacks to verified tarball routes only
- Fix `cli/src/agent-runtime.js` to resolve the latest published asset from GitHub release metadata

## Still Needed Outside This Commit

- Publish a new `yaver-cli` npm version with the bootstrap fix
- Repair the `apt-yaver` repository metadata and hosting path
- Re-publish valid x64 `.deb`, `.rpm`, AppImage`, and tarball release assets if those methods should come back
