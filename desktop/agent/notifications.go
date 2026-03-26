package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// NotificationConfig holds notification channel settings.
type NotificationConfig struct {
	Telegram  *TelegramConfig  `json:"telegram,omitempty"`
	Discord   *DiscordConfig   `json:"discord,omitempty"`
	Slack     *SlackConfig     `json:"slack,omitempty"`
	Teams     *TeamsConfig     `json:"teams,omitempty"`
	Linear    *LinearConfig    `json:"linear,omitempty"`
	Jira      *JiraConfig      `json:"jira,omitempty"`
	PagerDuty *PagerDutyConfig `json:"pagerduty,omitempty"`
	Opsgenie  *OpsgenieConfig  `json:"opsgenie,omitempty"`
	Email     *EmailNotifyConfig `json:"email_notify,omitempty"`
}

type TelegramConfig struct {
	BotToken string `json:"botToken"` // from @BotFather
	ChatID   string `json:"chatId"`   // user/group chat ID
	Enabled  bool   `json:"enabled"`
}

type DiscordConfig struct {
	WebhookURL string `json:"webhookUrl"` // Discord webhook URL
	Enabled    bool   `json:"enabled"`
}

type SlackConfig struct {
	WebhookURL string `json:"webhookUrl"` // Slack incoming webhook URL
	Enabled    bool   `json:"enabled"`
}

type TeamsConfig struct {
	WebhookURL string `json:"webhookUrl"` // Microsoft Teams incoming webhook URL
	Enabled    bool   `json:"enabled"`
}

type LinearConfig struct {
	APIKey  string `json:"apiKey"`  // Linear API key
	TeamID  string `json:"teamId"`  // Linear team ID for issue creation
	Enabled bool   `json:"enabled"`
}

type JiraConfig struct {
	BaseURL    string `json:"baseUrl"`    // e.g. "https://mycompany.atlassian.net"
	Email      string `json:"email"`      // Jira account email
	APIToken   string `json:"apiToken"`   // Jira API token
	ProjectKey string `json:"projectKey"` // e.g. "DEV"
	Enabled    bool   `json:"enabled"`
}

type PagerDutyConfig struct {
	RoutingKey string `json:"routingKey"` // PagerDuty Events API v2 routing key
	Enabled    bool   `json:"enabled"`
	OnFailOnly bool   `json:"onFailOnly"` // only alert on task failure
}

type OpsgenieConfig struct {
	APIKey  string `json:"apiKey"`  // Opsgenie API key
	Enabled bool   `json:"enabled"`
	OnFailOnly bool `json:"onFailOnly"` // only alert on task failure
}

type EmailNotifyConfig struct {
	To      string `json:"to"`      // recipient email address
	Enabled bool   `json:"enabled"`
}

// NotificationManager handles sending notifications across channels.
type NotificationManager struct {
	config *NotificationConfig
	client *http.Client
}

