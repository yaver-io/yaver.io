package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	taskPlacementHTTPTimeout        = 1500 * time.Millisecond
	taskPlacementPreviewHTTPTimeout = 250 * time.Millisecond
)

type CloudWorkspaceRequiredError struct {
	PendingTaskID        string
	Placement            *TaskPlacementMetadata
	Activation           map[string]any
	Reason               string
	ClearedBlockedAction bool
}

type taskCreateHTTPResponse struct {
	OK       bool       `json:"ok"`
	TaskID   string     `json:"taskId"`
	Status   TaskStatus `json:"status"`
	RunnerID string     `json:"runnerId"`
	Model    string     `json:"model"`
	Error    string     `json:"error"`
}

type pendingCloudTaskDispatchRetryResult struct {
	LocalTaskID string
	TaskID      string
	TargetLabel string
	Err         error
	ProgressLog []cloudTaskHandoffProgress
}

type cloudTaskHandoffProgress struct {
	LocalTaskID    string
	Status         string
	TargetDeviceID string
	TargetLabel    string
	Attempt        int
	LastError      string
	Message        string
}

type cloudTaskHandoffProgressFunc func(cloudTaskHandoffProgress)

type taskPlacementActivationError struct {
	StatusCode int
	Body       map[string]any
	Raw        string
}

func (e *taskPlacementActivationError) Error() string {
	if e == nil {
		return ""
	}
	action := ""
	reason := ""
	if e.Body != nil {
		action, _ = e.Body["action"].(string)
		reason, _ = e.Body["reason"].(string)
		if reason == "" {
			reason, _ = e.Body["error"].(string)
		}
	}
	parts := []string{fmt.Sprintf("backend status %d", e.StatusCode)}
	if action = strings.TrimSpace(action); action != "" {
		parts = append(parts, "action="+action)
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		parts = append(parts, reason)
	} else if raw := strings.TrimSpace(e.Raw); raw != "" {
		parts = append(parts, raw)
	}
	return strings.Join(parts, ": ")
}

func activationMapFromError(err error) map[string]any {
	if err == nil {
		return nil
	}
	var activationErr *taskPlacementActivationError
	if errors.As(err, &activationErr) && activationErr.Body != nil {
		return activationErr.Body
	}
	return nil
}

type taskDispatchIntent struct {
	ID             string `json:"id"`
	LocalTaskID    string `json:"localTaskId"`
	Status         string `json:"status"`
	BlockedAction  string `json:"blockedAction,omitempty"`
	TaskID         string `json:"taskId,omitempty"`
	TargetDeviceID string `json:"targetDeviceId,omitempty"`
	Reason         string `json:"reason,omitempty"`
	LastError      string `json:"lastError,omitempty"`
	ExpiresAt      int64  `json:"expiresAt,omitempty"`
}

type relaySourceIntent struct {
	ID                        string `json:"id"`
	LocalTaskID               string `json:"localTaskId"`
	Status                    string `json:"status"`
	Branch                    string `json:"branch,omitempty"`
	BaseBranch                string `json:"baseBranch,omitempty"`
	ProjectSlug               string `json:"projectSlug,omitempty"`
	ProviderKind              string `json:"providerKind,omitempty"`
	ProviderHost              string `json:"providerHost,omitempty"`
	ProviderRepo              string `json:"providerRepo,omitempty"`
	ProviderBranch            string `json:"providerBranch,omitempty"`
	ProviderBranchURL         string `json:"providerBranchUrl,omitempty"`
	ProviderAppInstallationID string `json:"providerAppInstallationId,omitempty"`
	ProviderAuthMode          string `json:"providerAuthMode,omitempty"`
	ProviderAuthStatus        string `json:"providerAuthStatus,omitempty"`
}

type relaySourceGitHubAppToken struct {
	OK                        bool   `json:"ok"`
	ProviderKind              string `json:"providerKind"`
	ProviderHost              string `json:"providerHost"`
	ProviderRepo              string `json:"providerRepo"`
	ProviderBranch            string `json:"providerBranch"`
	ProviderBranchURL         string `json:"providerBranchUrl,omitempty"`
	ProviderAppInstallationID string `json:"providerAppInstallationId,omitempty"`
	ProviderAuthMode          string `json:"providerAuthMode"`
	ProviderAuthStatus        string `json:"providerAuthStatus"`
	Token                     string `json:"token"`
	ExpiresAt                 string `json:"expiresAt,omitempty"`
}

func (e *CloudWorkspaceRequiredError) Error() string {
	parts := []string{"cloud workspace required"}
	if e != nil {
		if e.PendingTaskID != "" {
			parts = append(parts, "pendingTaskId="+e.PendingTaskID)
		}
		if e.Placement != nil {
			if e.Placement.TargetDeviceID != "" {
				parts = append(parts, "targetDeviceId="+e.Placement.TargetDeviceID)
			}
			if e.Placement.Lane != "" {
				parts = append(parts, "lane="+e.Placement.Lane)
			}
		}
		if e.Reason != "" {
			parts = append(parts, e.Reason)
		}
	}
	return strings.Join(parts, "; ")
}

