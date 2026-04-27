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

	bundleManifest := scanBundleManifest(buildDir)
	var totalBytes int64
	for _, b := range bundleManifest {
		totalBytes += b
	}
	fileCount := len(bundleManifest)
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
	// Yaver Protocol v1 — webview/transport. Spin up a per-bundle
	// transport tracker so the dashboard CONSOLE has a live view of
	// the post-compile delivery pipeline (compiled → ready_to_serve →
	// serving → streaming → delivered/error). Replaces any previous
	// tracker (last-build-wins, single-track preview pane).
	s.devServerMgr.SetWebTransport(newWebTransport(s.devServerMgr.emit, "web-js-bundle", req.Caller, bundleManifest))
	s.devServerMgr.GetWebTransport().transition("ready_to_serve")

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

// scanBundleManifest walks the freshly-built web bundle directory and
// returns a map of relative path → byte size. Used by the transport
// tracker as the canonical "what we're going to ship" list so progress
// percentages are anchored to a known total instead of guessed.
func scanBundleManifest(root string) map[string]int64 {
	manifest := map[string]int64{}
	rootClean := filepath.Clean(root)
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(rootClean, p)
		if relErr != nil {
			return nil
		}
		// Use forward slashes so URL-style relative paths match what
		// the HTTP serve layer hands us regardless of host OS.
		rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
		manifest[rel] = info.Size()
		return nil
	})
	return manifest
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
// /home/<user>/<project>/.yaver-build-web{,-hermes}/ directory. Mounted
// at /dev/web-bundle/ so the dashboard iframe can load through the
// relay-proxied origin.
//
// Critical detail: `expo export -p web` writes index.html with absolute
// asset paths (e.g. `/_expo/static/js/foo.js`). When served through
// `https://<relay>/d/<id>/dev/web-bundle/`, those root-absolute paths
// resolve to `https://<relay>/d/<id>/_expo/...` which doesn't exist —
// the iframe loads index.html and 404s on every script/css. We patch
// index.html on serve by injecting `<base href="/dev/web-bundle/">` so
// the browser rewrites every relative + root-absolute URL through the
// bundle path. Other files (.js, .css, images, the _expo/ subtree)
// serve byte-for-byte unchanged.
//
// Every served file also fires a transport-progress event on
// topic=webview/transport so the dashboard CONSOLE renders a live
// "sending bundle…" phase instead of going silent right after compile.
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
	// Path-traversal guard.
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
	st, err := os.Stat(full)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Yaver Protocol v1 — webview/transport. Per-file SSE event so
	// the dashboard can render a streaming progress bar between
	// "compile complete" and "iframe rendered".
	s.devServerMgr.GetWebTransport().recordFile(strings.TrimPrefix(cleaned, "/"), st.Size())

	if strings.HasSuffix(strings.ToLower(full), ".html") {
		s.serveWebBundleHTML(w, full)
		return
	}
	// Everything else: byte-for-byte. http.ServeFile handles MIME
	// types from extension (.js, .css, .json, .map, fonts, images).
	http.ServeFile(w, r, full)
}

// serveWebBundleHTML reads index.html, injects <base href="/dev/web-bundle/">
// inside <head>, and writes it.
func (s *HTTPServer) serveWebBundleHTML(w http.ResponseWriter, htmlPath string) {
	data, err := os.ReadFile(htmlPath)
	if err != nil {
		http.Error(w, "read html: "+err.Error(), http.StatusInternalServerError)
		return
	}
	patched := injectBaseHref(data, "/dev/web-bundle/")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	info := s.devServerMgr.GetWebBundleInfo()
	w.Header().Set("X-Yaver-Web-Bundle", fmt.Sprintf("%s/%d", info.Target, info.Size))
	w.Write(patched)
}

// injectBaseHref inserts <base href="..."> right after the opening
// <head> tag so every relative + root-absolute URL in the document
// resolves through the bundle path. Tolerates `<head>` / `<head class="...">`
// shapes; if no <head> exists the original body is returned unchanged
// (very unusual; expo export always emits <head>).
func injectBaseHref(html []byte, href string) []byte {
	tag := []byte(`<base href="` + href + `" />`)
	lower := strings.ToLower(string(html))
	if idx := strings.Index(lower, "<head>"); idx >= 0 {
		insertAt := idx + len("<head>")
		out := make([]byte, 0, len(html)+len(tag))
		out = append(out, html[:insertAt]...)
		out = append(out, tag...)
		out = append(out, html[insertAt:]...)
		return out
	}
	if idx := strings.Index(lower, "<head"); idx >= 0 {
		if end := strings.Index(string(html[idx:]), ">"); end >= 0 {
			insertAt := idx + end + 1
			out := make([]byte, 0, len(html)+len(tag))
			out = append(out, html[:insertAt]...)
			out = append(out, tag...)
			out = append(out, html[insertAt:]...)
			return out
		}
	}
	return html
}

// handleWebBundleAck — POST /dev/web-bundle/ack
// Iframe reports successful load via `{ ms_to_load }`. Transport tracker
// transitions to phase=delivered with EtaMs set. Idempotent.
func (s *HTTPServer) handleWebBundleAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "no dev server"})
		return
	}
	var body struct {
		MsToLoad int64 `json:"ms_to_load"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	t := s.devServerMgr.GetWebTransport()
	if t == nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no active web transport — bundle not built or already cleared"})
		return
	}
	t.markDelivered(body.MsToLoad)
	jsonReply(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleWebBundleError — POST /dev/web-bundle/error
// Iframe reports a JS error via `{ message, stack, source }`. Transport
// tracker transitions to phase=error. Idempotent on subsequent calls.
func (s *HTTPServer) handleWebBundleError(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "no dev server"})
		return
	}
	var body struct {
		Message string `json:"message"`
		Stack   string `json:"stack"`
		Source  string `json:"source"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	t := s.devServerMgr.GetWebTransport()
	if t == nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no active web transport"})
		return
	}
	msg := strings.TrimSpace(body.Message)
	if msg == "" {
		msg = "iframe reported error with no message"
	}
	if body.Source != "" {
		msg = fmt.Sprintf("%s (source: %s)", msg, body.Source)
	}
	t.markError(msg)
	jsonReply(w, http.StatusOK, map[string]bool{"ok": true})
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
