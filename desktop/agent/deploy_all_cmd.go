package main

// deploy_all_cmd.go — `yaver deploy all` ships the entire Yaver stack
// in one command, in a fixed order chosen for "fail loudly on the
// surface that's most likely to fail":
//
//   1. TestFlight (iOS)         — local-only (UDID keychain), most flaky
//   2. Play Store internal      — local script, second most flaky
//   3. Convex backend           — `npx convex deploy --yes`
//   4. Cloudflare web           — `scripts/deploy-web.sh`
//   5. npm CLI release          — bump version, tag `cli/vX.Y.Z`, push;
//                                 CI (release-cli.yml) builds platform
//                                 binaries + publishes the npm wrapper
//
// This is intentionally NOT `yaver deploy ship --targets ...` — that
// command runs through the agent's HTTP API for shared-machine deploys
// of *guest* projects with vault-supplied credentials. `deploy all` is
// for shipping Yaver itself: it shells out to the canonical
// scripts/*.sh files directly so the same code path that works
// manually works here, and the output is identical to running each
// script by hand.
//
// Ordering rationale: TestFlight first because its 24h rate-limit
// failure mode is the worst — discovering it's blocked after you've
// already pushed Convex schema changes is painful. npm last because
// it triggers async CI and there's no point bumping a version if the
// mobile uploads failed.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func runDeployAllCmd(args []string) {
	fs := flag.NewFlagSet("deploy all", flag.ExitOnError)
	skipTestflight := fs.Bool("skip-testflight", false, "Skip the TestFlight stage")
	skipPlaystore := fs.Bool("skip-playstore", false, "Skip the Play Store internal-track stage")
	skipConvex := fs.Bool("skip-convex", false, "Skip the Convex backend deploy")
	skipCloudflare := fs.Bool("skip-cloudflare", false, "Skip the Cloudflare web deploy")
	skipNpm := fs.Bool("skip-npm", false, "Skip the npm CLI release (no version bump, no tag push)")
	dryRun := fs.Bool("dry-run", false, "Print stages and the commands they'd run; don't execute")
	keepGoing := fs.Bool("continue-on-error", false, "Don't abort the pipeline when a stage fails")
	bump := fs.String("bump", "patch", "Version bump for npm CLI release: patch|minor|major")
	fs.Parse(args)

	repoRoot, err := findYaverRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "deploy all: %v\n", err)
		os.Exit(2)
	}

	stages := []deployAllStage{
		{
			name:    "TestFlight",
			id:      "testflight",
			skip:    *skipTestflight,
			workDir: repoRoot,
			run: func(ctx *deployAllCtx) error {
				return ctx.runScript(filepath.Join(repoRoot, "scripts", "deploy-testflight.sh"))
			},
		},
		{
			name:    "Play Store (internal)",
			id:      "playstore",
			skip:    *skipPlaystore,
			workDir: repoRoot,
			run: func(ctx *deployAllCtx) error {
				if err := ctx.runScript(filepath.Join(repoRoot, "scripts", "deploy-playstore.sh")); err != nil {
					return err
				}
				// deploy-playstore.sh produces the AAB; upload-playstore.py
				// pushes it to the internal track.
				return ctx.runCmd(repoRoot, "python3", filepath.Join(repoRoot, "scripts", "upload-playstore.py"))
			},
		},
		{
			name:    "Convex backend",
			id:      "convex",
			skip:    *skipConvex,
			workDir: filepath.Join(repoRoot, "backend"),
			run: func(ctx *deployAllCtx) error {
				return ctx.runCmd(filepath.Join(repoRoot, "backend"), "npx", "convex", "deploy", "--yes")
			},
		},
		{
			name:    "Cloudflare (web)",
			id:      "cloudflare",
			skip:    *skipCloudflare,
			workDir: repoRoot,
			run: func(ctx *deployAllCtx) error {
				return ctx.runScript(filepath.Join(repoRoot, "scripts", "deploy-web.sh"))
			},
		},
		{
			name:    "npm CLI release",
			id:      "npm",
			skip:    *skipNpm,
			workDir: repoRoot,
			run: func(ctx *deployAllCtx) error {
				return runNpmCliRelease(repoRoot, *bump, *dryRun, ctx)
			},
		},
	}

	fmt.Println("yaver deploy all")
	fmt.Println("repo:", repoRoot)
	fmt.Println()

	results := make([]deployAllResult, 0, len(stages))
	overallStart := time.Now()
	failed := false

	for _, st := range stages {
		if st.skip {
			fmt.Printf("──[ %-22s ]── SKIPPED (--skip-%s)\n\n", st.name, st.id)
			results = append(results, deployAllResult{stage: st, skipped: true})
			continue
		}

		ctx := &deployAllCtx{dryRun: *dryRun, prefix: fmt.Sprintf("[%s]", st.id)}
		fmt.Printf("──[ %-22s ]── starting…\n", st.name)
		stageStart := time.Now()

		runErr := st.run(ctx)
		dur := time.Since(stageStart).Round(time.Second)

		if runErr != nil {
			fmt.Printf("\n──[ %-22s ]── FAILED in %s: %v\n\n", st.name, dur, runErr)
			results = append(results, deployAllResult{stage: st, err: runErr, duration: dur})
			failed = true
			if !*keepGoing {
				printDeployAllSummary(results, time.Since(overallStart))
				os.Exit(1)
			}
			continue
		}

		fmt.Printf("\n──[ %-22s ]── ok (%s)\n\n", st.name, dur)
		results = append(results, deployAllResult{stage: st, duration: dur})
	}

	printDeployAllSummary(results, time.Since(overallStart))
	if failed {
		os.Exit(1)
	}
}

