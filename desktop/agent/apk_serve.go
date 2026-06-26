package main

// Android HTTPS-serving cell.
//
// Lets a normie hand their Android app to real users without the Play Store:
//   1. LAN install  — start an in-process server, open the URL on any phone on
//      the same Wi-Fi, tap "Install".
//   2. HTTPS deploy — register a Caddy domain (auto Let's Encrypt) that
//      reverse-proxies this server, so the app installs from a public https url
//      with a working /.well-known/assetlinks.json (App Links + passkeys).
//
// Ported from talos cli/cmd/apkserve.go, but yaver-native: no hardcoded infra
// IPs (forbidden in this public repo). Publishing reuses the existing Caddy
// domain plane (domains.go) for TLS instead of SCP-to-a-fixed-Hetzner-box.
//
// Process residency matters: the install server must live in the always-on
// DAEMON (Caddy reverse-proxies to localhost:<port>, which must stay up — the
// ephemeral `yaver mcp` stdio process dies when Claude Code disconnects). So
// the ops verbs run the core directly when they're already inside the daemon
// (isDaemonProcess), and otherwise proxy over loopback to the daemon's
// /android/apk/* routes. One in-process mux owns every route (apk bytes,
// install page, version.json, assetlinks) so we keep full control of the
// dotfile path + content-types Caddy's file_server hides by default.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// apkEntry is one published Android app the server can hand out.
type apkEntry struct {
	App         string    `json:"app"`              // slug, used in URLs (/<app>.apk)
	Path        string    `json:"-"`                // local file path — never serialized (leaks home-dir username)
	File        string    `json:"file"`             // basename of the staged apk
	Package     string    `json:"package"`          // android package name, for assetlinks
	VersionName string    `json:"versionName"`      // e.g. 1.2.3
	VersionCode int       `json:"versionCode"`      // monotonically increasing int
	SHA256      []string  `json:"sha256,omitempty"` // signing cert fingerprints (colon-hex)
	Size        int64     `json:"size"`
	PublishedAt time.Time `json:"publishedAt"`
}

type apkServer struct {
	mu      sync.Mutex
	srv     *http.Server
	ln      net.Listener
	port    int
	apps    map[string]*apkEntry // by app slug
	latest  string               // app slug of the most recently published entry
	started time.Time
}

var apkSrv = &apkServer{apps: map[string]*apkEntry{}}

// apkServeDir is where published APKs are copied so they survive the source
// build dir being cleaned. ~/.yaver/apk/<app>-<versionCode>.apk
func apkServeDir() string {
	base, _ := ConfigDir()
	return filepath.Join(base, "apk")
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '_', r == '.', r == '/', r == '-':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "app"
	}
	return out
}

// resolveAPKPath validates a caller-supplied apk path.
func resolveAPKPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("apk required: pass apk=<path to .apk> (build a universal APK from your AAB with bundletool if needed)")
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("apk not found: %s", p)
	}
	if st.IsDir() {
		return "", fmt.Errorf("apk path is a directory, not a file: %s", p)
	}
	return p, nil
}

// register copies the apk into the serve dir and records it. Caller holds no lock.
func (a *apkServer) register(e *apkEntry) error {
	src, err := resolveAPKPath(e.Path)
	if err != nil {
		return err
	}
	dir := apkServeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, fmt.Sprintf("%s-%d.apk", e.App, e.VersionCode))
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read apk: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("stage apk: %w", err)
	}
	e.Path = dst
	e.File = filepath.Base(dst)
	e.Size = int64(len(data))
	e.PublishedAt = time.Now().UTC()

	a.mu.Lock()
	a.apps[e.App] = e
	a.latest = e.App
	a.mu.Unlock()
	return nil
}

func (a *apkServer) snapshot() (running bool, port int, apps []*apkEntry, latest string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	running = a.srv != nil
	port = a.port
	latest = a.latest
	apps = make([]*apkEntry, 0, len(a.apps))
	for _, e := range a.apps {
		apps = append(apps, e)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].App < apps[j].App })
	return
}

