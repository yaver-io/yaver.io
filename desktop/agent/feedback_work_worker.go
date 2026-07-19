package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FeedbackWorkWorkerConfig struct {
	Enabled              bool   `json:"enabled,omitempty"`
	IntervalSeconds      int    `json:"interval_seconds,omitempty"`
	WorkerID             string `json:"worker_id,omitempty"`
	ProjectSlug          string `json:"project_slug,omitempty"`
	CreateProviderIssues bool   `json:"create_provider_issues,omitempty"`
}

const (
	defaultFeedbackWorkWorkerInterval = 20 * time.Second
	minFeedbackWorkWorkerInterval     = 5 * time.Second
	maxFeedbackWorkWorkerInterval     = 5 * time.Minute
)

type feedbackWorkItem struct {
	ID                  string `json:"id"`
	ProjectSlug         string `json:"projectSlug,omitempty"`
	Status              string `json:"status"`
	Target              string `json:"target,omitempty"`
	Title               string `json:"title,omitempty"`
	Body                string `json:"body,omitempty"`
	Kind                string `json:"kind,omitempty"`
	Priority            string `json:"priority,omitempty"`
	Component           string `json:"component,omitempty"`
	AppVersion          string `json:"appVersion,omitempty"`
	Platform            string `json:"platform,omitempty"`
	RelaySourceIntentID string `json:"relaySourceIntentId,omitempty"`
	Branch              string `json:"branch,omitempty"`
}

type feedbackWorkRelayQueueResult struct {
	Item              feedbackWorkItem  `json:"item"`
	RelaySourceIntent relaySourceIntent `json:"relaySourceIntent"`
}

type feedbackWorkTickResult struct {
	Item              *feedbackWorkItem  `json:"item,omitempty"`
	TaskID            string             `json:"taskId,omitempty"`
	RelaySourceIntent *relaySourceIntent `json:"relaySourceIntent,omitempty"`
}

type feedbackWorkCloudPlacementBlockedError struct {
	PendingTaskID string
	Reason        string
}

func (e *feedbackWorkCloudPlacementBlockedError) Error() string {
	if e == nil {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(e.Reason), "feedback work requires Cloud Workspace") + " (" + strings.TrimSpace(e.PendingTaskID) + ")"
}

type feedbackWorkWorkerRuntimeStatus struct {
	Running bool
	Reason  string
}

var createFeedbackProviderIssue = createFeedbackProviderIssueDefault

func feedbackWorkWorkerEnabled(cfg *Config) bool {
	if env := strings.TrimSpace(os.Getenv("YAVER_FEEDBACK_WORK_WORKER")); env != "" {
		return envTruthy(env)
	}
	return cfg != nil && cfg.FeedbackWorkWorker != nil && cfg.FeedbackWorkWorker.Enabled
}

func feedbackWorkWorkerInterval(cfg *Config) time.Duration {
	seconds := 0
	if cfg != nil && cfg.FeedbackWorkWorker != nil {
		seconds = cfg.FeedbackWorkWorker.IntervalSeconds
	}
	if env := strings.TrimSpace(os.Getenv("YAVER_FEEDBACK_WORK_WORKER_INTERVAL")); env != "" {
		if parsed, err := time.ParseDuration(env); err == nil {
			return clampFeedbackWorkWorkerInterval(parsed)
		}
		if parsedSeconds, err := parsePositiveInt(env); err == nil {
			seconds = parsedSeconds
		}
	}
	if seconds <= 0 {
		return defaultFeedbackWorkWorkerInterval
	}
	return clampFeedbackWorkWorkerInterval(time.Duration(seconds) * time.Second)
}

func feedbackWorkWorkerID(cfg *Config, deviceID string) string {
	if cfg != nil && cfg.FeedbackWorkWorker != nil {
		if id := strings.TrimSpace(cfg.FeedbackWorkWorker.WorkerID); id != "" {
			return id
		}
	}
	if id := strings.TrimSpace(deviceID); id != "" {
		return "feedback-work-worker:" + id
	}
	return "feedback-work-worker"
}

func feedbackWorkWorkerProjectSlug(cfg *Config) string {
	if cfg != nil && cfg.FeedbackWorkWorker != nil {
		return strings.TrimSpace(cfg.FeedbackWorkWorker.ProjectSlug)
	}
	return ""
}

