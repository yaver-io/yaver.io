# Yaver Mini-Figma — Direct Manipulation for Spec-Driven Apps

> Status: design + P0/P1 prototype. 2026-06-21.
> Scope: a Lovable + Figma hybrid inside the Yaver webui (and, later, the
> mobile app) where a user **chats** to build an app **and** directly
> **selects / drags / edits** widgets in the live preview. Both inputs edit one
> declarative spec. Reuses the existing browser sandbox (`web/lib/sandbox/*`).

This doc is the source of intent. The code is the source of truth — grep before
trusting any claim here (per `CLAUDE.md`).

---

## 1. The thesis (why Yaver can do what Lovable can't)

Lovable manipulates **generated React/Tailwind code**. To let you drag an
element it would have to map a pixel → a JSX node → edit JSX → re-bundle, and
hope nothing else moved. Too brittle — so it falls back to chat for *everything*,
including a 2-second margin nudge. (Observed: a user typing *"add new task is too
close to top navigation"* into Lovable's chat instead of dragging.)

Figma is the inverse: pure direct manipulation, but the output is a **picture**,
not a running app with a database.

**Yaver sits in the gap, structurally.** The preview is not arbitrary code — it
is rendered by a renderer *we own* (`web/lib/sandbox/preview.ts`) from a
**declarative spec** (`PhoneAppSpec`). Therefore:

> **A drag = "reorder/restyle node X in the spec" = a spec patch = re-render.**

That single property makes a Figma-like layer tractable here and impossible in a
code-first tool. The central rule, from which everything else follows:

> **Direct manipulation edits the *spec* (the scene graph), never the pixels and
> never generated code.** Chat and drag become two input modalities over one
> source of truth.

That convergence — *one spec, two editors* — **is** the Lovable+Figma hybrid.

---

## 2. What already exists (the foundation)

| Capability | Where | State |
|---|---|---|
| Declarative app spec | `PhoneAppSpec.screens[]` (`agent-client.ts:7342`) | exists, shallow (§4) |
| Generic renderer, fully owned (~170 lines) | `web/lib/sandbox/preview.ts:18` `RENDERER_SOURCE` | pure DOM → esbuild → iframe |
| Parent↔iframe channel | `web/lib/sandbox/dataBridge.ts` `yaver-sandbox-req/-rep` | tiny, versioned, extensible |
| AI draft (NL → spec) | `web/lib/sandbox/aiDraft.ts` + `gateway.ts` (GLM) | one-shot |
| Local data (sql.js + IndexedDB) | `web/lib/sandbox/localProjects.ts` | working |
| Build/deploy `.yaver.tgz` | `bundle.ts`, `deploy.ts` → `/phone/projects/receive` | working |
| Device frames | `web/components/dashboard/PreviewPane.tsx` | exists, not wired to sandbox |
| Same spec runs on mobile | `mobile/app/run-app.tsx` (RN renderer vs `/data`) | separate renderer, shared contract |

The decisive fact: **the spec is the cross-platform contract.** Web and mobile
are two renderers of one spec — so a spec edit (chat *or* drag) lands on both
surfaces for free.

---

## 3. Open-source landscape (what we reuse, what we avoid)

Repo is **public / FSL** and the Cloudflare web bundle must stay **< 15 MB**
(`CLAUDE.md`). Both constrain dependency choice.

| Project | License | Fit | Verdict |
|---|---|---|---|
| **react-moveable + selecto** (Daybrush) | **MIT** | Figma-style drag/resize/rotate/**snap**/group handles + marquee multi-select, positioned over arbitrary DOM rects | **Core of the overlay handles.** |
| **dnd-kit** (clauderic) | **MIT**, 6 KB core | reorder primitive for the layer-tree panel + host-side lists | **Use for layer tree / list reordering.** |
| **Puck** (puckeditor) | **MIT** | React visual editor that emits a clean **JSON tree**; field-config-driven inspector | **Reference model**, not a dependency (see fork in §6). Borrow the JSON-tree + field-config inspector pattern. |
| **Craft.js** | MIT | lower-level builder; you assemble the editor | reference only; more setup than we need |
| **react-native-gesture-handler + reanimated** | MIT | native 60fps gestures; already in the mobile app | **Mobile drag substrate.** |
| **react-native-draggable-flatlist / reorderable-list** | MIT | end-user drag-reorder lists on mobile | **Mobile runtime drag.** |
| **Penpot** | MPL-2.0 (file-level copyleft, self-host OK) | full Figma alternative; SVG/CSS/HTML/JSON | too heavy to embed; possible *import-a-design* source later |
| **Excalidraw** | MIT, embeddable | wireframe/whiteboard, not a UI/component builder | optional "sketch a layout" mode, not core |
| **tldraw** | **source-available, NOT OSI; production needs a paid license** | great canvas, wrong license for a public/FSL repo | **Avoid** unless we license it. |

**Chosen stack:** our own thin builder = **react-moveable + selecto** (handles) +
**dnd-kit** (layer tree) over the **real iframe renderer**, with **Puck/Craft.js
as reference**. Mobile = **gesture-handler + reanimated** (+ draggable-flatlist
for runtime). This keeps the bundle small, the license clean, and — critically —
keeps one renderer (no builder/runtime drift; see §6).

---

## 4. The gap: the spec must become a widget tree

Today the renderer **hardcodes** the layout: nav-tabs → `<h2>` title → add-form →
table (`preview.ts:62-183`). The spec only says *"kind: list, table: todos,
actions: [Add task]"* — it has **nowhere to record** *"the Add button is above the
list with 8px gap."* So *"move the add button down"* cannot be expressed.

The required evolution: `PhoneScreenSpec` grows an optional **widget tree** —
composable, addressable nodes typed against the registry (§5):

```
screen(todo_list)
 └─ Stack(vertical, gap=12)
     ├─ Header(title="Today", subtitle="1 task left")     node: hdr1
     ├─ QuickAdd(bind=todos, action=create)               node: add1   ← the dragged thing
     ├─ FilterTabs(options=[All,Active,Done])             node: flt1
     └─ List(bind=todos, rowTemplate=TaskRow)             node: lst1
          └─ TaskRow(checkbox=done, label=title, swipe=delete)
```

Each node = `{ id, type, props, bind?, children? }`.

**Backward compatibility is mandatory.** When a screen has *no* explicit tree,
the renderer **derives a default tree** from `kind`+`table` (header→quickadd→
list) — exactly today's layout. Every existing project keeps working and
*becomes editable* the first time it's touched. The full type (additive to
`agent-client.ts`):

