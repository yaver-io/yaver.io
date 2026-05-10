# UI improvements — Tasks, Projects, connection banner, tab bar

Companion brief to [LOGIN_REDESIGN.md](LOGIN_REDESIGN.md). Same goal: phone +
tablet, light + dark, both themes equally first-class. This brief covers the
post-login surfaces visible in the supplied screenshots:

1. The shared **connection status banner** at the top of every primary tab
2. The **Tasks** tab (filter chips + task cards + FAB)
3. The **Projects** tab (search + filter chips + empty state)
4. The bottom **tab bar** (Reload / Tasks / Projects / Devices / More)
5. The **Reload** tab (active project card, agent-operation panel,
   action buttons, Other Apps list)
6. The **Devices** tab (top action bar, invite-code row, device cards)
7. **Dark-mode regressions** observed across the above (added after a
   second screenshot pass — see end of brief)

Same constraints as the login brief — no new deps, no new auth flows, no new
theme tokens. Edit JSX + StyleSheet only; reach for `useColors()`,
`useResponsiveLayout()`, and the existing typography tokens.

## File scope (all edits stay inside these files)

| Section | File |
|---|---|
| Connection banner | `mobile/src/components/RemoteBoxBanner.tsx` |
| Tasks screen | `mobile/app/(tabs)/tasks.tsx` (filter chip + `TaskCard` + empty/header sections only — leave the 5658-line behavior untouched) |
| Projects screen | `mobile/app/(tabs)/apps.tsx` (search row, filter row, list cards, empty state) |
| Tab bar | `mobile/app/(tabs)/_layout.tsx` (`TabIcon`, `tabBarStyle`, `styles`) |
| Reload tab | `mobile/app/(tabs)/hotreload.tsx` (banner `extra` slot, active project card, agent-operation inset, action buttons, Other Apps list, empty state) |
| Devices tab | `mobile/app/(tabs)/devices.tsx` (top "N Connected" + Disconnect bar, invite-code row, device card layout, action button cluster) |

Frozen — do not touch:
- Any state, network, polling, or subscription code
- `useDevice()`, `quicClient`, `openAppBus`, navigation
- The action-sheet, modals, ping overlay, devicePicker, or device-card
  rendering inside `tasks.tsx` (line ~3494+) — these are out of scope
- The 5,000+ lines of task lifecycle / agent integration in `tasks.tsx`
- Theme tokens (`colors.ts`, `tokens.ts`)

## What's wrong with each surface

### Connection banner (`RemoteBoxBanner`)

Reference: green "Connected · Mobiles-Ma… [Secondary] [Switch ›]" bar +
optional second line "Direct · OpenAI Codex ready [no response]".

Issues today (`mobile/src/components/RemoteBoxBanner.tsx`):
- Light-mode green (`#f0fdf4` bg + `#bbf7d0` border) is fine but pulls eye
  hard — feels like a global success toast that never goes away. The banner
  is supposed to be ambient.
- The "Secondary" role pill, "Switch ›" CTA pill, and "no response" extra
  pill are three different shapes/weights — visually noisy. They should
  cluster as one chip group.
- The `extra` slot sits at `marginLeft: 18` which doesn't align cleanly with
  anything; on tablet it floats orphaned.
