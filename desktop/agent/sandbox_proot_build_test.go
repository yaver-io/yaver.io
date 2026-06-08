package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildProotArgv's extraBinds must appear as -b flags after the cred binds and
// before -w / the inner command.
func TestBuildProotArgvExtraBinds(t *testing.T) {
	cfg := sampleCfg()
	dir := "/data/data/io.yaver.mobile/files/phone-projects/demo"
	argv := buildProotArgv(cfg, []string{"hermesc", "-out", dir + "/.yaver-build/main.jsbundle"},
		dir, dir+":"+dir)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-b "+dir+":"+dir) {
		t.Errorf("missing project bind\n got: %s", joined)
	}
	if !strings.Contains(joined, "-w "+dir) {
		t.Errorf("missing -w for project dir\n got: %s", joined)
	}
	// empty bind specs are skipped, not emitted as a bare "-b".
	argv2 := buildProotArgv(cfg, []string{"node"}, dir, "", "  ")
	if strings.Contains(strings.Join(argv2, " "), "-b -w") {
		t.Errorf("empty extraBind leaked a bare -b: %v", argv2)
	}
}

// sandboxBuildEnv keeps the build's NODE_*/EXPO_*/Convex vars but forces the
// rootfs PATH/HOME and drops host-specific knobs.
func TestSandboxBuildEnvPreservesBuildVarsOverridesHostPath(t *testing.T) {
	cfg := sampleCfg()
	caller := []string{
		"PATH=/Users/x/.yaver/runtimes/node/bin:/usr/bin", // host PATH — must be overridden
		"HOME=/Users/x",                          // host HOME — must be overridden
		"NODE_OPTIONS=--max-old-space-size=5120", // must survive
		"NODE_ENV=production",                    // must survive
		"EXPO_PUBLIC_API=https://api.example",    // must survive
		"CONVEX_URL=https://box.convex",          // must survive
		"LD_PRELOAD=/evil.so",                    // host-specific — must drop
		"TERM=xterm-256color",
	}
	env := sandboxBuildEnv(cfg, caller)
	get := func(k string) (string, bool) {
		for _, kv := range env {
			if strings.HasPrefix(kv, k+"=") {
				return strings.TrimPrefix(kv, k+"="), true
			}
		}
		return "", false
	}

	if v, _ := get("PATH"); v != "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Errorf("PATH not overridden to rootfs PATH, got %q", v)
	}
	if v, _ := get("HOME"); v != "/root" {
		t.Errorf("HOME not overridden to /root, got %q", v)
	}
	for _, k := range []string{"NODE_OPTIONS", "NODE_ENV", "EXPO_PUBLIC_API", "CONVEX_URL"} {
		if _, ok := get(k); !ok {
			t.Errorf("build var %s was dropped", k)
		}
	}
	if _, ok := get("LD_PRELOAD"); ok {
		t.Error("host-specific LD_PRELOAD leaked into rootfs env")
	}
	// PATH/HOME/NODE_OPTIONS must each appear exactly once (no dup from the
	// rootfs base + caller merge).
	for _, k := range []string{"PATH", "HOME", "NODE_OPTIONS"} {
		n := 0
		for _, kv := range env {
			if strings.HasPrefix(kv, k+"=") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s appears %d times, want 1", k, n)
		}
	}
}

// sandboxWrapBuildCmd binds an existing host project dir at its own path and
// rewrites the command to run under proot, preserving build env.
func TestSandboxWrapBuildCmdBindsExistingProjectDir(t *testing.T) {
	proj := t.TempDir() // a real dir so the os.Stat check passes
	t.Setenv(envSandboxRootfs, "/data/rootfs")
	t.Setenv(envSandboxProot, "/lib/libproot.so")

	cmd := exec.Command("hermesc", "-out", filepath.Join(proj, ".yaver-build/main.jsbundle"))
	cmd.Dir = proj
	cmd.Env = []string{"NODE_OPTIONS=--max-old-space-size=5120", "PATH=/host/bin"}

	out := sandboxWrapBuildCmd(cmd)
	if out.Path != "/lib/libproot.so" {
		t.Fatalf("cmd.Path not rewritten to proot: %q", out.Path)
	}
	joined := strings.Join(out.Args, " ")
	if !strings.Contains(joined, "-b "+proj+":"+proj) {
		t.Errorf("project dir not bound at its own path\n got: %s", joined)
	}
	if !strings.Contains(joined, "-w "+proj) {
		t.Errorf("missing -w project dir\n got: %s", joined)
	}
	if out.Dir != "" {
		t.Errorf("cmd.Dir should be cleared (proot runs from native cwd), got %q", out.Dir)
	}
	// Build env preserved + host PATH overridden.
	envJoined := strings.Join(out.Env, " ")
	if !strings.Contains(envJoined, "NODE_OPTIONS=--max-old-space-size=5120") {
		t.Errorf("NODE_OPTIONS dropped from wrapped build env: %v", out.Env)
	}
	if strings.Contains(envJoined, "PATH=/host/bin") {
		t.Errorf("host PATH leaked into wrapped build env: %v", out.Env)
	}
}

// A rootfs-internal cmd.Dir (not present on the native fs) gets no project
// bind — only the standard -w, matching the runner-wrap behavior.
func TestSandboxWrapBuildCmdNoBindForRootfsInternalDir(t *testing.T) {
	t.Setenv(envSandboxRootfs, "/data/rootfs")
	t.Setenv(envSandboxProot, "/lib/libproot.so")

	cmd := exec.Command("npm", "install")
	cmd.Dir = "/root/projects/demo" // does not exist on the test host fs
	out := sandboxWrapBuildCmd(cmd)
	joined := strings.Join(out.Args, " ")
	if strings.Contains(joined, "-b /root/projects/demo:/root/projects/demo") {
		t.Errorf("rootfs-internal dir should not be bound: %s", joined)
	}
	if !strings.Contains(joined, "-w /root/projects/demo") {
		t.Errorf("missing -w for rootfs-internal dir: %s", joined)
	}
}

// Off-sandbox, sandboxWrapBuildCmd is a pure no-op.
func TestSandboxWrapBuildCmdNoOpWithoutEnv(t *testing.T) {
	os.Unsetenv(envSandboxRootfs)
	os.Unsetenv(envSandboxProot)
	cmd := exec.Command("npx", "expo", "export:embed")
	cmd.Dir = "/some/project"
	out := sandboxWrapBuildCmd(cmd)
	if out.Path == "" || filepath.Base(out.Args[0]) != "npx" {
		t.Errorf("expected unchanged command off-sandbox, got path=%q args=%v", out.Path, out.Args)
	}
	if out.Dir != "/some/project" {
		t.Errorf("cmd.Dir should be untouched off-sandbox, got %q", out.Dir)
	}
}
