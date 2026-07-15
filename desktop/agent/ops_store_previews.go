package main

// ops_store_previews.go — APP PREVIEW VIDEOS for App Store Connect.
//
// Screenshots were the only store asset Yaver could push. This adds the other
// one: the 15-30s app preview video that sits in front of the screenshots on the
// product page.
//
// The upload contract is IDENTICAL to screenshots (ops_store_submit.go):
//
//	POST   /v1/appPreviewSets  {previewType, relationships.appStoreVersionLocalization}
//	POST   /v1/appPreviews     {fileSize, fileName, relationships.appPreviewSet}
//	                           → attributes.uploadOperations[{method,url,requestHeaders,offset,length}]
//	<each op>                  → raw byte-range PUT to Apple's asset host, Apple's
//	                             own headers, NO ASC JWT
//	PATCH  /v1/appPreviews/{id} {uploaded:true, sourceFileChecksum:<md5 of whole file>}
//
// …and then ONE thing screenshots do not do: Apple TRANSCODES the video
// asynchronously. The PATCH returning 200 means "we have your bytes", NOT "your
// video is accepted". Apple can still reject it minutes later for a bad
// resolution / codec / frame rate, and it says so in
// `assetDeliveryState.errors`.
//
// ─────────────────────────────────────────────────────────────────────────────
// SO: WE POLL, AND WE NEVER CLAIM AN UPLOAD APPLE FAILED.
//
//   - assetDeliveryState.state == FAILED  → OpsResult.OK = false, and Apple's
//     errors are reproduced VERBATIM. This is the ONLY place Apple explains that
//     the video was the wrong size or codec.
//   - still transcoding when the wait budget runs out → reported as PENDING, with
//     the preview ids to re-check. Not "done". Not "failed". Pending.
//   - COMPLETE → done, with the videoUrl.
//
// A local ffprobe preflight catches the obvious rejects (wrong length, wrong
// size) BEFORE a multi-megabyte upload and a multi-minute transcode. It NEVER
// re-encodes: silently "fixing" a developer's video would ship something they
// did not make. It reports, and refuses.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ascPreviewPollInterval is how often we ask Apple whether the transcode is
// done. A var so tests can drive the poll loop without sleeping.
var ascPreviewPollInterval = 3 * time.Second

// ascPreviewDefaultWait is the default transcode budget. Apple usually finishes
// a short preview in well under a minute, but it is a queue, not a promise.
const ascPreviewDefaultWait = 180

// Apple's App Preview length window. Documented, and enforced by the transcoder
// — a video outside it is rejected every time, so we refuse before uploading.
const (
	ascPreviewMinSeconds = 15.0
	ascPreviewMaxSeconds = 30.0
)

// ascPreviewTypes documents the preview types we know about. As with
// ascDisplayTypes, unknown values are still passed through to Apple — this map
// only powers hints. Note the enum does NOT simply mirror screenshotDisplayType:
// the iPhone/iPad members drop the `APP_` prefix, while Vision Pro keeps it.
var ascPreviewTypes = map[string]string{
	"APP_APPLE_VISION_PRO": "Apple Vision Pro — 3840x2160",
	"IPHONE_67":            "iPhone 6.7\"",
	"IPHONE_65":            "iPhone 6.5\"",
	"IPHONE_61":            "iPhone 6.1\"",
	"IPHONE_58":            "iPhone 5.8\"",
	"IPHONE_55":            "iPhone 5.5\"",
	"IPAD_PRO_3GEN_129":    "iPad Pro 12.9\" (3rd gen)",
	"IPAD_PRO_3GEN_11":     "iPad Pro 11\"",
	"IPAD_PRO_129":         "iPad Pro 12.9\" (2nd gen)",
	"APPLE_TV":             "Apple TV",
	"DESKTOP":              "Mac",
}

// ascPreviewExactSize holds ONLY the preview types whose required pixel size we
// have actually verified. A wrong entry here would reject a legitimate video, so
// the table stays small on purpose: types absent from it are not size-checked
// locally and Apple remains the authority.
var ascPreviewExactSize = map[string][2]int{
	"APP_APPLE_VISION_PRO": {3840, 2160},
}

