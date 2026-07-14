package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakePlay is a REAL HTTP server that speaks enough Android Publisher v3 to
// drive the Play submission verbs end to end (repo convention: no mocks). It
// serves BOTH the JSON API and the separate `/upload/` media host, because the
// real API does too — routing them from one server is how we prove the verbs
// aim the upload at the right path.
type fakePlay struct {
	t  *testing.T
	mu sync.Mutex

	srv *httptest.Server

	// canned responses
	detailsJSON  string
	listingsJSON string
	imagesJSON   map[string]string // imageType -> {"images":[…]}
	bundlesJSON  string
	tracksJSON   string
	trackJSON    map[string]string // track -> Track object

	// failure injection: "METHOD /suffix" -> (status, body)
	failStatus map[string]int
	failBody   map[string]string

	// recorded
	calls        []string
	editsOpened  int
	editsDeleted []string
	validated    []string
	commits      []string // editId, with "?review" / "?noreview" suffix
	patchedList  map[string]interface{}
	putTrack     map[string]interface{}
	uploads      []fakeUpload
	deletedAll   []string
}

type fakeUpload struct {
	Path        string
	ContentType string
	Body        []byte
}

func newFakePlay(t *testing.T) *fakePlay {
	t.Helper()
	f := &fakePlay{
		t:            t,
		detailsJSON:  `{"defaultLanguage":"en-US","contactEmail":"dev@example.com"}`,
		listingsJSON: `{"listings":[]}`,
		imagesJSON:   map[string]string{},
		bundlesJSON:  `{"bundles":[]}`,
		tracksJSON:   `{"tracks":[]}`,
		trackJSON:    map[string]string{},
		failStatus:   map[string]int{},
		failBody:     map[string]string{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.route))
	t.Cleanup(f.srv.Close)

	oldAPI, oldUp, oldClient, oldNew := playAPIBase, playUploadBase, playUploadClient, newPlayClientFn
	playAPIBase = f.srv.URL
	playUploadBase = f.srv.URL + "/upload"
	playUploadClient = f.srv.Client()
	newPlayClientFn = func(project, pkg string) (*playClient, error) {
		return &playClient{pkg: pkg, token: "test-token", http: f.srv.Client()}, nil
	}
	t.Cleanup(func() {
		playAPIBase, playUploadBase, playUploadClient, newPlayClientFn = oldAPI, oldUp, oldClient, oldNew
	})
	return f
}

func (f *fakePlay) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakePlay) called(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == s {
			return true
		}
	}
	return false
}

// fail makes the next matching request return an error. key is "METHOD suffix".
func (f *fakePlay) fail(key string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failStatus[key] = status
	f.failBody[key] = body
}

// maybeFail applies an injected failure, returning true when it fired.
func (f *fakePlay) maybeFail(w http.ResponseWriter, r *http.Request) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, status := range f.failStatus {
		parts := strings.SplitN(key, " ", 2)
		if len(parts) != 2 {
			continue
		}
		if r.Method == parts[0] && strings.Contains(r.URL.Path, parts[1]) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(f.failBody[key]))
			return true
		}
	}
	return false
}