func decodeCloudWorkspaceRequiredError(status int, raw []byte) error {
	if status != http.StatusConflict || len(raw) == 0 {
		return nil
	}
	var body struct {
		Action        string                 `json:"action"`
		PendingTaskID string                 `json:"pendingTaskId"`
		Placement     *TaskPlacementMetadata `json:"placement"`
		Activation    map[string]any         `json:"activation"`
		Reason        string                 `json:"reason"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil
	}
	if body.Action != "cloud_workspace_required" {
		return nil
	}
	return &CloudWorkspaceRequiredError{
		PendingTaskID: body.PendingTaskID,
		Placement:     body.Placement,
		Activation:    body.Activation,
		Reason:        body.Reason,
	}
}

type taskPlacementRequestInput struct {
	KindHint         string
	Title            string
	Description      string
	CustomCommand    string
	Source           string
	Runner           string
	ProjectName      string
	WorkDir          string
	TargetDeviceID   string
	ForceCloud       bool
	ForceRelaySource bool
}

type TaskIngressPlacementConfig struct {
	ConvexURL     string
	Token         string
	LocalDeviceID string
	WorkDir       string
}

type taskIngressCloudDeferral struct {
	PendingTaskID string
	Placement     *TaskPlacementMetadata
	Activation    map[string]any
	Blocker       string
}

type taskPlacementRecordRequest struct {
	TaskID           string `json:"taskId,omitempty"`
	Kind             string `json:"kind"`
	SourceSurface    string `json:"sourceSurface,omitempty"`
	ProjectSlug      string `json:"projectSlug,omitempty"`
	RequestedRunner  string `json:"requestedRunner,omitempty"`
	TargetDeviceID   string `json:"targetDeviceId,omitempty"`
	ForceCloud       bool   `json:"forceCloud,omitempty"`
	ForceRelaySource bool   `json:"forceRelaySource,omitempty"`
	AppCount         int    `json:"appCount,omitempty"`
	RepoSizeMb       int    `json:"repoSizeMb,omitempty"`
	FileCount        int    `json:"fileCount,omitempty"`
	HasNativeMobile  bool   `json:"hasNativeMobile,omitempty"`
	HasDocker        bool   `json:"hasDocker,omitempty"`
}

type taskPlacementBackendClient struct {
	baseURL string
	token   string
}

func newTaskPlacementBackendClient(baseURL, token string) (*taskPlacementBackendClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, fmt.Errorf("missing backend auth")
	}
	return &taskPlacementBackendClient{baseURL: baseURL, token: token}, nil
}

// isScannableProjectDir reports whether dir is worth recursively classifying.
//
// The rule is narrow on purpose: only scan somewhere that actually looks like a
// project root. Everything about this is resolved at runtime — a remote box can
// be any OS, any username, any layout — so nothing here may hardcode a path.
//
// Rejects, in order:
//   - empty / "." — an unspecified workDir means "we don't know", and the
//     daemon's CWD is not a safe stand-in. This is the exact default that made
//     POST /tasks scan a home directory and hang forever (2026-07-20).
//   - the user's home directory, and the filesystem root — neither is a repo,
//     and both are unbounded to walk.
//   - anything that is not a directory.
//   - a directory with no project marker at all.
//
// Callers that need metadata for an unscannable directory get empty values, and
// must treat that as "unknown", never as "no projects found".
func isScannableProjectDir(dir string) bool {
	d := strings.TrimSpace(dir)
	if d == "" || d == "." {
		return false
	}
	abs, err := filepath.Abs(d)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	if abs == string(filepath.Separator) {
		return false
	}
	if home, err := os.UserHomeDir(); err == nil {
		if h := filepath.Clean(home); h != "" && abs == h {
			return false
		}
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return false
	}
	// Require a marker so we never recurse through an arbitrary directory that
	// merely happens to be named as a workDir.
	for _, marker := range []string{
		".git", "package.json", "go.mod", "pubspec.yaml", "Cargo.toml",
		"pyproject.toml", "requirements.txt", "Gemfile", "pom.xml",
		"build.gradle", "build.gradle.kts", "yaver.workspace.yaml",
	} {
		if _, err := os.Stat(filepath.Join(abs, marker)); err == nil {
			return true
		}
	}
	return false
}

func taskPlacementRequestFromTaskBody(in taskPlacementRequestInput) taskPlacementRecordRequest {
	workDir := strings.TrimSpace(in.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	projectSlug := basenameSlug(firstNonEmpty(strings.TrimSpace(in.ProjectName), workDir))
	project := DetectProjectInfo(workDir)

	// Only classify a directory that is plausibly a project root.
	//
	// 2026-07-20: workDir defaulted to "." — the agent's CWD — which on a real
	// box was the user's HOME directory. taskPlacementStackLabel then ran
	// DetectMonorepo across the entire home tree on EVERY task creation, and
	// POST /tasks never returned. The phone reported the machine unreachable
	// while it was perfectly healthy.
	//
	// Resolved dynamically, never against a hardcoded path: a remote box can be
	// any OS, any user, any layout. A home directory is not a monorepo, and an
	// unspecified workDir is not a licence to scan whatever the daemon happens
	// to be sitting in.
	var stackLabel string
	var appCount, fileCount, repoSizeMb int
	if isScannableProjectDir(workDir) {
		stackLabel = taskPlacementStackLabel(project, workDir)
		appCount, fileCount, repoSizeMb = boundedRepoMetrics(workDir)
	}
	return taskPlacementRecordRequest{
		Kind:             inferPlacementTaskKind(in.KindHint, in.Title, in.Description, in.CustomCommand, in.Source),
		SourceSurface:    strings.TrimSpace(in.Source),
		ProjectSlug:      projectSlug,
		RequestedRunner:  strings.TrimSpace(in.Runner),
		TargetDeviceID:   strings.TrimSpace(in.TargetDeviceID),
		ForceCloud:       in.ForceCloud,
		ForceRelaySource: in.ForceRelaySource,
		AppCount:         appCount,
		RepoSizeMb:       repoSizeMb,
		FileCount:        fileCount,
		HasNativeMobile:  hasNativeMobileProjectSignal(workDir, stackLabel),
		HasDocker:        hasDockerProjectSignal(workDir, stackLabel),
	}
}

func (s *HTTPServer) recordTaskPlacement(ctx context.Context, taskID string, meta taskPlacementRecordRequest) (*TaskPlacementMetadata, error) {
	if s == nil {
		return nil, fmt.Errorf("server unavailable")
	}
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("missing backend auth")
	}
	client, err := newTaskPlacementBackendClient(s.convexURL, s.token)
	if err != nil {
		return nil, err
	}
	meta.TaskID = strings.TrimSpace(taskID)
	return client.postTaskPlacement(ctx, "/tasks/placement/record", meta)
}

func (s *HTTPServer) previewTaskPlacement(ctx context.Context, meta taskPlacementRecordRequest) (*TaskPlacementMetadata, error) {
	if s == nil {
		return nil, fmt.Errorf("server unavailable")
	}
	client, err := newTaskPlacementBackendClient(s.convexURL, s.token)
	if err != nil {
		return nil, err
	}
	meta.TaskID = ""
	return client.postTaskPlacementWithTimeout(ctx, "/tasks/placement/preview", meta, taskPlacementPreviewHTTPTimeout)
}

func (s *HTTPServer) activateTaskPlacement(ctx context.Context, placementID, taskID string) (map[string]any, error) {
	if s == nil {
		return nil, fmt.Errorf("server unavailable")
	}
	client, err := newTaskPlacementBackendClient(s.convexURL, s.token)
	if err != nil {
		return nil, err
	}
	return client.activateTaskPlacement(ctx, placementID, taskID)
}

func (c *taskPlacementBackendClient) activateTaskPlacement(ctx context.Context, placementID, taskID string) (map[string]any, error) {
	payload := map[string]string{}
	if id := strings.TrimSpace(placementID); id != "" {
		payload["placementId"] = id
	}
	if id := strings.TrimSpace(taskID); id != "" {
		payload["taskId"] = id
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("placementId or taskId is required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, taskPlacementHTTPTimeout)
	defer cancel()
	req, err := newBearerRequest(http.MethodPost, c.baseURL+"/tasks/placement/activate", c.token, bytes.NewReader(body))
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
	var out map[string]any
	decodeErr := json.Unmarshal(raw, &out)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &taskPlacementActivationError{
			StatusCode: resp.StatusCode,
			Body:       out,
			Raw:        strings.TrimSpace(string(raw)),
		}
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	return out, nil
}

func rebindCloudTaskPlacement(ctx context.Context, placementID, taskID, status, authHeader string) error {
	placementID = strings.TrimSpace(placementID)
	taskID = strings.TrimSpace(taskID)
	if placementID == "" || taskID == "" {
		return nil
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return fmt.Errorf("load config: %w", err)
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
		return fmt.Errorf("missing auth token")
	}
	payload := map[string]string{
		"placementId": placementID,
		"taskId":      taskID,
	}
	if s := strings.TrimSpace(status); s != "" {
		payload["status"] = s
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, taskPlacementHTTPTimeout)
	defer cancel()
	req, err := newBearerRequest(http.MethodPost, baseURL+"/tasks/placement/rebind", token, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req = req.WithContext(reqCtx)
	resp, err := (&http.Client{Timeout: taskPlacementHTTPTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("backend status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func markCloudTaskPlacementStatus(ctx context.Context, placementID, status, authHeader string) error {
	placementID = strings.TrimSpace(placementID)
	status = strings.TrimSpace(status)
	if placementID == "" || status == "" {
		return nil
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return fmt.Errorf("load config: %w", err)
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
		return fmt.Errorf("missing auth token")
	}
	payload := map[string]string{
		"placementId": placementID,
		"status":      status,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, taskPlacementHTTPTimeout)
	defer cancel()
	req, err := newBearerRequest(http.MethodPost, baseURL+"/tasks/placement/status", token, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req = req.WithContext(reqCtx)
	resp, err := (&http.Client{Timeout: taskPlacementHTTPTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("backend status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func finalTaskPlacementStatus(taskStatus TaskStatus) string {
	switch taskStatus {
	case TaskStatusFinished:
		return "completed"
	case TaskStatusFailed, TaskStatusStopped:
		return "failed"
	default:
		return ""
	}
}

func syncFinalTaskPlacementStatus(ctx context.Context, task *Task, authHeader string) error {
	if task == nil || task.Placement == nil {
		return nil
	}
	status := finalTaskPlacementStatus(task.Status)
	if status == "" {
		return nil
	}
	return markCloudTaskPlacementStatus(ctx, task.Placement.PlacementID, status, authHeader)
}

func createCloudTaskDispatchIntent(ctx context.Context, cloudErr *CloudWorkspaceRequiredError, sourceSurface, requestedRunner, projectSlug, authHeader string) (*taskDispatchIntent, error) {
	if cloudErr == nil {
		return nil, nil
	}
	localTaskID := strings.TrimSpace(cloudErr.PendingTaskID)
	if localTaskID == "" {
		return nil, nil
	}
	payload := map[string]any{
		"localTaskId":   localTaskID,
		"sourceSurface": firstNonEmpty(strings.TrimSpace(sourceSurface), "desktop"),
		"ttlMs":         24 * 60 * 60 * 1000,
	}
	if cloudErr.Placement != nil {
		if id := strings.TrimSpace(cloudErr.Placement.PlacementID); id != "" {
			payload["placementId"] = id
		}
		if lane := strings.TrimSpace(cloudErr.Placement.Lane); lane != "" {
			payload["lane"] = lane
		}
		if target := strings.TrimSpace(cloudErr.Placement.TargetDeviceID); target != "" {
			payload["targetDeviceId"] = target
		}
		if cloudMachine := strings.TrimSpace(cloudErr.Placement.CloudMachineID); cloudMachine != "" {
			payload["cloudMachineId"] = cloudMachine
		}
	}
	if runner := strings.TrimSpace(requestedRunner); runner != "" {
		payload["requestedRunner"] = runner
	}
	if slug := strings.TrimSpace(projectSlug); slug != "" {
		payload["projectSlug"] = slug
	}
	if reason := strings.TrimSpace(cloudErr.Reason); reason != "" {
		payload["reason"] = reason
	}
	return postTaskDispatchIntent(ctx, "/tasks/dispatch-intents", payload, authHeader)
}

func updateCloudTaskDispatchIntent(ctx context.Context, intent *taskDispatchIntent, localTaskID, status, taskID, targetDeviceID, lastError, reason, blockedAction, authHeader string, bumpAttempt, clearBlockedAction bool) (*taskDispatchIntent, error) {
	payload := map[string]any{
		"status": status,
	}
	if intent != nil && strings.TrimSpace(intent.ID) != "" {
		payload["intentId"] = strings.TrimSpace(intent.ID)
	} else if strings.TrimSpace(localTaskID) != "" {
		payload["localTaskId"] = strings.TrimSpace(localTaskID)
	} else {
		return nil, nil
	}
	if taskID = strings.TrimSpace(taskID); taskID != "" {
		payload["taskId"] = taskID
	}
	if targetDeviceID = strings.TrimSpace(targetDeviceID); targetDeviceID != "" {
		payload["targetDeviceId"] = targetDeviceID
	}
	if lastError = strings.TrimSpace(lastError); lastError != "" {
		payload["lastError"] = lastError
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		payload["reason"] = reason
	}
	if blockedAction = strings.TrimSpace(blockedAction); blockedAction != "" {
		payload["blockedAction"] = blockedAction
	}
	if bumpAttempt {
		payload["bumpAttempt"] = true
	}
	if clearBlockedAction {
		payload["clearBlockedAction"] = true
	}
	return postTaskDispatchIntent(ctx, "/tasks/dispatch-intents/status", payload, authHeader)
}

func ensurePromptFreeConvexMetadataPayload(payload map[string]any) error {
	for key := range payload {
		normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "_", ""), "-", ""))
		switch normalized {
		case "title", "description", "prompt", "userprompt", "input", "body", "bodyjson", "workdir", "gitremote", "gitbranch", "diff", "patch", "sourcecode", "customcommand":
			return fmt.Errorf("refusing to send sensitive task field %q to Convex metadata", key)
		}
	}
	return nil
}

func postTaskDispatchIntent(ctx context.Context, path string, payload map[string]any, authHeader string) (*taskDispatchIntent, error) {
	if err := ensurePromptFreeConvexMetadataPayload(payload); err != nil {
		return nil, err
	}
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
	var out taskDispatchIntent
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func listCloudTaskDispatchIntents(ctx context.Context, authHeader string, limit int) ([]taskDispatchIntent, error) {
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
	if limit <= 0 {
		limit = 80
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	reqCtx, cancel := context.WithTimeout(ctx, taskPlacementHTTPTimeout)
	defer cancel()
	req, err := newBearerRequest(http.MethodGet, baseURL+"/tasks/dispatch-intents?"+query.Encode(), token, nil)
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
	var out []taskDispatchIntent
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func refreshPendingCloudTaskDispatchFromBackend(ctx context.Context, row pendingCloudTaskDispatch, authHeader string) pendingCloudTaskDispatch {
	intents, err := listCloudTaskDispatchIntents(ctx, authHeader, 80)
	if err != nil {
		return row
	}
	wasUserAction := pendingCloudTaskDispatchNeedsUserAction(row)
	localTaskID := strings.TrimSpace(row.LocalTaskID)
	intentID := strings.TrimSpace(row.DispatchIntentID)
	for _, intent := range intents {
		if localTaskID != "" && strings.TrimSpace(intent.LocalTaskID) != localTaskID &&
			(intentID == "" || strings.TrimSpace(intent.ID) != intentID) {
			continue
		}
		if id := strings.TrimSpace(intent.ID); id != "" {
			row.DispatchIntentID = id
		}
		if status := strings.TrimSpace(intent.Status); status != "" {
			row.Status = status
		}
		row.BlockedAction = strings.TrimSpace(intent.BlockedAction)
		if target := strings.TrimSpace(intent.TargetDeviceID); target != "" && row.Placement != nil {
			row.Placement.TargetDeviceID = target
		}
		if errText := strings.TrimSpace(firstNonEmpty(intent.LastError, intent.Reason)); errText != "" {
			row.LastError = errText
		} else if strings.TrimSpace(row.Status) != "blocked" {
			row.LastError = ""
		}
		if intent.ExpiresAt > 0 {
			row.ExpiresAt = time.UnixMilli(intent.ExpiresAt)
		}
		row = normalizePendingCloudTaskDispatch(row, time.Now())
		row.ClearedBlocker = wasUserAction && !pendingCloudTaskDispatchNeedsUserAction(row)
		return row
	}
	return row
}

func updateRelaySourceIntentStatus(ctx context.Context, authHeader, intentID, localTaskID, status, taskID, relayID, reason, lastError string, bumpAttempt bool) (*relaySourceIntent, error) {
	payload := map[string]any{"status": strings.TrimSpace(status)}
	if payload["status"] == "" {
		return nil, nil
	}
	if intentID = strings.TrimSpace(intentID); intentID != "" {
		payload["intentId"] = intentID
	} else if localTaskID = strings.TrimSpace(localTaskID); localTaskID != "" {
		payload["localTaskId"] = localTaskID
	} else {
		return nil, nil
	}
	if taskID = strings.TrimSpace(taskID); taskID != "" {
		payload["taskId"] = taskID
	}
	if relayID = strings.TrimSpace(relayID); relayID != "" {
		payload["relayId"] = relayID
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		payload["reason"] = reason
	}
	if lastError = strings.TrimSpace(lastError); lastError != "" {
		payload["lastError"] = lastError
	}
	if bumpAttempt {
		payload["bumpAttempt"] = true
	}
	if err := ensurePromptFreeConvexMetadataPayload(payload); err != nil {
		return nil, err
	}
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
	req, err := newBearerRequest(http.MethodPost, baseURL+"/tasks/relay-source-intents/status", token, bytes.NewReader(body))
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
	var out relaySourceIntent
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func updateRelaySourceIntentProviderBranch(ctx context.Context, authHeader, intentID, localTaskID, status, relayID, reason string, branch ManagedGitRelaySourceProviderBranch) (*relaySourceIntent, error) {
	payload := map[string]any{"status": strings.TrimSpace(status)}
	if payload["status"] == "" {
		return nil, nil
	}
	if intentID = strings.TrimSpace(intentID); intentID != "" {
		payload["intentId"] = intentID
	} else if localTaskID = strings.TrimSpace(localTaskID); localTaskID != "" {
		payload["localTaskId"] = localTaskID
	} else {
		return nil, nil
	}
	if relayID = strings.TrimSpace(relayID); relayID != "" {
		payload["relayId"] = relayID
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		payload["reason"] = reason
	}
	payload["providerKind"] = branch.ProviderKind
	payload["providerHost"] = branch.ProviderHost
	payload["providerRepo"] = branch.ProviderRepo
	payload["providerBranch"] = branch.ProviderBranch
	payload["providerBranchUrl"] = branch.ProviderBranchURL
	payload["providerAuthMode"] = branch.ProviderAuthMode
	payload["providerAuthStatus"] = branch.ProviderAuthStatus
	if err := ensurePromptFreeConvexMetadataPayload(payload); err != nil {
		return nil, err
	}
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
	req, err := newBearerRequest(http.MethodPost, baseURL+"/tasks/relay-source-intents/status", token, bytes.NewReader(body))
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
	var out relaySourceIntent
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func requestRelaySourceGitHubAppToken(ctx context.Context, authHeader, intentID, localTaskID string) (*relaySourceGitHubAppToken, error) {
	payload := map[string]any{}
	if intentID = strings.TrimSpace(intentID); intentID != "" {
		payload["intentId"] = intentID
	} else if localTaskID = strings.TrimSpace(localTaskID); localTaskID != "" {
		payload["localTaskId"] = localTaskID
	} else {
		return nil, fmt.Errorf("intentId or localTaskId required")
	}
	if err := ensurePromptFreeConvexMetadataPayload(payload); err != nil {
		return nil, err
	}
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
	req, err := newBearerRequest(http.MethodPost, baseURL+"/tasks/relay-source-intents/github-app-token", token, bytes.NewReader(body))
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
	var out relaySourceGitHubAppToken
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.Token) == "" {
		return nil, fmt.Errorf("github app token missing")
	}
	return &out, nil
}

func markRelaySourceGitLabScopedTokenUnsupported(ctx context.Context, authHeader, intentID, localTaskID string) error {
	payload := map[string]any{}
	if intentID = strings.TrimSpace(intentID); intentID != "" {
		payload["intentId"] = intentID
	} else if localTaskID = strings.TrimSpace(localTaskID); localTaskID != "" {
		payload["localTaskId"] = localTaskID
	} else {
		return fmt.Errorf("intentId or localTaskId required")
	}
	if err := ensurePromptFreeConvexMetadataPayload(payload); err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return fmt.Errorf("load config: %w", err)
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
		return fmt.Errorf("missing auth token")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, taskPlacementHTTPTimeout)
	defer cancel()
	req, err := newBearerRequest(http.MethodPost, baseURL+"/tasks/relay-source-intents/gitlab-token", token, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req = req.WithContext(reqCtx)
	resp, err := (&http.Client{Timeout: taskPlacementHTTPTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode == http.StatusNotImplemented {
		return fmt.Errorf("gitlab scoped token unsupported: %s", strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("backend status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func claimRelaySourceIntent(ctx context.Context, authHeader, projectSlug, relayID string) (*relaySourceIntent, error) {
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
	payload := map[string]any{}
	if projectSlug = strings.TrimSpace(projectSlug); projectSlug != "" {
		payload["projectSlug"] = projectSlug
	}
	if relayID = strings.TrimSpace(relayID); relayID != "" {
		payload["relayId"] = relayID
	}
	if err := ensurePromptFreeConvexMetadataPayload(payload); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, taskPlacementHTTPTimeout)
	defer cancel()
	req, err := newBearerRequest(http.MethodPost, baseURL+"/tasks/relay-source-intents/claim", token, bytes.NewReader(body))
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
	var none struct {
		OK     bool `json:"ok"`
		Intent any  `json:"intent"`
	}
	if err := json.Unmarshal(raw, &none); err == nil && none.OK && none.Intent == nil {
		return nil, nil
	}
	var out relaySourceIntent
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.ID) == "" {
		return nil, nil
	}
	return &out, nil
}

func (c *taskPlacementBackendClient) postTaskPlacement(ctx context.Context, path string, meta taskPlacementRecordRequest) (*TaskPlacementMetadata, error) {
	return c.postTaskPlacementWithTimeout(ctx, path, meta, taskPlacementHTTPTimeout)
}

func (c *taskPlacementBackendClient) postTaskPlacementWithTimeout(ctx context.Context, path string, meta taskPlacementRecordRequest, timeout time.Duration) (*TaskPlacementMetadata, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" || strings.TrimSpace(c.token) == "" {
		return nil, fmt.Errorf("missing backend auth")
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = taskPlacementHTTPTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := newBearerRequest(http.MethodPost, c.baseURL+path, c.token, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(reqCtx)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("backend status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return decodeTaskPlacementMetadata(raw)
}

func decodeTaskPlacementMetadata(raw []byte) (*TaskPlacementMetadata, error) {
	var out struct {
		ID                  string              `json:"id"`
		Lane                string              `json:"lane"`
		ResourceClass       string              `json:"resourceClass"`
		TargetDeviceID      string              `json:"targetDeviceId"`
		CloudMachineID      string              `json:"cloudMachineId"`
		SubscriptionPlan    string              `json:"subscriptionPlan"`
		Entitlement         string              `json:"entitlement"`
		Status              string              `json:"status"`
		Reason              string              `json:"reason"`
		WakeRequired        bool                `json:"wakeRequired"`
		WakeTargetMs        int                 `json:"wakeTargetMs"`
		EstimatedCreditCost int                 `json:"estimatedCreditCost"`
		CreditEstimate      *TaskCreditEstimate `json:"creditEstimate"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &TaskPlacementMetadata{
		PlacementID:        out.ID,
		Lane:               out.Lane,
		ResourceClass:      out.ResourceClass,
		TargetDeviceID:     out.TargetDeviceID,
		CloudMachineID:     out.CloudMachineID,
		SubscriptionPlan:   out.SubscriptionPlan,
		Entitlement:        out.Entitlement,
		Status:             out.Status,
		Reason:             out.Reason,
		WakeRequired:       out.WakeRequired,
		WakeTargetMs:       out.WakeTargetMs,
		EstimatedCostCents: out.EstimatedCreditCost,
		CreditEstimate:     out.CreditEstimate,
	}, nil
}

func shouldDeferLocalTaskForPlacement(placement *TaskPlacementMetadata, localDeviceID string) bool {
	if placement == nil || !strings.HasPrefix(strings.TrimSpace(placement.Lane), "cloud_") {
		return false
	}
	target := strings.TrimSpace(placement.TargetDeviceID)
	local := strings.TrimSpace(localDeviceID)
	if target != "" && local != "" && target == local {
		return false
	}
	if target != "" && target != local {
		return true
	}
	return placement.WakeRequired
}

func deferIngressTaskToCloudWorkspace(ctx context.Context, cfg TaskIngressPlacementConfig, sourceSurface, kindHint string, runner ...string) (*taskIngressCloudDeferral, bool, error) {
	client, err := newTaskPlacementBackendClient(cfg.ConvexURL, cfg.Token)
	if err != nil {
		return nil, false, err
	}
	requestedRunner := ""
	if len(runner) > 0 {
		requestedRunner = strings.TrimSpace(runner[0])
	}
	meta := taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
		KindHint: strings.TrimSpace(kindHint),
		Source:   strings.TrimSpace(sourceSurface),
		Runner:   requestedRunner,
		WorkDir:  cfg.WorkDir,
	})
	preview, err := client.postTaskPlacement(ctx, "/tasks/placement/preview", meta)
	if err != nil {
		return nil, false, err
	}
	if !shouldDeferLocalTaskForPlacement(preview, cfg.LocalDeviceID) {
		return nil, false, nil
	}

	pendingTaskID := newPendingCloudTaskID()
	recordedPlacement, err := client.postTaskPlacement(ctx, "/tasks/placement/record", func() taskPlacementRecordRequest {
		meta.TaskID = pendingTaskID
		return meta
	}())
	if err != nil {
		return &taskIngressCloudDeferral{PendingTaskID: pendingTaskID, Placement: preview}, true, err
	}
	if recordedPlacement == nil {
		recordedPlacement = preview
	}
	var activation map[string]any
	if strings.TrimSpace(recordedPlacement.PlacementID) != "" {
		activation, err = client.activateTaskPlacement(ctx, recordedPlacement.PlacementID, pendingTaskID)
		if err != nil {
			return &taskIngressCloudDeferral{PendingTaskID: pendingTaskID, Placement: recordedPlacement}, true, err
		}
	}
	return &taskIngressCloudDeferral{
		PendingTaskID: pendingTaskID,
		Placement:     recordedPlacement,
		Activation:    activation,
		Blocker:       cloudActivationBlockerMessage(activation),
	}, true, nil
}