// assetlinksJSON builds the aggregated Digital Asset Links document for every
// published app that has a package name + at least one fingerprint. Android
// Credential Manager (passkeys) and verified App Links both read this from
// https://<domain>/.well-known/assetlinks.json.
func (a *apkServer) assetlinksJSON() []byte {
	_, _, apps, _ := a.snapshot()
	type target struct {
		Namespace    string   `json:"namespace"`
		PackageName  string   `json:"package_name"`
		Fingerprints []string `json:"sha256_cert_fingerprints"`
	}
	type stmt struct {
		Relation []string `json:"relation"`
		Target   target   `json:"target"`
	}
	out := []stmt{}
	for _, e := range apps {
		if e.Package == "" || len(e.SHA256) == 0 {
			continue
		}
		out = append(out, stmt{
			Relation: []string{
				"delegate_permission/common.get_login_creds",
				"delegate_permission/common.handle_all_urls",
			},
			Target: target{Namespace: "android_app", PackageName: e.Package, Fingerprints: e.SHA256},
		})
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return b
}

const apkInstallPageTmpl = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Install __TITLE__</title>
<style>
body{font-family:-apple-system,Segoe UI,Roboto,sans-serif;background:radial-gradient(120% 80% at 50% 0%,#16243f,#0f1b34 45%,#070b13);color:#e6edf8;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
.card{text-align:center;max-width:360px;padding:8px}
h1{letter-spacing:2px;font-weight:800;text-shadow:0 0 18px rgba(120,160,230,.5);margin:0 0 6px}
.v{color:#9fbcef;font-weight:700;letter-spacing:1px;font-size:13px}
a.btn{display:block;margin:18px 0 8px;background:#3f6bd0;color:#fff;padding:15px 24px;border-radius:14px;text-decoration:none;font-weight:800;box-shadow:0 8px 24px rgba(63,107,208,.35)}
.s{color:#9fb2cd;font-size:12.5px;margin-top:14px;line-height:1.5}
</style></head><body><div class="card"><h1>__TITLE__</h1><div class="v">__SUBTITLE__</div>
__BUTTONS__
<div class="s">Android only. If the install is blocked, allow &ldquo;install unknown apps&rdquo; for your browser, then reopen the download.</div>
</div></body></html>`

func (a *apkServer) installPage() string {
	_, _, apps, _ := a.snapshot()
	title := "Install App"
	subtitle := ""
	var buttons strings.Builder
	if len(apps) == 1 {
		title = "Install " + strings.Title(apps[0].App)
		subtitle = "v" + apps[0].VersionName
	} else if len(apps) > 1 {
		title = "Install"
		subtitle = fmt.Sprintf("%d apps available", len(apps))
	}
	for _, e := range apps {
		label := "⬇ Install " + strings.Title(e.App)
		if e.VersionName != "" {
			label += " (v" + e.VersionName + ")"
		}
		buttons.WriteString(fmt.Sprintf("<a class=\"btn\" href=\"/%s.apk\">%s</a>", e.App, label))
	}
	if buttons.Len() == 0 {
		buttons.WriteString("<div class=\"s\">No app published yet.</div>")
	}
	page := strings.ReplaceAll(apkInstallPageTmpl, "__TITLE__", title)
	page = strings.ReplaceAll(page, "__SUBTITLE__", subtitle)
	page = strings.ReplaceAll(page, "__BUTTONS__", buttons.String())
	return page
}

func (a *apkServer) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/assetlinks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(a.assetlinksJSON())
	})

	mux.HandleFunc("/version.json", func(w http.ResponseWriter, r *http.Request) {
		app := strings.TrimSpace(r.URL.Query().Get("app"))
		a.mu.Lock()
		if app == "" {
			app = a.latest
		}
		e := a.apps[app]
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if e == nil {
			http.Error(w, `{"error":"no app published"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(e)
	})

	// /<app>.apk and /latest.apk
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, a.installPage())
			return
		}
		if strings.HasSuffix(p, ".apk") {
			slug := strings.TrimSuffix(p, ".apk")
			a.mu.Lock()
			if slug == "latest" {
				slug = a.latest
			}
			e := a.apps[slug]
			a.mu.Unlock()
			if e == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.android.package-archive")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", slug+".apk"))
			http.ServeFile(w, r, e.Path)
			return
		}
		http.NotFound(w, r)
	})
	return mux
}

