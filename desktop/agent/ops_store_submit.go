package main

// ops_store_submit.go — App Store *submission* verbs for the `ops` MCP
// grand-tool. Until now Yaver could upload a build (testflight.go) and manage
// TestFlight testers (appstoreconnect.go / ops_store.go), but the actual
// App Store submission was modelled as a human Console job
// (publish_status.go: "Console forms the human must submit"). These verbs
// encode the App Store Connect REST sequence that was proven by hand against
// the live API for a visionOS submission.
//
// Multi-tenant like every other store_* verb: each verb takes an optional
// `project` and resolves that project's ASC key from its vault scope via
// newASCClient(project) → resolveAppleASCCreds(project). A managed-cloud box
// submits dev B's app with dev B's key, never Yaver's.
//
// ─────────────────────────────────────────────────────────────────────────────
// TWO GATES APPLE DOES NOT EXPOSE OVER THE API. Verified against the live API,
// not assumed. We refuse to fake them:
//
//   1. ADD A PLATFORM TO AN APP (e.g. enabling visionOS on an existing bundle
//      id). There is no ASC REST endpoint that creates an app-platform. Until a
//      human does it in the Console, /apps/{id}/appStoreVersions is empty for
//      that platform and there is nothing to submit.
//
//   2. APP MOTION INFORMATION (`hasHighMotionLabel`, required for visionOS).
//      PATCHing it was attempted against appStoreVersions, ageRatingDeclarations,
//      appStoreVersionLocalizations and builds — ALL four return
//      ENTITY_ERROR.ATTRIBUTE.UNKNOWN. It is not an attribute on any public
//      resource, and it cannot even be *read* back. Console-only.
//
// store_submit_status reports both as explicit human steps with the exact
// Console path. store_submit_for_review fails with that message rather than
// pretending. Gate 2 is only discoverable at submit time: Apple reports it in
// `meta.associatedErrors` on the 409 from POST /reviewSubmissionItems — which
// is exactly how it was found. Those associated errors are surfaced VERBATIM;
// they are never collapsed into "submission failed".
// ─────────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ascUploadClient performs the raw asset PUTs that App Store Connect hands back
// in `uploadOperations`. Those go to a DIFFERENT host, carry Apple-supplied
// headers, and must NOT carry the ASC JWT — so they deliberately bypass
// ascClient.do. Overridable in tests.
var ascUploadClient = &http.Client{Timeout: 10 * time.Minute}

// newASCClientFn is the seam the submission verbs resolve their client through.
// Production points at newASCClient (per-project vault creds); tests point it at
// a client aimed at a real httptest ASC server, so the handlers — Console gates
// included — are exercised end to end without a vault.
var newASCClientFn = newASCClient

// ─────────────────────────── Console-only gates ────────────────────────────

// consoleGate is a step a human must perform in App Store Connect because
// Apple ships no API for it. Never silently "handled".
type consoleGate struct {
	Gate        string `json:"gate"`
	Reason      string `json:"reason"`
	ConsolePath string `json:"consolePath"`
	// Verifiable reports whether the agent can even CHECK this over the API.
	// The motion label cannot be read back, so it is always false for it.
	Verifiable bool `json:"verifiable"`
}

func gateAddPlatform(appName, platform string) consoleGate {
	return consoleGate{
		Gate: "add_platform",
		Reason: fmt.Sprintf(
			"%s has no App Store version for platform %s. Adding a platform to an existing app has NO App Store Connect REST endpoint — it cannot be automated. A human must add it once; after that every verb here works.",
			appName, platform),
		ConsolePath: "App Store Connect → Apps → " + appName +
			" → platform selector in the left sidebar → (+) → add " + platform +
			" → create the first version. Then re-run store_submit_status.",
		Verifiable: true,
	}
}

func gateMotionInfo(appName, platform string) consoleGate {
	return consoleGate{
		Gate: "app_motion_information",
		Reason: "App Motion Information (`hasHighMotionLabel`) is REQUIRED for " + platform +
			" and is not an attribute on any public ASC resource. PATCHing it against appStoreVersions, ageRatingDeclarations, appStoreVersionLocalizations and builds all return ENTITY_ERROR.ATTRIBUTE.UNKNOWN. It can neither be set nor read over the API — this agent cannot verify or perform it.",
		ConsolePath: "App Store Connect → Apps → " + appName +
			" → the " + platform + " version → App Review Information → App Motion Information → declare whether the app contains high-motion content, and Save. Apple reports this as an associatedError on POST /reviewSubmissionItems if it is missing.",
		Verifiable: false,
	}
}

// motionLabelBlocked reports whether Apple's associated errors are complaining
// about the motion label, so we can attach the exact Console path.
func motionLabelBlocked(assoc []ascAssocError) bool {
	for _, e := range assoc {
		hay := strings.ToLower(e.Code + " " + e.Title + " " + e.Detail)
		if strings.Contains(hay, "highmotion") || strings.Contains(hay, "motion") {
			return true
		}
	}
	return false
}

// ───────────────────── rich Apple errors (associatedErrors) ─────────────────

