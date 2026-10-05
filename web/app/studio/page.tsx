import WorkspaceShell from "@/components/workspace/WorkspaceShell";

export const dynamic = "force-dynamic";

/** Canonical web/desktop/XR entry point for lane-left + SSH-right Studio. */
export default function StudioPage(): React.ReactElement {
  return <WorkspaceShell />;
}
