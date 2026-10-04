package main

import "testing"

func TestSameMeshOwnerFailsClosedOnPublicRelay(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		target        string
		convexBacked  bool
		want          bool
	}{
		{name: "same authenticated owner", source: "user-a", target: "user-a", convexBacked: true, want: true},
		{name: "different tenants", source: "user-a", target: "user-b", convexBacked: true, want: false},
		{name: "missing source owner", source: "", target: "user-a", convexBacked: true, want: false},
		{name: "missing target owner", source: "user-a", target: "", convexBacked: true, want: false},
		{name: "both owners missing", source: "", target: "", convexBacked: true, want: false},
		{name: "explicit self-hosted single tenant", source: "", target: "", convexBacked: false, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameMeshOwner(tt.source, tt.target, tt.convexBacked); got != tt.want {
				t.Fatalf("sameMeshOwner(%q, %q, %v) = %v, want %v", tt.source, tt.target, tt.convexBacked, got, tt.want)
			}
		})
	}
}
