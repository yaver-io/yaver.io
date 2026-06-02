#!/usr/bin/env python3
"""
Set App Store Connect metadata for any app.

Usage:
    pip install PyJWT cryptography requests
    python scripts/set-appstore-info.py [--bundle-id ID] [--meta-json FILE]

Defaults below describe Yaver (io.yaver.mobile) so the no-arg form stays
backward compatible. For any other app, pass --bundle-id and a --meta-json
file (or drop a `.yaver/appstore.json` next to the project) so the metadata
is data, not code. Recognized JSON keys mirror the module constants:
name, subtitle, copyright, privacyPolicyUrl, supportUrl, marketingUrl,
primaryCategory, secondaryCategory, description, keywords, whatsNew, locale.

Idempotent — safe to run multiple times.
"""

import argparse
import json
import os
import sys
import time
from pathlib import Path

import jwt
import requests

# --- Configuration ---

API_KEY_ID = os.environ.get("APP_STORE_KEY_ID", "")
ISSUER_ID = os.environ.get("APP_STORE_KEY_ISSUER", "")
API_KEY_PATH = Path(os.environ.get("APP_STORE_KEY_PATH", str(Path.home() / ".appstore/AuthKey.p8")))
BUNDLE_ID = "io.yaver.mobile"
# The bundle the built-in defaults below describe. If a caller targets a
# DIFFERENT app without supplying metadata, we must NOT apply these
# defaults to it (that would clobber another app's listing with Yaver's
# copy). main() refuses in that case.
DEFAULT_BUNDLE_ID = "io.yaver.mobile"
_META_LOADED = False  # set True once apply_overrides reads a metadata file
BASE_URL = "https://api.appstoreconnect.apple.com/v1"

# App info
APP_NAME = "Yaver IO"
SUBTITLE = "Code from your phone"
COPYRIGHT = "2026 SIMKAB ELEKTRIK"
PRIVACY_POLICY_URL = "https://yaver.io/privacy"
SUPPORT_URL = "https://yaver.io"
MARKETING_URL = "https://yaver.io"
PRIMARY_CATEGORY = "DEVELOPER_TOOLS"
SECONDARY_CATEGORY = "PRODUCTIVITY"
LOCALE = "en-US"

DESCRIPTION = """\
Yaver lets developers run AI coding agents on their development machines — directly from their phone.

Your code never leaves your machine. Tasks flow peer-to-peer between your phone and your dev machine through encrypted connections. Our servers only handle authentication and peer discovery.

HOW IT WORKS
1. Install the Yaver CLI on your dev machine
2. Open the Yaver app on your phone
3. Send coding tasks to your machine from anywhere

FEATURES
\u2022 Run Claude, Codex, Aider, or any custom AI agent
\u2022 Switch between agents per task — use the best tool for each job
\u2022 Works over Wi-Fi and cellular — seamless roaming between networks
\u2022 Direct connection when on the same network, relay fallback when remote
\u2022 See real-time output as your agent works
\u2022 Multiple device support — connect to any of your dev machines

PRIVACY FIRST
\u2022 Your code and task data never touch our servers
\u2022 All communication is end-to-end encrypted
\u2022 Relay servers are pass-through only — zero data storage
\u2022 Open infrastructure: relay servers, CLI, and networking are transparent

REQUIREMENTS
\u2022 A Mac, Linux, or Windows machine with the Yaver CLI installed
\u2022 An AI agent (Claude Code, OpenAI Codex, Aider, or any CLI-based agent)"""

KEYWORDS = "ai,coding,developer,agent,claude,remote,peer-to-peer,terminal,codex,aider"

WHATS_NEW = """\
\u2022 Network-aware reconnection — seamless WiFi to cellular transitions
\u2022 Increased connection resilience with 15 retry attempts
\u2022 Choose your AI agent per task from the app
\u2022 Improved connection stability and error recovery"""


# --- Config overrides (CLI flags / --meta-json) ---

# Map metadata-JSON keys → module-global names they override.
_META_KEYS = {
    "name": "APP_NAME",
    "subtitle": "SUBTITLE",
    "copyright": "COPYRIGHT",
    "privacyPolicyUrl": "PRIVACY_POLICY_URL",
    "supportUrl": "SUPPORT_URL",
    "marketingUrl": "MARKETING_URL",
    "primaryCategory": "PRIMARY_CATEGORY",
    "secondaryCategory": "SECONDARY_CATEGORY",
    "description": "DESCRIPTION",
    "keywords": "KEYWORDS",
    "whatsNew": "WHATS_NEW",
    "locale": "LOCALE",
}


