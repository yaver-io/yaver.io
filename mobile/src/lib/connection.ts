// Connection-status helpers shared across tabs (apps, agent, more…).
//
// The raw values the QUIC client / DeviceContext emit ("error" /
// "disconnected" / "connecting") are useful for branching logic but terrible
// to put in front of a user. describeConnectionStatus maps them to actionable
// text that explains what happened AND what to do next.

export function describeConnectionStatus(status?: string | null): string {
  switch ((status || "").toLowerCase()) {
    case "connected":
      return "connected";
    case "connecting":
      return "still connecting — give it a few seconds and try again";
    case "disconnected":
      return "disconnected — open the Devices tab and tap your machine";
    case "error":
      return "hit a network error — check Wi-Fi / relay and reconnect on the Devices tab";
    case "auth_failed":
      return "rejected your auth token — sign in again from Settings";
    default:
      return status ? `currently ${status}` : "offline";
  }
}

// Build a user-facing message for a failed action that needs the dev machine
// online. Takes the raw error and the current connectionStatus and returns a
// single string combining both pieces of information.
export function describeActionError(err: unknown, connectionStatus?: string | null): string {
  const raw = err instanceof Error ? err.message : String(err);
  return `${raw}\n\nYaver ${describeConnectionStatus(connectionStatus)}.`;
}
