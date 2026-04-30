package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	terminalLocalTaskSource  = "terminal-local"
	terminalRemoteTaskSource = "terminal-remote"
)

type terminalCommand struct {
	Kind   string
	TaskID string
	Input  string
	Runner string
	Model  string
}

type terminalPromptPayload struct {
	Prompt       string
	UserEcho     string
	Images       []ImageAttachment
	Attachments  []terminalAttachment
	OriginalText string
}

type terminalAttachment struct {
	Path string
	Kind string
}

func parseTerminalCommand(line string) (terminalCommand, bool) {
	raw := strings.TrimSpace(line)
	if raw == "" {
		return terminalCommand{}, false
	}

	normalized := strings.ToLower(raw)
	switch normalized {
	case "help", "/help", "-h", "--help":
		return terminalCommand{Kind: "help"}, true
	case "exit", "/exit", "\\exit", "quit", "/quit", "\\quit", "detach", "/detach", "\\detach":
		return terminalCommand{Kind: "detach"}, true
	case "clear", "/clear", "cls":
		return terminalCommand{Kind: "clear"}, true
	case "tasks", "/tasks", "list", "/list":
		return terminalCommand{Kind: "tasks"}, true
	case "agent", "/agent", "runner", "/runner", "get agent", "/get agent", "\\get agent", "which agent", "which agent is running", "which runner":
		return terminalCommand{Kind: "agent"}, true
	case "version", "/version", "\\version", "--version", "-v":
		return terminalCommand{Kind: "version"}, true
	case "about", "/about", "\\about":
		return terminalCommand{Kind: "about"}, true
	case "machine", "/machine", "\\machine", "where", "/where", "host", "/host":
		return terminalCommand{Kind: "machine"}, true
	case "discover", "/discover", "\\discover":
		return terminalCommand{Kind: "discover"}, true
	case "/phone", "phone", "/phone status", "phone status":
		// Default verb for the phone surface is `status` — same shape
		// as `/agent` printing the current runner. Future phone verbs
		// (push, pull, token *) are added below as prefix matches so
		// they can carry arguments without bloating this switch.
		return terminalCommand{Kind: "phone-status"}, true
	}

	for _, prefix := range []string{"set agent ", "/set agent ", "\\set agent "} {
		if strings.HasPrefix(normalized, prefix) {
			rest := strings.TrimSpace(raw[len(prefix):])
			if rest == "" {
				return terminalCommand{}, false
			}
			parts := strings.Fields(rest)
			cmd := terminalCommand{Kind: "set-agent", Runner: normalizeRunnerID(parts[0])}
			if len(parts) > 1 {
				cmd.Model = strings.Join(parts[1:], " ")
			}
			return cmd, true
		}
	}

	for _, prefix := range []string{"stop ", "/stop "} {
		if strings.HasPrefix(normalized, prefix) {
			taskID := strings.TrimSpace(raw[len(prefix):])
			if taskID != "" {
				return terminalCommand{Kind: "stop-task", TaskID: taskID}, true
			}
		}
	}
	for _, prefix := range []string{"exit-task ", "/exit-task ", "graceful-stop ", "/graceful-stop "} {
		if strings.HasPrefix(normalized, prefix) {
			taskID := strings.TrimSpace(raw[len(prefix):])
			if taskID != "" {
				return terminalCommand{Kind: "exit-task", TaskID: taskID}, true
			}
		}
	}
	for _, prefix := range []string{"continue ", "/continue "} {
		if strings.HasPrefix(normalized, prefix) {
			rest := strings.TrimSpace(raw[len(prefix):])
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
				return terminalCommand{
					Kind:   "continue-task",
					TaskID: strings.TrimSpace(parts[0]),
					Input:  strings.TrimSpace(parts[1]),
				}, true
			}
		}
	}

	return terminalCommand{}, false
}

