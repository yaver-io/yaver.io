#!/usr/bin/env bash
set -euo pipefail

section="${1:?usage: run_test_suite_section.sh <section>}"

log_dir="/var/log/yaver-ci"
mkdir -p "$log_dir"
log_file="${log_dir}/test-suite-${section}.log"

cd /opt/yaver

echo "=== test-suite section: ${section} ===" | tee "$log_file"
echo "=== started: $(date -u '+%Y-%m-%dT%H:%M:%SZ') ===" | tee -a "$log_file"

./scripts/test-suite.sh "--${section}" 2>&1 | tee -a "$log_file"
status=${PIPESTATUS[0]}

echo "=== finished: $(date -u '+%Y-%m-%dT%H:%M:%SZ') (status=${status}) ===" | tee -a "$log_file"
exit "$status"