- Dark mode bg is the same `#15151A` for every state — only the dot/text
  color shifts. Fine for "connected" but the "Disconnected" state needs
  more visual weight (it's actionable).

### Tasks tab (`tasks.tsx`)

Reference: "REVIEW" badge + title + preview + "1h ago" stacked cards, with
"Active 8 / Review 8 / Completed 3 / Failed" filter chips above and a
purple "+" FAB bottom-right.

Issues:
- Filter chips at line 3438+ use 5 different hardcoded hex colors
  (`c.accent`, `#8b5cf6`, `#22c55e`, `#ef4444`, `c.textSecondary`) —
  pleasant in dark but reads as a rainbow in light mode.
- Selected chip bg is `withAlpha(c.accent, "24")` regardless of which
  status — the status color hint is lost the moment you tap.
- "Active 8" runs the count straight against the label. Linear-style
  separation (`Active · 8` or a separate count pill) reads cleaner.
- Action chips in the same scroller (Stop All, Clear, Tmux, Logs, Ship It,
  Summary) sit alongside status-filter chips with the same shape. Two
  different concepts, one visual treatment — they should be visually split
  (status filters on one row OR a thin divider between groups).
- Task card status badge (`statusBadge` at line 1141) uses
  `STATUS_COLORS[item.status] + "1f"` — same alpha trick. Looks fine in
  dark, washes out in light mode.
- "REVIEW" all-caps + IP address `192.168.111.25` on the right read like
  forensic metadata. The IP is useful but doesn't need the same weight as
  the status.
- FAB is full-bleed solid `accent` with a white "+". Fine on this card-
  heavy screen, but the shadow is missing on light mode so it disappears
  against the page.

### Projects tab (`apps.tsx`)

Reference: "Projects" header → green connection banner → search box → "All
(0)" filter chip → centered empty state → "Rediscover" button.

Issues:
- With zero projects the chip "All (0)" is the only visible UI between the
  search bar and the empty state — looks like a dead control. Either hide
  it or merge the count into the empty state.
- Empty state copy ("No projects discovered yet. The agent scans your home
  directory automatically.") is 2 lines centered with a single button.
  Reads as a placeholder — needs an icon, hierarchy, and a secondary line
  about timing ("This usually takes 5–10 seconds").
- "Rediscover" button uses `c.bgCard` + `c.border` — vanishes against the
  card bg in light mode (same problem as login providers).
- Search row uses `c.bgInput` with no border (`borderColor: "transparent"`)
  — fine in dark, looks like it floats in light mode. Add `borderSubtle`
  in light mode only, or rely on a subtle 1px inset shadow.
- Filter chips at line 1828+ duplicate the same pattern as Tasks but with
  different alpha math. Same redesign should apply to both.

### Tab bar (`_layout.tsx`)

Reference: "Reload / Tasks / Projects / Devices / More" with the Projects
icon currently focused (filled black circle "▶" against a white bg).

Issues:
- Active tab uses `c.tabActive` (`textPrimary`) — pure black on white in
  light mode, pure white on near-black in dark. Reads as inverted, not
  selected. The accent color isn't used at all on phone (only on the 2px
  indicator bar above the icon).
- Indicator bar is 24×2 directly above the icon at `marginBottom: 7`. On
  iOS this looks like an artifact (no other Apple app does this). Move it
  to the bottom of the icon (under the label) or replace with an
  accent-tinted icon background pill.
- The `greenDot` (dev-server-running indicator) at `position: { top: 6,
  right: 6 }` overlaps the icon glyph itself in some glyph variants
  (`hammer-outline`, `desktop-outline`). Move to top-right of the wrapper
  with proper hit-padding.
- Tab labels are 12pt regular / 12pt 600 — the weight change is the only
  active signal in the label. Add a slight color shift (accent on focused)
  for redundancy.

### Reload tab (`hotreload.tsx`)

Reference: green "Connected · yaver-test-eph… [Primary] [Switch ›]" banner
with three-line `extra` ("Go agent 1.99.191" / "Hermes reload ready" /
"/root"), then a **violet-bordered active project card** containing a
black "agent operation" inset, then green/red action buttons (loading-
spinner / Reload / Stop), then an "OTHER APPS" list of project cards.

Issues today (`mobile/app/(tabs)/hotreload.tsx`):
- The active project card uses `s.activeCard` with a violet border + light
  bg in light mode — combined with the all-green banner above and the
  black agent-operation inset, the screen has **four competing visual
  systems** stacked vertically (green / violet / dark navy / white).
  Reads as patchwork, not a designed surface.
- The "agent operation" inset (line ~922-941) hardcodes `#0b1220` bg +
  `#1d4ed8` border + `#93c5fd` / `#dbeafe` / `#bfdbfe` text. Pure-dark
  inset on a light card is jarring and the colors don't read as Yaver
  brand — looks like a console transplanted from another app.
- "current blocker" inset (line ~944-959) uses `#2a0a0a` bg + `#ef444466`
  border. Same problem: a deep-red box embedded in a white card in light
  mode is shocking.
- "Start failed" inset (line ~969-977) repeats the same hardcoded red.
- Agent-stdout streaming view (line ~990-995, hardcoded `#cbd5e1` 10pt
  Menlo) is small and grey on light card — illegible.
- Action buttons (line 1249-1255):
  - Open: `#22c55e` solid + `#000` text. Bright "Kermit green" — fine in
    dark, candy-colored in light mode.
  - Reload: `#22c55e22` (low-alpha green) + `#22c55e` text. Tints invisibly
    against the violet active card border on light mode.
  - Stop: `#ef444422` + `#ef4444` text — same problem.
  - All three are equal-flex with no clear primary, but visually Open
    grabs attention purely by saturation, not hierarchy.
- Spinner button (Open while building) shows just an `ActivityIndicator`
  with no text — no affordance for "what is this button when it's done."
- Stop confirmation banner (line ~1060-1089) hardcodes `#0f3a1f` /
  `#3a1f1f` — dark mode values pasted as-is, ugly in light mode.
- "OTHER APPS" section title is `c.textMuted` 12pt all-caps — fine, but
  there's no visual rhythm between "active card" (heavy, bordered, padded)
  and "Other Apps" (loose cards, no shared container). Section feels
  detached.
- Empty state has the same `Rediscover` button bug as Projects — invisible
  in light mode.
- The "this device" / "target · this device" line uses `#7dd3fc`
  hardcoded — light blue text on a white card has insufficient contrast.

## Design direction

### Connection banner — quieter, denser

Phone + tablet:
- Drop the full-width tinted background. Use `bgCardElevated` for the bg,
  `borderSubtle` for the bottom border in BOTH light and dark. The green
  signal stays via the dot + accent stripe on the left edge — no need to
  paint the whole bar.
- Cluster the status pills: `[role pill] [device name] · [extra]` on one
  line, `[Switch ›]` chip on the right. The role pill ("Secondary",
  "Primary") should match the device-name baseline, not float above it.
- "extra" slot (used by Tasks for "no response", by Reload for Hermes-ready,
  etc.) becomes inline chips in the same row when there's space, falling
  back to a second indented row only on phones with long device names.
- Connecting/error states keep their tinted accent stripe (amber/red) so
  they remain glanceable; the bar bg stays neutral. This is the iOS Lock
  Screen banner pattern — you notice the color, not the surface.

Light mode specifically: the current pure-green bar in the screenshot is
the loudest thing on the screen (louder than the title "Tasks"). After
the change, the title should win the hierarchy contest, with the banner
playing supporting role.

### Tasks — collapse the chip salad, calm the cards

Filter chips (line 3438+):
- Status filters and action chips become **two visually distinct groups**.
  Either split into two rows OR keep the single horizontal scroll but add
  a 12pt gap + a subtle vertical hairline (`borderSubtle`) between the
  last status chip and the first action chip.
- Status-chip selected state: `bg: chip.color + "1f"`,
  `borderColor: chip.color + "60"`, `text: chip.color`. Unselected:
  `bg: bgInput`, `borderColor: transparent`, `text: textSecondary`. So the
  status color survives selection.
- Format "Active 8" as `Active · 8` with the count in `textMuted` — the
  middle dot is the same separator the banner uses, gives consistency.

Task card (`TaskCard` at line 1045+):
- Replace the per-row IP address with a smaller `c.textMuted` chip on the
  right of the card header (same row as status badge, NOT a top-right
  corner overlay) — IP is useful but tertiary.
- Status badge: keep the colored dot (with the existing pulse for running),
  drop the all-caps and shrink to mixed-case ("Review", "Running"). All-
  caps reads as a system label; mixed-case feels like a UI affordance.
- Card background `bgCard`, border `borderSubtle` (currently `border` —
  step it down one level so cards feel like cards, not boxed inputs).
- Card radius 12 → 14 to match the banner's softer corners.
- Light mode only: add `shadowSm` to the card. This is the trick that
  makes light mode read as a designed surface instead of "just lighter."
- Spacing inside the card: title up to 16pt 600 (currently 15-ish), preview
  textSecondary at 14pt with `lineHeight: 20`, footer (timestamp + retry)
  at 12pt textMuted.

FAB:
- Keep the solid accent + white "+". Bump radius to 28 (it's currently
  32×2 = 64 wide, so radius 32 = pill — confirm in code), add a soft
  shadow that works in both themes (use `shadowMd` token).
- Position: bottom-right, 24pt from edge, sitting ABOVE the bottom safe-
  area inset (use `useSafeAreaInsets`).

### Projects — make the empty state intentional

When `projects.length === 0` and not searching:
- Hide the `All (0)` filter chip entirely. A single chip with a zero count
  is just visual noise.
- Empty state becomes a **centered card** (not loose text):
  - Icon: `Ionicons` `folder-open-outline`, 32pt, `textMuted`
  - Title: "No projects yet" (16pt 600, `textPrimary`)
  - Body: "Yaver scans your home directory automatically. This usually
    takes a few seconds after connecting." (13pt, `textSecondary`,
    `lineHeight: 19`, max-width 320)
  - Primary CTA: "Rediscover" — solid `accent` bg, white text, 12pt
    vertical pad, radius 10
  - Secondary link below: "Or open a folder manually" → opens whatever
    the existing manual-add path is (if there isn't one, drop this line
    rather than invent it)
