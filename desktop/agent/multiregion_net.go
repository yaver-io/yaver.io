package main

import (
	"net"
	"time"
)

// net_Dial wraps net.DialTimeout so the orchestrate file doesn't need to
// re-import "net" alongside its other deps (helps keep the per-file imports
// minimal for clarity).
func net_Dial(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, address, timeout)
}
