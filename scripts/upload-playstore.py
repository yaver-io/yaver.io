#!/usr/bin/env python3
"""Upload one or more AABs to a Google Play track.

Multi-tenant: PLAY_PACKAGE_NAME + PLAY_TRACK are env-overridable so the
SAME helper ships a CUSTOMER's app to THEIR package/track (driven by the
generated deploy script), not just Yaver's own. Defaults preserve the
original Yaver self-deploy behaviour (io.yaver.mobile, internal track).
"""

import os
import re
import shutil
import socket
import subprocess
import sys
import zipfile

# Large AABs on slow links run past httplib2's default (~60s) socket timeout.
# Setting this BEFORE importing google clients so their httplib2.Http picks it up.
socket.setdefaulttimeout(int(os.environ.get("PLAY_UPLOAD_SOCKET_TIMEOUT", "1800")))

# Package + track are env-overridable for multi-tenant customer deploys;
# defaults keep Yaver's own self-deploy working unchanged.
PACKAGE = os.environ.get("PLAY_PACKAGE_NAME", "io.yaver.mobile")
KEY_FILE = os.environ.get("PLAY_STORE_KEY_FILE", "")
AAB_PATH = os.path.join(os.path.dirname(__file__), "..", "mobile", "android", "app", "build", "outputs", "bundle", "release", "app-release.aab")
AAB_PATH = os.environ.get("AAB_PATH", AAB_PATH)
AAB_PATHS = [p.strip() for p in os.environ.get("AAB_PATHS", AAB_PATH).split(",") if p.strip()]
DEFAULT_GRADLE_PATH = os.path.abspath(os.path.join(
    os.path.dirname(__file__), "..", "mobile", "android", "app", "build.gradle"
))
# internal | alpha | beta | production (Google Play track names).
TRACK = os.environ.get("PLAY_TRACK", "internal")
# Internal testing (≤100 testers) is the SAFE automated lane: a release must be
# LIVE to actually reach testers. `completed` is the 100%-to-testers state for a
# fixed-track like internal (the API refuses an inProgress release without a
# <1 userFraction, and completed needs none). The old blanket "draft" default
# made every automated internal upload a silent dead end — the AAB was on the
# track, but no tester could ever receive it (measured 2026-08-21). alpha/beta/
# production MUST stay draft by default: those never auto-go-live without an
# explicit promote. An explicit PLAY_RELEASE_STATUS always wins.
def track_is_internal(track: str):
    return track in {"internal", "qa"} or track.endswith((":internal", ":qa"))


_DEFAULT_RELEASE_STATUS = "completed" if track_is_internal(TRACK) else "draft"
RELEASE_STATUS = os.environ.get("PLAY_RELEASE_STATUS", _DEFAULT_RELEASE_STATUS)

SCOPES = ["https://www.googleapis.com/auth/androidpublisher"]

def extract_aab_version_code(aab_path: str):
    """Best-effort versionCode for a build, read from the AAB's own manifest.

    An AAB is a zip; the merged manifest at base/manifest/AndroidManifest.xml
    is BINARY Android XML, so the plain-text `android:versionCode="N"` regex
    only matches when the manifest happens to be text (rare). Returns the int
    when readable, else None — callers then fall back to the build.gradle
    versionCode (read_gradle_version_code) and finally to Play's own 403.
    """
    try:
        with zipfile.ZipFile(aab_path) as z:
            mn = "base/manifest/AndroidManifest.xml"
            if mn not in z.namelist():
                return None
            data = z.read(mn).decode("utf-8", errors="replace")
            m = re.search(r'android:versionCode="(\d+)"', data)
            if m:
                return int(m.group(1))
            # Binary XML: versionCode is a 4-byte little-endian int that sits
            # near the 'versionCode' attr name in the string pool. Fragile —
            # prefer gradle, but try once.
            raw = z.read(mn)
            idx = raw.find(b"versionCode")
            if idx >= 0 and idx + 16 <= len(raw):
                candidate = int.from_bytes(raw[idx + 8:idx + 12], "little")
                if 0 < candidate < 10000000:
                    return candidate
            return None
    except Exception:
        return None


def read_gradle_version_code(gradle_path: str):
    """versionCode from an app/build.gradle (the value the AAB was built with).

    Every Yaver Android surface (phone, wear, tv, xr, auto) derives its
    versionCode from mobile/android/app/build.gradle, so this is the same
    number Play sees. Returns int or None.
    """
    try:
        with open(gradle_path, "r", encoding="utf-8") as f:
            m = re.search(r"versionCode\s+(\d+)", f.read())
            return int(m.group(1)) if m else None
    except Exception:
        return None


