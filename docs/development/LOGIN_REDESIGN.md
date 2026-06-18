# Login screen — visual redesign brief

Make the sign-in screen feel like a modern consumer app login (Linear, Raycast,
Vercel) instead of a stacked button list. Phone, tablet-portrait, and
tablet-landscape must all feel deliberately designed, and both light + dark
themes must look first-class — neither should read as "the other one
inverted."

## File scope

- **Edit only** `mobile/app/login.tsx`. All theming flows through
  `useColors()` from `mobile/src/context/ThemeContext.tsx` (do not hardcode
  hex). Layout decisions read from `useResponsiveLayout()` in
  `mobile/src/hooks/useResponsiveLayout.ts` (`isTablet`, `isTabletLandscape`,
  `width`).
- Do **not** touch `mobile/src/constants/colors.ts` or
  `mobile/src/theme/tokens.ts` — if a color is missing, pick the closest
  existing token (`accent`, `accentSoft`, `bgCard`, `bgCardElevated`,
  `border`, `borderSubtle`, `textPrimary`, `textSecondary`, `textMuted`)
  rather than introducing a new one.
- Auth handlers (`handlePasskeySignin`, `handleAppleSignIn`, `handleOAuth`,
  `handleEmailSubmit`, `handlePasskeySignup`), state shape, navigation, and
  deep-link `useEffect` are **frozen** — only the JSX and `StyleSheet` change.
- Keep all six providers (passkey, Apple, Google, GitHub, GitLab, Microsoft,
  Email). Don't drop or hide any.

## What's wrong with the current screen

Reference: see screenshot supplied with this brief. Current issues:

1. Seven full-width buttons of identical weight — no visual hierarchy. Eye
   has no entry point.
2. Wordmark is `fontWeight: 800` at 48pt with `letterSpacing: -1` — reads as
   a heading inside a form, not a logo.
3. Light mode: page bg and button bg are both near-white, so the buttons
   look like text rows. There is no card / surface contrast.
4. Dark mode: works but is flat — no accent glow, no depth on the hero
   passkey CTA.
5. Tablet portrait just centers a 440pt phone column on a 1024pt canvas;
   acres of empty space, no sense of "tablet design."
6. Tablet landscape splits left/right but the right column is the same
   stacked phone buttons, so it still reads as a phone UI.

## Design direction

### Hierarchy (all form factors)

1. **Hero**: Yaver wordmark + tagline.
2. **Primary CTA**: passkey button — visually elevated, brand-tinted, the
   only filled/glowing element above the fold. (Hide cleanly when
   `passkeySupported` is false; do not leave a gap.)
3. **Provider cluster**: Apple, Google, GitHub, GitLab, Microsoft as a
   visually-equal group of secondary buttons. Group them tighter than they
   are now (8pt gap, not 12).
4. **Tertiary**: "Continue with Email" sits below a thin divider — it's an
   escape hatch, not a peer of OAuth. When expanded, the form replaces the
   button in place (current behavior is fine).
5. **Footer**: terms + privacy + version, muted and small.

### Provider buttons

- Use the **brand color** of each provider's logo at full saturation
  (Apple = `textPrimary`, Google's "G" stays multi-color via the existing
  Ionicon glyph, GitHub = `textPrimary`, GitLab orange `#FC6D26`,
  Microsoft `#0078D4`). Buttons themselves stay neutral
  (`bgCard` + `border`); only the icon carries brand color. This is the
  Vercel/Linear pattern and reads more refined than monochrome icons.
- Reduce vertical padding from 14 to 12, radius from 12 to 10. Buttons feel
  lighter.
- Add a subtle 1px border using `borderSubtle` (not `border`) in light mode
  so the buttons don't disappear into the page bg.
- Pressed state: `transform: [{ scale: 0.985 }]` + opacity 0.85, not just
  opacity 0.7 (current). 60ms feels snappier.

### Passkey hero CTA

- Filled background `accentSoft` (light mode) / `accent + "1F"` (dark
  mode), border `accent + "55"`, text + icon `accent`.
- Add a soft shadow only in light mode using the existing `shadowSm`
  token — gives the hero some lift.
- Icon: keep `key-outline` but bump to 20.
- Text: "Sign in with passkey" stays. Loading state: replace text with
  "Waiting for passkey…" + a small `ActivityIndicator` to the left of the
  text (not replacing it — current behavior loses the affordance).

### Wordmark + tagline

- "Yaver" → `fontWeight: 700`, `letterSpacing: -1.5`, size 44 (phone) /
  56 (tablet-portrait) / 64 (tablet-landscape). Slightly thinner is more
  premium than the current 800.
- Tagline: keep "Your AI coding assistant, everywhere." Use `textSecondary`
  at 15pt phone / 17pt tablet, `lineHeight: 22`.
- Optional: tiny mark above the wordmark — a 32pt circular brand glyph
  using `accentSoft` background + `accent` initial "Y", letter-spaced. Only
  add if it doesn't bloat the header.