func (f *fakePlay) route(w http.ResponseWriter, r *http.Request) {
	// Every call must carry the bearer — and nothing else may leak it.
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		f.t.Errorf("bad auth on %s %s: %q", r.Method, r.URL.Path, got)
	}
	p := r.URL.Path
	f.record(r.Method + " " + p)

	if f.maybeFail(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")

	// ---- media upload host ----
	if strings.HasPrefix(p, "/upload/") {
		body, _ := readAllLimit(r)
		f.mu.Lock()
		f.uploads = append(f.uploads, fakeUpload{Path: p, ContentType: r.Header.Get("Content-Type"), Body: body})
		f.mu.Unlock()
		if r.URL.Query().Get("uploadType") != "media" {
			f.t.Errorf("upload without uploadType=media: %s", r.URL.String())
		}
		switch {
		case strings.HasSuffix(p, "/bundles"):
			_, _ = w.Write([]byte(`{"versionCode":812,"sha256":"abc123"}`))
		default: // images
			_, _ = w.Write([]byte(`{"image":{"id":"img-1","url":"https://play/img","sha256":"deadbeef"}}`))
		}
		return
	}

	switch {
	// ---- edit lifecycle ----
	case r.Method == "POST" && strings.HasSuffix(p, "/edits"):
		f.mu.Lock()
		f.editsOpened++
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"edit-1","expiryTimeSeconds":"1800"}`))

	case r.Method == "POST" && strings.HasSuffix(p, ":validate"):
		f.mu.Lock()
		f.validated = append(f.validated, "edit-1")
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"edit-1"}`))

	case r.Method == "POST" && strings.HasSuffix(p, ":commit"):
		tag := "?review"
		if r.URL.Query().Get("changesNotSentForReview") == "true" {
			tag = "?noreview"
		}
		f.mu.Lock()
		f.commits = append(f.commits, "edit-1"+tag)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"edit-1"}`))

	case r.Method == "DELETE" && strings.HasSuffix(p, "/edits/edit-1"):
		f.mu.Lock()
		f.editsDeleted = append(f.editsDeleted, "edit-1")
		f.mu.Unlock()
		w.WriteHeader(204)

	// ---- details / listings ----
	case r.Method == "GET" && strings.HasSuffix(p, "/details"):
		_, _ = w.Write([]byte(f.detailsJSON))

	case r.Method == "GET" && strings.HasSuffix(p, "/listings"):
		_, _ = w.Write([]byte(f.listingsJSON))

	case r.Method == "PATCH" && strings.Contains(p, "/listings/"):
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.patchedList = body
		f.mu.Unlock()
		out, _ := json.Marshal(body)
		_, _ = w.Write(out)

	// ---- images: /listings/{lang}/{imageType} ----
	case r.Method == "GET" && playIsImagePath(p):
		it := imageTypeOf(p)
		if j, ok := f.imagesJSON[it]; ok {
			_, _ = w.Write([]byte(j))
			return
		}
		_, _ = w.Write([]byte(`{"images":[]}`))

	case r.Method == "DELETE" && playIsImagePath(p):
		it := imageTypeOf(p)
		f.mu.Lock()
		f.deletedAll = append(f.deletedAll, it)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"deleted":[{"id":"old-1"}]}`))

	// ---- bundles / tracks ----
	case r.Method == "GET" && strings.HasSuffix(p, "/bundles"):
		_, _ = w.Write([]byte(f.bundlesJSON))

	case r.Method == "GET" && strings.HasSuffix(p, "/tracks"):
		_, _ = w.Write([]byte(f.tracksJSON))

	case r.Method == "GET" && strings.Contains(p, "/tracks/"):
		tr := playLastSeg(p)
		if j, ok := f.trackJSON[tr]; ok {
			_, _ = w.Write([]byte(j))
			return
		}
		_, _ = w.Write([]byte(`{"track":"` + tr + `","releases":[]}`))

	case r.Method == "PUT" && strings.Contains(p, "/tracks/"):
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.putTrack = body
		f.mu.Unlock()
		out, _ := json.Marshal(body)
		_, _ = w.Write(out)

	default:
		f.t.Errorf("unexpected %s %s", r.Method, p)
		w.WriteHeader(404)
	}
}

func readAllLimit(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
		if len(buf) > 1<<20 {
			return buf, nil
		}
	}
}

// playIsImagePath reports whether p is /listings/{lang}/{imageType} (3 trailing
// segments after "listings"), as opposed to /listings or /listings/{lang}.
func playIsImagePath(p string) bool {
	i := strings.Index(p, "/listings/")
	if i < 0 {
		return false
	}
	rest := strings.Trim(p[i+len("/listings/"):], "/")
	return len(strings.Split(rest, "/")) == 2
}

func imageTypeOf(p string) string { return playLastSeg(p) }

func playLastSeg(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	return parts[len(parts)-1]
}

func mustPayload(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// initial pulls the OpsResult's Initial map.
func initialMap(t *testing.T, res OpsResult) map[string]interface{} {
	t.Helper()
	m, ok := res.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("Initial is not a map: %#v", res.Initial)
	}
	return m
}

