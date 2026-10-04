/**
 * Reversible release switches. Remote runner OAuth is intentionally off:
 * runner credentials are created and retained on the endpoint that executes
 * the runner. Expo inlines EXPO_PUBLIC_* values into every shared RN surface.
 */
export function applyProductPolicy(_policy: unknown): void {
}

export function isRemoteRunnerOAuthEnabled(): boolean {
  return false;
}
