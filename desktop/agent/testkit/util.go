package testkit

import (
	"encoding/base64"
	"net"
)

// pickFreePort asks the kernel for an unused TCP port. Used by the
// Firefox driver to start geckodriver on a non-clashing port.
func pickFreePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 4444 // default WebDriver port — almost always free on a dev box
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// base64Decode wraps the stdlib so callers don't have to import the
// package directly. Used by the Firefox driver's screenshot endpoint.
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
