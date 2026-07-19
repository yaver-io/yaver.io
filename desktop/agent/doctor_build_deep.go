package main

// doctor_build_deep.go — deeper capability checks layered on top of the
// basic toolchain + secret probes in doctor_build.go. The wedge: today
// `RunBuildDoctor` reports OK as long as `xcodebuild` exists on PATH and
// the secret env vars are set. That gives false greens on:
//
//   • Macs with only Command Line Tools — `/usr/bin/xcodebuild` is a stub
//     that errors "tool 'xcodebuild' requires Xcode." We need `xcrun
//     -find xcodebuild` to point inside Xcode.app, not at the stub.
//   • Java < 17 — Gradle for RN/Expo needs Java 17. A box with Java 11
//     looks fine to LookPath but fails bundleRelease.
//   • `APP_STORE_KEY_PATH` set in vault but pointing at a missing .p8
//     file. Today doctor reports the secret as Found because the env var
//     resolves; the actual file may have been deleted on cleanup.
//   • Remote machine where the user wants to deploy but the project's
//     source tree isn't checked out — toolchain looks great but there's
//     no app to build.
//
// All four are real "user picks a machine, deploy fails 30s in" paths.
// This file fills them in without breaking the existing report shape:
// new fields are additive on BuildToolResult / BuildSecretResult and a
// new top-level Project block. Old clients keep reading `ok` + `reason`
// the same way; new clients (mobile pane v2, desktop) can render the
// extended fields.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// BuildProjectStatus reports whether the agent can locate the named app
// in this device's workspace manifest. Crucial for the multi-machine
// picker — a remote machine without the project shouldn't claim ok=true
// just because java/python3 are installed.
type BuildProjectStatus struct {
	// Name is the slug the doctor was asked about.
	Name string `json:"name"`
	// Found is true if a workspace manifest in the agent's CWD ancestry
	// contains an apps[] entry matching Name.
	Found bool `json:"found"`
	// Stack is the app's declared stack (e.g. react-native-expo). Empty
	// if Found=false.
	Stack string `json:"stack,omitempty"`
	// HasGit is true if the resolved app path is inside a git working
	// tree (best-effort — runs `git -C <path> rev-parse --is-inside-work-tree`).
	HasGit bool `json:"hasGit,omitempty"`
	// Reason is a one-liner explaining the failure when Found=false.
	Reason string `json:"reason,omitempty"`
}

// pathSecretSuffixes identifies vault keys whose VALUE is expected to be
// a filesystem path. We file-stat the resolved path and surface a
// PathError if it's missing — otherwise the doctor green-lights a secret
// whose target file got deleted, and the deploy fails at upload time.
var pathSecretSuffixes = []string{"_PATH", "_FILE", "_KEY_PATH", "_KEYSTORE"}

func isPathSecret(name string) bool {
	for _, s := range pathSecretSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// resolveSecretValue walks the same lookup order as RunBuildDoctor:
// vault:project → vault:global → env. Returns ("", "") if unresolved.
// Pulled into a helper so the path-existence check can re-resolve
// without re-implementing the search.
func resolveSecretValue(name, project string, vs *VaultStore) (value, source string) {
	if vs != nil {
		if project != "" {
			if e, err := vs.Get(project, name); err == nil && e.Value != "" {
				return e.Value, "vault:project"
			}
		}
		if e, err := vs.Get("", name); err == nil && e.Value != "" {
			return e.Value, "vault:global"
		}
	}
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v, "env"
	}
	return "", ""
}

// checkPathSecret expands ~ and verifies the file exists. Empty value
// means "secret unresolved" — handled upstream, not here.
func checkPathSecret(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", errors.New("empty value")
	}
	if strings.HasPrefix(v, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			v = filepath.Join(home, v[2:])
		}
	}
	if !filepath.IsAbs(v) {
		// Relative paths are evaluated against the agent's CWD; we
		// stat as-is. Most consumers (deploy scripts) export to env
		// then `cd` into the project, so abs paths are normal.
	}
	st, err := os.Stat(v)
	if err != nil {
		if os.IsNotExist(err) {
			return v, fmt.Errorf("file not found at %s", v)
		}
		return v, err
	}
	if st.IsDir() {
		// Some users point KEY_PATH at a folder by mistake. Catch it.
		return v, fmt.Errorf("expected a file at %s, got a directory", v)
	}
	return v, nil
}

