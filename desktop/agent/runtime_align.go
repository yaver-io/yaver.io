package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RuntimeAlignmentReport summarises an attempt to align a guest project's
// React / React-Native / Expo versions to a host runtime family that the
// Yaver mobile binary actually has compiled in. The agent runs alignment
// before bundling so that the resulting Hermes bytecode is ABI-compatible
// with the device — this is what previously required the user to manually
// edit package.json on the test box and re-run npm install.
type RuntimeAlignmentReport struct {
	Attempted        bool              `json:"attempted"`
	Applied          bool              `json:"applied"`
	SkippedReason    string            `json:"skippedReason,omitempty"`
	TargetFamilyID   string            `json:"targetFamilyId,omitempty"`
	TargetFamily     *RuntimeFamily    `json:"targetFamily,omitempty"`
	OverridesBefore  map[string]string `json:"overridesBefore,omitempty"`
	OverridesAfter   map[string]string `json:"overridesAfter,omitempty"`
	NPMInstallRan    bool              `json:"npmInstallRan"`
	NPMInstallMs     int64             `json:"npmInstallMs,omitempty"`
	WorkspaceRoot    string            `json:"workspaceRoot,omitempty"`
	WorkspaceMember  string            `json:"workspaceMember,omitempty"`
	OverridesWritten string            `json:"overridesWritten,omitempty"`
	Notes            []string          `json:"notes,omitempty"`
	Error            string            `json:"error,omitempty"`
}

// pickCompiledInRuntimeFamily walks a list of host-advertised families and
// returns the closest one that is actually compiled into the mobile binary
// (compiledIn=true). Falls back to the closest by Levenshtein-on-versions
// when nothing is preferred-by-package-name. Returns nil + reason when no
// compiledIn family is on offer at all.
func pickCompiledInRuntimeFamily(guest RuntimeFingerprint, families []RuntimeFamily) (*RuntimeFamily, string) {
	if len(families) == 0 {
		return nil, "no host runtime families advertised"
	}
	var compiled []RuntimeFamily
	for _, f := range families {
		if f.CompiledIn {
			compiled = append(compiled, f)
		}
	}
	if len(compiled) == 0 {
		return nil, "host advertises only compiledIn=false families; nothing safe to bundle for"
	}
	sel := SelectRuntimeFamily(guest, compiled)
	chosen := sel.Selected
	return &chosen, ""
}

