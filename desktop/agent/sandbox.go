package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SandboxConfig controls command validation before execution.
type SandboxConfig struct {
	Enabled         bool     `json:"enabled"`
	AllowSudo       bool     `json:"allow_sudo,omitempty"`
	AllowedPaths    []string `json:"allowed_paths,omitempty"`
	BlockedCommands []string `json:"blocked_commands,omitempty"`
	MaxOutputSizeMB int      `json:"max_output_size_mb,omitempty"`
}

// DefaultSandboxConfig returns the default command guard. Temporarily disabled:
// Yaver's primary new-app path now assumes an owned remote box / mesh workspace
// with real git + runner state, not a local command sandbox onboarding step.
// An explicit config can still re-enable validation for shared/guest hosts.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Enabled:         false,
		AllowSudo:       false,
		MaxOutputSizeMB: 100,
	}
}

// dangerousPatterns are regex patterns that match dangerous commands.
// Each pattern has a human-readable reason for the block.
var dangerousPatterns = []struct {
	pattern *regexp.Regexp
	reason  string
}{
	// Filesystem destruction — broad recursive removal of critical paths
	// Match rm with any combination of -r, -f, -rf, -fr flags, targeting dangerous paths
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*(/\s*$|/\s)`), "recursive removal of root"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*(~|~/)(\s|$)`), "recursive removal of home directory"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*\$HOME(\s|$|/)`), "recursive removal of $HOME"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*\$\{HOME\}(\s|$|/)`), "recursive removal of ${HOME}"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*/boot(\s|$|/)`), "removal of /boot"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*/etc(\s|$|/)`), "removal of /etc"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*/usr(\s|$|/)`), "removal of /usr"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*/var(\s|$|/)`), "removal of /var"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*/sys(\s|$|/)`), "removal of /sys"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*/proc(\s|$|/)`), "removal of /proc"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*/lib(\s|$|/)`), "removal of /lib"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*/bin(\s|$|/)`), "removal of /bin"},
	{regexp.MustCompile(`\brm\s+(-\w+\s+)*/sbin(\s|$|/)`), "removal of /sbin"},

	// Disk/partition manipulation
	{regexp.MustCompile(`\bmkfs\b`), "filesystem creation on block device"},
	{regexp.MustCompile(`\bdd\s+.*\bof=/dev/`), "raw write to block device"},
	{regexp.MustCompile(`\bfdisk\b`), "partition table manipulation"},
	{regexp.MustCompile(`\bparted\b`), "partition manipulation"},
	{regexp.MustCompile(`\bshred\b`), "secure file destruction"},

	// Encryption/ransomware patterns
	{regexp.MustCompile(`\bgpg\s+.*--encrypt\s+.*(-r|--recipient)\s+.*(/|~|\$HOME)`), "bulk encryption of home/root"},
	{regexp.MustCompile(`\bopenssl\s+enc\s+.*(-in\s+/|-in\s+~)`), "encrypting system files with openssl"},
	{regexp.MustCompile(`\bfind\s+/\s+.*-exec.*openssl\s+enc`), "bulk encryption via find"},
	{regexp.MustCompile(`\bfind\s+/\s+.*-exec.*gpg.*--encrypt`), "bulk encryption via find"},

	// System compromise
	{regexp.MustCompile(`\bchmod\s+(-[a-zA-Z]*R[a-zA-Z]*\s+)*777\s+/\s*$`), "chmod 777 on root"},
	{regexp.MustCompile(`\bchmod\s+(-[a-zA-Z]*R[a-zA-Z]*\s+)*777\s+/etc\b`), "chmod 777 on /etc"},
	{regexp.MustCompile(`\bchown\s+(-[a-zA-Z]*R[a-zA-Z]*\s+)*.*\s+/\s*$`), "chown on root filesystem"},
	{regexp.MustCompile(`>\s*/etc/passwd\b`), "overwriting /etc/passwd"},
	{regexp.MustCompile(`>\s*/etc/shadow\b`), "overwriting /etc/shadow"},
	{regexp.MustCompile(`\bcrontab\s+-r\b`), "removing all cron jobs"},

	// Network exfiltration — piping sensitive files to remote
	{regexp.MustCompile(`\bcurl\b.*\|\s*(ba)?sh\b`), "piping remote content to shell"},
	{regexp.MustCompile(`\bwget\b.*\|\s*(ba)?sh\b`), "piping remote content to shell"},
	{regexp.MustCompile(`\bcurl\b.*-[a-zA-Z]*d\s*@(/etc/passwd|/etc/shadow|~/.ssh)`), "exfiltrating sensitive files via curl"},

	// Process kill-all
	{regexp.MustCompile(`\bkillall\s+-9\b`), "force killing all processes by name"},
	{regexp.MustCompile(`\bpkill\s+-9\s+\.\*`), "force killing all matching processes"},
	{regexp.MustCompile(`\bkill\s+-9\s+-1\b`), "killing all user processes"},

	// Fork bomb
	{regexp.MustCompile(`:\(\)\{.*\|.*&\s*\}`), "fork bomb"},
	{regexp.MustCompile(`\bwhile\s+true.*fork\b`), "fork bomb variant"},

	// Systemd / init system abuse
	{regexp.MustCompile(`\bsystemctl\s+(stop|disable|mask)\s+(sshd|networking|network-manager|firewalld|iptables)\b`), "disabling critical system services"},

	// Kernel module manipulation
	{regexp.MustCompile(`\binsmod\b`), "inserting kernel module"},
	{regexp.MustCompile(`\brmmod\b`), "removing kernel module"},
	{regexp.MustCompile(`\bmodprobe\s+-r\b`), "removing kernel module"},
}