func playWritePNG(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	// Real PNG magic — the verbs only check the extension, but a byte-accurate
	// file means the upload assertions compare something meaningful.
	if err := os.WriteFile(p, []byte("\x89PNG\r\n\x1a\n"+name), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// ─────────────────────────── play_submit_status ───────────────────────────

func TestPlaySubmitStatusReportsMissingAndConsoleGates(t *testing.T) {
	newFakePlay(t)
	// Empty app: no listing, no images, no bundle, no track.
	res := playSubmitStatusHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
	}))
	if !res.OK {
		t.Fatalf("status failed: %s", res.Error)
	}
	m := initialMap(t, res)
	if m["ready"] != false {
		t.Fatalf("expected ready=false, got %v", m["ready"])
	}

	missing := fmt.Sprint(m["missing"])
	for _, want := range []string{"title", "shortDescription", "fullDescription", "icon", "featureGraphic", "phoneScreenshots", "app bundle", "production track"} {
		if !strings.Contains(missing, want) {
			t.Errorf("missing[] does not mention %q: %s", want, missing)
		}
	}

	// Console-only gates must be named, and must NOT be claimed as done.
	gates, ok := m["consoleOnly"].([]playGate)
	if !ok || len(gates) == 0 {
		t.Fatalf("expected consoleOnly gates, got %#v", m["consoleOnly"])
	}
	names := map[string]playGate{}
	for _, g := range gates {
		names[g.Gate] = g
	}
	for _, want := range []string{"content_rating", "app_content_declarations", "first_release_on_draft_app", "per_email_testers"} {
		if _, ok := names[want]; !ok {
			t.Errorf("consoleOnly missing gate %q (have %v)", want, names)
		}
	}
	// Content rating cannot even be READ over the API — it must say so.
	if names["content_rating"].Verifiable {
		t.Error("content_rating must be marked unverifiable: v3 exposes no endpoint for it")
	}
	if names["content_rating"].ConsolePath == "" {
		t.Error("content_rating gate must carry a Console path")
	}

	// Data safety has a REAL API — it must NOT be smuggled into consoleOnly.
	if _, isConsoleOnly := names["data_safety"]; isConsoleOnly {
		t.Error("data_safety must not be listed as Console-only: POST /applications/{pkg}/dataSafety exists")
	}
	na, ok := m["notAutomated"].([]playGate)
	if !ok || len(na) != 1 || na[0].Gate != "data_safety" {
		t.Fatalf("expected data_safety under notAutomated, got %#v", m["notAutomated"])
	}
	if na[0].APIPath == "" {
		t.Error("data_safety must carry the real API path it is NOT driving")
	}
}

func TestPlaySubmitStatusReadyWhenComplete(t *testing.T) {
	f := newFakePlay(t)
	f.listingsJSON = `{"listings":[{"language":"en-US","title":"Acme","shortDescription":"Short","fullDescription":"Full description."}]}`
	f.imagesJSON["icon"] = `{"images":[{"id":"i1"}]}`
	f.imagesJSON["featureGraphic"] = `{"images":[{"id":"f1"}]}`
	f.imagesJSON["phoneScreenshots"] = `{"images":[{"id":"s1"},{"id":"s2"}]}`
	f.bundlesJSON = `{"bundles":[{"versionCode":812,"sha256":"abc"}]}`
	f.tracksJSON = `{"tracks":[{"track":"production","releases":[{"versionCodes":["812"],"status":"completed"}]}]}`

	res := playSubmitStatusHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
	}))
	if !res.OK {
		t.Fatalf("status failed: %s", res.Error)
	}
	m := initialMap(t, res)
	if m["ready"] != true {
		t.Fatalf("expected ready=true, got %v (missing: %v)", m["ready"], m["missing"])
	}
	if m["bundleUploaded"] != true {
		t.Error("expected bundleUploaded=true")
	}
	// Even when API-ready, the unverifiable gates must still be reported — `ready`
	// is explicitly only about what the API can see.
	if gates, _ := m["consoleOnly"].([]playGate); len(gates) == 0 {
		t.Error("consoleOnly gates must be reported even when API-ready")
	}
	if !strings.Contains(fmt.Sprint(m["note"]), "API-visible") {
		t.Errorf("note must qualify what `ready` means, got: %v", m["note"])
	}
}

// A preflight must never leave an edit (a lock) behind on the app.
func TestPlaySubmitStatusDiscardsItsEdit(t *testing.T) {
	f := newFakePlay(t)
	res := playSubmitStatusHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
	}))
	if !res.OK {
		t.Fatalf("status failed: %s", res.Error)
	}
	if len(f.editsDeleted) != 1 {
		t.Fatalf("status must DELETE its read-only edit, deleted=%v", f.editsDeleted)
	}
	if len(f.commits) != 0 {
		t.Fatalf("status must never commit, commits=%v", f.commits)
	}
}

// ─────────────────── the edits transaction: open → mutate → validate → commit ───

func TestPlayListingSetRunsFullEditTransaction(t *testing.T) {
	f := newFakePlay(t)
	res := playListingSetHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName":      "com.acme.app",
		"language":         "en-US",
		"title":            "Acme",
		"shortDescription": "Short one",
	}))
	if !res.OK {
		t.Fatalf("listing set failed: %s", res.Error)
	}
	if f.editsOpened != 1 {
		t.Fatalf("expected exactly 1 edit opened, got %d", f.editsOpened)
	}
	if len(f.validated) != 1 {
		t.Fatalf("edit must be validated before commit, validated=%v", f.validated)
	}
	// Committed, and NOT sent for review: setting a title must never ship the app.
	if len(f.commits) != 1 || f.commits[0] != "edit-1?noreview" {
		t.Fatalf("expected one commit with changesNotSentForReview=true, got %v", f.commits)
	}
	if len(f.editsDeleted) != 0 {
		t.Fatalf("a committed edit must NOT be deleted, deleted=%v", f.editsDeleted)
	}
	if f.patchedList["title"] != "Acme" || f.patchedList["shortDescription"] != "Short one" {
		t.Fatalf("unexpected PATCH body: %#v", f.patchedList)
	}
	// Only the fields passed may be sent — fullDescription must be absent, not "".
	if _, present := f.patchedList["fullDescription"]; present {
		t.Error("fullDescription was not supplied and must not be written (would wipe it)")
	}
	m := initialMap(t, res)
	if m["sentForReview"] != false {
		t.Error("result must state sentForReview=false")
	}
}