// start brings the in-process server up on the given port (0 => 8000). Idempotent.
func (a *apkServer) start(port int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv != nil {
		return nil
	}
	if port <= 0 {
		port = 8000
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("cannot bind :%d (already in use?): %w", port, err)
	}
	srv := &http.Server{Handler: a.handler()}
	a.srv = srv
	a.ln = ln
	a.port = port
	a.started = time.Now()
	go func() { _ = srv.Serve(ln) }()
	return nil
}

func (a *apkServer) stop() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv == nil {
		return false
	}
	_ = a.srv.Close()
	a.srv = nil
	a.ln = nil
	a.port = 0
	return true
}

// entryFromArgs builds an apkEntry from a payload map, falling back to the vault
// for the signing SHA when the caller doesn't pass one explicitly.
func entryFromArgs(args map[string]interface{}, vs *VaultStore) (*apkEntry, error) {
	path, err := resolveAPKPath(strv(args["apk"]))
	if err != nil {
		return nil, err
	}
	app := slugify(strv(args["app"]))
	if strv(args["app"]) == "" {
		app = slugify(strings.TrimSuffix(filepath.Base(path), ".apk"))
	}
	e := &apkEntry{
		App:         app,
		Path:        path,
		Package:     strv(args["package"]),
		VersionName: strv(args["versionName"]),
		VersionCode: intv(args["versionCode"]),
	}
	if sha := strv(args["sha256"]); sha != "" {
		e.SHA256 = splitSHAs(sha)
	}
	// Vault fallback for the signing fingerprint (CLAUDE.md: ANDROID_RELEASE_SHA256).
	if len(e.SHA256) == 0 && vs != nil {
		if ent, gerr := vs.Get("", "ANDROID_RELEASE_SHA256"); gerr == nil && ent != nil && ent.Value != "" {
			e.SHA256 = splitSHAs(ent.Value)
		}
	}
	return e, nil
}

func splitSHAs(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, strings.ToUpper(part))
		}
	}
	return out
}

func strv(v interface{}) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func intv(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		n := 0
		fmt.Sscanf(strings.TrimSpace(t), "%d", &n)
		return n
	}
	return 0
}

func apkVaultStore(s *HTTPServer) *VaultStore {
	if s != nil && s.vaultStore != nil {
		return s.vaultStore
	}
	return currentRuntimeVaultStore()
}

// ---- core (operate on the persistent apkSrv; run inside the daemon) ----

func (s *HTTPServer) apkServeCore(args map[string]interface{}) (map[string]interface{}, error) {
	e, err := entryFromArgs(args, apkVaultStore(s))
	if err != nil {
		return nil, err
	}
	if err := apkSrv.register(e); err != nil {
		return nil, err
	}
	if err := apkSrv.start(intv(args["port"])); err != nil {
		return nil, err
	}
	ip := getLocalIP()
	if ip == "" {
		ip = "127.0.0.1"
	}
	_, port, apps, _ := apkSrv.snapshot()
	AuditLog("", "android_apk_serve", e.App, fmt.Sprintf("v%s(%d)", e.VersionName, e.VersionCode), "success", "", "")
	return map[string]interface{}{
		"ok":     true,
		"mode":   "lan",
		"url":    fmt.Sprintf("http://%s:%d/", ip, port),
		"apkUrl": fmt.Sprintf("http://%s:%d/%s.apk", ip, port, e.App),
		"port":   port,
		"apps":   apps,
		"hint":   "Open the url on any Android phone on the same Wi-Fi to install. For a public https url, run android_apk_publish with a domain. Stop with android_apk_stop.",
	}, nil
}

