package main

import "testing"

func TestResolveACLEndpoint(t *testing.T) {
	dev2ip := map[string]string{"devA": "100.96.0.2", "devB": "100.96.0.3"}
	tag2ips := map[string][]string{"prod": {"100.96.0.2", "100.96.0.3"}}
	user2ips := map[string][]string{"user123": {"100.96.0.2"}}

	if p, ok := resolveACLEndpoint("any", "*", dev2ip, tag2ips, user2ips); !ok || p != nil {
		t.Errorf("any → unconstrained nil/true, got %v/%v", p, ok)
	}
	if p, ok := resolveACLEndpoint("device", "devA", dev2ip, tag2ips, user2ips); !ok || len(p) != 1 || p[0].String() != "100.96.0.2/32" {
		t.Errorf("device → /32, got %v/%v", p, ok)
	}
	if _, ok := resolveACLEndpoint("device", "ghost", dev2ip, tag2ips, user2ips); ok {
		t.Error("unknown device must be unresolvable (fail-safe)")
	}
	if p, ok := resolveACLEndpoint("tag", "tag:prod", dev2ip, tag2ips, user2ips); !ok || len(p) != 2 {
		t.Errorf("tag → 2 prefixes, got %v/%v", p, ok)
	}
	if p, ok := resolveACLEndpoint("user", "user123", dev2ip, tag2ips, user2ips); !ok || len(p) != 1 {
		t.Errorf("user → 1 prefix, got %v/%v", p, ok)
	}
	if _, ok := resolveACLEndpoint("user", "nobody", dev2ip, tag2ips, user2ips); ok {
		t.Error("unknown user must be unresolvable")
	}
}

func TestParseACLPorts(t *testing.T) {
	if ports, err := parseACLPorts([]string{"22", "80-90"}); err != nil || len(ports) != 2 || ports[0].Lo != 22 || ports[1].Hi != 90 {
		t.Errorf("parse ports: %v %v", ports, err)
	}
	if ports, err := parseACLPorts([]string{"*"}); err != nil || ports != nil {
		t.Errorf("* → all ports (nil): %v %v", ports, err)
	}
	if _, err := parseACLPorts([]string{"99-1"}); err == nil {
		t.Error("inverted range should error")
	}
	if _, err := parseACLPorts([]string{"abc"}); err == nil {
		t.Error("non-numeric port should error")
	}
}
