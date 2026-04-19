export function isCloudPreviewUser(email?: string | null): boolean {
  return (email ?? "").trim() !== "";
}