// ── asset delivery state (the transcode verdict) ────────────────────────────

// ascAssetError is one entry from assetDeliveryState.errors / .warnings. Apple
// uses `description` here, not the `detail` it uses in API error envelopes —
// both are read so nothing is dropped.
type ascAssetError struct {
	Code        string `json:"code"`
	Description string `json:"description,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

func (e ascAssetError) String() string {
	msg := e.Description
	if msg == "" {
		msg = e.Detail
	}
	if e.Code == "" {
		return msg
	}
	if msg == "" {
		return e.Code
	}
	return e.Code + ": " + msg
}

// ascAssetDeliveryState is Apple's verdict on an uploaded asset.
type ascAssetDeliveryState struct {
	State    string          `json:"state"` // AWAITING_UPLOAD | UPLOAD_COMPLETE | COMPLETE | FAILED
	Errors   []ascAssetError `json:"errors,omitempty"`
	Warnings []ascAssetError `json:"warnings,omitempty"`
}

// asset delivery states.
const (
	assetStateAwaitingUpload = "AWAITING_UPLOAD"
	assetStateUploadComplete = "UPLOAD_COMPLETE"
	assetStateComplete       = "COMPLETE"
	assetStateFailed         = "FAILED"
)

// ── types ───────────────────────────────────────────────────────────────────

// ASCPreviewSet is one preview-type bucket inside a localization.
type ASCPreviewSet struct {
	ID          string `json:"id"`
	PreviewType string `json:"previewType"`
}

// ASCPreview is one uploaded preview video.
type ASCPreview struct {
	ID                   string                `json:"id"`
	FileName             string                `json:"fileName,omitempty"`
	FileSize             int64                 `json:"fileSize,omitempty"`
	PreviewFrameTimeCode string                `json:"previewFrameTimeCode,omitempty"`
	VideoURL             string                `json:"videoUrl,omitempty"`
	AssetDeliveryState   ascAssetDeliveryState `json:"assetDeliveryState"`
}

// ── client ──────────────────────────────────────────────────────────────────

func (a *ascClient) PreviewSets(locID string) ([]ASCPreviewSet, error) {
	out, _, err := a.do("GET", "/appStoreVersionLocalizations/"+locID+"/appPreviewSets?limit=50", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string        `json:"id"`
			Attributes ASCPreviewSet `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	sets := make([]ASCPreviewSet, 0, len(r.Data))
	for _, d := range r.Data {
		s := d.Attributes
		s.ID = d.ID
		sets = append(sets, s)
	}
	return sets, nil
}