func feedbackWorkWorkerCreateProviderIssues(cfg *Config) bool {
	if env := strings.TrimSpace(os.Getenv("YAVER_FEEDBACK_WORK_CREATE_PROVIDER_ISSUES")); env != "" {
		return envTruthy(env)
	}
	return cfg != nil && cfg.FeedbackWorkWorker != nil && cfg.FeedbackWorkWorker.CreateProviderIssues
}

func clampFeedbackWorkWorkerInterval(d time.Duration) time.Duration {
	if d < minFeedbackWorkWorkerInterval {
		return minFeedbackWorkWorkerInterval
	}
	if d > maxFeedbackWorkWorkerInterval {
		return maxFeedbackWorkWorkerInterval
	}
	return d
}

func StartFeedbackWorkWorker(ctx context.Context, s *HTTPServer, cfg *Config) {
	if s == nil {
		log.Printf("[feedback-work-worker] disabled: HTTP server unavailable")
		return
	}
	status := s.configureFeedbackWorkWorker(ctx, cfg)
	if !status.Running && strings.TrimSpace(status.Reason) != "" {
		log.Printf("[feedback-work-worker] disabled: %s", status.Reason)
	}
}

func (s *HTTPServer) configureFeedbackWorkWorker(ctx context.Context, cfg *Config) feedbackWorkWorkerRuntimeStatus {
	if s == nil {
		return feedbackWorkWorkerRuntimeStatus{Reason: "HTTP server unavailable"}
	}
	if ctx == nil {
		ctx = s.serveCtx
	}
	if ctx == nil {
		ctx = context.Background()
	}

	enabled := feedbackWorkWorkerEnabled(cfg)
	interval := feedbackWorkWorkerInterval(cfg)
	projectSlug := feedbackWorkWorkerProjectSlug(cfg)
	workerID := feedbackWorkWorkerID(cfg, s.deviceID)
	key := fmt.Sprintf("%s\x00%s\x00%s", workerID, projectSlug, interval.String())

	s.feedbackWorkWorkerMu.Lock()
	defer s.feedbackWorkWorkerMu.Unlock()

	s.feedbackWorkWorkerRootCtx = ctx
	if !enabled {
		s.stopFeedbackWorkWorkerLocked()
		return feedbackWorkWorkerRuntimeStatus{Reason: "disabled"}
	}
	if strings.TrimSpace(s.token) == "" || strings.TrimSpace(s.convexURL) == "" {
		s.stopFeedbackWorkWorkerLocked()
		return feedbackWorkWorkerRuntimeStatus{Reason: "backend auth unavailable"}
	}

	if s.feedbackWorkWorkerCancel != nil && s.feedbackWorkWorkerKey == key {
		return feedbackWorkWorkerRuntimeStatus{Running: true}
	}
	s.stopFeedbackWorkWorkerLocked()

	workerCtx, cancel := context.WithCancel(ctx)
	s.feedbackWorkWorkerCancel = cancel
	s.feedbackWorkWorkerKey = key
	log.Printf("[feedback-work-worker] enabled interval=%s project=%s", interval, firstNonEmpty(projectSlug, "all-owned"))
	go func() {
		defer s.clearFeedbackWorkWorkerIfCurrent(key)
		feedbackWorkWorkerLoop(workerCtx, s, interval, projectSlug, workerID)
	}()
	return feedbackWorkWorkerRuntimeStatus{Running: true}
}

func (s *HTTPServer) feedbackWorkWorkerRuntimeStatus() feedbackWorkWorkerRuntimeStatus {
	if s == nil {
		return feedbackWorkWorkerRuntimeStatus{Reason: "HTTP server unavailable"}
	}
	s.feedbackWorkWorkerMu.Lock()
	defer s.feedbackWorkWorkerMu.Unlock()
	if s.feedbackWorkWorkerCancel != nil {
		return feedbackWorkWorkerRuntimeStatus{Running: true}
	}
	return feedbackWorkWorkerRuntimeStatus{}
}

func (s *HTTPServer) stopFeedbackWorkWorkerLocked() {
	if s.feedbackWorkWorkerCancel != nil {
		s.feedbackWorkWorkerCancel()
	}
	s.feedbackWorkWorkerCancel = nil
	s.feedbackWorkWorkerKey = ""
}

