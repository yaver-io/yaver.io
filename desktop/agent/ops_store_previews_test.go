package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePreviewASC is a REAL HTTP server speaking enough App Store Connect to drive
// the app-preview verbs end to end (repo convention: no mocks). It is deliberately
// its own server rather than an extension of newFakeASC — previews own their
// routes, and the screenshot harness stays untouched.
type fakePreviewASC struct {
	t   *testing.T
	mu  sync.Mutex
	srv *httptest.Server

	previewSetsJSON string
	previewsJSON    string

	// transcodeStates is consumed one entry per GET /appPreviews/{id}, so a test
	// can make Apple take its time before answering COMPLETE or FAILED. The last
	// entry sticks once the list is exhausted.
	transcodeStates []string
	transcodeErrors string // JSON array for assetDeliveryState.errors when FAILED

	calls          []string
	uploadChunks   [][]byte
	uploadHeaders  []http.Header
	commitAttrs    map[string]interface{}
	setsCreated    int
	deletedPreview []string
	stateReads     int
}

func newFakePreviewASC(t *testing.T) *fakePreviewASC {
	t.Helper()
	f := &fakePreviewASC{
		t:               t,
		previewSetsJSON: `[]`,
		previewsJSON:    `[]`,
		transcodeStates: []string{assetStateComplete},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.route))
	t.Cleanup(f.srv.Close)

	oldBase, oldUp, oldNew, oldPoll := ascAPIBase, ascUploadClient, newASCClientFn, ascPreviewPollInterval
	ascAPIBase = f.srv.URL
	ascUploadClient = f.srv.Client()
	ascPreviewPollInterval = time.Millisecond // don't actually wait on Apple
	creds := testASCCreds(t)
	newASCClientFn = func(project string) (*ascClient, error) {
		return &ascClient{creds: creds, http: f.srv.Client()}, nil
	}
	t.Cleanup(func() {
		ascAPIBase, ascUploadClient, newASCClientFn, ascPreviewPollInterval = oldBase, oldUp, oldNew, oldPoll
	})
	return f
}

func (f *fakePreviewASC) client() *ascClient {
	cl, err := newASCClientFn("")
	if err != nil {
		f.t.Fatalf("client: %v", err)
	}
	return cl
}

func (f *fakePreviewASC) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakePreviewASC) called(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == s {
			return true
		}
	}
	return false
}

// nextState pops the next transcode state, sticking on the last one.
func (f *fakePreviewASC) nextState() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateReads++
	if len(f.transcodeStates) == 0 {
		return assetStateComplete
	}
	if f.stateReads-1 < len(f.transcodeStates) {
		return f.transcodeStates[f.stateReads-1]
	}
	return f.transcodeStates[len(f.transcodeStates)-1]
}