// ascAssocError is one entry from `errors[].meta.associatedErrors`. Apple keys
// them by the resource path they apply to; each carries its own code/detail.
// This is the ONLY place Apple tells you which specific metadata field is
// blocking a submission — surfaced verbatim, never summarised away.
type ascAssocError struct {
	Resource string `json:"resource"`
	Code     string `json:"code"`
	Status   string `json:"status,omitempty"`
	Title    string `json:"title,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func (e ascAssocError) String() string {
	s := e.Code
	if e.Detail != "" {
		s += ": " + e.Detail
	} else if e.Title != "" {
		s += ": " + e.Title
	}
	if e.Resource != "" {
		s += "  [" + e.Resource + "]"
	}
	return s
}

// ascAssociatedErrors pulls every meta.associatedErrors entry out of an ASC
// error envelope, flattened and stably sorted.
func ascAssociatedErrors(body []byte) []ascAssocError {
	var env struct {
		Errors []struct {
			Code   string `json:"code"`
			Title  string `json:"title"`
			Detail string `json:"detail"`
			Meta   struct {
				AssociatedErrors map[string][]struct {
					Code   string `json:"code"`
					Status string `json:"status"`
					Title  string `json:"title"`
					Detail string `json:"detail"`
				} `json:"associatedErrors"`
			} `json:"meta"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &env) != nil {
		return nil
	}
	var out []ascAssocError
	for _, e := range env.Errors {
		for resource, list := range e.Meta.AssociatedErrors {
			for _, a := range list {
				out = append(out, ascAssocError{
					Resource: resource,
					Code:     a.Code,
					Status:   a.Status,
					Title:    a.Title,
					Detail:   a.Detail,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Resource != out[j].Resource {
			return out[i].Resource < out[j].Resource
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// ascFailure turns a failed ascClient.do into an OpsResult that keeps Apple's
// own words. do() returns the response body even on error, so nothing is lost.
func ascFailure(what string, body []byte, err error) OpsResult {
	assoc := ascAssociatedErrors(body)
	msg := what + ": " + err.Error()
	if len(assoc) > 0 {
		lines := make([]string, 0, len(assoc)+1)
		lines = append(lines, msg, "Apple reported these specific problems (verbatim):")
		for _, a := range assoc {
			lines = append(lines, "  • "+a.String())
		}
		msg = strings.Join(lines, "\n")
	}
	res := OpsResult{OK: false, Code: "apple_error", Error: msg}
	if len(assoc) > 0 {
		res.Code = "apple_rejected"
		res.Initial = map[string]interface{}{"associatedErrors": assoc}
	}
	return res
}

// ────────────────────────────── platform helpers ────────────────────────────

// normalizePlatform maps the friendly spellings onto Apple's enum.
func normalizePlatform(p string) (string, error) {
	switch strings.ToUpper(strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.TrimSpace(p))) {
	case "", "IOS", "IPHONEOS", "IPADOS":
		return "IOS", nil
	case "MACOS", "MAC", "OSX":
		return "MAC_OS", nil
	case "TVOS", "APPLETV":
		return "TV_OS", nil
	case "VISIONOS", "VISIONPRO", "XROS":
		return "VISION_OS", nil
	}
	return "", fmt.Errorf("unknown platform %q (want IOS | MAC_OS | TV_OS | VISION_OS)", p)
}

// ascDisplayTypes documents the screenshot display types we know about. Unknown
// values are still passed through to Apple — this map only powers hints.
var ascDisplayTypes = map[string]string{
	"APP_APPLE_VISION_PRO":  "Apple Vision Pro — 3840x2160",
	"APP_IPHONE_67":         "iPhone 6.7\" — 1290x2796",
	"APP_IPHONE_65":         "iPhone 6.5\" — 1242x2688 / 1284x2778",
	"APP_IPHONE_61":         "iPhone 6.1\" — 1179x2556",
	"APP_IPHONE_58":         "iPhone 5.8\" — 1125x2436",
	"APP_IPHONE_55":         "iPhone 5.5\" — 1242x2208",
	"APP_IPAD_PRO_3GEN_129": "iPad Pro 12.9\" (3rd gen) — 2048x2732",
	"APP_IPAD_PRO_3GEN_11":  "iPad Pro 11\" — 1668x2388",
	"APP_IPAD_PRO_129":      "iPad Pro 12.9\" (2nd gen) — 2048x2732",
	"APP_APPLE_TV":          "Apple TV — 1920x1080 / 3840x2160",
	"APP_DESKTOP":           "Mac — 1280x800 … 2880x1800",
	"APP_WATCH_ULTRA":       "Apple Watch Ultra — 410x502",
}

// editableVersionStates are the appStoreState values where metadata,
// screenshots and the build attachment can still be changed.
var editableVersionStates = map[string]bool{
	"PREPARE_FOR_SUBMISSION": true,
	"METADATA_REJECTED":      true,
	"DEVELOPER_REJECTED":     true,
	"REJECTED":               true,
	"INVALID_BINARY":         true,
}

// ─────────────────────────── ASC submission client ──────────────────────────

// ASCVersion is one App Store version (a platform+versionString pair).
type ASCVersion struct {
	ID              string `json:"id"`
	VersionString   string `json:"versionString"`
	Platform        string `json:"platform"`
	AppStoreState   string `json:"appStoreState,omitempty"`
	AppVersionState string `json:"appVersionState,omitempty"`
	ReleaseType     string `json:"releaseType,omitempty"`
	CreatedDate     string `json:"createdDate,omitempty"`
	Editable        bool   `json:"editable"`
}

// state returns whichever state field Apple populated. The API is mid-migration
// from `appStoreState` to `appVersionState`; both are read so we don't silently
// report an empty state when Apple flips the default.
func (v *ASCVersion) state() string {
	if v.AppVersionState != "" {
		return v.AppVersionState
	}
	return v.AppStoreState
}

// Versions lists the app's versions for a platform, newest first.
func (a *ascClient) Versions(appID, platform string) ([]ASCVersion, error) {
	// NO `sort=` here. Apple rejects it on this relationship with
	// "400 The parameter 'sort' can not be used with this request" — found by
	// running against the live API, not the fake (which happily accepted it).
	// Callers depend on newest-first, so we order client-side below.
	path := "/apps/" + appID + "/appStoreVersions?filter[platform]=" + platform + "&limit=20"
	out, _, err := a.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string     `json:"id"`
			Attributes ASCVersion `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	vs := make([]ASCVersion, 0, len(r.Data))
	for _, d := range r.Data {
		v := d.Attributes
		v.ID = d.ID
		v.Editable = editableVersionStates[v.state()]
		vs = append(vs, v)
	}
	// Newest first. createdDate is RFC3339, so a lexical compare orders it
	// correctly; ties (or a missing date) keep Apple's own order via SliceStable.
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].CreatedDate > vs[j].CreatedDate })
	return vs, nil
}

// EditableVersion picks the version a submission should target: the newest one
// still in an editable state, else the newest overall (so callers can report
// "already WAITING_FOR_REVIEW" instead of a bare not-found).
func (a *ascClient) EditableVersion(appID, platform string) (*ASCVersion, error) {
	vs, err := a.Versions(appID, platform)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, nil // caller raises the add_platform Console gate
	}
	for i := range vs {
		if vs[i].Editable {
			return &vs[i], nil
		}
	}
	return &vs[0], nil
}

// ASCVersionLocalization is the per-locale metadata on a version.
type ASCVersionLocalization struct {
	ID              string `json:"id"`
	Locale          string `json:"locale"`
	Description     string `json:"description,omitempty"`
	Keywords        string `json:"keywords,omitempty"`
	PromotionalText string `json:"promotionalText,omitempty"`
	SupportURL      string `json:"supportUrl,omitempty"`
	MarketingURL    string `json:"marketingUrl,omitempty"`
	WhatsNew        string `json:"whatsNew,omitempty"`
}

func (a *ascClient) Localizations(versionID string) ([]ASCVersionLocalization, error) {
	out, _, err := a.do("GET", "/appStoreVersions/"+versionID+"/appStoreVersionLocalizations?limit=50", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string                 `json:"id"`
			Attributes ASCVersionLocalization `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	locs := make([]ASCVersionLocalization, 0, len(r.Data))
	for _, d := range r.Data {
		l := d.Attributes
		l.ID = d.ID
		locs = append(locs, l)
	}
	return locs, nil
}

// LocalizationFor finds one locale's localization on a version.
func (a *ascClient) LocalizationFor(versionID, locale string) (*ASCVersionLocalization, error) {
	locs, err := a.Localizations(versionID)
	if err != nil {
		return nil, err
	}
	for i := range locs {
		if strings.EqualFold(locs[i].Locale, locale) {
			return &locs[i], nil
		}
	}
	have := make([]string, 0, len(locs))
	for _, l := range locs {
		have = append(have, l.Locale)
	}
	return nil, fmt.Errorf("locale %q not on this version (have: %s); add the locale in App Store Connect first", locale, strings.Join(have, ", "))
}

// SetLocalization PATCHes only the attributes the caller actually supplied.
func (a *ascClient) SetLocalization(locID string, attrs map[string]interface{}) (*ASCVersionLocalization, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "appStoreVersionLocalizations",
			"id":         locID,
			"attributes": attrs,
		},
	}
	out, _, err := a.do("PATCH", "/appStoreVersionLocalizations/"+locID, body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			ID         string                 `json:"id"`
			Attributes ASCVersionLocalization `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	l := r.Data.Attributes
	l.ID = r.Data.ID
	return &l, nil
}

// ASCScreenshotSet is one display-type bucket inside a localization.
type ASCScreenshotSet struct {
	ID          string `json:"id"`
	DisplayType string `json:"screenshotDisplayType"`
}

func (a *ascClient) ScreenshotSets(locID string) ([]ASCScreenshotSet, error) {
	out, _, err := a.do("GET", "/appStoreVersionLocalizations/"+locID+"/appScreenshotSets?limit=50", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string           `json:"id"`
			Attributes ASCScreenshotSet `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	sets := make([]ASCScreenshotSet, 0, len(r.Data))
	for _, d := range r.Data {
		s := d.Attributes
		s.ID = d.ID
		sets = append(sets, s)
	}
	return sets, nil
}

// EnsureScreenshotSet reuses the existing set for a display type, creating it
// only when absent (Apple 409s on a duplicate display type).
func (a *ascClient) EnsureScreenshotSet(locID, displayType string) (*ASCScreenshotSet, bool, error) {
	sets, err := a.ScreenshotSets(locID)
	if err != nil {
		return nil, false, err
	}
	for i := range sets {
		if sets[i].DisplayType == displayType {
			return &sets[i], false, nil
		}
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "appScreenshotSets",
			"attributes": map[string]interface{}{"screenshotDisplayType": displayType},
			"relationships": map[string]interface{}{
				"appStoreVersionLocalization": map[string]interface{}{
					"data": map[string]string{"type": "appStoreVersionLocalizations", "id": locID},
				},
			},
		},
	}
	out, _, err := a.do("POST", "/appScreenshotSets", body)
	if err != nil {
		return nil, false, err
	}
	var r struct {
		Data struct {
			ID         string           `json:"id"`
			Attributes ASCScreenshotSet `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, false, err
	}
	s := r.Data.Attributes
	s.ID = r.Data.ID
	if s.DisplayType == "" {
		s.DisplayType = displayType
	}
	return &s, true, nil
}

// ASCScreenshot is one uploaded screenshot asset.
type ASCScreenshot struct {
	ID                 string `json:"id"`
	FileName           string `json:"fileName,omitempty"`
	FileSize           int64  `json:"fileSize,omitempty"`
	AssetDeliveryState struct {
		State string `json:"state"`
	} `json:"assetDeliveryState,omitempty"`
}

func (a *ascClient) Screenshots(setID string) ([]ASCScreenshot, error) {
	out, _, err := a.do("GET", "/appScreenshotSets/"+setID+"/appScreenshots?limit=100", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string        `json:"id"`
			Attributes ASCScreenshot `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	shots := make([]ASCScreenshot, 0, len(r.Data))
	for _, d := range r.Data {
		s := d.Attributes
		s.ID = d.ID
		shots = append(shots, s)
	}
	return shots, nil
}

func (a *ascClient) DeleteScreenshot(id string) error {
	_, _, err := a.do("DELETE", "/appScreenshots/"+id, nil)
	return err
}

// ascUploadOperation is one chunk Apple wants us to PUT. Note `offset`/`length`
// slice into the ORIGINAL file bytes — Apple may hand back several ops for one
// asset.
type ascUploadOperation struct {
	Method         string `json:"method"`
	URL            string `json:"url"`
	Length         int    `json:"length"`
	Offset         int    `json:"offset"`
	RequestHeaders []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"requestHeaders"`
}

// ReserveScreenshot performs step (b): declare the asset and get back the
// upload operations Apple wants us to execute.
func (a *ascClient) ReserveScreenshot(setID, fileName string, fileSize int64) (string, []ascUploadOperation, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "appScreenshots",
			"attributes": map[string]interface{}{
				"fileSize": fileSize,
				"fileName": fileName,
			},
			"relationships": map[string]interface{}{
				"appScreenshotSet": map[string]interface{}{
					"data": map[string]string{"type": "appScreenshotSets", "id": setID},
				},
			},
		},
	}
	out, _, err := a.do("POST", "/appScreenshots", body)
	if err != nil {
		return "", nil, err
	}
	var r struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				UploadOperations []ascUploadOperation `json:"uploadOperations"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", nil, err
	}
	if r.Data.ID == "" {
		return "", nil, fmt.Errorf("app store connect returned no screenshot id for %s", fileName)
	}
	return r.Data.ID, r.Data.Attributes.UploadOperations, nil
}

// runUploadOperations performs step (c): raw HTTP to Apple's asset host with
// Apple's own headers and NO JWT (sending the ASC bearer here is rejected).
func runUploadOperations(ops []ascUploadOperation, data []byte) error {
	if len(ops) == 0 {
		return fmt.Errorf("app store connect returned no uploadOperations")
	}
	for i, op := range ops {
		if op.Offset < 0 || op.Length < 0 || op.Offset+op.Length > len(data) {
			return fmt.Errorf("uploadOperation %d out of range (offset=%d length=%d file=%d bytes)", i, op.Offset, op.Length, len(data))
		}
		method := op.Method
		if method == "" {
			method = "PUT"
		}
		chunk := data[op.Offset : op.Offset+op.Length]
		req, err := http.NewRequest(method, op.URL, bytes.NewReader(chunk))
		if err != nil {
			return err
		}
		for _, h := range op.RequestHeaders {
			req.Header.Set(h.Name, h.Value)
		}
		req.ContentLength = int64(len(chunk))
		resp, err := ascUploadClient.Do(req)
		if err != nil {
			return fmt.Errorf("upload operation %d: %w", i, err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("upload operation %d: HTTP %d %s", i, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
	return nil
}

// CommitScreenshot performs step (d): tell Apple the bytes landed and prove it
// with an md5 of the WHOLE file (not per-chunk).
func (a *ascClient) CommitScreenshot(id, md5hex string) (*ASCScreenshot, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "appScreenshots",
			"id":   id,
			"attributes": map[string]interface{}{
				"uploaded":           true,
				"sourceFileChecksum": md5hex,
			},
		},
	}
	out, _, err := a.do("PATCH", "/appScreenshots/"+id, body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			ID         string        `json:"id"`
			Attributes ASCScreenshot `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	s := r.Data.Attributes
	s.ID = r.Data.ID
	if s.ID == "" {
		s.ID = id
	}
	return &s, nil
}

// UploadScreenshot runs the full proven reserve → upload → commit sequence for
// one file.
func (a *ascClient) UploadScreenshot(setID, path string) (*ASCScreenshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	name := filepath.Base(path)
	id, ops, err := a.ReserveScreenshot(setID, name, int64(len(data)))
	if err != nil {
		return nil, err
	}
	if err := runUploadOperations(ops, data); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	sum := md5.Sum(data) // #nosec G401 — Apple mandates md5 for sourceFileChecksum
	return a.CommitScreenshot(id, hex.EncodeToString(sum[:]))
}

// ASCReviewDetail is the App Review Information form (notes + demo account).
type ASCReviewDetail struct {
	ID                  string `json:"id"`
	ContactFirstName    string `json:"contactFirstName,omitempty"`
	ContactLastName     string `json:"contactLastName,omitempty"`
	ContactPhone        string `json:"contactPhone,omitempty"`
	ContactEmail        string `json:"contactEmail,omitempty"`
	DemoAccountName     string `json:"demoAccountName,omitempty"`
	DemoAccountPassword string `json:"demoAccountPassword,omitempty"`
	DemoAccountRequired bool   `json:"demoAccountRequired"`
	Notes               string `json:"notes,omitempty"`
}

// ReviewDetail returns the version's review detail, or nil when none exists yet.
func (a *ascClient) ReviewDetail(versionID string) (*ASCReviewDetail, error) {
	out, status, err := a.do("GET", "/appStoreVersions/"+versionID+"/appStoreReviewDetail", nil)
	if err != nil {
		if status == 404 {
			return nil, nil
		}
		return nil, err
	}
	var r struct {
		Data *struct {
			ID         string          `json:"id"`
			Attributes ASCReviewDetail `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	if r.Data == nil || r.Data.ID == "" {
		return nil, nil
	}
	d := r.Data.Attributes
	d.ID = r.Data.ID
	return &d, nil
}

// SetReviewDetail PATCHes an existing review detail, or POSTs one when the
// version has none yet.
func (a *ascClient) SetReviewDetail(versionID string, attrs map[string]interface{}) (*ASCReviewDetail, error) {
	cur, err := a.ReviewDetail(versionID)
	if err != nil {
		return nil, err
	}
	var out []byte
	if cur == nil {
		body := map[string]interface{}{
			"data": map[string]interface{}{
				"type":       "appStoreReviewDetails",
				"attributes": attrs,
				"relationships": map[string]interface{}{
					"appStoreVersion": map[string]interface{}{
						"data": map[string]string{"type": "appStoreVersions", "id": versionID},
					},
				},
			},
		}
		out, _, err = a.do("POST", "/appStoreReviewDetails", body)
	} else {
		body := map[string]interface{}{
			"data": map[string]interface{}{
				"type":       "appStoreReviewDetails",
				"id":         cur.ID,
				"attributes": attrs,
			},
		}
		out, _, err = a.do("PATCH", "/appStoreReviewDetails/"+cur.ID, body)
	}
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			ID         string          `json:"id"`
			Attributes ASCReviewDetail `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	d := r.Data.Attributes
	d.ID = r.Data.ID
	return &d, nil
}

// AttachedBuild returns the build bound to a version, or nil when none is.
func (a *ascClient) AttachedBuild(versionID string) (*ASCBuild, error) {
	out, status, err := a.do("GET", "/appStoreVersions/"+versionID+"/build", nil)
	if err != nil {
		if status == 404 {
			return nil, nil
		}
		return nil, err
	}
	var r struct {
		Data *struct {
			ID         string   `json:"id"`
			Attributes ASCBuild `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	if r.Data == nil || r.Data.ID == "" {
		return nil, nil
	}
	b := r.Data.Attributes
	b.ID = r.Data.ID
	return &b, nil
}

// AttachBuild binds a build to a version (the "Build" row on the version page).
func (a *ascClient) AttachBuild(versionID, buildID string) error {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "appStoreVersions",
			"id":   versionID,
			"relationships": map[string]interface{}{
				"build": map[string]interface{}{
					"data": map[string]string{"type": "builds", "id": buildID},
				},
			},
		},
	}
	_, _, err := a.do("PATCH", "/appStoreVersions/"+versionID, body)
	return err
}

// BuildsForPlatform lists builds filtered by platform, and optionally by the
// marketing version (preReleaseVersion.version) and/or build number (version).
func (a *ascClient) BuildsForPlatform(appID, platform, marketingVersion, buildNumber string) ([]ASCBuild, error) {
	path := "/builds?filter[app]=" + appID +
		"&filter[preReleaseVersion.platform]=" + platform +
		"&limit=50&sort=-uploadedDate"
	if marketingVersion != "" {
		path += "&filter[preReleaseVersion.version]=" + urlQueryEscape(marketingVersion)
	}
	if buildNumber != "" {
		path += "&filter[version]=" + urlQueryEscape(buildNumber)
	}
	out, _, err := a.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string   `json:"id"`
			Attributes ASCBuild `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	builds := make([]ASCBuild, 0, len(r.Data))
	for _, d := range r.Data {
		b := d.Attributes
		b.ID = d.ID
		builds = append(builds, b)
	}
	return builds, nil
}

// ASCReviewSubmission is the submission envelope that carries versions to review.
type ASCReviewSubmission struct {
	ID        string `json:"id"`
	Platform  string `json:"platform,omitempty"`
	State     string `json:"state,omitempty"`
	Submitted bool   `json:"submitted"`
}

// reviewSubmissionOpen is the state of a created-but-not-yet-submitted envelope.
const reviewSubmissionOpen = "READY_FOR_REVIEW"

func (a *ascClient) ReviewSubmissions(appID, platform string) ([]ASCReviewSubmission, error) {
	path := "/reviewSubmissions?filter[app]=" + appID + "&filter[platform]=" + platform + "&limit=50"
	out, _, err := a.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string              `json:"id"`
			Attributes ASCReviewSubmission `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	subs := make([]ASCReviewSubmission, 0, len(r.Data))
	for _, d := range r.Data {
		s := d.Attributes
		s.ID = d.ID
		subs = append(subs, s)
	}
	return subs, nil
}

// EnsureReviewSubmission performs step (a): reuse the open (never-submitted)
// envelope if one is lying around, otherwise create one.
//
// Reuse is not an optimisation — it is mandatory. A dangling reviewSubmission
// CANNOT be deleted (Apple answers 403 on DELETE /reviewSubmissions/{id}), and
// creating a second open one for the same app+platform 409s. So the only way
// forward past an aborted attempt is to pick the existing one back up.
func (a *ascClient) EnsureReviewSubmission(appID, platform string) (*ASCReviewSubmission, bool, error) {
	subs, err := a.ReviewSubmissions(appID, platform)
	if err != nil {
		return nil, false, err
	}
	for i := range subs {
		if !subs[i].Submitted && subs[i].State == reviewSubmissionOpen {
			return &subs[i], false, nil
		}
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "reviewSubmissions",
			"attributes": map[string]interface{}{"platform": platform},
			"relationships": map[string]interface{}{
				"app": map[string]interface{}{
					"data": map[string]string{"type": "apps", "id": appID},
				},
			},
		},
	}
	out, _, err := a.do("POST", "/reviewSubmissions", body)
	if err != nil {
		return nil, false, err
	}
	var r struct {
		Data struct {
			ID         string              `json:"id"`
			Attributes ASCReviewSubmission `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, false, err
	}
	s := r.Data.Attributes
	s.ID = r.Data.ID
	return &s, true, nil
}

// ReviewSubmissionVersionIDs lists the appStoreVersion ids already itemised on
// a submission, so we don't 409 by adding the same version twice.
func (a *ascClient) ReviewSubmissionVersionIDs(subID string) (map[string]bool, error) {
	out, _, err := a.do("GET", "/reviewSubmissions/"+subID+"/items?limit=50", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			Relationships struct {
				AppStoreVersion struct {
					Data *struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"appStoreVersion"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, d := range r.Data {
		if d.Relationships.AppStoreVersion.Data != nil {
			ids[d.Relationships.AppStoreVersion.Data.ID] = true
		}
	}
	return ids, nil
}

// AddReviewSubmissionItem performs step (b). This is where Apple validates the
// whole version and answers 409 with meta.associatedErrors naming every missing
// field — including `hasHighMotionLabel`. The raw body is returned alongside the
// error precisely so the caller can surface those verbatim.
func (a *ascClient) AddReviewSubmissionItem(subID, versionID string) ([]byte, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "reviewSubmissionItems",
			"relationships": map[string]interface{}{
				"reviewSubmission": map[string]interface{}{
					"data": map[string]string{"type": "reviewSubmissions", "id": subID},
				},
				"appStoreVersion": map[string]interface{}{
					"data": map[string]string{"type": "appStoreVersions", "id": versionID},
				},
			},
		},
	}
	out, _, err := a.do("POST", "/reviewSubmissionItems", body)
	return out, err
}

// setReviewSubmissionFlag performs step (c) / the cancel: PATCH submitted:true
// or canceled:true.
func (a *ascClient) setReviewSubmissionFlag(subID, flag string) ([]byte, *ASCReviewSubmission, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "reviewSubmissions",
			"id":         subID,
			"attributes": map[string]interface{}{flag: true},
		},
	}
	out, _, err := a.do("PATCH", "/reviewSubmissions/"+subID, body)
	if err != nil {
		return out, nil, err
	}
	var r struct {
		Data struct {
			ID         string              `json:"id"`
			Attributes ASCReviewSubmission `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return out, nil, err
	}
	s := r.Data.Attributes
	s.ID = r.Data.ID
	if s.ID == "" {
		s.ID = subID
	}
	return out, &s, nil
}

func (a *ascClient) SubmitReviewSubmission(subID string) ([]byte, *ASCReviewSubmission, error) {
	return a.setReviewSubmissionFlag(subID, "submitted")
}

func (a *ascClient) CancelReviewSubmission(subID string) ([]byte, *ASCReviewSubmission, error) {
	return a.setReviewSubmissionFlag(subID, "canceled")
}

// ─────────────────────────────── ops verbs ──────────────────────────────────

// storeSubmitArgs is the union payload of the submission verbs. Optional
// strings are pointers so "" (deliberately clear the field) is distinguishable
// from "not supplied" — a plain string would silently wipe a description.
type storeSubmitArgs struct {
	Project  string `json:"project"`
	BundleID string `json:"bundleId"`
	AppID    string `json:"appId"`
	Platform string `json:"platform"`
	Locale   string `json:"locale"`

	// store_metadata_set
	Description     *string `json:"description"`
	PromotionalText *string `json:"promotionalText"`
	Keywords        *string `json:"keywords"`
	SupportURL      *string `json:"supportUrl"`
	MarketingURL    *string `json:"marketingUrl"`
	WhatsNew        *string `json:"whatsNew"`

	// store_screenshots_set
	DisplayType string   `json:"displayType"`
	Files       []string `json:"files"`
	Replace     bool     `json:"replace"`

	// store_review_details_set
	Notes               *string `json:"notes"`
	DemoAccountName     *string `json:"demoAccountName"`
	DemoAccountPassword *string `json:"demoAccountPassword"`
	DemoAccountRequired *bool   `json:"demoAccountRequired"`
	ContactFirstName    *string `json:"contactFirstName"`
	ContactLastName     *string `json:"contactLastName"`
	ContactPhone        *string `json:"contactPhone"`
	ContactEmail        *string `json:"contactEmail"`

	// store_build_attach
	Build   string `json:"build"`   // CFBundleVersion (build number)
	Version string `json:"version"` // marketing version, e.g. "1.0"
	BuildID string `json:"buildId"` // explicit ASC build id (skips resolution)
}

func parseSubmitArgs(payload json.RawMessage) (storeSubmitArgs, *OpsResult) {
	var a storeSubmitArgs
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &a); err != nil {
			return a, &OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if strings.TrimSpace(a.Locale) == "" {
		a.Locale = "en-US"
	}
	return a, nil
}

// submitTarget is everything a submission verb needs resolved up front.
type submitTarget struct {
	cl       *ascClient
	app      *ASCApp
	platform string
	version  *ASCVersion // nil when the platform isn't on the app (Console gate)
}

// resolveSubmitTarget resolves creds → app → platform → editable version.
// It never invents a version: when the platform isn't enabled on the app it
// returns version==nil and the caller raises the add_platform Console gate.
func resolveSubmitTarget(a storeSubmitArgs) (*submitTarget, *OpsResult) {
	platform, err := normalizePlatform(a.Platform)
	if err != nil {
		return nil, &OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(a.BundleID) == "" && strings.TrimSpace(a.AppID) == "" {
		return nil, &OpsResult{OK: false, Code: "bad_payload", Error: "bundleId or appId required"}
	}
	cl, err := newASCClientFn(a.Project)
	if err != nil {
		return nil, &OpsResult{OK: false, Code: "no_credentials", Error: err.Error()}
	}
	var app *ASCApp
	if strings.TrimSpace(a.AppID) != "" {
		app = &ASCApp{ID: strings.TrimSpace(a.AppID), Name: "app " + strings.TrimSpace(a.AppID)}
	} else {
		app, err = cl.AppByBundleID(a.BundleID)
		if err != nil {
			return nil, &OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
	}
	v, err := cl.EditableVersion(app.ID, platform)
	if err != nil {
		return nil, &OpsResult{OK: false, Error: err.Error()}
	}
	return &submitTarget{cl: cl, app: app, platform: platform, version: v}, nil
}

// requireVersion turns a missing platform version into the add_platform gate.
// Every mutating verb goes through this — none of them pretend.
func (t *submitTarget) requireVersion() *OpsResult {
	if t.version != nil {
		return nil
	}
	g := gateAddPlatform(t.app.Name, t.platform)
	return &OpsResult{
		OK:    false,
		Code:  "console_only",
		Error: g.Reason + "\n\nDo this in the Console: " + g.ConsolePath,
		Initial: map[string]interface{}{
			"consoleOnly": []consoleGate{g},
			"app":         t.app,
			"platform":    t.platform,
		},
	}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "store_submit_status",
		Description: "PREFLIGHT an App Store submission: report the version + its state, whether a build is attached, screenshot counts per display type, which metadata (description/keywords/support URL/review notes/demo account) is set, any open review submission — and exactly WHAT IS MISSING. Also names the steps Apple exposes NO API for (adding a platform to an app; App Motion Information for visionOS) with the exact Console path. Run this before store_submit_for_review.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":  map[string]interface{}{"type": "string", "description": "Project slug whose vault holds the App Store Connect key. Omit for the default project."},
			"bundleId": map[string]interface{}{"type": "string", "description": "The app's bundle id (e.g. com.acme.app). Either this or appId."},
			"appId":    map[string]interface{}{"type": "string", "description": "App Store Connect app id, if you already know it."},
			"platform": map[string]interface{}{"type": "string", "description": "IOS | MAC_OS | TV_OS | VISION_OS (default IOS)."},
			"locale":   map[string]interface{}{"type": "string", "description": "Primary locale to check in detail (default en-US)."},
		}),
		Handler:    storeSubmitStatusHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_metadata_set",
		Description: "Set App Store listing metadata for one locale on the editable version (description, promotional text, keywords, support/marketing URL, what's new). Only the fields you pass are changed.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":         map[string]interface{}{"type": "string"},
			"bundleId":        map[string]interface{}{"type": "string"},
			"appId":           map[string]interface{}{"type": "string"},
			"platform":        map[string]interface{}{"type": "string", "description": "IOS | MAC_OS | TV_OS | VISION_OS (default IOS)."},
			"locale":          map[string]interface{}{"type": "string", "description": "Locale to write (default en-US)."},
			"description":     map[string]interface{}{"type": "string"},
			"promotionalText": map[string]interface{}{"type": "string"},
			"keywords":        map[string]interface{}{"type": "string", "description": "Comma-separated, 100 chars max (Apple's limit)."},
			"supportUrl":      map[string]interface{}{"type": "string"},
			"marketingUrl":    map[string]interface{}{"type": "string"},
			"whatsNew":        map[string]interface{}{"type": "string", "description": "Release notes. Only accepted once the app has a released version."},
		}),
		Handler:    storeMetadataSetHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_screenshots_set",
		Description: "Upload App Store screenshots for one display type: reserves each asset, executes Apple's uploadOperations against its asset host, then commits with an md5 checksum. Reuses the existing screenshot set for the display type; pass replace:true to delete the screenshots already in it first. Vision Pro is APP_APPLE_VISION_PRO (3840x2160).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":     map[string]interface{}{"type": "string"},
			"bundleId":    map[string]interface{}{"type": "string"},
			"appId":       map[string]interface{}{"type": "string"},
			"platform":    map[string]interface{}{"type": "string", "description": "IOS | MAC_OS | TV_OS | VISION_OS (default IOS)."},
			"locale":      map[string]interface{}{"type": "string", "description": "Default en-US."},
			"displayType": map[string]interface{}{"type": "string", "description": "Apple screenshot display type, e.g. APP_APPLE_VISION_PRO, APP_IPHONE_67, APP_IPAD_PRO_3GEN_129, APP_APPLE_TV."},
			"files":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Absolute paths to the PNG/JPEG screenshots on the target machine, in display order."},
			"replace":     map[string]interface{}{"type": "boolean", "description": "Delete the screenshots already in this set before uploading (default false = append)."},
		}, "displayType", "files"),
		Handler:    storeScreenshotsSetHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_review_details_set",
		Description: "Set the App Review Information form on the editable version: review notes, demo account (name/password/required) and the review contact. Creates the form if the version has none yet.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":             map[string]interface{}{"type": "string"},
			"bundleId":            map[string]interface{}{"type": "string"},
			"appId":               map[string]interface{}{"type": "string"},
			"platform":            map[string]interface{}{"type": "string", "description": "IOS | MAC_OS | TV_OS | VISION_OS (default IOS)."},
			"notes":               map[string]interface{}{"type": "string", "description": "Notes for the App Review team."},
			"demoAccountName":     map[string]interface{}{"type": "string"},
			"demoAccountPassword": map[string]interface{}{"type": "string"},
			"demoAccountRequired": map[string]interface{}{"type": "boolean", "description": "Whether review needs a demo account to get past a login."},
			"contactFirstName":    map[string]interface{}{"type": "string"},
			"contactLastName":     map[string]interface{}{"type": "string"},
			"contactPhone":        map[string]interface{}{"type": "string"},
			"contactEmail":        map[string]interface{}{"type": "string"},
		}),
		Handler:    storeReviewDetailsSetHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_build_attach",
		Description: "Attach an uploaded build to the editable App Store version. Resolves the build by marketing version + build number for the platform (newest processed build when unspecified), or takes an explicit buildId.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":  map[string]interface{}{"type": "string"},
			"bundleId": map[string]interface{}{"type": "string"},
			"appId":    map[string]interface{}{"type": "string"},
			"platform": map[string]interface{}{"type": "string", "description": "IOS | MAC_OS | TV_OS | VISION_OS (default IOS)."},
			"version":  map[string]interface{}{"type": "string", "description": "Marketing version of the build, e.g. 1.0 (defaults to the App Store version's own versionString)."},
			"build":    map[string]interface{}{"type": "string", "description": "Build number (CFBundleVersion). Omit for the newest processed build."},
			"buildId":  map[string]interface{}{"type": "string", "description": "Explicit App Store Connect build id — skips resolution."},
		}),
		Handler:    storeBuildAttachHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_submit_for_review",
		Description: "Submit the version to App Review: create (or reuse) a review submission, add the version as an item, then mark it submitted → state becomes WAITING_FOR_REVIEW. If Apple rejects the item with missing-metadata errors they are surfaced VERBATIM, including the Console-only App Motion Information gate for visionOS. Run store_submit_status first.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":  map[string]interface{}{"type": "string"},
			"bundleId": map[string]interface{}{"type": "string"},
			"appId":    map[string]interface{}{"type": "string"},
			"platform": map[string]interface{}{"type": "string", "description": "IOS | MAC_OS | TV_OS | VISION_OS (default IOS)."},
		}),
		Handler:    storeSubmitForReviewHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_submit_cancel",
		Description: "Cancel a submission that is WAITING_FOR_REVIEW (marks the review submission canceled). Once the app is IN_REVIEW, Apple no longer accepts a cancel over the API.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":  map[string]interface{}{"type": "string"},
			"bundleId": map[string]interface{}{"type": "string"},
			"appId":    map[string]interface{}{"type": "string"},
			"platform": map[string]interface{}{"type": "string", "description": "IOS | MAC_OS | TV_OS | VISION_OS (default IOS)."},
		}),
		Handler:    storeSubmitCancelHandler,
		AllowGuest: false,
	})
}

