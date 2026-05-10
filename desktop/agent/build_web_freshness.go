package main

// build_web_freshness.go — serve-time freshness guard for /dev/web-bundle/.
//
// Without this, opening the dashboard "Web App" tab loads whatever
// /dev/web-bundle/ has on disk, regardless of how stale it is relative
// to the source tree. If the user pushed code from another machine
// (or pulled changes via `git pull` outside Yaver), the iframe keeps
// rendering the old bundle until someone explicitly clicks Rebuild.
//
// The guard fires only on the *index* request (/dev/web-bundle/ or any
// .html), never on asset fetches — so it costs one git rev-parse per
// iframe load, not per CSS/JS request. When the bundle is older than
// HEAD (or the BuildDir vanished), it kicks off an asynchronous rebuild
// via the existing /dev/build-native pipeline (which already runs the
// pre-build pull) and serves a small placeholder page that polls
// /dev/web-bundle/info every 2 s and reloads when builtAt advances.
//
// Failure modes are intentionally silent: not a git checkout, no
// upstream, detached HEAD, parse errors, etc. all skip the rebuild and
// fall through to the normal serve. Visible-failure-over-silent-retry
// is for *user-visible* state — here, the user just sees the stale
// bundle, which is the same as before this change.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// webRebuildSlots tracks per-workDir in-flight auto-rebuilds so that
// concurrent iframe loads don't fan out N parallel `expo export` jobs.
// Build still completes in the background; subsequent index hits just
// serve the rebuilding page until the build finishes and BuiltAt
// advances.
var (
	webRebuildSlotsMu sync.Mutex
	webRebuildSlots   = map[string]time.Time{}
)

const webRebuildSlotTTL = 5 * time.Minute

// claimWebRebuildSlot returns true when the caller acquired the
// per-workDir slot. False means another goroutine is already rebuilding
// (or rebuilt within the TTL). Cleared by releaseWebRebuildSlot or by
// TTL expiry on a later call.
func claimWebRebuildSlot(workDir string) bool {
	if strings.TrimSpace(workDir) == "" {
		return false
	}
	webRebuildSlotsMu.Lock()
	defer webRebuildSlotsMu.Unlock()
	now := time.Now()
	if at, ok := webRebuildSlots[workDir]; ok && now.Sub(at) < webRebuildSlotTTL {
		return false
	}
	webRebuildSlots[workDir] = now
	return true
}

func releaseWebRebuildSlot(workDir string) {
	if strings.TrimSpace(workDir) == "" {
		return
	}
	webRebuildSlotsMu.Lock()
	delete(webRebuildSlots, workDir)
	webRebuildSlotsMu.Unlock()
}

// webBundleStaleVsHead reports whether the bundle is older than the
// current git HEAD commit. ok=false means we can't decide (not a git
// repo, no HEAD, builtAt unparseable) and the caller should not auto-
// rebuild — too risky to flip "I don't know" into "rebuild on every
// hit" in a non-git workdir.
func webBundleStaleVsHead(workDir, builtAt string) (stale bool, headTime time.Time, ok bool) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return false, time.Time{}, false
	}
	if out, err := runGit(workDir, "rev-parse", "--is-inside-work-tree"); err != nil ||
		strings.TrimSpace(out) != "true" {
		return false, time.Time{}, false
	}
	out, err := runGit(workDir, "log", "-1", "--format=%ct", "HEAD")
	if err != nil {
		return false, time.Time{}, false
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return false, time.Time{}, false
	}
	headTime = time.Unix(secs, 0).UTC()
	if strings.TrimSpace(builtAt) == "" {
		// No prior build timestamp recorded: treat anything-newer-than-
		// nothing as stale so the first hit triggers a build.
		return true, headTime, true
	}
	built, err := time.Parse(time.RFC3339, builtAt)
	if err != nil {
		return false, headTime, false
	}
	return headTime.After(built.UTC()), headTime, true
}

// resolveWebBundleWorkDir falls back to deriving WorkDir from BuildDir
// when the persisted info predates the WorkDir field. BuildDir is
// always `<workDir>/.yaver-build-web{,-hermes}` so the parent is the
// repo root.
func resolveWebBundleWorkDir(info WebBundleInfo) string {
	if wd := strings.TrimSpace(info.WorkDir); wd != "" {
		return wd
	}
	if strings.TrimSpace(info.BuildDir) == "" {
		return ""
	}
	parent := filepath.Dir(info.BuildDir)
	base := filepath.Base(info.BuildDir)
	switch base {
	case ".yaver-build-web", ".yaver-build-web-hermes":
		return parent
	}
	return ""
}

