package main

// shots_asc.go — App Store Connect backend for `yaver shots`, POST /shots/upload
// and the Studio uploader.
//
// NATIVE GO. This used to shell out to three //go:embed'd python scripts
// (upload-appstore.py, set-appstore-info.py, submit-appstore.py) materialized to
// ~/.yaver/shots-scripts. They duplicated the App Store Connect REST sequence
// that ops_store_submit.go now drives natively, so they are gone and the agent
// no longer needs a python runtime (nor PyJWT / requests) to talk to Apple.
//
// Everything here goes through ops_store_submit.go's proven ascClient helpers:
// EditableVersion / Localizations / EnsureScreenshotSet / UploadScreenshot
// (reserve → uploadOperations → commit) / EnsureReviewSubmission →
// AddReviewSubmissionItem → SubmitReviewSubmission.
//
// The canonical HUMAN-runnable copies of the python scripts still live at
// scripts/screenshots/*.py and scripts/set-appstore-info.py. Those are for
// people; the agent does not read them.
//
// ── AUTH ────────────────────────────────────────────────────────────────────
// `yaver shots` is the single-user LOCAL path, and the python scripts it
// replaces read the APP_STORE_KEY_ID / _ISSUER / _PATH env triple. So creds here
// resolve vault-first, then fall back to that env triple — which is also
// CLAUDE.md's documented escape hatch when the vault locks after a token
// rotation (`source ~/.appstoreconnect/yaver.env`).
//
// The multi-tenant ops_store_* verbs deliberately do NOT get this fallback: on a
// managed-cloud box a process env var must never leak one developer's ASC key
// into another developer's project scope. That asymmetry is the point — do not
// "simplify" it by moving the env fallback into resolveAppleASCCreds.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ascLog mirrors what the python scripts printed, on stderr so stdout stays
// clean for callers that parse it.
func ascLog(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}

// ── creds ───────────────────────────────────────────────────────────────────

// resolveShotsASCCreds resolves the App Store Connect key for the local shots
// path: vault first, then the env triple the python scripts used.
func resolveShotsASCCreds(project string) (*ascCreds, error) {
	if c, err := resolveAppleASCCreds(project); err == nil {
		return c, nil
	}
	keyPath := strings.TrimSpace(os.Getenv("APP_STORE_KEY_PATH"))
	keyID := strings.TrimSpace(os.Getenv("APP_STORE_KEY_ID"))
	issuer := strings.TrimSpace(os.Getenv("APP_STORE_KEY_ISSUER"))
	if keyPath == "" || keyID == "" || issuer == "" {
		return nil, fmt.Errorf("no App Store Connect credentials — put them in the vault " +
			"(`yaver vault add APP_STORE_KEY_ID --project mobile --value …`) or export " +
			"APP_STORE_KEY_ID / APP_STORE_KEY_ISSUER / APP_STORE_KEY_PATH " +
			"(e.g. `source ~/.appstoreconnect/yaver.env`)")
	}
	pem, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read ASC key %s: %w", keyPath, err)
	}
	return &ascCreds{KeyPEM: string(pem), KeyID: keyID, IssuerID: issuer}, nil
}

// newShotsASCClient is the seam the shots backend resolves its client through,
// so tests can aim it at a real httptest ASC server (repo convention: no mocks).
var newShotsASCClient = func(project string) (*ascClient, error) {
	creds, err := resolveShotsASCCreds(project)
	if err != nil {
		return nil, err
	}
	return &ascClient{creds: creds, http: &http.Client{Timeout: 45 * time.Second}}, nil
}

// ── target resolution ───────────────────────────────────────────────────────

// ascUploadPlan says WHERE a set of screenshots belongs in App Store Connect.
// The zero value is the historical default: the iPhone pair on the iOS platform,
// which is exactly what shots did before Vision Pro existed as a target.
type ascUploadPlan struct {
	Platform     string   // ASC platform enum; "" → IOS
	DisplayTypes []string // ASC screenshotDisplayType(s); nil → the iPhone pair
}