// sudoPattern matches commands starting with sudo, su, or doas.
var sudoPattern = regexp.MustCompile(`^\s*(sudo\b|su\s|doas\b)`)

// dangerousAbsolutePaths are absolute paths whose recursive deletion
// is always refused. Matched case-insensitively so that on
// case-insensitive filesystems (macOS HFS+/APFS default, Windows NTFS)
// `rm -rf /Users` and `rm -rf /users` both get caught. Case-insensitive
// comparison is strictly safer than case-sensitive: on Linux it never
// blocks a legitimate different-case path (those don't collide with
// the dangerous one on the filesystem either), and on macOS/Windows
// it closes a real exploitation gap.
var dangerousAbsolutePaths = []string{
	"/",
	"/Users", // parent of every macOS user home
	"/home",  // parent of every Linux user home
	"/root",
	"/etc",
	"/var",
	"/usr",
	"/bin",
	"/sbin",
	"/boot",
	"/lib",
	"/lib64",
	"/sys",
	"/proc",
	"/opt",
	"/srv",
	"/System",  // macOS
	"/Library", // macOS (user and root)
	"/Applications",
}

// dangerousHomeSubdirs are single-component names that, when they
// appear directly under $HOME, name a directory that an AI agent
// should never recursively delete: either it holds the user's source
// code (Workspace, Projects, repos, ...) or it holds credentials
// (.ssh, .aws, .gnupg). Matched case-insensitively so `~/workspace`
// and `~/Workspace` both resolve to a blocked target on macOS.
var dangerousHomeSubdirs = []string{
	// Common codebase roots
	"Workspace",
	"workspace",
	"Projects",
	"projects",
	"Code",
	"code",
	"repos",
	"Repos",
	"src",
	"Src",
	"dev",
	"Dev",
	"git",
	"Git",
	"Documents",
	"Desktop",
	"Downloads",
	// Credential / config dirs
	".ssh",
	".aws",
	".gnupg",
	".config",
	".yaver",
	".claude",
	".anthropic",
	".codex",
	".openai",
}

// rmCommandRegex detects an rm invocation anywhere in a segment.
// Matches `rm`, `/bin/rm`, `/usr/bin/rm`. Does not require any
// particular flag ordering.
var rmCommandRegex = regexp.MustCompile(`(?i)(^|[\s;&|(])((?:/[^\s]+/)?rm)(\s|$)`)

