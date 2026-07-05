#!/usr/bin/env python3
"""Upload screenshots to App Store Connect and set age rating.

Parameterized so it works for any app (not just Yaver). The `yaver shots`
flow shells out to this; it is also runnable by hand.

    python3 upload-appstore.py \
        --bundle-id com.example.app \
        --dir /path/to/pngs \
        --locale en-US \
        [--platform IOS|TV_OS] \
        [--files 01_a.png,02_b.png] \
        [--display-types APP_IPHONE_67,APP_IPHONE_65|APP_APPLE_TV]

Auth (same as the rest of the App Store Connect scripts):
    APP_STORE_KEY_ID, APP_STORE_KEY_ISSUER, APP_STORE_KEY_PATH (.p8)

If --bundle-id / --dir are omitted they fall back to the SHOTS_BUNDLE_ID /
SHOTS_DIR env vars, then to sensible defaults.
"""

import argparse
import hashlib
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

# App Store Connect display types (6.7"/6.9" share the same 1290x2796 set).
DISPLAY_TYPE_67 = "APP_IPHONE_67"
DISPLAY_TYPE_65 = "APP_IPHONE_65"


def generate_token():
    private_key = API_KEY_PATH.read_text()
    now = int(time.time())
    payload = {"iss": ISSUER_ID, "iat": now, "exp": now + 20 * 60, "aud": "appstoreconnect-v1"}
    return jwt.encode(payload, private_key, algorithm="ES256", headers={"kid": API_KEY_ID})


def headers(content_type="application/json"):
    return {"Authorization": f"Bearer {generate_token()}", "Content-Type": content_type}


def api_get(path, params=None):
    resp = requests.get(f"{BASE_URL}{path}", headers=headers(), params=params)
    if resp.status_code != 200:
        print(f"GET {path} failed ({resp.status_code}): {resp.text}")
        sys.exit(1)
    return resp.json()


def api_post(path, payload):
    resp = requests.post(f"{BASE_URL}{path}", headers=headers(), json=payload)
    if resp.status_code not in (200, 201):
        print(f"POST {path} failed ({resp.status_code}): {resp.text}")
        return None
    return resp.json()


def api_patch(path, payload):
    resp = requests.patch(f"{BASE_URL}{path}", headers=headers(), json=payload)
    if resp.status_code not in (200, 204):
        print(f"PATCH {path} failed ({resp.status_code}): {resp.text}")
        return None
    return resp.json() if resp.status_code == 200 else {}


def find_app(bundle_id):
    data = api_get("/apps", params={"filter[bundleId]": bundle_id})
    apps = data.get("data", [])
    if not apps:
        print(f"ERROR: No app found with bundle ID {bundle_id}")
        sys.exit(1)
    app = apps[0]
    print(f"App: {app['attributes']['name']} ({app['id']})")
    return app["id"]


def get_version_localization(app_id, locale, platform):
    versions = api_get(
        f"/apps/{app_id}/appStoreVersions",
        params={"filter[appStoreState]": "PREPARE_FOR_SUBMISSION", "filter[platform]": platform}
    )
    if not versions.get("data"):
        print("ERROR: No version in PREPARE_FOR_SUBMISSION. Create a version in "
              "App Store Connect (or run submit-appstore.py which can create one) first.")
        sys.exit(1)
    ver = versions["data"][0]
    ver_id = ver["id"]
    print(f"Version: {ver['attributes']['versionString']} ({ver['attributes']['appStoreState']}, {platform})")

    locs = api_get(f"/appStoreVersions/{ver_id}/appStoreVersionLocalizations")
    for loc in locs["data"]:
        if loc["attributes"]["locale"] == locale:
            return loc["id"], ver_id
    print(f"ERROR: No {locale} localization found")
    sys.exit(1)


