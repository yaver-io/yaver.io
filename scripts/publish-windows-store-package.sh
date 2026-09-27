#!/usr/bin/env bash
# Publish one immutable, signed Windows Store candidate to Yaver's existing R2
# download origin. This script intentionally has no overwrite path.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bucket="${YAVER_DOWNLOAD_BUCKET:-yaver-apk}"
origin="${YAVER_DOWNLOAD_ORIGIN:-https://download.yaver.io}"
version="${1:-}"
installer="${2:-}"
[[ "$version" =~ ^[0-9]+[.][0-9]+[.][0-9]+$ ]] || { echo "usage: $0 <x.y.z> <signed-installer.exe>" >&2; exit 2; }
[[ -f "$installer" ]] || { echo "installer not found: $installer" >&2; exit 2; }
expected="yaver-gui-${version}-win-x64-setup.exe"
[[ "$(basename "$installer")" == "$expected" ]] || { echo "expected filename $expected" >&2; exit 2; }
key="windows/${version}/${expected}"
url="${origin}/${key}"
status="$(curl --silent --show-error --head --output /dev/null --write-out '%{http_code}' "$url")"
case "$status" in 404) ;; 200) echo "refusing to overwrite immutable Store object: $key" >&2; exit 1 ;; *) echo "could not prove object absent (HTTP $status)" >&2; exit 1 ;; esac

expected_size="$(stat -f '%z' "$installer" 2>/dev/null || stat -c '%s' "$installer")"
expected_sha="$(shasum -a 256 "$installer" | awk '{print $1}')"
check_dir="$(mktemp -d /tmp/yaver-store-publish.XXXXXX)"
trap 'rm -rf "$check_dir"' EXIT
npx --prefix "$repo_root/web" wrangler r2 object put "${bucket}/${key}" --remote --file "$installer" \
  --content-type 'application/vnd.microsoft.portable-executable' \
  --content-disposition "attachment; filename=\"${expected}\"" \
  --cache-control 'public, max-age=31536000, immutable'
readback_status="$(curl --fail --silent --show-error --max-redirs 0 --output "$check_dir/readback.exe" --write-out '%{http_code} %{num_redirects}' "$url")"
[[ "$readback_status" == "200 0" ]] || { echo "unexpected public response: $readback_status" >&2; exit 1; }
actual_size="$(stat -f '%z' "$check_dir/readback.exe" 2>/dev/null || stat -c '%s' "$check_dir/readback.exe")"
actual_sha="$(shasum -a 256 "$check_dir/readback.exe" | awk '{print $1}')"
[[ "$actual_size" == "$expected_size" && "$actual_sha" == "$expected_sha" ]] || { echo "public readback differs from candidate" >&2; exit 1; }
printf 'URL: %s\nSize: %s\nSHA-256: %s\n' "$url" "$actual_size" "$actual_sha"
