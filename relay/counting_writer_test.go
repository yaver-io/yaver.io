package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Real HTTP server, no mocks — house convention.
func TestCountingResponseWriterCountsExactly(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 4096)
	var counted int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := &countingResponseWriter{ResponseWriter: w}
		for i := 0; i < 3; i++ {
			if _, err := cw.Write(payload); err != nil {
				t.Errorf("write: %v", err)
			}
			cw.Flush()
		}
		counted = cw.BytesWritten()
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	want := int64(len(payload) * 3)
	if counted != want {
		t.Errorf("counted %d bytes, want %d", counted, want)
	}
	// The counter must match what the client actually received — if these
	// diverge, the meter is billing for something the user never got (or
	// worse, missing bytes we did pay to ship).
	if int64(len(body)) != want {
		t.Errorf("client received %d bytes, want %d", len(body), want)
	}
}

// Flush must reach the underlying writer. A wrapper that swallowed it would
// buffer the whole response and silently break live media — the exact traffic
// this counter was added to meter.
func TestCountingResponseWriterForwardsFlush(t *testing.T) {
	flushed := make(chan struct{}, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := &countingResponseWriter{ResponseWriter: w}
		cw.Write([]byte("first-chunk"))
		cw.Flush()
		flushed <- struct{}{}
		<-r.Context().Done()
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	<-flushed
	buf := make([]byte, len("first-chunk"))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("client did not receive the flushed chunk: %v", err)
	}
	if string(buf) != "first-chunk" {
		t.Errorf("got %q, want %q", buf, "first-chunk")
	}
}

// THE CORE MONEY GUARANTEE: a single long-lived stream must be cut when the
// device exhausts its allowance. Before this, CheckAllowed ran once against a
// ContentLength of 0 and the stream then ran unmetered to the tunnel timeout.
func TestCountingResponseWriterBudgetCutsStream(t *testing.T) {
	const budget = 8 * 1024
	var wrote int64
	var cutErr error

	rec := httptest.NewRecorder()
	cw := &countingResponseWriter{ResponseWriter: rec, budget: budget}

	chunk := bytes.Repeat([]byte("z"), 1024)
	for i := 0; i < 100; i++ {
		n, err := cw.Write(chunk)
		wrote += int64(n)
		if err != nil {
			cutErr = err
			break
		}
	}

	if cutErr == nil {
		t.Fatal("stream was never cut — budget not enforced")
	}
	if !errors.Is(cutErr, errBandwidthBudgetExhausted) {
		t.Errorf("cut with %v, want errBandwidthBudgetExhausted", cutErr)
	}
	if !cw.Exhausted() {
		t.Error("Exhausted() should report the cut")
	}
	// It must stop promptly, not after another megabyte.
	if wrote > budget+int64(len(chunk)) {
		t.Errorf("wrote %d bytes past a %d budget", wrote, budget)
	}
	// Further writes stay refused rather than resuming.
	if _, err := cw.Write(chunk); !errors.Is(err, errBandwidthBudgetExhausted) {
		t.Errorf("post-cut write returned %v, want refusal", err)
	}
}

// budget == 0 means UNMETERED, never "zero bytes allowed". A device with no
// record yet must not be cut off — that would break first use for everyone.
func TestCountingResponseWriterZeroBudgetIsUnmetered(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := &countingResponseWriter{ResponseWriter: rec, budget: 0}
	for i := 0; i < 64; i++ {
		if _, err := cw.Write(bytes.Repeat([]byte("q"), 4096)); err != nil {
			t.Fatalf("unmetered writer refused at iteration %d: %v", i, err)
		}
	}
	if cw.Exhausted() {
		t.Error("unmetered writer must never report exhausted")
	}
}

// A long stream must be visible to concurrent requests WHILE it runs, not only
// when it ends — otherwise two parallel streams each see a stale zero and both
// blow the cap.
func TestCountingResponseWriterReportsIncrementally(t *testing.T) {
	var reported int64
	rec := httptest.NewRecorder()
	cw := &countingResponseWriter{
		ResponseWriter: rec,
		report:         func(d int64) { atomic.AddInt64(&reported, d) },
	}

	chunk := bytes.Repeat([]byte("r"), 64*1024)
	for i := 0; i < 8; i++ { // 512 KiB total, > reportEvery
		if _, err := cw.Write(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if atomic.LoadInt64(&reported) == 0 {
		t.Fatal("nothing reported mid-stream — a long stream would stay invisible")
	}
	if got := atomic.LoadInt64(&reported); got > cw.BytesWritten() {
		t.Errorf("reported %d > written %d (double counting)", got, cw.BytesWritten())
	}

	cw.Close()
	if got, want := atomic.LoadInt64(&reported), cw.BytesWritten(); got != want {
		t.Errorf("after Close reported %d, want %d — the remainder was lost", got, want)
	}
}

// Close must be idempotent: the proxy path calls it, and a later call must not
// re-bill bytes that were already reported.
func TestCountingResponseWriterCloseIdempotent(t *testing.T) {
	var reported int64
	rec := httptest.NewRecorder()
	cw := &countingResponseWriter{
		ResponseWriter: rec,
		report:         func(d int64) { atomic.AddInt64(&reported, d) },
	}
	cw.Write(bytes.Repeat([]byte("k"), 1000))
	cw.Close()
	first := atomic.LoadInt64(&reported)
	cw.Close()
	cw.Close()
	if got := atomic.LoadInt64(&reported); got != first {
		t.Errorf("repeated Close re-billed: %d then %d", first, got)
	}
	if first != 1000 {
		t.Errorf("reported %d, want 1000", first)
	}
}

// Bytes shipped before a cut still cost real money and must be billed.
func TestCountingResponseWriterBillsBytesSentBeforeCut(t *testing.T) {
	var reported int64
	rec := httptest.NewRecorder()
	cw := &countingResponseWriter{
		ResponseWriter: rec,
		budget:         4096,
		report:         func(d int64) { atomic.AddInt64(&reported, d) },
	}
	for i := 0; i < 50; i++ {
		if _, err := cw.Write(bytes.Repeat([]byte("m"), 1024)); err != nil {
			break
		}
	}
	cw.Close()
	if atomic.LoadInt64(&reported) != cw.BytesWritten() {
		t.Errorf("billed %d but shipped %d — an aborted transfer must not be free",
			atomic.LoadInt64(&reported), cw.BytesWritten())
	}
	if cw.BytesWritten() == 0 {
		t.Error("expected some bytes to have been shipped before the cut")
	}
}

func TestCountingResponseWriterZeroWhenUnused(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := &countingResponseWriter{ResponseWriter: rec}
	if got := cw.BytesWritten(); got != 0 {
		t.Errorf("fresh writer counted %d, want 0", got)
	}
}