// defaultShotsDisplayTypes is what upload-appstore.py defaulted --display-types
// to. 6.7"/6.9" and 6.5" share the same 1290x2796 asset.
var defaultShotsDisplayTypes = []string{"APP_IPHONE_67", "APP_IPHONE_65"}

func (p ascUploadPlan) platform() (string, error) {
	return normalizePlatform(p.Platform)
}

func (p ascUploadPlan) displayTypes() []string {
	if len(p.DisplayTypes) == 0 {
		return defaultShotsDisplayTypes
	}
	return p.DisplayTypes
}

// ascEditableVersionFor resolves app → the editable version for the platform,
// erroring with the same guidance the python scripts printed when there is none.
func ascEditableVersionFor(cl *ascClient, bundleID, platform string) (*ASCApp, *ASCVersion, error) {
	app, err := cl.AppByBundleID(bundleID)
	if err != nil {
		return nil, nil, err
	}
	ascLog("App: %s (%s)", app.Name, app.ID)
	v, err := cl.EditableVersion(app.ID, platform)
	if err != nil {
		return app, nil, err
	}
	if v == nil {
		return app, nil, fmt.Errorf("no %s App Store version for %s — add the platform and create a version "+
			"in App Store Connect first (adding a platform has no REST endpoint)", platform, app.Name)
	}
	if !v.Editable {
		return app, nil, fmt.Errorf("version %s is %s, not editable — App Store Connect only accepts "+
			"metadata/screenshot changes in PREPARE_FOR_SUBMISSION", v.VersionString, v.state())
	}
	ascLog("Version: %s (%s)", v.VersionString, v.state())
	return app, v, nil
}

// ── screenshots ─────────────────────────────────────────────────────────────

// ascUploadScreenshots uploads every PNG in dir (sorted, so 01_/02_ ordering
// holds) into each display type of the plan, and sets the age rating — the exact
// job upload-appstore.py did.
func ascUploadScreenshots(bundleID, dir, locale string, plan ascUploadPlan) error {
	platform, err := plan.platform()
	if err != nil {
		return err
	}
	files, err := ascScreenshotFiles(dir)
	if err != nil {
		return err
	}
	cl, err := newShotsASCClient("")
	if err != nil {
		return err
	}
	app, version, err := ascEditableVersionFor(cl, bundleID, platform)
	if err != nil {
		return err
	}
	loc, err := cl.LocalizationFor(version.ID, locale)
	if err != nil {
		return err
	}

	// Age rating is best-effort, exactly as in the python (a warning, never fatal).
	if err := cl.SetAgeRatingAllAges(app.ID); err != nil {
		ascLog("  WARNING: could not set age rating: %v", err)
	} else {
		ascLog("  Age rating set (4+ / suitable for all ages)")
	}

	for _, displayType := range plan.displayTypes() {
		ascLog("\nUploading %d screenshots for %s…", len(files), displayType)
		set, created, err := cl.EnsureScreenshotSet(loc.ID, displayType)
		if err != nil {
			return fmt.Errorf("screenshot set %s: %w", displayType, err)
		}
		if created {
			ascLog("  Created screenshot set for %s: %s", displayType, set.ID)
		} else {
			ascLog("  Found existing screenshot set for %s: %s", displayType, set.ID)
		}
		for _, f := range files {
			shot, err := cl.UploadScreenshot(set.ID, f)
			if err != nil {
				return fmt.Errorf("upload %s to %s: %w", filepath.Base(f), displayType, err)
			}
			ascLog("    Uploaded: %s", shot.FileName)
		}
		ascLog("Uploaded %d/%d screenshots to %s", len(files), len(files), displayType)
	}
	return nil
}

// ascScreenshotFiles lists the PNGs to upload, sorted by name.
func ascScreenshotFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".png") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no PNGs found in %s", dir)
	}
	return files, nil
}

