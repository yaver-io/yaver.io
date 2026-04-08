# Yaver.io — Setup Checklist

## 1. Convex Backend

- [ ] Run `cd backend && npx convex dev` and select the `yaver-io` project
- [ ] Note the deployment URL from `.env.local` (e.g. `https://xxx.convex.cloud`)
- [ ] Note the site URL (e.g. `https://xxx.convex.site`) — this is where HTTP actions live

## 2. Google OAuth

- [ ] Go to [Google Cloud Console](https://console.cloud.google.com/)
- [ ] Create project "Yaver" (or use existing)
- [ ] Enable "Google Identity" API
- [ ] Go to **Credentials** > **Create Credentials** > **OAuth 2.0 Client ID**
- [ ] Application type: **Web application**
- [ ] Name: `Yaver`
- [ ] Authorized redirect URIs:
  - `https://<your-convex-site-url>/auth/google/callback` (for Convex HTTP action)
  - `https://yaver.io/auth/callback` (for web)
  - `http://localhost:3000/auth/callback` (for local dev)
- [ ] Copy **Client ID** and **Client Secret**
- [ ] Set in Convex:
  ```bash
  cd backend
  npx convex env set GOOGLE_CLIENT_ID "your-client-id"
  npx convex env set GOOGLE_CLIENT_SECRET "your-client-secret"
  ```

## 3. Microsoft / Office 365 OAuth

- [ ] Go to [Azure Portal - App registrations](https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationsListBlade)
- [ ] **New registration**
  - Name: `Yaver`
  - Supported account types: **Accounts in any organizational directory and personal Microsoft accounts**
  - Redirect URI (Web):
    - `https://<your-convex-site-url>/auth/microsoft/callback`
    - `https://yaver.io/auth/callback`
    - `http://localhost:3000/auth/callback`
- [ ] Copy **Application (client) ID**
- [ ] Go to **Certificates & secrets** > **New client secret** > Copy the **Value**
- [ ] Go to **API permissions** > **Add permission** > **Microsoft Graph** > **Delegated**:
  - `openid`
  - `profile`
  - `email`
  - `User.Read`
- [ ] Grant admin consent
- [ ] Set in Convex:
  ```bash
  cd backend
  npx convex env set MICROSOFT_CLIENT_ID "your-client-id"
  npx convex env set MICROSOFT_CLIENT_SECRET "your-client-secret"
  ```

## 4. Convex Environment Variables

- [ ] Set all env vars in Convex:
  ```bash
  cd backend
  npx convex env set AUTH_REDIRECT_URL "https://<your-convex-site-url>"
  npx convex env set MOBILE_DEEP_LINK "yaver://oauth-callback"
  npx convex env set GOOGLE_CLIENT_ID "..."
  npx convex env set GOOGLE_CLIENT_SECRET "..."
  npx convex env set MICROSOFT_CLIENT_ID "..."
  npx convex env set MICROSOFT_CLIENT_SECRET "..."
  ```
- [ ] Deploy: `cd backend && npx convex deploy`

## 5. Cloudflare Workers Environment

- [ ] Log in to [Cloudflare Dashboard](https://dash.cloudflare.com) > Workers & Pages > `yaver-io`
- [ ] Set secrets via CLI:
  ```bash
  cd web
  npx wrangler secret put NEXT_PUBLIC_CONVEX_URL      # https://<your-convex-deployment>.convex.cloud
  npx wrangler secret put NEXT_PUBLIC_CONVEX_SITE_URL  # https://<your-convex-deployment>.convex.site
  ```
- [ ] Deploy: `cd web && npm run deploy`

## 6. Domain Setup (Cloudflare DNS)

- [ ] DNS zone for `yaver.io` must be on Cloudflare (required for Workers routes)
- [ ] Routes are configured in `web/wrangler.toml`:
  - `yaver.io/*` → `yaver-io` worker
  - `www.yaver.io/*` → `yaver-io` worker
- [ ] SSL is automatic via Cloudflare (Universal SSL)

## 7. Mobile App Setup (later — after name decision)

- [ ] Apple Developer Account ($99/year)
  - Create App ID: `io.yaver.mobile`
  - Create provisioning profile
  - Register `yaver://` URL scheme
- [ ] Google Play Console ($25 one-time)
  - Create app: package `io.yaver.mobile`
  - Register deep link: `yaver://oauth-callback`
- [ ] Update `app.json` with final bundle IDs once name is decided

## 8. Desktop Code Signing (later)

- [ ] macOS: Apple Developer ID certificate for notarization
- [ ] Windows: Authenticode code signing certificate
- [ ] Update `desktop/installer/package.json` with signing config
