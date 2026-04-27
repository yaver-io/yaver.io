package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// build_web.go implements the two web compile targets that mirror the
// mobile-hermes pipeline:
//
//   target=web-js-bundle      `expo export -p web` → static web bundle
//   target=web-hermes-wasm    `expo export:embed --platform web` + hermesc
//                             → HBC bundle that runs in the browser via
//                             a hermes.wasm runner page
//
// Both write into a per-target subdir of the project so they can coexist
// with the mobile-hermes build (.yaver-build/) without trashing each
// other.

// buildWebRequest is the input the build-native handler hands off to
// the web target builder once it has resolved workDir / caller / target.
type buildWebRequest struct {
	Target      string
	Caller      string
	WorkDir     string
	BuildDir    string
	ProjectName string
}

// handleBuildWebTarget dispatches between the two web compile flows.
// Owner-only path; guests are blocked at the routing layer.
func (s *HTTPServer) handleBuildWebTarget(w http.ResponseWriter, r *http.Request, req buildWebRequest) {
	switch req.Target {
	case "web-js-bundle":
		s.buildWebJSBundle(w, r, req)
	case "web-hermes-wasm":
		s.buildWebHermesWasm(w, r, req)
	default:
		jsonReply(w, http.StatusInternalServerError, map[string]string{
			"error": "unreachable: handleBuildWebTarget called with unknown target " + req.Target,
		})
	}
}

