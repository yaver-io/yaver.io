"use client";

// widgets.ts — the "aware widget" registry. Each WidgetDef wraps an underlying
// rendered primitive but carries a manipulation manifest so the overlay knows
// what a gesture MEANS for that widget — what the builder may restyle/move
// (design.*) and what the end user may do at runtime (runtime.*). The inspector
// generates its controls from `controls`; it never hard-codes per-widget UI.
//
// P1 ships a curated core (Nav/Header/QuickAdd/List) plus an opaque fallback so
// any unknown node is still selectable. Expand toward the full set in
// docs/yaver-mini-figma-direct-manipulation.md (Stack/Row/Field/Button/…).

import type { NodeUi } from "./preview";

/** Inspector controls a widget can expose. Each maps to a NodeUi field.
 * "reorderable"/"swipeDelete" are END-USER (runtime) toggles the builder flips
 * — i.e. whether the finished app has "yaver draggable mode" for that widget. */
export type DesignControl = "title" | "spacing" | "hidden" | "reorderable" | "swipeDelete" | "board";

export interface WidgetDef {
  type: string;
  label: string;
  description: string;
  /** Design-time affordances (what the BUILDER may do — the Figma side). */
  design: {
    movable: boolean;
    restyle: ("gap" | "padding" | "hidden")[];
    editableText: boolean;
  };
  /** Runtime affordances (what the END USER may do — declarable, P5). */
  runtime: {
    reorderable?: boolean;
    swipe?: ("delete" | "complete")[];
    tap?: "navigate" | "toggle" | "edit";
  };
  /** Which inspector controls this widget exposes today. */
  controls: DesignControl[];
}

const FALLBACK: WidgetDef = {
  type: "Custom",
  label: "Element",
  description: "Opaque widget — selectable and hideable, not yet introspectable.",
  design: { movable: true, restyle: ["hidden"], editableText: false },
  runtime: {},
  controls: ["hidden"],
};

export const WIDGETS: Record<string, WidgetDef> = {
  Nav: {
    type: "Nav",
    label: "Navigation",
    description: "Top tab bar switching between tables/screens.",
    design: { movable: false, restyle: ["hidden"], editableText: false },
    runtime: { tap: "navigate" },
    controls: ["hidden"],
  },
  Header: {
    type: "Header",
    label: "Header",
    description: "Screen title shown above the content.",
    design: { movable: true, restyle: ["padding", "hidden"], editableText: true },
    runtime: {},
    controls: ["title", "spacing", "hidden"],
  },
  QuickAdd: {
    type: "QuickAdd",
    label: "Quick add",
    description: "Inline add-row controls. Move it down to clear the top nav.",
    design: { movable: true, restyle: ["gap", "padding", "hidden"], editableText: false },
    runtime: { tap: "edit" },
    controls: ["spacing", "hidden"],
  },
  List: {
    type: "List",
    label: "List",
    description: "Rows of records. Toggle end-user drag-reorder / swipe-to-delete.",
    design: { movable: true, restyle: ["padding", "hidden"], editableText: false },
    runtime: { reorderable: true, swipe: ["delete"], tap: "edit" },
    controls: ["spacing", "hidden", "reorderable", "swipeDelete", "board"],
  },
  // Declared for the Jira/kanban case: a data-bound board whose end users drag
  // cards between columns (grouped by a status column), persisting via update.
  // Registry entry today; renderer implementation tracked as P5 (Board).
  Board: {
    type: "Board",
    label: "Board (kanban)",
    description: "Cards grouped by a status column; end users drag between columns.",
    design: { movable: true, restyle: ["gap", "padding", "hidden"], editableText: false },
    runtime: { reorderable: true, tap: "edit" },
    controls: ["hidden", "spacing"],
  },
};

export function widgetDef(kind: string | null | undefined): WidgetDef {
  return (kind && WIDGETS[kind]) || FALLBACK;
}

/** Default per-node overrides (empty) — the inspector merges edits into this. */
export function emptyUi(): NodeUi {
  return {};
}
