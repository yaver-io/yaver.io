# Schema-Driven Panels — native UI for any verb, without a hand-written View

**Status:** design + build scope (2026-07-06). No code lands from this doc alone.

**One line:** turn the JSON Schema that every `ops` verb already publishes into
auto-generated **native** panels on web, mobile, glass, and watch — so a tool
gets a real button/form on every surface without anyone writing a `View.tsx`
for it.

This is the differentiator half of [Connect Your Project](./yaver-connect-your-project.md).
The conversational surfaces already work today for free; this makes the *native*
surfaces work too.

---

## The gap this closes

Two facts from the current code, side by side:

1. **The schema already exists and is already published.** Every verb registers
   an `opsVerbSpec` carrying a machine-readable `Schema map[string]interface{}`
   (`desktop/agent/ops.go:131`). `/ops/verbs` emits it verbatim as `payload`
   for every verb (`desktop/agent/ops_http.go:90`), and the `ops_verbs` MCP tool
   exposes the same. There are ~160 registration sites across ~95 `ops_*.go`
   files — all already carrying schemas.

2. **No UI reads it.** Every web panel is a hand-written `View.tsx` (58 of them,
   wired into a ~50-entry string-union in `web/app/dashboard/page.tsx`). Every
   mobile feature is a hand-written `*Client.ts` + screen (21 of them). A repo-wide
   search for a schema-to-form renderer (`SchemaForm`/`autoForm`/`renderSchema`)
   returns **nothing**. The schema is consumed only by AI agents.

So: the contract for "render a control for this tool" is already emitted on the
wire — it just terminates at the agent instead of at a screen. This build points
a renderer at that same feed.

---

## Design

### Data source — no new backend

The renderer consumes the **existing** `/ops/verbs` feed. Each entry:

```json
{
  "name": "mfg_rfq_import_bom",
  "description": "Import a BOM CSV into a new RFQ",
  "streaming": false,
  "allowGuest": false,
  "payload": { "type": "object", "properties": { … }, "required": [ … ] }
}
```

External / connected-project tools come in through the same door: their MCP
`inputSchema` is JSON Schema too, and `mcp_external.go` already namespaces them
`<server>__<tool>`. The renderer treats a merged external tool identically to a
native verb — one code path, whether it's `circuit_solve` or `talos__rfq_create`.

**No schema changes are required to ship v1.** Optional, additive `x-yaver-ui`
hints (below) improve the result but are not a prerequisite.

### The renderer (one component per surface)

A `SchemaForm` that maps JSON Schema → native controls:

| JSON Schema | Web control | Mobile control |
|---|---|---|
| `string` | text input | `TextInput` |
| `string` + `enum` | select | picker |
| `string` + `format:"file"`/`csv` | file drop | document picker |
| `number`/`integer` | number input | numeric `TextInput` |
| `boolean` | switch | `Switch` |
| `array` | repeatable rows | repeatable rows |
| nested `object` | fieldset | grouped section |
| `required` | validated, blocks submit | same |

Submit calls the existing transport verbatim:
- web: `agentClient.callOps(verb, payload)` → `POST /ops` (`web/lib/agent-client.ts:1936`)
- mobile: `quicClient.callOpsOnDevice(deviceId, verb, payload)` (`mobile/src/lib/circuitClient.ts:101`)

Result rendering: `OpsResult` is already uniform (`{ok, code, error, …}` +
optional `streamId`). The panel shows ok/error inline; when `streaming:true`,
it subscribes to the stream the way existing streaming panels do. **No per-verb
result parsing** in v1 — show the structured result generically; rich result
views are an opt-in upgrade (below).

### Optional UI hints (`x-yaver-ui`) — additive, backward-compatible

For verbs that want a nicer panel, an optional extension key inside the schema.
Absent → sensible defaults. Present → richer layout. Never required.