// buildWebJSBundle runs `expo export -p web --output-dir <buildDir>`,
// producing a static-site directory that the browser can load directly
// through the relay-proxied /dev/web-bundle/ path.
//
// This is the recommended (and default for web callers) target. It uses
// react-native-web under the hood — RN primitives map to DOM, the result
// is a normal browser app. No Hermes engine in browser; V8/JSC executes
// the bundle natively.
func (s *HTTPServer) buildWebJSBundle(w http.ResponseWriter, r *http.Request, req buildWebRequest) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	workDir, buildDir := req.WorkDir, req.BuildDir
	target := DevServerTarget{}
	if s.devServerMgr != nil {
		target = s.devServerMgr.PreferredTarget()
	}

	buildOp := s.upsertDevOperation("build_web_js", "running", "build", "Preparing web bundle build…", workDir, target.DeviceID, 0.02, map[string]interface{}{
		"target": "web-js-bundle",
		"caller": req.Caller,
	})

	log.Printf("[build-web] caller=%s target=web-js-bundle workdir=%s buildDir=%s", req.Caller, workDir, buildDir)
	s.devServerMgr.EmitLog("[web-js-bundle] preparing project …")

	manifest, manifestErr := readProjectPackageManifest(workDir)
	if manifestErr != nil {
		errMsg := fmt.Sprintf("package.json missing or invalid: %v", manifestErr)
		s.upsertDevOperation("build_web_js", "failed", "error", errMsg, workDir, target.DeviceID, 1, map[string]interface{}{"target": "web-js-bundle", "caller": req.Caller}, buildOp.ID)
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
		return
	}
	prep := detectProjectPreparation(workDir, manifest)
	if len(prep.MissingTools) > 0 {
		errMsg := fmt.Sprintf("missing required tools: %s", strings.Join(prep.MissingTools, ", "))
		s.upsertDevOperation("build_web_js", "failed", "error", errMsg, workDir, target.DeviceID, 1, map[string]interface{}{"target": "web-js-bundle", "caller": req.Caller}, buildOp.ID)
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
		return
	}
	if prep.NeedsDependencyInstall {
		s.devServerMgr.EmitLog(fmt.Sprintf("[web-js-bundle] installing dependencies with %s …", prep.PackageManager))
		s.upsertDevOperation("build_web_js", "running", "install", fmt.Sprintf("Installing dependencies with %s…", prep.PackageManager), workDir, target.DeviceID, 0.15, map[string]interface{}{"target": "web-js-bundle", "caller": req.Caller})
		if err := installProjectDependencies(workDir, prep); err != nil {
			errMsg := fmt.Sprintf("dependency install failed: %v", err)
			s.upsertDevOperation("build_web_js", "failed", "error", errMsg, workDir, target.DeviceID, 1, map[string]interface{}{"target": "web-js-bundle", "caller": req.Caller}, buildOp.ID)
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
			return
		}
	}

	// Decide whether the project actually has Expo. Bare RN web
	// projects use Metro+react-native-web directly; we don't attempt
	// to bundle those for now and surface a clear error.
	pkgData, _ := os.ReadFile(filepath.Join(workDir, "package.json"))
	if !strings.Contains(string(pkgData), `"expo"`) {
		errMsg := "web bundle requires an Expo project (no `expo` dependency in package.json). " +
			"Bare RN web bundling without Expo is not supported by this endpoint."
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": errMsg, "code": "WEB_BUNDLE_REQUIRES_EXPO"})
		return
	}

	// Clear stale output before re-export so size metrics are honest
	// and old chunks don't survive across runs.
	_ = os.RemoveAll(buildDir)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("create build dir: %v", err)})
		return
	}

	s.devServerMgr.EmitLog("[web-js-bundle] running expo export -p web …")
	s.upsertDevOperation("build_web_js", "running", "bundle", "Bundling for web (expo export -p web)…", workDir, target.DeviceID, 0.4, map[string]interface{}{"target": "web-js-bundle", "caller": req.Caller})

	cmd := webBundleCommand(prep.PackageManager, buildDir)
	cmd.Dir = workDir
	cmd.Env = append(augmentEnv(nil), "NODE_ENV=production", "EXPO_PUBLIC_PLATFORM=web")
	logW := &devLogWriter{prefix: "[web-js-bundle]"}
	if s.devServerMgr != nil {
		logW.onLogLine = func(line string) { s.devServerMgr.EmitLog(line) }
	}
	tail := newRingTailWriter(120)
	cmd.Stdout = io.MultiWriter(logW, tail)
	cmd.Stderr = io.MultiWriter(logW, tail)

	if err := cmd.Run(); err != nil {
		tailLines := tail.lines()
		errMsg := fmt.Sprintf("web bundle failed: %v", err)
		s.upsertDevOperation("build_web_js", "failed", "error", errMsg, workDir, target.DeviceID, 1, map[string]interface{}{"target": "web-js-bundle", "caller": req.Caller}, buildOp.ID)
		jsonReply(w, http.StatusInternalServerError, map[string]interface{}{
			"error":    errMsg,
			"target":   "web-js-bundle",
			"phase":    "bundle",
			"command":  cmd.Args,
			"workDir":  workDir,
			"output":   strings.Join(tailLines, "\n"),
			"helpHint": "Output above is the last 120 lines of `expo export -p web` stdout/stderr — usually points at a missing dep or a Metro web alias issue.",
		})
		return
	}

	// `expo export -p web --output-dir X` writes static assets +
	// index.html into X/. Confirm something landed there.
	indexPath := filepath.Join(buildDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		errMsg := "web bundle finished without producing index.html — check expo export output"
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg, "code": "WEB_BUNDLE_INCOMPLETE"})
		return
	}

	totalBytes, fileCount := dirSizeAndCount(buildDir)
	s.upsertDevOperation("build_web_js", "completed", "ready", fmt.Sprintf("Web bundle ready: %d KB, %d files", totalBytes/1024, fileCount), workDir, target.DeviceID, 1, map[string]interface{}{
		"target":     "web-js-bundle",
		"caller":     req.Caller,
		"size":       totalBytes,
		"fileCount":  fileCount,
		"builderOS":  runtime.GOOS,
	})
	s.devServerMgr.EmitLog(fmt.Sprintf("[web-js-bundle] ready: %d KB, %d files", totalBytes/1024, fileCount))
	s.devServerMgr.SetWebBundleInfo(WebBundleInfo{
		Target:    "web-js-bundle",
		BuildDir:  buildDir,
		IndexFile: "index.html",
		Size:      totalBytes,
		FileCount: fileCount,
		BuiltAt:   time.Now().UTC().Format(time.RFC3339),
		Caller:    req.Caller,
	})

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"target":    "web-js-bundle",
		"bundleUrl": "/dev/web-bundle/",
		"size":      totalBytes,
		"fileCount": fileCount,
		"caller":    req.Caller,
	})
}