// ── store_submit_status ──

// localizationStatus is the per-locale readiness view.
type localizationStatus struct {
	Locale             string           `json:"locale"`
	HasDescription     bool             `json:"hasDescription"`
	HasKeywords        bool             `json:"hasKeywords"`
	HasPromotionalText bool             `json:"hasPromotionalText"`
	HasSupportURL      bool             `json:"hasSupportUrl"`
	HasMarketingURL    bool             `json:"hasMarketingUrl"`
	HasWhatsNew        bool             `json:"hasWhatsNew"`
	ScreenshotSets     []screenshotStat `json:"screenshotSets"`
	ScreenshotCount    int              `json:"screenshotCount"`
}

type screenshotStat struct {
	DisplayType string `json:"displayType"`
	Count       int    `json:"count"`
	Hint        string `json:"hint,omitempty"`
}

func storeSubmitStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseSubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	t, deny := resolveSubmitTarget(a)
	if deny != nil {
		return *deny
	}

	gates := []consoleGate{}
	var missing []string

	// Gate 1 — platform not on the app. Nothing else is knowable.
	if t.version == nil {
		gates = append(gates, gateAddPlatform(t.app.Name, t.platform))
		missing = append(missing, "platform "+t.platform+" is not enabled on this app (Console-only — see consoleOnly)")
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"app":         t.app,
			"platform":    t.platform,
			"version":     nil,
			"ready":       false,
			"missing":     missing,
			"consoleOnly": gates,
		}}
	}

	res := map[string]interface{}{
		"app":      t.app,
		"platform": t.platform,
		"version":  t.version,
	}
	if !t.version.Editable {
		missing = append(missing, "version "+t.version.VersionString+" is in state "+t.version.state()+
			" — it is not editable; metadata/screenshot/build verbs will be rejected by Apple until it returns to PREPARE_FOR_SUBMISSION")
	}

	// Build attachment.
	build, err := t.cl.AttachedBuild(t.version.ID)
	if err != nil {
		return ascFailure("read attached build", nil, err)
	}
	if build == nil {
		res["build"] = map[string]interface{}{"attached": false}
		missing = append(missing, "no build attached — upload one, then run store_build_attach")
	} else {
		res["build"] = map[string]interface{}{"attached": true, "id": build.ID, "version": build.Version, "processingState": build.ProcessingState}
		if build.ProcessingState != "" && build.ProcessingState != "VALID" {
			missing = append(missing, "attached build "+build.Version+" is "+build.ProcessingState+" (Apple must finish processing it before review)")
		}
	}

	// Localizations + screenshots.
	locs, err := t.cl.Localizations(t.version.ID)
	if err != nil {
		return ascFailure("list localizations", nil, err)
	}
	if len(locs) == 0 {
		missing = append(missing, "the version has no locales — add one in App Store Connect")
	}
	stats := make([]localizationStatus, 0, len(locs))
	for _, l := range locs {
		ls := localizationStatus{
			Locale:             l.Locale,
			HasDescription:     strings.TrimSpace(l.Description) != "",
			HasKeywords:        strings.TrimSpace(l.Keywords) != "",
			HasPromotionalText: strings.TrimSpace(l.PromotionalText) != "",
			HasSupportURL:      strings.TrimSpace(l.SupportURL) != "",
			HasMarketingURL:    strings.TrimSpace(l.MarketingURL) != "",
			HasWhatsNew:        strings.TrimSpace(l.WhatsNew) != "",
		}
		sets, err := t.cl.ScreenshotSets(l.ID)
		if err != nil {
			return ascFailure("list screenshot sets for "+l.Locale, nil, err)
		}
		for _, s := range sets {
			shots, err := t.cl.Screenshots(s.ID)
			if err != nil {
				return ascFailure("list screenshots for "+s.DisplayType, nil, err)
			}
			ls.ScreenshotSets = append(ls.ScreenshotSets, screenshotStat{
				DisplayType: s.DisplayType,
				Count:       len(shots),
				Hint:        ascDisplayTypes[s.DisplayType],
			})
			ls.ScreenshotCount += len(shots)
		}
		stats = append(stats, ls)

		// Apple blocks on the primary locale; report the others as info.
		if strings.EqualFold(l.Locale, a.Locale) {
			if !ls.HasDescription {
				missing = append(missing, l.Locale+": description is empty — set it with store_metadata_set")
			}
			if !ls.HasKeywords {
				missing = append(missing, l.Locale+": keywords are empty — set them with store_metadata_set")
			}
			if !ls.HasSupportURL {
				missing = append(missing, l.Locale+": support URL is empty — set it with store_metadata_set")
			}
			if ls.ScreenshotCount == 0 {
				missing = append(missing, l.Locale+": no screenshots — upload with store_screenshots_set")
			}
		}
	}
	res["localizations"] = stats

	// Review details.
	rd, err := t.cl.ReviewDetail(t.version.ID)
	if err != nil {
		return ascFailure("read review details", nil, err)
	}
	if rd == nil {
		res["reviewDetails"] = nil
		missing = append(missing, "App Review Information is not filled in — set it with store_review_details_set")
	} else {
		hasDemo := strings.TrimSpace(rd.DemoAccountName) != "" && strings.TrimSpace(rd.DemoAccountPassword) != ""
		hasContact := strings.TrimSpace(rd.ContactEmail) != "" && strings.TrimSpace(rd.ContactPhone) != ""
		// NEVER echo the demo password — presence only.
		res["reviewDetails"] = map[string]interface{}{
			"id":                  rd.ID,
			"hasNotes":            strings.TrimSpace(rd.Notes) != "",
			"demoAccountRequired": rd.DemoAccountRequired,
			"hasDemoAccount":      hasDemo,
			"hasContact":          hasContact,
		}
		if rd.DemoAccountRequired && !hasDemo {
			missing = append(missing, "demo account is marked required but name/password are empty — set them with store_review_details_set")
		}
		if !hasContact {
			missing = append(missing, "App Review contact email/phone missing — set them with store_review_details_set")
		}
	}

	// Any open / in-flight review submission.
	subs, err := t.cl.ReviewSubmissions(t.app.ID, t.platform)
	if err != nil {
		return ascFailure("list review submissions", nil, err)
	}
	res["reviewSubmissions"] = subs
	for _, s := range subs {
		if s.State == "WAITING_FOR_REVIEW" || s.State == "IN_REVIEW" {
			missing = append(missing, "a submission is already "+s.State+" (id "+s.ID+") — cancel it with store_submit_cancel before resubmitting")
		}
	}

	// Gate 2 — App Motion Information. Unverifiable by construction: it is not
	// an attribute on ANY public resource, so we cannot read it back. Always
	// surfaced for visionOS as a human step rather than guessed at.
	if t.platform == "VISION_OS" {
		gates = append(gates, gateMotionInfo(t.app.Name, t.platform))
	}
	res["consoleOnly"] = gates
	res["missing"] = missing
	// `ready` covers only what the API can see. The unverifiable gates are
	// listed separately and deliberately do NOT flip it — claiming readiness we
	// cannot check would be the lie this whole file exists to avoid.
	res["ready"] = len(missing) == 0
	if len(gates) > 0 {
		res["note"] = "consoleOnly lists steps App Store Connect exposes NO API for. `ready` reflects only API-visible state; a Console-only gate with verifiable=false (App Motion Information) cannot be checked from here at all — Apple will report it as an associatedError at submit time if it is missing."
	}
	return OpsResult{OK: true, Initial: res}
}