def apply_overrides(args):
    """Override module globals from --bundle-id and a metadata-JSON file.

    Resolution order: built-in defaults < --meta-json file < --bundle-id flag.
    If --meta-json points at a directory, look for `.yaver/appstore.json` in it.
    """
    global BUNDLE_ID, _META_LOADED

    meta_path = None
    if args.meta_json:
        p = Path(args.meta_json)
        meta_path = p / ".yaver" / "appstore.json" if p.is_dir() else p
    if meta_path and meta_path.exists():
        meta = json.loads(meta_path.read_text())
        _META_LOADED = True
        print(f"Loaded metadata overrides from {meta_path}")
        for key, value in meta.items():
            if key == "bundleId" and value:
                BUNDLE_ID = value
            elif key in _META_KEYS and value is not None:
                globals()[_META_KEYS[key]] = value

    if args.bundle_id:
        BUNDLE_ID = args.bundle_id


# --- JWT Auth ---

def generate_token():
    """Generate a JWT for App Store Connect API authentication."""
    if not API_KEY_PATH.exists():
        print(f"ERROR: API key not found at {API_KEY_PATH}")
        sys.exit(1)

    private_key = API_KEY_PATH.read_text()
    now = int(time.time())
    payload = {
        "iss": ISSUER_ID,
        "iat": now,
        "exp": now + 20 * 60,  # 20 minutes
        "aud": "appstoreconnect-v1",
    }
    token = jwt.encode(payload, private_key, algorithm="ES256", headers={"kid": API_KEY_ID})
    return token


def headers():
    return {
        "Authorization": f"Bearer {generate_token()}",
        "Content-Type": "application/json",
    }


# --- API Helpers ---

def api_get(path, params=None):
    url = f"{BASE_URL}{path}"
    resp = requests.get(url, headers=headers(), params=params)
    if resp.status_code != 200:
        print(f"GET {path} failed ({resp.status_code}): {resp.text}")
        sys.exit(1)
    return resp.json()


def api_patch(path, payload):
    url = f"{BASE_URL}{path}"
    resp = requests.patch(url, headers=headers(), json=payload)
    if resp.status_code not in (200, 204):
        print(f"PATCH {path} failed ({resp.status_code}): {resp.text}")
        return None
    if resp.status_code == 204:
        return {}
    return resp.json()


# --- Steps ---

def find_app():
    """Find the app by bundle ID and return its ID."""
    print(f"Finding app with bundle ID: {BUNDLE_ID}")
    data = api_get("/apps", params={"filter[bundleId]": BUNDLE_ID, "include": "appInfos"})
    apps = data.get("data", [])
    if not apps:
        print(f"ERROR: No app found with bundle ID {BUNDLE_ID}")
        sys.exit(1)
    app = apps[0]
    app_id = app["id"]
    print(f"  Found app: {app['attributes'].get('name', 'N/A')} (ID: {app_id})")
    return app_id, data


def get_app_info(app_id):
    """Get the current (editable) appInfo for the app."""
    print("Fetching app info records...")
    data = api_get(f"/apps/{app_id}/appInfos", params={"include": "appInfoLocalizations"})
    infos = data.get("data", [])
    # Pick the one in an editable state (PREPARE_FOR_SUBMISSION or READY_FOR_SALE)
    editable_states = {"PREPARE_FOR_SUBMISSION", "READY_FOR_SALE"}
    for info in infos:
        state = info["attributes"].get("appStoreState", "")
        if state in editable_states:
            print(f"  Using appInfo ID: {info['id']} (state: {state})")
            return info["id"], data
    # Fallback: use the first one
    if infos:
        info = infos[0]
        print(f"  Using appInfo ID: {info['id']} (state: {info['attributes'].get('appStoreState', 'unknown')})")
        return info["id"], data
    print("ERROR: No appInfo records found")
    sys.exit(1)


def get_category_id(category_name):
    """Map category name to App Store Connect category ID format."""
    # App Store Connect uses IDs like "DEVELOPER_TOOLS" or "PRODUCTIVITY"
    return category_name


def update_app_info_categories(app_info_id):
    """Set primary and secondary categories on the appInfo."""
    print(f"Setting categories: primary={PRIMARY_CATEGORY}, secondary={SECONDARY_CATEGORY}")
    payload = {
        "data": {
            "type": "appInfos",
            "id": app_info_id,
            "relationships": {
                "primaryCategory": {
                    "data": {
                        "type": "appCategories",
                        "id": PRIMARY_CATEGORY,
                    }
                },
                "secondaryCategory": {
                    "data": {
                        "type": "appCategories",
                        "id": SECONDARY_CATEGORY,
                    }
                },
            },
        }
    }
    result = api_patch(f"/appInfos/{app_info_id}", payload)
    if result is not None:
        print("  Categories updated.")
    else:
        print("  WARNING: Failed to update categories (may need different category IDs).")


