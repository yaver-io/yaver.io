import DeviceCodeClient, { type DeviceCodeInfo } from "./DeviceCodeClient";

export const dynamic = "force-dynamic";

function getConvexUrl(): string {
  return process.env.NEXT_PUBLIC_CONVEX_SITE_URL || "https://perceptive-minnow-557.eu-west-1.convex.site";
}

async function getInitialDeviceInfo(code: string): Promise<DeviceCodeInfo> {
  if (!code) return null;
  try {
    const res = await fetch(`${getConvexUrl()}/auth/device-code/info?user_code=${encodeURIComponent(code)}`, {
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
  searchParams: Promise<{ code?: string }>;
}) {
  const params = await searchParams;
  const initialCode = params.code || "";
  const initialDeviceInfo = await getInitialDeviceInfo(initialCode);
  return <DeviceCodeClient initialCode={initialCode} initialDeviceInfo={initialDeviceInfo} />;
}
