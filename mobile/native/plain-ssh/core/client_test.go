package plainssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestKeygenReturnsMatchingOpenSSHIdentity(t *testing.T) {
	var got struct {
		OK    bool                                                `json:"ok"`
		Value struct{ PublicKey, PrivateKey, Fingerprint string } `json:"value"`
	}
	if err := json.Unmarshal([]byte(Invoke(`{"op":"keygen"}`)), &got); err != nil || !got.OK {
		t.Fatalf("keygen response: %v %#v", err, got)
	}
	signer, err := ssh.ParsePrivateKey([]byte(got.Value.PrivateKey))
	if err != nil {
		t.Fatalf("private key: %v", err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(got.Value.PublicKey))
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), pub.Marshal()) {
		t.Fatal("public/private key mismatch")
	}
	if ssh.FingerprintSHA256(pub) != got.Value.Fingerprint {
		t.Fatal("fingerprint mismatch")
	}
}

// A real SSH handshake and real isolated tmux server exercise the exact native
// core, including authentication, raw streaming, targeting and detach survival.
func testServer(t *testing.T) (request, *atomic.Int32, func(...string) string) {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux required for integration test")
	}
	dir, err := os.MkdirTemp("/tmp", "yaver-ssh-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "tmux.sock")
	// No real user session or remote machine is involved.
	wrapper := "#!/bin/sh\nexec '" + bin + "' -S '" + socket + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yaver"), []byte("#!/bin/sh\nprintf 'Remote test account setup complete\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("-f", "/dev/null", "new-session", "-d", "-s", "work", "cat")
	t.Cleanup(func() { exec.Command(bin, "-S", socket, "kill-server").Run() })
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(key)
	var auths atomic.Int32
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		auths.Add(1)
		if meta.User() == "tester" && string(password) == "fixture-password" {
			return nil, nil
		}
		return nil, fmt.Errorf("denied")
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			nc, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				sc, channels, reqs, err := ssh.NewServerConn(nc, config)
				if err != nil {
					return
				}
				defer sc.Close()
				go func() {
					for r := range reqs {
						r.Reply(true, nil)
					}
				}()
				for incoming := range channels {
					ch, requests, err := incoming.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer ch.Close()
						for r := range requests {
							if r.Type != "exec" {
								r.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							if ssh.Unmarshal(r.Payload, &payload) != nil {
								return
							}
							// tmux 3.7 preserves literal backslash-t in -F. The wire
							// must contain actual tabs, independent of tmux version.
							if strings.Contains(payload.Command, "list-panes") && (!strings.Contains(payload.Command, "tmux -u list-panes") || strings.Contains(payload.Command, `\t`)) {
								t.Error("pane wire format must force UTF-8 and contain actual tabs (tmux 3.7 C-locale regression)")
							}
							cmd := exec.Command("/bin/sh", "-c", payload.Command)
							cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "LANG=C", "LC_ALL=C")
							cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
							stdin, _ := cmd.StdinPipe()
							if cmd.Start() != nil {
								r.Reply(false, nil)
								return
							}
							r.Reply(true, nil)
							go func() { io.Copy(stdin, ch); stdin.Close() }()
							err := cmd.Wait()
							status := uint32(0)
							if err != nil {
								status = 1
								if ee, ok := err.(*exec.ExitError); ok {
									status = uint32(ee.ExitCode())
								}
							}
							ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
							return
						}
					}()
				}
			}()
		}
	}()
	return request{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, User: "tester", Password: "fixture-password", Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}, &auths, run
}

func TestHostPinBeforeAuthentication(t *testing.T) {
	r, auths, _ := testServer(t)
	r.Op = "probe"
	value, err := dispatch(r)
	if err != nil || value.(map[string]string)["fingerprint"] != r.Fingerprint || auths.Load() != 0 {
		t.Fatalf("probe authenticated or wrong fingerprint: %v %v", value, err)
	}
	r.Op = "connect"
	r.Fingerprint = "SHA256:wrong"
	if _, err := dispatch(r); err == nil {
		t.Fatal("changed host key was accepted")
	}
	if auths.Load() != 0 {
		t.Fatal("credentials sent before host verification")
	}
	r.Fingerprint = ""
	if _, err := dispatch(r); err == nil {
		t.Fatal("empty pin accepted")
	}
}

