// Package e2ee implements Yaver's endpoint-only session cryptography.
// Relay/control-plane processes must never import this package.
package e2ee

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	ProtocolVersion  = 1
	maxClockSkew     = 2 * time.Minute
	maxHelloLifetime = 10 * time.Minute
)

type Hello struct {
	Version            int    `json:"version"`
	SessionID          string `json:"sessionId"`
	DeviceID           string `json:"deviceId"`
	EphemeralPublicKey string `json:"ephemeralPublicKey"`
	IssuedAtMS         int64  `json:"issuedAtMs"`
	ExpiresAtMS        int64  `json:"expiresAtMs"`
	Signature          string `json:"signature"`
}

type SessionKeys struct {
	Send    [32]byte
	Receive [32]byte
	File    [32]byte
	Control [32]byte
	Rekey   [32]byte
}

type Envelope struct {
	Version        int    `json:"version"`
	SessionID      string `json:"sessionId"`
	SenderDeviceID string `json:"senderDeviceId"`
	Sequence       uint64 `json:"sequence"`
	Nonce          string `json:"nonce"`
	Ciphertext     string `json:"ciphertext"`
}

func helloCanonical(h Hello) []byte {
	return []byte(fmt.Sprintf("yaver-e2ee-hello-v1\n%d\n%s\n%s\n%s\n%d\n%d",
		h.Version, h.SessionID, h.DeviceID, h.EphemeralPublicKey, h.IssuedAtMS, h.ExpiresAtMS))
}

func NewHello(signingKey ed25519.PrivateKey, deviceID, sessionID string, now time.Time) (Hello, *ecdh.PrivateKey, error) {
	if len(signingKey) != ed25519.PrivateKeySize || deviceID == "" || sessionID == "" {
		return Hello{}, nil, errors.New("invalid identity or opaque identifiers")
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Hello{}, nil, fmt.Errorf("generate ephemeral X25519 key: %w", err)
	}
	h := Hello{
		Version:            ProtocolVersion,
		SessionID:          sessionID,
		DeviceID:           deviceID,
		EphemeralPublicKey: base64.RawStdEncoding.EncodeToString(ephemeral.PublicKey().Bytes()),
		IssuedAtMS:         now.UnixMilli(),
		ExpiresAtMS:        now.Add(5 * time.Minute).UnixMilli(),
	}
	h.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(signingKey, helloCanonical(h)))
	return h, ephemeral, nil
}

func VerifyHello(h Hello, trustedSigningKey ed25519.PublicKey, expectedSessionID string, now time.Time) error {
	if h.Version != ProtocolVersion || h.SessionID == "" || h.SessionID != expectedSessionID || h.DeviceID == "" {
		return errors.New("invalid hello binding")
	}
	issued := time.UnixMilli(h.IssuedAtMS)
	expires := time.UnixMilli(h.ExpiresAtMS)
	if issued.After(now.Add(maxClockSkew)) || expires.Before(now) || expires.Sub(issued) > maxHelloLifetime {
		return errors.New("hello outside validity window")
	}
	pub, err := base64.RawStdEncoding.DecodeString(h.EphemeralPublicKey)
	if err != nil || len(pub) != 32 {
		return errors.New("invalid ephemeral public key")
	}
	sig, err := base64.RawStdEncoding.DecodeString(h.Signature)
	if err != nil || !ed25519.Verify(trustedSigningKey, helloCanonical(h), sig) {
		return errors.New("untrusted hello signature")
	}
	return nil
}

func transcript(clientHello, agentHello Hello) []byte {
	h := sha256.New()
	h.Write(helloCanonical(clientHello))
	h.Write([]byte{0})
	h.Write(helloCanonical(agentHello))
	return h.Sum(nil)
}

func derive(shared, salt []byte, label string) ([32]byte, error) {
	var out [32]byte
	r := hkdf.New(sha256.New, shared, salt, []byte("yaver-e2ee-v1/"+label))
	if _, err := io.ReadFull(r, out[:]); err != nil {
		return out, err
	}
	return out, nil
}

