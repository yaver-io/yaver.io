import { NextResponse } from "next/server";
import {
  type OAuthProvider,
  decodeOAuthState,
  exchangeCodeForTokens,
  getUserInfo,
} from "@/lib/oauth";
import {
  createSessionToken,
  hashSessionToken,
  sessionExpiresAtMs,
} from "@/lib/session";

const VALID_PROVIDERS = new Set<OAuthProvider>([
  "google",
  "microsoft",
  "apple",
  "github",
  "gitlab",
]);

function extractDeviceCode(returnTo?: string): string | null {
  if (!returnTo) return null;
  try {
    const url = new URL(returnTo, "https://yaver.io");
    if (url.pathname !== "/auth/device") return null;
    const code = (url.searchParams.get("code") || "").trim().toUpperCase();
    return code || null;
  } catch {
    return null;
  }
}

function getBaseUrl(): string {
  if (process.env.NEXT_PUBLIC_BASE_URL) return process.env.NEXT_PUBLIC_BASE_URL;
  return "http://localhost:3000";
}

function getConvexSiteUrl(): string {
  return process.env.CONVEX_SITE_URL || "";
}

async function logToConvex(
  provider: string,
  step: string,
  level: "info" | "error" | "warn",
  message: string,
  details?: string
) {
  const convexSiteUrl = getConvexSiteUrl();
  if (!convexSiteUrl) return;
  try {
    await fetch(`${convexSiteUrl}/auth/log`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ level, provider, step, message, details }),
    });
  } catch {
    // Best-effort logging, don't block the flow
  }
}

function errorRedirect(message: string): NextResponse {
  const url = new URL("/auth", getBaseUrl());
  url.searchParams.set("error", message);
  return NextResponse.redirect(url, 303);
}

