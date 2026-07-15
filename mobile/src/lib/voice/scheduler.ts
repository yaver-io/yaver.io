/**
 * voice/scheduler.ts — the injected time/timer sources for the voice core.
 *
 * The core needs a timer that fires in the ABSENCE of input (that's how silence
 * is detected). Real timers make tests slow and flaky, so the core takes a
 * Scheduler + Clock interface; production uses the globals, tests use the fake
 * pair below to drive virtual time deterministically.
 */
import type { Clock, Scheduler } from "./types";

/** Production clock — real wall time. */
export const realClock: Clock = {
  now: () => Date.now(),
};

/** Production scheduler — backed by the JS event loop's global timers. */
export const realScheduler: Scheduler = {
  setInterval: (fn, ms) => {
    const id = setInterval(fn, ms);
    return () => clearInterval(id);
  },
  setTimeout: (fn, ms) => {
    const id = setTimeout(fn, ms);
    return () => clearTimeout(id);
  },
};

// ── Test doubles ─────────────────────────────────────────────────────────

interface Pending {
  id: number;
  fireAt: number;
  interval: number | null; // ms for repeating, null for one-shot
  fn: () => void;
  cancelled: boolean;
}

/**
 * A clock + scheduler driven by an explicit `advance(ms)`. Interval and timeout
 * callbacks fire in virtual-time order as you advance. Lets a test say "800ms
 * passed with no new partial" without any real waiting.
 */
export class FakeTime implements Clock, Scheduler {
  private t = 0;
  private seq = 0;
  private pending: Pending[] = [];

  now(): number {
    return this.t;
  }

  setInterval(fn: () => void, ms: number): () => void {
    const p: Pending = {
      id: ++this.seq,
      fireAt: this.t + ms,
      interval: ms,
      fn,
      cancelled: false,
    };
    this.pending.push(p);
    return () => {
      p.cancelled = true;
    };
  }

  setTimeout(fn: () => void, ms: number): () => void {
    const p: Pending = {
      id: ++this.seq,
      fireAt: this.t + ms,
      interval: null,
      fn,
      cancelled: false,
    };
    this.pending.push(p);
    return () => {
      p.cancelled = true;
    };
  }

  /**
   * Advance virtual time by `ms`, firing every scheduled callback whose time
   * arrives (repeating intervals re-arm). Callbacks may schedule more work;
   * that's handled because we re-scan until nothing is due within the window.
   */
  advance(ms: number): void {
    const target = this.t + ms;
    // Guard against a pathological re-arm loop (a callback that schedules a
    // 0ms timer forever). 100k iterations is far beyond any real turn.
    let guard = 0;
    for (;;) {
      if (++guard > 100000) throw new Error("FakeTime.advance: runaway timers");
      // Next non-cancelled callback due at or before target.
      let next: Pending | null = null;
      for (const p of this.pending) {
        if (p.cancelled) continue;
        if (p.fireAt > target) continue;
        if (!next || p.fireAt < next.fireAt || (p.fireAt === next.fireAt && p.id < next.id)) {
          next = p;
        }
      }
      if (!next) break;
      this.t = next.fireAt;
      if (next.interval !== null) {
        next.fireAt = this.t + next.interval;
      } else {
        next.cancelled = true;
      }
      next.fn();
    }
    this.t = target;
    // Drop fired one-shots so the pending list doesn't grow unbounded.
    this.pending = this.pending.filter((p) => !p.cancelled);
  }

  /** Let queued microtasks (awaited promises in the core) settle between
   *  time steps. Call `await ft.flush()` after advance() in async tests. */
  async flush(): Promise<void> {
    // Drain to quiescence via real macrotask boundaries, not a fixed count of
    // microtask turns — the core's await chain (stopSession → judge → dispatch
    // → speak → beginListen) is deeper than any small constant, and a macrotask
    // hop empties the whole microtask queue between hops. A few hops cover
    // chains that re-queue microtasks as they unwind. FakeTime timers are driven
    // by advance(), not these, so re-armed listen loops stay dormant here.
    for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0));
  }
}
