package main

import (
	"strings"
	"testing"
)

// A device_id proxy must speak the receiving route's ACTUAL contract. This test
// exists because the first git_branches proxy did not: it POSTed JSON to a route
// that answers `405 use GET` and reads its directory from a `workDir` QUERY
// param. It would have failed on every call — an advertised capability that
// always errors, which is strictly worse than not offering one.
//
// The mistake is easy to repeat because the neighbouring mobile_* proxies are
// all POST/JSON, so copying one looks right.
func TestGitProxiesUseTheRoutesActualMethod(t *testing.T) {
	disp := readSourceFile(t, "httpserver.go")
	routes := readSourceFile(t, "git_http.go")

	// Every /git/* handler in this file answers GET only.
	for _, handler := range []string{"handleGitBranches", "handleGitStatus", "handleGitLog", "handleGitDiff"} {
		i := strings.Index(routes, "func (s *HTTPServer) "+handler)
		if i < 0 {
			continue // route may have been renamed; other tests cover that
		}
		seg := routes[i : i+400]
		if !strings.Contains(seg, "http.MethodGet") {
			t.Errorf("%s no longer looks GET-only — re-check every proxy that targets it", handler)
		}
	}

	// So a proxy to one of them must use MethodGet, never MethodPost.
	for _, call := range []string{
		`proxyToDeviceJSON(context.Background(), "git_branches"`,
	} {
		i := strings.Index(disp, call)
		if i < 0 {
			t.Fatalf("proxy call not found: %s", call)
		}
		seg := disp[i : i+260]
		if strings.Contains(seg, "http.MethodPost") {
			t.Errorf("%s proxies with POST to a GET-only route — it would 405 on every call", call)
		}
		if !strings.Contains(seg, "http.MethodGet") {
			t.Errorf("%s does not use MethodGet", call)
		}
		// And the directory must travel as the query param the handler reads.
		if !strings.Contains(seg, "workDir=") {
			t.Errorf("%s does not pass workDir= — getGitWorkDir reads that query param, not a JSON body", call)
		}
	}
}