// NewNotificationManager creates a notification manager.
func NewNotificationManager(config *NotificationConfig) *NotificationManager {
	if config == nil {
		config = &NotificationConfig{}
	}
	return &NotificationManager{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// UpdateConfig updates the notification configuration.
func (nm *NotificationManager) UpdateConfig(config *NotificationConfig) {
	if config != nil {
		nm.config = config
	}
}

// NotifyTaskCompleted sends a notification when a task completes.
func (nm *NotificationManager) NotifyTaskCompleted(taskID, title, status string, costUSD float64, durationSec int) {
	icon := "✅"
	if status == "failed" {
		icon = "❌"
	} else if status == "stopped" {
		icon = "⏹"
	}

	msg := fmt.Sprintf("%s Task %s: %s\n\nStatus: %s", icon, taskID[:8], title, status)
	if costUSD > 0 {
		msg += fmt.Sprintf("\nCost: $%.4f", costUSD)
	}
	if durationSec > 0 {
		msg += fmt.Sprintf("\nDuration: %ds", durationSec)
	}

	nm.sendAll(msg)

	// Developer integrations: issue trackers create on completion, alerting on failure
	if nm.config.Linear != nil && nm.config.Linear.Enabled {
		go nm.sendLinear(title, status, msg)
	}
	if nm.config.Jira != nil && nm.config.Jira.Enabled {
		go nm.sendJira(title, status, msg)
	}
	isFailed := status == "failed"
	if nm.config.PagerDuty != nil && nm.config.PagerDuty.Enabled && (isFailed || !nm.config.PagerDuty.OnFailOnly) {
		go nm.sendPagerDuty(title, msg)
	}
	if nm.config.Opsgenie != nil && nm.config.Opsgenie.Enabled && (isFailed || !nm.config.Opsgenie.OnFailOnly) {
		go nm.sendOpsgenie(title, msg)
	}
}

// NotifyExecCompleted sends a notification when an exec command finishes.
func (nm *NotificationManager) NotifyExecCompleted(command, status string, exitCode int) {
	icon := "✅"
	if exitCode != 0 {
		icon = "❌"
	}

	cmd := command
	if len(cmd) > 50 {
		cmd = cmd[:50] + "..."
	}

	msg := fmt.Sprintf("%s Exec: %s\nExit code: %d", icon, cmd, exitCode)
	nm.sendAll(msg)
}

// NotifySessionTransfer sends a notification when a session is transferred.
func (nm *NotificationManager) NotifySessionTransfer(title, sourceDevice, targetDevice string) {
	msg := fmt.Sprintf("🔄 Session transferred\n\"%s\"\n%s → %s", title, sourceDevice, targetDevice)
	nm.sendAll(msg)
}

// NotifyAgentEvent sends a notification for agent lifecycle events.
func (nm *NotificationManager) NotifyAgentEvent(event, detail string) {
	msg := fmt.Sprintf("🔔 Agent: %s\n%s", event, detail)
	nm.sendAll(msg)
}

// NotifyHealthCheck sends a notification when a health check changes status.
func (nm *NotificationManager) NotifyHealthCheck(label, url, status string, responseMs int64) {
	icon := "🔴"
	switch status {
	case "warning":
		icon = "🟡"
	case "recovered":
		icon = "🟢"
	}

	msg := fmt.Sprintf("%s Health: %s\nURL: %s\nStatus: %s", icon, label, url, status)
	if responseMs > 0 {
		msg += fmt.Sprintf("\nResponse: %dms", responseMs)
	}
	nm.sendAll(msg)

	// Trigger PagerDuty/Opsgenie for "down" status
	if status == "down" {
		if nm.config.PagerDuty != nil && nm.config.PagerDuty.Enabled {
			go nm.sendPagerDuty(label+" health check failed", msg)
		}
		if nm.config.Opsgenie != nil && nm.config.Opsgenie.Enabled {
			go nm.sendOpsgenie(label+" health check failed", msg)
		}
	}

	// Send email if configured
	if globalEmailMgr != nil && nm.config.Email != nil && nm.config.Email.Enabled && nm.config.Email.To != "" {
		go func() {
			subject := fmt.Sprintf("Health %s: %s", status, label)
			_ = globalEmailMgr.SendEmail(nm.config.Email.To, subject, msg, "")
		}()
	}
}

// NotifyQualityCheck sends a notification when a quality check fails or warns.
func (nm *NotificationManager) NotifyQualityCheck(checkType, status string, issues int) {
	icon := "🔴"
	if status == "warning" {
		icon = "🟡"
	}
	msg := fmt.Sprintf("%s Quality Gate: %s\nStatus: %s\nIssues: %d", icon, checkType, status, issues)
	nm.sendAll(msg)

	if globalEmailMgr != nil && nm.config.Email != nil && nm.config.Email.Enabled && nm.config.Email.To != "" {
		go func() {
			subject := fmt.Sprintf("Quality %s: %s (%d issues)", status, checkType, issues)
			_ = globalEmailMgr.SendEmail(nm.config.Email.To, subject, msg, "")
		}()
	}
}

// sendAll sends a message to all configured notification channels.
func (nm *NotificationManager) sendAll(message string) {
	if nm.config.Telegram != nil && nm.config.Telegram.Enabled {
		go nm.sendTelegram(message)
	}
	if nm.config.Discord != nil && nm.config.Discord.Enabled {
		go nm.sendDiscord(message)
	}
	if nm.config.Slack != nil && nm.config.Slack.Enabled {
		go nm.sendSlack(message)
	}
	if nm.config.Teams != nil && nm.config.Teams.Enabled {
		go nm.sendTeams(message)
	}
	if nm.config.Email != nil && nm.config.Email.Enabled {
		go nm.sendEmailNotify(message)
	}
}

// --- Telegram ---

func (nm *NotificationManager) sendTelegram(message string) {
	if nm.config.Telegram == nil || nm.config.Telegram.BotToken == "" || nm.config.Telegram.ChatID == "" {
		return
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", nm.config.Telegram.BotToken)
	body, _ := json.Marshal(map[string]interface{}{
		"chat_id":    nm.config.Telegram.ChatID,
		"text":       message,
		"parse_mode": "Markdown",
	})

	resp, err := nm.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify:telegram] send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[notify:telegram] API error %d: %s", resp.StatusCode, string(respBody))
	}
}

// --- Discord ---

func (nm *NotificationManager) sendDiscord(message string) {
	if nm.config.Discord == nil || nm.config.Discord.WebhookURL == "" {
		return
	}

	body, _ := json.Marshal(map[string]string{"content": message})
	resp, err := nm.client.Post(nm.config.Discord.WebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify:discord] send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[notify:discord] API error %d", resp.StatusCode)
	}
}

