# Remote MCP — Hermes-reload from glasses / keyboard / mic

**Status:** plan + audit, 2026-05-28. Implementation phases tracked below.

## Goal

From glasses + BT keyboard + (iPhone OR Beam Pro), drive a **remote MCP** server (running on a self-hosted dev box OR a Yaver managed-cloud box) and have it trigger a **Hermes reload** of the React Native / Expo app under test on the same phone. Input can be **keyboard** (typed prompt) OR **microphone** (whisper.rn STT). Output through paired headset (TTS).

```
glasses ── USB-C DP ──┐
                      │
  ┌───────────────────▼────────────────────────────────────────────────┐
  │  phone  (iPhone 15+  |  Xreal Beam Pro / Android)                  │
  │                                                                    │
  │  Yaver mobile app                                                  │
  │    ├── glass-terminal screen                                       │
  │    │     • keyboard input (BT foldable)                            │
  │    │     • mic chip → whisper.rn realtime STT → input              │
  │    │     • 🔊 toggle → speakText() on every model_text             │
  │    │     • vibe bar chips → ⟳ reload / 📦 push / 📊 status / 🩺   │
  │    ├── on-phone agent runner (runYaverAgent — tool-use loop)       │
  │    ├── DIRECT MCP client (NEW — phase 3)                           │
  │    ├── BlackBox SSE listener (receives reload broadcasts)          │
  │    └── app under test  ← Hermes hot-reloads its JS bundle          │
  └────────────────────────────────────────────────────────────────────┘
                          │ HTTPS / SSE / WS
                          │ via Yaver relay OR direct tunnel
                          ▼
  ┌─────────────────────────────────────────────────────────────────────┐
  │  remote dev box (self-hosted | Yaver managed cloud | home NUC)      │
  │                                                                     │
  │  Yaver Go agent                                                     │
  │    ├── tmux session: `claude code`, `codex`, vim, git               │
  │    ├── MCP HTTP server  (:8322 — `talcli mcp serve --http`)         │
  │    ├── dev-server manager  (Metro / Expo bundler)                   │
  │    ├── POST /dev/reload  (existing — broadcasts hot_reload)         │
  │    └── BlackBox manager — pushes `hot_reload` cmd to SDK devices    │
  │                                                                     │
  │  NEW MCP tool: `mobile_hermes_reload`  (phase 2)                    │
  │    thin wrapper over /dev/reload — returns status:                  │
  │    { ok, changeClass: "js_only" | "native_rebuild_required",        │
  │      nativeChanges?, devicesReached }                               │
  └─────────────────────────────────────────────────────────────────────┘
```

The shell websocket + tmux session on the remote box **never closes** during a reload — only the JS bundle of the app-under-test gets swapped on the phone.

## Audit — what's already wired

| Piece | Where | Status |
|---|---|---|
| `POST /dev/reload` HTTP route on Go agent | `desktop/agent/devserver_http.go:1755` `handleDevServerReload` | ✅ Exists. Broadcasts `hot_reload` control signal + BlackBoxCommand to all subscribed SDK devices. Computes native-fingerprint delta and distinguishes `js_only` vs `native_rebuild_required`. |
| BlackBox SSE command stream | `desktop/agent/blackbox_http.go:123` `handleBlackBoxCommandStream` | ✅ Exists. `GET /blackbox/command-stream?device=...` |
| Mobile SSE subscription | `mobile/src/lib/quic.ts:5645` `streamBlackBoxCommands` | ✅ Exists. Hands `BlackBoxCommandEnvelope` to a callback. |
| ACL peer protocol (cross-device tool dispatch) | `acl_add_peer`, `acl_call_peer_tool` MCP tools | ✅ Exists per `account_list` / `acl_*` MCP families. |
| MCP HTTP server (remote MCP) | `talcli mcp serve --http :8322 --secret <secret>` | ✅ Exists per `CLAUDE.md` (Talos has a Hetzner copy; same binary serves Yaver). |
| Mobile agent runner with tool-use | `mobile/src/lib/yaverAgentRunner.ts` | ✅ Exists. Dispatches tools via `yaverAgentTools`. |
| Glass-terminal voice in/out | `mobile/app/glass-terminal.tsx` (shipped 1.18.128) | ✅ Shipped this session. |
| Glass-terminal vibe-chip handlers | `triggerVibe()` in same file | ✅ Wired — but currently goes through LLM round-trip (slow). |

## Gap — what's missing

1. **No direct-call MCP tool for Hermes reload.** Today's `triggerVibe` chip asks an LLM to pick the right MCP tool. That's:
   - **Slow** (1-3 s LLM latency + tool call vs 100 ms direct)
   - **Non-deterministic** (model might pick wrong tool, or hallucinate args)
   - **Burns BYOK / subscription budget** on a trivial action
