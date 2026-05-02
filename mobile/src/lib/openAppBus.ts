// Tiny pubsub for `open_app` commands sent over the BlackBox
// command stream. The tab layout (`app/(tabs)/_layout.tsx`)
// publishes when an `open_app` command arrives; the Hot Reload
// tab (`app/(tabs)/apps.tsx`) subscribes and replays the same
// `handleTapProject(name)` flow a manual tap would trigger.
//
// We deliberately use a tiny in-memory bus instead of AsyncStorage
// or zustand: the only consumer is on the same JS bridge, the
// payload is a project name (no need to persist), and a missed
// command should be silently lost rather than queued.

type Listener = (app: string) => void;

let pending: string | null = null;
const listeners = new Set<Listener>();

export const openAppBus = {
  publish(app: string) {
    const trimmed = app.trim();
    if (!trimmed) return;
    if (listeners.size === 0) {
      // No subscriber yet — store so the next subscribe replays it.
      pending = trimmed;
      return;
    }
    listeners.forEach((cb) => {
      try {
        cb(trimmed);
      } catch {
        // ignore; one bad listener mustn't block others.
      }
    });
  },

  subscribe(cb: Listener): () => void {
    listeners.add(cb);
    if (pending) {
      const replay = pending;
      pending = null;
      // Defer to next tick so the subscriber isn't called inline.
      setTimeout(() => cb(replay), 0);
    }
    return () => {
      listeners.delete(cb);
    };
  },
};