// --- Slack ---

func (nm *NotificationManager) sendSlack(message string) {
	if nm.config.Slack == nil || nm.config.Slack.WebhookURL == "" {
		return
	}

	body, _ := json.Marshal(map[string]string{"text": message})
	resp, err := nm.client.Post(nm.config.Slack.WebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify:slack] send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[notify:slack] API error %d", resp.StatusCode)
	}
}

// --- Microsoft Teams ---

func (nm *NotificationManager) sendTeams(message string) {
	if nm.config.Teams == nil || nm.config.Teams.WebhookURL == "" {
		return
	}

	// Teams Incoming Webhook expects an Adaptive Card or simple text payload
	body, _ := json.Marshal(map[string]string{"text": message})
	resp, err := nm.client.Post(nm.config.Teams.WebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify:teams] send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[notify:teams] API error %d", resp.StatusCode)
	}
}

// --- Linear ---

func (nm *NotificationManager) sendLinear(title, status, detail string) {
	if nm.config.Linear == nil || nm.config.Linear.APIKey == "" || nm.config.Linear.TeamID == "" {
		return
	}

	query := `mutation($title: String!, $teamId: String!, $description: String!) {
		issueCreate(input: { title: $title, teamId: $teamId, description: $description }) {
			success issue { id identifier url }
		}
	}`
	variables := map[string]string{
		"title":       fmt.Sprintf("[Yaver] %s — %s", title, status),
		"teamId":      nm.config.Linear.TeamID,
		"description": detail,
	}
	body, _ := json.Marshal(map[string]interface{}{"query": query, "variables": variables})

	req, _ := http.NewRequest("POST", "https://api.linear.app/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", nm.config.Linear.APIKey)

	resp, err := nm.client.Do(req)
	if err != nil {
		log.Printf("[notify:linear] send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[notify:linear] API error %d", resp.StatusCode)
	}
}

// --- Jira ---

func (nm *NotificationManager) sendJira(title, status, detail string) {
	if nm.config.Jira == nil || nm.config.Jira.BaseURL == "" || nm.config.Jira.APIToken == "" {
		return
	}

	issueData := map[string]interface{}{
		"fields": map[string]interface{}{
			"project":     map[string]string{"key": nm.config.Jira.ProjectKey},
			"summary":     fmt.Sprintf("[Yaver] %s — %s", title, status),
			"description": detail,
			"issuetype":   map[string]string{"name": "Task"},
		},
	}
	body, _ := json.Marshal(issueData)

	url := strings.TrimRight(nm.config.Jira.BaseURL, "/") + "/rest/api/2/issue"
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(nm.config.Jira.Email, nm.config.Jira.APIToken)

	resp, err := nm.client.Do(req)
	if err != nil {
		log.Printf("[notify:jira] send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[notify:jira] API error %d: %s", resp.StatusCode, string(respBody))
	}
}

// --- PagerDuty ---

