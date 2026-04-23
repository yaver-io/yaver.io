# Unity Remained

This file is the working backlog for the Unity lane in Yaver.

It is intentionally practical: what still needs to be implemented, hardened, or verified after the current round of SDK, agent, CI, docs, and blog work.

## Current state

Already in repo:

- Unity UPM package scaffold
- auth, discovery, overlay, screenshots, logs, crash capture
- black-box scene/lifecycle capture
- feedback upload and feedback -> fix trigger
- vibing request path
- reload ladder (`content`, `custom`, `scene`, `relaunch`, `redeploy`)
- reusable content refresh handler
- optional Addressables-oriented refresh handler
- Unity agent endpoints for test/build/relaunch
- `yaver test unity`
- sample project build methods
- sample JSON config refresh flow
- package CI + sample CI
- Unity docs + blog + audits

Not done yet:

- verified real Unity Editor import/build in this environment
- polished mobile overlay UX
- richer desktop overlay UX
- structured Unity summaries surfaced back into phone/session UI
- true Addressables project example
- Remote Config adapter
- better build artifact discovery across more Unity targets
- iOS/macOS CI strategy
- OpenUPM/Asset Store publishing prep

## Highest-priority implementation work

1. Surface Unity run summaries in the user-facing session flow
   - show test/build/relaunch summaries in phone/mobile/web UI
   - show artifacts, next action, status, and stage
   - stop leaving this only in raw SDK/agent payloads

2. Strengthen Unity build output resolution
   - handle `.app`, `.exe`, `.x86_64`, APK/AAB output patterns more cleanly
   - recover output even when project-specific build methods vary
   - reduce required manual `UnityDesktopExecutablePath` setup

3. Add a real Addressables sample
   - sample project that actually uses Addressables
   - document catalog refresh flow
   - validate that the optional handler works against a concrete project

4. Add a Remote Config / JSON-hosted config adapter
   - one reusable SDK component for remote-tunable gameplay values
   - keep it lightweight for hypercasual/casual teams

5. Improve overlay UX
   - better mobile layout than raw `OnGUI`
   - better desktop panel mode
   - more useful activity/result display
   - action grouping for auth / feedback / tests / build / relaunch

## Verification work still missing

1. Real Unity version verification
   - pick one concrete Unity version and import the sample
   - fix compile/runtime issues from actual Editor feedback

2. Sample project verification
   - verify package import
   - verify test scripts compile
   - verify build methods run
   - verify content refresh sample behaves correctly

3. CI hardening
   - confirm GameCI jobs on real GitHub runners
   - inspect artifact layout from desktop + Android jobs
   - decide what should block merges vs. stay informational

4. iOS/macOS path
   - document or add self-hosted macOS runner flow
   - later: integrate with Yaver-owned runner story

## Agent-side work still needed

1. Better Unity result schema consumption in higher layers
2. Unity-specific project helpers beyond raw endpoints
3. Better relaunch orchestration for desktop players
4. Better mobile redeploy orchestration for Unity mobile builds
5. Optional smoke-run automation after build/relaunch

## SDK-side work still needed

1. Better runtime UI/overlay
2. More opinionated config helpers
3. Optional replay/video capture
4. Better crash artifact enrichment strategy
5. Cleaner extension points for project-specific reload/build hooks

## Publishing work still needed

1. OpenUPM prep checklist
2. Asset Store UPM prep checklist
3. package metadata polish
4. docs/screenshots/demo assets for distribution
5. release versioning strategy for the Unity package

## Product/docs work still needed

1. dedicated OpenUPM publishing doc
2. dedicated Asset Store publishing doc
3. Unity setup doc with sample scene wiring
4. runner patterns doc for solo vs. studio use
5. clearer “Unity is not Hermes” messaging in external surfaces

## Good next execution order

1. wire Unity summaries into session/mobile/web UI
2. verify sample project in a real Unity Editor
3. add true Addressables sample or adapter proof
4. improve overlay UX
5. document/polish OpenUPM path

## Definition of “good enough to hand to a Unity friend”

Before calling the Unity lane ready for real external use, these should be true:

- sample project imports cleanly in a real Unity version
- package CI is green
- sample CI is green
- test/build/relaunch path is demonstrated end-to-end
- content refresh path is demonstrated end-to-end
- setup docs are short and usable
- package can be shared as a UPM dependency without repo archaeology