func (f *fakePreviewASC) route(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	// Apple's asset host: different host, Apple's own headers, and the ASC JWT
	// must NOT be sent there.
	if strings.HasPrefix(p, "/upload/") {
		if auth := r.Header.Get("Authorization"); auth != "" {
			f.t.Errorf("preview upload op must not carry the ASC bearer, got %q", auth)
		}
		chunk, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.uploadChunks = append(f.uploadChunks, chunk)
		f.uploadHeaders = append(f.uploadHeaders, r.Header.Clone())
		f.mu.Unlock()
		f.record(r.Method + " " + p)
		w.WriteHeader(200)
		return
	}

	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		f.t.Errorf("missing ASC bearer on %s %s", r.Method, p)
	}
	f.record(r.Method + " " + p)
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == "GET" && p == "/apps":
		fmt.Fprint(w, `{"data":[{"id":"app-1","attributes":{"name":"Acme","bundleId":"com.acme.app","sku":"ACME"}}]}`)

	case r.Method == "GET" && p == "/apps/app-1/appStoreVersions":
		fmt.Fprint(w, `{"data":[{"id":"ver-1","attributes":{"versionString":"1.0","platform":"VISION_OS","appStoreState":"PREPARE_FOR_SUBMISSION"}}]}`)

	case r.Method == "GET" && p == "/appStoreVersions/ver-1/appStoreVersionLocalizations":
		fmt.Fprint(w, `{"data":[{"id":"loc-1","attributes":{"locale":"en-US"}}]}`)

	case r.Method == "GET" && p == "/appStoreVersionLocalizations/loc-1/appPreviewSets":
		fmt.Fprintf(w, `{"data":%s}`, f.previewSetsJSON)

	case r.Method == "POST" && p == "/appPreviewSets":
		attrs := decodeAttrs(f.t, r)
		f.setsCreated++
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"data":{"id":"pset-1","attributes":{"previewType":%q}}}`, attrs["previewType"])

	case r.Method == "GET" && p == "/appPreviewSets/pset-1/appPreviews":
		fmt.Fprintf(w, `{"data":%s}`, f.previewsJSON)

	case r.Method == "POST" && p == "/appPreviews":
		attrs := decodeAttrs(f.t, r)
		size, _ := attrs["fileSize"].(float64)
		if size <= 0 {
			f.t.Errorf("reserve must declare fileSize, got %v", attrs["fileSize"])
		}
		if attrs["fileName"] == nil || attrs["fileName"] == "" {
			f.t.Errorf("reserve must declare fileName, got %v", attrs["fileName"])
		}
		// Two operations on purpose: proves offset/length slicing of the file.
		half := int(size) / 2
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"data":{"id":"prev-1","attributes":{"uploadOperations":[
			{"method":"PUT","url":%q,"offset":0,"length":%d,"requestHeaders":[{"name":"X-Apple-Part","value":"1"}]},
			{"method":"PUT","url":%q,"offset":%d,"length":%d,"requestHeaders":[{"name":"X-Apple-Part","value":"2"}]}
		]}}}`,
			f.srv.URL+"/upload/a", half,
			f.srv.URL+"/upload/b", half, int(size)-half)

	case r.Method == "PATCH" && p == "/appPreviews/prev-1":
		f.commitAttrs = decodeAttrs(f.t, r)
		// The commit 200 means "Apple has the bytes" — NOT "Apple accepted them".
		fmt.Fprint(w, `{"data":{"id":"prev-1","attributes":{"fileName":"preview.mov",
			"assetDeliveryState":{"state":"UPLOAD_COMPLETE"}}}}`)

	case r.Method == "GET" && p == "/appPreviews/prev-1":
		state := f.nextState()
		errs := f.transcodeErrors
		if errs == "" {
			errs = `[]`
		}
		video := ""
		if state == assetStateComplete {
			video = `"videoUrl":"https://apple.example/preview.mov",`
		}
		fmt.Fprintf(w, `{"data":{"id":"prev-1","attributes":{"fileName":"preview.mov",%s
			"assetDeliveryState":{"state":%q,"errors":%s}}}}`, video, state, errs)

	case r.Method == "DELETE" && strings.HasPrefix(p, "/appPreviews/"):
		f.deletedPreview = append(f.deletedPreview, strings.TrimPrefix(p, "/appPreviews/"))
		w.WriteHeader(204)

	default:
		f.t.Errorf("unexpected %s %s", r.Method, p)
		w.WriteHeader(404)
	}
}

// writeTempVideo writes n bytes with a video-ish extension. Content does not
// matter — the fake never decodes it, and preflight is stubbed per-test.
func writeTempVideo(t *testing.T, name string, n int) (string, []byte) {
	t.Helper()
	data := make([]byte, n)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write video: %v", err)
	}
	return path, data
}

// noFFprobe makes the local preflight a no-op (Apple becomes the only judge),
// which is what we want for the pure upload-sequence tests.
func noFFprobe(t *testing.T) {
	t.Helper()
	old := ffprobeLookPath
	ffprobeLookPath = func() (string, error) { return "", fmt.Errorf("not installed") }
	t.Cleanup(func() { ffprobeLookPath = old })
}

// fakeFFprobe installs a REAL executable that prints canned ffprobe JSON — a
// real subprocess, not a mocked function.
func fakeFFprobe(t *testing.T, width, height int, codec, frameRate, duration string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "ffprobe")
	body := fmt.Sprintf(`#!/bin/sh
cat <<'EOF'
{"streams":[{"width":%d,"height":%d,"codec_name":"%s","avg_frame_rate":"%s"}],
 "format":{"duration":"%s"}}
EOF
`, width, height, codec, frameRate, duration)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	old := ffprobeLookPath
	ffprobeLookPath = func() (string, error) { return script, nil }
	t.Cleanup(func() { ffprobeLookPath = old })
}

// ── the upload contract: reserve → uploadOperations → commit ──