func (s *HTTPServer) clearFeedbackWorkWorkerIfCurrent(key string) {
	s.feedbackWorkWorkerMu.Lock()
	defer s.feedbackWorkWorkerMu.Unlock()
	if s.feedbackWorkWorkerKey == key {
		s.feedbackWorkWorkerCancel = nil
		s.feedbackWorkWorkerKey = ""
	}
}

func feedbackWorkWorkerLoop(ctx context.Context, s *HTTPServer, interval time.Duration, projectSlug, workerID string) {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if _, err := feedbackWorkWorkerTick(ctx, s, projectSlug, workerID); err != nil && ctx.Err() == nil {
				log.Printf("[feedback-work-worker] tick failed: %v", err)
			}
			timer.Reset(interval)
		}
	}
}

func feedbackWorkWorkerTick(ctx context.Context, s *HTTPServer, projectSlug, workerID string, createProviderIssues ...bool) (*feedbackWorkTickResult, error) {
	if s == nil {
		return nil, fmt.Errorf("server unavailable")
	}
	shouldCreateProviderIssues := len(createProviderIssues) > 0 && createProviderIssues[0]
	if len(createProviderIssues) == 0 {
		if cfg, err := LoadConfig(); err == nil {
			shouldCreateProviderIssues = feedbackWorkWorkerCreateProviderIssues(cfg)
		}
	}
	authHeader := "Bearer " + strings.TrimSpace(s.token)
	claimed, err := claimFeedbackWorkItem(ctx, authHeader, projectSlug, workerID)
	if err != nil {
		return nil, err
	}
	if claimed == nil {
		return nil, nil
	}
	if strings.EqualFold(strings.TrimSpace(claimed.Target), "task") {
		task, err := createTaskFromFeedbackWorkItem(ctx, s, claimed, workerID)
		if err != nil {
			var placementBlocked *feedbackWorkCloudPlacementBlockedError
			if errors.As(err, &placementBlocked) {
				return nil, err
			}
			authHeader := "Bearer " + strings.TrimSpace(s.token)
			_, _ = updateFeedbackWorkItemStatus(ctx, authHeader, claimed.ID, "blocked", "", "", "", "feedback worker could not create owner task", err.Error(), workerID)
			return nil, err
		}
		return &feedbackWorkTickResult{Item: claimed, TaskID: task.ID}, nil
	}
	if strings.EqualFold(strings.TrimSpace(claimed.Target), "issue") {
		draftPath, err := writeFeedbackIssueDraft(claimed)
		if err != nil {
			_, _ = updateFeedbackWorkItemStatus(ctx, authHeader, claimed.ID, "blocked", "", "", "", "feedback worker could not write local issue draft", err.Error(), workerID)
			return nil, err
		}
		if shouldCreateProviderIssues {
			issueURL, err := createFeedbackProviderIssue(claimed, draftPath)
			if err != nil {
				_, _ = updateFeedbackWorkItemStatus(ctx, authHeader, claimed.ID, "blocked", "", "", "", "feedback worker could not create provider issue", err.Error(), workerID)
				return nil, err
			}
			updated, err := updateFeedbackWorkItemStatus(
				ctx,
				authHeader,
				claimed.ID,
				"issue_created",
				"",
				issueURL,
				"",
				"feedback worker created provider issue from private local draft",
				"",
				workerID,
			)
			if err != nil {
				return nil, err
			}
			return &feedbackWorkTickResult{Item: updated}, nil
		}
		updated, err := updateFeedbackWorkItemStatus(
			ctx,
			authHeader,
			claimed.ID,
			"issue_draft_created",
			"",
			"",
			"",
			"feedback worker wrote a private local issue draft on the owner machine",
			"",
			workerID,
		)
		if err != nil {
			return nil, err
		}
		return &feedbackWorkTickResult{Item: updated}, nil
	}
	result, err := queueFeedbackWorkItemRelaySource(ctx, authHeader, claimed.ID, "", workerID)
	if err != nil {
		_, _ = updateFeedbackWorkItemStatus(ctx, authHeader, claimed.ID, "blocked", "", "", "", "feedback worker could not queue relay source work", err.Error(), workerID)
		return nil, err
	}
	if strings.TrimSpace(result.RelaySourceIntent.ID) == "" {
		_, _ = updateFeedbackWorkItemStatus(ctx, authHeader, claimed.ID, "blocked", "", "", "", "feedback worker queued no relay source intent", "", workerID)
		return nil, fmt.Errorf("backend returned no relay source intent")
	}
	return &feedbackWorkTickResult{Item: &result.Item, RelaySourceIntent: &result.RelaySourceIntent}, nil
}

