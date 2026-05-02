package main

import "testing"

func TestArgContainsShellMetacharacter(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"--token=foo", false},
		{"--prod", false},
		{"hello-world.com", false},
		{"v1.2.3", false},
		// Metacharacters that escape `sh -c <cmd>` quoting.
		{"; rm -rf $HOME", true},
		{"$(curl evil)", true},
		{"`whoami`", true},
		{"foo|bar", true},
		{"foo&bar", true},
		{"foo<bar", true},
		{"foo>bar", true},
		{"foo&&bar", true},
		{"foo'bar", true},
		{`foo"bar`, true},
		{"foo\\bar", true},
		{"foo\nbar", true},
		{"foo\rbar", true},
		{"(echo)", true},
		{"{evil}", true},
	}
	for _, tc := range cases {
		got := argContainsShellMetacharacter(tc.in)
		if got != tc.want {
			t.Errorf("argContainsShellMetacharacter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
