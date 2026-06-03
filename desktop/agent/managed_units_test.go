//go:build !windows

package main

import "strings"

import "testing"

func TestManagedUnitSystemdRender(t *testing.T) {
	env := map[string]string{
		"B_KEY":   "second",
		"A_TOKEN": `va"l/with=special`,
	}
	unit := renderSystemdUserUnit("yaver-companion-eback-mailer", "/usr/bin/node",
		[]string{"server.js", "--port", "8788"}, "/srv/eback", env)

	for _, want := range []string{
		"Description=yaver-companion-eback-mailer (Yaver companion service)",
		"ExecStart=/usr/bin/node server.js --port 8788",
		"WorkingDirectory=/srv/eback",
		"Restart=always",
		"WantedBy=default.target",
		// secret value is quote/backslash escaped so =,/,\" survive
		`Environment="A_TOKEN=va\"l/with=special"`,
		`Environment="B_KEY=second"`,
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q in:\n%s", want, unit)
		}
	}

	// Env keys must be emitted in sorted order for deterministic units.
	if strings.Index(unit, "A_TOKEN") > strings.Index(unit, "B_KEY") {
		t.Fatalf("env keys not sorted:\n%s", unit)
	}
}

func TestManagedUnitLaunchdRender(t *testing.T) {
	env := map[string]string{"TOKEN": "a&b<c>"}
	plist := buildManagedLaunchdPlist("io.yaver.companion.eback-mailer", "/usr/bin/node",
		[]string{"server.js"}, "/srv/eback", env, "/tmp/logs")

	for _, want := range []string{
		"<key>Label</key>",
		"<string>io.yaver.companion.eback-mailer</string>",
		"<string>/usr/bin/node</string>",
		"<string>server.js</string>",
		"<key>EnvironmentVariables</key>",
		"<key>TOKEN</key>",
		// XML-escaped secret
		"<string>a&amp;b&lt;c&gt;</string>",
		"<key>RunAtLoad</key>",
		"/tmp/logs/io.yaver.companion.eback-mailer.out.log",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("launchd plist missing %q in:\n%s", want, plist)
		}
	}
}

func TestManagedUnitLaunchdNoEnvOmitsDict(t *testing.T) {
	plist := buildManagedLaunchdPlist("io.yaver.companion.x", "/bin/true", nil, "/tmp", nil, "/tmp")
	if strings.Contains(plist, "EnvironmentVariables") {
		t.Fatalf("empty env should omit EnvironmentVariables dict:\n%s", plist)
	}
}