- Keep the empty-state contained at max-width 360, vertically centered in
  remaining space below the search row.

When projects DO exist:
- Search row: in light mode add `borderColor: borderSubtle, borderWidth: 1`
  (current `borderColor: "transparent"` is the problem). Dark mode
  unchanged.
- Filter chips: same treatment as Tasks status chips — selected state
  carries the chip's color, not always accent.
- Project cards: same `borderSubtle` + `shadowSm` (light only) treatment as
  task cards. Framework icon stays at 22pt; project name 15pt 600; framework
  tag pill 11pt in `textSecondary` on `bgInput` (current is fine but bump
  the radius to match new chip system).

### Tab bar — accent-forward focus, real corners

`TabIcon` (`_layout.tsx` line 21+):
- Active state: icon color → `accent` (not `tabActive`), label color →
  `accent`, label weight stays 600. So the active tab is visibly tinted
  brand-purple, not just bolder.
- Indicator bar (currently 24×2 above the icon): remove. Replace with a
  pill background behind the icon glyph itself —
  `width: 48, height: 28, borderRadius: 14, backgroundColor: accent + "1A"`
  visible only on focus. This is the Material 3 / Linear pattern — works
  cleanly in both themes without theme-specific tweaks.
- Inactive: icon `tabInactive`, label `tabInactive`, no pill. Unchanged.
- `greenDot` (dev-server indicator): move to `top: 2, right: 8`, use
  `borderColor: bgTabBar` instead of hardcoded `#ffffff` so it doesn't
  ring-fence in dark mode. Same fix for any other status dots in the
  tab system.

