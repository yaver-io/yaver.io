import { CONVEX_URL } from "@/lib/constants";

export type DownloadEntry = {
  platform: "macos" | "windows" | "linux" | "android" | "ios";
  arch: string;
  format: string;
  version: string;
  filename: string;
  size: number;
  url: string | null;
};

export type DownloadSlug =
  | "linux-appimage-amd64"
  | "linux-appimage-arm64"
  | "linux-deb-amd64"
  | "linux-deb-arm64"
  | "linux-rpm-amd64"
  | "linux-rpm-arm64"
  | "linux-tarball-amd64"
  | "linux-tarball-arm64"
  | "macos-arm64"
  | "windows-x64";

export const DOWNLOAD_SLUGS: Record<
  DownloadSlug,
  { platform: DownloadEntry["platform"]; arch: string; format: string }
> = {
  "linux-appimage-amd64": { platform: "linux", arch: "amd64", format: "appimage" },
  "linux-appimage-arm64": { platform: "linux", arch: "arm64", format: "appimage" },
  "linux-deb-amd64": { platform: "linux", arch: "amd64", format: "deb" },
  "linux-deb-arm64": { platform: "linux", arch: "arm64", format: "deb" },
  "linux-rpm-amd64": { platform: "linux", arch: "amd64", format: "rpm" },
  "linux-rpm-arm64": { platform: "linux", arch: "arm64", format: "rpm" },
  "linux-tarball-amd64": { platform: "linux", arch: "amd64", format: "tarball" },
  "linux-tarball-arm64": { platform: "linux", arch: "arm64", format: "tarball" },
  "macos-arm64": { platform: "macos", arch: "arm64", format: "dmg" },
  "windows-x64": { platform: "windows", arch: "amd64", format: "exe" },
};

export async function fetchDownloads(): Promise<DownloadEntry[]> {
  const response = await fetch(`${CONVEX_URL}/downloads/list`, {
    next: { revalidate: 300 },
  });

  if (!response.ok) {
    throw new Error(`Failed to load downloads: ${response.status}`);
  }

  const data = (await response.json()) as { downloads?: DownloadEntry[] };
  return Array.isArray(data.downloads) ? data.downloads : [];
}

export async function fetchClientConfig(): Promise<{
  cliVersion?: string;
  mobileVersion?: string;
}> {
  const response = await fetch(`${CONVEX_URL}/config`, {
    next: { revalidate: 300 },
  });

  if (!response.ok) {
    return {};
  }

  return (await response.json()) as { cliVersion?: string; mobileVersion?: string };
}

export function findDownload(
  downloads: DownloadEntry[],
  target: { platform: DownloadEntry["platform"]; arch: string; format: string }
) {
  return downloads.find(
    (entry) =>
      entry.platform === target.platform &&
      entry.arch === target.arch &&
      entry.format === target.format &&
      entry.url
  );
}

export function formatFileSize(size: number) {
  if (!Number.isFinite(size) || size <= 0) return null;
  const mb = size / (1024 * 1024);
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  return `${mb.toFixed(1)} MB`;
}
