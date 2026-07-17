package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// writeTree materialises a fixture project from a path→content map.
// Real files on disk — the detector's whole job is reading a real tree.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return root
}

func hasTag(d *StackDetection, tag string) bool {
	for _, s := range d.Tags {
		if s == tag {
			return true
		}
	}
	return false
}

func hasFramework(d *StackDetection, framework string) bool {
	return slices.Contains(d.Frameworks, framework)
}

func findTarget(d *StackDetection, id string) *DetectedTarget {
	for i := range d.Targets {
		if d.Targets[i].ID == id {
			return &d.Targets[i]
		}
	}
	return nil
}

func TestStackDetectSupabaseConfigIsDeployable(t *testing.T) {
	root := writeTree(t, map[string]string{
		"supabase/config.toml":              "project_id = \"abc\"\n",
		"supabase/functions/hello/index.ts": "export default () => {}\n",
		"package.json":                      `{"name":"api","dependencies":{"@supabase/supabase-js":"^2"}}`,
	})

	d := stackDetect(root)

	if d.Backend != string(BackendSupabase) {
		t.Errorf("backend = %q, want supabase", d.Backend)
	}
	if !hasTag(d, "supabase") {
		t.Errorf("tags = %v, want supabase", d.Tags)
	}
	tgt := findTarget(d, "supabase")
	if tgt == nil {
		t.Fatal("no supabase target detected")
	}
	if !tgt.Supported {
		t.Errorf("supabase target unsupported (%s), want deployable", tgt.Reason)
	}
	if tgt.Weak {
		t.Error("config.toml present — target must not be weak")
	}
	if tgt.Evidence != "supabase/config.toml" {
		t.Errorf("evidence = %q, want supabase/config.toml", tgt.Evidence)
	}
	if len(d.DeployableTargets()) == 0 {
		t.Error("DeployableTargets() empty — deploy would still refuse to guess")
	}
}

// The safety case. A repo that merely TALKS to someone else's Supabase must
// never be offered "supabase db push" — pushing a schema to a project you
// don't own is not a recoverable mistake.
func TestStackDetectSupabaseDepOnlyIsNotDeployable(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"name":"web","dependencies":{"@supabase/supabase-js":"^2","next":"14"}}`,
		"next.config.js": "module.exports = {}\n",
	})

	d := stackDetect(root)

	if !hasTag(d, "supabase") {
		t.Errorf("tags = %v — a supabase client should still be tagged", d.Tags)
	}
	tgt := findTarget(d, "supabase")
	if tgt == nil {
		t.Fatal("no supabase target detected")
	}
	if tgt.Supported {
		t.Error("dep-only supabase must NOT be deployable — this would push a schema to someone else's project")
	}
	if !tgt.Weak {
		t.Error("dep-only supabase must be marked weak")
	}
	// Backend is a claim of ownership; a weak signal must not set it.
	if d.Backend == string(BackendSupabase) {
		t.Error("dep-only supabase must not claim Backend=supabase")
	}
	for _, dt := range d.DeployableTargets() {
		if dt.ID == "supabase" {
			t.Error("supabase leaked into DeployableTargets()")
		}
	}
}

// Expo must win over react-native: every Expo app also depends on
// react-native, so ordering here is load-bearing.
func TestStackDetectExpoBeatsReactNative(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json": `{"name":"m","dependencies":{"expo":"51","react-native":"0.74","react":"18"}}`,
		"app.json":     `{"expo":{"name":"m"}}`,
	})
	if got := stackDetect(root).Framework; got != FwExpo {
		t.Errorf("framework = %q, want %q", got, FwExpo)
	}
}

func TestStackDetectSolitoReportsAllFrameworks(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"name":"app","dependencies":{"expo":"51","react-native":"0.74","react":"18","next":"14"}}`,
		"app.json":       `{"expo":{"name":"app"}}`,
		"next.config.js": "module.exports = {}\n",
	})

	d := stackDetect(root)
	if d.Framework != FwExpo {
		t.Fatalf("primary framework = %q, want %q", d.Framework, FwExpo)
	}
	for _, want := range []string{FwExpo, FwReactNative, FwNextJS, FwReact} {
		if !hasFramework(d, want) {
			t.Errorf("frameworks = %v, missing %q", d.Frameworks, want)
		}
		if !hasTag(d, want) {
			t.Errorf("tags = %v, missing %q", d.Tags, want)
		}
	}
}

func TestStackDetectBareReactNative(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json": `{"name":"m","dependencies":{"react-native":"0.74","react":"18"}}`,
	})
	if got := stackDetect(root).Framework; got != FwReactNative {
		t.Errorf("framework = %q, want %q", got, FwReactNative)
	}
}

