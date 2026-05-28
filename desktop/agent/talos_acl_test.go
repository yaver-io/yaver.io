package main

import "testing"

func TestBuildTalosACLPeerLocal(t *testing.T) {
	peer, err := buildTalosACLPeer(talosACLSetup{
		Mode:       "local",
		Command:    "talcli",
		LicenseKey: "TALOS-TEST",
	})
	if err != nil {
		t.Fatalf("buildTalosACLPeer local: %v", err)
	}
	if peer.ID != "talos" || peer.Type != "stdio" {
		t.Fatalf("unexpected peer: %#v", peer)
	}
	if peer.Command != "talcli mcp serve --license TALOS-TEST" {
		t.Fatalf("command = %q", peer.Command)
	}
}

func TestBuildTalosACLPeerRemote(t *testing.T) {
	peer, err := buildTalosACLPeer(talosACLSetup{
		Mode: "remote",
		URL:  "https://talos-box.example.com/mcp",
		Auth: "token",
	})
	if err != nil {
		t.Fatalf("buildTalosACLPeer remote: %v", err)
	}
	if peer.ID != "talos" || peer.Type != "http" {
		t.Fatalf("unexpected peer: %#v", peer)
	}
	if peer.URL != "https://talos-box.example.com/mcp" || peer.Auth != "token" {
		t.Fatalf("unexpected remote target: %#v", peer)
	}
}

func TestUpsertACLPeerReplacesTalos(t *testing.T) {
	peers := []ACLPeerConfig{
		{ID: "talos", Name: "Talos", Type: "stdio", Command: "old"},
		{ID: "other", Name: "Other", Type: "http", URL: "https://example.com/mcp"},
	}
	next := upsertACLPeer(peers, ACLPeerConfig{ID: "talos", Name: "Talos", Type: "stdio", Command: "new"})
	if len(next) != 2 {
		t.Fatalf("len = %d", len(next))
	}
	if next[0].Command != "new" {
		t.Fatalf("talos peer was not replaced: %#v", next[0])
	}
}
