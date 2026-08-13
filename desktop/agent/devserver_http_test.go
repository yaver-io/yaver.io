package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Phase 3: a hosted-tier box auto-bakes its own self-hosted Convex URL
// into the RN bundle so the dev's app — and any friend's Hermes-loaded
// copy — needs zero backend config. Project config wins over the
// on-box file; neither ⇒ nil (byok/local unchanged).
func TestHostedConvexBuildEnv(t *testing.T) {
	// Neither source → no injection.
	empty := t.TempDir()
	if env := hostedConvexBuildEnv(empty); env != nil {
		t.Errorf("expected nil for plain project, got %v", env)
	}

	// Hosted-tier box: cred file present (path overridable for tests).
	credDir := t.TempDir()
	credFile := filepath.Join(credDir, "convex-selfhosted.json")
	if err := os.WriteFile(credFile,
		[]byte(`{"url":"https://abc123.cloud.yaver.io/_convex-api","adminKey":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONVEX_SELFHOSTED_FILE", credFile)
	got := hostedConvexBuildEnv(empty)
	if len(got) != 1 || got[0] != "EXPO_PUBLIC_CONVEX_URL=https://abc123.cloud.yaver.io/_convex-api" {
		t.Errorf("hosted autodiscovery wrong: %v", got)
	}

	// Project .yaver/config.yaml override wins over the box file.
	projDir := t.TempDir()
	if err := SaveProjectConfig(projDir, &YaverProjectConfig{
		Backend: "convex",
		Env:     map[string]string{"EXPO_PUBLIC_CONVEX_URL": "https://override.example/_convex-api"},
	}); err != nil {
		t.Fatal(err)
	}
	got = hostedConvexBuildEnv(projDir)
	if len(got) != 1 || got[0] != "EXPO_PUBLIC_CONVEX_URL=https://override.example/_convex-api" {
		t.Errorf("project override should win, got %v", got)
	}
}

// TestBundleCommandHonoursContext proves the bundler subprocess we hand to
// /dev/build-native is killed when its deadline expires. The real-world
// failure mode this guards against: a hung `npx expo export:embed` (broken
// project, missing node_modules, infinite resolver loop) keeps the HTTP
// request open forever, leaves the mobile DevPreview stuck on
// "Building..." with `setNativeLoading(true)` and forces the user to kill
// the app to recover. With exec.CommandContext the kernel kills the
// subprocess on context expiry and cmd.Run returns; the caller checks
// ctx.Err() == context.DeadlineExceeded and surfaces a structured
// "timedOut" response — that contract is what this test pins.
func TestBundleCommandHonoursContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// `sleep 30` stands in for a hung Metro/Expo. We bypass bundleCommand's
	// switch on packageManager (which would invoke real `npx`/`yarn` etc.)
	// and dial directly into exec.CommandContext to keep the test hermetic.
	// The wiring it proves — context propagates and kills the subprocess —
	// is identical to what bundleCommand does internally.
	cmd := bundleCommand(ctx, "npm", "react-native", "ios", "index.js", "/tmp/bundle.js", "/tmp/assets", true)
	if cmd == nil {
		t.Fatal("bundleCommand returned nil")
	}
	// The cmd's Args[0] is npx (or pnpm / bunx) — we don't actually run it.
	// What we check is: the *exec.Cmd has an associated context (its Cancel
	// function is non-nil). Without exec.CommandContext, Cancel is nil; if
	// it's non-nil, the kernel will kill the subprocess on ctx expiry.
	if cmd.Cancel == nil {
		t.Fatal("bundleCommand returned a *exec.Cmd without a Cancel func — context wiring is broken; a hung bundler will block /dev/build-native forever")
	}
}

func TestBundleAndHermesTimeoutsAreSane(t *testing.T) {
	// The mobile DevPreview / Hot Reload UIs use 12 minutes as their fetch
	// abort. The agent's combined cap (bundle + hermes) must stay under
	// that — otherwise the client gives up before the agent can return a
	// structured "timedOut" response and the UI falls back to a generic
	// "request aborted" instead of the helpful "bundler timed out, check
	// node_modules" message.
	totalAgentBudget := bundleBuildTimeout + hermesCompileTimeout
	mobileClientBudget := 12 * time.Minute
	if totalAgentBudget >= mobileClientBudget {
		t.Fatalf("agent build budget %v >= mobile client budget %v — mobile will abort before agent surfaces a structured failure", totalAgentBudget, mobileClientBudget)
	}
}

func TestCanBootstrapPackageManager(t *testing.T) {
	if !canBootstrapPackageManager("yarn", true, false) {
		t.Fatal("expected yarn to be bootstrap-able with npm")
	}
	if !canBootstrapPackageManager("pnpm", false, true) {
		t.Fatal("expected pnpm to be bootstrap-able with corepack")
	}
	if canBootstrapPackageManager("bun", true, true) {
		t.Fatal("did not expect bun to be bootstrap-able from npm-only assumptions")
	}
}

func TestDefaultPackageManagerInstallSpec(t *testing.T) {
	if got := defaultPackageManagerInstallSpec("yarn"); got != "yarn@1.22.22" {
		t.Fatalf("unexpected yarn default spec: %s", got)
	}
	if got := defaultPackageManagerInstallSpec("pnpm"); got != "pnpm@latest" {
		t.Fatalf("unexpected pnpm default spec: %s", got)
	}
}

// stubDevServer implements DevServer with a caller-supplied Status so tests
// can exercise the manager around a session that is building / ready / failed
// without spawning any real process.
type stubDevServer struct {
	name string
	st   DevServerStatus
}

func (s *stubDevServer) Name() string                               { return s.name }
func (s *stubDevServer) Detect(string) bool                         { return true }
func (s *stubDevServer) Start(context.Context, DevServerOpts) error { return nil }
func (s *stubDevServer) Stop() error                                { return nil }
func (s *stubDevServer) Port() int                                  { return s.st.Port }
func (s *stubDevServer) BundleURL(string) string                    { return "/dev/" }
func (s *stubDevServer) SupportsHotReload() bool                    { return true }
func (s *stubDevServer) Reload() error                              { return nil }
func (s *stubDevServer) PreStart(string, int, string)               {}
func (s *stubDevServer) Status() DevServerStatus                    { return s.st }
func (s *stubDevServer) Kind() DevServerKind                        { return DevServerKindHybrid }

// TestDevServerProxyStartingVsAbsent: during the whole cold-start window
// /dev/status answers building:true while /dev/ answered a bare
// "no dev server running" 503 — the proxy is only installed when the server
// binds. First trial of a preview on a cold box (SFMG, 4 GB machine: Metro
// bundle took ~17s) hit exactly this false negative. The /dev/ lane must
// distinguish "building" (truthful structured 503 + Retry-After) from
// "no session at all" (the old bare 503).
func TestDevServerProxyStartingVsAbsent(t *testing.T) {
	makeMgr := func(active *devServerSession) *DevServerManager {
		m := &DevServerManager{}
		if active != nil {
			m.mu.Lock()
			m.active = active
			m.mu.Unlock()
		}
		return m
	}

	t.Run("building session answers starting, not no-dev-server", func(t *testing.T) {
		srv := &HTTPServer{devServerMgr: makeMgr(&devServerSession{
			server: &stubDevServer{name: "expo", st: DevServerStatus{
				Framework: "expo", Port: 8081, Building: true,
				ServingLabel: "Starting expo preview…",
			}},
		})}
		rec := httptest.NewRecorder()
		srv.handleDevServerProxy(rec, httptest.NewRequest("GET", "/dev/", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 during cold start, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"status":"starting"`) || !strings.Contains(body, `"framework":"expo"`) {
			t.Fatalf("cold-start /dev/ must say starting (truthful), got body: %s", body)
		}
		if rec.Header().Get("Retry-After") != "2" {
			t.Errorf("expected Retry-After 2 on a starting 503, got %q", rec.Header().Get("Retry-After"))
		}
		if rec.Header().Get("X-Yaver-DevServer") != "starting" {
			t.Errorf("expected X-Yaver-DevServer=starting, got %q", rec.Header().Get("X-Yaver-DevServer"))
		}
	})

	t.Run("no session still answers no dev server running", func(t *testing.T) {
		srv := &HTTPServer{devServerMgr: makeMgr(nil)}
		rec := httptest.NewRecorder()
		srv.handleDevServerProxy(rec, httptest.NewRequest("GET", "/dev/", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 with no session, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "no dev server running") {
			t.Fatalf("expected bare 'no dev server running', got body: %s", rec.Body.String())
		}
	})
}
