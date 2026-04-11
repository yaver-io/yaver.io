#!/usr/bin/env python3
"""Upload AAB to Google Play Internal Testing track."""

import os
import subprocess
import sys
from google.oauth2.service_account import Credentials
from googleapiclient.discovery import build
from googleapiclient.http import MediaFileUpload

PACKAGE = "io.yaver.mobile"
KEY_FILE = os.environ.get("PLAY_STORE_KEY_FILE", "")
AAB_PATH = os.path.join(os.path.dirname(__file__), "..", "mobile", "android", "app", "build", "outputs", "bundle", "release", "app-release.aab")
TRACK = "internal"

SCOPES = ["https://www.googleapis.com/auth/androidpublisher"]

def main():
    print(f"Uploading {AAB_PATH} to Google Play ({PACKAGE}) - {TRACK} track...")

    credentials = Credentials.from_service_account_file(KEY_FILE, scopes=SCOPES)
    service = build("androidpublisher", "v3", credentials=credentials)

    # Create an edit
    edit = service.edits().insert(body={}, packageName=PACKAGE).execute()
    edit_id = edit["id"]
    print(f"Created edit: {edit_id}")

    # Upload AAB
    media = MediaFileUpload(AAB_PATH, mimetype="application/octet-stream", resumable=True)
    bundle = service.edits().bundles().upload(
        packageName=PACKAGE,
        editId=edit_id,
        media_body=media
    ).execute()
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

    subprocess.run(
        ["mobile-cache-cleanup.sh", "mark-deployed", "yaver"],
        check=False,
    )

if __name__ == "__main__":
    main()