func (s *HTTPServer) deferIngressTaskToCloudWorkspace(ctx context.Context, sourceSurface, kindHint, runner, workDir string) (*taskIngressCloudDeferral, bool, error) {
	if s == nil || s.taskMgr == nil {
		return nil, false, fmt.Errorf("server unavailable")
	}
	cfg := TaskIngressPlacementConfig{
		ConvexURL:     s.convexURL,
		Token:         s.token,
		LocalDeviceID: s.deviceID,
		WorkDir:       firstNonEmpty(strings.TrimSpace(workDir), s.taskMgr.workDir),
	}
	return deferIngressTaskToCloudWorkspace(ctx, cfg, sourceSurface, kindHint, runner)
}

func newPendingCloudTaskID() string {
	return "pending-cloud:" + uuid.NewString()
}

func cloudActivationBlockerMessage(activation map[string]any) string {
	action := cloudActivationBlockerAction(activation)
	if action == "" {
		return ""
	}
	reason, _ := activation["reason"].(string)
	if reason = strings.TrimSpace(reason); reason != "" {
		return action + ": " + reason
	}
	errText, _ := activation["error"].(string)
	if errText = strings.TrimSpace(errText); errText != "" {
		return action + ": " + errText
	}
	return action
}

func cloudActivationBlockerAction(activation map[string]any) string {
	if activation == nil {
		return ""
	}
	action, _ := activation["action"].(string)
	action = strings.TrimSpace(action)
	switch action {
	case "runner_auth_required", "yaver_auth_required", "wake_failed", "resize_required", "resize_failed", "subscription_required", "billing_required":
		return action
	default:
		return ""
	}
}

