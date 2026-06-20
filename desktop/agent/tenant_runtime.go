package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type tenantRuntime struct {
	Enabled bool
	UserID  string
	User    string
	Root    string
	Home    string
}

// betaHostEnabled reports whether THIS box is a designated beta runtime host.
// Default false — a general-purpose / owner-personal box (e.g. the Talos box)
// must never execute beta-tenant code. Only the ephemeral scale-to-zero pool
// boxes set YAVER_BETA_HOST=1 (baked into their golden image / boot env).
func betaHostEnabled() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("YAVER_BETA_HOST"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func runnerNeedsTenantRuntime(runnerID string) bool {
	switch normalizeRunnerID(runnerID) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

func tenantRuntimeForGuest(userID string) tenantRuntime {
	id := betaSanitizeRef(userID)
	if id == "anon" {
		return tenantRuntime{}
	}
	root := filepath.Join(betaTenantRoot, id)
	return tenantRuntime{
		Enabled: true,
		UserID:  userID,
		User:    betaTenantUser(userID),
		Root:    root,
		Home:    filepath.Join(root, "home"),
	}
}

func tenantRuntimeForTask(task *Task) tenantRuntime {
	if task == nil || strings.TrimSpace(task.GuestUserID) == "" || !task.GuestRequireIsolation {
		return tenantRuntime{}
	}
	if !runnerNeedsTenantRuntime(firstNonEmpty(task.RunnerID, task.runner.RunnerID)) {
		return tenantRuntime{}
	}
	return tenantRuntimeForGuest(task.GuestUserID)
}

func (tr tenantRuntime) authEnv() []string {
	if !tr.Enabled {
		return nil
	}
	return []string{
		"HOME=" + tr.Home,
		"USER=" + tr.User,
		"LOGNAME=" + tr.User,
		"CLAUDE_CONFIG_DIR=" + filepath.Join(tr.Home, ".claude"),
		"CODEX_HOME=" + filepath.Join(tr.Home, ".codex"),
		"PATH=/usr/local/bin:/usr/bin:/bin:/usr/local/sbin:/usr/sbin:/sbin",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/true",
	}
}

func (tr tenantRuntime) taskEnv(task *Task) []string {
	env := append([]string{}, tr.authEnv()...)
	env = append(env,
		"CI=1",
		"NO_COLOR=1",
		"TERM=xterm-256color",
		"CLICOLOR_FORCE=1",
		"FORCE_COLOR=1",
	)
	if task != nil {
		env = append(env,
			"YAVER_TASK_SOURCE="+strings.TrimSpace(task.Source),
			"YAVER_SESSION_MODE=remote",
			"YAVER_SOURCE_SURFACE="+firstNonEmpty(strings.TrimSpace(task.Source), "unknown"),
			"YAVER_TENANT_USER_ID="+strings.TrimSpace(task.GuestUserID),
		)
		if strings.TrimSpace(task.ID) != "" {
			env = append(env, "YAVER_TASK_ID="+task.ID)
		}
	}
	return env
}

func (tr tenantRuntime) prepare() error {
	if !tr.Enabled {
		return nil
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("tenant runtime is Linux-only")
	}
	if err := validTenant(tr.User); err != nil {
		return err
	}
	if handled, err := privilegedBetaTenantCreate(tr); handled {
		return err
	}
	if os.Geteuid() == 0 {
		if _, err := (execBetaSysRunner{}).run("useradd", "--system", "--no-create-home", "--shell", "/bin/bash", "--home-dir", tr.Home, tr.User); err != nil {
			// Existing users are fine; useradd wording varies by distro.
			if _, lookupErr := exec.Command("id", "-u", tr.User).Output(); lookupErr != nil {
				return fmt.Errorf("useradd %s: %w", tr.User, err)
			}
		}
		for _, dir := range []string{tr.Root, tr.Home, filepath.Join(tr.Root, "workspace"), filepath.Join(tr.Home, ".claude"), filepath.Join(tr.Home, ".codex")} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
		}
		if out, err := exec.Command("chown", "-R", tr.User+":"+tr.User, tr.Root).CombinedOutput(); err != nil {
			return fmt.Errorf("chown %s: %w (%s)", tr.Root, err, strings.TrimSpace(string(out)))
		}
		if err := os.Chmod(tr.Root, 0o700); err != nil {
			return err
		}
		return nil
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("tenant runtime needs root/helper/sudo to create %s", tr.User)
	}
	if _, err := exec.Command("sudo", "-n", "id", "-u", tr.User).Output(); err != nil {
		out, addErr := exec.Command("sudo", "-n", "useradd", "--system", "--no-create-home", "--shell", "/bin/bash", "--home-dir", tr.Home, tr.User).CombinedOutput()
		if addErr != nil {
			return fmt.Errorf("sudo useradd %s: %w (%s)", tr.User, addErr, strings.TrimSpace(string(out)))
		}
	}
	for _, dir := range []string{tr.Root, tr.Home, filepath.Join(tr.Root, "workspace"), filepath.Join(tr.Home, ".claude"), filepath.Join(tr.Home, ".codex")} {
		if out, err := exec.Command("sudo", "-n", "install", "-d", "-o", tr.User, "-g", tr.User, "-m", "0700", dir).CombinedOutput(); err != nil {
			return fmt.Errorf("sudo install %s: %w (%s)", dir, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func (tr tenantRuntime) command(ctx context.Context, cwd, name string, args []string, env []string) (*exec.Cmd, error) {
	if !tr.Enabled {
		return exec.CommandContext(ctx, name, args...), nil
	}
	if err := tr.prepare(); err != nil {
		return nil, err
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	cleanRoot := filepath.Clean(tr.Root)
	cleanCwd := filepath.Clean(absCwd)
	if cleanCwd != cleanRoot && !strings.HasPrefix(cleanCwd, cleanRoot+string(filepath.Separator)) {
		return nil, fmt.Errorf("tenant task cwd %s is outside tenant root %s", cleanCwd, cleanRoot)
	}
	script := `cd "$YAVER_TENANT_CWD" && exec "$@"`
	sudoArgs := []string{"-n", "-u", tr.User, "-H", "env"}
	sudoArgs = append(sudoArgs, env...)
	sudoArgs = append(sudoArgs, "YAVER_TENANT_CWD="+cleanCwd, "sh", "-c", script, "yaver-tenant", name)
	sudoArgs = append(sudoArgs, args...)
	cmd := exec.CommandContext(ctx, "sudo", sudoArgs...)
	cmd.Env = append(os.Environ(), "PATH="+expandedPath())
	return cmd, nil
}

func privilegedBetaTenantCreate(tr tenantRuntime) (bool, error) {
	if !helperAvailable() {
		return false, nil
	}
	r := helperCall(helperRequest{Verb: "tenant_create", Tenant: tr.User, Root: tr.Root, Home: tr.Home})
	if r.OK {
		return true, nil
	}
	return true, errFromResp(r)
}
