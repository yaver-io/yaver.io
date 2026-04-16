package main

// blobs.go — local blob / object storage for the solo-SaaS
// "I need to serve user avatars + attachments without paying
// for S3" case. Backed by ~/.yaver/blobs/<bucket>/<key> on
// disk, with HMAC-signed public URLs for time-limited
// distribution.
//
// Design choices:
//
//   - One file per blob plus a sibling `<key>.meta.json` with
//     size, mime, sha256, uploadedBy, uploadedAt. Cheap, grep-
//     friendly, trivial to back up with the #4 backup feature.
//   - Buckets and keys are sanitized to a filesystem-safe
//     subset so a crafty key can't escape the blobs dir.
//   - Signed URLs are HMAC-SHA256 over (bucket|key|exp) keyed
//     by a blob secret we persist the first time /blobs/public
//     is accessed. No DB, no coordination server.
//   - Content-Type is sniffed on upload (first 512 bytes
//     → http.DetectContentType) and cached in the meta file.
//   - List + stats are cheap: directory walks on demand.
//
// HTTP surface (registered in httpserver.go):
//
//   GET    /blobs                             — list buckets
//   GET    /blobs/<bucket>                    — list keys in a bucket
//   PUT    /blobs/<bucket>/<key>              — upload bytes
//   GET    /blobs/<bucket>/<key>              — download bytes
//   DELETE /blobs/<bucket>/<key>              — delete bytes + meta
//   GET    /blobs/url/<bucket>/<key>          — sign a URL
//   GET    /blobs/public?b=...&k=...&exp=...&sig=... — public fetch

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BlobMetadata is the sibling record written alongside every
// blob. Kept small so a bucket of 10k avatars doesn't balloon
// into megabytes of JSON.
type BlobMetadata struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"contentType"`
	UploadedAt  string `json:"uploadedAt"`
	UploadedBy  string `json:"uploadedBy,omitempty"`
}

var blobMu sync.Mutex

// blobsRoot returns the on-disk root for blob storage, creating
// it on first access.
func blobsRoot() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(base, "blobs")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return root, nil
}

// sanitizeBlobName strips anything that isn't alphanumeric /
// dash / underscore / dot / slash-in-key — so a crafted key
// like "../../etc/passwd" can never escape the bucket dir.
func sanitizeBlobName(name string) string {
	out := strings.Builder{}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == '/':
			out.WriteRune(r)
		}
	}
	res := out.String()
	res = strings.TrimPrefix(res, "/")
	res = strings.ReplaceAll(res, "..", "")
	return res
}

// blobPath resolves to the on-disk file, performing containment
// checks. Returns (absolute, metaAbs, error).
func blobPath(bucket, key string) (string, string, error) {
	bucket = sanitizeBlobName(bucket)
	key = sanitizeBlobName(key)
	if bucket == "" || key == "" {
		return "", "", errors.New("bucket and key required")
	}
	if strings.Contains(bucket, "/") {
		return "", "", errors.New("bucket cannot contain /")
	}
	root, err := blobsRoot()
	if err != nil {
		return "", "", err
	}
	bucketDir := filepath.Join(root, bucket)
	abs := filepath.Join(bucketDir, key)
	absResolved, err := filepath.Abs(abs)
	if err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(absResolved, bucketDir) {
		return "", "", errors.New("forbidden path")
	}
	meta := absResolved + ".meta.json"
	return absResolved, meta, nil
}

// writeBlob persists bytes + metadata atomically.
func writeBlob(bucket, key, contentType, uploadedBy string, data []byte) (*BlobMetadata, error) {
	abs, metaPath, err := blobPath(bucket, key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if contentType == "" {
		if len(data) > 512 {
			contentType = http.DetectContentType(data[:512])
		} else {
			contentType = http.DetectContentType(data)
		}
	}
	meta := &BlobMetadata{
		Key:         key,
		Size:        int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
		ContentType: contentType,
		UploadedAt:  time.Now().UTC().Format(time.RFC3339),
		UploadedBy:  uploadedBy,
	}

	// Atomic write: tmp + rename so a mid-upload crash never
	// leaves a partial blob next to a stale meta record.
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		return nil, err
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(metaPath, metaBytes, 0600)
	return meta, nil
}

// readBlob returns the bytes + metadata for one blob, or
// (nil, nil, os.ErrNotExist) when the blob is missing.
func readBlob(bucket, key string) ([]byte, *BlobMetadata, error) {
	abs, metaPath, err := blobPath(bucket, key)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	var meta BlobMetadata
	if metaData, merr := os.ReadFile(metaPath); merr == nil {
		_ = json.Unmarshal(metaData, &meta)
	}
	return data, &meta, nil
}

// deleteBlob removes the blob + sibling meta file.
func deleteBlob(bucket, key string) error {
	abs, metaPath, err := blobPath(bucket, key)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(metaPath)
	return nil
}

// listBlobs walks a bucket and returns metadata for every
// non-meta file. Limited to a sane count so a wildly-sized
// bucket doesn't blow the request context.
func listBlobs(bucket string) ([]*BlobMetadata, error) {
	root, err := blobsRoot()
	if err != nil {
		return nil, err
	}
	bucket = sanitizeBlobName(bucket)
	if bucket == "" {
		return nil, errors.New("bucket required")
	}
	dir := filepath.Join(root, bucket)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return []*BlobMetadata{}, nil
		}
		return nil, err
	}
	var out []*BlobMetadata
	err = filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || strings.HasSuffix(info.Name(), ".meta.json") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		meta := &BlobMetadata{Key: rel, Size: info.Size()}
		// Enrich with sibling meta if present.
		if md, rerr := os.ReadFile(path + ".meta.json"); rerr == nil {
			_ = json.Unmarshal(md, meta)
		}
		out = append(out, meta)
		if len(out) >= 10_000 {
			return filepath.SkipDir
		}
		return nil
	})
	return out, err
}

// listBuckets returns the set of top-level bucket directories.
func listBuckets() ([]string, error) {
	root, err := blobsRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// --- HMAC-signed public URLs ----------------------------------------------

// blobSecret returns the persisted HMAC signing key, generating
// a fresh 32-byte random value on first access. Kept in
// ~/.yaver/blobs/.secret so it survives restarts but never
// leaves the machine.
func blobSecret() ([]byte, error) {
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(base, "blobs", ".secret")
	if data, rerr := os.ReadFile(path); rerr == nil && len(data) >= 32 {
		return data, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, buf, 0600); err != nil {
		return nil, err
	}
	return buf, nil
}

// signBlobURL returns a `/blobs/public?...&sig=...` query
// string valid for `ttl` seconds. The signature covers
// (bucket|key|exp) which makes this safe to share publicly.
func signBlobURL(bucket, key string, ttl time.Duration) (string, error) {
	secret, err := blobSecret()
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%s|%s|%d", bucket, key, exp)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf(
		"/blobs/public?b=%s&k=%s&exp=%d&sig=%s",
		bucket, key, exp, sig,
	), nil
}

// verifyBlobSig checks a public URL's HMAC + expiry.
func verifyBlobSig(bucket, key, sig string, exp int64) error {
	if time.Now().Unix() > exp {
		return errors.New("expired")
	}
	secret, err := blobSecret()
	if err != nil {
		return err
	}
	payload := fmt.Sprintf("%s|%s|%d", bucket, key, exp)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("bad signature")
	}
	return nil
}

// --- HTTP handlers --------------------------------------------------------

// handleBlobs dispatches every /blobs/* URL except the public
// fetch endpoint (which has its own handler for unauth'd reads).
func (s *HTTPServer) handleBlobs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/blobs")
	path = strings.TrimPrefix(path, "/")
	parts := strings.SplitN(path, "/", 2)

	blobMu.Lock()
	defer blobMu.Unlock()

	// /blobs  → list buckets
	if path == "" {
		if r.Method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		buckets, err := listBuckets()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "buckets": buckets})
		return
	}

	// /blobs/url/<bucket>/<key>  → sign a public URL
	if strings.HasPrefix(path, "url/") {
		rest := strings.TrimPrefix(path, "url/")
		rp := strings.SplitN(rest, "/", 2)
		if len(rp) != 2 {
			jsonError(w, http.StatusBadRequest, "url/<bucket>/<key>")
			return
		}
		ttl := 300 * time.Second
		if t := r.URL.Query().Get("ttl"); t != "" {
			if sec, err := strconv.Atoi(t); err == nil {
				ttl = time.Duration(sec) * time.Second
			}
		}
		signed, err := signBlobURL(rp[0], rp[1], ttl)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"url":       signed,
			"expiresIn": int(ttl.Seconds()),
		})
		return
	}

	// /blobs/<bucket>                → list keys (GET)
	// /blobs/<bucket>/<key>          → get / put / delete
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		all, err := listBlobs(parts[0])
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Sort deterministically so ?after=<key> is a stable cursor.
		sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })

		limit := 500
		if q := r.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 5000 {
				limit = n
			}
		}
		after := r.URL.Query().Get("after")
		start := 0
		if after != "" {
			// Advance past the cursor. Linear scan is fine — the
			// pagination gets invoked per-page, and we've already
			// walked the whole directory on the line above.
			for i, m := range all {
				if m.Key > after {
					start = i
					break
				}
				// If after matches an existing key, start after it.
				if m.Key == after {
					start = i + 1
				}
			}
		}
		end := start + limit
		if end > len(all) {
			end = len(all)
		}
		page := all[start:end]
		nextCursor := ""
		if end < len(all) {
			nextCursor = page[len(page)-1].Key
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":         true,
			"bucket":     parts[0],
			"keys":       page,      // preferred
			"items":      page,      // back-compat
			"nextCursor": nextCursor, // empty when no more pages
			"total":      len(all),
		})
		return
	}

	bucket := parts[0]
	key := parts[1]
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}
		ct := r.Header.Get("Content-Type")
		meta, werr := writeBlob(bucket, key, ct, r.Header.Get("X-Yaver-Token-Hash"), data)
		if werr != nil {
			jsonError(w, http.StatusInternalServerError, werr.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{
			"ok":   true,
			"blob": meta,
		})
	case http.MethodGet:
		data, meta, err := readBlob(bucket, key)
		if err != nil {
			if os.IsNotExist(err) {
				jsonError(w, http.StatusNotFound, "blob not found")
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if meta != nil && meta.ContentType != "" {
			w.Header().Set("Content-Type", meta.ContentType)
		}
		w.Header().Set("X-Yaver-Blob-SHA256", meta.SHA256)
		w.Write(data)
	case http.MethodDelete:
		if err := deleteBlob(bucket, key); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "PUT/GET/DELETE")
	}
}

// handleBlobPublic serves a signed blob without auth. The HMAC
// is checked before anything else so an expired / invalid URL
// never reads disk.
func (s *HTTPServer) handleBlobPublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	bucket := q.Get("b")
	key := q.Get("k")
	sig := q.Get("sig")
	expRaw := q.Get("exp")
	exp, _ := strconv.ParseInt(expRaw, 10, 64)
	if bucket == "" || key == "" || sig == "" || exp == 0 {
		http.Error(w, "missing params", http.StatusBadRequest)
		return
	}
	if err := verifyBlobSig(bucket, key, sig, exp); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	data, meta, err := readBlob(bucket, key)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if meta != nil && meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(data)
}

// --- CLI ------------------------------------------------------------------

func runBlob(args []string) {
	if len(args) == 0 {
		printBlobUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "put":
		blobPutCmd(args[1:])
	case "get":
		blobGetCmd(args[1:])
	case "list", "ls":
		blobListCmd(args[1:])
	case "delete", "rm":
		blobDeleteCmd(args[1:])
	case "url":
		blobURLCmd(args[1:])
	case "help", "--help", "-h":
		printBlobUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown blob subcommand: %s\n\n", args[0])
		printBlobUsage()
		os.Exit(1)
	}
}

func printBlobUsage() {
	fmt.Print(`Yaver blob — local object storage with HMAC-signed URLs.

