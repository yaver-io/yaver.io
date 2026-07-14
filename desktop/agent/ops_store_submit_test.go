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
)

// fakeASC is a REAL HTTP server that speaks enough App Store Connect to drive
// the submission verbs end to end (repo convention: no mocks). Every field is a
// canned JSON body; the recorded fields let a test assert what we actually sent.
type fakeASC struct {
	t   *testing.T
	mu  sync.Mutex
	srv *httptest.Server

	// canned responses (raw JSON `data` payloads)
	versionsJSON      string
	localizationsJSON string
	screenshotSetsRes string
	screenshotsJSON   string
	buildRelJSON      string
	buildsJSON        string
	reviewDetailJSON  string
	reviewSubsJSON    string
	itemsJSON         string

	// POST /reviewSubmissionItems outcome
	itemStatus int
	itemBody   string

	// recorded
	calls         []string
	uploadChunks  [][]byte
	uploadHeaders []http.Header
	commitAttrs   map[string]interface{}
	patchedLoc    map[string]interface{}
	patchedVer    map[string]interface{}
	patchedDetail map[string]interface{}
	patchedSub    map[string]interface{}
	subsCreated   int
	setsCreated   int
	deletedShots  []string
}

func newFakeASC(t *testing.T) *fakeASC {
	t.Helper()
	f := &fakeASC{
		t:                 t,
		versionsJSON:      `[{"id":"ver-1","attributes":{"versionString":"1.0","platform":"VISION_OS","appStoreState":"PREPARE_FOR_SUBMISSION"}}]`,
		localizationsJSON: `[{"id":"loc-1","attributes":{"locale":"en-US","description":"An app.","keywords":"a,b","supportUrl":"https://example.com"}}]`,
		screenshotSetsRes: `[]`,
		screenshotsJSON:   `[]`,
		buildRelJSON:      `null`,
		buildsJSON:        `[]`,
		reviewDetailJSON:  `null`,
		reviewSubsJSON:    `[]`,
		itemsJSON:         `[]`,
		itemStatus:        201,
		itemBody:          `{"data":{"id":"item-1"}}`,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.route))
	t.Cleanup(f.srv.Close)

	oldBase, oldUp, oldNew := ascAPIBase, ascUploadClient, newASCClientFn
	ascAPIBase = f.srv.URL
	ascUploadClient = f.srv.Client()
	creds := testASCCreds(t)
	newASCClientFn = func(project string) (*ascClient, error) {
		return &ascClient{creds: creds, http: f.srv.Client()}, nil
	}
	t.Cleanup(func() {
		ascAPIBase, ascUploadClient, newASCClientFn = oldBase, oldUp, oldNew
	})
	return f
}

func (f *fakeASC) client() *ascClient {
	cl, err := newASCClientFn("")
	if err != nil {
		f.t.Fatalf("client: %v", err)
	}
	return cl
}

func (f *fakeASC) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeASC) called(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == s {
			return true
		}
	}
	return false
}