// resolveShellPath expands the portion of a shell-style path that
// Yaver cares about for dangerous-deletion detection: leading `~`,
// `~/`, `$HOME`, and `${HOME}` are rewritten to the actual home
// directory. It does NOT execute the shell and does NOT expand
// arbitrary variables — only the home-directory forms. The result
// is run through filepath.Clean.
func resolveShellPath(raw string) string {
	p := strings.TrimSpace(raw)
	if len(p) >= 2 {
		if (p[0] == '"' && p[len(p)-1] == '"') || (p[0] == '\'' && p[len(p)-1] == '\'') {
			p = p[1 : len(p)-1]
		}
	}
	if p == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		switch {
		case p == "~":
			p = home
		case strings.HasPrefix(p, "~/"):
			p = filepath.Join(home, p[2:])
		}
		// Expand ${HOME} and $HOME anywhere in the string. We only
		// do this when the literal token appears — we never invoke
		// the shell or dereference other env vars.
		p = strings.ReplaceAll(p, "${HOME}", home)
		// $HOME must be followed by a non-identifier char or end
		// of string to avoid clobbering e.g. $HOMEBREW_PREFIX.
		p = expandHomeVar(p, home)
	}
	return filepath.Clean(p)
}

// expandHomeVar rewrites `$HOME` to home, but only when the `$HOME`
// is not immediately followed by an identifier character.
func expandHomeVar(s, home string) string {
	const needle = "$HOME"
	var out strings.Builder
	for {
		idx := strings.Index(s, needle)
		if idx < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:idx])
		after := idx + len(needle)
		if after < len(s) {
			c := s[after]
			if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
				// e.g. $HOMEBREW_PREFIX — leave as-is.
				out.WriteString(needle)
				s = s[after:]
				continue
			}
		}
		out.WriteString(home)
		s = s[after:]
	}
}

// pathEqualIgnoreCase compares two paths for equality after
// filepath.Clean and ASCII lowercasing. Adequate for the POSIX
// path separators we care about; not a full Unicode case fold.
func pathEqualIgnoreCase(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// isDangerousDeletionTarget returns (true, reason) when `target` —
// after `~`/$HOME expansion and path cleaning — names a directory
// that an AI agent should never recursively delete. The check is
// case-insensitive so macOS's case-insensitive filesystem (where
// `~/workspace` and `~/Workspace` are the same inode) cannot defeat
// it. A direct-child-of-$HOME match is the primary guard against
// the real-world incident where an agent "cleaned up" `~/workspace`
// and wiped `~/Workspace`.
func isDangerousDeletionTarget(target string) (bool, string) {
	if target == "" {
		return false, ""
	}
	resolved := resolveShellPath(target)
	if resolved == "" || resolved == "." {
		return false, ""
	}
	// Absolute system paths (exact match, case-insensitive).
	for _, ancestor := range dangerousAbsolutePaths {
		if pathEqualIgnoreCase(resolved, ancestor) {
			return true, fmt.Sprintf("recursive deletion of system path %s is refused (resolved from %q)", ancestor, target)
		}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return false, ""
	}
	cleanHome := filepath.Clean(home)
	if pathEqualIgnoreCase(resolved, cleanHome) {
		return true, fmt.Sprintf("recursive deletion of $HOME (%s) is refused (resolved from %q)", cleanHome, target)
	}
	for _, sub := range dangerousHomeSubdirs {
		candidate := filepath.Join(cleanHome, sub)
		if pathEqualIgnoreCase(resolved, candidate) {
			return true, fmt.Sprintf("recursive deletion of %s is refused (resolved from %q; case-insensitive filesystem match — this is the class of bug that wipes a user's project tree)", candidate, target)
		}
	}
	return false, ""
}

// extractDeletionTargets returns every path argument passed to an
// `rm` invocation that carries a recursive flag (-r / -R, in any
// combination with other flags). Splits on shell command separators
// before analysing so chained commands are covered.
func extractDeletionTargets(cmd string) []string {
	var targets []string
	for _, seg := range splitShellSegments(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" || !rmCommandRegex.MatchString(seg) {
			continue
		}
		tokens := tokenizeShellArgs(seg)
		seenRm := false
		hasRecursive := false
		var nonFlagArgs []string
		for _, t := range tokens {
			if !seenRm {
				base := strings.ToLower(t)
				if base == "rm" || strings.HasSuffix(base, "/rm") {
					seenRm = true
				}
				continue
			}
			if strings.HasPrefix(t, "--") {
				// GNU long flags (`--force`, `--recursive`, `--`) —
				// `--recursive` is the only one that implies -r.
				if strings.EqualFold(t, "--recursive") {
					hasRecursive = true
				}
				continue
			}
			if strings.HasPrefix(t, "-") {
				if strings.ContainsAny(t, "rR") {
					hasRecursive = true
				}
				continue
			}
			nonFlagArgs = append(nonFlagArgs, t)
		}
		if hasRecursive {
			targets = append(targets, nonFlagArgs...)
		}
	}
	return targets
}

