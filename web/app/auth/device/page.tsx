import DeviceCodeClient, { type DeviceCodeInfo } from "./DeviceCodeClient";
import { CONVEX_URL } from "@/lib/constants";

export const dynamic = "force-dynamic";

function getConvexUrl(searchParam?: string): string {
  return searchParam || CONVEX_URL;
}

async function getInitialDeviceInfo(code: string, convexUrl: string): Promise<DeviceCodeInfo> {
  if (!code) return null;
  try {
    const res = await fetch(`${convexUrl}/auth/device-code/info?user_code=${encodeURIComponent(code)}`, {
      cache: "no-store",
    });
    if (!res.ok) {
      if (res.status === 404 || res.status === 410) {
        return {
          machineName: null,
          platform: null,
          arch: null,
          shell: null,
          environment: null,
          runtimeVersion: null,
          preferredProvider: null,
          isWsl: false,
          expiresAt: 0,
          status: "expired",
        };
      }
      return null;
    }
    return await res.json();
  } catch {
    return null;
  }
}

export default async function DeviceCodePage({
  searchParams,
}: {
  searchParams: Promise<{ code?: string; convex?: string }>;
}) {
  const params = await searchParams;
  const initialCode = params.code || "";
  const convexUrl = getConvexUrl(params.convex);
  const initialDeviceInfo = await getInitialDeviceInfo(initialCode, convexUrl);
  return <DeviceCodeClient initialCode={initialCode} initialDeviceInfo={initialDeviceInfo} initialConvexUrl={convexUrl} />;
}
