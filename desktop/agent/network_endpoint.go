package main

import (
	"net"
	"strconv"
	"strings"
)

// bareNetworkHost normalizes a host supplied by a registry row or user config.
// URL.Hostname-style bracketed IPv6 values are accepted so callers do not
// accidentally produce http://[[::1]]:18080 when data crosses surfaces.
func bareNetworkHost(host string) string {
	host = strings.TrimSpace(host)
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		return host[1 : len(host)-1]
	}
	return host
}

// hostPort is the only supported way to join a dynamic host and port. Unlike
// fmt.Sprintf("%s:%d"), net.JoinHostPort brackets IPv6 literals and preserves
// zone identifiers such as fe80::1%en0.
func hostPort(host string, port int) string {
	return net.JoinHostPort(bareNetworkHost(host), strconv.Itoa(port))
}

func agentHTTPBase(host string, port int) string {
	return "http://" + hostPort(host, port)
}
