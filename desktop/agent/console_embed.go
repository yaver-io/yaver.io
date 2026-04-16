package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Console SPA static bundle. Populated by the build pipeline
// (cd web && npm run build && cp -r out/* ../desktop/agent/console_static/).
// Until then, this directory contains only .keep — the handler falls back
// to a minimal HTML page pointing at yaver.io/dashboard.
//
//go:embed console_static
var consoleStaticFS embed.FS

// mountConsoleEmbed wires GET /app/* and GET /app to serve the embedded
// console SPA. Router config / deep links are handled by the SPA — non-file
// paths fall through to /app/index.html.
func (s *HTTPServer) mountConsoleEmbed(mux *http.ServeMux) {
	sub, err := fs.Sub(consoleStaticFS, "console_static")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc("/app/", func(w http.ResponseWriter, r *http.Request) {
		// Strip /app prefix before looking up the file.
		path := strings.TrimPrefix(r.URL.Path, "/app/")
		if path == "" || path == "index.html" {
			serveConsoleIndex(w, sub)
			return
		}
		// Try the exact file.
		if f, err := sub.Open(path); err == nil {
			f.Close()
			http.StripPrefix("/app", fileServer).ServeHTTP(w, r)
			return
		}
		// SPA fallback — any non-file route serves index.html.
		serveConsoleIndex(w, sub)
	})
	mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app/", http.StatusTemporaryRedirect)
	})
}

func serveConsoleIndex(w http.ResponseWriter, sub fs.FS) {
	if f, err := sub.Open("index.html"); err == nil {
		defer f.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fsCopy(w, f)
		return
	}
	// Fallback page when the SPA hasn't been built into the binary
	// yet. We keep it functional — support-session redeem + a tiny
	// exec form — so `yaver ui --local --code <CODE>` does something
	// useful even before `cd web && npm run build` has ever run.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(consoleFallbackHTML))
}