func printAttachWelcome(info *attachInfo) {
	fmt.Println("Yaver terminal")
	if info != nil && strings.TrimSpace(info.Hostname) != "" {
		fmt.Printf("machine  %s\n", info.Hostname)
	}
	if info != nil && strings.TrimSpace(info.WorkDir) != "" {
		fmt.Printf("cwd      %s\n", info.WorkDir)
	}
	if runnerLine := attachRunnerLine(info); runnerLine != "" {
		fmt.Printf("agent    %s\n", runnerLine)
	}
	if info != nil && strings.TrimSpace(info.Version) != "" {
		fmt.Printf("version  %s\n", info.Version)
	}
	fmt.Println()
	fmt.Println("Type a task and press Enter.")
	fmt.Println("`/` opens the command palette. `help` shows local terminal commands. Ctrl+C detaches.")
	fmt.Println()
}

func printAttachHelp(info *attachInfo) {
	fmt.Println("Local terminal commands")
	fmt.Println("  help                  show this screen")
	fmt.Println("  tasks                 list recent tasks")
	if runnerLine := attachRunnerLine(info); runnerLine != "" {
		fmt.Printf("  agent                 show current coding agent (%s)\n", runnerLine)
	} else {
		fmt.Println("  agent                 show current coding agent")
	}
	fmt.Println("  stop <taskId>         hard-stop a task")
	fmt.Println("  exit-task <taskId>    graceful runner exit for a task")
	fmt.Println("  continue <id> <msg>   continue a finished task")
	fmt.Println("  set agent <id> [model]  change the terminal's default coding agent")
	fmt.Println("  clear                 clear the screen")
	fmt.Println("  version               show the agent's version")
	fmt.Println("  about                 show what `yaver code` is + key shortcuts")
	fmt.Println("  machine               show which host is running this session")
	fmt.Println("  discover              show the cached machine discovery snapshot")
	fmt.Println("  /                     open the slash-command picker (↑/↓ + Enter, Esc cancels)")
	fmt.Println("  exit / quit / detach  leave the terminal without stopping the agent")
	fmt.Println("  /help /exit /quit     common slash-command aliases")
	fmt.Println("  \\exit / \\quit         common backslash-command aliases")
	fmt.Println("  anything else         send that text to the coding agent")
	fmt.Println()
	fmt.Println("Common terminal flows")
	fmt.Println("  Local terminal coding:")
	fmt.Println("    yaver code")
	fmt.Println("    yaver code --agent opencode")
	fmt.Println("    These edit the local repo/files on this machine.")
	fmt.Println("  Terminal coding on another Yaver machine:")
	fmt.Println("    yaver code --attach <deviceId|deviceName>")
	fmt.Println("    This edits files on that attached remote machine.")
	fmt.Println("  Sign in on a browserless box:")
	fmt.Println("    yaver auth --headless")
	fmt.Println("  Pair a fresh headless box from another signed-in box:")
	fmt.Println("    target: yaver auth pair")
	fmt.Println("    source: yaver auth send <code> <target-url>")
	fmt.Println("  Re-auth Yaver on this machine from a clean state:")
	fmt.Println("    yaver auth factory-reset --headless")
	fmt.Println("  Re-auth the coding runner on a remote machine without SSH:")
	fmt.Println("    yaver runner-auth setup codex --target <deviceId> --openai-api-key $OPENAI_API_KEY")
	fmt.Println("    yaver runner-auth setup claude --target <deviceId> --anthropic-api-key $ANTHROPIC_API_KEY")
	fmt.Println("    yaver runner-auth setup opencode --target <deviceId>")
	fmt.Println()
}

// printTerminalVersion prints a compact "yaver vX.Y.Z" line plus the
// remote agent version when the terminal is attached. Local CLI
// version is the canonical "what binary am I running"; agentVersion
// is what the host on the other end of an attach reports — they can
// drift if the user upgraded one side without the other.
func printTerminalVersion(agentVersion string) {
	fmt.Printf("yaver %s\n", version)
	agentVersion = strings.TrimSpace(agentVersion)
	if agentVersion != "" && agentVersion != version {
		fmt.Printf("agent  %s\n", agentVersion)
	}
	fmt.Println()
}