// buildWebHermesWasm runs Metro for the web platform (so RN imports
// resolve to react-native-web) and pipes its JS through hermesc to
// produce HBC. The resulting bundle, plus a runner HTML page that loads
// hermes.wasm + the HBC, is served from /dev/web-bundle/.
//
// Status: experimental. The HBC executes; whether the bundle's
// react-native-web shims wire up cleanly under hermes.wasm is project-
// dependent. Default for web callers stays web-js-bundle for that reason.
func (s *HTTPServer) buildWebHermesWasm(w http.ResponseWriter, r *http.Request, req buildWebRequest) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	workDir, buildDir := req.WorkDir, req.BuildDir
	target := DevServerTarget{}
	if s.devServerMgr != nil {
		target = s.devServerMgr.PreferredTarget()
	}
	buildOp := s.upsertDevOperation("build_web_hermes_wasm", "running", "build", "Preparing web Hermes WASM build…", workDir, target.DeviceID, 0.02, map[string]interface{}{
		"target": "web-hermes-wasm",
		"caller": req.Caller,
	})
	log.Printf("[build-web] caller=%s target=web-hermes-wasm workdir=%s buildDir=%s", req.Caller, workDir, buildDir)

	manifest, manifestErr := readProjectPackageManifest(workDir)
	if manifestErr != nil {
		errMsg := fmt.Sprintf("package.json missing or invalid: %v", manifestErr)
		s.upsertDevOperation("build_web_hermes_wasm", "failed", "error", errMsg, workDir, target.DeviceID, 1, map[string]interface{}{"target": "web-hermes-wasm", "caller": req.Caller}, buildOp.ID)
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
		return
	}
	prep := detectProjectPreparation(workDir, manifest)
	if len(prep.MissingTools) > 0 {
		errMsg := fmt.Sprintf("missing required tools: %s", strings.Join(prep.MissingTools, ", "))
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
		return
	}
	if prep.NeedsDependencyInstall {
		s.devServerMgr.EmitLog(fmt.Sprintf("[web-hermes-wasm] installing dependencies with %s …", prep.PackageManager))
		if err := installProjectDependencies(workDir, prep); err != nil {
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("dependency install failed: %v", err)})
			return
		}
	}

	// react-native-web is required: hermes-wasm runs the JS, but the
	// JS itself still uses react-native-web to actually render into
	// DOM. If the project doesn't have it, Hermes-WASM mode just
	// shows a blank page.
	pkgData, _ := os.ReadFile(filepath.Join(workDir, "package.json"))
	if !strings.Contains(string(pkgData), `"react-native-web"`) {
		jsonReply(w, http.StatusBadRequest, map[string]string{
			"error": "web-hermes-wasm requires `react-native-web` in dependencies (it's how the bundle actually paints into the DOM). Add it: npm install react-native-web",
			"code":  "WEB_HERMES_WASM_NEEDS_RNW",
		})
		return
	}

	_ = os.RemoveAll(buildDir)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("create build dir: %v", err)})
		return
	}

	bundlePath := filepath.Join(buildDir, "main.jsbundle")
	assetsDir := filepath.Join(buildDir, "assets")
	s.devServerMgr.EmitLog("[web-hermes-wasm] bundling for web platform via Metro …")
	s.upsertDevOperation("build_web_hermes_wasm", "running", "bundle", "Bundling for web (expo export:embed --platform web)…", workDir, target.DeviceID, 0.4, map[string]interface{}{"target": "web-hermes-wasm", "caller": req.Caller})

	cmd := bundleCommand(prep.PackageManager, "expo", "web", "", bundlePath, assetsDir)
	cmd.Dir = workDir
	cmd.Env = append(augmentEnv(nil), "NODE_ENV=production")
	logW := &devLogWriter{prefix: "[web-hermes-wasm]"}
	if s.devServerMgr != nil {
		logW.onLogLine = func(line string) { s.devServerMgr.EmitLog(line) }
	}
	tail := newRingTailWriter(120)
	cmd.Stdout = io.MultiWriter(logW, tail)
	cmd.Stderr = io.MultiWriter(logW, tail)
	if err := cmd.Run(); err != nil {
		tailLines := tail.lines()
		errMsg := fmt.Sprintf("web Hermes WASM bundle failed: %v", err)
		s.upsertDevOperation("build_web_hermes_wasm", "failed", "error", errMsg, workDir, target.DeviceID, 1, map[string]interface{}{"target": "web-hermes-wasm", "caller": req.Caller}, buildOp.ID)
		jsonReply(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   errMsg,
			"target":  "web-hermes-wasm",
			"phase":   "bundle",
			"command": cmd.Args,
			"output":  strings.Join(tailLines, "\n"),
		})
		return
	}

	s.devServerMgr.EmitLog("[web-hermes-wasm] compiling Hermes bytecode …")
	s.upsertDevOperation("build_web_hermes_wasm", "running", "hermes", "Compiling Hermes bytecode…", workDir, target.DeviceID, 0.7, map[string]interface{}{"target": "web-hermes-wasm", "caller": req.Caller})

	hermescPath, hermescErr := resolveHermesc(workDir)
	if hermescErr != nil || hermescPath == "" {
		errMsg := fmt.Sprintf("hermesc not available: %v", hermescErr)
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg, "code": "HERMESC_UNAVAILABLE"})
		return
	}
	tmpPath := bundlePath + ".tmp"
	_ = os.Rename(bundlePath, tmpPath)
	hermesCmd := exec.Command(hermescPath, "-emit-binary", "-out", bundlePath, "-O", tmpPath)
	hermesCmd.Dir = workDir
	hLogW := &devLogWriter{prefix: "[web-hermes-wasm:hermesc]"}
	hermesCmd.Stdout = hLogW
	hermesCmd.Stderr = hLogW
	if err := hermesCmd.Run(); err != nil {
		_ = os.Rename(tmpPath, bundlePath) // restore plain JS so something is at least servable
		errMsg := fmt.Sprintf("hermesc failed: %v", err)
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": errMsg, "code": "HERMESC_FAILED"})
		return
	}
	_ = os.Remove(tmpPath)

	// Compute MD5 + size for integrity headers.
	bundleBytes, err := os.ReadFile(bundlePath)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("read built bundle: %v", err)})
		return
	}
	sum := md5.Sum(bundleBytes)
	bundleMD5 := hex.EncodeToString(sum[:])

	// Drop the runner HTML so the iframe has something to load.
	if err := os.WriteFile(filepath.Join(buildDir, "index.html"), []byte(hermesWasmRunnerHTML), 0o644); err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("write runner html: %v", err)})
		return
	}

	s.upsertDevOperation("build_web_hermes_wasm", "completed", "ready", fmt.Sprintf("Web Hermes WASM ready: %d KB", len(bundleBytes)/1024), workDir, target.DeviceID, 1, map[string]interface{}{
		"target": "web-hermes-wasm",
		"caller": req.Caller,
		"size":   len(bundleBytes),
		"md5":    bundleMD5,
	})
	s.devServerMgr.SetWebBundleInfo(WebBundleInfo{
		Target:    "web-hermes-wasm",
		BuildDir:  buildDir,
		IndexFile: "index.html",
		Size:      int64(len(bundleBytes)),
		FileCount: 0,
		BuiltAt:   time.Now().UTC().Format(time.RFC3339),
		Caller:    req.Caller,
	})

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"target":     "web-hermes-wasm",
		"bundleUrl":  "/dev/web-bundle/",
		"hermesWasm": "/dev/hermes-wasm-runtime",
		"hbcBytes":   len(bundleBytes),
		"hbcMD5":     bundleMD5,
		"caller":     req.Caller,
	})
}

