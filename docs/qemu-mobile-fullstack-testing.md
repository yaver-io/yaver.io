# QEMU For Phone-First Fullstack Testing

Status: initial design + working first harness implementation

This document defines how QEMU should be used for Yaver's phone-first fullstack development story:

- a project can begin on mobile
- the project can carry local backend state and app intent
- the project can continue on another machine
- AI can keep developing it there
- the result can be compiled, tested, and sent back toward mobile or another target

This is about **testing and continuation of development**, not claiming that raw QEMU is the end-user mobile runtime.

## Current Working Result

As of this implementation, the following path works on an Apple Silicon host using raw QEMU and an Ubuntu ARM64 guest:

1. boot local ARM64 guest with `scripts/qemu-local-arm64-vm.sh`
2. scaffold a mobile/backend monorepo in the guest with `yaver new --quick`
3. install managed Node inside the guest with `yaver install node`
4. mutate the generated mobile app inside the guest
5. run `npm install`
6. export the Expo Android bundle inside the guest

That flow is currently automated by:

- `scripts/qemu-dummy-mobile-cycle.sh`

Important current ARM64 note:

- Expo Android export works in the guest with `--no-bytecode`
- Hermes bytecode generation does **not** work on this ARM64 guest yet because the React Native package currently exposes `node_modules/react-native/sdks/hermesc/linux64-bin/hermesc`, which is x86_64-only in this tested path

So the current reliable mobile build signal in ARM64 QEMU is:

- JS bundle/export success

not:

- Hermes bytecode success

## Short Answer

Yes, this is possible, with an important boundary:

- **QEMU is a strong test/build/isolation target for the "other machine" part of the workflow**
- **QEMU is not the primary replacement for real mobile runtime validation**

That means the correct use is:

1. the developer starts from mobile
2. Yaver exports or re-materializes the project onto another machine
3. a QEMU guest acts as that clean other machine
4. AI continues coding there
5. the guest builds/tests/runs the project
6. the resulting artifact or source tree can be promoted again

For the product claim we care about, this is exactly the useful test:

- "can a phone-started project survive outside the phone?"
- "can AI continue fullstack development on another machine?"
- "can the guest compile and run the resulting mobile/web/backend stack?"

## What QEMU Is Good For Here

QEMU is useful for:

- reproducible clean-machine testing
- safe destructive testing
- simulating "export to my other machine"
- validating that phone-originated artifacts are portable
- giving AI an isolated Linux box to mutate, build, and break
- validating remote-dev-machine workflows without using the developer's real workstation
- validating the host-side parts of mobile development:
  - Node
  - npm
  - git
  - Yaver agent
  - backend services
  - web builds
  - React Native / Expo workspace operations
  - Android/Gradle/Hermes build steps, when the guest has the toolchain

## What QEMU Is Not Good For

QEMU is not the primary answer for:

- real iOS execution
- iPhone UX validation
- phone sensors, share sheets, native auth callbacks, and OS-level mobile behavior
- final Android UX validation compared with the Android Emulator or a real device

So QEMU must be treated as one lane in the matrix:

- **Phone sandbox lane**: local SQLite/backend, phone-first creation, early CRUD loop
- **QEMU lane**: exported continuation, AI coding, clean-machine build/test
- **Device/emulator lane**: actual runtime validation

## What Exists In This Repo Already

The current codebase already gives us useful primitives:

- phone backend export/import:
  - `desktop/agent/phone_backend.go`
  - `desktop/agent/phone_backend_http.go`
  - `desktop/agent/phone_cmd.go`
- agent-side tests for export/import/receive:
  - `desktop/agent/phone_export_test.go`
  - `desktop/agent/phone_backend_test.go`
- mobile phone-project flows:
  - `mobile/src/lib/phoneProjects.ts`
  - `mobile/src/lib/phoneSandboxLocal.ts`
  - `mobile/app/phone-projects.tsx`
  - `mobile/app/phone-project/[slug].tsx`