### Reload — one card, real hierarchy, no console transplant

The active project card is the hero of this screen. Treat it like one.

**Active project card** (line ~865+, `s.card + s.activeCard`):
- Border: drop the violet (`borderColor: c.accent`-derived). Use
  `accent + "55"` only in **dark mode** for the soft glow — in light
  mode, border `borderSubtle` + `shadowSm` carries the elevation. Same
  rule as cards elsewhere: light leans on shadow, dark leans on tinted
  border.
- Background: `bgCardElevated` in both themes. Drop the `c.bgCard` /
  `c.warn` / `c.error` border-recolor branches at lines 869-870 — replace
  with a small status pill at the top of the card ("Building", "Failed",
  "Running") that carries the color, leaving the card itself neutral.
- Card radius 14, padding 16. The current padding is fine; verify the
  internal sections breathe (status row → `agent operation` → actions).

**Status meta lines** (lines 875-915):
- Currently 7+ stacked lines in different colors (`textPrimary`,
  `textSecondary`, `c.success`, `#d1d5db`, `#cbd5e1`, `#7dd3fc`, etc.).
  Collapse into 3 rows max:
  1. **Title row**: `runningProject` (16pt 600 textPrimary) + a small
     status pill on the right ("Building" amber, "Running" emerald,
     "Failed" red).
  2. **Meta row**: `framework · port · hot reload on/off` — single line,
     `textSecondary` 13pt, separated by `·`.
  3. **Target/host row** (only if non-default): `target · this device`
     OR `target · <worker name>`, `textMuted` 12pt. Use `accent` for the
     "target ·" prefix, `textSecondary` for the value — consistent
     dot-separated rhythm with the rest of the app.
- Drop `mode · Hermes bytecode` / `mode · native install` line entirely
  unless it's actionable; promote to a tiny pill next to the status pill
  if it must stay visible. (Hermes vs native is a property of the
  framework + iOS install reason — surface only when the user can do
  something about it.)
- `runningGuidance`, `iosInstallReason`, `worker · online/offline`,
  `remote box · …` — these are all useful but don't all belong on the
  hero card. Move to a **tap-to-expand "details" disclosure** (chevron
  on the card right edge, expand inline). Keeps the card calm by default.

**Agent operation inset** (lines 922-941) — biggest single offender:
- Drop the hardcoded navy/blue palette entirely. Use
  `bg: bgInput`, `border: borderSubtle`, `radius: 10`, `padding: 12`
  in BOTH themes. The kind/status/phase line uses `textSecondary` 12pt;
  the message uses `textPrimary` 13pt; `runtimeFamilyLine` uses
  `textMuted` 11pt.
- Add a small `accent`-tinted icon or dot before "agent operation" so
  the user can scan past it when they don't care. The current header
  text in `#93c5fd` reads like a syntax-highlighted code comment.
- This inset is the live-status ticker, not a console. Treat it like a
  notification card, not a terminal output.

**Current blocker / Start failed insets** (lines 944-977):
- Replace hardcoded reds with `errorBg` + `errorBorder` + `error` text
  tokens. Same shape as the agent-operation inset (radius 10, padding
  12, monospace only on the actual error string, not the title).
- "current blocker" header → `error` 12pt 600. Body → `errorText`
  derived value (use `error` token for both; the existing tokens may not
  have a separate "error text" — rely on `error` and accept slightly
  saturated text).

**Live agent stdout** (lines 989-996):
- Promote font to 11pt (currently 10pt is illegible). Color
  `textTertiary` in light, `textSecondary` in dark — use the existing
  tokens, not hardcoded grey. Cap at 6 lines with a fade-out gradient
  on the bottom (or just hard-clip and add a "View logs ›" link).

**Action buttons** (`cardActions` at line 1248+):
- Establish primary/secondary/destructive hierarchy:
  - **Open in Yaver** = primary. Filled `accent`, white text, flex 2
    (twice as wide as the others). When loading, replace text with
    spinner + "Opening…" — never spinner alone.
  - **Reload** = secondary. `accentSoft` bg + `accent` text + 1px
    `accent + "55"` border. Flex 1.
  - **Stop** = destructive. `errorBg` token bg + `error` text + 1px
    `errorBorder` border. Flex 1, smaller min-width.
