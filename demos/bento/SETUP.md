# Setup — Bento

This file walks you through every manual step that a generator can't automate. Take them in order.

## 1. Domain & DNS

- Sign in at https://dash.cloudflare.com and add the zone `bento.yaver.dev` (Cloudflare will give you two nameservers).
- At your domain registrar, replace the current nameservers with Cloudflare's.
- Wait for the zone to go Active (5 min–24 h), then run `cd web && npx wrangler deploy`.
- The wrangler config already has a route for `bento.yaver.dev`, so the first deploy will wire production immediately.

## 2. OAuth providers

### Apple Sign-In

- https://developer.apple.com/account/resources/identifiers/list/serviceId — create a Services ID.
  - Identifier: `io.yaver.bento.auth`
  - Return URL: `https://bento.yaver.dev/auth/callback/apple`
- https://developer.apple.com/account/resources/authkeys/list — create a Sign in with Apple key. Save the .p8 file and the Key ID.
- Paste the Key ID, Team ID, and .p8 path into `.env` under `APPLE_CLIENT_ID`, `APPLE_KEY_ID`, `APPLE_TEAM_ID`, `APPLE_PRIVATE_KEY_PATH`.

### Google Sign-In

- https://console.cloud.google.com/apis/credentials — create an OAuth client ID (Type: Web).
  - Authorized redirect URI: `https://bento.yaver.dev/auth/callback/google`
- Copy the Client ID + Client Secret into `.env` under `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`.

### Email + password fallback

- No external signup; the Convex `users` table stores hashed passwords out of the box.

## 3. iOS TestFlight

- Grab your Team ID from https://developer.apple.com/account (top right).
- https://appstoreconnect.apple.com/access/api — create an App Store Connect API key with Admin or App Manager role.
- Save the .p8 file and note the Key ID + Issuer ID.
- Put them in `.env` under `APP_STORE_KEY_PATH`, `APP_STORE_KEY_ID`, `APP_STORE_KEY_ISSUER`, `APPLE_TEAM_ID`.
- Deploy with `./scripts/deploy.sh testflight`.

## 4. Android Play Store

- https://play.google.com/console — create your app listing.
- https://console.cloud.google.com/iam-admin/serviceaccounts — create a service account, grant it access in Play Console under Users & Permissions.
- Download the JSON key.
- Put the path in `.env` under `PLAY_STORE_KEY_FILE`.
- Deploy with `./scripts/deploy.sh playstore`.

## Done

Once `.env` is filled in:

```bash
./scripts/deploy.sh web
```

Check back into the wizard any time with `yaver new --resume bento`.
