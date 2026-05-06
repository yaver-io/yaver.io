// Tiny pubsub for "open this task in the chat detail" intents.
// The background-running pill in the root layout publishes a
// taskId when tapped; the Tasks screen subscribes and opens its
// chat-detail modal for that task. Same shape as openAppBus.ts —
// in-memory, last-write replays for late subscribers, no
// persistence (intent is a UI shortcut, not state worth saving).

type Listener = (taskId: string) => void;

let pending: string | null = null;
const listeners = new Set<Listener>();

export const openTaskBus = {
  publish(taskId: string) {
    const trimmed = taskId.trim();
    if (!trimmed) return;
    if (listeners.size === 0) {
      pending = trimmed;
      return;
    }
    listeners.forEach((cb) => {
      try { cb(trimmed); } catch { /* one bad listener mustn't block the others */ }
    });
  },

  subscribe(cb: Listener): () => void {
    listeners.add(cb);
    if (pending) {
      const replay = pending;
      pending = null;
      setTimeout(() => cb(replay), 0);
    }
    return () => { listeners.delete(cb); };
  },
};
