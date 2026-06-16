package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yaver-io/agent/studio"
)

var androidPackageRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)

type androidAppRequest struct {
	DeviceID    string   `json:"device_id,omitempty"`
	PackageName string   `json:"package_name,omitempty"`
	Label       string   `json:"label,omitempty"`
	APKPath     string   `json:"apk_path,omitempty"`
	Source      string   `json:"source,omitempty"` // apk | play | manual | yaver-build
	Query       string   `json:"query,omitempty"`
	WaitText    string   `json:"wait_text,omitempty"`
	Text        string   `json:"text,omitempty"`
	Key         string   `json:"key,omitempty"`
	HostWorkDir string   `json:"host_work_dir,omitempty"`
	Image       string   `json:"image,omitempty"`
	Container   string   `json:"container,omitempty"`
	Packages    []string `json:"packages,omitempty"`
}

func validateAndroidPackage(pkg string) error {
	if strings.TrimSpace(pkg) == "" {
		return fmt.Errorf("package_name is required")
	}
	if !androidPackageRe.MatchString(pkg) {
		return fmt.Errorf("invalid Android package name %q", pkg)
	}
	return nil
}

func defaultRedroidWorkDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return filepath.Join(os.TempDir(), "yaver-redroid-app-sync")
	}
	return filepath.Join(home, ".yaver", "redroid-app-sync")
}

func redroidForAppSync(req androidAppRequest) *studio.RedroidSurface {
	name := strings.TrimSpace(req.Container)
	if name == "" {
		name = "yaver-app-sync-redroid"
	}
	work := strings.TrimSpace(req.HostWorkDir)
	if work == "" {
		work = defaultRedroidWorkDir()
	}
	return &studio.RedroidSurface{
		R:           studio.LocalRunner{},
		Name:        name,
		Image:       req.Image,
		HostWorkDir: work,
	}
}

func androidAppStatus(ctx context.Context, req androidAppRequest) (map[string]any, error) {
	if err := validateAndroidPackage(req.PackageName); err != nil {
		return nil, err
	}
	surf := redroidForAppSync(req)
	if err := surf.EnsureReady(ctx); err != nil {
		return nil, err
	}
	installed, err := surf.IsPackageInstalled(ctx, req.PackageName)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":          true,
		"packageName": req.PackageName,
		"installed":   installed,
		"state":       map[bool]string{true: "installed", false: "missing"}[installed],
	}, nil
}

func androidAppInstall(ctx context.Context, req androidAppRequest) (map[string]any, error) {
	if err := validateAndroidPackage(req.PackageName); err != nil {
		return nil, err
	}
	source := strings.TrimSpace(req.Source)
	if source == "" && strings.TrimSpace(req.APKPath) != "" {
		source = "apk"
	}
	if source == "" {
		source = "play"
	}
	if source != "apk" && source != "yaver-build" && source != "manual" {
		return map[string]any{
			"ok":          false,
			"packageName": req.PackageName,
			"installed":   false,
			"state":       "unsupported_source",
			"error":       "Play/package-name install is not implemented; provide apk_path or install the app manually inside Redroid",
		}, nil
	}
	if strings.TrimSpace(req.APKPath) == "" {
		return nil, fmt.Errorf("apk_path is required for source=%s", source)
	}
	surf := redroidForAppSync(req)
	if err := surf.EnsureReady(ctx); err != nil {
		return nil, err
	}
	if err := surf.Install(ctx, req.APKPath); err != nil {
		return nil, err
	}
	installed, _ := surf.IsPackageInstalled(ctx, req.PackageName)
	return map[string]any{
		"ok":          installed,
		"packageName": req.PackageName,
		"installed":   installed,
		"state":       "installed",
	}, nil
}

func androidAppLaunch(ctx context.Context, req androidAppRequest) (map[string]any, error) {
	if err := validateAndroidPackage(req.PackageName); err != nil {
		return nil, err
	}
	surf := redroidForAppSync(req)
	if err := surf.EnsureReady(ctx); err != nil {
		return nil, err
	}
	installed, err := surf.IsPackageInstalled(ctx, req.PackageName)
	if err != nil {
		return nil, err
	}
	if !installed {
		return map[string]any{"ok": false, "packageName": req.PackageName, "installed": false, "state": "missing"}, nil
	}
	if err := surf.Driver().Launch(ctx, studio.App{Package: req.PackageName}); err != nil {
		return nil, err
	}
	time.Sleep(1200 * time.Millisecond)
	tree := ""
	if d, ok := surf.Driver().(studio.Dumper); ok {
		tree, _ = d.ViewTree(ctx)
	}
	return map[string]any{
		"ok":          true,
		"packageName": req.PackageName,
		"installed":   true,
		"state":       "launched",
		"visibleText": summarizeAndroidViewTree(tree),
	}, nil
}

