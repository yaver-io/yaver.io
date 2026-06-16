"use client";
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import AppleTVCellView from "@/components/dashboard/AppleTVCellView";

export default function AppleTVDashboardPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <div className="mx-auto max-w-3xl p-4 sm:p-6">
      <AppleTVCellView devices={devices} token={token} />
    </div>
  );
}
