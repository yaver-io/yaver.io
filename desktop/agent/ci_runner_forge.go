package main

// ci_runner_forge.go owns the forge-facing safety boundary for Model 1 CI.
// A host runner executes repository-controlled code with the runner user's
// permissions, so a registration is accepted only after the real forge says
// the target is private. GitLab runners are also created locked, tagged,
// unable to run untagged jobs, and restricted to protected refs.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type ciForgeProject struct {
	ID         string
	FullName   string
	Visibility string
	WebURL     string
}

type ciRunnerLease struct {
	Token      string
	RunnerName string
	cleanup    func(context.Context) error
}

// ciRunnerMinFreeBytes is the floor that must exist on the volume that owns
// ~/.yaver/runner before we create an upstream runner record. The checkout,
// runner binary and job scratch space all land there. This is intentionally the
// same 2 GiB floor used by deployPreflight: below it, even compiling the agent
// itself has failed with ENOSPC before a useful diagnostic could be written.
const ciRunnerMinFreeBytes = int64(2) << 30

type ciPreflightFailure struct {
	Code    string
	Message string
	Initial map[string]interface{}
}

func (e *ciPreflightFailure) Error() string { return e.Message }

var ciRunnerDiskStat = diskGuardStat

func (l ciRunnerLease) Cleanup(ctx context.Context) error {
	if l.cleanup == nil {
		return nil
	}
	return l.cleanup(ctx)
}

// prepareCIRegistration performs non-mutating forge and local capability
// probes before the registration is persisted. This prevents the old false
// green where ci_runner_register returned success and the background
// supervisor immediately entered an invisible retry loop.
func prepareCIRegistration(ctx context.Context, r CIRunnerRegistration) (CIRunnerRegistration, error) {
	var err error
	r, err = normalizeCIRegistration(r)
	if err != nil {
		return CIRunnerRegistration{}, err
	}
	configDir, err := ConfigDir()
	if err != nil {
		return CIRunnerRegistration{}, &ciPreflightFailure{
			Code:    "ci_runner_storage_unavailable",
			Message: "CI runner storage is unavailable: " + err.Error(),
		}
	}
	fs, statErr := ciRunnerDiskStat(configDir)
	if statErr != nil {
		return CIRunnerRegistration{}, &ciPreflightFailure{
			Code:    "ci_runner_storage_unmeasured",
			Message: fmt.Sprintf("cannot measure free space on the CI runner volume at %s: %v — Yaver will not create a runner whose checkout volume has unknown capacity", configDir, statErr),
		}
	}
	if err := validateCIRunnerDisk(fs); err != nil {
		return CIRunnerRegistration{}, err
	}
	if r.Isolation == CIIsolationContainer {
		dockerPath, lookErr := exec.LookPath("docker")
		if lookErr != nil {
			return CIRunnerRegistration{}, fmt.Errorf("container isolation requires Docker; install/start Docker or choose isolation=host for a trusted private project on a dedicated box")
		}
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if out, probeErr := exec.CommandContext(probeCtx, dockerPath, "info", "--format", "{{.ServerVersion}}").CombinedOutput(); probeErr != nil {
			return CIRunnerRegistration{}, fmt.Errorf("Docker is installed but the daemon is not usable: %s", compactCIError(out, probeErr))
		}
	}

	switch r.Provider {
	case CIGitLab:
		token := detectGitLabToken(gitLabHost(r.Host))
		if token == "" {
			return CIRunnerRegistration{}, fmt.Errorf("no GitLab token; connect GitLab with a token that has create_runner access")
		}
		project, fetchErr := fetchGitLabProject(ctx, r.Host, token, firstNonEmpty(r.ProjectID, r.Target))
		if fetchErr != nil {
			return CIRunnerRegistration{}, fetchErr
		}
		if privateErr := requirePrivateCIProject(CIGitLab, project); privateErr != nil {
			return CIRunnerRegistration{}, privateErr
		}
		r.ProjectID = project.ID
		if project.FullName != "" {
			r.Target = project.FullName
		}
	case CIGitHub:
		if r.Scope != "repo" {
			return CIRunnerRegistration{}, fmt.Errorf("private-only enforcement cannot prove every repository behind an organization runner; register each private repository separately")
		}
		token := detectGitHubToken()
		if token == "" {
			return CIRunnerRegistration{}, fmt.Errorf("no GitHub token; connect GitHub before registering this runner")
		}
		project, fetchErr := fetchGitHubProject(ctx, r.Host, token, r.Target)
		if fetchErr != nil {
			return CIRunnerRegistration{}, fetchErr
		}
		if privateErr := requirePrivateCIProject(CIGitHub, project); privateErr != nil {
			return CIRunnerRegistration{}, privateErr
		}
	}
	return r, nil
}