// The core cleanup guarantee: any failure inside the transaction discards the
// edit, so a leaked edit never blocks the next run.
func TestPlayEditIsDeletedWhenAMutationFails(t *testing.T) {
	f := newFakePlay(t)
	f.fail("PATCH /listings/", 400, `{"error":{"code":400,"message":"Invalid listing.","status":"INVALID_ARGUMENT","errors":[{"domain":"androidpublisher","reason":"listingInvalid","message":"The listing is invalid."}]}}`)

	res := playListingSetHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"title":       "Acme",
	}))
	if res.OK {
		t.Fatal("expected failure")
	}
	if len(f.editsDeleted) != 1 {
		t.Fatalf("failed edit MUST be deleted (it locks the app), deleted=%v", f.editsDeleted)
	}
	if len(f.commits) != 0 {
		t.Fatalf("a failed edit must never be committed, commits=%v", f.commits)
	}
	// Google's words, verbatim.
	if !strings.Contains(res.Error, "The listing is invalid.") || !strings.Contains(res.Error, "listingInvalid") {
		t.Fatalf("Google's error must be surfaced verbatim, got: %s", res.Error)
	}
}

// A failure at COMMIT must also clean up.
func TestPlayEditIsDeletedWhenCommitFails(t *testing.T) {
	f := newFakePlay(t)
	f.fail("POST :commit", 409, `{"error":{"code":409,"message":"Edit conflict.","errors":[{"reason":"editAlreadyCommitted","message":"Edit has already been committed."}]}}`)

	res := playListingSetHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"title":       "Acme",
	}))
	if res.OK {
		t.Fatal("expected failure")
	}
	if len(f.editsDeleted) != 1 {
		t.Fatalf("edit must be deleted after a failed commit, deleted=%v", f.editsDeleted)
	}
	if !strings.Contains(res.Error, "Edit has already been committed.") {
		t.Fatalf("verbatim commit error missing: %s", res.Error)
	}
}

// A caller-owned editId must be neither committed nor deleted — it is theirs.
func TestPlayAdoptedEditIsNeitherCommittedNorDeleted(t *testing.T) {
	f := newFakePlay(t)
	res := playListingSetHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"editId":      "edit-1",
		"title":       "Acme",
	}))
	if !res.OK {
		t.Fatalf("listing set failed: %s", res.Error)
	}
	if f.editsOpened != 0 {
		t.Fatalf("an adopted edit must not open a new one, opened=%d", f.editsOpened)
	}
	if len(f.commits) != 0 || len(f.editsDeleted) != 0 || len(f.validated) != 0 {
		t.Fatalf("adopted edit must be left alone: commits=%v deleted=%v validated=%v",
			f.commits, f.editsDeleted, f.validated)
	}
	m := initialMap(t, res)
	if m["editCommitted"] != false {
		t.Error("result must say the adopted edit was NOT committed")
	}
}

// Even on failure, an adopted edit must not be destroyed — discarding someone
// else's transaction would throw away uncommitted work.
func TestPlayAdoptedEditSurvivesFailure(t *testing.T) {
	f := newFakePlay(t)
	f.fail("PATCH /listings/", 400, `{"error":{"code":400,"message":"nope"}}`)
	res := playListingSetHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"editId":      "edit-1",
		"title":       "Acme",
	}))
	if res.OK {
		t.Fatal("expected failure")
	}
	if len(f.editsDeleted) != 0 {
		t.Fatalf("must NOT delete the caller's edit, deleted=%v", f.editsDeleted)
	}
}

// ───────────────────────────── play_images_set ─────────────────────────────

