export function isCloudPreviewUser(email?: string | null): boolean {
  const normalized = (email ?? "").trim().toLowerCase();
  if (!normalized) return false;
  const raw =
    process.env.EXPO_PUBLIC_YAVER_CLOUD_PREVIEW_EMAILS ||
    process.env.EXPO_PUBLIC_CLOUD_PREVIEW_EMAILS ||
    "";
  const allowed = raw
    .split(",")
    .map((item: string) => item.trim().toLowerCase())
    .filter(Boolean);
  if (allowed.length === 0) return false;
  return allowed.includes(normalized);
}
