const DEFAULT_YAVER_CLOUD_BASE = "https://cloud.yaver.io";

export function getYaverCloudBaseUrl(): string {
  return (
    process.env.NEXT_PUBLIC_YAVER_CLOUD_BASE_URL ||
    process.env.NEXT_PUBLIC_YAVER_CLOUD_PREVIEW_BASE_URL ||
    DEFAULT_YAVER_CLOUD_BASE
  ).replace(/\/$/, "");
}

export function getYaverCloudHost(): string {
  try {
    return new URL(getYaverCloudBaseUrl()).host;
  } catch {
    return "cloud.yaver.io";
  }
}

export function getSelfHostedRuntimeBaseUrl(): string {
  return (process.env.NEXT_PUBLIC_YAVER_SELF_HOSTED_BASE_URL || "").replace(/\/$/, "");
}

export function getSelfHostedRuntimeLabel(): string {
  return process.env.NEXT_PUBLIC_YAVER_SELF_HOSTED_LABEL || "Self-Hosted Runtime";
}