func createTaskOnCloudWorkspace(ctx context.Context, cloudErr *CloudWorkspaceRequiredError, authHeader string, bodyJSON []byte, wait time.Duration, progressFns ...cloudTaskHandoffProgressFunc) (RemoteAgentCandidate, *taskCreateHTTPResponse, error) {
	if cloudErr == nil || cloudErr.Placement == nil || strings.TrimSpace(cloudErr.Placement.TargetDeviceID) == "" {
		return RemoteAgentCandidate{}, nil, fmt.Errorf("cloud placement has no target device")
	}
	emitProgress := func(p cloudTaskHandoffProgress) {
		p.LocalTaskID = firstNonEmpty(strings.TrimSpace(p.LocalTaskID), strings.TrimSpace(cloudErr.PendingTaskID))
		if cloudErr.Placement != nil {
			p.TargetDeviceID = firstNonEmpty(strings.TrimSpace(p.TargetDeviceID), strings.TrimSpace(cloudErr.Placement.TargetDeviceID))
		}
		for _, fn := range progressFns {
			if fn != nil {
				fn(p)
			}
		}
	}
	token := bearerTokenFromAuthHeader(authHeader)
	if token == "" {
		cfg, _ := LoadConfig()
		if cfg != nil {
			token = strings.TrimSpace(cfg.AuthToken)
		}
	}
	if token == "" {
		return RemoteAgentCandidate{}, nil, fmt.Errorf("missing auth token for cloud handoff")
	}
	bodyJSON, err := taskBodyWithLocalFallback(bodyJSON)
	if err != nil {
		return RemoteAgentCandidate{}, nil, err
	}
	sourceSurface, requestedRunner, projectSlug := taskIntentLabelsFromTaskBody(bodyJSON)
	if err := savePendingCloudTaskDispatch(pendingCloudTaskDispatch{
		LocalTaskID:     cloudErr.PendingTaskID,
		PlacementID:     cloudErr.Placement.PlacementID,
		Placement:       cloudErr.Placement,
		Status:          "queued",
		SourceSurface:   sourceSurface,
		RequestedRunner: requestedRunner,
		ProjectSlug:     projectSlug,
		BodyJSON:        append([]byte(nil), bodyJSON...),
	}); err != nil {
		logPendingCloudDispatchStoreError("save", cloudErr.PendingTaskID, err)
	}
	intent, _ := createCloudTaskDispatchIntent(ctx, cloudErr, sourceSurface, requestedRunner, projectSlug, authHeader)
	if intent != nil && strings.TrimSpace(intent.ID) != "" {
		_ = patchPendingCloudTaskDispatch(cloudErr.PendingTaskID, func(row *pendingCloudTaskDispatch) {
			row.DispatchIntentID = strings.TrimSpace(intent.ID)
			if intent.ExpiresAt > 0 {
				row.ExpiresAt = time.UnixMilli(intent.ExpiresAt)
			}
		})
	}
	targetDeviceID := strings.TrimSpace(cloudErr.Placement.TargetDeviceID)
	if blocker := cloudActivationBlockerMessage(cloudErr.Activation); blocker != "" {
		blockedAction := cloudActivationBlockerAction(cloudErr.Activation)
		_, _ = updateCloudTaskDispatchIntent(ctx, intent, cloudErr.PendingTaskID, "blocked", "", targetDeviceID, blocker, "cloud workspace activation needs user action before dispatch", cloudActivationBlockerAction(cloudErr.Activation), authHeader, false, false)
		_ = patchPendingCloudTaskDispatch(cloudErr.PendingTaskID, func(row *pendingCloudTaskDispatch) {
			row.Status = "blocked"
			row.LastError = blocker
			row.BlockedAction = blockedAction
		})
		emitProgress(cloudTaskHandoffProgress{
			Status:    "blocked",
			LastError: blocker,
			Message:   "cloud workspace activation needs user action before dispatch",
		})
		return RemoteAgentCandidate{}, nil, fmt.Errorf("%w; %s", cloudErr, blocker)
	}
	deadline := time.Now().Add(wait)
	var lastErr error
	attempt := 0
	emitProgress(cloudTaskHandoffProgress{
		Status:  "queued",
		Message: "Cloud Workspace selected; waiting for target workspace to become reachable",
	})
	for {
		if ctx.Err() != nil {
			return RemoteAgentCandidate{}, nil, ctx.Err()
		}
		candidates, _, err := resolveRemoteAgentCandidates(targetDeviceID)
		if err != nil {
			lastErr = err
			emitProgress(cloudTaskHandoffProgress{
				Status:    "queued",
				Attempt:   attempt,
				LastError: err.Error(),
				Message:   "workspace is not reachable yet",
			})
		} else {
			attempt++
			_, _ = updateCloudTaskDispatchIntent(ctx, intent, cloudErr.PendingTaskID, "dispatching", "", targetDeviceID, "", "workspace reachable; dispatching prompt-held task", "", authHeader, true, cloudErr.ClearedBlockedAction)
			_ = patchPendingCloudTaskDispatch(cloudErr.PendingTaskID, func(row *pendingCloudTaskDispatch) {
				row.Status = "dispatching"
				row.Attempts++
				row.LastError = ""
				row.BlockedAction = ""
				row.ClearedBlocker = false
			})
			emitProgress(cloudTaskHandoffProgress{
				Status:  "dispatching",
				Attempt: attempt,
				Message: "workspace reachable; sending task",
			})
			chosen, status, raw, err := doRemoteAgentRequest(ctx, candidates, token, http.MethodPost, "/tasks", bodyJSON, 12*time.Second)
			if err != nil {
				lastErr = err
				_, _ = updateCloudTaskDispatchIntent(ctx, intent, cloudErr.PendingTaskID, "queued", "", targetDeviceID, err.Error(), "target request failed; will retry while caller is waiting", "", authHeader, false, false)
				_ = patchPendingCloudTaskDispatch(cloudErr.PendingTaskID, func(row *pendingCloudTaskDispatch) {
					row.Status = "queued"
					row.LastError = err.Error()
					row.BlockedAction = ""
				})
				emitProgress(cloudTaskHandoffProgress{
					Status:    "queued",
					Attempt:   attempt,
					LastError: err.Error(),
					Message:   "target request failed; retrying while caller waits",
				})
			} else if cloudAgain := decodeCloudWorkspaceRequiredError(status, raw); cloudAgain != nil {
				lastErr = cloudAgain
				_ = patchPendingCloudTaskDispatch(cloudErr.PendingTaskID, func(row *pendingCloudTaskDispatch) {
					row.Status = "queued"
					row.LastError = cloudAgain.Error()
					row.BlockedAction = ""
				})
				emitProgress(cloudTaskHandoffProgress{
					Status:    "queued",
					Attempt:   attempt,
					LastError: cloudAgain.Error(),
					Message:   "target is still activating; retrying",
				})
			} else if status >= 400 {
				msg := strings.TrimSpace(string(raw))
				if msg == "" {
					msg = http.StatusText(status)
				}
				return RemoteAgentCandidate{}, nil, fmt.Errorf("cloud task create failed: HTTP %d: %s", status, msg)
			} else {
				var out taskCreateHTTPResponse
				if err := json.Unmarshal(raw, &out); err != nil {
					return RemoteAgentCandidate{}, nil, err
				}
				if !out.OK {
					return RemoteAgentCandidate{}, nil, fmt.Errorf("cloud task create: %s", out.Error)
				}
				if cloudErr.Placement != nil {
					_ = rebindCloudTaskPlacement(ctx, cloudErr.Placement.PlacementID, out.TaskID, "running", authHeader)
				}
				_, _ = updateCloudTaskDispatchIntent(ctx, intent, cloudErr.PendingTaskID, "dispatched", out.TaskID, targetDeviceID, "", "workspace accepted task", "", authHeader, false, false)
				if err := deletePendingCloudTaskDispatch(cloudErr.PendingTaskID); err != nil {
					logPendingCloudDispatchStoreError("delete", cloudErr.PendingTaskID, err)
				}
				emitProgress(cloudTaskHandoffProgress{
					Status:      "dispatched",
					TargetLabel: firstNonEmpty(chosen.Label, chosen.DeviceID, targetDeviceID),
					Attempt:     attempt,
					Message:     "workspace accepted task",
				})
				return chosen, &out, nil
			}
		}
		if time.Now().After(deadline) {
			_, _ = updateCloudTaskDispatchIntent(ctx, intent, cloudErr.PendingTaskID, "blocked", "", targetDeviceID, fmt.Sprintf("%v", lastErr), "workspace was not reachable before the caller wait timeout", "", authHeader, false, false)
			_ = patchPendingCloudTaskDispatch(cloudErr.PendingTaskID, func(row *pendingCloudTaskDispatch) {
				row.Status = "blocked"
				if lastErr != nil {
					row.LastError = lastErr.Error()
				}
			})
			lastErrText := ""
			if lastErr != nil {
				lastErrText = lastErr.Error()
			}
			emitProgress(cloudTaskHandoffProgress{
				Status:    "blocked",
				Attempt:   attempt,
				LastError: lastErrText,
				Message:   "workspace was not reachable before the wait timeout",
			})
			return RemoteAgentCandidate{}, nil, fmt.Errorf("%w; cloud workspace target %s not reachable after %s (pendingTaskId=%s; last error: %v)", cloudErr, targetDeviceID, wait.Round(time.Second), cloudErr.PendingTaskID, lastErr)
		}
		select {
		case <-ctx.Done():
			return RemoteAgentCandidate{}, nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func retryPendingCloudTaskDispatches(ctx context.Context, authHeader string, wait time.Duration) []pendingCloudTaskDispatchRetryResult {
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		return nil
	}
	rows, err := store.load()
	if err != nil || len(rows) == 0 {
		return nil
	}
	results := make([]pendingCloudTaskDispatchRetryResult, 0, len(rows))
	for _, row := range rows {
		if pendingCloudTaskDispatchNeedsUserAction(row) {
			refreshed := refreshPendingCloudTaskDispatchFromBackend(ctx, row, authHeader)
			if refreshed.Status != row.Status ||
				refreshed.BlockedAction != row.BlockedAction ||
				refreshed.ClearedBlocker != row.ClearedBlocker ||
				refreshed.LastError != row.LastError ||
				!refreshed.ExpiresAt.Equal(row.ExpiresAt) {
				row = refreshed
				_ = patchPendingCloudTaskDispatch(row.LocalTaskID, func(stored *pendingCloudTaskDispatch) {
					stored.DispatchIntentID = row.DispatchIntentID
					stored.ExpiresAt = row.ExpiresAt
					stored.Status = row.Status
					stored.BlockedAction = row.BlockedAction
					stored.ClearedBlocker = row.ClearedBlocker
					stored.LastError = row.LastError
					if row.Placement != nil {
						stored.Placement = row.Placement
					}
				})
			}
		}
		status := strings.TrimSpace(row.Status)
		if pendingCloudTaskDispatchNeedsUserAction(row) {
			continue
		}
		if status == "dispatching" || status == "dispatched" {
			continue
		}
		if status == "expired" {
			_, _ = updateCloudTaskDispatchIntent(ctx, &taskDispatchIntent{ID: row.DispatchIntentID}, row.LocalTaskID, "expired", "", "", "", "local Cloud Workspace dispatch window expired before retry", "", authHeader, false, false)
			continue
		}
		localTaskID := strings.TrimSpace(row.LocalTaskID)
		if localTaskID == "" || row.Placement == nil || strings.TrimSpace(row.Placement.TargetDeviceID) == "" || len(row.BodyJSON) == 0 {
			continue
		}
		cloudErr := &CloudWorkspaceRequiredError{
			PendingTaskID:        localTaskID,
			Placement:            row.Placement,
			Reason:               "retrying locally persisted Cloud Workspace dispatch",
			ClearedBlockedAction: row.ClearedBlocker,
		}
		var progressLog []cloudTaskHandoffProgress
		chosen, task, err := createTaskOnCloudWorkspace(ctx, cloudErr, authHeader, row.BodyJSON, wait, func(p cloudTaskHandoffProgress) {
			progressLog = append(progressLog, p)
		})
		result := pendingCloudTaskDispatchRetryResult{LocalTaskID: localTaskID, Err: err}
		result.ProgressLog = progressLog
		if err == nil && task != nil {
			result.TaskID = task.TaskID
			result.TargetLabel = firstNonEmpty(chosen.Label, chosen.DeviceID, row.Placement.TargetDeviceID)
		}
		results = append(results, result)
	}
	return results
}

func logPendingCloudDispatchStoreError(action, localTaskID string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cloud] pending dispatch %s failed for %s: %v\n", action, firstNonEmpty(strings.TrimSpace(localTaskID), "unknown"), err)
	}
}

