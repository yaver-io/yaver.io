package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ChatBot manages bidirectional chat integrations (Telegram, Discord).
// Runs inside the agent process — no external server needed.
type ChatBot struct {
	taskMgr   *TaskManager
	execMgr   *ExecManager
	notifyMgr *NotificationManager
	config    *NotificationConfig
	placement TaskIngressPlacementConfig
	cancel    context.CancelFunc
}

// ChatBotPlacementConfig lets chat ingress participate in the Cloud Workspace
// placement layer without giving the chat bot direct provider controls.
type ChatBotPlacementConfig = TaskIngressPlacementConfig

// NewChatBot creates a chat bot manager.
func NewChatBot(taskMgr *TaskManager, execMgr *ExecManager, notifyMgr *NotificationManager, config *NotificationConfig, placement ...TaskIngressPlacementConfig) *ChatBot {
	cb := &ChatBot{
		taskMgr:   taskMgr,
		execMgr:   execMgr,
		notifyMgr: notifyMgr,
		config:    config,
	}
	if len(placement) > 0 {
		cb.placement = placement[0]
	}
	return cb
}

// Start begins listening for messages on all configured channels.
func (cb *ChatBot) Start(ctx context.Context) {
	ctx, cb.cancel = context.WithCancel(ctx)

	if cb.config != nil && cb.config.Telegram != nil && cb.config.Telegram.Enabled && cb.config.Telegram.BotToken != "" {
		go cb.telegramPollLoop(ctx)
	}
}

// Stop stops all chat bot listeners.
func (cb *ChatBot) Stop() {
	if cb.cancel != nil {
		cb.cancel()
	}
}

// UpdateConfig updates config and restarts listeners.
func (cb *ChatBot) UpdateConfig(ctx context.Context, config *NotificationConfig) {
	cb.Stop()
	cb.config = config
	cb.Start(ctx)
}

// ── Telegram Bot ──────────────────────────────────────────────────

type telegramUpdate struct {
	UpdateID int              `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int `json:"message_id"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text string `json:"text"`
	From struct {
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	} `json:"from"`
}

