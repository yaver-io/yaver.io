#!/usr/bin/env python3
"""Print the highest CFBundleVersion already on App Store Connect for the app.

Used by deploy-testflight.sh to bump from max(local, ASC) + 1 so a new build can
never collide with an existing one (ITMS "build number already used") and burn a
TestFlight upload slot. See CLAUDE.md → iOS TestFlight.

BEST-EFFORT by design: if PyJWT / cryptography / requests aren't importable, or
the network/API errors, this prints NOTHING to STDOUT and exits 0 — the deploy
script then falls back to local+1 (and its export-error handler surfaces any
collision clearly). Never let a build-number lookup block a deploy.

But best-effort must never mean SILENT. On 2026-07-20 no python on the mac mini
could import PyJWT, so this returned nothing on every run; the deploy bumped from
local 450 while ASC already had 451, the upload collided, and the retry cost a
slot out of the ~15-20/day TestFlight cap. The operator saw only "WARN: could not
read ASC max build" — which names no cause and no remedy, so nobody fixed it.
Degrading is fine; degrading without saying why is how a metered quota leaks.

Every early return therefore explains itself on STDERR, naming the interpreter
that came up short so the fix can be applied to the RIGHT one of this box's
several python3s. stdout stays contractual: the number, or nothing.

Env (same names the deploy script already exports):
  APP_STORE_KEY_PATH   path to the .p8 App Store Connect API key
  APP_STORE_KEY_ID     key id
  APP_STORE_KEY_ISSUER issuer id
  ASC_BUNDLE_ID        optional; defaults to io.yaver.mobile
"""
import os
import sys
import time


def why(msg: str) -> int:
    """Explain a degraded lookup on stderr, keeping stdout contractual."""
    print("asc-max-build: %s" % msg, file=sys.stderr)
    return 0


def main() -> int:
    try:
        import jwt  # PyJWT
        import requests
    except Exception as e:
        return why(
            "cannot query App Store Connect (%s) using %s — build number will be "
            "bumped from the LOCAL plist, which collides if ASC is ahead. Fix: "
            "%s -m pip install --break-system-packages PyJWT cryptography requests"
            % (e, sys.executable, sys.executable)
        )

    key_path = os.environ.get("APP_STORE_KEY_PATH", "")
    kid = os.environ.get("APP_STORE_KEY_ID", "")
    iss = os.environ.get("APP_STORE_KEY_ISSUER", "")
    bundle = os.environ.get("ASC_BUNDLE_ID", "io.yaver.mobile")
    if not (key_path and kid and iss and os.path.exists(key_path)):
        missing = [
            n
            for n, v in (
                ("APP_STORE_KEY_PATH", key_path),
                ("APP_STORE_KEY_ID", kid),
                ("APP_STORE_KEY_ISSUER", iss),
            )
            if not v
        ]
        if not missing and not os.path.exists(key_path):
            return why("APP_STORE_KEY_PATH points at %s, which does not exist" % key_path)
        return why("missing credentials: %s" % ", ".join(missing))

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
            return why("App Store Connect knows no app with bundle id %s" % bundle)
        app_id = apps[0]["id"]
        # CFBundleVersion monotonicity is per (platform, marketing version), so
        # the max must be scoped to THIS platform — otherwise a visionOS/tvOS
        # build with a date-based number (e.g. 2607160313) poisons an iOS bump.
        # Default to iOS; override with ASC_PLATFORM.
        platform = os.environ.get("ASC_PLATFORM", "IOS")
        r = requests.get("https://api.appstoreconnect.apple.com/v1/builds",
                         headers=h, params={"filter[app]": app_id,
                                            "filter[preReleaseVersion.platform]": platform,
                                            "sort": "-uploadedDate", "limit": "100",
                                            "fields[builds]": "version"}, timeout=20)
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
            return 0
        return why(
            "App Store Connect returned no numeric %s build for this app — "
            "falling back to the local plist" % platform
        )
    except Exception as e:
        return why("App Store Connect query failed (%s: %s)" % (type(e).__name__, e))
    return 0


if __name__ == "__main__":
    sys.exit(main())
