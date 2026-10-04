/**
 * Reversible release switches. Remote runner OAuth is intentionally off:
 * runner credentials are created and retained on the endpoint that executes
 * the runner. Expo inlines EXPO_PUBLIC_* values into every shared RN surface.
 */
let remoteRunnerOAuthEnabled = false;

export function applyProductPolicy(policy: unknown): void {
  const value = (policy as { remoteRunnerOAuthEnabled?: unknown } | null)?.remoteRunnerOAuthEnabled;
  remoteRunnerOAuthEnabled = value === true;
}

export function isRemoteRunnerOAuthEnabled(): boolean {
  return remoteRunnerOAuthEnabled;
}
