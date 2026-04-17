package main

// blobs_http_integration_test.go — end-to-end over HTTP for the blob
// surface: upload, list with pagination, sign + fetch unauth, delete.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// putBlob is a tiny helper: PUT /blobs/<bucket>/<key> with the given
// body + content-type, using the owner token.
func putBlob(t *testing.T, baseURL, token, bucket, key, ct string, body []byte) {
	t.Helper()
	req, err := http.NewRequest("PUT",
		fmt.Sprintf("%s/blobs/%s/%s", baseURL, bucket, key),
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PUT %s/%s: %v", bucket, key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT %s/%s: HTTP %d — %s", bucket, key, resp.StatusCode, string(raw))
	}
}

// getBlobBytes fetches the raw bytes at /blobs/<bucket>/<key> with
// the given bearer (empty for unauth). Returns (status, body-bytes).
func getBlobBytes(t *testing.T, fullURL, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", fullURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestBlobsHTTPRoundtrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()

	// 1. PUT a blob.
	payload := []byte("hello-blob-world")
	putBlob(t, baseURL, "owner-tok", "testbucket", "greeting.txt", "text/plain", payload)

	// 2. GET it back with owner token.
	status, got := getBlobBytes(t, baseURL+"/blobs/testbucket/greeting.txt", "owner-tok")
	if status != 200 {
		t.Fatalf("authed GET: HTTP %d", status)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("bytes mismatch: got %q, want %q", string(got), string(payload))
	}

	// 3. Sign a public URL and fetch unauth.
	req, _ := http.NewRequest("GET", baseURL+"/blobs/url/testbucket/greeting.txt?ttl=60", nil)
	req.Header.Set("Authorization", "Bearer owner-tok")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("sign req: %v", err)
	}
	var signOut map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&signOut)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("sign: HTTP %d", resp.StatusCode)
	}
	signedPath, _ := signOut["url"].(string)
	if !strings.HasPrefix(signedPath, "/blobs/public") {
		t.Fatalf("signed URL shape is wrong: %q", signedPath)
	}
	status, got = getBlobBytes(t, baseURL+signedPath, "") // NO auth header
	if status != 200 {
		t.Fatalf("public GET via signed URL: HTTP %d", status)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("public bytes mismatch")
	}

	// 4. Tampered signature fails.
	tampered := strings.Replace(signedPath, "sig=", "sig=zzzz", 1)
	status, _ = getBlobBytes(t, baseURL+tampered, "")
	if status == 200 {
		t.Error("tampered signature should have been rejected")
	}

	// 5. DELETE then re-GET → 404.
	delReq, _ := http.NewRequest("DELETE", baseURL+"/blobs/testbucket/greeting.txt", nil)
	delReq.Header.Set("Authorization", "Bearer owner-tok")
	dresp, err := (&http.Client{Timeout: 5 * time.Second}).Do(delReq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != 200 {
		t.Fatalf("delete: HTTP %d", dresp.StatusCode)
	}
	status, _ = getBlobBytes(t, baseURL+"/blobs/testbucket/greeting.txt", "owner-tok")
	if status != 404 {
		t.Errorf("after delete: expected 404, got %d", status)
	}
}

func TestBlobsHTTPListPagination(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()

	// Seed 6 blobs so `limit=3` gives us two pages.
	keys := []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt", "f.txt"}
	for _, k := range keys {
		putBlob(t, baseURL, "owner-tok", "pagebucket", k, "text/plain", []byte(k))
	}

	// Page 1.
	status, body := doRequest(t, "GET", baseURL+"/blobs/pagebucket?limit=3", "owner-tok", "")
	if status != 200 {
		t.Fatalf("list page 1: HTTP %d", status)
	}
	if total, _ := body["total"].(float64); int(total) != 6 {
		t.Errorf("expected total=6, got %v", body["total"])
	}
	page1, _ := body["keys"].([]interface{})
	if len(page1) != 3 {
		t.Fatalf("page 1 expected 3 keys, got %d", len(page1))
	}
	nextCursor, _ := body["nextCursor"].(string)
	if nextCursor == "" {
		t.Fatal("expected nextCursor on page 1")
	}

	// Page 2.
	status, body = doRequest(t, "GET",
		baseURL+"/blobs/pagebucket?limit=3&after="+nextCursor,
		"owner-tok", "")
	if status != 200 {
		t.Fatalf("list page 2: HTTP %d", status)
	}
	page2, _ := body["keys"].([]interface{})
	if len(page2) != 3 {
		t.Fatalf("page 2 expected 3 keys, got %d", len(page2))
	}
	final, _ := body["nextCursor"].(string)
	if final != "" {
		t.Errorf("page 2 nextCursor should be empty, got %q", final)
	}

	// Combined page1 + page2 must be exactly the 6 keys we uploaded.
	seen := map[string]bool{}
	for _, e := range append(page1, page2...) {
		m := e.(map[string]interface{})
		seen[m["key"].(string)] = true
	}
	for _, k := range keys {
		if !seen[k] {
			t.Errorf("missing key %q across both pages", k)
		}
	}

	// Back-compat: the server also returns the old "items" field.
	if _, ok := body["items"]; !ok {
		t.Error("server should keep the legacy `items` field for back-compat")
	}
}

func TestBlobsListUnauthRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()
	status, _ := doRequest(t, "GET", baseURL+"/blobs", "", "")
	if status == 200 {
		t.Fatal("/blobs list without auth should be rejected")
	}
}
