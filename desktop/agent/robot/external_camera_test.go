package robot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// tiny valid-enough JPEG header (SOI + APP0 marker bytes) for the looksJPEG gate.
var fakeJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}

func TestExternalCameraGrabAndStale(t *testing.T) {
	now := time.Unix(1000, 0)
	cam := NewExternalCamera()
	cam.MaxAge = 2 * time.Second
	cam.nowFn = func() time.Time { return now }

	// No frame yet → not available, Grab errors.
	if cam.Available() {
		t.Fatal("Available() true before any frame")
	}
	if _, err := cam.Grab(context.Background()); err == nil {
		t.Fatal("Grab() should error before any frame")
	}
	if cam.AgeMs() != -1 {
		t.Fatalf("AgeMs()=%d want -1 before any frame", cam.AgeMs())
	}

	// Push a frame → fresh.
	cam.SetFrame(fakeJPEG)
	if !cam.Available() {
		t.Fatal("Available() false right after SetFrame")
	}
	got, err := cam.Grab(context.Background())
	if err != nil {
		t.Fatalf("Grab() after SetFrame: %v", err)
	}
	if len(got) != len(fakeJPEG) {
		t.Fatalf("Grab() returned %d bytes want %d", len(got), len(fakeJPEG))
	}
	// returned slice must be a copy (mutating it must not affect the buffer).
	got[0] = 0x00
	again, _ := cam.Grab(context.Background())
	if again[0] != 0xFF {
		t.Fatal("Grab() did not return a defensive copy")
	}

	// Advance past MaxAge → stale: not available, Grab errors.
	now = now.Add(3 * time.Second)
	if cam.Available() {
		t.Fatal("Available() true on a stale frame")
	}
	if _, err := cam.Grab(context.Background()); err == nil {
		t.Fatal("Grab() should error on a stale frame")
	}

	// Re-push refreshes.
	cam.SetFrame(fakeJPEG)
	if !cam.Available() {
		t.Fatal("Available() false after refreshing a stale camera")
	}
}

func TestHTTPCameraGrab(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(fakeJPEG)
	}))
	defer srv.Close()

	cam := NewHTTPCamera(srv.URL)
	if !cam.Available() {
		t.Fatal("HTTPCamera.Available() false with a URL set")
	}
	got, err := cam.Grab(context.Background())
	if err != nil {
		t.Fatalf("HTTPCamera.Grab(): %v", err)
	}
	if len(got) != len(fakeJPEG) {
		t.Fatalf("Grab() %d bytes want %d", len(got), len(fakeJPEG))
	}
}

func TestHTTPCameraRejectsNonJPEG(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not an image</html>"))
	}))
	defer srv.Close()

	cam := NewHTTPCamera(srv.URL)
	if _, err := cam.Grab(context.Background()); err == nil {
		t.Fatal("HTTPCamera.Grab() should reject a non-JPEG body")
	}
}

// ExternalCamera and HTTPCamera must satisfy the Camera interface.
var _ Camera = (*ExternalCamera)(nil)
var _ Camera = (*HTTPCamera)(nil)
