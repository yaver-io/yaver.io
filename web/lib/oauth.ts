import crypto from "crypto";

export type OAuthProvider =
  | "google"
  | "microsoft"
  | "apple"
  | "github"
  | "gitlab";

type ProviderConfig = {
  authUrl: string;
  tokenUrl: string;
  userInfoUrl: string;
  emailUrl?: string;
  clientId: string;
  clientSecret: string;
  scope: string;
};

function envOr(defaultValue: string, envName: string): string {
  const value = process.env[envName];
  return value && value.trim() ? value.trim() : defaultValue;
}

function getBaseUrl(): string {
  if (process.env.NEXT_PUBLIC_BASE_URL) return process.env.NEXT_PUBLIC_BASE_URL;
  return "http://localhost:3000";
}

export function getCallbackUrl(provider: OAuthProvider): string {
  return `${getBaseUrl()}/api/auth/oauth/${provider}/callback`;
}

function getProviderConfig(provider: OAuthProvider): ProviderConfig {
  switch (provider) {
    case "google":
      return {
        authUrl: envOr("https://accounts.google.com/o/oauth2/v2/auth", "OAUTH_GOOGLE_AUTH_URL"),
        tokenUrl: envOr("https://oauth2.googleapis.com/token", "OAUTH_GOOGLE_TOKEN_URL"),
        userInfoUrl: envOr("https://www.googleapis.com/oauth2/v2/userinfo", "OAUTH_GOOGLE_USERINFO_URL"),
        clientId: process.env.OAUTH_GOOGLE_CLIENT_ID || "",
        clientSecret: process.env.OAUTH_GOOGLE_CLIENT_SECRET || "",
        scope: "openid email profile",
      };
    case "microsoft":
      return {
        authUrl: envOr(
          `https://login.microsoftonline.com/${process.env.OAUTH_MICROSOFT_TENANT_ID || "common"}/oauth2/v2.0/authorize`,
          "OAUTH_MICROSOFT_AUTH_URL",
        ),
        tokenUrl: envOr(
          `https://login.microsoftonline.com/${process.env.OAUTH_MICROSOFT_TENANT_ID || "common"}/oauth2/v2.0/token`,
          "OAUTH_MICROSOFT_TOKEN_URL",
        ),
        userInfoUrl: envOr("https://graph.microsoft.com/v1.0/me", "OAUTH_MICROSOFT_USERINFO_URL"),
        clientId: process.env.OAUTH_MICROSOFT_CLIENT_ID || "",
        clientSecret: process.env.OAUTH_MICROSOFT_CLIENT_SECRET || "",
        scope: "openid email profile",
      };
    case "apple":
      return {
        authUrl: envOr("https://appleid.apple.com/auth/authorize", "OAUTH_APPLE_AUTH_URL"),
        tokenUrl: envOr("https://appleid.apple.com/auth/token", "OAUTH_APPLE_TOKEN_URL"),
        userInfoUrl: "",
        clientId: process.env.OAUTH_APPLE_CLIENT_ID || "",
        clientSecret: process.env.OAUTH_APPLE_CLIENT_SECRET || "",
        scope: "name email",
      };
    case "github":
      return {
        authUrl: envOr("https://github.com/login/oauth/authorize", "OAUTH_GITHUB_AUTH_URL"),
        tokenUrl: envOr("https://github.com/login/oauth/access_token", "OAUTH_GITHUB_TOKEN_URL"),
        userInfoUrl: envOr("https://api.github.com/user", "OAUTH_GITHUB_USERINFO_URL"),
        emailUrl: envOr("https://api.github.com/user/emails", "OAUTH_GITHUB_EMAILS_URL"),
        clientId: process.env.OAUTH_GITHUB_CLIENT_ID || "",
        clientSecret: process.env.OAUTH_GITHUB_CLIENT_SECRET || "",
        scope: "read:user user:email",
      };
    case "gitlab":
      return {
        authUrl: envOr("https://gitlab.com/oauth/authorize", "OAUTH_GITLAB_AUTH_URL"),
        tokenUrl: envOr("https://gitlab.com/oauth/token", "OAUTH_GITLAB_TOKEN_URL"),
        userInfoUrl: envOr("https://gitlab.com/oauth/userinfo", "OAUTH_GITLAB_USERINFO_URL"),
        clientId: process.env.OAUTH_GITLAB_CLIENT_ID || "",
        clientSecret: process.env.OAUTH_GITLAB_CLIENT_SECRET || "",
        scope: "openid profile email",
      };
    default:
      throw new Error(`Unknown OAuth provider: ${provider}`);
  }
}

export function isProviderConfigured(provider: OAuthProvider): boolean {
  const config = getProviderConfig(provider);
  return !!(config.clientId && config.clientSecret);
}

type OAuthState = {
  client?: string;
  returnTo?: string;
  intent?: "signin" | "link";
  linkToken?: string;
  openerOrigin?: string;
};

export function sanitizeReturnTo(value?: string | null): string | undefined {
  if (!value) return undefined;
  const trimmed = value.trim();
  if (!trimmed.startsWith("/") || trimmed.startsWith("//")) return undefined;
  return trimmed;
}

