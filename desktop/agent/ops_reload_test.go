package main

// ops_reload_test.go — the `reload` verb is a thin shim over two dev handlers
// it calls in-process with a forged *http.Request. Everything that can go wrong
// here is a wiring bug that a type-checker cannot see: a field renamed across
// the boundary, a handler swapped for a lower-level method that skips the
// broadcast, or an auth header that never gets carried inward. Each test below
// pins one of those.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guestHeaders builds the header set the auth middleware stamps onto an
// authenticated guest request, which is what OpsContext.RequestHeaders carries.
func guestHeaders(userID string) http.Header {
	h := http.Header{}
	h.Set("X-Yaver-Guest", "true")
	h.Set("X-Yaver-GuestUserID", userID)
	h.Set("X-Yaver-GuestScope", string(GuestScopeFull))
	return h
}

// mode=dev must go through /dev/reload — NOT devServerMgr.Reload().
//
// The two are not equivalent and the difference is invisible to the caller:
// Reload() pokes the bundler's own HTTP endpoint, while handleDevServerReload
// ALSO broadcasts the BlackBox "reload" command that phones, simulators, and
// the preview worker actually listen for. A verb wired to the former succeeds
// locally and reloads nothing, and the human is told it worked.
//
// `deliveredTo` is the tell: it is computed only on the handler path, so its
// presence in Initial proves the delegation is real. The pre-fix handler
// returned {mode, framework, reloaded} and no listener count at all.
func TestOpsReloadDevDelegatesToDevReloadHandler(t *testing.T) {
	fx := startGuestShareFixture(t, false)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	fx.server.devServerMgr = &DevServerManager{
		active: &devServerSession{
			server: &guestShareStubDevServer{status: DevServerStatus{
				Framework: "expo",
				Running:   true,
				WorkDir:   fx.sfmgDir,
				Port:      8081,
			}},
		},
	}

	// Owner call: no guest headers.
	res := opsReloadHandler(
		OpsContext{Ctx: context.Background(), Server: fx.server},
		json.RawMessage(`{"mode":"dev"}`),
	)
	if !res.OK {
		t.Fatalf("reload mode=dev failed: code=%s err=%s", res.Code, res.Error)
	}
	initial, ok := res.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("Initial is %T, want map[string]interface{}", res.Initial)
	}
	if _, ok := initial["deliveredTo"]; !ok {
		t.Fatalf("Initial has no deliveredTo — mode=dev did not go through /dev/reload, "+
			"so the BlackBox reload command was never broadcast. Initial=%#v", initial)
	}
	if got := initial["mode"]; got != "dev" {
		t.Fatalf("mode = %v, want dev", got)
	}
}

// mode=bundle must send `projectPath` — the field /dev/reload-app reads.
//
// It used to send `workDir`, which handleReloadApp does not decode, so the
// project silently resolved to "" and the Hermes push either rebuilt the wrong
// project or died with PROJECT_REQUIRED. With no dev server running there is no
// fallback to mask it, which is exactly the state a headset/TV surface is in.
//
// The discriminator is which error the inner /dev/build-native returns:
//   - path forwarded  -> gets past the project gate, then rejects the temp dir
//     as not-a-React-Native-project (FRAMEWORK_NOT_SUPPORTED) and echoes the path
//   - path dropped    -> never had a project at all (PROJECT_REQUIRED)
func TestOpsReloadBundleSendsProjectPathNotWorkDir(t *testing.T) {
	fx := startGuestShareFixture(t, false)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	// No active dev server: nothing to fall back to, so the only way the inner
	// build can learn the project is the field we send.
	fx.server.devServerMgr = &DevServerManager{}
	bb, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	fx.server.blackboxMgr = bb

	// A real directory that is deliberately not an RN/Expo project.
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "README.md"), []byte("not rn\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"mode": "bundle", "workDir": projectDir})
	res := opsReloadHandler(
		OpsContext{Ctx: context.Background(), Server: fx.server},
		payload,
	)

	if strings.Contains(res.Error, "PROJECT_REQUIRED") {
		t.Fatalf("reload-app never received the project — the bundle path is being sent under the "+
			"wrong field name (want projectPath). err=%s", res.Error)
	}
	if !strings.Contains(res.Error, projectDir) {
		t.Fatalf("expected the resolved project path to reach /dev/build-native and be echoed back, "+
			"got err=%s", res.Error)
	}
}