- Drop the `#22c55e` (Kermit green) entirely — Yaver's brand is purple
  accent, not green. Reserve green for status indicators only.
- Pressed state: `transform: [{ scale: 0.98 }]` + opacity 0.85.
- Equal vertical padding 11pt across all three (current 8pt feels
  cramped against the surrounding card padding).

**Stop confirmation banner** (lines 1060-1089):
- Replace hardcoded `#0f3a1f` / `#3a1f1f` with `successBg` /
  `successBorder` / `success` (or `errorBg` / `errorBorder` / `error`
  for the failure case). Tokens exist for this.
- Move the banner inside the page padding (currently has its own
  `marginHorizontal: 16` which doesn't match the surrounding 16pt
  container — they happen to align but it's accidental).

**Banner `extra` slot for Reload** (lines 788-815):
- Currently 4 lines: "Go agent 1.99.191" / "Hermes reload ready" /
  "/root" / sometimes a Hermes-not-ready note. Cluster onto 2 lines
  max:
  1. `Go agent 1.99.191 · Hermes reload ready` (Hermes phrase tinted
     `success` if ready, `warn` if not — keep current logic).
  2. `/root` in monospace `textTertiary` 11pt.
- Drop the indent (`marginLeft: 18` from `extra`). Align with the
  banner content baseline.

**"OTHER APPS" section** (line 1093):
- Section title: keep `textMuted` all-caps but bump `letterSpacing` to
  0.5 and add `marginTop: 24, marginBottom: 8` so it visually separates
  from the active card.
- Project cards in the list: same `borderSubtle` + light-mode
  `shadowSm` treatment as Tasks/Projects cards. Consistent across all
  project surfaces.
- Tag pill ("expo", "hermes", "swift", "webrtc"): `accentSoft` bg +
  `accent` text, 11pt 600, radius 6, padding 4×8. Currently they use
  default `s.tag`/`s.tagText` styles — verify these inherit properly
  through the shared visual.

**Empty state**:
- Same redesign as Projects: centered card with icon + title + body +
  primary "Rediscover" button (filled `accent`).
- Stop Discovery button stays as a secondary/destructive variant beneath.
- "Taking longer than usual…" copy gets a `warnBg` callout treatment
  with the existing "Open Devices ›" link as the CTA.

**Preview Target card** (lines 819-862, only when `mobileWorkers.length > 0`):
- Wrap chips in `bgCard` (already does) but inherit the same
  `borderSubtle` rule. Selected chip: `accentSoft` bg + `accent` border +
  `accent` text. Unselected: `bgInput` bg, transparent border,
  `textSecondary` text. Drop the `+ "22"` alpha math and rely on the
  token system.

`tabBarStyle`:
- Phone: `height: 64` (currently 68, marginally too tall — eats keyboard
  shortcut bar space). Drop the `borderTopWidth` to 0 in light mode if
  the bar is `bgTabBar` (which is the page bg in light) — currently the
  border line is the only visual separator AND looks like a stuck divider.
  Replace with `shadowSm` upward shadow.
- Tablet landscape (left rail at `width: 96`): widen to `width: 104`,
  bump icon-row gap, keep right border. The pill background on focus
  matches phone.

## Light vs dark — the rule for this brief

Same gut-check rule as the login brief: screenshot every surface in both
themes, neither should look like an inversion of the other.

Where the redesign explicitly diverges by theme:
- **Cards (`TaskCard`, project card):** `shadowSm` in light only. RN
  shadows on dark RN surfaces blend into the bg — don't ship them.
- **Search row + filter chips:** subtle 1px `borderSubtle` border in light
  mode for definition; in dark mode rely on the `bgInput` ↔ `bg` contrast.
- **Status banner:** `bgCardElevated` in both, but the accent stripe carries
  the connected/connecting/error tint. Don't tint the bg in either theme.
- **Tab bar pill:** `accent + "1A"` (10% alpha) in both. Same color reads
  brighter against dark, softer against light — that's the right behavior
  here, not a bug.
- **Reload action buttons:** Open in Yaver is filled `accent` in both —
  that's the primary CTA, theme-independent. Reload + Stop use token-
  based soft backgrounds (`accentSoft` / `errorBg`) that already adapt
  per theme — don't second-guess them.
- **Reload "agent operation" inset:** must NOT be a dark navy console in
  light mode. Use `bgInput` in both — same surface as the search row on
  Projects, gives a consistent "muted info container" feel.

## Behavior preserved (do not change)

- All `Pressable` / `TouchableOpacity` `onPress` handlers
- `connectionStatus` derivation, `effectiveConnectionState` mapping
- Filter logic, search logic, project category derivation
- Long-press menus on task cards
- Animated entry of task cards (the spring + pulse loops at line 1058+)
- `keyboardShouldPersistTaps`, scroll behavior, FlatList virtualization
- Tab routing (do NOT add or remove `Tabs.Screen` entries)
- Reload tab data wiring: `devStatus` polling, `loadApp`, dev-server
  start/stop/reload/screenshot RPCs, `currentOperation` SSE feed,
  `runtimeFamilyLine`, `mobileWorkers`, `lastGuestCrash` hook. The
  redesign only restyles what's already there — don't re-architect the
  data flow.
- Reload action button onPress handlers: `handleOpen`, `handleReload`,
  `handleStop`, `handleRequestScreenshot`, retry-on-error path

## Verification

For each surface, screenshot all six combinations:

```
phone-light    phone-dark
tabletP-light  tabletP-dark
tabletL-light  tabletL-dark
```

Specifically check:
- The connection banner is no longer the loudest thing on Tasks/Projects
- Task cards in light mode have visible elevation (shadowSm) — they
  shouldn't blend into the page
- Empty Projects state reads as deliberate, not as "the screen failed to
  load"
- The active tab on the tab bar is unambiguously identifiable WITHOUT the
  little 2px line above the icon
- "Active · 8" / "Review · 8" / "Completed · 3" / "Failed" all read
  consistently in both themes; the selected one keeps its status color
- The Reload active project card reads as ONE card (not card + violet
  border + black inset + green-on-green buttons). The "agent operation"
  ticker should look like a notification panel, not a transplanted
  terminal.
- Open in Yaver / Reload / Stop have visibly distinct hierarchy: Open is
  the loudest (filled accent), Reload is medium (tinted), Stop is
  quietest but unambiguously destructive (red-tinted)

## Run

```bash
cd mobile
npm run web                      # quick light/dark via OS
yaver wireless push              # real device (from repo root)
```

Tablet preview: rotate the iPad simulator (Cmd+Right) to verify
landscape rail + portrait single column behavior.

---

## Dark-mode pass — concrete regressions to fix

After a second look at the actual rendered dark-mode screenshots, several
issues are NOT just "could be more elegant" but real visual bugs that
need explicit calling-out. Treat this section as a punch list — each
bullet maps to something visibly broken in the screenshots.

### Banner — extra slot looks like text-selection

In the Reload screenshot, the lines `Go agent 1.99.191` and
`Hermes reload ready` render with a noticeably different background
than the rest of the banner — they look like text that someone
selected with the cursor. Causes:

- The `extra` slot's child views inherit/declare a background that
  diverges from the banner surface (likely `c.bgInput` on a wrapping
  `View` somewhere in `hotreload.tsx` lines 788-815, or a stray
  `backgroundColor` on the `Text` itself).
- Fix: the `extra` slot's container must be `backgroundColor: "transparent"`
  in both themes. Children inherit. No `bgInput`, no `bgCard`, no
  per-line backgrounds. Color is carried by the text alone.

### Banner — device-name typography too heavy

In the dark-mode screenshots, `yaver-test-ephemeral` renders as a
large bold heading on its own line — looks like a page subtitle, not a
banner element. The banner takes ~25% of the phone viewport.

- The `deviceText` style in [RemoteBoxBanner.tsx:156](mobile/src/components/RemoteBoxBanner.tsx) currently
  uses `typography.captionStrong`. That token resolves to a larger
  size than the surrounding banner content suggests it should.
- Fix: device name should be the same size as the "Connected" label
  (12-13pt 600), inline with it, separated by `·`. Remove the line
  break / wrap behavior that's pushing it onto its own row.
- Banner total height target: ≤ 88pt with no `extra`, ≤ 132pt with
  `extra`. Currently it's pushing 180-220pt on Reload because of the
  4-line extra + heading-sized device name.

### Banner — accent stripe invisible in pure-dark

The 3px left-edge accent stripe is barely visible against the
near-pure-black banner bg in dark mode. The stripe is the primary
ambient signal that the banner is "alive" — it has to read.

- Bump stripe width to 4px in both themes, or saturate the dark-mode
  `palette.stripe` to full `#22c55e` instead of the current
  derived/dimmed value.

### "Active state" violet border — repeats THREE times in dark mode

The same anti-pattern appears in three different places:

1. **Reload** — active project card has a violet outer border
2. **Projects** — active project card same violet outer border, with
   a green-tinted inner card stacked inside
3. **Devices** — currently-focused device card (`yaver-test-ephemeral`)
   has a violet outer border + glow

Three different surfaces, same "I am the selected one" treatment, all
fighting for attention simultaneously. Pick ONE:

- **Drop the violet border across the board.** Use a small "Active"
  pill in the card header instead (filled `accent` bg + white text,
  same shape as the existing `Primary`/`Secondary` role pills).
- The card itself stays neutral (`bgCard` + `borderSubtle`) so the
  user's eye lands on the ACTION buttons, not the border.
- This is the same rule we apply to login-screen provider buttons:
  neutral container, brand color in the icon/badge, never paint the
  whole frame.

### Project/devserver action buttons — Kermit green everywhere

Every primary CTA in the dark-mode screenshots is solid `#22c55e`
("Open in Yaver" appears 3+ times in different shapes). Yaver's brand
accent is purple — green is reserved for status indicators (connection
state, success toasts).

- Already called out in the Reload section above. Reiterating here:
  drop the green-button pattern across **Tasks devserver mini-card**
  AND **Projects active project card** AND wherever else it appears.
- Single rule: filled `accent` for primary, `accentSoft` for secondary,
  `errorBg` for destructive. No green-as-CTA.

### "Stop Serving" / "Stop" / "Disconnect" buttons

Three different surfaces, three different red treatments:
- Tasks devserver mini-card: red-tinted "Stop Serving" with red border
- Projects active card: solid-bg "Stop" red button
- Devices header: solid-bg "Disconnect" red button at the top

All three are destructive. They should all use the same
`errorBg` + `errorBorder` + `error`-text pattern. Solid-red buttons
are reserved for the most destructive single action — a "Disconnect
all 3 devices" button shouldn't outweigh anything else on the screen.
Demote it to the same treatment as Stop.

### Projects — emoji buttons "🚀 Ship It" and "📱 Screenshots"

In the Projects active-project card, two action buttons use literal
emoji glyphs as their primary visual ("🚀 Ship It", "📱 Screenshots").
This reads as placeholder UI.

- Replace with `Ionicons`: `rocket-outline` for Ship It, `images-outline`
  for Screenshots. Same size as other card icons (16-18pt). Tint
  `accent`.
- These two buttons are in a 2-col grid below the primary action row.
  Visually they're heavier than they should be (taller cards, larger
  glyphs). Reduce to single-row icon-text pills matching the height
  of the Reload/Stop buttons above them.

