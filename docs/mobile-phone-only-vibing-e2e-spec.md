# Mobile-Only Vibing + Project Creation E2E Spec

Status: proposed implementation plan

## Scope

This spec covers the narrow product claim:

- the user starts from the Yaver mobile app
- the user creates a project from mobile
- the user uses vibing from mobile
- the project's local backend/runtime is phone-local
- a third-party app also runs on the same phone

This spec does **not** cover:

- cloud export/deploy
- dev-hardware promotion
- cross-machine continuation
- App Store / Play Store release validation

## Product Claim We Need To Prove

For this lane, the strongest honest claim is:

> A user can create and evolve a phone-first app from the mobile app, keep backend state locally on the phone, and run the corresponding app experience on that same phone.

That breaks into two distinct contracts:

1. `mobile control plane`
   The Yaver mobile flows work: wizard, phone-project creation, vibing, local backend actions.
2. `mobile host runtime`
   The generated or hosted third-party app actually runs inside the real mobile host/runtime path on the phone.

`mobile-headless` covers the first contract well. It does not prove the second by itself.

## Existing Repo Seams

### Mobile control plane

- `mobile-headless/src/mobile-client.ts`
- `mobile-headless/src/bin/cli.ts`
- `mobile-headless/test/hermetic.test.ts`
- `mobile-headless/test/smoke-local.test.ts`
- `mobile/src/lib/phoneProjects.ts`
- `mobile/src/lib/quic.ts`

### Agent-side phone backend/runtime

- `desktop/agent/phone_backend.go`
- `desktop/agent/phone_backend_http.go`
- `desktop/agent/phone_backend_test.go`
- `desktop/agent/phone_export_test.go`
- `desktop/agent/vibing.go`

### Existing end-to-end pattern we should imitate

- `desktop/agent/bento_e2e_test.go`

That test already proves the repo prefers HTTP-driven dogfood tests over invented abstractions. The phone-only spec should extend that style.

## Test Strategy

Use three lanes.

### Lane 1: Pure contract tests

Goal:
- prove the phone-project and vibing contracts are stable and unit-testable

Where:
- Go unit tests
- headless hermetic tests

What it proves:
- stable request/response shape
- deterministic export/import/runtime behavior
- no dependency on real mobile UI

### Lane 2: Headless mobile integration

Goal:
- drive the same mobile app flows without a real phone UI

Where:
- `mobile-headless`

What it proves:
- mobile wizard and project creation work against the real agent
- vibing endpoints are callable from the mobile control path
- local phone-project CRUD and export work through the mobile facade

### Lane 3: Real mobile host/runtime smoke

Goal:
- prove the actual "third-party app also runs on the same phone" claim

Where:
- real iPhone / Android device
- optionally simulator/emulator for preflight, but not as the final proof

What it proves:
- the bundle/app really loads in the mobile host path
- local backend writes survive reload and reopen
- the phone-only story is real, not just API-deep

## Canonical Todo Scenario

Use one canonical scenario everywhere:

`todo-mobile-only`

Flow:

1. mobile starts a new project
2. choose `todos` template or prompt for a todo app
3. verify schema contains `users` and `todos`
4. verify seeded rows exist
5. trigger one vibing action
6. apply a small change that is observable in the app or runtime
7. run the app on the same phone
8. create a todo item from the app
9. reload the app
10. confirm the todo still exists
11. reopen the app
12. confirm the todo still exists

This is the minimum end-to-end story for the phone-only claim.

## Concrete Test Matrix

### A. Go unit tests

Owner:
- `desktop/agent`

Required tests:

1. `CreatePhoneProject(todos)` returns expected schema and seed stats
2. `ExportPhoneProject` includes expected manifest/schema files
3. `ImportPhoneProject` round-trips seeded and runtime rows
4. `ApplyPhoneSchema` additive change preserves existing rows
5. vibing quick actions for a phone project include deploy/runtime-aware actions only when eligible
6. vibing execution result is machine-readable and does not require parsing human prose

Existing coverage already present:

- `desktop/agent/phone_backend_test.go`
- `desktop/agent/phone_export_test.go`

Gap to add:

- a focused test around phone-project vibing execution contract

### B. Agent HTTP dogfood test

Owner:
- `desktop/agent`

New test:

- `TestPhoneOnlyTodoE2E_MobileHTTPFlow`

Pattern:
- match `bento_e2e_test.go`

Flow:

1. start test server
2. `POST /phone/projects/create`
3. `GET /phone/projects/get`
4. `GET /phone/projects/browse?table=todos`
5. `GET /vibing?...`
6. `POST /vibing/execute`
7. verify task/runtime action response
8. `POST /phone/projects/insert`
9. `GET /phone/projects/browse?table=todos`
10. `GET /phone/projects/export`

Assertions:

