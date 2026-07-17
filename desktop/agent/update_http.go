package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

type agentUpdateStatus struct {
	CurrentVersion    string `json:"currentVersion"`
	LatestVersion     string `json:"latestVersion,omitempty"`
	UpdateAvailable   bool   `json:"updateAvailable"`
	AutoUpdateEnabled bool   `json:"autoUpdateEnabled"`
	Repo              string `json:"repo"`
	Updating          bool   `json:"updating"`
}

var runForcedAgentUpdate = func() {
	cfg, _ := LoadConfig()
	checkAutoUpdate(forcedAutoUpdateConfig(cfg))
}

var latestAgentReleaseVersionFunc = func() (string, error) {
	type ghRelease struct {
		TagName string `json:"tag_name"`
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", updateRepo()))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(release.TagName), "v"), nil
}

func buildAgentUpdateStatus(cfg *Config, updating bool) (*agentUpdateStatus, error) {
	latest, err := latestAgentReleaseVersionFunc()
	if err != nil {
		return nil, err
	}
	current := strings.TrimPrefix(version, "v")
	status := &agentUpdateStatus{
		CurrentVersion:    current,
		LatestVersion:     latest,
		AutoUpdateEnabled: shouldAutoUpdate(cfg),
		Repo:              updateRepo(),
		Updating:          updating,
	}
	currentSv := "v" + current
	latestSv := "v" + latest
	if semver.IsValid(currentSv) && semver.IsValid(latestSv) {
		status.UpdateAvailable = semver.Compare(latestSv, currentSv) > 0
	}
	return status, nil
}

func (s *HTTPServer) handleAgentUpdate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := LoadConfig()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		status, err := buildAgentUpdateStatus(cfg, s.agentUpdateRunning.Load())
		if err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, status)
	case http.MethodPost:
		if s.agentUpdateRunning.Load() {
			jsonError(w, http.StatusConflict, "update already in progress")
			return
		}
		cfg, err := LoadConfig()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		status, err := buildAgentUpdateStatus(cfg, false)
		if err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		if !status.UpdateAvailable {
			jsonReply(w, http.StatusOK, map[string]interface{}{
				"ok":              true,
				"started":         false,
				"message":         "already up to date",
				"currentVersion":  status.CurrentVersion,
				"latestVersion":   status.LatestVersion,
				"updateAvailable": false,
			})
			return
		}
		if !s.agentUpdateRunning.CompareAndSwap(false, true) {
			jsonError(w, http.StatusConflict, "update already in progress")
			return
		}
		go func() {
			defer s.agentUpdateRunning.Store(false)
			runForcedAgentUpdate()
		}()
		jsonReply(w, http.StatusAccepted, map[string]interface{}{
			"ok":              true,
			"started":         true,
			"message":         "update check started; the agent may disconnect briefly if it replaces itself and restarts",
			"currentVersion":  status.CurrentVersion,
			"latestVersion":   status.LatestVersion,
			"updateAvailable": true,
		})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}
