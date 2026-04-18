# Mobile UI Generation Task

## Title

Mobile-first project builder and UI-generation intake for Yaver

## Problem

The existing mobile "New Project" flow was functionally a thin wrapper
over the desktop agent's terminal-style wizard. That was not enough for
the product direction we want:

- a user should be able to define and start a real mobile app from the
  phone
- the builder should collect product-shaping inputs, not just infra and
  repo settings
- the generated mobile starter should reflect those inputs visibly
- the flow should support non-developers and full-stack developers alike
- optional decisions should be easy to skip without breaking the path to
  generation

Before this task, the mobile wizard had these major gaps:

- no app-template choice
- no supported-language intake
- no mobile navigation structure intake
- weak palette capture: only primary and accent, no fuller palette
- no design-reference intake for Figma, Canva, or screenshots
- OAuth was asked only as raw booleans with weak guidance
- generated Expo starter was just a centered label, not a mobile-first
  scaffold
- mobile UI felt like a remote shell, not a phone-native builder
- final confirm/generate behavior was fragile in the mobile screen

## Product Goal

Turn the mobile builder into a proper phone-first onboarding and app
definition flow that captures enough information to generate a more
useful mobile scaffold and to guide later agent-driven UI generation.

The desired user experience:

1. The user opens `New Project` from mobile.
2. The app asks for product inputs in a comfortable survey-like flow.
3. The user can skip individual questions or fast-forward with defaults.
4. The generator produces a scaffold whose mobile starter already
   reflects the chosen direction.
5. OAuth providers are scaffolded as stubs and the user is told exactly
   what secrets still need to be filled manually.

## External Reference Patterns

This task is informed by patterns that current app-building tools expose:

- FlutterFlow Designer:
  prompt-driven generation, navigation-aware screens, import path from
  design tools
- FlutterFlow Designer import:
  turning visual references into screens instead of re-entering all
  design intent manually
- Power Apps template flow:
  start from templates instead of a blank app
- Power Apps Figma import:
  use a design link as generation input
- Canva app integration surface:
  a plausible future path for bringing board-like design artifacts into
  the builder

Implication for Yaver:

- Yaver should collect design intent up front
- Yaver should treat screenshots / Figma / Canva as first-class inputs
- Yaver should make navigation a concrete, early decision
- Yaver should feel phone-native when run from the phone

## Scope

### In scope

- extend the shared project wizard contract on the agent
- add product/UI questions beyond infra questions
- redesign the mobile builder UI around grouped, skippable survey steps
- add better defaulting behavior for optional questions
- improve the generated Expo starter so it visibly reflects inputs
- clarify OAuth as "stub now, fill secrets later"
- support fuller color-palette capture

### Out of scope for this task

- true Figma API ingestion
- true Canva API ingestion
- screenshot upload and parsing pipeline
- automatic OAuth provider registration
- deep template-specific screen generation beyond the starter preview
- persistent resumable wizard sessions across agent restarts
- web dashboard parity for the richer intake flow

## UX Requirements

### Mobile survey requirements

- each step should feel like a single clear decision
- progress should be visible
- the user should be able to skip a single item
- the user should be able to default the rest of the flow
- choice questions should render as touch-friendly cards
- color questions should offer quick swatches plus manual hex entry
- the screen should summarize key answers as the flow progresses

### Product-intake questions

The builder should collect:

- app name
- slug
- description
- tagline
- app template
- supported app languages
- domain
- primary color
- secondary color
- accent color
- surface color
- visual tone
- web/mobile/backend/landing inclusion
- web/mobile/backend stack choices
- mobile nav style
- mobile nav item count
- mobile nav labels
- design source
- design reference URL
- design notes
- OAuth provider choices
- payments provider
- release identifiers
- git host details

### OAuth requirements

- provider selection should stay in the wizard
- generated output should clearly say auth is scaffolded only
- setup docs should tell the user to add real client IDs and secrets
- no false impression that provider creation is automated

## Generator Requirements

The generated project should carry forward builder choices into:

- README product defaults section
- shared constants/types
- setup documentation
- Expo starter UI

The generated mobile starter should show:

- brand palette
- template label
- chosen or defaulted nav items
- supported languages
- auth provider stubs
- design-reference metadata

## Implementation Plan

### Phase 1: Extend wizard contract

Files:

- `desktop/agent/project_wizard.go`
- `desktop/agent/project_wizard_cmd.go`

Work:

- add new wizard questions for template, languages, nav, palette, and
  design references
- update skip logic so conditional questions disappear when not needed
- add helpers for CSV parsing, default nav labels, auth provider lists,
  and shared formatting
- make CLI quick mode aware of the new wizard fields

