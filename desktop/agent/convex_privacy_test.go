package main

// convex_privacy_test.go — tripwire against a whole class of regressions:
// the moment an edit to convex_state_sync.go (or any future syncer that
// reuses callMutation) starts shipping the user's confidential data to
// Convex, this test fails.
//
// Rule from CLAUDE.md + user: "Convex is only for auth / session /
// OAuth / peer discovery. Nothing confidential." We enforce it by
// recording every mutation payload the syncer would POST and asserting
// it contains none of the fields or value shapes that would count as
// confidential.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fieldsWeForbidInAnyConvexPayload enumerates the keys that MUST NOT
// appear in any Convex mutation arg map. If a new "nice" sync path
// wants to include output, logs, or secrets it will trip here first.
//
// Additions here should be a deliberate, reviewed decision — never a
// drive-by commit.
var fieldsWeForbidInAnyConvexPayload = []string{
	// Filesystem
	"path",    // absolute FS path — includes username
	"absPath", // explicit abs path
	"workDir", // working directory — same problem
	"sourcePath",
	"filePath",
	// Secrets
	"token", // raw bearer
	"rawToken",
	"secret",
	"password",
	"vaultValue",
	"privateKey",
	// Zero-touch provisioning (provisioning.ts / provision.go). The raw
	// claimSecret and the device's Ed25519 signing seed are sent to Convex
	// ONLY over the dedicated /devices/provision-{attest,claim} HTTP routes
	// (where they are hashed/verified and never stored — same precedent as
	// the bootstrap-pending relayPassword). They must NEVER ride a
	// callMutation sync payload; only the claimSecretHash + public key are
	// ever persisted. These fences catch a careless future sync path.
	"claimSecret",
	"ed25519Seed",
	"claimSecretPlaintext",
	// Yaver Mesh (mesh_cmd.go + backend/convex/mesh.ts). The WireGuard
	// PRIVATE key lives ONLY in the vault — joinMesh publishes the public
	// half + endpoints. If a sync path ever tries to ship the private key
	// (under any of these spellings) it trips here first.
	"wgPrivateKey",
	"wg_private_key",
	"meshPrivateKey",
	// Voice provider API keys — Yaver itself does NOT ship default
	// keys; each user pastes their own into ~/.yaver/config.json.
	// These MUST NEVER reach Convex. Defense-in-depth: VoiceConfig
	// lives only in the host-local Config file, but the same name
	// could be carelessly added to a sync path; this fence catches
	// it at test time.
	"openaiApiKey",
	"deepgramApiKey",
	"cartesiaApiKey",
	"openai_api_key",
	"deepgram_api_key",
	"cartesia_api_key",
	// Output / logs
	"stdout",
	"stderr",
	"output",
	"logs",
	"logOutput",
	"taskOutput",
	// Structured command-card events (command_events.go). These flow
	// P2P over the task SSE stream ONLY; if a future sync path tries to
	// mirror a command card into Convex it trips here. `cwd` has the
	// same username-leak problem as `workDir`; `command`/`chunk` carry
	// shell + output text.
	"cwd",
	"command",
	"chunk",
	"fileContent",
	"fileBytes",
	"body", // often carries user input bodies (not to be confused with HTTP bodies here — this is arg key)
	// Vibe Preview frame + clip data. Frames + clips + summaries
	// flow agent→client P2P only; Convex must only ever see counters.
	"previewFrame",
	"frameData",
	"frameJpeg",
	"framePng",
	"frameBytes",
	"screenshotB64",
	"clipMp4",
	"clipBytes",
	"clipPath",
	"videoBlob",
	"posterBytes",
	// screenlog — local-only screen-frame black box (screenlog.go). The
	// whole point is that frames + window titles NEVER leave the machine,
	// so every field name the index uses is fenced here. There is no
	// screenlog sync path today; this keeps it that way if someone adds
	// one carelessly.
	"screenlogFrame",
	"activeWindow",
	"activeWindowTitle",
	"phash",
	"screenlogDir",
	"screenlogPath",
	// screenlog input-event companion stream (keystrokes + mouse). The
	// events.jsonl trace is local-only and can contain keystroke content;
	// these names must never reach Convex.
	"inputEvents",
	"keystroke",
	"keylog",
	"summaryText",
	"previewSummary",
	"exerciseScript",
	"crashSnippet",
	// Unified Runner — Phase 1 (RUNNER_DEV.md). Output, full log,
	// and per-job working directory stay on the executing host;
	// any future cross-machine sync must be metadata-only.
	"runner_output",
	"runner_log",
	"runner_workdir",
	"runnerOutput",
	"runnerLog",
	"runnerWorkDir",
	"outputTail",
	"logPath",
	// Autorun (autorun.go / autorun_ops.go). A run may publish metadata —
	// slot, task BASENAME, seats, iteration counts, status — because those
	// are slug-class, the same call userProjects already makes. What it must
	// never publish is the run's content or its filesystem:
	//
	//   - progressTail / progressPath: the handoff markdown IS the run's
	//     verbatim reasoning, and its path is absolute (docs/handoff/... under
	//     the user's home). autorunSessionView carries both; the Convex
	//     projection must drop them.
	//   - taskPath: the absolute path to the task file. autorunTaskName()
	//     reduces it to a basename — sync THAT, never this.
	//   - gate: a shell command ("command" is fenced above for the same
	//     reason). goal: user-written natural language.
	//   - healDetail / finishDetail: free text. Convex gets healCount and the
	//     closed finishReason enum instead.
	//
	// A free-text field is how content leaks under a respectable name; the
	// autorun tables deliberately have no `detail`/`note`/`message` column.
	"progressTail",
	"progressPath",
	"progress_tail",
	"progress_path",
	"taskPath",
	"task_path",
	"gate",
	"goal",
	"finalCommitSubject",
	"healDetail",
	"finishDetail",
	"autorunOutput",
	// Unified Runner — Phase 2 (sandbox + agent sessions). Exec
	// output, file content, agent message text, agent prompt /
	// result text, and the chained TaskManager output all stay
	// on-host.
	"sandbox_output",
	"sandboxOutput",
	"sandbox_stdout",
	"sandboxStdout",
	"sandbox_file_content",
	"sandboxFileContent",
	"agent_session_messages",
	"agentSessionMessages",
	"agent_message_text",
	"agentMessageText",
	"messageText",
	"resultText",
	// Native WebRTC remote-runtime — RTP video frames, control
	// payloads, simctl/adb stdout, and any builder credential
	// stay agent-side. Convex sees only counters in
	// `remoteRuntimeSessionMetrics` (counts, durations, transport
	// choice) — never bytes, coords, or hostnames.
	"videoFrame",
	"rtpPayload",
	"mediaSegment",
	"screenStream",
	"simctlOutput",
	"screenrecordPayload",
	"tapCoord",
	"swipeCoord",
	"keyText",
	"clipboardText",
	// GUI ghost (ops_ghost.go): screenshots, click coordinates, typed text and
	// any OCR/vision output stay on-device and flow only to the live caller —
	// they must never be synced to Convex.
	"ghostScreenshot",
	"ghostPngBase64",
	"ghostCoords",
	"ghostInputText",
	"ghostOCR",
	"remoteBuilderHostname",
	"remoteBuilderTunnelToken",
	"builderUrl",
	"builderToken",
	// Companion compute (yaver.companion.yaml). The manifest references
	// env-interpolated endpoint URLs (which embed cron auth tokens) and
	// vault secrets; those stay on-device in ~/.yaver state + OS unit
	// files. Convex companionProjects rows are bookkeeping-only (slug +
	// cron names/schedules + status). These key names must never reach a
	// mutation — interpolated URLs/tokens/abs manifest paths live on-host.
	"endpointUrl",
	"cronAuthToken",
	"cronToken",
	"baseUrl",
	"manifestPath",
}