```jsonc
"payload": {
  "type": "object",
  "x-yaver-ui": {
    "title": "Import BOM",
    "icon": "table",
    "surfaces": ["web", "mobile", "glass"],   // where to show a native panel
    "group": "manufacturing",                  // catalog grouping
    "confirm": true,                            // destructive → confirm first
    "resultView": "table"                       // generic | table | log | none
  },
  "properties": {
    "csv": { "type": "string", "format": "csv",
             "x-yaver-ui": { "widget": "file-drop", "label": "BOM CSV" } }
  }
}
```

Because `Schema` is `map[string]interface{}`, extra keys pass through untouched
today — adding `x-yaver-ui` to a verb is a one-line change with zero risk to
existing agent consumption.

### Discovery / registry

`/ops/verbs` already returns the full list. The renderer builds its navigation
from it, filtered by `x-yaver-ui.surfaces` (default: show on web + mobile,
hide `streaming` internals unless flagged). This replaces the hand-maintained
`activeTab` union in `dashboard/page.tsx` for *generic* tools — bespoke
`View.tsx` panels stay for features that have earned custom UI (Circuit,
Remote Desktop, Stores). **The two coexist:** hand-built where it matters,
generated for the long tail.

---

## Scope

### v1 (the build)

- [ ] **Web `SchemaForm`** — `web/components/dashboard/SchemaForm.tsx`: JSON
      Schema → controls → `callOps`. Generic `OpsResult` rendering.
- [ ] **Web generic panel host** — a single `ToolPanelView.tsx` that lists
      verbs from `/ops/verbs`, groups by `x-yaver-ui.group`, renders the picked
      verb's `SchemaForm`. One new tab, not one per tool.
- [ ] **Mobile `SchemaForm`** — `mobile/src/components/SchemaForm.tsx` +
      `mobile/app/tools.tsx` generic host over `callOpsOnDevice`.
- [ ] **`x-yaver-ui` passthrough test** — assert extra schema keys survive
      registration → `/ops/verbs` (extends existing ops registry tests).
- [ ] **Guest gating respected** — hide/disable verbs where `allowGuest:false`
      for scoped sessions (the feed already reports `allowGuest`).
- [ ] **Streaming** — wire `streaming:true` verbs to the existing stream
      subscription used by current streaming panels.

### v2 (upgrades, opt-in per verb)

- [ ] `x-yaver-ui.resultView: table|log` rich result renderers.
- [ ] Glass + watch renderers (thin: a subset of controls; voice already covers
      these surfaces conversationally).
- [ ] Author `x-yaver-ui` hints for the highest-traffic verb families.
- [ ] Catalog integration — generated panels appear as tiles in
      `yaverNativeCatalog.ts`.

### Explicitly out of scope

- Rewriting existing bespoke `View.tsx` panels. They stay. This is for the long
  tail + connected-project tools.
- Runtime verb registration from outside the Go binary. Verbs remain compile-time
  (`registerOpsVerb` from `init()`); *external* tools arrive via `mcp_external`,
  not via new native verbs. The renderer unifies both at display time only.
- The dev workflow. Untouched.

---

## Why this is the moat, not the adapter

The MCP-merge (Connect Your Project) is cheap and copyable — anyone can bridge
MCP tools into a chat box. What is *not* cheap to copy is a schema-driven
renderer that lands the same tool as a native control on mobile **and** a web
dashboard **and** a glass HUD **and** a watch, over a remote runtime, behind
subscription auth. That breadth of already-built surfaces is the asset. This
build is what makes the breadth pay off for arbitrary connected projects
instead of only for the ~58 features that got a hand-written panel.

---

## Risks / honest notes

- **JSON Schema is open-ended.** v1 handles the common subset (object of
  scalars/enums/arrays/nested objects). Verbs with exotic schemas fall back to a
  raw JSON editor + submit — never a broken form. Log what fell back; don't
  silently drop fields.
- **Generated ≠ good by default.** Without `x-yaver-ui` hints the panels are
  functional but plain. That's fine for the long tail; promote a verb to hints
  (or a bespoke `View.tsx`) when it earns traffic.
- **Two rendering systems now exist** (generated + bespoke). Keep the boundary
  explicit: a verb is either in the generated host or has a bespoke panel, never
  half in both, to avoid drift.