// ── store_metadata_set ──

func storeMetadataSetHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseSubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	t, deny := resolveSubmitTarget(a)
	if deny != nil {
		return *deny
	}
	if g := t.requireVersion(); g != nil {
		return *g
	}
	attrs := map[string]interface{}{}
	put := func(key string, v *string) {
		if v != nil {
			attrs[key] = *v
		}
	}
	put("description", a.Description)
	put("promotionalText", a.PromotionalText)
	put("keywords", a.Keywords)
	put("supportUrl", a.SupportURL)
	put("marketingUrl", a.MarketingURL)
	put("whatsNew", a.WhatsNew)
	if len(attrs) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "nothing to set — pass at least one of description, promotionalText, keywords, supportUrl, marketingUrl, whatsNew"}
	}
	loc, err := t.cl.LocalizationFor(t.version.ID, a.Locale)
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	updated, err := t.cl.SetLocalization(loc.ID, attrs)
	if err != nil {
		return ascFailure("set metadata for "+a.Locale, nil, err)
	}
	fields := make([]string, 0, len(attrs))
	for k := range attrs {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"app":          t.app,
		"platform":     t.platform,
		"version":      t.version,
		"locale":       updated.Locale,
		"updated":      fields,
		"localization": updated,
	}}
}

// ── store_screenshots_set ──

func storeScreenshotsSetHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseSubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	if strings.TrimSpace(a.DisplayType) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "displayType required (e.g. APP_APPLE_VISION_PRO, APP_IPHONE_67)"}
	}
	if len(a.Files) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "files required — absolute paths to the screenshots, in display order"}
	}
	for _, f := range a.Files {
		st, err := os.Stat(f)
		if err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: "screenshot " + f + ": " + err.Error()}
		}
		if st.IsDir() {
			return OpsResult{OK: false, Code: "bad_payload", Error: f + " is a directory"}
		}
		switch strings.ToLower(filepath.Ext(f)) {
		case ".png", ".jpg", ".jpeg":
		default:
			return OpsResult{OK: false, Code: "bad_payload", Error: f + ": App Store screenshots must be PNG or JPEG"}
		}
	}
	t, deny := resolveSubmitTarget(a)
	if deny != nil {
		return *deny
	}
	if g := t.requireVersion(); g != nil {
		return *g
	}
	loc, err := t.cl.LocalizationFor(t.version.ID, a.Locale)
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	set, created, err := t.cl.EnsureScreenshotSet(loc.ID, a.DisplayType)
	if err != nil {
		return ascFailure("ensure screenshot set "+a.DisplayType, nil, err)
	}

	deleted := 0
	if a.Replace {
		existing, err := t.cl.Screenshots(set.ID)
		if err != nil {
			return ascFailure("list existing screenshots", nil, err)
		}
		for _, s := range existing {
			if err := t.cl.DeleteScreenshot(s.ID); err != nil {
				return ascFailure("delete screenshot "+s.ID, nil, err)
			}
			deleted++
		}
	}

	uploaded := make([]map[string]interface{}, 0, len(a.Files))
	for _, f := range a.Files {
		shot, err := t.cl.UploadScreenshot(set.ID, f)
		if err != nil {
			return OpsResult{OK: false, Code: "upload_failed", Error: fmt.Sprintf(
				"uploaded %d/%d screenshots, then %s failed: %v", len(uploaded), len(a.Files), filepath.Base(f), err)}
		}
		uploaded = append(uploaded, map[string]interface{}{
			"id":       shot.ID,
			"fileName": shot.FileName,
			"state":    shot.AssetDeliveryState.State,
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"app":            t.app,
		"platform":       t.platform,
		"version":        t.version,
		"locale":         loc.Locale,
		"displayType":    set.DisplayType,
		"setId":          set.ID,
		"setCreated":     created,
		"deleted":        deleted,
		"uploaded":       uploaded,
		"uploadedCount":  len(uploaded),
		"expectedPixels": ascDisplayTypes[a.DisplayType],
	}}
}