```ts
export interface WidgetNode {
  id: string;                       // stable, unique within the screen
  type: string;                     // resolved against the widget registry
  props?: Record<string, unknown>;  // gap, padding, title, variant, hidden…
  bind?: { table?: string; column?: string; mode?: "read" | "write" };
  children?: WidgetNode[];
}
export interface PhoneScreenSpec {
  // …existing fields stay…
  root?: WidgetNode;                // optional; absent → derive default tree
}
```

This spec-tree evolution is the **single load-bearing dependency**. Everything
above it (overlay, inspector, chat patches) manipulates `WidgetNode`s.

---

## 5. The "aware widget" registry (the heart of the request)

> *"a lib that wraps other widgets but is aware of what they are and what the
> user will do and manipulate."*

That is a **Widget Registry**: a typed catalog where each widget declares its
identity, its rendering, and — critically — a **manipulation manifest**. "Aware"
means the overlay never sees raw DOM; it sees registry-typed nodes and reads each
node's manifest to know *what a gesture even means here*. "Wraps but aware" = each
def *renders* via a primitive (DOM on web, RN component on mobile) but *carries
the manifest* so tooling understands its semantics.

```ts
interface WidgetDef {
  type: "Stack" | "Header" | "List" | "Row" | "Field" | "Button"
      | "QuickAdd" | "FilterTabs" | "Toggle" | "Card" | "Fab";
  label: string;
  // inspector controls are GENERATED from this prop schema
  props: Record<string, "spacing" | "text" | "color" | "enum" | "bool" | "ref">;
  bind?: { table: "ref"; column?: "ref"; mode: "read" | "write" };

  // ── DESIGN-TIME: what the BUILDER may do (Figma side) ──
  design: {
    movable: boolean;                 // reorderable within parent
    resizable: "none" | "w" | "h" | "both";
    acceptsChildren?: string[];       // container drop-rules
    restyle: ("gap" | "padding" | "color" | "radius" | "hidden")[];
    rebindable: boolean;              // repoint to another table/column
    editableText: boolean;
  };

  // ── RUNTIME: what the END USER may do ("draggable mode for users") ──
  runtime: {
    reorderable?: boolean;            // drag-to-reorder rows
    swipe?: ("delete" | "complete")[];
    dragCreate?: boolean;             // drag-to-add
    tap?: "navigate" | "toggle" | "edit";
  };
}
```

