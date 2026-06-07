package main

import (
	"os/exec"
	"strings"
	"testing"
)

func sampleCfg() sandboxConfig {
	return sandboxConfig{
		Proot:  "/data/app/lib/libproot.so",
		Loader: "/data/app/lib/libproot-loader.so",
		Rootfs: "/data/data/io.yaver.mobile/files/rootfs",
		Tmp:    "/data/data/io.yaver.mobile/cache/proot-tmp",
	}
}

func TestBuildProotArgvWrapsInner(t *testing.T) {
	cfg := sampleCfg()
	argv := buildProotArgv(cfg, []string{"claude", "-p", "fix the bug"}, "/root/project")

	if argv[0] != cfg.Proot {
		t.Fatalf("argv[0] = %q, want proot %q", argv[0], cfg.Proot)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"-r " + cfg.Rootfs,
		"-w /root/project",
		"-b /dev",
		"--kill-on-exit",
		"claude -p fix the bug",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q\n got: %s", want, joined)
		}
	}
	// inner command must be the tail, in order, after all proot flags.
	if argv[len(argv)-3] != "claude" || argv[len(argv)-2] != "-p" || argv[len(argv)-1] != "fix the bug" {
		t.Errorf("inner argv not preserved at tail: %v", argv[len(argv)-3:])
	}
}

func TestBuildProotArgvCredBinds(t *testing.T) {
	cfg := sampleCfg()
	cfg.CredHome = "/data/data/io.yaver.mobile/files/home"
	argv := buildProotArgv(cfg, []string{"claude", "-p", "go"}, "/root/project")
	joined := strings.Join(argv, " ")

	// Every runner's cred dir must bind host → /root/<rel> so a mirrored token
	// reaches the real CLI inside the rootfs.
	for _, rel := range []string{".claude", ".codex", ".config/opencode", ".local/share/opencode"} {
		want := "-b " + cfg.CredHome + "/" + rel + ":/root/" + rel
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing cred bind %q\n got: %s", want, joined)
		}
	}
	// Inner command still terminates the argv (binds are flags, not the command).
	if argv[len(argv)-3] != "claude" || argv[len(argv)-1] != "go" {
		t.Errorf("inner argv not at tail after cred binds: %v", argv[len(argv)-3:])
	}
	// -w must still be present and precede the inner command.
	if !strings.Contains(joined, "-w /root/project") {
		t.Errorf("workDir flag dropped: %s", joined)
	}
}

func TestBuildProotArgvNoCredBindsWhenHomeEmpty(t *testing.T) {
	// sampleCfg has no CredHome → zero cred binds (the unit-test/host default).
	argv := buildProotArgv(sampleCfg(), []string{"/bin/sh"}, "")
	if strings.Contains(strings.Join(argv, " "), ":/root/.claude") {
		t.Errorf("emitted a cred bind with no CredHome set: %v", argv)
	}
}

func TestSandboxConfigFromEnvCredHome(t *testing.T) {
	t.Setenv(envSandboxRootfs, "/r")
	t.Setenv(envSandboxProot, "/p")
	t.Setenv(envSandboxCredHome, "/home/agent")
	cfg, ok := sandboxConfigFromEnv()
	if !ok || cfg.CredHome != "/home/agent" {
		t.Fatalf("CredHome not read from env: ok=%v cfg=%+v", ok, cfg)
	}
}

func TestBuildProotArgvDefaultWorkDir(t *testing.T) {
	argv := buildProotArgv(sampleCfg(), []string{"/bin/sh"}, "")
	if !strings.Contains(strings.Join(argv, " "), "-w /root") {
		t.Errorf("empty workDir should default to /root, got: %v", argv)
	}
}

func TestSandboxEnvReplacesHostPathPreservesTermAndYaver(t *testing.T) {
	cfg := sampleCfg()
	caller := []string{
		"PATH=/system/bin:/system/xbin", // host PATH — must be dropped
		"TERM=xterm-kitty",              // must be preserved
		"HOME=/data/data/io.yaver.mobile", // host HOME — must be dropped
		"YAVER_HOST_SHARE=1",            // must be preserved
		"LD_PRELOAD=/evil.so",           // host-specific — must be dropped
	}
	env := sandboxEnv(cfg, caller)
	joined := strings.Join(env, "\n")

	if strings.Contains(joined, "/system/bin") {
		t.Error("host PATH leaked into sandbox env")
	}
	if strings.Contains(joined, "LD_PRELOAD") {
		t.Error("host LD_PRELOAD leaked into sandbox env")
	}
	if strings.Contains(joined, "/data/data/io.yaver.mobile\n") {
		t.Error("host HOME leaked into sandbox env")
	}
	for _, want := range []string{
		"HOME=/root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-kitty",
		"PROOT_LOADER=" + cfg.Loader,
		"PROOT_TMP_DIR=" + cfg.Tmp,
		"YAVER_HOST_SHARE=1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("sandbox env missing %q\n got:\n%s", want, joined)
		}
	}
}

func TestSandboxConfigFromEnvGate(t *testing.T) {
	// Without the env vars set, the sandbox must be inactive.
	if _, ok := sandboxConfigFromEnv(); ok {
		t.Fatal("sandboxConfigFromEnv reported active without env vars set")
	}

	t.Setenv(envSandboxRootfs, "/r")
	t.Setenv(envSandboxProot, "/p")
	cfg, ok := sandboxConfigFromEnv()
	if !ok {
		t.Fatal("sandboxConfigFromEnv should be active when rootfs+proot set")
	}
	if cfg.Rootfs != "/r" || cfg.Proot != "/p" {
		t.Errorf("unexpected cfg: %+v", cfg)
	}

	// Rootfs alone (no proot) must NOT activate — both are required.
	t.Setenv(envSandboxProot, "")
	if _, ok := sandboxConfigFromEnv(); ok {
		t.Error("sandbox activated with rootfs but no proot")
	}
}

func TestSandboxWrapCmdNoOpWithoutEnv(t *testing.T) {
	// No env → cmd is returned untouched (the macOS/Linux/CI path).
	cmd := exec.Command("/bin/bash")
	cmd.Dir = "/tmp/work"
	wrapped := sandboxWrapCmd(cmd)
	if wrapped.Path != "/bin/bash" {
		t.Errorf("cmd.Path mutated without sandbox env: %q", wrapped.Path)
	}
	if wrapped.Dir != "/tmp/work" {
		t.Errorf("cmd.Dir mutated without sandbox env: %q", wrapped.Dir)
	}
}

func TestSandboxWrapCmdRewritesUnderEnv(t *testing.T) {
	t.Setenv(envSandboxRootfs, "/data/rootfs")
	t.Setenv(envSandboxProot, "/lib/libproot.so")
	t.Setenv(envSandboxLoader, "/lib/libproot-loader.so")

	cmd := exec.Command("/bin/sh")
	cmd.Dir = "/root/app"
	cmd.Env = []string{"TERM=xterm-256color"}
	wrapped := sandboxWrapCmd(cmd)

	if wrapped.Path != "/lib/libproot.so" {
		t.Errorf("cmd.Path = %q, want proot", wrapped.Path)
	}
	if wrapped.Dir != "" {
		t.Errorf("cmd.Dir should be cleared (proot -w carries the inner cwd), got %q", wrapped.Dir)
	}
	joined := strings.Join(wrapped.Args, " ")
	if !strings.Contains(joined, "-w /root/app") {
		t.Errorf("inner workDir not carried into -w: %v", wrapped.Args)
	}
	if !strings.HasSuffix(joined, "/bin/sh") {
		t.Errorf("inner shell not at tail: %v", wrapped.Args)
	}
}
