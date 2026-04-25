#!/usr/bin/env bash
# embedded/c-agent/build.sh
#
# Opt-in build for the c-agent runtime. Standalone — does not touch
# Yaver's main build, CI, or release pipelines. See
# docs/c-agent-architecture.md "Status".
#
# Usage:
#   ./build.sh                # debug + tests
#   ./build.sh release        # release + tests, -Werror
#   ./build.sh asan           # debug + ASAN/UBSan
#   ./build.sh clean          # wipe build/

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="${ROOT_DIR}/build"

mode="${1:-debug}"

case "$mode" in
clean)
    rm -rf "$BUILD_DIR"
    echo "cleaned ${BUILD_DIR}"
    exit 0
    ;;
debug)
    cmake_args=(-DCMAKE_BUILD_TYPE=Debug -DYVR_CAGENT_TESTS=ON)
    ;;
release)
    cmake_args=(-DCMAKE_BUILD_TYPE=Release -DYVR_CAGENT_TESTS=ON -DYVR_CAGENT_WERROR=ON)
    ;;
asan)
    cmake_args=(-DCMAKE_BUILD_TYPE=Debug -DYVR_CAGENT_TESTS=ON -DYVR_CAGENT_ASAN=ON)
    ;;
*)
    echo "unknown mode: ${mode} (expected: debug | release | asan | clean)" >&2
    exit 1
    ;;
esac

mkdir -p "${BUILD_DIR}"
cmake -S "${ROOT_DIR}" -B "${BUILD_DIR}" \
    -DCMAKE_EXPORT_COMPILE_COMMANDS=ON \
    "${cmake_args[@]}"
cmake --build "${BUILD_DIR}" -j
ctest --test-dir "${BUILD_DIR}" --output-on-failure
