# Tablet UI Improvements Audit

Date: 2026-05-10
Repo: `yaver.io`
Surface: `mobile/`
Method: code audit only. This is not a simulator/device pixel pass, so any runtime-only issue should be validated separately on real iPad and Android tablet hardware.

## Executive Summary

Yaver mobile is still fundamentally a phone-first app.

The app has a few responsive behaviors:

- several modals use `presentationStyle="pageSheet"`
- some layouts use `flexWrap`
- a few cards constrain width or allow wrapping
- one or two components use explicit `maxWidth`

But there is no shared tablet layout system. There is no breakpoint hook, no device class abstraction, no regular-width shell, no split-view patterns, and almost no width-aware rendering logic. Most large screens simply stretch phone UIs across a wide canvas.

The main conclusion:

- tablet support today is incidental, not designed
- the app is usable on tablets in many places, but not optimized
- the largest gains will come from a small layout foundation plus 4 high-traffic screens:
  `Tasks`, `Devices`, `Projects`, `Settings`

## Audit Scope

Primary files inspected:

- `mobile/app/_layout.tsx`
- `mobile/app/(tabs)/_layout.tsx`
- `mobile/app/(tabs)/tasks.tsx`
- `mobile/app/(tabs)/devices.tsx`
- `mobile/app/(tabs)/apps.tsx`
- `mobile/app/(tabs)/settings.tsx`
- `mobile/app/(tabs)/files.tsx`
- `mobile/app/(tabs)/infra.tsx`
- `mobile/src/components/AppScreenHeader.tsx`
- `mobile/src/components/TaskHeader.tsx`
- `mobile/src/components/TaskTargetWizard.tsx`
- `mobile/src/components/DeviceDetailsModal.tsx`
- `mobile/src/components/RunnerAuthModal.tsx`
- `mobile/src/theme/tokens.ts`

File sizes matter here because large monolithic screens make responsive cleanup expensive:

- `mobile/app/(tabs)/tasks.tsx`: 5630 lines
- `mobile/app/(tabs)/settings.tsx`: 5244 lines
- `mobile/app/(tabs)/apps.tsx`: 2477 lines
- `mobile/app/(tabs)/devices.tsx`: 1526 lines
- `mobile/src/components/DeviceDetailsModal.tsx`: 1441 lines

## What We Already Have

### 1. Tablet-safe modal primitives in some places

Good signs:

- `DeviceDetailsModal` uses `presentationStyle="pageSheet"`
- `TaskTargetWizard` uses `presentationStyle="pageSheet"`
- several settings subflows use `presentationStyle="pageSheet"`

Files:

- `mobile/src/components/DeviceDetailsModal.tsx`
- `mobile/src/components/TaskTargetWizard.tsx`
- `mobile/app/(tabs)/settings.tsx`

This is better than forcing every flow full-screen.

### 2. A few components already constrain content width

Examples:

- `RunnerAuthModal` uses `maxWidth: 460`
- `TaskTargetWizard` confirmation dialog uses `maxWidth: 380`

Files:

- `mobile/src/components/RunnerAuthModal.tsx`
- `mobile/src/components/TaskTargetWizard.tsx`

This is the right instinct for tablets and should become standard.

### 3. Some screens already wrap cards instead of forcing one rigid column

Best example:

- `infra.tsx` metric cards use `flexWrap` and `minWidth: "47%"`

File:

- `mobile/app/(tabs)/infra.tsx`

This is one of the clearest tablet-friendly patterns currently in the app.

### 4. Some detail surfaces are already conceptually separable

These screens naturally want split-pane layouts:

- `Tasks`: task list + task detail
- `Devices`: device list + device detail
- `Files`: roots/tree + preview
- `Projects`: project list + active preview/details

The information architecture already exists. The layout does not.

## What We Do Not Have

### 1. No shared responsive/tablet foundation

There is no central abstraction for:

