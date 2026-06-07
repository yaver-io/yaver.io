package main

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/yaver-io/agent/netcapture"
)

// HTTP surface for the wire-observe & deep-analysis layer. Routes are registered
// in httpserver.go next to /streams. Live decoded events fan out over the
// existing /streams/netcapture:<session> SSE channel (no new streaming code) —
// web + mobile already consume it via streamLog().

// ensureNetcapture lazily constructs the engine; non-netcapture agents pay
// nothing. Mirrors ensureMachine.
func (s *HTTPServer) ensureNetcapture() (*netcapture.Engine, error) {
	s.netcaptureOnce.Do(func() {
		s.netcaptureEngine, s.netcaptureErr = netcapture.New()
	})
	return s.netcaptureEngine, s.netcaptureErr
}

// netcaptureGate enforces the opt-in flag and resolves the engine.
func (s *HTTPServer) netcaptureGate(w http.ResponseWriter) (*netcapture.Engine, bool) {
	if !s.netcaptureEnabled {
		jsonError(w, http.StatusForbidden, "netcapture is disabled on this agent; start it with `yaver serve --netcapture`")
		return nil, false
	}
	eng, err := s.ensureNetcapture()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "netcapture engine: "+err.Error())
		return nil, false
	}
	return eng, true
}

// eventToStreamMap renders a decoded event as a stream event the UIs render by
// `type`.
func eventToStreamMap(ev netcapture.Event) map[string]interface{} {
	m := map[string]interface{}{"type": "netcapture", "ts": ev.TS, "proto": ev.Proto, "summary": ev.Summary}
	if ev.Src != "" {
		m["src"] = ev.Src
	}
	if ev.Dst != "" {
		m["dst"] = ev.Dst
	}
	if ev.Severity != "" {
		m["severity"] = ev.Severity
	}
	if ev.Detail != nil {
		m["detail"] = ev.Detail
	}
	return m
}

// ncPublisher returns an OnEvent callback plus a holder for the session id (the
// id is only known after Start returns, but capture goroutines start spawning
// tcpdump first, so the id is set well before the first event fires).
func (s *HTTPServer) ncPublisher() (func(netcapture.Event), *string) {
	holder := new(string)
	fn := func(ev netcapture.Event) {
		id := *holder
		if id == "" || s.streams == nil {
			return
		}
		s.streams.Get("netcapture:" + id).AppendEvent(eventToStreamMap(ev))
	}
	return fn, holder
}

// startNetcapture is the shared start path used by both the HTTP route and the
// ops verb. kind is "net" (default) or "serial".
func (s *HTTPServer) startNetcapture(kind, iface, filter, device, decoder string, baud int, capturePayload bool) (string, error) {
	eng, err := s.ensureNetcapture()
	if err != nil {
		return "", err
	}
	onEv, holder := s.ncPublisher()
	var id string
	if kind == "serial" {
		id, err = eng.StartSerial(netcapture.SerialOptions{Device: device, Baud: baud, Decoder: decoder, OnEvent: onEv})
	} else {
		id, err = eng.StartNet(netcapture.NetOptions{Iface: iface, Filter: filter, CapturePayload: capturePayload, OnEvent: onEv})
	}
	if id != "" {
		*holder = id
	}
	return id, err
}

func (s *HTTPServer) handleNetcaptureStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if _, ok := s.netcaptureGate(w); !ok {
		return
	}
	var req struct {
		Kind           string `json:"kind"`
		Iface          string `json:"iface"`
		Filter         string `json:"filter"`
		Device         string `json:"device"`
		Decoder        string `json:"decoder"`
		Baud           int    `json:"baud"`
		CapturePayload bool   `json:"capturePayload"`
	}
	_ = decodeJSONBody(r, &req)
	id, err := s.startNetcapture(req.Kind, req.Iface, req.Filter, req.Device, req.Decoder, req.Baud, req.CapturePayload)
	resp := map[string]interface{}{"ok": err == nil || id != "", "session": id, "stream": "netcapture:" + id}
	if err != nil {
		resp["warning"] = err.Error() // e.g. serial tty unsupported off Linux — manual Feed still works
	}
	jsonReply(w, http.StatusOK, resp)
}

func (s *HTTPServer) handleNetcaptureStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	eng, ok := s.netcaptureGate(w)
	if !ok {
		return
	}
	var req struct {
		Session string `json:"session"`
	}
	_ = decodeJSONBody(r, &req)
	an, found := eng.Stop(req.Session)
	if !found {
		jsonError(w, http.StatusNotFound, "unknown session")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "analysis": an})
}

func (s *HTTPServer) handleNetcaptureStatus(w http.ResponseWriter, r *http.Request) {
	eng, ok := s.netcaptureGate(w)
	if !ok {
		return
	}
	if sess := r.URL.Query().Get("session"); sess != "" {
		an, found := eng.Analyze(sess, 25)
		if !found {
			jsonError(w, http.StatusNotFound, "unknown session")
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "analysis": an})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "sessions": eng.Sessions()})
}

func (s *HTTPServer) handleNetcaptureAnalysis(w http.ResponseWriter, r *http.Request) {
	eng, ok := s.netcaptureGate(w)
	if !ok {
		return
	}
	sess := r.URL.Query().Get("session")
	top := 25
	if t := r.URL.Query().Get("top"); t != "" {
		if n, err := strconv.Atoi(t); err == nil {
			top = n
		}
	}
	an, found := eng.Analyze(sess, top)
	if !found {
		jsonError(w, http.StatusNotFound, "unknown session")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "analysis": an})
}

func (s *HTTPServer) handleNetcaptureFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	eng, ok := s.netcaptureGate(w)
	if !ok {
		return
	}
	var req struct {
		Session string `json:"session"`
		Hex     string `json:"hex"`
	}
	_ = decodeJSONBody(r, &req)
	raw, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(req.Hex), " ", ""))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid hex: "+err.Error())
		return
	}
	if err := eng.Feed(req.Session, raw); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "fed": len(raw)})
}

func (s *HTTPServer) handleNetcaptureDownload(w http.ResponseWriter, r *http.Request) {
	eng, ok := s.netcaptureGate(w)
	if !ok {
		return
	}
	path, found := eng.PcapPath(r.URL.Query().Get("session"))
	if !found {
		jsonError(w, http.StatusNotFound, "no pcap for session")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	http.ServeFile(w, r, path)
}