// SetAgeRatingAllAges declares the lowest ratings on the app's ageRatingDeclaration.
func (a *ascClient) SetAgeRatingAllAges(appID string) error {
	infos, err := a.AppInfos(appID)
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		return fmt.Errorf("app has no appInfo records")
	}
	out, _, err := a.do("GET", "/appInfos/"+infos[0].ID+"/ageRatingDeclaration", nil)
	if err != nil {
		return err
	}
	var r struct {
		Data *struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return err
	}
	if r.Data == nil || r.Data.ID == "" {
		return fmt.Errorf("no age rating declaration on this app")
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "ageRatingDeclarations",
			"id":   r.Data.ID,
			"attributes": map[string]interface{}{
				"alcoholTobaccoOrDrugUseOrReferences":         "NONE",
				"contests":                                    "NONE",
				"gamblingAndContests":                         false,
				"gambling":                                    false,
				"gamblingSimulated":                           "NONE",
				"horrorOrFearThemes":                          "NONE",
				"matureOrSuggestiveThemes":                    "NONE",
				"medicalOrTreatmentInformation":               "NONE",
				"profanityOrCrudeHumor":                       "NONE",
				"sexualContentGraphicAndNudity":               "NONE",
				"sexualContentOrNudity":                       "NONE",
				"violenceCartoonOrFantasy":                    "NONE",
				"violenceRealistic":                           "NONE",
				"violenceRealisticProlongedGraphicOrSadistic": "NONE",
				"unrestrictedWebAccess":                       false,
				"kidsAgeBand":                                 nil,
				"seventeenPlus":                               false,
			},
		},
	}
	_, _, err = a.do("PATCH", "/ageRatingDeclarations/"+r.Data.ID, body)
	return err
}

// ── metadata ────────────────────────────────────────────────────────────────

// shotsDefaultMetaBundleID is the bundle the built-in metadata below describes.
// Targeting a DIFFERENT app with no metadata file must never stamp this copy
// onto someone else's listing — ascSetMetadata refuses instead (the python did
// the same, and exited 0, so a missing listing file never fails a shots run).
const shotsDefaultMetaBundleID = "io.yaver.mobile"

// ascListingMeta is the listing content set-appstore-info.py drove, as data.
// Loaded from a metadata JSON file or a project's .yaver/appstore.json.
type ascListingMeta struct {
	BundleID          string `json:"bundleId"`
	Name              string `json:"name"`
	Subtitle          string `json:"subtitle"`
	Copyright         string `json:"copyright"`
	PrivacyPolicyURL  string `json:"privacyPolicyUrl"`
	SupportURL        string `json:"supportUrl"`
	MarketingURL      string `json:"marketingUrl"`
	PrimaryCategory   string `json:"primaryCategory"`
	SecondaryCategory string `json:"secondaryCategory"`
	Description       string `json:"description"`
	Keywords          string `json:"keywords"`
	WhatsNew          string `json:"whatsNew"`
	Locale            string `json:"locale"`
}

// defaultYaverListingMeta is the built-in listing for shotsDefaultMetaBundleID,
// carried over verbatim from set-appstore-info.py's module constants.
func defaultYaverListingMeta() ascListingMeta {
	return ascListingMeta{
		BundleID:          shotsDefaultMetaBundleID,
		Name:              "Yaver IO",
		Subtitle:          "Code from your phone",
		Copyright:         "2026 SIMKAB ELEKTRIK",
		PrivacyPolicyURL:  "https://yaver.io/privacy",
		SupportURL:        "https://yaver.io",
		MarketingURL:      "https://yaver.io",
		PrimaryCategory:   "DEVELOPER_TOOLS",
		SecondaryCategory: "PRODUCTIVITY",
		Locale:            "en-US",
		Keywords:          "ai,coding,developer,agent,claude,remote,peer-to-peer,terminal,codex,aider",
		Description: `Yaver lets developers run AI coding agents on their development machines — directly from their phone.

Your code never leaves your machine. Tasks flow peer-to-peer between your phone and your dev machine through encrypted connections. Our servers only handle authentication and peer discovery.

HOW IT WORKS
1. Install the Yaver CLI on your dev machine
2. Open the Yaver app on your phone
3. Send coding tasks to your machine from anywhere

FEATURES
• Run Claude, Codex, Aider, or any custom AI agent
• Switch between agents per task — use the best tool for each job
• Works over Wi-Fi and cellular — seamless roaming between networks
• Direct connection when on the same network, relay fallback when remote
• See real-time output as your agent works
• Multiple device support — connect to any of your dev machines

PRIVACY FIRST
• Your code and task data never touch our servers
• All communication is end-to-end encrypted
• Relay servers are pass-through only — zero data storage
• Open infrastructure: relay servers, CLI, and networking are transparent

REQUIREMENTS
• A Mac, Linux, or Windows machine with the Yaver CLI installed
• An AI agent (Claude Code, OpenAI Codex, Aider, or any CLI-based agent)`,
		WhatsNew: `• Network-aware reconnection — seamless WiFi to cellular transitions
• Increased connection resilience with 15 retry attempts
• Choose your AI agent per task from the app
• Improved connection stability and error recovery`,
	}
}