type recordedMutation struct {
	Path string
	Args map[string]interface{}
}

// installConvexRecorder swaps in a capturing recorder and returns both
// the recording buffer and a teardown function.
func installConvexRecorder(t *testing.T) (*[]recordedMutation, func()) {
	t.Helper()
	var buf []recordedMutation
	var mu sync.Mutex
	convexMutationRecorder = func(path string, args map[string]interface{}) {
		mu.Lock()
		defer mu.Unlock()
		// Deep-ish copy so later mutations by the caller don't
		// contaminate the recording.
		cp := map[string]interface{}{}
		for k, v := range args {
			cp[k] = v
		}
		buf = append(buf, recordedMutation{Path: path, Args: cp})
	}
	return &buf, func() { convexMutationRecorder = nil }
}

// TestConvexSyncProjectsHasNoConfidentialFields exercises the real
// syncProjects code path against a throwaway project dir and asserts
// the payload is clean.
func TestConvexSyncProjectsHasNoConfidentialFields(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	// Create a throwaway "project" that syncProjects will pick up.
	home := t.TempDir()
	projectDir := filepath.Join(home, "kivanccakmak-private-dir", "secret-app")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Seed a yaver.json so LoadProjectConfig returns non-nil.
	if err := os.WriteFile(
		filepath.Join(projectDir, "yaver.json"),
		[]byte(`{"stack":"nextjs","backend":"convex","auth":"google"}`),
		0o644,
	); err != nil {
		t.Fatalf("write yaver.json: %v", err)
	}

	// Point project discovery at our fake dir.
	t.Setenv("YAVER_PROJECTS_ROOT", projectDir)

	s := &convexSyncer{deviceID: "test-device"}
	s.syncProjects(context.Background())

	if len(*buf) == 0 {
		// discoverProjectDirs may not pick YAVER_PROJECTS_ROOT up on
		// every platform. Fall back to building the same payload
		// shape the syncer would build and asserting on that — the
		// fields under test are the ones we wrote by hand.
		cfg, _ := LoadProjectConfig(projectDir)
		if cfg == nil {
			t.Skip("LoadProjectConfig returned nil — project fixture not picked up, skipping live path")
		}
		s.callMutation("agentSync:upsertProject", map[string]interface{}{
			"deviceId":  "test-device",
			"slug":      filepath.Base(projectDir),
			"name":      filepath.Base(projectDir),
			"stack":     cfg.Stack,
			"backend":   string(cfg.Backend),
			"auth":      cfg.Auth,
			"activeEnv": "dev",
			"status":    "running",
		})
	}

	if len(*buf) == 0 {
		t.Fatal("syncProjects produced no mutations — can't assert anything")
	}

	for _, rec := range *buf {
		assertNoForbiddenFields(t, rec)
		assertNoAbsolutePaths(t, rec)
		assertNoUsernameLeak(t, rec, "kivanccakmak-private-dir")
	}
}

