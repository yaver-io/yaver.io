// Keep agent-relative preview routes inside the selected device's transport
// base. A relay client lives below /d/<device>; URL("/dev-web/", base) silently
// drops that prefix and sends the WebView to the relay root, which answers 404.
// The agent owns the route path, but never the origin.
export function resolveAgentPreviewUrl(baseUrl: string, reportedPath: string): string {
  // A phone-only execution choice intentionally has no agent origin. Stale
  // preview status can survive the mode switch for one render, so URL
  // construction must degrade to "no preview" instead of crashing the whole
  // Projects tab (for example `http://:null`).
  let base: URL;
  let reported: URL;
  try {
    base = new URL(baseUrl);
    reported = new URL(reportedPath, base.origin);
  } catch {
    return "";
  }
  const basePath = base.pathname.replace(/\/+$/, "");
  const reportedPathname = reported.pathname || "/";
  const alreadyScoped = basePath !== "" &&
    (reportedPathname === basePath || reportedPathname.startsWith(`${basePath}/`));

  base.pathname = alreadyScoped
    ? reportedPathname
    : `${basePath}/${reportedPathname.replace(/^\/+/, "")}`;
  base.search = reported.search;
  base.hash = reported.hash;
  return base.toString();
}

/** Resolve the logical guest-router root on the same device transport.
 *
 * Expo Router replaces the scoped /dev-web/ document path with "/" before it
 * renders. A later HMR/full refresh therefore asks the agent for the transport
 * root (or /d/<device>/ through the relay), not /dev-web/. Probing only the
 * entry document lets an older agent report ready and then hand the phone a
 * bare Go 404 on its first refresh.
 */
export function resolveAgentLogicalPreviewUrl(baseUrl: string): string {
  return resolveAgentPreviewUrl(baseUrl, "/");
}

/**
 * React Native WebView reports HTTP failures for the document and for its
 * subresources through the same callback. Only the document failure makes the
 * attached surface unavailable; a missing favicon, source map, or bundle-side
 * request must not replace an already-running app with a fatal error panel.
 *
 * Missing or malformed event URLs fail closed because older WebView builds do
 * not always identify the failed request and we cannot safely prove it was a
 * subresource.
 */
export function isAgentPreviewDocumentRequest(
  requestUrl: string | undefined,
  attachedUrl: string,
): boolean {
  if (!requestUrl) return true;
  try {
    const request = new URL(requestUrl);
    const attached = new URL(attachedUrl);
    const normalizedPath = (path: string) => path.length > 1 ? path.replace(/\/+$/, "") : path;
    return request.origin === attached.origin &&
      normalizedPath(request.pathname) === normalizedPath(attached.pathname) &&
      request.search === attached.search;
  } catch {
    return true;
  }
}

export type AgentPreviewRouteProbe = {
  ok: boolean;
  status: number;
  contentType: string;
  error?: string;
  transient?: boolean;
  state?: string;
  timedOut?: boolean;
  attempts?: number;
};

function waitForRetry(ms: number, signal?: AbortSignal): Promise<void> {
  if (ms <= 0 || signal?.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    const timer = setTimeout(done, ms);
    function done() {
      clearTimeout(timer);
      signal?.removeEventListener("abort", done);
      resolve();
    }
    signal?.addEventListener("abort", done, { once: true });
  });
}

/** Probe the exact URL the phone will hand to its WebView. The box-local
 * browser doctor cannot see a relay-prefix mistake, so it cannot replace this
 * transport-side operation check. */
export async function probeAgentPreviewRoute(
  url: string,
  headers: Record<string, string>,
  request: typeof fetch = fetch,
  timeoutMs = 15_000,
  signal?: AbortSignal,
): Promise<AgentPreviewRouteProbe> {
  const controller = typeof AbortController !== "undefined" ? new AbortController() : undefined;
  const timeout = setTimeout(() => controller?.abort(), timeoutMs);
  const forwardAbort = () => controller?.abort();
  if (signal?.aborted) controller?.abort();
  else signal?.addEventListener("abort", forwardAbort, { once: true });
  try {
    const response = await request(url, {
      // Use the same operation as the WebView. A HEAD can report that the
      // socket exists without asking Expo to serve the HTML that starts its
      // first compile.
      method: "GET",
      headers: { ...headers, "Cache-Control": "no-cache" },
      signal: controller?.signal,
    });
    const state = String(response.headers.get("x-yaver-devserver") || "").trim().toLowerCase();
    const retryAfter = String(response.headers.get("retry-after") || "").trim();
    const transient = response.status === 502 || response.status === 504 ||
      (response.status === 503 && (state === "starting" || retryAfter !== ""));
    const contentType = String(response.headers.get("content-type") || "unknown").split(";")[0].trim().toLowerCase();
    const html = contentType === "text/html" || contentType === "application/xhtml+xml";
    return {
      // This is a document probe, not generic liveness. A JSON success body is
      // not something the WebView can render and must never become "ready".
      ok: response.ok && html,
      status: response.status,
      contentType,
      ...response.ok && !html ? { error: `preview route returned ${contentType}, expected HTML` } : {},
      ...(transient ? { transient: true } : {}),
      ...(state ? { state } : {}),
    };
  } catch (error) {
    return {
      ok: false,
      status: 0,
      contentType: "unknown",
      error: error instanceof Error ? error.message : String(error),
      transient: true,
    };
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener("abort", forwardAbort);
  }
}

/**
 * Wait for the exact phone-facing preview route to serve HTML.
 *
 * `/dev/start` is deliberately asynchronous. On a cold Expo build the route
 * therefore answers a structured 503 for tens of seconds before becoming
 * healthy. That is progress, not a terminal renderer failure. Authentication,
 * routing and request errors (401/403/404/5xx other than gateway availability)
 * still fail immediately; only the bounded startup statuses are retried.
 */
export async function waitForAgentPreviewRoute(
  url: string,
  headers: Record<string, string>,
  onWaiting?: (probe: AgentPreviewRouteProbe, elapsedMs: number, attempt: number) => void,
  options: {
    request?: typeof fetch;
    timeoutMs?: number;
    attemptTimeoutMs?: number;
    intervalMs?: number;
    signal?: AbortSignal;
  } = {},
): Promise<AgentPreviewRouteProbe> {
  const request = options.request || fetch;
  const timeoutMs = Math.max(1, options.timeoutMs ?? 120_000);
  const attemptTimeoutMs = Math.max(1, options.attemptTimeoutMs ?? 15_000);
  const intervalMs = Math.max(0, options.intervalMs ?? 1_500);
  const startedAt = Date.now();
  let attempt = 0;

  while (true) {
    if (options.signal?.aborted) {
      return { ok: false, status: 0, contentType: "unknown", error: "Dogfood launch stopped", attempts: attempt };
    }
    attempt += 1;
    const elapsed = Date.now() - startedAt;
    const remaining = Math.max(1, timeoutMs - elapsed);
    const probe = await probeAgentPreviewRoute(url, headers, request, Math.min(attemptTimeoutMs, remaining), options.signal);
    if (probe.ok || !probe.transient) return { ...probe, attempts: attempt };

    const afterProbeElapsed = Date.now() - startedAt;
    onWaiting?.(probe, afterProbeElapsed, attempt);
    if (afterProbeElapsed >= timeoutMs) return { ...probe, timedOut: true, attempts: attempt };
    await waitForRetry(Math.min(intervalMs, timeoutMs - afterProbeElapsed), options.signal);
  }
}