- no step requires a desktop-only surface
- all payloads are owner-auth mobile-safe
- result objects are structured enough for headless automation

### C. Mobile-headless hermetic test

Owner:
- `mobile-headless`

New test:

- `mobile-headless/test/phone-only-vibing.hermetic.test.ts`

Flow:

1. create mock/mobile client
2. create todo phone project through `mobile.phoneProjects.create`
3. fetch project and verify schema/stats
4. call raw vibing state endpoint or new typed helper
5. execute one vibing action through the mobile facade
6. export project and inspect byte size / content type

Goal:
- catch drift in the mobile facade without booting the real Go agent

### D. Mobile-headless smoke against real agent

Owner:
- `mobile-headless`

New test:

- `mobile-headless/test/phone-only-vibing.smoke.test.ts`

Flow:

1. boot a real local `yaver serve`
2. create a todo phone project through `MobileClient`
3. browse rows through the same mobile facade
4. call vibing state
5. execute one vibing action
6. insert a todo row
7. export the project

Assertions:

- request succeeds against the real agent HTTP contract
- row count changes after insert
- exported archive is non-empty

### E. Real mobile smoke

Owner:
- mobile app + QA/dev workflow

Minimum required manual or device-driven smoke:

1. open Yaver mobile app on a real phone
2. create `todo-mobile-only`
3. trigger one vibing action from mobile
4. open the third-party app on the same phone
5. create one todo
6. reload
7. reopen
8. verify persistence

This lane is required before claiming the full phone-only story works.

## API Shape Needed For Easy Testing

The key requirement is to keep pure domain logic separate from HTTP wrappers.

### Go domain functions

These should remain callable without HTTP:

- `CreatePhoneProject(spec) (PhoneProject, error)`
- `LoadPhoneProject(slug) (PhoneProject, error)`
- `ApplyPhoneSchema(slug, schema) error`
- `ApplyPhoneSeed(slug, seed) error`
- `ExportPhoneProject(slug) ([]byte, error)`
- `ExportPhoneProjectWithOptions(slug, opts) ([]byte, error)`
- `ImportPhoneProject(bundle, opts) (PhoneProject, error)`

Current code is already close here.

### Go vibing contract

Recommended stable functions:

- `GetVibingState(projectPath, query) (VibingState, error)`
- `ExecuteVibingAction(req) (VibingExecutionResult, error)`

Recommended result shape:

```go
type VibingExecutionResult struct {
    OK          bool                   `json:"ok"`
    Kind        string                 `json:"kind"` // task | runtime_action | noop | error
    TaskID      string                 `json:"taskId,omitempty"`
    ActionID    string                 `json:"actionId,omitempty"`
    Project     string                 `json:"project,omitempty"`
    Path        string                 `json:"path,omitempty"`
    Message     string                 `json:"message,omitempty"`
    Details     map[string]interface{} `json:"details,omitempty"`
    Error       string                 `json:"error,omitempty"`
}
```

Reason:
- unit tests and headless tests should assert a typed outcome, not parse chatty text

### Mobile-headless facade additions

Recommended typed methods in `mobile-headless/src/mobile-client.ts`:

```ts
vibing = {
  state: async (query: string) => ...,
  execute: async (body: {
    prompt?: string;
    suggestionId?: string;
    project?: string;
    path?: string;
  }) => ...,
};
```

Why:
- current headless facade clearly supports wizard and phone projects
- vibing should be first-class too if this is a headline mobile-only story

## Recommended Implementation Order

1. add typed `vibing` methods to `mobile-headless`
2. add one Go HTTP dogfood test for phone-only todo flow
3. add one headless hermetic test
4. add one headless smoke-local test
5. define one real-device smoke checklist and make it part of release validation

## Pass/Fail Criteria

We can say the phone-only story is green when all of these are true:

- Go phone-project tests pass
- headless hermetic mobile test passes
- headless smoke against real local agent passes
- HTTP dogfood test passes
- at least one real-device smoke passes for same-phone app + local backend persistence

We should **not** claim full proof from `mobile-headless` alone.

## Immediate Repo Tasks

### `desktop/agent`

- add `TestPhoneOnlyTodoE2E_MobileHTTPFlow`
- ensure `/vibing/execute` returns a stable structured result for phone-project flows

### `mobile-headless`

- add typed `vibing` facade
- add `phone-only-vibing.hermetic.test.ts`
- add `phone-only-vibing.smoke.test.ts`

### Mobile app / release process

- add one real-device smoke checklist for:
  - create project
  - vibe a change
  - load app on same phone
  - write data
  - reload
  - reopen
  - verify persistence

## Recommended First PR

The first PR should stay narrow:

1. add typed `mobile-headless` vibing methods
2. add one smoke test using local agent
3. add one Go dogfood HTTP test

That is enough to move this from “conceptual” to “real regression coverage” without blocking on full device automation.