func createFeedbackProviderIssueDefault(item *feedbackWorkItem, draftPath string) (string, error) {
	if item == nil {
		return "", fmt.Errorf("feedback work item required")
	}
	slug := strings.TrimSpace(item.ProjectSlug)
	if slug == "" {
		return "", fmt.Errorf("project slug is required for provider issue creation")
	}
	workDir, err := managedGitWorkDir(slug, "")
	if err != nil || strings.TrimSpace(workDir) == "" {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("project workdir not found")
	}
	if st, err := os.Stat(workDir); err != nil || !st.IsDir() {
		if err != nil {
			return "", fmt.Errorf("project workdir not found: %w", err)
		}
		return "", fmt.Errorf("project workdir is not a directory")
	}
	remoteURL, err := runGit(workDir, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("no origin remote: %s", strings.TrimSpace(remoteURL))
	}
	remote, err := parseGitRemote(remoteURL)
	if err != nil {
		return "", err
	}
	if remote.Provider == "" {
		remote.Provider = providerForHost(remote.Host)
	}
	if remote.Provider == "" {
		return "", fmt.Errorf("unsupported git host for provider issue: %s", remote.Host)
	}
	token := tokenForProviderHost(remote.Provider, remote.Host)
	if token == "" {
		return "", fmt.Errorf("no %s token configured on this machine", remote.Provider)
	}
	bodyBytes, err := os.ReadFile(draftPath)
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "Feedback issue"
	}
	switch remote.Provider {
	case "github":
		url, _, err := createIssueGitHub(remote.Host, token, remote.Owner, remote.Repo, title, string(bodyBytes))
		return url, err
	case "gitlab":
		url, _, err := createIssueGitLab(remote.Host, token, remote.Owner, remote.Repo, title, string(bodyBytes))
		return url, err
	default:
		return "", fmt.Errorf("unsupported provider: %s", remote.Provider)
	}
}

func writeFeedbackIssueDraft(item *feedbackWorkItem) (string, error) {
	if item == nil {
		return "", fmt.Errorf("feedback work item required")
	}
	baseDir := ""
	if slug := strings.TrimSpace(item.ProjectSlug); slug != "" {
		if workDir, err := managedGitWorkDir(slug, ""); err == nil && strings.TrimSpace(workDir) != "" {
			if st, statErr := os.Stat(workDir); statErr == nil && st.IsDir() {
				baseDir = filepath.Join(workDir, ".yaver", "feedback-issue-drafts")
			}
		}
	}
	if baseDir == "" {
		cfgDir, err := ConfigDir()
		if err != nil {
			return "", err
		}
		project := safeFeedbackIssueDraftSegment(firstNonEmpty(item.ProjectSlug, "unscoped"))
		baseDir = filepath.Join(cfgDir, "feedback-issue-drafts", project)
	}
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return "", err
	}
	name := safeFeedbackIssueDraftSegment(firstNonEmpty(item.ID, item.Title, "feedback")) + ".md"
	path := filepath.Join(baseDir, name)
	if err := os.WriteFile(path, []byte(feedbackWorkIssueDraftMarkdown(item)), 0600); err != nil {
		return "", err
	}
	return path, nil
}

func safeFeedbackIssueDraftSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "feedback"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 96 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "feedback"
	}
	if len(out) > 96 {
		out = out[:96]
		out = strings.TrimRight(out, "-")
	}
	return out
}