func TestPlayImagesSetReplaceDeletesThenUploads(t *testing.T) {
	f := newFakePlay(t)
	dir := t.TempDir()
	a := playWritePNG(t, dir, "shot1.png")
	b := playWritePNG(t, dir, "shot2.png")

	res := playImagesSetHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"imageType":   "phoneScreenshots",
		"files":       []string{a, b},
		"replace":     true,
	}))
	if !res.OK {
		t.Fatalf("images set failed: %s", res.Error)
	}
	if len(f.deletedAll) != 1 || f.deletedAll[0] != "phoneScreenshots" {
		t.Fatalf("replace:true must deleteall first, got %v", f.deletedAll)
	}
	if len(f.uploads) != 2 {
		t.Fatalf("expected 2 uploads, got %d", len(f.uploads))
	}
	// The upload MUST go to /listings/{lang}/{imageType} on the /upload/ host —
	// this is the path Google actually documents (NOT /images/…).
	want := "/upload/applications/com.acme.app/edits/edit-1/listings/en-US/phoneScreenshots"
	for _, u := range f.uploads {
		if u.Path != want {
			t.Fatalf("upload path = %q, want %q", u.Path, want)
		}
		if u.ContentType != "image/png" {
			t.Errorf("content-type = %q, want image/png", u.ContentType)
		}
	}
	if !strings.Contains(string(f.uploads[0].Body), "shot1.png") {
		t.Error("first upload did not carry the first file's bytes")
	}
	if len(f.commits) != 1 || f.commits[0] != "edit-1?noreview" {
		t.Fatalf("images must commit without review, got %v", f.commits)
	}
	m := initialMap(t, res)
	if m["uploadedCount"] != 2 {
		t.Errorf("uploadedCount = %v", m["uploadedCount"])
	}
}

func TestPlayImagesSetRejectsUnknownTypeAndMultiSlotIcon(t *testing.T) {
	newFakePlay(t)
	dir := t.TempDir()
	a := playWritePNG(t, dir, "a.png")
	b := playWritePNG(t, dir, "b.png")

	res := playImagesSetHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"imageType":   "bogusShots",
		"files":       []string{a},
	}))
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("unknown imageType must be rejected, got %+v", res)
	}

	// icon holds exactly one image — two files is a guaranteed Google rejection.
	res = playImagesSetHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"imageType":   "icon",
		"files":       []string{a, b},
	}))
	if res.OK || !strings.Contains(res.Error, "exactly ONE") {
		t.Fatalf("icon with 2 files must be rejected, got %+v", res)
	}
}

// A mid-sequence upload failure must discard the edit, so Play never sees a
// half-populated screenshot bucket.
func TestPlayImagesSetPartialUploadDiscardsEdit(t *testing.T) {
	f := newFakePlay(t)
	dir := t.TempDir()
	a := playWritePNG(t, dir, "a.png")
	f.fail("POST /listings/en-US/phoneScreenshots", 403, `{"error":{"code":403,"message":"Quota exceeded."}}`)

	res := playImagesSetHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"imageType":   "phoneScreenshots",
		"files":       []string{a},
	}))
	if res.OK {
		t.Fatal("expected failure")
	}
	if len(f.editsDeleted) != 1 {
		t.Fatalf("edit must be discarded on upload failure, deleted=%v", f.editsDeleted)
	}
	if len(f.commits) != 0 {
		t.Fatalf("must not commit a half-uploaded bucket, commits=%v", f.commits)
	}
	if !strings.Contains(res.Error, "Quota exceeded.") {
		t.Fatalf("Google's upload error must be verbatim: %s", res.Error)
	}
}

// ──────────────────────────── play_bundle_upload ────────────────────────────

func TestPlayBundleUploadReturnsVersionCode(t *testing.T) {
	f := newFakePlay(t)
	dir := t.TempDir()
	aab := filepath.Join(dir, "app.aab")
	if err := os.WriteFile(aab, []byte("PK\x03\x04fake-bundle"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := playBundleUploadHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"file":        aab,
	}))
	if !res.OK {
		t.Fatalf("bundle upload failed: %s", res.Error)
	}
	m := initialMap(t, res)
	if fmt.Sprint(m["versionCode"]) != "812" {
		t.Fatalf("versionCode = %v, want 812", m["versionCode"])
	}
	if len(f.uploads) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(f.uploads))
	}
	want := "/upload/applications/com.acme.app/edits/edit-1/bundles"
	if f.uploads[0].Path != want {
		t.Fatalf("upload path = %q, want %q", f.uploads[0].Path, want)
	}
	if f.uploads[0].ContentType != "application/octet-stream" {
		t.Errorf("content-type = %q", f.uploads[0].ContentType)
	}
	// An upload is not a submission.
	if len(f.commits) != 1 || f.commits[0] != "edit-1?noreview" {
		t.Fatalf("bundle upload must commit WITHOUT review, got %v", f.commits)
	}
	if m["sentForReview"] != false {
		t.Error("bundle upload must report sentForReview=false")
	}
}

