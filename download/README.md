# download.yaver.io — Worker + R2 APK hub

Public, HTTPS, `$0`-egress distribution for the Yaver Android app, plus the
QR install page. The serverless analog of talos's Hetzner APK server.

- **`src/index.js`** — Worker: serves the QR install page at `/` (the
  `index.html` object in R2) and streams every other key from the `yaver-apk`
  bucket (APK, `version.json`, `.well-known/assetlinks.json`, setup pages).
  Supports HTTP Range (download resume) and conditional GETs.
- **`wrangler.toml`** — binds the bucket and claims the `download.yaver.io`
  custom domain.

## Publish a new APK + refresh the page

```bash
# 1. build the release AAB (bumps versionCode, signs)
scripts/deploy-playstore.sh

# 2. build the universal APK + push apk/latest.apk/version.json/index.html/
#    assetlinks.json to the yaver-apk bucket
scripts/publish-android-r2.sh
```

The page itself (`scripts/download-site/index.html`) is version-agnostic — it
reads `version.json` client-side — so a new release only re-uploads
`latest.apk` + `version.json`; the Worker needs no redeploy.

## First-time deploy (one-time migration)

`download.yaver.io` is currently an **R2 custom domain** on the `yaver-apk`
bucket. A hostname can't be both an R2 custom domain and a Worker custom
domain, so:

1. Cloudflare dashboard → **R2 → `yaver-apk` → Settings → Custom Domains** →
   remove `download.yaver.io`.
2. Make sure the page object exists: `scripts/publish-android-r2.sh` (uploads
   `index.html` among the rest).
3. `cd download && npx wrangler deploy` — wrangler recreates
   `download.yaver.io` as this Worker's custom domain (DNS + cert managed).

After the cutover the Worker reads objects through its R2 **binding**, so
`/latest.apk`, `/version.json`, `/.well-known/assetlinks.json` keep serving
exactly as before — plus `/` now renders the QR page instead of 404.

## Local dev

```bash
cd download && npx wrangler dev   # binds the real remote bucket
```
