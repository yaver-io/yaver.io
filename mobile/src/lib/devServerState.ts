import type { DevServerStatus } from "./quic";

export function isActiveDevServerStatus(
  status: DevServerStatus | null | undefined,
): status is DevServerStatus {
  return !!status && (status.running === true || status.building === true);
}
