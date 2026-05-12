# iOS Mobile vs Tablet Hot Reload Comparison

Date: 2026-05-11
Scope: compare the current iPhone hot-reload experience against the general tablet experience used by iPad and Android tablets. Assumption for this note: iPhone is the reference experience, native Android Hermes parity is being handled in another thread, and the goal is stylistic / interaction parity rather than one-to-one layout cloning.

## Executive Summary

The iPhone hot-reload flow feels coherent because the entire shell is narrow, portrait-first, and single-purpose. The tablet flow is operating inside a different product shell:

- rotation is unlocked on tablets
- navigation changes between bottom bar and left rail
- content gets width caps and multi-column grids
- connection, targeting, serving, and project discovery all stay visible at once

That means tablet should not be judged as "iPhone but bigger". It needs a more explicit dashboard-style information hierarchy.

Today, the codebase already treats tablet as a separate shell, but the hot-reload surfaces historically carried too much phone DNA:

- overly generic serving labels
- weak active-project identity
- action rows that stretched badly on wide screens
- mixed "workspace project" vs "currently served project" signals

The recent UI edits in `DevPreview.tsx` and `hotreload.tsx` improve that, but the deeper lesson is architectural: the tablet shell has different navigation, orientation, and density rules before hot reload even starts.

Important framing:

- tablet should not literally copy iPhone layout
- tablet should feel like it belongs to the same product
- parity target is shared visual language, control hierarchy, motion/tone, and state clarity
- divergence in panel structure is acceptable when it is driven by the larger canvas

## Baseline: Why iPhone Feels Fine

### 1. iPhone is explicitly portrait-first

The app root locks phones to portrait and unlocks tablets:

- `mobile/app/_layout.tsx:77`
- `mobile/app/_layout.tsx:85`
- `mobile/app/_layout.tsx:89`

Effect:

- on iPhone the user always sees the same narrow, vertically stacked control model
- controls do not need to survive orientation changes or dual-pane transitions

### 2. iPhone gets the simplest layout class

The responsive model has three shells:

- `phone`
- `tablet-portrait`
- `tablet-landscape`

Source:

- `mobile/src/hooks/useResponsiveLayout.ts:5`
- `mobile/src/hooks/useResponsiveLayout.ts:42`

Effect:

- iPhone is always in the most constrained and predictable class
- the hot-reload UI can rely on one reading width, one action grouping, one scan pattern

### 3. iPhone keeps navigation simple

Tablet landscape can switch to a left rail while other shells stay on bottom tabs:

- `mobile/app/(tabs)/_layout.tsx:76`
- `mobile/app/(tabs)/_layout.tsx:79`
- `mobile/app/(tabs)/_layout.tsx:86`

Effect:

- iPhone does not compete with a broader shell for visual attention
- the user reads hot reload as one screen, not as one panel inside a larger tablet environment

## Why Tablet Is Harder

### 1. Tablet is a genuinely different shell, not a scaled phone

Tablet content is clamped and centered via `useTabletContentStyle`:

- `mobile/src/hooks/useTabletContentStyle.ts:5`
- `mobile/src/hooks/useTabletContentStyle.ts:19`

Grid counts and gutters also change by shell:

- `mobile/src/hooks/useResponsiveLayout.ts:51`
- `mobile/src/hooks/useResponsiveLayout.ts:56`
- `mobile/src/hooks/useResponsiveLayout.ts:63`

Effect:

- tablet has more whitespace, more lateral room, and more opportunity for hierarchy mistakes
- any phone-like "single dominant CTA plus crumbs" composition looks under-designed on tablet

### 2. Tablet lets more state coexist

In Hot Reload, the tablet view can show all of these within one scroll pass:

- machine banner
- Hermes readiness
- preview target chooser
- active dev-server card
- operation state
- incident state
- activity log tail
- app grid

Relevant code:

