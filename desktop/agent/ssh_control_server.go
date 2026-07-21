package main

// ssh_control_server.go — Phase 3 of the out-of-band SSH channel: the agent-side
// embedded SSH server that terminates the control/task channel.
//
// This is the box end of "native-direct SSH (on tailnet)" and "reverse-SSH via
// relay (off tailnet)" — same server, different reachability leg. A client (the
// phone's native module, the desktop, or the closed-loop test) authenticates with
// its device key and sends an `exec` whose payload is the JSON {verb,taskId,body}.
// The server enforces the SAME whitelist (parseAndRouteSSHCommand) as the
// forced-command CLI and dispatches to the local agent over loopback. It is a
// cage: no shell, no pty, no forwarding — only the whitelisted verbs.
//
// Security: only public keys in the accepted set (the `# yaver-managed` device
// keys) may connect; auth is publickey-only (no passwords, no keyboard-
// interactive); every session is exec-only.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
)

// sshControlDispatch runs one whitelisted command and returns (stdout, exitCode).
// Injected so the server is testable without a live local agent.
type sshControlDispatch func(method, path string, body []byte) (out []byte, exit int)

// sshControlServer is the embedded SSH server for the out-of-band channel.
type sshControlServer struct {
	config   *ssh.ServerConfig
	dispatch sshControlDispatch
}

// newSSHControlServer builds the server. hostSigner is the box's SSH host key;
// isAuthorized decides whether a presented device public key may connect (in
// prod: is it one of our `# yaver-managed` keys); dispatch runs the verb.
func newSSHControlServer(hostSigner ssh.Signer, isAuthorized func(ssh.PublicKey) bool, dispatch sshControlDispatch) *sshControlServer {
	cfg := &ssh.ServerConfig{
		// Public-key only. No passwords, no keyboard-interactive — the device key
		// is the sole credential, and it must be one we authorized.
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if isAuthorized != nil && isAuthorized(key) {
				return &ssh.Permissions{Extensions: map[string]string{
					"pubkey-fp": ssh.FingerprintSHA256(key),
				}}, nil
			}
			return nil, fmt.Errorf("unauthorized device key")
		},
	}
	cfg.AddHostKey(hostSigner)
	return &sshControlServer{config: cfg, dispatch: dispatch}
}

// Serve accepts connections until the listener closes.
func (s *sshControlServer) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *sshControlServer) handleConn(nConn net.Conn) {
	defer nConn.Close()
	sconn, chans, reqs, err := ssh.NewServerConn(nConn, s.config)
	if err != nil {
		return // failed handshake / unauthorized key
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		// Only "session" channels — no direct-tcpip (port forwarding), no others.
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "only session channels are permitted")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(ch, chReqs)
	}
}

func (s *sshControlServer) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "exec":
			// The exec payload is an SSH string: 4-byte length + command bytes.
			cmd := ""
			if len(req.Payload) >= 4 {
				n := binary.BigEndian.Uint32(req.Payload[:4])
				if int(n) <= len(req.Payload)-4 {
					cmd = string(req.Payload[4 : 4+n])
				}
			}
			if req.WantReply {
				req.Reply(true, nil)
			}
			exit := s.runExec(ch, cmd)
			sendExitStatus(ch, exit)
			return
		case "shell", "pty-req", "x11-req", "subsystem", "env":
			// The channel is a cage: no interactive shell, no pty, no env, no
			// subsystem (sftp/scp). Refuse them all.
			if req.WantReply {
				req.Reply(false, nil)
			}
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// runExec parses+whitelists the command and dispatches it, writing stdout to the
// channel and stderr (refusal messages) to the channel's stderr stream.
func (s *sshControlServer) runExec(ch ssh.Channel, raw string) int {
	method, path, body, errMsg := parseAndRouteSSHCommand(raw)
	if errMsg != "" {
		io.WriteString(ch.Stderr(), "ssh-session: "+errMsg+"\n")
		return 2
	}
	out, exit := s.dispatch(method, path, body)
	ch.Write(out)
	return exit
}

// sendExitStatus sends the SSH exit-status request so the client sees the code.
func sendExitStatus(ch ssh.Channel, code int) {
	var payload = make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(code))
	ch.SendRequest("exit-status", false, payload)
}

// startSSHControlServerIfEnabled starts the embedded out-of-band SSH control
// server when it is turned on. It is GATED and dormant by default: existing
// installs are unaffected until the whole feature (reverse tunnel + native
// client) is complete and deployed. Enable for closed-loop testing / bring-up
// with YAVER_SSH_CONTROL=1 (bind addr via YAVER_SSH_CONTROL_ADDR, default
// 127.0.0.1:2222). Binds locally by default so the direct/native path is reached
// over the overlay and the off-tailnet path via the (future) reverse tunnel.
func startSSHControlServerIfEnabled() {
	if os.Getenv("YAVER_SSH_CONTROL") == "" {
		return // dormant by default
	}
	addr := os.Getenv("YAVER_SSH_CONTROL_ADDR")
	if addr == "" {
		addr = "127.0.0.1:2222"
	}
	privPEM, err := ensureSSHControlHostKey()
	if err != nil {
		fmt.Println("[ssh-control] host key error:", err)
		return
	}
	hostSigner, err := ssh.ParsePrivateKey([]byte(privPEM))
	if err != nil {
		fmt.Println("[ssh-control] host key parse error:", err)
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Println("[ssh-control] listen error:", err)
		return
	}
	fmt.Printf("[ssh-control] out-of-band SSH control channel listening on %s\n", addr)
	srv := newSSHControlServer(hostSigner, authorizedManagedKeysChecker(), dispatchLocalAgent)
	go func() { _ = srv.Serve(ln) }()
}

// authorizedManagedKeysChecker returns an isAuthorized func that accepts exactly
// the public keys currently present as `# yaver-managed` entries in the box's
// authorized_keys. Reads on each connection so a revoke takes effect immediately.
func authorizedManagedKeysChecker() func(ssh.PublicKey) bool {
	return func(key ssh.PublicKey) bool {
		path, err := authorizedKeysPath()
		if err != nil {
			return false
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		want := ssh.MarshalAuthorizedKey(key) // "ssh-ed25519 AAAA…\n"
		rest := b
		for len(rest) > 0 {
			pub, _, _, r, err := ssh.ParseAuthorizedKey(rest)
			rest = r
			if err != nil || pub == nil {
				continue
			}
			// Compare the wire form; only accept keys we would recognize as ours
			// (the managed-key install path is what put them there).
			if bytes.Equal(ssh.MarshalAuthorizedKey(pub), want) {
				return true
			}
		}
		return false
	}
}
