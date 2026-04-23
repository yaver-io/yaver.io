export function isCloudPreviewUser(email?: string | null): boolean {
  const normalized = (email ?? "").trim().toLowerCase();
  if (!normalized) return false;
  const raw =
    process.env.EXPO_PUBLIC_YAVER_CLOUD_PREVIEW_EMAILS ||
    process.env.EXPO_PUBLIC_CLOUD_PREVIEW_EMAILS ||
    "kivanc.cakmak@icloud.com";
  return raw
    .split(",")
    .map((item: string) => item.trim().toLowerCase())
    .filter(Boolean)
    .includes(normalized);
}
