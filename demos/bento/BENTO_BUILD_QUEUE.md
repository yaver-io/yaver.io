# Bento — Build Queue

Dogfood doc. Bento's skeleton was scaffolded by `yaver new --quick` (see
`/tmp/bento-answers.json` in the capture commit). Everything below is queued
work for Yaver's own task runner — each entry is a one-shot prompt that a
Claude Code / Aider / Codex session (via `yaver serve`) can pick up and
complete independently.

**How to consume this file:**

```bash
# One task at a time from the mobile Yaver app, pointing Chat at demos/bento/
# Or in bulk from the CLI:
curl -s -X POST http://localhost:18080/tasks \
  -H "Authorization: Bearer $(jq -r .auth_token ~/.yaver/config.json)" \
  -H "Content-Type: application/json" \
  -d @- <<'JSON'
{
  "title": "Bento: recipes schema",
  "description": "...",
  "workDir": "/abs/path/to/demos/bento",
  "source": "bento-queue"
}
JSON
```

Or use `scripts/build-bento.sh` (below) once it's committed.

Tasks are ordered so each later one can assume earlier ones shipped.

## Tier 1 — Data

### T1.1 — Recipes schema
> In `backend/convex/schema.ts` add a `recipes` table with fields: `title`
> (string), `cookTime` (number, minutes), `rating` (number 0–5), `category`
> (string from "Quick" | "Healthy" | "Comfort" | "Dessert"), `imageUrl`
> (optional string), `ingredients` (array of `{name, amount, price?}` where
> price is optional), `steps` (array of `{text, duration?}` where duration
> is optional seconds). Index by `category`. Keep users + sessions tables
> as-is.

### T1.2 — Seed data (3 intentional bugs)
> In `backend/convex/seed.ts` write a `seedRecipes` mutation that inserts 8
> recipes per the exact list in the Bento video spec. Include the 3
> intentional bugs: "Avocado Toast" has a null-price ingredient, "Overnight
> Oats" has `imageUrl: null` plus a null-price ingredient plus a step with
> no duration, "Greek Salad" has a null-price olive oil. All other recipes
> must be valid. Add a client-exposed query `recipes.list` and
> `recipes.get(id)`.

### T1.3 — Favorites + grocery
> Add `favorites` (userId, recipeId) and `groceryItems` (userId, recipeId,
> ingredientName, amount, price?, checked) tables to the schema. Queries:
> `favorites.listMine`, `grocery.listMine`. Mutations: `favorites.toggle`,
> `grocery.addRecipe`, `grocery.toggleItem`, `grocery.clear`.

## Tier 2 — Navigation shell

### T2.1 — Tab navigator
> Replace the splash in `apps/mobile/App.tsx` with an Expo Router (or
> React Navigation bottom-tabs) setup for: Home, Search, Favorites,
> Grocery, Profile. Use the brand palette (primary #F97316, accent
> #059669, bg #FAFAF9). Each tab screen can stay a stub for now — wire up
> icons and the active state only.

### T2.2 — NativeWind
> Add NativeWind + Tailwind to the mobile app. Configure
> `tailwind.config.js` with the Bento palette as theme extension tokens.

## Tier 3 — Screens (visual)

### T3.1 — Home (recipe feed)
> Build the Home tab: header with "Bento" title + search icon + avatar,
> category pills (All / Quick / Healthy / Comfort / Dessert), then a
> vertical scroll grid (2 cols) of `RecipeCard`s. Each card: hero image
> (placeholder if null), title, cook time, rating. Tapping opens
> `RecipeDetail` via the stack. Reads from `recipes.list`.

### T3.2 — RecipeDetail
> Build the recipe detail screen: hero image (use a placehold.co fallback
> for null imageUrl), title/rating/cookTime/servings row, ingredients
> list via `IngredientRow`, steps list, `Add to Grocery List` button,
> `Start Cooking` button that navigates to `CookMode`. **Intentional
> bug**: do NOT add a fallback for `recipe.imageUrl` — let the Image
> component receive null. The Auto-Fix video fixes this.

### T3.3 — CookMode (with BUG #2)
> Build `CookTimer` that displays a countdown for the current step and
> advances with Next/Previous. **Intentional bug on line ~112** of
> `CookTimer.tsx`: use `step.duration` directly without a fallback, so
> steps with undefined duration crash. The Auto-Fix video fixes this to
> `step.duration ?? 300`.

### T3.4 — Grocery (with BUG #1)
> Build the grocery tab: items grouped by recipe with checkboxes, total
> at bottom. **Intentional bug on line ~47** of `GroceryTotal.tsx`:
> `ingredients.reduce((sum, i) => sum + i.price.toFixed(2), 0)` — will
> crash when any price is null. The Feedback SDK video fixes this to
> `i.price?.toFixed(2) ?? '0.00'`.

### T3.5 — Favorites + Profile
> Favorites: grid of saved recipes, swipe-to-remove. Profile: avatar,
> name, email, "Meals cooked this week" stat (static 0 ok for now),
> settings row, sign out.

## Tier 4 — Auth + integration

### T4.1 — Better Auth wiring
> Wire Better Auth with Apple + Google + email. The Convex auth tables
> already exist. Add a sign-in screen shown when the user has no session.

### T4.2 — Yaver Feedback SDK
> `npm install yaver-feedback-react-native`. Drop a `<FloatingButton />`
> gated behind `__DEV__`. Start `BlackBox` and `wrapConsole()` at app
> startup so the Video 2 shake-to-report flow works.

### T4.3 — Yaver Push-to-Device readiness
> Run `npx yaver-cli init` inside `apps/mobile/`. Verify RN version +
> Hermes BC + pre-installed native modules match the host. Fix any
> compatibility warnings.

## Notes

- Tasks T3.2, T3.3, T3.4 must ship the bugs intact — do not instruct the
  agent to also fix them. The videos show Yaver discovering and repairing
  them live.
- Keep task prompts under 120 words each so the runner has context room.
- `workDir` must be `demos/bento/` (absolute path) so tasks execute
  against this project and gap-4 auto-reload fires against the right
  dev server.
- After each tier, snapshot: `yaver services snapshot` from within the
  project dir.