func TestASCPreviewUploadSequence(t *testing.T) {
	f := newFakePreviewASC(t)
	path, data := writeTempVideo(t, "preview.mov", 11) // odd size → uneven chunks

	prev, err := f.client().UploadPreview("pset-1", path, "00:00:03:00")
	if err != nil {
		t.Fatalf("UploadPreview: %v", err)
	}
	if prev.ID != "prev-1" {
		t.Fatalf("preview = %+v", prev)
	}

	// Every uploadOperation ran, in order, slicing the ORIGINAL file bytes.
	if len(f.uploadChunks) != 2 {
		t.Fatalf("want 2 upload operations, got %d", len(f.uploadChunks))
	}
	joined := append(append([]byte{}, f.uploadChunks[0]...), f.uploadChunks[1]...)
	if string(joined) != string(data) {
		t.Fatalf("reassembled upload = %q, want %q", joined, data)
	}
	if len(f.uploadChunks[0]) != 5 || len(f.uploadChunks[1]) != 6 {
		t.Fatalf("chunk sizes = %d/%d, want 5/6 (offset+length must slice the file)",
			len(f.uploadChunks[0]), len(f.uploadChunks[1]))
	}
	// Apple's own requestHeaders, applied verbatim.
	if got := f.uploadHeaders[0].Get("X-Apple-Part"); got != "1" {
		t.Errorf("apple request header not applied: %q", got)
	}

	// Commit carries uploaded:true + md5 of the WHOLE file + the poster frame.
	if f.commitAttrs["uploaded"] != true {
		t.Errorf("commit uploaded = %v", f.commitAttrs["uploaded"])
	}
	sum := md5.Sum(data)
	if want := hex.EncodeToString(sum[:]); f.commitAttrs["sourceFileChecksum"] != want {
		t.Errorf("sourceFileChecksum = %v, want md5 of whole file %s", f.commitAttrs["sourceFileChecksum"], want)
	}
	if f.commitAttrs["previewFrameTimeCode"] != "00:00:03:00" {
		t.Errorf("previewFrameTimeCode = %v", f.commitAttrs["previewFrameTimeCode"])
	}
}

// ── THE money path: Apple fails the transcode ──

// A commit 200 does NOT mean the video was accepted. When Apple's transcoder
// fails it, the verb must FAIL and reproduce Apple's errors verbatim — never
// report the asset as uploaded.
func TestStorePreviewsSetSurfacesTranscodeFailureVerbatim(t *testing.T) {
	f := newFakePreviewASC(t)
	noFFprobe(t)
	f.transcodeStates = []string{assetStateUploadComplete, assetStateFailed}
	f.transcodeErrors = `[{"code":"ASSET_VALIDATION_FAILED",
		"description":"The video resolution 1920x1080 does not match the required 3840x2160 for Apple Vision Pro."}]`

	path, _ := writeTempVideo(t, "preview.mov", 12)
	payload, _ := json.Marshal(map[string]interface{}{
		"bundleId":    "com.acme.app",
		"platform":    "visionos",
		"previewType": "APP_APPLE_VISION_PRO",
		"files":       []string{path},
	})

	res := storePreviewsSetHandler(OpsContext{}, payload)
	if res.OK {
		t.Fatal("Apple FAILED the transcode — the verb must not report success")
	}
	if res.Code != "apple_rejected" {
		t.Errorf("code = %q, want apple_rejected", res.Code)
	}
	// Apple's own words, verbatim.
	if !strings.Contains(res.Error, "does not match the required 3840x2160") {
		t.Errorf("Apple's description must survive verbatim, got:\n%s", res.Error)
	}
	if !strings.Contains(res.Error, "ASSET_VALIDATION_FAILED") {
		t.Errorf("Apple's error code must survive, got:\n%s", res.Error)
	}
	// And we must say plainly that we will not re-encode.
	if !strings.Contains(res.Error, "does NOT re-encode") {
		t.Errorf("must state we never silently re-encode, got:\n%s", res.Error)
	}

	init := opsInitial(t, res)
	if init["uploadedCount"] != 0 {
		t.Errorf("a rejected asset must NEVER be counted as uploaded, got %v", init["uploadedCount"])
	}
	failed, _ := init["failed"].([]map[string]interface{})
	if len(failed) != 1 {
		t.Fatalf("failed = %#v", init["failed"])
	}
	errs, _ := failed[0]["errors"].([]ascAssetError)
	if len(errs) != 1 || errs[0].Code != "ASSET_VALIDATION_FAILED" {
		t.Errorf("structured Apple errors missing: %#v", failed[0]["errors"])
	}
}

