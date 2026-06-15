"use client";

// Standalone dashboard route for the data-collection cell, at
// /dashboard/collection. Its own route (not a tab in the big dashboard page) so
// it wires cleanly without touching the shared page — same agent verbs the
// mobile data-collection cell calls, over the relay.
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import CollectionVantagesView from "@/components/dashboard/CollectionVantagesView";

export default function CollectionDashboardPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <div className="mx-auto max-w-3xl p-4 sm:p-6">
      <CollectionVantagesView devices={devices} token={token} />
    </div>
  );
}
