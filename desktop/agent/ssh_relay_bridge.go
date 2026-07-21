package main

// ssh_relay_bridge.go — Phase A core: bridging a relay-forwarded stream to the
// box's local embedded SSH control server, and the tag that tells the box a
// stream is SSH (not the JSON control protocol).
//
// Reverse-SSH-over-relay reuses the EXISTING QUIC relay tunnel (no new bastion,
// no per-device bastion port — multi-tenancy stays deviceId-routed, transport
// doc §4a). The relay opens a stream to the box tagged `relayStreamTagSSH`; the
// box splices that stream to `127.0.0.1:<ssh control addr>`. The box's embedded
// server (ssh_control_server.go) then does public-key auth + the forced-command
// cage on it — so the relay only piped bytes and a hostile tenant's stream still
// fails the SSH handshake (§4d).

import (
	"context"
	"io"
	"net"
	"os"
	"time"
)

// relayStreamTagSSH marks a relay-forwarded stream as raw SSH bytes destined for
// the box's SSH control server. The existing JSON control protocol is untagged
// (or tag 0x01); a first byte of relayStreamTagSSH means "splice me to SSH".
const relayStreamTagSSH byte = 0x02

// relaySSHControlSentinelPath is the envelope path the relay forwards to request
// that a tunnel stream be spliced to the box's SSH control server instead of the
// HTTP agent. Additive: no real agent route uses this path, so normal traffic is
// never affected. The phone reaches it as `/d/<deviceId>/_yaver_ssh_control`.
const relaySSHControlSentinelPath = "/_yaver_ssh_control"

// relayStreamTagIsSSH reports whether a stream's leading tag byte routes it to
// the local SSH control server rather than the JSON control handler. Pure so the
// routing decision is unit-tested.
func relayStreamTagIsSSH(tag byte) bool { return tag == relayStreamTagSSH }

// localSSHControlAddr is the box's embedded SSH control server address (matches
// startSSHControlServerIfEnabled's default / env).
func localSSHControlAddr() string {
	if a := os.Getenv("YAVER_SSH_CONTROL_ADDR"); a != "" {
		return a
	}
	return "127.0.0.1:2222"
}

// bridgeToLocalSSH splices a relay-forwarded client stream to the local SSH
// control server: dial sshAddr, then copy bytes both ways until either side
// closes. Returns when the bridge tears down. The box's SSH server authenticates
// the client on this pipe — the bridge itself grants nothing.
func bridgeToLocalSSH(ctx context.Context, client io.ReadWriteCloser, sshAddr string) error {
	return spliceStreamToSSH(ctx, client, nil, sshAddr)
}

// spliceStreamToSSH is what the agent's relay-stream handler calls for an SSH
// sentinel stream: dial the local SSH control server, first flush any bytes the
// JSON decoder over-read past the envelope (`overflow` — the start of the SSH
// handshake the relay flushed together with the envelope), then splice the
// stream both ways. Testable in isolation so the agent-side glue (not just the
// primitives) is covered.
func spliceStreamToSSH(ctx context.Context, stream io.ReadWriteCloser, overflow io.Reader, sshAddr string) error {
	d := net.Dialer{Timeout: 5 * time.Second}
	srv, err := d.DialContext(ctx, "tcp", sshAddr)
	if err != nil {
		return err
	}
	if overflow != nil {
		// Push the over-read bytes to the SSH server before splicing, so the first
		// SSH bytes the client already sent aren't dropped.
		if _, err := io.Copy(srv, overflow); err != nil && err != io.EOF {
			srv.Close()
			stream.Close()
			return err
		}
	}
	return spliceBidirectional(stream, srv)
}

// spliceBidirectional copies a↔b until one side ends, then closes both. Returns
// the first non-EOF error seen (or nil on a clean close).
func spliceBidirectional(a io.ReadWriteCloser, b io.ReadWriteCloser) error {
	defer a.Close()
	defer b.Close()
	errc := make(chan error, 2)
	cp := func(dst io.Writer, src io.Reader) {
		_, err := io.Copy(dst, src)
		errc <- err
	}
	go cp(b, a)
	go cp(a, b)
	// First direction to finish tears the pipe down (the deferred Closes unblock
	// the other Copy). Return its error if it was a real one.
	if err := <-errc; err != nil && err != io.EOF {
		return err
	}
	return nil
}
