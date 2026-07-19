#!/usr/bin/env python3
"""Print the highest CFBundleVersion already on App Store Connect for the app.

Used by deploy-testflight.sh to bump from max(local, ASC) + 1 so a new build can
never collide with an existing one (ITMS "build number already used") and burn a
TestFlight upload slot. See CLAUDE.md → iOS TestFlight.

BEST-EFFORT by design: if PyJWT / cryptography / requests aren't importable, or
the network/API errors, this prints NOTHING and exits 0 — the deploy script then
falls back to local+1 (and its export-error handler surfaces any collision
clearly). Never let a build-number lookup block a deploy.

Env (same names the deploy script already exports):
  APP_STORE_KEY_PATH   path to the .p8 App Store Connect API key
  APP_STORE_KEY_ID     key id
  APP_STORE_KEY_ISSUER issuer id
  ASC_BUNDLE_ID        optional; defaults to io.yaver.mobile
"""
import os
import sys
import time


def main() -> int:
    try:
        import jwt  # PyJWT
        import requests
    except Exception:
        return 0  # libs absent → silent fallback

    key_path = os.environ.get("APP_STORE_KEY_PATH", "")
    kid = os.environ.get("APP_STORE_KEY_ID", "")
    iss = os.environ.get("APP_STORE_KEY_ISSUER", "")
    bundle = os.environ.get("ASC_BUNDLE_ID", "io.yaver.mobile")
    if not (key_path and kid and iss and os.path.exists(key_path)):
        return 0

    try:
        key = open(key_path).read()
        token = jwt.encode(
            {"iss": iss, "iat": int(time.time()), "exp": int(time.time()) + 600,
             "aud": "appstoreconnect-v1"},
            key, algorithm="ES256", headers={"kid": kid})
        h = {"Authorization": f"Bearer {token}"}
        # Resolve the app id from the bundle id.
        r = requests.get("https://api.appstoreconnect.apple.com/v1/apps",
                         headers=h, params={"filter[bundleId]": bundle, "limit": "1"}, timeout=20)
        r.raise_for_status()
        apps = r.json().get("data", [])
        if not apps:
            return 0
        app_id = apps[0]["id"]
        # Highest build across all uploaded builds (they sort by version string,
        # so pull a page and max() numerically to be safe).
        r = requests.get("https://api.appstoreconnect.apple.com/v1/builds",
                         headers=h, params={"filter[app]": app_id, "sort": "-uploadedDate",
                                            "limit": "50", "fields[builds]": "version"}, timeout=20)
        r.raise_for_status()
        vals = []
        for b in r.json().get("data", []):
            v = b.get("attributes", {}).get("version")
            try:
                vals.append(int(v))
            except (TypeError, ValueError):
                continue
        if vals:
            print(max(vals))
    except Exception:
        return 0
    return 0


if __name__ == "__main__":
    sys.exit(main())