### Two kinds of "drag" — one manifest, two facets

This is the crux of the user's two follow-ups, made explicit:

- **Builder drag** (design-time, Figma-like): *you* drag QuickAdd down to fix the
  spacing — instead of chatting Lovable. Governed by `design.*`.
- **End-user drag** (runtime): the *app's* user drags to reorder tasks or
  swipe-to-complete — *"I can't simply drag add a new task."* Governed by
  `runtime.*`. Today's renderer is tap-only; this makes drag a **declarable
  widget affordance** the builder can toggle.

Same registry, two seats. "Draggable mode for the builder" and "draggable mode
for the user" are one system viewed from each side.

### Curated core, not everything

Ship ~10–12 defs covering ~80% of CRUD apps: `Screen, Stack, Header, List,
Row, Field, Button, QuickAdd, FilterTabs, Toggle, Card, Fab`. Anything else =
an **opaque `Custom` node**: still selectable and movable as a block, just not
introspectable. That escape hatch is what keeps the system from becoming a cage
(the classic no-code trap).

---

## 6. Overlay architecture (the Figma layer over a live app)

### Fork: iframe-renderer + overlay  vs  host-rendered builder canvas

- **Host-canvas (Puck/Craft.js):** richer editor out of the box, but the design
  canvas is a *different* render path than the deployed/mobile app → **drift**
  between what you design and what ships.
- **Iframe-renderer + overlay (chosen):** the preview is byte-identical to what
  deploys and what `run-app.tsx` shows — **one renderer, no drift**, on-brand with
  Yaver's "one spec → all surfaces" philosophy, and small enough for the 15 MB
  cap.

**Decision: iframe-renderer + overlay.** Use Puck/Craft.js only as design
reference.

### Where the overlay lives — hybrid

- **Inside the iframe:** hit-testing + selection handles. Geometry lives where the
  render lives → no fragile cross-boundary rect streaming on scroll. The renderer
  enters a **design mode**: stamps `data-ynode="<id>"` on each node, captures
  clicks/drags locally, draws the selection outline, and reports the node id + rect
  to the parent.
- **In the parent host:** the **inspector**, the **layer tree**, and **chat**.
  Normal React, full UI freedom, not sandbox-limited. (react-moveable can also run
  here, positioned over the iframe using reported rects, once we want resize/snap.)
- **Connected by the existing protocol**, extended with design ops (additive,
  same `source`-tag discipline as `dataBridge.ts`):

```
host → iframe:  { source:"yaver-design-cmd", op:"setMode", mode:"edit"|"run" }
iframe → host:  { source:"yaver-design-evt", op:"select", nodeId, kind, rect }
iframe → host:  { source:"yaver-design-evt", op:"moved",  nodeId, newParent, newIndex }
host → iframe:  (re-render with patched spec — same path as a data refresh)
```

A drag's lifecycle: user drags QuickAdd → iframe posts `moved` → host applies a
**spec patch** (`reorder add1 within stack`) → host re-renders the iframe → handles
re-attach. The patch flows through the **same pipeline** an AI chat edit uses.
Deterministic, instant, **zero LLM tokens** — the opposite of the screenshot.

