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
	Attempted       bool              `json:"attempted"`
	Applied         bool              `json:"applied"`
	SkippedReason   string            `json:"skippedReason,omitempty"`
	TargetFamilyID  string            `json:"targetFamilyId,omitempty"`
	TargetFamily    *RuntimeFamily    `json:"targetFamily,omitempty"`
	OverridesBefore map[string]string `json:"overridesBefore,omitempty"`
	OverridesAfter  map[string]string `json:"overridesAfter,omitempty"`
	NPMInstallRan   bool              `json:"npmInstallRan"`
	NPMInstallMs    int64             `json:"npmInstallMs,omitempty"`
	Notes           []string          `json:"notes,omitempty"`
	Error           string            `json:"error,omitempty"`
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

	pkgPath := filepath.Join(workDir, "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		report.Error = fmt.Sprintf("read package.json: %v", err)
		return report
	}
	// Use a sorted-key map decode so we can preserve all sibling fields when
	// writing back. json.Unmarshal into json.RawMessage keeps the rest
	// untouched.
	var pkgObj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &pkgObj); err != nil {
		report.Error = fmt.Sprintf("parse package.json: %v", err)
		return report
	}

	currentOverrides := map[string]string{}
	if rawOv, ok := pkgObj["overrides"]; ok {
		// Best-effort decode — overrides values can be strings or nested
		// objects; we only manage string-valued entries we care about.
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

	// ── 1. Patch overrides ──
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

	// ── 2. Patch dependencies (and devDependencies) ──
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

	pkgChanged := overridesChanged || len(depsRewrites) > 0
	if overridesChanged {
		var ovValue map[string]json.RawMessage
		if rawOv, ok := pkgObj["overrides"]; ok {
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
		pkgObj["overrides"] = ovBytes
	}
	report.OverridesAfter = merged

	if pkgChanged {
		out, err := marshalOrderedJSON(pkgObj)
		if err != nil {
			report.Error = fmt.Sprintf("marshal package.json: %v", err)
			return report
		}
		if err := atomicWrite(pkgPath, out, 0o644); err != nil {
			report.Error = fmt.Sprintf("write package.json: %v", err)
			return report
		}
		if overridesChanged {
			report.Notes = append(report.Notes, "Wrote npm overrides for "+strings.Join(sortedKeys(merged), ", ")+" to align with host family "+family.ID)
		}
		for kind, writes := range depKindsTouched {
			report.Notes = append(report.Notes, fmt.Sprintf("Pinned %s for %s to host family %s", kind, strings.Join(sortedKeys(writes), ", "), family.ID))
		}
	} else {
		report.SkippedReason = "package.json already pins host family — only npm install needed"
	}

	// ── 3. Run npm install ──
	// Build explicit `<pkg>@<version>` specs for every package we just pinned
	// in dependencies, and pass --save-exact so the lockfile + node_modules
	// match the new pin in one pass. Plain `npm install --legacy-peer-deps`
	// without specs sometimes leaves a stale node_modules/<pkg> at the old
	// version when the lockfile was already pointing there — this is what
	// caused the "auto-align ran, package.json says react 19.1.0, but
	// node_modules/react/package.json still reads 19.2.5" failure mode and
	// the consequent RUNTIME_FAMILY_MISMATCH at the post-align re-probe.
	args := []string{"install", "--legacy-peer-deps"}
	if len(depsRewrites) > 0 {
		args = append(args, "--save-exact")
		for _, name := range sortedKeys(depsRewrites) {
			args = append(args, fmt.Sprintf("%s@%s", name, depsRewrites[name]))
		}
	}
	npmCmd := exec.CommandContext(ctx, "npm", args...)
	npmCmd.Dir = workDir
	start := time.Now()
	out, err := npmCmd.CombinedOutput()
	report.NPMInstallRan = true
	report.NPMInstallMs = time.Since(start).Milliseconds()
	if err != nil {
		report.Error = fmt.Sprintf("npm install failed: %v: %s", err, tailBytes(out, 1200))
		return report
	}

	// ── 4. Verify the install actually downgraded what we asked for ──
	// If `node_modules/<pkg>/package.json` still doesn't match after the
	// install, the alignment effectively failed. Surface that in the report
	// so devserver_http's RUNTIME_FAMILY_MISMATCH is explained instead of
	// silently re-firing.
	var verifyDrift []string
	for name, want := range desired {
		installed, rerr := readInstalledPackageVersion(workDir, name)
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
