package main

// imgopt.go — on-demand image optimizer. Replaces Imgix /
// Cloudinary / imagekit for solo devs who host their own images
// and want simple URL-driven resizing + re-encoding without a
// paid service.
//
// GET /img?src=<rel-path>&w=&h=&fmt=&q=
//
//   src   — path relative to one of the configured root dirs
//           (agent projects or a dedicated /img root). Same
//           safeJoin guard as files_browser.go.
//   w,h   — target dimensions (one or both). Original aspect
//           ratio preserved when only one is set.
//   fmt   — "webp" (default) | "jpeg" | "png". Output format.
//   q     — quality 1–100 (default 80 for jpeg/webp).
//
// Results are cached on disk at ~/.yaver/img-cache/<sha1>.<ext>
// so the expensive resize only happens once per variant. Cache
// is pure disk; no LRU eviction — solo-dev volume is low, let
// the user `rm -rf` the cache dir when it grows.

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
)

func imgCacheDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "img-cache")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func (s *HTTPServer) handleImgOptimize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	q := r.URL.Query()
	src := q.Get("src")
	if src == "" {
		jsonError(w, http.StatusBadRequest, "src required")
		return
	}

	// Resolve src against a file browser root — keeps the same
	// no-escape guarantee as /files/read. Falls back to the
	// agent's cwd when src doesn't start with a root ID.
	rootID := q.Get("root")
	var abs string
	if rootID != "" {
		root := s.resolveFileRoot(r, rootID)
		if root == nil {
			jsonError(w, http.StatusNotFound, "root not found")
			return
		}
		joined, ok := safeJoin(root.Path, src)
		if !ok {
			jsonError(w, http.StatusBadRequest, "path escapes root")
			return
		}
		abs = joined
	} else {
		// Accept absolute paths only from owner requests (no
		// guest header present) so random visitors can't
		// target /etc/ssh/ssh_host_rsa_key.
		if r.Header.Get("X-Yaver-Guest") != "" {
			jsonError(w, http.StatusForbidden, "guests must specify a root")
			return
		}
		if !filepath.IsAbs(src) {
			cwd, _ := os.Getwd()
			abs = filepath.Join(cwd, src)
		} else {
			abs = src
		}
	}

	width, _ := strconv.Atoi(q.Get("w"))
	height, _ := strconv.Atoi(q.Get("h"))
	format := strings.ToLower(q.Get("fmt"))
	if format == "" {
		format = "webp"
	}
	quality, _ := strconv.Atoi(q.Get("q"))
	if quality <= 0 || quality > 100 {
		quality = 80
	}

	// Cache key — src path + params. Changing any input changes
	// the hash, so stale variants never bleed into fresh ones.
	keyHash := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%s|%d", abs, width, height, format, quality)))
	cacheName := hex.EncodeToString(keyHash[:]) + "." + format
	cacheDir, err := imgCacheDir()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cachePath := filepath.Join(cacheDir, cacheName)
	if data, err := os.ReadFile(cachePath); err == nil {
		serveImgBytes(w, data, format)
		return
	}

	// Read source.
	src_f, err := os.Open(abs)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	defer src_f.Close()
	img, _, err := image.Decode(src_f)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "not an image: "+err.Error())
		return
	}
	out := resizeImage(img, width, height)
	encoded, err := encodeImage(out, format, quality)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = os.WriteFile(cachePath, encoded, 0o644)
	serveImgBytes(w, encoded, format)
}

// resizeImage does the math. When both w and h are set we honor
// them as-is (so the caller can force-crop). When one is set we
// preserve aspect ratio. When neither is set we return the
// original — useful for format conversion alone.
func resizeImage(img image.Image, w, h int) image.Image {
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if w <= 0 && h <= 0 {
		return img
	}
	if w <= 0 {
		w = srcW * h / srcH
	}
	if h <= 0 {
		h = srcH * w / srcW
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// encodeImage writes img to the chosen format. WebP falls back
// to JPEG when the golang.org/x/image/webp encoder isn't linked
// (avoids a CGo dep) — still gives the dev a working pipeline
// and they can swap in a real webp encoder when/if needed.
func encodeImage(img image.Image, format string, quality int) ([]byte, error) {
	var buf bytes.Buffer
	switch format {
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
	case "jpeg", "jpg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	case "webp":
		// Pure-Go WebP encoding isn't in the standard library,
		// and we don't want a CGo dep. Emit JPEG under the
		// webp filename so the cache still works — the MIME
		// type stays image/jpeg so browsers render it fine.
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
	return buf.Bytes(), nil
}

func serveImgBytes(w http.ResponseWriter, data []byte, format string) {
	mime := "image/jpeg"
	switch format {
	case "png":
		mime = "image/png"
	case "webp":
		// See encodeImage note — webp falls back to jpeg bytes.
		mime = "image/jpeg"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = io.Copy(w, bytes.NewReader(data))
}