---

## 6.5 Isolation & the two-mode model — no collision with the user's own drag

This is the sharpest correctness question: **what if the app the user is building
itself uses drag-and-drop** (a Jira/kanban board, a sortable list)? Won't our
builder's drag collide with the app's drag?

**The framing that dissolves most of the problem: Yaver authors the app.** The
target user is a **normie**, and the whole app is authored **inside the sandbox**.
The app is not hand-written React with a foreign `dnd-kit` — it is rendered by
**our** renderer from **our** registry. So the app's drag (kanban card-move,
list reorder, swipe) is **our** `runtime.*` affordance that we emit and control.
Builder-drag and app-drag are therefore **two facets of one system we wrote**, not
two libraries fighting — and they are coordinated by a single mode flag.

### The two modes are mutually exclusive

| | RUN mode ("use the app") | DESIGN mode ("edit the app") |
|---|---|---|
| Who drives | end user | builder |
| App's own drag (kanban/reorder/swipe) | **active** (if builder enabled it) | **frozen** |
| Design instrumentation | **none attached** | Design Glass owns all input |
| Result | app behaves exactly as shipped | app is inert; you manipulate widgets |

Because design instrumentation is **completely absent** in run mode, the app's
own gestures can never be intercepted. Because the **Design Glass** (a single
capture surface above the app) owns all input in design mode, the app's gestures
never fire while editing. **One flag flips between them — collision is impossible
by construction**, not by careful coexistence.

### Three layers of isolation (defence in depth)

1. **Mode gate** — run vs design are mutually exclusive; instrumentation exists in
   exactly one of them. (Primary guarantee.)
2. **The Design Glass** — the "another layer" you intuited: a transparent
   full-surface capture div, present only in design mode, that hit-tests widgets
   via `elementsFromPoint` and reports select/drag to the host. The app beneath
   receives no events. (Shipped in `preview.ts`.)
3. **The iframe realm boundary** — on web the app runs in a sandboxed iframe (its
   own JS realm, its own event loop). The builder's host-side overlay/inspector
   lives in the **parent** document. Even if the app bundled its *own* `dnd-kit`,
   it is a different realm from anything the builder uses — they cannot share
   listeners. Plus our channels are **namespaced** (`yaver-sandbox-*` for data,
   `yaver-design-evt` for design) so postMessage traffic never crosses.

### "Yaver draggable mode — or not" is a builder choice

Whether the finished app has end-user drag is a **per-widget toggle** the builder
flips in design mode (`runtime.reorderable` / `runtime.swipeDelete`, shipped as
inspector checkboxes; `Board.runtime` for kanban). A normie never sees "dnd-kit"
— they see *"let users drag to reorder? [✓]"*. The library underneath is ours and
invisible. On web we don't even ship a DnD library into the generated app — our
renderer implements the runtime gestures directly (pointer events), so there is
literally no third-party DnD in the app to collide with.

### The only real collision risk: the custom-code escape hatch

If a power user drops to a `Custom` opaque node containing hand-written code with
its own drag, that code lives **inside the iframe app realm** and runs only in run
mode — still isolated from the builder by layers 1–3. In design mode the Custom
node is selectable/movable **as a block** (the glass treats it opaquely); we don't
reach inside it. That's the correct, honest boundary: we manipulate the spec we
own, and treat foreign code as a sealed box.

---

## 7. Convergence: chat ⇄ drag ⇄ inspector, one spec

```
              ┌──────── PhoneAppSpec (single source of truth) ────────┐
 Chat (GLM) ──┤  NL → spec patch (aiDraft)                            │
 Overlay   ───┤  gesture → spec patch (registry manifest)            │── history / undo
 Inspector ───┤  form field → spec patch                             │── re-render web + mobile
              └───────────────────────────────────────────────────────┘
```

Chat for *generative/structural* ("add a Done filter"), drag for *spatial/
cosmetic* ("8px more gap") — each where it's actually faster. When you drag, chat
can narrate/learn; when AI proposes, you can grab a handle and refine. One patch
queue + one undo stack — never two writers racing.