// xcodebuildIsRealXcode runs `xcrun -find xcodebuild` and confirms the
// resolved path lives inside Xcode.app, not the CLT stub. Mac-only —
// callers gate by GOOS first.
//
// Why this matters: `/usr/bin/xcodebuild` exists on every Mac as a
// dispatcher. Without Xcode installed it prints
//
//	xcode-select: error: tool 'xcodebuild' requires Xcode, but active
//	developer directory '/Library/Developer/CommandLineTools' is a
//	command line tools instance
//
// to stderr and exits non-zero. The basic probeTool sees a non-empty
// version string (the error message) and reports Found=true Version=<garbage>.
// `xcrun -find` is the canonical way to confirm a real Xcode is active.
func xcodebuildIsRealXcode(ctx context.Context) (path string, ok bool, reason string) {
	if runtime.GOOS != "darwin" {
		return "", false, "only on darwin"
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, "xcrun", "-find", "xcodebuild").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", false, "xcrun -find xcodebuild failed: " + firstLineRaw(msg)
	}
	resolved := strings.TrimSpace(string(out))
	if resolved == "" {
		return "", false, "xcrun -find xcodebuild returned empty path"
	}
	if !strings.Contains(resolved, "Xcode.app/") {
		return resolved, false, "Xcode is not installed (resolved to " + resolved + " — Command Line Tools only)"
	}
	return resolved, true, ""
}

// javaMajorVersion runs `java -version` and parses the major component.
// Output format varies — Oracle prints `java version "17.0.1"`, OpenJDK
// prints `openjdk version "17.0.1"`, both go to stderr. CombinedOutput
// captures both.
func javaMajorVersion(ctx context.Context, path string) (int, string, error) {
	if path == "" {
		return 0, "", errors.New("empty path")
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, path, "-version").CombinedOutput()
	if err != nil && len(out) == 0 {
		return 0, "", err
	}
	first := firstLineRaw(strings.TrimSpace(string(out)))
	// Match `(java|openjdk) version "X.Y.Z"`. The X is the major; for
	// Java 9+ (the dotted-X scheme) it's just an int.
	rx := regexp.MustCompile(`version\s+"(\d+)(?:\.(\d+))?`)
	m := rx.FindStringSubmatch(first)
	if len(m) < 2 {
		return 0, first, fmt.Errorf("could not parse: %s", first)
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, first, err
	}
	// Pre-9 Java reported `1.8.0_xx`. The "real" major in that scheme
	// is the second component.
	if major == 1 && len(m) >= 3 && m[2] != "" {
		if real2, err := strconv.Atoi(m[2]); err == nil {
			major = real2
		}
	}
	return major, first, nil
}

// resolveProjectPresenceLocally checks the workspace manifest visible to
// this agent. Calls into resolveProjectRef (already used by /deploy/run)
// so we reuse the same lookup order. Failure is non-fatal for the
// overall doctor — it just means the user picked a remote machine that
// doesn't have a checkout of this app.
//
// We don't return the absolute path; that would leak the user's home
// dir over the wire. Stack is safe (it's metadata, not a path).
func resolveProjectPresenceLocally(name string) BuildProjectStatus {
	st := BuildProjectStatus{Name: strings.TrimSpace(name)}
	if st.Name == "" {
		st.Reason = "no app slug supplied"
		return st
	}
	ref, err := resolveProjectRef(st.Name, "")
	if err != nil {
		st.Reason = "no workspace entry on this machine"
		return st
	}
	st.Found = true
	st.Stack = ref.Stack
	// Best-effort git-tree check — most legitimate Yaver deploys run
	// from a tracked repo, and a missing .git is a strong "this isn't
	// the source of truth" signal.
	gitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", "-C", ref.Path, "rev-parse", "--is-inside-work-tree")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil && strings.TrimSpace(out.String()) == "true" {
		st.HasGit = true
	}
	return st
}

