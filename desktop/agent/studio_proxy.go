package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// StudioTarget describes a native DB dashboard we can reverse-proxy.
type StudioTarget struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	URL     string `json:"url"`
	Probe   string `json:"probe"`
	Running bool   `json:"running"`
}

func studioTargets() []StudioTarget {
	return []StudioTarget{
		{ID: "drizzle", Label: "Drizzle Studio", URL: "http://127.0.0.1:4983", Probe: "http://127.0.0.1:4983"},
		{ID: "supabase", Label: "Supabase Studio", URL: "http://127.0.0.1:54323", Probe: "http://127.0.0.1:54323"},
		{ID: "convex", Label: "Convex Dashboard", URL: "http://127.0.0.1:6791", Probe: "http://127.0.0.1:6791"},
		{ID: "pocketbase", Label: "PocketBase Admin", URL: "http://127.0.0.1:8090/_/", Probe: "http://127.0.0.1:8090/api/health"},
		{ID: "minio", Label: "MinIO Console", URL: "http://127.0.0.1:9001", Probe: "http://127.0.0.1:9001"},
		{ID: "mailpit", Label: "Mailpit", URL: "http://127.0.0.1:8025", Probe: "http://127.0.0.1:8025"},
		{ID: "firebase", Label: "Firebase Emulator UI", URL: "http://127.0.0.1:4000", Probe: "http://127.0.0.1:4000"},
		{ID: "code-server", Label: "VS Code", URL: "http://127.0.0.1:8787", Probe: "http://127.0.0.1:8787"},
	}
}

func studioByID(id string) *StudioTarget {
	for _, t := range studioTargets() {
		if t.ID == id {
			return &t
		}
	}
	return nil
}

// probeStudio does a short HEAD/GET to see if the target is live.
func probeStudio(url string) bool {
	c := &http.Client{Timeout: 700 * time.Millisecond}
	res, err := c.Get(url)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode < 500
}

// mcpStudioList returns every known studio target with a live-probe result.
func mcpStudioList() interface{} {
	out := studioTargets()
	for i := range out {
		out[i].Running = probeStudio(out[i].Probe)
	}
	return map[string]interface{}{"studios": out}
}

// ---- HTTP handlers ----

func (s *HTTPServer) handleStudioList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpStudioList())
}

// handleStudioProxy reverse-proxies /proxy/{id}/* to the corresponding local
// studio dashboard. Path rewriting strips the /proxy/{id} prefix.
func (s *HTTPServer) handleStudioProxy(w http.ResponseWriter, r *http.Request) {
	// /proxy/{id}/rest/of/path
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/proxy/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing studio id", http.StatusBadRequest)
		return
	}
	target := studioByID(parts[0])
	if target == nil {
		http.Error(w, "unknown studio "+parts[0], http.StatusNotFound)
		return
	}
	upstream, err := url.Parse(target.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rest := ""
	if len(parts) == 2 {
		rest = "/" + parts[1]
	}
	r.URL.Path = singleJoin(upstream.Path, rest)
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.Host = upstream.Host
			q := req.URL.Query()
			q.Del("browser_session")
			req.URL.RawQuery = q.Encode()
			// strip our auth header — the upstream doesn't want it.
			req.Header.Del("Authorization")
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "studio unreachable at "+upstream.String()+": "+err.Error(), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

func singleJoin(a, b string) string {
	a = strings.TrimRight(a, "/")
	b = "/" + strings.TrimLeft(b, "/")
	if b == "/" {
		return a + "/"
	}
	return a + b
}
