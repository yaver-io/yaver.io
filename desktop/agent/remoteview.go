package main

// remoteview.go — abstract remote-view / remote-desktop software management.
//
// Yaver is the underlying platform: it provides AI runners (Claude Code / Codex /
// OpenRouter / local), the QUIC mesh, and — here — a pluggable layer for
// remote-view tools (RustDesk first-class, AnyDesk / VNC extensible). The
// "blackbox" ghost connects one of these to the customer's PC (where only the
// remote-view tool is installed) and then drives the resulting window via the
// ghost_* verbs to sync/hijack the legacy ERP.
//
// All the heavy management (binary discovery, process lifecycle, connection
// state) lives HERE in Yaver; Talos only issues intents.

import (
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// RemoteView is a remote-desktop client Yaver can drive on the operator's
// behalf. Implementations launch/track a client connected to a peer.
type RemoteView interface {
	Name() string
	Available() bool
	Connect(peerID, password string, opts map[string]string) error
	Disconnect() error
	Status() map[string]interface{}
}

var (
	remoteViewMu sync.RWMutex
	remoteViews  = map[string]RemoteView{}
)

func registerRemoteView(rv RemoteView) {
	remoteViewMu.Lock()
	defer remoteViewMu.Unlock()
	remoteViews[rv.Name()] = rv
}

// getRemoteView resolves a provider by name; empty defaults to "rustdesk".
func getRemoteView(name string) (RemoteView, bool) {
	if name == "" {
		name = "rustdesk"
	}
	remoteViewMu.RLock()
	defer remoteViewMu.RUnlock()
	rv, ok := remoteViews[name]
	return rv, ok
}

func listRemoteViews() []map[string]interface{} {
	remoteViewMu.RLock()
	defer remoteViewMu.RUnlock()
	out := make([]map[string]interface{}, 0, len(remoteViews))
	for name, rv := range remoteViews {
		out = append(out, map[string]interface{}{"name": name, "available": rv.Available()})
	}
	return out
}

// processRemoteView is the shared base for CLI-launched remote-view clients
// (RustDesk, AnyDesk, VNC viewers). Each provider supplies its candidate binary
// names and an arg builder.
type processRemoteView struct {
	name      string
	bins      []string
	buildArgs func(peerID, password string, opts map[string]string) []string

	mu        sync.Mutex
	cmd       *exec.Cmd
	peerID    string
	startedAt time.Time
}

func (p *processRemoteView) Name() string { return p.name }

func (p *processRemoteView) binary() (string, error) {
	for _, b := range p.bins {
		if path, err := exec.LookPath(b); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s not installed", p.name)
}

func (p *processRemoteView) Available() bool {
	_, err := p.binary()
	return err == nil
}

func (p *processRemoteView) Connect(peerID, password string, opts map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	bin, err := p.binary()
	if err != nil {
		return err
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		p.cmd = nil
	}
	cmd := exec.Command(bin, p.buildArgs(peerID, password, opts)...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s connect failed: %w", p.name, err)
	}
	p.cmd = cmd
	p.peerID = peerID
	p.startedAt = time.Now()
	go func() { _ = cmd.Wait() }() // reap
	return nil
}

func (p *processRemoteView) Disconnect() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	p.cmd = nil
	p.peerID = ""
	return nil
}

func (p *processRemoteView) Status() map[string]interface{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	bin, binErr := p.binary()
	connected := p.cmd != nil && p.cmd.Process != nil && (p.cmd.ProcessState == nil || !p.cmd.ProcessState.Exited())
	out := map[string]interface{}{
		"provider":  p.name,
		"installed": binErr == nil,
		"binary":    bin,
		"connected": connected,
		"peerId":    p.peerID,
	}
	if connected && p.cmd.Process != nil {
		out["pid"] = p.cmd.Process.Pid
		out["since"] = p.startedAt.Unix()
	}
	return out
}

func init() {
	// RustDesk — first-class. `rustdesk --connect <id> [--password <pw>]`.
	registerRemoteView(&processRemoteView{
		name: "rustdesk",
		bins: []string{"rustdesk", "RustDesk", "/usr/bin/rustdesk", "/usr/local/bin/rustdesk"},
		buildArgs: func(peerID, password string, _ map[string]string) []string {
			args := []string{"--connect", peerID}
			if password != "" {
				args = append(args, "--password", password)
			}
			return args
		},
	})
	// AnyDesk — `anydesk <id>` (password typically piped; best-effort).
	registerRemoteView(&processRemoteView{
		name: "anydesk",
		bins: []string{"anydesk"},
		buildArgs: func(peerID, _ string, _ map[string]string) []string {
			return []string{peerID}
		},
	})
	// VNC — `vncviewer <host[:port]>`.
	registerRemoteView(&processRemoteView{
		name: "vnc",
		bins: []string{"vncviewer", "xtigervncviewer", "vinagre"},
		buildArgs: func(peerID, _ string, _ map[string]string) []string {
			return []string{peerID}
		},
	})
}
