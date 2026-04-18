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
  | "raspi-image-arm64"
  | "linux-appimage-amd64"
  | "linux-appimage-arm64"
  | "linux-deb-amd64"
  | "linux-deb-arm64"
  | "linux-rpm-amd64"
  | "linux-rpm-arm64"
  | "linux-tarball-amd64"
  | "linux-tarball-arm64"
  | "macos-arm64"
  | "macos-x64"
  | "windows-x64";

export const DOWNLOAD_SLUGS: Record<
  DownloadSlug,
  { platform: DownloadEntry["platform"]; arch: string; format: string }
> = {
  "raspi-image-arm64": { platform: "linux", arch: "arm64", format: "image" },
  "linux-appimage-amd64": { platform: "linux", arch: "amd64", format: "appimage" },
  "linux-appimage-arm64": { platform: "linux", arch: "arm64", format: "appimage" },
  "linux-deb-amd64": { platform: "linux", arch: "amd64", format: "deb" },
  "linux-deb-arm64": { platform: "linux", arch: "arm64", format: "deb" },
  "linux-rpm-amd64": { platform: "linux", arch: "amd64", format: "rpm" },
  "linux-rpm-arm64": { platform: "linux", arch: "arm64", format: "rpm" },
  "linux-tarball-amd64": { platform: "linux", arch: "amd64", format: "tarball" },
  "linux-tarball-arm64": { platform: "linux", arch: "arm64", format: "tarball" },
  "macos-arm64": { platform: "macos", arch: "arm64", format: "tarball" },
  "macos-x64": { platform: "macos", arch: "amd64", format: "tarball" },
  "windows-x64": { platform: "windows", arch: "amd64", format: "exe" },
};

const AGENT_RELEASE_REPO = "kivanccakmak/yaver.io";
const CLI_RELEASE_REPO = "kivanccakmak/yaver-cli";

type GitHubRelease = {
  tag_name: string;
  html_url: string;
  draft: boolean;
  prerelease: boolean;
  assets: Array<{
    name: string;
    size: number;
    browser_download_url: string;
  }>;
};

export type DownloadFallback = {
  href: string;
  filename?: string;
  size?: number;
  version?: string;
  direct: boolean;
};

export const VERIFIED_DOWNLOAD_SLUGS = new Set<DownloadSlug>([
  "raspi-image-arm64",
  "linux-tarball-amd64",
  "linux-tarball-arm64",
  "macos-arm64",
  "macos-x64",
]);

async function fetchLatestSemverRelease(repo: string): Promise<GitHubRelease | null> {
  const response = await fetch(`https://api.github.com/repos/${repo}/releases?per_page=20`, {
    next: { revalidate: 300 },
    headers: {
      Accept: "application/vnd.github+json",
    },
  });

  if (!response.ok) {
    return null;
  }

  const releases = (await response.json()) as GitHubRelease[];
  return (
    releases.find(
      (release) =>
        !release.draft &&
        !release.prerelease &&
        /^v\d+(?:\.\d+)*(?:[-+][0-9A-Za-z.-]+)?$/.test(release.tag_name)
    ) ?? null
  );
}

function findGitHubAsset(release: GitHubRelease | null, names: string[]) {
  if (!release) return null;
  const assets = new Map(release.assets.map((asset) => [asset.name, asset]));
  for (const name of names) {
    const match = assets.get(name);
    if (match) return match;
  }
  return null;
}

function directFallbackFromAsset(
  release: GitHubRelease | null,
  names: string[]
): DownloadFallback | null {
  const asset = findGitHubAsset(release, names);
  if (!asset || !release) return null;
  return {
    href: asset.browser_download_url,
    filename: asset.name,
    size: asset.size,
    version: release.tag_name.replace(/^v/, ""),
    direct: true,
  };
}

function releasePageFallback(release: GitHubRelease | null): DownloadFallback | null {
  if (!release) return null;
  return {
    href: release.html_url,
    version: release.tag_name.replace(/^v/, ""),
    direct: false,
  };
}

export async function fetchDownloadFallbacks(): Promise<Partial<Record<DownloadSlug, DownloadFallback>>> {
  const [agentRelease, cliRelease] = await Promise.all([
    fetchLatestSemverRelease(AGENT_RELEASE_REPO),
    fetchLatestSemverRelease(CLI_RELEASE_REPO),
  ]);

  const agentReleasePage = releasePageFallback(agentRelease);
  const agentTag = agentRelease?.tag_name;

  return {
    "linux-tarball-amd64":
      directFallbackFromAsset(agentRelease, [
        agentTag ? `yaver-${agentTag}-linux-amd64.tar.gz` : "",
        "yaver-linux-amd64.tar.gz",
      ]) ??
      agentReleasePage ??
      undefined,
    "linux-tarball-arm64":
      directFallbackFromAsset(agentRelease, [
        agentTag ? `yaver-${agentTag}-linux-arm64.tar.gz` : "",
        "yaver-linux-arm64.tar.gz",
      ]) ??
      agentReleasePage ??
      undefined,
    "macos-arm64":
      directFallbackFromAsset(agentRelease, [
        agentTag ? `yaver-${agentTag}-darwin-arm64.tar.gz` : "",
        "yaver-darwin-arm64.tar.gz",
      ]) ??
      agentReleasePage ??
      undefined,
    "macos-x64":
      directFallbackFromAsset(agentRelease, [
        agentTag ? `yaver-${agentTag}-darwin-amd64.tar.gz` : "",
        "yaver-darwin-amd64.tar.gz",
      ]) ??
      agentReleasePage ??
      undefined,
  };
}

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