### Phase 2: Upgrade mobile builder UI

Files:

- `mobile/app/(tabs)/newproject.tsx`
- `mobile/src/lib/quic.ts`

Work:

- fetch the wizard question catalog
- group steps into sections
- add progress UI
- add summary chips
- add choice cards
- add quick color swatches
- add `Skip this`
- add `Use defaults for rest`
- fix final confirm/generate behavior

### Phase 3: Improve generated output

Files:

- `desktop/agent/project_wizard.go`

Work:

- enrich README with product defaults
- add setup guide section for design handoff
- strengthen OAuth setup copy
- expose shared constants for languages/nav/template
- replace the placeholder Expo screen with a richer starter shell

## Implemented in this pass

### Wizard contract

Added these questions:

- `app_template`
- `supported_languages`
- `secondary_color`
- `surface_color`
- `mobile_nav_style`
- `mobile_nav_count`
- `mobile_nav_labels`
- `design_source`
- `design_reference_url`
- `design_notes`

Conditional logic was updated so:

- mobile nav questions are skipped when mobile is off
- design reference URL is skipped when the source is `prompt-only`
- existing conditional branches still work

### Mobile UI

The mobile builder was rewritten from a basic question runner into a
survey-style screen with:

- section labels
- progress bar
- answer summary pills
- touch-friendly choice cards
- color swatches
- single-step skip
- "use defaults for rest"
- improved generated-state result screen

The confirm/generate path was also fixed so mobile no longer depends on
an unreliable terminal-like assumption about the last step.

### Generated scaffold

The generated output now includes:

- richer product defaults in `README.md`
- design handoff section in `SETUP.md`
- clearer OAuth "stub now, fill secrets later" copy
- shared constants for app template, languages, and nav items
- a more useful Expo starter screen that reflects palette, nav,
  languages, auth stubs, and design intake metadata

## Implemented in follow-up pass: Design Mode

The mobile app now also has a first real Design Mode surface:

- hidden route + entry from More and New Project
- live Figma import using a file/frame URL and PAT
- local secure storage for the Figma access token
- imported design summary with preview image, layers, colors, and text
- optional AI-generated implementation brief using the phone's stored
  OpenAI-compatible key
- one-tap handoff of the imported design + brief to the paired dev
  machine as a coding task

Expanded further:

- screenshot/mockup import from the phone photo library
- Canva or generic design-link references as first-class design inputs
- image-aware AI brief generation using the imported screenshot as
  multimodal context
- provider-aware reference handling for Canva, Framer, Miro, Dribbble,
  Behance, and generic links
- direct structured-plan generation on mobile for navigation, screens,
  shared components, integrations, and build order
- remote handoff now carries that structured plan alongside the imported
  design and optional brief

This is intentionally the first real integration slice, not the final
design-tool story. It proves that Yaver mobile can act as a bridge from
design artifact to implementation task, instead of only collecting text
prompts.

## Files Changed

- `desktop/agent/project_wizard.go`
- `desktop/agent/project_wizard_cmd.go`
- `mobile/src/lib/quic.ts`
- `mobile/app/(tabs)/newproject.tsx`

## Verification

Completed:

- `npx tsc --noEmit` in `mobile`
- `go test ./... -run TestDoesNotExist` in `desktop/agent`

## Remaining Follow-ups

### High value

- add first-class Figma/Canva URL validation and copy specific to each
  provider
- use template choice to generate deeper starter IA and screens, not
  just nav labels and starter metadata
- add web dashboard parity for the richer builder
- turn provider-specific references into deeper imports where APIs allow
  it, not only planning context

### Medium value

- persist in-flight wizard sessions locally on mobile for resume
- add richer color-palette presets instead of only swatches
- preview nav icons, not just labels
- add template-specific default auth and payments suggestions

### Longer-term

- connect screenshot ingestion to a visual analysis pipeline
- connect Figma/Canva references to an import or conversion path
- generate starter assets like onboarding slides and store screenshots
- add a post-generation "refine this app" loop using the captured
  builder metadata as prompt context

## Acceptance Criteria

This task is complete when:

- the agent wizard supports mobile-product inputs, not just infra inputs
- the mobile builder feels like a survey, not a shell
- the user can skip single steps and default the rest
- palette capture includes more than primary plus accent
- the flow captures languages, nav structure, and design references
- OAuth is scaffolded as stub-only with explicit follow-up guidance
- generated mobile starter visibly reflects the captured inputs

## Notes

This task intentionally prioritizes better intake and better defaults
over full automation. The immediate goal is not "import and recreate any
design artifact automatically." The immediate goal is to capture enough
product and visual intent from a phone so later generation passes have a
real contract to work from.