def get_or_create_app_info_localization(app_info_id):
    """Get existing en-US localization or create one."""
    print(f"Fetching appInfo localizations for locale: {LOCALE}")
    data = api_get(f"/appInfos/{app_info_id}/appInfoLocalizations")
    localizations = data.get("data", [])
    for loc in localizations:
        if loc["attributes"].get("locale") == LOCALE:
            print(f"  Found existing localization: {loc['id']}")
            return loc["id"]
    # Create one
    print(f"  No {LOCALE} localization found, creating...")
    url = f"{BASE_URL}/appInfoLocalizations"
    payload = {
        "data": {
            "type": "appInfoLocalizations",
            "attributes": {
                "locale": LOCALE,
                "name": APP_NAME,
                "subtitle": SUBTITLE,
                "privacyPolicyUrl": PRIVACY_POLICY_URL,
            },
            "relationships": {
                "appInfo": {
                    "data": {"type": "appInfos", "id": app_info_id}
                }
            },
        }
    }
    resp = requests.post(url, headers=headers(), json=payload)
    if resp.status_code not in (200, 201):
        print(f"  ERROR creating localization: {resp.status_code} {resp.text}")
        sys.exit(1)
    loc_id = resp.json()["data"]["id"]
    print(f"  Created localization: {loc_id}")
    return loc_id


def update_app_info_localization(localization_id):
    """Update the appInfo localization (name, subtitle, privacy URL)."""
    print("Updating appInfo localization (name, subtitle, privacy policy URL)...")
    payload = {
        "data": {
            "type": "appInfoLocalizations",
            "id": localization_id,
            "attributes": {
                "name": APP_NAME,
                "subtitle": SUBTITLE,
                "privacyPolicyUrl": PRIVACY_POLICY_URL,
            },
        }
    }
    result = api_patch(f"/appInfoLocalizations/{localization_id}", payload)
    if result is not None:
        print("  appInfo localization updated.")
    else:
        print("  WARNING: Failed to update appInfo localization.")


def get_app_store_version(app_id):
    """Get the latest editable App Store version."""
    print("Fetching App Store versions...")
    data = api_get(
        f"/apps/{app_id}/appStoreVersions",
        params={
            "filter[appStoreState]": "PREPARE_FOR_SUBMISSION,READY_FOR_SALE,IN_REVIEW,WAITING_FOR_REVIEW",
            "include": "appStoreVersionLocalizations",
            "limit": 5,
        },
    )
    versions = data.get("data", [])
    editable_states = {"PREPARE_FOR_SUBMISSION"}
    for v in versions:
        state = v["attributes"].get("appStoreState", "")
        if state in editable_states:
            ver = v["attributes"].get("versionString", "?")
            print(f"  Found editable version: {ver} (ID: {v['id']}, state: {state})")
            return v["id"], data
    # Fallback to most recent
    if versions:
        v = versions[0]
        ver = v["attributes"].get("versionString", "?")
        state = v["attributes"].get("appStoreState", "unknown")
        print(f"  Using version: {ver} (ID: {v['id']}, state: {state})")
        return v["id"], data
    print("WARNING: No App Store versions found. Skipping version localization updates.")
    return None, data


def get_or_create_version_localization(version_id):
    """Get existing en-US version localization or create one."""
    print(f"Fetching version localizations for locale: {LOCALE}")
    data = api_get(f"/appStoreVersions/{version_id}/appStoreVersionLocalizations")
    localizations = data.get("data", [])
    for loc in localizations:
        if loc["attributes"].get("locale") == LOCALE:
            print(f"  Found existing version localization: {loc['id']}")
            return loc["id"]
    # Create one
    print(f"  No {LOCALE} version localization found, creating...")
    url = f"{BASE_URL}/appStoreVersionLocalizations"
    payload = {
        "data": {
            "type": "appStoreVersionLocalizations",
            "attributes": {
                "locale": LOCALE,
                "description": DESCRIPTION,
                "keywords": KEYWORDS,
                "whatsNew": WHATS_NEW,
                "supportUrl": SUPPORT_URL,
                "marketingUrl": MARKETING_URL,
            },
            "relationships": {
                "appStoreVersion": {
                    "data": {"type": "appStoreVersions", "id": version_id}
                }
            },
        }
    }
    resp = requests.post(url, headers=headers(), json=payload)
    if resp.status_code not in (200, 201):
        print(f"  ERROR creating version localization: {resp.status_code} {resp.text}")
        sys.exit(1)
    loc_id = resp.json()["data"]["id"]
    print(f"  Created version localization: {loc_id}")
    return loc_id