// loadShotsMeta resolves the listing metadata. metaSource is a metadata JSON
// file, or a project dir holding .yaver/appstore.json; empty means "no file".
// Returns (meta, loadedFromFile, err) — built-in defaults when no file is found.
func loadShotsMeta(metaSource string) (ascListingMeta, bool, error) {
	meta := defaultYaverListingMeta()
	metaSource = strings.TrimSpace(metaSource)
	if metaSource == "" {
		return meta, false, nil
	}
	path := metaSource
	if st, err := os.Stat(metaSource); err == nil && st.IsDir() {
		path = filepath.Join(metaSource, ".yaver", "appstore.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return meta, false, nil // no listing file is not an error
		}
		return meta, false, err
	}
	// Unmarshal ONTO the defaults so an omitted key keeps its default and an
	// explicitly-set key wins — the python's globals()-override semantics.
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, false, fmt.Errorf("parse %s: %w", path, err)
	}
	ascLog("Loaded metadata overrides from %s", path)
	return meta, true, nil
}

// ascSetMetadata sets the App Store listing for the bundle: categories, app-info
// localization (name/subtitle/privacy URL), version localization (description /
// keywords / what's new / URLs), copyright, content rights and free pricing.
//
// metaSource is a metadata JSON file or a project dir holding
// .yaver/appstore.json; empty uses the built-in defaults, which describe
// shotsDefaultMetaBundleID ONLY.
func ascSetMetadata(bundleID, metaSource, platform string) error {
	plat, err := normalizePlatform(platform)
	if err != nil {
		return err
	}
	meta, loaded, err := loadShotsMeta(metaSource)
	if err != nil {
		return err
	}
	if loaded && strings.TrimSpace(meta.BundleID) != "" {
		bundleID = meta.BundleID
	}

	// Cross-app contamination guard: the built-in copy describes Yaver. Refuse to
	// stamp it onto another developer's listing. Not an error — screenshots were
	// uploaded regardless; only metadata is skipped (python exited 0 here too).
	if bundleID != shotsDefaultMetaBundleID && !loaded {
		ascLog("Refusing to set metadata for %s: no metadata provided.", bundleID)
		ascLog("Add a .yaver/appstore.json (or pass a metadata file) with this app's " +
			"name/subtitle/description/keywords/categories/URLs. Screenshots were " +
			"uploaded regardless — only metadata is skipped.")
		return nil
	}
	if strings.TrimSpace(meta.Locale) == "" {
		meta.Locale = "en-US"
	}

	cl, err := newShotsASCClient("")
	if err != nil {
		return err
	}
	app, err := cl.AppByBundleID(bundleID)
	if err != nil {
		return err
	}
	ascLog("App Store Connect Info Updater — %s (%s)", app.Name, bundleID)

	// 1. Categories on the editable appInfo.
	infos, err := cl.AppInfos(app.ID)
	if err != nil {
		return err
	}
	info := pickEditableAppInfo(infos)
	if info == nil {
		return fmt.Errorf("no appInfo records for %s", bundleID)
	}
	if meta.PrimaryCategory != "" || meta.SecondaryCategory != "" {
		if err := cl.SetAppInfoCategories(info.ID, meta.PrimaryCategory, meta.SecondaryCategory); err != nil {
			ascLog("  WARNING: could not set categories: %v", err)
		} else {
			ascLog("  Categories updated: %s / %s", meta.PrimaryCategory, meta.SecondaryCategory)
		}
	}

	// 2. App-info localization — name, subtitle, privacy policy URL.
	infoLocAttrs := map[string]interface{}{
		"name":             meta.Name,
		"subtitle":         meta.Subtitle,
		"privacyPolicyUrl": meta.PrivacyPolicyURL,
	}
	if err := cl.EnsureAppInfoLocalization(info.ID, meta.Locale, infoLocAttrs); err != nil {
		ascLog("  WARNING: could not set app info localization: %v", err)
	} else {
		ascLog("  appInfo localization updated (name, subtitle, privacy policy URL)")
	}

	// 3. Version localization + copyright. Absent an editable version there is
	// nothing to write — say so instead of failing the whole run.
	version, err := cl.EditableVersion(app.ID, plat)
	if err != nil {
		return err
	}
	if version == nil {
		ascLog("WARNING: no %s App Store version — skipping version localization + copyright.", plat)
		return nil
	}
	verLocAttrs := map[string]interface{}{
		"description":  meta.Description,
		"keywords":     meta.Keywords,
		"supportUrl":   meta.SupportURL,
		"marketingUrl": meta.MarketingURL,
	}
	// whatsNew is rejected on a first version (nothing to be "new" against), so
	// retry without it — the python did the same.
	withNew := map[string]interface{}{"whatsNew": meta.WhatsNew}
	for k, v := range verLocAttrs {
		withNew[k] = v
	}
	if err := cl.EnsureVersionLocalization(version.ID, meta.Locale, withNew); err != nil {
		ascLog("  Retrying version localization without whatsNew (initial version)…")
		if err := cl.EnsureVersionLocalization(version.ID, meta.Locale, verLocAttrs); err != nil {
			ascLog("  WARNING: could not set version localization: %v", err)
		} else {
			ascLog("  Version localization updated (description, keywords, URLs)")
		}
	} else {
		ascLog("  Version localization updated (description, keywords, what's new, URLs)")
	}
	if meta.Copyright != "" {
		if err := cl.SetVersionAttrs(version.ID, map[string]interface{}{"copyright": meta.Copyright}); err != nil {
			ascLog("  WARNING: could not set copyright: %v", err)
		} else {
			ascLog("  Copyright updated: %s", meta.Copyright)
		}
	}

	// 4. Content rights + free pricing — both best-effort, as in the python.
	if err := cl.SetContentRights(app.ID, "DOES_NOT_USE_THIRD_PARTY_CONTENT"); err != nil {
		ascLog("  WARNING: could not set content rights: %v", err)
	} else {
		ascLog("  Content rights set (no third-party content)")
	}
	if err := cl.SetPricingFree(app.ID); err != nil {
		ascLog("  WARNING: could not set pricing to FREE: %v", err)
		ascLog("  Set pricing manually in App Store Connect.")
	} else {
		ascLog("  Pricing set to FREE")
	}
	return nil
}

