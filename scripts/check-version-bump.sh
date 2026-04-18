#!/bin/bash
# check-version-bump.sh — Verify that PRs bump versions for changed components.
# Used by CI. Compares versions.json on PR branch vs base branch.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Determine base branch (default: main)
BASE_BRANCH="${1:-origin/main}"

# Get list of changed files between base and HEAD
CHANGED_FILES=$(git diff --name-only "$BASE_BRANCH"...HEAD 2>/dev/null || git diff --name-only "$BASE_BRANCH" HEAD)

# Map directories to component names in versions.json
file_to_component() {
  local file="$1"
  case "$file" in
    desktop/agent/*) echo "cli" ;;
    desktop/installer/*) echo "installer" ;;
    mobile/*) echo "mobile" ;;
    relay/*) echo "relay" ;;
    web/*) echo "web" ;;
    backend/*) echo "backend" ;;
    pi-image/*) echo "piImage" ;;
    *) echo "" ;;
  esac
}

# Files/patterns to exclude from version bump requirements
should_exclude() {
  local file="$1"
  case "$file" in
    *.md) return 0 ;;
    .github/*) return 0 ;;
    scripts/*) return 0 ;;
    versions.json) return 0 ;;
    .gitignore) return 0 ;;
    .gitattributes) return 0 ;;
    LICENSE*) return 0 ;;
    CLAUDE.md) return 0 ;;
    *.txt) return 0 ;;
    *) return 1 ;;
  esac
}

# Collect components that had code changes
declare -A COMPONENTS_CHANGED
for file in $CHANGED_FILES; do
  if should_exclude "$file"; then
    continue
  fi
  component=$(file_to_component "$file")
  if [ -n "$component" ]; then
    COMPONENTS_CHANGED[$component]=1
  fi
done

if [ ${#COMPONENTS_CHANGED[@]} -eq 0 ]; then
  echo "No component code changes detected — version bump not required."
  exit 0
fi

echo "Components with code changes: ${!COMPONENTS_CHANGED[*]}"

# Compare versions.json between base and HEAD
BASE_VERSIONS=$(git show "$BASE_BRANCH":versions.json 2>/dev/null || echo "{}")
HEAD_VERSIONS=$(cat "$REPO_ROOT/versions.json")

failed=0
for component in "${!COMPONENTS_CHANGED[@]}"; do
  base_ver=$(echo "$BASE_VERSIONS" | node -e "
    let d=''; process.stdin.on('data',c=>d+=c); process.stdin.on('end',()=>{
      try { console.log(JSON.parse(d)['$component']||'0.0.0'); }
      catch { console.log('0.0.0'); }
    });
  ")
  head_ver=$(echo "$HEAD_VERSIONS" | node -e "
    let d=''; process.stdin.on('data',c=>d+=c); process.stdin.on('end',()=>{
      try { console.log(JSON.parse(d)['$component']||'0.0.0'); }
      catch { console.log('0.0.0'); }
    });
  ")

  if [ "$base_ver" = "$head_ver" ]; then
    echo "FAIL: '$component' code changed but version not bumped (still $base_ver)"
    echo "  → Update versions.json and run: ./scripts/sync-versions.sh"
    failed=1
  else
    echo "OK: '$component' version bumped $base_ver → $head_ver"
  fi
done

if [ "$failed" -eq 1 ]; then
  echo ""
  echo "Version bump required. Update versions.json for changed components,"
  echo "then run ./scripts/sync-versions.sh to propagate to all files."
  exit 1
fi

echo ""
echo "All version bumps verified."
