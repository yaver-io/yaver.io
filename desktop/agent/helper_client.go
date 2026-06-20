package main

// helper_client.go — agent-side client for the privilege-separated helper.
//
// When the helper socket is present (operator-confined nodes), privileged
// operations route through it so the agent itself needs no sudo and can run
// with NoNewPrivileges=true. When it is absent (personal machine, self-host
// dedicated box), these shims fall back to the scoped-sudo path that exists
// today — so nothing regresses where the helper isn't installed.

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"time"
)

// helperSocketPath resolves the socket, honoring an env override for tests /
// non-default layouts.
func helperSocketPath() string {
	if p := os.Getenv("YAVER_HELPER_SOCKET"); p != "" {
		return p
	}
	return defaultHelperSocket
}

// helperAvailable reports whether the privileged helper socket exists.
func helperAvailable() bool {
	p := helperSocketPath()
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeSocket != 0
}

// helperCall sends one request and returns the response.
func helperCall(req helperRequest) helperResponse {
	conn, err := net.DialTimeout("unix", helperSocketPath(), 5*time.Second)
	if err != nil {
		return helperResponse{Error: "helper dial: " + err.Error()}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(120 * time.Second))

	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return helperResponse{Error: "helper write: " + err.Error()}
	}
	var resp helperResponse
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return helperResponse{Error: "helper read: " + err.Error()}
	}
	return resp
}

// privilegedSystemctl manages a systemd unit: helper-first, sudo fallback.
func privilegedSystemctl(action, unit string) (string, error) {
	if helperAvailable() {
		r := helperCall(helperRequest{Verb: "service", Action: action, Unit: unit})
		if r.OK {
			return r.Output, nil
		}
		return r.Output, errFromResp(r)
	}
	return runCmd("sudo", "systemctl", action, unit)
}

// privilegedPackageInstall installs packages: helper-first, sudo fallback.
func privilegedPackageInstall(manager string, names []string) (string, error) {
	if helperAvailable() {
		r := helperCall(helperRequest{Verb: "package_install", Manager: manager, Names: names})
		if r.OK {
			return r.Output, nil
		}
		return r.Output, errFromResp(r)
	}
	args := append([]string{manager}, packageInstallSudoArgs(manager, names)...)
	return runCmd("sudo", args...)
}

// privilegedTenantCreate / Remove route the operator tenant lifecycle through
// the helper when present. Returns (handled, error): handled=false means no
// helper, so the caller should use its sudo path.
func privilegedTenantCreate(tenant string) (bool, error) {
	if !helperAvailable() {
		return false, nil
	}
	r := helperCall(helperRequest{Verb: "tenant_create", Tenant: tenant})
	if r.OK {
		return true, nil
	}
	return true, errFromResp(r)
}

func privilegedTenantRemove(tenant string) (bool, error) {
	if !helperAvailable() {
		return false, nil
	}
	r := helperCall(helperRequest{Verb: "tenant_remove", Tenant: tenant})
	if r.OK {
		return true, nil
	}
	return true, errFromResp(r)
}

func errFromResp(r helperResponse) error {
	if r.Error != "" {
		return &helperError{r.Error}
	}
	return &helperError{"helper call failed"}
}

type helperError struct{ msg string }

func (e *helperError) Error() string { return e.msg }

// packageInstallSudoArgs mirrors the manager-specific install argv used on the
// non-helper sudo fallback path.
func packageInstallSudoArgs(manager string, names []string) []string {
	switch manager {
	case "dnf":
		return append([]string{"install", "-y"}, names...)
	case "pacman":
		return append([]string{"-S", "--noconfirm"}, names...)
	default: // apt / apt-get
		return append([]string{"install", "-y"}, names...)
	}
}
