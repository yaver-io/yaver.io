package main

// ops_store.go — first-class MCP ops verbs for managing app-store TESTERS,
// GROUPS, BUILDS and RELEASES on behalf of third-party developers, for both
// Apple (App Store Connect / TestFlight) and Google (Play). Registered ops
// verbs are automatically exposed through the `ops` MCP grand-tool, and the
// same handlers back the web + mobile UI over /ops.
//
// Multi-tenant: every verb takes a `project` (the customer's project slug).
// Credentials are read from that project's vault scope (appstoreconnect.go /
// playpublish_api.go), so a managed-cloud box can manage dev B's app with dev
// B's keys without ever touching Yaver's own. Owner-only (no guest).
//
// Asymmetry to remember: Apple's API fully manages individual beta testers;
// Google's API manages a track's *Google Groups* and release rollout, NOT the
// per-email internal tester list (Console-only). The google verbs say so.

import (
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "store_credentials_status",
		Description: "Report which app-store API credentials are configured for a project (apple App Store Connect key, google Play service account). Reveals presence only, never values.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"project": map[string]interface{}{"type": "string", "description": "Project slug whose vault holds the store credentials. Omit for the global/default project."},
		}),
		Handler:    storeCredentialsStatusHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_group_list",
		Description: "List beta/tester groups. apple: TestFlight beta groups (internal+external) for the app. google: the Google Groups bound to a Play testing track.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"store":       map[string]interface{}{"type": "string", "description": "apple | google"},
			"project":     map[string]interface{}{"type": "string"},
			"bundleId":    map[string]interface{}{"type": "string", "description": "apple: the app's bundle id (e.g. com.acme.app)"},
			"packageName": map[string]interface{}{"type": "string", "description": "google: the app's package name (e.g. com.acme.app)"},
			"track":       map[string]interface{}{"type": "string", "description": "google: testing track (default internal)"},
		}, "store"),
		Handler:    storeGroupListHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_group_create",
		Description: "apple: create a TestFlight beta group (optionally with a public TestFlight link). google: not applicable — Play uses external Google Groups; bind one with store_tester_invite.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"store":      map[string]interface{}{"type": "string", "description": "apple"},
			"project":    map[string]interface{}{"type": "string"},
			"bundleId":   map[string]interface{}{"type": "string"},
			"name":       map[string]interface{}{"type": "string", "description": "group name"},
			"publicLink": map[string]interface{}{"type": "boolean", "description": "apple: enable a public TestFlight join link"},
		}, "store", "name"),
		Handler:    storeGroupCreateHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_tester_list",
		Description: "List testers. apple: beta testers for the app (with state + groups). google: the track's bound Google Groups (Play has no per-email tester list over the API).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"store":       map[string]interface{}{"type": "string", "description": "apple | google"},
			"project":     map[string]interface{}{"type": "string"},
			"bundleId":    map[string]interface{}{"type": "string"},
			"packageName": map[string]interface{}{"type": "string"},
			"track":       map[string]interface{}{"type": "string", "description": "google: default internal"},
		}, "store"),
		Handler:    storeTesterListHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_tester_invite",
		Description: "Add a tester. apple: create a beta tester + add to a group (Apple emails the invite). google: bind a Google Group (groupEmail) to the track so its members become testers — individual emails must be added in the Group (Workspace), not via this API.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"store":       map[string]interface{}{"type": "string", "description": "apple | google"},
			"project":     map[string]interface{}{"type": "string"},
			"bundleId":    map[string]interface{}{"type": "string", "description": "apple"},
			"packageName": map[string]interface{}{"type": "string", "description": "google"},
			"email":       map[string]interface{}{"type": "string", "description": "apple: tester email to invite"},
			"firstName":   map[string]interface{}{"type": "string"},
			"lastName":    map[string]interface{}{"type": "string"},
			"group":       map[string]interface{}{"type": "string", "description": "apple: beta group name or id (default: first internal group)"},
			"groupEmail":  map[string]interface{}{"type": "string", "description": "google: a Google Group email to bind to the track"},
			"track":       map[string]interface{}{"type": "string", "description": "google: default internal"},
		}, "store"),
		Handler:    storeTesterInviteHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_tester_remove",
		Description: "Remove a tester. apple: delete the beta tester by email (revokes access). google: unbind a Google Group from the track (groupEmail).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"store":       map[string]interface{}{"type": "string"},
			"project":     map[string]interface{}{"type": "string"},
			"bundleId":    map[string]interface{}{"type": "string"},
			"packageName": map[string]interface{}{"type": "string"},
			"email":       map[string]interface{}{"type": "string", "description": "apple"},
			"groupEmail":  map[string]interface{}{"type": "string", "description": "google"},
			"track":       map[string]interface{}{"type": "string"},
		}, "store"),
		Handler:    storeTesterRemoveHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_build_list",
		Description: "List recent builds. apple: TestFlight builds (version, processing state, expiry). google: the testing track's releases (version codes + rollout status).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"store":       map[string]interface{}{"type": "string"},
			"project":     map[string]interface{}{"type": "string"},
			"bundleId":    map[string]interface{}{"type": "string"},
			"packageName": map[string]interface{}{"type": "string"},
			"track":       map[string]interface{}{"type": "string", "description": "google: default internal"},
		}, "store"),
		Handler:    storeBuildListHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "store_release_promote",
		Description: "Make a build reach testers. apple: assign the latest build to a beta group. google: roll a track's draft release out (status=completed) or stage it (status=inProgress + userFraction).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"store":        map[string]interface{}{"type": "string"},
			"project":      map[string]interface{}{"type": "string"},
			"bundleId":     map[string]interface{}{"type": "string", "description": "apple"},
			"packageName":  map[string]interface{}{"type": "string", "description": "google"},
			"group":        map[string]interface{}{"type": "string", "description": "apple: beta group name/id to assign the latest build to"},
			"track":        map[string]interface{}{"type": "string", "description": "google: default internal"},
			"status":       map[string]interface{}{"type": "string", "description": "google: completed | inProgress | halted | draft (default completed)"},
			"userFraction": map[string]interface{}{"type": "number", "description": "google: 0..1 staged rollout fraction (only for inProgress)"},
		}, "store"),
		Handler:    storeReleasePromoteHandler,
		AllowGuest: false,
	})
}