// A COMPLETE transcode is the only thing that counts as uploaded.
func TestStorePreviewsSetCompletes(t *testing.T) {
	f := newFakePreviewASC(t)
	noFFprobe(t)
	f.transcodeStates = []string{assetStateUploadComplete, assetStateComplete}

	path, _ := writeTempVideo(t, "preview.mov", 10)
	payload, _ := json.Marshal(map[string]interface{}{
		"bundleId":    "com.acme.app",
		"platform":    "VISION_OS",
		"previewType": "APP_APPLE_VISION_PRO",
		"files":       []string{path},
	})

	res := storePreviewsSetHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("preview upload failed: %s", res.Error)
	}
	if !f.called("POST /appPreviewSets") || !f.called("POST /appPreviews") || !f.called("PATCH /appPreviews/prev-1") {
		t.Fatalf("must be create-set → reserve → commit; calls = %v", f.calls)
	}
	init := opsInitial(t, res)
	if init["uploadedCount"] != 1 {
		t.Errorf("uploadedCount = %v, want 1", init["uploadedCount"])
	}
	uploaded, _ := init["uploaded"].([]map[string]interface{})
	if len(uploaded) != 1 || uploaded[0]["state"] != assetStateComplete {
		t.Fatalf("uploaded = %#v", init["uploaded"])
	}
	if uploaded[0]["videoUrl"] != "https://apple.example/preview.mov" {
		t.Errorf("videoUrl not surfaced: %#v", uploaded[0])
	}
	if _, hasFailed := init["failed"]; hasFailed {
		t.Error("nothing failed — there must be no failed list")
	}
}

// Still transcoding when the budget runs out is PENDING — not done, not failed.
func TestStorePreviewsSetPendingIsNotClaimedDone(t *testing.T) {
	f := newFakePreviewASC(t)
	noFFprobe(t)
	f.transcodeStates = []string{assetStateUploadComplete} // never resolves

	path, _ := writeTempVideo(t, "preview.mov", 10)
	payload, _ := json.Marshal(map[string]interface{}{
		"bundleId":    "com.acme.app",
		"platform":    "VISION_OS",
		"previewType": "APP_APPLE_VISION_PRO",
		"files":       []string{path},
		"waitSeconds": 1,
	})

	res := storePreviewsSetHandler(OpsContext{}, payload)
	init := opsInitial(t, res)
	if init["uploadedCount"] != 0 {
		t.Errorf("a still-transcoding preview must not be counted as uploaded, got %v", init["uploadedCount"])
	}
	pending, _ := init["pending"].([]map[string]interface{})
	if len(pending) != 1 || pending[0]["state"] != assetStateUploadComplete {
		t.Fatalf("pending = %#v", init["pending"])
	}
	note, _ := init["note"].(string)
	if !strings.Contains(note, "STILL TRANSCODING") || !strings.Contains(note, "NOT accepted yet") {
		t.Errorf("must say plainly that Apple has not accepted it yet, got: %q", note)
	}
}

func TestStorePreviewsSetReusesSetAndReplaces(t *testing.T) {
	f := newFakePreviewASC(t)
	noFFprobe(t)
	f.previewSetsJSON = `[{"id":"pset-1","attributes":{"previewType":"APP_APPLE_VISION_PRO"}}]`
	f.previewsJSON = `[{"id":"old-1","attributes":{"fileName":"old.mov"}}]`

	path, _ := writeTempVideo(t, "preview.mov", 8)
	payload, _ := json.Marshal(map[string]interface{}{
		"bundleId":    "com.acme.app",
		"platform":    "VISION_OS",
		"previewType": "APP_APPLE_VISION_PRO",
		"files":       []string{path},
		"replace":     true,
	})

	res := storePreviewsSetHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("handler failed: %s", res.Error)
	}
	if f.setsCreated != 0 {
		t.Errorf("must REUSE the existing set for the preview type, but created %d", f.setsCreated)
	}
	if len(f.deletedPreview) != 1 || f.deletedPreview[0] != "old-1" {
		t.Errorf("replace:true must delete existing previews first, deleted = %v", f.deletedPreview)
	}
	if init := opsInitial(t, res); init["deleted"] != 1 {
		t.Errorf("deleted = %v, want 1", init["deleted"])
	}
}

// ── local preflight: refuse, never re-encode ──

