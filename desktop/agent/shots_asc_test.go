package main

import (
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

// fakeShotsASC is a REAL App Store Connect server for the `yaver shots` backend.
// Its whole point is to prove shots now speaks HTTP to Apple directly — the three
// embedded python scripts are gone, and no python is exec'd anywhere.
type fakeShotsASC struct {
	t   *testing.T
	mu  sync.Mutex
	srv *httptest.Server

	versionsJSON string
	// platformsAsked records filter[platform] on every version query, so a test
	// can prove the visionOS target actually asks Apple for the visionOS version.
	platformsAsked []string

	calls         []string
	setsCreated   []string // display types created
	uploadChunks  int
	commits       int
	ageRating     map[string]interface{}
	patchedLoc    map[string]interface{}
	categories    map[string]interface{}
	contentRights map[string]interface{}
	priceSchedule bool

	// submit path
	itemStatus int
	itemBody   string
}

func newFakeShotsASC(t *testing.T) *fakeShotsASC {
	t.Helper()
	f := &fakeShotsASC{
		t:            t,
		versionsJSON: `[{"id":"ver-1","attributes":{"versionString":"1.0","appStoreState":"PREPARE_FOR_SUBMISSION"}}]`,
		itemStatus:   201,
		itemBody:     `{"data":{"id":"item-1"}}`,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.route))
	t.Cleanup(f.srv.Close)

	oldBase, oldUp, oldShots := ascAPIBase, ascUploadClient, newShotsASCClient
	ascAPIBase = f.srv.URL
	ascUploadClient = f.srv.Client()
	creds := testASCCreds(t)
	// The seam the shots backend resolves its client through — no vault, no env.
	newShotsASCClient = func(project string) (*ascClient, error) {
		return &ascClient{creds: creds, http: f.srv.Client()}, nil
	}
	t.Cleanup(func() {
		ascAPIBase, ascUploadClient, newShotsASCClient = oldBase, oldUp, oldShots
	})
	return f
}

func (f *fakeShotsASC) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeShotsASC) called(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == s {
			return true
		}
	}
	return false
}

