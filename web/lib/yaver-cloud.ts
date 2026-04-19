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