// A bundle push must report how many listeners actually got the bundle.
//
// /dev/reload has always returned deliveredTo; the bundle path threw the
// broadcast's count away and forwarded the build response verbatim, so
// deliveredTo was absent and no caller could tell "pushed to your phone" from
// "pushed to nobody". The headset's Hermes Push button reported success either
// way. withDeliveredTo is what closes that: it must add the count WITHOUT
// dropping any field the SDK/CLI already read off the build response.
func TestWithDeliveredToMergesWithoutLosingBuildFields(t *testing.T) {
	build := []byte(`{"ok":true,"bundleUrl":"http://x/main.jsbundle","platform":"ios"}`)

	var got map[string]interface{}
	if err := json.Unmarshal(withDeliveredTo(build, 0), &got); err != nil {
		t.Fatalf("merged body is not valid JSON: %v", err)
	}
	if got["deliveredTo"] != float64(0) {
		t.Fatalf("deliveredTo = %v, want 0 — a build that reached no listener must say so", got["deliveredTo"])
	}
	for _, key := range []string{"ok", "bundleUrl", "platform"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("merge dropped %q — existing consumers read this field off the build response", key)
		}
	}

	// Decoration must never be the reason a response fails to send.
	raw := []byte("not json at all")
	if string(withDeliveredTo(raw, 3)) != string(raw) {
		t.Fatalf("non-JSON body should pass through untouched")
	}
}

// If a guest ever reaches this handler, it must respect their project share.
//
// requireGuestAccessToActiveDevServer enforces that on /dev/reload, but it
// authorizes by header (X-Yaver-GuestUserID) and reads "" as "the owner is
// calling". The verb reaches that handler through a request IT forges, so
// unless the caller's identity is carried inward, the gate sees an owner and
// waves the guest through.
//
// This calls the handler DIRECTLY, below the /ops authorization layer, and
// that is deliberate: it pins the handler's own guarantee. In production
// dispatchOps refuses a guest before this point (see the HTTP test below), so
// this is the second lock, not the first. It matters because `reload` is
// declared AllowGuest:true — the verb intends to be guest-reachable, and if it
// is ever added to the deploy scope's verb allow-list the first lock opens.
// The project gate has to already be standing when that happens.
func TestOpsReloadGuestCannotReloadUnsharedActiveProject(t *testing.T) {
	fx := startGuestShareFixture(t, false)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	// guest-user is scoped to sfmg. The live dev server is talos.
	fx.server.devServerMgr = &DevServerManager{
		active: &devServerSession{
			server: &guestShareStubDevServer{status: DevServerStatus{
				Framework: "vite",
				Running:   true,
				WorkDir:   fx.talosDir,
				Port:      5173,
			}},
		},
	}

	res := opsReloadHandler(
		OpsContext{
			Ctx:            context.Background(),
			Server:         fx.server,
			Caller:         "guest",
			ActorUserID:    "guest-user",
			Scope:          string(GuestScopeFull),
			RequestHeaders: guestHeaders("guest-user"),
		},
		json.RawMessage(`{"mode":"dev"}`),
	)

	if res.OK {
		t.Fatalf("guest reloaded a project outside their share via ops reload — the guest identity " +
			"is not reaching /dev/reload's access gate")
	}
	if !strings.Contains(res.Error, "guest cannot access the active dev server project") {
		t.Fatalf("expected the dev-server guest-access refusal, got code=%s err=%s", res.Code, res.Error)
	}
}

