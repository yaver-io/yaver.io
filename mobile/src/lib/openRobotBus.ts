// Tiny pubsub for shortcuts that select a robot and open the Robot tab.
// AsyncStorage persists the choice for cold starts; this bus updates an
// already-mounted Robot tab immediately on the same JS bridge.

type Listener = (deviceId: string) => void;

let pending: string | null = null;
const listeners = new Set<Listener>();

export const openRobotBus = {
  publish(deviceId: string) {
    const trimmed = deviceId.trim();
    if (!trimmed) return;
    if (listeners.size === 0) {
      pending = trimmed;
      return;
    }
    listeners.forEach((cb) => {
      try {
        cb(trimmed);
      } catch {
        // One bad listener must not block robot selection for others.
      }
    });
  },

  subscribe(cb: Listener): () => void {
    listeners.add(cb);
    if (pending) {
      const replay = pending;
      pending = null;
      setTimeout(() => cb(replay), 0);
    }
    return () => {
      listeners.delete(cb);
    };
  },
};