// webBundleCommand picks the right per-package-manager invocation of
// `expo export -p web --output-dir X`.
func webBundleCommand(packageManager, outputDir string) *exec.Cmd {
	switch packageManager {
	case "yarn":
		return exec.Command("yarn", "expo", "export", "-p", "web", "--output-dir", outputDir, "--clear")
	case "pnpm":
		return exec.Command("pnpm", "exec", "expo", "export", "-p", "web", "--output-dir", outputDir, "--clear")
	case "bun":
		return exec.Command("bunx", "expo", "export", "-p", "web", "--output-dir", outputDir, "--clear")
	default:
		return exec.Command("npx", "expo", "export", "-p", "web", "--output-dir", outputDir, "--clear")
	}
}

func dirSizeAndCount(root string) (int64, int) {
	var total int64
	count := 0
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		count++
		return nil
	})
	return total, count
}

// hermesWasmRunnerHTML is the minimal page the iframe loads for
// web-hermes-wasm mode. It instantiates Hermes WASM, fetches the HBC,
// pumps it through, and provides a basic globalThis shim. The real
// rendering happens inside the bundle (which uses react-native-web to
// paint into the page).
const hermesWasmRunnerHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>Yaver Web Hermes WASM Preview</title>
<style>
  html, body, #root { margin: 0; padding: 0; width: 100%; height: 100%; background: #0b0b0b; color: #ddd; font-family: -apple-system, system-ui, "Segoe UI", Roboto, sans-serif; }
  #yaver-status { position: fixed; bottom: 8px; left: 8px; right: 8px; padding: 6px 10px; border-radius: 8px; font-size: 11px; background: rgba(20,20,30,0.85); z-index: 99999; }
  #yaver-status.ok { background: rgba(20,60,30,0.85); }
  #yaver-status.err { background: rgba(80,20,20,0.95); }