// No guest gets to run `reload` over the live /ops route. This is the FIRST
// lock — the one that actually holds today — and it is the reason the
// handler-level tests above are defence in depth rather than a patched breach.
//
// The guest here is DEPLOY-scoped because that is the closest any guest gets:
// /ops is off the path allow-list for full / feedback-only / sdk-project /
// support guests entirely, and the capability scopes that do reach it (circuit,
// stream) are pinned to their own verb families. `deploy` reaches /ops — and is
// then held at the verb allow-list (info/status/deploy), which does not include
// reload. That is easy to widen by accident, so pin it.
//
// Note the assertion is on the BODY, not the status: /ops answers typed
// refusals as HTTP 200 with {ok:false}, so a status-only check here would pass
// against a completely wide-open agent.
func TestOpsReloadOverHTTPGuestIsRefusedTheReloadVerb(t *testing.T) {
	fx := startGuestShareFixture(t, false)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	// Re-grant the fixture's guest as deploy-scoped: still pinned to sfmg.
	shareAllDevices := false
	fx.server.guestConfigMgr.UpdateConfigs([]GuestConfig{{
		GuestUserID:     "guest-user",
		GuestEmail:      "guest@example.com",
		GuestName:       "Guest User",
		Scope:           GuestScopeDeploy,
		AllowedProjects: []string{"sfmg"},
		ShareAllDevices: &shareAllDevices,
		DeviceIDs:       []string{"hetzner-talos-1"},
	}})
	fx.server.guestConfigMgr.SetProjectAccess("guest-user", []string{"sfmg"})

	fx.server.devServerMgr = &DevServerManager{
		active: &devServerSession{
			server: &guestShareStubDevServer{status: DevServerStatus{
				Framework: "vite",
				Running:   true,
				WorkDir:   fx.talosDir, // guest-user is scoped to sfmg, not talos
				Port:      5173,
			}},
		},
	}

	body, _ := json.Marshal(map[string]interface{}{
		"verb":    "reload",
		"machine": "local",
		"payload": map[string]string{"mode": "dev"},
	})
	req, err := http.NewRequest(http.MethodPost, fx.baseURL+"/ops", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+fx.guestToken)
	req.Header.Set("Content-Type", "application/json")
	// A hostile client trying to shed its guest identity: the middleware must
	// strip this, not trust it.
	req.Header.Set("X-Yaver-GuestUserID", "")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /ops: %v", err)
	}
	defer resp.Body.Close()

	var out struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode /ops response: %v", err)
	}
	if out.OK {
		t.Fatalf("a guest ran the reload verb through the live /ops route — the ops " +
			"authorization layer is no longer holding")
	}
	if out.Code != "unauthorized" {
		t.Fatalf("expected an authorization refusal, got code=%q err=%q", out.Code, out.Error)
	}
}

// A guest configured to require isolation must not be able to run a native
// bundle build on the host, which is what mode=bundle does.
//
// isolatedGuestDevMutationBlocked is the gate, and like the one above it keys
// off the guest header — so the same forged-request gap would hand an isolated
// guest a host-side Hermes build. Asserting on the message matters: without the
// identity forwarded the call still fails, but for the unrelated reason that no
// SDK device is connected, which would let a broken gate look like a passing test.
func TestOpsReloadIsolatedGuestCannotBuildBundle(t *testing.T) {
	fx := startGuestShareFixture(t, true) // requireIsolation
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	fx.server.devServerMgr = &DevServerManager{}

	payload, _ := json.Marshal(map[string]string{"mode": "bundle", "workDir": fx.sfmgDir})
	res := opsReloadHandler(
		OpsContext{
			Ctx:            context.Background(),
			Server:         fx.server,
			Caller:         "guest",
			ActorUserID:    "guest-user",
			Scope:          string(GuestScopeFull),
			RequestHeaders: guestHeaders("guest-user"),
		},
		payload,
	)

	if res.OK {
		t.Fatalf("isolation-required guest ran a host native bundle build via ops reload")
	}
	if !strings.Contains(res.Error, "isolation") {
		t.Fatalf("expected the isolation refusal to be the reason this failed, got code=%s err=%s",
			res.Code, res.Error)
	}
}