func (nm *NotificationManager) sendPagerDuty(title, detail string) {
	if nm.config.PagerDuty == nil || nm.config.PagerDuty.RoutingKey == "" {
		return
	}

	event := map[string]interface{}{
		"routing_key":  nm.config.PagerDuty.RoutingKey,
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"summary":  fmt.Sprintf("[Yaver] %s", title),
			"severity": "error",
			"source":   "yaver-agent",
			"custom_details": map[string]string{
				"detail": detail,
			},
		},
	}
	body, _ := json.Marshal(event)

	resp, err := nm.client.Post("https://events.pagerduty.com/v2/enqueue", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify:pagerduty] send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[notify:pagerduty] API error %d", resp.StatusCode)
	}
}

// --- Opsgenie ---

func (nm *NotificationManager) sendOpsgenie(title, detail string) {
	if nm.config.Opsgenie == nil || nm.config.Opsgenie.APIKey == "" {
		return
	}

	alert := map[string]interface{}{
		"message":     fmt.Sprintf("[Yaver] %s", title),
		"description": detail,
		"priority":    "P2",
		"source":      "yaver-agent",
	}
	body, _ := json.Marshal(alert)

	req, _ := http.NewRequest("POST", "https://api.opsgenie.com/v2/alerts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "GenieKey "+nm.config.Opsgenie.APIKey)

	resp, err := nm.client.Do(req)
	if err != nil {
		log.Printf("[notify:opsgenie] send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[notify:opsgenie] API error %d", resp.StatusCode)
	}
}

// --- Email Notification ---

func (nm *NotificationManager) sendEmailNotify(message string) {
	if nm.config.Email == nil || nm.config.Email.To == "" {
		return
	}

	// Use the global email manager if available
	if globalEmailMgr != nil {
		err := globalEmailMgr.SendEmail(nm.config.Email.To, "Yaver Notification", message, "")
		if err != nil {
			log.Printf("[notify:email] send failed: %v", err)
		}
	} else {
		log.Printf("[notify:email] email manager not configured — skipping")
	}
}

// globalEmailMgr is set by main.go to enable email notifications.
var globalEmailMgr *EmailManager

// TestNotification sends a test message to verify configuration.
func (nm *NotificationManager) TestNotification(channel string) string {
	msg := "🧪 Yaver test notification — your integration is working!"

	switch strings.ToLower(channel) {
	case "telegram":
		if nm.config.Telegram == nil || !nm.config.Telegram.Enabled {
			return "Telegram not configured"
		}
		nm.sendTelegram(msg)
		return "Test sent to Telegram"
	case "discord":
		if nm.config.Discord == nil || !nm.config.Discord.Enabled {
			return "Discord not configured"
		}
		nm.sendDiscord(msg)
		return "Test sent to Discord"
	case "slack":
		if nm.config.Slack == nil || !nm.config.Slack.Enabled {
			return "Slack not configured"
		}
		nm.sendSlack(msg)
		return "Test sent to Slack"
	case "teams":
		if nm.config.Teams == nil || !nm.config.Teams.Enabled {
			return "Teams not configured"
		}
		nm.sendTeams(msg)
		return "Test sent to Teams"
	case "linear":
		if nm.config.Linear == nil || !nm.config.Linear.Enabled {
			return "Linear not configured"
		}
		nm.sendLinear("Test", "completed", msg)
		return "Test issue created in Linear"
	case "jira":
		if nm.config.Jira == nil || !nm.config.Jira.Enabled {
			return "Jira not configured"
		}
		nm.sendJira("Test", "completed", msg)
		return "Test issue created in Jira"
	case "pagerduty":
		if nm.config.PagerDuty == nil || !nm.config.PagerDuty.Enabled {
			return "PagerDuty not configured"
		}
		nm.sendPagerDuty("Test", msg)
		return "Test alert sent to PagerDuty"
	case "opsgenie":
		if nm.config.Opsgenie == nil || !nm.config.Opsgenie.Enabled {
			return "Opsgenie not configured"
		}
		nm.sendOpsgenie("Test", msg)
		return "Test alert sent to Opsgenie"
	case "email":
		if nm.config.Email == nil || !nm.config.Email.Enabled {
			return "Email notifications not configured"
		}
		nm.sendEmailNotify(msg)
		return "Test email sent"
	default:
		nm.sendAll(msg)
		return "Test sent to all configured channels"
	}
}
