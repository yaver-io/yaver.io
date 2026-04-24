import { NextResponse } from "next/server";
import {
  type OAuthProvider,
  isProviderConfigured,
  encodeOAuthState,
  sanitizeOpenerOrigin,
  buildAuthorizationUrl,
  sanitizeReturnTo,
} from "@/lib/oauth";

const VALID_PROVIDERS = new Set<OAuthProvider>([
  "google",
  "microsoft",
  "apple",
  "github",
  "gitlab",
]);

export async function GET(
  request: Request,
  { params }: { params: Promise<{ provider: string }> }
) {
  const { provider: rawProvider } = await params;
  const provider = rawProvider as OAuthProvider;

  if (!VALID_PROVIDERS.has(provider)) {
    return NextResponse.json({ error: "Invalid provider" }, { status: 400 });
  }

  if (!isProviderConfigured(provider)) {
    return NextResponse.json(
      { error: `${provider} OAuth is not configured` },
      { status: 501 }
    );
  }

  const url = new URL(request.url);
  const client = url.searchParams.get("client") || "web";
  const returnTo = sanitizeReturnTo(url.searchParams.get("return"));
  const openerOrigin = sanitizeOpenerOrigin(url.searchParams.get("origin"));
  const intent = url.searchParams.get("intent") === "link" ? "link" : "signin";
  const linkToken = url.searchParams.get("linkToken") || undefined;

  const state = encodeOAuthState({ client, returnTo, intent, linkToken, openerOrigin });
  const authUrl = buildAuthorizationUrl(provider, state);

  return NextResponse.redirect(authUrl);
}
