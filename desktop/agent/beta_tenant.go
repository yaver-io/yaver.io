package main

// beta_tenant.go — per-tenant isolation primitives for the beta box.
//
// Two security-critical, pure functions (unit-tested) plus the privileged
// partition provisioner (runs as root on the Hetzner box — structured here,
// not unit-tested since it shells out to useradd/chown/quota).
//
//   betaConfinePath   — refuse any path that escapes the tenant root
//                       (path-traversal guard for /dev/* + opencode file ops)
//   betaTenantRunnerEnv — the ALLOWLIST env a tenant process gets: gateway
//                       inference only, ZERO host secrets (it's a fresh env,
//                       not os.Environ() minus a denylist — nothing leaks by
//                       omission). Mirrors gateway_runner_env.go intent.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// betaTenantRoot is the on-box base for tenant partitions. A tenant's
// workspace is betaTenantRoot/<userId>/. Overridable for tests/box config.
const betaTenantRoot = "/srv/yaver/tenants"

// betaConfinePath resolves rel against tenantRoot and refuses anything that
// escapes it (../ traversal, absolute breakout). Returns the cleaned
// absolute path on success.
func betaConfinePath(tenantRoot, rel string) (string, error) {
	// A tenant must pass project-relative paths only. An absolute path is
	// invalid input (and a breakout attempt) — reject it outright rather
	// than let filepath.Join silently fold it under the root.
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path %q not allowed in tenant", rel)
	}
	root := filepath.Clean(tenantRoot)
	joined := filepath.Clean(filepath.Join(root, rel))
	if joined != root && !strings.HasPrefix(joined, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes tenant root %q", rel, tenantRoot)
	}
	return joined, nil
}

// betaTenantRunnerEnv is the COMPLETE environment a tenant's opencode / dev
// process receives — an allowlist, not a filter. It carries ONLY the
// gateway inference endpoint (so inference works, capped + metered) and a
// confined HOME/PATH. No HCLOUD_TOKEN, no GLM key, no owner vault, no git
// creds — none of it is present to leak, because we never start from
// os.Environ(). gatewayURL + ygwToken come from the per-tenant gateway
// mint (gateway_runner_env.go precedent).
func betaTenantRunnerEnv(gatewayURL, ygwToken, projectDir string) []string {
	base := strings.TrimRight(gatewayURL, "/")
	return []string{
		"HOME=" + projectDir, // confine tool state to the tenant dir
		"OPENAI_BASE_URL=" + base + "/v1",
		"OPENAI_API_KEY=" + ygwToken, // scoped ygw_ token, NOT the GLM key
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"GIT_TERMINAL_PROMPT=0", // never block on a credential prompt
		"GIT_ASKPASS=/bin/true", // and never source one — tenant pushes can't auth
	}
}

// betaTenantUser returns the unprivileged OS user name for a tenant.
func betaTenantUser(userID string) string {
	slug := betaSanitizeRef(userID)
	if len(slug) > 12 {
		slug = slug[:12]
	}
	return "yv-" + slug
}

// privilegedRunner shells out as root (box-only). Injectable for tests.
type betaSysRunner interface {
	run(name string, args ...string) (string, error)
}

type execBetaSysRunner struct{}

func (execBetaSysRunner) run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// provisionTenantPartition creates the tenant's unprivileged OS user and a
// 0700 workspace dir under betaTenantRoot, owned by that user. PRIVILEGED —
// runs as root on the Hetzner box. Idempotent-ish: a useradd that fails
// because the user exists is tolerated. Disk quota + a dedicated mount are
// box-config concerns layered on top (documented in the design doc); this
// establishes the user + dir + ownership that the rest of the isolation
// relies on.
func provisionTenantPartition(sys betaSysRunner, userID string) (tenantDir string, err error) {
	user := betaTenantUser(userID)
	root := betaTenantRoot
	tenantDir = filepath.Join(root, betaSanitizeRef(userID))

	// Create the unprivileged, no-login, no-home-elsewhere tenant user.
	if out, e := sys.run("useradd", "--system", "--no-create-home",
		"--shell", "/usr/sbin/nologin", "--home-dir", tenantDir, user); e != nil &&
		!strings.Contains(out, "already exists") {
		return "", fmt.Errorf("useradd %s: %w (%s)", user, e, out)
	}
	if _, e := sys.run("mkdir", "-p", tenantDir); e != nil {
		return "", fmt.Errorf("mkdir %s: %w", tenantDir, e)
	}
	if _, e := sys.run("chown", "-R", user+":"+user, tenantDir); e != nil {
		return "", fmt.Errorf("chown %s: %w", tenantDir, e)
	}
	if _, e := sys.run("chmod", "0700", tenantDir); e != nil {
		return "", fmt.Errorf("chmod %s: %w", tenantDir, e)
	}
	return tenantDir, nil
}