- `mobile/app/(tabs)/hotreload.tsx:788`
- `mobile/app/(tabs)/hotreload.tsx:832`
- `mobile/app/(tabs)/hotreload.tsx:884`
- `mobile/app/(tabs)/hotreload.tsx:1091`

Effect:

- tablet needs dashboard hierarchy, not just better spacing
- each block must clearly answer one question:
  - which box?
  - which target?
  - which app?
  - what state?
  - what action?

### 3. Tablet has more than one "project" concept on screen

Tasks shows:

- serving state via `DevPreview`
- workspace/project context via `agentInfo()`

Relevant code:

- `mobile/app/(tabs)/tasks.tsx:3161`
- `mobile/app/(tabs)/tasks.tsx:3408`
- `mobile/app/(tabs)/tasks.tsx:3411`
- `mobile/app/(tabs)/tasks.tsx:4815`

Effect:

- on tablet, where more context sits side-by-side, any ambiguity between "current workspace" and "currently served app" becomes much more obvious than on iPhone

## Surface-by-Surface Comparison

### A. Remote box / machine context

Shared banner:

- `mobile/src/components/RemoteBoxBanner.tsx:54`
- `mobile/src/components/RemoteBoxBanner.tsx:67`
- `mobile/src/components/RemoteBoxBanner.tsx:96`

Comparison:

- iPhone: this reads like a compact status strip above the main screen
- tablet: this becomes the top row of a dashboard and therefore needs stronger secondary grouping under it

What tablet needs:

- machine identity must stay short
- all extra status must collapse into pills or structured rows
- long path strings should never compete with the main machine label

### B. Active serving / preview state

Shared serving banner:

- `mobile/src/components/DevPreview.tsx:423`
- `mobile/src/components/DevPreview.tsx:445`
- `mobile/src/components/DevPreview.tsx:486`
- `mobile/src/components/DevPreview.tsx:739`

Comparison:

- iPhone tolerates a simple vertical status block with a large primary CTA
- tablet needs the serving banner to behave like a control module with explicit title, state, metadata, and bounded actions

Why the old tablet version felt wrong:

- generic title instead of app identity
- primary green action dominated the card
- stop action floated as a smaller afterthought
- metadata looked like leftovers rather than part of the main state model

What changed:

- served project name is now the title
- state pill is explicit (`SERVING` / `BUILDING`)
- framework/port/target are grouped into one readable line
- actions are bounded for tablet instead of stretching horizontally

### C. Hot Reload main screen

Current active card:

- `mobile/app/(tabs)/hotreload.tsx:895`
- `mobile/app/(tabs)/hotreload.tsx:928`
- `mobile/app/(tabs)/hotreload.tsx:956`
- `mobile/app/(tabs)/hotreload.tsx:1043`
- `mobile/app/(tabs)/hotreload.tsx:1307`

Comparison:

- iPhone succeeds with a simple title + status + buttons
- tablet needs stronger "control-room" treatment because the user reads the page more like an operations panel

Tablet-specific improvements now present:

- framework icon badge at the top of the active card
- explicit meta pills for framework, port, hot-reload state, and target
- path promoted into a dedicated mono line instead of being buried
- action row wraps so `Open`, `Reload`, `Shot`, and `Stop` do not fight each other

### D. Preview target selection

Target chooser:

- `mobile/app/(tabs)/hotreload.tsx:834`
- `mobile/app/(tabs)/hotreload.tsx:847`
- `mobile/app/(tabs)/hotreload.tsx:1374`

Comparison:

- iPhone can get away with "select target if needed"
- tablet needs a more explicit target model because multi-device work feels more natural on a large screen

What matters on tablet:

- current target should be visible without reading chip colors alone
- chooser should read like current routing state, not just a filter control

### E. App discovery grid

Hot Reload apps list uses different column counts by shell:

- `mobile/app/(tabs)/hotreload.tsx:142`
- `mobile/app/(tabs)/hotreload.tsx:1096`