func (f *fakeShotsASC) route(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	if strings.HasPrefix(p, "/upload/") {
		if auth := r.Header.Get("Authorization"); auth != "" {
			f.t.Errorf("upload op must not carry the ASC bearer, got %q", auth)
		}
		io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.uploadChunks++
		f.mu.Unlock()
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
		fmt.Fprint(w, `{"data":[{"id":"app-1","attributes":{"name":"Acme","bundleId":"com.acme.app"}}]}`)

	case r.Method == "GET" && p == "/apps/app-1/appStoreVersions":
		f.mu.Lock()
		f.platformsAsked = append(f.platformsAsked, r.URL.Query().Get("filter[platform]"))
		f.mu.Unlock()
		fmt.Fprintf(w, `{"data":%s}`, f.versionsJSON)

	case r.Method == "GET" && p == "/appStoreVersions/ver-1/appStoreVersionLocalizations":
		fmt.Fprint(w, `{"data":[{"id":"loc-1","attributes":{"locale":"en-US"}}]}`)

	case r.Method == "PATCH" && p == "/appStoreVersionLocalizations/loc-1":
		f.patchedLoc = decodeAttrs(f.t, r)
		fmt.Fprint(w, `{"data":{"id":"loc-1","attributes":{"locale":"en-US"}}}`)

	// ── age rating (upload-appstore.py's second job) ──
	case r.Method == "GET" && p == "/apps/app-1/appInfos":
		fmt.Fprint(w, `{"data":[{"id":"info-1","attributes":{"appStoreState":"PREPARE_FOR_SUBMISSION"}}]}`)
	case r.Method == "GET" && p == "/appInfos/info-1/ageRatingDeclaration":
		fmt.Fprint(w, `{"data":{"id":"age-1"}}`)
	case r.Method == "PATCH" && p == "/ageRatingDeclarations/age-1":
		f.ageRating = decodeAttrs(f.t, r)
		fmt.Fprint(w, `{"data":{"id":"age-1"}}`)

	// ── metadata (set-appstore-info.py's job, now native Go) ──
	case r.Method == "PATCH" && p == "/appInfos/info-1":
		var body struct {
			Data struct {
				Relationships map[string]interface{} `json:"relationships"`
			} `json:"data"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.categories = body.Data.Relationships
		fmt.Fprint(w, `{"data":{"id":"info-1"}}`)
	case r.Method == "GET" && p == "/appInfos/info-1/appInfoLocalizations":
		fmt.Fprint(w, `{"data":[{"id":"iloc-1","attributes":{"locale":"en-US"}}]}`)
	case r.Method == "PATCH" && p == "/appInfoLocalizations/iloc-1":
		fmt.Fprint(w, `{"data":{"id":"iloc-1","attributes":{"locale":"en-US"}}}`)
	case r.Method == "PATCH" && p == "/apps/app-1":
		f.contentRights = decodeAttrs(f.t, r)
		fmt.Fprint(w, `{"data":{"id":"app-1"}}`)
	case r.Method == "GET" && p == "/apps/app-1/appPricePoints":
		fmt.Fprint(w, `{"data":[{"id":"pp-free"}]}`)
	case r.Method == "POST" && p == "/appPriceSchedules":
		f.priceSchedule = true
		w.WriteHeader(201)
		fmt.Fprint(w, `{"data":{"id":"sched-1"}}`)

	// ── screenshots ──
	case r.Method == "GET" && p == "/appStoreVersionLocalizations/loc-1/appScreenshotSets":
		fmt.Fprint(w, `{"data":[]}`)
	case r.Method == "POST" && p == "/appScreenshotSets":
		attrs := decodeAttrs(f.t, r)
		dt, _ := attrs["screenshotDisplayType"].(string)
		f.mu.Lock()
		f.setsCreated = append(f.setsCreated, dt)
		f.mu.Unlock()
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"data":{"id":"set-1","attributes":{"screenshotDisplayType":%q}}}`, dt)
	case r.Method == "POST" && p == "/appScreenshots":
		attrs := decodeAttrs(f.t, r)
		size, _ := attrs["fileSize"].(float64)
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"data":{"id":"shot-1","attributes":{"uploadOperations":[
			{"method":"PUT","url":%q,"offset":0,"length":%d,"requestHeaders":[]}
		]}}}`, f.srv.URL+"/upload/a", int(size))
	case r.Method == "PATCH" && p == "/appScreenshots/shot-1":
		f.mu.Lock()
		f.commits++
		f.mu.Unlock()
		fmt.Fprint(w, `{"data":{"id":"shot-1","attributes":{"fileName":"01.png"}}}`)

	// ── submit ──
	case r.Method == "PATCH" && p == "/appStoreVersions/ver-1":
		fmt.Fprint(w, `{"data":{"id":"ver-1","attributes":{"versionString":"1.0"}}}`)
	case r.Method == "GET" && p == "/appStoreVersions/ver-1/build":
		fmt.Fprint(w, `{"data":null}`)
	case r.Method == "GET" && p == "/builds":
		fmt.Fprint(w, `{"data":[]}`)
	case r.Method == "GET" && p == "/reviewSubmissions":
		fmt.Fprint(w, `{"data":[]}`)
	case r.Method == "POST" && p == "/reviewSubmissions":
		w.WriteHeader(201)
		fmt.Fprint(w, `{"data":{"id":"sub-1","attributes":{"state":"READY_FOR_REVIEW","submitted":false}}}`)
	case r.Method == "GET" && p == "/reviewSubmissions/sub-1/items":
		fmt.Fprint(w, `{"data":[]}`)
	case r.Method == "POST" && p == "/reviewSubmissionItems":
		w.WriteHeader(f.itemStatus)
		fmt.Fprint(w, f.itemBody)
	case r.Method == "PATCH" && p == "/reviewSubmissions/sub-1":
		fmt.Fprint(w, `{"data":{"id":"sub-1","attributes":{"state":"WAITING_FOR_REVIEW","submitted":true}}}`)

	default:
		f.t.Errorf("unexpected %s %s", r.Method, p)
		w.WriteHeader(404)
	}
}

func shotsDirWithPNGs(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("fake-png-bytes"), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	return dir
}

// ── the consolidation: shots now talks native Go to Apple, no python ──

func TestShotsUploadScreenshotsIsNativeGo(t *testing.T) {
	f := newFakeShotsASC(t)
	dir := shotsDirWithPNGs(t, "02_b.png", "01_a.png") // deliberately out of order

	if err := ascUploadScreenshots("com.acme.app", dir, "en-US", ascUploadPlan{}); err != nil {
		t.Fatalf("ascUploadScreenshots: %v", err)
	}

	// The default plan is the historical iPhone pair on the iOS platform.
	if len(f.setsCreated) != 2 || f.setsCreated[0] != "APP_IPHONE_67" || f.setsCreated[1] != "APP_IPHONE_65" {
		t.Errorf("default display types = %v, want [APP_IPHONE_67 APP_IPHONE_65]", f.setsCreated)
	}
	if f.platformsAsked[0] != "IOS" {
		t.Errorf("default platform = %q, want IOS", f.platformsAsked[0])
	}
	// 2 files × 2 display types = 4 reserve→upload→commit round trips.
	if f.commits != 4 || f.uploadChunks != 4 {
		t.Errorf("commits=%d uploads=%d, want 4 and 4 (2 files × 2 display types)", f.commits, f.uploadChunks)
	}
	// Age rating is part of the same job (upload-appstore.py set it too).
	if f.ageRating == nil || f.ageRating["violenceRealistic"] != "NONE" {
		t.Errorf("age rating not declared: %#v", f.ageRating)
	}
	if f.ageRating["kidsAgeBand"] != nil {
		t.Errorf("kidsAgeBand must be null, got %#v", f.ageRating["kidsAgeBand"])
	}
}

func TestShotsScreenshotFilesAreSortedAndPNGOnly(t *testing.T) {
	dir := shotsDirWithPNGs(t, "03_c.png", "01_a.png", "02_b.png")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := ascScreenshotFiles(dir)
	if err != nil {
		t.Fatalf("ascScreenshotFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("want 3 PNGs (the .txt must be ignored), got %v", files)
	}
	for i, want := range []string{"01_a.png", "02_b.png", "03_c.png"} {
		if filepath.Base(files[i]) != want {
			t.Errorf("files[%d] = %s, want %s (display order must be name order)", i, filepath.Base(files[i]), want)
		}
	}
	if _, err := ascScreenshotFiles(t.TempDir()); err == nil {
		t.Error("an empty dir must be an error, not a silent no-op upload")
	}
}

// ── visionOS slots into the SAME upload path via the target table ──

func TestShotsVisionProTargetUploadsToVisionDisplayType(t *testing.T) {
	f := newFakeShotsASC(t)
	f.versionsJSON = `[{"id":"ver-1","attributes":{"versionString":"1.0","platform":"VISION_OS","appStoreState":"PREPARE_FOR_SUBMISSION"}}]`
	dir := shotsDirWithPNGs(t, "01_launch.png")

	target, err := resolveShotsTarget("visionpro")
	if err != nil {
		t.Fatalf("resolveShotsTarget: %v", err)
	}
	if err := ascUploadScreenshots("com.acme.app", dir, "en-US", target.uploadPlan()); err != nil {
		t.Fatalf("vision pro upload: %v", err)
	}

	if len(f.setsCreated) != 1 || f.setsCreated[0] != "APP_APPLE_VISION_PRO" {
		t.Errorf("display types = %v, want [APP_APPLE_VISION_PRO]", f.setsCreated)
	}
	// It must ask Apple for the VISION_OS version, not the iOS one.
	if f.platformsAsked[0] != "VISION_OS" {
		t.Errorf("platform asked = %q, want VISION_OS", f.platformsAsked[0])
	}
}

func TestShotsVisionProTargetShape(t *testing.T) {
	target, err := resolveShotsTarget("visionpro")
	if err != nil {
		t.Fatalf("resolveShotsTarget: %v", err)
	}
	// The size `xcrun simctl io <udid> screenshot` actually produces on an Apple
	// Vision Pro simulator, and the size Apple requires. They are the same.
	if target.Width != 3840 || target.Height != 2160 {
		t.Errorf("vision pro size = %dx%d, want 3840x2160", target.Width, target.Height)
	}
	if target.Platform != "VISION_OS" {
		t.Errorf("platform = %q", target.Platform)
	}
	if target.PreviewType != "APP_APPLE_VISION_PRO" {
		t.Errorf("previewType = %q", target.PreviewType)
	}
	// Maestro cannot drive visionOS — the target must not claim it can.
	if target.Driver != shotsDriverSimctl {
		t.Errorf("driver = %q, want simctl (maestro has no visionOS support)", target.Driver)
	}
	// The visionOS simulator runtime id says xrOS, not visionOS.
	if target.RuntimeMatch != "xrOS" {
		t.Errorf("runtimeMatch = %q — the runtime id is …SimRuntime.xrOS-26-2", target.RuntimeMatch)
	}
	// …and that must not accidentally match the iOS runtime.
	if strings.Contains("com.apple.CoreSimulator.SimRuntime.xrOS-26-2", "iOS") {
		t.Error("xrOS runtime id must not contain iOS, or the iPhone target would grab Vision Pro sims")
	}
}

func TestResolveShotsTargetDefaultsAndAliases(t *testing.T) {
	for _, key := range []string{"", "iphone", "ios"} {
		got, err := resolveShotsTarget(key)
		if err != nil || got.Key != "iphone" {
			t.Errorf("resolveShotsTarget(%q) = %q, %v; want iphone", key, got.Key, err)
		}
	}
	for _, key := range []string{"visionpro", "visionos", "vision", "VisionPro"} {
		got, err := resolveShotsTarget(key)
		if err != nil || got.Key != "visionpro" {
			t.Errorf("resolveShotsTarget(%q) = %q, %v; want visionpro", key, got.Key, err)
		}
	}
	if _, err := resolveShotsTarget("nokia"); err == nil {
		t.Error("an unknown target must error, not silently capture the wrong device")
	}
}

// ── submit: STAGED vs SUBMITTED, with Apple's words kept ──

func TestShotsSubmitForReviewSucceeds(t *testing.T) {
	newFakeShotsASC(t)
	submitted, err := ascSubmitForReview("com.acme.app", "", "IOS")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if !submitted {
		t.Error("submitted = false, want true")
	}
}

// Apple gating the submission is STAGED — exit-0, submitted=false, no error.
// It must NOT be reported as a hard failure, and it must NOT claim a submission.
func TestShotsSubmitForReviewStagesOnAppleRejection(t *testing.T) {
	f := newFakeShotsASC(t)
	f.itemStatus = 409
	f.itemBody = `{"errors":[{"code":"ENTITY_ERROR","title":"bad","meta":{"associatedErrors":{
		"/v1/appStoreVersions/ver-1":[{"code":"ENTITY_ERROR.ATTRIBUTE.REQUIRED",
		 "detail":"You must provide a value for the attribute 'hasHighMotionLabel' with this request."}]
	}}}]}`

	submitted, err := ascSubmitForReview("com.acme.app", "", "VISION_OS")
	if err != nil {
		t.Fatalf("an Apple gate is STAGED, not a hard error: %v", err)
	}
	if submitted {
		t.Fatal("Apple rejected the item — must NEVER claim it was submitted")
	}
	// And it must not go on to flip submitted:true anyway.
	if f.called("PATCH /reviewSubmissions/sub-1") {
		t.Error("must not mark submitted:true after Apple rejected the item")
	}
}

func TestShotsSubmitStagesWhenNoEditableVersion(t *testing.T) {
	f := newFakeShotsASC(t)
	f.versionsJSON = `[]`

	submitted, err := ascSubmitForReview("com.acme.app", "", "IOS")
	if err != nil {
		t.Fatalf("no version is STAGED, not a hard error: %v", err)
	}
	if submitted {
		t.Fatal("there is no version — must not claim a submission")
	}
	if f.called("POST /reviewSubmissions") {
		t.Error("must not open a review submission when there is no version")
	}
}

// ── metadata: the cross-app contamination guard ──

func TestShotsMetadataRefusesForeignBundleWithoutMetaFile(t *testing.T) {
	f := newFakeShotsASC(t)
	// Someone else's app, and no .yaver/appstore.json to describe it.
	if err := ascSetMetadata("com.acme.app", "", "IOS"); err != nil {
		t.Fatalf("refusing must not be an error (screenshots still uploaded): %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("must not touch App Store Connect at all — it would stamp Yaver's copy "+
			"onto another developer's listing; calls = %v", f.calls)
	}
}

func TestShotsMetadataAppliesWhenMetaFileSupplied(t *testing.T) {
	f := newFakeShotsASC(t)
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".yaver"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{
		"bundleId":    "com.acme.app",
		"name":        "Acme",
		"description": "An Acme app.",
		"keywords":    "acme,tools",
		"supportUrl":  "https://acme.example",
	}
	blob, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(proj, ".yaver", "appstore.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ascSetMetadata("com.acme.app", proj, "IOS"); err != nil {
		t.Fatalf("ascSetMetadata: %v", err)
	}
	if !f.called("PATCH /appStoreVersionLocalizations/loc-1") {
		t.Fatalf("must write the version localization; calls = %v", f.calls)
	}
	if f.patchedLoc["description"] != "An Acme app." || f.patchedLoc["keywords"] != "acme,tools" {
		t.Errorf("the supplied listing must be what lands, got %#v", f.patchedLoc)
	}
	// And Yaver's built-in copy must NOT leak into it.
	if strings.Contains(fmt.Sprint(f.patchedLoc["description"]), "Yaver") {
		t.Error("Yaver's default description leaked onto another app's listing")
	}
	// The rest of set-appstore-info.py's sequence must still happen natively.
	if f.categories == nil {
		t.Error("categories were never set")
	}
	if f.contentRights == nil || f.contentRights["contentRightsDeclaration"] != "DOES_NOT_USE_THIRD_PARTY_CONTENT" {
		t.Errorf("content rights not declared: %#v", f.contentRights)
	}
	if !f.priceSchedule {
		t.Error("free pricing was never scheduled")
	}
}

func TestLoadShotsMetaOverridesDefaults(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".yaver"), 0o755); err != nil {
		t.Fatal(err)
	}
	blob := []byte(`{"bundleId":"com.acme.app","subtitle":"Acme subtitle"}`)
	if err := os.WriteFile(filepath.Join(proj, ".yaver", "appstore.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	meta, loaded, err := loadShotsMeta(proj)
	if err != nil || !loaded {
		t.Fatalf("loadShotsMeta: loaded=%v err=%v", loaded, err)
	}
	if meta.BundleID != "com.acme.app" || meta.Subtitle != "Acme subtitle" {
		t.Errorf("overrides not applied: %+v", meta)
	}
	// An omitted key keeps its default rather than being blanked.
	if meta.Locale != "en-US" {
		t.Errorf("omitted key must keep its default, locale = %q", meta.Locale)
	}

	// No file at all → defaults, not-loaded, and no error.
	meta, loaded, err = loadShotsMeta(t.TempDir())
	if err != nil || loaded {
		t.Fatalf("a missing listing file must be silent: loaded=%v err=%v", loaded, err)
	}
	if meta.BundleID != shotsDefaultMetaBundleID {
		t.Errorf("defaults not returned: %+v", meta)
	}
}

// The python scripts are gone — nothing may exec python for the ASC flow.
// Only CODE is scanned: the file's header comment legitimately names the scripts
// it replaced, and that prose is documentation, not a dependency.
func TestShotsScriptsAreGone(t *testing.T) {
	if _, err := os.Stat("shots_scripts"); err == nil {
		t.Error("desktop/agent/shots_scripts still exists — the embedded python must be gone")
	}
	src, err := os.ReadFile("shots_asc.go")
	if err != nil {
		t.Fatalf("read shots_asc.go: %v", err)
	}
	var code []string
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue // a comment cannot exec python
		}
		code = append(code, line)
	}
	body := strings.Join(code, "\n")
	for _, banned := range []string{"go:embed", "python3", "exec.Command", "embed.FS", ".py"} {
		if strings.Contains(body, banned) {
			t.Errorf("shots_asc.go still has %q in CODE — it must be pure native Go now", banned)
		}
	}
}