func feedbackWorkIssueDraftMarkdown(item *feedbackWorkItem) string {
	if item == nil {
		return ""
	}
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "Feedback issue"
	}
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	writeFeedbackIssueDraftField(&b, "Kind", item.Kind)
	writeFeedbackIssueDraftField(&b, "Priority", item.Priority)
	writeFeedbackIssueDraftField(&b, "Component", item.Component)
	writeFeedbackIssueDraftField(&b, "Platform", item.Platform)
	writeFeedbackIssueDraftField(&b, "App version", item.AppVersion)
	writeFeedbackIssueDraftField(&b, "Project", item.ProjectSlug)
	if body := strings.TrimSpace(item.Body); body != "" {
		b.WriteString("\n## Feedback\n\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

func writeFeedbackIssueDraftField(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString("- ")
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\n")
}

func createTaskFromFeedbackWorkItem(ctx context.Context, s *HTTPServer, item *feedbackWorkItem, workerID string) (*Task, error) {
	if s == nil || s.taskMgr == nil {
		return nil, fmt.Errorf("task manager unavailable")
	}
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "Feedback work item"
	}
	body := feedbackWorkTaskPrompt(item)
	workDir := ""
	if slug := strings.TrimSpace(item.ProjectSlug); slug != "" {
		if resolved, err := managedGitWorkDir(slug, ""); err == nil {
			workDir = resolved
		}
	}
	taskOpts := TaskCreateOptions{
		WorkDir:           workDir,
		InitialUserPrompt: body,
	}
	meta := taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
		KindHint:       "vibe",
		Title:          title,
		Description:    body,
		Source:         "feedback-work",
		ProjectName:    item.ProjectSlug,
		WorkDir:        firstNonEmpty(workDir, s.taskMgr.workDir),
		TargetDeviceID: s.deviceID,
	})
	if previewPlacement, perr := s.previewTaskPlacement(ctx, meta); perr != nil {
		log.Printf("[placement] feedback work preview skipped before task create: %v", perr)
	} else if shouldDeferLocalTaskForPlacement(previewPlacement, s.deviceID) {
		pendingTaskID := newPendingCloudTaskID()
		recordedPlacement := previewPlacement
		if placement, rerr := s.recordTaskPlacement(ctx, pendingTaskID, meta); rerr != nil {
			log.Printf("[placement] feedback work pending record skipped for %s: %v", pendingTaskID, rerr)
		} else if placement != nil {
			recordedPlacement = placement
		}
		var activation map[string]any
		if recordedPlacement != nil && (recordedPlacement.PlacementID != "" || pendingTaskID != "") {
			if result, aerr := s.activateTaskPlacement(ctx, recordedPlacement.PlacementID, pendingTaskID); aerr != nil {
				activation = activationMapFromError(aerr)
				log.Printf("[placement] feedback work activation skipped for %s: %v", pendingTaskID, aerr)
			} else {
				activation = result
			}
		}
		reason := "feedback work requires Cloud Workspace before task creation"
		if blocker := cloudActivationBlockerMessage(activation); blocker != "" {
			reason = blocker
		}
		authHeader := "Bearer " + strings.TrimSpace(s.token)
		_, _ = updateFeedbackWorkItemStatus(
			ctx,
			authHeader,
			item.ID,
			"blocked",
			pendingTaskID,
			"",
			"",
			"feedback worker deferred task creation to Cloud Workspace",
			reason,
			workerID,
		)
		return nil, &feedbackWorkCloudPlacementBlockedError{PendingTaskID: pendingTaskID, Reason: reason}
	} else if previewPlacement != nil {
		taskOpts.Placement = previewPlacement
	}
	task, err := s.taskMgr.CreateTaskWithOptions(title, body, "", "feedback-work", "", "", nil, taskOpts)
	if err != nil {
		return nil, err
	}
	authHeader := "Bearer " + strings.TrimSpace(s.token)
	_, err = updateFeedbackWorkItemStatus(
		ctx,
		authHeader,
		item.ID,
		"task_created",
		task.ID,
		"",
		"",
		"feedback worker created an owner-machine task",
		"",
		workerID,
	)
	if err != nil {
		return task, err
	}
	return task, nil
}