func (cb *ChatBot) telegramPollLoop(ctx context.Context) {
	if cb.config == nil || cb.config.Telegram == nil {
		return
	}

	token := cb.config.Telegram.BotToken
	chatID := cb.config.Telegram.ChatID
	offset := 0
	client := &http.Client{Timeout: 35 * time.Second}

	log.Printf("[chatbot:telegram] Starting bot listener (chat=%s)", chatID)

	// Send welcome message
	cb.telegramSend(token, chatID, "🟢 Yaver agent connected. Send a message to create a task, or use commands:\n\n"+
		"/task <prompt> — Create an AI task\n"+
		"/exec <command> — Run a shell command\n"+
		"/status — Agent status\n"+
		"/tasks — List recent tasks\n"+
		"/help — Show commands")

	for {
		select {
		case <-ctx.Done():
			log.Println("[chatbot:telegram] Stopped")
			return
		default:
		}

		// Long poll for updates (30s timeout)
		apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30&allowed_updates=[\"message\"]", token, offset)
		resp, err := client.Get(apiURL)
		if err != nil {
			log.Printf("[chatbot:telegram] Poll error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		var result struct {
			OK     bool             `json:"ok"`
			Result []telegramUpdate `json:"result"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if !result.OK {
			time.Sleep(5 * time.Second)
			continue
		}

		for _, update := range result.Result {
			offset = update.UpdateID + 1

			if update.Message == nil || update.Message.Text == "" {
				continue
			}

			msg := update.Message
			msgChatID := fmt.Sprintf("%d", msg.Chat.ID)

			// Only respond to the configured chat
			if chatID != "" && msgChatID != chatID {
				log.Printf("[chatbot:telegram] Ignoring message from chat %s (expected %s)", msgChatID, chatID)
				continue
			}

			log.Printf("[chatbot:telegram] Message from %s: %s", msg.From.Username, msg.Text)

			// Process the message
			response := cb.processCommand(msg.Text)
			cb.telegramSend(token, msgChatID, response)
		}
	}
}

func (cb *ChatBot) telegramSend(botToken, chatID, text string) {
	if len(text) > 4000 {
		text = text[:4000] + "\n\n...(truncated)"
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	data := url.Values{
		"chat_id":    {chatID},
		"text":       {text},
		"parse_mode": {"Markdown"},
	}
	resp, err := http.PostForm(apiURL, data)
	if err != nil {
		log.Printf("[chatbot:telegram] Send error: %v", err)
		return
	}
	resp.Body.Close()
}

// ── Command Processing ────────────────────────────────────────────

func (cb *ChatBot) processCommand(text string) string {
	text = strings.TrimSpace(text)

	// Commands
	if strings.HasPrefix(text, "/") {
		parts := strings.SplitN(text, " ", 2)
		cmd := strings.ToLower(parts[0])
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}

		switch cmd {
		case "/start", "/help":
			return "🛠 *Yaver Agent Commands*\n\n" +
				"/task <prompt> — Create an AI coding task\n" +
				"/exec <command> — Run a shell command\n" +
				"/status — Agent & runner status\n" +
				"/tasks — List recent tasks\n" +
				"/stop <taskId> — Stop a running task\n" +
				"/sessions — List transferable sessions\n\n" +
				"Or just send any message to create a task."

		case "/task":
			if arg == "" {
				return "Usage: /task <prompt>\nExample: /task Fix the login bug in auth.ts"
			}
			return cb.createTask(arg)

		case "/exec":
			if arg == "" {
				return "Usage: /exec <command>\nExample: /exec git status"
			}
			return cb.execCommand(arg)

		case "/status":
			return cb.getStatus()

		case "/tasks":
			return cb.listTasks()

		case "/stop":
			if arg == "" {
				return "Usage: /stop <taskId>"
			}
			return cb.stopTask(arg)

		case "/sessions":
			return cb.listSessions()

		default:
			return fmt.Sprintf("Unknown command: %s\nSend /help for available commands.", cmd)
		}
	}

	// Plain text — create a task
	return cb.createTask(text)
}

func (cb *ChatBot) createTask(prompt string) string {
	if msg, deferred := cb.deferTaskToCloudWorkspace(context.Background()); deferred {
		return msg
	}

	task, err := cb.taskMgr.CreateTask(prompt, "", "", "telegram", "", "", nil, nil)
	if err != nil {
		return fmt.Sprintf("❌ Failed to create task: %v", err)
	}

	// Monitor task in background and send result
	go func() {
		for i := 0; i < 600; i++ { // max 10 min
			time.Sleep(3 * time.Second)
			t, ok := cb.taskMgr.GetTask(task.ID)
			if !ok {
				return
			}
			cb.taskMgr.mu.RLock()
			status := t.Status
			result := t.ResultText
			cost := t.CostUSD
			cb.taskMgr.mu.RUnlock()

			if status == TaskStatusFinished || status == TaskStatusFailed || status == TaskStatusStopped {
				icon := "✅"
				if status == TaskStatusFailed {
					icon = "❌"
				}

				msg := fmt.Sprintf("%s *Task %s* — %s\n\n", icon, string(status), prompt)
				if result != "" {
					if len(result) > 3000 {
						result = result[:3000] + "\n...(truncated)"
					}
					msg += result
				}
				if cost > 0 {
					msg += fmt.Sprintf("\n\n💰 $%.4f", cost)
				}

				if cb.config != nil && cb.config.Telegram != nil {
					cb.telegramSend(cb.config.Telegram.BotToken, cb.config.Telegram.ChatID, msg)
				}
				return
			}
		}
	}()

	return fmt.Sprintf("🚀 Task created: `%s`\nID: `%s`\nI'll send the result when it's done.", prompt, task.ID)
}

func (cb *ChatBot) deferTaskToCloudWorkspace(ctx context.Context) (string, bool) {
	if cb == nil {
		return "", false
	}
	deferral, deferred, err := deferIngressTaskToCloudWorkspace(ctx, cb.placement, "telegram", "unknown")
	if err != nil && !deferred {
		log.Printf("[placement] telegram preview skipped before task create: %v", err)
		return "", false
	}
	if !deferred {
		return "", false
	}
	if err != nil {
		pendingTaskID := ""
		if deferral != nil {
			pendingTaskID = deferral.PendingTaskID
		}
		log.Printf("[placement] telegram cloud deferral failed for %s: %v", pendingTaskID, err)
		return fmt.Sprintf("Cloud Workspace is selected for this task, but I could not queue the handoff yet: %v", err), true
	}
	if blocker := strings.TrimSpace(deferral.Blocker); blocker != "" {
		return fmt.Sprintf("Cloud Workspace is selected for this task, but it needs your attention first: %s", blocker), true
	}
	target := ""
	if deferral.Placement != nil {
		target = strings.TrimSpace(deferral.Placement.TargetDeviceID)
	}
	if target == "" {
		target = "Cloud Workspace"
	}
	return fmt.Sprintf("Cloud Workspace is selected for this task. I queued a pending handoff (`%s`) for %s, so I will not run it on the relay while the workspace wakes.", deferral.PendingTaskID, target), true
}

func (cb *ChatBot) execCommand(command string) string {
	if cb.execMgr == nil {
		return "❌ Exec not available"
	}

	sess, err := cb.execMgr.StartExec(command, "", "", nil, 60)
	if err != nil {
		return fmt.Sprintf("❌ Exec failed: %v", err)
	}

	// Wait for completion
	select {
	case <-sess.doneCh:
	case <-time.After(60 * time.Second):
		return fmt.Sprintf("⏱ Exec timed out (60s)\nID: `%s`", sess.ID)
	}

	snapshot := sess.Snapshot()
	exitCode := 0
	if code, ok := snapshot["exitCode"].(int); ok {
		exitCode = code
	}

	icon := "✅"
	if exitCode != 0 {
		icon = "❌"
	}

	result := fmt.Sprintf("%s `%s` (exit %d)", icon, command, exitCode)
	if stdout, ok := snapshot["stdout"].(string); ok && stdout != "" {
		if len(stdout) > 3000 {
			stdout = stdout[:3000] + "\n...(truncated)"
		}
		result += "\n\n```\n" + stdout + "\n```"
	}
	if stderr, ok := snapshot["stderr"].(string); ok && stderr != "" {
		if len(stderr) > 500 {
			stderr = stderr[:500] + "\n...(truncated)"
		}
		result += "\n\nstderr:\n```\n" + stderr + "\n```"
	}

	return result
}

func (cb *ChatBot) getStatus() string {
	status := cb.taskMgr.GetAgentStatus()
	return fmt.Sprintf("🖥 *Agent Status*\n\n"+
		"Runner: %s (%s)\n"+
		"Running tasks: %d\n"+
		"Total tasks: %d\n"+
		"System: %s/%s",
		status.Runner.Name, status.Runner.ID,
		status.RunningTasks, status.TotalTasks,
		status.System.Hostname, status.System.OS)
}

func (cb *ChatBot) listTasks() string {
	tasks := cb.taskMgr.ListTasks()
	if len(tasks) == 0 {
		return "No tasks."
	}

	var sb strings.Builder
	sb.WriteString("📋 *Recent Tasks*\n\n")
	limit := 10
	if len(tasks) < limit {
		limit = len(tasks)
	}
	for i := 0; i < limit; i++ {
		t := tasks[i]
		icon := "⏳"
		switch TaskStatus(t.Status) {
		case TaskStatusFinished:
			icon = "✅"
		case TaskStatusFailed:
			icon = "❌"
		case TaskStatusRunning:
			icon = "🔄"
		case TaskStatusStopped:
			icon = "⏹"
		}

		title := t.Title
		if len(title) > 40 {
			title = title[:40] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s `%s` %s\n", icon, t.ID[:8], title))
	}
	return sb.String()
}

func (cb *ChatBot) stopTask(taskID string) string {
	// Try prefix match
	tasks := cb.taskMgr.ListTasks()
	for _, t := range tasks {
		if strings.HasPrefix(t.ID, taskID) {
			if err := cb.taskMgr.StopTask(t.ID); err != nil {
				return fmt.Sprintf("❌ Failed to stop: %v", err)
			}
			return fmt.Sprintf("⏹ Stopped task `%s`", t.ID[:8])
		}
	}
	return fmt.Sprintf("❌ Task not found: `%s`", taskID)
}

func (cb *ChatBot) listSessions() string {
	sessions := ListTransferableSessions(cb.taskMgr)
	if len(sessions) == 0 {
		return "No transferable sessions."
	}

	var sb strings.Builder
	sb.WriteString("🔄 *Transferable Sessions*\n\n")
	for _, s := range sessions {
		title := s.Title
		if len(title) > 35 {
			title = title[:35] + "..."
		}
		resumable := ""
		if s.Resumable {
			resumable = " 🔁"
		}
		sb.WriteString(fmt.Sprintf("`%s` %s [%s]%s\n", s.TaskID[:8], title, s.AgentType, resumable))
	}
	return sb.String()
}
