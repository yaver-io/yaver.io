"use client";

// Standalone dashboard route for a 3D printer cell, at /dashboard/printer. Its
// own route (not a tab in the big dashboard page) so it wires cleanly without
// touching the shared dashboard page. Drive a Bambu printer over the relay —
// same agent verbs as mobile.
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import PrinterCellView from "@/components/dashboard/PrinterCellView";
import OwnerGate from "@/components/OwnerGate";

export default function PrinterDashboardPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <OwnerGate>
      <div className="mx-auto max-w-3xl p-4 sm:p-6">
        <PrinterCellView devices={devices} token={token} />
      </div>
    </OwnerGate>
  );
}