// ── store_review_details_set ──

func storeReviewDetailsSetHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseSubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	t, deny := resolveSubmitTarget(a)
	if deny != nil {
		return *deny
	}
	if g := t.requireVersion(); g != nil {
		return *g
	}
	attrs := map[string]interface{}{}
	put := func(key string, v *string) {
		if v != nil {
			attrs[key] = *v
		}
	}
	put("notes", a.Notes)
	put("demoAccountName", a.DemoAccountName)
	put("demoAccountPassword", a.DemoAccountPassword)
	put("contactFirstName", a.ContactFirstName)
	put("contactLastName", a.ContactLastName)
	put("contactPhone", a.ContactPhone)
	put("contactEmail", a.ContactEmail)
	if a.DemoAccountRequired != nil {
		attrs["demoAccountRequired"] = *a.DemoAccountRequired
	}
	if len(attrs) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "nothing to set — pass at least one of notes, demoAccount*, contact*"}
	}
	rd, err := t.cl.SetReviewDetail(t.version.ID, attrs)
	if err != nil {
		return ascFailure("set review details", nil, err)
	}
	fields := make([]string, 0, len(attrs))
	for k := range attrs {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	// Presence only — the demo-account password never leaves this process.
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"app":      t.app,
		"platform": t.platform,
		"version":  t.version,
		"updated":  fields,
		"reviewDetails": map[string]interface{}{
			"id":                  rd.ID,
			"hasNotes":            strings.TrimSpace(rd.Notes) != "",
			"demoAccountRequired": rd.DemoAccountRequired,
			"hasDemoAccount":      strings.TrimSpace(rd.DemoAccountName) != "" && strings.TrimSpace(rd.DemoAccountPassword) != "",
			"hasContact":          strings.TrimSpace(rd.ContactEmail) != "",
		},
	}}
}