// A React Native repo always contains ios/*.xcodeproj. It must never be
// labelled swift — the ordering contract from classify.go:319.
func TestStackDetectReactNativeWithXcodeprojIsNotSwift(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":                    `{"name":"m","dependencies":{"react-native":"0.74"}}`,
		"ios/m.xcodeproj/project.pbxproj": "// pbxproj\n",
	})
	if got := stackDetect(root).Framework; got != FwReactNative {
		t.Errorf("framework = %q, want %q", got, FwReactNative)
	}
}

func TestStackDetectNextWithVercelAndCloudflare(t *testing.T) {
	root := writeTree(t, map[string]string{
		"next.config.js": "module.exports = {}\n",
		"vercel.json":    "{}\n",
		"wrangler.toml":  "name = \"w\"\n",
		"package.json":   `{"name":"web","dependencies":{"next":"14"}}`,
	})
	d := stackDetect(root)
	if d.Framework != FwNextJS {
		t.Errorf("framework = %q, want nextjs", d.Framework)
	}
	// Both hosts are genuinely configured — the detector must report both
	// rather than silently picking one. Disambiguation is the caller's job.
	if len(d.DeployableTargets()) < 2 {
		t.Errorf("DeployableTargets() = %d, want >=2 (vercel + cloudflare)", len(d.DeployableTargets()))
	}
	if !hasTag(d, "vercel") || !hasTag(d, "cloudflare") {
		t.Errorf("tags = %v, want both vercel and cloudflare", d.Tags)
	}
}