// EnsurePreviewSet reuses the existing set for a preview type, creating it only
// when absent (Apple 409s on a duplicate preview type — same as screenshot sets).
func (a *ascClient) EnsurePreviewSet(locID, previewType string) (*ASCPreviewSet, bool, error) {
	sets, err := a.PreviewSets(locID)
	if err != nil {
		return nil, false, err
	}
	for i := range sets {
		if sets[i].PreviewType == previewType {
			return &sets[i], false, nil
		}
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "appPreviewSets",
			"attributes": map[string]interface{}{"previewType": previewType},
			"relationships": map[string]interface{}{
				"appStoreVersionLocalization": map[string]interface{}{
					"data": map[string]string{"type": "appStoreVersionLocalizations", "id": locID},
				},
			},
		},
	}
	out, _, err := a.do("POST", "/appPreviewSets", body)
	if err != nil {
		return nil, false, err
	}
	var r struct {
		Data struct {
			ID         string        `json:"id"`
			Attributes ASCPreviewSet `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, false, err
	}
	s := r.Data.Attributes
	s.ID = r.Data.ID
	if s.PreviewType == "" {
		s.PreviewType = previewType
	}
	return &s, true, nil
}

func (a *ascClient) Previews(setID string) ([]ASCPreview, error) {
	out, _, err := a.do("GET", "/appPreviewSets/"+setID+"/appPreviews?limit=100", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string     `json:"id"`
			Attributes ASCPreview `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	previews := make([]ASCPreview, 0, len(r.Data))
	for _, d := range r.Data {
		p := d.Attributes
		p.ID = d.ID
		previews = append(previews, p)
	}
	return previews, nil
}

func (a *ascClient) DeletePreview(id string) error {
	_, _, err := a.do("DELETE", "/appPreviews/"+id, nil)
	return err
}

// PreviewByID re-reads one preview — this is how the transcode is polled.
func (a *ascClient) PreviewByID(id string) (*ASCPreview, error) {
	out, _, err := a.do("GET", "/appPreviews/"+id, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			ID         string     `json:"id"`
			Attributes ASCPreview `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	p := r.Data.Attributes
	p.ID = r.Data.ID
	if p.ID == "" {
		p.ID = id
	}
	return &p, nil
}

// ReservePreview declares the asset and gets back Apple's upload operations —
// the same reserve step screenshots use.
func (a *ascClient) ReservePreview(setID, fileName string, fileSize int64) (string, []ascUploadOperation, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "appPreviews",
			"attributes": map[string]interface{}{
				"fileSize": fileSize,
				"fileName": fileName,
			},
			"relationships": map[string]interface{}{
				"appPreviewSet": map[string]interface{}{
					"data": map[string]string{"type": "appPreviewSets", "id": setID},
				},
			},
		},
	}
	out, _, err := a.do("POST", "/appPreviews", body)
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
		return "", nil, fmt.Errorf("app store connect returned no preview id for %s", fileName)
	}
	return r.Data.ID, r.Data.Attributes.UploadOperations, nil
}

// CommitPreview tells Apple the bytes landed, proving it with an md5 of the
// WHOLE file. frameTimeCode (optional, "00:00:03:00") picks the poster frame.
//
// A 200 here means Apple HAS the file. It does NOT mean Apple accepted it —
// that is what the transcode poll is for.
func (a *ascClient) CommitPreview(id, md5hex, frameTimeCode string) (*ASCPreview, error) {
	attrs := map[string]interface{}{
		"uploaded":           true,
		"sourceFileChecksum": md5hex,
	}
	if strings.TrimSpace(frameTimeCode) != "" {
		attrs["previewFrameTimeCode"] = frameTimeCode
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "appPreviews",
			"id":         id,
			"attributes": attrs,
		},
	}
	out, _, err := a.do("PATCH", "/appPreviews/"+id, body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			ID         string     `json:"id"`
			Attributes ASCPreview `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	p := r.Data.Attributes
	p.ID = r.Data.ID
	if p.ID == "" {
		p.ID = id
	}
	return &p, nil
}

// UploadPreview runs reserve → uploadOperations → commit for one video file.
// It does NOT wait for the transcode — see WaitForPreviewTranscode.
func (a *ascClient) UploadPreview(setID, path, frameTimeCode string) (*ASCPreview, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	name := filepath.Base(path)
	id, ops, err := a.ReservePreview(setID, name, int64(len(data)))
	if err != nil {
		return nil, err
	}
	if err := runUploadOperations(ops, data); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	sum := md5.Sum(data) // #nosec G401 — Apple mandates md5 for sourceFileChecksum
	return a.CommitPreview(id, hex.EncodeToString(sum[:]), frameTimeCode)
}

// WaitForPreviewTranscode polls until Apple's transcoder reaches a terminal
// state or the budget runs out. It returns the preview in whatever state it
// actually reached — a timeout is NOT an error and NOT a success, it is a
// pending preview, and the caller reports it as such.
func (a *ascClient) WaitForPreviewTranscode(id string, budget time.Duration) (*ASCPreview, error) {
	deadline := time.Now().Add(budget)
	var last *ASCPreview
	for {
		p, err := a.PreviewByID(id)
		if err != nil {
			return last, err
		}
		last = p
		switch p.AssetDeliveryState.State {
		case assetStateComplete, assetStateFailed:
			return p, nil
		}
		if !time.Now().Before(deadline) {
			return p, nil // still transcoding — reported as pending, never as done
		}
		time.Sleep(ascPreviewPollInterval)
	}
}

// ── local preflight (ffprobe) — report, never re-encode ─────────────────────

// videoProbe is what ffprobe can tell us about a preview candidate.
type videoProbe struct {
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Codec    string  `json:"codec"`
	FPS      float64 `json:"fps"`
	Duration float64 `json:"durationSeconds"`
}

// ffprobeLookPath is a seam so tests can pretend ffprobe is (not) installed.
var ffprobeLookPath = func() (string, error) { return exec.LookPath("ffprobe") }

// probeVideo reads a video's real properties. Returns (nil, nil) when ffprobe is
// not installed — that is not an error, it just means Apple is the only judge.
func probeVideo(path string) (*videoProbe, error) {
	bin, err := ffprobeLookPath()
	if err != nil {
		return nil, nil // no ffprobe → skip the preflight, say so, upload anyway
	}
	out, err := exec.Command(bin,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,codec_name,avg_frame_rate:format=duration",
		"-of", "json", path,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", filepath.Base(path), err)
	}
	var r struct {
		Streams []struct {
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			CodecName    string `json:"codec_name"`
			AvgFrameRate string `json:"avg_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("parse ffprobe output for %s: %w", filepath.Base(path), err)
	}
	if len(r.Streams) == 0 {
		return nil, fmt.Errorf("%s has no video stream", filepath.Base(path))
	}
	s := r.Streams[0]
	p := &videoProbe{Width: s.Width, Height: s.Height, Codec: s.CodecName}
	p.FPS = parseFFRational(s.AvgFrameRate)
	p.Duration, _ = strconv.ParseFloat(r.Format.Duration, 64)
	return p, nil
}

// parseFFRational turns ffprobe's "30000/1001" frame rate into 29.97.
func parseFFRational(s string) float64 {
	parts := strings.SplitN(strings.TrimSpace(s), "/", 2)
	num, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	if len(parts) == 1 {
		return num
	}
	den, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || den == 0 {
		return 0
	}
	return num / den
}

// preflightPreview refuses a video Apple would reject anyway. It NEVER modifies
// the file. Returns the probe (nil when ffprobe is absent) so the caller can
// report exactly what it saw.
func preflightPreview(path, previewType string) (*videoProbe, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mov", ".m4v", ".mp4":
	default:
		return nil, fmt.Errorf("%s: App Store previews must be .mov, .m4v or .mp4", filepath.Base(path))
	}
	probe, err := probeVideo(path)
	if err != nil {
		return nil, err
	}
	if probe == nil {
		return nil, nil // ffprobe absent — Apple is the only judge
	}
	name := filepath.Base(path)
	if probe.Duration > 0 && (probe.Duration < ascPreviewMinSeconds || probe.Duration > ascPreviewMaxSeconds) {
		return probe, fmt.Errorf("%s is %.1fs — Apple requires an app preview between %.0f and %.0f seconds. "+
			"Re-cut the video; Yaver will not silently re-encode it",
			name, probe.Duration, ascPreviewMinSeconds, ascPreviewMaxSeconds)
	}
	if want, ok := ascPreviewExactSize[previewType]; ok {
		if probe.Width != want[0] || probe.Height != want[1] {
			return probe, fmt.Errorf("%s is %dx%d — %s requires exactly %dx%d. "+
				"Re-export at the right size; Yaver will not silently re-encode it",
				name, probe.Width, probe.Height, previewType, want[0], want[1])
		}
	}
	return probe, nil
}

// ── ops verb ────────────────────────────────────────────────────────────────

// storePreviewArgs is store_previews_set's payload. It mirrors
// store_screenshots_set, plus the video-only knobs.
type storePreviewArgs struct {
	Project  string `json:"project"`
	BundleID string `json:"bundleId"`
	AppID    string `json:"appId"`
	Platform string `json:"platform"`
	Locale   string `json:"locale"`

	PreviewType   string   `json:"previewType"`
	Files         []string `json:"files"`
	Replace       bool     `json:"replace"`
	FrameTimeCode string   `json:"previewFrameTimeCode"`
	WaitSeconds   int      `json:"waitSeconds"`
	SkipPreflight bool     `json:"skipPreflight"`
}

// submitArgs reuses ops_store_submit.go's target resolution (creds → app →
// platform → editable version) so previews cannot drift from screenshots.
func (a storePreviewArgs) submitArgs() storeSubmitArgs {
	return storeSubmitArgs{
		Project:  a.Project,
		BundleID: a.BundleID,
		AppID:    a.AppID,
		Platform: a.Platform,
		Locale:   a.Locale,
	}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "store_previews_set",
		Description: "Upload App Store PREVIEW VIDEOS for one preview type: reserves each asset, executes Apple's uploadOperations against its asset host, commits with an md5 checksum, then POLLS Apple's async transcoder and reports the verdict. If Apple fails the transcode (wrong resolution/codec/frame rate) its assetDeliveryState.errors are surfaced VERBATIM and the verb FAILS — an asset Apple rejected is never reported as uploaded. A local ffprobe preflight refuses an obviously-invalid video before uploading; nothing is ever re-encoded. Vision Pro is APP_APPLE_VISION_PRO (3840x2160); iPhone is IPHONE_67 (note: preview types drop the APP_ prefix that screenshot display types carry).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project":              map[string]interface{}{"type": "string"},
			"bundleId":             map[string]interface{}{"type": "string"},
			"appId":                map[string]interface{}{"type": "string"},
			"platform":             map[string]interface{}{"type": "string", "description": "IOS | MAC_OS | TV_OS | VISION_OS (default IOS)."},
			"locale":               map[string]interface{}{"type": "string", "description": "Default en-US."},
			"previewType":          map[string]interface{}{"type": "string", "description": "Apple preview type, e.g. APP_APPLE_VISION_PRO, IPHONE_67, IPAD_PRO_3GEN_129, APPLE_TV."},
			"files":                map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Absolute paths to the .mov/.m4v/.mp4 previews on the target machine, in display order. Apple requires 15-30 seconds."},
			"replace":              map[string]interface{}{"type": "boolean", "description": "Delete the previews already in this set before uploading (default false = append)."},
			"previewFrameTimeCode": map[string]interface{}{"type": "string", "description": "Poster-frame timecode, e.g. 00:00:03:00."},
			"waitSeconds":          map[string]interface{}{"type": "integer", "description": "How long to wait for Apple's transcode verdict (default 180). Still-transcoding previews are reported as pending, never as done."},
			"skipPreflight":        map[string]interface{}{"type": "boolean", "description": "Skip the local ffprobe check and let Apple be the only judge."},
		}, "previewType", "files"),
		Handler:    storePreviewsSetHandler,
		AllowGuest: false,
	})
}

func storePreviewsSetHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var a storePreviewArgs
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &a); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if strings.TrimSpace(a.Locale) == "" {
		a.Locale = "en-US"
	}
	if strings.TrimSpace(a.PreviewType) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "previewType required (e.g. APP_APPLE_VISION_PRO, IPHONE_67)"}
	}
	if len(a.Files) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "files required — absolute paths to the preview videos, in display order"}
	}

	// Local preflight FIRST: never start a multi-megabyte upload for a video
	// Apple is certain to reject.
	probes := map[string]*videoProbe{}
	for _, f := range a.Files {
		st, err := os.Stat(f)
		if err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: "preview " + f + ": " + err.Error()}
		}
		if st.IsDir() {
			return OpsResult{OK: false, Code: "bad_payload", Error: f + " is a directory"}
		}
		if a.SkipPreflight {
			continue
		}
		probe, err := preflightPreview(f, a.PreviewType)
		if err != nil {
			res := OpsResult{OK: false, Code: "invalid_asset", Error: err.Error()}
			if probe != nil {
				res.Initial = map[string]interface{}{"probe": probe, "file": f}
			}
			return res
		}
		if probe != nil {
			probes[filepath.Base(f)] = probe
		}
	}

	t, deny := resolveSubmitTarget(a.submitArgs())
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
	set, created, err := t.cl.EnsurePreviewSet(loc.ID, a.PreviewType)
	if err != nil {
		return ascFailure("ensure preview set "+a.PreviewType, nil, err)
	}

	deleted := 0
	if a.Replace {
		existing, err := t.cl.Previews(set.ID)
		if err != nil {
			return ascFailure("list existing previews", nil, err)
		}
		for _, p := range existing {
			if err := t.cl.DeletePreview(p.ID); err != nil {
				return ascFailure("delete preview "+p.ID, nil, err)
			}
			deleted++
		}
	}

	budget := time.Duration(a.WaitSeconds) * time.Second
	if a.WaitSeconds <= 0 {
		budget = ascPreviewDefaultWait * time.Second
	}

	uploaded := make([]map[string]interface{}, 0, len(a.Files))
	var failed, pending []map[string]interface{}

	for _, f := range a.Files {
		name := filepath.Base(f)
		prev, err := t.cl.UploadPreview(set.ID, f, a.FrameTimeCode)
		if err != nil {
			return OpsResult{OK: false, Code: "upload_failed", Error: fmt.Sprintf(
				"uploaded %d/%d previews, then %s failed: %v", len(uploaded), len(a.Files), name, err)}
		}
		// Apple has the bytes. Now find out whether Apple ACCEPTS them.
		final, err := t.cl.WaitForPreviewTranscode(prev.ID, budget)
		if err != nil {
			return ascFailure("poll transcode state for "+name, nil, err)
		}
		entry := map[string]interface{}{
			"id":       final.ID,
			"fileName": name,
			"state":    final.AssetDeliveryState.State,
		}
		if final.VideoURL != "" {
			entry["videoUrl"] = final.VideoURL
		}
		if p, ok := probes[name]; ok {
			entry["probe"] = p
		}
		if len(final.AssetDeliveryState.Warnings) > 0 {
			entry["warnings"] = final.AssetDeliveryState.Warnings
		}

		switch final.AssetDeliveryState.State {
		case assetStateFailed:
			// Apple's own words. This is the ONLY place it says WHY.
			entry["errors"] = final.AssetDeliveryState.Errors
			failed = append(failed, entry)
		case assetStateComplete:
			uploaded = append(uploaded, entry)
		default:
			pending = append(pending, entry)
		}
	}

	res := map[string]interface{}{
		"app":           t.app,
		"platform":      t.platform,
		"version":       t.version,
		"locale":        loc.Locale,
		"previewType":   set.PreviewType,
		"setId":         set.ID,
		"setCreated":    created,
		"deleted":       deleted,
		"uploaded":      uploaded,
		"uploadedCount": len(uploaded),
		"expectedSpec":  ascPreviewTypes[a.PreviewType],
	}
	if len(pending) > 0 {
		res["pending"] = pending
		res["note"] = fmt.Sprintf(
			"%d preview(s) are STILL TRANSCODING after %s. Apple has the bytes but has not returned a verdict — "+
				"they are NOT accepted yet. Re-run store_previews_set (or check App Store Connect) to see the final state.",
			len(pending), budget)
	}

	// An asset Apple failed is never reported as uploaded.
	if len(failed) > 0 {
		res["failed"] = failed
		lines := []string{fmt.Sprintf(
			"Apple FAILED the transcode for %d of %d preview(s). The bytes uploaded, but Apple REJECTED the video — "+
				"it is not on your listing. Apple's own words:", len(failed), len(a.Files))}
		for _, f := range failed {
			name, _ := f["fileName"].(string)
			errs, _ := f["errors"].([]ascAssetError)
			if len(errs) == 0 {
				lines = append(lines, fmt.Sprintf("  • %s: FAILED (Apple returned no error detail)", name))
				continue
			}
			for _, e := range errs {
				lines = append(lines, fmt.Sprintf("  • %s: %s", name, e.String()))
			}
		}
		lines = append(lines, "",
			"Yaver does NOT re-encode your video. Fix the source to match Apple's spec "+
				"(resolution / codec / frame rate / 15-30s) and re-run.")
		return OpsResult{
			OK:      false,
			Code:    "apple_rejected",
			Error:   strings.Join(lines, "\n"),
			Initial: res,
		}
	}
	return OpsResult{OK: true, Initial: res}
}
