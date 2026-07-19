package main

import (
	"errors"
	"net/http"
	"sync/atomic"
)

// errBandwidthBudgetExhausted aborts a stream that has consumed the device's
// remaining daily allowance mid-flight.
var errBandwidthBudgetExhausted = errors.New("bandwidth limit exceeded mid-stream")

// countingResponseWriter wraps an http.ResponseWriter to meter and CAP a
// streaming response in flight.
//
// TWO DISTINCT MONEY BUGS ARE FIXED HERE.
//
//  1. Streaming outbound was recorded as a literal zero ("treat streaming
//     outbound as best-effort"). BandwidthManager.CheckAllowed enforces a real
//     per-device daily cap (500 MB free / 20 GB paid, bandwidth.go:88-89) off
//     BytesIn+BytesOut, so a response path reporting 0 was invisible to it.
//     Continuous media — desktop streams, WebRTC/TURN fallback — is the most
//     expensive traffic the relay carries, and it was the traffic that counted
//     for nothing.
//
//  2. CheckAllowed runs ONCE, at request start (server.go:1678), against
//     ContentLength — which is 0 for a streaming GET. So even with correct
//     accounting, a SINGLE long-lived stream could never be stopped: it passed
//     the check at byte zero and then ran to the 15-minute tunnel timeout
//     unchallenged. Metering it after the fact only makes the NEXT request fail,
//     long after the money is spent.
//
// So this writer does two things the plain accounting could not:
//   - reports usage INCREMENTALLY (every reportEvery bytes) so concurrent
//     requests and the dashboard see a long stream while it is still running,
//     rather than in one lump at the end;
//   - enforces a byte BUDGET, failing the write once the device's remaining
//     allowance is gone. Returning an error unwinds readStreamingResponse and
//     tears the stream down.
//
// budget == 0 means unmetered (paid tiers where the caller opted out, or a
// device with no record yet). It is a deliberate opt-in: an unset budget must
// never silently mean "zero bytes allowed".
type countingResponseWriter struct {
	http.ResponseWriter

	n int64 // total bytes written

	// budget is the remaining allowance in bytes; 0 disables enforcement.
	budget int64
	// reported is how much of n has already been handed to the reporter, so
	// increments are never double-counted.
	reported int64
	// report receives byte deltas. Called from Write, so it must be cheap and
	// must not block on I/O.
	report func(delta int64)

	exhausted bool
}

// reportEvery bounds both the accounting lag and the lock traffic: at a typical
// 1.8 Mbps desktop stream this is roughly one update per second.
const reportEvery = 256 * 1024

func (c *countingResponseWriter) Write(b []byte) (int, error) {
	if c.exhausted {
		return 0, errBandwidthBudgetExhausted
	}
	n, err := c.ResponseWriter.Write(b)
	if n > 0 {
		total := atomic.AddInt64(&c.n, int64(n))
		if total-c.reported >= reportEvery {
			c.flushReport()
		}
		if c.budget > 0 && total >= c.budget {
			// Flush what was actually sent before giving up, so the final
			// over-limit bytes are still billed rather than written off.
			c.flushReport()
			c.exhausted = true
			return n, errBandwidthBudgetExhausted
		}
	}
	return n, err
}

// flushReport hands any not-yet-reported bytes to the reporter.
func (c *countingResponseWriter) flushReport() {
	total := atomic.LoadInt64(&c.n)
	delta := total - c.reported
	if delta <= 0 {
		return
	}
	c.reported = total
	if c.report != nil {
		c.report(delta)
	}
}

// Close finalizes accounting. Safe to call more than once; it reports only the
// remainder, so a stream that ended cleanly and one that was cut off are both
// billed for exactly the bytes that left the machine.
func (c *countingResponseWriter) Close() { c.flushReport() }

func (c *countingResponseWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// BytesWritten is safe to call after the handler returns.
func (c *countingResponseWriter) BytesWritten() int64 { return atomic.LoadInt64(&c.n) }

// Exhausted reports whether the stream was cut short by the budget.
func (c *countingResponseWriter) Exhausted() bool { return c.exhausted }
