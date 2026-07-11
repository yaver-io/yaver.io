package main

// sigauth.go — verify per-device ed25519 request signatures so the relay can
// authenticate a client holding ONLY the device's public key. No shared secret
// on the relay: breaching the (open-source, self-hostable) relay yields nothing
// reusable. This is the relay half of docs/yaver-relay-asymmetric-auth.md.
//
// IMPORTANT: canonicalSigString MUST stay byte-for-byte identical to
// desktop/agent/device_sign_key.go::canonicalRelaySigString — they are the two
// halves of one wire contract. Change them together.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sigMaxSkew bounds how far a signed request's timestamp may be from now, in
// either direction — the replay window.
const sigMaxSkew = 60 * time.Second

// canonicalSigString is the exact bytes a relay request is signed over.
func canonicalSigString(method, path, deviceID, ts, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{method, path, deviceID, ts, nonce, hex.EncodeToString(sum[:])}, "\n")
}

// sigNonceCache rejects replays of a (deviceId, nonce) seen within the window.
// Bounded: entries older than 2×window are swept opportunistically.
type sigNonceCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newSigNonceCache() *sigNonceCache {
	return &sigNonceCache{seen: make(map[string]time.Time)}
}

// remember records key and returns true if it was NOT seen before (fresh).
// A nonce that repeats within the window returns false → reject the request.
func (c *sigNonceCache) remember(key string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, t := range c.seen {
		if now.Sub(t) > 2*sigMaxSkew {
			delete(c.seen, k)
		}
	}
	if _, ok := c.seen[key]; ok {
		return false
	}
	c.seen[key] = now
	return true
}

// hasDeviceSig reports whether a request even claims to be signature-authed, so
// callers can cheaply decide whether to attempt sig verification before falling
// back to the password path. Uses a dedicated header, NOT Authorization — the
// Bearer token there authenticates the end agent and must ride through.
func hasDeviceSig(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-Yaver-Sig")) != ""
}

// verifyDeviceSig verifies a Yaver-Sig request against the device's ed25519
// public key. `body` is the already-read request body (nil for none). On
// success it returns the claimed deviceId and true; the CALLER is responsible
// for confirming that deviceId is owned by the acting user (ownership is a
// Convex concern, not a crypto one). Fails closed on any malformed input.
//
// A non-nil nonces enforces replay protection; pass nil only where a caller has
// its own idempotency (e.g. a read that can't cause harm on replay).
func verifyDeviceSig(r *http.Request, body []byte, pubKey ed25519.PublicKey, nonces *sigNonceCache) (string, bool) {
	if !hasDeviceSig(r) {
		return "", false
	}
	deviceID := strings.TrimSpace(r.Header.Get("X-Yaver-Device"))
	tsStr := strings.TrimSpace(r.Header.Get("X-Yaver-Timestamp"))
	nonce := strings.TrimSpace(r.Header.Get("X-Yaver-Nonce"))
	sigB64 := strings.TrimSpace(r.Header.Get("X-Yaver-Signature"))
	if deviceID == "" || tsStr == "" || sigB64 == "" {
		return "", false
	}
	if len(pubKey) != ed25519.PublicKeySize {
		return "", false
	}
	tsMs, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return "", false
	}
	// Timestamp must be within the skew window in either direction.
	if d := time.Since(time.UnixMilli(tsMs)); d > sigMaxSkew || d < -sigMaxSkew {
		return "", false
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return "", false
	}
	canonical := canonicalSigString(r.Method, r.URL.Path, deviceID, tsStr, nonce, body)
	if !ed25519.Verify(pubKey, []byte(canonical), sig) {
		return "", false
	}
	// Replay: a valid signature is a bearer artifact until it expires. Reject a
	// repeated (deviceId, nonce) within the window.
	if nonces != nil && !nonces.remember(deviceID+"\x00"+nonce, time.Now()) {
		return "", false
	}
	return deviceID, true
}

// decodeSignPubKey parses a base64 ed25519 public key (as published to Convex).
// Returns nil on malformed input so verifyDeviceSig fails closed.
func decodeSignPubKey(b64 string) ed25519.PublicKey {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(raw)
}

// sigDeviceMatches is a constant-time check that a signed-in deviceId equals the
// one a request is routed to — used to close the authorized-vs-routed gap.
func sigDeviceMatches(signed, routed string) bool {
	return subtle.ConstantTimeCompare([]byte(signed), []byte(routed)) == 1
}