func TestPlayBundleUploadRejectsNonAAB(t *testing.T) {
	newFakePlay(t)
	dir := t.TempDir()
	apk := filepath.Join(dir, "app.apk")
	if err := os.WriteFile(apk, []byte("PK"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := playBundleUploadHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"file":        apk,
	}))
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("an .apk must be rejected by play_bundle_upload, got %+v", res)
	}
}

// ──────────────────────────── play_track_release ────────────────────────────

func TestPlayTrackReleaseSetsRelease(t *testing.T) {
	f := newFakePlay(t)
	res := playTrackReleaseHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName":  "com.acme.app",
		"track":        "internal",
		"versionCode":  "812",
		"status":       "completed",
		"releaseNotes": "First cut.",
	}))
	if !res.OK {
		t.Fatalf("track release failed: %s", res.Error)
	}
	releases, _ := f.putTrack["releases"].([]interface{})
	if len(releases) != 1 {
		t.Fatalf("expected 1 release in PUT, got %#v", f.putTrack)
	}
	rel := releases[0].(map[string]interface{})
	if rel["status"] != "completed" {
		t.Errorf("status = %v", rel["status"])
	}
	codes, _ := rel["versionCodes"].([]interface{})
	if len(codes) != 1 || codes[0] != "812" {
		t.Errorf("versionCodes = %v", rel["versionCodes"])
	}
	notes, _ := rel["releaseNotes"].([]interface{})
	if len(notes) != 1 {
		t.Fatalf("releaseNotes = %v", rel["releaseNotes"])
	}
	if len(f.commits) != 1 || f.commits[0] != "edit-1?noreview" {
		t.Fatalf("track release must commit WITHOUT review, got %v", f.commits)
	}
}

func TestPlayTrackReleaseValidatesArgs(t *testing.T) {
	newFakePlay(t)
	base := map[string]interface{}{"packageName": "com.acme.app", "versionCode": "812"}

	with := func(extra map[string]interface{}) json.RawMessage {
		m := map[string]interface{}{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return mustPayload(t, m)
	}

	if res := playTrackReleaseHandler(OpsContext{}, with(map[string]interface{}{"track": "nightly"})); res.OK || res.Code != "bad_payload" {
		t.Errorf("unknown track must be rejected, got %+v", res)
	}
	if res := playTrackReleaseHandler(OpsContext{}, with(map[string]interface{}{"track": "beta", "status": "shipped"})); res.OK || res.Code != "bad_payload" {
		t.Errorf("unknown status must be rejected, got %+v", res)
	}
	// userFraction is meaningless outside a staged (inProgress) rollout.
	if res := playTrackReleaseHandler(OpsContext{}, with(map[string]interface{}{"track": "production", "status": "completed", "userFraction": 0.1})); res.OK {
		t.Errorf("userFraction with status=completed must be rejected, got %+v", res)
	}
	if res := playTrackReleaseHandler(OpsContext{}, with(map[string]interface{}{"track": "production", "status": "inProgress", "userFraction": 1.5})); res.OK {
		t.Errorf("userFraction=1.5 must be rejected, got %+v", res)
	}
	// No version codes at all.
	if res := playTrackReleaseHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app", "track": "beta",
	})); res.OK || res.Code != "bad_payload" {
		t.Errorf("missing versionCodes must be rejected, got %+v", res)
	}
}

// ────────────────────────── play_submit_for_review ──────────────────────────

func TestPlaySubmitForReviewValidatesThenCommitsForReview(t *testing.T) {
	f := newFakePlay(t)
	res := playSubmitForReviewHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"editId":      "edit-1",
		"track":       "production",
	}))
	if !res.OK {
		t.Fatalf("submit failed: %s", res.Error)
	}
	if len(f.validated) != 1 {
		t.Fatalf("submit MUST validate before committing, validated=%v", f.validated)
	}
	// The submission: commit WITHOUT changesNotSentForReview.
	if len(f.commits) != 1 || f.commits[0] != "edit-1?review" {
		t.Fatalf("expected a review-sending commit, got %v", f.commits)
	}
	if len(f.editsDeleted) != 0 {
		t.Fatalf("a committed edit must not be deleted, deleted=%v", f.editsDeleted)
	}
	m := initialMap(t, res)
	if m["sentForReview"] != true || m["committed"] != true {
		t.Fatalf("result must report the submission: %#v", m)
	}
}

