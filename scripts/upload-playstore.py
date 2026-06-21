#!/usr/bin/env python3
"""Upload an AAB to a Google Play track.

Multi-tenant: PLAY_PACKAGE_NAME + PLAY_TRACK are env-overridable so the
SAME helper ships a CUSTOMER's app to THEIR package/track (driven by the
generated deploy script), not just Yaver's own. Defaults preserve the
original Yaver self-deploy behaviour (io.yaver.mobile, internal track).
"""

import os
import shutil
import socket
import subprocess
import sys

# 245 MB AABs on slow links run past httplib2's default (~60s) socket timeout.
# Setting this BEFORE importing google clients so their httplib2.Http picks it up.
socket.setdefaulttimeout(600)

from google.oauth2.service_account import Credentials
from googleapiclient.discovery import build
from googleapiclient.http import MediaFileUpload

# Package + track are env-overridable for multi-tenant customer deploys;
# defaults keep Yaver's own self-deploy working unchanged.
PACKAGE = os.environ.get("PLAY_PACKAGE_NAME", "io.yaver.mobile")
KEY_FILE = os.environ.get("PLAY_STORE_KEY_FILE", "")
AAB_PATH = os.path.join(os.path.dirname(__file__), "..", "mobile", "android", "app", "build", "outputs", "bundle", "release", "app-release.aab")
AAB_PATH = os.environ.get("AAB_PATH", AAB_PATH)
# internal | alpha | beta | production (Google Play track names).
TRACK = os.environ.get("PLAY_TRACK", "internal")

SCOPES = ["https://www.googleapis.com/auth/androidpublisher"]

def main():
    print(f"Uploading {AAB_PATH} to Google Play ({PACKAGE}) - {TRACK} track...")

    credentials = Credentials.from_service_account_file(KEY_FILE, scopes=SCOPES)
    service = build("androidpublisher", "v3", credentials=credentials)

    # Create an edit
    edit = service.edits().insert(body={}, packageName=PACKAGE).execute()
    edit_id = edit["id"]
    print(f"Created edit: {edit_id}")

    # Upload AAB in 5 MB chunks so we can report progress and tolerate transient stalls.
    size = os.path.getsize(AAB_PATH)
    print(f"AAB size: {size / 1024 / 1024:.1f} MB")
    media = MediaFileUpload(
        AAB_PATH,
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
            print(f"  upload progress: {pct:3d}% ({status.resumable_progress} / {size} bytes)")
    bundle = response
    version_code = bundle["versionCode"]
    print(f"Uploaded bundle: versionCode={version_code}")

    # Assign to internal track
    service.edits().tracks().update(
        packageName=PACKAGE,
        editId=edit_id,
        track=TRACK,
        body={
            "track": TRACK,
            "releases": [{
                "versionCodes": [str(version_code)],
                "status": "draft",
            }],
        }
    ).execute()
    print(f"Assigned to {TRACK} track")

    # Commit the edit
    service.edits().commit(packageName=PACKAGE, editId=edit_id).execute()
    print(f"Edit committed! Build {version_code} is live on {TRACK} track.")

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
