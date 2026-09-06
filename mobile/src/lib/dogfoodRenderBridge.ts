/** Cross-JS-context render signal for Dogfood mode.
 *
 * The Tasks screen runs inside the attached RN-web WebView, while the surface
 * that owns reload lives in the native host. Module event emitters cannot cross
 * that boundary, so the inner app posts one small, non-authoritative message.
 */

export const DOGFOOD_RENDER_MESSAGE = "yaver:dogfood-render";
export const DOGFOOD_CHECKOUT_KEY = "yaver.attach.checkout";

export type DogfoodRenderMessage = {
  type: typeof DOGFOOD_RENDER_MESSAGE;
  source: string;
};

export function makeDogfoodRenderMessage(source: string): string {
  return JSON.stringify({ type: DOGFOOD_RENDER_MESSAGE, source } satisfies DogfoodRenderMessage);
}

export function parseDogfoodRenderMessage(raw: string): DogfoodRenderMessage | null {
  try {
    const value = JSON.parse(raw) as Partial<DogfoodRenderMessage>;
    if (value.type !== DOGFOOD_RENDER_MESSAGE) return null;
    return { type: DOGFOOD_RENDER_MESSAGE, source: String(value.source || "dogfood-task") };
  } catch {
    return null;
  }
}

export function isAttachedDogfoodWebRuntime(scope: any = globalThis): boolean {
  try {
    return scope?.localStorage?.getItem?.("yaver.attach.mode") === "1" &&
      typeof scope?.ReactNativeWebView?.postMessage === "function";
  } catch {
    return false;
  }
}

export function normalizedDogfoodPath(value: unknown): string {
  const raw = String(value || "").trim();
  if (!raw) return "";
  const withoutScheme = raw.replace(/^file:(\/\/)?/i, "");
  const decoded = withoutScheme.replace(/\\/g, "/");
  const withoutQuery = decoded.split(/[?#]/, 1)[0] || "";
  const withoutTrailingSlash = withoutQuery.replace(/\/+$/, "");
  if (/^[A-Za-z]:$/.test(withoutTrailingSlash)) return `${withoutTrailingSlash.toLowerCase()}/`;
  if (/^[A-Za-z]:\//.test(withoutTrailingSlash)) return withoutTrailingSlash.toLowerCase();
  return withoutTrailingSlash || "/";
}

/**
 * The verified checkout rendered by the OUTER Dogfood host.
 *
 * Like the attach sentinel, this is display context only. It grants no
 * authority; the HttpOnly attach cookie remains the capability boundary.
 */
export function attachedDogfoodCheckout(scope: any = globalThis): string | null {
  if (!isAttachedDogfoodWebRuntime(scope)) return null;
  try {
    const checkout = normalizedDogfoodPath(scope?.localStorage?.getItem?.(DOGFOOD_CHECKOUT_KEY));
    return checkout && checkout !== "/" && !/^[a-z]:\/$/.test(checkout) ? checkout : null;
  } catch {
    return null;
  }
}

/** Exact directory-boundary match: `/repo-copy` is not inside `/repo`. */
export function isPathInsideAttachedDogfoodCheckout(projectPath: string, checkoutPath: string | null): boolean {
  const project = normalizedDogfoodPath(projectPath);
  const checkout = normalizedDogfoodPath(checkoutPath);
  if (!project || !checkout || checkout === "/" || /^[a-z]:\/$/.test(checkout)) return false;
  return project === checkout || project.startsWith(`${checkout}/`);
}

export function dogfoodProjectRootPath(
  workDir: string | null | undefined,
  checkoutPath: string | null | undefined,
): string {
  const project = normalizedDogfoodPath(workDir);
  const checkout = normalizedDogfoodPath(checkoutPath);
  if (project && checkout && isPathInsideAttachedDogfoodCheckout(project, checkout)) return checkout;
  return project || checkout || "";
}

/** Human/project identity for a guest launched inside the Yaver Dogfood
 * container. Discovery may label a checkout `root (sfmg) / mobile` because
 * `root` is the remote account/container directory; that is not the app the
 * user is vibing. The absolute path remains the authoritative task scope. */
export function dogfoodGuestProjectName(
  workDir: string | null | undefined,
  discoveredName: string | null | undefined,
  fallback = "Preview",
): string {
  const normalizedPath = normalizedDogfoodPath(workDir);
  const segments = normalizedPath.split("/").filter(Boolean);
  const display = String(discoveredName || "").trim().split(" / ")[0].trim();
  const nested = display.match(/^[^(]+\(([^()]+)\)$/)?.[1]?.trim();
  if (nested) return nested;
  if (display && !/[\\/]/.test(display)) return display;

  const pathLeaf = segments[segments.length - 1]?.trim();
  const parentLeaf = segments[segments.length - 2]?.trim();
  if (pathLeaf === "mobile" && parentLeaf) return parentLeaf;
  return pathLeaf || display || fallback;
}
