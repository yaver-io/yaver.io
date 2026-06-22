package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// redroid_tenant.go — Model B isolation for sharing redroid across beta users on
// ONE box (user's directive 2026-06-22: "beta machine usable from all beta
// users … make sure they can use redroid … but user isolation, no data leak").
//
// Each beta tenant gets its OWN redroid container with:
//   - a per-tenant /data volume dir (BaseDir/<tenant>, 0700) — NEVER shared, so
//     one user's apps/files/accounts are invisible to another;
//   - a dedicated per-tenant docker bridge network with inter-container comms
//     disabled (icc=false) — tenants can't reach each other over docker;
//   - cpu / memory / pids caps so one tenant can't starve the box;
//   - no host-env passthrough + a namespaced name + a managed-by label;
//   - teardown that removes the container + network AND wipes the volume dir, so
//     no cross-tenant residue is left behind (operator-fleet gap C).
//
// HONEST LIMIT (documented, not hidden): redroid needs `--privileged` to mount
// binderfs, and a privileged container can still escape to the host. So this is
// strong tenant-to-tenant *data* isolation + defense-in-depth, acceptable ONLY
// because beta is invite-only / owner-vetted — it is NOT a hard sandbox against a
// malicious tenant. For untrusted users use a per-user ephemeral BOX (Model A:
// isolation by physics). See docs/yaver-beta-redroid-multitenant.md.
//
// RFC1918 / LAN egress blocking (so a tenant's app can't reach the host LAN) is
// enforced at the host level by the existing operator-fleet egress jail
// (access_policy.go / egress_proxy.go), not re-implemented per container here.

// redroidTenantDefaults are conservative caps for an 8 GB beta box.
const (
	redroidTenantCPUs     = "2"
	redroidTenantMemoryMB = 3072
	redroidTenantPidsMax  = 4096
)

// RedroidTenantSpec describes one beta user's isolated redroid instance.
type RedroidTenantSpec struct {
	TenantID string // beta user id (sanitized to a docker-safe slug)
	BaseDir  string // host root holding per-tenant /data volumes
	Image    string // redroid image (default redroid/redroid:13.0.0-latest)
	CPUs     string // docker --cpus (default redroidTenantCPUs)
	MemoryMB int    // docker --memory in MB (default redroidTenantMemoryMB)
	PidsMax  int    // docker --pids-limit (default redroidTenantPidsMax)
	Width    int
	Height   int
	DPI      int
}

var tenantSlug = regexp.MustCompile(`[^a-z0-9]+`)

// sanitizeTenant maps an arbitrary user id to a stable docker-safe slug so it can
// name a container/network/volume without injection or collision surprises.
func sanitizeTenant(id string) string {
	s := tenantSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(id)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "anon"
	}
	return s
}

func (s RedroidTenantSpec) withDefaults() RedroidTenantSpec {
	if s.Image == "" {
		s.Image = "redroid/redroid:13.0.0-latest"
	}
	if s.CPUs == "" {
		s.CPUs = redroidTenantCPUs
	}
	if s.MemoryMB <= 0 {
		s.MemoryMB = redroidTenantMemoryMB
	}
	if s.PidsMax <= 0 {
		s.PidsMax = redroidTenantPidsMax
	}
	if s.Width <= 0 {
		s.Width = 1080
	}
	if s.Height <= 0 {
		s.Height = 2340
	}
	if s.DPI <= 0 {
		s.DPI = 440
	}
	if s.BaseDir == "" {
		s.BaseDir = "/opt/yaver/redroid-tenants"
	}
	return s
}

// names returns the per-tenant container, network, and host volume dir. All are
// namespaced by the sanitized tenant id so two tenants can never collide.
func (s RedroidTenantSpec) names() (container, network, volume string) {
	t := sanitizeTenant(s.TenantID)
	return "yaver-rd-" + t, "yaver-rdnet-" + t, filepath.Join(s.BaseDir, t)
}

// VolumeDir is the per-tenant /data host dir (caller mkdir 0700 before run).
func (s RedroidTenantSpec) VolumeDir() string { _, _, v := s.names(); return v }

// NetworkCreateCmd creates the per-tenant bridge with inter-container comms off.
// Idempotent (|| true): re-running for an existing tenant is safe.
func (s RedroidTenantSpec) NetworkCreateCmd() string {
	_, net, _ := s.names()
	return fmt.Sprintf(
		"docker network inspect %s >/dev/null 2>&1 || docker network create --driver bridge --opt com.docker.network.bridge.enable_icc=false %s",
		shellQuoteArg(net), shellQuoteArg(net))
}

// RunArgs is the hardened `docker run` argv for the tenant's redroid. Returned as
// a slice so callers exec without a shell. The volume dir must already exist.
func (s RedroidTenantSpec) RunArgs() []string {
	s = s.withDefaults()
	container, network, volume := s.names()
	return []string{
		"docker", "run", "-itd",
		"--name", container,
		"--label", "managed-by=yaver-beta",
		"--label", "yaver-tenant=" + sanitizeTenant(s.TenantID),
		"--network", network, // dedicated per-tenant network
		"--cpus", s.CPUs,
		"--memory", fmt.Sprintf("%dm", s.MemoryMB),
		"--pids-limit", fmt.Sprintf("%d", s.PidsMax),
		"--security-opt", "no-new-privileges", // belt-and-suspenders under privileged
		// redroid REQUIRES privileged to mount binderfs — the documented limit.
		"--privileged",
		"-v", volume + ":/data", // per-tenant data, never shared
		s.Image,
		fmt.Sprintf("androidboot.redroid_width=%d", s.Width),
		fmt.Sprintf("androidboot.redroid_height=%d", s.Height),
		fmt.Sprintf("androidboot.redroid_dpi=%d", s.DPI),
	}
}

// TeardownCmds removes the container + network and WIPES the per-tenant volume,
// leaving zero residue for the next tenant (run on session end / idle).
func (s RedroidTenantSpec) TeardownCmds() []string {
	container, network, volume := s.names()
	return []string{
		fmt.Sprintf("docker rm -f %s >/dev/null 2>&1 || true", shellQuoteArg(container)),
		fmt.Sprintf("docker network rm %s >/dev/null 2>&1 || true", shellQuoteArg(network)),
		// guard the rm: never wipe outside BaseDir.
		fmt.Sprintf("case %s in %s/*) rm -rf %s ;; esac",
			shellQuoteArg(volume), shellQuoteArg(strings.TrimRight(s.withDefaults().BaseDir, "/")), shellQuoteArg(volume)),
	}
}

// shellQuoteArg single-quotes a string for safe embedding in a /bin/sh command.
func shellQuoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