// ASCAppInfo is one appInfo record (the app-level, version-independent listing).
type ASCAppInfo struct {
	ID            string `json:"id"`
	AppStoreState string `json:"appStoreState,omitempty"`
	State         string `json:"state,omitempty"`
}

func (i *ASCAppInfo) state() string {
	if i.State != "" {
		return i.State
	}
	return i.AppStoreState
}

func (a *ascClient) AppInfos(appID string) ([]ASCAppInfo, error) {
	out, _, err := a.do("GET", "/apps/"+appID+"/appInfos?limit=10", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID         string     `json:"id"`
			Attributes ASCAppInfo `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	infos := make([]ASCAppInfo, 0, len(r.Data))
	for _, d := range r.Data {
		i := d.Attributes
		i.ID = d.ID
		infos = append(infos, i)
	}
	return infos, nil
}

// pickEditableAppInfo prefers an appInfo in an editable state, else the first.
func pickEditableAppInfo(infos []ASCAppInfo) *ASCAppInfo {
	editable := map[string]bool{
		"PREPARE_FOR_SUBMISSION": true,
		"READY_FOR_SALE":         true,
		"READY_FOR_DISTRIBUTION": true,
	}
	for i := range infos {
		if editable[infos[i].state()] {
			return &infos[i]
		}
	}
	if len(infos) > 0 {
		return &infos[0]
	}
	return nil
}

// SetAppInfoCategories sets the primary/secondary category relationships.
func (a *ascClient) SetAppInfoCategories(appInfoID, primary, secondary string) error {
	rels := map[string]interface{}{}
	if primary != "" {
		rels["primaryCategory"] = map[string]interface{}{
			"data": map[string]string{"type": "appCategories", "id": primary},
		}
	}
	if secondary != "" {
		rels["secondaryCategory"] = map[string]interface{}{
			"data": map[string]string{"type": "appCategories", "id": secondary},
		}
	}
	if len(rels) == 0 {
		return nil
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":          "appInfos",
			"id":            appInfoID,
			"relationships": rels,
		},
	}
	_, _, err := a.do("PATCH", "/appInfos/"+appInfoID, body)
	return err
}

// EnsureAppInfoLocalization PATCHes the locale's app-info localization, creating
// it when the app has none for that locale.
func (a *ascClient) EnsureAppInfoLocalization(appInfoID, locale string, attrs map[string]interface{}) error {
	out, _, err := a.do("GET", "/appInfos/"+appInfoID+"/appInfoLocalizations?limit=50", nil)
	if err != nil {
		return err
	}
	var r struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Locale string `json:"locale"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return err
	}
	for _, d := range r.Data {
		if strings.EqualFold(d.Attributes.Locale, locale) {
			body := map[string]interface{}{
				"data": map[string]interface{}{
					"type":       "appInfoLocalizations",
					"id":         d.ID,
					"attributes": attrs,
				},
			}
			_, _, err := a.do("PATCH", "/appInfoLocalizations/"+d.ID, body)
			return err
		}
	}
	create := map[string]interface{}{"locale": locale}
	for k, v := range attrs {
		create[k] = v
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "appInfoLocalizations",
			"attributes": create,
			"relationships": map[string]interface{}{
				"appInfo": map[string]interface{}{
					"data": map[string]string{"type": "appInfos", "id": appInfoID},
				},
			},
		},
	}
	_, _, err = a.do("POST", "/appInfoLocalizations", body)
	return err
}