### Tasks — orphaned "root" project pill

Below the devserver mini-card, a chip reading "● root" sits on its own
with no context. The user has no way to know it's the active project
context for the filter chips below.

- Wrap with a label: `Project · root` where `Project` is in `textMuted`
  and `root` is in `textPrimary`. Or move the project context INTO the
  filter chip row as a leading non-tappable chip, e.g.
  `[root] Active 8 Review 8 Completed 3 Failed`.
- The leading purple `●` dot is fine but should be `accent`, sized
  6-8pt to match other status dots.

### Filter chips — counts inconsistently rendered

Tasks dark-mode screenshot shows: `Active` (selected, no count visible),
`Review` (no count), `Completed · 94` (count present), `Failed` (clipped).

- Current code (`tasks.tsx` line 3438-3442) ALWAYS appends the count
  when > 0. But "Active" with `count: 0` shows just "Active" — looks
  like a different chip variant.
- Fix: always render the dot-separator format, even at zero:
  `Active · 0`, `Review · 0`, `Completed · 94`. Consistent rhythm.
- Failed chip getting clipped at the right edge: drop the trailing
  action chip group's right padding so the last status chip has
  enough room, or wrap at viewport edge.

### Tab bar — green dots on multiple icons

In the Reload + Tasks + Projects screenshots, multiple bottom-tab
icons show a green dot indicator simultaneously (Reload + Projects
both lit, sometimes Tasks too). The dot signals "dev server running"
on the Reload tab and "matching project context" on Projects — but
having both lit at once is visually noisy and the user can't tell
they mean different things.

