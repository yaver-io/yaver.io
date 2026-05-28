// /workspace — i3-style tiling view of the connected yaver agent.
//
// Companion to /dashboard (tabbed, single-pane) and /spatial (3D / VR).
// Use this when you want to SEE the terminal + the live preview + the
// most recent clips + the last-failed test all at the same time. The
// classic "vibe + view simultaneously" loop.
//
// Auto-routes to a default pane set based on the agent's project kind
// (mobile / web / backend / generic). Use Cmd-1..9 to jump panes,
// Cmd-J/K to cycle, Esc to unfocus.

import WorkspaceShell from "@/components/workspace/WorkspaceShell";

export const dynamic = "force-dynamic";

export default function WorkspacePage(): React.ReactElement {
  return <WorkspaceShell />;
}
