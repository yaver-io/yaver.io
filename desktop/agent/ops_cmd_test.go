package main

import "testing"

func TestOpsHTTPClientOutlivesStorageScan(t *testing.T) {
	if opsHTTPClient.Timeout <= scanDeadline {
		t.Fatalf("ops client timeout %s must outlive storage scan deadline %s", opsHTTPClient.Timeout, scanDeadline)
	}
}