func decodeAttrs(t *testing.T, r *http.Request) map[string]interface{} {
	t.Helper()
	var body struct {
		Data struct {
			Attributes map[string]interface{} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s %s: %v", r.Method, r.URL.Path, err)
	}
	return body.Data.Attributes
}

func (f *fakeASC) route(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	// The asset-upload host is NOT the API host: Apple rejects the ASC JWT
	// there. Assert we never leak it, and that Apple's own headers are applied.
	if strings.HasPrefix(p, "/upload/") {
		if auth := r.Header.Get("Authorization"); auth != "" {
			f.t.Errorf("upload op must not carry the ASC bearer, got %q", auth)
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
		fmt.Fprintf(w, `{"data":%s}`, f.versionsJSON)

	case r.Method == "GET" && p == "/appStoreVersions/ver-1/appStoreVersionLocalizations":
		fmt.Fprintf(w, `{"data":%s}`, f.localizationsJSON)

	case r.Method == "PATCH" && p == "/appStoreVersionLocalizations/loc-1":
		f.patchedLoc = decodeAttrs(f.t, r)
		fmt.Fprint(w, `{"data":{"id":"loc-1","attributes":{"locale":"en-US"}}}`)

	case r.Method == "GET" && p == "/appStoreVersionLocalizations/loc-1/appScreenshotSets":
		fmt.Fprintf(w, `{"data":%s}`, f.screenshotSetsRes)

	case r.Method == "POST" && p == "/appScreenshotSets":
		attrs := decodeAttrs(f.t, r)
		f.setsCreated++
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"data":{"id":"set-1","attributes":{"screenshotDisplayType":%q}}}`, attrs["screenshotDisplayType"])

	case r.Method == "GET" && p == "/appScreenshotSets/set-1/appScreenshots":
		fmt.Fprintf(w, `{"data":%s}`, f.screenshotsJSON)

	case r.Method == "POST" && p == "/appScreenshots":
		attrs := decodeAttrs(f.t, r)
		size, _ := attrs["fileSize"].(float64)
		if size <= 0 {
			f.t.Errorf("reserve must declare fileSize, got %v", attrs["fileSize"])
		}
		// Two operations on purpose: proves offset/length slicing of the file.
		half := int(size) / 2
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"data":{"id":"shot-1","attributes":{"uploadOperations":[
			{"method":"PUT","url":%q,"offset":0,"length":%d,"requestHeaders":[{"name":"X-Apple-Part","value":"1"}]},
			{"method":"PUT","url":%q,"offset":%d,"length":%d,"requestHeaders":[{"name":"X-Apple-Part","value":"2"}]}
		]}}}`,
			f.srv.URL+"/upload/a", half,
			f.srv.URL+"/upload/b", half, int(size)-half)

	case r.Method == "PATCH" && p == "/appScreenshots/shot-1":
		f.commitAttrs = decodeAttrs(f.t, r)
		fmt.Fprint(w, `{"data":{"id":"shot-1","attributes":{"fileName":"shot.png","assetDeliveryState":{"state":"COMPLETE"}}}}`)

	case r.Method == "DELETE" && strings.HasPrefix(p, "/appScreenshots/"):
		f.deletedShots = append(f.deletedShots, strings.TrimPrefix(p, "/appScreenshots/"))
		w.WriteHeader(204)

	case r.Method == "GET" && p == "/appStoreVersions/ver-1/build":
		fmt.Fprintf(w, `{"data":%s}`, f.buildRelJSON)

	case r.Method == "GET" && p == "/builds":
		fmt.Fprintf(w, `{"data":%s}`, f.buildsJSON)

	case r.Method == "PATCH" && p == "/appStoreVersions/ver-1":
		var body struct {
			Data struct {
				Relationships map[string]interface{} `json:"relationships"`
			} `json:"data"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.patchedVer = body.Data.Relationships
		fmt.Fprint(w, `{"data":{"id":"ver-1","attributes":{"versionString":"1.0","platform":"VISION_OS"}}}`)

	case r.Method == "GET" && p == "/appStoreVersions/ver-1/appStoreReviewDetail":
		fmt.Fprintf(w, `{"data":%s}`, f.reviewDetailJSON)

	case r.Method == "POST" && p == "/appStoreReviewDetails":
		f.patchedDetail = decodeAttrs(f.t, r)
		w.WriteHeader(201)
		fmt.Fprint(w, `{"data":{"id":"rd-1","attributes":{"notes":"n","contactEmail":"a@b.c","demoAccountRequired":false}}}`)

	case r.Method == "PATCH" && p == "/appStoreReviewDetails/rd-1":
		f.patchedDetail = decodeAttrs(f.t, r)
		fmt.Fprint(w, `{"data":{"id":"rd-1","attributes":{"notes":"n","contactEmail":"a@b.c"}}}`)

	case r.Method == "GET" && p == "/reviewSubmissions":
		fmt.Fprintf(w, `{"data":%s}`, f.reviewSubsJSON)

	case r.Method == "POST" && p == "/reviewSubmissions":
		f.subsCreated++
		w.WriteHeader(201)
		fmt.Fprint(w, `{"data":{"id":"sub-1","attributes":{"platform":"VISION_OS","state":"READY_FOR_REVIEW","submitted":false}}}`)

	case r.Method == "GET" && p == "/reviewSubmissions/sub-1/items":
		fmt.Fprintf(w, `{"data":%s}`, f.itemsJSON)

	case r.Method == "POST" && p == "/reviewSubmissionItems":
		w.WriteHeader(f.itemStatus)
		fmt.Fprint(w, f.itemBody)

	case r.Method == "PATCH" && p == "/reviewSubmissions/sub-1":
		f.patchedSub = decodeAttrs(f.t, r)
		state := "WAITING_FOR_REVIEW"
		if v, ok := f.patchedSub["canceled"].(bool); ok && v {
			state = "CANCELING"
		}
		fmt.Fprintf(w, `{"data":{"id":"sub-1","attributes":{"state":%q,"submitted":true}}}`, state)

	default:
		f.t.Errorf("unexpected %s %s", r.Method, p)
		w.WriteHeader(404)
	}
}

func writeTempPNG(t *testing.T, name string, n int) (string, []byte) {
	t.Helper()
	data := make([]byte, n)
	for i := range data {
		data[i] = byte('A' + i%26)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}
	return path, data
}

func opsInitial(t *testing.T, r OpsResult) map[string]interface{} {
	t.Helper()
	m, ok := r.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("Initial is not a map: %#v", r.Initial)
	}
	return m
}

// ── the proven screenshot sequence: reserve → uploadOperations → commit ──

func TestASCScreenshotUploadSequence(t *testing.T) {
	f := newFakeASC(t)
	path, data := writeTempPNG(t, "shot.png", 11) // odd size → uneven chunks

	shot, err := f.client().UploadScreenshot("set-1", path)
	if err != nil {
		t.Fatalf("UploadScreenshot: %v", err)
	}
	if shot.ID != "shot-1" {
		t.Fatalf("shot = %+v", shot)
	}

	// (c) every uploadOperation ran, in order, slicing the ORIGINAL file bytes.
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
	// Apple's own requestHeaders must be applied verbatim.
	if got := f.uploadHeaders[0].Get("X-Apple-Part"); got != "1" {
		t.Errorf("apple request header not applied: %q", got)
	}
	if got := f.uploadHeaders[1].Get("X-Apple-Part"); got != "2" {
		t.Errorf("apple request header not applied: %q", got)
	}

	// (d) commit carries uploaded:true + md5 of the WHOLE file.
	if f.commitAttrs["uploaded"] != true {
		t.Errorf("commit uploaded = %v", f.commitAttrs["uploaded"])
	}
	sum := md5.Sum(data)
	if want := hex.EncodeToString(sum[:]); f.commitAttrs["sourceFileChecksum"] != want {
		t.Errorf("sourceFileChecksum = %v, want md5 of whole file %s", f.commitAttrs["sourceFileChecksum"], want)
	}
}

func TestStoreScreenshotsSetReusesSetAndReplaces(t *testing.T) {
	f := newFakeASC(t)
	// An existing set for this display type + one stale screenshot in it.
	f.screenshotSetsRes = `[{"id":"set-1","attributes":{"screenshotDisplayType":"APP_APPLE_VISION_PRO"}}]`
	f.screenshotsJSON = `[{"id":"old-1","attributes":{"fileName":"old.png"}}]`
	path, _ := writeTempPNG(t, "shot.png", 8)

	payload, _ := json.Marshal(map[string]interface{}{
		"bundleId":    "com.acme.app",
		"platform":    "visionos",
		"displayType": "APP_APPLE_VISION_PRO",
		"files":       []string{path},
		"replace":     true,
	})
	res := storeScreenshotsSetHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("handler failed: %s", res.Error)
	}
	if f.setsCreated != 0 {
		t.Errorf("must REUSE the existing set for the display type, but created %d", f.setsCreated)
	}
	if len(f.deletedShots) != 1 || f.deletedShots[0] != "old-1" {
		t.Errorf("replace:true must delete existing screenshots first, deleted = %v", f.deletedShots)
	}
	init := opsInitial(t, res)
	if init["uploadedCount"] != 1 || init["deleted"] != 1 {
		t.Errorf("initial = %+v", init)
	}
}

// ── the proven submit sequence + Apple's associatedErrors ──

func TestStoreSubmitForReviewSequence(t *testing.T) {
	f := newFakeASC(t)
	payload, _ := json.Marshal(map[string]interface{}{
		"bundleId": "com.acme.app",
		"platform": "VISION_OS",
	})
	res := storeSubmitForReviewHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("submit failed: %s", res.Error)
	}
	if !f.called("POST /reviewSubmissions") || !f.called("POST /reviewSubmissionItems") || !f.called("PATCH /reviewSubmissions/sub-1") {
		t.Fatalf("submit must be create → item → submitted:true; calls = %v", f.calls)
	}
	if f.patchedSub["submitted"] != true {
		t.Errorf("final PATCH must set submitted:true, got %+v", f.patchedSub)
	}
	init := opsInitial(t, res)
	if init["state"] != "WAITING_FOR_REVIEW" {
		t.Errorf("state = %v, want WAITING_FOR_REVIEW", init["state"])
	}
}

// A dangling reviewSubmission cannot be DELETEd (Apple 403s), so an open one
// MUST be reused rather than re-created.
func TestStoreSubmitForReviewReusesOpenSubmission(t *testing.T) {
	f := newFakeASC(t)
	f.reviewSubsJSON = `[{"id":"sub-1","attributes":{"platform":"VISION_OS","state":"READY_FOR_REVIEW","submitted":false}}]`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitForReviewHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("submit failed: %s", res.Error)
	}
	if f.subsCreated != 0 {
		t.Errorf("must reuse the dangling submission (it can't be deleted), but created %d", f.subsCreated)
	}
	if init := opsInitial(t, res); init["submissionCreated"] != false {
		t.Errorf("submissionCreated = %v, want false", init["submissionCreated"])
	}
}

// The version is already itemised on the reused submission → don't 409 by
// adding it twice; go straight to submitted:true.
func TestStoreSubmitForReviewSkipsDuplicateItem(t *testing.T) {
	f := newFakeASC(t)
	f.reviewSubsJSON = `[{"id":"sub-1","attributes":{"platform":"VISION_OS","state":"READY_FOR_REVIEW","submitted":false}}]`
	f.itemsJSON = `[{"id":"item-1","relationships":{"appStoreVersion":{"data":{"type":"appStoreVersions","id":"ver-1"}}}}]`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitForReviewHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("submit failed: %s", res.Error)
	}
	if f.called("POST /reviewSubmissionItems") {
		t.Errorf("version is already an item — must not re-add it")
	}
	if init := opsInitial(t, res); init["itemAdded"] != false {
		t.Errorf("itemAdded = %v, want false", init["itemAdded"])
	}
}

// THE money path: Apple's 409 on reviewSubmissionItems carries
// meta.associatedErrors naming the missing field. That is how the visionOS
// motion label was discovered. It must be surfaced verbatim AND mapped to the
// Console-only gate — never collapsed to "submission failed".
func TestStoreSubmitForReviewSurfacesAssociatedErrors(t *testing.T) {
	f := newFakeASC(t)
	f.itemStatus = 409
	f.itemBody = `{"errors":[{
		"status":"409","code":"ENTITY_ERROR.RELATIONSHIP.INVALID",
		"title":"The provided entity includes a relationship with an invalid value",
		"detail":"The specified resource does not exist",
		"meta":{"associatedErrors":{"/v1/appStoreVersions/ver-1":[
			{"status":"409","code":"ENTITY_ERROR.ATTRIBUTE.REQUIRED",
			 "title":"The provided entity is missing a required attribute",
			 "detail":"You must provide a value for the attribute 'hasHighMotionLabel' with this request."}
		]}}
	}]}`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitForReviewHandler(OpsContext{}, payload)
	if res.OK {
		t.Fatalf("submission must FAIL, not silently succeed")
	}
	if f.called("PATCH /reviewSubmissions/sub-1") {
		t.Fatalf("must not mark submitted:true after the item was rejected")
	}
	// Apple's own words, verbatim.
	if !strings.Contains(res.Error, "hasHighMotionLabel") {
		t.Errorf("associatedErrors must be surfaced verbatim, got:\n%s", res.Error)
	}
	if !strings.Contains(res.Error, "ENTITY_ERROR.ATTRIBUTE.REQUIRED") {
		t.Errorf("Apple's error code must survive, got:\n%s", res.Error)
	}
	// Mapped to the Console-only gate with the exact path.
	if res.Code != "console_only" {
		t.Errorf("code = %q, want console_only", res.Code)
	}
	if !strings.Contains(res.Error, "App Store Connect →") {
		t.Errorf("must name the exact Console path, got:\n%s", res.Error)
	}
	init := opsInitial(t, res)
	assoc, _ := init["associatedErrors"].([]ascAssocError)
	if len(assoc) != 1 || assoc[0].Code != "ENTITY_ERROR.ATTRIBUTE.REQUIRED" {
		t.Errorf("structured associatedErrors missing: %#v", init["associatedErrors"])
	}
	gates, _ := init["consoleOnly"].([]consoleGate)
	if len(gates) != 1 || gates[0].Gate != "app_motion_information" || gates[0].Verifiable {
		t.Errorf("motion gate missing or claimed verifiable: %#v", init["consoleOnly"])
	}
}

// A non-motion 409 must still surface Apple's associated errors verbatim, but
// must NOT be misreported as the Console-only gate.
func TestStoreSubmitForReviewNonMotion409(t *testing.T) {
	f := newFakeASC(t)
	f.itemStatus = 409
	f.itemBody = `{"errors":[{"code":"ENTITY_ERROR","title":"bad","meta":{"associatedErrors":{"/v1/appStoreVersions/ver-1":[
		{"code":"ENTITY_ERROR.ATTRIBUTE.REQUIRED","detail":"You must provide a value for the attribute 'description' with this request."}
	]}}}]}`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitForReviewHandler(OpsContext{}, payload)
	if res.OK {
		t.Fatal("must fail")
	}
	if !strings.Contains(res.Error, "'description'") {
		t.Errorf("verbatim detail lost:\n%s", res.Error)
	}
	if res.Code != "apple_rejected" {
		t.Errorf("code = %q, want apple_rejected (not a Console gate)", res.Code)
	}
}

// ── Console gate 1: adding a platform to an app has NO API ──

func TestStoreSubmitStatusAddPlatformGate(t *testing.T) {
	f := newFakeASC(t)
	f.versionsJSON = `[]` // visionOS not enabled on the app
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitStatusHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("status should report, not error: %s", res.Error)
	}
	init := opsInitial(t, res)
	if init["ready"] != false {
		t.Errorf("ready = %v, want false", init["ready"])
	}
	gates, _ := init["consoleOnly"].([]consoleGate)
	if len(gates) != 1 || gates[0].Gate != "add_platform" {
		t.Fatalf("consoleOnly = %#v", init["consoleOnly"])
	}
	if !strings.Contains(gates[0].ConsolePath, "App Store Connect →") {
		t.Errorf("gate must give the exact Console path: %q", gates[0].ConsolePath)
	}
}

// The same gate must HARD-FAIL the mutating verbs — never a silent success.
func TestStoreSubmitForReviewRefusesWhenPlatformMissing(t *testing.T) {
	f := newFakeASC(t)
	f.versionsJSON = `[]`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitForReviewHandler(OpsContext{}, payload)
	if res.OK {
		t.Fatal("must refuse: there is no version to submit")
	}
	if res.Code != "console_only" {
		t.Errorf("code = %q, want console_only", res.Code)
	}
	if !strings.Contains(res.Error, "NO App Store Connect REST endpoint") {
		t.Errorf("must say plainly that this cannot be automated:\n%s", res.Error)
	}
	if f.called("POST /reviewSubmissions") {
		t.Error("must not create a review submission for a platform that isn't on the app")
	}
}

// ── preflight ──

func TestStoreSubmitStatusReportsWhatIsMissing(t *testing.T) {
	f := newFakeASC(t)
	f.localizationsJSON = `[{"id":"loc-1","attributes":{"locale":"en-US","description":"","keywords":""}}]`
	f.buildRelJSON = `null`
	f.reviewDetailJSON = `null`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitStatusHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("status: %s", res.Error)
	}
	init := opsInitial(t, res)
	missing, _ := init["missing"].([]string)
	joined := strings.Join(missing, "\n")
	for _, want := range []string{"no build attached", "description is empty", "keywords are empty", "support URL is empty", "no screenshots", "App Review Information is not filled in"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing should mention %q; got:\n%s", want, joined)
		}
	}
	if init["ready"] != false {
		t.Error("ready must be false")
	}
	// visionOS always carries the unverifiable motion gate.
	gates, _ := init["consoleOnly"].([]consoleGate)
	if len(gates) != 1 || gates[0].Gate != "app_motion_information" || gates[0].Verifiable {
		t.Fatalf("visionOS status must always surface the unverifiable motion gate: %#v", gates)
	}
}

func TestStoreSubmitStatusReadyAndNoDemoPasswordLeak(t *testing.T) {
	f := newFakeASC(t)
	f.buildRelJSON = `{"id":"build-9","attributes":{"version":"42","processingState":"VALID"}}`
	f.screenshotSetsRes = `[{"id":"set-1","attributes":{"screenshotDisplayType":"APP_APPLE_VISION_PRO"}}]`
	f.screenshotsJSON = `[{"id":"s1"},{"id":"s2"},{"id":"s3"}]`
	f.reviewDetailJSON = `{"id":"rd-1","attributes":{"notes":"hi","contactEmail":"a@b.c","contactPhone":"+1","demoAccountName":"demo","demoAccountPassword":"hunter2","demoAccountRequired":true}}`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitStatusHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("status: %s", res.Error)
	}
	init := opsInitial(t, res)
	if init["ready"] != true {
		t.Errorf("ready = %v, missing = %v", init["ready"], init["missing"])
	}
	locs, _ := init["localizations"].([]localizationStatus)
	if len(locs) != 1 || locs[0].ScreenshotCount != 3 {
		t.Errorf("screenshot count = %#v", init["localizations"])
	}
	// Credentials must never be echoed back.
	blob, _ := json.Marshal(init)
	if strings.Contains(string(blob), "hunter2") {
		t.Fatal("demo-account password leaked into the ops result")
	}
}

// ── build attach ──

func TestStoreBuildAttachResolvesByVersion(t *testing.T) {
	f := newFakeASC(t)
	f.buildsJSON = `[{"id":"build-old","attributes":{"version":"41","processingState":"PROCESSING"}},
	                 {"id":"build-9","attributes":{"version":"42","processingState":"VALID"}}]`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS", "version": "1.0"})

	res := storeBuildAttachHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("attach: %s", res.Error)
	}
	// Skips the still-PROCESSING build; picks the VALID one.
	if init := opsInitial(t, res); init["buildId"] != "build-9" {
		t.Errorf("buildId = %v, want build-9 (the processed one)", init["buildId"])
	}
	rel, _ := f.patchedVer["build"].(map[string]interface{})
	data, _ := rel["data"].(map[string]interface{})
	if data["id"] != "build-9" || data["type"] != "builds" {
		t.Errorf("PATCH /appStoreVersions relationship = %#v", f.patchedVer)
	}
}

func TestStoreBuildAttachNoProcessedBuild(t *testing.T) {
	f := newFakeASC(t)
	f.buildsJSON = `[]`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeBuildAttachHandler(OpsContext{}, payload)
	if res.OK {
		t.Fatal("must fail when Apple has no processed build")
	}
	if res.Code != "not_found" || !strings.Contains(res.Error, "upload one first") {
		t.Errorf("res = %+v", res)
	}
}

// ── metadata + review details ──

func TestStoreMetadataSetOnlyPatchesSuppliedFields(t *testing.T) {
	f := newFakeASC(t)
	payload, _ := json.Marshal(map[string]interface{}{
		"bundleId":   "com.acme.app",
		"platform":   "VISION_OS",
		"keywords":   "spatial,dev",
		"supportUrl": "https://example.com/help",
	})
	res := storeMetadataSetHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("metadata: %s", res.Error)
	}
	if len(f.patchedLoc) != 2 || f.patchedLoc["keywords"] != "spatial,dev" || f.patchedLoc["supportUrl"] != "https://example.com/help" {
		t.Errorf("PATCH must carry ONLY the supplied fields, got %#v", f.patchedLoc)
	}
	if _, present := f.patchedLoc["description"]; present {
		t.Error("an unsupplied field must not be wiped")
	}
}

func TestStoreMetadataSetNothingToDo(t *testing.T) {
	newFakeASC(t)
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})
	if res := storeMetadataSetHandler(OpsContext{}, payload); res.OK || res.Code != "bad_payload" {
		t.Fatalf("res = %+v", res)
	}
}

func TestStoreReviewDetailsSetCreatesWhenAbsent(t *testing.T) {
	f := newFakeASC(t)
	f.reviewDetailJSON = `null`
	payload, _ := json.Marshal(map[string]interface{}{
		"bundleId":            "com.acme.app",
		"platform":            "VISION_OS",
		"notes":               "Sign in with the demo account.",
		"demoAccountName":     "demo",
		"demoAccountPassword": "hunter2",
		"demoAccountRequired": true,
		"contactEmail":        "dev@acme.com",
	})
	res := storeReviewDetailsSetHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("review details: %s", res.Error)
	}
	if !f.called("POST /appStoreReviewDetails") {
		t.Errorf("must CREATE the form when the version has none; calls = %v", f.calls)
	}
	if f.patchedDetail["demoAccountRequired"] != true {
		t.Errorf("attrs = %#v", f.patchedDetail)
	}
	blob, _ := json.Marshal(res.Initial)
	if strings.Contains(string(blob), "hunter2") {
		t.Fatal("demo-account password echoed back in the result")
	}
}

func TestStoreReviewDetailsSetPatchesWhenPresent(t *testing.T) {
	f := newFakeASC(t)
	f.reviewDetailJSON = `{"id":"rd-1","attributes":{"notes":"old"}}`
	payload, _ := json.Marshal(map[string]interface{}{
		"bundleId": "com.acme.app", "platform": "VISION_OS", "notes": "new notes",
	})
	if res := storeReviewDetailsSetHandler(OpsContext{}, payload); !res.OK {
		t.Fatalf("review details: %s", res.Error)
	}
	if !f.called("PATCH /appStoreReviewDetails/rd-1") {
		t.Errorf("must PATCH the existing form; calls = %v", f.calls)
	}
}

// ── cancel ──

func TestStoreSubmitCancel(t *testing.T) {
	f := newFakeASC(t)
	f.reviewSubsJSON = `[{"id":"sub-1","attributes":{"platform":"VISION_OS","state":"WAITING_FOR_REVIEW","submitted":true}}]`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitCancelHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("cancel: %s", res.Error)
	}
	if f.patchedSub["canceled"] != true {
		t.Errorf("cancel must PATCH canceled:true, got %#v", f.patchedSub)
	}
}

func TestStoreSubmitCancelNothingWaiting(t *testing.T) {
	f := newFakeASC(t)
	f.reviewSubsJSON = `[{"id":"sub-1","attributes":{"platform":"VISION_OS","state":"IN_REVIEW","submitted":true}}]`
	payload, _ := json.Marshal(map[string]interface{}{"bundleId": "com.acme.app", "platform": "VISION_OS"})

	res := storeSubmitCancelHandler(OpsContext{}, payload)
	if res.OK {
		t.Fatal("IN_REVIEW cannot be canceled over the API — must not pretend")
	}
	if !strings.Contains(res.Error, "IN_REVIEW") {
		t.Errorf("error should name the actual state:\n%s", res.Error)
	}
}

// ── units ──

func TestASCAssociatedErrorsParse(t *testing.T) {
	body := []byte(`{"errors":[{"code":"X","meta":{"associatedErrors":{
		"/v1/appStoreVersions/1":[{"code":"A","detail":"da"}],
		"/v1/appStoreVersionLocalizations/2":[{"code":"B","detail":"db"},{"code":"C","detail":"dc"}]
	}}}]}`)
	got := ascAssociatedErrors(body)
	if len(got) != 3 {
		t.Fatalf("got %d associated errors, want 3: %#v", len(got), got)
	}
	// Stable order: by resource, then code.
	if got[0].Code != "B" || got[1].Code != "C" || got[2].Code != "A" {
		t.Fatalf("unstable order: %#v", got)
	}
	if !strings.Contains(got[2].String(), "/v1/appStoreVersions/1") {
		t.Errorf("String() must keep the resource: %q", got[2].String())
	}
	if ascAssociatedErrors([]byte("not json")) != nil {
		t.Error("garbage should yield no associated errors")
	}
}

func TestNormalizePlatform(t *testing.T) {
	cases := map[string]string{
		"": "IOS", "ios": "IOS", "IOS": "IOS",
		"visionos": "VISION_OS", "VISION_OS": "VISION_OS", "vision-os": "VISION_OS", "xrOS": "VISION_OS",
		"tvos": "TV_OS", "macos": "MAC_OS",
	}
	for in, want := range cases {
		got, err := normalizePlatform(in)
		if err != nil || got != want {
			t.Errorf("normalizePlatform(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := normalizePlatform("watchos"); err == nil {
		t.Error("unknown platform must error, not guess")
	}
}

func TestRunUploadOperationsRejectsOutOfRange(t *testing.T) {
	ops := []ascUploadOperation{{Method: "PUT", URL: "http://127.0.0.1:1/x", Offset: 0, Length: 99}}
	if err := runUploadOperations(ops, []byte("short")); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("err = %v, want an out-of-range guard", err)
	}
	if err := runUploadOperations(nil, []byte("x")); err == nil {
		t.Fatal("no uploadOperations must be an error, not a silent success")
	}
}