Comparison:

- iPhone: single column, low ambiguity
- tablet portrait: 2 columns
- tablet landscape: 3 columns

Implication:

- tablet cards need stronger per-card identity because the user scans laterally
- path, framework, and action affordance all need to be legible at a glance

## iPhone vs Tablet Mental Model

### iPhone mental model

"I am doing one thing right now."

That one thing is:

- pick an app
- open it
- reload it
- stop it

The screen can be linear and heavy on a single primary action.

### Tablet mental model

"I am supervising a dev loop."

That loop includes:

- host box
- target device
- served project
- runtime state
- operation state
- app switching

So tablet must feel like a compact control panel, not a stretched phone card stack.

## Where Tablet Still Differs From iPhone Even With Better UI

### 1. Orientation volatility

Phones are portrait-locked, tablets are not:

- `mobile/app/_layout.tsx:77`
- `mobile/app/_layout.tsx:88`

Consequence:

- any tablet UI must survive rotation without losing information hierarchy
- phone-only spacing instincts are not reliable

### 2. Navigation shell changes

Tablet landscape can use a left rail:

- `mobile/app/(tabs)/_layout.tsx:79`

Consequence:

- hot reload on tablet may be read as part of a larger workbench
- the UI has to stand on its own within a broader shell

### 3. State density is higher

Tasks and Hot Reload both keep more context visible at once than iPhone needs:

- machine status
- serving status
- workspace project
- to-do stats
- dev operations

Consequence:

- weak labels or duplicated meanings hurt tablet faster than phone

## Design Rules For Tablet Hot Reload Going Forward

### Rule 1: always lead with app identity

Not:

- generic framework name
- generic "dev server"

Instead:

- served project title first
- framework/port/target as supporting metadata

### Rule 2: actions should form a bounded cluster

Not:

- one giant filled CTA plus drifting secondary actions

Instead:

- clear primary action
- bounded secondary actions
- wrapping or vertical grouping when width increases

### Rule 3: distinguish these labels explicitly

Tablet should not blur:

- host machine
- target device
- workspace project
- served app

### Rule 4: long paths are context, not headline

Paths belong in:

- mono
- one line
- lower emphasis

Never as the main identity surface.

### Rule 5: pills should carry machine-readable state

Good tablet pills:

- `SERVING`
- `BUILDING`
- `HOT RELOAD ON`
- `TARGET THIS DEVICE`

Weak tablet pills:

- decorative status without clear semantic meaning

## Recommended Next UI Passes

### 1. Make Tasks explicitly show "serving" vs "workspace"

Today Tasks still mixes those concepts:

- `mobile/app/(tabs)/tasks.tsx:3408`
- `mobile/app/(tabs)/tasks.tsx:3411`

Recommendation:

- one chip for `Serving <app>`
- one chip for `Workspace <project>`

### 2. Consider a two-zone tablet Hot Reload header

Potential structure:

- zone 1: machine + Hermes readiness + target
- zone 2: active app + actions

That would make the top of tablet Hot Reload read more intentionally than stacked cards.

### 3. Normalize terminology across Hot Reload and Tasks

Use the same labels in both places for:

- serving
- target
- workspace
- machine

### 4. Tune tablet landscape for true control-panel density

The current improvements help, but tablet landscape still has room for:

- denser action grouping
- side-by-side target and active-state blocks
- clearer separation between active app and app discovery grid

## Final Take

iPhone feels good because the shell, navigation, and action model are all narrow and singular.

Tablet is a different product surface:

- more room
- more state
- more simultaneous context
- more rotation
- more navigation variance

So the right comparison is not "make tablet look like iPhone". The right target is:

"make tablet feel as intentional, visually related, and as low-friction as iPhone, while still using a tablet-native information hierarchy."

That is the direction the current `DevPreview` and `hotreload.tsx` edits move toward.