- compact vs regular width
- portrait tablet vs landscape tablet
- content max widths
- pane counts
- adaptive spacing/typography
- modal sizing policy

Evidence:

- `rg` found only two window-size reads in the mobile app:
  - `mobile/app/(tabs)/files.tsx`
  - `mobile/src/components/FeedbackOverlay.tsx`
- there is no `useWindowDimensions()` based layout system
- there is no `isTablet` helper or size-class hook

### 2. No tablet-aware navigation shell

The main app shell is still a bottom tab bar with fixed phone sizing:

- tab bar height is hardcoded to `68`
- `tabIconWrap` has `minWidth: 56`
- no left rail / sidebar mode
- no `tabBarPosition` or adaptive nav variant

File:

- `mobile/app/(tabs)/_layout.tsx`

On tablets, especially landscape, this wastes the best available space.

### 3. No consistent content width policy

Across major screens, content usually stretches edge-to-edge with phone paddings like:

- `paddingHorizontal: 14`
- `paddingHorizontal: 16`
- `padding: 24`

This is acceptable on phones, but on tablets it creates:

- long scan lines
- oversized empty gutters inside wide cards
- bottom sheets that feel undersized vertically but oversized horizontally
- single-column fatigue

### 4. No split-view or master-detail patterns on high-value screens

The app relies heavily on:

- full-screen pages
- full-height modals
- bottom-sheet style overlays
- list-first then drill-in

That is a phone model. Tablets should use persistent parallel context.

## Current Problem Areas by Screen

## A. Global Shell

Files:

- `mobile/app/(tabs)/_layout.tsx`
- `mobile/src/components/AppScreenHeader.tsx`

Current state:

- bottom tab bar is always bottom-mounted
- tab treatment is tuned for phones
- headers are compact, centered, single-row app bars
- no tablet-only secondary nav

Problems on tablets:

- too much width is wasted
- tabs become physically small relative to screen size
- hidden routes under `More` stay buried even though tablet space could expose them directly
- headers do not support richer right-side actions or breadcrumb/context bars

Recommendation:

- introduce a regular-width shell with left navigation rail or sidebar
- keep bottom tabs on phones only
- give regular-width screens a wider header pattern with title, subtitle, and actions

## B. Tasks

File:

- `mobile/app/(tabs)/tasks.tsx`

Current state:

- task list lives in one screen
- task detail opens in a full-height modal
- new task composer is a bottom-sheet style modal
- logs open in another full-height modal
- chat input and bubbles are sized for phones

Strong existing UX:

- clear task detail concept
- good header cleanup through `TaskHeader`
- separate logs/detail/follow-up flows already exist

Tablet issues:

- task detail should not need a full-screen modal on landscape tablets
- logs should not replace the whole context
- composer sheet is phone-shaped:
  - `modalContent` has big rounded top corners and bottom-sheet semantics
  - `chatInput` has `minHeight: 190`, which is large even on phones and awkward on tablets
- bubbles use phone max-width assumptions:
  - user bubble `maxWidth: "80%"`
- action bars remain single-row chip scrollers instead of a persistent side panel or toolbar

Recommendation:

- tablet layout should be 2-pane by default:
  - left: task list + filters
  - right: selected task detail/chat
- logs should be a side drawer or right-column tab, not a separate full-screen layer
- new task composer should become a centered dialog or right-side composer panel on regular width
- follow-up composer should dock below task detail, not expand a phone-style card

Priority: highest

## C. Devices

Files:

- `mobile/app/(tabs)/devices.tsx`
- `mobile/src/components/DeviceDetailsModal.tsx`

Current state:

- device list is a single-column `FlatList`
- device details open in `pageSheet`
- cards wrap internal chips reasonably well

Good existing behavior:

- `DeviceDetailsModal` already uses `pageSheet`
- device badges and runner chips use wrapping, which helps on medium widths

Tablet issues:

- the main screen is still list-only, even though device details are one of the best candidates for persistent side-by-side layout
- cards stretch full width instead of becoming a 2-column grid or master-detail index
- long-press driven workflows are less discoverable on a bigger screen with pointer/keyboard expectations