async function handleCallback(
  provider: OAuthProvider,
  code: string,
  stateParam: string
) {
  const state = decodeOAuthState(stateParam);
  await logToConvex(provider, "callback_start", "info", `OAuth callback started`, `client=${state.client || "web"}`);

  let tokens;
  try {
    tokens = await exchangeCodeForTokens(provider, code);
    await logToConvex(provider, "token_exchange", "info", "Token exchange succeeded");
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    await logToConvex(provider, "token_exchange", "error", "Token exchange failed", msg);
    throw err;
  }

  let userInfo;
  try {
    userInfo = await getUserInfo(provider, tokens);
    await logToConvex(provider, "get_user_info", "info", `Got user info: ${userInfo.email}`);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    await logToConvex(provider, "get_user_info", "error", "getUserInfo failed", msg);
    throw err;
  }

  if (!userInfo.email) {
    await logToConvex(provider, "get_user_info", "error", "No email from provider");
    return errorRedirect("Could not retrieve email from provider.");
  }

  const convexSiteUrl = getConvexSiteUrl();
  if (!convexSiteUrl) {
    throw new Error("CONVEX_SITE_URL is not set");
  }

  const baseUrl = getBaseUrl();
  if (state.intent === "link" && state.linkToken) {
    const linkRes = await fetch(`${convexSiteUrl}/auth/oauth-link/complete`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        linkToken: state.linkToken,
        provider,
        providerId: userInfo.providerId,
        email: userInfo.email.toLowerCase(),
        fullName: userInfo.name || "",
        avatarUrl: userInfo.avatarUrl,
      }),
    });
    if (!linkRes.ok) {
      const text = await linkRes.text();
      await logToConvex(provider, "oauth_link", "error", "OAuth link failed", text);
      throw new Error(`OAuth link failed: ${text}`);
    }
    await logToConvex(provider, "oauth_link", "info", "OAuth link completed");

    if (state.client === "mobile") {
      const mobileUrl = new URL(process.env.MOBILE_DEEP_LINK || "yaver://oauth-callback");
      mobileUrl.searchParams.set("linkedProvider", provider);
      mobileUrl.searchParams.set("linked", "1");
      return NextResponse.redirect(mobileUrl.toString(), 303);
    }

    const linkUrl = new URL(state.returnTo || "/dashboard", baseUrl);
    linkUrl.searchParams.set("linkedProvider", provider);
    linkUrl.searchParams.set("linked", "1");
    return NextResponse.redirect(linkUrl.toString(), 303);
  }

  // Upsert user via Convex HTTP action
  let userId;
  try {
    const userRes = await fetch(`${convexSiteUrl}/auth/upsert-user`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        email: userInfo.email.toLowerCase(),
        fullName: userInfo.name || "",
        provider,
        providerId: userInfo.providerId,
        avatarUrl: userInfo.avatarUrl,
      }),
    });

    if (!userRes.ok) {
      const text = await userRes.text();
      await logToConvex(provider, "upsert_user", "error", "User upsert failed", text);
      throw new Error(`User upsert failed: ${text}`);
    }

    const data = await userRes.json();
    userId = data.userId;
    await logToConvex(provider, "upsert_user", "info", `User upserted: ${userId}`);
  } catch (err) {
    if (!(err instanceof Error && err.message.startsWith("User upsert failed"))) {
      const msg = err instanceof Error ? err.message : String(err);
      await logToConvex(provider, "upsert_user", "error", "User upsert exception", msg);
    }
    throw err;
  }

  // Check if user has 2FA enabled before creating session
  try {
    const totpCheckRes = await fetch(`${convexSiteUrl}/auth/totp/check-user`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ userId }),
    });

    if (totpCheckRes.ok) {
      const totpData = await totpCheckRes.json();
      if (totpData.totpEnabled) {
        // Create pending auth instead of session
        const pendingRes = await fetch(`${convexSiteUrl}/auth/totp/create-pending`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ userId }),
        });

        if (pendingRes.ok) {
          const { pendingToken } = await pendingRes.json();
          await logToConvex(provider, "2fa_required", "info", "2FA required, redirecting to TOTP page");

          // All clients go to web TOTP page first
          const totpUrl = new URL("/auth/totp", baseUrl);
          totpUrl.searchParams.set("pendingToken", pendingToken);
          totpUrl.searchParams.set("client", state.client || "web");
          if (state.returnTo) {
            totpUrl.searchParams.set("return", state.returnTo);
          }
          return NextResponse.redirect(totpUrl.toString(), 303);
        }
      }
    }
  } catch (err) {
    // If TOTP check fails, fall through to normal session creation
    await logToConvex(provider, "2fa_check", "warn", "TOTP check failed, proceeding without 2FA");
  }

  // Create session via Convex HTTP action
  const token = createSessionToken();
  const tokenHash = hashSessionToken(token);
  const expiresAt = sessionExpiresAtMs();

  try {
    const sessionRes = await fetch(`${convexSiteUrl}/auth/create-session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ tokenHash, userId, expiresAt }),
    });

    if (!sessionRes.ok) {
      const text = await sessionRes.text();
      await logToConvex(provider, "create_session", "error", "Session creation failed", text);
      throw new Error(`Session creation failed: ${text}`);
    }

    await logToConvex(provider, "create_session", "info", "Session created successfully");
  } catch (err) {
    if (!(err instanceof Error && err.message.startsWith("Session creation failed"))) {
      const msg = err instanceof Error ? err.message : String(err);
      await logToConvex(provider, "create_session", "error", "Session creation exception", msg);
    }
    throw err;
  }

  const deepLink = process.env.MOBILE_DEEP_LINK || "yaver://oauth-callback";

  // Mobile client: redirect to deep link
  if (state.client === "mobile") {
    const mobileUrl = new URL(deepLink);
    mobileUrl.searchParams.set("token", token);
    mobileUrl.searchParams.set("provider", provider);
    await logToConvex(provider, "redirect", "info", "Redirecting to mobile deep link");
    return NextResponse.redirect(mobileUrl.toString(), 303);
  }

  // Desktop CLI client: redirect via bridge page (Safari blocks HTTPS→HTTP server redirects)
  if (state.client === "desktop") {
    const bridgeUrl = new URL("/auth/desktop-callback", baseUrl);
    bridgeUrl.searchParams.set("token", token);
    await logToConvex(provider, "redirect", "info", "Redirecting to desktop bridge page");
    const response = NextResponse.redirect(bridgeUrl.toString(), 303);
    response.cookies.set("yaver_auth_token", token, {
      path: "/",
      maxAge: 60 * 60 * 24 * 30,
      sameSite: "lax",
      secure: true,
      httpOnly: false,
    });
    return response;
  }

  // SDK client (yaver-feedback-web popup): redirect to a page that
  // window.opener.postMessage(token) and closes itself.
  if (state.client === "sdk") {
    const sdkUrl = new URL("/auth/sdk-callback", baseUrl);
    sdkUrl.searchParams.set("token", token);
    await logToConvex(provider, "redirect", "info", "Redirecting to SDK popup callback");
    return NextResponse.redirect(sdkUrl.toString(), 303);
  }

  const deviceCode = extractDeviceCode(state.returnTo);
  if (deviceCode) {
    try {
      const authorizeRes = await fetch(`${convexSiteUrl}/auth/device-code/authorize`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ userCode: deviceCode }),
      });

      if (authorizeRes.ok) {
        const successUrl = new URL("/auth/device", baseUrl);
        successUrl.searchParams.set("code", deviceCode);
        successUrl.searchParams.set("authorized", "1");
        await logToConvex(provider, "device_authorize", "info", `Authorized device code ${deviceCode}`);
        const response = NextResponse.redirect(successUrl.toString(), 303);
        response.cookies.set("yaver_auth_token", token, {
          path: "/",
          maxAge: 60 * 60 * 24 * 30,
          sameSite: "lax",
          secure: true,
          httpOnly: false,
        });
        return response;
      }

      const body = await authorizeRes.text();
      await logToConvex(provider, "device_authorize", "warn", `Device auth fallback for ${deviceCode}`, body);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      await logToConvex(provider, "device_authorize", "warn", `Device auth fallback for ${deviceCode}`, msg);
    }
  }

  // Web client: redirect to /auth/callback which stores token in localStorage
  const callbackUrl = new URL("/auth/callback", baseUrl);
  callbackUrl.searchParams.set("token", token);
  if (state.returnTo) {
    callbackUrl.searchParams.set("return", state.returnTo);
  }
  await logToConvex(provider, "redirect", "info", "Redirecting to web /auth/callback");
  const response = NextResponse.redirect(callbackUrl.toString(), 303);
  response.cookies.set("yaver_auth_token", token, {
    path: "/",
    maxAge: 60 * 60 * 24 * 30,
    sameSite: "lax",
    secure: true,
    httpOnly: false,
  });
  return response;
}

export async function GET(
  request: Request,
  { params }: { params: Promise<{ provider: string }> }
) {
  const { provider: rawProvider } = await params;
  const provider = rawProvider as OAuthProvider;

  if (!VALID_PROVIDERS.has(provider)) {
    return errorRedirect("Invalid provider");
  }

  const url = new URL(request.url);
  const code = url.searchParams.get("code");
  const stateParam = url.searchParams.get("state");
  const oauthError = url.searchParams.get("error");

  if (oauthError) {
    await logToConvex(provider, "callback_error", "error", `OAuth error param: ${oauthError}`);
    return errorRedirect(`OAuth error: ${oauthError}`);
  }

  if (!code || !stateParam) {
    await logToConvex(provider, "callback_error", "error", "Missing code or state param");
    return errorRedirect("Missing authorization code.");
  }

  try {
    return await handleCallback(provider, code, stateParam);
  } catch (err) {
    const message = err instanceof Error ? err.message : "OAuth callback failed";
    console.error("OAuth callback error:", err);
    await logToConvex(provider, "callback_exception", "error", message);
    return errorRedirect(message);
  }
}

// Apple Sign In uses form_post response mode
export async function POST(
  request: Request,
  { params }: { params: Promise<{ provider: string }> }
) {
  const { provider: rawProvider } = await params;
  const provider = rawProvider as OAuthProvider;

  if (provider !== "apple") {
    return errorRedirect("POST callback only supported for Apple");
  }

  const formData = await request.formData();
  const code = formData.get("code") as string | null;
  const stateParam = formData.get("state") as string | null;
  const oauthError = formData.get("error") as string | null;

  if (oauthError) {
    await logToConvex(provider, "callback_error", "error", `OAuth error param (POST): ${oauthError}`);
    return errorRedirect(`OAuth error: ${oauthError}`);
  }

  if (!code || !stateParam) {
    await logToConvex(provider, "callback_error", "error", "Missing code or state (POST)");
    return errorRedirect("Missing authorization code.");
  }

  try {
    return await handleCallback(provider, code, stateParam);
  } catch (err) {
    const message = err instanceof Error ? err.message : "OAuth callback failed";
    console.error("OAuth callback error:", err);
    await logToConvex(provider, "callback_exception", "error", message);
    return errorRedirect(message);
  }
}