func (s *HTTPServer) apkPublishCore(args map[string]interface{}) (map[string]interface{}, error) {
	e, err := entryFromArgs(args, apkVaultStore(s))
	if err != nil {
		return nil, err
	}
	if e.VersionName == "" || e.VersionCode == 0 {
		return nil, fmt.Errorf("versionName and versionCode are required to publish")
	}
	if err := apkSrv.register(e); err != nil {
		return nil, err
	}
	if err := apkSrv.start(intv(args["port"])); err != nil {
		return nil, err
	}
	_, port, apps, _ := apkSrv.snapshot()
	resp := map[string]interface{}{
		"ok":          true,
		"app":         e.App,
		"versionName": e.VersionName,
		"versionCode": e.VersionCode,
		"size":        e.Size,
		"apps":        apps,
	}
	domain := strv(args["domain"])
	if domain == "" {
		ip := getLocalIP()
		if ip == "" {
			ip = "127.0.0.1"
		}
		resp["mode"] = "lan"
		resp["url"] = fmt.Sprintf("http://%s:%d/", ip, port)
		resp["hint"] = "No domain given — serving on LAN only. Pass domain=apps.example.com (DNS A-record → this box) for a public https install url."
		return resp, nil
	}
	// HTTPS: Caddy reverse-proxies the in-process server, auto Let's Encrypt.
	dnsMode := strv(args["dnsMode"])
	if _, derr := AddDomain(domain, fmt.Sprintf("localhost:%d", port), "", dnsMode); derr != nil {
		ip := getLocalIP()
		resp["mode"] = "lan"
		resp["caddyError"] = derr.Error()
		resp["url"] = fmt.Sprintf("http://%s:%d/", ip, port)
		resp["hint"] = "APK is staged + served locally, but Caddy setup failed. Fix DNS/Caddy then retry, or use the LAN url."
		return resp, nil
	}
	resp["mode"] = "https"
	resp["url"] = fmt.Sprintf("https://%s/", domain)
	resp["apkUrl"] = fmt.Sprintf("https://%s/%s.apk", domain, e.App)
	resp["assetlinks"] = fmt.Sprintf("https://%s/.well-known/assetlinks.json", domain)
	if e.Package == "" || len(e.SHA256) == 0 {
		resp["assetlinksWarning"] = "package and/or sha256 missing — assetlinks.json will be empty, so App Links + passkeys won't bind. Pass package=<id> sha256=<fingerprint> (or set vault ANDROID_RELEASE_SHA256)."
	}
	resp["hint"] = "Point a DNS A-record for " + domain + " at this box's public IP; Caddy fetches the TLS cert on first hit. Share the url for real installs."
	AuditLog("", "android_apk_publish", domain, e.App, "success", "", "")
	return resp, nil
}

func (s *HTTPServer) apkStatusCore() map[string]interface{} {
	running, port, apps, latest := apkSrv.snapshot()
	ip := getLocalIP()
	out := map[string]interface{}{"running": running, "port": port, "apps": apps, "latest": latest}
	if running && ip != "" {
		out["url"] = fmt.Sprintf("http://%s:%d/", ip, port)
	}
	return out
}

func (s *HTTPServer) apkStopCore() map[string]interface{} {
	return map[string]interface{}{"ok": true, "stopped": apkSrv.stop()}
}

// ---- daemon HTTP routes (own the persistent server) ----

func (s *HTTPServer) handleAndroidApkServe(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	res, err := s.apkServeCore(body)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleAndroidApkPublish(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	res, err := s.apkPublishCore(body)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleAndroidApkStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.apkStatusCore())
}

func (s *HTTPServer) handleAndroidApkStop(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.apkStopCore())
}

// ---- ops verbs (web + mobile + `ops` grand-tool + CLI) ----
//
// When already inside the always-on daemon, run the core directly so the
// server lives here. Otherwise (the ephemeral `yaver mcp` stdio process, or a
// one-shot CLI), proxy over loopback to the daemon so the server outlives us.

