package main

// publish_status.go — ONE "are you ready to ship?" report. Aggregates every
// publish check (permissions, listing identity, privacy, assets, store auth,
// console forms) into a single readiness verdict the normie (and the Publish
// UI) reads at a glance. The pure aggregation (assemblePublishReadiness) is
// unit-tested; the wrapper gathers the filesystem/vault inputs.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

type CheckStatus struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Blocker bool   `json:"blocker"` // a failed blocker ⇒ not ready to submit
	Detail  string `json:"detail"`
}

type PublishReadiness struct {
	Checks   []CheckStatus `json:"checks"`
	Ready    bool          `json:"ready"`
	Blockers []string      `json:"blockers"`
}

// assemblePublishReadiness is the pure verdict from gathered inputs.
func assemblePublishReadiness(iosGaps, androidGaps []string, l StoreListing, missingAssets []string, appleAuth, googleAuth bool) PublishReadiness {
	var r PublishReadiness
	add := func(name string, ok, blocker bool, detail string) {
		r.Checks = append(r.Checks, CheckStatus{Name: name, OK: ok, Blocker: blocker, Detail: detail})
		if blocker && !ok {
			r.Blockers = append(r.Blockers, name+": "+detail)
		}
	}

	// Permissions — missing iOS usage strings crash on launch ⇒ blocker.
	if len(iosGaps) > 0 {
		add("permissions", false, true, "missing iOS usage strings: "+strings.Join(iosGaps, ", ")+" (yaver caps generate --write)")
	} else if len(androidGaps) > 0 {
		add("permissions", false, false, "missing Android permissions: "+strings.Join(androidGaps, ", ")+" (yaver caps generate --write)")
	} else {
		add("permissions", true, true, "all declared")
	}

	// Listing identity.
	idOK := l.AppName != "" && l.BundleID != "" && l.PackageName != ""
	add("listing-identity", idOK, true, fmt.Sprintf("name=%s ios=%s android=%s", dashIfEmpty(l.AppName), dashIfEmpty(l.BundleID), dashIfEmpty(l.PackageName)))

	// Privacy is informational (truthfully derived; never blocks).
	add("privacy", true, false, fmt.Sprintf("%d data type(s) declared", len(l.Privacy)))

	// Assets — required screenshots present ⇒ blocker for a store submission.
	if len(missingAssets) > 0 {
		add("assets", false, true, "missing: "+strings.Join(missingAssets, ", ")+" (yaver assets capture)")
	} else {
		add("assets", true, true, "all required sizes present")
	}

	// Store auth — needed only for live push, not a submission blocker.
	add("store-auth-apple", appleAuth, false, authDetail(appleAuth, "ASC key in vault", "add via yaver stores apple-asc-key"))
	add("store-auth-google", googleAuth, false, authDetail(googleAuth, "service account in vault", "add via yaver stores google-service-account"))

	// Console forms the human must submit (informational).
	if len(l.ConsoleForms) > 0 {
		add("console-forms", false, false, fmt.Sprintf("%d to submit in the store console", len(l.ConsoleForms)))
	} else {
		add("console-forms", true, false, "none")
	}

	r.Ready = len(r.Blockers) == 0
	return r
}

func authDetail(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

// buildPublishReadiness gathers the inputs (gaps, listing, assets, vault auth)
// and returns the verdict. assetsDir defaults to the asset generator's output.
func buildPublishReadiness(path, assetsDir string) PublishReadiness {
	if assetsDir == "" {
		assetsDir = "yaver-store-assets"
	}
	listing := BuildStoreListing(path)
	iosGaps, andGaps, _ := manifestGaps(resolveProjectDirOr(path))

	// Required assets present?
	var missing []string
	for _, tg := range buildCapturePlan(listing, assetsDir) {
		if tg.MinCount <= 0 {
			continue
		}
		if _, err := os.Stat(tg.OutFile); err != nil {
			missing = append(missing, tg.DeviceClass)
		}
	}

	// Store auth in vault (best-effort).
	have := vaultSecretNames()
	appleAuth := have["APP_STORE_KEY_PATH"] && have["APP_STORE_KEY_ID"] && have["APP_STORE_KEY_ISSUER"]
	googleAuth := have["PLAY_STORE_KEY_FILE"]

	return assemblePublishReadiness(iosGaps, andGaps, listing, missing, appleAuth, googleAuth)
}

// resolveProjectDirOr uses an explicit path when given, else the workspace/cwd
// resolver. (manifestGaps needs an actual directory.)
func resolveProjectDirOr(path string) string {
	if path != "" && path != "." {
		return path
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return path
}

func runListingStatus(args []string) {
	path := "."
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--path":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case "--json":
			jsonOut = true
		}
	}
	r := buildPublishReadiness(path, "")
	if jsonOut {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
		return
	}
	if r.Ready {
		fmt.Println("✓ Ready to submit — all blockers clear.")
	} else {
		fmt.Printf("✗ Not ready — %d blocker(s):\n", len(r.Blockers))
	}
	fmt.Println()
	for _, c := range r.Checks {
		glyph := "✓"
		if !c.OK {
			if c.Blocker {
				glyph = "✗"
			} else {
				glyph = "○"
			}
		}
		fmt.Printf("  %s %-18s %s\n", glyph, c.Name, c.Detail)
	}
}

func (s *HTTPServer) handlePublishStatus(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	writeJSON(w, http.StatusOK, buildPublishReadiness(path, ""))
}
