package main

// helper.go — the privilege-separated root helper (docs step 4).
//
// On an operator/multi-tenant fleet node we want the agent itself to run fully
// unprivileged with NoNewPrivileges=true (so a tenant escape can never re-gain
// root via sudo). But the agent legitimately needs a SMALL set of privileged
// operations: install packages, manage yaver/docker services, and create/wipe
// per-tenant OS users. This helper is the only thing that runs as root: it
// exposes a FIXED, validated RPC surface over a local Unix socket and refuses
// everything else. The agent asks the helper instead of shelling `sudo`.
//
// Security model:
//   - The helper NEVER trusts the client. Every request is re-validated here
//     (package names, service scope, tenant id) regardless of what the agent
//     sends — the agent is the thing we're trying to contain.
//   - Commands are executed by argv (exec.Command), never through a shell, so
//     there is no string-injection surface.
//   - The socket is mode 0660 root:yaver and the accept loop checks the peer's
//     uid (SO_PEERCRED) — only the yaver user (or root) may call.
//   - The verb set === the operator sudoers allowlist (install_privilege.go).
//     Anything outside it (rm, modprobe, ufw, reboot, arbitrary systemctl) is
//     simply not reachable: that's the point — an operator node shouldn't do
//     those, and now it provably can't.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const defaultHelperSocket = "/run/yaver/helper.sock"

// helperRequest is the wire format (one JSON object per connection).
type helperRequest struct {
	Verb    string   `json:"verb"`              // package_install | service | tenant_create | tenant_remove | tenant_shell
	Manager string   `json:"manager,omitempty"` // apt | apt-get | dnf | pacman
	Names   []string `json:"names,omitempty"`   // package names
	Action  string   `json:"action,omitempty"`  // start|stop|restart|enable|disable
	Unit    string   `json:"unit,omitempty"`    // systemd unit
	Tenant  string   `json:"tenant,omitempty"`  // yv-<id>
	// tenant_shell only: launch an interactive login shell AS the tenant and
	// hand the PTY master fd back to the agent over SCM_RIGHTS. This is what
	// lets the agent run with NoNewPrivileges=true (no `sudo -u`).
	Shell string   `json:"shell,omitempty"`
	Env   []string `json:"env,omitempty"` // KEY=VALUE overlay (sanitized here)
	Cwd   string   `json:"cwd,omitempty"`
	Root  string   `json:"root,omitempty"` // tenant_create override, e.g. /srv/yaver/tenants/<id>
	Home  string   `json:"home,omitempty"` // tenant_create override, e.g. <root>/home
}

type helperResponse struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// helperServer holds the profile + an injectable executor (so tests can verify
// dispatch + validation without actually running root commands).
type helperServer struct {
	profile privilegeProfile
	exec    func(name string, args ...string) (string, error)
}

func newHelperServer(profile privilegeProfile) *helperServer {
	return &helperServer{
		profile: profile,
		exec: func(name string, args ...string) (string, error) {
			out, err := exec.Command(name, args...).CombinedOutput()
			return strings.TrimSpace(string(out)), err
		},
	}
}

// ---- validators (pure, security-critical, unit-tested) --------------------