// triggerWebBundleRebuildAsync fires off /dev/build-native for the
// recorded target in the background. Idempotent per workDir — only the
// first caller within webRebuildSlotTTL actually spawns the build.
// The HTTP response from handleBuildNativeBundle is discarded; the
// dashboard learns about completion by polling /dev/web-bundle/info
// (which is what the rebuilding page already does).
func (s *HTTPServer) triggerWebBundleRebuildAsync(info WebBundleInfo) bool {
	if s == nil {
		return false
	}
	workDir := resolveWebBundleWorkDir(info)
	if workDir == "" {
		return false
	}
	target := strings.TrimSpace(info.Target)
	if target == "" {
		target = "web-js-bundle"
	}
	if !claimWebRebuildSlot(workDir) {
		return false
	}
	go func() {
		defer releaseWebRebuildSlot(workDir)
		body, _ := json.Marshal(map[string]string{
			"target":      target,
			"workDir":     workDir,
			"projectPath": workDir,
		})
		req, err := http.NewRequest(http.MethodPost, "/dev/build-native", bytes.NewReader(body))
		if err != nil {
			log.Printf("[web-bundle-freshness] build trigger failed: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Yaver-Caller", "web-bundle-freshness")
		rec := httptest.NewRecorder()
		log.Printf("[web-bundle-freshness] rebuild kicked off target=%s workDir=%s", target, workDir)
		if s.devServerMgr != nil {
			s.devServerMgr.EmitLog(fmt.Sprintf("[web-bundle-freshness] auto-rebuild kicked off (target=%s)", target))
		}
		s.handleBuildNativeBundle(rec, req)
		if rec.Code >= 400 {
			log.Printf("[web-bundle-freshness] auto-rebuild status=%d body=%s",
				rec.Code, strings.TrimSpace(rec.Body.String()))
			if s.devServerMgr != nil {
				s.devServerMgr.EmitLog(fmt.Sprintf("[web-bundle-freshness] auto-rebuild failed (status=%d)", rec.Code))
			}
			return
		}
		log.Printf("[web-bundle-freshness] auto-rebuild completed target=%s", target)
		if s.devServerMgr != nil {
			s.devServerMgr.EmitLog(fmt.Sprintf("[web-bundle-freshness] auto-rebuild completed (target=%s)", target))
		}
	}()
	return true
}

var webRebuildingPageTmpl = template.Must(template.New("rebuilding").Parse(`<!doctype html>
<html><head>
<meta charset="utf-8">
<title>Yaver — rebuilding bundle</title>
<style>
  body{margin:0;background:#0b0d12;color:#e8eaed;font:14px/1.5 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh}
  .card{max-width:420px;text-align:center;padding:24px}
  .dot{display:inline-block;width:8px;height:8px;border-radius:50%;background:#7c5cff;margin-right:8px;animation:p 1.4s infinite}
  @keyframes p{0%,100%{opacity:.3}50%{opacity:1}}
  .hint{color:#8a8f98;font-size:12px;margin-top:14px}
  code{background:#1a1d24;padding:1px 6px;border-radius:4px;color:#bfc4cc}
</style>
</head><body>
<div class="card">
  <div><span class="dot"></span>Rebuilding web bundle…</div>
  <div class="hint">HEAD is newer than the cached bundle. Pulling latest + rebuilding before serving.</div>
  <div class="hint">Built at: <code>{{.BuiltAt}}</code><br>HEAD at: <code>{{.HeadAt}}</code></div>
</div>
<script>
(function(){
  var startedBuiltAt = {{.BuiltAtJS}};
  function poll(){
    fetch('/dev/web-bundle/info', {credentials:'same-origin'}).then(function(r){return r.json()}).then(function(j){
      if(j && j.builtAt && j.builtAt !== startedBuiltAt){ location.reload(); return; }
      setTimeout(poll, 2000);
    }).catch(function(){ setTimeout(poll, 3000); });
  }
  setTimeout(poll, 1500);
})();
</script>
</body></html>`))

func renderWebRebuildingPage(builtAt string, headTime time.Time) []byte {
	var buf bytes.Buffer
	data := struct {
		BuiltAt   string
		HeadAt    string
		BuiltAtJS template.JS
	}{
		BuiltAt:   firstNonEmptyString(builtAt, "(no prior build)"),
		HeadAt:    headTime.Format(time.RFC3339),
		BuiltAtJS: template.JS(strconv.Quote(builtAt)),
	}
	if err := webRebuildingPageTmpl.Execute(&buf, data); err != nil {
		// Fallback to a minimal static body so the iframe never gets
		// an empty response on template error.
		return []byte(`<!doctype html><meta http-equiv="refresh" content="2"><body>Rebuilding…</body>`)
	}
	return buf.Bytes()
}

func firstNonEmptyString(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
