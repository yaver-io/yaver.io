#!/usr/bin/env python3
"""Provision headless Apple signing identities in a Yaver-owned keychain.

This deliberately creates new DEVELOPMENT and DISTRIBUTION certificates. It
never revokes an existing certificate: a certificate visible to the team may
belong to another release machine or product. The App Store Connect API key
must be a team key with access to the provisioning endpoints.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import shlex
import shutil
import stat
import subprocess
import sys
import tempfile
import time

import jwt
import requests


API = "https://api.appstoreconnect.apple.com/v1"
CERTIFICATE_TYPES = ("DEVELOPMENT", "DISTRIBUTION")


def fail(message: str) -> "NoReturn":
    raise SystemExit(f"ERROR: {message}")


def run(*args: str, capture: bool = False) -> str:
    result = subprocess.run(
        args,
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
        stderr=subprocess.PIPE if capture else None,
    )
    return result.stdout.strip() if capture else ""


def require_env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        fail(f"{name} is required")
    return value


def token() -> str:
    key_path = Path(require_env("APP_STORE_KEY_PATH")).expanduser()
    if not key_path.is_file():
        fail(f"APP_STORE_KEY_PATH does not exist: {key_path}")
    return jwt.encode(
        {
            "iss": require_env("APP_STORE_KEY_ISSUER"),
            "iat": int(time.time()),
            "exp": int(time.time()) + 600,
            "aud": "appstoreconnect-v1",
        },
        key_path.read_text(),
        algorithm="ES256",
        headers={"kid": require_env("APP_STORE_KEY_ID")},
    )


def api_request(method: str, path: str, *, payload: dict | None = None) -> dict:
    response = requests.request(
        method,
        f"{API}{path}",
        headers={"Authorization": f"Bearer {token()}", "Content-Type": "application/json"},
        json=payload,
        timeout=30,
    )
    if response.status_code >= 400:
        detail = response.text[:1000]
        fail(f"App Store Connect {method} {path} returned {response.status_code}: {detail}")
    return response.json() if response.content else {}


def certificate_inventory() -> list[dict]:
    response = api_request(
        "GET",
        "/certificates?limit=200&fields%5Bcertificates%5D=certificateType,displayName,expirationDate,activated,certificateContent",
    )
    return response.get("data", [])


def create_identity(keychain: Path, password: str, certificate_type: str, directory: Path) -> None:
    slug = certificate_type.lower()
    private_key = directory / f"{slug}.key.pem"
    csr = directory / f"{slug}.csr.pem"
    certificate = directory / f"{slug}.cer"

    run(
        "/usr/bin/openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes",
        "-keyout", str(private_key), "-out", str(csr),
        "-subj", f"/CN=Yaver Headless {certificate_type}/O=Yaver.io",
    )
    os.chmod(private_key, stat.S_IRUSR | stat.S_IWUSR)
    payload = {
        "data": {
            "type": "certificates",
            "attributes": {
                "certificateType": certificate_type,
                "csrContent": csr.read_text(),
            },
        }
    }
    created = api_request("POST", "/certificates", payload=payload)
    content = created.get("data", {}).get("attributes", {}).get("certificateContent", "")
    if not content:
        fail(f"Apple created {certificate_type} but returned no certificate content")
    certificate.write_bytes(base64.b64decode(content))

    run(
        "/usr/bin/security", "import", str(private_key), "-k", str(keychain), "-P", "",
        "-T", "/usr/bin/codesign", "-T", "/usr/bin/security",
    )
    run("/usr/bin/security", "import", str(certificate), "-k", str(keychain), "-T", "/usr/bin/codesign")
    run(
        "/usr/bin/security", "set-key-partition-list", "-S", "apple-tool:,apple:,codesign:",
        "-s", "-k", password, str(keychain),
    )


def identities(keychain: Path) -> str:
    return run("/usr/bin/security", "find-identity", "-v", "-p", "codesigning", str(keychain), capture=True)


def prove_identity(keychain: Path, label: str, directory: Path) -> None:
    output = identities(keychain)
    matching = next((line.split()[1] for line in output.splitlines() if label in line), "")
    if not matching:
        fail(f"the new keychain has no usable {label} identity")
    probe = directory / f"probe-{label.replace(' ', '-').lower()}"
    shutil.copyfile("/usr/bin/true", probe)
    os.chmod(probe, 0o700)
    run("/usr/bin/codesign", "--force", "--sign", matching, str(probe))


def update_env(env_path: Path, keychain: Path, password: str) -> None:
    existing = env_path.read_text().splitlines() if env_path.exists() else []
    kept = [
        line for line in existing
        if not line.lstrip().startswith(("export YAVER_SIGNING_KEYCHAIN=", "export YAVER_SIGNING_KEYCHAIN_PASSWORD="))
    ]
    kept.extend(
        [
            f"export YAVER_SIGNING_KEYCHAIN={shlex.quote(str(keychain))}",
            f"export YAVER_SIGNING_KEYCHAIN_PASSWORD={shlex.quote(password)}",
        ]
    )
    env_path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = env_path.with_name(f".{env_path.name}.{os.getpid()}.tmp")
    temporary.write_text("\n".join(kept).rstrip() + "\n")
    os.chmod(temporary, 0o600)
    os.replace(temporary, env_path)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true", help="only prove provisioning API access")
    parser.add_argument(
        "--revoke-orphaned", action="append", default=[], metavar="ID=SHA1",
        help="revoke an unusable certificate only when its downloaded certificate matches SHA1",
    )
    parser.add_argument(
        "--keychain", type=Path,
        default=Path.home() / "Library/Keychains/yaver-signing.keychain-db",
    )
    parser.add_argument(
        "--env-file", type=Path,
        default=Path.home() / ".appstoreconnect/yaver.env",
    )
    args = parser.parse_args()

    inventory = certificate_inventory()
    by_id = {item.get("id", ""): item for item in inventory}
    for specification in args.revoke_orphaned:
        try:
            certificate_id, expected_sha1 = specification.split("=", 1)
        except ValueError:
            fail("--revoke-orphaned must be ID=SHA1")
        item = by_id.get(certificate_id)
        if not item:
            fail(f"certificate {certificate_id} is not active or does not exist")
        content = item.get("attributes", {}).get("certificateContent", "")
        actual_sha1 = hashlib.sha1(base64.b64decode(content)).hexdigest().upper()
        if actual_sha1 != expected_sha1.upper():
            fail(f"certificate {certificate_id} fingerprint mismatch; refusing to revoke it")
        api_request("DELETE", f"/certificates/{certificate_id}")
        print(f"Revoked verified orphan certificate {certificate_id} ({actual_sha1})")
    if args.revoke_orphaned:
        inventory = certificate_inventory()

    counts: dict[str, int] = {}
    for item in inventory:
        attributes = item.get("attributes", {})
        if attributes.get("activated", True):
            kind = attributes.get("certificateType", "UNKNOWN")
            counts[kind] = counts.get(kind, 0) + 1
    print("Apple provisioning API access: ok")
    print("Active signing certificate inventory: " + ", ".join(f"{k}={v}" for k, v in sorted(counts.items())))
    if args.check:
        return 0

    keychain = args.keychain.expanduser().resolve()
    if keychain.exists():
        configured = Path(os.environ.get("YAVER_SIGNING_KEYCHAIN", "")).expanduser()
        password = os.environ.get("YAVER_SIGNING_KEYCHAIN_PASSWORD", "")
        if not password or not configured.exists() or configured.resolve() != keychain:
            fail(f"refusing to overwrite existing keychain without its configured password: {keychain}")
        with tempfile.TemporaryDirectory(prefix="yaver-apple-signing-verify-") as temporary_name:
            run("/usr/bin/security", "unlock-keychain", "-p", password, str(keychain))
            run(
                "/usr/bin/security", "set-key-partition-list", "-S", "apple-tool:,apple:,codesign:",
                "-s", "-k", password, str(keychain),
            )
            run("/usr/bin/security", "list-keychains", "-d", "user", "-s", str(keychain), str(Path.home() / "Library/Keychains/login.keychain-db"))
            prove_identity(keychain, "Apple Development", Path(temporary_name))
            prove_identity(keychain, "Apple Distribution", Path(temporary_name))
        run("/usr/bin/security", "list-keychains", "-d", "user", "-s", str(keychain), str(Path.home() / "Library/Keychains/login.keychain-db"))
        print(f"Existing Yaver signing keychain verified: {keychain}")
        return 0
    keychain.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    password = secrets.token_urlsafe(36)

    with tempfile.TemporaryDirectory(prefix="yaver-apple-signing-") as temporary_name:
        directory = Path(temporary_name)
        run("/usr/bin/security", "create-keychain", "-p", password, str(keychain))
        os.chmod(keychain, 0o600)
        # Persist recovery material before asking Apple to create anything. If
        # the second certificate hits a team quota, the first identity and its
        # private key remain usable instead of becoming another orphan.
        update_env(args.env_file.expanduser(), keychain, password)
        try:
            run("/usr/bin/security", "unlock-keychain", "-p", password, str(keychain))
            run("/usr/bin/security", "set-keychain-settings", "-t", "100000", "-u", str(keychain))
            run("/usr/bin/security", "list-keychains", "-d", "user", "-s", str(keychain), str(Path.home() / "Library/Keychains/login.keychain-db"))
            for certificate_type in CERTIFICATE_TYPES:
                print(f"Creating Yaver {certificate_type} certificate…")
                create_identity(keychain, password, certificate_type, directory)
            prove_identity(keychain, "Apple Development", directory)
            prove_identity(keychain, "Apple Distribution", directory)
        except BaseException:
            # Preserve the keychain when Apple has created either certificate:
            # deleting its private key would turn a recoverable partial run into
            # another orphaned certificate. The next run refuses to overwrite it.
            print(f"Partial provisioning keychain preserved at {keychain}", file=sys.stderr)
            raise

    run("/usr/bin/security", "list-keychains", "-d", "user", "-s", str(keychain), str(Path.home() / "Library/Keychains/login.keychain-db"))
    print(f"Yaver signing keychain ready: {keychain}")
    print(f"Headless credentials saved owner-only in: {args.env_file.expanduser()}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
