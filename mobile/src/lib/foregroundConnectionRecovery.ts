// foregroundConnectionRecovery.ts — bounded proof before trusting a connection
// after iOS/Android has suspended the app.
//
// A React Native process can retain its JavaScript `connected` flag while the
// OS discards the TCP/relay path underneath it. Before this guard existed,
// DeviceContext returned early on foreground whenever that stale flag was set;
// only force-closing and reopening the app constructed a fresh client. Keep the
// decision here dependency-free so the failure and the cancellation races are
// executable in Node, not inferred from React lifecycle wiring.

export interface ForegroundRecoveryClient {
  readonly connectionState: "disconnected" | "connecting" | "connected" | "error";
  verifyStillConnected(timeoutMs?: number): Promise<boolean>;
  fullReconnect(): void;
}

export interface ForegroundRecoveryOptions {
  client: ForegroundRecoveryClient;
  /** False when another AppState transition superseded this resume probe. */
  isCurrent: () => boolean;
  timeoutMs?: number;
  onStale?: () => void;
}

/**
 * Prove that an apparently-connected client still reaches the agent after an
 * app resume. A failed proof starts the normal full transport ladder; a late
 * proof from an older lifecycle epoch is ignored.
 *
 * Returns true only when this call initiated a reconnect.
 */
export async function recoverStaleConnectionOnForeground({
  client,
  isCurrent,
  timeoutMs = 2_000,
  onStale,
}: ForegroundRecoveryOptions): Promise<boolean> {
  if (client.connectionState !== "connected") return false;

  let stillConnected = false;
  try {
    stillConnected = await client.verifyStillConnected(timeoutMs);
  } catch {
    // A transport/native rejection is the same operational verdict as a
    // timeout: the remembered path was not proven and must be rebuilt.
  }
  if (!isCurrent() || client.connectionState !== "connected") return false;
  if (stillConnected) return false;

  onStale?.();
  client.fullReconnect();
  return true;
}