func TestStackDetectMonorepoWorkspaces(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":                      `{"name":"root","workspaces":["apps/*","packages/*"]}`,
		"apps/web/package.json":             `{"name":"web","dependencies":{"next":"14"}}`,
		"apps/web/next.config.js":           "module.exports = {}\n",
		"apps/web/vercel.json":              "{}\n",
		"packages/api/package.json":         `{"name":"api"}`,
		"packages/api/supabase/config.toml": "project_id = \"x\"\n",
		"packages/ui/package.json":          `{"name":"ui"}`,
	})

	d := stackDetect(root)
	if !d.IsMonorepo {
		t.Fatal("IsMonorepo = false, want true")
	}
	// ui has nothing detectable and must be dropped as noise.
	if len(d.Packages) != 2 {
		names := []string{}
		for _, p := range d.Packages {
			names = append(names, p.RelPath)
		}
		t.Fatalf("packages = %v, want exactly apps/web and packages/api", names)
	}

	byRel := map[string]*StackDetection{}
	for _, p := range d.Packages {
		byRel[filepath.ToSlash(p.RelPath)] = p
	}
	web, ok := byRel["apps/web"]
	if !ok {
		t.Fatal("apps/web not detected")
	}
	if web.Framework != FwNextJS {
		t.Errorf("apps/web framework = %q, want nextjs", web.Framework)
	}
	api, ok := byRel["packages/api"]
	if !ok {
		t.Fatal("packages/api not detected")
	}
	if api.Backend != string(BackendSupabase) {
		t.Errorf("packages/api backend = %q, want supabase", api.Backend)
	}
	// Child tags roll up so a monorepo card shows the whole stack.
	if !hasTag(d, "supabase") || !hasTag(d, "vercel") {
		t.Errorf("root tags = %v, want rolled-up supabase + vercel", d.Tags)
	}
	if got := d.Roles["backend"]; got != "supabase" {
		t.Errorf("backend role = %q, want supabase", got)
	}
	if got := d.Roles["frontend"]; got != "nextjs" {
		t.Errorf("frontend role = %q, want nextjs", got)
	}
}

func TestStackDetectMonorepoRolesRollUp(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":              `{"name":"root","workspaces":["apps/*","packages/*"]}`,
		"apps/web/package.json":     `{"name":"web","dependencies":{"next":"14"}}`,
		"apps/web/next.config.js":   "module.exports = {}\n",
		"packages/api/package.json": `{"name":"api"}`,
		"packages/api/convex.json":  "{}\n",
		"apps/mobile/package.json":  `{"name":"mobile","dependencies":{"expo":"51","react-native":"0.74"}}`,
		"apps/mobile/app.json":      `{"expo":{"name":"mobile"}}`,
	})

	d := stackDetect(root)
	if d.Role != "unknown" {
		t.Fatalf("root role = %q, want unknown", d.Role)
	}
	want := map[string]string{"backend": "convex", "frontend": "nextjs", "mobile": "expo"}
	if len(d.Roles) != len(want) {
		t.Fatalf("roles = %#v, want %#v", d.Roles, want)
	}
	for role, stack := range want {
		if got := d.Roles[role]; got != stack {
			t.Errorf("role %q = %q, want %q", role, got, stack)
		}
	}
}

func TestStackDetectMonorepoTwoFrontendsWarns(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":              `{"name":"root","workspaces":["apps/*"]}`,
		"apps/web/package.json":     `{"name":"web","dependencies":{"next":"14"}}`,
		"apps/web/next.config.js":   "module.exports = {}\n",
		"apps/admin/package.json":   `{"name":"admin","dependencies":{"react":"18"}}`,
		"apps/admin/vite.config.ts": "export default {}\n",
	})

	d := stackDetect(root)
	if got := d.Roles["frontend"]; got != "nextjs" {
		t.Fatalf("frontend role = %q, want nextjs (shortest rel path wins)", got)
	}
	if len(d.Warnings) == 0 {
		t.Fatal("expected collision warning for multiple frontend packages")
	}
}

func TestStackDetectPnpmWorkspace(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":           `{"name":"root"}`,
		"pnpm-workspace.yaml":    "packages:\n  - \"apps/*\"\n",
		"apps/api/package.json":  `{"name":"api"}`,
		"apps/api/firebase.json": "{}\n",
	})
	d := stackDetect(root)
	if !d.IsMonorepo {
		t.Fatal("IsMonorepo = false, want true for pnpm workspace")
	}
	if !hasTag(d, "firebase") {
		t.Errorf("tags = %v, want rolled-up firebase", d.Tags)
	}
}

// The layout yaver.io itself uses, and the one that made the first cut of
// this detector return absolutely nothing: sibling project dirs, no root
// package.json, no workspace declaration anywhere.
func TestStackDetectConventionMonorepoWithoutWorkspaces(t *testing.T) {
	root := writeTree(t, map[string]string{
		"web/package.json":     `{"name":"web","dependencies":{"next":"14"}}`,
		"web/next.config.ts":   "export default {}\n",
		"web/wrangler.toml":    "name = \"web\"\n",
		"mobile/package.json":  `{"name":"m","dependencies":{"expo":"51","react-native":"0.74"}}`,
		"backend/convex.json":  "{}\n",
		"backend/package.json": `{"name":"backend"}`,
		"relay/go.mod":         "module relay\n",
		"README.md":            "# repo\n",
	})

	d := stackDetect(root)
	if !d.IsMonorepo {
		t.Fatal("IsMonorepo = false — a repo of sibling projects with no workspace declaration is still a monorepo")
	}
	byRel := map[string]*StackDetection{}
	for _, p := range d.Packages {
		byRel[filepath.ToSlash(p.RelPath)] = p
	}
	for rel, wantFw := range map[string]string{
		"web": FwNextJS, "mobile": FwExpo, "relay": FwGo,
	} {
		p, ok := byRel[rel]
		if !ok {
			t.Errorf("%s not detected", rel)
			continue
		}
		if p.Framework != wantFw {
			t.Errorf("%s framework = %q, want %q", rel, p.Framework, wantFw)
		}
	}
	if p, ok := byRel["backend"]; !ok || p.Backend != string(BackendConvex) {
		t.Errorf("backend/ should detect convex, got %+v", p)
	}
}

// Build output and platform dirs must never surface as projects. mobile/ios
// holds an .xcodeproj belonging to the RN app — detecting it as a separate
// Swift project would put a bogus TestFlight button on a phantom package.
func TestStackDetectSkipsPlatformAndBuildDirs(t *testing.T) {
	root := writeTree(t, map[string]string{
		"mobile/package.json":                    `{"name":"m","dependencies":{"expo":"51"}}`,
		"mobile/ios/m.xcodeproj/project.pbxproj": "// x\n",
		"node_modules/foo/package.json":          `{"name":"foo","dependencies":{"next":"14"}}`,
		"dist/package.json":                      `{"name":"dist"}`,
	})
	d := stackDetect(root)
	for _, p := range d.Packages {
		rel := filepath.ToSlash(p.RelPath)
		if rel != "mobile" {
			t.Errorf("detected %q as a package — build/platform dirs must be skipped", rel)
		}
	}
}

// Evidence paths must stay relative — an absolute path leaks the user's
// home-dir username, which the Convex privacy contract forbids.
func TestStackDetectEvidencePathsAreRelative(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":                  `{"name":"root","workspaces":["apps/*"]}`,
		"apps/api/package.json":         `{"name":"api"}`,
		"apps/api/supabase/config.toml": "project_id = \"x\"\n",
	})
	d := stackDetect(root)
	var check func(*StackDetection)
	check = func(s *StackDetection) {
		for _, e := range s.Evidence {
			if filepath.IsAbs(e.Path) {
				t.Errorf("evidence path %q is absolute — leaks the home dir to Convex", e.Path)
			}
		}
		if filepath.IsAbs(s.RelPath) {
			t.Errorf("RelPath %q is absolute", s.RelPath)
		}
		for _, p := range s.Packages {
			check(p)
		}
	}
	check(d)
}

func TestStackDetectEmptyDirIsQuiet(t *testing.T) {
	d := stackDetect(t.TempDir())
	if d.Framework != "" || len(d.Targets) != 0 || d.IsMonorepo {
		t.Errorf("empty dir produced framework=%q targets=%d monorepo=%v, want all empty",
			d.Framework, len(d.Targets), d.IsMonorepo)
	}
}

func TestStackDetectCacheSameTreeHits(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"name":"web","dependencies":{"next":"14"}}`,
		"next.config.js": "module.exports = {}\n",
	})
	stackDetectInvalidate(root)
	first, changed := stackDetectCached(root)
	if !changed {
		t.Fatal("cold cache should report changed=true")
	}
	second, changed := stackDetectCached(root)
	if changed {
		t.Fatal("same tree should report changed=false on cache hit")
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("fingerprints differ: %q vs %q", first.Fingerprint, second.Fingerprint)
	}
}

func TestStackDetectCacheDetectsAddedSupabaseConfig(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json": `{"name":"web","dependencies":{"@supabase/supabase-js":"^2","next":"14"}}`,
	})
	stackDetectInvalidate(root)
	first, _ := stackDetectCached(root)
	if findTarget(first, "supabase") == nil || findTarget(first, "supabase").Supported {
		t.Fatalf("initial detection = %+v, want weak supabase only", first.Targets)
	}
	mustSleepForMtime()
	if err := os.MkdirAll(filepath.Join(root, "supabase"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "supabase", "config.toml"), []byte("project_id = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, changed := stackDetectCached(root)
	if !changed {
		t.Fatal("adding supabase/config.toml must invalidate the cache")
	}
	if second.Fingerprint == first.Fingerprint {
		t.Fatal("fingerprint did not change after adding supabase config")
	}
	tgt := findTarget(second, "supabase")
	if tgt == nil || !tgt.Supported || tgt.Weak {
		t.Fatalf("supabase target = %+v, want strong deployable target", tgt)
	}
}

func TestStackDetectCacheDetectsRemovedFirebase(t *testing.T) {
	root := writeTree(t, map[string]string{
		"firebase.json": "{}\n",
	})
	stackDetectInvalidate(root)
	first, _ := stackDetectCached(root)
	if findTarget(first, "firebase") == nil {
		t.Fatal("expected firebase target before removal")
	}
	mustSleepForMtime()
	if err := os.Remove(filepath.Join(root, "firebase.json")); err != nil {
		t.Fatal(err)
	}
	second, changed := stackDetectCached(root)
	if !changed {
		t.Fatal("removing firebase.json must invalidate the cache")
	}
	if findTarget(second, "firebase") != nil {
		t.Fatalf("firebase target still present after removal: %+v", second.Targets)
	}
}

func TestStackDetectCacheDetectsPackageJSONEdit(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json": `{"name":"web","dependencies":{"react":"18"}}`,
	})
	stackDetectInvalidate(root)
	first, _ := stackDetectCached(root)
	mustSleepForMtime()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"web","dependencies":{"react":"18","next":"14"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	second, changed := stackDetectCached(root)
	if !changed {
		t.Fatal("editing package.json must invalidate the cache")
	}
	if second.Fingerprint == first.Fingerprint {
		t.Fatal("fingerprint did not change after package.json edit")
	}
}

func TestStackDetectCacheIgnoresUnrelatedReadmeTouch(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"name":"web","dependencies":{"next":"14"}}`,
		"next.config.js": "module.exports = {}\n",
		"README.md":      "# repo\n",
	})
	stackDetectInvalidate(root)
	first, _ := stackDetectCached(root)
	mustSleepForMtime()
	now := time.Now().UTC().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(root, "README.md"), now, now); err != nil {
		t.Fatal(err)
	}
	second, changed := stackDetectCached(root)
	if changed {
		t.Fatal("touching README.md should not invalidate detection cache")
	}
	if second.Fingerprint != first.Fingerprint {
		t.Fatal("fingerprint changed after unrelated README touch")
	}
}

func TestStackDetectInvalidateForcesRedetect(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"name":"web","dependencies":{"next":"14"}}`,
		"next.config.js": "module.exports = {}\n",
	})
	stackDetectInvalidate(root)
	_, _ = stackDetectCached(root)
	stackDetectInvalidate(root)
	_, changed := stackDetectCached(root)
	if !changed {
		t.Fatal("invalidate should force changed=true on next lookup")
	}
}

func mustSleepForMtime() {
	time.Sleep(20 * time.Millisecond)
}
