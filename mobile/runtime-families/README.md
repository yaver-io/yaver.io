# Runtime Families

This directory is scaffolding for the eventual single-Yaver-shell / multi-host-family split.

Current state (2026-05-02):

- Only `family-a/` is shipped. It mirrors the default Yaver host contract.
- `family-b/` was removed after build 297 crashed on TestFlight with a Hermes
  heap-corruption signature inside `JSError::setMessage` → `HiddenClass::addProperty`.
  The two families had been merged into the same compiled runtime weeks earlier
  (per `mobile/sdk-manifest.json`'s `compiledInNote`), so the routing distinction
  added no production value while keeping a non-trivial init-path surface area.
- Cross-project loading is done via the **yaver agent remote-compile + Hermes
  bundle push** path (`/dev/build-native` on the agent → host's super-host
  loads the signed bundle URL). That flow does not depend on multi-family
  routing — the host accepts any bundle whose Hermes BC version + native-module
  fingerprint match the family-a manifest.

When we want a real second compiled family in the future, restore this
directory with a second pinned native dependency graph (own `Podfile.lock`,
own pinned `package.json`) and add it back to all six `sdk-manifest.json`
copies.

The source of truth for the active shell contract is `mobile/sdk-manifest.json`.