// storeArgs is the union of all verb payloads (parsed once per handler).
type storeArgs struct {
	Store        string  `json:"store"`
	Project      string  `json:"project"`
	BundleID     string  `json:"bundleId"`
	PackageName  string  `json:"packageName"`
	Track        string  `json:"track"`
	Email        string  `json:"email"`
	FirstName    string  `json:"firstName"`
	LastName     string  `json:"lastName"`
	Name         string  `json:"name"`
	Group        string  `json:"group"`
	GroupEmail   string  `json:"groupEmail"`
	PublicLink   bool    `json:"publicLink"`
	Status       string  `json:"status"`
	UserFraction float64 `json:"userFraction"`
}

func parseStoreArgs(payload json.RawMessage) (storeArgs, *OpsResult) {
	var a storeArgs
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &a); err != nil {
			return a, &OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	a.Store = strings.ToLower(strings.TrimSpace(a.Store))
	if a.Track == "" {
		a.Track = "internal"
	}
	return a, nil
}

func badStore(a storeArgs) *OpsResult {
	if a.Store != "apple" && a.Store != "google" {
		return &OpsResult{OK: false, Code: "bad_payload", Error: "store must be 'apple' or 'google'"}
	}
	return nil
}

func storeCredentialsStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseStoreArgs(payload)
	if bad != nil {
		return *bad
	}
	res := map[string]interface{}{"project": a.Project}
	if _, err := resolveAppleASCCreds(a.Project); err != nil {
		res["apple"] = map[string]interface{}{"configured": false, "detail": err.Error()}
	} else {
		res["apple"] = map[string]interface{}{"configured": true}
	}
	if _, err := resolveGoogleSA(a.Project); err != nil {
		res["google"] = map[string]interface{}{"configured": false, "detail": err.Error()}
	} else {
		res["google"] = map[string]interface{}{"configured": true}
	}
	return OpsResult{OK: true, Initial: res}
}