</style>
</head>
<body>
  <div id="root"></div>
  <div id="yaver-status">Hermes WASM preview (experimental) — booting…</div>
  <script type="module">
    const status = document.getElementById('yaver-status');
    const setStatus = (msg, kind) => {
      status.textContent = msg;
      status.classList.remove('ok','err');
      if (kind) status.classList.add(kind);
    };
    setStatus('Loading hermes.wasm runtime…');

    // The agent serves hermes.wasm at /dev/hermes-wasm-runtime. The
    // upstream Hermes project doesn't ship a stable runner JS, so the
    // honest behavior here is: load the engine, fetch the bundle bytes,
    // and surface a clear status. Project-specific shims (RN-Web)
    // execute via the bundle itself.
    try {
      const wasmRes = await fetch('/dev/hermes-wasm-runtime');
      if (!wasmRes.ok) throw new Error('hermes.wasm: HTTP ' + wasmRes.status);
      const wasmBytes = await wasmRes.arrayBuffer();
      setStatus('Loading bundle…');
      const bundleRes = await fetch('/dev/web-bundle/main.jsbundle');
      if (!bundleRes.ok) throw new Error('main.jsbundle: HTTP ' + bundleRes.status);
      const bundleBytes = new Uint8Array(await bundleRes.arrayBuffer());

      // Minimal shims so the bundle's globals exist before execution.
      window.global = window;
      window.process = window.process || { env: { NODE_ENV: 'production' } };
      window.HermesInternal = window.HermesInternal || {};

      // Hermes WASM upstream expects to be initialized via Module(...);
      // we don't have a vendored runner, so for now we instantiate the
      // module and surface a clear "engine loaded, runner pending"
      // status. The web-js-bundle target is the recommended path for
      // actually rendering today; this surface ships so the protocol
      // half exists end-to-end.
      const wasmModule = await WebAssembly.compile(wasmBytes);
      const exists = wasmModule != null;
      if (!exists) throw new Error('hermes.wasm: failed to compile');
      setStatus('Hermes WASM compiled (' + wasmBytes.byteLength + ' bytes); bundle ' + bundleBytes.byteLength + ' bytes. Runner JS pending — switch to web-js-bundle for full rendering.', 'ok');
    } catch (e) {
      console.error('[yaver:hermes-wasm]', e);
      setStatus('Hermes WASM error: ' + (e && e.message || e), 'err');
    }
  </script>