var (
	rePackage = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]*$`)
	reTenant  = regexp.MustCompile(`^yv-[a-z0-9]{1,12}$`)
	reUnit    = regexp.MustCompile(`^[a-zA-Z0-9@._-]+(\.service)?$`)
	reEnvKey  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// allowedShells bounds tenant_shell to known login shells (absolute paths only).
var allowedShells = map[string]bool{
	"/bin/bash": true, "/usr/bin/bash": true,
	"/bin/sh": true, "/usr/bin/sh": true,
	"/bin/zsh": true, "/usr/bin/zsh": true,
	"/bin/dash": true, "/usr/bin/dash": true,
	"/usr/bin/fish": true,
}

func validShell(shell string) error {
	if !allowedShells[shell] {
		return fmt.Errorf("disallowed shell %q", shell)
	}
	return nil
}

// sanitizeTenantEnv validates a KEY=VALUE overlay before it is handed to a
// root-spawned (then uid-dropped) shell: keys must be conventional env names,
// values must carry no NUL/newline. Rejects the whole batch on any bad entry.
func sanitizeTenantEnv(env []string) ([]string, error) {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return nil, fmt.Errorf("malformed env entry %q", kv)
		}
		k, v := kv[:i], kv[i+1:]
		if !reEnvKey.MatchString(k) {
			return nil, fmt.Errorf("invalid env key %q", k)
		}
		if strings.ContainsAny(v, "\x00\n\r") {
			return nil, fmt.Errorf("env value for %q has control characters", k)
		}
		out = append(out, kv)
	}
	return out, nil
}

var serviceActions = map[string]bool{
	"start": true, "stop": true, "restart": true, "enable": true, "disable": true,
}

// packageInstallArgv validates the manager + names and returns the full argv to
// exec (manager binary + install subcommand + sanitized names). Names are
// strict: no shell metacharacters, no flags (can't sneak `-t` or `./evil.deb`).
func packageInstallArgv(manager string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("no package names")
	}
	if len(names) > 50 {
		return nil, fmt.Errorf("too many packages (%d)", len(names))
	}
	for _, n := range names {
		if len(n) == 0 || len(n) > 100 || !rePackage.MatchString(n) {
			return nil, fmt.Errorf("invalid package name %q", n)
		}
	}
	var prefix []string
	switch manager {
	case "apt", "apt-get":
		prefix = []string{manager, "install", "-y"}
	case "dnf":
		prefix = []string{"dnf", "install", "-y"}
	case "pacman":
		prefix = []string{"pacman", "-S", "--noconfirm"}
	default:
		return nil, fmt.Errorf("unsupported package manager %q", manager)
	}
	return append(prefix, names...), nil
}

// serviceAllowed enforces the per-profile service scope. Operator nodes may
// only touch yaver* units + docker (never sshd or a tenant's adjacent
// service); self-host may manage any well-formed unit (it's the owner's box).
func (s *helperServer) serviceAllowed(action, unit string) error {
	if !serviceActions[action] {
		return fmt.Errorf("disallowed service action %q", action)
	}
	if strings.ContainsAny(unit, "/ \t") || strings.Contains(unit, "..") || !reUnit.MatchString(unit) {
		return fmt.Errorf("invalid unit %q", unit)
	}
	if s.profile == profileOperator {
		base := strings.TrimSuffix(unit, ".service")
		if !strings.HasPrefix(base, "yaver") && base != "docker" {
			return fmt.Errorf("operator helper refuses unit %q (only yaver*/docker)", unit)
		}
	}
	return nil
}

func validTenant(name string) error {
	if !reTenant.MatchString(name) {
		return fmt.Errorf("invalid tenant name %q (want yv-<≤12 alnum>)", name)
	}
	return nil
}

// ---- dispatch -------------------------------------------------------------

func (s *helperServer) handle(req helperRequest) helperResponse {
	switch req.Verb {
	case "package_install":
		argv, err := packageInstallArgv(req.Manager, req.Names)
		if err != nil {
			return helperResponse{Error: err.Error()}
		}
		out, err := s.exec(argv[0], argv[1:]...)
		return resp(out, err)

	case "service":
		if err := s.serviceAllowed(req.Action, req.Unit); err != nil {
			return helperResponse{Error: err.Error()}
		}
		out, err := s.exec("systemctl", req.Action, req.Unit)
		return resp(out, err)

	case "tenant_create":
		if err := validTenant(req.Tenant); err != nil {
			return helperResponse{Error: err.Error()}
		}
		home := "/home/" + req.Tenant
		root := home
		if strings.TrimSpace(req.Home) != "" {
			home = filepath.Clean(req.Home)
		}
		if strings.TrimSpace(req.Root) != "" {
			root = filepath.Clean(req.Root)
		}
		if !filepath.IsAbs(root) || !filepath.IsAbs(home) || (home != root && !strings.HasPrefix(home, root+string(filepath.Separator))) {
			return helperResponse{Error: "tenant root/home must be absolute and home must be under root"}
		}
		if root != "/home/"+req.Tenant {
			base := filepath.Clean(betaTenantRoot)
			rel, relErr := filepath.Rel(base, root)
			if relErr != nil || rel == "." || strings.HasPrefix(rel, "..") || strings.Contains(rel, string(filepath.Separator)) {
				return helperResponse{Error: "tenant root must be the canonical /home/yv-* or /srv/yaver/tenants/<id> path"}
			}
			for _, r := range rel {
				if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
					return helperResponse{Error: "tenant root id contains invalid characters"}
				}
			}
		}
		if _, lookErr := user.Lookup(req.Tenant); lookErr != nil {
			if out, err := s.exec("useradd", "--system", "--no-create-home", "--home-dir", home, "--shell", "/bin/bash", req.Tenant); err != nil {
				return resp(out, err)
			}
		}
		dirs := []string{root, home, filepath.Join(root, "workspace"), filepath.Join(home, ".claude"), filepath.Join(home, ".codex")}
		if root == home {
			dirs = append(dirs, filepath.Join(home, "Workspace"))
		}
		for _, dir := range dirs {
			if out, err := s.exec("install", "-d", "-o", req.Tenant, "-g", req.Tenant, "-m", "0700", dir); err != nil {
				return resp(out, err)
			}
		}
		return helperResponse{OK: true}

	case "tenant_remove":
		if err := validTenant(req.Tenant); err != nil {
			return helperResponse{Error: err.Error()}
		}
		if _, lookErr := user.Lookup(req.Tenant); lookErr != nil {
			return helperResponse{OK: true} // already gone
		}
		_, _ = s.exec("pkill", "-KILL", "-u", req.Tenant) // best-effort
		out, err := s.exec("userdel", "-r", req.Tenant)
		return resp(out, err)

	default:
		return helperResponse{Error: fmt.Sprintf("unknown verb %q", req.Verb)}
	}
}

func resp(out string, err error) helperResponse {
	if err != nil {
		return helperResponse{OK: false, Output: out, Error: err.Error()}
	}
	return helperResponse{OK: true, Output: out}
}

// ---- server loop ----------------------------------------------------------

// runPrivilegedHelper is the `yaver __privileged-helper` subcommand. It listens
// on the Unix socket as root and serves validated requests until killed.
func runPrivilegedHelper(args []string) {
	socket := defaultHelperSocket
	profile := profileSelfHost
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--socket":
			if i+1 < len(args) {
				socket = args[i+1]
				i++
			}
		case "--operator":
			profile = profileOperator
		}
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "yaver __privileged-helper must run as root")
		os.Exit(1)
	}

	_ = os.Remove(socket) // clear a stale socket from an unclean shutdown
	ln, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper: listen %s: %v\n", socket, err)
		os.Exit(1)
	}
	defer ln.Close()

	// Lock the socket down to root:yaver 0660 so only the agent user can call.
	if gid, ok := lookupGID(yaverSystemUser); ok {
		_ = os.Chown(socket, 0, gid)
	}
	_ = os.Chmod(socket, 0660)

	srv := newHelperServer(profile)
	fmt.Printf("yaver helper listening on %s (profile=%d)\n", socket, profile)
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go srv.serveConn(conn)
	}
}

func (s *helperServer) serveConn(conn net.Conn) {
	defer conn.Close()
	// Peer-credential gate: only the yaver user or root may call.
	uid, err := peerUID(conn)
	if err != nil {
		writeResp(conn, helperResponse{Error: "peer credential check failed"})
		return
	}
	if uid != 0 {
		if want, ok := lookupUID(yaverSystemUser); !ok || uint32(want) != uid {
			writeResp(conn, helperResponse{Error: "unauthorized peer uid"})
			return
		}
	}

	var req helperRequest
	dec := json.NewDecoder(bufio.NewReader(conn))
	if err := dec.Decode(&req); err != nil {
		writeResp(conn, helperResponse{Error: "bad request: " + err.Error()})
		return
	}

	// tenant_shell is special: it returns a PTY master fd over SCM_RIGHTS, so it
	// needs the *net.UnixConn rather than the generic JSON reply path.
	if req.Verb == "tenant_shell" {
		uc, ok := conn.(*net.UnixConn)
		if !ok {
			writeResp(conn, helperResponse{Error: "tenant_shell requires a unix socket"})
			return
		}
		s.serveTenantShell(uc, req)
		return
	}
	writeResp(conn, s.handle(req))
}

func writeResp(conn net.Conn, r helperResponse) {
	b, _ := json.Marshal(r)
	_, _ = conn.Write(append(b, '\n'))
}

func lookupGID(name string) (int, bool) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, false
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, false
	}
	return gid, true
}

func lookupUID(name string) (int, bool) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, false
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, false
	}
	return uid, true
}
