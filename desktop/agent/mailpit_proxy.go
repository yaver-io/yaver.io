package main

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// mailpit proxy — surfaces its JSON API at /mail/* so mobile can render
// caught dev emails natively (no iframe). Targets the local mailpit service
// at 127.0.0.1:8025 launched by the `mailpit` services preset.

const mailpitBase = "http://127.0.0.1:8025"

type mailpitMessage struct {
	ID      string   `json:"ID"`
	From    struct{ Address string `json:"Address"`; Name string `json:"Name"` } `json:"From"`
	To      []struct{ Address string `json:"Address"`; Name string `json:"Name"` } `json:"To"`
	Subject string   `json:"Subject"`
	Created string   `json:"Created"`
	Size    int64    `json:"Size"`
	Tags    []string `json:"Tags"`
	Read    bool     `json:"Read"`
}

func mailpitGet(path string) ([]byte, error) {
	c := &http.Client{Timeout: 5 * time.Second}
	res, err := c.Get(mailpitBase + path)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

func (s *HTTPServer) handleMailpitList(w http.ResponseWriter, r *http.Request) {
	data, err := mailpitGet("/api/v1/messages?limit=100")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "hint": "Is mailpit running? `yaver services start mailpit`"})
		return
	}
	var out struct {
		Total    int              `json:"total"`
		Unread   int              `json:"unread"`
		Messages []mailpitMessage `json:"messages"`
	}
	_ = json.Unmarshal(data, &out)
	writeJSON(w, http.StatusOK, out)
}

func (s *HTTPServer) handleMailpitMessage(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "id required")
		return
	}
	data, err := mailpitGet("/api/v1/message/" + id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *HTTPServer) handleMailpitDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct{ IDs []string `json:"ids"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	body, _ := json.Marshal(map[string]interface{}{"IDs": b.IDs})
	req, _ := http.NewRequest("DELETE", mailpitBase+"/api/v1/messages",
		newBuf(body))
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 5 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	defer res.Body.Close()
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": res.StatusCode < 400})
}

// small helper since we don't want to import bytes at file top for one call
func newBuf(b []byte) io.Reader {
	return &bufReader{b: b}
}

type bufReader struct{ b []byte; i int }

func (r *bufReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