- Show the green dot ONLY on Reload when `devServerRunning` is true.
- Drop the dot from Projects (the active-project context is already
  shown by the `Project · root` chip per above).
- Keep the Tasks dot only when there are running tasks (don't repurpose
  it for connection state).

### Devices — pill cacophony

The Devices card alone has 4 different pill treatments visible
simultaneously on one card:
- `Codex` purple-text purple-bordered pill (with star icon)
- `CONNECTED` green-bg green-text pill
- `Yaver public relay` green-bg green-text pill (different padding
  than CONNECTED)
- `Details` purple-text purple-bordered button

Plus action buttons: `Use This Device` (text-only purple), `Make
Secondary` (bordered with empty star), `Make Primary` (bordered with
filled star).

- Collapse to ONE pill design: rounded-pill 24px tall, 8x4 padding,
  `bgInput` bg, `borderSubtle` border, `textSecondary` text. Color
  comes from a leading dot or icon (not the bg), so:
  - Codex → `[★] Codex` with star in `accent`
  - CONNECTED → `[●] Connected` with dot in `success`
  - Yaver public relay → `[●] Yaver public relay` with dot in `success`
- Action buttons: keep ONE shape across all four (Use This Device,
  Details, Make Secondary, Make Primary). Bordered pill, `accent`
  text, `accent + "55"` border, transparent bg. No mixing of
  text-only and bordered.

### Devices — "3 Connected" + "Disconnect" header

