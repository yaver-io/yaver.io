package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type RelaySourceWorkerConfig struct {
	Enabled         bool   `json:"enabled,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	RelayID         string `json:"relay_id,omitempty"`
	ProjectSlug     string `json:"project_slug,omitempty"`
}

const (
	defaultRelaySourceWorkerInterval = 15 * time.Second
	minRelaySourceWorkerInterval     = 5 * time.Second
	maxRelaySourceWorkerInterval     = 5 * time.Minute
)

func relaySourceWorkerEnabled(cfg *Config) bool {
	if env := strings.TrimSpace(os.Getenv("YAVER_RELAY_SOURCE_WORKER")); env != "" {
		return envTruthy(env)
	}
	return cfg != nil && cfg.RelaySourceWorker != nil && cfg.RelaySourceWorker.Enabled
}

func relaySourceWorkerInterval(cfg *Config) time.Duration {
	seconds := 0
	if cfg != nil && cfg.RelaySourceWorker != nil {
		seconds = cfg.RelaySourceWorker.IntervalSeconds
	}
	if env := strings.TrimSpace(os.Getenv("YAVER_RELAY_SOURCE_WORKER_INTERVAL")); env != "" {
		if parsed, err := time.ParseDuration(env); err == nil {
			return clampRelaySourceWorkerInterval(parsed)
		}
		if parsedSeconds, err := parsePositiveInt(env); err == nil {
			seconds = parsedSeconds
		}
	}
	if seconds <= 0 {
		return defaultRelaySourceWorkerInterval
	}
	return clampRelaySourceWorkerInterval(time.Duration(seconds) * time.Second)
}

func relaySourceWorkerRelayID(cfg *Config, deviceID string) string {
	if cfg != nil && cfg.RelaySourceWorker != nil {
		if id := strings.TrimSpace(cfg.RelaySourceWorker.RelayID); id != "" {
			return id
		}
	}
	if id := strings.TrimSpace(deviceID); id != "" {
		return "relay-source-worker:" + id
	}
	return "relay-source-worker"
}

func relaySourceWorkerProjectSlug(cfg *Config) string {
	if cfg != nil && cfg.RelaySourceWorker != nil {
		return strings.TrimSpace(cfg.RelaySourceWorker.ProjectSlug)
	}
	return ""
}

func clampRelaySourceWorkerInterval(d time.Duration) time.Duration {
	if d < minRelaySourceWorkerInterval {
		return minRelaySourceWorkerInterval
	}
	if d > maxRelaySourceWorkerInterval {
		return maxRelaySourceWorkerInterval
	}
	return d
}

func parsePositiveInt(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("not positive")
	}
	return n, nil
}

func StartRelaySourceWorker(ctx context.Context, s *HTTPServer, cfg *Config) {
	if !relaySourceWorkerEnabled(cfg) {
		log.Printf("[relay-source-worker] disabled")
		return
	}
	if s == nil {
		log.Printf("[relay-source-worker] disabled: HTTP server unavailable")
		return
	}
	if strings.TrimSpace(s.token) == "" || strings.TrimSpace(s.convexURL) == "" {
		log.Printf("[relay-source-worker] disabled: backend auth unavailable")
		return
	}
	interval := relaySourceWorkerInterval(cfg)
	projectSlug := relaySourceWorkerProjectSlug(cfg)
	relayID := relaySourceWorkerRelayID(cfg, s.deviceID)
	log.Printf("[relay-source-worker] enabled interval=%s project=%s", interval, firstNonEmpty(projectSlug, "all-owned"))
	go relaySourceWorkerLoop(ctx, s, interval, projectSlug, relayID)
}

func relaySourceWorkerLoop(ctx context.Context, s *HTTPServer, interval time.Duration, projectSlug, relayID string) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if _, err := relaySourceWorkerTick(ctx, s, projectSlug, relayID); err != nil && ctx.Err() == nil {
				log.Printf("[relay-source-worker] tick failed: %v", err)
			}
			timer.Reset(interval)
		}
	}
}

func relaySourceWorkerTick(ctx context.Context, s *HTTPServer, projectSlug, relayID string) (*ManagedGitRelaySourceWorkResult, error) {
	if s == nil {
		return nil, fmt.Errorf("server unavailable")
	}
	authHeader := "Bearer " + strings.TrimSpace(s.token)
	claimed, err := claimRelaySourceIntent(ctx, authHeader, projectSlug, relayID)
	if err != nil {
		return nil, err
	}
	if claimed == nil {
		return &ManagedGitRelaySourceWorkResult{OK: true}, nil
	}
	slug := strings.TrimSpace(claimed.ProjectSlug)
	workDir := ""
	if slug == "" && s.taskMgr != nil {
		workDir = s.taskMgr.workDir
	}
	workDir, err = managedGitWorkDir(slug, workDir)
	if err != nil {
		_, _ = updateRelaySourceIntentStatus(
			ctx,
			authHeader,
			claimed.ID,
			claimed.LocalTaskID,
			"failed",
			"",
			relayID,
			"relay source worker could not resolve the project workspace",
			err.Error(),
			false,
		)
		return nil, err
	}
	result, err := ManagedGitPrepareRelaySourceBranch(workDir, claimed.Branch, claimed.BaseBranch)
	if err != nil {
		_, _ = updateRelaySourceIntentStatus(
			ctx,
			authHeader,
			claimed.ID,
			claimed.LocalTaskID,
			"failed",
			"",
			relayID,
			"relay source worker branch preparation failed",
			err.Error(),
			false,
		)
		return nil, err
	}
	_, _ = updateRelaySourceIntentStatus(
		ctx,
		authHeader,
		claimed.ID,
		claimed.LocalTaskID,
		"handoff_ready",
		"",
		relayID,
		"relay source worker prepared a scoped branch from prompt-free metadata; compute still owns execution",
		"",
		false,
	)
	_ = markFeedbackWorkItemBranchCreated(ctx, authHeader, claimed.LocalTaskID, result.Branch, relayID)
	return &ManagedGitRelaySourceWorkResult{
		OK:      true,
		Intent:  claimed,
		Prepare: result,
		Plan: &ManagedGitRelaySourcePlanResult{
			OK:            true,
			RepoID:        result.RepoID,
			Branch:        result.Branch,
			BaseBranch:    result.BaseBranch,
			Mode:          "prepare_only",
			RelayEligible: true,
			CanApply:      false,
			Reasons:       []string{"background worker only has prompt-free intent metadata, so it prepared a scoped branch for compute handoff"},
		},
	}, nil
}

func feedbackWorkItemIDFromRelayLocalTaskID(localTaskID string) string {
	localTaskID = strings.TrimSpace(localTaskID)
	if !strings.HasPrefix(localTaskID, "feedback:") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(localTaskID, "feedback:"))
}

func markFeedbackWorkItemBranchCreated(ctx context.Context, authHeader, localTaskID, branch, workerID string) error {
	itemID := feedbackWorkItemIDFromRelayLocalTaskID(localTaskID)
	if itemID == "" {
		return nil
	}
	_, err := updateFeedbackWorkItemStatus(
		ctx,
		authHeader,
		itemID,
		"branch_created",
		"",
		"",
		branch,
		"relay source worker prepared a scoped branch for owner review",
		"",
		workerID,
	)
	return err
}
