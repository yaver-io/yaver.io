# Yaver Developer Platform

This is the baseline third-party developer use case. It applies to SFMG first,
then to external developers.

## Core Flow

```text
download Yaver mobile app
-> create or import a sandbox
-> choose source home: Yaver Git | GitHub | GitLab | self-hosted Git | local repo
-> develop on phone, own machine, or Yaver Cloud
-> deploy/private preview
-> optional Yaver catalog review
-> optional Yaver catalog publish
```

Development, private deploy, and catalog publication are separate states.
Publishing inside Yaver is never required just because a developer used Yaver
to build or deploy.

## Source Ownership

Yaver must support all of these source homes:

- Yaver Git
- GitHub
- GitLab
- self-hosted Git
- local folder / exported package
- signed package artifact

The developer can leave Yaver with the project intact. Yaver should not make the
repository, build artifacts, or private deploy path hostage to catalog
publication.

## SFMG Template

SFMG is the first concrete template:

- owners: Kivanc and Serhat
- source: closed-source SFMG repo, not copied into `yaver.io`
- development: allowed through Yaver app, Yaver MCP, Yaver Cloud, self-hosted
  runner, or local checkout
- cloud: optional dynamic Hetzner allocation, scale-to-zero required
- runners: Claude, Codex, OpenCode, GLM, or custom tmux runner
- OpenCode/GLM: credentials live on the local device or target machine; Yaver
  can sell managed inference/credits, but must not require it
- catalog publication: optional reviewed release with Yaver OAuth and Yaver
  billing

Kivanc and Serhat should be able to open Yaver mobile, choose SFMG, allocate a
temporary managed box if needed, clone/check out the SFMG repo from the selected
source provider, configure OpenCode/GLM on that target, run the app, deploy a
private preview, and tear the box down when idle.

## Deploy vs Publish

Deploy means a private or developer-controlled runtime:

- phone sandbox
- browser preview
- native preview
- self-hosted Yaver
- developer VPS
- Yaver Cloud / Hetzner managed machine

Publish means official Yaver catalog distribution:

- Yaver OAuth is required
- Yaver billing owns mobile/TV/web entitlements
- manifest scanner must pass
- Yaver gets private source or reproducible package review access
- revenue share applies only where the catalog contract says it applies

## Cloud Allocation

Yaver Cloud is optional managed compute for developers who do not want to use
their own machine.

Rules:

- Hetzner is metered. Never design this as always-on monthly infrastructure.
- Every managed dev box must be scale-to-zero: snapshot, delete, recreate from
  snapshot on demand.
- Runner credentials and API keys are configured on the target machine or kept
  in the user's local Yaver vault. Raw secrets must not be stored in Convex.
- Source provider credentials are pushed only through owner-approved onboarding
  paths, never embedded in catalog metadata.

## Product Boundary

Yaver monetizes:

- managed cloud / dynamic workers
- inference credits
- OpenCode/GLM setup convenience
- remote runner hosting
- private deploy and preview infrastructure
- feedback, testing, release automation, MCP hosting
- optional catalog distribution

Yaver does not monetize by trapping source code. A developer can use Yaver, ship
outside Yaver, and later remove the project from Yaver.

## Yaver-Native OAuth Layer

Yaver OAuth is the platform identity layer for Yaver-native builds. It sits
below an app's own standalone auth providers:

```text
Yaver build:
  Yaver OAuth -> Yaver bootstrap -> app backend link -> local player/user

Standalone external build:
  developer auth provider(s) -> app backend -> local player/user
```

This means a game like SFMG or Carrotbet can keep Google, Apple, email, or any
other developer-owned auth outside Yaver, while its Yaver catalog build uses
Yaver OAuth as the account of record.

The shared web contract lives in `web/lib/yaver-native-auth.ts` and defines:

- `YAVER_NATIVE_AUTH_PROVIDER`
- app and game required scopes
- bootstrap validation helpers
- bearer header helpers
- adapter guidance text used by MCP

New Yaver-native apps should use that contract instead of hand-rolling scopes
or provider names. Coding agents can call the hosted MCP tool
`yaver_native_oauth_guide` while wiring a new app.
