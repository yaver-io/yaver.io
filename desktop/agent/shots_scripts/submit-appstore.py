#!/usr/bin/env python3
"""Submit an App Store version for review — with a graceful "staged" fallback.

This is the step Yaver never had. It assumes the binary + metadata + screenshots
are already in App Store Connect (handled by /deploy/ship + set-appstore-info.py +
upload-appstore.py) and tries to push the version into review:

  1. Find the app, ensure an editable version exists in PREPARE_FOR_SUBMISSION
     (create one with --version if there is none).
  2. Set export-compliance (usesNonExemptEncryption=false) so Apple doesn't gate
     on the encryption question.
  3. Bind the latest processed (VALID) build to the version if one exists.
  4. POST /appStoreVersionSubmissions to submit for review.

Apple commonly blocks a first submission on things only settable in the web UI
(paid-app agreements, pricing, tax forms, manual export-compliance docs, a build
still processing). Those are NOT failures of this tool — we classify them, print
Apple's own message, emit `STAGED_MANUAL`, and **exit 0** so the caller reports
"staged — one tap to Submit" rather than a red error.

Usage:
    python3 submit-appstore.py --bundle-id com.example.app [--version 1.2.3] [--platform IOS]

Auth: APP_STORE_KEY_ID, APP_STORE_KEY_ISSUER, APP_STORE_KEY_PATH (.p8)
"""

import argparse
import os
import sys
import time
from pathlib import Path

import jwt
import requests

API_KEY_ID = os.environ.get("APP_STORE_KEY_ID", "")
ISSUER_ID = os.environ.get("APP_STORE_KEY_ISSUER", "")
API_KEY_PATH = Path(os.environ.get("APP_STORE_KEY_PATH", str(Path.home() / ".appstore/AuthKey.p8")))
BASE_URL = "https://api.appstoreconnect.apple.com/v1"

# Exit codes the Go caller distinguishes.
EXIT_SUBMITTED = 0
EXIT_STAGED = 0        # staged-but-not-submitted is success from the user's POV
EXIT_HARD_FAIL = 1     # auth / app-not-found / unexpected — a real failure


def generate_token():
    private_key = API_KEY_PATH.read_text()
    now = int(time.time())
    payload = {"iss": ISSUER_ID, "iat": now, "exp": now + 20 * 60, "aud": "appstoreconnect-v1"}
    return jwt.encode(payload, private_key, algorithm="ES256", headers={"kid": API_KEY_ID})


def headers():
    return {"Authorization": f"Bearer {generate_token()}", "Content-Type": "application/json"}


def api_get(path, params=None):
    resp = requests.get(f"{BASE_URL}{path}", headers=headers(), params=params)
    if resp.status_code != 200:
        print(f"GET {path} failed ({resp.status_code}): {resp.text}")
        return None
    return resp.json()


def api_post(path, payload):
    return requests.post(f"{BASE_URL}{path}", headers=headers(), json=payload)


def api_patch(path, payload):
    return requests.patch(f"{BASE_URL}{path}", headers=headers(), json=payload)


def find_app(bundle_id):
    data = api_get("/apps", params={"filter[bundleId]": bundle_id})
    apps = (data or {}).get("data", [])
    if not apps:
        print(f"ERROR: No app found with bundle ID {bundle_id}")
        sys.exit(EXIT_HARD_FAIL)
    app = apps[0]
    print(f"App: {app['attributes']['name']} ({app['id']})")
    return app["id"]


def get_editable_version(app_id, platform, version_string):
    """Return the version id in PREPARE_FOR_SUBMISSION, creating one if needed."""
    data = api_get(
        f"/apps/{app_id}/appStoreVersions",
        params={"filter[appStoreState]": "PREPARE_FOR_SUBMISSION", "limit": 5},
    )
    versions = (data or {}).get("data", [])
    if versions:
        v = versions[0]
        print(f"Editable version: {v['attributes']['versionString']} "
              f"({v['attributes']['appStoreState']}) [{v['id']}]")
        return v["id"]

    if not version_string:
        print("No PREPARE_FOR_SUBMISSION version exists and --version not given.")
        print("STAGED_MANUAL: create the version in App Store Connect (or pass --version).")
        sys.exit(EXIT_STAGED)

    print(f"No editable version — creating {version_string} ({platform})...")
    resp = api_post("/appStoreVersions", {
        "data": {
            "type": "appStoreVersions",
            "attributes": {"platform": platform, "versionString": version_string},
            "relationships": {"app": {"data": {"type": "apps", "id": app_id}}},
        }
    })
    if resp.status_code in (200, 201):
        vid = resp.json()["data"]["id"]
        print(f"  Created version {version_string} [{vid}]")
        return vid
    print(f"  Could not create version ({resp.status_code}): {resp.text}")
    print("STAGED_MANUAL: create the version manually, then re-run.")
    sys.exit(EXIT_STAGED)