func validateCIRunnerDisk(fs diskGuardFS) error {
	if fs.FreeBytes >= ciRunnerMinFreeBytes {
		return nil
	}
	return &ciPreflightFailure{
		Code: "ci_runner_insufficient_disk",
		Message: fmt.Sprintf(
			"CI runner preflight found only %s free on %s (need at least %s). The runner checkout, toolchain and build scratch use this volume; creating the forge runner now would queue a job that dies with 'no space left on device'. Scan reclaimable build caches, or move YAVER_CONFIG_DIR to a volume with enough space before retrying.",
			humanBytesDG(fs.FreeBytes), fs.Path, humanBytesDG(ciRunnerMinFreeBytes)),
		Initial: map[string]interface{}{
			"filesystem":        fs,
			"requiredFreeBytes": ciRunnerMinFreeBytes,
			"fix": map[string]interface{}{
				"label":   "Scan reclaimable storage",
				"opsVerb": "storage_scan",
				"payload": map[string]interface{}{"refresh": true},
			},
		},
	}
}

func mintCIRunnerLease(ctx context.Context, reg CIRunnerRegistration) (ciRunnerLease, error) {
	switch reg.Provider {
	case CIGitLab:
		accessToken := detectGitLabToken(gitLabHost(reg.Host))
		if accessToken == "" {
			return ciRunnerLease{}, fmt.Errorf("no GitLab token; connect GitLab with a token that has create_runner access")
		}
		project, err := fetchGitLabProject(ctx, reg.Host, accessToken, firstNonEmpty(reg.ProjectID, reg.Target))
		if err != nil {
			return ciRunnerLease{}, err
		}
		if privateErr := requirePrivateCIProject(CIGitLab, project); privateErr != nil {
			return ciRunnerLease{}, privateErr
		}
		return createGitLabRunnerLease(ctx, reg, project, accessToken)
	default:
		if reg.Scope != "repo" {
			return ciRunnerLease{}, fmt.Errorf("organization runners are disabled while private-only enforcement is active")
		}
		accessToken := detectGitHubToken()
		if accessToken == "" {
			return ciRunnerLease{}, fmt.Errorf("no GitHub token; connect GitHub before registering this runner")
		}
		project, err := fetchGitHubProject(ctx, reg.Host, accessToken, reg.Target)
		if err != nil {
			return ciRunnerLease{}, err
		}
		if privateErr := requirePrivateCIProject(CIGitHub, project); privateErr != nil {
			return ciRunnerLease{}, privateErr
		}
		token, err := fetchRegistrationToken(ctx, githubRegistrationTokenURL(reg.Host, reg.Scope, reg.Target), "Authorization", "Bearer "+accessToken)
		if err != nil {
			return ciRunnerLease{}, err
		}
		runnerName := newCIRunnerName()
		return ciRunnerLease{
			Token:      token,
			RunnerName: runnerName,
			cleanup: func(cleanupCtx context.Context) error {
				return deleteGitHubRunnerByName(cleanupCtx, reg.Host, reg.Target, accessToken, runnerName)
			},
		}, nil
	}
}

func newCIRunnerName() string {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err == nil {
		return fmt.Sprintf("yaver-%x", suffix[:])
	}
	return fmt.Sprintf("yaver-%x", time.Now().UnixNano())
}