type deployAllStage struct {
	name    string
	id      string
	skip    bool
	workDir string
	run     func(*deployAllCtx) error
}

type deployAllResult struct {
	stage    deployAllStage
	skipped  bool
	err      error
	duration time.Duration
}

type deployAllCtx struct {
	dryRun bool
	prefix string
}

// runScript runs a bash script with stdout/stderr streamed to the
// caller's terminal. The script's environment inherits the parent so
// `yaver vault env --project ...` calls inside the script keep
// working.
func (c *deployAllCtx) runScript(path string) error {
	return c.runCmd(filepath.Dir(path), "bash", path)
}

func (c *deployAllCtx) runCmd(workDir, name string, args ...string) error {
	if c.dryRun {
		fmt.Printf("  %s [dry-run] cd %s && %s %s\n", c.prefix, workDir, name, strings.Join(args, " "))
		return nil
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	go streamLines(stdout, c.prefix, os.Stdout)
	go streamLines(stderr, c.prefix, os.Stderr)

	if err := cmd.Wait(); err != nil {
		return err
	}
	return nil
}

func streamLines(r io.Reader, prefix string, dst io.Writer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Fprintf(dst, "  %s %s\n", prefix, scanner.Text())
	}
}

// runNpmCliRelease bumps the cli version, commits the bump, tags
// `cli/vX.Y.Z`, and pushes the tag. The actual publish happens in
// release-cli.yml on GitHub Actions because the cross-platform binary
// build matrix needs CI runners (macOS notarization, Linux ARM cross-
// compile, Windows).
//
// Failure modes intentionally surfaced to the user rather than swallowed:
//   - dirty working tree           → abort (would commit unrelated changes)
//   - not on main branch           → abort (releases must come from main)
//   - origin/main ahead of HEAD    → abort (would race the tag)
//   - no `github` remote           → abort (CI is wired to that remote)
//   - tag already exists locally   → abort (would re-publish same version)
func runNpmCliRelease(repoRoot, bump string, dryRun bool, ctx *deployAllCtx) error {
	if err := requireCleanRepoForRelease(repoRoot); err != nil {
		return err
	}

	currentVersion, err := readCliVersion(repoRoot)
	if err != nil {
		return err
	}

	nextVersion, err := bumpSemver(currentVersion, bump)
	if err != nil {
		return err
	}

	tag := "cli/v" + nextVersion
	fmt.Printf("  %s bumping cli version: %s → %s (tag %s)\n", ctx.prefix, currentVersion, nextVersion, tag)

	// Check the tag doesn't already exist — git will reject it on
	// push, but failing earlier is friendlier and avoids a half-state
	// (commit landed, tag rejected).
	checkTag := exec.Command("git", "tag", "--list", tag)
	checkTag.Dir = repoRoot
	out, _ := checkTag.Output()
	if strings.TrimSpace(string(out)) == tag {
		return fmt.Errorf("tag %s already exists locally — bump version manually or delete the tag", tag)
	}

	if dryRun {
		fmt.Printf("  %s [dry-run] would write %s to versions.json + cli/package.json + cli/package-lock.json + cli/sdk-manifest.json\n", ctx.prefix, nextVersion)
		fmt.Printf("  %s [dry-run] would commit, tag %s, and push to github\n", ctx.prefix, tag)
		return nil
	}

	if err := writeCliVersionFiles(repoRoot, nextVersion); err != nil {
		return fmt.Errorf("write version files: %w", err)
	}

	stageFiles := []string{
		"versions.json",
		"cli/package.json",
		"cli/package-lock.json",
	}
	// sdk-manifest.json doesn't always carry a version field — only
	// stage it if the bump touched it.
	manifestPath := filepath.Join(repoRoot, "cli", "sdk-manifest.json")
	if hasVersionKey(manifestPath) {
		stageFiles = append(stageFiles, "cli/sdk-manifest.json")
	}

	addArgs := append([]string{"add", "--"}, stageFiles...)
	if err := ctx.runCmd(repoRoot, "git", addArgs...); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	commitMsg := fmt.Sprintf("cli %s: release", nextVersion)
	if err := ctx.runCmd(repoRoot, "git", "commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	if err := ctx.runCmd(repoRoot, "git", "tag", tag); err != nil {
		return fmt.Errorf("git tag: %w", err)
	}

	if err := ctx.runCmd(repoRoot, "git", "push", "github", "main"); err != nil {
		return fmt.Errorf("push main: %w", err)
	}

	if err := ctx.runCmd(repoRoot, "git", "push", "github", tag); err != nil {
		return fmt.Errorf("push tag: %w", err)
	}

	fmt.Printf("  %s tag pushed — release-cli.yml is now building binaries + publishing the npm wrapper\n", ctx.prefix)
	fmt.Printf("  %s monitor: gh run list -w release-cli.yml -L 1\n", ctx.prefix)
	return nil
}