def create_screenshot_set(loc_id, display_type):
    """Create a screenshot set for the given display type, or return existing."""
    sets = api_get(f"/appStoreVersionLocalizations/{loc_id}/appScreenshotSets")
    for s in sets["data"]:
        if s["attributes"]["screenshotDisplayType"] == display_type:
            print(f"  Found existing screenshot set for {display_type}: {s['id']}")
            return s["id"]

    payload = {
        "data": {
            "type": "appScreenshotSets",
            "attributes": {"screenshotDisplayType": display_type},
            "relationships": {
                "appStoreVersionLocalization": {
                    "data": {"type": "appStoreVersionLocalizations", "id": loc_id}
                }
            },
        }
    }
    result = api_post("/appScreenshotSets", payload)
    if result:
        set_id = result["data"]["id"]
        print(f"  Created screenshot set for {display_type}: {set_id}")
        return set_id
    return None


def upload_screenshot(set_id, filepath):
    """Upload a single screenshot via the App Store Connect API."""
    filename = filepath.name
    filesize = filepath.stat().st_size

    print(f"  Uploading {filename} ({filesize // 1024} KB)...")

    # 1. Reserve the screenshot
    payload = {
        "data": {
            "type": "appScreenshots",
            "attributes": {
                "fileName": filename,
                "fileSize": filesize,
            },
            "relationships": {
                "appScreenshotSet": {
                    "data": {"type": "appScreenshotSets", "id": set_id}
                }
            },
        }
    }
    result = api_post("/appScreenshots", payload)
    if not result:
        print(f"    Failed to reserve {filename}")
        return False

    screenshot_id = result["data"]["id"]
    upload_ops = result["data"]["attributes"].get("uploadOperations", [])

    if not upload_ops:
        print(f"    No upload operations returned for {filename}")
        return False

    # 2. Upload the binary data
    file_data = filepath.read_bytes()
    for op in upload_ops:
        url = op["url"]
        op_headers = {h["name"]: h["value"] for h in op.get("requestHeaders", [])}
        offset = op.get("offset", 0)
        length = op.get("length", len(file_data))
        chunk = file_data[offset:offset + length]

        resp = requests.put(url, headers=op_headers, data=chunk)
        if resp.status_code not in (200, 201):
            print(f"    Upload chunk failed ({resp.status_code})")
            return False

    # 3. Commit the upload
    md5 = hashlib.md5(file_data).hexdigest()
    commit_payload = {
        "data": {
            "type": "appScreenshots",
            "id": screenshot_id,
            "attributes": {
                "uploaded": True,
                "sourceFileChecksum": md5,
            },
        }
    }
    result = api_patch(f"/appScreenshots/{screenshot_id}", commit_payload)
    if result is not None:
        print(f"    Uploaded: {filename}")
        return True
    else:
        print(f"    Failed to commit {filename}")
        return False


def set_age_rating(app_id):
    """Set age rating declaration to the lowest ratings (suitable for all ages)."""
    print("\nSetting age rating...")

    infos = api_get(f"/apps/{app_id}/appInfos")
    app_info_id = infos["data"][0]["id"]

    try:
        decl = api_get(f"/appInfos/{app_info_id}/ageRatingDeclaration")
        decl_id = decl["data"]["id"]
    except Exception:
        print("  Could not find age rating declaration")
        return

    payload = {
        "data": {
            "type": "ageRatingDeclarations",
            "id": decl_id,
            "attributes": {
                "alcoholTobaccoOrDrugUseOrReferences": "NONE",
                "contests": "NONE",
                "gamblingAndContests": False,
                "gambling": False,
                "gamblingSimulated": "NONE",
                "horrorOrFearThemes": "NONE",
                "matureOrSuggestiveThemes": "NONE",
                "medicalOrTreatmentInformation": "NONE",
                "profanityOrCrudeHumor": "NONE",
                "sexualContentGraphicAndNudity": "NONE",
                "sexualContentOrNudity": "NONE",
                "violenceCartoonOrFantasy": "NONE",
                "violenceRealistic": "NONE",
                "violenceRealisticProlongedGraphicOrSadistic": "NONE",
                "unrestrictedWebAccess": False,
                "kidsAgeBand": None,
                "seventeenPlus": False,
            },
        }
    }
    result = api_patch(f"/ageRatingDeclarations/{decl_id}", payload)
    if result is not None:
        print("  Age rating set (4+ / suitable for all ages)")
    else:
        print("  WARNING: Could not set age rating")