func requirePrivateCIProject(provider CIProvider, project ciForgeProject) error {
	if strings.EqualFold(strings.TrimSpace(project.Visibility), "private") {
		return nil
	}
	return fmt.Errorf("refusing self-hosted %s runner for %q: visibility is %q, expected private", provider, project.FullName, project.Visibility)
}

func fetchGitHubProject(ctx context.Context, host, token, target string) (ciForgeProject, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubRepoAPIBase(host, target), nil)
	if err != nil {
		return ciForgeProject{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := ciForgeHTTPClient().Do(req)
	if err != nil {
		return ciForgeProject{}, fmt.Errorf("GitHub repository probe failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return ciForgeProject{}, fmt.Errorf("GitHub repository probe returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
		HTMLURL  string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ciForgeProject{}, fmt.Errorf("parse GitHub repository probe: %w", err)
	}
	visibility := "public"
	if out.Private {
		visibility = "private"
	}
	return ciForgeProject{ID: strconv.FormatInt(out.ID, 10), FullName: out.FullName, Visibility: visibility, WebURL: out.HTMLURL}, nil
}

func githubRepoAPIBase(host, target string) string {
	base := "https://api.github.com"
	if h := strings.TrimSpace(host); h != "" && !strings.EqualFold(h, "github.com") {
		base = "https://" + strings.TrimSuffix(h, "/") + "/api/v3"
	}
	return base + "/repos/" + strings.Trim(target, "/")
}

func deleteGitHubRunnerByName(ctx context.Context, host, target, accessToken, runnerName string) error {
	return deleteGitHubRunnerByNameAt(ctx, githubRepoAPIBase(host, target), accessToken, runnerName)
}

// deleteGitHubRunnerByNameAt cleans the offline record left when an ephemeral
// runner is cancelled before it claims a job. A normally completed ephemeral
// job has already removed itself, so "not found" is success.
func deleteGitHubRunnerByNameAt(ctx context.Context, repoAPIBase, accessToken, runnerName string) error {
	repoAPIBase = strings.TrimSuffix(repoAPIBase, "/")
	for page := 1; page <= 10; page++ {
		listURL := fmt.Sprintf("%s/actions/runners?per_page=100&page=%d", repoAPIBase, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := ciForgeHTTPClient().Do(req)
		if err != nil {
			return fmt.Errorf("list GitHub runners for cleanup: %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("list GitHub runners for cleanup returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var out struct {
			Runners []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"runners"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return fmt.Errorf("parse GitHub runner cleanup list: %w", err)
		}
		for _, runner := range out.Runners {
			if runner.Name != runnerName {
				continue
			}
			deleteReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/actions/runners/%d", repoAPIBase, runner.ID), nil)
			if err != nil {
				return err
			}
			deleteReq.Header.Set("Authorization", "Bearer "+accessToken)
			deleteReq.Header.Set("Accept", "application/vnd.github+json")
			deleteResp, err := ciForgeHTTPClient().Do(deleteReq)
			if err != nil {
				return fmt.Errorf("delete GitHub runner: %w", err)
			}
			deleteBody, _ := io.ReadAll(io.LimitReader(deleteResp.Body, 64*1024))
			deleteResp.Body.Close()
			if deleteResp.StatusCode != http.StatusNoContent && deleteResp.StatusCode != http.StatusNotFound {
				return fmt.Errorf("delete GitHub runner returned %d: %s", deleteResp.StatusCode, strings.TrimSpace(string(deleteBody)))
			}
			return nil
		}
		if len(out.Runners) < 100 {
			return nil
		}
	}
	return fmt.Errorf("could not prove GitHub runner %q absent after scanning 1000 repository runners", runnerName)
}

func fetchGitLabProject(ctx context.Context, host, token, target string) (ciForgeProject, error) {
	return fetchGitLabProjectAt(ctx, gitLabAPIBase(host), token, target)
}

func fetchGitLabProjectAt(ctx context.Context, apiBase, token, target string) (ciForgeProject, error) {
	projectRef := url.PathEscape(strings.TrimSpace(target))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(apiBase, "/")+"/projects/"+projectRef, nil)
	if err != nil {
		return ciForgeProject{}, err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	resp, err := ciForgeHTTPClient().Do(req)
	if err != nil {
		return ciForgeProject{}, fmt.Errorf("GitLab project probe failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return ciForgeProject{}, fmt.Errorf("GitLab project probe returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID                int64  `json:"id"`
		PathWithNamespace string `json:"path_with_namespace"`
		Visibility        string `json:"visibility"`
		WebURL            string `json:"web_url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ciForgeProject{}, fmt.Errorf("parse GitLab project probe: %w", err)
	}
	if out.ID <= 0 {
		return ciForgeProject{}, fmt.Errorf("GitLab project probe returned no numeric id")
	}
	return ciForgeProject{ID: strconv.FormatInt(out.ID, 10), FullName: out.PathWithNamespace, Visibility: out.Visibility, WebURL: out.WebURL}, nil
}

func createGitLabRunnerLease(ctx context.Context, reg CIRunnerRegistration, project ciForgeProject, accessToken string) (ciRunnerLease, error) {
	return createGitLabRunnerLeaseAt(ctx, gitLabAPIBase(reg.Host), reg, project, accessToken)
}

func createGitLabRunnerLeaseAt(ctx context.Context, apiBase string, reg CIRunnerRegistration, project ciForgeProject, accessToken string) (ciRunnerLease, error) {
	form := url.Values{}
	form.Set("runner_type", "project_type")
	form.Set("project_id", project.ID)
	form.Set("description", "Yaver ephemeral runner")
	form.Set("paused", "false")
	form.Set("locked", "true")
	form.Set("run_untagged", "false")
	form.Set("tag_list", strings.Join(reg.runnerLabels(), ","))
	form.Set("access_level", "ref_protected")

	apiBase = strings.TrimSuffix(apiBase, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/user/runners", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return ciRunnerLease{}, err
	}
	req.Header.Set("PRIVATE-TOKEN", accessToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := ciForgeHTTPClient().Do(req)
	if err != nil {
		return ciRunnerLease{}, fmt.Errorf("create protected GitLab runner: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusCreated {
		return ciRunnerLease{}, fmt.Errorf("create protected GitLab runner returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ciRunnerLease{}, fmt.Errorf("parse GitLab runner response: %w", err)
	}
	if out.ID <= 0 || out.Token == "" {
		return ciRunnerLease{}, fmt.Errorf("GitLab runner response omitted id or authentication token")
	}
	leaseToken := out.Token
	runnerID := out.ID
	return ciRunnerLease{
		Token: leaseToken,
		cleanup: func(cleanupCtx context.Context) error {
			if err := deleteGitLabRunnerByToken(cleanupCtx, apiBase, leaseToken); err == nil {
				return nil
			}
			return deleteGitLabRunnerByID(cleanupCtx, apiBase, accessToken, runnerID)
		},
	}, nil
}

func deleteGitLabRunnerByToken(ctx context.Context, apiBase, token string) error {
	form := url.Values{"token": []string{token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimSuffix(apiBase, "/")+"/runners", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := ciForgeHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("delete GitLab runner by token returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func deleteGitLabRunnerByID(ctx context.Context, apiBase, accessToken string, runnerID int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/runners/%d", strings.TrimSuffix(apiBase, "/"), runnerID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", accessToken)
	resp, err := ciForgeHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("delete GitLab runner by id returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func gitLabHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "gitlab.com"
	}
	return strings.TrimSuffix(host, "/")
}

func gitLabAPIBase(host string) string {
	return "https://" + gitLabHost(host) + "/api/v4"
}

func ciForgeHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

func compactCIError(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err.Error()
	}
	if len(message) > 512 {
		message = message[:512] + "…"
	}
	return message
}
