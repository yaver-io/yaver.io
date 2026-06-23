"use client";

// designBridge.ts — parent side of the preview's design channel (the mini-figma
// overlay). The Design Glass in the renderer posts namespaced events when the
// builder selects or drag-reorders a widget; this bridge forwards them to the
// host. It is the design-time twin of dataBridge.ts (runtime data ops), on a
// separate `source` tag so the two channels never collide — and entirely absent
// in run mode, so the app's own drag is never intercepted.

const EVT = "yaver-design-evt";

export interface DesignSelectEvent {
  source: typeof EVT;
  op: "select";
  nodeId: string;
  kind: string;
  rect?: { x: number; y: number; w: number; h: number };
}
export interface DesignMovedEvent {
  source: typeof EVT;
  op: "moved";
  nodeId: string;
  /** Insert nodeId before this node id; null = move to the end. */
  beforeId: string | null;
}
export type DesignEvent = DesignSelectEvent | DesignMovedEvent;

function isDesignEvent(data: unknown): data is DesignEvent {
  return (
    !!data &&
    typeof data === "object" &&
    (data as { source?: unknown }).source === EVT &&
    ((data as { op?: unknown }).op === "select" || (data as { op?: unknown }).op === "moved")
  );
}

/**
 * Listen for design events from the preview iframe. Returns a detach function.
 * Pass the iframe's contentWindow to ignore events from other frames.
 */
export function attachDesignBridge(
  onEvent: (e: DesignEvent) => void,
  frame?: Window | null,
): () => void {
  const listener = (event: MessageEvent) => {
    if (frame && event.source !== frame) return;
    if (!isDesignEvent(event.data)) return;
    onEvent(event.data);
  };
  window.addEventListener("message", listener);
  return () => window.removeEventListener("message", listener);
}