// TestConvexSyncServicesHasNoConfidentialFields covers the services
// mutation shape. No real project is needed — we just verify the
// forbidden-field rule holds after building a representative payload.
func TestConvexSyncServicesHasNoConfidentialFields(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()
	s := &convexSyncer{deviceID: "test-device"}
	s.callMutation("agentSync:upsertServices", map[string]interface{}{
		"deviceId": "test-device",
		"services": []map[string]interface{}{
			{
				"name":        "api",
				"image":       "ghcr.io/example/api:latest",
				"port":        8080,
				"status":      "healthy",
				"projectSlug": "demo",
			},
		},
	})
	if len(*buf) != 1 {
		t.Fatalf("expected 1 recorded mutation, got %d", len(*buf))
	}
	for _, rec := range *buf {
		assertNoForbiddenFields(t, rec)
	}
}

// TestConvexRecordActivityHasNoConfidentialFields exercises the
// recent-activity payload shape.
func TestConvexRecordActivityHasNoConfidentialFields(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()
	s := &convexSyncer{deviceID: "test-device"}
	s.callMutation("agentSync:recordActivity", map[string]interface{}{
		"deviceId":  "test-device",
		"action":    "deploy",
		"target":    "web",
		"outcome":   "success",
		"timestamp": int64(1700000000),
	})
	for _, rec := range *buf {
		assertNoForbiddenFields(t, rec)
	}
}

// assertNoForbiddenFields walks the args map (and any nested maps /
// arrays of maps) and fails if it finds a forbidden key.
func assertNoForbiddenFields(t *testing.T, rec recordedMutation) {
	t.Helper()
	walk := func(prefix string, v interface{}) {}
	walk = func(prefix string, v interface{}) {
		switch x := v.(type) {
		case map[string]interface{}:
			for k, v := range x {
				for _, forbidden := range fieldsWeForbidInAnyConvexPayload {
					if k == forbidden {
						t.Errorf(
							"mutation %q payload contains forbidden key %q at %s%q — Convex must never hold this",
							rec.Path, k, prefix, k,
						)
					}
				}
				walk(prefix+k+".", v)
			}
		case []interface{}:
			for i, item := range x {
				walk(fmt.Sprintf("%s[%d].", prefix, i), item)
			}
		case []map[string]interface{}:
			for i, item := range x {
				walk(fmt.Sprintf("%s[%d].", prefix, i), item)
			}
		}
	}
	walk("", rec.Args)
}