// ── store_build_attach ──

func storeBuildAttachHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseSubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	t, deny := resolveSubmitTarget(a)
	if deny != nil {
		return *deny
	}
	if g := t.requireVersion(); g != nil {
		return *g
	}

	buildID := strings.TrimSpace(a.BuildID)
	var chosen *ASCBuild
	if buildID == "" {
		marketing := strings.TrimSpace(a.Version)
		if marketing == "" {
			marketing = t.version.VersionString
		}
		builds, err := t.cl.BuildsForPlatform(t.app.ID, t.platform, marketing, strings.TrimSpace(a.Build))
		if err != nil {
			return ascFailure("list builds", nil, err)
		}
		for i := range builds {
			if builds[i].Expired {
				continue
			}
			if builds[i].ProcessingState != "" && builds[i].ProcessingState != "VALID" {
				continue
			}
			chosen = &builds[i]
			break
		}
		if chosen == nil {
			hint := "no processed " + t.platform + " build for version " + marketing
			if a.Build != "" {
				hint += " build " + a.Build
			}
			return OpsResult{OK: false, Code: "not_found", Error: hint +
				" — upload one first (yaver publish upload / deploy-testflight.sh), and wait for Apple to finish processing it. store_build_list shows what Apple has."}
		}
		buildID = chosen.ID
	}
	if err := t.cl.AttachBuild(t.version.ID, buildID); err != nil {
		return ascFailure("attach build "+buildID, nil, err)
	}
	res := map[string]interface{}{
		"app":      t.app,
		"platform": t.platform,
		"version":  t.version,
		"buildId":  buildID,
	}
	if chosen != nil {
		res["build"] = chosen
	}
	return OpsResult{OK: true, Initial: res}
}