// changesNotSentForReview:true lands the changes but must NOT claim a submission.
func TestPlaySubmitForReviewHonorsChangesNotSentForReview(t *testing.T) {
	f := newFakePlay(t)
	res := playSubmitForReviewHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName":             "com.acme.app",
		"editId":                  "edit-1",
		"changesNotSentForReview": true,
	}))
	if !res.OK {
		t.Fatalf("submit failed: %s", res.Error)
	}
	if len(f.commits) != 1 || f.commits[0] != "edit-1?noreview" {
		t.Fatalf("expected a non-review commit, got %v", f.commits)
	}
	m := initialMap(t, res)
	if m["sentForReview"] != false {
		t.Fatal("must not claim the app was sent for review")
	}
	note := fmt.Sprint(m["note"])
	if !strings.Contains(note, "did NOT submit") {
		t.Errorf("note must state plainly that no submission happened, got: %s", note)
	}
	if _, ok := m["consoleOnly"].([]playGate); !ok {
		t.Error("must name the human 'Send changes for review' step")
	}
}

// The draft-app gate: Google refuses the first rollout of a never-published app.
// We must name it as Console-only and keep Google's exact words.
func TestPlaySubmitForReviewSurfacesDraftAppConsoleGate(t *testing.T) {
	f := newFakePlay(t)
	f.fail("POST :commit", 400, `{"error":{"code":400,"message":"Only releases with status draft may be created on draft app.","status":"INVALID_ARGUMENT","errors":[{"domain":"androidpublisher","reason":"rolloutNotPermittedOnDraftApp","message":"Only releases with status draft may be created on draft app."}]}}`)

	res := playSubmitForReviewHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"editId":      "edit-1",
	}))
	if res.OK {
		t.Fatal("expected the draft-app gate to fail the submission")
	}
	if res.Code != "console_only" {
		t.Fatalf("code = %q, want console_only", res.Code)
	}
	// Verbatim, not collapsed to "failed".
	if !strings.Contains(res.Error, "Only releases with status draft may be created on draft app.") {
		t.Fatalf("Google's message must be verbatim: %s", res.Error)
	}
	if !strings.Contains(res.Error, "Play Console") {
		t.Errorf("must tell the human where to go: %s", res.Error)
	}
	m := initialMap(t, res)
	gates, ok := m["consoleOnly"].([]playGate)
	if !ok || len(gates) != 1 || gates[0].Gate != "first_release_on_draft_app" {
		t.Fatalf("expected the first_release_on_draft_app gate, got %#v", m["consoleOnly"])
	}
	gerr, ok := m["googleError"].(*playAPIError)
	if !ok || len(gerr.reasons()) == 0 {
		t.Fatalf("the raw Google error must be attached, got %#v", m["googleError"])
	}
}

// The other Console gate: Google refusing to auto-send an app for review.
func TestPlaySubmitForReviewSurfacesSendForReviewConsoleGate(t *testing.T) {
	f := newFakePlay(t)
	f.fail("POST :commit", 400, `{"error":{"code":400,"message":"Changes cannot be sent for review automatically. Please set the query parameter changesNotSentForReview to true.","errors":[{"reason":"badRequest","message":"Changes cannot be sent for review automatically. Please set the query parameter changesNotSentForReview to true."}]}}`)

	res := playSubmitForReviewHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"editId":      "edit-1",
	}))
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.Code != "console_only" {
		t.Fatalf("code = %q, want console_only", res.Code)
	}
	if !strings.Contains(res.Error, "changesNotSentForReview to true") {
		t.Fatalf("Google's remedy must be surfaced verbatim: %s", res.Error)
	}
	m := initialMap(t, res)
	gates, _ := m["consoleOnly"].([]playGate)
	if len(gates) != 1 || gates[0].Gate != "send_changes_for_review" {
		t.Fatalf("expected send_changes_for_review gate, got %#v", m["consoleOnly"])
	}
}

// A plain (non-gate) Google error must still come through word for word.
func TestPlaySubmitForReviewSurfacesArbitraryGoogleErrorVerbatim(t *testing.T) {
	f := newFakePlay(t)
	f.fail("POST :validate", 403, `{"error":{"code":403,"message":"The caller does not have permission","status":"PERMISSION_DENIED","errors":[{"domain":"global","reason":"forbidden","message":"The caller does not have permission"}]}}`)

	res := playSubmitForReviewHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"editId":      "edit-1",
	}))
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.Code != "google_rejected" {
		t.Fatalf("code = %q, want google_rejected", res.Code)
	}
	for _, want := range []string{"The caller does not have permission", "PERMISSION_DENIED", "forbidden"} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("error must contain %q verbatim, got: %s", want, res.Error)
		}
	}
	// A failed validate must never commit.
	if len(f.commits) != 0 {
		t.Fatalf("must not commit after a failed validate, commits=%v", f.commits)
	}
}