func taskIntentLabelsFromTaskBody(raw []byte) (sourceSurface, requestedRunner, projectSlug string) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", "", ""
	}
	if v, ok := body["source"].(string); ok {
		sourceSurface = strings.TrimSpace(v)
	}
	if v, ok := body["runner"].(string); ok {
		requestedRunner = strings.TrimSpace(v)
	}
	if v, ok := body["projectName"].(string); ok {
		projectSlug = basenameSlug(v)
	}
	return sourceSurface, requestedRunner, projectSlug
}

func taskBodyWithLocalFallback(raw []byte) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	body["allowLocalFallback"] = true
	return json.Marshal(body)
}

func createHTTPTaskWithCloudHandoff(ctx context.Context, client *http.Client, baseURL, authHeader string, bodyJSON []byte, wait time.Duration, progressFns ...cloudTaskHandoffProgressFunc) (*taskCreateHTTPResponse, *RemoteAgentCandidate, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, nil, fmt.Errorf("missing task API base URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/tasks", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("create task: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if err := decodeCloudWorkspaceRequiredError(resp.StatusCode, raw); err != nil {
		cloudErr, ok := err.(*CloudWorkspaceRequiredError)
		if !ok {
			return nil, nil, err
		}
		chosen, result, handoffErr := createTaskOnCloudWorkspace(ctx, cloudErr, authHeader, bodyJSON, wait, progressFns...)
		if handoffErr != nil {
			return nil, nil, handoffErr
		}
		return result, &chosen, nil
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, nil, fmt.Errorf("create task failed (status %d): %s", resp.StatusCode, msg)
	}
	var result taskCreateHTTPResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, nil, err
	}
	if !result.OK || strings.TrimSpace(result.TaskID) == "" {
		if strings.TrimSpace(result.Error) != "" {
			return nil, nil, fmt.Errorf("create task: %s", result.Error)
		}
		return nil, nil, fmt.Errorf("create task returned no task id")
	}
	return &result, nil, nil
}

