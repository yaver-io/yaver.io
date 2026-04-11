package main

// chat_widget.go — embeddable live-chat widget for the solo
// dev's landing page. Replaces Crisp / Intercom / Tawk.to.
//
// Two surfaces:
//
//   1. A tiny JavaScript snippet the dev drops into their
//      landing page. It posts messages to /chat/messages and
//      opens an SSE connection to /chat/stream for replies.
//
//   2. An owner-side inbox the dev watches from the mobile app
//      or a terminal. Messages append to a JSONL log per
//      conversation so nothing lives in RAM unbounded.
//
// Conversations are keyed by visitor ID — either a cookie the
// snippet sets on first load, or a query param for iframes.
//
// HTTP:
//
//   POST /chat/messages       public  — visitor send
//   GET  /chat/stream?vid=    public  — visitor SSE (listen for dev)
//   GET  /chat/conversations  owner   — list
//   GET  /chat/messages?vid=  owner   — history
//   POST /chat/reply          owner   — send reply
//   GET  /chat/widget.js      public  — the snippet itself

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ChatMessage struct {
	ID     string    `json:"id"`
	VID    string    `json:"vid"`  // visitor id
	From   string    `json:"from"` // "visitor" | "owner"
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
	Name   string    `json:"name,omitempty"`
	Email  string    `json:"email,omitempty"`
	Origin string    `json:"origin,omitempty"`
}

var (
	chatMu     sync.Mutex
	chatSubs   = map[string][]chan ChatMessage{} // vid → subscribers
)

func chatDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "chat")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func chatLogFile(vid string) (string, error) {
	dir, err := chatDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, vid+".jsonl"), nil
}

func appendChatMessage(m ChatMessage) error {
	p, _ := chatLogFile(m.VID)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(m)
}