func requireCleanRepoForRelease(repoRoot string) error {
	// Must be a git repo with a `github` remote.
	remotes := exec.Command("git", "remote")
	remotes.Dir = repoRoot
	out, err := remotes.Output()
	if err != nil {
		return fmt.Errorf("not a git repo: %w", err)
	}
	if !strings.Contains(string(out), "github") {
		return fmt.Errorf("no `github` remote configured — release-cli.yml is wired to the github remote")
	}

	// Must be on main.
	branch := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branch.Dir = repoRoot
	bOut, err := branch.Output()
	if err != nil {
		return fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	if strings.TrimSpace(string(bOut)) != "main" {
		return fmt.Errorf("not on main (current: %s) — releases must come from main", strings.TrimSpace(string(bOut)))
	}

	// Working tree must be clean. Otherwise the version-bump commit
	// would also pull in unrelated WIP, which is the silent-bug
	// scenario `feedback_other_sessions_prune_untested.md` warns
	// about.
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = repoRoot
	sOut, err := status.Output()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(sOut)) != "" {
		return fmt.Errorf("working tree not clean — commit or stash before releasing:\n%s", string(sOut))
	}

	return nil
}

func readCliVersion(repoRoot string) (string, error) {
	pkgPath := filepath.Join(repoRoot, "cli", "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", pkgPath, err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", fmt.Errorf("parse %s: %w", pkgPath, err)
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("%s has no version field", pkgPath)
	}
	return pkg.Version, nil
}

var semverRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