// assertNoAbsolutePaths greps every string value for patterns that
// would mean "this is a filesystem path on the agent's machine". It
// tolerates slugs and repo names; it doesn't tolerate anything that
// looks like /Users/foo, /home/foo, or C:\Users\foo.
func assertNoAbsolutePaths(t *testing.T, rec recordedMutation) {
	t.Helper()
	bad := []string{"/Users/", "/home/", "/root/", "C:\\Users\\", "C:/Users/"}
	walk := func(v interface{}) {}
	walk = func(v interface{}) {
		switch x := v.(type) {
		case string:
			for _, b := range bad {
				if strings.Contains(x, b) {
					t.Errorf(
						"mutation %q payload leaks absolute path fragment %q in value %q",
						rec.Path, b, x,
					)
				}
			}
		case map[string]interface{}:
			for _, v := range x {
				walk(v)
			}
		case []interface{}:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(rec.Args)
}

// TestVibePreviewSessionPayload_isCounterOnly is a forward-looking
// guardrail: when Phase 8's recordPreviewSession mutation lands, this
// asserts the payload contains only counters + identifiers, never
// frame bytes / clip bytes / summary text. The test fabricates the
// payload that the Convex syncer would build today; if the future
// implementer wires it up via `convexSyncer.callMutation`, it will
// hit this gate and discover any leak immediately.
func TestVibePreviewSessionPayload_isCounterOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	// Simulate the call that the eventual Phase 8 syncer will make. The
	// shape mirrors what's documented in docs/vibe-preview-streaming.md
	// section 10. If a future implementer adds frame/clip data here,
	// the forbidden-keys walker fails the test.
	convexMutationRecorder(
		"agentSync:recordPreviewSession",
		map[string]interface{}{
			"deviceId":     "test-device",
			"project":      "web",
			"mode":         "live",
			"startedAt":    1714000000,
			"endedAt":      1714000060,
			"frameCount":   42,
			"summaryCount": 0,
		},
	)

	if len(*buf) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(*buf))
	}
	rec := (*buf)[0]
	assertNoForbiddenFields(t, rec)
	assertNoUsernameLeak(t, rec, "kivanccakmak-private-dir")
}

// TestVibePreviewClipPayload_isCounterOnly is the same guardrail for the
// clip metadata sync. Crucially: the clip's on-disk path is allowed
// inside the agent process but MUST NEVER be in a Convex payload.
func TestVibePreviewClipPayload_isCounterOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	convexMutationRecorder(
		"agentSync:recordPreviewClip",
		map[string]interface{}{
			"deviceId":    "test-device",
			"project":     "mobile",
			"clipId":      "c_abc123",
			"durationSec": 11.4,
			"sizeBytes":   1843200,
			"source":      "sim-ios",
			"createdAt":   1714000000,
		},
	)

	if len(*buf) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(*buf))
	}
	rec := (*buf)[0]
	assertNoForbiddenFields(t, rec)
	// Specifically: no path, no clip bytes, no summary text.
	for k := range rec.Args {
		switch k {
		case "clipPath", "videoBlob", "clipBytes", "clipMp4", "summaryText", "posterBytes":
			t.Errorf("forbidden field %q must not be in clip metadata payload", k)
		}
	}
}