Recommendation:

- portrait tablet:
  - 2-column device grid
- landscape tablet:
  - left device list/grid
  - right persistent detail panel
- preserve `pageSheet` only for compact widths or secondary flows

Priority: highest

## D. Projects / Apps

File:

- `mobile/app/(tabs)/apps.tsx`

Current state:

- repo cards are horizontally scrollable with `minWidth: 140`, `maxWidth: 220`
- project cards remain list-oriented
- web view and vibing use modal/full-screen transitions

Good existing behavior:

- some chip wrapping exists
- repo cards already constrain width
- vibing grid items use `width: "48%"`

Tablet issues:

- horizontal repo rows are still phone-first; on tablet they should become a visible grid
- project list/detail/preview are not laid out together
- `showWebView` is still a full-screen modal, even though tablet can support embedded preview
- action sheets still consume bottom-sheet semantics instead of popover/sidebar/detail rail patterns

Recommendation:

- replace horizontal repo scrollers with a responsive grid on regular width
- split screen into:
  - left: repo/project index
  - center: project detail/actions
  - optional right: preview/webview/build status

Priority: high

## E. Settings

File:

- `mobile/app/(tabs)/settings.tsx`

Current state:

- one huge scroll surface
- many independent cards and inline controls
- multiple nested flows and page sheets

Strengths:

- sections exist conceptually
- cards are already visually separated

Tablet issues:

- 5200+ line single-screen architecture is a major maintainability problem
- on tablet, the screen likely becomes a very long stretched settings form
- many rows use side-by-side button groups that are still tuned for phone widths
- no left section index / right detail panel structure

Recommendation:

- break settings into section-driven navigation on tablets:
  - left rail: General, Agents, Voice, Relays, Deploy, Security, Diagnostics, etc.
  - right pane: section detail
- extract repeated row/card primitives before doing layout work
- normalize modals to centered forms or sheet-with-max-width on regular screens

Priority: highest for architecture, medium-high for immediate UI polish

## F. Files

File:

- `mobile/app/(tabs)/files.tsx`

Current state:

- the only major screen reading `Dimensions.get("window")`
- image preview size is `win.width - 24`
- CSV viewer is horizontally scrollable
- everything else is single-pane browsing

Good existing behavior:

- file preview modes are already separated by type
- CSV uses horizontal scroll instead of truncation

Tablet issues:

- image preview uses full screen width instead of content max width
- no tree/list/detail structure
- file roots, directory listing, and file preview compete for the same pane

Recommendation:

- make Files the first true 3-pane screen:
  - left: roots/projects
  - middle: folder listing
  - right: preview
- on portrait tablet, collapse to 2 panes
- constrain image preview to a readable content width, not the entire display width

Priority: high

## G. Shared Overlays and Auth Flows

Files:

- `mobile/src/components/RunnerAuthModal.tsx`
- `mobile/src/components/TaskTargetWizard.tsx`
- `mobile/src/components/FeedbackOverlay.tsx`

Current state:

- `RunnerAuthModal` is one of the better tablet-ready surfaces because it uses `maxWidth: 460`
- `TaskTargetWizard` uses `pageSheet`, which is better than full screen
- `FeedbackOverlay` positions itself from raw screen width

Issues:

- overlay logic is not consistently driven by layout context
- `FeedbackOverlay` uses absolute screen assumptions rather than a safe tablet positioning policy
- confirmation dialogs use ad hoc max-widths instead of a standard modal scale

Recommendation:

- define shared modal sizes:
  - `compactDialog`
  - `formDialog`
  - `pageSheet`
  - `fullScreen`
- centralize floating overlay positioning rules

Priority: medium

## What This Means Architecturally

The tablet problem is not "we need to tweak spacing."

The real issue is:

- screens own too much layout logic themselves
- screen structure is modal-heavy and linear
- there is no responsive contract above individual styles

