---
master: claude
doer: codex
---

# Fixed slots and one status vocabulary, wired to the surfaces

## Why this exists

The spine for this already shipped and almost nothing imports it. Three modules
encode the whole design, are tested, green, and have ~3 consumers between them:

- `desktop/agent/autorun.go:284` — `autorunSlotKey(task, seat)`, an agent's stable
  address. Shipped.
- `desktop/agent/autorun_ops.go:162` — `sortAutorunViewsBySlot`, so a status flip
  moves nothing. Shipped.
- `mobile/src/lib/agentStatus.ts` — the one status vocabulary (7 states, 5 hues,
  `pulse`/`hollow` modifiers). Two consumers.
- `web/lib/agentStatus.ts` — same, web side. **One** consumer.
- `mobile/src/lib/agentSlots.ts` — the slot allocator. **Zero** consumers.

The remaining work is mostly `import` statements. Read the header comments in
those files first — they explain the intent better than this file can, and they
are the contract.

**The law, and everything below follows from it:** position is immutable; colour
carries all change. A slot never moves because something *else* changed.

## Ground rules

- Do not invent a new status vocabulary or a new palette. `agentStatus.ts` is
  canonical on both platforms. If a hue seems wrong, change it in `tokens.ts` /
  `globals.css`, never at a call site.
- Never hardcode a status hex. If you are typing `#` followed by six characters
  next to the word `running`, stop.
- Do not add a colour to the palette. Seven states over five hues is deliberate.
- Keep each iteration to one increment that the gate can verify.

## Work

### 1. Adopt `agentStatus.ts` on web — delete the divergent hex maps

Four copies of the status→colour map exist on web and two disagree about what
`running` means. Only the first is canonical:

- `web/lib/agentStatus.ts:43` — canonical. Only consumer is
  `web/app/spatial/lib/fleetStats.ts:26`.
- `web/app/spatial/page.tsx:627` `dotColor()` — `running` is `#10b981` (green).
- `web/app/spatial/vr/TerminalPane3D.tsx:211` — identical literals to the above.
- `web/app/spatial/vr/VRScene.tsx:285` — `running` is `#3b82f6` (blue). Disagrees.

Point all three at `agentStateHex()` / `agentStateVar()` and delete the literals.
Note `#10b981` is not `--success` in either theme, so the spatial palette is a
third independent one — this is a real colour change on screen, and it is the
intended outcome.

### 2. Adopt `agentSlots.ts` — give the mobile session strip real slots

`mobile/src/components/SessionStrip.tsx` has three bugs the allocator exists to
fix. Use `useAgentSlots` + `slotKeyForTask`:

- It never sorts (`:57` keeps server order) and caps at 8 (`:128`), silently
  dropping the tail.
- It evicts finished chips on a 120s timer (`:79-86`) — and `ageSeconds` (`:88`)
  measures from `startedAt`, not from finish, so a task that ran five minutes is
  already past 120s the instant it completes and its chip vanishes on the next
  4s poll. The grace period is granted only to tasks that finish in under two
  minutes. Stop evicting; a finished agent goes `verified` in place.
- It returns `null` when empty (`:126`), collapsing to zero height and shifting
  the whole screen. Render empty slots instead — the unlit key is what makes the
  lit one findable.

### 3. Pin the VR arc to slot keys

`web/app/spatial/vr/VRScene.tsx:130` `PaneArc` sorts by status score (`:137-146`)
then `sorted.slice(0, 3)` (`:151`), and takes its angle from the *sorted index*
(`ANGLES[i]`, `:165`). So a task changing status physically moves through space
even though its React key is stable. Address `ANGLES` by slot index from
`assignSlots`, not by position in a sorted list. Same bug, same fix, in
`RemoteWindowStack` (`:194`) and `StatusStrip` (`:282`).

`agentSlots.ts` is pure and framework-free precisely so the arc can use it —
but **do not import it across packages.** `web/` reaching into
`../../../../mobile/src/lib/agentSlots` fails the gate; web and mobile are
separate TypeScript projects with separate tsconfigs. Port the pure functions
(`assignSlots`, `buildSlots`, `overflowItems`, `Slot`, `DEFAULT_SLOT_COUNT`) to
`web/lib/agentSlots.ts`, exactly as `web/lib/agentStatus.ts` already mirrors the
mobile module. Duplication across the two packages is the accepted shape here —
`agentStatus.ts` set that precedent deliberately. Keep the React binding
(`useAgentSlots`) mobile-only; the arc drives the pure `assignSlots` itself.

### 4. Give `autorun_status` a route, then a screen

Nothing anywhere consumes the autorun loop. It reports iterations, seats, heals,
resources and a final commit to nobody.

- Mobile models it already (`agentStatus.ts:154` `AutorunSession`,
  `agentSignalFromAutorun:189`, both tested) and **cannot call it**:
  `mobile/src/lib/quic.ts` has no `/ops` route. Add one.
- The verb is `autorun_status`, registered at
  `desktop/agent/autorun_ops.go:218`, served only by the generic `/ops` endpoint
  (`httpserver.go:520`). It returns `{"sessions": [...]}`, always an array.
- Then render it. One screen. Use `agentSignalFromAutorun` — do not re-derive.

### 5. Slim the Coding Agents block on the device card

`web/components/dashboard/DevicesView.tsx:3226`. Today every card carries a
PREFERRED label, an agent chip, a Test button, a SUGGESTED badge, a runner
`<select>`, a model `<select>` (or two for OpenCode), a Confirm button, and an
"Other available agents (N)" fold. Mostly the user only ever needs to see their
default agent.

- Card default: the `Coding agents` label and **one** chip — the preferred agent
  with its auth/status. Nothing else.
- Everything else moves behind the card's existing `⋯` menu
  (`DeviceActionsMenu:3998`): add a "Coding agent…" item that opens a modal
  hosting the runner select, the model/provider selects, Confirm, Test, and the
  other-agents list.
- Do **not** put the `<select>`s directly in the `⋯` dropdown — it closes on the
  backdrop button at `:4057` and they will fight it. A modal is the shape.
- Business logic is unchanged: keep `setPrimaryRunner`, `RunnerChipWithTest`,
  `preferredDefaultModelForRunner` and the OpenCode provider catalogue exactly as
  they are. This is a layout move, not a rewrite.

### 6. Stop sniffing strings for runner auth state

`DevicesView.tsx:455` decides a runner needs auth with
`s.includes("needs-auth") || s.includes("needs_auth") || s.includes("unauth") ||
s.includes("login")`. Give it a real discriminated union. The Go wire vocabulary
is five bare strings (`running`, `completed`, `failed`, `stopped`, `stopping` —
`autorun_ops.go:94-197`); the runner health strings are separate. Model them
explicitly rather than guessing at substrings.

## Out of scope

Do not touch: `desktop/agent/autorun*.go` (the spine is correct — read it, don't
change it), the Live Activity native module, push registration, or anything
under `backend/convex/`. Do not add dependencies. Do not change `tokens.ts`
values.

## Definition of done

Say DONE, alone, only when all six sections are complete and verified in the git
log, the gate passes, and no `grep -rn "#[0-9a-fA-F]\{6\}" ` next to a status
word survives in the files listed above.
