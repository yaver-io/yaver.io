#!/usr/bin/env bash
set -euo pipefail

jsign_version="7.5"
expected_sha256="602a51c3545a6dc4fb99bd2ea7152b26d1345916d0c93ddfbd5936cb735af91c"
download_url="https://github.com/ebourg/jsign/releases/download/${jsign_version}/jsign-${jsign_version}.jar"
destination="${YAVER_JSIGN_JAR:-${TMPDIR:-/tmp}/yaver-jsign-${jsign_version}.jar}"

verify_jar() {
  local actual_sha256
  actual_sha256="$(shasum -a 256 "$destination" | awk '{print $1}')"
  [[ "$actual_sha256" == "$expected_sha256" ]] || {
    echo "Jsign checksum mismatch at ${destination}; refusing to use it." >&2
    return 1
  }
}

if [[ -f "$destination" ]]; then
  verify_jar
else
  curl --fail --silent --show-error --location "$download_url" --output "$destination"
  verify_jar
  chmod 0600 "$destination"
fi

echo "Verified Jsign ${jsign_version}: ${destination}"