### Background

- Phone: solid `bg`. No gradient — keep it calm.
- Tablet-portrait: `bg` page with a single elevated card
  (`bgCardElevated`, radius 24, `shadowSm`, max-width 480, padded 32) that
  contains everything from wordmark through email button. Footer sits
  below the card on the page, not inside it. This is what makes tablet
  portrait actually feel designed for tablet.
- Tablet-landscape: keep the side-by-side split but give the left brand
  pane a subtle visual weight — large wordmark, tagline, plus a third line
  ("Sign in to start coding from anywhere." or similar — keep the existing
  tagline if you don't want to invent copy). Right pane is the card from
  tablet-portrait, vertically centered, max-width 420.

## Light mode vs dark mode

Both must look intentional. Quick gut check: screenshot both, neither
should look like the other one with channels inverted.

### Dark mode

- Page `bg` (deep), buttons on `bgCard` (one step up). Borders
  `borderSubtle` so buttons read as subtly raised, not outlined.
- Passkey CTA gets a soft accent glow (use `shadowMd` if it works, or skip
  shadow and rely on the tinted background — RN shadows on dark are
  unreliable).
- Tagline `textSecondary`, footer `textMuted`.

### Light mode

- Page `bg` is the slightly off-white surface, card is pure `bgCardElevated`
  with `shadowSm` — this is where the depth comes from. Pure white-on-white
  is the current failure mode; avoid it.
- Borders `border` (one notch stronger than dark) so the provider buttons
  remain legible against the card.
- Apple "" mark: `textPrimary` (near-black). GitHub mark: same. GitLab and
  Microsoft keep their brand colors.

## Layout specifics

| Form factor              | Container                           | Wordmark | Provider gap | Card  |
|--------------------------|-------------------------------------|----------|--------------|-------|
| Phone                    | full-width, 24pt h-padding          | 44pt     | 8pt          | none  |
| Tablet portrait          | centered card, 480 max-w, 32pt pad  | 56pt     | 10pt         | yes   |
| Tablet landscape         | 2-pane row, brand left + card right | 64pt     | 10pt         | right |

Tablet detection: `layout.isTablet` and `layout.isTabletLandscape` from
`useResponsiveLayout()` — already wired. Don't add a new breakpoint hook.

## Email form (expanded state)

When `showEmailForm` is true:

- Keep the current divider but use `borderSubtle` for the line and
  `textMuted` for the "email" label at 12pt.
- Inputs: same `bgInput` token (don't reuse `bgCard` — there's a dedicated
  input surface), border `borderSubtle`, radius 10 to match buttons,
  vertical padding 13.
- Submit button: filled `accent`, white text. Reuses the same height as
  provider buttons for consistency.
- Toggle ("Don't have an account?") and forgot-password link: `textMuted`
  for the prefix, `accent` for the actionable phrase.

## Footer

- Two lines, `textMuted`, 12pt and 11pt. Terms/Privacy links use `accent`
  with no underline (it's already styled that way; keep it).
- Version: `v{Constants.expoConfig?.version ?? "1.0.0"}` — keep it. Drop
  the explicit `opacity: 0.6` and rely on `textMuted` so it adapts to
  theme.
- Tablet landscape: footer is centered along the bottom edge, full-width
  under both panes (already wired — keep that behavior).

## Accessibility / behavior — must preserve

- All `Pressable`s keep their existing `onPress` handlers verbatim.
- All loading states (`isLoading`, `passkeyLoading`) keep their existing
  disabled / opacity behavior; don't introduce a new spinner pattern.
- `keyboardShouldPersistTaps="handled"` on the ScrollView stays so taps on
  buttons dismiss the keyboard cleanly when the email form is open.
- `KeyboardAvoidingView` stays.
- All error messages (`emailError`) render in the same place they do now.

## Out of scope

- No new dependencies. No `expo-linear-gradient`, no `react-native-svg`
  custom logo, no animation libs. Use what's already imported.
- No new auth flows. No "magic link", no SSO selector, no SSO email
  domain check. Just visual refresh.
- No copy changes beyond what's listed above. Marketing tagline stays.
- Don't touch `mobile/app/two-factor-challenge.tsx`,
  `mobile/app/oauth-callback.tsx`, `web/app/auth/page.tsx`, or any other
  auth surface — this brief is mobile login only. (Web login redesign is
  a separate brief.)

## How to verify

After implementing, snapshot all six combinations and eyeball:

```
phone-light    phone-dark
tabletP-light  tabletP-dark
tabletL-light  tabletL-dark
```

For each: hero passkey CTA must be the visually loudest element above the
fold, the page must not look like a stack of equal-weight rows, and the
two themes must each look intentional rather than mechanically inverted.

Run from repo root:

```bash
cd mobile
npm run web                # quick browser preview, light/dark via OS
# or, real device:
yaver wireless push        # WiFi-paired iPhone
```