---

## 8. Cross-platform (web + mobile)

- **Free:** spec + registry are platform-neutral. Any edit lands on web preview
  *and* `mobile/app/run-app.tsx`. Already true today.
- **Not free:** the **overlay** is per-platform. The web DOM overlay can't be
  reused on mobile — RN needs a native design mode (gesture-handler + reanimated:
  long-press select, drag handles). Recommendation:
  1. **Web builder overlay first** (where building happens).
  2. **Mobile gets *runtime* drag affordances first** (end-user reorder/swipe via
     draggable-flatlist) — high value, far cheaper than a full mobile Figma.
  3. **Mobile builder overlay** only if validated.

---

## 9. North star — Yaver's own UI as a Yaver app (dogfooding)

> *"we can write our code in a figma-style editable way as well."*

End-state: the Yaver webui and mobile UI are themselves authored from the same
aware-widget registry, so Yaver is editable inside its own mini-Figma. This is
aspirational and **explicitly later** — it only becomes safe once the registry
covers enough widgets and the opaque-`Custom` fallback is rock-solid. Sequencing
it early would hold the whole product hostage to the editor. Treat §1–§8 as the
product; §9 as the horizon.

---

## 9.5 MCP & prompting layer (AI drives the same spec — collision-free by design)

The AI must be able to make the same edits a human makes — *"move quick-add below
the list", "let users drag to reorder tasks", "hide the nav"*. The clean way:
**the AI emits a structured design patch against the spec**, exactly like the
overlay and inspector do. It never touches the browser, the glass, or any DnD —
so the collision question simply doesn't arise for the AI path (it mutates data,
not the DOM).

### Two surfaces, one patch shape

A **design patch** is the lingua franca:

```ts
type DesignPatch =
  | { op: "set";    nodeId: string; props: PhoneNodeUi }   // hidden/marginTop/title/reorderable/swipeDelete
  | { op: "move";   nodeId: string; beforeId: string|null } // reorder in the layout
  | { op: "enable"; nodeId: string; affordance: "reorder"|"swipe" }
```

- **Browser (sandbox, normie path):** an NL→patch helper (`designChat.ts`, the
  design-time twin of `aiDraft.ts`) calls the existing GLM **gateway**
  (`gateway.ts`, session-token auth) with a constrained system prompt that returns
  *only* a `DesignPatch[]`. The host validates and applies it through the **same**
  `applyDesign` pipeline as a drag — so chat, drag, and inspector share one undo
  stack. This is the "yaver prompting that uses these libraries", and it's
  testable today.
- **Agent (connected projects):** new MCP verbs on the desktop agent operating on
  the real project's `app.yaml` design block (mirrors `setLocalDesign`):
  - `phone_project_design_get { slug } → PhoneDesign`
  - `phone_project_design_patch { slug, patch: DesignPatch[] } → PhoneDesign`
  - `phone_project_widget_list { slug, screen? } → { nodeId, kind, props }[]`
  These are pure spec mutations — deterministic, auditable, and (per the privacy
  contract) carry no row data or paths. They belong next to the existing
  `phone_project_*` verbs (`desktop/agent/phone_backend.go` / `ops_*.go`).

### Why this is the right seam

The AI shouldn't "puppeteer the mouse" over a live canvas (brittle, racy, and it
*would* reintroduce collision worries). It should speak **patches** to the spec.
The renderer re-renders deterministically; a human can grab a handle and refine;
the overlay can drag the same node. All three writers funnel through one patch
applier and one history — which is exactly the convergence in §7, now including
the AI. Status: patch shape + browser `designChat` are specified here and are the
next implementation step; the Go MCP verbs are designed, not yet built.

---

## 10. Risks (honest list)

1. **Spec-tree evolution is load-bearing** — version it; always derive a default
   tree so nothing breaks.
2. **Structured vs freeform tension** — the opaque-`Custom` escape hatch is
   non-negotiable, or you've built a cage.