def update_version_localization(localization_id, include_whats_new=True):
    """Update version localization (description, keywords, what's new, URLs)."""
    attrs = {
        "description": DESCRIPTION,
        "keywords": KEYWORDS,
        "supportUrl": SUPPORT_URL,
        "marketingUrl": MARKETING_URL,
    }
    if include_whats_new:
        attrs["whatsNew"] = WHATS_NEW
        print("Updating version localization (description, keywords, what's new, URLs)...")
    else:
        print("Updating version localization (description, keywords, URLs — skipping whatsNew for initial version)...")

    payload = {
        "data": {
            "type": "appStoreVersionLocalizations",
            "id": localization_id,
            "attributes": attrs,
        }
    }
    result = api_patch(f"/appStoreVersionLocalizations/{localization_id}", payload)
    if result is not None:
        print("  Version localization updated.")
    else:
        # If it failed with whatsNew, retry without it
        if include_whats_new:
            print("  Retrying without whatsNew...")
            update_version_localization(localization_id, include_whats_new=False)
        else:
            print("  WARNING: Failed to update version localization.")


def update_version_copyright(version_id):
    """Set copyright on the App Store version."""
    print(f"Setting copyright: {COPYRIGHT}")
    payload = {
        "data": {
            "type": "appStoreVersions",
            "id": version_id,
            "attributes": {
                "copyright": COPYRIGHT,
            },
        }
    }
    result = api_patch(f"/appStoreVersions/{version_id}", payload)
    if result is not None:
        print("  Copyright updated.")
    else:
        print("  WARNING: Failed to update copyright.")


def set_content_rights(app_id):
    """Declare content rights — no third-party content."""
    print("Setting content rights (no third-party content)...")
    payload = {
        "data": {
            "type": "apps",
            "id": app_id,
            "attributes": {
                "contentRightsDeclaration": "DOES_NOT_USE_THIRD_PARTY_CONTENT",
            },
        }
    }
    result = api_patch(f"/apps/{app_id}", payload)
    if result is not None:
        print("  Content rights set.")
    else:
        print("  WARNING: Failed to set content rights.")


def set_pricing_free(app_id):
    """Ensure the app is set to Free pricing."""
    print("Checking/setting pricing to FREE...")
    # In the modern App Store Connect API, pricing is managed via appPriceSchedules
    # For a free app, we need to check if there's already a free price point
    # First, let's check current pricing
    url = f"{BASE_URL}/apps/{app_id}/appPriceSchedule"
    resp = requests.get(url, headers=headers(), params={"include": "manualPrices,baseTerritoryPrices"})
    if resp.status_code == 200:
        print("  Pricing schedule exists. Checking if free...")
        # Try to read base prices
        base_url = f"{BASE_URL}/apps/{app_id}/appPriceSchedule/baseTerritoryPrices"
        base_resp = requests.get(base_url, headers=headers())
        if base_resp.status_code == 200:
            prices = base_resp.json().get("data", [])
            if prices:
                # Check if the price point is the free tier
                for price in prices:
                    pp_link = price.get("relationships", {}).get("appPricePoint", {}).get("links", {}).get("related")
                    if pp_link:
                        pp_resp = requests.get(pp_link, headers=headers())
                        if pp_resp.status_code == 200:
                            pp_data = pp_resp.json().get("data", {})
                            customer_price = pp_data.get("attributes", {}).get("customerPrice", "")
                            if customer_price == "0.0" or customer_price == "0":
                                print("  App is already FREE.")
                                return
        print("  Setting price to FREE...")
    else:
        print(f"  Could not fetch pricing (status {resp.status_code}). Attempting to set FREE pricing...")

    # To set free pricing, we need to find the free price point for the base territory (US)
    # First get the app's price points for the US territory
    # The free tier price point ID is typically predictable
    # Let's find it via the API
    pp_url = f"{BASE_URL}/apps/{app_id}/appPricePoints"
    pp_resp = requests.get(pp_url, headers=headers(), params={
        "filter[territory]": "USA",
        "limit": 1,
    })
    if pp_resp.status_code == 200:
        price_points = pp_resp.json().get("data", [])
        if price_points:
            free_pp_id = price_points[0]["id"]  # First price point is typically FREE
            print(f"  Found price point: {free_pp_id}")

            # Create/update price schedule
            schedule_payload = {
                "data": {
                    "type": "appPriceSchedules",
                    "relationships": {
                        "app": {
                            "data": {"type": "apps", "id": app_id}
                        },
                        "baseTerritory": {
                            "data": {"type": "territories", "id": "USA"}
                        },
                        "manualPrices": {
                            "data": [
                                {
                                    "type": "appPrices",
                                    "id": "${price1}",
                                }
                            ]
                        },
                    },
                },
                "included": [
                    {
                        "type": "appPrices",
                        "id": "${price1}",
                        "relationships": {
                            "appPricePoint": {
                                "data": {
                                    "type": "appPricePoints",
                                    "id": free_pp_id,
                                }
                            },
                        },
                    }
                ],
            }
            schedule_url = f"{BASE_URL}/appPriceSchedules"
            sched_resp = requests.post(schedule_url, headers=headers(), json=schedule_payload)
            if sched_resp.status_code in (200, 201):
                print("  Pricing set to FREE.")
            elif sched_resp.status_code == 409:
                print("  Pricing already set (conflict — likely already FREE).")
            else:
                print(f"  WARNING: Could not set pricing ({sched_resp.status_code}): {sched_resp.text}")
                print("  You may need to set pricing manually in App Store Connect.")
        else:
            print("  WARNING: No price points found for USA territory.")
    else:
        print(f"  WARNING: Could not fetch price points ({pp_resp.status_code}). Set pricing manually.")