def parse_version_code_hints(value: str, bundle_count: int):
    """Parse build-time version codes supplied by the surface deployer.

    Standalone Wear/TV projects receive their version code through Gradle
    properties, so neither their source file nor the phone build.gradle is an
    authoritative description of the resulting AAB.  Their deploy scripts
    pass the exact build-time value through PLAY_VERSION_CODE(S).
    """
    if not value:
        return [None] * bundle_count
    raw_codes = [item.strip() for item in value.split(",") if item.strip()]
    if len(raw_codes) != bundle_count:
        raise ValueError(
            "PLAY_VERSION_CODES must contain exactly one positive integer "
            "for each AAB"
        )
    try:
        codes = [int(item) for item in raw_codes]
    except ValueError as exc:
        raise ValueError("PLAY_VERSION_CODES must contain only integers") from exc
    if any(code <= 0 for code in codes):
        raise ValueError("PLAY_VERSION_CODES must contain only positive integers")
    return codes


def is_form_factor_track(track: str):
    return ":" in track


def main():
    # Keep SDK imports inside the executable path. Pure preflight helpers and
    # their tests must remain runnable before run-playstore-upload.sh bootstraps
    # the isolated Google client environment.
    from google.oauth2.service_account import Credentials
    from googleapiclient.discovery import build
    from googleapiclient.errors import HttpError
    from googleapiclient.http import MediaFileUpload

    print(f"Uploading {len(AAB_PATHS)} AAB(s) to Google Play ({PACKAGE}) - {TRACK} track...", flush=True)

    version_code_value = os.environ.get(
        "PLAY_VERSION_CODES", os.environ.get("PLAY_VERSION_CODE", "")
    )
    try:
        version_code_hints = parse_version_code_hints(
            version_code_value, len(AAB_PATHS)
        )
    except ValueError as exc:
        print(f"ERROR: {exc}", file=sys.stderr, flush=True)
        raise SystemExit(2)

    credentials = Credentials.from_service_account_file(KEY_FILE, scopes=SCOPES)
    service = build("androidpublisher", "v3", credentials=credentials)

    # Create an edit
    edit = service.edits().insert(body={}, packageName=PACKAGE).execute()
    edit_id = edit["id"]
    print(f"Created edit: {edit_id}", flush=True)

    # Dedicated Wear/TV/XR/Automotive tracks must first be enabled for the app
    # in Play Console. Probe that real operation before uploading bytes into an
    # edit that can never be committed.
    if is_form_factor_track(TRACK):
        try:
            service.edits().tracks().get(
                packageName=PACKAGE, editId=edit_id, track=TRACK
            ).execute()
        except HttpError as exc:
            if getattr(getattr(exc, "resp", None), "status", None) == 404:
                print(
                    f"PLAY FORM-FACTOR TRACK REQUIRED: {TRACK} is not enabled "
                    f"for {PACKAGE}. In Play Console, open the app, add the "
                    "matching form-factor release track, configure its internal "
                    "testers, then rerun this deploy. No bundle was uploaded. "
                    "Console: https://play.google.com/console/developers",
                    flush=True,
                )
                raise SystemExit(2)
            raise

    # Pre-flight versionCode collision check (2026-08-11, Wear 298 / TV 300 /
    # XR 301 on the SAME io.yaver.mobile package). Tracks are incomplete: an
    # uploaded bundle can reserve a versionCode even when no current release
    # references it. edits.bundles.list is the operation-backed inventory of
    # all current bundles for this app/edit, so ask it before uploading bytes.
    highest = 0
    try:
        bundles = service.edits().bundles().list(
            packageName=PACKAGE, editId=edit_id
        ).execute().get("bundles", [])
        for bundle in bundles:
            code = bundle.get("versionCode")
            if isinstance(code, int) and code > highest:
                highest = code
    except Exception as exc:
        print(
            f"note: Play bundle inventory unavailable ({exc}); "
            "falling back to track inventory.",
            flush=True,
        )
        try:
            tracks = service.edits().tracks().list(
                packageName=PACKAGE, editId=edit_id
            ).execute().get("tracks", [])
            for tr in tracks:
                for rel in tr.get("releases", []):
                    for code in rel.get("versionCodes", []):
                        if isinstance(code, int) and code > highest:
                            highest = code
        except Exception:
            highest = 0

    if highest:
        for index, aab_path in enumerate(AAB_PATHS):
            code = version_code_hints[index]
            if code is None:
                code = extract_aab_version_code(aab_path)
            if code is None:
                # Binary manifests defeat the zip reader; the AAB was built
                # from mobile/android/app/build.gradle, so read that.
                code = read_gradle_version_code(DEFAULT_GRADLE_PATH)
            if code is None:
                print(
                    f"note: could not read versionCode from {aab_path} "
                    f"(binary manifest, no gradle) — skipping pre-flight check; "
                    f"Play's 403 will catch a collision.",
                    flush=True,
                )
                continue
            if 0 < highest and code <= highest:
                print(
                    f"VERSION CODE COLLISION: {aab_path} carries versionCode {code}, "
                    f"but {highest} already exists for {PACKAGE}. "
                    f"Play refuses re-uploaded codes. Bump the app's versionCode "
                    f"above {highest} (e.g. to {highest + 1}) and rebuild before "
                    f"uploading — this script never rewrites your build.",
                    flush=True,
                )
                raise SystemExit(2)
    else:
        print(
            f"note: no prior app bundles found for {PACKAGE} — first upload "
            "(or read-only access); proceeding.",
            flush=True,
        )

    version_codes = []
    for aab_path in AAB_PATHS:
        # Upload AAB in 5 MB chunks so we can report progress and tolerate transient stalls.
        size = os.path.getsize(aab_path)
        print(f"AAB: {aab_path}", flush=True)
        print(f"AAB size: {size / 1024 / 1024:.1f} MB", flush=True)
        media = MediaFileUpload(
            aab_path,
            mimetype="application/octet-stream",
            resumable=True,
            chunksize=5 * 1024 * 1024,
        )
        request = service.edits().bundles().upload(
            packageName=PACKAGE,
            editId=edit_id,
            media_body=media,
        )
        response = None
        while response is None:
            status, response = request.next_chunk()
            if status:
                pct = status.resumable_progress * 100 // max(1, size)
                print(f"  upload progress: {pct:3d}% ({status.resumable_progress} / {size} bytes)", flush=True)
        bundle = response
        version_code = str(bundle["versionCode"])
        version_codes.append(version_code)
        print(f"Uploaded bundle: versionCode={version_code}", flush=True)

    # Assign to internal track
    release_body = {
        "versionCodes": version_codes,
        "status": RELEASE_STATUS,
    }
    if RELEASE_STATUS == "inProgress":
        # The API refuses an inProgress release without a rollout fraction
        # ("IN_PROGRESS release must have fraction"), and the fraction must be
        # < 1. completed needs none. Only set a fraction for an explicit
        # inProgress caller.
        release_body["userFraction"] = 0.99
    service.edits().tracks().update(
        packageName=PACKAGE,
        editId=edit_id,
        track=TRACK,
        body={
            "track": TRACK,
            "releases": [release_body],
        }
    ).execute()
    print(f"Assigned versionCodes={','.join(version_codes)} to {TRACK} track with status={RELEASE_STATUS}", flush=True)

    # Commit the edit
    try:
        service.edits().commit(packageName=PACKAGE, editId=edit_id).execute()
    except HttpError as exc:
        detail = str(exc)
        if "Foreground Service permissions" in detail:
            print(
                "PLAY POLICY DECLARATION REQUIRED: Google accepted the bundle "
                "upload but refused to publish the edit because this app has not "
                "declared its foreground-service usage. In Play Console, open "
                f"{PACKAGE} > App content > Foreground service permissions, "
                "declare every service type present in the release manifest, "
                "submit the declaration, then rerun android-upload with the same "
                "signed AAB. Console: https://play.google.com/console/developers",
                flush=True,
            )
        elif "health features" in detail.lower():
            print(
                "PLAY HEALTH DECLARATION REQUIRED: Google accepted the bundle "
                "upload but refused to publish the edit until the app answers "
                "the Health apps declaration. In Play Console, open "
                f"{PACKAGE} > Policy and programs > App content > Health apps, "
                "declare whether this app includes health features, submit the "
                "answer, then rerun the same deploy with this signed AAB. "
                "Console: https://play.google.com/console/developers",
                flush=True,
            )
        raise
    print(f"Edit committed! Builds {','.join(version_codes)} are on {TRACK} track.", flush=True)

    # Best-effort local-Mac cache bookkeeping. This script lives only in
    # ~/.local/bin on the dev machine; on CI runners it isn't on PATH, and a
    # bare subprocess.run() raises FileNotFoundError (check=False suppresses
    # exit codes, not spawn failures) — which would fail the job *after* the
    # upload already committed. Resolve it first and never let it be fatal.
    cleanup = shutil.which("mobile-cache-cleanup.sh")
    if cleanup:
        try:
            subprocess.run([cleanup, "mark-deployed", "yaver"], check=False)
        except OSError as exc:
            print(f"(skipped cache cleanup: {exc})")

if __name__ == "__main__":
    main()
