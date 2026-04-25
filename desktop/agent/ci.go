package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"strings"
	"time"
)

// CIProvider identifies a CI/CD platform.
type CIProvider string

const (
	CIGitHub CIProvider = "github"
	CIGitLab CIProvider = "gitlab"
)

// triggerGitHubWorkflow triggers a GitHub Actions workflow_dispatch event.
func triggerGitHubWorkflow(token, repo, workflow, branch string, inputs map[string]string) error {
	if token == "" {
		return fmt.Errorf("GitHub token required. Add it to vault: yaver vault add github-token --category git-credential --value <token>")
	}
	if repo == "" {
		return fmt.Errorf("repository required (format: owner/repo)")
	}
	if workflow == "" {
		return fmt.Errorf("workflow filename required (e.g., build.yml)")
	}
	if branch == "" {
		branch = "main"
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/actions/workflows/%s/dispatches", repo, workflow)

	body := map[string]interface{}{
		"ref": branch,
	}
	if len(inputs) > 0 {
		body["inputs"] = inputs
	}

	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 204 {
		return nil // success — no content
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(respBody))
}

// listGitHubWorkflowRuns lists recent workflow runs.
func listGitHubWorkflowRuns(token, repo string, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 5
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/actions/runs?per_page=%d", repo, limit)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		WorkflowRuns []map[string]interface{} `json:"workflow_runs"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.WorkflowRuns, nil
}

// triggerGitLabPipeline triggers a GitLab CI pipeline.
func triggerGitLabPipeline(token, projectID, branch string, variables map[string]string) error {
	if token == "" {
		return fmt.Errorf("GitLab token required. Add it to vault: yaver vault add gitlab-token --category git-credential --value <token>")
	}
	if projectID == "" {
		return fmt.Errorf("project ID required")
	}
	if branch == "" {
		branch = "main"
	}

	url := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/pipeline", projectID)

	body := map[string]interface{}{
		"ref": branch,
	}
	if len(variables) > 0 {
		vars := make([]map[string]string, 0, len(variables))
		for k, v := range variables {
			vars = append(vars, map[string]string{"key": k, "value": v})
		}
		body["variables"] = vars
	}

	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("GitLab API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 201 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		if webURL, ok := result["web_url"].(string); ok {
			fmt.Printf("Pipeline created: %s\n", webURL)
		}
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("GitLab API returned %d: %s", resp.StatusCode, string(respBody))
}

// uploadGitHubRelease uploads a file as a GitHub Release asset.
func uploadGitHubRelease(token, repo, tag, filePath string) error {
	if token == "" {
		return fmt.Errorf("GitHub token required")
	}

	fi, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("file not found: %w", err)
	}

	// Get or create release by tag
	releaseID, err := getOrCreateGitHubRelease(token, repo, tag)
	if err != nil {
		return err
	}

	// Upload asset
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	fileName := fi.Name()
	url := fmt.Sprintf("https://uploads.github.com/repos/%s/releases/%d/assets?name=%s", repo, releaseID, fileName)

	req, _ := http.NewRequest("POST", url, f)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fi.Size()

	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 201 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		if downloadURL, ok := result["browser_download_url"].(string); ok {
			fmt.Printf("Uploaded: %s\n", downloadURL)
		}
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("upload failed (%d): %s", resp.StatusCode, string(body))
}

func getOrCreateGitHubRelease(token, repo, tag string) (int, error) {
	// Try to get existing release
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, tag)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var release map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&release)
		if id, ok := release["id"].(float64); ok {
			return int(id), nil
		}
	}

	// Create release
	createURL := fmt.Sprintf("https://api.github.com/repos/%s/releases", repo)
	body, _ := json.Marshal(map[string]interface{}{
		"tag_name": tag,
		"name":     tag,
		"draft":    false,
	})
	req, _ = http.NewRequest("POST", createURL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp2, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp2.Body.Close()

	if resp2.StatusCode == 201 {
		var release map[string]interface{}
		json.NewDecoder(resp2.Body).Decode(&release)
		if id, ok := release["id"].(float64); ok {
			return int(id), nil
		}
	}

	respBody, _ := io.ReadAll(resp2.Body)
	return 0, fmt.Errorf("create release failed (%d): %s", resp2.StatusCode, string(respBody))
}

// getVaultToken retrieves a token from the vault by name.
func getVaultToken(name string) string {
	vs := openVault()
	entry, err := vs.Get("", name)
	if err != nil {
		return ""
	}
	return entry.Value
}

type detectedGitRepo struct {
	Provider CIProvider
	Host     string
	Repo     string
}

// detectRepoFromGit tries to detect GitHub/GitLab repo from git remotes.
func detectRepoFromGit(dir string) (provider CIProvider, repo string) {
	detected := detectRepoRemoteFromGit(dir)
	return detected.Provider, detected.Repo
}

func detectRepoRemoteFromGit(dir string) detectedGitRepo {
	out, err := runCmdDir(dir, "git", "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		return detectedGitRepo{}
	}

	var (
		best      detectedGitRepo
		bestScore int
	)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		rawURL := fields[1]
		remoteName := strings.TrimPrefix(strings.TrimSuffix(key, ".url"), "remote.")

		detected := detectRepoFromRemoteURL(rawURL)
		if detected.Provider == "" || strings.TrimSpace(detected.Repo) == "" {
			continue
		}
		score := gitRemotePriority(remoteName)
		if best.Provider == "" || score < bestScore {
			best = detected
			bestScore = score
		}
	}
	return best
}

func detectRepoFromRemoteURL(raw string) detectedGitRepo {
	host, repo := parseGitRemoteHostRepo(raw)
	if host == "" || repo == "" {
		return detectedGitRepo{}
	}

	switch {
	case strings.EqualFold(host, "github.com"):
		return detectedGitRepo{Provider: CIGitHub, Host: host, Repo: repo}
	case strings.EqualFold(host, "gitlab.com"):
		return detectedGitRepo{Provider: CIGitLab, Host: host, Repo: repo}
	case strings.Contains(strings.ToLower(host), "gitlab"):
		return detectedGitRepo{Provider: CIGitLab, Host: host, Repo: repo}
	}

	if provider := findProvider(host); provider != nil {
		switch strings.ToLower(strings.TrimSpace(provider.Provider)) {
		case string(CIGitHub):
			return detectedGitRepo{Provider: CIGitHub, Host: host, Repo: repo}
		case string(CIGitLab):
			return detectedGitRepo{Provider: CIGitLab, Host: host, Repo: repo}
		}
	}

	return detectedGitRepo{}
}

func parseGitRemoteHostRepo(raw string) (host, repo string) {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, ".git"))
	if raw == "" {
		return "", ""
	}

	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err == nil {
			return strings.ToLower(u.Hostname()), strings.TrimPrefix(u.Path, "/")
		}
	}

	if at := strings.Index(raw, "@"); at >= 0 {
		raw = raw[at+1:]
	}
	if parts := strings.SplitN(raw, ":", 2); len(parts) == 2 && !strings.Contains(parts[0], "/") {
		return strings.ToLower(parts[0]), strings.TrimPrefix(parts[1], "/")
	}

	return "", ""
}

func gitRemotePriority(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "origin":
		return 0
	case "github", "gitlab":
		return 1
	case "upstream":
		return 2
	default:
		return 10
	}
}

// runCmdDir runs a command in a specific directory and returns stdout.
func runCmdDir(dir string, name string, args ...string) (string, error) {
	cmd := osexec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
