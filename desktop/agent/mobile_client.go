package main

// MobileClient mirrors the HTTP surface the Yaver mobile app's
// `QuicClient` (mobile/src/lib/quic.ts) uses when it talks to a live
// agent. Every method here corresponds 1:1 with a mobile-app method
// so the two can evolve in lockstep:
//
//   mobile QuicClient method       →  Go MobileClient method
//   ──────────────────────────────────────────────────────
//   getDevServerStatus             →  DevServerStatus
//   startDevServer                 →  StartDevServer
//   getDevServerTarget             →  DevServerTarget
//   subscribeDevEvents             →  SubscribeDevEvents
//   cloneRepo                      →  CloneRepo
//   pullRepo                       →  PullRepo
//   (direct fetch `/projects/mobile`)  →  ListMobileProjects
//
// The `yaver emu` CLI uses this to replay the mobile card's flow end
// to end from the terminal — so a developer can dogfood clone +
// start + log streaming + error handling without a phone in the
// loop. Keep these methods aligned with quic.ts: when a new HTTP
// call is added on the mobile side, add its twin here.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MobileClient is the shared HTTP client the mobile emulator uses.
// All methods are safe for concurrent use once the instance is
// created; each call opens its own HTTP request.
type MobileClient struct {
	BaseURL   string
	AuthToken string
	HTTP      *http.Client
}

// NewMobileClient returns a client configured for the given agent.
// Pass "" for the default http.Client with a 30s timeout.
func NewMobileClient(baseURL, authToken string, hc *http.Client) *MobileClient {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &MobileClient{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		AuthToken: authToken,
		HTTP:      hc,
	}
}

