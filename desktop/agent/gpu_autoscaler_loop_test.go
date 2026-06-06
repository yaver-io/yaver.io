package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchLoadSample(t *testing.T) {
	// Canonical field names.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"concurrency":17,"p95TtftMs":640}`))
	}))
	defer srv.Close()
	s, err := fetchLoadSample(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if s.Concurrency != 17 || s.P95TTFTms != 640 {
		t.Fatalf("sample = %+v, want {17 640}", s)
	}
}

func TestFetchLoadSampleAliases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"activeCalls":9,"ttftP95":700}`))
	}))
	defer srv.Close()
	s, err := fetchLoadSample(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if s.Concurrency != 9 || s.P95TTFTms != 700 {
		t.Fatalf("alias sample = %+v, want {9 700}", s)
	}
}

func TestFetchLoadSampleErrors(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer down.Close()
	if _, err := fetchLoadSample(context.Background(), down.URL); err == nil {
		t.Error("503 should error")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer bad.Close()
	if _, err := fetchLoadSample(context.Background(), bad.URL); err == nil {
		t.Error("non-JSON should error")
	}
}