// EnsureVersionLocalization PATCHes the locale's version localization, creating
// it when the version has none for that locale.
func (a *ascClient) EnsureVersionLocalization(versionID, locale string, attrs map[string]interface{}) error {
	locs, err := a.Localizations(versionID)
	if err != nil {
		return err
	}
	for _, l := range locs {
		if strings.EqualFold(l.Locale, locale) {
			_, err := a.SetLocalization(l.ID, attrs)
			return err
		}
	}
	create := map[string]interface{}{"locale": locale}
	for k, v := range attrs {
		create[k] = v
	}
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "appStoreVersionLocalizations",
			"attributes": create,
			"relationships": map[string]interface{}{
				"appStoreVersion": map[string]interface{}{
					"data": map[string]string{"type": "appStoreVersions", "id": versionID},
				},
			},
		},
	}
	_, _, err = a.do("POST", "/appStoreVersionLocalizations", body)
	return err
}

// SetVersionAttrs PATCHes attributes on an App Store version (copyright,
// usesNonExemptEncryption, …).
func (a *ascClient) SetVersionAttrs(versionID string, attrs map[string]interface{}) error {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "appStoreVersions",
			"id":         versionID,
			"attributes": attrs,
		},
	}
	_, _, err := a.do("PATCH", "/appStoreVersions/"+versionID, body)
	return err
}

// SetContentRights declares whether the app uses third-party content.
func (a *ascClient) SetContentRights(appID, declaration string) error {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type":       "apps",
			"id":         appID,
			"attributes": map[string]interface{}{"contentRightsDeclaration": declaration},
		},
	}
	_, _, err := a.do("PATCH", "/apps/"+appID, body)
	return err
}