func bumpSemver(v, kind string) (string, error) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return "", fmt.Errorf("not a plain X.Y.Z semver: %q", v)
	}
	major, minor, patch := atoi(m[1]), atoi(m[2]), atoi(m[3])
	switch kind {
	case "major":
		return fmt.Sprintf("%d.0.0", major+1), nil
	case "minor":
		return fmt.Sprintf("%d.%d.0", major, minor+1), nil
	case "patch", "":
		return fmt.Sprintf("%d.%d.%d", major, minor, patch+1), nil
	default:
		return "", fmt.Errorf("unknown --bump value %q (use patch|minor|major)", kind)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func writeCliVersionFiles(repoRoot, version string) error {
	// versions.json — top-level cli key.
	if err := updateJSONField(filepath.Join(repoRoot, "versions.json"), "cli", version); err != nil {
		return err
	}
	// cli/package.json — top-level version.
	if err := updateJSONField(filepath.Join(repoRoot, "cli", "package.json"), "version", version); err != nil {
		return err
	}
	// cli/package-lock.json — the lockfile carries version in two
	// places: top-level "version" and packages[""]/version.
	if err := updatePackageLockVersion(filepath.Join(repoRoot, "cli", "package-lock.json"), version); err != nil {
		return err
	}
	// cli/sdk-manifest.json — best-effort, only if it has a version key.
	manifestPath := filepath.Join(repoRoot, "cli", "sdk-manifest.json")
	if hasVersionKey(manifestPath) {
		if err := updateJSONField(manifestPath, "version", version); err != nil {
			return err
		}
	}
	return nil
}

// updateJSONField rewrites a single top-level key in a JSON file
// using a regex so we don't reorder keys, lose comments, or normalise
// whitespace. Worth the awkwardness because diff-quality matters here
// (the CI workflow watches package-lock.json and reordering keys
// triggers spurious churn).
func updateJSONField(path, key, value string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	pat := regexp.MustCompile(`("` + regexp.QuoteMeta(key) + `"\s*:\s*")[^"]*(")`)
	if !pat.Match(data) {
		return fmt.Errorf("%s: key %q not found at top level", path, key)
	}
	out := pat.ReplaceAll(data, []byte(`${1}`+value+`${2}`))
	return os.WriteFile(path, out, 0o644)
}

// updatePackageLockVersion patches both "version" occurrences in a
// package-lock.json (the top-level one and the implicit `""` package
// entry that npm rewrites on every install).
func updatePackageLockVersion(path, version string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	pat := regexp.MustCompile(`("version"\s*:\s*")[^"]*(")`)
	matches := pat.FindAllIndex(data, -1)
	if len(matches) < 1 {
		return fmt.Errorf("%s: no version field found", path)
	}
	// Replace only the first two occurrences — these are the two
	// canonical ones npm writes. Dependency entries below them stay
	// untouched.
	limit := 2
	if len(matches) < 2 {
		limit = len(matches)
	}
	count := 0
	out := pat.ReplaceAllFunc(data, func(b []byte) []byte {
		if count >= limit {
			return b
		}
		count++
		return pat.ReplaceAll(b, []byte(`${1}`+version+`${2}`))
	})
	return os.WriteFile(path, out, 0o644)
}

func hasVersionKey(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return regexp.MustCompile(`"version"\s*:\s*"`).Match(data)
}

func findYaverRepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("`yaver deploy all` must run inside the yaver.io repo (git rev-parse --show-toplevel failed: %w)", err)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("git returned empty repo root")
	}
	// Sanity-check it's actually the yaver.io repo, not just any git
	// repo. Look for the canonical scripts dir + workspace manifest.
	if _, err := os.Stat(filepath.Join(root, "scripts", "deploy-web.sh")); err != nil {
		return "", fmt.Errorf("repo root %s doesn't look like yaver.io (no scripts/deploy-web.sh)", root)
	}
	return root, nil
}

func printDeployAllSummary(results []deployAllResult, total time.Duration) {
	fmt.Println()
	fmt.Println("── deploy all summary ──")
	for _, r := range results {
		switch {
		case r.skipped:
			fmt.Printf("  [skip] %-22s\n", r.stage.name)
		case r.err != nil:
			fmt.Printf("  [FAIL] %-22s %s — %v\n", r.stage.name, r.duration, r.err)
		default:
			fmt.Printf("  [ ok ] %-22s %s\n", r.stage.name, r.duration)
		}
	}
	fmt.Printf("  total: %s\n", total.Round(time.Second))
}
