/** Browser-visible release switches. Server enforcement is independent. */
let remoteRunnerOAuthEnabled = false;

export function applyProductPolicy(policy: unknown): void {
  const value = (policy as { remoteRunnerOAuthEnabled?: unknown } | null)?.remoteRunnerOAuthEnabled;
  remoteRunnerOAuthEnabled = value === true;
}

export function isRemoteRunnerOAuthEnabled(): boolean {
  return remoteRunnerOAuthEnabled;
}
