// polyfills.ts — fill gaps in React Native's Hermes runtime. Side-effect import;
// must run before any code that touches the shimmed globals (wire it into
// app/_layout.tsx alongside cryptoSetup).
//
// RN's whatwg-fetch / abort-controller polyfill provides `AbortController` and
// `AbortSignal`, but NOT the *static* helpers `AbortSignal.timeout()` and
// `AbortSignal.any()` (added to the spec later). Any call site that does
// `fetch(url, { signal: AbortSignal.timeout(ms) })` therefore throws
// "undefined is not a function" at the point the options object is built —
// before fetch even runs. This silently broke every agent probe that used it:
// mesh enable (/mesh/up surfaced the throw; /mesh/status + self-heal swallowed
// it, so live status never loaded), deviceStatus, and the DeviceContext
// presence/auto-pair checks. Install minimal, spec-compatible shims when absent.

declare const AbortSignal: {
  timeout?: (ms: number) => AbortSignal;
  any?: (signals: AbortSignal[]) => AbortSignal;
  prototype: AbortSignal;
} & (new () => AbortSignal);

if (typeof AbortSignal !== "undefined") {
  const AS = AbortSignal as any;

  if (typeof AS.timeout !== "function") {
    AS.timeout = (ms: number): AbortSignal => {
      const controller = new AbortController();
      setTimeout(() => {
        // Spec aborts with a TimeoutError; RN's abort() may ignore the reason
        // arg, which is harmless — the signal still fires "abort".
        try {
          controller.abort(new DOMException("TimedOut", "TimeoutError"));
        } catch {
          controller.abort();
        }
      }, ms);
      return controller.signal;
    };
  }

  if (typeof AS.any !== "function") {
    AS.any = (signals: AbortSignal[]): AbortSignal => {
      const controller = new AbortController();
      for (const s of signals) {
        if (s.aborted) {
          try {
            controller.abort((s as any).reason);
          } catch {
            controller.abort();
          }
          break;
        }
        s.addEventListener(
          "abort",
          () => {
            try {
              controller.abort((s as any).reason);
            } catch {
              controller.abort();
            }
          },
          { once: true }
        );
      }
      return controller.signal;
    };
  }
}