func androidAppQuery(ctx context.Context, req androidAppRequest) (map[string]any, error) {
	out, err := androidAppLaunch(ctx, req)
	if err != nil {
		return nil, err
	}
	if out["ok"] != true {
		return out, nil
	}
	surf := redroidForAppSync(req)
	driver := surf.Driver()
	if strings.TrimSpace(req.Text) != "" {
		if err := driver.Type(ctx, req.Text); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(req.Key) != "" {
		if err := driver.Key(ctx, req.Key); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(req.WaitText) != "" {
		_ = driver.WaitText(ctx, req.WaitText, 8)
	}
	tree := ""
	if d, ok := driver.(studio.Dumper); ok {
		tree, _ = d.ViewTree(ctx)
	}
	needle := strings.TrimSpace(req.Query)
	found := needle != "" && strings.Contains(strings.ToLower(tree), strings.ToLower(needle))
	out["state"] = "queried"
	out["query"] = req.Query
	out["found"] = found
	out["visibleText"] = summarizeAndroidViewTree(tree)
	out["needsUser"] = looksLikeLoginScreen(tree)
	return out, nil
}

func summarizeAndroidViewTree(tree string) string {
	if strings.TrimSpace(tree) == "" {
		return ""
	}
	re := regexp.MustCompile(`(?:text|content-desc)="([^"]{1,120})"`)
	matches := re.FindAllStringSubmatch(tree, 40)
	seen := map[string]bool{}
	var parts []string
	for _, m := range matches {
		v := strings.TrimSpace(m[1])
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		parts = append(parts, v)
	}
	return strings.Join(parts, " | ")
}

func looksLikeLoginScreen(tree string) bool {
	lower := strings.ToLower(tree)
	for _, s := range []string{"login", "log in", "sign in", "email", "password", "otp", "verification"} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func (s *HTTPServer) handleAndroidAppStatus(w http.ResponseWriter, r *http.Request) {
	s.handleAndroidAppAction(w, r, androidAppStatus)
}

func (s *HTTPServer) handleAndroidAppInstall(w http.ResponseWriter, r *http.Request) {
	s.handleAndroidAppAction(w, r, androidAppInstall)
}

func (s *HTTPServer) handleAndroidAppLaunch(w http.ResponseWriter, r *http.Request) {
	s.handleAndroidAppAction(w, r, androidAppLaunch)
}

func (s *HTTPServer) handleAndroidAppQuery(w http.ResponseWriter, r *http.Request) {
	s.handleAndroidAppAction(w, r, androidAppQuery)
}

func (s *HTTPServer) handleAndroidAppAction(w http.ResponseWriter, r *http.Request, fn func(context.Context, androidAppRequest) (map[string]any, error)) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req androidAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	out, err := fn(r.Context(), req)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, out)
}

func dispatchAndroidAppMCP(s *HTTPServer, name string, raw json.RawMessage) (bool, interface{}) {
	action := func(path string, local func(context.Context, androidAppRequest) (map[string]any, error)) interface{} {
		var req androidAppRequest
		_ = json.Unmarshal(raw, &req)
		if strings.TrimSpace(req.DeviceID) != "" {
			body, _ := json.Marshal(req)
			status, resp, err := proxyToDevice(context.Background(), name, req.DeviceID, http.MethodPost, path, body)
			if err != nil && err != errProxyLocal {
				return mcpToolError(err.Error())
			}
			if err == nil {
				if status >= 400 {
					return mcpToolError(fmt.Sprintf("remote %s returned HTTP %d: %s", path, status, strings.TrimSpace(string(resp))))
				}
				return mcpToolJSON(json.RawMessage(resp))
			}
		}
		out, err := local(context.Background(), req)
		if err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolJSON(out)
	}
	switch name {
	case "android_app_status":
		return true, action("/android/apps/status", androidAppStatus)
	case "android_app_install":
		return true, action("/android/apps/install", androidAppInstall)
	case "android_app_launch":
		return true, action("/android/apps/launch", androidAppLaunch)
	case "android_app_query":
		return true, action("/android/apps/query", androidAppQuery)
	default:
		return false, nil
	}
}

func init() {
	reg := func(name, desc string, h func(context.Context, androidAppRequest) (map[string]any, error)) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, AllowGuest: false, Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var req androidAppRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			out, err := h(context.Background(), req)
			if err != nil {
				return OpsResult{OK: false, Code: "android_app_failed", Error: err.Error()}
			}
			return OpsResult{OK: out["ok"] != false, Initial: out}
		}})
	}
	reg("android_app_status", "Check whether a package is installed in the local Redroid app-sync surface.", androidAppStatus)
	reg("android_app_install", "Install an APK into the local Redroid app-sync surface. Requires package_name and apk_path; Play/package-name restore is intentionally unsupported until a store integration exists.", androidAppInstall)
	reg("android_app_launch", "Launch a package in the local Redroid app-sync surface and return visible UI text.", androidAppLaunch)
	reg("android_app_query", "Launch/query a package in Redroid and return whether visible UI contains query text plus login-handoff hints.", androidAppQuery)
}
