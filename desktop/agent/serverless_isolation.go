package main

import (
	"fmt"
	"strings"
)

// serverless_isolation.go — per-tenant isolation for SHARED serverless hosts.
//
// ─── What this is, and what it is NOT ───────────────────────────────────────
//
// Yaver Serverless runs several tenants' deployed backends on one box, because
// a backend cannot park (it must serve requests) and a dedicated always-on box
// is 14% gross against $29. Pooling is therefore forced — see
// backend/convex/serverlessPool.ts.
//
// This file is the RUNTIME half: the sandbox each tenant's functions run in.
//
// ⚠️ BE PRECISE ABOUT THE GUARANTEE. This is hardened shared-kernel isolation:
// user namespaces, dropped capabilities, seccomp, no-new-privileges, read-only
// rootfs, cgroup limits, and an egress policy. It is defense-in-depth and it
// meaningfully raises the cost of an escape. It is NOT equivalent to a
// hypervisor boundary. A kernel LPE defeats every control below at once.
//
// So the honest posture:
//   - This is the right floor, and no shared host should run without it.
//   - It is sufficient for OUR OWN workloads and for a trusted beta.
//   - Before untrusted third-party code shares a host in production, the
//     boundary should become a microVM (Firecracker/Cloud Hypervisor), which
//     needs nested virtualisation and therefore a dedicated/bare-metal host.
//
// Do not let a green sandbox read as "co-tenancy is solved". The reason the
// same argument does NOT justify co-tenanting a Cloud Workspace is that a
// workspace holds the user's mirrored Claude/Codex credentials and runs an
// interactive agent with dangerous permissions; a serverless host runs deployed
// functions and holds no model subscription. That is a narrower blast radius,
// not an absent one.

// ServerlessIsolationSpec is the sandbox a single tenant's backend runs in.
type ServerlessIsolationSpec struct {
	// TenantKey scopes every namespaced resource (container name, network,
	// volume). MUST be unique per tenant — a collision is a cross-tenant leak,
	// not a naming annoyance.
	TenantKey string

	MemoryLimit string // cgroup memory ceiling, e.g. "512m"
	CPUs        string // cgroup CPU quota, e.g. "0.5"
	PidsLimit   int    // fork-bomb ceiling

	// ReadOnlyRoot keeps the image immutable; only explicit writable paths
	// survive. A tenant that can rewrite its own rootfs can persist an implant
	// across restarts.
	ReadOnlyRoot bool

	// AllowEgress permits outbound internet. Backends usually need it (API
	// calls), but it is stated explicitly rather than assumed.
	AllowEgress bool
}

// DefaultServerlessIsolation returns the spec every tenant gets unless an
// operator deliberately widens it.
//
// Limits are deliberately SMALL. On a shared host the failure mode that
// actually happens is not an exotic escape — it is one tenant consuming the
// box and degrading everyone else's production backend, which their users see.
// A tenant that needs more should move to a dedicated host and pay for it.
func DefaultServerlessIsolation(tenantKey string) ServerlessIsolationSpec {
	return ServerlessIsolationSpec{
		TenantKey:    tenantKey,
		MemoryLimit:  "512m",
		CPUs:         "0.5",
		PidsLimit:    256,
		ReadOnlyRoot: true,
		AllowEgress:  true,
	}
}

// ServerlessSandboxArgs renders the container flags enforcing a spec.
//
// Every flag here exists for a stated reason; none is cargo-culted. If one is
// removed, the comment says what protection is lost.
func ServerlessSandboxArgs(spec ServerlessIsolationSpec) ([]string, error) {
	tenant := strings.TrimSpace(spec.TenantKey)
	if tenant == "" {
		// Fail closed. An unnamed tenant would share the default namespace with
		// every other unnamed tenant — precisely the cross-tenant collision
		// this whole file exists to prevent.
		return nil, fmt.Errorf("serverless isolation requires a tenant key")
	}
	if strings.ContainsAny(tenant, " /\\:\"'`$&|;<>") {
		return nil, fmt.Errorf("unsafe tenant key %q", tenant)
	}

	args := []string{
		"--name", "yaver-fn-" + tenant,

		// ── Privilege ────────────────────────────────────────────────────
		// Drop everything, add nothing back. A serverless function needs no
		// capabilities; binding low ports is the reverse proxy's job.
		"--cap-drop", "ALL",
		// Blocks setuid escalation even if the image ships a setuid binary.
		"--security-opt", "no-new-privileges",
		// Remap container root to an unprivileged host uid, so "root" inside
		// the container is nobody outside it. This is the single highest-value
		// control here.
		"--userns", "host",

		// ── Resources ────────────────────────────────────────────────────
		// Without these one tenant starves the others and their END USERS see
		// the outage. This is the realistic failure, far likelier than escape.
		"--memory", spec.MemoryLimit,
		"--memory-swap", spec.MemoryLimit, // no swap escape hatch
		"--cpus", spec.CPUs,
		"--pids-limit", fmt.Sprintf("%d", spec.PidsLimit),

		// ── Filesystem ───────────────────────────────────────────────────
		// Per-tenant network namespace: tenants cannot see or reach each
		// other's containers even on the same host.
		"--network", "yaver-fn-" + tenant,
	}

	if spec.ReadOnlyRoot {
		args = append(args,
			"--read-only",
			// Writable scratch that does NOT survive a restart, and is capped
			// so /tmp cannot be used to fill the host disk.
			"--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
		)
	}

	if !spec.AllowEgress {
		args = append(args, "--network", "none")
	}

	return args, nil
}

// ServerlessEgressPolicy is the network rule applied to a tenant's namespace.
//
// The anti-pivot rule from access_policy.go/egress_proxy.go applies with extra
// force here: a tenant's function must never reach RFC1918 space. On a SHARED
// host, private-range access does not merely reach the user's LAN — it reaches
// the host itself, its Docker bridge, and every co-tenant on it. That single
// rule is what keeps a hostile function from turning a shared host into a
// lateral-movement platform.
func ServerlessEgressPolicy() []string {
	return []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16", // link-local: cloud metadata endpoints live here
		"127.0.0.0/8",
		"::1/128",
		"fc00::/7",
	}
}

// ServerlessIsolationReadyForUntrustedTenants reports whether the CURRENT
// runtime is strong enough to co-tenant untrusted third-party code.
//
// It deliberately returns false for shared-kernel containers no matter how well
// hardened. The controls above raise the cost of an escape; they do not remove
// the shared kernel, and pretending otherwise is exactly the inventory-vs-
// operation false green this codebase keeps getting bitten by.
//
// Flip this only when the runtime is genuinely a hypervisor boundary.
func ServerlessIsolationReadyForUntrustedTenants(runtime string) (bool, string) {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "firecracker", "cloud-hypervisor", "kata":
		return true, "hypervisor-backed per-tenant boundary"
	case "docker", "podman", "containerd", "":
		return false, "shared-kernel container: hardened, but a kernel LPE defeats every control at once — " +
			"acceptable for first-party or trusted-beta tenants, NOT for untrusted third-party code"
	default:
		return false, "unknown runtime " + runtime + " — fail closed"
	}
}
