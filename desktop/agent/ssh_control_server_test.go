package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// newTestSigner makes a throwaway ed25519 ssh.Signer for tests.
func newTestSigner(t *testing.T) (ssh.Signer, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return signer, sshPub
}

// startTestControlServer boots the embedded SSH control server on a loopback
// port, authorizing exactly authorizedPub, with a stub dispatch that echoes the
// routed call. Returns the addr + a cleanup.
func startTestControlServer(t *testing.T, authorizedPub ssh.PublicKey) (string, func()) {
	t.Helper()
	hostSigner, _ := newTestSigner(t)
	stub := func(method, path string, body []byte) ([]byte, int) {
		return []byte("DISPATCH " + method + " " + path), 0
	}
	isAuth := func(key ssh.PublicKey) bool {
		return bytes.Equal(ssh.MarshalAuthorizedKey(key), ssh.MarshalAuthorizedKey(authorizedPub))
	}
	srv := newSSHControlServer(hostSigner, isAuth, stub)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	return ln.Addr().String(), func() { ln.Close() }
}

func dialTest(t *testing.T, addr string, clientSigner ssh.Signer) (*ssh.Client, error) {
	t.Helper()
	cfg := &ssh.ClientConfig{
		User:            "yaver",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(clientSigner)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	return ssh.Dial("tcp", addr, cfg)
}

// The core closed loop: an authorized device key connects, sends a whitelisted
// verb over exec, and gets the routed dispatch back. This is a REAL SSH
// handshake + publickey auth + exec round-trip, the same path native-direct and
// reverse-SSH both terminate at.
func TestSSHControlServer_ClosedLoop_AuthorizedVerb(t *testing.T) {
	clientSigner, clientPub := newTestSigner(t)
	addr, cleanup := startTestControlServer(t, clientPub)
	defer cleanup()

	client, err := dialTest(t, addr, clientSigner)
	if err != nil {
		t.Fatalf("authorized key should connect: %v", err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	out, err := sess.Output(`{"verb":"health"}`)
	if err != nil {
		t.Fatalf("health exec failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "DISPATCH GET /health" {
		t.Fatalf("health routed wrong: got %q, want %q", got, "DISPATCH GET /health")
	}
}

// A task-scoped verb round-trips with the id substituted into the path.
func TestSSHControlServer_ClosedLoop_TaskScopedVerb(t *testing.T) {
	clientSigner, clientPub := newTestSigner(t)
	addr, cleanup := startTestControlServer(t, clientPub)
	defer cleanup()
	client, err := dialTest(t, addr, clientSigner)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sess, _ := client.NewSession()
	defer sess.Close()
	out, err := sess.Output(`{"verb":"stop-task","taskId":"task_abc123"}`)
	if err != nil {
		t.Fatalf("stop-task exec failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "DISPATCH POST /tasks/task_abc123/stop" {
		t.Fatalf("task-scoped routing wrong: got %q", got)
	}
}

// An UNAUTHORIZED key must be refused at the SSH handshake — never reaches a verb.
func TestSSHControlServer_RejectsUnauthorizedKey(t *testing.T) {
	_, authorizedPub := newTestSigner(t) // server authorizes THIS key
	attackerSigner, _ := newTestSigner(t) // attacker holds a DIFFERENT key
	addr, cleanup := startTestControlServer(t, authorizedPub)
	defer cleanup()
	if client, err := dialTest(t, addr, attackerSigner); err == nil {
		client.Close()
		t.Fatal("an unauthorized device key must NOT be able to connect")
	}
}

// A non-whitelisted verb and a shell request are both refused inside the cage.
func TestSSHControlServer_RefusesShellAndBadVerb(t *testing.T) {
	clientSigner, clientPub := newTestSigner(t)
	addr, cleanup := startTestControlServer(t, clientPub)
	defer cleanup()
	client, err := dialTest(t, addr, clientSigner)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Non-whitelisted verb → non-zero exit, no dispatch output.
	sess1, _ := client.NewSession()
	out, err := sess1.Output(`{"verb":"vault"}`)
	sess1.Close()
	if err == nil {
		t.Fatal("a non-whitelisted verb must fail (non-zero exit)")
	}
	if strings.Contains(string(out), "DISPATCH") {
		t.Fatal("a refused verb must never reach dispatch")
	}

	// Shell request → refused (forced-command cage, no interactive shell).
	sess2, _ := client.NewSession()
	err = sess2.Shell()
	sess2.Close()
	if err == nil {
		t.Fatal("shell must be refused on the out-of-band channel")
	}
}