// alignProjectRuntimeIfNeeded looks at the project's installed React /
// React-Native / Expo versions and, when they differ from the chosen host
// family by anything more than nil, writes a `overrides` block into the
// project's package.json and runs `npm install --legacy-peer-deps` so the
// bundle that the agent is about to compile uses the host's exact ABI.
//
// Idempotent: if the overrides already match, npm install runs but is fast
// (npm checks the lockfile and exits in a few seconds when nothing
// changes). On follow-up builds with the same family, the overrides stay
// in place — no churn.
//
// Skipped when:
//   - autoAlign is false (caller opted out)
//   - the chosen family is nil (host had nothing compiledIn)
//   - the project's versions already match family within (major, minor, patch)
//   - package.json is unparseable (we don't want to corrupt user state)
func alignProjectRuntimeIfNeeded(ctx context.Context, workDir string, family *RuntimeFamily, guest RuntimeFingerprint, autoAlign bool) RuntimeAlignmentReport {
	report := RuntimeAlignmentReport{Attempted: false}
	if !autoAlign {
		report.SkippedReason = "autoAlignRuntime=false"
		return report
	}
	if family == nil {
		report.SkippedReason = "no compiledIn host family available"
		return report
	}

	wantReact := strings.TrimSpace(family.React)
	wantRN := strings.TrimSpace(family.ReactNative)
	wantExpo := strings.TrimSpace(family.ExpoVersion)

	// Don't try to pin to "" — we'd silently allow anything. If the family
	// declared nothing, there is nothing to enforce.
	if wantReact == "" && wantRN == "" && wantExpo == "" {
		report.SkippedReason = "host family did not declare React/RN/Expo versions"
		return report
	}

	if runtimeVersionEquals(guest.ReactVersion, wantReact) &&
		runtimeVersionEquals(guest.ReactNativeVersion, wantRN) &&
		runtimeVersionEquals(guest.ExpoVersion, wantExpo) {
		report.SkippedReason = "project already matches host family — no align needed"
		report.TargetFamilyID = family.ID
		report.TargetFamily = family
		return report
	}

	report.Attempted = true
	report.TargetFamilyID = family.ID
	report.TargetFamily = family

	// Detect npm workspace. carrotbet ships /root/carrotbet/package.json with
	// `"workspaces": ["mobile", ...]` and the guest project lives in
	// /root/carrotbet/mobile. npm only honours `overrides` declared in the
	// **workspace root** package.json — overrides written into a child are
	// silently ignored, which is exactly why the prior align run on carrotbet
	// produced `mobile/package.json` with `dependencies.react-native:0.81.6`
	// AND `overrides.react-native:0.81.5`, then npm install picked 0.81.6.
	wsRoot, wsMember := detectNpmWorkspaceRoot(workDir)
	report.WorkspaceRoot = wsRoot
	report.WorkspaceMember = wsMember

	pkgPath := filepath.Join(workDir, "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		report.Error = fmt.Sprintf("read package.json: %v", err)
		return report
	}
	var pkgObj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &pkgObj); err != nil {
		report.Error = fmt.Sprintf("parse package.json: %v", err)
		return report
	}

	// Pick the package.json that actually owns `overrides` for npm. In a
	// workspace this is the root; otherwise the project itself.
	overridesPath := pkgPath
	overridesObj := pkgObj
	if wsRoot != "" && wsRoot != workDir {
		overridesPath = filepath.Join(wsRoot, "package.json")
		rawRoot, rerr := os.ReadFile(overridesPath)
		if rerr != nil {
			report.Error = fmt.Sprintf("read workspace root package.json: %v", rerr)
			return report
		}
		var rootObj map[string]json.RawMessage
		if jerr := json.Unmarshal(rawRoot, &rootObj); jerr != nil {
			report.Error = fmt.Sprintf("parse workspace root package.json: %v", jerr)
			return report
		}
		overridesObj = rootObj
	}
	report.OverridesWritten = overridesPath

	currentOverrides := map[string]string{}
	if rawOv, ok := overridesObj["overrides"]; ok {
		var existing map[string]json.RawMessage
		if err := json.Unmarshal(rawOv, &existing); err == nil {
			for k, v := range existing {
				var s string
				if err := json.Unmarshal(v, &s); err == nil {
					currentOverrides[k] = s
				}
			}
		}
	}
	report.OverridesBefore = cloneStringMap(currentOverrides)

	desired := map[string]string{}
	if wantReact != "" {
		desired["react"] = wantReact
		desired["react-dom"] = wantReact
	}
	if wantRN != "" {
		desired["react-native"] = wantRN
	}
	if wantExpo != "" {
		desired["expo"] = wantExpo
	}

	// ── 1. Patch overrides (in the workspace root when present) ──
	// Overrides handle TRANSITIVE deps that pull a different React/RN/Expo
	// (e.g. some plugin that peer-deps on a newer React). They do NOT
	// downgrade a top-level direct dep — npm refuses to override what the
	// project already lists in `dependencies` unless we use the `$pkg`
	// reference form, which doesn't help us pin to a specific version.
	// So this block alone is not sufficient; the dependencies-block patch
	// below is the load-bearing change for the React 19.2.5 → 19.1.0 case.
	merged := cloneStringMap(currentOverrides)
	overridesChanged := false
	for k, v := range desired {
		if existing, ok := merged[k]; ok && existing == v {
			continue
		}
		merged[k] = v
		overridesChanged = true
	}

	// ── 2. Patch dependencies (and devDependencies) in workDir/package.json ──
	// Only rewrite keys that ALREADY exist in the project so we don't add
	// react-dom / expo to projects that genuinely don't depend on them.
	// This is what npm actually honours when resolving the install plan
	// for a top-level package.
	depsRewrites := map[string]string{}
	depKindsTouched := map[string]map[string]string{}
	for _, kind := range []string{"dependencies", "devDependencies"} {
		raw, ok := pkgObj[kind]
		if !ok {
			continue
		}
		var existing map[string]json.RawMessage
		if err := json.Unmarshal(raw, &existing); err != nil {
			continue
		}
		writes := map[string]string{}
		for name, want := range desired {
			rawCur, present := existing[name]
			if !present {
				continue
			}
			var cur string
			_ = json.Unmarshal(rawCur, &cur)
			if strings.TrimSpace(cur) == want {
				continue
			}
			b, _ := json.Marshal(want)
			existing[name] = b
			writes[name] = want
			depsRewrites[name] = want
		}
		if len(writes) > 0 {
			ordered := orderedRawMap(existing)
			out, err := json.MarshalIndent(ordered, "  ", "  ")
			if err != nil {
				report.Error = fmt.Sprintf("marshal %s: %v", kind, err)
				return report
			}
			pkgObj[kind] = out
			depKindsTouched[kind] = writes
		}
	}

	if overridesChanged {
		var ovValue map[string]json.RawMessage
		if rawOv, ok := overridesObj["overrides"]; ok {
			_ = json.Unmarshal(rawOv, &ovValue)
		}
		if ovValue == nil {
			ovValue = map[string]json.RawMessage{}
		}
		for k, v := range merged {
			b, _ := json.Marshal(v)
			ovValue[k] = b
		}
		ovBytes, err := json.MarshalIndent(orderedRawMap(ovValue), "  ", "  ")
		if err != nil {
			report.Error = fmt.Sprintf("marshal overrides: %v", err)
			return report
		}
		overridesObj["overrides"] = ovBytes
	}
	report.OverridesAfter = merged

	// ── 2.5. Sync root dependencies/devDependencies to match overrides ──
	// npm 9+ rejects an install with EOVERRIDE when an `overrides.<pkg>` is
	// pinned to an exact version while the SAME pkg appears as a direct dep
	// at the same level with a non-identical specifier (range OR a different
	// exact version). When that fires, npm silently falls back to a broken
	// install — carrotbet hit this with `dependencies.expo: "~54.0.33"` +
	// `overrides.expo: "54.0.33"`, which let `apps/web`'s `react: ^18.3.1`
	// hoist React 18 into the workspace root next to mobile's React 19,
	// and `expo export -p web` shipped a bundle that crashed at
	// `null is not an object (evaluating 'H.H.useState')`.
	//
	// Whenever we promote an override, also rewrite the same key in the
	// override-owner's deps/devDeps to the identical version so npm cannot
	// detect a conflict. We only touch keys that ALREADY exist in those
	// blocks — never add a dep the project didn't ask for.
	rootDepsTouched := map[string]map[string]string{}
	if overridesChanged && len(merged) > 0 {
		for _, kind := range []string{"dependencies", "devDependencies"} {
			rawKind, ok := overridesObj[kind]
			if !ok {
				continue
			}
			var existing map[string]json.RawMessage
			if err := json.Unmarshal(rawKind, &existing); err != nil {
				continue
			}
			writes := map[string]string{}
			for name, want := range merged {
				rawCur, present := existing[name]
				if !present {
					continue
				}
				var cur string
				_ = json.Unmarshal(rawCur, &cur)
				if strings.TrimSpace(cur) == want {
					continue
				}
				b, _ := json.Marshal(want)
				existing[name] = b
				writes[name] = want
			}
			if len(writes) > 0 {
				out, err := json.MarshalIndent(orderedRawMap(existing), "  ", "  ")
				if err != nil {
					report.Error = fmt.Sprintf("marshal root %s: %v", kind, err)
					return report
				}
				overridesObj[kind] = out
				rootDepsTouched[kind] = writes
			}
		}
	}

	// ── 3. Strip stale overrides from the workspace child ──
	// npm ignores overrides outside of the root, so leaving them in mobile/
	// just confuses humans diffing the file. If we just promoted them to the
	// root, drop the child copy in the same write pass.
	childOverridesStripped := false
	if wsRoot != "" && wsRoot != workDir {
		if _, ok := pkgObj["overrides"]; ok {
			delete(pkgObj, "overrides")
			childOverridesStripped = true
		}
	}

	// ── 4. Write back ──
	if len(depsRewrites) > 0 || childOverridesStripped {
		out, err := marshalOrderedJSON(pkgObj)
		if err != nil {
			report.Error = fmt.Sprintf("marshal package.json: %v", err)
			return report
		}
		if err := atomicWrite(pkgPath, out, 0o644); err != nil {
			report.Error = fmt.Sprintf("write package.json: %v", err)
			return report
		}
		for kind, writes := range depKindsTouched {
			report.Notes = append(report.Notes, fmt.Sprintf("Pinned %s for %s to host family %s", kind, strings.Join(sortedKeys(writes), ", "), family.ID))
		}
		if childOverridesStripped {
			report.Notes = append(report.Notes, "Removed stale overrides from workspace child (npm only honours overrides at the workspace root)")
		}
	}

	if overridesChanged || len(rootDepsTouched) > 0 {
		out, err := marshalOrderedJSON(overridesObj)
		if err != nil {
			report.Error = fmt.Sprintf("marshal overrides target: %v", err)
			return report
		}
		if err := atomicWrite(overridesPath, out, 0o644); err != nil {
			report.Error = fmt.Sprintf("write overrides target: %v", err)
			return report
		}
		if overridesChanged {
			report.Notes = append(report.Notes, "Wrote npm overrides for "+strings.Join(sortedKeys(merged), ", ")+" to "+overridesPath+" (host family "+family.ID+")")
		}
		for kind, writes := range rootDepsTouched {
			report.Notes = append(report.Notes, fmt.Sprintf("Synced root %s for %s to match overrides (npm EOVERRIDE guard)", kind, strings.Join(sortedKeys(writes), ", ")))
		}
	}

	if !overridesChanged && len(depsRewrites) == 0 && !childOverridesStripped && len(rootDepsTouched) == 0 {
		report.SkippedReason = "package.json already pins host family — only npm install needed"
	}

	// ── 5. Run npm install ──
	// In a workspace, run from the workspace root with `-w <member>` so npm
	// resolves the whole tree using the root's overrides. Outside a
	// workspace, install from the project itself.
	//
	// Build explicit `<pkg>@<version>` specs for every package we just pinned
	// in dependencies, and pass --save-exact so the lockfile + node_modules
	// match the new pin in one pass. Plain `npm install --legacy-peer-deps`
	// without specs sometimes leaves a stale node_modules/<pkg> at the old
	// version when the lockfile was already pointing there — this is what
	// caused the "auto-align ran, package.json says react 19.1.0, but
	// node_modules/react/package.json still reads 19.2.5" failure mode and
	// the consequent RUNTIME_FAMILY_MISMATCH at the post-align re-probe.
	npmCwd := workDir
	args := []string{"install", "--legacy-peer-deps"}
	if wsRoot != "" && wsRoot != workDir {
		npmCwd = wsRoot
		if wsMember != "" {
			args = append(args, "-w", wsMember)
		}
	}
	if len(depsRewrites) > 0 {
		args = append(args, "--save-exact")
		for _, name := range sortedKeys(depsRewrites) {
			args = append(args, fmt.Sprintf("%s@%s", name, depsRewrites[name]))
		}
	}
	npmCmd := exec.CommandContext(ctx, "npm", args...)
	npmCmd.Dir = npmCwd
	start := time.Now()
	out, err := npmCmd.CombinedOutput()
	report.NPMInstallRan = true
	report.NPMInstallMs = time.Since(start).Milliseconds()
	if err != nil {
		report.Error = fmt.Sprintf("npm install failed: %v: %s", err, tailBytes(out, 1200))
		return report
	}

	// ── 6. Verify the install actually downgraded what we asked for ──
	// If `node_modules/<pkg>/package.json` still doesn't match after the
	// install, the alignment effectively failed. Surface that in the report
	// so devserver_http's RUNTIME_FAMILY_MISMATCH is explained instead of
	// silently re-firing. In a workspace setup the package may be hoisted
	// to the workspace root, so check both locations.
	var verifyDrift []string
	for name, want := range desired {
		installed, rerr := readInstalledPackageVersion(workDir, name)
		if rerr != nil && wsRoot != "" && wsRoot != workDir {
			installed, rerr = readInstalledPackageVersion(wsRoot, name)
		}
		if rerr != nil {
			continue
		}
		if !runtimeVersionEquals(installed, want) {
			verifyDrift = append(verifyDrift, fmt.Sprintf("%s installed=%s want=%s", name, installed, want))
		}
	}
	if len(verifyDrift) > 0 {
		report.Error = "post-install version drift: " + strings.Join(verifyDrift, ", ")
		return report
	}

	report.Applied = true
	report.Notes = append(report.Notes, fmt.Sprintf("npm install completed in %dms", report.NPMInstallMs))
	return report
}

