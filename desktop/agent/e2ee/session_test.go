package e2ee

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func newIdentity(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func pairedKeys(t *testing.T) (SessionKeys, SessionKeys, Hello, Hello) {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	clientPub, clientPriv := newIdentity(t)
	agentPub, agentPriv := newIdentity(t)
	clientHello, clientEph, err := NewHello(clientPriv, "device-client", "opaque-session", now)
	if err != nil {
		t.Fatal(err)
	}
	agentHello, agentEph, err := NewHello(agentPriv, "device-agent", "opaque-session", now)
	if err != nil {
		t.Fatal(err)
	}
	clientKeys, err := DeriveSessionKeys(clientEph, clientHello, agentHello, agentPub, true, now)
	if err != nil {
		t.Fatal(err)
	}
	agentKeys, err := DeriveSessionKeys(agentEph, clientHello, agentHello, clientPub, false, now)
	if err != nil {
		t.Fatal(err)
	}
	return clientKeys, agentKeys, clientHello, agentHello
}

func TestAuthenticatedSessionRoundTripAndKeySeparation(t *testing.T) {
	client, agent, _, _ := pairedKeys(t)
	if client.Send != agent.Receive || client.Receive != agent.Send {
		t.Fatal("directional keys do not match peers")
	}
	if client.Send == client.Receive || client.Send == client.File || client.File == client.Control || client.Control == client.Rekey {
		t.Fatal("purpose-separated keys collided")
	}
	env, err := Encrypt(client.Send, "opaque-session", "device-client", 1, []byte("SECRET_MARKER_YAVER_91827"))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.Ciphertext; got == "" || got == "SECRET_MARKER_YAVER_91827" {
		t.Fatal("plaintext crossed envelope boundary")
	}
	plain, err := Decrypt(agent.Receive, env, "opaque-session", "device-client", &ReplayWindow{})
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "SECRET_MARKER_YAVER_91827" {
		t.Fatalf("plaintext mismatch: %q", plain)
	}
}

func TestTamperReplayAndMetadataSubstitutionRejected(t *testing.T) {
	client, agent, _, _ := pairedKeys(t)
	env, err := Encrypt(client.Send, "opaque-session", "device-client", 7, []byte("git status"))
	if err != nil {
		t.Fatal(err)
	}
	w := &ReplayWindow{}
	if _, err := Decrypt(agent.Receive, env, "opaque-session", "device-client", w); err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(agent.Receive, env, "opaque-session", "device-client", w); err == nil {
		t.Fatal("replay accepted")
	}

	tampered := env
	raw, _ := base64.RawStdEncoding.DecodeString(tampered.Ciphertext)
	raw[0] ^= 1
	tampered.Ciphertext = base64.RawStdEncoding.EncodeToString(raw)
	tampered.Sequence = 8
	if _, err := Decrypt(agent.Receive, tampered, "opaque-session", "device-client", &ReplayWindow{}); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}

	substituted := env
	substituted.SenderDeviceID = "attacker-device"
	if _, err := Decrypt(agent.Receive, substituted, "opaque-session", "attacker-device", &ReplayWindow{}); err == nil {
		t.Fatal("metadata substitution accepted")
	}
}

func TestUntrustedOrExpiredHelloRejected(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	_, signer := newIdentity(t)
	wrongPub, _ := newIdentity(t)
	h, _, err := NewHello(signer, "device-a", "session-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyHello(h, wrongPub, "session-a", now); err == nil {
		t.Fatal("key substitution accepted")
	}
	pub := signer.Public().(ed25519.PublicKey)
	if err := VerifyHello(h, pub, "session-a", now.Add(11*time.Minute)); err == nil {
		t.Fatal("expired hello accepted")
	}
}
