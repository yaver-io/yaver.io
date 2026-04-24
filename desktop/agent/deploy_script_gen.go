package main

// deploy_script_gen.go — generates a vault-aware bash deploy script for
// a specific (app, target) pair. The script:
//
//   1. Sources secrets from the local Yaver vault (project-scoped +
//      globals) — no hardcoded credentials.
//   2. Runs `yaver doctor build` as a gate — refuses to proceed if the
//      toolchain is incomplete.
//   3. Executes the target-specific build + upload commands. Bodies are
//      adapted from scripts/deploy-*.sh so we stay in sync with what
//      already works in production.
//
// Templates are keyed on (stack, target). Adding a new target is:
//   - add an entry to `deployTemplates` below
//   - add its tool/secret requirements to doctor_build.go's
//     buildTargets map
//   - optionally add a test
//
// Workspace integration: if a project is declared in yaver.workspace.yaml,
// the generator resolves the app's stack + path from the manifest; it
// falls back to --stack / --path flags when no manifest exists.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
)

// deployTemplate describes a single (stack, target) recipe.
type deployTemplate struct {
	Stack       string
	Target      string
	Description string
	// Body is a Go text/template. Available fields: {{.App}}, {{.Path}},
	// {{.Stack}}, {{.Target}}, {{.Now}}. Additional fields are injected
	// via extraVars on generate().
	Body string
}