// SetPricingFree puts the app on the free price point in the USA base territory.
// Apple 409s when a schedule already exists — that means "already priced", so it
// is a success, not a failure.
func (a *ascClient) SetPricingFree(appID string) error {
	out, _, err := a.do("GET", "/apps/"+appID+"/appPricePoints?filter[territory]=USA&limit=1", nil)
	if err != nil {
		return err
	}
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return err
	}
	if len(r.Data) == 0 {
		return fmt.Errorf("no price points for the USA territory")
	}
	freePP := r.Data[0].ID
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "appPriceSchedules",
			"relationships": map[string]interface{}{
				"app":           map[string]interface{}{"data": map[string]string{"type": "apps", "id": appID}},
				"baseTerritory": map[string]interface{}{"data": map[string]string{"type": "territories", "id": "USA"}},
				"manualPrices": map[string]interface{}{
					"data": []map[string]string{{"type": "appPrices", "id": "${price1}"}},
				},
			},
		},
		"included": []map[string]interface{}{{
			"type": "appPrices",
			"id":   "${price1}",
			"relationships": map[string]interface{}{
				"appPricePoint": map[string]interface{}{
					"data": map[string]string{"type": "appPricePoints", "id": freePP},
				},
			},
		}},
	}
	_, status, err := a.do("POST", "/appPriceSchedules", body)
	if status == http.StatusConflict {
		return nil // already scheduled — nothing to do
	}
	return err
}

// ── submit ──────────────────────────────────────────────────────────────────

// ascStagedHint is what "staged" means to the user: nothing is broken, one
// gated item remains, and it is a single click in the Console.
const ascStagedHint = "everything is uploaded and staged in App Store Connect. " +
	"Finish the gated item above (often export compliance, pricing/agreements, a build " +
	"still processing, or a Console-only field) and tap Submit — one click."

// ascSubmitForReview attempts the submission. Returns (submitted, err):
//
//   - submitted=true → the version is WAITING_FOR_REVIEW.
//   - submitted=false, err=nil → STAGED: everything is uploaded and Apple is
//     gating on something (pricing/agreements/export compliance, a build still
//     processing, or a Console-only field). Apple's own words are printed
//     VERBATIM — including meta.associatedErrors, the only place Apple names the
//     specific field that is blocking. We never collapse that to "failed", and we
//     never claim a submission that did not happen.
//   - err != nil → a hard failure (auth, app not found, transport).
func ascSubmitForReview(bundleID, version, platform string) (bool, error) {
	plat, err := normalizePlatform(platform)
	if err != nil {
		return false, err
	}
	cl, err := newShotsASCClient("")
	if err != nil {
		return false, err
	}
	app, err := cl.AppByBundleID(bundleID)
	if err != nil {
		return false, err
	}
	ascLog("App: %s (%s)", app.Name, app.ID)

	// 1. An editable version, created on demand when a version string says which.
	v, err := cl.EditableVersion(app.ID, plat)
	if err != nil {
		return false, err
	}
	if v != nil && !v.Editable {
		ascLog("Version %s is %s — not editable.", v.VersionString, v.state())
		ascLog("STAGED: nothing to submit until it returns to PREPARE_FOR_SUBMISSION.")
		return false, nil
	}
	if v == nil {
		if strings.TrimSpace(version) == "" {
			ascLog("No editable %s version exists and no version string was given.", plat)
			ascLog("STAGED: create the version in App Store Connect (or pass --version).")
			return false, nil
		}
		v, err = cl.CreateVersion(app.ID, plat, strings.TrimSpace(version))
		if err != nil {
			ascLog("Could not create version %s: %v", version, err)
			ascLog("STAGED: create the version manually, then re-run.")
			return false, nil
		}
		ascLog("  Created version %s [%s]", v.VersionString, v.ID)
	}
	ascLog("Editable version: %s (%s) [%s]", v.VersionString, v.state(), v.ID)

	// 2. Export compliance — best-effort; some apps declare it on the build.
	if err := cl.SetVersionAttrs(v.ID, map[string]interface{}{"usesNonExemptEncryption": false}); err != nil {
		ascLog("  (skip) export compliance not set via the version: %v", err)
	} else {
		ascLog("  Export compliance set (usesNonExemptEncryption=false)")
	}

	// 3. Bind the newest processed build, if one exists and none is bound yet.
	if err := ascBindLatestBuild(cl, app.ID, plat, v); err != nil {
		ascLog("  (skip) %v", err)
	}

	// 4. Submit: create/reuse the envelope, itemise the version, flip submitted.
	sub, _, err := cl.EnsureReviewSubmission(app.ID, plat)
	if err != nil {
		ascLog("Submission not accepted yet: %v", err)
		ascLog("STAGED: %s", ascStagedHint)
		return false, nil
	}
	existing, err := cl.ReviewSubmissionVersionIDs(sub.ID)
	if err != nil {
		return false, err
	}
	if !existing[v.ID] {
		body, err := cl.AddReviewSubmissionItem(sub.ID, v.ID)
		if err != nil {
			// THE money path: Apple's 409 carries meta.associatedErrors naming each
			// field that is blocking. Print them verbatim — never summarise away.
			ascLog("Submission not accepted yet: %v", err)
			ascPrintAssociated(body)
			ascLog("STAGED: %s", ascStagedHint)
			return false, nil
		}
	}
	body, done, err := cl.SubmitReviewSubmission(sub.ID)
	if err != nil {
		ascLog("Submission not accepted yet: %v", err)
		ascPrintAssociated(body)
		ascLog("STAGED: %s", ascStagedHint)
		return false, nil
	}
	ascLog("  ✓ SUBMITTED for App Store review (state: %s)", done.State)
	return true, nil
}

