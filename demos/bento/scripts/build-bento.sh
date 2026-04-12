#!/usr/bin/env bash
# build-bento.sh — dispatch every task from BENTO_BUILD_QUEUE.md to a running
# `yaver serve` and wait for each to complete before queuing the next. This
# IS the "build Bento with only Yaver" pipeline — no local file-writing,
# no manual Claude Code prompts. Just queue → observe → auto-advance.
#
# Usage:
#   cd demos/bento
#   ./scripts/build-bento.sh [--dry-run] [--start-at T2.1]
#
# Prereqs:
#   - `yaver serve` running on localhost:18080
#   - A runner configured (`yaver set-runner claude` or similar) with creds
#   - jq, curl

set -euo pipefail

AGENT="${AGENT:-http://localhost:18080}"
CFG="${HOME}/.yaver/config.json"
TOKEN="$(jq -r '.auth_token' "$CFG")"
WORK_DIR="$(cd "$(dirname "$0")/.." && pwd)"

DRY_RUN=0
START_AT=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --start-at) shift; START_AT="$1" ;;
    --start-at=*) START_AT="${arg#*=}" ;;
  esac
done

auth_headers=(-H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json")

create_task() {
  local title="$1" desc="$2"
  if [[ $DRY_RUN -eq 1 ]]; then
    echo "[dry-run] $title"
    return
  fi
  local payload
  payload=$(jq -n \
    --arg t "$title" --arg d "$desc" --arg w "$WORK_DIR" \
    '{title:$t, description:$d, workDir:$w, source:"bento-queue"}')
  local id
  id=$(curl -sS -X POST "$AGENT/tasks" "${auth_headers[@]}" -d "$payload" | jq -r .taskId)
  if [[ -z "$id" || "$id" == "null" ]]; then
    echo "  ✗ task creation failed" >&2
    return 1
  fi
  echo "  → task $id queued"
  # Poll until status leaves queued/running
  while :; do
    sleep 5
    local status
    status=$(curl -sS "$AGENT/tasks/$id" "${auth_headers[@]}" | jq -r .status)
    case "$status" in
      completed) echo "  ✓ done"; return 0 ;;
      failed|stopped) echo "  ✗ task ended: $status"; return 1 ;;
      *) printf "." ;;
    esac
  done
}

# Task catalog. Each entry: id|title|description. Keep descriptions trimmed —
# BENTO_BUILD_QUEUE.md is the readable long-form spec.
read -r -d '' TASKS <<'EOF' || true
T1.1|Bento: recipes schema|In backend/convex/schema.ts add a recipes table: title (string), cookTime (number, minutes), rating (0-5), category enum (Quick/Healthy/Comfort/Dessert), imageUrl (optional string), ingredients (array of {name, amount, price?}), steps (array of {text, duration?}). Index by category. Keep users+sessions as-is.
T1.2|Bento: seed 8 recipes with intentional bugs|In backend/convex/seed.ts add seedRecipes mutation per BENTO_BUILD_QUEUE.md T1.2. Include the 3 intentional bugs (null prices on Avocado Toast / Overnight Oats / Greek Salad; imageUrl null on Overnight Oats; one step with no duration). Expose recipes.list + recipes.get queries.
T1.3|Bento: favorites + grocery tables|Add favorites (userId, recipeId) and groceryItems (userId, recipeId, ingredientName, amount, price?, checked) tables. Expose favorites.listMine / favorites.toggle and grocery.listMine / grocery.addRecipe / grocery.toggleItem / grocery.clear.
T2.1|Bento: tab navigator|Replace App.tsx splash with expo-router bottom tabs: Home, Search, Favorites, Grocery, Profile. Use brand palette (primary #F97316, accent #059669, bg #FAFAF9). Tab stubs fine for now.
T2.2|Bento: NativeWind|Add NativeWind + Tailwind. tailwind.config.js uses Bento palette as theme tokens.
T3.1|Bento: Home screen|Build Home tab: header (Bento title + search + avatar), category pills, 2-col grid of RecipeCard reading from recipes.list. Tap card → RecipeDetail stack.
T3.2|Bento: RecipeDetail (leave imageUrl fallback off)|RecipeDetail screen with hero image, rating/cookTime/servings row, IngredientRow list, steps list, Add to Grocery + Start Cooking buttons. DO NOT add imageUrl fallback — the video will fix this on camera.
T3.3|Bento: CookMode with intentional null-duration bug|Build CookTimer on CookMode.tsx. Around line 112, use step.duration directly without fallback so undefined-duration steps crash. Do NOT fix.
T3.4|Bento: Grocery tab with intentional null-price crash|Build GroceryTotal.tsx with ingredients.reduce((sum,i)=>sum+i.price.toFixed(2),0). This crashes on null price. Do NOT fix.
T3.5|Bento: Favorites + Profile|Favorites: saved recipes grid + swipe-to-remove. Profile: avatar, name, email, meals-cooked stat, settings row, sign out.
T4.1|Bento: Better Auth|Wire Better Auth (Apple+Google+email) against the existing Convex auth tables. Sign-in screen when no session.
T4.2|Bento: Feedback SDK|Install yaver-feedback-react-native, drop FloatingButton behind __DEV__, start BlackBox + wrapConsole at startup.
T4.3|Bento: yaver-cli init|Run npx yaver-cli init in apps/mobile. Fix any compatibility warnings.
EOF

echo "Bento build queue — 13 tasks"
echo "work-dir: $WORK_DIR"
if [[ -n "$START_AT" ]]; then echo "starting at: $START_AT"; fi
echo

skipping=0
[[ -n "$START_AT" ]] && skipping=1

while IFS='|' read -r id title desc; do
  [[ -z "$id" ]] && continue
  if [[ $skipping -eq 1 ]]; then
    if [[ "$id" == "$START_AT" ]]; then skipping=0; else continue; fi
  fi
  echo "── $id  $title"
  if ! create_task "$title" "$desc"; then
    echo
    echo "Pipeline halted at $id. Fix/retry with: $0 --start-at $id"
    exit 1
  fi
  echo
done <<< "$TASKS"

echo "All Bento tasks completed. Fire up \`cd apps/mobile && npx expo start\` to see it."