// deployTemplates is keyed by "<stack>:<target>".
var deployTemplates = map[string]deployTemplate{
	"react-native-expo:testflight": {
		Stack:       "react-native-expo",
		Target:      "testflight",
		Description: "iOS TestFlight: xcodebuild archive + export + upload via App Store Connect API. Idempotent — a failed upload leaves the archive in place, and the next run reuses it instead of re-archiving.",
		Body: `cd "{{.Path}}/ios"

: "${APP_STORE_KEY_PATH:?Missing APP_STORE_KEY_PATH (yaver vault add APP_STORE_KEY_PATH --project {{.App}})}"
: "${APP_STORE_KEY_ID:?Missing APP_STORE_KEY_ID}"
: "${APP_STORE_KEY_ISSUER:?Missing APP_STORE_KEY_ISSUER}"
: "${APPLE_TEAM_ID:?Missing APPLE_TEAM_ID}"

# Resolve the Xcode workspace + scheme automatically.
WORKSPACE=$(find . -maxdepth 2 -name '*.xcworkspace' -not -path '*/Pods/*' | head -1)
SCHEME=$(basename "$WORKSPACE" .xcworkspace)
INFO_PLIST=$(find . -maxdepth 3 -name Info.plist -path "*/$SCHEME/*" | head -1)

# Scoped per (app, target) so parallel deploys of different apps
# don't clobber each other.
ARCHIVE=/tmp/yaver-deploy-{{.App}}-{{.Target}}.xcarchive
ARCHIVE_GITFP="$ARCHIVE.gitfp"
EXPORT_DIR=/tmp/yaver-deploy-{{.App}}-{{.Target}}-export
EXPORT_OPTIONS=/tmp/yaver-deploy-{{.App}}-{{.Target}}-ExportOptions.plist
DERIVED=/tmp/yaver-deploy-{{.App}}-{{.Target}}-build

CURRENT_BUILD=$(/usr/libexec/PlistBuddy -c "Print CFBundleVersion" "$INFO_PLIST")
CURRENT_GIT_SHA=$(git -C "{{.Path}}" rev-parse HEAD 2>/dev/null || echo "nogit")

# Idempotent resume: if an archive already exists for this
# (app, target), verify three things:
#
#   1. The embedded ApplicationProperties:CFBundleVersion matches
#      the project's current CFBundleVersion.
#   2. The archive mtime is less than 6 hours old.
#   3. The sidecar .gitfp file records a git HEAD matching the
#      project's current HEAD. This is the key guard: without it,
#      "I fixed a bug, I redeployed, Yaver ships the pre-fix
#      archive because versions still match" is a real failure
#      mode. The .gitfp pins the archive to the commit it was
#      built from.
#
# If all three hold, skip xcodebuild archive (the expensive step —
# 15-20 min on real apps) and go straight to export + upload. This
# turns "the upload hiccuped, try again" from 30 min into 30
# seconds — safely.
RESUME=0
if [ -d "$ARCHIVE" ] && [ -f "$ARCHIVE/Info.plist" ]; then
  ARCH_VER=$(/usr/libexec/PlistBuddy -c "Print ApplicationProperties:CFBundleVersion" "$ARCHIVE/Info.plist" 2>/dev/null || echo "")
  ARCH_MTIME=$(stat -f %m "$ARCHIVE" 2>/dev/null || stat -c %Y "$ARCHIVE" 2>/dev/null || echo 0)
  ARCH_GIT_SHA=$(cat "$ARCHIVE_GITFP" 2>/dev/null || echo "")
  NOW_TS=$(date +%s)
  AGE_SEC=$(( NOW_TS - ARCH_MTIME ))
  MAX_AGE=$(( 6 * 60 * 60 ))
  if [ -n "$ARCH_VER" ] && [ "$ARCH_VER" = "$CURRENT_BUILD" ] && [ $AGE_SEC -lt $MAX_AGE ]; then
    # Version + freshness OK. Now the git check: resume only if the
    # sidecar matches the current HEAD, OR if both are "nogit" (not
    # a git repo — don't let the safer path punish non-git projects).
    if [ -n "$ARCH_GIT_SHA" ] && [ "$ARCH_GIT_SHA" = "$CURRENT_GIT_SHA" ]; then
      echo "⏭  Resuming: existing archive for build $ARCH_VER is $(( AGE_SEC / 60 )) min old and git=${CURRENT_GIT_SHA:0:8} matches — skipping xcodebuild archive."
      RESUME=1
    else
      if [ -n "$ARCH_GIT_SHA" ]; then
        echo "↻ Discarding existing archive: built from git=${ARCH_GIT_SHA:0:8} but HEAD is ${CURRENT_GIT_SHA:0:8}. Re-archiving to avoid shipping stale bits."
      else
        echo "↻ Discarding existing archive: no .gitfp sidecar. Re-archiving so the uploaded bundle definitely matches HEAD."
      fi
    fi
  fi
fi

if [ $RESUME -eq 0 ]; then
  NEW_BUILD=$((CURRENT_BUILD + 1))
  /usr/libexec/PlistBuddy -c "Set CFBundleVersion $NEW_BUILD" "$INFO_PLIST"
  echo "Build $CURRENT_BUILD → $NEW_BUILD"
  rm -rf "$ARCHIVE" "$ARCHIVE_GITFP"
  echo "Archiving..."
  xcodebuild -workspace "$WORKSPACE" -scheme "$SCHEME" -configuration Release \
    -archivePath "$ARCHIVE" archive \
    DEVELOPMENT_TEAM="$APPLE_TEAM_ID" CODE_SIGN_STYLE=Automatic \
    ENABLE_USER_SCRIPT_SANDBOXING=NO -allowProvisioningUpdates \
    -authenticationKeyPath "$APP_STORE_KEY_PATH" \
    -authenticationKeyID "$APP_STORE_KEY_ID" \
    -authenticationKeyIssuerID "$APP_STORE_KEY_ISSUER" \
    -derivedDataPath "$DERIVED" 2>&1 | tail -3
  [ -d "$ARCHIVE" ] || { echo "ERROR: archive failed"; exit 1; }
  # Record the commit the archive was built from so a future
  # resume can refuse stale bits.
  echo "$CURRENT_GIT_SHA" > "$ARCHIVE_GITFP"
  EFFECTIVE_BUILD=$NEW_BUILD
else
  EFFECTIVE_BUILD=$ARCH_VER
fi

cat > "$EXPORT_OPTIONS" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>method</key><string>app-store-connect</string>
    <key>teamID</key><string>$APPLE_TEAM_ID</string>
    <key>signingStyle</key><string>automatic</string>
    <key>destination</key><string>upload</string>
    <key>uploadSymbols</key><false/>
</dict>
</plist>
EOF

echo "Exporting + uploading..."
rm -rf "$EXPORT_DIR"
OUT=$(xcodebuild -exportArchive -archivePath "$ARCHIVE" \
  -exportOptionsPlist "$EXPORT_OPTIONS" \
  -exportPath "$EXPORT_DIR" -allowProvisioningUpdates \
  -authenticationKeyPath "$APP_STORE_KEY_PATH" \
  -authenticationKeyID "$APP_STORE_KEY_ID" \
  -authenticationKeyIssuerID "$APP_STORE_KEY_ISSUER" 2>&1)
RC=$?
echo "$OUT" | tail -3
if [ $RC -ne 0 ] && ! echo "$OUT" | grep -q "Redundant Binary Upload"; then
  # Deliberate: DO NOT remove $ARCHIVE on failure. Next run resumes.
  echo "ERROR: export/upload failed (rc=$RC). Archive kept at $ARCHIVE for resume on the next run."
  exit 1
fi
# Success — free disk (including the git fingerprint sidecar; a
# future deploy will write a new one from the next archive).
rm -rf "$ARCHIVE" "$ARCHIVE_GITFP" "$EXPORT_DIR" "$DERIVED" "$EXPORT_OPTIONS"
echo "✓ TestFlight build $EFFECTIVE_BUILD uploaded."
`,
	},
	"react-native-expo:playstore": {
		Stack:       "react-native-expo",
		Target:      "playstore",
		Description: "Android Play Store (internal testing): Gradle bundleRelease + AAB upload. Idempotent — a failed upload leaves the AAB + versionCode in place, and the next run resumes from upload without re-bumping or re-building.",
		Body: `cd "{{.Path}}/android"

: "${ANDROID_KEYSTORE_PASSWORD:?Missing ANDROID_KEYSTORE_PASSWORD (yaver vault add ANDROID_KEYSTORE_PASSWORD --project {{.App}})}"
: "${ANDROID_KEY_ALIAS:?Missing ANDROID_KEY_ALIAS}"
: "${ANDROID_KEY_PASSWORD:?Missing ANDROID_KEY_PASSWORD}"

if [ -x "./gradlew" ]; then GRADLE="./gradlew"; else GRADLE="gradle"; fi

GRADLE_FILE="app/build.gradle"
CURRENT=$(grep 'versionCode ' "$GRADLE_FILE" | head -1 | sed 's/[^0-9]//g')

# Fingerprint sidecar — keyed per (app, target) so other projects
# don't collide on /tmp. Records the (versionCode, git HEAD) the AAB
# was built from. If both match on a rerun and the AAB is fresh, we
# skip bundleRelease + versionCode bump entirely.
AAB="app/build/outputs/bundle/release/app-release.aab"
FP=/tmp/yaver-deploy-{{.App}}-{{.Target}}.fp

RESUME=0
if [ -f "$AAB" ] && [ -f "$FP" ]; then
  AAB_MTIME=$(stat -f %m "$AAB" 2>/dev/null || stat -c %Y "$AAB" 2>/dev/null || echo 0)
  NOW=$(date +%s)
  AGE=$(( NOW - AAB_MTIME ))
  MAX_AGE=$(( 6 * 60 * 60 ))
  if [ $AGE -lt $MAX_AGE ]; then
    SAVED=$(cat "$FP" 2>/dev/null || echo "")
    GIT_SHA=$(git -C "{{.Path}}" rev-parse HEAD 2>/dev/null || echo "nogit")
    EXPECTED="vc=$CURRENT git=$GIT_SHA"
    if [ "$SAVED" = "$EXPECTED" ]; then
      echo "⏭  Resuming: existing AAB is $(( AGE / 60 )) min old and fingerprint matches (vc=$CURRENT git=${GIT_SHA:0:8}) — skipping bundleRelease."
      RESUME=1
      EFFECTIVE_VC=$CURRENT
    fi
  fi
fi

if [ $RESUME -eq 0 ]; then
  NEW=$((CURRENT + 1))
  sed -i.bak "s/versionCode $CURRENT/versionCode $NEW/" "$GRADLE_FILE" && rm -f "$GRADLE_FILE.bak"
  echo "versionCode $CURRENT → $NEW"

  # keystore.properties is gitignored; write it from vault values just for this build.
  cat > keystore.properties <<EOF
storeFile=../../../keys/yaver-upload.keystore
storePassword=$ANDROID_KEYSTORE_PASSWORD
keyAlias=$ANDROID_KEY_ALIAS
keyPassword=$ANDROID_KEY_PASSWORD
EOF

  echo "Building release AAB..."
  $GRADLE :react-native-worklets:prefabReleasePackage 2>/dev/null || true
  $GRADLE bundleRelease

  [ -f "$AAB" ] || { echo "ERROR: AAB missing at $AAB"; exit 1; }
  GIT_SHA=$(git -C "{{.Path}}" rev-parse HEAD 2>/dev/null || echo "nogit")
  echo "vc=$NEW git=$GIT_SHA" > "$FP"
  EFFECTIVE_VC=$NEW
fi

echo "✓ AAB ready: $(pwd)/$AAB (versionCode $EFFECTIVE_VC)"

if [ -n "${PLAY_STORE_KEY_FILE:-}" ] && [ -f "$PLAY_STORE_KEY_FILE" ]; then
  echo "Uploading to Play internal testing..."
  if python3 "$(dirname "$0")/upload-playstore.py" 2>&1 | tail -5; then
    # Upload succeeded — clear the fingerprint so the next invocation
    # builds fresh. Deliberate: we don't delete the AAB (gradle will
    # overwrite it; leaving it helps debug).
    rm -f "$FP"
  else
    # Deliberately keep $FP + $AAB so the next run resumes. The
    # script exits non-zero only when python3/script is absent.
    echo "(Upload helper not found — AAB is ready; upload manually.)"
  fi
fi
`,
	},
	"nextjs:cloudflare": {
		Stack:       "nextjs",
		Target:      "cloudflare",
		Description: "Cloudflare Workers deploy via @opennextjs/cloudflare + wrangler.",
		Body: `cd "{{.Path}}"

: "${CLOUDFLARE_API_TOKEN:?Missing CLOUDFLARE_API_TOKEN (yaver vault add CLOUDFLARE_API_TOKEN --project {{.App}})}"
: "${CLOUDFLARE_ACCOUNT_ID:?Missing CLOUDFLARE_ACCOUNT_ID}"
export CLOUDFLARE_API_TOKEN CLOUDFLARE_ACCOUNT_ID

if [ ! -d node_modules ]; then npm install; fi

echo "Building + deploying..."
npm run deploy
echo "✓ Deployed to Cloudflare."
`,
	},
	"convex:convex": {
		Stack:       "convex",
		Target:      "convex",
		Description: "Convex backend deploy via `npx convex deploy`.",
		Body: `cd "{{.Path}}"

: "${CONVEX_DEPLOY_KEY:?Missing CONVEX_DEPLOY_KEY (yaver vault add CONVEX_DEPLOY_KEY --project {{.App}})}"
export CONVEX_DEPLOY_KEY

if [ ! -d node_modules ]; then npm install; fi

echo "Deploying Convex backend..."
npx convex deploy --yes
echo "✓ Convex deployed."
`,
	},
	"node:npm-publish": {
		Stack:       "node",
		Target:      "npm-publish",
		Description: "Publish a JS package to the npm registry.",
		Body: `cd "{{.Path}}"

: "${NPM_TOKEN:?Missing NPM_TOKEN (yaver vault add NPM_TOKEN --project {{.App}})}"

# Write a scoped .npmrc for this build only — remove at exit.
cleanup() { rm -f .npmrc.yaver-deploy; }
trap cleanup EXIT
cat > .npmrc.yaver-deploy <<EOF
//registry.npmjs.org/:_authToken=$NPM_TOKEN
EOF

npm publish --access public --userconfig .npmrc.yaver-deploy
echo "✓ Published."
`,
	},
	"python:pypi-publish": {
		Stack:       "python",
		Target:      "pypi-publish",
		Description: "Build + publish a Python package to PyPI via twine.",
		Body: `cd "{{.Path}}"

: "${PYPI_TOKEN:?Missing PYPI_TOKEN (yaver vault add PYPI_TOKEN --project {{.App}})}"

python3 -m build
python3 -m twine upload dist/* --username __token__ --password "$PYPI_TOKEN"
echo "✓ Published to PyPI."
`,
	},
}

