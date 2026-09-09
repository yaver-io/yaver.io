export type WorkspaceClientRoute = {
  useSelectedClient: boolean;
  peerTarget?: string;
};

/**
 * Choose how Mobile Workspace reaches the box selected in its wizard.
 *
 * A live pooled client is authoritative and goes directly to that box. The
 * focused client's /peer route is only a fallback while the selected box has
 * not connected yet. This keeps a healthy selection independent from an
 * unrelated focused machine.
 */
export function workspaceClientRoute(
  selectedDeviceId: string | null | undefined,
  activeDeviceId: string | null | undefined,
  connectedDeviceIds: readonly string[],
): WorkspaceClientRoute {
  const selected = selectedDeviceId?.trim();
  if (!selected) return { useSelectedClient: false };
  if (connectedDeviceIds.includes(selected)) return { useSelectedClient: true };
  if (selected !== activeDeviceId) return { useSelectedClient: false, peerTarget: selected };
  return { useSelectedClient: false };
}
