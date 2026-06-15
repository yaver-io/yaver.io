package main

import "testing"

func TestEvaluateAccessPolicy(t *testing.T) {
	cases := []struct {
		src, act, jur, want string
	}{
		{"betfair.com", "bet", "TR", "block"},        // foreign book, betting from TR illegal
		{"betfair.com", "data", "TR", "allow"},       // reading public data is fine
		{"betfair.com", "bet", "", "warn"},           // unknown jurisdiction + gambling => warn
		{"betfair.com", "login", "TR", "warn"},       // account action in blocked jurisdiction => warn
		{"superbet.rs", "deposit", "TR", "block"},    // regional book, funding from TR illegal
		{"misli.com", "bet", "TR", "allow"},          // TR state-licensed, legal from TR
		{"example-saas.com", "login", "TR", "allow"}, // unknown source => don't over-block
		{"betfair.com", "bet", "GB", "allow"},        // not in blocked list for GB
	}
	for _, c := range cases {
		got := EvaluateAccessPolicy(c.src, c.act, c.jur)
		if got.Decision != c.want {
			t.Errorf("%s/%s/%s: got %q (%s), want %q", c.src, c.act, c.jur, got.Decision, got.Reason, c.want)
		}
	}
}