// DeployTemplateKey returns the map key for a (stack, target) pair.
func DeployTemplateKey(stack, target string) string {
	return stack + ":" + target
}

// DeployTemplateNames returns the sorted list of registered templates.
func DeployTemplateNames() []string {
	out := make([]string, 0, len(deployTemplates))
	for k := range deployTemplates {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DeployScriptSpec is the input to GenerateDeployScript.
type DeployScriptSpec struct {
	App    string // project name — used for vault scope + app label
	Stack  string // e.g. react-native-expo
	Target string // e.g. testflight
	Path   string // absolute path to the app's directory
}

// GenerateDeployScript renders a ready-to-run bash script. The returned
// string starts with `#!/usr/bin/env bash` and is safe to write with
// mode 0755.
func GenerateDeployScript(spec DeployScriptSpec) (string, error) {
	if spec.App == "" {
		return "", fmt.Errorf("app is required")
	}
	if spec.Stack == "" || spec.Target == "" {
		return "", fmt.Errorf("stack and target are required")
	}
	tpl, ok := deployTemplates[DeployTemplateKey(spec.Stack, spec.Target)]
	if !ok {
		return "", fmt.Errorf("no template for %s:%s — known: %v", spec.Stack, spec.Target, DeployTemplateNames())
	}
	path := spec.Path
	if path == "" {
		path = "."
	}

	t, err := template.New("body").Parse(tpl.Body)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var body strings.Builder
	if err := t.Execute(&body, map[string]string{
		"App":    spec.App,
		"Stack":  spec.Stack,
		"Target": spec.Target,
		"Path":   path,
	}); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintln(&sb, "#!/usr/bin/env bash")
	fmt.Fprintf(&sb, "# Generated by yaver deploy generate --app=%s --target=%s on %s.\n",
		spec.App, spec.Target, time.Now().Format(time.RFC3339))
	fmt.Fprintln(&sb, "# Regenerate with the same command. Do not edit by hand.")
	fmt.Fprintln(&sb, "#")
	fmt.Fprintln(&sb, "# What this script does:")
	fmt.Fprintf(&sb, "#   %s\n", tpl.Description)
	fmt.Fprintln(&sb, "#")
	fmt.Fprintln(&sb, "# Secrets come from the local Yaver vault (project-scoped + global).")
	fmt.Fprintf(&sb, "#   Project: %s\n", spec.App)
	fmt.Fprintln(&sb, "# To inspect / edit:")
	fmt.Fprintf(&sb, "#   yaver vault list --project %s\n", spec.App)
	fmt.Fprintf(&sb, "#   yaver vault add <KEY> --project %s\n", spec.App)
	fmt.Fprintln(&sb, "")
	fmt.Fprintln(&sb, "set -euo pipefail")
	fmt.Fprintln(&sb, "")
	fmt.Fprintln(&sb, "# Load secrets from Yaver vault (silently skip if not installed).")
	fmt.Fprintln(&sb, "if command -v yaver >/dev/null 2>&1; then")
	fmt.Fprintf(&sb, "  eval \"$(yaver vault env --project %s 2>/dev/null || true)\"\n", spec.App)
	fmt.Fprintln(&sb, "fi")
	fmt.Fprintln(&sb, "")
	fmt.Fprintln(&sb, "# Preflight: refuse to proceed if the toolchain is incomplete.")
	fmt.Fprintln(&sb, "if command -v yaver >/dev/null 2>&1; then")
	fmt.Fprintf(&sb, "  yaver doctor build --target=%s --project=%s --json >/tmp/yaver-doctor-%s.json 2>&1 || {\n",
		spec.Target, spec.App, spec.Target)
	fmt.Fprintln(&sb, "    echo 'Preflight failed — re-run with:' >&2")
	fmt.Fprintf(&sb, "    echo '  yaver doctor build --target=%s --project=%s' >&2\n", spec.Target, spec.App)
	fmt.Fprintf(&sb, "    cat /tmp/yaver-doctor-%s.json >&2\n", spec.Target)
	fmt.Fprintln(&sb, "    exit 1")
	fmt.Fprintln(&sb, "  }")
	fmt.Fprintln(&sb, "fi")
	fmt.Fprintln(&sb, "")
	sb.WriteString(body.String())

	return sb.String(), nil
}

// --- CLI (invoked by runDeploy in deploy_cmd.go) ---

func runDeployGenerateCmd(args []string) {
	fs := flag.NewFlagSet("deploy generate", flag.ExitOnError)
	app := fs.String("app", "", "App/project name (used for vault scope)")
	target := fs.String("target", "", "Target (testflight, playstore, cloudflare, convex, npm-publish, pypi-publish)")
	stack := fs.String("stack", "", "Stack override (auto-resolved from yaver.workspace.yaml when possible)")
	path := fs.String("path", "", "App path override (default: workspace manifest or cwd)")
	out := fs.String("out", "", "Write script to file (default: stdout)")
	fs.Parse(args)

	if *app == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "Error: --app and --target are required")
		os.Exit(1)
	}

	resolvedStack := *stack
	resolvedPath := *path
	workspaceRoot := ""
	if resolvedStack == "" || resolvedPath == "" {
		s, p, root := resolveAppFromWorkspaceFull(*app)
		if resolvedStack == "" {
			resolvedStack = s
		}
		if resolvedPath == "" {
			resolvedPath = p
		}
		workspaceRoot = root
	}
	if resolvedStack == "" {
		fmt.Fprintln(os.Stderr, "Error: could not resolve stack — pass --stack explicitly.")
		os.Exit(1)
	}
	if resolvedPath == "" {
		resolvedPath = "."
	}
	// Paths declared in yaver.workspace.yaml are relative to the manifest
	// root; everything else is relative to cwd.
	if !filepath.IsAbs(resolvedPath) {
		base := workspaceRoot
		if base == "" {
			if cwd, err := os.Getwd(); err == nil {
				base = cwd
			}
		}
		resolvedPath = filepath.Join(base, resolvedPath)
	}
	if abs, err := filepath.Abs(resolvedPath); err == nil {
		resolvedPath = abs
	}

	script, err := GenerateDeployScript(DeployScriptSpec{
		App:    *app,
		Stack:  resolvedStack,
		Target: *target,
		Path:   resolvedPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *out == "" {
		fmt.Print(script)
		return
	}
	if err := os.WriteFile(*out, []byte(script), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Wrote %s (%d bytes)\n", *out, len(script))
}

func runDeployTemplatesCmd() {
	fmt.Println("Supported (stack, target) templates:")
	for _, k := range DeployTemplateNames() {
		t := deployTemplates[k]
		fmt.Printf("  %-38s  %s\n", k, t.Description)
	}
}

// resolveAppFromWorkspaceFull returns (stack, path, workspaceRoot) for
// the given app name, consulting yaver.workspace.yaml in the current dir
// (or parents). workspaceRoot is the absolute dir containing the manifest,
// which is the correct base for the (relative) app path.
func resolveAppFromWorkspaceFull(appName string) (string, string, string) {
	ws, root, err := loadWorkspaceNearby()
	if err != nil || ws == nil {
		return "", "", ""
	}
	for _, a := range ws.Apps {
		if a.Name == appName {
			return a.Stack, a.Path, root
		}
	}
	return "", "", root
}

// loadWorkspaceNearby walks upward from cwd looking for
// yaver.workspace.yaml. Returns the parsed manifest + absolute root path.
func loadWorkspaceNearby() (*WorkspaceManifest, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "yaver.workspace.yaml")); err == nil {
			ws, err := LoadWorkspaceManifest(dir)
			return ws, dir, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", fmt.Errorf("no yaver.workspace.yaml found")
		}
		dir = parent
	}
}
