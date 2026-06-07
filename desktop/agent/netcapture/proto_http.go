package netcapture

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// HTTP/1.x decoder. Request/response are paired per-connection FIFO (HTTP/1.1 is
// serialized on a connection) to derive latency. We read only the start line +
// Host header — never bodies.

func init() {
	registerProto(&protoDecoder{name: "http", ports: []int{80, 8080, 8000, 8081}, fn: decodeHTTP})
}

var httpMethods = []string{"GET ", "POST ", "PUT ", "DELETE ", "HEAD ", "OPTIONS ", "PATCH ", "TRACE ", "CONNECT "}

func decodeHTTP(p *packet, f *Flow, a *Analyzer) []Event {
	b := p.payload
	if len(b) < 14 {
		return nil
	}
	h := a.httpStats()
	key := "http|" + f.Key

	// response?
	if bytes.HasPrefix(b, []byte("HTTP/1.")) {
		line := firstLine(b)
		parts := strings.SplitN(line, " ", 3)
		status := ""
		if len(parts) >= 2 {
			status = parts[1]
		}
		h.Responses++
		h.ByStatus[status]++
		code, _ := strconv.Atoi(status)
		sev := "info"
		if code >= 400 {
			h.Errors++
			sev = "warn"
			if code >= 500 {
				sev = "error"
			}
		}
		lat, _ := a.takeReq(key, p.ts)
		return []Event{{
			TS: p.ts, Proto: "http", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: sev,
			Summary: fmt.Sprintf("HTTP %s (%.0fms)", strings.TrimPrefix(line, "HTTP/1.1 "), lat),
			Detail:  map[string]interface{}{"status": status, "latencyMs": lat},
		}}
	}

	// request?
	for _, m := range httpMethods {
		if bytes.HasPrefix(b, []byte(m)) {
			line := firstLine(b)
			parts := strings.SplitN(line, " ", 3)
			method, path := strings.TrimSpace(m), ""
			if len(parts) >= 2 {
				path = parts[1]
			}
			host := headerValue(b, "Host")
			h.Requests++
			h.ByMethod[method]++
			a.markReq(key, p.ts)
			return []Event{{
				TS: p.ts, Proto: "http", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info",
				Summary: fmt.Sprintf("HTTP %s %s%s", method, host, path),
				Detail:  map[string]interface{}{"method": method, "host": host, "path": path},
			}}
		}
	}
	return nil
}

func firstLine(b []byte) string {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return strings.TrimRight(string(b[:i]), "\r")
	}
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}

func headerValue(b []byte, name string) string {
	lower := name + ":"
	for _, raw := range bytes.Split(b, []byte("\n")) {
		ln := strings.TrimRight(string(raw), "\r")
		if len(ln) > len(lower) && strings.EqualFold(ln[:len(lower)], lower) {
			return strings.TrimSpace(ln[len(lower):])
		}
		if ln == "" {
			break // end of headers
		}
	}
	return ""
}