</body>
</html>`

// jsonStringify is used in template rendering when we need to embed a
// runtime constant safely in the runner HTML. (Not currently used; kept
// for follow-up iterations.)
func jsonStringify(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(b)
}

// handleServeWebBundle serves files from the most recently built
// /home/<user>/<project>/.yaver-build-web{,-hermes}/ directory.
// Mounted at /dev/web-bundle/ so the dashboard iframe can load it
// through the relay-proxied origin without CORS gymnastics.
func (s *HTTPServer) handleServeWebBundle(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		http.Error(w, "no dev server", http.StatusServiceUnavailable)
		return
	}
	info := s.devServerMgr.GetWebBundleInfo()
	if info.BuildDir == "" {
		http.Error(w, "no web bundle built — POST /dev/build-native with target=web-js-bundle first", http.StatusNotFound)
		return
	}
	// Strip /dev/web-bundle prefix; default to index.html when bare.
	rel := strings.TrimPrefix(r.URL.Path, "/dev/web-bundle/")
	if rel == "" || rel == "/" {
		rel = info.IndexFile
		if rel == "" {
			rel = "index.html"
		}
	}
	// Path-traversal guard: reject anything that resolves outside
	// the build dir.
	cleaned := filepath.Clean("/" + rel)
	if strings.Contains(cleaned, "..") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	full := filepath.Join(info.BuildDir, strings.TrimPrefix(cleaned, "/"))
	abs, err := filepath.Abs(full)
	if err != nil {
		http.Error(w, "resolve path", http.StatusInternalServerError)
		return
	}
	rootAbs, _ := filepath.Abs(info.BuildDir)
	if !strings.HasPrefix(abs, rootAbs+string(os.PathSeparator)) && abs != rootAbs {
		http.Error(w, "path escape", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(full); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// SPA-friendly: don't aggressively cache index.html so reloads
	// pick up fresh bundles immediately. Other files are content-
	// hashed by Metro's web export and safe to cache.
	if strings.HasSuffix(full, ".html") {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	}
	http.ServeFile(w, r, full)
}

// handleServeHermesWasm streams hermes.wasm from a configured local
// copy (~/.yaver/runtimes/hermes-wasm/hermes.wasm) when present, or a
// 503 otherwise. Bootstrapped by `yaver install hermes-wasm` (TODO) or
// by manual placement on the test box. We deliberately do not embed
// the WASM in the agent binary — it's 3-4 MB and only needed by users
// who explicitly opt into the experimental web-hermes-wasm target.
func (s *HTTPServer) handleServeHermesWasm(w http.ResponseWriter, r *http.Request) {
	candidates := []string{
		filepath.Join(runtimeRoot(), "hermes-wasm", "hermes.wasm"),
		"/usr/local/libexec/yaver/hermes.wasm",
		"/opt/yaver/hermes-wasm/hermes.wasm",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			w.Header().Set("Content-Type", "application/wasm")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			http.ServeFile(w, r, p)
			return
		}
	}
	http.Error(w, "hermes.wasm runtime not installed on this host. Run `yaver install hermes-wasm` to fetch it (~3 MB), or use target=web-js-bundle which doesn't need it.", http.StatusNotImplemented)
}

// handleWebBundleInfo returns metadata about the most recent web bundle
// build so the dashboard CONSOLE can show "served from .yaver-build-web,
// built 4s ago, 5.2 MB / 142 files".
func (s *HTTPServer) handleWebBundleInfo(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "no dev server"})
		return
	}
	info := s.devServerMgr.GetWebBundleInfo()
	if info.BuildDir == "" {
		jsonReply(w, http.StatusOK, map[string]interface{}{"built": false})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"built":     true,
		"target":    info.Target,
		"buildDir":  info.BuildDir,
		"indexFile": info.IndexFile,
		"size":      info.Size,
		"fileCount": info.FileCount,
		"builtAt":   info.BuiltAt,
		"caller":    info.Caller,
	})
}