// doJSON is the shared helper: adds auth header, encodes body as
// JSON if non-nil, decodes response into out if non-nil. Returns an
// error for any non-2xx response.
func (c *MobileClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return err
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ── Health + status ─────────────────────────────────────────────

// Health hits /health — used to verify the agent is reachable and
// authenticated before running any other mobile-flow.
func (c *MobileClient) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, "GET", "/health", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Info returns the agent's /info payload (hostname, version, device
// id, capabilities…). Same endpoint the mobile app hits on connect.
func (c *MobileClient) Info(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, "GET", "/info", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── Mobile projects (the Hot Reload tab's list) ─────────────────

// MobileProjectSummary mirrors the fields the mobile Hot Reload tab reads.
type MobileProjectSummary struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Framework string   `json:"framework"`
	SDK       string   `json:"sdk,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// ListMobileProjectsResponse wraps the scanner payload.
type ListMobileProjectsResponse struct {
	OK        bool                   `json:"ok"`
	Projects  []MobileProjectSummary `json:"projects"`
	Scanning  bool                   `json:"scanning"`
	ScannedAt string                 `json:"scannedAt"`
}

// ListMobileProjects hits GET /projects/mobile — the same call the
// Hot Reload tab makes every 15s (or every 2.5s while scanning).
func (c *MobileClient) ListMobileProjects(ctx context.Context) (*ListMobileProjectsResponse, error) {
	var out ListMobileProjectsResponse
	if err := c.doJSON(ctx, "GET", "/projects/mobile", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── Repos (clone / pull) ────────────────────────────────────────

// CloneRepoResult mirrors the JSON response shape.
type CloneRepoResult struct {
	OK     bool   `json:"ok"`
	Path   string `json:"path"`
	Output string `json:"output"`
}

// SetGitCredential hits POST /repos/credentials — stores a PAT for
// a git host so subsequent clones of private repos on that host
// authenticate. Same endpoint the mobile "Git credentials" form uses.
func (c *MobileClient) SetGitCredential(ctx context.Context, host, username, token string) error {
	return c.doJSON(ctx, "POST", "/repos/credentials", map[string]string{
		"host":     host,
		"username": username,
		"token":    token,
	}, nil)
}

// CloneRepo hits POST /repos/clone — the same call the mobile
// "Clone" action triggers. On success the agent also invalidates
// project caches, so a subsequent ListMobileProjects call (within
// ~2.5s) should show the new project.
func (c *MobileClient) CloneRepo(ctx context.Context, url, dir, branch string) (*CloneRepoResult, error) {
	body := map[string]any{"url": url}
	if dir != "" {
		body["dir"] = dir
	}
	if branch != "" {
		body["branch"] = branch
	}
	var out CloneRepoResult
	if err := c.doJSON(ctx, "POST", "/repos/clone", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── Dev server (start / status / stop / events) ─────────────────

// DevStartRequest mirrors the mobile StartDevServerRequest shape.
type DevStartRequest struct {
	Framework         string `json:"framework,omitempty"`
	WorkDir           string `json:"workDir"`
	Port              int    `json:"port,omitempty"`
	Platform          string `json:"platform,omitempty"`
	TargetDeviceID    string `json:"targetDeviceId,omitempty"`
	TargetDeviceName  string `json:"targetDeviceName,omitempty"`
	TargetDeviceClass string `json:"targetDeviceClass,omitempty"`
}

// StartDevServer hits POST /dev/start.
func (c *MobileClient) StartDevServer(ctx context.Context, req DevStartRequest) error {
	return c.doJSON(ctx, "POST", "/dev/start", req, nil)
}

// StopDevServer hits POST /dev/stop.
func (c *MobileClient) StopDevServer(ctx context.Context) error {
	return c.doJSON(ctx, "POST", "/dev/stop", nil, nil)
}

// GetDevStatus hits GET /dev/status. Returns nil if no dev server
// is active (agent returns 404 or empty body — we treat both as nil).
func (c *MobileClient) GetDevStatus(ctx context.Context) (*DevServerStatus, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/dev/status", nil)
	if err != nil {
		return nil, err
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET /dev/status: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out DevServerStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Framework == "" && !out.Running {
		return nil, nil
	}
	return &out, nil
}

// BuildNativeBundleResult mirrors the JSON response of /dev/build-native.
type BuildNativeBundleResult struct {
	Status                        string                  `json:"status"`
	Code                          string                  `json:"code,omitempty"`
	Error                         string                  `json:"error,omitempty"`
	HelpHint                      string                  `json:"helpHint,omitempty"`
	BundleURL                     string                  `json:"bundleUrl"`
	AssetsURL                     string                  `json:"assetsUrl,omitempty"`
	Size                          int64                   `json:"size"`
	MD5                           string                  `json:"md5"`
	BCVersion                     int                     `json:"bcVersion"`
	Platform                      string                  `json:"platform"`
	ModuleName                    string                  `json:"moduleName"`
	HasAssets                     bool                    `json:"hasAssets"`
	HostSDKVersion                string                  `json:"hostSdkVersion,omitempty"`
	HostExpoVersion               string                  `json:"hostExpoVersion,omitempty"`
	HostReactNative               string                  `json:"hostReactNative,omitempty"`
	HostReactVersion              string                  `json:"hostReactVersion,omitempty"`
	SupportedRNRange              string                  `json:"supportedRNRange,omitempty"`
	GuestRuntime                  *RuntimeFingerprint     `json:"guestRuntime,omitempty"`
	RuntimeFamilySelection        *RuntimeFamilySelection `json:"runtimeFamilySelection,omitempty"`
	HostRuntimeFamilies           []RuntimeFamily         `json:"hostRuntimeFamilies,omitempty"`
	IncompatibleNativeModules     []string                `json:"incompatibleNativeModules,omitempty"`
	MatchedNativeModules          []string                `json:"matchedNativeModules,omitempty"`
	NativeModuleVersionMismatches []NativeModuleMismatch  `json:"nativeModuleVersionMismatches,omitempty"`
	ExpoVersionMismatch           *VersionMismatch        `json:"expoVersionMismatch,omitempty"`
	ReactNativeVersionMismatch    *VersionMismatch        `json:"reactNativeVersionMismatch,omitempty"`
	ReactVersionMismatch          *VersionMismatch        `json:"reactVersionMismatch,omitempty"`
}

type NativeBuildConsumerContract struct {
	ConsumerVersion              string          `json:"consumerVersion,omitempty"`
	ConsumerBuild                string          `json:"consumerBuild,omitempty"`
	ConsumerSDKVersion           string          `json:"consumerSdkVersion,omitempty"`
	ConsumerHermesBCVersion      int             `json:"consumerHermesBCVersion,omitempty"`
	ConsumerCurrentRuntimeFamily string          `json:"consumerCurrentRuntimeFamilyId,omitempty"`
	ConsumerDefaultRuntimeFamily string          `json:"consumerDefaultRuntimeFamilyId,omitempty"`
	ConsumerRuntimeFamilies      []RuntimeFamily `json:"consumerRuntimeFamilies,omitempty"`
}

type UnityRunResult struct {
	OK             bool     `json:"ok"`
	Status         string   `json:"status,omitempty"`
	Stage          string   `json:"stage,omitempty"`
	ProjectPath    string   `json:"projectPath,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	BuildTarget    string   `json:"buildTarget,omitempty"`
	ExecuteMethod  string   `json:"executeMethod,omitempty"`
	OutputPath     string   `json:"outputPath,omitempty"`
	ExecutablePath string   `json:"executablePath,omitempty"`
	LogPath        string   `json:"logPath,omitempty"`
	ResultsPath    string   `json:"resultsPath,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	Artifacts      []string `json:"artifacts,omitempty"`
	NextAction     string   `json:"nextAction,omitempty"`
	Command        []string `json:"command,omitempty"`
}

// BuildNativeBundle hits POST /dev/build-native — the same call the
// mobile Hot Reload "Open in Yaver" button makes. Agent runs
// metro+hermesc, validates the HBC header, writes the bundle, and
// returns metadata the phone uses to fetch + load it.
func (c *MobileClient) BuildNativeBundle(ctx context.Context, platform string) (*BuildNativeBundleResult, error) {
	return c.BuildNativeBundleWithContract(ctx, platform, nil)
}

func (c *MobileClient) BuildNativeBundleWithContract(ctx context.Context, platform string, contract *NativeBuildConsumerContract) (*BuildNativeBundleResult, error) {
	if platform == "" {
		platform = "ios"
	}
	body := map[string]any{"platform": platform}
	if contract != nil {
		if contract.ConsumerVersion != "" {
			body["consumerVersion"] = contract.ConsumerVersion
		}
		if contract.ConsumerBuild != "" {
			body["consumerBuild"] = contract.ConsumerBuild
		}
		if contract.ConsumerSDKVersion != "" {
			body["consumerSdkVersion"] = contract.ConsumerSDKVersion
		}
		if contract.ConsumerHermesBCVersion > 0 {
			body["consumerHermesBCVersion"] = contract.ConsumerHermesBCVersion
		}
		if contract.ConsumerCurrentRuntimeFamily != "" {
			body["consumerCurrentRuntimeFamilyId"] = contract.ConsumerCurrentRuntimeFamily
		}
		if contract.ConsumerDefaultRuntimeFamily != "" {
			body["consumerDefaultRuntimeFamilyId"] = contract.ConsumerDefaultRuntimeFamily
		}
		if len(contract.ConsumerRuntimeFamilies) > 0 {
			body["consumerRuntimeFamilies"] = contract.ConsumerRuntimeFamilies
		}
	}
	var out BuildNativeBundleResult
	if err := c.doJSON(ctx, "POST", "/dev/build-native", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *MobileClient) RunUnityTests(ctx context.Context, projectPath, testMode string) (*UnityRunResult, error) {
	body := map[string]string{
		"projectPath": projectPath,
		"testMode":    testMode,
	}
	var out UnityRunResult
	if err := c.doJSON(ctx, "POST", "/unity/test", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *MobileClient) BuildUnity(ctx context.Context, projectPath, buildTarget, executeMethod, outputPath string) (*UnityRunResult, error) {
	body := map[string]string{
		"projectPath":   projectPath,
		"buildTarget":   buildTarget,
		"executeMethod": executeMethod,
		"outputPath":    outputPath,
	}
	var out UnityRunResult
	if err := c.doJSON(ctx, "POST", "/unity/build", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *MobileClient) RelaunchUnity(ctx context.Context, projectPath, executablePath string) (*UnityRunResult, error) {
	body := map[string]string{
		"projectPath":    projectPath,
		"executablePath": executablePath,
	}
	var out UnityRunResult
	if err := c.doJSON(ctx, "POST", "/unity/relaunch", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *MobileClient) ListUnityRuns(ctx context.Context) ([]UnityRunResult, error) {
	var out []UnityRunResult
	if err := c.doJSON(ctx, "GET", "/unity/runs", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DownloadBundleAndValidate GETs the compiled HBC bundle from
// bundleURL and verifies the Hermes bytecode header. Returns the
// bundle bytes on success so a caller can pipe them further (e.g.
// push to a paired phone's on-device HTTP server, or just prove
// the full pipeline completes). Parses the HBC magic + BC version
// exactly as the mobile-side `ValidateHBC` does on the phone.
func (c *MobileClient) DownloadBundleAndValidate(ctx context.Context, bundleURL string, expectedBC int) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+bundleURL, nil)
	if err != nil {
		return nil, 0, err
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("GET %s: %d %s", bundleURL, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	// HBC header: magic 0x1F1903C1 starts at offset 4, BC version
	// at offset 8 (uint32 LE). Same checks the on-device validator
	// performs before loading into the Hermes runtime.
	if len(data) < 12 {
		return data, 0, fmt.Errorf("bundle too short: %d bytes (need >= 12)", len(data))
	}
	magic := uint32(data[4]) | uint32(data[5])<<8 | uint32(data[6])<<16 | uint32(data[7])<<24
	if magic != 0x1F1903C1 {
		return data, 0, fmt.Errorf("bundle HBC magic mismatch: got 0x%08X, want 0x1F1903C1", magic)
	}
	bc := int(uint32(data[8]) | uint32(data[9])<<8 | uint32(data[10])<<16 | uint32(data[11])<<24)
	if expectedBC != 0 && bc != expectedBC {
		return data, bc, fmt.Errorf("bundle BC version mismatch: got %d, want %d", bc, expectedBC)
	}
	return data, bc, nil
}

// SubscribeDevEvents opens an SSE stream on /dev/events and invokes
// onEvent for each frame until ctx is cancelled or the server
// disconnects. Shaped to match mobile's quicClient.subscribeDevEvents.
//
// Uses a dedicated http.Client with no timeout — c.HTTP is fine for
// normal JSON calls but its 30s deadline would kill a long-running
// SSE subscription as soon as the bundler is quiet for a bit. The
// caller controls the stream lifetime via ctx.
func (c *MobileClient) SubscribeDevEvents(ctx context.Context, onEvent func(DevServerEvent)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/dev/events", nil)
	if err != nil {
		return err
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	req.Header.Set("Accept", "text/event-stream")
	sseClient := &http.Client{Transport: http.DefaultTransport}
	resp, err := sseClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET /dev/events: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	reader := bufio.NewReader(resp.Body)
	var dataBuf strings.Builder
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if dataBuf.Len() > 0 {
				var ev DevServerEvent
				if jerr := json.Unmarshal([]byte(dataBuf.String()), &ev); jerr == nil {
					onEvent(ev)
				}
				dataBuf.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataBuf.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

// ─── Vibe Preview ────────────────────────────────────────────────────────────
// Same API the mobile app's quic.ts hits — exposed here so the ephemeral
// test box (and any headless harness) can drive the new pipeline.

// VibePreviewStartOptsHeadless mirrors the agent's VibePreviewStartOpts
// without depending on it (the agent + headless client may live in
// different binaries one day).
type VibePreviewStartOptsHeadless struct {
	Project   string `json:"project"`
	TargetURL string `json:"targetUrl"`
	Mode      string `json:"mode,omitempty"`
	Profile   string `json:"profile,omitempty"`
	NetMode   string `json:"netMode,omitempty"`
}

func (c *MobileClient) StartVibePreview(ctx context.Context, opts VibePreviewStartOptsHeadless) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, "POST", "/vibing/preview/start", opts, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *MobileClient) StopVibePreview(ctx context.Context, project string) error {
	return c.doJSON(ctx, "POST", "/vibing/preview/stop", map[string]string{"project": project}, nil)
}

func (c *MobileClient) VibePreviewStatus(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, "GET", "/vibing/preview/status", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *MobileClient) VibePreviewSnapshot(ctx context.Context, project string) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, "POST", "/vibing/preview/snapshot", map[string]string{"project": project}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *MobileClient) StartVibeClip(ctx context.Context, project, source string, durationSec int) (map[string]any, error) {
	body := map[string]any{"project": project, "source": source, "durationMaxSec": durationSec}
	var out map[string]any
	if err := c.doJSON(ctx, "POST", "/vibing/preview/clip/start", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *MobileClient) ListVibeClips(ctx context.Context, project string) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, "GET", "/vibing/preview/clips?project="+project, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FetchVibeFrame downloads a frame's binary bytes by hash. Returns the
// raw PNG so the headless smoke test can assert size > 0.
func (c *MobileClient) FetchVibeFrame(ctx context.Context, project, hash string) ([]byte, error) {
	url := fmt.Sprintf("%s/vibing/preview/frames/%s?project=%s", c.BaseURL, hash, project)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("frame %s: %d %s", hash, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return io.ReadAll(resp.Body)
}