// printTerminalAbout prints a short blurb explaining what `yaver code`
// is and the key shortcuts the user is most likely to need next. Kept
// short so it doesn't drown out the prompt; longer guidance lives in
// `help` and YAVER_CODE_TODO.md.
func printTerminalAbout() {
	fmt.Println("yaver code — terminal-native coding session")
	fmt.Println()
	fmt.Println("  Local commands run on this machine. `--attach <device>`")
	fmt.Println("  edits files on a remote Yaver host through the same prompt.")
	fmt.Println("  Every session maps to a Task — `tasks` lists them, `continue`")
	fmt.Println("  resumes one, `/sessions` shows recent runner sessions.")
	fmt.Println()
	fmt.Println("Key shortcuts:")
	fmt.Println("  /              open the slash-command picker (arrow keys)")
	fmt.Println("  Tab            cycle slash matches in the picker")
	fmt.Println("  Ctrl+C         detach without stopping the agent")
	fmt.Println("  Ctrl+L / clear clear the screen")
	fmt.Println()
}

// printTerminalMachine prints what host (and identity) the current
// terminal session is talking to. Local mode says "this machine";
// attached mode names the device + connection mode.
func printTerminalMachine(info *attachInfo) {
	if info == nil || strings.TrimSpace(info.Hostname) == "" {
		host := strings.TrimSpace(localHostname())
		if host == "" {
			host = "this machine"
		}
		fmt.Printf("Local terminal · %s\n", host)
		fmt.Println("  Type `yaver code --attach <device>` to drive a remote host instead.")
		fmt.Println()
		return
	}
	fmt.Printf("Attached to %s\n", strings.TrimSpace(info.Hostname))
	if v := strings.TrimSpace(info.Version); v != "" {
		fmt.Printf("  agent     %s\n", v)
	}
	if wd := strings.TrimSpace(info.WorkDir); wd != "" {
		fmt.Printf("  cwd       %s\n", wd)
	}
	if rl := attachRunnerLine(info); rl != "" {
		fmt.Printf("  runner    %s\n", rl)
	}
	fmt.Println()
}

// localHostname returns os.Hostname() but defaults to "" on error so
// we never print a noisy fallback string.
func localHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func attachRunnerLine(info *attachInfo) string {
	if info == nil {
		return ""
	}
	name := strings.TrimSpace(info.Runner.Name)
	if name == "" {
		name = strings.TrimSpace(info.Runner.ID)
	}
	model := strings.TrimSpace(info.Runner.Model)
	mode := strings.TrimSpace(info.Runner.Mode)
	if mode != "" {
		mode = "mode=" + mode
	}
	switch {
	case name != "" && model != "" && mode != "":
		return name + " · " + model + " · " + mode
	case name != "" && model != "":
		return name + " · " + model
	case name != "" && mode != "":
		return name + " · " + mode
	case name != "":
		return name
	case model != "":
		if mode != "" {
			return model + " · " + mode
		}
		return model
	case mode != "":
		return mode
	default:
		return ""
	}
}

func summarizeTerminalInputEcho(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	lineCount := 1 + strings.Count(trimmed, "\n")
	if len(trimmed) > 500 || lineCount > 8 {
		return fmt.Sprintf("[Pasted Content %d chars]", len(trimmed))
	}
	return trimmed
}

func buildTerminalPromptPayload(input string) terminalPromptPayload {
	trimmed := strings.TrimSpace(input)
	payload := terminalPromptPayload{
		Prompt:       trimmed,
		UserEcho:     summarizeTerminalInputEcho(trimmed),
		OriginalText: trimmed,
	}
	attachments := detectTerminalAttachments(trimmed)
	if len(attachments) == 0 {
		return payload
	}
	payload.Attachments = attachments
	for _, att := range attachments {
		if att.Kind != "image" {
			continue
		}
		data, err := os.ReadFile(att.Path)
		if err != nil {
			continue
		}
		payload.Images = append(payload.Images, ImageAttachment{
			Base64:   base64.StdEncoding.EncodeToString(data),
			MimeType: mimeTypeFromPath(att.Path),
			Filename: filepath.Base(att.Path),
		})
	}
	var extra []string
	extra = append(extra, "", "[Attached local files]")
	for i, att := range attachments {
		extra = append(extra, fmt.Sprintf("%s %d: %s", strings.Title(att.Kind), i+1, att.Path))
	}
	extra = append(extra, "Inspect these files directly. Preserve native diffs, colors, and runner-specific progress wording when you respond.")
	payload.Prompt = strings.TrimSpace(trimmed + "\n\n" + strings.Join(extra, "\n"))
	if echo := summarizeTerminalAttachmentsEcho(attachments); echo != "" {
		payload.UserEcho = echo
	}
	return payload
}