func bearerTokenFromAuthHeader(authHeader string) string {
	authHeader = strings.TrimSpace(authHeader)
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[len("bearer "):])
	}
	return authHeader
}

func inferPlacementTaskKind(kindHint, title, description, customCommand, source string) string {
	if kind := normalizePlacementKind(kindHint); kind != "" {
		return kind
	}
	text := strings.ToLower(strings.Join([]string{title, description, customCommand, source}, " "))
	switch {
	case strings.Contains(text, "deploy") || strings.Contains(text, "ship") || strings.Contains(text, "release"):
		return "deploy"
	case strings.Contains(text, "build") || strings.Contains(text, "apk") || strings.Contains(text, "ipa") || strings.Contains(text, "xcodebuild") || strings.Contains(text, "gradle"):
		return "build"
	case strings.Contains(text, "test") || strings.Contains(text, "pytest") || strings.Contains(text, "go test") || strings.Contains(text, "npm test"):
		return "test"
	case strings.Contains(text, "autorun") || strings.Contains(text, "autoideas"):
		return "autorun"
	case strings.Contains(text, "source") || strings.Contains(text, "git"):
		return "source"
	case strings.Contains(text, "vibe") || strings.Contains(text, "mobile") || strings.Contains(text, "web"):
		return "vibe"
	default:
		return "unknown"
	}
}