3. **Geometry** — keep handles inside the iframe; don't stream rects on scroll.
4. **AI-edit vs human-drag conflicts** — one patch queue + undo, not two writers.
5. **Mobile gesture collision** — design-select vs reorder vs scroll all fight for
   touch; mode-gating mandatory.
6. **Persistence/deploy** — a spec edit must round-trip through `bundle.ts` so a
   dragged layout actually ships in the `.yaver.tgz`, not just the live preview.

---

## 11. Phased plan

- **P0 — Make widgets addressable.** `PhoneScreenSpec.root?` widget tree (optional);
  renderer walks it + stamps `data-ynode`; auto-derive default tree. Substrate only.
- **P1 — Select + inspector (web).** Design mode in the renderer: click → `select`
  → host inspector → edits emit a spec/ui patch → re-render. Kills the screenshot's
  pain (select QuickAdd, bump its gap).
- **P2 — Drag-reorder (builder)** via react-moveable/selecto → `moved` patches;
  **persist** ui/tree to IndexedDB + bundle.
- **P3 — Registry as engine.** Formalize `WidgetDef`s; inspector controls + allowed
  gestures generated from the manifest. Curated core + opaque-`Custom`.
- **P4 — Convergence.** Chat (`aiDraft`) + overlay through one patch/undo pipeline;
  layer-tree panel.
- **P5 — Runtime drag affordances** (end-user reorder/swipe) from `runtime.*`, on
  web + `run-app.tsx`.
- **P6 — Mobile builder overlay** (if validated).
- **P9 — Dogfood:** Yaver UI as a Yaver app (§9).

### Shipped with this doc (web builder, P0–P5 web slice — tsc green, uncommitted)

Additive, non-breaking, behind a **Design** toggle in the Browser Sandbox preview:

- `web/lib/agent-client.ts` — `PhoneNodeUi` / `PhoneDesign` types; `PhoneAppSpec.design`.
- `web/lib/sandbox/preview.ts` — `data-ynode` stamping; the **two-mode model**;
  the **Design Glass** isolation layer (select + drag-reorder, `elementsFromPoint`
  hit-test, namespaced `yaver-design-evt`); layout ordering; override application
  (marginTop / hidden / title); **runtime swipe-delete** in run mode when the
  builder enabled it.
- `web/lib/sandbox/designBridge.ts` — parent listener for `select` + `moved` events.
- `web/lib/sandbox/widgets.ts` — `WidgetDef` registry incl. runtime toggles +
  a declared `Board` (kanban) entry; inspector controls generated from it.
- `web/lib/sandbox/localProjects.ts` — `getLocalDesign` / `setLocalDesign`:
  **persist** design into `app.yaml` → survives reload AND **ships in the .tgz**.
- `web/lib/sandbox/designChat.ts` — NL → validated `DesignPatch[]` via the GLM
  gateway (the prompting path).
- `web/components/dashboard/BrowserSandbox.tsx` — Design toggle, click-to-select +
  drag-reorder, registry-driven inspector (spacing / hide / title / order ↑↓ /
  end-user drag toggles), **undo/redo**, and the **AI layout** input — chat, drag,
  and inspector all flow through one `applyDesign` + history.

**Remaining:** Go MCP verbs (`phone_project_design_*`, §9.5 — designed, not built);
full `Board`/kanban renderer (P5); the full `WidgetNode` sub-tree beyond top-level
blocks (P3); mobile builder overlay (P6); dogfood (P9).

---

## Sources (OSS license/fit, accessed 2026-06-21)

- Puck — https://github.com/puckeditor/puck (MIT)
- tldraw license — https://tldraw.dev/community/license (source-available, paid for prod)
- dnd-kit — https://github.com/clauderic/dnd-kit (MIT)
- react-moveable — https://github.com/daybrush/moveable (MIT) + selecto
- react-native-draggable-flatlist — https://github.com/computerjazz/react-native-draggable-flatlist (MIT)
- Penpot — https://github.com/penpot/penpot (MPL-2.0)
- Excalidraw — https://github.com/excalidraw/excalidraw (MIT)