func feedbackWorkTaskPrompt(item *feedbackWorkItem) string {
	if item == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("A user submitted feedback for this project. Review it and make the smallest useful owner-approved task plan or fix.\n\n")
	if title := strings.TrimSpace(item.Title); title != "" {
		b.WriteString("Title: ")
		b.WriteString(title)
		b.WriteString("\n")
	}
	if kind := strings.TrimSpace(item.Kind); kind != "" {
		b.WriteString("Kind: ")
		b.WriteString(kind)
		b.WriteString("\n")
	}
	if priority := strings.TrimSpace(item.Priority); priority != "" {
		b.WriteString("Priority: ")
		b.WriteString(priority)
		b.WriteString("\n")
	}
	if component := strings.TrimSpace(item.Component); component != "" {
		b.WriteString("Component: ")
		b.WriteString(component)
		b.WriteString("\n")
	}
	if platform := strings.TrimSpace(item.Platform); platform != "" {
		b.WriteString("Platform: ")
		b.WriteString(platform)
		b.WriteString("\n")
	}
	if version := strings.TrimSpace(item.AppVersion); version != "" {
		b.WriteString("App version: ")
		b.WriteString(version)
		b.WriteString("\n")
	}
	if project := strings.TrimSpace(item.ProjectSlug); project != "" {
		b.WriteString("Project: ")
		b.WriteString(project)
		b.WriteString("\n")
	}
	if body := strings.TrimSpace(item.Body); body != "" {
		b.WriteString("\nFeedback:\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

func claimFeedbackWorkItem(ctx context.Context, authHeader, projectSlug, workerID string) (*feedbackWorkItem, error) {
	payload := map[string]any{}
	if projectSlug = strings.TrimSpace(projectSlug); projectSlug != "" {
		payload["projectSlug"] = projectSlug
	}
	if workerID = strings.TrimSpace(workerID); workerID != "" {
		payload["workerId"] = workerID
	}
	raw, err := postFeedbackWorkJSON(ctx, authHeader, "/feedback-work-items/claim", payload)
	if err != nil {
		return nil, err
	}
	var none struct {
		OK   bool `json:"ok"`
		Item any  `json:"item"`
	}
	if err := json.Unmarshal(raw, &none); err == nil && none.OK && none.Item == nil {
		return nil, nil
	}
	var out feedbackWorkItem
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.ID) == "" {
		return nil, nil
	}
	return &out, nil
}

func queueFeedbackWorkItemRelaySource(ctx context.Context, authHeader, itemID, branch, workerID string) (*feedbackWorkRelayQueueResult, error) {
	payload := map[string]any{"itemId": strings.TrimSpace(itemID)}
	if payload["itemId"] == "" {
		return nil, fmt.Errorf("itemId is required")
	}
	if branch = strings.TrimSpace(branch); branch != "" {
		payload["branch"] = branch
	}
	if workerID = strings.TrimSpace(workerID); workerID != "" {
		payload["workerId"] = workerID
	}
	raw, err := postFeedbackWorkJSON(ctx, authHeader, "/feedback-work-items/queue-relay-source", payload)
	if err != nil {
		return nil, err
	}
	var out feedbackWorkRelayQueueResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func updateFeedbackWorkItemStatus(ctx context.Context, authHeader, itemID, status, taskID, issueURL, branch, reason, lastError, workerID string) (*feedbackWorkItem, error) {
	payload := map[string]any{
		"itemId": strings.TrimSpace(itemID),
		"status": strings.TrimSpace(status),
	}
	if payload["itemId"] == "" || payload["status"] == "" {
		return nil, nil
	}
	if taskID = strings.TrimSpace(taskID); taskID != "" {
		payload["taskId"] = taskID
	}
	if issueURL = strings.TrimSpace(issueURL); issueURL != "" {
		payload["issueUrl"] = issueURL
	}
	if branch = strings.TrimSpace(branch); branch != "" {
		payload["branch"] = branch
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		payload["reason"] = reason
	}
	if lastError = strings.TrimSpace(lastError); lastError != "" {
		payload["lastError"] = lastError
	}
	if workerID = strings.TrimSpace(workerID); workerID != "" {
		payload["workerId"] = workerID
	}
	raw, err := postFeedbackWorkJSON(ctx, authHeader, "/feedback-work-items/status", payload)
	if err != nil {
		return nil, err
	}
	var out feedbackWorkItem
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func postFeedbackWorkJSON(ctx context.Context, authHeader, path string, payload map[string]any) ([]byte, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.ConvexSiteURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(defaultConvexSiteURL, "/")
	}
	token := bearerTokenFromAuthHeader(authHeader)
	if token == "" {
		token = strings.TrimSpace(cfg.AuthToken)
	}
	if token == "" {
		return nil, fmt.Errorf("missing auth token")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, taskPlacementHTTPTimeout)
	defer cancel()
	req, err := newBearerRequest(http.MethodPost, baseURL+path, token, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(reqCtx)
	resp, err := (&http.Client{Timeout: taskPlacementHTTPTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("backend status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}
