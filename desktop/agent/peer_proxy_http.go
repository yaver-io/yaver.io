package main

// peer_proxy_http.go — /peer/<deviceId>/<path> HTTP handler.
//
// Lets mobile + web clients call any agent endpoint on a paired peer
// machine by forwarding the request through proxyToDevice (which
// already handles relay routing, auth-header signing, and peer-auth
// via the per-user agent token). The main consumers today are:
//
//   GET  /peer/<id>/install/list        — what's installed on that peer
//   POST /peer/<id>/install/<tool>      — install a tool on that peer
//   GET  /peer/<id>/infra/summary       — CPU/RAM/disk/GPU on that peer
//
// But the route is intentionally generic — any endpoint registered on
// the target agent becomes reachable without teaching each callsite
// about relays.

import (
	"errors"
	"io"
	"net/http"
	"strings"
)

func (s *HTTPServer) handlePeerProxy(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/peer/")
	if rest == "" {
		jsonError(w, http.StatusBadRequest, "usage: /peer/<deviceId>/<path>")
		return
	}
	deviceID, tail, found := strings.Cut(rest, "/")
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		jsonError(w, http.StatusBadRequest, "missing deviceId in /peer/<deviceId>/<path>")
		return
	}
	path := "/"
	if found {
		path = "/" + tail
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}

	// proxyToDevice handles Bearer + X-Relay-Password + X-Yaver-Proxied-*
	// headers. We always POST the body verbatim if there is one; the
	// target agent inspects its own Method-check in the specific handler.
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(io.LimitReader(r.Body, 8*1024*1024))
		if err != nil {
			jsonError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}
	}

	status, resp, err := proxyToDevice(r.Context(), "peer-proxy", deviceID, r.Method, path, bodyBytes)
	if err != nil {
		// errProxyLocal means the deviceId resolved to this very machine —
		// reject rather than recurse. Clients should drop the /peer/<id>/
		// prefix in that case.
		if errors.Is(err, errProxyLocal) {
			jsonError(w, http.StatusBadRequest, "peer target is the local machine; call the endpoint directly")
			return
		}
		jsonError(w, http.StatusBadGateway, "peer proxy failed: "+err.Error())
		return
	}

	// Pass the target's body through verbatim. Content-Type is best-effort
	// JSON; downstream handlers mostly emit application/json already.
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(resp)
}