// consoleFallbackHTML is intentionally inline, zero deps, ~200 lines.
// Lets a host prove out the remote-support loop end-to-end without the
// SPA build. When ?support=CODE is present it redeems automatically.
const consoleFallbackHTML = `<!doctype html><html lang="en"><head>
<meta charset="utf-8"><title>Yaver Console</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
  :root{color-scheme:dark}
  body{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;background:#0b0d10;color:#d1d5db;margin:0;padding:1.5rem;max-width:820px;margin:0 auto}
  h1{font-size:1.25rem;margin:0 0 1rem}
  h2{font-size:1rem;margin:1.5rem 0 .5rem;color:#9ca3af;text-transform:uppercase;letter-spacing:.05em}
  a{color:#818cf8}
  button{background:#4338ca;color:#fff;border:0;padding:.5rem 1rem;font-family:inherit;cursor:pointer;border-radius:.25rem}
  button:disabled{opacity:.4;cursor:not-allowed}
  input{background:#111827;color:#d1d5db;border:1px solid #374151;padding:.5rem;font-family:inherit;border-radius:.25rem;width:100%;box-sizing:border-box}
  .row{display:flex;gap:.5rem;margin:.5rem 0}
  .row input{flex:1}
  pre{background:#111827;padding:.75rem;border-radius:.25rem;overflow-x:auto;white-space:pre-wrap;word-break:break-all;margin:.5rem 0;max-height:20rem;overflow-y:auto}
  .banner{background:#052e16;border:1px solid #166534;color:#bbf7d0;padding:.75rem;border-radius:.25rem;margin:1rem 0}
  .banner.err{background:#3f0a0a;border-color:#991b1b;color:#fecaca}
  .muted{color:#6b7280;font-size:.875rem}
  .hide{display:none}
</style>
</head><body>
<h1>Yaver Console <span class="muted" id="host"></span></h1>
<p class="muted">Minimal built-in control surface. The full SPA ships when <code>web/</code> is built and copied to <code>console_static/</code>; this page is what the agent always serves as a fallback.</p>

<div id="banner" class="banner hide"></div>

<h2>Support session</h2>
<div id="sess-none">
  <p class="muted">Paste a 6-char code from <code>yaver support start</code> to connect.</p>
  <div class="row">
    <input id="code-in" placeholder="ABCD23" maxlength="6" autocapitalize="characters" autocomplete="off">
    <button id="redeem-btn">Redeem</button>
  </div>
</div>
<div id="sess-active" class="hide">
  <p>Connected to <b id="sess-host"></b> — expires <span id="sess-exp"></span>.</p>
</div>

<h2>Run a command</h2>
<div class="row">
  <input id="cmd-in" placeholder="uname -a" autocomplete="off">
  <button id="run-btn" disabled>Run</button>
</div>
<pre id="out">(no output yet)</pre>

<h2>Files (read-only)</h2>
<div class="row">
  <button id="roots-btn" disabled>List project roots</button>
</div>
<pre id="files" class="hide"></pre>

<script>
(function(){
  var bearer = "";
  var banner = document.getElementById("banner");
  var out = document.getElementById("out");
  var runBtn = document.getElementById("run-btn");
  var rootsBtn = document.getElementById("roots-btn");
  var sessNone = document.getElementById("sess-none");
  var sessActive = document.getElementById("sess-active");

  function flash(msg, err){
    banner.textContent = msg;
    banner.className = "banner" + (err ? " err" : "");
  }

  function authed(bearer, path, opts){
    opts = opts || {};
    opts.headers = Object.assign({"Authorization":"Bearer "+bearer}, opts.headers || {});
    return fetch(path, opts);
  }

  function applySession(info){
    bearer = info.token;
    document.getElementById("sess-host").textContent = info.host || "(unknown)";
    document.getElementById("sess-exp").textContent = info.expiresAt || "";
    sessNone.classList.add("hide");
    sessActive.classList.remove("hide");
    runBtn.disabled = false;
    rootsBtn.disabled = false;
    flash("Connected — bearer kept in memory only, lost on page reload.");
  }

  async function redeem(code){
    try {
      var r = await fetch("/support/redeem", {
        method:"POST", headers:{"Content-Type":"application/json"},
        body: JSON.stringify({code: code})
      });
      var info = await r.json();
      if (!r.ok) throw new Error(info.error || ("HTTP "+r.status));
      applySession(info);
    } catch (e) {
      flash("Redeem failed: " + e.message, true);
    }
  }

  async function runCmd(cmd){
    if (!bearer || !cmd) return;
    out.textContent = "(running…)\n";
    try {
      var r = await authed(bearer, "/exec", {
        method:"POST", headers:{"Content-Type":"application/json"},
        body: JSON.stringify({command: cmd, timeout: 120})
      });
      var j = await r.json();
      if (!r.ok) { out.textContent = "error: " + (j.error || r.status); return; }
      var execId = j.execId;
      var seenOut = 0, seenErr = 0;
      out.textContent = "";
      var poll = setInterval(async function(){
        var rr = await authed(bearer, "/exec/"+execId);
        var jj = await rr.json();
        var sess = jj.exec || {};
        var so = sess.stdout || "", se = sess.stderr || "";
        if (so.length > seenOut) { out.textContent += so.slice(seenOut); seenOut = so.length; }
        if (se.length > seenErr) { out.textContent += se.slice(seenErr); seenErr = se.length; }
        if (sess.status === "completed" || sess.status === "failed") {
          clearInterval(poll);
          out.textContent += "\n[exit " + (sess.exitCode ?? "?") + "]";
        }
      }, 300);
    } catch (e) {
      out.textContent = "error: " + e.message;
    }
  }

  document.getElementById("redeem-btn").onclick = function(){
    var code = (document.getElementById("code-in").value || "").trim().toUpperCase();
    if (code) redeem(code);
  };
  runBtn.onclick = function(){ runCmd(document.getElementById("cmd-in").value.trim()); };
  document.getElementById("cmd-in").addEventListener("keydown", function(e){ if (e.key==="Enter") runBtn.click(); });
  rootsBtn.onclick = async function(){
    var box = document.getElementById("files");
    box.classList.remove("hide");
    box.textContent = "loading…";
    try {
      var r = await authed(bearer, "/files/roots");
      box.textContent = JSON.stringify(await r.json(), null, 2);
    } catch (e) { box.textContent = "error: " + e.message; }
  };

  // Show probe info (host, active?) on page load so a fresh visit sees
  // what the agent thinks is happening.
  fetch("/support/info").then(function(r){return r.json();}).then(function(info){
    if (info && info.host) document.getElementById("host").textContent = "— " + info.host;
    if (!info || !info.active) {
      flash("No support session is open on this agent. Run 'yaver support start' on the host.");
    }
  }).catch(function(){ /* agent may have rate-limited; ignore */ });

  // Auto-redeem from ?code= / ?support= / ?c=.
  var q = new URLSearchParams(location.search);
  var autoCode = (q.get("code") || q.get("support") || q.get("c") || "").trim().toUpperCase();
  if (autoCode) {
    document.getElementById("code-in").value = autoCode;
    redeem(autoCode);
  }
})();
</script>
</body></html>`

func fsCopy(w http.ResponseWriter, src fs.File) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			nw, werr := w.Write(buf[:n])
			total += int64(nw)
			if werr != nil {
				return total, werr
			}
		}
		if err != nil {
			if err.Error() == "EOF" {
				return total, nil
			}
			return total, err
		}
	}
}
