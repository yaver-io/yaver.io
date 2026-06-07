// meshTypes.ts — shared shapes + the node-role taxonomy for the Yaver Mesh
// mobile surface. The roles here are the UI vocabulary documented in
// docs/yaver-mesh-mobile-tailscale-ui-design.md §2; they are DERIVED from the
// fields the Convex control plane already returns on /mesh/peers, not new
// backend state.

/** A wide-internet exit node advertises the default route. */
export const DEFAULT_ROUTE_V4 = "0.0.0.0/0";
export const DEFAULT_ROUTE_V6 = "::/0";

// Bridging a Tailnet = advertising Tailscale's CGNAT block as a mesh route on a
// node sitting on BOTH networks. Mesh peer /32s + the 100.96/12 overlay are
// longer-prefix, so they still win — only real Tailnet hosts route through the
// bridge. (Mirrors the comment that lived in network.tsx.)
export const TAILSCALE_BRIDGE_CIDR = "100.64.0.0/10";

export type AccessScope = "owner" | "shared" | "peer";

export type MeshPeer = {
  deviceId: string;
  alias?: string;
  meshIPv4?: string;
  meshIPv6?: string;
  magicDns?: string; // <alias>.mesh — populated once backend Gap G1 lands
  online?: boolean;
  isExitNode?: boolean;
  accessScope?: AccessScope;
  advertisedRoutes?: string[];
  // Desired state set by the console, read by the agent on its reconcile tick.
  wantExitNode?: boolean;
  wantUseExitNode?: string; // deviceId of the exit node this node routes through
  wantRoutes?: string[];
  // Optional telemetry (backend Gap G2 — absent until enriched; render-safe).
  connectionType?: "direct" | "relay";
  lastHandshake?: number;
  clientVersion?: string;
  os?: string;
  platform?: string;
};

export type ACLRule = {
  srcType: "tag" | "device" | "user" | "any";
  src: string;
  dstType: "tag" | "device" | "user" | "any";
  dst: string;
  ports: string[];
  action: "accept" | "drop";
};

export type SupportConn = {
  grantId: string;
  deviceId: string | null;
  counterpartName: string;
  allowDesktopControl: boolean;
  expiresAt: number | null;
};

/** A node's effective subnet routes = advertised routes minus the default route. */
export function effectiveRoutes(p: MeshPeer): string[] {
  const src = p.wantRoutes ?? p.advertisedRoutes ?? [];
  return src.filter((r) => r !== DEFAULT_ROUTE_V4 && r !== DEFAULT_ROUTE_V6);
}

/** True when the node serves (or is set to serve) as a wide-internet exit node. */
export function isExitProvider(p: MeshPeer): boolean {
  return !!(p.isExitNode || p.wantExitNode);
}

/** True when the node advertises specific subnet routes — i.e. it's a Gateway. */
export function isGatewayProvider(p: MeshPeer): boolean {
  return effectiveRoutes(p).length > 0;
}

/** Desired ≠ effective → the agent hasn't converged yet ("applying…"). */
export function exitNodePending(p: MeshPeer): boolean {
  return p.wantExitNode !== undefined && !!p.wantExitNode !== !!p.isExitNode;
}

/** Short, human node label. */
export function nodeLabel(p: MeshPeer): string {
  return p.alias || p.deviceId;
}

/** A node usable AS an exit node by others. */
export function isSelectableExit(p: MeshPeer): boolean {
  return isExitProvider(p);
}

export function osGlyph(os?: string, platform?: string): string {
  const v = (os || platform || "").toLowerCase();
  if (v.includes("ios") || v.includes("mac") || v.includes("darwin") || v.includes("apple")) return "";
  if (v.includes("android")) return "▸"; // ▸ (no brand glyph; neutral)
  if (v.includes("linux")) return "⌗"; // ⌗-ish
  if (v.includes("win")) return "▧";
  return "";
}
