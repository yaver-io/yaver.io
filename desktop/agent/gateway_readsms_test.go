package main

import "testing"

func TestParseContentRowBody(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Row: 0 body=Your code is 123456", "Your code is 123456"},
		{"Row: 1 body=", ""},
		{"no body here", ""},
		{"Row: 2 body=  spaced 999111  ", "spaced 999111"},
	}
	for _, c := range cases {
		if got := parseContentRowBody(c.in); got != c.want {
			t.Fatalf("parseContentRowBody(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestReadSMSGatedByConsent confirms that without the read_device_sms grant the
// driver returns "" WITHOUT touching adb (so the auth handler escalates to a
// human gate). We use a bogus serial: if it tried adb it would error, but the
// consent gate short-circuits first.
func TestReadSMSGatedByConsent(t *testing.T) {
	isolateHome(t) // no consent granted
	d := &redroidDeviceDriver{serial: "no-such-device"}
	code, err := d.ReadSMS()
	if err != nil || code != "" {
		t.Fatalf("ungranted ReadSMS must be (\"\", nil) without calling adb, got (%q, %v)", code, err)
	}
}