Without fixing that, tablet polish will turn into many one-off `if (width > X)` patches.

## Recommended Foundation Work

Build these first.

### 1. Add a shared responsive hook

Suggested API:

```ts
type LayoutClass = "phone" | "tablet-portrait" | "tablet-landscape";

function useResponsiveLayout() {
  return {
    width,
    height,
    isTablet,
    isLandscape,
    layoutClass,
    contentMaxWidth,
    paneCount,
  };
}
```

This should be the single source of truth for:

- breakpoints
- modal sizing
- pane decisions
- content widths

### 2. Add shared layout primitives

Suggested primitives:

- `ScreenScaffold`
- `ResponsiveContent`
- `SplitPane`
- `TabletSidebar`
- `TabletDetailPane`
- `AdaptiveSheet`

### 3. Define tablet tokens

Current `mobile/src/theme/tokens.ts` has spacing and typography, but not adaptive scale tiers.

Add:

- compact / regular spacing sets
- content max widths
- pane gaps
- modal widths
- rail widths

### 4. Centralize modal policy

Standardize:

- phone bottom sheet
- tablet centered dialog
- tablet page sheet
- tablet full-screen only for immersive surfaces

## Suggested Rollout Order

## Phase 1: Foundation

1. Add `useResponsiveLayout`
2. Add `ScreenScaffold` and `SplitPane`
3. Add tablet tokens and max-width policy
4. Make the main tab shell adaptive

## Phase 2: Highest-impact screens

1. `Tasks`
2. `Devices`
3. `Settings`
4. `Apps`

## Phase 3: Secondary screens

1. `Files`
2. `Infra`
3. `Runs`
4. `More`

## Phase 4: Overlay cleanup

1. auth modals
2. action sheets
3. feedback overlay
4. logs/detail subflows

## Concrete Target States

## Tablet target for Tasks

- left pane: task list, filters, connection banner
- right pane: selected task conversation
- optional bottom-right dock: composer
- logs as a side tab or collapsible drawer

## Tablet target for Devices

- left pane: searchable device list/grid
- right pane: persistent device detail
- connection/auth actions visible without long-press dependence

## Tablet target for Apps

- left pane: repos and project list
- middle pane: selected project metadata/actions
- right pane: live preview or build output

## Tablet target for Settings

- left pane: settings sections
- right pane: active section form/cards
- section-local save/apply actions

## Tablet target for Files

- left pane: roots
- middle pane: folder tree/list
- right pane: preview

## Risks

### 1. Monolithic screens

`tasks.tsx` and `settings.tsx` are large enough that direct tablet retrofits will be brittle unless component extraction happens first.

### 2. Modal proliferation

Many flows assume "cover the screen, then dismiss." Tablets work better with persistent context. Refactoring modal ownership will be necessary.

### 3. Inconsistent width assumptions

Ad hoc `paddingHorizontal: 14/16` and percentage-width chips/grids will create uneven results unless replaced by a shared container system.

## QA Gaps

No clear tablet-specific UI infrastructure was found in the mobile app code:

- no tablet layout hook
- no tablet snapshots
- no tablet-specific route variants
- no visible iPad/Android-tablet screen QA layer in app code

Recommended QA additions:

- iPad portrait
- iPad landscape
- Android 11"+ tablet portrait
- Android tablet landscape
- split-view / resized-window test on iPad if supported

For each of these, validate at minimum:

- shell navigation
- Tasks
- Devices
- Apps
- Settings
- Files
- all major modal flows

## Bottom Line

Yaver mobile is not far from being good on tablets, because the information architecture already has natural pane boundaries. The missing piece is a real responsive system.

If we do only one thing, it should be this:

- build a shared tablet layout foundation, then convert `Tasks`, `Devices`, `Settings`, and `Apps` to split-pane regular-width layouts

If we do not do that, tablet work will likely degrade into scattered one-off fixes and the app will keep feeling like an enlarged phone UI.