Top of Devices: a green-bg "● 3 Connected" pill on the left, a solid
red "Disconnect" button on the right. The green pill is correctly
informational; the red button is action.

- Issue: "Disconnect" is bigger than the green pill, drawing the eye
  to the destructive action when the user opened this tab to MANAGE
  devices, not nuke them.
- Fix: shrink "Disconnect" to a text link with `error` color, or move
  it into a header overflow menu (the kebab/three-dots that shows in
  expo-router headers). The header should be informational by default;
  destructive is one-tap-deeper.

### Devices — invite code row

The `Invite code` text input uses monospace placeholder + a purple
`Join` button on the right. In dark mode the input has no visible
border — it relies entirely on `bgInput` ↔ `bg` contrast.

- Add `borderColor: borderSubtle, borderWidth: 1` in both themes (it's
  not theme-conditional anymore — the input needs a real edge).
- Bump radius to match other rounded inputs (10pt).
- `Join` button: filled `accent` only when the input has 6+ chars;
  disabled-state (`accent + "33"`) when empty. Currently it looks
  active even with an empty input, which is misleading.

### Reload — "Available Apps" section header floats

In the Reload screenshot the `AVAILABLE APPS` all-caps section title
sits in a large empty band between the banner and the first project
card. The first card (`Todo Swift / mobile`) is then clipped at the
top — its title is cut off mid-glyph.

- Two issues stacked: the banner extra makes the banner taller than
  expected, pushing the section title into the viewport, but the
  FlatList content inset isn't accounting for it correctly.
- Fix: cap the banner at 132pt total height (already in the banner
  fixes above). Section title should sit `marginTop: 16, marginBottom: 8`
  with no extra padding above it.
- The clipped first card is a `contentContainerStyle` paddingTop bug —
  the FlatList isn't reserving enough space below the section title.
  Add `paddingTop: 4` (or whatever lines up; verify in simulator).

## Dark-mode rules — codified

For Claude Code implementing the redesign: when in doubt about
dark-mode-specific behavior, apply these rules:

1. **No hardcoded near-white text colors** (`#cbd5e1`, `#dbeafe`,
   `#bfdbfe`, `#86efac`, `#fbbf24`, `#fecaca`, `#fca5a5`, `#7dd3fc`,
   `#93c5fd`). All of these appear in `hotreload.tsx` and `tasks.tsx`.
   Replace with theme tokens (`textSecondary`, `textMuted`, `success`,
   `warn`, `error`, `info`, `accent`).
2. **No hardcoded near-black bg colors** (`#0b1220`, `#15151A`,
   `#0f3a1f`, `#3a1f1f`, `#2a0a0a`). Replace with `bgInput`, `bgCard`,
   `bgCardElevated`, `successBg`, `errorBg`, `warnBg`.
3. **No bordered "selected" state with brand-color full border**
   anywhere. The "I am the active thing" signal is a small pill or a
   left-edge accent stripe — not a 1.5px violet outline that paints
   the whole frame.
4. **No green as a primary CTA color** anywhere, ever. Green = status
   only (success states, online dots, "ready" indicators). Primary
   actions use `accent` (purple).
5. **Banner extra slot is transparent.** Inherits the banner bg.
   Color comes from text colors, never from per-line backgrounds.
6. **One pill design across the app.** Rounded 24pt-tall pill,
   `bgInput` bg, `borderSubtle` 1px border, `textSecondary` text,
   leading dot/star/icon carries the semantic color. Don't mix
   bg-tinted pills with border-tinted pills with text-only chips.

## Devices tab — additional notes

Issues beyond what's covered in the dark-mode pass:

- Top "● 3 Connected" header: keep the green dot + count format. It's
  the right amount of signal. Just shrink the "Disconnect" trailing
  control to text-link weight (see above).
- Active device card (`yaver-test-ephemeral` in screenshots): drop the
  violet border, use a small `Primary ★` pill in the header instead.
  The PRIMARY pill currently exists but reads as "boxed text" — bump
  its prominence so it earns its keep without needing the surrounding
  border.
- Per-device meta lines: `linux ·` / `connected · just now` / "This
  phone is attached to the device." / "Yaver version unknown" — the
  trailing `·` after `linux` is dangling (no second value). Fix the
  joiner so empty values don't leave orphaned separators.
- "This phone is attached to the device." copy reads as awkward English.
  Change to "This is the phone you're using." or drop entirely if it's
  redundant with the device-class signal already shown.
- "Yaver version unknown" should not render at all when the value is
  unknown — just show the version when present. Surfacing the
  not-loaded state as text noise hurts more than it helps.
- Action button cluster (`Use This Device` / `Details` / `Make Secondary`
  / `Make Primary`): collapse to ONE row, ONE shape (per the dark-mode
  pill rule above). When the device IS the primary, hide the "Make
  Primary" button (it's a no-op).