Usage:
  yaver blob put <file> --bucket <b> --key <k> [--type <mime>]
  yaver blob get <bucket>/<key> [-o out.ext]
  yaver blob list [<bucket>]
  yaver blob delete <bucket>/<key>
  yaver blob url <bucket>/<key> [--ttl 300s]

Blobs live under ~/.yaver/blobs/<bucket>/<key> with a sibling
.meta.json. Public URLs are HMAC-signed with a per-agent
secret in ~/.yaver/blobs/.secret and never leave this machine.
Point your app at /blobs/public?b=...&k=...&exp=...&sig=... and
serve it through the existing P2P relay for $0/mo of S3.
`)
}

func blobPutCmd(args []string) {
	fs := flag.NewFlagSet("blob put", flag.ExitOnError)
	bucket := fs.String("bucket", "", "target bucket")
	key := fs.String("key", "", "target key")
	contentType := fs.String("type", "", "override content-type")
	fs.Parse(args)
	if fs.NArg() < 1 || *bucket == "" || *key == "" {
		fmt.Fprintln(os.Stderr, "usage: yaver blob put <file> --bucket <b> --key <k>")
		os.Exit(1)
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
	meta, err := writeBlob(*bucket, *key, *contentType, "cli", data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "put: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s/%s  %d bytes  %s  %s\n",
		*bucket, *key, meta.Size, meta.ContentType, meta.SHA256[:12])
}

func blobGetCmd(args []string) {
	fs := flag.NewFlagSet("blob get", flag.ExitOnError)
	out := fs.String("o", "", "output path (default: stdout)")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver blob get <bucket>/<key> [-o out]")
		os.Exit(1)
	}
	parts := strings.SplitN(fs.Arg(0), "/", 2)
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "expected <bucket>/<key>")
		os.Exit(1)
	}
	data, _, err := readBlob(parts[0], parts[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "get: %v\n", err)
		os.Exit(1)
	}
	if *out == "" {
		os.Stdout.Write(data)
	} else {
		_ = os.WriteFile(*out, data, 0600)
		fmt.Printf("✓ wrote %s (%d bytes)\n", *out, len(data))
	}
}

func blobListCmd(args []string) {
	if len(args) == 0 {
		buckets, _ := listBuckets()
		if len(buckets) == 0 {
			fmt.Println("No buckets yet.")
			return
		}
		for _, b := range buckets {
			fmt.Println(b)
		}
		return
	}
	items, err := listBlobs(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "list: %v\n", err)
		os.Exit(1)
	}
	for _, it := range items {
		fmt.Printf("  %s  %d bytes  %s\n", it.Key, it.Size, it.ContentType)
	}
}

func blobDeleteCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver blob delete <bucket>/<key>")
		os.Exit(1)
	}
	parts := strings.SplitN(args[0], "/", 2)
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "expected <bucket>/<key>")
		os.Exit(1)
	}
	if err := deleteBlob(parts[0], parts[1]); err != nil {
		fmt.Fprintf(os.Stderr, "delete: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ removed %s\n", args[0])
}

func blobURLCmd(args []string) {
	fs := flag.NewFlagSet("blob url", flag.ExitOnError)
	ttl := fs.Duration("ttl", 5*time.Minute, "signed URL validity")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver blob url <bucket>/<key> [--ttl 5m]")
		os.Exit(1)
	}
	parts := strings.SplitN(fs.Arg(0), "/", 2)
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "expected <bucket>/<key>")
		os.Exit(1)
	}
	signed, err := signBlobURL(parts[0], parts[1], *ttl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "url: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(signed)
}
