"use client";
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import AppleTVCellView from "@/components/dashboard/AppleTVCellView";
import OwnerGate from "@/components/OwnerGate";

export default function AppleTVDashboardPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <OwnerGate>
      <div className="mx-auto max-w-3xl p-4 sm:p-6">
        <AppleTVCellView devices={devices} token={token} />
      </div>
    </OwnerGate>
  );
}