// splitShellSegments breaks a command line on the common shell
// separators (`;`, `&&`, `||`, `|`) so each segment can be analysed
// independently. Not a full shell parser — quoted separators leak
// through, but the worst case is an extra segment that fails
// downstream extraction cleanly.
func splitShellSegments(cmd string) []string {
	parts := []string{cmd}
	for _, sep := range []string{"&&", "||", ";", "|"} {
		var next []string
		for _, p := range parts {
			for _, piece := range strings.Split(p, sep) {
				piece = strings.TrimSpace(piece)
				if piece != "" {
					next = append(next, piece)
				}
			}
		}
		parts = next
	}
	return parts
}

// tokenizeShellArgs splits a single command segment on whitespace,
// respecting balanced single and double quotes. Backslash escapes
// are not interpreted — they pass through as literal characters.
func tokenizeShellArgs(s string) []string {
	var out []string
	var buf strings.Builder
	var quote byte
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, buf.String())
			buf.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				continue
			}
			buf.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	return out
}

// ValidateCommand checks a command string against the sandbox rules.
// Returns nil if the command is allowed, or an error describing why it was blocked.
func ValidateCommand(cmd string, cfg SandboxConfig) error {
	if !cfg.Enabled {
		return nil
	}

	// Normalize: collapse whitespace, trim
	normalized := strings.TrimSpace(cmd)
	if normalized == "" {
		return nil
	}

	// Check sudo/su/doas
	if !cfg.AllowSudo && sudoPattern.MatchString(normalized) {
		return fmt.Errorf("sandbox: blocked privilege escalation (%s) — enable with sandbox.allow_sudo=true", extractFirstWord(normalized))
	}

	// Check user-defined blocked commands
	for _, blocked := range cfg.BlockedCommands {
		if strings.Contains(normalized, blocked) {
			return fmt.Errorf("sandbox: blocked by custom rule — command contains '%s'", blocked)
		}
	}

	// Resolve and case-fold every recursive-rm target against the
	// deny-list. This runs before the legacy regex patterns because
	// its error messages name the exact resolved path, which is
	// more useful than "recursive removal of home directory" when
	// the offending token was `/Users/kivanc/workspace` rather than
	// a literal `~`.
	for _, target := range extractDeletionTargets(normalized) {
		if blocked, reason := isDangerousDeletionTarget(target); blocked {
			return fmt.Errorf("sandbox: %s", reason)
		}
	}

	// Check dangerous patterns
	for _, dp := range dangerousPatterns {
		if dp.pattern.MatchString(normalized) {
			return fmt.Errorf("sandbox: blocked dangerous command — %s", dp.reason)
		}
	}

	// For piped commands, validate each segment
	if strings.Contains(normalized, "|") {
		segments := strings.Split(normalized, "|")
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			if err := validateSegment(seg, cfg); err != nil {
				return err
			}
		}
	}

	// For chained commands (&&, ;), validate each
	for _, sep := range []string{"&&", ";"} {
		if strings.Contains(normalized, sep) {
			parts := strings.Split(normalized, sep)
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if err := validateSegment(part, cfg); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// validateSegment checks a single command segment (no pipes).
func validateSegment(seg string, cfg SandboxConfig) error {
	if !cfg.AllowSudo && sudoPattern.MatchString(seg) {
		return fmt.Errorf("sandbox: blocked privilege escalation in pipe/chain — %s", extractFirstWord(seg))
	}
	for _, dp := range dangerousPatterns {
		if dp.pattern.MatchString(seg) {
			return fmt.Errorf("sandbox: blocked dangerous command in pipe/chain — %s", dp.reason)
		}
	}
	return nil
}

// ValidateWorkDir ensures the work directory is within allowed paths (if configured).
func ValidateWorkDir(workDir string, cfg SandboxConfig) error {
	if !cfg.Enabled || len(cfg.AllowedPaths) == 0 {
		return nil
	}
	for _, allowed := range cfg.AllowedPaths {
		if strings.HasPrefix(workDir, allowed) {
			return nil
		}
	}
	return fmt.Errorf("sandbox: work directory %s is not within allowed paths %v", workDir, cfg.AllowedPaths)
}

func extractFirstWord(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, " \t"); idx > 0 {
		return s[:idx]
	}
	return s
}