// DeriveSessionKeys authenticates both signed ephemeral hellos before ECDH.
// trustedPeerSigningKey must come from QR/existing-device authorization, never
// from an unverified control-plane key lookup.
func DeriveSessionKeys(
	localEphemeral *ecdh.PrivateKey,
	clientHello, agentHello Hello,
	trustedPeerSigningKey ed25519.PublicKey,
	localIsClient bool,
	now time.Time,
) (SessionKeys, error) {
	peerHello := clientHello
	if localIsClient {
		peerHello = agentHello
	}
	if err := VerifyHello(peerHello, trustedPeerSigningKey, clientHello.SessionID, now); err != nil {
		return SessionKeys{}, err
	}
	peerBytes, _ := base64.RawStdEncoding.DecodeString(peerHello.EphemeralPublicKey)
	peerKey, err := ecdh.X25519().NewPublicKey(peerBytes)
	if err != nil {
		return SessionKeys{}, errors.New("invalid peer agreement key")
	}
	shared, err := localEphemeral.ECDH(peerKey)
	if err != nil {
		return SessionKeys{}, errors.New("X25519 agreement failed")
	}
	salt := transcript(clientHello, agentHello)
	c2a, err := derive(shared, salt, "client-to-agent")
	if err != nil {
		return SessionKeys{}, err
	}
	a2c, err := derive(shared, salt, "agent-to-client")
	if err != nil {
		return SessionKeys{}, err
	}
	file, err := derive(shared, salt, "file")
	if err != nil {
		return SessionKeys{}, err
	}
	control, err := derive(shared, salt, "control")
	if err != nil {
		return SessionKeys{}, err
	}
	rekey, err := derive(shared, salt, "rekey")
	if err != nil {
		return SessionKeys{}, err
	}
	keys := SessionKeys{File: file, Control: control, Rekey: rekey}
	if localIsClient {
		keys.Send, keys.Receive = c2a, a2c
	} else {
		keys.Send, keys.Receive = a2c, c2a
	}
	return keys, nil
}

func envelopeAAD(version int, sessionID, senderDeviceID string, sequence uint64) []byte {
	b := make([]byte, 0, len(sessionID)+len(senderDeviceID)+32)
	b = append(b, []byte(fmt.Sprintf("yaver-e2ee-envelope-v1\n%d\n%s\n%s\n", version, sessionID, senderDeviceID))...)
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], sequence)
	return append(b, seq[:]...)
}

func Encrypt(key [32]byte, sessionID, senderDeviceID string, sequence uint64, plaintext []byte) (Envelope, error) {
	if sessionID == "" || senderDeviceID == "" || sequence == 0 {
		return Envelope{}, errors.New("invalid envelope metadata")
	}
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, err
	}
	aad := envelopeAAD(ProtocolVersion, sessionID, senderDeviceID, sequence)
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	return Envelope{
		Version: ProtocolVersion, SessionID: sessionID, SenderDeviceID: senderDeviceID, Sequence: sequence,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}, nil
}

type ReplayWindow struct {
	mu      sync.Mutex
	highest uint64
	seen    uint64
}

func (w *ReplayWindow) alreadySeen(sequence uint64) bool {
	if sequence == 0 {
		return true
	}
	if sequence > w.highest {
		return false
	}
	delta := w.highest - sequence
	return delta >= 64 || w.seen&(uint64(1)<<delta) != 0
}

func (w *ReplayWindow) mark(sequence uint64) {
	if sequence > w.highest {
		shift := sequence - w.highest
		if shift >= 64 {
			w.seen = 0
		} else {
			w.seen <<= shift
		}
		w.highest = sequence
		w.seen |= 1
		return
	}
	w.seen |= uint64(1) << (w.highest - sequence)
}

func Decrypt(key [32]byte, env Envelope, expectedSessionID, expectedSenderDeviceID string, replay *ReplayWindow) ([]byte, error) {
	if env.Version != ProtocolVersion || env.SessionID != expectedSessionID || env.SenderDeviceID != expectedSenderDeviceID {
		return nil, errors.New("envelope binding mismatch")
	}
	if replay == nil {
		return nil, errors.New("replay window required")
	}
	replay.mu.Lock()
	defer replay.mu.Unlock()
	if replay.alreadySeen(env.Sequence) {
		return nil, errors.New("replayed or stale envelope")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(env.Nonce)
	if err != nil || len(nonce) != chacha20poly1305.NonceSizeX {
		return nil, errors.New("invalid nonce")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid ciphertext")
	}
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, envelopeAAD(env.Version, env.SessionID, env.SenderDeviceID, env.Sequence))
	if err != nil {
		return nil, errors.New("envelope authentication failed")
	}
	replay.mark(env.Sequence)
	return plaintext, nil
}