def resolve_files(screenshots_dir, files_arg):
    """Resolve the ordered list of PNG Paths to upload.

    --files wins (explicit, comma-separated, relative to --dir or absolute);
    otherwise glob *.png in --dir sorted by name (so 01_*, 02_* order holds).
    """
    if files_arg:
        out = []
        for name in [f.strip() for f in files_arg.split(",") if f.strip()]:
            p = Path(name)
            if not p.is_absolute():
                p = screenshots_dir / name
            out.append(p)
        return out
    return sorted(screenshots_dir.glob("*.png"))


def parse_args():
    ap = argparse.ArgumentParser(description="Upload App Store screenshots + set age rating.")
    ap.add_argument("--bundle-id", default=os.environ.get("SHOTS_BUNDLE_ID", ""),
                    help="App bundle identifier (or SHOTS_BUNDLE_ID env).")
    ap.add_argument("--dir", default=os.environ.get("SHOTS_DIR", str(Path(__file__).parent / "output")),
                    help="Directory containing the PNGs (or SHOTS_DIR env).")
    ap.add_argument("--locale", default=os.environ.get("SHOTS_LOCALE", "en-US"),
                    help="App Store localization locale (default en-US).")
    ap.add_argument("--platform", default=os.environ.get("SHOTS_PLATFORM", "IOS"),
                    help="App Store platform to target (default IOS; use TV_OS for Apple TV).")
    ap.add_argument("--files", default="",
                    help="Optional comma-separated ordered file list (default: sorted *.png in --dir).")
    ap.add_argument("--display-types", default="APP_IPHONE_67,APP_IPHONE_65",
                    help="Comma-separated App Store Connect display types to populate.")
    ap.add_argument("--no-age-rating", action="store_true",
                    help="Skip setting the age rating declaration.")
    args = ap.parse_args()
    if not args.bundle_id:
        ap.error("--bundle-id is required (or set SHOTS_BUNDLE_ID).")
    return args


def main():
    args = parse_args()
    screenshots_dir = Path(args.dir)
    display_types = [d.strip() for d in args.display_types.split(",") if d.strip()]

    print("=" * 60)
    print(f"Upload Screenshots — {args.bundle_id}")
    print(f"  dir={screenshots_dir}  locale={args.locale}")
    print("=" * 60)
    print()

    app_id = find_app(args.bundle_id)
    loc_id, _ver_id = get_version_localization(app_id, args.locale, args.platform)

    if not args.no_age_rating:
        set_age_rating(app_id)

    files = resolve_files(screenshots_dir, args.files)
    if not files:
        print(f"ERROR: no PNGs found in {screenshots_dir}")
        sys.exit(1)

    total_uploaded = 0
    for display_type in display_types:
        print(f"\nUploading {len(files)} screenshots for {display_type}...")
        set_id = create_screenshot_set(loc_id, display_type)
        if not set_id:
            print(f"  ERROR: could not create screenshot set for {display_type}")
            continue
        ok = 0
        for filepath in files:
            if not filepath.exists():
                print(f"  Skipping {filepath.name} (not found)")
                continue
            if upload_screenshot(set_id, filepath):
                ok += 1
        total_uploaded += ok
        print(f"Uploaded {ok}/{len(files)} screenshots to {display_type}")

    print(f"\n{'=' * 60}")
    print(f"DONE. {total_uploaded} screenshot uploads across {len(display_types)} display type(s).")
    print("Check App Store Connect to verify, then submit (or run submit-appstore.py).")
    print("=" * 60)


if __name__ == "__main__":
    main()
