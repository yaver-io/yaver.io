import * as FileSystem from 'expo-file-system';
import { Linking, Platform } from 'react-native';

export interface BuildInfo {
  id: string;
  platform: string;
  status: 'running' | 'completed' | 'failed' | 'cancelled';
  command?: string;
  workDir?: string;
  artifactName?: string;
  artifactSize?: number;
  artifactHash?: string;
  startedAt: string;
  finishedAt?: string;
  exitCode?: number;
  error?: string;
  installOnDevice?: boolean;
  installStatus?: '' | 'installing' | 'installed' | 'install_failed';
  installError?: string;
  deviceUDID?: string;
}

export interface BuildSummary {
  id: string;
  platform: string;
  status: string;
  artifactName?: string;
  artifactSize?: number;
  startedAt: string;
  finishedAt?: string;
}

export interface DownloadProgress {
  totalBytes: number;
  downloadedBytes: number;
  percent: number;
}

// Build directory in app cache
const BUILDS_DIR = `${FileSystem.cacheDirectory}builds/`;

async function ensureBuildsDir() {
  const info = await FileSystem.getInfoAsync(BUILDS_DIR);
  if (!info.exists) {
    await FileSystem.makeDirectoryAsync(BUILDS_DIR, { intermediates: true });
  }
}

// Download artifact from CLI agent via P2P
// baseUrl is the QuicClient's baseUrl (direct, relay, or tunnel)
// Returns local file path
export async function downloadArtifact(
  baseUrl: string,
  authHeaders: Record<string, string>,
  buildId: string,
  onProgress?: (progress: DownloadProgress) => void
): Promise<string> {
  await ensureBuildsDir();

  // First get build info to know artifact name and hash
  const infoResp = await fetch(`${baseUrl}/builds/${buildId}`, {
    headers: authHeaders,
  });
  if (!infoResp.ok) throw new Error(`Failed to get build info: ${infoResp.status}`);
  const build: BuildInfo = await infoResp.json();

  if (!build.artifactName) throw new Error('No artifact available for this build');

  const localPath = BUILDS_DIR + build.artifactName;

  // Download with progress using expo-file-system
  const downloadResumable = FileSystem.createDownloadResumable(
    `${baseUrl}/builds/${buildId}/artifact`,
    localPath,
    { headers: authHeaders },
    (downloadProgress) => {
      if (onProgress && downloadProgress.totalBytesExpectedToWrite > 0) {
        onProgress({
          totalBytes: downloadProgress.totalBytesExpectedToWrite,
          downloadedBytes: downloadProgress.totalBytesWritten,
          percent: Math.round(
            (downloadProgress.totalBytesWritten / downloadProgress.totalBytesExpectedToWrite) * 100
          ),
        });
      }
    }
  );

  const result = await downloadResumable.downloadAsync();
  if (!result) throw new Error('Download failed');

  // Verify SHA256 if hash is available
  // Note: expo-file-system doesn't have built-in hash. We trust the server hash
  // and verify content-length match for now.
  if (build.artifactSize && result.headers?.['content-length']) {
    const downloadedSize = parseInt(result.headers['content-length'], 10);
    if (downloadedSize !== build.artifactSize) {
      await FileSystem.deleteAsync(localPath, { idempotent: true });
      throw new Error(`Size mismatch: expected ${build.artifactSize}, got ${downloadedSize}`);
    }
  }

  return localPath;
}

// Get list of cached build artifacts
export async function listCachedArtifacts(): Promise<string[]> {
  await ensureBuildsDir();
  const files = await FileSystem.readDirectoryAsync(BUILDS_DIR);
  return files.map(f => BUILDS_DIR + f);
}

// Delete cached artifacts
export async function clearCachedArtifacts(): Promise<void> {
  await FileSystem.deleteAsync(BUILDS_DIR, { idempotent: true });
  await ensureBuildsDir();
}

// Format file size for display
export function formatSize(bytes: number): string {
  if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GB`;
  if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${bytes} B`;
}

// Install IPA via iOS OTA (itms-services)
// manifestUrl MUST be HTTPS (works through relay)
export async function installIPA(manifestUrl: string): Promise<void> {
  if (Platform.OS !== 'ios') {
    throw new Error('iOS OTA install only available on iOS');
  }
  const url = `itms-services://?action=download-manifest&url=${encodeURIComponent(manifestUrl)}`;
  const canOpen = await Linking.canOpenURL(url);
  if (!canOpen) {
    throw new Error('Cannot open itms-services URL. The IPA must be signed with a dev profile including this device.');
  }
  await Linking.openURL(url);
}

// Check if the current platform can install a given artifact
export function canInstallArtifact(artifactName: string): boolean {
  if (!artifactName) return false;
  const lower = artifactName.toLowerCase();
  if (Platform.OS === 'android' && (lower.endsWith('.apk') || lower.endsWith('.aab'))) return true;
  if (Platform.OS === 'ios' && lower.endsWith('.ipa')) return true;
  return false;
}