def parse_args():
    ap = argparse.ArgumentParser(description="Set App Store Connect metadata for an app.")
    ap.add_argument("--bundle-id", default=os.environ.get("SHOTS_BUNDLE_ID", ""),
                    help="App bundle identifier (overrides default + meta-json).")
    ap.add_argument("--meta-json", default=os.environ.get("SHOTS_META_JSON", ""),
                    help="Path to a metadata JSON file (or a project dir holding .yaver/appstore.json).")
    return ap.parse_args()


def main():
    apply_overrides(parse_args())

    # Guard against cross-app contamination: the built-in metadata
    # describes DEFAULT_BUNDLE_ID. If we're targeting a different app and
    # no metadata file was supplied, refuse rather than stamp Yaver's
    # description/keywords/URLs onto someone else's listing.
    if BUNDLE_ID != DEFAULT_BUNDLE_ID and not _META_LOADED:
        print(f"Refusing to set metadata for {BUNDLE_ID}: no metadata provided.")
        print("Add a .yaver/appstore.json (or pass --meta-json) with this app's "
              "name/subtitle/description/keywords/categories/URLs. Screenshots "
              "were uploaded regardless — only metadata is skipped.")
        sys.exit(0)

    print("=" * 60)
    print(f"App Store Connect Info Updater — {BUNDLE_ID}")
    print("=" * 60)
    print()

    # 1. Find the app
    app_id, _ = find_app()
    print()

    # 2. Get app info and update categories
    app_info_id, _ = get_app_info(app_id)
    update_app_info_categories(app_info_id)
    print()

    # 3. Update app info localization (name, subtitle, privacy URL)
    info_loc_id = get_or_create_app_info_localization(app_info_id)
    update_app_info_localization(info_loc_id)
    print()

    # 4. Get app store version and update localization + copyright
    version_id, _ = get_app_store_version(app_id)
    if version_id:
        ver_loc_id = get_or_create_version_localization(version_id)
        update_version_localization(ver_loc_id)
        update_version_copyright(version_id)
    print()

    # 5. Set content rights (no third-party content)
    set_content_rights(app_id)
    print()

    # 6. Set pricing to FREE
    set_pricing_free(app_id)
    print()

    # Summary
    print("=" * 60)
    print("DONE. Summary of updates:")
    print(f"  App:              {APP_NAME} ({BUNDLE_ID})")
    print(f"  Subtitle:         {SUBTITLE}")
    print(f"  Categories:       {PRIMARY_CATEGORY} / {SECONDARY_CATEGORY}")
    print(f"  Privacy URL:      {PRIVACY_POLICY_URL}")
    print(f"  Support URL:      {SUPPORT_URL}")
    print(f"  Marketing URL:    {MARKETING_URL}")
    print(f"  Description:      {len(DESCRIPTION)} chars")
    print(f"  Keywords:         {KEYWORDS}")
    print(f"  What's New:       {len(WHATS_NEW)} chars")
    print(f"  Pricing:          FREE")
    print("=" * 60)


if __name__ == "__main__":
    main()