func TestPreflightPreviewRejectsBadDuration(t *testing.T) {
	fakeFFprobe(t, 3840, 2160, "h264", "30/1", "42.0") // Apple's window is 15-30s
	path, _ := writeTempVideo(t, "preview.mov", 8)

	probe, err := preflightPreview(path, "APP_APPLE_VISION_PRO")
	if err == nil {
		t.Fatal("a 42s preview must be refused — Apple rejects anything outside 15-30s")
	}
	if !strings.Contains(err.Error(), "42.0s") || !strings.Contains(err.Error(), "15 and 30") {
		t.Errorf("error must name the real duration and the real limit: %v", err)
	}
	if !strings.Contains(err.Error(), "will not silently re-encode") {
		t.Errorf("must promise not to re-encode: %v", err)
	}
	if probe == nil || probe.Duration != 42.0 {
		t.Errorf("probe facts must be reported back: %#v", probe)
	}
}

func TestPreflightPreviewRejectsWrongVisionProSize(t *testing.T) {
	fakeFFprobe(t, 1920, 1080, "h264", "30/1", "20.0")
	path, _ := writeTempVideo(t, "preview.mov", 8)

	_, err := preflightPreview(path, "APP_APPLE_VISION_PRO")
	if err == nil {
		t.Fatal("1920x1080 must be refused for Apple Vision Pro (needs 3840x2160)")
	}
	if !strings.Contains(err.Error(), "1920x1080") || !strings.Contains(err.Error(), "3840x2160") {
		t.Errorf("error must name BOTH the actual and the required size: %v", err)
	}
}

func TestPreflightPreviewAcceptsGoodVisionProVideo(t *testing.T) {
	fakeFFprobe(t, 3840, 2160, "h264", "30000/1001", "20.0")
	path, _ := writeTempVideo(t, "preview.mov", 8)

	probe, err := preflightPreview(path, "APP_APPLE_VISION_PRO")
	if err != nil {
		t.Fatalf("a spec-compliant preview must pass: %v", err)
	}
	if probe.Width != 3840 || probe.Height != 2160 {
		t.Errorf("probe = %#v", probe)
	}
	if probe.FPS < 29.9 || probe.FPS > 30.0 {
		t.Errorf("fps = %v, want ~29.97 from 30000/1001", probe.FPS)
	}
}

// An unknown preview type is NOT size-checked locally — Apple stays the
// authority rather than us guessing a size and rejecting a valid video.
func TestPreflightPreviewDoesNotGuessUnknownTypeSizes(t *testing.T) {
	fakeFFprobe(t, 1234, 5678, "h264", "30/1", "20.0")
	path, _ := writeTempVideo(t, "preview.mov", 8)

	if _, err := preflightPreview(path, "IPHONE_67"); err != nil {
		t.Fatalf("we have no verified size for IPHONE_67 — must not invent one and reject: %v", err)
	}
}

func TestPreflightPreviewRejectsNonVideoExtension(t *testing.T) {
	noFFprobe(t)
	path, _ := writeTempVideo(t, "preview.gif", 8)
	if _, err := preflightPreview(path, "IPHONE_67"); err == nil {
		t.Fatal(".gif must be refused — Apple accepts .mov/.m4v/.mp4")
	}
}

// No ffprobe is not a failure: we say nothing we cannot check, and let Apple judge.
func TestPreflightPreviewWithoutFFprobeIsNotAnError(t *testing.T) {
	noFFprobe(t)
	path, _ := writeTempVideo(t, "preview.mov", 8)
	probe, err := preflightPreview(path, "APP_APPLE_VISION_PRO")
	if err != nil {
		t.Fatalf("missing ffprobe must not fail the upload: %v", err)
	}
	if probe != nil {
		t.Errorf("no ffprobe → no probe facts, got %#v", probe)
	}
}

func TestParseFFRational(t *testing.T) {
	cases := map[string]float64{"30/1": 30, "30000/1001": 29.97003, "0/0": 0, "60": 60, "": 0}
	for in, want := range cases {
		got := parseFFRational(in)
		if diff := got - want; diff > 0.001 || diff < -0.001 {
			t.Errorf("parseFFRational(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStorePreviewsSetRequiresTypeAndFiles(t *testing.T) {
	newFakePreviewASC(t)
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "files": []string{"/x.mov"}})
	if res := storePreviewsSetHandler(OpsContext{}, payload); res.OK || res.Code != "bad_payload" {
		t.Errorf("missing previewType must be a bad_payload, got %+v", res)
	}
	payload, _ = json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "previewType": "IPHONE_67"})
	if res := storePreviewsSetHandler(OpsContext{}, payload); res.OK || res.Code != "bad_payload" {
		t.Errorf("missing files must be a bad_payload, got %+v", res)
	}
}