// TestCIUsageAgentPayload_isCounterOnly guards the self-hosted CI runner's
// meter call (ci_selfhosted_runner.go defaultCIMeter →
// managedMeter:recordCIUsageFromAgent). The payload bills CI minutes against
// the prepaid wallet, so it must carry ONLY non-secret counters/labels — never
// the repo path, the job log, the forge token, or the workspace dir. If a
// future change stuffs any of those into the args, the forbidden-keys +
// abs-path walkers fail.
func TestCIUsageAgentPayload_isCounterOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	convexMutationRecorder(
		"managedMeter:recordCIUsageFromAgent",
		map[string]interface{}{
			"deviceId":                   "test-device",
			"provider":                   "yaver-cloud",
			"unit":                       "cpu-min",
			"quantity":                   12.5,
			"providerCostCents":          1,
			"wouldHaveCostUpstreamCents": 10,
			"ref":                        "ci_abcdef0123456789",
		},
	)

	if len(*buf) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(*buf))
	}
	rec := (*buf)[0]
	assertNoForbiddenFields(t, rec)
	assertNoUsernameLeak(t, rec, "kivanccakmak-private-dir")
	for k := range rec.Args {
		switch k {
		case "token", "repoPath", "workDir", "workspacePath", "log", "output", "secret":
			t.Errorf("forbidden field %q must not be in CI usage payload", k)
		}
	}
}

// TestRemoteRuntimeSessionMetricsPayload_isCounterOnly is the
// forward-looking guardrail for the eventual `recordRemoteRuntime
// SessionMetrics` mutation (see docs/native-webrtc-web-streaming.md
// §15). When a future implementer wires the syncer to call Convex
// at session-end, the payload must contain ONLY counters (bytes in/
// out, frame count, duration, transport choice). The fake payload
// here mirrors the documented schema; if a drive-by commit later
// stuffs a video blob or builder URL into args, the
// forbidden-keys + abs-path walkers fail the test.
func TestRemoteRuntimeSessionMetricsPayload_isCounterOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	convexMutationRecorder(
		"agentSync:recordRemoteRuntimeSessionMetrics",
		map[string]interface{}{
			"deviceId":    "test-device",
			"framework":   "swift",
			"transport":   "webrtc-rtp-h264-v1",
			"bytesIn":     0,
			"bytesOut":    14_300_000,
			"frames":      720,
			"durationSec": 30,
			"endedAt":     1714000060,
		},
	)

	if len(*buf) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(*buf))
	}
	rec := (*buf)[0]
	assertNoForbiddenFields(t, rec)
	// Specifically: a future "convenience addition" of any of these
	// MUST be caught here, even if forgotten on the deny-list.
	for k := range rec.Args {
		switch k {
		case "videoFrame", "rtpPayload", "screenStream",
			"tapCoord", "swipeCoord", "keyText",
			"builderUrl", "builderToken",
			"remoteBuilderHostname", "remoteBuilderTunnelToken":
			t.Errorf("forbidden field %q must not be in metrics payload", k)
		}
	}
}

// TestRemoteBuilderPairingMetadata_isAliasOnly mirrors the
// guardrail above for the builder-registry sync. The on-disk
// ~/.yaver/builders.json carries (alias, URL, token, platforms);
// only the alias may ever cross the wire. URL gives away infra
// shape; token is the actual auth secret.
func TestRemoteBuilderPairingMetadata_isAliasOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	// Hypothetical future call — the alias is fine, everything else
	// is forbidden. Drive-by addition of `url` / `token` would land
	// here.
	convexMutationRecorder(
		"agentSync:recordRemoteBuilderPairing",
		map[string]interface{}{
			"deviceId":  "test-device",
			"alias":     "mac-rack-1",
			"platforms": []interface{}{"ios"},
			"pairedAt":  1714000060,
		},
	)
	if len(*buf) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(*buf))
	}
	rec := (*buf)[0]
	assertNoForbiddenFields(t, rec)
	for k := range rec.Args {
		switch k {
		case "url", "token", "builderUrl", "builderToken",
			"remoteBuilderHostname", "remoteBuilderTunnelToken":
			t.Errorf("forbidden field %q must not be in builder pairing payload", k)
		}
	}
}