// A non-JSON upstream body (proxy HTML, gateway error) must still reach the user.
func TestPlayNonJSONErrorBodyIsStillSurfaced(t *testing.T) {
	f := newFakePlay(t)
	f.fail("POST :commit", 502, `<html><body>502 Bad Gateway</body></html>`)

	res := playSubmitForReviewHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"editId":      "edit-1",
	}))
	if res.OK {
		t.Fatal("expected failure")
	}
	if !strings.Contains(res.Error, "502 Bad Gateway") {
		t.Fatalf("non-JSON body must still be surfaced, got: %s", res.Error)
	}
}

// ─────────────────────── play_release_halt / _resume ────────────────────────

func TestPlayReleaseHaltAndResume(t *testing.T) {
	f := newFakePlay(t)
	// A staged rollout carrying fields we do NOT model — they must survive.
	f.trackJSON["production"] = `{"track":"production","releases":[{"name":"1.2.0","versionCodes":["812"],"status":"inProgress","userFraction":0.1,"countryTargeting":{"countries":["DE"]},"inAppUpdatePriority":3}]}`

	res := playReleaseHaltHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"track":       "production",
	}))
	if !res.OK {
		t.Fatalf("halt failed: %s", res.Error)
	}
	releases, _ := f.putTrack["releases"].([]interface{})
	if len(releases) != 1 {
		t.Fatalf("PUT body = %#v", f.putTrack)
	}
	rel := releases[0].(map[string]interface{})
	if rel["status"] != "halted" {
		t.Fatalf("status = %v, want halted", rel["status"])
	}
	// A halted release must not carry a rollout fraction.
	if _, present := rel["userFraction"]; present {
		t.Error("userFraction must be dropped when halting")
	}
	// The read-modify-write must be LOSSLESS: unmodelled fields survive.
	if rel["countryTargeting"] == nil {
		t.Error("countryTargeting was dropped — the round-trip must be lossless")
	}
	if fmt.Sprint(rel["inAppUpdatePriority"]) != "3" {
		t.Errorf("inAppUpdatePriority was dropped/changed: %v", rel["inAppUpdatePriority"])
	}
	if len(f.commits) != 1 || f.commits[0] != "edit-1?noreview" {
		t.Fatalf("halt must commit without review, got %v", f.commits)
	}

	// Now resume it as a staged rollout.
	f.trackJSON["production"] = `{"track":"production","releases":[{"versionCodes":["812"],"status":"halted"}]}`
	f.putTrack = nil
	res = playReleaseResumeHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName":  "com.acme.app",
		"track":        "production",
		"userFraction": 0.25,
	}))
	if !res.OK {
		t.Fatalf("resume failed: %s", res.Error)
	}
	releases, _ = f.putTrack["releases"].([]interface{})
	rel = releases[0].(map[string]interface{})
	if rel["status"] != "inProgress" {
		t.Fatalf("status = %v, want inProgress", rel["status"])
	}
	if fmt.Sprint(rel["userFraction"]) != "0.25" {
		t.Fatalf("userFraction = %v, want 0.25", rel["userFraction"])
	}
}

func TestPlayReleaseHaltWithNothingToHalt(t *testing.T) {
	f := newFakePlay(t)
	f.trackJSON["production"] = `{"track":"production","releases":[{"versionCodes":["812"],"status":"completed"}]}`

	res := playReleaseHaltHandler(OpsContext{}, mustPayload(t, map[string]interface{}{
		"packageName": "com.acme.app",
		"track":       "production",
	}))
	if res.OK || res.Code != "not_found" {
		t.Fatalf("expected not_found, got %+v", res)
	}
	// It must say what IS there rather than a bare "no".
	if !strings.Contains(res.Error, "812=completed") {
		t.Errorf("error should report the current releases, got: %s", res.Error)
	}
	// And it must not leave the edit behind.
	if len(f.editsDeleted) != 1 {
		t.Fatalf("edit must be discarded, deleted=%v", f.editsDeleted)
	}
}

// ───────────────────────────── registration ─────────────────────────────

func TestPlayVerbsAreRegisteredOwnerOnly(t *testing.T) {
	want := []string{
		"play_submit_status", "play_listing_set", "play_images_set",
		"play_bundle_upload", "play_track_release", "play_submit_for_review",
		"play_release_halt", "play_release_resume",
	}
	registered := map[string]opsVerbSpec{}
	for _, v := range listOpsVerbs() {
		registered[v.Name] = v
	}
	for _, name := range want {
		spec, ok := registered[name]
		if !ok {
			t.Fatalf("verb %q is not registered", name)
		}
		// These publish to a third party's store account with their credentials —
		// never reachable by a guest token.
		if spec.AllowGuest {
			t.Errorf("verb %q must be owner-only (AllowGuest=false)", name)
		}
		if spec.Schema == nil || spec.Description == "" {
			t.Errorf("verb %q needs a schema + description for MCP self-discovery", name)
		}
	}
}
