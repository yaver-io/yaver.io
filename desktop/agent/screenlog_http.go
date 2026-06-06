package main

// screenlog_http.go — local HTTP surface for the screen-frame black box.
// Unlike clips (public share links), screenlog is PRIVATE: every route is
// behind s.auth, served straight off local disk, and nothing is ever
// mirrored to Convex/relay. The viewer is a same-origin frame grid.

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// writeSessionTarGz streams a session directory as a gzip-compressed tar
// into w. Used by GET /screenlog/<id>/export so a session can be pulled in
// one request.
func writeSessionTarGz(w io.Writer, dir, id string) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		hdr := &tar.Header{
			Name:    id + "/" + e.Name(),
			Mode:    0o600,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		f.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func (s *HTTPServer) handleScreenlogStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Title  string           `json:"title,omitempty"`
		Config *ScreenlogConfig `json:"config,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	cfg := defaultScreenlogConfig()
	if body.Config != nil {
		cfg = *body.Config
	}
	caller := screenlogCaller{
		Remote: !isLoopbackAddr(r.RemoteAddr),
		PeerID: r.Header.Get("X-Yaver-Peer"),
	}
	caller.Mesh = caller.PeerID != ""
	sess, err := startScreenlogGuarded(cfg, body.Title, caller)
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"session": sess,
		"viewUrl": "/screenlog/" + sess.ID,
	})
}

func (s *HTTPServer) handleScreenlogStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	sess, err := stopScreenlog()
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "session": sess})
}

func (s *HTTPServer) handleScreenlogStatus(w http.ResponseWriter, r *http.Request) {
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "status": screenlogStatus()})
}

func (s *HTTPServer) handleScreenlogDrivers(w http.ResponseWriter, r *http.Request) {
	cfg := defaultScreenlogConfig()
	if q := r.URL.Query().Get("displays"); q != "" {
		cfg.Displays = q
	}
	if q := r.URL.Query().Get("wslTarget"); q != "" {
		cfg.WSLTarget = q
	}
	info, _ := screenlogProbe(cfg)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "drivers": info})
}

func (s *HTTPServer) handleScreenlogList(w http.ResponseWriter, r *http.Request) {
	sessions, err := listScreenlogSessions()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Strip the (potentially large) frame arrays from the list view.
	type summary struct {
		ID        string `json:"id"`
		Title     string `json:"title,omitempty"`
		Host      string `json:"host,omitempty"`
		StartedAt int64  `json:"startedAt"`
		StoppedAt int64  `json:"stoppedAt,omitempty"`
		Frames    int    `json:"frames"`
	}
	out := make([]summary, 0, len(sessions))
	for _, se := range sessions {
		out = append(out, summary{se.ID, se.Title, se.Host, se.StartedAt, se.StoppedAt, len(se.Frames)})
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "sessions": out})
}

// handleScreenlogDetail serves three things off /screenlog/<id>...:
//
//	/screenlog/<id>             → HTML frame-grid viewer
//	/screenlog/<id>/frames.json → frame metadata (agent-readable)
//	/screenlog/<id>/<file>      → a single frame image
func (s *HTTPServer) handleScreenlogDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/screenlog/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		jsonError(w, http.StatusNotFound, "session id required")
		return
	}
	id := parts[0]
	sess, err := loadScreenlogSession(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "session not found")
		return
	}

	if len(parts) == 2 && parts[1] == "frames.json" {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "session": sess})
		return
	}

	// Input-event companion stream. POST ingests a batch (producer model —
	// works without the built-in agent loop), GET reads it back.
	if len(parts) == 2 && (parts[1] == "events" || parts[1] == "events.jsonl") {
		if r.Method == http.MethodPost {
			pol := loadScreenlogPolicy()
			if !pol.Enabled || !pol.AllowInputCapture {
				appendScreenlogAudit(screenlogAuditEntry{Action: "deny", Session: id, Note: "input capture not allowed"})
				jsonError(w, http.StatusForbidden, "input capture disabled — owner must `yaver screenlog allow-input`")
				return
			}
			var payload struct {
				Events []InputEvent `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			redact := !sess.Config.AllowRawText
			n, err := ingestInputEvents(id, payload.Events, redact)
			if err != nil {
				jsonError(w, http.StatusInternalServerError, err.Error())
				return
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "ingested": n, "redacted": redact})
			return
		}
		events, _ := readInputEvents(id, 0)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "events": events, "stats": inputStats(events)})
		return
	}

	// Bulk pull: stream the whole session dir (index + frames + events) as
	// a tar.gz so a client can download/archive it in one request.
	if len(parts) == 2 && parts[1] == "export" {
		dir, _ := screenlogSessionDir(id)
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", "attachment; filename="+id+".tar.gz")
		if err := writeSessionTarGz(w, dir, id); err != nil {
			// headers already sent; best-effort
			return
		}
		return
	}

	if len(parts) == 2 && parts[1] != "" {
		// Serve a frame file. Guard against path traversal.
		clean := filepath.Base(parts[1])
		dir, _ := screenlogSessionDir(id)
		full := filepath.Join(dir, clean)
		http.ServeFile(w, r, full)
		return
	}

	// HTML viewer — newest frames first.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var cards strings.Builder
	for i := len(sess.Frames) - 1; i >= 0; i-- {
		fr := sess.Frames[i]
		ts := time.UnixMilli(fr.CapturedAt).Format("15:04:05")
		label := ts
		if fr.ActiveApp != "" {
			label += " · " + fr.ActiveApp
		}
		cards.WriteString(fmt.Sprintf(
			`<figure style="margin:0"><img loading="lazy" src="/screenlog/%s/%s" style="width:100%%;border-radius:6px;border:1px solid #222"><figcaption style="font-size:11px;color:#888;padding:4px 0">%s</figcaption></figure>`,
			sess.ID, fr.File, htmlEscapeSimple(label)))
	}
	title := sess.Title
	if title == "" {
		title = sess.ID
	}
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title></head>
<body style="font-family:system-ui;background:#0b0b0c;color:#eee;margin:0;padding:24px">
<h1 style="font-size:18px">%s</h1>
<p style="color:#888;font-size:13px">%d frames · %s · local-only, never uploaded</p>
<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:14px">%s</div>
</body></html>`, htmlEscapeSimple(title), htmlEscapeSimple(title), len(sess.Frames), htmlEscapeSimple(sess.Host), cards.String())
}

func htmlEscapeSimple(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// handleScreenlogPolicy is GET (read) / POST (update) for the consent policy.
func (s *HTTPServer) handleScreenlogPolicy(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "policy": loadScreenlogPolicy()})
	case http.MethodPost:
		pol := loadScreenlogPolicy()
		var body struct {
			Enabled            *bool  `json:"enabled"`
			AllowRemoteControl *bool  `json:"allowRemoteControl"`
			RequireMeshGrant   *bool  `json:"requireMeshGrant"`
			NotifyOnStart      *bool  `json:"notifyOnStart"`
			AllowInputCapture  *bool  `json:"allowInputCapture"`
			AllowPeer          string `json:"allowPeer"`
			RevokePeer         string `json:"revokePeer"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Enabled != nil {
			pol.Enabled = *body.Enabled
		}
		if body.AllowRemoteControl != nil {
			pol.AllowRemoteControl = *body.AllowRemoteControl
		}
		if body.RequireMeshGrant != nil {
			pol.RequireMeshGrant = *body.RequireMeshGrant
		}
		if body.NotifyOnStart != nil {
			pol.NotifyOnStart = *body.NotifyOnStart
		}
		if body.AllowInputCapture != nil {
			pol.AllowInputCapture = *body.AllowInputCapture
		}
		if body.AllowPeer != "" && !peerAllowed(pol, body.AllowPeer) {
			pol.AllowedPeers = append(pol.AllowedPeers, body.AllowPeer)
		}
		if body.RevokePeer != "" {
			kept := pol.AllowedPeers[:0]
			for _, p := range pol.AllowedPeers {
				if p != body.RevokePeer {
					kept = append(kept, p)
				}
			}
			pol.AllowedPeers = kept
		}
		if err := saveScreenlogPolicy(pol); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		appendScreenlogAudit(screenlogAuditEntry{Action: "policy", Note: "updated via http"})
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "policy": pol})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleScreenlogAudit(w http.ResponseWriter, r *http.Request) {
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "audit": readScreenlogAudit(100)})
}

// handleScreenlogAnalyze runs the deterministic activity report for a
// session id (query ?id=, optional ?idle_gap_sec= / ?max_attribute_sec=).
func (s *HTTPServer) handleScreenlogAnalyze(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "id required")
		return
	}
	gap := atoiDefault(r.URL.Query().Get("idle_gap_sec"), 0)
	maxAttr := atoiDefault(r.URL.Query().Get("max_attribute_sec"), 0)
	rep, _, err := analyzeScreenlogSession(id, gap, maxAttr)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	top := ""
	if len(rep.ByCategory) > 0 {
		top = rep.ByCategory[0].Name
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok": true, "report": rep, "topActivity": top, "narrativePrompt": rep.NarrativePrompt(),
	})
}
