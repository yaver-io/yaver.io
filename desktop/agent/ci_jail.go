package main

// ci_jail.go — container hardening + network jail for untrusted CI jobs
// (operator-fleet gap C). The per-job sandbox runs ephemerally with dropped
// capabilities, no privilege escalation, and pid/memory caps so a malicious or
// runaway job can't pivot or exhaust a shared/operator box.
//
// The RELAY-ONLY / RFC1918-block half of the jail is a HOST-LEVEL network the
// operator-fleet provisioning creates once (YAVER_CI_JAIL_NETWORK); CI
// containers join it when set. On a private box it's unset and the user's own
// network is used (the user opted into container mode for process/FS isolation,
// not network isolation — their LAN is theirs). See
// docs/yaver-public-compute-operator-fleet.md (the jail) +
// docs/yaver-managed-cloud-ci-absorption.md.

import (
	"os"
	"strings"
)

// ciJailNetwork is the operator-fleet jailed docker network name (egress to
// RFC1918 blocked), or "" for the default bridge. Reads the env override first,
// then the marker written by `ci_jail_setup` (so the jail persists across agent
// restarts without an env var).
func ciJailNetwork() string {
	if v := strings.TrimSpace(os.Getenv("YAVER_CI_JAIL_NETWORK")); v != "" {
		return v
	}
	return readCIJailMarker()
}

func ciContainerPidsLimit() string {
	if v := os.Getenv("YAVER_CI_PIDS_LIMIT"); v != "" {
		return v
	}
	return "1024"
}

func ciContainerMemLimit() string {
	if v := os.Getenv("YAVER_CI_MEM_LIMIT"); v != "" {
		return v
	}
	return "6g"
}

// ciDockerHardeningArgs returns the `docker run` flags that sandbox an
// untrusted CI job: ephemeral (--rm), no capabilities, no privilege
// escalation, pid + memory caps, and the jail network when configured. Inserted
// right after `run` and before the image.
func ciDockerHardeningArgs() []string {
	args := []string{
		"--rm",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", ciContainerPidsLimit(),
		"--memory", ciContainerMemLimit(),
	}
	if net := ciJailNetwork(); net != "" {
		args = append(args, "--network", net)
	}
	return args
}

// scrubGitHubRunnerCreds removes the registration credentials the GitHub runner
// writes into its dir, so a later run (or a snoop) on a shared box can't read
// the previous job's runner token. `--ephemeral` deregisters server-side; this
// is the local-disk half.
func scrubGitHubRunnerCreds(runnerDir string) {
	if runnerDir == "" {
		return
	}
	for _, f := range []string{".runner", ".credentials", ".credentials_rsaparams", ".env", ".path"} {
		_ = os.Remove(runnerDir + string(os.PathSeparator) + f)
	}
	_ = os.RemoveAll(runnerDir + string(os.PathSeparator) + "_diag")
}