// runDeepChecks layers the deep probes onto an already-populated
// BuildDoctorReport. It mutates the report in place — adds DeepError /
// DeepValid to tools, PathValid / PathError to secrets, and sets the
// Project block. Toggles report.OK to false for any new hard failure.
//
// Order of evaluation is intentional: project-presence first (cheapest,
// catches the multi-machine "wrong box" case before we burn time on
// xcrun), then per-target deep tool probes, then path-secret validity.
func runDeepChecks(report *BuildDoctorReport, target, project string, vs *VaultStore) {
	ctx := context.Background()

	// 1. Project presence on this device.
	if strings.TrimSpace(project) != "" {
		ps := resolveProjectPresenceLocally(project)
		report.Project = project
		report.ProjectStatus = &ps
		if !ps.Found {
			report.OK = false
			report.Notes = append(report.Notes,
				fmt.Sprintf("Project %q is not in this machine's workspace manifest — pick a different device or check it out here.", project))
		}
	}

	// 2. Deep tool probes.
	for i := range report.Tools {
		t := &report.Tools[i]
		if t.Skipped || !t.Found {
			continue
		}
		switch t.Name {
		case "xcodebuild":
			resolved, ok, reason := xcodebuildIsRealXcode(ctx)
			f := ok
			t.DeepValid = &f
			if !ok {
				t.DeepError = reason
				report.OK = false
				report.Notes = append(report.Notes, "xcodebuild stub detected — install Xcode (not just Command Line Tools).")
			} else if resolved != "" {
				// Promote the xcrun-resolved path; it's the one the
				// build will actually use.
				t.Path = resolved
			}
		case "java":
			major, raw, err := javaMajorVersion(ctx, t.Path)
			if err == nil {
				t.VersionMajor = major
				if raw != "" && t.Version == "" {
					t.Version = raw
				}
			}
			f := major >= 17
			t.DeepValid = &f
			if !f {
				if major == 0 {
					t.DeepError = "could not parse java -version"
				} else {
					t.DeepError = fmt.Sprintf("Java 17+ required (found %d). Gradle for RN/Expo will fail to build.", major)
				}
				report.OK = false
				report.Notes = append(report.Notes, "Install Java 17 (brew install openjdk@17) and ensure it's first on PATH.")
			}
		}
	}

	// 3. Path-secret validity. Iterate by index so we mutate the slice
	// elements in place. Secrets that are NOT path-shaped get nil
	// PathValid and the basic Found flag governs.
	for i := range report.Secrets {
		s := &report.Secrets[i]
		if !isPathSecret(s.Name) {
			continue
		}
		if !s.Found {
			// Already counted as missing — no point checking the path.
			f := false
			s.PathValid = &f
			continue
		}
		value, _ := resolveSecretValue(s.Name, project, vs)
		_, err := checkPathSecret(value)
		valid := err == nil
		s.PathValid = &valid
		if err != nil {
			// Privacy: don't echo the resolved path verbatim if it's an
			// absolute path under the user's home dir — could leak the
			// macOS username in cross-device responses. The error string
			// from checkPathSecret already includes the path; sanitise.
			s.PathError = sanitisePathInError(err.Error())
			report.OK = false
			report.Notes = append(report.Notes,
				fmt.Sprintf("%s is set but the file is missing — re-run yaver vault add %s with a valid path.", s.Name, s.Name))
		}
	}

	t, known := buildTargets[target]
	if !known {
		return
	}

	// 4. Disk headroom. Checked BEFORE signing: it's the cheaper probe,
	// and a full volume breaks the codesign probe too (it needs to stage a
	// temp file), which would otherwise produce a confusing signing error
	// for what is really a disk problem.
	if t.MinFreeGB > 0 {
		d := checkDiskHeadroom(t.MinFreeGB)
		if d.Checked {
			report.Disk = &d
			if !d.OK {
				report.OK = false
				report.Notes = append(report.Notes,
					fmt.Sprintf("Only %.1f GB free on %s — %s needs ~%d GB. The build will die partway through with \"No space left on device\". Free space first (Xcode DerivedData, ~/.gradle/caches, Docker images).",
						d.FreeGB, d.Mount, target, d.RequiredGB))
			}
		}
	}

	// 5. Real signing capability. Must run last: it is the most expensive
	// probe (spawns codesign) and is pointless if the disk is already full.
	if t.NeedsCodesign {
		s := probeSigningCapability(ctx)
		if s.Checked {
			report.Signing = &s
			if !s.CanSign {
				report.OK = false
				note := "Cannot codesign on this machine"
				if s.Identity != "" {
					// Name the trap explicitly: the certificate IS present.
					// Without this the reader assumes a missing cert and
					// goes looking in the wrong place.
					note += fmt.Sprintf(" even though %q is installed", s.Identity)
				}
				report.Notes = append(report.Notes, note+" — "+s.Remedy)
			} else if s.Repaired {
				report.Notes = append(report.Notes,
					fmt.Sprintf("Signing keychain %s was locked and has been unlocked automatically.", s.Keychain))
			}
		}
	}
}

// sanitisePathInError replaces the user's home dir with `~` in error
// messages so cross-device doctor responses don't leak the macOS short
// username. Other absolute paths under /Users/ get the same treatment
// (we surface "/Users/<u>/…" as "~/…" only when <u> matches our home).
func sanitisePathInError(msg string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return msg
	}
	return strings.ReplaceAll(msg, home, "~")
}

// jsonForRoundTripDeep is a tiny helper for tests — round-trips through
// JSON to confirm the extended struct stays serialisable.
func jsonForRoundTripDeep(report BuildDoctorReport) (BuildDoctorReport, error) {
	b, err := json.Marshal(report)
	if err != nil {
		return BuildDoctorReport{}, err
	}
	var out BuildDoctorReport
	if err := json.Unmarshal(b, &out); err != nil {
		return BuildDoctorReport{}, err
	}
	return out, nil
}