- fullstack scaffold generator:
  - `desktop/agent/project_wizard.go`
  - `desktop/agent/project_wizard_cmd.go`
  - `yaver new --quick`
- build surfaces:
  - `yaver build ...`
  - `yaver autodev ...`

This means we do **not** need a brand-new product abstraction to start. We can build a QEMU harness around the current contracts.

## Two Execution Modes We Need To Support

The user requirement has two distinct modes.

### Mode A: QEMU As Remote Dev Machine

In this mode:

- the phone is the origin
- the QEMU guest acts like "my other dev machine"
- Yaver or the developer on the host can continue coding against that guest
- the guest compiles and runs the exported or scaffolded project

This validates:

- portability
- clean-machine bootstrap
- remote continuation of development
- host-side build behavior

Typical flow:

1. user creates the project on mobile
2. user exports the portable unit
3. host sends the portable unit into the QEMU guest
4. guest imports or scaffolds the project
5. guest runs build/test commands
6. guest becomes the continued development target

### Mode B: QEMU With User-Provided OpenAI Key

In this mode:

- the user provides an OpenAI key
- AI coding is triggered for the guest-side project
- the guest runs `yaver autodev` or another AI-driven loop
- after edits, the guest compiles/tests the project

This validates:

- AI continuation from a phone-started or wizard-generated project
- a guest-side coding loop that is isolated from the host machine
- compile-after-edit behavior
- the repo's ability to support "mobile starts it, AI continues elsewhere"

Important boundary:

- the **control plane** may begin on mobile
- the **heavy compile/build loop** should normally happen on the guest or another host
- this is still consistent with the product story

## Source Shapes We Need To Test

There are currently two useful source shapes.

### 1. Phone Export Bundle

This is the current portable phone-backend artifact:

- schema
- auth personas
- seed data
- optional SQLite rows
- generated SQL
- optional containerization scaffold

This path is real today. It is mostly backend-oriented, but it is already portable and testable.

### 2. Fullstack Wizard Scaffold

This is the current fullstack monorepo seed:

- web
- mobile
- backend
- shared layout
- project metadata

This is produced by:

```bash
yaver new --quick answers.json <parent-dir>
```

This path is the closest current proxy for "mobile-first fullstack monorepo continues on another machine."

## Current Architectural Gap

There is still an important product gap between the two source shapes:

- phone export today is primarily a phone-backend portability contract
- full monorepo generation today is a wizard/scaffold contract

The final product direction wants these to converge:

- start from phone
- grow into a real monorepo
- continue elsewhere
- preserve portability

For now, the QEMU test strategy should explicitly validate both:

- **backend continuity**
- **fullstack monorepo continuity**

That is still valuable and honest.

## Recommended QEMU Topology

Use a Linux guest as the clean-machine target.

Recommended baseline:

- Ubuntu 24.04 or Debian 12 guest
- 4 vCPU minimum
- 8 GB RAM minimum
- 40 GB disk minimum
- OpenSSH server enabled
- outbound internet available

For stronger mobile build coverage:

- 8 vCPU
- 12-16 GB RAM
- larger disk
- Android SDK preinstalled if Android builds are part of the loop

## What The Guest Should Own

The guest should be treated as a disposable development machine.

It should own:

- the imported or scaffolded project directory
- a Yaver binary
- git checkout state
- build/test caches
- AI runner credentials for the guest-side loop when needed

It should not be treated as:

- the real phone
- the final deployment target by default

## Compile/Run Expectations

The guest can reasonably own these compile/run tasks:

- backend validation
- web install/build/test
- monorepo install
- React Native / Expo workspace checks
- Android build steps if the Android toolchain is installed
- Hermes-oriented Android host build steps where applicable

The guest should not be the only validator for:

- iPhone execution
- native iOS signing/runtime behavior

## Test Matrix

