export type CodingRunnersProbeState = "ok" | "timed-out" | "http-error" | "network-error";

export function classifyRunnerFetchOutcome(input: {
  aborted?: boolean;
  status?: number;
  networkError?: unknown;
}): CodingRunnersProbeState {
  if (input.aborted === true) return "timed-out";
  if (typeof input.status === "number") {
    return input.status >= 400 ? "http-error" : "ok";
  }
  return "network-error";
}