func TestExactPaneAndDetach(t *testing.T) {
	r, _, tmux := testServer(t)
	first := tmux("display-message", "-p", "-t", "work", "#{pane_id}")
	second := tmux("split-window", "-h", "-t", "work", "-P", "-F", "#{pane_id}", "cat")
	r.Op = "connect"
	value, err := dispatch(r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID = value.(map[string]string)["id"]
	defer dispatch(request{Op: "close", ID: r.ID})
	r.Op = "panes"
	value, err = dispatch(r)
	if err != nil {
		t.Fatal(err)
	}
	panes := value.([]pane)
	if len(panes) != 2 {
		t.Fatalf("panes: %+v", panes)
	}
	r.Op = "open"
	r.Pane = first
	r.Session = panes[0].SessionID
	r.Identity = panes[0].Identity
	if _, err = dispatch(r); err != nil {
		t.Fatal(err)
	}
	tmux("select-pane", "-t", second)
	r.Op = "write"
	r.Data = base64.StdEncoding.EncodeToString([]byte("PHONE_EXACT_PANE\n"))
	if _, err = dispatch(r); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(output.String(), "PHONE_EXACT_PANE") {
		v, err := dispatch(request{Op: "read", ID: r.ID})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := base64.StdEncoding.DecodeString(v.(map[string]any)["data"].(string))
		output.Write(data)
	}
	if !strings.Contains(output.String(), "PHONE_EXACT_PANE") {
		t.Fatalf("raw output missing: %q", output.String())
	}
	if strings.Contains(tmux("capture-pane", "-p", "-t", second), "PHONE_EXACT_PANE") {
		t.Fatal("input leaked to desktop active pane")
	}
	if got := tmux("display-message", "-p", "-t", "work", "#{pane_id}"); got != second {
		t.Fatalf("desktop active pane changed: %s", got)
	}
	dispatch(request{Op: "close", ID: r.ID})
	if got := tmux("list-panes", "-t", "work", "-F", "#{pane_id}"); !strings.Contains(got, first) || !strings.Contains(got, second) {
		t.Fatal("disconnect destroyed pane")
	}
}

func TestControlBytesAndTargetValidation(t *testing.T) {
	if got := unescapeControl(`a\033[31m\134\015\012`); string(got) != "a\x1b[31m\\\r\n" {
		t.Fatalf("decode: %q", got)
	}
	for _, target := range []string{"work", "%1;kill-server", "%1\nsend-keys", ""} {
		if paneID.MatchString(target) {
			t.Fatalf("unsafe target accepted: %q", target)
		}
	}
	var result response
	json.Unmarshal([]byte(Invoke(`{"op":"read","id":"missing"}`)), &result)
	if result.OK || result.Error.Code != "SSH_DISCONNECTED" {
		t.Fatal("missing connection false green")
	}
}

// Opt-in fixture for the real RN-web loop; never binds outside loopback.
func TestBrowserFixture(t *testing.T) {
	path := os.Getenv("PLAIN_SSH_BROWSER_FIXTURE")
	if path == "" {
		t.Skip("browser fixture not requested")
	}
	r, _, _ := testServer(t)
	b, _ := json.Marshal(r)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path + ".done"); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("browser fixture timed out")
}

func TestStalePaneIdentity(t *testing.T) {
	r, _, _ := testServer(t)
	r.Op = "connect"
	value, err := dispatch(r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID = value.(map[string]string)["id"]
	defer dispatch(request{Op: "close", ID: r.ID})
	r.Op = "panes"
	value, err = dispatch(r)
	if err != nil {
		t.Fatal(err)
	}
	p := value.([]pane)[0]
	r.Op = "open"
	r.Pane = p.ID
	r.Session = p.SessionID
	r.Identity = p.Identity + "stale"
	if _, err = dispatch(r); err == nil {
		t.Fatal("stale pane identity accepted")
	}
}

func TestComposedInputUsesExistingPane(t *testing.T) {
	r, _, tmux := testServer(t)
	r.Op = "connect"
	value, err := dispatch(r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID = value.(map[string]string)["id"]
	defer dispatch(request{Op: "close", ID: r.ID})
	r.Op = "panes"
	value, err = dispatch(r)
	if err != nil {
		t.Fatal(err)
	}
	p := value.([]pane)[0]
	r.Op = "open"
	r.Pane = p.ID
	r.Session = p.SessionID
	r.Identity = p.Identity
	if _, err = dispatch(r); err != nil {
		t.Fatal(err)
	}
	r.Op = "submit"
	r.Data = base64.StdEncoding.EncodeToString([]byte("composed-pane-message"))
	if _, err = dispatch(r); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(tmux("capture-pane", "-p", "-t", p.ID), "composed-pane-message") >= 2 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("composed message was not submitted to the existing pane")
}

func TestEnrollmentDoesNotTypeIntoPane(t *testing.T) {
	r, _, tmux := testServer(t)
	r.Op = "connect"
	value, err := dispatch(r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID = value.(map[string]string)["id"]
	defer dispatch(request{Op: "close", ID: r.ID})
	r.Op = "enroll"
	if _, err = dispatch(r); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	output := ""
	for time.Now().Before(deadline) {
		r.Op = "enrollmentRead"
		value, err = dispatch(r)
		if err != nil {
			t.Fatal(err)
		}
		reply := value.(map[string]any)
		output += reply["output"].(string)
		if reply["done"].(bool) {
			if reply["error"] != "" {
				t.Fatal(reply)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(output, "Remote test account setup complete") {
		t.Fatal("setup did not stream output")
	}
	if strings.Contains(tmux("capture-pane", "-p", "-t", "work"), "Remote test account") {
		t.Fatal("enrollment used the runner pane")
	}
}

func TestInputRejectsRecycledPaneAfterAttachment(t *testing.T) {
	r, _, tmux := testServer(t)
	r.Op = "connect"
	v, err := dispatch(r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID = v.(map[string]string)["id"]
	defer dispatch(request{Op: "close", ID: r.ID})
	v, err = dispatch(request{Op: "panes", ID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	p := v.([]pane)[0]
	if _, err = dispatch(request{Op: "open", ID: r.ID, Pane: p.ID, Session: p.SessionID, Identity: p.Identity}); err != nil {
		t.Fatal(err)
	}
	tmux("kill-server")
	tmux("-f", "/dev/null", "new-session", "-d", "-s", "replacement", "cat")
	for _, op := range []string{"submit", "snapshot"} {
		_, err = dispatch(request{Op: op, ID: r.ID, Data: base64.StdEncoding.EncodeToString([]byte("must-not-arrive"))})
		var named codedError
		if !errors.As(err, &named) || named.code != "TMUX_PANE_GONE" {
			t.Fatalf("%s did not reject replacement pane: %v", op, err)
		}
	}
	if strings.Contains(tmux("capture-pane", "-p", "-t", "replacement"), "must-not-arrive") {
		t.Fatal("input reached replacement pane")
	}
}
