"use client";

// Standalone dashboard route for self-hosted CI runner config, at /dashboard/ci.
// Its own route (mirrors /dashboard/arm) so it wires without touching the shared
// dashboard page. Register a box as a GitHub/GitLab self-hosted runner, see the
// savings ledger, scaffold deploy workflows — same agent verbs as mobile.
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import CIRunnerView from "@/components/dashboard/CIRunnerView";

export default function CIDashboardPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <div className="mx-auto max-w-3xl p-4 sm:p-6">
      <CIRunnerView devices={devices} token={token} />
    </div>
  );
}