Minimum useful matrix:

### Lane 1: Phone Sandbox

Validate:

- create project
- mutate local SQLite data
- inspect schema and rows
- prepare export

### Lane 2: QEMU Guest

Validate:

- clean-machine receipt
- import or scaffold success
- repo/bootstrap success
- AI continuation success
- build/test success

### Lane 3: Device/Emulator

Validate:

- resulting mobile app behavior
- real runtime reload/install path

## Pass Criteria

The QEMU lane is a pass if:

1. a phone-originated or wizard-originated project can be materialized in the guest
2. AI or remote-dev continuation can edit it there
3. at least one meaningful build/test command succeeds
4. the resulting workspace remains exportable or reusable

The stronger pass criteria are:

1. import/scaffold succeeds on a clean guest
2. AI edits source files
3. build passes after AI edits
4. backend or web runtime starts
5. mobile-target build command passes where toolchains exist

## Risks And Constraints

### 1. Phone Export Is Not Yet The Same As Full Monorepo Export

This is the most important product gap.

Current implication:

- test backend portability with `yaver phone export`
- test fullstack continuation with `yaver new --quick`
- do not pretend the two are already fully unified

### 2. Guest Toolchains Matter

If the guest lacks:

- Node
- npm
- Android SDK
- Java
- Gradle
- codex/Claude/aider

then those lanes should fail clearly, not silently degrade.

### 3. AI Runner Availability Matters

OpenAI-key-driven mode is only as real as the guest's installed runner path.

For the first implementation:

- use a guest-side `yaver autodev --engine codex` path when available
- allow override of the AI command

### 4. QEMU Is Not The Device

The guest validates portability and continuation, not final mobile UX correctness.

## Implementation Plan

### Phase 1: Harness And Contracts

Add:

- host-side orchestration script
- guest-side bootstrap script
- explicit support for:
  - `remote-dev`
  - `openai-key`
- support for:
  - `phone-export`
  - `wizard-quick`

This is the first implementation in this repo.

### Phase 2: Build Profiles

Add standardized guest build profiles:

- backend-smoke
- web-smoke
- monorepo-install
- rn-android-build

### Phase 3: Round-Trip Export

Add:

- export from guest after AI edits
- receipt on another machine or host
- diff and runtime verification

### Phase 4: Full Phone-Originated Monorepo Continuity

Unify:

- phone-first project creation
- monorepo scaffold continuation
- exported fullstack project contract

## Initial Repo Support Added With This Document

The initial implementation should live alongside this file:

- `scripts/qemu-phone-fullstack.sh`
- `scripts/qemu-guest-bootstrap.sh`

The intended behavior:

- build a local Yaver binary
- send it to the guest
- either:
  - export a phone project and import it in the guest
  - or run `yaver new --quick` in the guest from a checked-in answers JSON
- optionally run AI continuation in the guest
- run a caller-supplied build command

## How To Think About The Two Modes

### Remote-Dev Mode

This mode asks:

"If the project leaves the phone and lands on a clean other machine, can work continue there?"

### OpenAI-Key Mode

This mode asks:

"If the user gives Yaver an OpenAI key, can the isolated guest continue the project with AI and still compile afterward?"

Both are legitimate product claims. Both should be tested.

## Recommended Next Product Steps

After the first harness exists, the next high-value work is:

1. define a single fullstack export contract, not separate backend-only and wizard-only continuations
2. remove destructive sync behavior from mobile export helpers
3. eliminate row truncation during local-to-agent staging
4. add one-click guest build profiles instead of caller-supplied shell commands
5. add round-trip "phone -> QEMU -> another machine/cloud" validation

## Bottom Line

This is possible.

The correct statement is:

- **QEMU is a valid clean-machine continuation and AI-development target for Yaver's phone-first fullstack workflow**
- **QEMU is not the only runtime validation surface**

That is enough to make it a strong and worthwhile part of the testing architecture.