// ascPrintAssociated prints Apple's meta.associatedErrors verbatim. This is the
// ONLY place Apple names the specific metadata field blocking a submission.
func ascPrintAssociated(body []byte) {
	assoc := ascAssociatedErrors(body)
	if len(assoc) == 0 {
		return
	}
	ascLog("Apple reported these specific problems (verbatim):")
	for _, a := range assoc {
		ascLog("  • %s", a.String())
	}
}

// CreateVersion creates a new App Store version for a platform.
func (a *ascClient) CreateVersion(appID, platform, versionString string) (*ASCVersion, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "appStoreVersions",
			"attributes": map[string]interface{}{
				"platform":      platform,
				"versionString": versionString,
			},
			"relationships": map[string]interface{}{
				"app": map[string]interface{}{
					"data": map[string]string{"type": "apps", "id": appID},
				},
			},
		},
	}
	out, _, err := a.do("POST", "/appStoreVersions", body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			ID         string     `json:"id"`
			Attributes ASCVersion `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	v := r.Data.Attributes
	v.ID = r.Data.ID
	if v.VersionString == "" {
		v.VersionString = versionString
	}
	v.Editable = true
	return &v, nil
}

// ascBindLatestBuild attaches the newest processed build to the version when one
// is not already attached. Best-effort: no build yet is a normal state (the
// binary may still be processing), not a submission failure.
func ascBindLatestBuild(cl *ascClient, appID, platform string, v *ASCVersion) error {
	if b, err := cl.AttachedBuild(v.ID); err == nil && b != nil {
		ascLog("  Build already bound: %s (%s)", b.Version, b.ProcessingState)
		return nil
	}
	builds, err := cl.BuildsForPlatform(appID, platform, "", "")
	if err != nil {
		return fmt.Errorf("could not list builds: %w", err)
	}
	for _, b := range builds {
		if b.Expired || (b.ProcessingState != "" && b.ProcessingState != "VALID") {
			continue
		}
		if err := cl.AttachBuild(v.ID, b.ID); err != nil {
			return fmt.Errorf("could not bind build %s: %w", b.ID, err)
		}
		ascLog("  Build bound: %s [%s]", b.Version, b.ID)
		return nil
	}
	return fmt.Errorf("no VALID build yet (still processing?) — submission may need to wait")
}