func apkOpsResult(res map[string]interface{}, err error) OpsResult {
	if err != nil {
		return OpsResult{OK: false, Code: "apk_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: res}
}

func apkProxyOrCore(c OpsContext, path string, payload json.RawMessage, core func() (map[string]interface{}, error)) OpsResult {
	if isDaemonProcess {
		return apkOpsResult(core())
	}
	var body map[string]interface{}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &body)
	}
	res, err := localAgentRequest("POST", path, body)
	if err != nil {
		return OpsResult{OK: false, Code: "apk_error", Error: err.Error()}
	}
	if ok, present := res["ok"].(bool); present && !ok {
		msg, _ := res["error"].(string)
		return OpsResult{OK: false, Code: "apk_error", Error: msg}
	}
	return OpsResult{OK: true, Initial: res}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "android_apk_serve",
		Description: "Serve an Android APK on the LAN for instant install — open the returned url on any phone on the same Wi-Fi, tap Install. No Play Store.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"apk":     map[string]interface{}{"type": "string", "description": "Path to the .apk (build a universal APK from your AAB with bundletool if needed)"},
			"app":     map[string]interface{}{"type": "string", "description": "Short app slug used in the url (default: derived from filename)"},
			"package": map[string]interface{}{"type": "string", "description": "Android package name (needed for assetlinks/App Links)"},
			"port":    map[string]interface{}{"type": "number", "description": "Port to bind (default: 8000)"},
		}, "apk"),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			return apkProxyOrCore(c, "/android/apk/serve", payload, func() (map[string]interface{}, error) {
				return serverFromCtx(c).apkServeCore(payloadMap(payload))
			})
		},
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "android_apk_publish",
		Description: "Publish an Android APK over public HTTPS via Caddy (auto Let's Encrypt) with a working /.well-known/assetlinks.json for App Links + passkeys. Pass a domain whose DNS A-record points at this box.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"apk":         map[string]interface{}{"type": "string", "description": "Path to the .apk"},
			"app":         map[string]interface{}{"type": "string", "description": "Short app slug used in the url"},
			"package":     map[string]interface{}{"type": "string", "description": "Android package name (for assetlinks)"},
			"versionName": map[string]interface{}{"type": "string", "description": "e.g. 1.2.3"},
			"versionCode": map[string]interface{}{"type": "number", "description": "monotonic integer"},
			"sha256":      map[string]interface{}{"type": "string", "description": "Signing cert SHA-256 fingerprint(s), colon-hex, comma-separated (falls back to vault ANDROID_RELEASE_SHA256)"},
			"domain":      map[string]interface{}{"type": "string", "description": "Public hostname to serve from (omit for LAN-only)"},
			"dnsMode":     map[string]interface{}{"type": "string", "description": "http or cloudflare (default: http)"},
			"port":        map[string]interface{}{"type": "number", "description": "Local port Caddy proxies to (default: 8000)"},
		}, "apk", "versionName", "versionCode"),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			return apkProxyOrCore(c, "/android/apk/publish", payload, func() (map[string]interface{}, error) {
				return serverFromCtx(c).apkPublishCore(payloadMap(payload))
			})
		},
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "android_apk_status",
		Description: "Show the Android APK server status — running, port, published apps, install url.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			if isDaemonProcess {
				return OpsResult{OK: true, Initial: serverFromCtx(c).apkStatusCore()}
			}
			res, err := localAgentRequest("GET", "/android/apk/status", nil)
			if err != nil {
				return OpsResult{OK: false, Code: "apk_error", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: res}
		},
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "android_apk_stop",
		Description: "Stop the Android APK server.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			if isDaemonProcess {
				return OpsResult{OK: true, Initial: serverFromCtx(c).apkStopCore()}
			}
			res, err := localAgentRequest("POST", "/android/apk/stop", nil)
			if err != nil {
				return OpsResult{OK: false, Code: "apk_error", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: res}
		},
	})
}

func payloadMap(payload json.RawMessage) map[string]interface{} {
	var m map[string]interface{}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &m)
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

func serverFromCtx(c OpsContext) *HTTPServer {
	if c.Server != nil {
		return c.Server
	}
	return &HTTPServer{}
}