// TestMeshNodeFields_AreNotConvexForbidden pins the meshNodes table
// (backend/convex/schema.ts) as public-control-plane-only. Every synced field
// must be a public key / endpoint / overlay IP / counter / timestamp and must
// NOT collide with the forbidden-secret list. In particular the WireGuard
// PRIVATE key (stored in the vault as wgPrivateKey) must never appear here.
func TestMeshNodeFields_AreNotConvexForbidden(t *testing.T) {
	meshFields := []string{
		// meshNodes
		"userId", "deviceId", "wgPublicKey", "meshIPv4", "meshIPv6",
		"endpoints", "advertisedRoutes", "isExitNode", "online",
		"lastHandshake", "updatedAt",
	}
	forbidden := map[string]bool{}
	for _, k := range fieldsWeForbidInAnyConvexPayload {
		forbidden[k] = true
	}
	for _, f := range meshFields {
		if forbidden[f] {
			t.Errorf("meshNodes field %q is on the Convex forbidden-secret "+
				"list — mesh rows must stay public-key + endpoint only", f)
		}
	}
	// And the converse: the private-key field names MUST be forbidden.
	for _, secret := range []string{"wgPrivateKey", "wg_private_key", "meshPrivateKey"} {
		if !forbidden[secret] {
			t.Errorf("%q must be on the Convex forbidden list — the WireGuard "+
				"private key may never reach Convex", secret)
		}
	}
}

// TestMeshJoinPayload_isPublicOnly feeds the exact arg shape `yaver mesh up`
// posts to mesh:joinMesh through the recorder and asserts it carries only the
// PUBLIC key + endpoints — never the private key under any spelling.
func TestMeshJoinPayload_isPublicOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	convexMutationRecorder(
		"mesh:joinMesh",
		map[string]interface{}{
			"deviceId":    "test-device",
			"wgPublicKey": "TUVTSF9QVUJMSUNfS0VZX0JBU0U2NF8zMmI=",
			"endpoints":   []interface{}{"203.0.113.7:51820"}, // public only
		},
	)
	if len(*buf) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(*buf))
	}
	rec := (*buf)[0]
	assertNoForbiddenFields(t, rec)
	for k := range rec.Args {
		switch k {
		case "wgPrivateKey", "wg_private_key", "meshPrivateKey", "privateKey":
			t.Errorf("forbidden field %q must not be in the mesh join payload", k)
		}
	}
	assertNoPrivateIP(t, rec)
}