// ── store_submit_for_review ──

func storeSubmitForReviewHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseSubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	t, deny := resolveSubmitTarget(a)
	if deny != nil {
		return *deny
	}
	// Gate 1 — no version for the platform. Refuse loudly with the Console path.
	if g := t.requireVersion(); g != nil {
		return *g
	}

	// (a) create or reuse the review submission envelope.
	sub, created, err := t.cl.EnsureReviewSubmission(t.app.ID, t.platform)
	if err != nil {
		return ascFailure("create review submission", nil, err)
	}

	// (b) add the version as an item. THIS is where Apple validates everything
	// and answers 409 with meta.associatedErrors naming each missing field.
	existing, err := t.cl.ReviewSubmissionVersionIDs(sub.ID)
	if err != nil {
		return ascFailure("list review submission items", nil, err)
	}
	itemAdded := false
	if !existing[t.version.ID] {
		body, err := t.cl.AddReviewSubmissionItem(sub.ID, t.version.ID)
		if err != nil {
			res := ascFailure("add version "+t.version.VersionString+" to review submission "+sub.ID, body, err)
			assoc := ascAssociatedErrors(body)
			details := map[string]interface{}{
				"associatedErrors":   assoc,
				"reviewSubmissionId": sub.ID,
				"versionId":          t.version.ID,
			}
			// The motion-label gate only ever shows up HERE. Attach the exact
			// Console path instead of leaving the operator staring at
			// ENTITY_ERROR.ATTRIBUTE.REQUIRED.
			if motionLabelBlocked(assoc) {
				g := gateMotionInfo(t.app.Name, t.platform)
				details["consoleOnly"] = []consoleGate{g}
				res.Code = "console_only"
				res.Error += "\n\nThis is a Console-only step — Apple exposes NO API for it.\n" + g.Reason + "\n\nDo this in the Console: " + g.ConsolePath
			}
			res.Initial = details
			return res
		}
		itemAdded = true
	}

	// (c) flip submitted:true → WAITING_FOR_REVIEW.
	body, done, err := t.cl.SubmitReviewSubmission(sub.ID)
	if err != nil {
		res := ascFailure("submit review submission "+sub.ID, body, err)
		if res.Initial == nil {
			res.Initial = map[string]interface{}{"reviewSubmissionId": sub.ID}
		}
		return res
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"app":                t.app,
		"platform":           t.platform,
		"version":            t.version,
		"reviewSubmissionId": done.ID,
		"submissionCreated":  created,
		"itemAdded":          itemAdded,
		"state":              done.State,
		"submitted":          done.Submitted,
	}}
}

// ── store_submit_cancel ──

func storeSubmitCancelHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseSubmitArgs(payload)
	if bad != nil {
		return *bad
	}
	t, deny := resolveSubmitTarget(a)
	if deny != nil {
		return *deny
	}
	subs, err := t.cl.ReviewSubmissions(t.app.ID, t.platform)
	if err != nil {
		return ascFailure("list review submissions", nil, err)
	}
	var target *ASCReviewSubmission
	for i := range subs {
		if subs[i].State == "WAITING_FOR_REVIEW" {
			target = &subs[i]
			break
		}
	}
	if target == nil {
		states := make([]string, 0, len(subs))
		for _, s := range subs {
			states = append(states, s.ID+"="+s.State)
		}
		msg := "no submission is WAITING_FOR_REVIEW for " + t.platform
		if len(states) > 0 {
			msg += " (current: " + strings.Join(states, ", ") + ")"
		}
		msg += ". Once Apple moves it to IN_REVIEW the API no longer accepts a cancel — use App Store Connect."
		return OpsResult{OK: false, Code: "not_found", Error: msg}
	}
	body, done, err := t.cl.CancelReviewSubmission(target.ID)
	if err != nil {
		return ascFailure("cancel review submission "+target.ID, body, err)
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"app":                t.app,
		"platform":           t.platform,
		"reviewSubmissionId": done.ID,
		"state":              done.State,
		"canceled":           true,
	}}
}
