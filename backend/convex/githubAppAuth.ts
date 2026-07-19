import { SignJWT, importPKCS8 } from "jose";

export type GitHubAppEnv = {
  appId: string;
  privateKey: string;
  apiBaseUrl: string;
  apiVersion: string;
};

export type GitHubAppRepo = {
  owner: string;
  repo: string;
  fullName: string;
};

export function normalizeGitHubPrivateKey(raw: string | undefined): string {
  const key = String(raw || "").trim();
  if (!key) return "";
  if (key.includes("-----BEGIN")) return key.replace(/\\n/g, "\n");
  try {
    const decoded = atob(key);
    return decoded.includes("-----BEGIN") ? decoded : key;
  } catch {
    return key;
  }
}

export function githubAppEnvFromProcess(env: Record<string, string | undefined> = process.env): GitHubAppEnv | null {
  const appId = String(env.YAVER_GITHUB_APP_ID || env.GITHUB_APP_ID || "").trim();
  const privateKey = normalizeGitHubPrivateKey(env.YAVER_GITHUB_APP_PRIVATE_KEY || env.GITHUB_APP_PRIVATE_KEY);
  if (!appId || !privateKey) return null;
  return {
    appId,
    privateKey,
    apiBaseUrl: String(env.YAVER_GITHUB_API_BASE_URL || "https://api.github.com").replace(/\/+$/, ""),
    apiVersion: String(env.YAVER_GITHUB_API_VERSION || "2022-11-28").trim(),
  };
}

export function parseGitHubRepoFullName(value: string | undefined): GitHubAppRepo | null {
  const fullName = String(value || "").trim().replace(/^\/+|\/+$/g, "");
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(fullName)) return null;
  const [owner, repo] = fullName.split("/");
  return { owner, repo, fullName };
}

export function githubInstallationTokenRequestBody(repo: GitHubAppRepo) {
  return {
    repositories: [repo.repo],
    permissions: { contents: "write" },
  };
}

export async function createGitHubAppJwt(env: GitHubAppEnv, nowSeconds = Math.floor(Date.now() / 1000)): Promise<string> {
  const key = await importPKCS8(env.privateKey, "RS256");
  return await new SignJWT({})
    .setProtectedHeader({ alg: "RS256" })
    .setIssuedAt(nowSeconds - 60)
    .setExpirationTime(nowSeconds + 9 * 60)
    .setIssuer(env.appId)
    .sign(key);
}

export async function requestGitHubInstallationTokenForRepo(args: {
  env: GitHubAppEnv;
  repo: GitHubAppRepo;
  fetchImpl?: typeof fetch;
}): Promise<{
  token: string;
  expiresAt?: string;
  installationId: string;
  permissions?: unknown;
}> {
  const fetcher = args.fetchImpl || fetch;
  const jwt = await createGitHubAppJwt(args.env);
  const commonHeaders = {
    Accept: "application/vnd.github+json",
    Authorization: `Bearer ${jwt}`,
    "X-GitHub-Api-Version": args.env.apiVersion,
  };
  const installResp = await fetcher(
    `${args.env.apiBaseUrl}/repos/${encodeURIComponent(args.repo.owner)}/${encodeURIComponent(args.repo.repo)}/installation`,
    { headers: commonHeaders },
  );
  const installRaw = await installResp.text();
  if (!installResp.ok) {
    throw new Error(`github app installation lookup failed (${installResp.status}): ${installRaw.slice(0, 300)}`);
  }
  const install = JSON.parse(installRaw || "{}");
  const installationId = String(install.id || "").trim();
  if (!installationId) throw new Error("github app installation id missing");

  const tokenResp = await fetcher(
    `${args.env.apiBaseUrl}/app/installations/${encodeURIComponent(installationId)}/access_tokens`,
    {
      method: "POST",
      headers: { ...commonHeaders, "Content-Type": "application/json" },
      body: JSON.stringify(githubInstallationTokenRequestBody(args.repo)),
    },
  );
  const tokenRaw = await tokenResp.text();
  if (!tokenResp.ok) {
    throw new Error(`github app installation token failed (${tokenResp.status}): ${tokenRaw.slice(0, 300)}`);
  }
  const token = JSON.parse(tokenRaw || "{}");
  const accessToken = String(token.token || "").trim();
  if (!accessToken) throw new Error("github app installation token missing");
  return {
    token: accessToken,
    expiresAt: typeof token.expires_at === "string" ? token.expires_at : undefined,
    installationId,
    permissions: token.permissions,
  };
}
