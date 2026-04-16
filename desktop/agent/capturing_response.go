package main

// capturing_response.go — tiny in-memory http.ResponseWriter used by
// MCP-tool dispatchers that want to reuse an existing HTTP handler
// without going through the network. Captures the body bytes,
// status code, and headers so the caller can inspect them.

import "net/http"

type capturingResponseWriter struct {
	header http.Header
	status int
	body   []byte
}

func newCapturingResponseWriter() *capturingResponseWriter {
	return &capturingResponseWriter{header: http.Header{}, status: http.StatusOK}
}

func (w *capturingResponseWriter) Header() http.Header { return w.header }
func (w *capturingResponseWriter) WriteHeader(code int) { w.status = code }
func (w *capturingResponseWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return len(b), nil
}
func (w *capturingResponseWriter) Body() []byte { return w.body }
func (w *capturingResponseWriter) Status() int  { return w.status }