def set_export_compliance(version_id):
    print("Setting export compliance (usesNonExemptEncryption=false)...")
    resp = api_patch(f"/appStoreVersions/{version_id}", {
        "data": {
            "type": "appStoreVersions",
            "id": version_id,
            "attributes": {"usesNonExemptEncryption": False},
        }
    })
    if resp.status_code in (200, 204):
        print("  Export compliance set.")
    else:
        # Non-fatal: some apps set this on the build instead.
        print(f"  (skip) export compliance not set via version ({resp.status_code}).")


def bind_latest_build(app_id, version_id):
    """Attach the newest processed (VALID) build to the version, if any."""
    data = api_get("/builds", params={
        "filter[app]": app_id,
        "filter[processingState]": "VALID",
        "sort": "-uploadedDate",
        "limit": 1,
    })
    builds = (data or {}).get("data", [])
    if not builds:
        print("No VALID build yet (still processing?) — submission may need to wait.")
        return False
    build_id = builds[0]["id"]
    print(f"Binding build {build_id} to version...")
    resp = api_patch(f"/appStoreVersions/{version_id}/relationships/build", {
        "data": {"type": "builds", "id": build_id}
    })
    if resp.status_code in (200, 204):
        print("  Build bound.")
        return True
    print(f"  (skip) could not bind build ({resp.status_code}): {resp.text}")
    return False


def _explain_and_stage(resp):
    """Print Apple's error detail(s) and exit as STAGED_MANUAL (success-ish)."""
    try:
        errs = resp.json().get("errors", [])
    except Exception:
        errs = []
    print(f"Submission not accepted yet (HTTP {resp.status_code}):")
    if errs:
        for e in errs:
            code = e.get("code", "")
            title = e.get("title", "")
            detail = e.get("detail", "")
            print(f"  • [{code}] {title}: {detail}")
    else:
        print(f"  {resp.text[:500]}")
    print()
    print("STAGED_MANUAL: everything is uploaded and staged in App Store Connect. "
          "Finish the gated item above (often export compliance, pricing/agreements, "
          "or a build still processing) and tap Submit — one click.")
    sys.exit(EXIT_STAGED)


def submit_for_review(version_id):
    print("Submitting for review...")
    resp = api_post("/appStoreVersionSubmissions", {
        "data": {
            "type": "appStoreVersionSubmissions",
            "relationships": {
                "appStoreVersion": {"data": {"type": "appStoreVersions", "id": version_id}}
            },
        }
    })
    if resp.status_code in (200, 201):
        print("  ✓ SUBMITTED for App Store review.")
        sys.exit(EXIT_SUBMITTED)
    # 409/422/4xx → almost always a fixable gate, not a tool failure.
    _explain_and_stage(resp)


def parse_args():
    ap = argparse.ArgumentParser(description="Submit an App Store version for review.")
    ap.add_argument("--bundle-id", default=os.environ.get("SHOTS_BUNDLE_ID", ""),
                    help="App bundle identifier (or SHOTS_BUNDLE_ID env).")
    ap.add_argument("--version", default="",
                    help="Version string to create if none is editable (e.g. 1.2.3).")
    ap.add_argument("--platform", default="IOS", help="Platform (default IOS).")
    args = ap.parse_args()
    if not args.bundle_id:
        ap.error("--bundle-id is required (or set SHOTS_BUNDLE_ID).")
    return args


def main():
    args = parse_args()
    print("=" * 60)
    print(f"Submit for Review — {args.bundle_id}")
    print("=" * 60)
    print()

    app_id = find_app(args.bundle_id)
    version_id = get_editable_version(app_id, args.platform, args.version)
    set_export_compliance(version_id)
    bind_latest_build(app_id, version_id)
    submit_for_review(version_id)


if __name__ == "__main__":
    main()