func readChatMessages(vid string, limit int) ([]ChatMessage, error) {
	p, _ := chatLogFile(vid)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	out := make([]ChatMessage, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var m ChatMessage
		if err := json.Unmarshal([]byte(line), &m); err == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// publishChatMessage fans out to any active SSE listeners for
// the conversation.
func publishChatMessage(m ChatMessage) {
	chatMu.Lock()
	subs := chatSubs[m.VID]
	chatMu.Unlock()
	for _, c := range subs {
		select {
		case c <- m:
		default:
			// drop on slow subscriber — they'll refetch
		}
	}
}

// --- public visitor surface ------------------------------------------------

func (s *HTTPServer) handleChatMessageIngest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			VID    string `json:"vid"`
			Text   string `json:"text"`
			Name   string `json:"name,omitempty"`
			Email  string `json:"email,omitempty"`
			Origin string `json:"origin,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.VID == "" || body.Text == "" {
			jsonError(w, http.StatusBadRequest, "vid and text required")
			return
		}
		msg := ChatMessage{
			ID:     randomFormID(),
			VID:    sanitizeVID(body.VID),
			From:   "visitor",
			Text:   body.Text,
			At:     time.Now().UTC(),
			Name:   body.Name,
			Email:  body.Email,
			Origin: body.Origin,
		}
		_ = appendChatMessage(msg)
		publishChatMessage(msg)
		// Forward to the owner's inbox via the existing SMTP
		// relay so they don't miss conversations while offline.
		go chatNotifyOwner(msg)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case http.MethodGet:
		vid := r.URL.Query().Get("vid")
		if vid == "" {
			jsonError(w, http.StatusBadRequest, "vid required")
			return
		}
		msgs, _ := readChatMessages(sanitizeVID(vid), 100)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "messages": msgs})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST")
	}
}

// handleChatStream is the visitor-facing SSE endpoint. Owner
// replies posted via /chat/reply arrive here.
func (s *HTTPServer) handleChatStream(w http.ResponseWriter, r *http.Request) {
	vid := sanitizeVID(r.URL.Query().Get("vid"))
	if vid == "" {
		jsonError(w, http.StatusBadRequest, "vid required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan ChatMessage, 16)
	chatMu.Lock()
	chatSubs[vid] = append(chatSubs[vid], ch)
	chatMu.Unlock()
	defer func() {
		chatMu.Lock()
		subs := chatSubs[vid]
		for i, c := range subs {
			if c == ch {
				chatSubs[vid] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		chatMu.Unlock()
		close(ch)
	}()
	flusher.Flush()
	for {
		select {
		case m := <-ch:
			data, _ := json.Marshal(m)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// --- owner surface ---------------------------------------------------------

func (s *HTTPServer) handleChatConversations(w http.ResponseWriter, r *http.Request) {
	dir, err := chatDir()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries, _ := os.ReadDir(dir)
	out := []map[string]interface{}{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		vid := strings.TrimSuffix(e.Name(), ".jsonl")
		msgs, _ := readChatMessages(vid, 5)
		last := ""
		if len(msgs) > 0 {
			last = msgs[len(msgs)-1].Text
		}
		out = append(out, map[string]interface{}{
			"vid":     vid,
			"last":    last,
			"count":   len(msgs),
			"updated": func() interface{} {
				if len(msgs) > 0 {
					return msgs[len(msgs)-1].At
				}
				return nil
			}(),
		})
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "conversations": out})
}

func (s *HTTPServer) handleChatReply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		VID  string `json:"vid"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.VID == "" || body.Text == "" {
		jsonError(w, http.StatusBadRequest, "vid and text required")
		return
	}
	msg := ChatMessage{
		ID:   randomFormID(),
		VID:  sanitizeVID(body.VID),
		From: "owner",
		Text: body.Text,
		At:   time.Now().UTC(),
	}
	_ = appendChatMessage(msg)
	publishChatMessage(msg)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// --- widget.js snippet -----------------------------------------------------

// handleChatWidgetJS serves the drop-in JS the dev pastes into
// their landing page. Zero external deps, vanilla ES.
func (s *HTTPServer) handleChatWidgetJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	base := publicOauthBase(r)
	fmt.Fprintf(w, `(function(){
  var VID = localStorage.getItem("yaver_chat_vid");
  if (!VID) { VID = "v-" + Math.random().toString(36).slice(2); localStorage.setItem("yaver_chat_vid", VID); }
  var API = %q;
  var bubble = document.createElement("div");
  bubble.innerHTML = "\u{1F4AC}";
  bubble.style.cssText = "position:fixed;bottom:24px;right:24px;width:56px;height:56px;border-radius:9999px;background:#4F46E5;color:#fff;font-size:26px;display:grid;place-items:center;cursor:pointer;box-shadow:0 10px 30px rgba(0,0,0,.2);z-index:9999";
  bubble.onclick = function() { panel.style.display = panel.style.display === "none" ? "flex" : "none"; };
  document.body.appendChild(bubble);
  var panel = document.createElement("div");
  panel.style.cssText = "position:fixed;bottom:96px;right:24px;width:320px;height:420px;background:#fff;border:1px solid #e5e5e5;border-radius:12px;display:none;flex-direction:column;z-index:9999;font-family:system-ui;box-shadow:0 20px 60px rgba(0,0,0,.2);overflow:hidden";
  panel.innerHTML = '<div style="padding:12px;background:#4F46E5;color:#fff;font-weight:600">Chat with us</div><div id="yv-msgs" style="flex:1;overflow-y:auto;padding:12px;font-size:14px"></div><form id="yv-form" style="display:flex;padding:8px;border-top:1px solid #eee"><input id="yv-input" style="flex:1;padding:8px;border:1px solid #ddd;border-radius:6px" placeholder="Type…"><button style="margin-left:6px;padding:0 12px;background:#4F46E5;color:#fff;border:0;border-radius:6px">Send</button></form>';
  document.body.appendChild(panel);
  var msgs = panel.querySelector("#yv-msgs");
  function renderMsg(m) {
    var el = document.createElement("div");
    el.style.cssText = "margin:6px 0;padding:8px 10px;border-radius:8px;max-width:82%%;word-wrap:break-word" + (m.from === "owner" ? ";background:#eef;align-self:flex-start" : ";background:#e0ffe0;margin-left:auto");
    el.textContent = m.text;
    msgs.appendChild(el);
    msgs.scrollTop = msgs.scrollHeight;
  }
  fetch(API + "/chat/messages?vid=" + VID).then(r => r.json()).then(d => (d.messages||[]).forEach(renderMsg));
  var sse = new EventSource(API + "/chat/stream?vid=" + VID);
  sse.onmessage = function(e) { try { renderMsg(JSON.parse(e.data)); } catch(_){} };
  panel.querySelector("#yv-form").onsubmit = function(ev) {
    ev.preventDefault();
    var input = panel.querySelector("#yv-input");
    var text = input.value.trim();
    if (!text) return;
    input.value = "";
    fetch(API + "/chat/messages", { method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({ vid: VID, text: text, origin: location.href }) });
  };
})();
`, base)
}

// --- helpers ---------------------------------------------------------------

func sanitizeVID(v string) string {
	out := make([]byte, 0, len(v))
	for _, c := range v {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, byte(c))
		}
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return string(out)
}

// chatNotifyOwner emails the owner when a new visitor message
// arrives. Best-effort — failures are silent.
func chatNotifyOwner(m ChatMessage) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.Email == nil || cfg.Email.SenderEmail == "" {
		return
	}
	body := fmt.Sprintf("New chat from %s on %s:\n\n%s\n\nReply via /chat/reply or the mobile app.", m.VID, m.Origin, m.Text)
	_, _ = SendTransactionalEmail(SendEmailRequest{
		To:      []string{cfg.Email.SenderEmail},
		Subject: "[Chat] new message from " + m.VID,
		Body:    body,
	})
}