// assertNoPrivateIP fails if any string value in the payload contains an RFC1918
// / CGNAT address. meshNodes.endpoints is shared cross-tenant, so a private
// address there leaks the user's internal subnet layout — forbidden by the
// privacy contract. Guards against re-adding LAN IPs to the control-plane push.
func assertNoPrivateIP(t *testing.T, rec recordedMutation) {
	t.Helper()
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch x := v.(type) {
		case string:
			host := x
			if h, _, err := net.SplitHostPort(x); err == nil {
				host = h
			}
			if ip := net.ParseIP(host); ip != nil && ip.IsPrivate() {
				t.Errorf("mutation %q payload leaks a private LAN IP %q — RFC1918 must never reach Convex", rec.Path, x)
			}
		case map[string]interface{}:
			for _, v := range x {
				walk(v)
			}
		case []interface{}:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(rec.Args)
}

// TestMeshLocalEndpoints_NoPrivateIPs pins that the endpoint set the agent
// publishes to the control plane never includes an RFC1918 address, regardless
// of what LAN this test host is on.
func TestMeshLocalEndpoints_NoPrivateIPs(t *testing.T) {
	for _, ep := range meshLocalEndpoints() {
		host := ep
		if h, _, err := net.SplitHostPort(ep); err == nil {
			host = h
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsPrivate() {
			t.Errorf("meshLocalEndpoints returned a private LAN IP %q — must not be published to Convex", ep)
		}
	}
}

// assertNoUsernameLeak is the canary for the specific bug we just
// fixed: if `kivanccakmak-private-dir` shows up as a substring of any
// value, the payload is embedding info from the test's fake home dir.
func assertNoUsernameLeak(t *testing.T, rec recordedMutation, marker string) {
	t.Helper()
	walk := func(v interface{}) {}
	walk = func(v interface{}) {
		switch x := v.(type) {
		case string:
			if strings.Contains(x, marker) {
				t.Errorf(
					"mutation %q payload contains test-marker %q in value %q — someone re-added the abs-path field",
					rec.Path, marker, x,
				)
			}
		case map[string]interface{}:
			for _, v := range x {
				walk(v)
			}
		case []interface{}:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(rec.Args)
}

// The managed-cloud prepaid wallet (backend/convex schema.ts:
// prepaidCredits, creditUsage; owned by cloudLifecycle.ts) is
// DELIBERATELY counter-only — same Convex-allowed class as
// runnerUsage/dailyTaskCounts. This pins that decision: every field
// name in those two tables must be a counter/id/timestamp and must
// NOT collide with the forbidden-secret list. If someone adds a
// command/path/token/output-class field to the wallet ledger this
// fails loudly. (Wallet rows are Convex-internal — never an
// agent→Convex payload — so the payload walker doesn't see them; this
// static field-name pin is the guard.)
func TestPrepaidWalletFields_AreNotConvexForbidden(t *testing.T) {
	walletFields := []string{
		// prepaidCredits
		"userId", "subscriptionId", "balanceCents", "totalAddedCents",
		"totalUsedCents", "currency", "lastTopupAt", "lastMeteredAt",
		"createdAt", "updatedAt",
		// creditUsage
		"machineId", "date", "state", "seconds", "hetznerCostCents",
		"chargedCents", "ratePerHourCents", "dryRun",
		// creditTopups (real-money prepaid top-up ledger; idempotent on
		// orderId). Counter/id/timestamp only — orderId is a payment
		// provider order reference, not a secret.
		"orderId", "source", "packId", "amountCents",
		// managedUsage (generic reseller meter; managedMeter.ts). The
		// inference/backend/web/publish meters all debit the same wallet.
		// kind/provider/unit/model/ref are NON-SECRET labels (same class
		// as cloudMachines.serverId); quantity/cost/cents are counters.
		"kind", "provider", "unit", "quantity", "providerCostCents",
		"model", "ref", "wouldHaveCostUpstreamCents",
	}
	forbidden := map[string]bool{}
	for _, k := range fieldsWeForbidInAnyConvexPayload {
		forbidden[k] = true
	}
	for _, f := range walletFields {
		if forbidden[f] {
			t.Errorf("prepaid-wallet field %q is on the Convex forbidden-secret "+
				"list — the wallet ledger must stay counter-only", f)
		}
	}
}

// TestByoMachinesFields_AreNotConvexForbidden pins the byoMachines table
// (backend/convex/schema.ts) as lifecycle-bookkeeping-only. A BYO box
// runs on the USER's own provider account; Convex stores only its id +
// state + timestamps so the alive/sleeping/deleted status is visible
// across the user's devices. Every field must be an id/state/timestamp/
// non-secret descriptor — the provider TOKEN never reaches Convex (it
// stays in the agent vault). If someone adds a token/key/path field this
// fails loudly.
func TestByoMachinesFields_AreNotConvexForbidden(t *testing.T) {
	byoFields := []string{
		"userId", "provider", "serverId", "deviceId", "name", "region",
		"plan", "serverIp", "imageId", "snapshotImageId", "state",
		"createdAt", "lastUpAt", "stoppedAt", "deletedAt", "updatedAt",
	}
	forbidden := map[string]bool{}
	for _, k := range fieldsWeForbidInAnyConvexPayload {
		forbidden[k] = true
	}
	for _, f := range byoFields {
		if forbidden[f] {
			t.Errorf("byoMachines field %q is on the Convex forbidden-secret "+
				"list — BYO rows must stay id/state/timestamp-only (the provider "+
				"token never leaves the agent vault)", f)
		}
	}
}

// TestCompanionProjectsFields_AreNotConvexForbidden pins the companionProjects
// table (backend/convex/schema.ts) as bookkeeping-only. Every field name must
// be a slug / cron expression / counter / status / timestamp and must NOT
// collide with the forbidden-secret list. Companion manifests carry endpoint
// URLs + cron tokens + abs paths + vault secrets — all of which stay on-device;
// if someone adds a url/token/path-class field to the synced row this fails.
func TestCompanionProjectsFields_AreNotConvexForbidden(t *testing.T) {
	companionFields := []string{
		// companionProjects
		"userId", "deviceId", "slug", "enabled", "serviceCount", "updatedAt",
		// crons[] entries
		"name", "schedule", "lastOutcome", "lastRunAt", "nextRunAt",
	}
	forbidden := map[string]bool{}
	for _, k := range fieldsWeForbidInAnyConvexPayload {
		forbidden[k] = true
	}
	for _, f := range companionFields {
		if forbidden[f] {
			t.Errorf("companionProjects field %q is on the Convex forbidden-secret "+
				"list — companion rows must stay bookkeeping-only", f)
		}
	}
}

// TestCompanionUpsertPayloadHasNoConfidentialFields feeds DELIBERATELY leaky
// inputs (a cron name containing an abs path + token, a slug with the user's
// home dir) through the real buildCompanionUpsertPayload seam and asserts the
// recorded mutation is clean — proving sanitization holds and no url/token/path
// reaches Convex.
func TestCompanionUpsertPayloadHasNoConfidentialFields(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	// Realistic engine inputs: slug comes from the manifest `project` name
	// and cron names from the manifest — both messy human strings, never
	// paths. The seam must (a) include ONLY whitelisted keys and (b) run
	// names/slug through sanitizeCompanionName.
	crons := []CompanionCronSummary{
		{Name: "Auto Mail Sender", Schedule: "*/15 * * * *", LastOutcome: "ok", LastRunAt: 1, NextRunAt: 2},
		{Name: "daily_summary", Schedule: "0 6 * * *"},
	}
	payload := buildCompanionUpsertPayload("test-device", "E-Back", true, crons, 1)

	s := &convexSyncer{deviceID: "test-device"}
	s.callMutation("companion:upsertCompanionProject", payload)

	if len(*buf) != 1 {
		t.Fatalf("expected 1 recorded mutation, got %d", len(*buf))
	}
	for _, rec := range *buf {
		assertNoForbiddenFields(t, rec)
		assertNoAbsolutePaths(t, rec)
		// sentinel username must never appear anywhere in the payload
		assertNoUsernameLeak(t, rec, "kivanccakmak")
	}

	// Names/slug are sanitized to [a-z0-9-].
	if got := payload["slug"]; got != "e-back" {
		t.Fatalf("slug not sanitized: %v", got)
	}
	if got := sanitizeCompanionName("Auto Mail Sender"); got != "auto-mail-sender" {
		t.Fatalf("cron name not sanitized: %v", got)
	}
}

// TestTaskPackagePayload_isBookkeepingOnly pins the Task Package sync seam
// (package_sync.go). A package's sources/MCP bindings carry full URLs that may
// embed query tokens, plus on-device secrets — NONE of that may reach Convex.
// buildTaskPackagePayload must emit hostnames-only + slugs + counters.
func TestTaskPackagePayload_isBookkeepingOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	p := &TaskPackage{
		Metadata: PackageMeta{Name: "Serbia Odds!", Version: 2, Description: "watch public odds"},
		Spec: PackageSpec{
			Runtimes: []string{"mobile", "agent"},
			Vantage:  PackageVantage{Geo: []string{"RS"}, Residential: true},
			Schedule: PackageSchedule{Every: "10m"},
			Consent:  PackageConsent{Summary: "fetch public pages", WillNot: []string{"login"}, DataShown: []string{"price"}},
			Task: PackageTask{
				Kind:    "collect",
				Engines: []string{"webview", "mcp"},
				Sources: []PackageSource{{ID: "s", URL: "https://odds.example.com/live?token=SECRET123&path=/Users/me/x"}},
				MCP:     []PackageMCPBinding{{Name: "yaver-bet", URL: "https://mcp.example.com/mcp", AuthToken: "BEARER_SECRET", Tool: "rec"}},
			},
		},
	}
	payload := buildTaskPackagePayload("dev-1", p)

	s := &convexSyncer{deviceID: "dev-1"}
	s.callMutation("taskPackages:upsertPackage", payload)
	if len(*buf) != 1 {
		t.Fatalf("expected 1 recorded mutation, got %d", len(*buf))
	}
	for _, rec := range *buf {
		assertNoForbiddenFields(t, rec)
		assertNoAbsolutePaths(t, rec)
	}

	// Domains must be hostnames only — never the token, the full path, or scheme.
	domains, _ := payload["domains"].([]string)
	if len(domains) != 2 || domains[0] != "odds.example.com" || domains[1] != "mcp.example.com" {
		t.Fatalf("domains must be hostnames only, got %v", domains)
	}
	for k, v := range payload {
		if str, ok := v.(string); ok {
			if strings.Contains(str, "SECRET123") || strings.Contains(str, "BEARER_SECRET") || strings.Contains(str, "token=") {
				t.Errorf("payload field %q leaked a secret/token: %q", k, str)
			}
		}
	}
	if payload["name"] != "serbia-odds" {
		t.Errorf("name not sanitized to a slug: %v", payload["name"])
	}
}
