package main

import (
	"fmt"
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
	case "exit", "/exit", "quit", "/quit", "detach", "/detach":
		return terminalCommand{Kind: "detach"}, true
	case "clear", "/clear", "cls":
		return terminalCommand{Kind: "clear"}, true
	case "tasks", "/tasks", "list", "/list":
		return terminalCommand{Kind: "tasks"}, true
	case "agent", "/agent", "runner", "/runner", "which agent", "which agent is running", "which runner":
		return terminalCommand{Kind: "agent"}, true
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
	fmt.Println("`help` shows local terminal commands. Ctrl+C detaches.")
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
	fmt.Println("  clear                 clear the screen")
	fmt.Println("  exit / quit / detach  leave the terminal without stopping the agent")
	fmt.Println("  /help /exit /quit     common slash-command aliases")
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

func attachRunnerLine(info *attachInfo) string {
	if info == nil {
		return ""
	}
	name := strings.TrimSpace(info.Runner.Name)
	if name == "" {
		name = strings.TrimSpace(info.Runner.ID)
	}
	model := strings.TrimSpace(info.Runner.Model)
	switch {
	case name != "" && model != "":
		return name + " · " + model
	case name != "":
		return name
	case model != "":
		return model
	default:
		return ""
	}
}