func normalizePlacementKind(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "vibe", "build", "deploy", "test", "source", "autorun", "unknown":
		return strings.TrimSpace(strings.ToLower(kind))
	default:
		return ""
	}
}

func basenameSlug(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	leaf := filepath.Base(value)
	leaf = strings.TrimSpace(leaf)
	if leaf == "." || leaf == string(filepath.Separator) {
		return ""
	}
	if len(leaf) > 80 {
		leaf = leaf[:80]
	}
	return leaf
}

func taskPlacementStackLabel(project ProjectInfo, workDir string) string {
	parts := []string{project.Framework}
	if mr, err := DetectMonorepo(workDir, DetectOpts{MaxDepth: 4}); err == nil && mr != nil {
		parts = append(parts, mr.Frameworks...)
		if mr.IsMonorepo {
			parts = append(parts, "monorepo", "workspace")
		}
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func hasNativeMobileProjectSignal(workDir, stackLabel string) bool {
	stackLabel = strings.ToLower(stackLabel)
	if strings.Contains(stackLabel, "react-native") || strings.Contains(stackLabel, "expo") ||
		strings.Contains(stackLabel, "flutter") || strings.Contains(stackLabel, "iosnative") ||
		strings.Contains(stackLabel, "androidnative") {
		return true
	}
	for _, rel := range []string{"ios", "android", "pubspec.yaml"} {
		if fileExists(filepath.Join(workDir, rel)) {
			return true
		}
	}
	return false
}

func hasDockerProjectSignal(workDir, stackLabel string) bool {
	if strings.Contains(strings.ToLower(stackLabel), "docker") {
		return true
	}
	for _, rel := range []string{"Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if fileExists(filepath.Join(workDir, rel)) {
			return true
		}
	}
	return false
}

func boundedRepoMetrics(workDir string) (appCount, fileCount, repoSizeMb int) {
	mr, _ := DetectMonorepo(workDir, DetectOpts{MaxDepth: 4})
	if mr != nil {
		appCount = len(mr.Projects)
	}
	const maxFiles = 100000
	const maxBytes = int64(10 * 1024 * 1024 * 1024)
	var totalBytes int64
	_ = filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if monorepoSkipDirs[name] && path != workDir {
				return filepath.SkipDir
			}
			return nil
		}
		fileCount++
		if info, statErr := d.Info(); statErr == nil {
			totalBytes += info.Size()
		}
		if fileCount >= maxFiles || totalBytes >= maxBytes {
			return filepath.SkipAll
		}
		return nil
	})
	repoSizeMb = int(totalBytes / (1024 * 1024))
	return appCount, fileCount, repoSizeMb
}