// resolveAppleApp builds an ASC client and resolves the app id from bundle id.
func resolveAppleApp(a storeArgs) (*ascClient, *ASCApp, error) {
	if strings.TrimSpace(a.BundleID) == "" {
		return nil, nil, fmt.Errorf("bundleId required for apple")
	}
	cl, err := newASCClient(a.Project)
	if err != nil {
		return nil, nil, err
	}
	app, err := cl.AppByBundleID(a.BundleID)
	if err != nil {
		return nil, nil, err
	}
	return cl, app, nil
}

func storeGroupListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseStoreArgs(payload)
	if bad != nil {
		return *bad
	}
	if b := badStore(a); b != nil {
		return *b
	}
	switch a.Store {
	case "apple":
		cl, app, err := resolveAppleApp(a)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		groups, err := cl.ListBetaGroups(app.ID)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "apple", "app": app, "groups": groups}}
	case "google":
		if a.PackageName == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "packageName required for google"}
		}
		cl, err := newPlayClient(a.Project, a.PackageName)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		t, err := cl.GetTesters(a.Track)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "google", "track": a.Track, "googleGroups": t.GoogleGroups}}
	}
	return OpsResult{OK: false, Code: "bad_payload", Error: "unknown store"}
}

func storeGroupCreateHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseStoreArgs(payload)
	if bad != nil {
		return *bad
	}
	if a.Store != "apple" {
		return OpsResult{OK: false, Code: "unsupported", Error: "group creation is apple-only; Play uses external Google Groups — bind one with store_tester_invite (groupEmail)"}
	}
	if strings.TrimSpace(a.Name) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "name required"}
	}
	cl, app, err := resolveAppleApp(a)
	if err != nil {
		return OpsResult{OK: false, Error: err.Error()}
	}
	g, err := cl.CreateBetaGroup(app.ID, a.Name, a.PublicLink)
	if err != nil {
		return OpsResult{OK: false, Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"store": "apple", "group": g}}
}

func storeTesterListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseStoreArgs(payload)
	if bad != nil {
		return *bad
	}
	if b := badStore(a); b != nil {
		return *b
	}
	switch a.Store {
	case "apple":
		cl, app, err := resolveAppleApp(a)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		testers, err := cl.ListBetaTesters(app.ID)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "apple", "app": app, "testers": testers}}
	case "google":
		if a.PackageName == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "packageName required for google"}
		}
		cl, err := newPlayClient(a.Project, a.PackageName)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		t, err := cl.GetTesters(a.Track)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"store":        "google",
			"track":        a.Track,
			"googleGroups": t.GoogleGroups,
			"note":         "Play's API exposes only the track's Google Groups, not individual internal testers. Manage per-email testers in the Play Console, or add members to a bound Google Group.",
		}}
	}
	return OpsResult{OK: false, Code: "bad_payload", Error: "unknown store"}
}

// resolveAppleGroupID matches a group by id or name; defaults to first internal.
func resolveAppleGroupID(cl *ascClient, appID, ref string) (string, error) {
	groups, err := cl.ListBetaGroups(appID)
	if err != nil {
		return "", err
	}
	if len(groups) == 0 {
		return "", fmt.Errorf("no beta groups exist; create one with store_group_create")
	}
	ref = strings.TrimSpace(ref)
	if ref != "" {
		for _, g := range groups {
			if g.ID == ref || strings.EqualFold(g.Name, ref) {
				return g.ID, nil
			}
		}
		return "", fmt.Errorf("beta group %q not found", ref)
	}
	for _, g := range groups {
		if g.IsInternal {
			return g.ID, nil
		}
	}
	return groups[0].ID, nil
}

func storeTesterInviteHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseStoreArgs(payload)
	if bad != nil {
		return *bad
	}
	if b := badStore(a); b != nil {
		return *b
	}
	switch a.Store {
	case "apple":
		if strings.TrimSpace(a.Email) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "email required for apple"}
		}
		cl, app, err := resolveAppleApp(a)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		gid, err := resolveAppleGroupID(cl, app.ID, a.Group)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		t, err := cl.InviteBetaTester(gid, a.Email, a.FirstName, a.LastName)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "apple", "tester": t, "groupId": gid}}
	case "google":
		if a.PackageName == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "packageName required for google"}
		}
		if strings.TrimSpace(a.GroupEmail) == "" {
			return OpsResult{OK: false, Code: "unsupported", Error: "Play's API can't add an individual email tester. Provide groupEmail (a Google Group) to bind to the track; add the person to that Group in Workspace, or add their email in the Play Console internal-testing list."}
		}
		cl, err := newPlayClient(a.Project, a.PackageName)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		cur, err := cl.GetTesters(a.Track)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		merged := mergeUnique(cur.GoogleGroups, a.GroupEmail)
		t, err := cl.SetTesters(a.Track, merged)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "google", "track": a.Track, "googleGroups": t.GoogleGroups}}
	}
	return OpsResult{OK: false, Code: "bad_payload", Error: "unknown store"}
}

func storeTesterRemoveHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseStoreArgs(payload)
	if bad != nil {
		return *bad
	}
	if b := badStore(a); b != nil {
		return *b
	}
	switch a.Store {
	case "apple":
		if strings.TrimSpace(a.Email) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "email required for apple"}
		}
		cl, app, err := resolveAppleApp(a)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		testers, err := cl.ListBetaTesters(app.ID)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		var id string
		for _, t := range testers {
			if strings.EqualFold(t.Email, a.Email) {
				id = t.ID
				break
			}
		}
		if id == "" {
			return OpsResult{OK: false, Code: "not_found", Error: "tester " + a.Email + " not found"}
		}
		if err := cl.RemoveBetaTester(id); err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "apple", "removed": a.Email}}
	case "google":
		if a.PackageName == "" || strings.TrimSpace(a.GroupEmail) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "packageName and groupEmail required for google"}
		}
		cl, err := newPlayClient(a.Project, a.PackageName)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		cur, err := cl.GetTesters(a.Track)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		remaining := removeValue(cur.GoogleGroups, a.GroupEmail)
		t, err := cl.SetTesters(a.Track, remaining)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "google", "track": a.Track, "googleGroups": t.GoogleGroups}}
	}
	return OpsResult{OK: false, Code: "bad_payload", Error: "unknown store"}
}

func storeBuildListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseStoreArgs(payload)
	if bad != nil {
		return *bad
	}
	if b := badStore(a); b != nil {
		return *b
	}
	switch a.Store {
	case "apple":
		cl, app, err := resolveAppleApp(a)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		builds, err := cl.ListBuilds(app.ID)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "apple", "app": app, "builds": builds}}
	case "google":
		if a.PackageName == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "packageName required for google"}
		}
		cl, err := newPlayClient(a.Project, a.PackageName)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		t, err := cl.GetTrack(a.Track)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "google", "track": a.Track, "releases": t.Releases}}
	}
	return OpsResult{OK: false, Code: "bad_payload", Error: "unknown store"}
}

func storeReleasePromoteHandler(c OpsContext, payload json.RawMessage) OpsResult {
	a, bad := parseStoreArgs(payload)
	if bad != nil {
		return *bad
	}
	if b := badStore(a); b != nil {
		return *b
	}
	switch a.Store {
	case "apple":
		cl, app, err := resolveAppleApp(a)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		builds, err := cl.ListBuilds(app.ID)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		if len(builds) == 0 {
			return OpsResult{OK: false, Code: "not_found", Error: "no builds to assign"}
		}
		gid, err := resolveAppleGroupID(cl, app.ID, a.Group)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		if err := cl.AssignBuildToGroup(gid, builds[0].ID); err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "apple", "assignedBuild": builds[0], "groupId": gid}}
	case "google":
		if a.PackageName == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "packageName required for google"}
		}
		status := a.Status
		if status == "" {
			status = "completed"
		}
		cl, err := newPlayClient(a.Project, a.PackageName)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		t, err := cl.PromoteRelease(a.Track, status, a.UserFraction)
		if err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"store": "google", "track": a.Track, "track_state": t}}
	}
	return OpsResult{OK: false, Code: "bad_payload", Error: "unknown store"}
}

func mergeUnique(list []string, add string) []string {
	for _, v := range list {
		if strings.EqualFold(v, add) {
			return list
		}
	}
	return append(append([]string{}, list...), add)
}

func removeValue(list []string, rm string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if !strings.EqualFold(v, rm) {
			out = append(out, v)
		}
	}
	return out
}
