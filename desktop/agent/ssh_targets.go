package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ssh_targets.go — the LOCAL device book that makes `yaver ssh <name>` work
// without depending on the Convex account (offline, not signed in, or the
// box has no relay/Tailscale route). The data model knows each PC's name,
// user, host/ip, port, and SSH identity — so `yaver ssh magara` resolves to
// `ssh -i <key> kivi@10.0.0.45` from local config alone.
//
// Populate it explicitly:
//   yaver ssh add magara kivi@10.0.0.45 --identity ~/.ssh/id_ed25519
//   yaver ssh ls
//   yaver ssh rm magara
// It also auto-learns the host (only) from the account's device rows when
// you're signed in, so the book self-fills over time.

type SSHTarget struct {
	Name         string `json:"name"`
	Host         string `json:"host"` // ip or hostname
	User         string `json:"user,omitempty"`
	Port         int    `json:"port,omitempty"`
	IdentityFile string `json:"identity_file,omitempty"`
	// Password is OPTIONAL and discouraged (plaintext in local config). Only
	// used when set AND `sshpass` is installed; the secure path is a key via
	// IdentityFile / ssh-agent. Kept so the data model is "aware" of it.
	Password string `json:"password,omitempty"`
}

// lookupSSHTarget finds a target by name (case-insensitive), or nil.
func lookupSSHTarget(cfg *Config, name string) *SSHTarget {
	if cfg == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	for i := range cfg.SSHTargets {
		if strings.EqualFold(strings.TrimSpace(cfg.SSHTargets[i].Name), name) {
			return &cfg.SSHTargets[i]
		}
	}
	return nil
}

// upsertSSHTarget replaces an entry with the same name or appends it.
func upsertSSHTarget(cfg *Config, t SSHTarget) {
	for i := range cfg.SSHTargets {
		if strings.EqualFold(strings.TrimSpace(cfg.SSHTargets[i].Name), strings.TrimSpace(t.Name)) {
			cfg.SSHTargets[i] = t
			return
		}
	}
	cfg.SSHTargets = append(cfg.SSHTargets, t)
}

// rememberSSHHost auto-learns a name→host mapping (host only) from the
// account device rows, without clobbering a user/identity/port the user set.
func rememberSSHHost(cfg *Config, name, host string) {
	name = strings.TrimSpace(name)
	host = strings.TrimSpace(host)
	if name == "" || host == "" {
		return
	}
	if t := lookupSSHTarget(cfg, name); t != nil {
		if t.Host == "" {
			t.Host = host
		}
		return
	}
	cfg.SSHTargets = append(cfg.SSHTargets, SSHTarget{Name: name, Host: host})
}

// parseUserHostPort splits "user@host:port" / "host:port" / "host".
func parseUserHostPort(s string) (user, host string, port int) {
	s = strings.TrimSpace(s)
	if at := strings.LastIndex(s, "@"); at >= 0 {
		user = s[:at]
		s = s[at+1:]
	}
	if c := strings.LastIndex(s, ":"); c >= 0 {
		if p, err := strconv.Atoi(s[c+1:]); err == nil {
			port = p
			s = s[:c]
		}
	}
	host = s
	return
}

// sshArgsFor builds the ssh argv for a local target: optional sshpass
// prefix, identity, port, then user@host. passthrough is appended.
func sshArgsFor(t *SSHTarget, sshPath string, passthrough []string) []string {
	var argv []string
	if strings.TrimSpace(t.Password) != "" {
		if sp, err := exec.LookPath("sshpass"); err == nil {
			argv = append(argv, sp, "-p", t.Password)
		} else {
			fmt.Fprintln(os.Stderr, "→ yaver ssh: password set for this target but `sshpass` isn't installed; relying on key/agent. (brew install sshpass / apt install sshpass)")
		}
	}
	argv = append(argv, sshPath)
	if strings.TrimSpace(t.IdentityFile) != "" {
		argv = append(argv, "-i", expandHome(t.IdentityFile))
	}
	if t.Port > 0 {
		argv = append(argv, "-p", strconv.Itoa(t.Port))
	}
	dest := t.Host
	if strings.TrimSpace(t.User) != "" {
		dest = t.User + "@" + t.Host
	}
	argv = append(argv, dest)
	argv = append(argv, passthrough...)
	return argv
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// runSSHTargetSubcommand handles `yaver ssh add|ls|list|rm <…>`. Returns
// true if it handled the command (caller should then return).
func runSSHTargetSubcommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "add":
		// yaver ssh add <name> <user@host[:port]> [--identity <key>] [--password <pw>]
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: yaver ssh add <name> <user@host[:port]> [--identity <key>] [--password <pw>]")
			os.Exit(2)
		}
		name := args[1]
		user, host, port := parseUserHostPort(args[2])
		t := SSHTarget{Name: name, Host: host, User: user, Port: port}
		for i := 3; i < len(args)-1; i++ {
			switch args[i] {
			case "--identity", "-i":
				t.IdentityFile = args[i+1]
				i++
			case "--password":
				t.Password = args[i+1]
				i++
			case "--port", "-p":
				if p, err := strconv.Atoi(args[i+1]); err == nil {
					t.Port = p
				}
				i++
			}
		}
		if t.Host == "" {
			fmt.Fprintln(os.Stderr, "yaver ssh add: a host is required (user@host)")
			os.Exit(2)
		}
		cfg, err := LoadConfig()
		if err != nil || cfg == nil {
			cfg = &Config{}
		}
		upsertSSHTarget(cfg, t)
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "save: %v\n", err)
			os.Exit(1)
		}
		where := t.Host
		if t.User != "" {
			where = t.User + "@" + t.Host
		}
		fmt.Printf("✓ saved ssh target %q → %s\n", t.Name, where)
		return true
	case "ls", "list":
		cfg, _ := LoadConfig()
		if cfg == nil || len(cfg.SSHTargets) == 0 {
			fmt.Println("No saved ssh targets. Add one: yaver ssh add <name> <user@host>")
			return true
		}
		for _, t := range cfg.SSHTargets {
			dest := t.Host
			if t.User != "" {
				dest = t.User + "@" + t.Host
			}
			extra := ""
			if t.Port > 0 {
				extra += fmt.Sprintf(" :%d", t.Port)
			}
			if t.IdentityFile != "" {
				extra += " (key)"
			}
			fmt.Printf("  %-20s %s%s\n", t.Name, dest, extra)
		}
		return true
	case "rm", "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver ssh rm <name>")
			os.Exit(2)
		}
		cfg, _ := LoadConfig()
		if cfg == nil {
			return true
		}
		name := args[1]
		out := cfg.SSHTargets[:0]
		removed := false
		for _, t := range cfg.SSHTargets {
			if strings.EqualFold(strings.TrimSpace(t.Name), strings.TrimSpace(name)) {
				removed = true
				continue
			}
			out = append(out, t)
		}
		cfg.SSHTargets = out
		_ = SaveConfig(cfg)
		if removed {
			fmt.Printf("✓ removed ssh target %q\n", name)
		} else {
			fmt.Printf("no ssh target named %q\n", name)
		}
		return true
	}
	return false
}