func summarizeTerminalAttachmentsEcho(attachments []terminalAttachment) string {
	if len(attachments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(attachments))
	for _, att := range attachments {
		parts = append(parts, fmt.Sprintf("%s %s", att.Kind, filepath.Base(att.Path)))
	}
	return "[Attached " + strings.Join(parts, ", ") + "]"
}

func detectTerminalAttachments(input string) []terminalAttachment {
	tokens := splitTerminalInputTokens(input)
	if len(tokens) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var attachments []terminalAttachment
	for _, token := range tokens {
		path, kind, ok := terminalAttachmentFromToken(token)
		if !ok || seen[path] {
			continue
		}
		seen[path] = true
		attachments = append(attachments, terminalAttachment{Path: path, Kind: kind})
	}
	return attachments
}

func splitTerminalInputTokens(input string) []string {
	var (
		tokens []string
		buf    []rune
		quote  rune
		escape bool
	)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		tokens = append(tokens, string(buf))
		buf = buf[:0]
	}
	for _, r := range input {
		switch {
		case escape:
			buf = append(buf, r)
			escape = false
		case r == '\\':
			escape = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				buf = append(buf, r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			buf = append(buf, r)
		}
	}
	flush()
	return tokens
}

func terminalAttachmentFromToken(token string) (string, string, bool) {
	candidate := strings.TrimSpace(token)
	if candidate == "" {
		return "", "", false
	}
	if strings.HasPrefix(candidate, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidate = filepath.Join(home, strings.TrimPrefix(candidate, "~/"))
		}
	}
	if !strings.HasPrefix(candidate, "/") && !strings.HasPrefix(candidate, "./") && !strings.HasPrefix(candidate, "../") {
		return "", "", false
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", "", false
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", "", false
	}
	switch strings.ToLower(filepath.Ext(abs)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".heic", ".heif":
		return abs, "image", true
	case ".mov", ".mp4", ".m4v", ".avi", ".mkv", ".webm":
		return abs, "video", true
	default:
		return abs, "file", true
	}
}

func printTerminalUserInput(payload terminalPromptPayload) {
	echo := strings.TrimSpace(payload.UserEcho)
	if echo == "" {
		echo = summarizeTerminalInputEcho(payload.OriginalText)
	}
	fmt.Printf("\n\033[1;36m⟩ %s\033[0m\n\n", echo)
}

func renderTerminalPromptLabel(workDir, runner, model, mode string) string {
	return renderTerminalPromptLabelWithStatus(workDir, runner, model, mode, "")
}

// renderTerminalPromptLabelWithStatus is the underlying implementation;
// status is appended as " · <status>" so the user can see at a glance
// whether the session is offline / attached / etc.
func renderTerminalPromptLabelWithStatus(workDir, runner, model, mode, status string) string {
	wd := strings.TrimSpace(workDir)
	if wd == "" {
		if cwd, err := os.Getwd(); err == nil {
			wd = cwd
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(wd, home) {
		wd = "~" + strings.TrimPrefix(wd, home)
	}
	agent := strings.TrimSpace(model)
	if agent == "" {
		agent = strings.TrimSpace(runner)
	}
	if agent == "" {
		agent = "default"
	}
	switch normalizeRunnerID(runner) {
	case "codex":
		if strings.TrimSpace(model) == "" {
			agent = "gpt-5.4 default"
		}
	case "claude":
		if strings.TrimSpace(model) == "" {
			agent = "claude default"
		}
	case "opencode":
		if strings.TrimSpace(model) == "" {
			agent = "opencode default"
		}
	}
	if normalizeRunnerID(runner) == "opencode" && strings.TrimSpace(mode) != "" {
		agent += " [" + strings.TrimSpace(mode) + "]"
	}
	parts := []string{agent}
	if wd != "" {
		parts = append(parts, wd)
	}
	if status != "" {
		parts = append(parts, status)
	}
	return strings.Join(parts, " · ")
}

func printInteractivePrompt(workDir, runner, model, mode string) {
	fmt.Printf("\033[2m%s\033[0m\n\033[1;35myaver>\033[0m ", renderTerminalPromptLabel(workDir, runner, model, mode))
}
