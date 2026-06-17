"use client";
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import HomeControlView from "@/components/dashboard/HomeControlView";

export default function HomeControlPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <div className="mx-auto max-w-3xl p-4 sm:p-6">
      <h1 className="mb-1 text-xl font-semibold">Home Control</h1>
      <p className="mb-4 text-sm opacity-60">Single kumanda — one remote for Apple TV, Mi Box, IR devices, and activities.</p>
      <HomeControlView devices={devices} token={token} />
    </div>
  );
}