2. **No first-class `mobile_hermes_reload` MCP tool** wrapping `/dev/reload`. Today the closest is `mobile_hermes_doctor` (diagnoses but doesn't reload) and a `hotreload` tab in the mobile app. A direct MCP tool lets ANY MCP client (Claude Code on remote, Yaver mobile app, ChatGPT desktop app, etc.) trigger it.
3. **No verified BlackBox listener** in glass-terminal context. The listener exists in the SDK but it's unclear whether the Yaver mobile app itself subscribes — important because when the app under test == the Yaver app, that path is the only thing that fires the JS reload.
4. **Beam Pro on Android** uses the same Yaver app codebase, but USB-C DP Alt Mode + Android dev-client + SSE keep-alive behaviour during external display mirroring has never been smoke-tested for this use case.

## Implementation plan

### Phase 1 — verify existing pieces (½ day, mostly read-only)

- [ ] Trace `quicClient.baseUrl` setter to confirm it correctly points at a **remote managed-cloud** Yaver agent (not just LAN). Already proven for Path A so likely fine — verify with a curl from the phone over relay.
- [ ] Find the mobile-app-side BlackBox listener registration. Search `mobile/` for `streamBlackBoxCommands(` calls. Confirm it runs at app boot and the `hot_reload` command path calls `DevSettings.reload()` (RN) or `Updates.reloadAsync()` (expo-updates).
- [ ] If listener is missing in the mobile app proper (vs. the feedback-SDK package), add it.

### Phase 2 — add `mobile_hermes_reload` MCP tool (½ day, Go agent)

Files:
- `desktop/agent/mcp_mobile_hermes_reload.go` *(new)* — handler `mcpMobileHermesReload()` calling the existing `s.handleDevServerReload` flow internally (factor out the core into a method that both the HTTP handler and the MCP handler use). Returns:
  ```go
  type MobileHermesReloadResult struct {
      OK             bool                       `json:"ok"`
      ChangeClass    string                     `json:"changeClass"`   // "js_only" | "native_rebuild_required" | "unknown"
      NativeChanges  []NativeFingerprintChange  `json:"nativeChanges,omitempty"`
      DevicesReached int                        `json:"devicesReached"`
      Error          string                     `json:"error,omitempty"`
  }
  ```
- `desktop/agent/mcp_tools.go` — register `mobile_hermes_reload` in the tool list with `inputSchema: { mode?: "dev"|"bundle", scope?: string }`
- `desktop/agent/httpserver.go` — `case "mobile_hermes_reload":` dispatcher
- `desktop/agent/mcp_mobile_hermes_reload_test.go` — table test for `js_only` vs `native_rebuild_required` paths

Risk: minor — directly mirrors an existing, well-tested HTTP endpoint.

### Phase 3 — direct-MCP path in glass-terminal (½ day, mobile)

Files:
- `mobile/src/lib/yaverMcpDirect.ts` *(new)*
  ```ts
  export interface McpDirectResult<T = unknown> { ok: boolean; result?: T; error?: string }
  export async function callMcpDirect<T = unknown>(
    toolName: string,
    args: Record<string, unknown>,
  ): Promise<McpDirectResult<T>>;
  ```
  Implementation: POST `${quicClient.baseUrl}/mcp/call` with bearer auth headers, body `{name, arguments}`. The Go MCP HTTP server already speaks this shape (`/call` endpoint per CLAUDE.md).

- `mobile/app/glass-terminal.tsx` — replace `triggerVibe(label, prompt)` with `triggerVibeDirect(label, toolName, args, summarize)`. The four vibe chips become:
  | Chip | MCP tool | Args |
  |---|---|---|
  | `⟳ reload` | `mobile_hermes_reload` | `{}` |
  | `📦 push` | `wire_push` or `wireless_push` (pick by current device transport) | `{ source: "current" }` |
  | `📊 status` | `mobile_project_status` | `{}` |
  | `🩺 doctor` | `mobile_hermes_doctor` | `{}` |

  Render the JSON result inline in the output buffer. No LLM round-trip.

- Keep the old LLM-narrated path as a fallback when `mcp_direct_failed` (tool not found, transport error) — fail back to `runYaverAgent` with the explanatory prompt.

### Phase 4 — Beam Pro / Android verification (¼ day)

**Software-side coverage shipped in cli/v1.99.236 — see `desktop/agent/mcp_mobile_hermes_reload_test.go`:**

| Test | Asserts |
|---|---|
| `TestSendCommandToDevice_DeliversOnlyToScopedSession` | scoped send hits ONLY the target device, sibling sessions stay silent (this is the cross-device targeting contract) |
| `TestSendCommandToDevice_UnknownDeviceReturnsFalse` | unknown id returns false so `/dev/reload`'s fallback-to-broadcast branch fires |
| `TestSendCommandToDevice_EmptyIDReturnsFalse` | empty id is handled defensively |
| `TestBroadcastCommand_HitsEverySession` | legacy broadcast regression guard |
| `TestMobileHermesReloadArgs_JSONTags` | catches drift between Go field names and the MCP `inputSchema` declared in `mcp_tools.go` |

**Mobile-side BlackBox listener wiring — verified at `mobile/app/(tabs)/_layout.tsx:161`:**

- `quicClient.streamBlackBoxCommands(resolved.id, …)` is registered at the tabs layout level (boots with the app).
- Handles `command === "reload" | "reload_bundle"` by calling `loadApp(${quicClient.baseUrl}${bundlePath}, moduleName, authHeaders)` — that's the preview-worker bundle swap. Works for the "Yaver mobile app IS the app under test" case (the most relevant one for this workflow).
- Echoes `preview_worker_bundle_loaded` / `preview_worker_bundle_load_failed` events back to the agent so the originating MCP caller can read state.

**Still needs Beam Pro hardware in hand (`BEAM_PRO_DEV.md` smoke checklist):**

- [ ] USB-C DP Alt Mode mirrors Android UI to Xreal One
- [ ] Android dev-client doesn't throttle SSE during external-display mirroring
- [ ] BT foldable keyboard works inside `TextInput` (KeyboardAvoidingView quirks)
- [ ] Mic / `RECORD_AUDIO` permission flow on Android dev-client
- [ ] Power: 25 000 mAh PD bank holds 4+ hours of glasses + phone
- [ ] Pair → enter glass-terminal → ⟳ chip → reload fires within 1 s

### Phase 5 — docs + memory (½ hour)

- [ ] Update `BEAM_PRO_DEV.md` Path C section with the direct-MCP path + the latency contrast (LLM vs direct).
- [ ] Add row to the audit table for `mobile_hermes_reload`.
- [ ] Update `project_remote_dev_via_beam_pro.md` memory entry with the new architecture diagram.

### Phase 6 — release (¼ day)

- [ ] Bump `versions.json` cli (Go agent) AND mobile.
- [ ] Tag `cli/v1.99.234` + `mobile/v1.18.129`.
- [ ] CI publishes npm + Go binaries + Play / TestFlight.

## Decision: keep BOTH the LLM-narrated and direct-MCP paths

The vibe chips become direct-MCP (deterministic + fast). The agent-mode prompt input keeps the LLM tool-use loop so freeform "find the bug and reload" type requests still work. Both share the same MCP transport — direct-MCP is just a shortcut around the LLM step.

## Latency budget

| Path | Step | Budget |
|---|---|---|
| Direct-MCP (NEW) | phone → relay → remote MCP `/call` → /dev/reload → BlackBox broadcast → SSE → DevSettings.reload | < 500 ms typical |
| LLM-narrated (existing) | + 1× LLM round-trip + 1× tool call | 1.5–3 s |

For the "edit → save → ⟳" loop, the direct path is what makes it feel native.

## Auth + multi-device scoping

`/dev/reload` broadcasts `hot_reload` to **all** connected SDK devices. For single-user, single-phone use this is fine. Multi-phone setups need:
- `BlackBoxCommand.targetCommandScope` already exists in the envelope shape (`mobile/src/lib/quic.ts:8218`).
- `mobile_hermes_reload` MCP tool accepts an optional `deviceId` filter; if omitted, broadcasts to all.

## Cross-device reload — Beam Pro driving an iPhone (or vice-versa)

The user often has **two phones in play**:
- Phone A = **driver**: Beam Pro (Android) with USB-C DP into the glasses, BT keyboard, mic
- Phone B = **target**: iPhone on the desk, running the build of the RN/Expo app under test

Phone A taps `⟳ reload` (or says "reload" via mic). The reload must fire on **phone B**, not on phone A.

```
glasses
  ↕ DP
phone A (Beam Pro — driver)
  Yaver mobile app
    ├── glass-terminal: types/says "reload"
    └── direct-MCP call: mobile_hermes_reload { targetDeviceId: phoneB.id }
                                    │
                       relay / direct WS
                                    ▼
              remote dev box  (or Yaver managed cloud)
                    Yaver Go agent
                      ├── MCP HTTP server :8322
                      ├── handler dispatches /dev/reload internally
                      └── BlackBox broadcasts hot_reload
                              ↓
                   filter by targetDeviceId == phoneB.id
                              ↓
                  phone B  (iPhone — target, app under test)
                    BlackBox SSE listener
                       └── DevSettings.reload() → Hermes pulls
                           new JS bundle from Metro
```

### What changes vs single-phone

| Surface | Single-phone | Cross-device |
|---|---|---|
| `mobile_hermes_reload` MCP tool | broadcasts to all subscribed devices | accepts optional `targetDeviceId` (or `targetAlias`, `targetPlatform`) — when set, BlackBox filters before send |
| BlackBox SSE registration | both phones subscribe to the same dev-box's `/blackbox/command-stream?device=<their-id>` | each phone registers with its **own** stable device id (already the case — `quicClient.baseUrl + auth header carries the identity`) |
| glass-terminal vibe chip | hits MCP with no target | NEW: device-picker chip in the **vibe bar** (separate from the existing shell-target picker) — lists OTHER user-owned phones the agent can see, sticky selection per session |
| Mobile listener | already wired (Phase 1) | unchanged — same path on phone B, just filtered by id upstream |

### Phase 7 — cross-device target selection ✅ SHIPPED 2026-05-28

Files:
- `desktop/agent/blackbox.go` — extend `BroadcastControlSignal` / `BlackBoxCommand` send loop to honour an optional `targetDeviceId` from the originating MCP call. (Field `targetCommandScope` already exists in the envelope — reuse it.)
- `desktop/agent/mcp_mobile_hermes_reload.go` (from Phase 2) — accept `target_device_id` / `target_platform` args, pass through.
- `mobile/src/lib/yaverMcpDirect.ts` (from Phase 3) — pass target args through.
- `mobile/app/glass-terminal.tsx` — add a **target-device chip** sitting in the vibe bar:
  - Tap once → pop a sheet showing other Yaver devices owned by the user (`useDevice().devices` filtered to platforms `ios`/`android` excluding self).
  - Tap a device → sticky select. Vibe-bar shows `⟳ → @iphone-on-desk` until cleared.
  - Long-press → clear target (revert to broadcast-to-self default).
- Output buffer: each chip fire prints `→ targeting @iphone-on-desk` so the user has confirmation.

**Landed in cli/v1.99.235 + mobile/v1.18.130:**
- `desktop/agent/devserver_http.go`: `/dev/reload` now accepts `{ targetDeviceId, mode }` JSON body. When `targetDeviceId` is set, calls `BlackBoxManager.SendCommandToDevice(id, BlackBoxCommand{Command: "reload"})` instead of broadcasting. Response gains `targetedDeviceId` field. Falls back to broadcast if the scoped device has no active session.
- `mobile/app/glass-terminal.tsx`: new 🎯 target chip at the front of the vibe bar. Tap → modal lists all Yaver devices (incl. a "broadcast" sentinel). Pick → sticky select, persisted via `AsyncStorage` key `@yaver/glass_terminal/reload_target/v1` so it survives relaunches. Long-press → clear target.
- ⟳ direct path now passes `{targetDeviceId}` through `callMobileHermesReload`. Output buffer shows `→ @target-alias` on each fire.

### Phase 8 — peer-to-peer (no dev-box) cross-device reload (stretch)

**Status:** mostly redundant with Phase 7 in practice.

**Why:** for the realistic case where both phones connect to the SAME Yaver agent (managed-cloud or self-hosted), Phase 7's scoped BlackBox send already handles cross-device routing without needing any phone-to-phone peer surface. Phase 7 unit tests confirm the contract.

The genuinely-new case Phase 8 would unlock is **two phones, NO shared agent** (e.g. on a train, offline-ish):
- Driver phone (Phone A) has no path to any Yaver agent.
- Target phone (Phone B) also has no path to any Yaver agent.
- Both need to reach each other directly.

To implement this cleanly the mobile app would have to:
1. Expose an MCP-compatible HTTP server inside the RN runtime (currently it's only a client).
2. Or implement a relay-mediated peer command bus (`/peer-command-push` + `/peer-command-stream` on the relay) — needs `relay/` codebase work.

Neither is small. Mark as stretch for a future session when the no-agent use case becomes a real pain point.

**Workaround today:** spin up a free-tier managed-cloud Yaver agent on `yaver_managed_cloud_onboarding`, point both phones at it, and Phase 7 covers the rest. Internet still required, but no source-code dev box needed if you're only reloading a pre-shipped Expo bundle.

## Open questions for follow-up

1. Should `mobile_hermes_reload` also bump a feedback-SDK overlay so the user sees "reloading…" on the app-under-test side? (Phase 4 smoke will tell.)
2. Should the direct-MCP path also be available outside glass-terminal? (Probably yes — useful for other chip-based UI surfaces too.)
3. For voice path: should the mic chip auto-trigger the reload chip if user says "reload" / "yenile" / "reset" without the LLM step? (Keyword shortcut layer — stretch.)
4. Cross-device: should the target chip auto-pick the last-used target on app re-launch? Probably yes — store in AsyncStorage next to the saved-prompts cache.
