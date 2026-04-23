import { getAvailableModules, loadApp } from "./bundleLoader";

export interface ThirdPartyPreviewGitRef {
  repoUrl?: string;
  branch?: string;
  commit?: string;
  path?: string;
}

export interface ThirdPartyPreviewFeedbackConfig {
  sdk?: "yaver";
  compileTimeInjected?: boolean;
}

export interface ThirdPartyPreviewCIConfig {
  provider?: string;
  workflow?: string;
  runId?: string;
}

export interface ThirdPartyPreviewSharing {
  hostVisible?: boolean;
  guestVisible?: boolean;
}

export interface ThirdPartyPreviewNativeModules {
  required?: string[];
}

export interface ThirdPartyPreviewManifest {
  version: number;
  name: string;
  description?: string;
  bundleUrl: string;
  moduleName?: string;
  framework?: string;
  runtime?: "hermes";
  git?: ThirdPartyPreviewGitRef;
  headers?: Record<string, string>;
  nativeModules?: ThirdPartyPreviewNativeModules;
  feedback?: ThirdPartyPreviewFeedbackConfig;
  ci?: ThirdPartyPreviewCIConfig;
  sharing?: ThirdPartyPreviewSharing;
}

export interface ThirdPartyPreviewCompatibility {
  ok: boolean;
  missingModules: string[];
  availableModules: string[];
}

function isHttpUrl(value: string): boolean {
  return /^https?:\/\//i.test(value);
}

export function normalizeThirdPartyPreviewManifest(
  input: ThirdPartyPreviewManifest,
): ThirdPartyPreviewManifest {
  const name = input.name?.trim();
  const bundleUrl = input.bundleUrl?.trim();
  if (!name) throw new Error("Preview manifest is missing a name.");
  if (!bundleUrl || !isHttpUrl(bundleUrl)) {
    throw new Error("Preview manifest must include an http(s) bundleUrl.");
  }
  return {
    ...input,
    name,
    bundleUrl,
    moduleName: input.moduleName?.trim() || "main",
    runtime: input.runtime ?? "hermes",
    version: Number.isFinite(input.version) ? input.version : 1,
  };
}

export function createThirdPartyPreviewManifest(
  input: Omit<ThirdPartyPreviewManifest, "version" | "runtime"> &
    Partial<Pick<ThirdPartyPreviewManifest, "version" | "runtime">>,
): ThirdPartyPreviewManifest {
  return normalizeThirdPartyPreviewManifest({
    ...input,
    version: input.version ?? 1,
    runtime: input.runtime ?? "hermes",
  });
}

export function serializeThirdPartyPreviewManifest(
  manifest: ThirdPartyPreviewManifest,
): string {
  return JSON.stringify(normalizeThirdPartyPreviewManifest(manifest), null, 2);
}

export async function fetchThirdPartyPreviewManifest(
  manifestUrl: string,
  headers?: Record<string, string>,
): Promise<ThirdPartyPreviewManifest> {
  if (!isHttpUrl(manifestUrl)) {
    throw new Error("Manifest URL must be an http(s) URL.");
  }
  const res = await fetch(manifestUrl, { headers });
  const body = await res.text();
  if (!res.ok) throw new Error(body || `HTTP ${res.status}`);
  return normalizeThirdPartyPreviewManifest(JSON.parse(body) as ThirdPartyPreviewManifest);
}

export async function checkThirdPartyPreviewCompatibility(
  manifest: ThirdPartyPreviewManifest,
): Promise<ThirdPartyPreviewCompatibility> {
  const normalized = normalizeThirdPartyPreviewManifest(manifest);
  const availableModules = await getAvailableModules();
  const required = normalized.nativeModules?.required ?? [];
  const availableSet = new Set(availableModules);
  const missingModules = required.filter((name) => !availableSet.has(name));
  return {
    ok: missingModules.length === 0,
    missingModules,
    availableModules,
  };
}

export async function launchThirdPartyPreview(
  manifest: ThirdPartyPreviewManifest,
  opts: { requestHeaders?: Record<string, string> } = {},
): Promise<{ compatibility: ThirdPartyPreviewCompatibility }> {
  const normalized = normalizeThirdPartyPreviewManifest(manifest);
  const compatibility = await checkThirdPartyPreviewCompatibility(normalized);
  if (!compatibility.ok) {
    throw new Error(`Preview bundle requires missing native modules: ${compatibility.missingModules.join(", ")}`);
  }
  await loadApp(
    normalized.bundleUrl,
    normalized.moduleName,
    { ...(opts.requestHeaders ?? {}), ...(normalized.headers ?? {}) },
  );
  return { compatibility };
}