// detectNpmWorkspaceRoot walks up from workDir looking for a parent
// package.json whose `workspaces` (array form or {packages: array}) declares
// a glob/literal that matches the relative path from that parent to workDir.
// Returns ("", "") when not in a workspace. The second return value is the
// workspace member's package.json `name`, used by `npm install -w <name>`.
func detectNpmWorkspaceRoot(workDir string) (string, string) {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	abs = filepath.Clean(abs)

	// Read the workspace child's name once; we may need it for `-w <name>`.
	memberName := ""
	if data, err := os.ReadFile(filepath.Join(abs, "package.json")); err == nil {
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &p) == nil {
			memberName = strings.TrimSpace(p.Name)
		}
	}

	parent := filepath.Dir(abs)
	for parent != abs {
		pkgPath := filepath.Join(parent, "package.json")
		if data, err := os.ReadFile(pkgPath); err == nil {
			rel, relErr := filepath.Rel(parent, abs)
			if relErr == nil {
				rel = filepath.ToSlash(rel)
				if matchesAnyWorkspacePattern(data, rel) {
					return parent, memberName
				}
			}
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	return "", ""
}

// matchesAnyWorkspacePattern returns true when package.json data declares a
// `workspaces` field (either []string or {packages: []string}) and at least
// one entry matches the candidate relative path under it.
func matchesAnyWorkspacePattern(pkgJSON []byte, rel string) bool {
	var probe struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if err := json.Unmarshal(pkgJSON, &probe); err != nil {
		return false
	}
	if len(probe.Workspaces) == 0 {
		return false
	}
	var patterns []string
	if err := json.Unmarshal(probe.Workspaces, &patterns); err != nil {
		var obj struct {
			Packages []string `json:"packages"`
		}
		if jerr := json.Unmarshal(probe.Workspaces, &obj); jerr != nil {
			return false
		}
		patterns = obj.Packages
	}
	for _, raw := range patterns {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			continue
		}
		pattern = strings.TrimPrefix(pattern, "./")
		if pattern == rel {
			return true
		}
		if ok, _ := filepath.Match(pattern, rel); ok {
			return true
		}
	}
	return false
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// marshalOrderedJSON marshals a map[string]json.RawMessage with stable key
// order so subsequent diffs are clean. Node.js / npm don't care about the
// order, but humans + CI diffs do.
func marshalOrderedJSON(obj map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("{\n")
	for i, k := range keys {
		kBytes, _ := json.Marshal(k)
		sb.WriteString("  ")
		sb.Write(kBytes)
		sb.WriteString(": ")
		sb.Write(obj[k])
		if i < len(keys)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return []byte(sb.String()), nil
}

func orderedRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	// Currently a passthrough — we re-emit in sorted order in
	// marshalOrderedJSON. Wrapper kept so callers can swap to a stable
	// emitter later (yaml/preserve-style) without changing the call site.
	return in
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pkg-align-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func tailBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return "…" + string(b[len(b)-max:])
}

// NativeModulesAlignmentReport summarises an attempt to pin per-native-module
// versions in a guest project to whatever Yaver's mobile host has compiled
// in. Sibling to RuntimeAlignmentReport but driven by the post-bundle compat
// report's flagged mismatches (e.g. expo-mail-composer 15.0.8 → host 55.0.13)
// rather than the runtime-family triple.
type NativeModulesAlignmentReport struct {
	Attempted        bool              `json:"attempted"`
	Applied          bool              `json:"applied"`
	SkippedReason    string            `json:"skippedReason,omitempty"`
	Pins             map[string]string `json:"pins,omitempty"`
	NPMInstallRan    bool              `json:"npmInstallRan"`
	NPMInstallMs     int64             `json:"npmInstallMs,omitempty"`
	WorkspaceRoot    string            `json:"workspaceRoot,omitempty"`
	WorkspaceMember  string            `json:"workspaceMember,omitempty"`
	OverridesWritten string            `json:"overridesWritten,omitempty"`
	Notes            []string          `json:"notes,omitempty"`
	Error            string            `json:"error,omitempty"`
}

// alignProjectNativeModulesIfNeeded pins each project dep listed in
// `mismatches` to the corresponding host version. The framework align
// (alignProjectRuntimeIfNeeded) handles React/RN/Expo; this function
// handles every other host-registered native module that drifted across
// a likely-breaking boundary (major / 0.x-minor). Without it, a project
// that was last `npm install`ed against an older Expo SDK keeps an old
// sub-module version (e.g. expo-mail-composer 15.0.8) and the Hermes
// load is blocked with NATIVE_MODULE_VERSION_MISMATCH even though the
// runtime family otherwise matches.
//
// Same workspace-aware mechanism as alignProjectRuntimeIfNeeded:
// overrides go to the workspace ROOT package.json (npm ignores them
// elsewhere), `dependencies` get rewritten in the project itself, and
// the install runs with `--save-exact <pkg>@<version>` so the lockfile
// + node_modules actually update.
//
// Idempotent: repeat calls with the same pins skip the package.json
// edit and only run `npm install` (which exits in a few seconds when
// the lockfile already matches).
//
// Skipped when:
//   - autoAlign is false
//   - mismatches is empty (no host versions to enforce)
//   - none of the named modules are direct deps in the project (we
//     never inject deps the project doesn't already declare).
func alignProjectNativeModulesIfNeeded(ctx context.Context, workDir string, mismatches []NativeModuleMismatch, autoAlign bool) NativeModulesAlignmentReport {
	report := NativeModulesAlignmentReport{Attempted: false}
	if !autoAlign {
		report.SkippedReason = "autoAlignRuntime=false"
		return report
	}
	if len(mismatches) == 0 {
		report.SkippedReason = "no native module mismatches"
		return report
	}

	pins := map[string]string{}
	for _, m := range mismatches {
		host := strings.TrimSpace(m.HostVersion)
		name := strings.TrimSpace(m.Name)
		if host == "" || name == "" {
			continue
		}
		pins[name] = host
	}
	if len(pins) == 0 {
		report.SkippedReason = "no host versions to enforce"
		return report
	}

	wsRoot, wsMember := detectNpmWorkspaceRoot(workDir)
	report.WorkspaceRoot = wsRoot
	report.WorkspaceMember = wsMember

	pkgPath := filepath.Join(workDir, "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		report.Error = fmt.Sprintf("read package.json: %v", err)
		return report
	}
	var pkgObj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &pkgObj); err != nil {
		report.Error = fmt.Sprintf("parse package.json: %v", err)
		return report
	}

	overridesPath := pkgPath
	overridesObj := pkgObj
	if wsRoot != "" && wsRoot != workDir {
		overridesPath = filepath.Join(wsRoot, "package.json")
		rawRoot, rerr := os.ReadFile(overridesPath)
		if rerr != nil {
			report.Error = fmt.Sprintf("read workspace root package.json: %v", rerr)
			return report
		}
		var rootObj map[string]json.RawMessage
		if jerr := json.Unmarshal(rawRoot, &rootObj); jerr != nil {
			report.Error = fmt.Sprintf("parse workspace root package.json: %v", jerr)
			return report
		}
		overridesObj = rootObj
	}
	report.OverridesWritten = overridesPath

	currentOverrides := map[string]string{}
	if rawOv, ok := overridesObj["overrides"]; ok {
		var existing map[string]json.RawMessage
		if err := json.Unmarshal(rawOv, &existing); err == nil {
			for k, v := range existing {
				var s string
				if err := json.Unmarshal(v, &s); err == nil {
					currentOverrides[k] = s
				}
			}
		}
	}

	// Patch dependencies / devDependencies — only rewrite keys the project
	// already declares so we never inject a dep the project did not opt
	// into. This is the load-bearing change npm honours when resolving the
	// install plan for top-level packages.
	depsRewrites := map[string]string{}
	depKindsTouched := map[string]map[string]string{}
	declaredAnywhere := map[string]bool{}
	for _, kind := range []string{"dependencies", "devDependencies"} {
		raw, ok := pkgObj[kind]
		if !ok {
			continue
		}
		var existing map[string]json.RawMessage
		if err := json.Unmarshal(raw, &existing); err != nil {
			continue
		}
		writes := map[string]string{}
		for name, want := range pins {
			rawCur, present := existing[name]
			if !present {
				continue
			}
			declaredAnywhere[name] = true
			var cur string
			_ = json.Unmarshal(rawCur, &cur)
			if strings.TrimSpace(cur) == want {
				continue
			}
			b, _ := json.Marshal(want)
			existing[name] = b
			writes[name] = want
			depsRewrites[name] = want
		}
		if len(writes) > 0 {
			ordered := orderedRawMap(existing)
			out, err := json.MarshalIndent(ordered, "  ", "  ")
			if err != nil {
				report.Error = fmt.Sprintf("marshal %s: %v", kind, err)
				return report
			}
			pkgObj[kind] = out
			depKindsTouched[kind] = writes
		}
	}

	if len(declaredAnywhere) == 0 {
		// Nothing to pin — the mismatches concern packages that aren't
		// direct deps of this project. Probably a transitive misreport;
		// don't touch anything.
		report.SkippedReason = "no flagged modules are direct deps of this project"
		return report
	}

	// Companion deps: some host native modules hard-import a runtime peer the
	// guest package.json never declares (react-native-iap 14.7.x →
	// react-native-nitro-modules). Once we pin/keep such a module the peer
	// must be present at the host version too, or `expo export:embed` fails
	// with "Unable to resolve module <peer>". This is the one sanctioned
	// exception to the "only rewrite declared deps" rule — the manifest, not
	// a heuristic, decides which peers get injected.
	companionPins := map[string]string{}
	for trigger := range declaredAnywhere {
		for cname, cver := range hostNativeModuleCompanions(trigger) {
			if _, already := pins[cname]; already {
				continue
			}
			companionPins[cname] = cver
		}
	}
	if len(companionPins) > 0 {
		var deps map[string]json.RawMessage
		if rawDeps, ok := pkgObj["dependencies"]; ok {
			_ = json.Unmarshal(rawDeps, &deps)
		}
		if deps == nil {
			deps = map[string]json.RawMessage{}
		}
		depsChanged := false
		for cname, cver := range companionPins {
			var cur string
			if rc, ok := deps[cname]; ok {
				_ = json.Unmarshal(rc, &cur)
			}
			// Always route the companion through the install/verify path so
			// it lands in node_modules even if package.json already lists it.
			pins[cname] = cver
			depsRewrites[cname] = cver
			declaredAnywhere[cname] = true
			if strings.TrimSpace(cur) == cver {
				continue
			}
			b, _ := json.Marshal(cver)
			deps[cname] = b
			depsChanged = true
		}
		if depsChanged {
			out, err := json.MarshalIndent(orderedRawMap(deps), "  ", "  ")
			if err != nil {
				report.Error = fmt.Sprintf("marshal companion dependencies: %v", err)
				return report
			}
			pkgObj["dependencies"] = out
			report.Notes = append(report.Notes, "Added companion deps to dependencies: "+strings.Join(sortedKeys(companionPins), ", "))
		}
	}

	// Patch overrides at the right level (workspace root in a workspace,
	// project itself otherwise). Overrides matter because npm install with
	// explicit specs only pins the named pkg; transitive deps that pull a
	// different version of one of these packages still need the override.
	merged := cloneStringMap(currentOverrides)
	overridesChanged := false
	for k, v := range pins {
		if !declaredAnywhere[k] {
			// Skip overrides for packages the project doesn't declare;
			// adding them would be noise and could force a transitive
			// downgrade we never asked for.
			continue
		}
		if existing, ok := merged[k]; ok && existing == v {
			continue
		}
		merged[k] = v
		overridesChanged = true
	}

	if overridesChanged {
		var ovValue map[string]json.RawMessage
		if rawOv, ok := overridesObj["overrides"]; ok {
			_ = json.Unmarshal(rawOv, &ovValue)
		}
		if ovValue == nil {
			ovValue = map[string]json.RawMessage{}
		}
		for k, v := range merged {
			b, _ := json.Marshal(v)
			ovValue[k] = b
		}
		ovBytes, err := json.MarshalIndent(orderedRawMap(ovValue), "  ", "  ")
		if err != nil {
			report.Error = fmt.Sprintf("marshal overrides: %v", err)
			return report
		}
		overridesObj["overrides"] = ovBytes
	}

	report.Pins = depsRewrites
	report.Attempted = true

	// Write back the project package.json (the framework align may have
	// already stripped child overrides; mirror that behaviour here so the
	// two passes compose without leaving stale data).
	childOverridesStripped := false
	if wsRoot != "" && wsRoot != workDir {
		if _, ok := pkgObj["overrides"]; ok {
			delete(pkgObj, "overrides")
			childOverridesStripped = true
		}
	}
	if len(depsRewrites) > 0 || childOverridesStripped {
		out, err := marshalOrderedJSON(pkgObj)
		if err != nil {
			report.Error = fmt.Sprintf("marshal package.json: %v", err)
			return report
		}
		if err := atomicWrite(pkgPath, out, 0o644); err != nil {
			report.Error = fmt.Sprintf("write package.json: %v", err)
			return report
		}
		for kind, writes := range depKindsTouched {
			report.Notes = append(report.Notes, fmt.Sprintf("Pinned %s for %s to host versions", kind, strings.Join(sortedKeys(writes), ", ")))
		}
		if childOverridesStripped {
			report.Notes = append(report.Notes, "Removed stale overrides from workspace child")
		}
	}
	if overridesChanged {
		out, err := marshalOrderedJSON(overridesObj)
		if err != nil {
			report.Error = fmt.Sprintf("marshal overrides target: %v", err)
			return report
		}
		if err := atomicWrite(overridesPath, out, 0o644); err != nil {
			report.Error = fmt.Sprintf("write overrides target: %v", err)
			return report
		}
		report.Notes = append(report.Notes, "Wrote npm overrides for native-module pins to "+overridesPath)
	}

	if !overridesChanged && len(depsRewrites) == 0 {
		report.SkippedReason = "package.json already pins host native modules — only npm install needed"
	}

	npmCwd := workDir
	args := []string{"install", "--legacy-peer-deps"}
	if wsRoot != "" && wsRoot != workDir {
		npmCwd = wsRoot
		if wsMember != "" {
			args = append(args, "-w", wsMember)
		}
	}
	if len(depsRewrites) > 0 {
		args = append(args, "--save-exact")
		for _, name := range sortedKeys(depsRewrites) {
			args = append(args, fmt.Sprintf("%s@%s", name, depsRewrites[name]))
		}
	}
	npmCmd := exec.CommandContext(ctx, "npm", args...)
	npmCmd.Dir = npmCwd
	start := time.Now()
	out, err := npmCmd.CombinedOutput()
	report.NPMInstallRan = true
	report.NPMInstallMs = time.Since(start).Milliseconds()
	if err != nil {
		report.Error = fmt.Sprintf("npm install failed: %v: %s", err, tailBytes(out, 1200))
		return report
	}

	// Verify each pin landed in node_modules — same fallback to workspace
	// root for hoisted installs as the framework path.
	var verifyDrift []string
	for name, want := range depsRewrites {
		installed, rerr := readInstalledPackageVersion(workDir, name)
		if rerr != nil && wsRoot != "" && wsRoot != workDir {
			installed, rerr = readInstalledPackageVersion(wsRoot, name)
		}
		if rerr != nil {
			continue
		}
		if !runtimeVersionEquals(installed, want) {
			verifyDrift = append(verifyDrift, fmt.Sprintf("%s installed=%s want=%s", name, installed, want))
		}
	}
	if len(verifyDrift) > 0 {
		report.Error = "post-install version drift: " + strings.Join(verifyDrift, ", ")
		return report
	}

	report.Applied = true
	report.Notes = append(report.Notes, fmt.Sprintf("npm install completed in %dms", report.NPMInstallMs))
	return report
}

// alignmentSignature is a stable hash of (workDir, family.id, react, rn,
// expo). When the same set is requested twice in a row, the agent can skip
// the npm install round-trip because nothing relevant changed. Reserved
// for a future caching layer — not used yet so changes can ship without
// state-file churn.
func alignmentSignature(workDir string, family *RuntimeFamily) string {
	if family == nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(workDir))
	h.Write([]byte("|"))
	h.Write([]byte(family.ID))
	h.Write([]byte("|"))
	h.Write([]byte(strings.TrimSpace(family.React)))
	h.Write([]byte("|"))
	h.Write([]byte(strings.TrimSpace(family.ReactNative)))
	h.Write([]byte("|"))
	h.Write([]byte(strings.TrimSpace(family.ExpoVersion)))
	return hex.EncodeToString(h.Sum(nil)[:8])
}
