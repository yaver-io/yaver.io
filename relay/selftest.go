package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// /admin/selftest — the relay probes ITS OWN tunnels and reports how many are
// actually delivering, for an EXTERNAL watchdog that cannot otherwise tell a
// healthy relay from one full of zombie tunnels (both answer /health with 200).
// See docs/adr/relay-watchdog-protocol.md.
//
// Security is the whole point of this endpoint — it is on the public internet and
// it introspects the fleet — so it fails closed at every step:
//
//   - Ed25519 signature over the body, verified against watchdog PUBLIC keys the
//     relay holds (YAVER_WATCHDOG_PUBKEYS). No shared secret; a stolen relay
//     config forges nothing.
//   - Timestamp within ±60s of now — bounds the replay window.
//   - Nonce unseen in that window — kills replay inside it.
//   - Body length capped BEFORE any crypto — an unauthenticated flood pays a
//     length check, not a curve operation.
//   - Returns COUNTS ONLY, never device ids — a compromised watchdog must not
//     become a fleet-enumeration oracle. The relay logs ids; that is where an
//     operator looks.
//
// With no pubkeys configured the endpoint returns 404, i.e. it does not exist —
// an un-provisioned relay must not expose an introspection surface at all.

const (
	selftestMaxBody   = 512
	selftestSkew      = 60 * time.Second
	selftestNonceTTL  = 10 * time.Minute
	selftestProbeConc = 8 // bound concurrent probes so selftest can't self-DoS
)

type selftestRequest struct {
	Nonce      string `json:"nonce"`
	IssuedAtMs int64  `json:"issuedAtMs"`
}

// watchdogPubKeys parses YAVER_WATCHDOG_PUBKEYS once — a comma-separated list of
// base64 Ed25519 public keys. Public keys are not secrets; env-sourced keeps
// them out of the (public) repo and lets an operator rotate without a rebuild.
var (
	watchdogKeysOnce sync.Once
	watchdogKeys     []ed25519.PublicKey
)

func loadWatchdogPubKeys() []ed25519.PublicKey {
	watchdogKeysOnce.Do(func() {
		watchdogKeys = parseWatchdogPubKeys(os.Getenv("YAVER_WATCHDOG_PUBKEYS"))
	})
	return watchdogKeys
}

// parseWatchdogPubKeys turns a comma-separated list of base64 Ed25519 public
// keys into keys, dropping any malformed entry rather than failing the whole
// set (one bad paste must not disable a working key). Split out so it is
// testable without the sync.Once.
func parseWatchdogPubKeys(raw string) []ed25519.PublicKey {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var keys []ed25519.PublicKey
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		der, err := base64.StdEncoding.DecodeString(part)
		if err != nil || len(der) != ed25519.PublicKeySize {
			continue
		}
		keys = append(keys, ed25519.PublicKey(der))
	}
	return keys
}

// nonceSeen is a tiny replay guard: a nonce is valid once within selftestNonceTTL.
type nonceGuard struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

var selftestNonces = &nonceGuard{seen: make(map[string]time.Time)}

// checkAndRecord returns false if the nonce was already used inside the TTL. It
// also opportunistically prunes, so the map cannot grow without bound under a
// flood (each entry lives at most selftestNonceTTL).
func (g *nonceGuard) checkAndRecord(nonce string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.seen) > 4096 {
		for k, t := range g.seen {
			if now.Sub(t) > selftestNonceTTL {
				delete(g.seen, k)
			}
		}
	}
	if t, ok := g.seen[nonce]; ok && now.Sub(t) < selftestNonceTTL {
		return false
	}
	g.seen[nonce] = now
	return true
}

func (s *RelayServer) handleAdminSelftest(w http.ResponseWriter, r *http.Request) {
	keys := loadWatchdogPubKeys()
	if len(keys) == 0 {
		// Not provisioned → the endpoint does not exist. No introspection surface
		// on a relay nobody has authorised to probe.
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		writeSelftestErr(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	// Cap the body BEFORE reading — signature verification on attacker-sized
	// input is itself a DoS.
	body := make([]byte, selftestMaxBody+1)
	n, _ := readFull(r.Body, body)
	if n > selftestMaxBody {
		writeSelftestErr(w, http.StatusBadRequest, "body too large")
		return
	}
	body = body[:n]

	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Yaver-Watchdog-Sig"))
	if err != nil || len(sig) != ed25519.SignatureSize {
		writeSelftestErr(w, http.StatusForbidden, "bad signature")
		return
	}
	verified := false
	for _, k := range keys {
		if ed25519.Verify(k, body, sig) {
			verified = true
			break
		}
	}
	if !verified {
		writeSelftestErr(w, http.StatusForbidden, "signature not authorised")
		return
	}

	var req selftestRequest
	if json.Unmarshal(body, &req) != nil || strings.TrimSpace(req.Nonce) == "" {
		writeSelftestErr(w, http.StatusBadRequest, "bad request")
		return
	}
	now := time.Now()
	skew := time.Duration(now.UnixMilli()-req.IssuedAtMs) * time.Millisecond
	if skew < 0 {
		skew = -skew
	}
	if skew > selftestSkew {
		writeSelftestErr(w, http.StatusUnauthorized, "stale or future-dated request")
		return
	}
	if !selftestNonces.checkAndRecord(req.Nonce, now) {
		writeSelftestErr(w, http.StatusUnauthorized, "nonce replay")
		return
	}

	total, delivering, zombies := s.probeAllTunnels()
	writeSelftestJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"tunnels":    total,
		"delivering": delivering,
		"zombies":    zombies,
	})
}

// probeAllTunnels probes every registered tunnel for delivery and returns
// counts only. Snapshots under the lock, then probes without holding it so a
// slow/zombie tunnel cannot block registration of others.
func (s *RelayServer) probeAllTunnels() (total, delivering, zombies int) {
	s.mu.RLock()
	snapshot := make([]*agentTunnel, 0, len(s.tunnels))
	for _, t := range s.tunnels {
		snapshot = append(snapshot, t)
	}
	s.mu.RUnlock()

	total = len(snapshot)
	if total == 0 {
		return 0, 0, 0
	}

	sem := make(chan struct{}, selftestProbeConc)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range snapshot {
		wg.Add(1)
		sem <- struct{}{}
		go func(t *agentTunnel) {
			defer wg.Done()
			defer func() { <-sem }()
			ok := s.tunnelDelivers(t)
			mu.Lock()
			if ok {
				delivering++
			} else {
				zombies++
			}
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	return total, delivering, zombies
}

// tunnelDelivers reports whether a tunnel can carry a request right now. QUIC
// tunnels reuse the existing probeTunnel (/health round-trip). A WS tunnel that
// is registered is treated as delivering here — its liveness is enforced by the
// websocket read loop, which tears the tunnel down the moment the socket dies,
// so a "registered but dead" WS tunnel cannot linger the way a zombie QUIC one
// can.
func (s *RelayServer) tunnelDelivers(t *agentTunnel) bool {
	if t == nil {
		return false
	}
	if t.ws != nil {
		return true
	}
	if t.conn == nil {
		return false
	}
	return s.probeTunnel(t) == nil
}

func writeSelftestJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSelftestErr(w http.ResponseWriter, status int, msg string) {
	writeSelftestJSON(w, status, map[string]any{"ok": false, "error": msg})
}

// readFull reads up to len(buf) bytes, returning how many were read. Unlike
// io.ReadFull it does not error on a short read — we want the actual length so
// the cap check above is exact.
func readFull(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