export function sanitizeOpenerOrigin(value?: string | null): string | undefined {
  if (!value) return undefined;
  const trimmed = value.trim();
  try {
    const url = new URL(trimmed);
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    return url.origin;
  } catch {
    return undefined;
  }
}

export function encodeOAuthState(state: OAuthState): string {
  return Buffer.from(JSON.stringify(state)).toString("base64url");
}

export function decodeOAuthState(encoded: string): OAuthState {
  return JSON.parse(Buffer.from(encoded, "base64url").toString("utf-8"));
}

export function buildAuthorizationUrl(provider: OAuthProvider, state: string): string {
  const config = getProviderConfig(provider);
  const params = new URLSearchParams({
    client_id: config.clientId,
    redirect_uri: getCallbackUrl(provider),
    response_type: "code",
    scope: config.scope,
    state,
  });

  if (provider === "google") {
    params.set("access_type", "offline");
    params.set("prompt", "select_account");
  }

  if (provider === "microsoft") {
    params.set("response_mode", "query");
    params.set("prompt", "select_account");
  }

  if (provider === "apple") {
    params.set("response_mode", "form_post");
  }

  return `${config.authUrl}?${params.toString()}`;
}

type OAuthTokens = {
  access_token: string;
  id_token?: string;
  token_type: string;
};

export async function exchangeCodeForTokens(
  provider: OAuthProvider,
  code: string
): Promise<OAuthTokens> {
  const config = getProviderConfig(provider);
  const body = new URLSearchParams({
    client_id: config.clientId,
    client_secret: config.clientSecret,
    code,
    grant_type: "authorization_code",
    redirect_uri: getCallbackUrl(provider),
  });

  const res = await fetch(config.tokenUrl, {
    method: "POST",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Accept: "application/json",
    },
    body: body.toString(),
  });

  if (!res.ok) {
    const text = await res.text();
    throw new Error(`Token exchange failed: ${text}`);
  }

  return await res.json();
}

export type OAuthUserInfo = {
  email: string;
  name?: string;
  providerId: string;
  avatarUrl?: string;
  username?: string;
};

function decodeJwtPayload(jwt: string): Record<string, unknown> {
  const parts = jwt.split(".");
  if (parts.length !== 3) throw new Error("Invalid JWT");
  return JSON.parse(Buffer.from(parts[1], "base64url").toString("utf-8"));
}

export async function getUserInfo(
  provider: OAuthProvider,
  tokens: OAuthTokens
): Promise<OAuthUserInfo> {
  if (provider === "apple") {
    if (!tokens.id_token) throw new Error("Apple did not return id_token");
    const payload = decodeJwtPayload(tokens.id_token);
    return {
      email: payload.email as string,
      name: undefined,
      providerId: payload.sub as string,
    };
  }

  if (provider === "microsoft") {
    if (!tokens.id_token) throw new Error("Microsoft did not return id_token");
    const payload = decodeJwtPayload(tokens.id_token);
    return {
      email: (payload.email || payload.preferred_username) as string,
      name: payload.name as string | undefined,
      providerId: payload.sub as string,
    };
  }

  const config = getProviderConfig(provider);
  const res = await fetch(config.userInfoUrl, {
    method: "GET",
    headers: {
      Authorization: `Bearer ${tokens.access_token}`,
      Accept: "application/json",
      "User-Agent": "Yaver OAuth",
    },
  });

  if (!res.ok) {
    throw new Error(`UserInfo request failed: ${await res.text()}`);
  }

  const data = await res.json();

  if (provider === "google") {
    return {
      email: data.email,
      name: data.name,
      providerId: data.id,
      avatarUrl: data.picture,
    };
  }

  if (provider === "github") {
    let email = typeof data.email === "string" ? data.email : "";
    if (!email) {
      const emailsRes = await fetch(config.emailUrl || "https://api.github.com/user/emails", {
        headers: {
          Authorization: `Bearer ${tokens.access_token}`,
          Accept: "application/json",
          "User-Agent": "Yaver OAuth",
        },
      });
      if (!emailsRes.ok) {
        throw new Error(`GitHub email request failed: ${await emailsRes.text()}`);
      }
      const emails = await emailsRes.json();
      if (Array.isArray(emails)) {
        const primaryVerified = emails.find((entry) => entry?.primary && entry?.verified && typeof entry?.email === "string");
        const verified = emails.find((entry) => entry?.verified && typeof entry?.email === "string");
        const anyEmail = emails.find((entry) => typeof entry?.email === "string");
        email = primaryVerified?.email || verified?.email || anyEmail?.email || "";
      }
    }
    if (!email) {
      throw new Error("GitHub did not return an email address");
    }
    return {
      email,
      name: data.name || data.login,
      providerId: String(data.id),
      avatarUrl: data.avatar_url,
      username: data.login,
    };
  }

  if (provider === "gitlab") {
    return {
      email: data.email,
      name: data.name || data.nickname || data.preferred_username,
      providerId: String(data.sub),
      avatarUrl: data.picture,
      username: data.preferred_username || data.nickname,
    };
  }

  throw new Error(`Unknown provider: ${provider}`);
}
