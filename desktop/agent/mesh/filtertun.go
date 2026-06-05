package mesh

// filtertun.go (Phase 4) — wraps the real TUN device so ACLs are enforced on the
// INBOUND path. wireguard-go calls Write to inject packets a peer sent us into
// the local stack; we drop packets the ACL forbids before they reach it. The
// matcher is swapped atomically so an ACL change on reconcile takes effect with
// no device restart and no lock on the hot path. A nil matcher = pass-through
// (zero overhead, the default until the user authors rules).

import (
	"sync/atomic"

	"golang.zx2c4.com/wireguard/tun"
)

type filterTUN struct {
	tun.Device
	matcher atomic.Pointer[Matcher]
}

func newFilterTUN(inner tun.Device) *filterTUN {
	return &filterTUN{Device: inner}
}

func (f *filterTUN) setMatcher(m *Matcher) { f.matcher.Store(m) }

// Write filters inbound packets. Dropped packets are silently discarded (that is
// what an ACL drop means); we still report the full batch as handled so
// wireguard-go does not retry. A nil/default-allow matcher passes everything.
func (f *filterTUN) Write(bufs [][]byte, offset int) (int, error) {
	m := f.matcher.Load()
	if m == nil {
		return f.Device.Write(bufs, offset)
	}
	keep := make([][]byte, 0, len(bufs))
	for _, b := range bufs {
		if offset <= len(b) && m.allowPacket(b[offset:]) {
			keep = append(keep, b)
		}
	}
	if len(keep) == 0 {
		return len(bufs), nil
	}
	if _, err := f.Device.Write(keep, offset); err != nil {
		return 0, err
	}
	return len(bufs), nil
}
