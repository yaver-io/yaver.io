package main

// getMCPToolsList returns the full MCP tools list for tools/list responses.
func (s *HTTPServer) getMCPToolsList() interface{} {
	tools := []map[string]interface{}{
		// --- Task Management ---
		{
			"name":        "create_task",
			"description": "Create a new coding task. The AI runner will execute this task on the connected development machine. Returns a structured task object; when video recording is enabled and a clip exists, the task includes videoClipId/videoStatus/videoClipUrl/videoPosterUrl so MCP clients can render a watch link or inline player for demos recorded on the producing machine.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"prompt"},
				"properties": map[string]interface{}{
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "The task prompt describing what the AI should do",
					},
					"verbosity": map[string]interface{}{
						"type":        "integer",
						"description": "Response detail level 0-10. 0=minimal ('done, no issues'), 5=moderate (key changes + reasoning), 10=full (all diffs, reasoning, alternatives). Default: 10.",
						"minimum":     0,
						"maximum":     10,
					},
					"runner": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"", "claude", "codex", "opencode"},
						"description": "Runner ID — claude / codex / opencode. Empty = agent default.",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "Model id forwarded to the runner (e.g. claude-opus-4-7, gpt-5-codex, or any opencode-configured provider/model). Empty = runner default.",
					},
					"mode": map[string]interface{}{
						"type":        "string",
						"description": "Runner-specific subcommand. Currently honored by opencode where it maps to `--agent <mode>` — typically 'build' or 'plan', or any custom agent defined in the user's opencode.json. Other runners ignore it.",
					},
					"video_enabled": map[string]interface{}{
						"type":        "boolean",
						"description": "Toggle the post-completion video summary. When true, after the task finishes the agent records a short MP4 demonstrating the running result via vibe-preview (sim/emulator MP4 for mobile, browser frame burst for web). The mobile + web task views render a '▶ Watch demo' button. Default false.",
					},
					"video_source": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"browser", "sim-ios", "sim-android", "phone"},
						"description": "Override the auto-detected recorder. Empty = let the agent infer from the task's workDir (e.g. RN/Expo with ios/ → sim-ios; web → browser).",
					},
					"ask_freely": map[string]interface{}{
						"type":        "boolean",
						"description": "Default false. When true, the new task is exempt from yaver's no-questions preamble + soft-question fallback — the runner may emit clarifying questions in prose. Only enable for audits or risky-change reviews where the user wants explicit confirmation. When false, the runner is told to pick sensible defaults and stop only via the yaver_ask_user MCP tool.",
					},
				},
			},
		},
		{
			"name":        "list_tasks",
			"description": "List all tasks and their current status (queued, running, completed, failed, stopped). Each task may also expose remote demo video artifacts via videoClipId/videoStatus/videoClipUrl/videoPosterUrl.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "get_task",
			"description": "Get detailed information about a specific task, including its full output. If the task recorded a remote demo clip, the response includes videoClipId/videoStatus/videoClipUrl/videoPosterUrl for playback.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"task_id"},
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "The task ID",
					},
				},
			},
		},
		{
			"name":        "stop_task",
			"description": "Stop a running task.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"task_id"},
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "The task ID to stop",
					},
				},
			},
		},
		{
			"name":        "continue_task",
			"description": "Continue a stopped task with additional input/instructions.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"task_id", "input"},
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "The task ID to continue",
					},
					"input": map[string]interface{}{
						"type":        "string",
						"description": "Follow-up instructions for the task",
					},
				},
			},
		},
		{
			"name":        "yaver_ask_user",
			"description": "Ask the human running this Yaver task a single structured question (Claude-Code-style: short 'header' chip + 2-4 'choices', optional multi-select, free-text 'Other' is always offered by the surface). The question is delivered to whichever Yaver surface the user is on (mobile app, web dashboard, CLI); the answer string is returned as the tool result. Blocks until answered or until the timeout (default 5 min, max 30). DEFAULT TO NOT CALLING THIS. Asking is the slow path — the user is on a phone and may have walked away, so an unanswered question stalls the whole run until it times out. Before calling, you must have already: (1) checked the project files / git log / vault for the answer, and (2) confirmed no sensible default exists. Only ask for genuinely irreversible actions, value judgements, or production / billing / customer-visible state. For everything else pick the most reasonable default, state the assumption in one line, and proceed — a reversible wrong guess is cheaper than a stalled run. Result on timeout / cancel: {cancelled:true} — handle it by taking the safest default and continuing, never by re-asking. Requires the agent to be running inside a Yaver task (YAVER_TASK_ID env var must be set by the spawning daemon).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"prompt"},
				"properties": map[string]interface{}{
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "The question to show the user. Be specific and brief — the user is on a phone or laptop and may have walked away. Include the consequence of each option if asking for a choice.",
					},
					"header": map[string]interface{}{
						"type":        "string",
						"description": "Optional short tag (≤12 chars, e.g. 'Auth method', 'DB', 'Deploy target') rendered as a chip above the prompt — the Claude-Code AskUserQuestion style. Omit for a plain question.",
					},
					"kind": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"text", "choice", "secret"},
						"description": "How to render the input on the user's surface. 'text' = free-form text input (default). 'choice' = pick from the choices array (the surface ALWAYS also offers a free-text 'Other…' so you never need to add one). 'secret' = password-style input; the answer is NOT echoed in any SSE event so neighbouring devices can't see it. The runner still receives it in the tool result.",
					},
					"choices": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Required when kind=choice. Each entry is one option label. Keep to 2-4 short, mutually-exclusive options (a free-text 'Other…' is appended automatically). Put your recommended option first.",
					},
					"multi": map[string]interface{}{
						"type":        "boolean",
						"description": "kind=choice only. true = the user may select multiple options; the answer comes back as the picked labels joined by '; '. Default false (single pick).",
					},
					"vault_hint": map[string]interface{}{
						"type":        "string",
						"description": "When asking for a credential, set to the vault entry name you'd ideally read instead. The mobile/web sheet renders a 'Use stored value' shortcut so the user doesn't have to retype. Combine with kind=secret.",
					},
					"timeout_sec": map[string]interface{}{
						"type":        "integer",
						"minimum":     30,
						"maximum":     1800,
						"description": "Seconds to wait for an answer before the tool returns {cancelled:true}. Default 300, max 1800.",
					},
				},
			},
		},
		{
			"name":        "wire_detect",
			"description": "List USB-cable-attached iPhones/iPads (xcrun devicectl, falls back to xctrace) plus Android devices (adb devices -l) on the agent's host machine. Skips simulators/emulators and WiFi-paired devices. Returns {devices:[{udid,name,platform,os}], count, hint}. Useful before calling wire_push to know which device IDs you can target. Same data the CLI's `yaver wire detect --json` returns.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "wire_push",
			"description": "Build a self-contained native binary (xcodebuild Release / gradle installRelease) and install it on a USB-attached phone via the agent's host machine. No Metro / dev server is involved — JS is bundled into the .app/.apk at build time. Auto-detects the framework (Expo, React Native, Flutter, native iOS, native Android) and walks into common subdirs (mobile/, app/, apps/*, packages/*) when the path itself isn't a mobile project. Long-running (5-30 min); captures stdout/stderr to ~/.yaver/logs/wire-push-*.log and returns the path + last 30 lines so you can grep for errors. Returns {ok, exit_code, device, platform, stack, log_path, log_tail, elapsed_sec}.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Project path. Empty = the AI session's working directory (the dir Claude Code / Codex / opencode was started in) — typically what you want. Walks one level into mobile/, app/, apps/*, packages/* if the given path isn't a mobile project itself.",
					},
					"device": map[string]interface{}{
						"type":        "string",
						"description": "Specific device UDID (iOS) or serial (Android). Empty = first attached. Run wire_detect first to see your options.",
					},
					"platform": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"", "ios", "android"},
						"description": "Force a platform when the project supports both. Empty = auto-pick (native projects pick by stack; cross-platform projects pick ios on macOS, android elsewhere).",
					},
					"config": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"", "Debug", "Release"},
						"description": "Build configuration. Default Release (self-contained binary, no Metro). Pass Debug only when iterating with a running Metro dev server.",
					},
					"no_launch": map[string]interface{}{
						"type":        "boolean",
						"description": "Install the app but don't launch it after. Default false.",
					},
					"timeout_sec": map[string]interface{}{
						"type":        "integer",
						"description": "Hard timeout in seconds. Default 1800 (30 min). Cold-cache xcodebuild + pod install + hermesc compile easily hits 20+ min on first run.",
						"minimum":     60,
					},
				},
			},
		},
		{
			"name":        "wireless_detect",
			"description": "List WiFi-paired iPhones/iPads (xcrun devicectl over network) AND Android devices (adb devices), PLUS Android devices that are visible on the local network via mDNS but haven't been adb-paired with this machine yet. Each entry has a status field: 'paired' (ready for wireless_push) or 'visible-unpaired' (call wireless_setup_android first). Use this BEFORE wireless_push to confirm the target is paired; if you see visible-unpaired entries, prompt the user via yaver_ask_user to tap 'Pair device with pairing code' on the phone, then call wireless_setup_android with the 6-digit code.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "wireless_setup_android",
			"description": "First-time Android wireless pairing for AI agents. Prerequisite: the user has tapped 'Pair device with pairing code' on the phone (Settings → Developer options → Wireless debugging) and read off the 6-digit code. This tool polls mDNS for the pairing service, runs `adb pair`, then auto-resolves the matching connect endpoint and runs `adb connect`. Returns the post-setup device list so you can verify pairing in one round trip. Use yaver_ask_user to collect the code BEFORE calling this — never make up a code.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"code"},
				"properties": map[string]interface{}{
					"code": map[string]interface{}{
						"type":        "string",
						"description": "The 6-digit pairing code shown on the phone's 'Pair device with pairing code' screen.",
					},
					"poll_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "How long to wait for the pairing service to appear in mDNS. Default 120, max 300.",
						"minimum":     10,
						"maximum":     300,
					},
				},
			},
		},
		{
			"name":        "wireless_pair_android",
			"description": "Manual one-shot Android wireless pair when you already know the pair host:port (e.g. user typed it from the phone screen). Prefer wireless_setup_android when you don't have the host:port yet — it auto-discovers via mDNS. The pair port is DIFFERENT from the connect port shown on the main Wireless debugging screen. With auto_connect=true (default), this tool also resolves the matching connect entry and runs adb connect immediately, returning a single ready-to-use paired device.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"ip_port", "code"},
				"properties": map[string]interface{}{
					"ip_port": map[string]interface{}{
						"type":        "string",
						"description": "The PAIR host:port from the phone's 'Pair device with pairing code' screen — NOT the connect port from the main Wireless debugging screen.",
					},
					"code": map[string]interface{}{
						"type":        "string",
						"description": "The 6-digit pairing code shown next to the pair host:port on the phone.",
					},
					"auto_connect": map[string]interface{}{
						"type":        "boolean",
						"description": "After pairing, auto-resolve the connect endpoint via mDNS and run adb connect. Default true.",
					},
				},
			},
		},
		{
			"name":        "wireless_connect_android",
			"description": "Reconnect a previously-paired Android phone over WiFi. Use this when wireless_detect shows the phone as visible-unpaired but it was paired in a past session (e.g. after a phone reboot). Empty ip_port = auto-discover via mDNS.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"ip_port": map[string]interface{}{
						"type":        "string",
						"description": "Connect host:port from the phone's main Wireless debugging screen. Empty = auto-discover via mDNS.",
					},
				},
			},
		},
		{
			"name":        "wireless_push",
			"description": "Build a self-contained native binary (xcodebuild Release / gradle installRelease) and install it on a WIFI-paired phone via the agent's host machine. Same long-running build pipeline as wire_push, but routes through the wireless device picker. If no paired wireless device is found, the error includes a count of visible-unpaired devices so you can chain wireless_setup_android. Returns {ok, exit_code, device, platform, transport, stack, log_path, log_tail, elapsed_sec}.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Project path. Empty = the AI session's working directory (the dir Claude Code / Codex / opencode was started in) — typically what you want when iterating inside the app repo. Walks one level into mobile/, app/, apps/*, packages/* if the given path isn't a mobile project itself.",
					},
					"device": map[string]interface{}{
						"type":        "string",
						"description": "Specific device UDID (iOS) or wireless serial like '192.168.1.42:5555' (Android). Empty = first paired wireless device for the platform. Run wireless_detect first to see your options.",
					},
					"platform": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"", "ios", "android"},
						"description": "Force a platform when the project supports both. Empty = auto-pick.",
					},
					"config": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"", "Debug", "Release"},
						"description": "Build configuration. Default Release.",
					},
					"no_launch": map[string]interface{}{
						"type":        "boolean",
						"description": "Install but don't launch. Default false.",
					},
					"timeout_sec": map[string]interface{}{
						"type":        "integer",
						"description": "Hard timeout in seconds. Default 1800 (30 min).",
						"minimum":     60,
					},
				},
			},
		},
		{
			"name":        "fork_task",
			"description": "Switch the coding agent (claude/codex/opencode) for an existing task. Creates a NEW child task running on the requested runner with a bounded recent-context handoff (last few turns + assistant tail) — the parent task stays immutable. Use this instead of continue_task when the user wants a different runner/model/mode mid-conversation. Claude/Codex/OpenCode don't share session formats, so an in-place runner swap would corrupt session state. Returns the child task ID + runner + how many words of context were carried.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"task_id", "runner", "input"},
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "The parent task ID to fork from.",
					},
					"runner": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"claude", "codex", "opencode"},
						"description": "Target runner for the child task.",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "Optional model id. Empty = runner default.",
					},
					"mode": map[string]interface{}{
						"type":        "string",
						"description": "Optional opencode mode: 'build', 'plan', or any custom agent in the user's opencode.json. Empty = opencode defaultAgent. Other runners ignore.",
					},
					"input": map[string]interface{}{
						"type":        "string",
						"description": "User's new prompt for the forked agent.",
					},
					"context_words": map[string]interface{}{
						"type":        "integer",
						"description": "Word budget for the recent-context handoff. Default 1200. Clamped to [100, 5000].",
						"minimum":     100,
						"maximum":     5000,
					},
				},
			},
		},
		{
			"name":        "get_info",
			"description": "Get information about the connected development machine (hostname, working directory, version).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "yaver_auth_factory_reset",
			"description": "Reset local Yaver auth state on this machine, then restart sign-in from the canonical hosted backend. Useful when browser OAuth succeeded but the local agent kept validating against stale auth state or an old backend.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"headless": map[string]interface{}{
						"type":        "boolean",
						"description": "Use device-code auth after reset instead of browser auth.",
					},
				},
			},
		},
		// --- Runner Management ---
		{
			"name":        "list_runners",
			"description": "List available AI runners (Claude Code, Codex, Aider, etc.) with install status.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "switch_runner",
			"description": "Switch the active AI runner. Available: claude, codex, aider.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"runner_id"},
				"properties": map[string]interface{}{
					"runner_id": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"claude", "codex", "opencode"},
						"description": "Runner ID — claude, codex, or opencode.",
					},
				},
			},
		},
		// --- System & Config ---
		{
			"name":        "get_system_info",
			"description": "Get detailed system info: OS, arch, memory, hostname, running tasks.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "web_search",
			"description": "Search the web. Use this for current information: competitor research, market gaps, library docs, error messages, news. Provider defaults to DuckDuckGo (free, no key); set provider=google or provider=bing to use paid backends if GOOGLE_CSE_KEY+GOOGLE_CSE_CX or BING_API_KEY are configured. Returns title/url/snippet for each hit.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]interface{}{
					"query":    map[string]interface{}{"type": "string", "description": "The search query"},
					"provider": map[string]interface{}{"type": "string", "description": "duckduckgo (default) | google | bing | auto", "default": "duckduckgo"},
					"limit":    map[string]interface{}{"type": "integer", "description": "Max results (default 10, max 25)"},
				},
			},
		},
		{
			"name":        "get_config",
			"description": "Get agent configuration (sandbox, auto-start, relay, email, ACL peers).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "set_work_dir",
			"description": "Change the agent's working directory for task execution.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"path"},
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute path to the new working directory",
					},
				},
			},
		},
		{
			"name":        "list_projects",
			"description": "List discovered git projects on this machine.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "publish_config_get",
			"description": "Load a project's Yaver publish config (.yaver/publish.yaml). Returns the existing config or a scaffold preview if none exists yet.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dir": map[string]interface{}{
						"type":        "string",
						"description": "Project directory. Defaults to the agent work dir.",
					},
				},
			},
		},
		{
			"name":        "publish_run",
			"description": "Run a publish target from .yaver/publish.yaml. Local/self-hosted execution is primary; GitHub fallback is used only when allowed and requested.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dir": map[string]interface{}{
						"type":        "string",
						"description": "Project directory. Defaults to the agent work dir.",
					},
					"target": map[string]interface{}{
						"type":        "string",
						"description": "Target ID from .yaver/publish.yaml. Defaults to defaultTarget.",
					},
					"allow_github_fallback": map[string]interface{}{
						"type":        "boolean",
						"description": "Allow explicit GitHub workflow_dispatch fallback if the target/project permits it.",
					},
				},
			},
		},
		{
			"name":        "publish_submit",
			"description": "Alias of publish_run. Uses Yaver's uploader/register flow first, then the local submitter, then GitHub fallback only when allowed.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dir": map[string]interface{}{
						"type":        "string",
						"description": "Project directory. Defaults to the agent work dir.",
					},
					"target": map[string]interface{}{
						"type":        "string",
						"description": "Target ID from .yaver/publish.yaml. Defaults to defaultTarget.",
					},
					"allow_github_fallback": map[string]interface{}{
						"type":        "boolean",
						"description": "Allow explicit GitHub workflow_dispatch fallback if the target/project permits it.",
					},
				},
			},
		},
		{
			"name":        "publish_upload",
			"description": "Alias of publish_run for MCP clients that think in terms of an uploader. The target still archives/registers through Yaver first, then submits locally.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dir": map[string]interface{}{
						"type":        "string",
						"description": "Project directory. Defaults to the agent work dir.",
					},
					"target": map[string]interface{}{
						"type":        "string",
						"description": "Target ID from .yaver/publish.yaml. Defaults to defaultTarget.",
					},
					"allow_github_fallback": map[string]interface{}{
						"type":        "boolean",
						"description": "Allow explicit GitHub workflow_dispatch fallback if the target/project permits it.",
					},
				},
			},
		},
		{
			"name":        "publish_ci_dispatch",
			"description": "Alias of publish_run for CI-oriented clients. Use allow_github_fallback=true when you want GitHub dispatch as the fallback path after Yaver/local execution fails.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dir": map[string]interface{}{
						"type":        "string",
						"description": "Project directory. Defaults to the agent work dir.",
					},
					"target": map[string]interface{}{
						"type":        "string",
						"description": "Target ID from .yaver/publish.yaml. Defaults to defaultTarget.",
					},
					"allow_github_fallback": map[string]interface{}{
						"type":        "boolean",
						"description": "Allow explicit GitHub workflow_dispatch fallback if the target/project permits it.",
					},
				},
			},
		},
		{
			"name":        "publish_list",
			"description": "List recent publish runs started by this agent.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "publish_status",
			"description": "Get the full status of one publish run.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"run_id"},
				"properties": map[string]interface{}{
					"run_id": map[string]interface{}{
						"type":        "string",
						"description": "Publish run ID.",
					},
				},
			},
		},
		// --- iOS Install Method ---
		{
			"name":        "get_ios_install_method",
			"description": "Get the current iOS install method. Returns 'auto' (detect platform), 'native' (xcodebuild+xcrun), or 'bundle' (Hermes push to super-host). Auto resolves to native on macOS with Xcode, bundle otherwise.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "set_ios_install_method",
			"description": "Set the iOS install method. 'auto' = detect platform (native on macOS+Xcode, bundle otherwise), 'native' = always xcodebuild+xcrun devicectl, 'bundle' = always Hermes bytecode push to super-host container.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"method"},
				"properties": map[string]interface{}{
					"method": map[string]interface{}{
						"type":        "string",
						"description": "Install method: auto, native, or bundle",
						"enum":        []string{"auto", "native", "bundle"},
					},
					"persist": map[string]interface{}{
						"type":        "boolean",
						"description": "Save to config for future sessions (default: true)",
					},
				},
			},
		},
		// --- Relay Management ---
		{
			"name":        "get_relay_config",
			"description": "List configured relay servers.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "add_relay_server",
			"description": "Add a relay server for NAT traversal.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"quic_addr"},
				"properties": map[string]interface{}{
					"quic_addr": map[string]interface{}{"type": "string", "description": "QUIC address (host:port)"},
					"http_url":  map[string]interface{}{"type": "string", "description": "HTTP proxy URL"},
					"password":  map[string]interface{}{"type": "string", "description": "Relay password"},
					"label":     map[string]interface{}{"type": "string", "description": "Human-friendly label"},
				},
			},
		},
		{
			"name":        "remove_relay_server",
			"description": "Remove a relay server by ID.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"relay_id"},
				"properties": map[string]interface{}{
					"relay_id": map[string]interface{}{"type": "string", "description": "Relay server ID to remove"},
				},
			},
		},
		// --- Filesystem ---
		{
			"name":        "read_file",
			"description": "Read contents of a file. Limited to 100KB.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"path"},
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "File path (absolute or relative to work dir)"},
				},
			},
		},
		{
			"name":        "write_file",
			"description": "Write content to a file. Creates parent directories if needed.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"path", "content"},
				"properties": map[string]interface{}{
					"path":    map[string]interface{}{"type": "string", "description": "File path"},
					"content": map[string]interface{}{"type": "string", "description": "File content"},
				},
			},
		},
		{
			"name":        "list_directory",
			"description": "List files and directories at a path.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "Directory path (default: work dir)"},
				},
			},
		},
		// --- Email ---
		{
			"name":        "email_list_inbox",
			"description": "List inbox or sent emails. Requires email to be configured (yaver email setup).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"folder": map[string]interface{}{"type": "string", "description": "inbox, sent, or all", "enum": []string{"inbox", "sent", "all"}},
					"search": map[string]interface{}{"type": "string", "description": "Search in subject, sender, body"},
					"limit":  map[string]interface{}{"type": "integer", "description": "Max results (default 20)"},
				},
			},
		},
		{
			"name":        "email_get",
			"description": "Get full email details by ID.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"email_id"},
				"properties": map[string]interface{}{
					"email_id": map[string]interface{}{"type": "string", "description": "Email ID"},
				},
			},
		},
		{
			"name":        "email_send",
			"description": "Send a plain text email.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"to", "subject", "body"},
				"properties": map[string]interface{}{
					"to":      map[string]interface{}{"type": "string", "description": "Recipient email"},
					"subject": map[string]interface{}{"type": "string", "description": "Subject line"},
					"body":    map[string]interface{}{"type": "string", "description": "Email body (plain text)"},
					"cc":      map[string]interface{}{"type": "string", "description": "CC recipients (comma-separated)"},
				},
			},
		},
		{
			"name":        "email_sync",
			"description": "Sync emails from provider (Office 365 or Gmail) to local database.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "email_search",
			"description": "Search synced emails in local database.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "Search keyword"},
					"limit": map[string]interface{}{"type": "integer", "description": "Max results (default 20)"},
				},
			},
		},
		// --- ACL (Agent Communication Layer) ---
		{
			"name":        "acl_list_peers",
			"description": "List connected MCP peers (other AI tools, databases, services).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "acl_add_peer",
			"description": "Connect to another MCP server (local or remote).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"name", "url"},
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Peer name"},
					"url":  map[string]interface{}{"type": "string", "description": "MCP endpoint URL"},
					"auth": map[string]interface{}{"type": "string", "description": "Bearer token"},
				},
			},
		},
		{
			"name":        "acl_remove_peer",
			"description": "Disconnect from an MCP peer.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"peer_id"},
				"properties": map[string]interface{}{
					"peer_id": map[string]interface{}{"type": "string", "description": "Peer ID to remove"},
				},
			},
		},
		{
			"name":        "acl_list_peer_tools",
			"description": "List all tools available from a connected MCP peer.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"peer_id"},
				"properties": map[string]interface{}{
					"peer_id": map[string]interface{}{"type": "string", "description": "Peer ID"},
				},
			},
		},
		{
			"name":        "acl_call_peer_tool",
			"description": "Call a tool on a connected MCP peer.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"peer_id", "tool_name"},
				"properties": map[string]interface{}{
					"peer_id":   map[string]interface{}{"type": "string", "description": "Peer ID"},
					"tool_name": map[string]interface{}{"type": "string", "description": "Tool name"},
					"arguments": map[string]interface{}{"type": "object", "description": "Tool arguments"},
				},
			},
		},
		{
			"name":        "acl_health",
			"description": "Health check all connected MCP peers.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}

	// --- Tmux Session Management ---
	tmuxTools := []map[string]interface{}{
		{
			"name":        "tmux_list_sessions",
			"description": "List all tmux sessions on this machine with agent detection (claude, codex, aider, etc.) and their relationship to Yaver (adopted, forked-by-yaver, unrelated).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "tmux_adopt_session",
			"description": "Adopt an existing tmux session as a Yaver task. The session continues running and its output is streamed as task output. Useful for bringing pre-existing agent sessions under Yaver management.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_name"},
				"properties": map[string]interface{}{
					"session_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the tmux session to adopt",
					},
				},
			},
		},
		{
			"name":        "tmux_detach_session",
			"description": "Detach (stop monitoring) an adopted tmux session. The tmux session keeps running but Yaver stops tracking it. The task is marked as stopped.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"task_id"},
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "The Yaver task ID of the adopted session",
					},
				},
			},
		},
		{
			"name":        "tmux_send_input",
			"description": "Send keyboard input to an adopted tmux session. The input is sent via tmux send-keys followed by Enter.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"task_id", "input"},
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "The Yaver task ID of the adopted session",
					},
					"input": map[string]interface{}{
						"type":        "string",
						"description": "The text to send to the tmux session",
					},
				},
			},
		},
	}
	tools = append(tools, tmuxTools...)

	// --- Diagnostics & Status ---
	diagnosticTools := []map[string]interface{}{
		{
			"name":        "yaver_doctor",
			"description": "Run a comprehensive system health check — auth, agent, runners, relay servers, tunnels, network, tmux sessions. Like 'yaver doctor' on the CLI.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "yaver_status",
			"description": "Show auth status, agent info, current runner, relay servers, and connection details. Like 'yaver status' on the CLI.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "yaver_devices",
			"description": "List all registered devices across your account (dev machines, laptops, servers) with online/offline status.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "yaver_logs",
			"description": "View the last N lines of the agent log file.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"lines": map[string]interface{}{
						"type":        "integer",
						"description": "Number of log lines to return (default 50, max 500)",
					},
				},
			},
		},
		{
			"name":        "yaver_clear_logs",
			"description": "Clear the agent log file.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "yaver_help",
			"description": "Answer 'how do I do X with Yaver?' questions. Call this whenever the user wonders what Yaver can do, which feature replaces which SaaS, or how to set something up. Accepts a topic keyword. Topics include: overview, solo-stack (costs + savings summary), forms, newsletter, jobs, image, pdf, oauth, mail, shortener, waitlist, docs, meetings, wizard, tmux, relay, tunnel, mobile, mcp, runners, tasks, auth.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic": map[string]interface{}{
						"type":        "string",
						"description": "Topic keyword (e.g. 'newsletter', 'solo-stack', 'meetings'). Pass empty string for an overview.",
					},
				},
			},
		},
		{
			"name":        "yaver_onboard",
			"description": "Drive the first-run onboarding flow for a fresh Yaver install. Returns the ordered checklist of steps the user still needs to complete (auth, bootstrap secret, tunnel, runner, etc.) based on the current config state. Call this before doing any setup work so you know where to start.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "yaver_self_host_onboarding",
			"description": "High-level guided MCP flow for setting up Yaver on the user's own machine/VPS. Returns normie-friendly next steps for auth, serve, phone pairing, repo selection, runner setup, GitHub/GitLab credentials, and optional cloud upgrade. Can start GitHub/GitLab Device Flow when start_git_oauth=true.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"repo_query":        map[string]interface{}{"type": "string", "description": "Optional app/repo name the user wants to use"},
					"runner":            map[string]interface{}{"type": "string", "description": "Preferred coding runner: codex, claude-code, opencode"},
					"git_provider":      map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab", "auto"}, "description": "Provider for optional Device Flow"},
					"start_git_oauth":   map[string]interface{}{"type": "boolean", "description": "Start GitHub/GitLab Device Flow now; user approves in browser"},
					"include_cloud_cta": map[string]interface{}{"type": "boolean", "description": "Include the managed-cloud upgrade path in the response"},
				},
			},
		},
		{
			"name":        "yaver_managed_cloud_onboarding",
			"description": "High-level guided MCP flow for buying and onboarding a Yaver managed cloud machine. Always returns status and post-purchase repo/credential sync steps. Only creates a checkout URL when confirm_checkout=true AND accept_cost=true, after explicit user approval.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"repo_query":       map[string]interface{}{"type": "string", "description": "Optional app/repo name to deploy after the cloud machine is ready"},
					"machine_type":     map[string]interface{}{"type": "string", "enum": []string{"cpu", "gpu"}, "description": "cpu default; gpu for heavier/model workloads"},
					"region":           map[string]interface{}{"type": "string", "description": "eu default"},
					"confirm_checkout": map[string]interface{}{"type": "boolean", "description": "Set true only after the user asks to buy/start checkout"},
					"accept_cost":      map[string]interface{}{"type": "boolean", "description": "Must be true with confirm_checkout after explicit user approval of billable managed cloud"},
					"start_git_oauth":  map[string]interface{}{"type": "boolean", "description": "Optionally start GitHub/GitLab Device Flow while preparing cloud onboarding"},
					"git_provider":     map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab", "auto"}},
				},
			},
		},
		{
			"name":        "yaver_ping",
			"description": "Ping the agent to verify it's alive and measure round-trip time.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "agent_shutdown",
			"description": "Gracefully shut down the Yaver agent. All running tasks will be stopped.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"confirm": map[string]interface{}{
						"type":        "boolean",
						"description": "Must be true to confirm shutdown",
					},
				},
				"required": []string{"confirm"},
			},
		},
		{
			"name":        "infra_summary",
			"description": "Return the managed infra summary for this device: machine profile, services, relays, networking, sharing posture, and control capabilities.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "infra_service_action",
			"description": "Start, stop, restart, or inspect a managed service. Scope can be dev (.yaver/services.yaml) or system (systemd/brew services).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"scope", "name", "action"},
				"properties": map[string]interface{}{
					"scope":  map[string]interface{}{"type": "string", "description": "dev or system"},
					"name":   map[string]interface{}{"type": "string", "description": "Service name"},
					"action": map[string]interface{}{"type": "string", "description": "start, stop, restart, or status"},
				},
			},
		},
		{
			"name":        "infra_power",
			"description": "Run a managed power action. Supports agent_shutdown and host_reboot. Requires confirm=true.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"action", "confirm"},
				"properties": map[string]interface{}{
					"action":  map[string]interface{}{"type": "string", "description": "agent_shutdown or host_reboot"},
					"confirm": map[string]interface{}{"type": "boolean", "description": "Must be true to confirm the power action"},
				},
			},
		},
		{
			"name":        "machine_remove",
			"description": "Permanently remove Yaver from this owned host machine: unregister the device, remove auto-start service, wipe ~/.yaver, then shut the agent down. Requires confirm=true and phrase='delete my machine'.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"confirm", "phrase"},
				"properties": map[string]interface{}{
					"confirm": map[string]interface{}{"type": "boolean", "description": "Must be true to confirm permanent machine removal"},
					"phrase":  map[string]interface{}{"type": "string", "description": "Must equal 'delete my machine'"},
				},
			},
		},
	}
	tools = append(tools, diagnosticTools...)

	// --- Config Management ---
	configTools := []map[string]interface{}{
		{
			"name":        "config_set",
			"description": "Set a Yaver configuration value. Keys: auto-start, auto-update, headless-keep-awake, require-private-recovery.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"key", "value"},
				"properties": map[string]interface{}{
					"key":   map[string]interface{}{"type": "string", "description": "Config key (auto-start, auto-update, headless-keep-awake, require-private-recovery)"},
					"value": map[string]interface{}{"type": "string", "description": "Config value"},
				},
			},
		},
		{
			"name":        "relay_test",
			"description": "Test connectivity and latency to configured relay servers (or a specific URL).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{"type": "string", "description": "Optional: specific relay URL to test. If omitted, tests all configured relays."},
				},
			},
		},
		{
			"name":        "relay_set_password",
			"description": "Set the default relay server password used for all relay connections.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"password"},
				"properties": map[string]interface{}{
					"password": map[string]interface{}{"type": "string", "description": "The relay password"},
				},
			},
		},
		{
			"name":        "relay_clear_password",
			"description": "Remove the default relay server password.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
	tools = append(tools, configTools...)

	// --- Tunnel Management ---
	tunnelTools := []map[string]interface{}{
		{
			"name":        "tunnel_list",
			"description": "List configured Cloudflare Tunnels.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "tunnel_add",
			"description": "Add a Cloudflare Tunnel endpoint for NAT traversal.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"url"},
				"properties": map[string]interface{}{
					"url":              map[string]interface{}{"type": "string", "description": "Tunnel URL (e.g. https://my-tunnel.example.com)"},
					"cf_client_id":     map[string]interface{}{"type": "string", "description": "CF Access Service Token Client ID (optional)"},
					"cf_client_secret": map[string]interface{}{"type": "string", "description": "CF Access Service Token Client Secret (optional)"},
					"label":            map[string]interface{}{"type": "string", "description": "Human-readable label (optional)"},
				},
			},
		},
		{
			"name":        "tunnel_remove",
			"description": "Remove a Cloudflare Tunnel by ID or URL.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"tunnel_id"},
				"properties": map[string]interface{}{
					"tunnel_id": map[string]interface{}{"type": "string", "description": "Tunnel ID or URL to remove"},
				},
			},
		},
		{
			"name":        "tunnel_test",
			"description": "Test connectivity to configured Cloudflare Tunnels (or a specific URL).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{"type": "string", "description": "Optional: specific tunnel URL to test. If omitted, tests all configured tunnels."},
				},
			},
		},
	}
	tools = append(tools, tunnelTools...)

	// --- Session Transfer ---
	sessionTools := []map[string]interface{}{
		{
			"name":        "session_list",
			"description": "List AI agent sessions that can be transferred to another machine. Shows task ID, agent type, title, status, and whether the session is resumable.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "session_export",
			"description": "Export an AI agent session as a portable bundle. The bundle contains conversation history, agent-specific session files, and optionally workspace info (git patch or tar). Use this to prepare a session for transfer to another machine.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"task_id"},
				"properties": map[string]interface{}{
					"task_id":           map[string]interface{}{"type": "string", "description": "The task ID of the session to export"},
					"include_workspace": map[string]interface{}{"type": "boolean", "description": "Include workspace files in the bundle (default: false)"},
					"workspace_mode":    map[string]interface{}{"type": "string", "description": "How to include workspace: 'none', 'git' (git patch), or 'tar'. Default: 'git' if git repo, else 'none'."},
				},
			},
		},
		{
			"name":        "session_import",
			"description": "Import a session bundle that was exported from another machine. Creates a new task with the transferred session state. Supports Claude Code, Aider, Codex, Goose, Amp, OpenCode, and custom agents.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"bundle_json"},
				"properties": map[string]interface{}{
					"bundle_json": map[string]interface{}{"type": "string", "description": "The JSON string of the transfer bundle"},
					"work_dir":    map[string]interface{}{"type": "string", "description": "Target working directory (default: agent's work dir)"},
					"git_clone":   map[string]interface{}{"type": "boolean", "description": "Clone the git repo from the bundle's remote URL (default: false)"},
				},
			},
		},
		{
			"name":        "session_transfer",
			"description": "Transfer an AI agent session from THIS machine to another device in one step. The session (conversation history, agent state, optionally workspace) is packaged, sent to the target device, and imported there. The user can then continue working from the target device via mobile or desktop. Supports Claude Code, Aider, Codex, Goose, Amp, OpenCode sessions.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"task_id", "target_device"},
				"properties": map[string]interface{}{
					"task_id":           map[string]interface{}{"type": "string", "description": "The task ID of the session to transfer"},
					"target_device":     map[string]interface{}{"type": "string", "description": "Target device ID or hostname prefix (from your registered devices)"},
					"include_workspace": map[string]interface{}{"type": "boolean", "description": "Include workspace files (default: false)"},
					"workspace_mode":    map[string]interface{}{"type": "string", "description": "How to transfer workspace: 'none', 'git', or 'tar'. Default: 'git'."},
				},
			},
		},
	}
	tools = append(tools, sessionTools...)

	// --- Exec (Remote Command Execution) ---
	execTools := []map[string]interface{}{
		{
			"name":        "exec_command",
			"description": "Execute a shell command on this machine and return the output. Like SSH but local. Commands are validated through the sandbox (dangerous patterns like rm -rf / are blocked). Use this for quick commands — for long-running tasks, use create_task instead.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"command"},
				"properties": map[string]interface{}{
					"command":  map[string]interface{}{"type": "string", "description": "Shell command to execute"},
					"work_dir": map[string]interface{}{"type": "string", "description": "Working directory (default: agent's work dir)"},
					"timeout":  map[string]interface{}{"type": "integer", "description": "Timeout in seconds (default: 300, max: 3600)"},
				},
			},
		},
	}
	tools = append(tools, execTools...)

	// --- Notifications ---
	notifTools := []map[string]interface{}{
		{
			"name":        "notify",
			"description": "Send a notification message to configured channels (Telegram, Discord, Slack, Teams). Useful for alerting yourself about task completions, deployments, or any important events.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"message"},
				"properties": map[string]interface{}{
					"message": map[string]interface{}{"type": "string", "description": "Message to send"},
					"channel": map[string]interface{}{"type": "string", "description": "Specific channel: 'telegram', 'discord', 'slack', 'teams'. Omit to send to all."},
				},
			},
		},
		{
			"name":        "integrations_list",
			"description": "List all configured notification and developer integrations (Telegram, Discord, Slack, Teams, Linear, Jira, PagerDuty, Opsgenie, Email). Shows which are enabled and their settings.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "integrations_set",
			"description": "Configure a notification or developer integration. Saves to config and activates immediately. Channels: telegram, discord, slack, teams, linear, jira, pagerduty, opsgenie, email.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"channel", "config"},
				"properties": map[string]interface{}{
					"channel": map[string]interface{}{"type": "string", "description": "Integration channel name (telegram, discord, slack, teams, linear, jira, pagerduty, opsgenie, email)"},
					"config":  map[string]interface{}{"type": "object", "description": "Channel-specific config. Examples: {\"webhookUrl\":\"...\",\"enabled\":true} for Discord/Slack/Teams, {\"apiKey\":\"...\",\"teamId\":\"...\",\"enabled\":true} for Linear, {\"routingKey\":\"...\",\"enabled\":true,\"onFailOnly\":true} for PagerDuty"},
				},
			},
		},
		{
			"name":        "integrations_test",
			"description": "Send a test notification to verify an integration is working. Specify a channel or omit to test all.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"channel": map[string]interface{}{"type": "string", "description": "Channel to test (telegram, discord, slack, teams, linear, jira, pagerduty, opsgenie, email). Omit to test all."},
				},
			},
		},
	}
	tools = append(tools, notifTools...)

	// --- Task Scheduling ---
	scheduleTools := []map[string]interface{}{
		{
			"name":        "schedule_task",
			"description": "Schedule a task to run at a specific time or on a recurring basis. Supports one-shot (runAt), interval-based (repeatInterval in minutes), and cron expressions.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"title"},
				"properties": map[string]interface{}{
					"title":           map[string]interface{}{"type": "string", "description": "Task prompt"},
					"run_at":          map[string]interface{}{"type": "string", "description": "ISO8601 datetime for one-shot execution (e.g. '2026-03-22T15:00:00Z')"},
					"repeat_interval": map[string]interface{}{"type": "integer", "description": "Repeat every N minutes"},
					"cron":            map[string]interface{}{"type": "string", "description": "Cron expression (minute hour day month weekday), e.g. '0 9 * * 1-5' for weekdays at 9am"},
					"max_runs":        map[string]interface{}{"type": "integer", "description": "Maximum number of runs (0 = unlimited)"},
					"runner":          map[string]interface{}{"type": "string", "description": "Runner ID (claude, codex, aider, etc.)"},
				},
			},
		},
		{
			"name":        "list_schedules",
			"description": "List all scheduled and recurring tasks with their status, next run time, and history.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "cancel_schedule",
			"description": "Cancel/remove a scheduled task by ID.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"schedule_id"},
				"properties": map[string]interface{}{
					"schedule_id": map[string]interface{}{"type": "string", "description": "Schedule ID to cancel"},
				},
			},
		},
	}
	tools = append(tools, scheduleTools...)

	// --- Routines (MCP-only Verb-mode schedules) ---
	// Same Scheduler under the hood as schedule_task, but each routine
	// targets an ops verb on any machine instead of a TaskManager
	// task. Surface is intentionally MCP-only (no CLI / mobile / web /
	// docs) — see routines_mcp.go header for rationale.
	tools = append(tools, routineToolSchemas()...)

	// --- Utility Tools ---
	utilTools := []map[string]interface{}{
		{
			"name":        "search_files",
			"description": "Search for files by name pattern in a directory. Uses glob patterns (e.g. '*.go', 'test_*.py'). Skips node_modules, .git, vendor, etc.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"pattern"},
				"properties": map[string]interface{}{
					"pattern":     map[string]interface{}{"type": "string", "description": "Glob pattern to match filenames (e.g. '*.go', 'README*', '*.test.ts')"},
					"directory":   map[string]interface{}{"type": "string", "description": "Directory to search in (default: agent work dir)"},
					"max_results": map[string]interface{}{"type": "integer", "description": "Max results (default: 50)"},
				},
			},
		},
		{
			"name":        "search_content",
			"description": "Search for text content inside files (like grep/ripgrep). Returns matching lines with file paths and line numbers.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]interface{}{
					"query":       map[string]interface{}{"type": "string", "description": "Text or regex to search for"},
					"directory":   map[string]interface{}{"type": "string", "description": "Directory to search in (default: agent work dir)"},
					"max_results": map[string]interface{}{"type": "integer", "description": "Max results (default: 30)"},
				},
			},
		},
		{
			"name":        "screenshot",
			"description": "Take a screenshot of the current screen. Returns base64-encoded PNG. Works on macOS, Linux (with gnome-screenshot/scrot), and Windows.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "system_info",
			"description": "Get system information: hostname, OS, CPU count, disk usage, memory, load average. Useful for monitoring headless machines.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "git_info",
			"description": "Get git repository information. Operations: status (changed files), diff (diff stats), log (last 20 commits), branch (all branches), remote (remote URLs).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"operation"},
				"properties": map[string]interface{}{
					"operation": map[string]interface{}{"type": "string", "description": "Git operation: status, diff, log, branch, remote"},
					"directory": map[string]interface{}{"type": "string", "description": "Git repo directory (default: agent work dir)"},
				},
			},
		},
	}
	tools = append(tools, utilTools...)

	// --- Developer Tools ---
	devTools := []map[string]interface{}{
		// Docker
		{"name": "docker_ps", "description": "List running Docker containers.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "docker_logs", "description": "Get logs from a Docker container.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"container"}, "properties": map[string]interface{}{"container": map[string]interface{}{"type": "string", "description": "Container name or ID"}, "tail": map[string]interface{}{"type": "integer", "description": "Number of lines (default: 100)"}}}},
		{"name": "docker_exec", "description": "Execute a command inside a Docker container.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"container", "command"}, "properties": map[string]interface{}{"container": map[string]interface{}{"type": "string", "description": "Container name or ID"}, "command": map[string]interface{}{"type": "string", "description": "Command to execute"}}}},
		{"name": "docker_images", "description": "List Docker images on the machine.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "docker_compose", "description": "Run docker compose actions (up, down, ps, logs, restart).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "Action: up, down, ps, logs, restart"}, "directory": map[string]interface{}{"type": "string", "description": "Directory with docker-compose.yml"}}}},
		// Test runner
		{"name": "run_tests", "description": "Run the project's test suite. Auto-detects framework (go test, jest, vitest, pytest, cargo test, make test) or accepts a custom command.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"command": map[string]interface{}{"type": "string", "description": "Custom test command (auto-detected if empty)"}, "directory": map[string]interface{}{"type": "string", "description": "Project directory (default: agent work dir)"}}}},
		// HTTP client
		{"name": "http_request", "description": "Make an HTTP request (like curl). Returns status code and response body.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"url"}, "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string", "description": "Request URL"}, "method": map[string]interface{}{"type": "string", "description": "HTTP method (default: GET)"}, "headers": map[string]interface{}{"type": "object", "description": "Request headers as key-value pairs"}, "body": map[string]interface{}{"type": "string", "description": "Request body"}}}},
		// Log tail
		{"name": "tail_logs", "description": "Tail log files or system logs (journalctl on Linux, system.log on macOS).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "Log file path (default: system logs)"}, "lines": map[string]interface{}{"type": "integer", "description": "Number of lines (default: 100)"}}}},
		// Clipboard
		{"name": "clipboard_read", "description": "Read the system clipboard contents.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "clipboard_write", "description": "Write text to the system clipboard.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"content"}, "properties": map[string]interface{}{"content": map[string]interface{}{"type": "string", "description": "Text to copy to clipboard"}}}},
		// Process management
		{"name": "process_list", "description": "List running processes. Optionally filter by name.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"filter": map[string]interface{}{"type": "string", "description": "Filter processes by name"}}}},
		{"name": "process_kill", "description": "Kill a process by PID.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"pid"}, "properties": map[string]interface{}{"pid": map[string]interface{}{"type": "integer", "description": "Process ID"}, "signal": map[string]interface{}{"type": "string", "description": "Signal (default: TERM)"}}}},
		{"name": "port_check", "description": "Check what process is using a specific port.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"port"}, "properties": map[string]interface{}{"port": map[string]interface{}{"type": "integer", "description": "Port number to check"}}}},
		// Code quality
		{"name": "lint", "description": "Run linter on the project. Auto-detects: go vet, eslint, ruff/flake8, clippy.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Project directory"}, "tool": map[string]interface{}{"type": "string", "description": "Custom lint command (auto-detected if empty)"}}}},
		{"name": "format_code", "description": "Format code in the project. Auto-detects: gofmt, prettier, ruff/black, cargo fmt.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Project directory"}, "tool": map[string]interface{}{"type": "string", "description": "Custom format command (auto-detected if empty)"}}}},
		{"name": "type_check", "description": "Run type checker. Auto-detects: tsc, go build, mypy/pyright.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Project directory"}, "tool": map[string]interface{}{"type": "string", "description": "Custom type check command (auto-detected if empty)"}}}},
		// Package dependencies
		{"name": "deps_outdated", "description": "Check for outdated dependencies. Auto-detects: npm, yarn, pnpm, pip, cargo, go.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Project directory"}, "manager": map[string]interface{}{"type": "string", "description": "Package manager (auto-detected if empty)"}}}},
		{"name": "deps_audit", "description": "Audit dependencies for security vulnerabilities.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Project directory"}, "manager": map[string]interface{}{"type": "string", "description": "Package manager (auto-detected if empty)"}}}},
		{"name": "deps_list", "description": "List installed project dependencies.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Project directory"}, "manager": map[string]interface{}{"type": "string", "description": "Package manager (auto-detected if empty)"}}}},
		{"name": "mobile_project_status", "description": "Inspect whether a React Native / Expo project on this machine is ready for Yaver iPhone testing. Reports package manager, missing local tools, dependency-install state, Hermes compiler availability, and whether Hermes has been built before.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Project directory (default: agent work dir)"}}}},
		{"name": "mobile_project_prepare", "description": "Prepare a fresh React Native / Expo clone for Yaver testing by auto-installing project dependencies when the machine has the right package manager available. Returns readiness fields after the install attempt.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Project directory (default: agent work dir)"}}}},
		{
			"name":        "mobile_hermes_doctor",
			"description": "Agent-friendly doctor for the common React Native / Expo phone reload path. Resolves the mobile project inside a monorepo, checks local tools, dependency install state, Hermes compiler readiness, prior bundle state, and native-module compatibility, then returns the exact MCP next actions to prepare/build before reloading in Yaver mobile.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"directory": map[string]interface{}{"type": "string", "description": "Project or monorepo directory (default: agent work dir)"},
					"availableModules": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Optional native module names reported by the paired Yaver mobile runtime",
					},
					"availableModuleMap": map[string]interface{}{
						"type":                 "object",
						"additionalProperties": map[string]interface{}{"type": "string"},
						"description":          "Optional native module name to version map from the paired Yaver mobile runtime",
					},
				},
			},
		},
		{"name": "mobile_project_build", "description": "Start the project's dev server if needed and build the Hermes bundle that Yaver loads on the phone. This is the MCP path for a contributor on WSL/Linux/macOS to prepare a fresh Expo / React Native clone for real iPhone testing without TestFlight.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Project directory (default: agent work dir)"}, "framework": map[string]interface{}{"type": "string", "description": "Optional framework override (expo or react-native)"}, "platform": map[string]interface{}{"type": "string", "description": "Target platform (default: ios)"}}}},
		{
			"name":        "device_broadcast_command",
			"description": "Push a BlackBox command directly to a paired SDK device (or broadcast to all) without needing an active dev-server. This is the Path-C/8 fallback for the cross-device reload workflow: Phone A drives, Phone B receives, no Metro bundler involved — both phones just share a Yaver agent (managed-cloud, self-hosted, or local). Examples of useful commands: \"reload\", \"reload_bundle\", \"open_app\". Returns { ok, mode: \"scoped\"|\"broadcast\"|\"no_blackbox\", targetDeviceId?, reachedSession? }.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"command"},
				"properties": map[string]interface{}{
					"command":          map[string]interface{}{"type": "string", "description": "BlackBox command name — what the SDK listener acts on (e.g. \"reload\", \"reload_bundle\", \"open_app\")."},
					"data":             map[string]interface{}{"type": "object", "description": "Optional command payload passed through verbatim to the SDK listener."},
					"target_device_id": map[string]interface{}{"type": "string", "description": "When set, scoped to that one SDK session. Empty/omitted = broadcast to all subscribed devices."},
				},
			},
		},
		{
			"name":        "mobile_hermes_reload",
			"description": "Trigger a Hermes hot-reload of the React Native / Expo app currently under test. Thin wrapper over POST /dev/reload — computes a native-fingerprint delta against the dev-server baseline and broadcasts a `hot_reload` (or `native_rebuild_required`) command via the BlackBox SSE channel to all connected SDK devices. Use this when an MCP client (Claude Code, glass-terminal vibe chip, ChatGPT) wants the app reloaded without an LLM round-trip. Returns { ok, changeClass: \"js_only\"|\"native_rebuild_required\"|\"unknown\", nativeChanges?, nativeChangesDetected }.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target_device_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional SDK device id to scope the broadcast to. Empty/omitted = broadcast to ALL subscribed devices. Used by the Path-C cross-device flow where Phone A drives a reload on Phone B.",
					},
					"mode": map[string]interface{}{
						"type":        "string",
						"description": "Reload mode: \"dev\" (Metro fast-refresh, default) or \"bundle\" (push pre-built bundle).",
					},
				},
			},
		},
		// GitHub
		{"name": "github_prs", "description": "List pull requests from the current repo (requires gh CLI).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Repo directory"}, "state": map[string]interface{}{"type": "string", "description": "Filter: open, closed, merged, all (default: open)"}}}},
		{"name": "github_issues", "description": "List issues from the current repo (requires gh CLI).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Repo directory"}, "state": map[string]interface{}{"type": "string", "description": "Filter: open, closed, all (default: open)"}}}},
		{"name": "github_ci_status", "description": "Show recent GitHub Actions workflow runs and their status (requires gh CLI).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Repo directory"}}}},
	}
	tools = append(tools, devTools...)

	// --- Developer Tools 2 ---
	devTools2 := []map[string]interface{}{
		// Database
		{"name": "db_query", "description": "Execute a database query via CLI (sqlite3, psql, mysql, redis-cli). For adapter-routed queries use data_query.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"driver", "query"}, "properties": map[string]interface{}{"driver": map[string]interface{}{"type": "string", "description": "Database: sqlite, postgres, mysql, redis"}, "dsn": map[string]interface{}{"type": "string", "description": "Connection string (or path for SQLite). Uses DATABASE_URL env if empty for postgres."}, "query": map[string]interface{}{"type": "string", "description": "SQL query or Redis command"}}}},
		{"name": "db_schema", "description": "Show database schema/tables.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"driver"}, "properties": map[string]interface{}{"driver": map[string]interface{}{"type": "string", "description": "Database: sqlite, postgres, mysql"}, "dsn": map[string]interface{}{"type": "string", "description": "Connection string"}}}},
		// Network diagnostics
		{"name": "dns_lookup", "description": "DNS lookup for a hostname.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string", "description": "Hostname to lookup"}, "type": map[string]interface{}{"type": "string", "description": "Record type: A, AAAA, MX, CNAME, TXT (default: A)"}}}},
		{"name": "ping", "description": "Ping a host.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string", "description": "Host to ping"}, "count": map[string]interface{}{"type": "integer", "description": "Number of pings (default: 4)"}}}},
		{"name": "ssl_check", "description": "Check SSL/TLS certificate for a domain — expiry, issuer, SANs.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string", "description": "Domain to check (e.g. yaver.io)"}}}},
		{"name": "http_timing", "description": "Measure HTTP response time and get basic info.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"url"}, "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string", "description": "URL to measure"}}}},
		// Data tools
		{"name": "base64", "description": "Base64 encode or decode text.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action", "input"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "encode or decode"}, "input": map[string]interface{}{"type": "string", "description": "Text to encode/decode"}}}},
		{"name": "hash", "description": "Hash text with MD5 or SHA256.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"input"}, "properties": map[string]interface{}{"input": map[string]interface{}{"type": "string", "description": "Text to hash"}, "algorithm": map[string]interface{}{"type": "string", "description": "md5 or sha256 (default: sha256)"}}}},
		{"name": "uuid", "description": "Generate a new UUID v4.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "jq", "description": "Query/transform JSON with jq expressions.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"expression", "input"}, "properties": map[string]interface{}{"expression": map[string]interface{}{"type": "string", "description": "jq expression (e.g. '.data[] | .name')"}, "input": map[string]interface{}{"type": "string", "description": "JSON input"}}}},
		{"name": "regex_test", "description": "Test a regex pattern against input text.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"pattern", "input"}, "properties": map[string]interface{}{"pattern": map[string]interface{}{"type": "string", "description": "Regex pattern"}, "input": map[string]interface{}{"type": "string", "description": "Text to match against"}}}},
		// Archive
		{"name": "archive_create", "description": "Create a zip or tar.gz archive.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"source"}, "properties": map[string]interface{}{"source": map[string]interface{}{"type": "string", "description": "File or directory to archive"}, "output": map[string]interface{}{"type": "string", "description": "Output filename (auto-generated if empty)"}, "format": map[string]interface{}{"type": "string", "description": "zip or tar.gz (default: tar.gz)"}}}},
		{"name": "archive_extract", "description": "Extract a zip or tar.gz archive.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"path"}, "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "Archive file path"}, "destination": map[string]interface{}{"type": "string", "description": "Extraction directory (default: current)"}}}},
		// System services
		{"name": "service_status", "description": "Check status of a system service (systemd on Linux, brew services on macOS).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string", "description": "Service name (e.g. nginx, postgresql, docker)"}}}},
		{"name": "service_action", "description": "Start, stop, restart, enable, or disable a system service.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name", "action"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string", "description": "Service name"}, "action": map[string]interface{}{"type": "string", "description": "start, stop, restart, enable, disable"}}}},
		{"name": "service_list", "description": "List system services and their status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Benchmark
		{"name": "benchmark", "description": "Run project benchmarks. Auto-detects: go bench, cargo bench, npm bench.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"command": map[string]interface{}{"type": "string", "description": "Custom benchmark command (auto-detected if empty)"}, "directory": map[string]interface{}{"type": "string", "description": "Project directory"}}}},
		// Diff
		{"name": "diff", "description": "Compare two files and show differences.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"path_a", "path_b"}, "properties": map[string]interface{}{"path_a": map[string]interface{}{"type": "string", "description": "First file path"}, "path_b": map[string]interface{}{"type": "string", "description": "Second file path"}}}},
		// Environment
		{"name": "env_list", "description": "List environment variables (secrets are masked).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"filter": map[string]interface{}{"type": "string", "description": "Filter by name (case-insensitive)"}}}},
		{"name": "env_read", "description": "Read a .env file (secrets are masked).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "Path to .env file (default: .env)"}}}},
		// Crontab
		{"name": "crontab", "description": "List or add system crontab entries.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "list or add (default: list)"}, "entry": map[string]interface{}{"type": "string", "description": "Cron entry to add (required for 'add')"}}}},
		// Cloud CLI
		{"name": "cloud_cli", "description": "Run AWS, GCP, or Azure CLI commands.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider", "args"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string", "description": "aws, gcloud, or az"}, "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "CLI arguments (e.g. ['s3', 'ls'])"}}}},
	}
	tools = append(tools, devTools2...)

	// --- Lifestyle & Home Automation ---
	lifestyleTools := []map[string]interface{}{
		// Home Assistant
		{"name": "ha_states", "description": "Get Home Assistant entity states. Control Xiaomi, Philips Hue, or any HA-connected device. Filter by entity type (light, switch, vacuum, climate, sensor, etc.).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"filter": map[string]interface{}{"type": "string", "description": "Filter entities (e.g. 'light', 'vacuum', 'switch', 'climate', 'sensor')"}, "url": map[string]interface{}{"type": "string", "description": "HA URL (default: http://homeassistant.local:8123)"}, "token": map[string]interface{}{"type": "string", "description": "HA long-lived access token"}}}},
		{"name": "ha_service", "description": "Call a Home Assistant service — turn on/off lights, start vacuum, set thermostat, trigger scenes. Works with Xiaomi, Hue, IKEA, and all HA integrations.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"domain", "service"}, "properties": map[string]interface{}{"domain": map[string]interface{}{"type": "string", "description": "Service domain (e.g. light, switch, vacuum, climate, scene, automation)"}, "service": map[string]interface{}{"type": "string", "description": "Service name (e.g. turn_on, turn_off, start, set_temperature, toggle)"}, "data": map[string]interface{}{"type": "object", "description": "Service data (e.g. {\"entity_id\": \"vacuum.xiaomi\", \"brightness\": 255})"}, "url": map[string]interface{}{"type": "string", "description": "HA URL"}, "token": map[string]interface{}{"type": "string", "description": "HA token"}}}},
		{"name": "ha_toggle", "description": "Toggle a Home Assistant entity on/off (light, switch, vacuum, etc.).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"entity_id"}, "properties": map[string]interface{}{"entity_id": map[string]interface{}{"type": "string", "description": "Entity ID (e.g. vacuum.xiaomi_roborock, light.living_room, switch.desk_lamp)"}, "url": map[string]interface{}{"type": "string", "description": "HA URL"}, "token": map[string]interface{}{"type": "string", "description": "HA token"}}}},
		// MQTT
		{"name": "mqtt_publish", "description": "Publish an MQTT message (for IoT devices, home automation).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"topic", "message"}, "properties": map[string]interface{}{"topic": map[string]interface{}{"type": "string", "description": "MQTT topic"}, "message": map[string]interface{}{"type": "string", "description": "Message payload"}, "broker": map[string]interface{}{"type": "string", "description": "MQTT broker (default: localhost)"}}}},
		// Desktop control
		{"name": "notify", "description": "Send a desktop notification.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"title", "message"}, "properties": map[string]interface{}{"title": map[string]interface{}{"type": "string", "description": "Notification title"}, "message": map[string]interface{}{"type": "string", "description": "Notification body"}}}},
		{"name": "open_url", "description": "Open a URL in the default browser.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"url"}, "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string", "description": "URL to open"}}}},
		{"name": "volume", "description": "Get or set system volume.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "get, set, mute, or unmute"}, "level": map[string]interface{}{"type": "integer", "description": "Volume level 0-100 (for set)"}}}},
		{"name": "screen_lock", "description": "Lock the screen.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "say", "description": "Text-to-speech — speak text aloud.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"text"}, "properties": map[string]interface{}{"text": map[string]interface{}{"type": "string", "description": "Text to speak"}}}},
		{"name": "brightness", "description": "Get or set screen brightness (macOS).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "get or set"}, "level": map[string]interface{}{"type": "integer", "description": "Brightness 0-100 (for set)"}}}},
		// Music
		{"name": "music", "description": "Control music playback (Spotify on macOS, playerctl on Linux).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "play, pause, next, previous, now_playing"}}}},
		// Weather
		{"name": "weather", "description": "Get current weather for a location.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"location": map[string]interface{}{"type": "string", "description": "City name (default: auto-detect)"}}}},
		// System extras
		{"name": "battery", "description": "Get battery status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "disk_usage", "description": "Show disk usage.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "Path to check (default: /)"}}}},
		{"name": "wifi_info", "description": "Get WiFi network information.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "public_ip", "description": "Get public IP address.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "uptime", "description": "Show system uptime.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "speed_test", "description": "Run an internet speed test.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "site_check", "description": "Check if a website is up and measure latency.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"url"}, "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string", "description": "URL to check"}}}},
		// Utilities
		{"name": "password_gen", "description": "Generate a secure random password.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"length": map[string]interface{}{"type": "integer", "description": "Password length (default: 24)"}, "no_symbols": map[string]interface{}{"type": "boolean", "description": "Omit special characters"}}}},
		{"name": "qr_code", "description": "Generate a QR code from text.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"text"}, "properties": map[string]interface{}{"text": map[string]interface{}{"type": "string", "description": "Text to encode"}}}},
		{"name": "timer", "description": "Set a timer with desktop notification when done.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"seconds"}, "properties": map[string]interface{}{"seconds": map[string]interface{}{"type": "integer", "description": "Timer duration in seconds"}, "label": map[string]interface{}{"type": "string", "description": "Timer label"}}}},
		{"name": "calculate", "description": "Evaluate a math expression.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"expression"}, "properties": map[string]interface{}{"expression": map[string]interface{}{"type": "string", "description": "Math expression (e.g. '2^10', 'sqrt(144)', '3.14 * 5^2')"}}}},
		{"name": "world_clock", "description": "Show current time in multiple timezones.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"timezones": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Timezone names (default: UTC, New York, London, Istanbul, Tokyo)"}}}},
		{"name": "countdown", "description": "Count down to a specific date.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"date"}, "properties": map[string]interface{}{"date": map[string]interface{}{"type": "string", "description": "Target date (e.g. 2026-04-01)"}}}},
		{"name": "convert_units", "description": "Convert between units (temperature, distance, weight, data sizes).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"value", "from", "to"}, "properties": map[string]interface{}{"value": map[string]interface{}{"type": "number", "description": "Value to convert"}, "from": map[string]interface{}{"type": "string", "description": "Source unit (c, f, km, mi, kg, lb, gb, mb, bytes)"}, "to": map[string]interface{}{"type": "string", "description": "Target unit"}}}},
	}
	tools = append(tools, lifestyleTools...)

	// --- IoT & Smart Devices ---
	iotTools := []map[string]interface{}{
		// Philips Hue (local bridge, no cloud)
		{"name": "hue_lights", "description": "List all Philips Hue lights on your bridge.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"bridge_ip", "api_key"}, "properties": map[string]interface{}{"bridge_ip": map[string]interface{}{"type": "string", "description": "Hue bridge IP"}, "api_key": map[string]interface{}{"type": "string", "description": "Hue API key"}}}},
		{"name": "hue_control", "description": "Control a Philips Hue light — on, off, toggle, brightness, color.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"bridge_ip", "api_key", "light_id", "action"}, "properties": map[string]interface{}{"bridge_ip": map[string]interface{}{"type": "string"}, "api_key": map[string]interface{}{"type": "string"}, "light_id": map[string]interface{}{"type": "string", "description": "Light number (e.g. '1')"}, "action": map[string]interface{}{"type": "string", "description": "on, off, toggle, brightness, color"}, "brightness": map[string]interface{}{"type": "integer", "description": "0-254 for brightness, 0-65535 for color hue"}}}},
		{"name": "hue_scenes", "description": "List Hue scenes.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"bridge_ip", "api_key"}, "properties": map[string]interface{}{"bridge_ip": map[string]interface{}{"type": "string"}, "api_key": map[string]interface{}{"type": "string"}}}},
		// Shelly (local HTTP, no hub)
		{"name": "shelly_status", "description": "Get Shelly device status (smart plug, relay, light).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"ip"}, "properties": map[string]interface{}{"ip": map[string]interface{}{"type": "string", "description": "Shelly device IP"}}}},
		{"name": "shelly_control", "description": "Control a Shelly relay/plug — on, off, toggle.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"ip", "action"}, "properties": map[string]interface{}{"ip": map[string]interface{}{"type": "string"}, "action": map[string]interface{}{"type": "string", "description": "on, off, toggle"}, "channel": map[string]interface{}{"type": "integer", "description": "Relay channel (default: 0)"}}}},
		{"name": "shelly_power", "description": "Get power consumption from a Shelly device.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"ip"}, "properties": map[string]interface{}{"ip": map[string]interface{}{"type": "string"}}}},
		// Elgato Key Light
		{"name": "elgato_status", "description": "Get Elgato Key Light status (for streaming/video calls).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ip": map[string]interface{}{"type": "string", "description": "Key Light IP (default: elgato-key-light.local)"}}}},
		{"name": "elgato_control", "description": "Control Elgato Key Light — on/off, brightness, color temperature.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ip": map[string]interface{}{"type": "string"}, "on": map[string]interface{}{"type": "boolean", "description": "Turn on/off"}, "brightness": map[string]interface{}{"type": "integer", "description": "Brightness 0-100"}, "temperature": map[string]interface{}{"type": "integer", "description": "Color temp 143-344 (warm to cool)"}}}},
		// Nanoleaf
		{"name": "nanoleaf", "description": "Control Nanoleaf light panels — on, off, brightness, effects.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"ip", "token", "action"}, "properties": map[string]interface{}{"ip": map[string]interface{}{"type": "string"}, "token": map[string]interface{}{"type": "string", "description": "Nanoleaf auth token"}, "action": map[string]interface{}{"type": "string", "description": "on, off, brightness, effects, status"}, "brightness": map[string]interface{}{"type": "integer"}}}},
		// Tasmota
		{"name": "tasmota", "description": "Send commands to Tasmota-flashed devices (smart plugs, relays).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"ip", "command"}, "properties": map[string]interface{}{"ip": map[string]interface{}{"type": "string"}, "command": map[string]interface{}{"type": "string", "description": "Tasmota command (e.g. Power ON, Status, Power TOGGLE)"}}}},
		// Govee LED strips
		{"name": "govee_devices", "description": "List Govee devices (LED strips, lights).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"api_key"}, "properties": map[string]interface{}{"api_key": map[string]interface{}{"type": "string", "description": "Govee API key"}}}},
		{"name": "govee_control", "description": "Control Govee lights/LED strips — on, off, brightness, color.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"api_key", "device", "model", "action"}, "properties": map[string]interface{}{"api_key": map[string]interface{}{"type": "string"}, "device": map[string]interface{}{"type": "string", "description": "Device address"}, "model": map[string]interface{}{"type": "string", "description": "Device model"}, "action": map[string]interface{}{"type": "string", "description": "on, off, brightness, color"}, "brightness": map[string]interface{}{"type": "integer"}, "color": map[string]interface{}{"type": "object", "description": "{r: 255, g: 0, b: 0}"}}}},
		// Wake on LAN
		{"name": "wake_on_lan", "description": "Send a Wake-on-LAN magic packet to wake up a machine.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"mac"}, "properties": map[string]interface{}{"mac": map[string]interface{}{"type": "string", "description": "MAC address (e.g. AA:BB:CC:DD:EE:FF)"}}}},
		// Apple Shortcuts
		{"name": "run_shortcut", "description": "Run an Apple Shortcut (macOS only).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string", "description": "Shortcut name"}, "input": map[string]interface{}{"type": "string", "description": "Input text"}}}},
		{"name": "list_shortcuts", "description": "List available Apple Shortcuts (macOS only).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// ADB (Android)
		{"name": "adb_devices", "description": "List connected Android devices/emulators.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "adb_command", "description": "Run a command on an Android device via ADB.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"command"}, "properties": map[string]interface{}{"command": map[string]interface{}{"type": "string", "description": "Shell command"}, "device": map[string]interface{}{"type": "string", "description": "Device serial (optional)"}}}},
		{"name": "adb_screenshot", "description": "Take a screenshot from an Android device.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device": map[string]interface{}{"type": "string"}}}},
		// Sonos
		{"name": "sonos_discover", "description": "Discover Sonos speakers on the network.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "sonos_control", "description": "Control a Sonos speaker — play, pause, next, previous, volume.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"ip", "action"}, "properties": map[string]interface{}{"ip": map[string]interface{}{"type": "string", "description": "Sonos speaker IP"}, "action": map[string]interface{}{"type": "string", "description": "play, pause, next, previous, volume_up, volume_down, status"}}}},
	}
	tools = append(tools, iotTools...)

	// --- Productivity & Sharing ---
	prodTools := []map[string]interface{}{
		{"name": "standup", "description": "Generate a daily standup from recent git commits. Shows what you did, files changed — ready to paste in Slack.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "days": map[string]interface{}{"type": "integer", "description": "Days to look back (default: 1)"}}}},
		{"name": "create_gist", "description": "Create a GitHub Gist to share code snippets.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"content"}, "properties": map[string]interface{}{"content": map[string]interface{}{"type": "string", "description": "Code/text content"}, "filename": map[string]interface{}{"type": "string", "description": "Filename (default: snippet.txt)"}, "description": map[string]interface{}{"type": "string"}, "public": map[string]interface{}{"type": "boolean", "description": "Public gist (default: false)"}}}},
		{"name": "changelog", "description": "Generate a changelog from git history between two refs/tags.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "from": map[string]interface{}{"type": "string", "description": "Start ref/tag (default: previous tag)"}, "to": map[string]interface{}{"type": "string", "description": "End ref (default: HEAD)"}}}},
		{"name": "commit_message", "description": "Get the current git diff to help generate a conventional commit message.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "gitignore", "description": "Generate a .gitignore file for specified languages/frameworks.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"languages"}, "properties": map[string]interface{}{"languages": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Languages (e.g. go, node, python, rust, java)"}}}},
		{"name": "license", "description": "Generate a license file (MIT, Apache, GPL).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"type"}, "properties": map[string]interface{}{"type": map[string]interface{}{"type": "string", "description": "mit, apache, gpl"}, "author": map[string]interface{}{"type": "string"}, "year": map[string]interface{}{"type": "integer"}}}},
		{"name": "color", "description": "Convert colors between hex, RGB, and HSL.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"input"}, "properties": map[string]interface{}{"input": map[string]interface{}{"type": "string", "description": "Color as hex (#ff0000), shorthand (#f00), or name (red)"}}}},
		{"name": "figlet", "description": "Generate ASCII art text banners.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"text"}, "properties": map[string]interface{}{"text": map[string]interface{}{"type": "string"}}}},
		{"name": "lorem_ipsum", "description": "Generate placeholder text.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"paragraphs": map[string]interface{}{"type": "integer", "description": "Number of paragraphs (default: 1)"}}}},
		{"name": "tldr", "description": "Quick command reference (like man pages but shorter).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"command"}, "properties": map[string]interface{}{"command": map[string]interface{}{"type": "string", "description": "Command to look up"}}}},
		{"name": "github_badge", "description": "Generate GitHub badges (CI, stars, license, release) for your README.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "invite", "description": "Share Yaver with a colleague — copies invite link, opens email, or generates a Slack message.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"method": map[string]interface{}{"type": "string", "description": "clipboard, email, or slack"}, "recipient": map[string]interface{}{"type": "string", "description": "Email address (for email method)"}}}},
		{"name": "git_stats", "description": "Show your git contribution stats — commits, lines, top files, languages.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "days": map[string]interface{}{"type": "integer", "description": "Days to analyze (default: 30)"}}}},
	}
	tools = append(tools, prodTools...)

	// --- Location & Lifestyle ---
	locationTools := []map[string]interface{}{
		{"name": "ev_charging", "description": "Find EV charging stations nearby. Filter by network (Tesla, IONITY, Trugo, ChargePoint, etc.), connector type, country, and minimum power. Covers Turkey (Trugo/Togg, Eşarj, ZES, Sharz.net), US (Tesla, Electrify America, ChargePoint, EVgo), and Europe (IONITY, Fastned, Shell, BP Pulse).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"lat", "lon"}, "properties": map[string]interface{}{"lat": map[string]interface{}{"type": "number"}, "lon": map[string]interface{}{"type": "number"}, "radius": map[string]interface{}{"type": "integer", "description": "Search radius in km (default: 10)"}, "connector_type": map[string]interface{}{"type": "string", "description": "Connector type ID (use ev_connector_types to see list)"}, "network": map[string]interface{}{"type": "string", "description": "Network filter: tesla, ionity, chargepoint, evgo, shell, bp"}, "country": map[string]interface{}{"type": "string", "description": "Country code or name (e.g. TR, turkey, US, DE)"}, "min_power_kw": map[string]interface{}{"type": "integer", "description": "Minimum charging power in kW (e.g. 50 for DC fast only)"}}}},
		{"name": "ev_networks", "description": "List EV charging networks by country — Turkey (Trugo/Togg, Eşarj, ZES, Sharz.net, Voltrun), US (Tesla, Electrify America, ChargePoint, EVgo), Europe (IONITY, Fastned, Shell, BP Pulse).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"country": map[string]interface{}{"type": "string", "description": "TR/turkey, US/usa, EU/europe (all if empty)"}}}},
		{"name": "ev_connector_types", "description": "Reference: EV connector types (CCS2, Type 2, CHAdeMO, Tesla NACS, etc.) with regions and max power.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "nobetci_eczane", "description": "Find on-duty pharmacies (nöbetçi eczane) in Turkish cities.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"city"}, "properties": map[string]interface{}{"city": map[string]interface{}{"type": "string", "description": "City name (e.g. istanbul, ankara, izmir, bursa)"}, "district": map[string]interface{}{"type": "string", "description": "District/ilçe (e.g. kadıköy, beşiktaş, çankaya)"}}}},
		{"name": "eczane_nearby", "description": "Find pharmacies near a location (worldwide, OpenStreetMap).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"lat", "lon"}, "properties": map[string]interface{}{"lat": map[string]interface{}{"type": "number"}, "lon": map[string]interface{}{"type": "number"}, "radius": map[string]interface{}{"type": "integer", "description": "Radius in meters (default: 2000)"}}}},
		{"name": "places_search", "description": "Search for places, addresses, businesses (OpenStreetMap, free).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string", "description": "Search query (e.g. 'coffee shop Istanbul')"}, "lat": map[string]interface{}{"type": "number", "description": "Center latitude"}, "lon": map[string]interface{}{"type": "number", "description": "Center longitude"}}}},
		{"name": "restaurants", "description": "Find restaurants nearby (OpenStreetMap, free).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"lat", "lon"}, "properties": map[string]interface{}{"lat": map[string]interface{}{"type": "number"}, "lon": map[string]interface{}{"type": "number"}, "radius": map[string]interface{}{"type": "integer", "description": "Radius in meters (default: 1000)"}, "cuisine": map[string]interface{}{"type": "string", "description": "Cuisine filter (e.g. italian, turkish, sushi)"}}}},
		{"name": "hotels", "description": "Find hotels and accommodation nearby (OpenStreetMap, free).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"lat", "lon"}, "properties": map[string]interface{}{"lat": map[string]interface{}{"type": "number"}, "lon": map[string]interface{}{"type": "number"}, "radius": map[string]interface{}{"type": "integer", "description": "Radius in meters (default: 2000)"}}}},
		{"name": "geocode", "description": "Get coordinates from an address.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"address"}, "properties": map[string]interface{}{"address": map[string]interface{}{"type": "string", "description": "Address to geocode"}}}},
		{"name": "directions", "description": "Get driving directions and distance between two points.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"from_lat", "from_lon", "to_lat", "to_lon"}, "properties": map[string]interface{}{"from_lat": map[string]interface{}{"type": "number"}, "from_lon": map[string]interface{}{"type": "number"}, "to_lat": map[string]interface{}{"type": "number"}, "to_lon": map[string]interface{}{"type": "number"}}}},
		{"name": "news", "description": "Get latest tech news from HackerNews, Lobsters, dev.to, TechCrunch, or any RSS feed.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"source"}, "properties": map[string]interface{}{"source": map[string]interface{}{"type": "string", "description": "hackernews, lobsters, devto, techcrunch, verge, ars, reddit_prog, or any RSS URL"}}}},
		{"name": "stock_price", "description": "Get current stock price (Yahoo Finance, free).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"symbol"}, "properties": map[string]interface{}{"symbol": map[string]interface{}{"type": "string", "description": "Stock symbol (e.g. AAPL, TSLA, GOOGL)"}}}},
		{"name": "translate", "description": "Translate text between languages (LibreTranslate, free).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"text"}, "properties": map[string]interface{}{"text": map[string]interface{}{"type": "string"}, "from": map[string]interface{}{"type": "string", "description": "Source language (default: auto)"}, "to": map[string]interface{}{"type": "string", "description": "Target language (default: en)"}, "api_url": map[string]interface{}{"type": "string", "description": "Custom LibreTranslate URL"}}}},
	}
	tools = append(tools, locationTools...)

	// --- Daily Dev Tools ---
	dailyTools := []map[string]interface{}{
		{"name": "crypto_price", "description": "Get cryptocurrency prices (CoinGecko, free).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"coins": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Coin IDs (default: bitcoin, ethereum). Use: bitcoin, ethereum, solana, cardano, dogecoin, etc."}}}},
		{"name": "currency_exchange", "description": "Convert currency (ECB rates, free).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"amount"}, "properties": map[string]interface{}{"amount": map[string]interface{}{"type": "number"}, "from": map[string]interface{}{"type": "string", "description": "Source currency (default: USD)"}, "to": map[string]interface{}{"type": "string", "description": "Target currency (default: EUR)"}}}},
		{"name": "npm_info", "description": "Get npm package info — version, downloads, description.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"package"}, "properties": map[string]interface{}{"package": map[string]interface{}{"type": "string", "description": "npm package name"}}}},
		{"name": "github_trending", "description": "Show trending GitHub repositories this week.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"language": map[string]interface{}{"type": "string", "description": "Filter by language (e.g. go, rust, python)"}, "since": map[string]interface{}{"type": "string", "description": "daily, weekly, monthly"}}}},
		{"name": "jwt_decode", "description": "Decode a JWT token — shows header, payload, expiry.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"token"}, "properties": map[string]interface{}{"token": map[string]interface{}{"type": "string", "description": "JWT token string"}}}},
		{"name": "epoch", "description": "Convert between Unix timestamps and human-readable dates.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"input": map[string]interface{}{"type": "string", "description": "Unix timestamp, date string, or 'now'"}}}},
		{"name": "cron_explain", "description": "Explain a cron expression in plain English.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"expression"}, "properties": map[string]interface{}{"expression": map[string]interface{}{"type": "string", "description": "Cron expression (5 fields: min hour dom month dow)"}}}},
		{"name": "http_status", "description": "Look up what an HTTP status code means.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"code"}, "properties": map[string]interface{}{"code": map[string]interface{}{"type": "integer", "description": "HTTP status code (e.g. 418)"}}}},
		{"name": "whois", "description": "Look up domain registration info.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"domain"}, "properties": map[string]interface{}{"domain": map[string]interface{}{"type": "string"}}}},
		{"name": "ip_geo", "description": "Geolocate an IP address — city, country, ISP, coordinates.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ip": map[string]interface{}{"type": "string", "description": "IP address (default: your public IP)"}}}},
		{"name": "subnet_calc", "description": "Calculate subnet details from CIDR notation.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"cidr"}, "properties": map[string]interface{}{"cidr": map[string]interface{}{"type": "string", "description": "CIDR (e.g. 192.168.1.0/24)"}}}},
		{"name": "fake_data", "description": "Generate fake test data — users, emails, addresses, credit cards.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"type": map[string]interface{}{"type": "string", "description": "user, email, address, uuid, credit_card (default: user)"}, "count": map[string]interface{}{"type": "integer", "description": "Number of records (max: 20)"}}}},
		{"name": "domain_check", "description": "Check if a domain is available or registered.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"domain"}, "properties": map[string]interface{}{"domain": map[string]interface{}{"type": "string", "description": "Domain to check (e.g. myapp.dev)"}}}},
	}
	tools = append(tools, dailyTools...)

	// --- Infrastructure & SaaS ---
	infraTools := []map[string]interface{}{
		// Kubernetes
		{"name": "k8s_pods", "description": "List Kubernetes pods.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"namespace": map[string]interface{}{"type": "string"}, "context": map[string]interface{}{"type": "string"}}}},
		{"name": "k8s_logs", "description": "Get logs from a Kubernetes pod.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"pod"}, "properties": map[string]interface{}{"pod": map[string]interface{}{"type": "string"}, "namespace": map[string]interface{}{"type": "string"}, "context": map[string]interface{}{"type": "string"}, "container": map[string]interface{}{"type": "string"}, "tail": map[string]interface{}{"type": "integer"}}}},
		{"name": "k8s_describe", "description": "Describe a Kubernetes resource.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"resource", "name"}, "properties": map[string]interface{}{"resource": map[string]interface{}{"type": "string", "description": "pod, service, deployment, etc."}, "name": map[string]interface{}{"type": "string"}, "namespace": map[string]interface{}{"type": "string"}, "context": map[string]interface{}{"type": "string"}}}},
		{"name": "k8s_get", "description": "Get Kubernetes resources (pods, services, deployments, etc.).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"resource"}, "properties": map[string]interface{}{"resource": map[string]interface{}{"type": "string", "description": "pods, services, deployments, ingress, configmaps, secrets, nodes, etc."}, "namespace": map[string]interface{}{"type": "string"}, "context": map[string]interface{}{"type": "string"}}}},
		{"name": "k8s_apply", "description": "Apply a Kubernetes manifest file.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string", "description": "Path to YAML manifest"}, "namespace": map[string]interface{}{"type": "string"}, "context": map[string]interface{}{"type": "string"}}}},
		{"name": "k8s_exec", "description": "Execute a command in a Kubernetes pod.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"pod", "command"}, "properties": map[string]interface{}{"pod": map[string]interface{}{"type": "string"}, "command": map[string]interface{}{"type": "string"}, "namespace": map[string]interface{}{"type": "string"}, "context": map[string]interface{}{"type": "string"}, "container": map[string]interface{}{"type": "string"}}}},
		{"name": "k8s_contexts", "description": "List available Kubernetes contexts.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "k8s_namespaces", "description": "List Kubernetes namespaces.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"context": map[string]interface{}{"type": "string"}}}},
		{"name": "k8s_top", "description": "Show resource usage (CPU/memory) for pods or nodes.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"resource"}, "properties": map[string]interface{}{"resource": map[string]interface{}{"type": "string", "description": "pods or nodes"}, "namespace": map[string]interface{}{"type": "string"}, "context": map[string]interface{}{"type": "string"}}}},
		{"name": "k8s_events", "description": "Show recent Kubernetes events (warnings, errors).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"namespace": map[string]interface{}{"type": "string"}, "context": map[string]interface{}{"type": "string"}}}},
		// Terraform
		{"name": "tf_plan", "description": "Run terraform plan.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "tf_apply", "description": "Run terraform apply.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "auto_approve": map[string]interface{}{"type": "boolean", "description": "Auto-approve (default: false)"}}}},
		{"name": "tf_state", "description": "List terraform state resources.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "tf_output", "description": "Show terraform outputs.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "tf_init", "description": "Initialize terraform.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "tf_validate", "description": "Validate terraform configuration.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Serverless
		{"name": "lambda_list", "description": "List AWS Lambda functions.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "lambda_invoke", "description": "Invoke an AWS Lambda function.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "payload": map[string]interface{}{"type": "string", "description": "JSON payload"}}}},
		{"name": "lambda_logs", "description": "Get AWS Lambda function logs.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "minutes": map[string]interface{}{"type": "integer", "description": "Minutes of logs (default: 30)"}}}},
		// Vercel
		{"name": "vercel_status", "description": "Show Vercel deployments.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "vercel_logs", "description": "Get Vercel deployment logs.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"url"}, "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string", "description": "Deployment URL"}}}},
		{"name": "vercel_env", "description": "List Vercel environment variables.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Netlify
		{"name": "netlify_status", "description": "Show Netlify site status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Sentry
		{"name": "sentry_issues", "description": "List recent Sentry errors.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"org", "project"}, "properties": map[string]interface{}{"org": map[string]interface{}{"type": "string"}, "project": map[string]interface{}{"type": "string"}}}},
		// Linear
		{"name": "linear_issues", "description": "List Linear issues.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"api_key"}, "properties": map[string]interface{}{"api_key": map[string]interface{}{"type": "string", "description": "Linear API key"}, "team": map[string]interface{}{"type": "string", "description": "Team key filter"}}}},
		// Notion
		{"name": "notion_search", "description": "Search Notion pages and databases.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"api_key", "query"}, "properties": map[string]interface{}{"api_key": map[string]interface{}{"type": "string", "description": "Notion integration token"}, "query": map[string]interface{}{"type": "string"}}}},
		// 1Password
		{"name": "op_get", "description": "Look up a 1Password item (passwords masked).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"item"}, "properties": map[string]interface{}{"item": map[string]interface{}{"type": "string", "description": "Item name or ID"}, "vault": map[string]interface{}{"type": "string"}}}},
		{"name": "op_list", "description": "List 1Password items.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"vault": map[string]interface{}{"type": "string"}}}},
		// Raycast
		{"name": "raycast", "description": "Trigger a Raycast extension (macOS).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"command"}, "properties": map[string]interface{}{"command": map[string]interface{}{"type": "string", "description": "Raycast command path"}}}},
	}
	tools = append(tools, infraTools...)

	// --- App Development (iOS/Android) ---
	appDevTools := []map[string]interface{}{
		// App Store Connect
		{"name": "appstore_status", "description": "Check App Store Connect app status (review state, builds). Requires APP_STORE_API_KEY_ID and APP_STORE_API_ISSUER env vars.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"bundle_id": map[string]interface{}{"type": "string", "description": "Bundle ID (e.g. io.yaver.mobile)"}}}},
		{"name": "testflight_builds", "description": "List TestFlight builds for an app.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"bundle_id"}, "properties": map[string]interface{}{"bundle_id": map[string]interface{}{"type": "string"}}}},
		{"name": "xcode_build", "description": "Build an Xcode project.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "scheme": map[string]interface{}{"type": "string"}, "destination": map[string]interface{}{"type": "string", "description": "Build destination (default: generic/platform=iOS)"}}}},
		{"name": "xcode_test", "description": "Run Xcode tests.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"scheme"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "scheme": map[string]interface{}{"type": "string"}, "destination": map[string]interface{}{"type": "string", "description": "Simulator destination"}}}},
		{"name": "simulators", "description": "List iOS simulators.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "simulator_boot", "description": "Boot an iOS simulator.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"device"}, "properties": map[string]interface{}{"device": map[string]interface{}{"type": "string", "description": "Device name or UUID"}}}},
		{"name": "simulator_screenshot", "description": "Take a screenshot from iOS simulator.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device": map[string]interface{}{"type": "string", "description": "Device ID (default: booted)"}}}},
		// Google Play
		{"name": "playstore_status", "description": "Check Google Play Console status for an app.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"package"}, "properties": map[string]interface{}{"package": map[string]interface{}{"type": "string", "description": "Package name (e.g. io.yaver.mobile)"}}}},
		{"name": "playstore_track", "description": "Check Play Store track status (production, beta, alpha).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"package"}, "properties": map[string]interface{}{"package": map[string]interface{}{"type": "string"}, "track": map[string]interface{}{"type": "string", "description": "Track: production, beta, alpha, internal (default: production)"}}}},
		{"name": "gradle_build", "description": "Run a Gradle build task.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "task": map[string]interface{}{"type": "string", "description": "Gradle task (default: assembleDebug)"}}}},
		{"name": "gradle_test", "description": "Run Android unit tests.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "android_lint", "description": "Run Android lint checks.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "emulators", "description": "List Android emulators (AVDs).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Firebase
		{"name": "firebase_projects", "description": "List Firebase projects.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "firebase_deploy", "description": "Deploy to Firebase (hosting, functions, etc.).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "only": map[string]interface{}{"type": "string", "description": "Deploy only: hosting, functions, firestore, etc."}}}},
		{"name": "firebase_crashlytics", "description": "Get Crashlytics issues for a Firebase project.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"project_id"}, "properties": map[string]interface{}{"project_id": map[string]interface{}{"type": "string"}}}},
		// React Native / Expo
		{"name": "expo_status", "description": "Show Expo project config.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "eas_build", "description": "Start an EAS Build (Expo).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "platform": map[string]interface{}{"type": "string", "description": "ios or android"}}}},
		{"name": "eas_submit", "description": "Submit to App Store / Play Store via EAS.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "platform": map[string]interface{}{"type": "string"}}}},
		// Flutter
		{"name": "flutter_doctor", "description": "Run flutter doctor to check environment.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "flutter_build", "description": "Build a Flutter app.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "platform": map[string]interface{}{"type": "string", "description": "apk, appbundle, ios, web, macos, linux, windows"}}}},
		{"name": "flutter_test", "description": "Run Flutter tests.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// CocoaPods
		{"name": "pod_install", "description": "Run pod install.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "pod_outdated", "description": "Check for outdated CocoaPods dependencies.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Review guidelines
		{"name": "app_review_check", "description": "Get App Store / Play Store review guidelines, common rejection reasons, and pre-submission checklist.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"platform": map[string]interface{}{"type": "string", "description": "ios or android (both if empty)"}}}},
	}
	tools = append(tools, appDevTools...)

	// --- Package Registries ---
	registryTools := []map[string]interface{}{
		{"name": "dockerhub_search", "description": "Search Docker Hub for images.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "integer"}}}},
		{"name": "dockerhub_tags", "description": "List tags for a Docker Hub image.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"image"}, "properties": map[string]interface{}{"image": map[string]interface{}{"type": "string", "description": "e.g. nginx, library/node, kivanccakmak/yaver-cli"}, "limit": map[string]interface{}{"type": "integer"}}}},
		{"name": "pypi_info", "description": "Get Python package info from PyPI.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"package"}, "properties": map[string]interface{}{"package": map[string]interface{}{"type": "string"}}}},
		{"name": "pypi_versions", "description": "List available versions of a PyPI package.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"package"}, "properties": map[string]interface{}{"package": map[string]interface{}{"type": "string"}}}},
		{"name": "npm_search", "description": "Search npm registry.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "integer"}}}},
		{"name": "npm_versions", "description": "List versions of an npm package.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"package"}, "properties": map[string]interface{}{"package": map[string]interface{}{"type": "string"}}}},
		{"name": "crates_info", "description": "Get Rust crate info from crates.io.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"crate"}, "properties": map[string]interface{}{"crate": map[string]interface{}{"type": "string"}}}},
		{"name": "crates_search", "description": "Search crates.io (Rust packages).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "integer"}}}},
		{"name": "go_module_info", "description": "Get Go module info from proxy.golang.org.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"module"}, "properties": map[string]interface{}{"module": map[string]interface{}{"type": "string", "description": "e.g. github.com/gin-gonic/gin"}}}},
		{"name": "go_module_versions", "description": "List Go module versions.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"module"}, "properties": map[string]interface{}{"module": map[string]interface{}{"type": "string"}}}},
		{"name": "pubdev_info", "description": "Get Dart/Flutter package info from pub.dev.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"package"}, "properties": map[string]interface{}{"package": map[string]interface{}{"type": "string"}}}},
		{"name": "pubdev_search", "description": "Search pub.dev (Dart/Flutter packages).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}}}},
		{"name": "brew_info", "description": "Get Homebrew formula info.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"formula"}, "properties": map[string]interface{}{"formula": map[string]interface{}{"type": "string"}}}},
		{"name": "brew_search", "description": "Search Homebrew formulae.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}}}},
		{"name": "gem_info", "description": "Get Ruby gem info from rubygems.org.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"gem"}, "properties": map[string]interface{}{"gem": map[string]interface{}{"type": "string"}}}},
		{"name": "gem_search", "description": "Search rubygems.org.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}}}},
		{"name": "maven_search", "description": "Search Maven Central (Java/Kotlin).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}}}},
		{"name": "nuget_search", "description": "Search NuGet (.NET packages).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}}}},
		{"name": "apt_search", "description": "Search apt packages (Debian/Ubuntu).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}}}},
		{"name": "apt_show", "description": "Show apt package details.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"package"}, "properties": map[string]interface{}{"package": map[string]interface{}{"type": "string"}}}},
		{"name": "pip_show", "description": "Show installed pip package details.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"package"}, "properties": map[string]interface{}{"package": map[string]interface{}{"type": "string"}}}},
		{"name": "pip_list", "description": "List installed pip packages.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "cargo_search", "description": "Search crates via cargo CLI.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}}}},
		{"name": "pkg_install", "description": "Install a package via any package manager (npm, pip, cargo, go, brew, gem, apt, dart).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"manager", "package"}, "properties": map[string]interface{}{"manager": map[string]interface{}{"type": "string", "description": "npm, pip, cargo, go, brew, gem, apt, dart"}, "package": map[string]interface{}{"type": "string"}, "global": map[string]interface{}{"type": "boolean", "description": "Install globally (npm -g)"}}}},
	}
	tools = append(tools, registryTools...)

	// --- Platforms & BaaS ---
	platformTools := []map[string]interface{}{
		// Supabase
		{"name": "supabase_status", "description": "Show Supabase project status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "supabase_db", "description": "Execute SQL on Supabase database.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "query": map[string]interface{}{"type": "string"}}}},
		{"name": "supabase_migrations", "description": "List Supabase migrations.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "supabase_functions", "description": "List Supabase Edge Functions.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "supabase_deploy", "description": "Deploy Supabase (db push or function deploy).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "function": map[string]interface{}{"type": "string", "description": "Function name (deploys DB if empty)"}}}},
		// Convex
		{"name": "convex_deploy", "description": "Deploy Convex functions.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "convex_logs", "description": "Show Convex function logs.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "convex_run", "description": "Run a Convex function.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"function"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "function": map[string]interface{}{"type": "string", "description": "Function path (e.g. tasks:list)"}, "args": map[string]interface{}{"type": "string", "description": "JSON args"}}}},
		{"name": "convex_local_status", "description": "Check if the local self-hosted Convex backend is running (port 3210).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "convex_tables", "description": "List tables in the local Convex backend (requires yaver_admin.ts helper).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "convex_browse", "description": "Browse documents in a Convex table.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"table"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "table": map[string]interface{}{"type": "string"}, "cursor": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "number"}}}},
		{"name": "convex_query", "description": "Run a Convex query via admin HTTP API (no CLI).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"function"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "function": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "string", "description": "JSON args"}}}},
		{"name": "convex_mutate", "description": "Run a Convex mutation via admin HTTP API.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"function"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "function": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "string"}}}},
		{"name": "convex_action", "description": "Run a Convex action via admin HTTP API.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"function"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "function": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "string"}}}},
		{"name": "convex_schema", "description": "Show the Convex schema.ts for a project.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "convex_export", "description": "Export all data from the local Convex backend (streaming_export).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "convex_install_helper", "description": "Write convex/yaver_admin.ts helper functions into a project so the Yaver dashboard can introspect it.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"directory"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Universal backend (adapter-based)
		{"name": "backend_status", "description": "Show health + URL of whichever backend the project uses (Convex, Postgres, Supabase, PocketBase, Appwrite, SQLite).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "data_tables", "description": "List tables/collections across any supported backend.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "data_browse", "description": "Browse rows/documents with pagination across any backend.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"table"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "table": map[string]interface{}{"type": "string"}, "cursor": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "number"}}}},
		{"name": "data_query", "description": "Run a query (SQL for Postgres/Supabase/SQLite, function path for Convex, REST path for PocketBase/Appwrite).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "query": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "string", "description": "JSON args"}}}},
		{"name": "data_insert", "description": "Insert a record (adapter-routed).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"table", "doc"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "table": map[string]interface{}{"type": "string"}, "doc": map[string]interface{}{"type": "string", "description": "JSON document"}}}},
		{"name": "data_update", "description": "Update a record by id.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"table", "id", "fields"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "table": map[string]interface{}{"type": "string"}, "id": map[string]interface{}{"type": "string"}, "fields": map[string]interface{}{"type": "string", "description": "JSON"}}}},
		{"name": "data_delete", "description": "Delete a record by id.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"table", "id"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "table": map[string]interface{}{"type": "string"}, "id": map[string]interface{}{"type": "string"}}}},
		// Cloud emulators (aws/gcp/azure)
		{"name": "cloud_emu_start", "description": "Start local cloud emulators. Provider: aws (MinIO/DynamoDB/ElasticMQ), azure (Azurite), gcp (Firebase Emulator Suite).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "provider": map[string]interface{}{"type": "string"}, "services": map[string]interface{}{"type": "string", "description": "Comma-separated service list (s3,dynamodb,sqs,blob,queue,firestore,...)"}}}},
		{"name": "cloud_emu_stop", "description": "Stop local cloud emulators.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "provider": map[string]interface{}{"type": "string"}, "services": map[string]interface{}{"type": "string"}}}},
		{"name": "cloud_emu_status", "description": "Show running cloud emulators across all providers.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "cloud_emu_config", "description": "Output SDK config snippets for connecting to local emulators.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string"}}}},
		// Switch engine (backend/host switching with snapshots + rollback)
		{"name": "switch_targets", "description": "List every backend/host target you can switch to.", "inputSchema": map[string]interface{}{"type": "object"}},
		{"name": "switch_plan", "description": "Plan a backend/host switch. Assesses complexity (trivial/easy/medium/hard) and generates ordered steps across 7 layers (data/code/env/infra/network/integrations/verify). Persists the plan — use switch_run to execute.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"target"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "target": map[string]interface{}{"type": "string"}, "dryRun": map[string]interface{}{"type": "boolean"}}}},
		{"name": "switch_run", "description": "Execute a previously-planned switch. HARD switches emit a rewrite prompt for the AI agent instead of running inline.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "id": map[string]interface{}{"type": "string"}}}},
		{"name": "switch_rollback", "description": "Roll back a switch (git branch + env + data restore). Only valid within the 7-day TTL.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "id": map[string]interface{}{"type": "string"}}}},
		{"name": "switch_history", "description": "Show all switches for this project, newest first.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "switch_cleanup", "description": "Delete expired snapshots and pre-switch branches.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "project_runtime", "description": "Summarize project runtime placement, export targets, provider requirements, and machine resolution for the current monorepo or project.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "project_runtime_apply", "description": "Merge runtime/provider updates into .yaver/project.yaml, optionally connect provider accounts, run manifest apply, and plan or run mobile sandbox promotions. Supports either a project directory or a phoneSlug for phone-first sandboxes.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "phoneSlug": map[string]interface{}{"type": "string"}, "name": map[string]interface{}{"type": "string"}, "backend": map[string]interface{}{"type": "string"}, "stack": map[string]interface{}{"type": "string"}, "auth": map[string]interface{}{"type": "string"}, "runtime": map[string]interface{}{"type": "object"}, "placement": map[string]interface{}{"type": "object"}, "jobs": map[string]interface{}{"type": "array"}, "domains": map[string]interface{}{"type": "array"}, "env": map[string]interface{}{"type": "object"}, "providers": map[string]interface{}{"type": "array"}, "phonePromotions": map[string]interface{}{"type": "array"}, "runManifestApply": map[string]interface{}{"type": "boolean"}, "dryRun": map[string]interface{}{"type": "boolean"}}}},
		// Accounts manager
		{"name": "account_list", "description": "List all cloud providers and their connection state.", "inputSchema": map[string]interface{}{"type": "object"}},
		{"name": "account_connect", "description": "Store credentials for a cloud provider (encrypted at rest).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider", "fields"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string"}, "label": map[string]interface{}{"type": "string"}, "fields": map[string]interface{}{"type": "string", "description": "JSON: {\"token\":\"...\"} or {\"accessKey\":\"...\",\"secretKey\":\"...\"}"}}}},
		{"name": "account_disconnect", "description": "Remove stored credentials for a provider.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string"}}}},
		{"name": "account_status", "description": "Show connection state for a provider.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string"}}}},
		// --- Yaver sign-in (headless OAuth for remote/WSL/Linux/macOS) ---
		// These tools let a coding agent (Claude Code remote over SSH, Codex,
		// Cursor, Windsurf, Zed) sign the user into Yaver itself — not a
		// cloud provider. The flow is device-code / browserless: start →
		// user opens URL on any device → poll/wait until authorized.
		{"name": "yaver_auth_status", "description": "Report Yaver's own sign-in state on this machine: whether a valid Apple/GitHub/Google/Microsoft OAuth token is present, which user it belongs to, and whether the environment looks headless. Safe to call before signing in.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "yaver_auth_start", "description": "Start a headless device-code sign-in for Yaver (Apple / GitHub / Google / Microsoft OAuth). Returns {url, user_code, device_code, qr_ascii, expires_at_ms}. Render the URL + QR to the user; they open it on their phone or laptop browser, sign in, and confirm the code. Then call yaver_auth_wait (or loop yaver_auth_poll) with the device_code to finish. Non-blocking — this tool does not wait for the user.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"convex_url": map[string]interface{}{"type": "string", "description": "Optional override for the Convex backend URL (defaults to the stored or hosted endpoint)."}}}},
		{"name": "yaver_auth_poll", "description": "Run one poll of the device-code authorization. Returns status = pending | authorized | expired. On authorized, the token is saved to ~/.yaver/config.json, the daemon is started in the background, and Yaver is auto-registered as an MCP server in every installed editor. Use this when you want full control over polling cadence.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"device_code"}, "properties": map[string]interface{}{"device_code": map[string]interface{}{"type": "string", "description": "Opaque device code returned by yaver_auth_start."}, "convex_url": map[string]interface{}{"type": "string"}}}},
		{"name": "yaver_auth_wait", "description": "Block until the device code is authorized, expires, or the timeout fires. Preferred over yaver_auth_poll for coding agents that can accept a ~2-minute tool call. On authorized: saves token, starts daemon, registers MCP in editors. Default timeout 120s, poll interval 3s — both tunable.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"device_code"}, "properties": map[string]interface{}{"device_code": map[string]interface{}{"type": "string"}, "convex_url": map[string]interface{}{"type": "string"}, "timeout_seconds": map[string]interface{}{"type": "integer", "description": "Max seconds to block (default 120, max 300)."}, "poll_interval_seconds": map[string]interface{}{"type": "integer", "description": "Seconds between polls (default 3)."}}}},
		{"name": "yaver_auth_logout", "description": "Clear the saved Yaver auth token from ~/.yaver/config.json on this machine. Daemon is left running — call agent_shutdown separately if you want to stop it.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "yaver_lazy_setup", "description": "One-shot install + auth + mobile-app handoff for a non-developer user being walked through setup by an AI agent. Call this FIRST instead of wiring up auth_status/start/wait manually. Returns a structured plan: whether yaver-cli is installed, whether the user is signed in, a sign-in URL (if needed) the AI should surface to the human, mobile app install links (TestFlight + Play), and a single `next_action` string the AI can speak verbatim. Idempotent: safe to call repeatedly while the user finishes steps on their phone — on each call it picks up where the last one left off. The ideal orchestration loop for a coding agent: (1) call yaver_lazy_setup → show the returned url to the human; (2) wait a bit; (3) call yaver_lazy_setup again — if status is now \"signed_in\", you're done.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"wait_seconds": map[string]interface{}{"type": "integer", "description": "If >0, block up to this many seconds (max 180) waiting for sign-in to complete in-call. Default 0 = return immediately. 120 is a reasonable value for agents that can afford a 2-minute tool call."}}}},
		// --- Account linking (connect additional OAuth providers, unlink, merge two accounts) ---
		{"name": "yaver_auth_list_identities", "description": "List every OAuth provider (Apple / GitHub / GitLab / Google / Microsoft / email) linked to the currently signed-in Yaver account. Returns each provider's email and which one is primary. Safe to call any time.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "yaver_auth_link_start", "description": "Connect an ADDITIONAL OAuth provider to the currently signed-in account — e.g., user signed up with Apple but wants to also sign in with GitHub, GitLab, Google, or Microsoft. Returns {url, qr_ascii, link_token, expires_at_ms}. Render the URL + QR; the user opens it, signs in with that provider, and Yaver binds the provider to the existing account. Call yaver_auth_link_wait afterwards to confirm.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string", "description": "Provider to link: apple | github | gitlab | google | microsoft"}}}},
		{"name": "yaver_auth_link_wait", "description": "Poll the account's linked identities until the requested provider appears (or timeout). Preferred over manual polling after yaver_auth_link_start. Default timeout 120s.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string", "description": "The provider passed to yaver_auth_link_start"}, "timeout_seconds": map[string]interface{}{"type": "integer"}, "poll_interval_seconds": map[string]interface{}{"type": "integer"}}}},
		{"name": "yaver_auth_unlink", "description": "Remove an OAuth provider from the currently signed-in account. Refuses if it is the ONLY sign-in method (would lock the user out). If the unlinked provider was the primary one, another linked provider is promoted automatically. If 2FA is enabled, pass totp_code.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string", "description": "apple | github | gitlab | google | microsoft | email"}, "totp_code": map[string]interface{}{"type": "string", "description": "Optional 6-digit TOTP code for 2FA-protected accounts"}}}},
		{"name": "yaver_auth_merge_start", "description": "Start a MANUAL account-merge: someone accidentally created two Yaver accounts and wants to fold one into the current one. Returns {merge_token, approval_url, qr_ascii, expires_at_ms, target_email}. The user opens the URL on a browser where the OTHER account is signed in, confirms, and the merge completes. Call yaver_auth_merge_wait to watch for completion. The currently signed-in account is the one that will be KEPT. If 2FA is enabled on the target account, pass totp_code.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"totp_code": map[string]interface{}{"type": "string", "description": "Optional 6-digit TOTP code for 2FA-protected accounts"}}}},
		{"name": "yaver_auth_merge_wait", "description": "Poll a merge intent's status until it completes, is cancelled, or expires. Default timeout 180s.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"merge_token"}, "properties": map[string]interface{}{"merge_token": map[string]interface{}{"type": "string", "description": "Token returned by yaver_auth_merge_start"}, "timeout_seconds": map[string]interface{}{"type": "integer"}, "poll_interval_seconds": map[string]interface{}{"type": "integer"}}}},
		{"name": "recovery_transport_status", "description": "Report this machine's auth-recovery exposure and readiness: whether direct public HTTP recovery is open, whether Tailscale/private relay/HTTPS tunnel paths exist, and whether a bootstrap secret is configured.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "recovery_target_status", "description": "Probe a specific remote Yaver base URL without relying on local Yaver sign-in. For security, public HTTP targets are refused unless you pass relay_password or allow_public_direct_http=true. Returns whether the box looks offline, bootstrap-reachable, auth-required, or generally reachable.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"target_url"}, "properties": map[string]interface{}{"target_url": map[string]interface{}{"type": "string", "description": "Exact remote base URL, e.g. https://edge.example.com or https://relay.example/d/device-id"}, "relay_password": map[string]interface{}{"type": "string", "description": "Optional relay password for private relay access."}, "allow_public_direct_http": map[string]interface{}{"type": "boolean", "description": "Unsafe override for plain public HTTP targets. Default false."}}}},
		{"name": "recovery_target_start", "description": "Start auth recovery against an explicit remote Yaver base URL when the caller is not signed into Yaver locally. Requires either bootstrap_secret or bearer_token. direct/device-code require bearer_token. Public HTTP targets are refused unless relay_password is provided or allow_public_direct_http=true.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"target_url"}, "properties": map[string]interface{}{"target_url": map[string]interface{}{"type": "string", "description": "Exact remote base URL, e.g. https://edge.example.com or https://relay.example/d/device-id"}, "mode": map[string]interface{}{"type": "string", "description": "auto (default), direct, pair, or device-code."}, "bootstrap_secret": map[string]interface{}{"type": "string", "description": "Bootstrap secret previously configured on the remote machine."}, "bearer_token": map[string]interface{}{"type": "string", "description": "Host Yaver bearer token to use for direct or device-code recovery."}, "relay_password": map[string]interface{}{"type": "string", "description": "Optional relay password for private relay access."}, "allow_public_direct_http": map[string]interface{}{"type": "boolean", "description": "Unsafe override for plain public HTTP targets. Default false."}}}},
		{"name": "recovery_target_wait", "description": "Poll a previously-started explicit-target recovery session until it completes, fails, or times out. Requires target_url plus the recovery_id/wait_token returned by recovery_target_start.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"target_url", "recovery_id", "wait_token"}, "properties": map[string]interface{}{"target_url": map[string]interface{}{"type": "string"}, "recovery_id": map[string]interface{}{"type": "string"}, "wait_token": map[string]interface{}{"type": "string"}, "relay_password": map[string]interface{}{"type": "string"}, "allow_public_direct_http": map[string]interface{}{"type": "boolean"}, "timeout_seconds": map[string]interface{}{"type": "integer", "description": "Default 120, max 300."}, "poll_interval_seconds": map[string]interface{}{"type": "integer", "description": "Default 3."}}}},
		{"name": "device_reauth_status", "description": "Inspect an owned remote Yaver machine's recovery state. Distinguishes healthy, bootstrap, auth-expired, unreachable, and offline cases so coding agents can decide whether to wait, reconnect, or start re-auth.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"device_id"}, "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Owned Yaver device ID, unique prefix, or exact device name."}}}},
		{"name": "device_reauth_start", "description": "Start Yaver re-auth on an owned remote machine through the existing /auth/recover path. auto picks the safest mode for the detected state: typically direct for auth-expired, pair for bootstrap.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"device_id"}, "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Owned Yaver device ID, unique prefix, or exact device name."}, "mode": map[string]interface{}{"type": "string", "description": "auto (default), direct, pair, or device-code."}, "bootstrap_secret": map[string]interface{}{"type": "string", "description": "Optional legacy bootstrap secret for secret-based recovery when host-token recovery is not enough."}}}},
		{"name": "device_reauth_wait", "description": "Wait for remote Yaver auth recovery to complete. Preferred usage is recovery_id + wait_token from device_reauth_start; device_id-only fallback still probes machine health for older callers.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Owned device selector. Required for remote-session waits; optional only when waiting on a local session."}, "recovery_id": map[string]interface{}{"type": "string"}, "wait_token": map[string]interface{}{"type": "string"}, "timeout_seconds": map[string]interface{}{"type": "integer", "description": "Default 120, max 300."}, "poll_interval_seconds": map[string]interface{}{"type": "integer", "description": "Default 3."}}}},
		// --- Runner/provider auth bootstrapping (vault-backed, local or remote) ---
		{"name": "runner_auth_status", "description": "Show whether Claude Code, Codex, and OpenCode are installed and authenticated. Optional device_id inspects another owned Yaver machine.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}}}},
		{"name": "runner_auth_set", "description": "Save runner/provider auth into the local or remote Yaver vault for headless setup. Supports claude, codex, and opencode. Optional device_id writes to another owned Yaver machine.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"runner"}, "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "runner": map[string]interface{}{"type": "string", "enum": []string{"claude", "claude-code", "codex", "opencode"}}, "openai_api_key": map[string]interface{}{"type": "string"}, "anthropic_api_key": map[string]interface{}{"type": "string"}, "anthropic_auth_token": map[string]interface{}{"type": "string"}, "claude_code_oauth_token": map[string]interface{}{"type": "string"}, "glm_api_key": map[string]interface{}{"type": "string"}, "zai_api_key": map[string]interface{}{"type": "string"}, "notes": map[string]interface{}{"type": "string"}}}},
		{"name": "runner_auth_setup", "description": "High-level runner bootstrap for a local or remote Yaver machine: install the runner if missing, save auth into the Yaver vault, headless-login Codex with an API key when requested, and register Yaver as an MCP server inside the runner when supported.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"runner"}, "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "runner": map[string]interface{}{"type": "string", "enum": []string{"claude", "claude-code", "codex", "opencode"}}, "openai_api_key": map[string]interface{}{"type": "string"}, "anthropic_api_key": map[string]interface{}{"type": "string"}, "anthropic_auth_token": map[string]interface{}{"type": "string"}, "claude_code_oauth_token": map[string]interface{}{"type": "string"}, "glm_api_key": map[string]interface{}{"type": "string"}, "zai_api_key": map[string]interface{}{"type": "string"}, "notes": map[string]interface{}{"type": "string"}, "install_if_missing": map[string]interface{}{"type": "boolean", "description": "Default true."}, "codex_login": map[string]interface{}{"type": "boolean", "description": "Default true for Codex; ignored for other runners."}, "setup_mcp": map[string]interface{}{"type": "boolean", "description": "Default true. Registers Yaver as an MCP server in the runner when supported."}}}},
		{"name": "runner_auth_browser_start", "description": "Start the interactive browser/device-auth login flow for Claude Code or Codex on the local or a remote Yaver machine.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"runner"}, "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "runner": map[string]interface{}{"type": "string", "enum": []string{"claude", "claude-code", "codex"}}}}},
		{"name": "runner_auth_browser_status", "description": "Read the live state of a previously-started runner browser-auth session on the local or a remote machine.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id"}, "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "session_id": map[string]interface{}{"type": "string"}}}},
		{"name": "runner_auth_browser_submit_code", "description": "Submit a copied authentication code/token back into a running runner browser-auth session.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id", "code"}, "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "session_id": map[string]interface{}{"type": "string"}, "code": map[string]interface{}{"type": "string"}}}},
		{"name": "runner_auth_browser_cancel", "description": "Cancel a running runner browser-auth session on the local or a remote machine.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id"}, "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "session_id": map[string]interface{}{"type": "string"}}}},
		{"name": "runner_auth_credentials_import", "description": "Copy a runner subscription token (Claude Max / Pro, or ChatGPT Plus / Pro for codex) from an already-signed-in machine to a remote one. Yaver is a single-user wrapper, so this is the preferred path when the user already has working subscription auth locally — it avoids re-running OAuth on every box (which hits the SSH-launched-daemon Keychain wall on macOS). Subscription only; never use API keys.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"runner", "credentials_json"}, "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID. When omitted, writes to the local agent."}, "runner": map[string]interface{}{"type": "string", "enum": []string{"claude", "claude-code", "codex"}}, "credentials_json": map[string]interface{}{"type": "string", "description": "The full credentials JSON blob — for claude on macOS this is the 'Claude Code-credentials' Keychain entry contents (or ~/.claude/.credentials.json on Linux); for codex this is ~/.codex/auth.json."}}}},
		{
			"name":        "code_config_get",
			"description": "Read the machine-aware `yaver code` control-plane state. Returns the persisted code config (runner, model, provider, base URL, attached machine, repo/work-mode, orchestration mode), plus best-effort current target info/context and the OpenCode config summary when the runner is opencode. Optional device_id reads another owned Yaver machine's code config.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"},
				},
			},
		},
		{
			"name":        "code_config_set",
			"description": "Update the machine-aware `yaver code` control-plane state on the local or a remote owned Yaver machine. Supports runner/model/orchestration/work-mode/attached machine/repo fields and an optional BYOK block for OpenCode-backed coding (OpenRouter, custom OpenAI-compatible providers, remote Ollama). Returns the updated code summary.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"device_id":            map[string]interface{}{"type": "string", "description": "Optional remote device ID"},
					"runner":               map[string]interface{}{"type": "string", "description": "Runner override such as claude, codex, or opencode"},
					"model":                map[string]interface{}{"type": "string", "description": "Model id for the selected runner"},
					"provider":             map[string]interface{}{"type": "string", "description": "Preferred provider id for opencode-backed BYOK flows, e.g. openrouter"},
					"base_url":             map[string]interface{}{"type": "string", "description": "Provider base URL such as https://openrouter.ai/api/v1"},
					"orchestration_mode":   map[string]interface{}{"type": "string", "enum": []string{"manual", "auto"}},
					"work_mode":            map[string]interface{}{"type": "string", "enum": []string{"local", "attached"}},
					"attached_device_id":   map[string]interface{}{"type": "string"},
					"attached_device_name": map[string]interface{}{"type": "string"},
					"repo_path":            map[string]interface{}{"type": "string", "description": "Set the effective repo/workdir for the current local or attached target"},
					"repo_remote":          map[string]interface{}{"type": "boolean"},
					"byok_provider":        map[string]interface{}{"type": "string", "description": "High-level BYOK provider id, e.g. openrouter"},
					"byok_api_key":         map[string]interface{}{"type": "string"},
					"small_model":          map[string]interface{}{"type": "string"},
					"plan_model":           map[string]interface{}{"type": "string"},
					"build_model":          map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "code_status",
			"description": "Return the shared `yaver code` control-plane status: current code config, target context, online machines, OpenCode summary when relevant, recent graph runs, and the current auto-orchestration policy. Optional device_id inspects another owned Yaver machine.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"},
				},
			},
		},
		{
			"name":        "code_attach",
			"description": "Attach `yaver code` to a machine so the repo/files and coding context live there while the terminal stays local. Returns the selected machine, runner summary, target context, and updated code config. Optional device_id mutates another owned Yaver machine's code control plane instead of this one.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"target"},
				"properties": map[string]interface{}{
					"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID whose local code-control plane should be updated"},
					"target":    map[string]interface{}{"type": "string", "description": "Device ID or machine name to attach to"},
					"username":  map[string]interface{}{"type": "string", "description": "Optional owner email hint when machine names collide"},
				},
			},
		},
		{
			"name":        "code_detach",
			"description": "Detach `yaver code` back to the local machine. Optional device_id detaches another owned Yaver machine's code control plane.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"},
				},
			},
		},
		{
			"name":        "code_repos",
			"description": "List candidate repos/projects for the current `yaver code` target. On local mode it reads local discovered projects; on attached mode it lists projects from the attached remote machine. Optional device_id queries another owned Yaver machine.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"},
				},
			},
		},
		{
			"name":        "code_repo_set",
			"description": "Switch the active repo/workdir for `yaver code` on the local or attached target. Accepts a project name, slug, or absolute path. Optional device_id updates another owned Yaver machine.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]interface{}{
					"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"},
					"query":     map[string]interface{}{"type": "string", "description": "Repo/project selector or absolute path"},
				},
			},
		},
		{
			"name":        "code_dev",
			"description": "Run a dev-loop action against the current `yaver code` target. Supported actions today: `status`, `reload`. Optional device_id targets another owned Yaver machine's code control plane.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"action"},
				"properties": map[string]interface{}{
					"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"},
					"action":    map[string]interface{}{"type": "string", "enum": []string{"status", "reload"}},
				},
			},
		},
		{
			"name":        "code_deploy",
			"description": "Run a deployment from the current `yaver code` target or from an explicitly selected repo/machine. Supports direct host deploys to TestFlight, Play internal testing, Convex, Cloudflare, or combined `all`, plus optional GitHub/GitLab CI fallback. `machine=auto` asks Yaver to choose the best machine for the target; `distribute=true` lets multi-target deploys fan out across different machines to reduce CI cost and load hot spots.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"device_id":   map[string]interface{}{"type": "string", "description": "Optional remote device ID"},
					"app":         map[string]interface{}{"type": "string", "description": "Optional explicit app/project name override"},
					"surface":     map[string]interface{}{"type": "string", "enum": []string{"mobile", "backend", "frontend", "all", "testflight", "playstore", "convex", "cloudflare"}},
					"targets":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional explicit deploy target list; overrides surface"},
					"repo_query":  map[string]interface{}{"type": "string", "description": "Optional repo/project selector to deploy instead of the current code repo"},
					"repo_path":   map[string]interface{}{"type": "string", "description": "Optional explicit repo path on the selected machine"},
					"machine":     map[string]interface{}{"type": "string", "description": "Optional executor machine: local, auto, or a device id/name"},
					"distribute":  map[string]interface{}{"type": "boolean", "description": "When true, multi-target deploys may choose different machines per target"},
					"ci_provider": map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab"}, "description": "Optional CI fallback instead of direct host deploy"},
					"ci_repo":     map[string]interface{}{"type": "string", "description": "Optional CI repo identifier; auto-detected from git when omitted"},
					"workflow":    map[string]interface{}{"type": "string", "description": "GitHub Actions workflow filename when ci_provider=github"},
					"branch":      map[string]interface{}{"type": "string", "description": "CI branch (default main)"},
					"tag":         map[string]interface{}{"type": "string", "description": "GitHub release tag for artifact upload mode"},
					"file":        map[string]interface{}{"type": "string", "description": "Optional artifact path for CI upload/release mode"},
				},
			},
		},
		// --- OpenCode config (build/plan agents, providers, models) ---
		{"name": "opencode_config_get", "description": "Read the OpenCode config (~/.config/opencode/opencode.json or env-overridden path). Returns the resolved path, default agent, top-level model + small_model, plus the per-agent build/plan models, all configured providers (with baseURL — useful for OpenRouter, remote Tailscale Ollama, or other BYOK OpenAI-compatible backends), and a flat list of model identifiers. Optional device_id reads another owned Yaver machine's config.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}}}},
		{"name": "opencode_config_set", "description": "Patch the OpenCode config on the local or a remote Yaver machine. Each top-level field is optional; pass empty string to clear. defaultAgent picks the agent invoked when no --agent flag is given. model is the top-level default model id. smallModel is used for cheap helper calls. buildModel and planModel set the model under agent.build / agent.plan in opencode.json. providers is an optional list of provider upserts — each entry creates or merges a provider entry by id. Common cases: BYOK OpenRouter via {id:'openrouter', baseUrl:'https://openrouter.ai/api/v1', apiKey:'...'} or pointing a remote machine's opencode at a Tailscale-reachable Ollama via {id:'ollama', baseUrl:'http://100.x.x.x:11434'}. Pass delete:true on a provider entry to remove it. Other config keys (custom agents, MCP servers) stay untouched. Returns the new config summary.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "default_agent": map[string]interface{}{"type": "string", "description": "Default agent name (e.g. build, plan, or any custom agent)"}, "model": map[string]interface{}{"type": "string", "description": "Top-level default model id"}, "small_model": map[string]interface{}{"type": "string", "description": "Cheap-helper model id"}, "build_model": map[string]interface{}{"type": "string", "description": "Model for agent.build"}, "plan_model": map[string]interface{}{"type": "string", "description": "Model for agent.plan"}, "providers": map[string]interface{}{"type": "array", "description": "Optional list of provider upserts — each {id, name?, baseUrl?, apiKey?, models?, delete?}.", "items": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string", "description": "Provider id (e.g. ollama, anthropic, openai, openrouter)"}, "name": map[string]interface{}{"type": "string"}, "baseUrl": map[string]interface{}{"type": "string", "description": "e.g. https://openrouter.ai/api/v1 or http://100.x.x.x:11434 for a Tailscale Ollama"}, "apiKey": map[string]interface{}{"type": "string"}, "models": map[string]interface{}{"type": "object", "description": "Map of model id → metadata. Optional."}, "delete": map[string]interface{}{"type": "boolean", "description": "Remove the provider entry entirely."}}}}}}},
		{"name": "machine_onboarding_status", "description": "Show whether OpenAI, GitHub, and GitLab are configured on this machine or on one or more owned Yaver machines. Reports OpenAI API key readiness plus Git clone and CI/deploy token state.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "device_ids": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional list of owned remote device IDs"}}}},
		{"name": "machine_onboarding_apply", "description": "Configure OpenAI, GitHub, and GitLab onboarding on the local machine or on one or more owned Yaver machines. Stores OpenAI in vault, and for GitHub/GitLab can write clone credentials plus CI/deploy tokens.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "device_ids": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional list of owned remote device IDs"}, "openai_api_key": map[string]interface{}{"type": "string"}, "github_token": map[string]interface{}{"type": "string"}, "gitlab_token": map[string]interface{}{"type": "string"}, "gitlab_host": map[string]interface{}{"type": "string", "description": "Defaults to gitlab.com"}, "apply_clone": map[string]interface{}{"type": "boolean", "description": "Write clone/pull credentials (default true)"}, "apply_ci_token": map[string]interface{}{"type": "boolean", "description": "Write CI/deploy vault token (default true)"}, "notes": map[string]interface{}{"type": "string"}}}},
		{"name": "machine_onboarding_remove", "description": "Remove GitHub/GitLab onboarding from the local machine or from one or more owned Yaver machines. Can remove clone credentials, CI/deploy vault tokens, or both.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID"}, "device_ids": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional list of owned remote device IDs"}, "providers": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab"}}, "description": "Providers to remove"}, "gitlab_host": map[string]interface{}{"type": "string", "description": "Optional specific GitLab host to clear"}, "remove_clone": map[string]interface{}{"type": "boolean", "description": "Remove clone/pull credentials and provider config (default true)"}, "remove_ci_token": map[string]interface{}{"type": "boolean", "description": "Remove CI/deploy vault token (default true)"}}}},
		{"name": "git_push_creds", "description": "Forward locally detected GitHub/GitLab tokens (gh CLI, env vars, git credential helper, vault) to one or more owned remote Yaver machines via the same /machine/onboarding/apply endpoint as the dashboard. Lets a fresh remote box (Hetzner runner, managed cloud, …) get clone-pull creds plus CI/deploy vault tokens without re-pasting a PAT. Self is always excluded — use machine_onboarding_apply or vault tools for the local machine. Tokens never reach Convex.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"device_id": map[string]interface{}{"type": "string", "description": "Single owned remote device ID or alias"}, "device_ids": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Multiple owned remote device IDs/aliases"}, "all": map[string]interface{}{"type": "boolean", "description": "Fan out to every owned online peer (excludes this machine)"}, "provider": map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab", "all"}, "description": "Which provider(s) to push (default all)"}, "gitlab_host": map[string]interface{}{"type": "string", "description": "Defaults to gitlab.com"}, "github_token": map[string]interface{}{"type": "string", "description": "Override auto-detection (omit to detect locally)"}, "gitlab_token": map[string]interface{}{"type": "string", "description": "Override auto-detection (omit to detect locally)"}, "apply_clone": map[string]interface{}{"type": "boolean", "description": "Write clone/pull credentials on each target (default true)"}, "apply_ci_token": map[string]interface{}{"type": "boolean", "description": "Write CI/deploy vault token on each target (default true)"}, "notes": map[string]interface{}{"type": "string"}}}},
		{"name": "git_oauth_start", "description": "Start a GitHub or GitLab Device Flow (RFC 8628) authorization on the local machine or a remote owned peer. Returns a short user_code + verification_uri the user opens in any browser to approve. The agent polls in the background and persists the resulting OAuth access token to ~/.yaver/git-credentials.json + provider metadata, exactly like /git/provider/setup. Token never reaches Convex. Poll git_oauth_status to learn when approval completes. Requires a registered Device Flow OAuth Client ID — see error message for setup if not configured.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab"}}, "host": map[string]interface{}{"type": "string", "description": "Defaults to github.com / gitlab.com"}, "device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID/alias — runs the flow on that peer"}}}},
		{"name": "git_oauth_status", "description": "Poll the state of an in-flight GitHub/GitLab Device Flow session started via git_oauth_start. Returns state ∈ {pending, done, error, expired, unknown} plus the username on success. Safe to call repeatedly at the interval the start call returned.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id"}, "properties": map[string]interface{}{"session_id": map[string]interface{}{"type": "string"}, "device_id": map[string]interface{}{"type": "string", "description": "Optional remote device ID/alias — checks the session on that peer"}}}},
		// Cloud provisioning
		{"name": "cloud_provision", "description": "Provision a resource on a cloud provider using stored credentials. Hosts: neon, supabase-cloud, turso, cloudflare-d1, vercel, hetzner, railway.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host", "name"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string"}, "name": map[string]interface{}{"type": "string"}, "opts": map[string]interface{}{"type": "string", "description": "JSON options"}}}},
		{"name": "cloud_destroy", "description": "Snapshot then delete a managed cloud box (host=hetzner). Vault-backed token, never from payload. Requires opts JSON with confirm=\"true\"; always snapshots first unless opts skipSnapshot=\"true\".", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host", "id"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string"}, "id": map[string]interface{}{"type": "string", "description": "provider numeric server id"}, "opts": map[string]interface{}{"type": "string", "description": "JSON options incl. confirm"}}}},
		// Studio proxy
		{"name": "studio_list", "description": "List native DB dashboards (Drizzle, Supabase, Convex, PocketBase, MinIO, Mailpit, Firebase) with live-probe.", "inputSchema": map[string]interface{}{"type": "object"}},
		// Cost comparison
		{"name": "switch_cost", "description": "Estimate monthly cost across all switch targets based on current project usage.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Direct init (assembles services.yaml + config.yaml + minimal scaffold)
		{"name": "init_project", "description": "Scaffold a new project directly (without the interactive wizard). Picks services.yaml presets + writes .yaver/config.yaml + kicks off create-next-app in the background.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"opts"}, "properties": map[string]interface{}{"opts": map[string]interface{}{"type": "string", "description": "JSON: {name, parentDir, stack, db, auth, payments, template, orm, services}"}}}},
		// Schema + storage + logs
		{"name": "backend_schema", "description": "Show schema (tables + columns + mermaid ERD) for the project's backend.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "storage_list", "description": "List files across Convex Storage / Supabase Storage / local uploads/.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "bucket": map[string]interface{}{"type": "string"}}}},
		{"name": "shared_storage_profiles", "description": "List machine-level shared storage profiles (NAS/SMB/WebDAV/Storage Box/S3) available to Yaver clients.", "inputSchema": map[string]interface{}{"type": "object"}},
		{"name": "shared_storage_upsert", "description": "Create or update a shared storage profile. Profile is JSON with fields like {name,type,path|mount_path|endpoint,bucket,...}.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"profile"}, "properties": map[string]interface{}{"profile": map[string]interface{}{"type": "string", "description": "JSON shared storage profile"}}}},
		{"name": "shared_storage_delete", "description": "Delete a shared storage profile.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}}},
		{"name": "shared_storage_list", "description": "Browse a configured shared storage profile.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}, "path": map[string]interface{}{"type": "string"}}}},
		{"name": "shared_storage_search", "description": "Search names and text documents inside a configured shared storage profile.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id", "query"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}, "query": map[string]interface{}{"type": "string"}, "path": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "number"}}}},
		{"name": "cron_list", "description": "List scheduled/cron jobs across backends (Convex scheduled functions, pg_cron).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "console_machines", "description": "List every machine running a Yaver agent (local Mac/Linux/Windows + Hetzner, AWS, GCP VPSes). Hybrid view across own-hardware and cloud.", "inputSchema": map[string]interface{}{"type": "object"}},
		// Deploy pipeline
		{"name": "deploy_run", "description": "Run a deploy: git pull → build → swap containers → healthcheck → auto-rollback on failure.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "deploy_list", "description": "List deploy history for a project.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "deploy_rollback", "description": "Roll back to a prior deploy's commit.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "id": map[string]interface{}{"type": "string"}}}},
		// Env clone + log search
		{"name": "clone_env", "description": "Clone one project's backend data into another (same backend family). Source snapshot → target restore.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"source", "target"}, "properties": map[string]interface{}{"source": map[string]interface{}{"type": "string"}, "target": map[string]interface{}{"type": "string"}, "subsetRows": map[string]interface{}{"type": "number"}}}},
		{"name": "log_search", "description": "Search all indexed container logs via SQLite FTS5.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"q"}, "properties": map[string]interface{}{"q": map[string]interface{}{"type": "string"}, "services": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "number"}}}},
		// Cloudflare
		{"name": "cf_workers", "description": "List Cloudflare Worker deployments.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "cf_deploy", "description": "Deploy Cloudflare Worker.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "cf_pages", "description": "List Cloudflare Pages deployments.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "cf_r2", "description": "Manage Cloudflare R2 storage (list buckets/objects).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "buckets or list"}, "bucket": map[string]interface{}{"type": "string"}, "key": map[string]interface{}{"type": "string"}}}},
		{"name": "cf_d1", "description": "Query Cloudflare D1 database.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "list or query"}, "database": map[string]interface{}{"type": "string"}, "query": map[string]interface{}{"type": "string"}}}},
		{"name": "cf_kv", "description": "Manage Cloudflare KV store.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "list, keys, get, put"}, "namespace": map[string]interface{}{"type": "string"}, "key": map[string]interface{}{"type": "string"}, "value": map[string]interface{}{"type": "string"}}}},
		// GitLab
		{"name": "gitlab_mrs", "description": "List GitLab merge requests.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "state": map[string]interface{}{"type": "string", "description": "opened, merged, closed"}}}},
		{"name": "gitlab_issues", "description": "List GitLab issues.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "state": map[string]interface{}{"type": "string"}}}},
		{"name": "gitlab_pipelines", "description": "List GitLab CI/CD pipelines.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "gitlab_ci", "description": "Show current GitLab CI status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// GitHub extras
		{"name": "github_repo_info", "description": "Get current repo info (stars, forks, language, license).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "github_releases", "description": "List GitHub releases.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "github_stars", "description": "Get star count for any repo.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"repo"}, "properties": map[string]interface{}{"repo": map[string]interface{}{"type": "string", "description": "owner/repo"}}}},
		// gh / glab — generic passthrough + opinionated write ops
		{"name": "gh_run", "description": "Run any `gh` (GitHub CLI) subcommand. Pass the args as a list (no leading `gh`). Pre-flights install + auth state, returns a clear error when the CLI is missing or unauthed. Use this for anything not covered by the specific github_* tools (e.g. `gh repo create`, `gh release create`, `gh secret set`).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"args"}, "properties": map[string]interface{}{"args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "subcommand + flags, e.g. [\"repo\", \"view\", \"--json\", \"description\"]"}, "directory": map[string]interface{}{"type": "string", "description": "Working directory; defaults to agent cwd"}}}},
		{"name": "glab_run", "description": "Run any `glab` (GitLab CLI) subcommand. Same shape as gh_run. Use for MR/issue/CI/snippet/release ops not covered by gitlab_* tools.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"args"}, "properties": map[string]interface{}{"args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "subcommand + flags, e.g. [\"mr\", \"view\", \"42\"]"}, "directory": map[string]interface{}{"type": "string"}}}},
		{"name": "github_pr_create", "description": "Create a GitHub pull request from the current branch.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"title"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "title": map[string]interface{}{"type": "string"}, "body": map[string]interface{}{"type": "string"}, "base": map[string]interface{}{"type": "string", "description": "Target branch (default: repo default branch)"}, "head": map[string]interface{}{"type": "string", "description": "Source branch (default: current)"}, "draft": map[string]interface{}{"type": "boolean"}}}},
		{"name": "github_issue_create", "description": "Open a GitHub issue.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"title"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "title": map[string]interface{}{"type": "string"}, "body": map[string]interface{}{"type": "string"}, "labels": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}}},
		{"name": "github_workflow_run", "description": "Trigger a GitHub Actions workflow_dispatch run.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"workflow"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "workflow": map[string]interface{}{"type": "string", "description": "Filename (ci.yml) or workflow display name"}, "ref": map[string]interface{}{"type": "string", "description": "Branch/tag/SHA to run against"}, "inputs": map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}}}}},
		{"name": "gitlab_mr_create", "description": "Open a GitLab merge request.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"title"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "title": map[string]interface{}{"type": "string"}, "description": map[string]interface{}{"type": "string"}, "sourceBranch": map[string]interface{}{"type": "string"}, "targetBranch": map[string]interface{}{"type": "string"}, "draft": map[string]interface{}{"type": "boolean"}}}},
		{"name": "gitlab_issue_create", "description": "Open a GitLab issue.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"title"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "title": map[string]interface{}{"type": "string"}, "description": map[string]interface{}{"type": "string"}, "labels": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}}},
		// PlanetScale
		{"name": "pscale_branches", "description": "List PlanetScale database branches.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"database"}, "properties": map[string]interface{}{"database": map[string]interface{}{"type": "string"}}}},
		{"name": "pscale_deploy", "description": "Create PlanetScale deploy request.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"database", "branch"}, "properties": map[string]interface{}{"database": map[string]interface{}{"type": "string"}, "branch": map[string]interface{}{"type": "string"}}}},
		// Prisma
		{"name": "prisma_status", "description": "Show Prisma migration status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "prisma_generate", "description": "Run prisma generate.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "prisma_push", "description": "Run prisma db push.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Drizzle
		{"name": "drizzle_push", "description": "Run drizzle-kit push.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "drizzle_generate", "description": "Run drizzle-kit generate.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Fly.io
		{"name": "fly_status", "description": "Show Fly.io app status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "fly_deploy", "description": "Deploy to Fly.io.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "fly_logs", "description": "Get Fly.io app logs.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"app"}, "properties": map[string]interface{}{"app": map[string]interface{}{"type": "string"}}}},
		// Railway
		{"name": "railway_status", "description": "Show Railway project status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "railway_deploy", "description": "Deploy to Railway.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
	}
	tools = append(tools, platformTools...)
	tools = append(tools, devEnvironmentCloneMCPTools()...)

	// --- Docker Extended ---
	dockerExtTools := []map[string]interface{}{
		{"name": "docker_prune", "description": "Prune Docker resources (containers, images, volumes, networks, or all).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"what"}, "properties": map[string]interface{}{"what": map[string]interface{}{"type": "string", "description": "containers, images, volumes, networks, or all"}}}},
		{"name": "docker_disk_usage", "description": "Show Docker disk usage.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "docker_networks", "description": "List Docker networks.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "docker_volumes", "description": "List Docker volumes.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "docker_inspect", "description": "Inspect a Docker container or image (detailed JSON).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"target"}, "properties": map[string]interface{}{"target": map[string]interface{}{"type": "string", "description": "Container or image name/ID"}}}},
		{"name": "docker_stats", "description": "Show live resource usage of all containers.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "docker_build", "description": "Build a Docker image from Dockerfile.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "tag": map[string]interface{}{"type": "string", "description": "Image tag"}, "dockerfile": map[string]interface{}{"type": "string"}}}},
		{"name": "docker_pull", "description": "Pull a Docker image.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"image"}, "properties": map[string]interface{}{"image": map[string]interface{}{"type": "string"}}}},
		{"name": "docker_push", "description": "Push a Docker image to registry.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"image"}, "properties": map[string]interface{}{"image": map[string]interface{}{"type": "string"}}}},
		{"name": "docker_stop", "description": "Stop a running container.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"container"}, "properties": map[string]interface{}{"container": map[string]interface{}{"type": "string"}}}},
		{"name": "docker_start", "description": "Start a stopped container.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"container"}, "properties": map[string]interface{}{"container": map[string]interface{}{"type": "string"}}}},
		{"name": "docker_restart", "description": "Restart a container.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"container"}, "properties": map[string]interface{}{"container": map[string]interface{}{"type": "string"}}}},
		{"name": "docker_rm", "description": "Remove a container.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"container"}, "properties": map[string]interface{}{"container": map[string]interface{}{"type": "string"}, "force": map[string]interface{}{"type": "boolean"}}}},
		{"name": "docker_rmi", "description": "Remove an image.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"image"}, "properties": map[string]interface{}{"image": map[string]interface{}{"type": "string"}, "force": map[string]interface{}{"type": "boolean"}}}},
		{"name": "docker_top", "description": "Show processes in a container.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"container"}, "properties": map[string]interface{}{"container": map[string]interface{}{"type": "string"}}}},
		{"name": "docker_port", "description": "Show port mappings.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"container"}, "properties": map[string]interface{}{"container": map[string]interface{}{"type": "string"}}}},
		{"name": "docker_cp", "description": "Copy files between host and container.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"source", "destination"}, "properties": map[string]interface{}{"source": map[string]interface{}{"type": "string"}, "destination": map[string]interface{}{"type": "string"}}}},
		{"name": "docker_history", "description": "Show image layer history.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"image"}, "properties": map[string]interface{}{"image": map[string]interface{}{"type": "string"}}}},
	}
	tools = append(tools, dockerExtTools...)

	// --- Git Extended ---
	gitExtTools := []map[string]interface{}{
		{"name": "git_stash", "description": "Manage git stashes (list, save, pop, apply, drop).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "list, save, pop, apply, drop"}, "message": map[string]interface{}{"type": "string"}}}},
		{"name": "git_blame_file", "description": "Show line-by-line blame.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}, "lines": map[string]interface{}{"type": "string", "description": "Line range (e.g. 10,20)"}}}},
		{"name": "git_log_advanced", "description": "Advanced git log with filters.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "author": map[string]interface{}{"type": "string"}, "since": map[string]interface{}{"type": "string"}, "until": map[string]interface{}{"type": "string"}, "path": map[string]interface{}{"type": "string"}, "count": map[string]interface{}{"type": "integer"}}}},
		{"name": "git_branches", "description": "List branches sorted by recent activity.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "git_tags", "description": "List tags.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "git_remotes", "description": "List remotes.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "git_reflog", "description": "Show reflog (undo history).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "count": map[string]interface{}{"type": "integer"}}}},
		{"name": "git_shortlog", "description": "Show commit count by author.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
	}
	tools = append(tools, gitExtTools...)

	// --- Helm ---
	helmTools := []map[string]interface{}{
		{"name": "helm_list", "description": "List Helm releases.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"namespace": map[string]interface{}{"type": "string"}}}},
		{"name": "helm_status", "description": "Show Helm release status.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"release"}, "properties": map[string]interface{}{"release": map[string]interface{}{"type": "string"}, "namespace": map[string]interface{}{"type": "string"}}}},
		{"name": "helm_values", "description": "Show Helm release values.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"release"}, "properties": map[string]interface{}{"release": map[string]interface{}{"type": "string"}, "namespace": map[string]interface{}{"type": "string"}}}},
		{"name": "helm_search", "description": "Search Helm charts.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}}}},
		{"name": "helm_repos", "description": "List Helm repos.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "helm_history", "description": "Show release history.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"release"}, "properties": map[string]interface{}{"release": map[string]interface{}{"type": "string"}, "namespace": map[string]interface{}{"type": "string"}}}},
	}
	tools = append(tools, helmTools...)

	// --- System Extended ---
	sysExtTools := []map[string]interface{}{
		{"name": "free_memory", "description": "Show memory/swap usage.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "listen_ports", "description": "Show listening ports and owning processes.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "find_large_files", "description": "Find large files.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "size_mb": map[string]interface{}{"type": "integer", "description": "Min size in MB (default: 100)"}}}},
		{"name": "tree_dir", "description": "Show directory tree.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "depth": map[string]interface{}{"type": "integer"}}}},
		{"name": "lines_of_code", "description": "Count lines of code by language.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
	}
	tools = append(tools, sysExtTools...)

	// --- Network & Packet Capture ---
	netTools := []map[string]interface{}{
		{"name": "tcpdump", "description": "Capture network packets with tcpdump.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"interface": map[string]interface{}{"type": "string", "description": "Network interface (default: any)"}, "count": map[string]interface{}{"type": "integer", "description": "Packets to capture (default: 20)"}, "filter": map[string]interface{}{"type": "string", "description": "BPF filter (e.g. 'tcp port 80', 'host 10.0.0.1')"}}}},
		{"name": "tcpdump_http", "description": "Capture HTTP/HTTPS packets.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"interface": map[string]interface{}{"type": "string"}, "count": map[string]interface{}{"type": "integer"}}}},
		{"name": "tcpdump_dns", "description": "Capture DNS packets.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"interface": map[string]interface{}{"type": "string"}, "count": map[string]interface{}{"type": "integer"}}}},
		{"name": "tshark", "description": "Capture and analyze packets with tshark (Wireshark CLI).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"interface": map[string]interface{}{"type": "string"}, "count": map[string]interface{}{"type": "integer"}, "filter": map[string]interface{}{"type": "string", "description": "Display filter (e.g. 'http', 'dns', 'tcp.port==443')"}, "fields": map[string]interface{}{"type": "string", "description": "Comma-separated fields (e.g. 'ip.src,ip.dst,tcp.port')"}}}},
		{"name": "pcap_analyze", "description": "Analyze a pcap file.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string", "description": "Path to .pcap file"}, "filter": map[string]interface{}{"type": "string"}}}},
		{"name": "pcap_stats", "description": "Show pcap file statistics (capinfos).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}}}},
		{"name": "netcat", "description": "TCP connection test or send data (nc).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host", "port"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string"}, "port": map[string]interface{}{"type": "integer"}, "data": map[string]interface{}{"type": "string", "description": "Data to send (empty = just test connection)"}}}},
		{"name": "port_scan", "description": "Scan common ports on a host (pure Go, no nmap needed).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string"}, "ports": map[string]interface{}{"type": "string", "description": "Comma-separated ports (default: common ports)"}}}},
		{"name": "arp_table", "description": "Show ARP table (IP → MAC mappings).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "arp_scan", "description": "Discover all devices on local network.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"subnet": map[string]interface{}{"type": "string", "description": "Subnet (default: 192.168.1.0/24)"}}}},
		{"name": "nmap_scan", "description": "Scan with nmap (quick, services, os, full, udp, ping).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"target"}, "properties": map[string]interface{}{"target": map[string]interface{}{"type": "string"}, "type": map[string]interface{}{"type": "string", "description": "quick, services, os, full, udp, ping"}, "ports": map[string]interface{}{"type": "string", "description": "Port range (e.g. '1-1000', '22,80,443')"}}}},
		{"name": "traceroute_host", "description": "Trace network route to host.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string"}, "max_hops": map[string]interface{}{"type": "integer"}}}},
		{"name": "mtr_report", "description": "Network diagnostic combining ping and traceroute.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string"}, "count": map[string]interface{}{"type": "integer"}}}},
		{"name": "network_interfaces", "description": "List all network interfaces with IPs and MACs.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "ip_route", "description": "Show routing table.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "network_connections", "description": "Show active network connections.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"state": map[string]interface{}{"type": "string", "description": "Filter by state: established, listen, time-wait"}}}},
		{"name": "bandwidth_test", "description": "Test bandwidth to an iperf3 server.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"host"}, "properties": map[string]interface{}{"host": map[string]interface{}{"type": "string", "description": "iperf3 server address"}}}},
		{"name": "curl_timings", "description": "Detailed HTTP timing breakdown (DNS, connect, TLS, TTFB, total).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"url"}, "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string"}}}},
	}
	tools = append(tools, netTools...)

	// --- Linux System ---
	linuxTools := []map[string]interface{}{
		// Kernel & modules
		{"name": "dmesg", "description": "Show kernel messages. Filter by level (emerg, alert, crit, err, warn, info, debug).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"level": map[string]interface{}{"type": "string", "description": "Log level filter: emerg, alert, crit, err, warn, info, debug"}, "lines": map[string]interface{}{"type": "integer", "description": "Number of lines (default: 100)"}}}},
		{"name": "lsmod", "description": "List loaded kernel modules.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "modinfo", "description": "Show kernel module info.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"module"}, "properties": map[string]interface{}{"module": map[string]interface{}{"type": "string"}}}},
		{"name": "insmod", "description": "Load a kernel module (modprobe).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"module"}, "properties": map[string]interface{}{"module": map[string]interface{}{"type": "string"}}}},
		{"name": "rmmod", "description": "Unload a kernel module.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"module"}, "properties": map[string]interface{}{"module": map[string]interface{}{"type": "string"}}}},
		{"name": "uname", "description": "Show kernel, architecture, hostname info.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "sysctl", "description": "Read kernel parameters. Pass a key or leave empty for all.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"key": map[string]interface{}{"type": "string", "description": "Sysctl key (e.g. net.ipv4.ip_forward)"}}}},
		// Processes
		{"name": "top_snapshot", "description": "Top processes snapshot sorted by CPU.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "ps_aux", "description": "List processes. Sort by cpu/mem, or filter by name.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"sort": map[string]interface{}{"type": "string", "description": "cpu or mem"}, "filter": map[string]interface{}{"type": "string", "description": "Filter by process name"}}}},
		{"name": "ps_tree", "description": "Show process tree.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "load_average", "description": "Show system load average and CPU count.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Memory
		{"name": "vmstat", "description": "Virtual memory statistics.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"count": map[string]interface{}{"type": "integer", "description": "Samples (default: 5)"}}}},
		{"name": "swap_info", "description": "Show swap usage.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Disk
		{"name": "df", "description": "Show disk space usage.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}}},
		{"name": "du", "description": "Show directory disk usage.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}, "depth": map[string]interface{}{"type": "integer", "description": "Max depth (default: 1)"}}}},
		{"name": "lsblk", "description": "List block devices (disks, partitions).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "fdisk_list", "description": "List disk partitions.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "mounts", "description": "Show mounted filesystems.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "iostat", "description": "Show I/O statistics per device.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Tree
		{"name": "tree", "description": "Show directory tree (auto-excludes node_modules, .git, etc.).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}, "depth": map[string]interface{}{"type": "integer", "description": "Max depth (default: 3)"}, "all": map[string]interface{}{"type": "boolean", "description": "Show hidden files"}, "dirs_only": map[string]interface{}{"type": "boolean"}}}},
		// Hardware
		{"name": "cpu_info", "description": "Show CPU info (brand, cores, architecture).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "lspci", "description": "List PCI devices (GPUs, NICs, etc.).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "lsusb", "description": "List USB devices.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "sensors", "description": "Show hardware sensors (CPU temp, fan speed).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Firewall
		{"name": "ufw", "description": "Manage UFW firewall (status, allow, deny, delete).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "status, allow, deny, delete"}, "rule": map[string]interface{}{"type": "string", "description": "Port/service (e.g. 80, 443/tcp, ssh)"}}}},
		{"name": "iptables_list", "description": "List iptables rules with line numbers.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Users & sessions
		{"name": "who_is_logged_in", "description": "Show who is logged in and what they're doing.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "last_logins", "description": "Show recent login history.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"count": map[string]interface{}{"type": "integer"}}}},
		{"name": "timedate_info", "description": "Show system time, timezone, NTP status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "hostname_info", "description": "Show hostname and OS info.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	}
	tools = append(tools, linuxTools...)

	// --- Compilers & Language Suites ---
	compilerTools := []map[string]interface{}{
		// Make
		{"name": "make_targets", "description": "List Makefile targets.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "make_run", "description": "Run a Make target.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "target": map[string]interface{}{"type": "string"}}}},
		{"name": "make_clean", "description": "Run make clean.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// CMake
		{"name": "cmake_configure", "description": "Configure CMake project.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "build_dir": map[string]interface{}{"type": "string", "description": "Build directory (default: build)"}, "generator": map[string]interface{}{"type": "string", "description": "e.g. Ninja, Unix Makefiles"}}}},
		{"name": "cmake_build", "description": "Build CMake project.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "build_dir": map[string]interface{}{"type": "string"}, "parallel": map[string]interface{}{"type": "integer"}}}},
		{"name": "cmake_test", "description": "Run CMake/CTest tests.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "build_dir": map[string]interface{}{"type": "string"}}}},
		{"name": "cmake_install", "description": "Install CMake project.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "build_dir": map[string]interface{}{"type": "string"}}}},
		// GCC/Clang/LLVM
		{"name": "gcc_compile", "description": "Compile C/C++ with GCC.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}, "output": map[string]interface{}{"type": "string"}, "flags": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "e.g. [\"-Wall\", \"-O2\", \"-std=c17\"]"}}}},
		{"name": "clang_compile", "description": "Compile C/C++ with Clang.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}, "output": map[string]interface{}{"type": "string"}, "flags": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}}},
		{"name": "clang_tidy_check", "description": "Run clang-tidy static analysis.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}, "directory": map[string]interface{}{"type": "string"}}}},
		{"name": "clang_format_file", "description": "Format C/C++ with clang-format.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}, "in_place": map[string]interface{}{"type": "boolean"}}}},
		{"name": "objdump", "description": "Disassemble a binary.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}}}},
		{"name": "binary_size", "description": "Show binary section sizes.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}}}},
		{"name": "nm_symbols", "description": "List symbols in a binary.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}}}},
		{"name": "compiler_version", "description": "Show compiler version (gcc, clang, rustc, go, etc.).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"compiler"}, "properties": map[string]interface{}{"compiler": map[string]interface{}{"type": "string", "description": "gcc, clang, rustc, go, javac, etc."}}}},
		// Cargo (Rust full suite)
		{"name": "cargo_build", "description": "Build Rust project.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "release": map[string]interface{}{"type": "boolean"}}}},
		{"name": "cargo_test_suite", "description": "Run Rust tests.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "test_name": map[string]interface{}{"type": "string", "description": "Specific test name filter"}}}},
		{"name": "cargo_clippy", "description": "Lint Rust with Clippy (pedantic).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "cargo_fmt", "description": "Format Rust code.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "check": map[string]interface{}{"type": "boolean", "description": "Check only, don't modify"}}}},
		{"name": "cargo_doc", "description": "Build Rust docs.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "open": map[string]interface{}{"type": "boolean"}}}},
		{"name": "cargo_bench_suite", "description": "Run Rust benchmarks.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "bench": map[string]interface{}{"type": "string"}}}},
		{"name": "cargo_tree_deps", "description": "Show Rust dependency tree.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "depth": map[string]interface{}{"type": "integer"}}}},
		{"name": "cargo_update_deps", "description": "Update Rust dependencies.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "cargo_audit_deps", "description": "Audit Rust deps for vulnerabilities.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "cargo_check_only", "description": "Check Rust without building.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "cargo_clean", "description": "Clean Rust build artifacts.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "cargo_add_crate", "description": "Add a Rust dependency.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"crate"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "crate": map[string]interface{}{"type": "string"}}}},
		{"name": "cargo_remove_crate", "description": "Remove a Rust dependency.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"crate"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "crate": map[string]interface{}{"type": "string"}}}},
		// Go (full suite)
		{"name": "go_build", "description": "Build Go project.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "output": map[string]interface{}{"type": "string"}}}},
		{"name": "go_test_suite", "description": "Run Go tests.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "verbose": map[string]interface{}{"type": "boolean"}, "race": map[string]interface{}{"type": "boolean"}, "cover": map[string]interface{}{"type": "boolean"}}}},
		{"name": "go_vet_check", "description": "Run Go vet.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "go_mod_tidy", "description": "Tidy Go modules.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "go_mod_graph", "description": "Show Go module dependency graph.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "go_mod_why", "description": "Explain why a Go module is needed.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"module"}, "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "module": map[string]interface{}{"type": "string"}}}},
		{"name": "go_generate", "description": "Run Go generate.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "go_fmt_check", "description": "Check Go formatting.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "go_staticcheck", "description": "Run staticcheck.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "go_vulncheck", "description": "Check Go deps for vulnerabilities.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Python (full suite)
		{"name": "pytest_suite", "description": "Run Python tests with pytest.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "verbose": map[string]interface{}{"type": "boolean"}, "coverage": map[string]interface{}{"type": "boolean"}, "marker": map[string]interface{}{"type": "string", "description": "Test marker filter"}}}},
		{"name": "ruff_suite", "description": "Run Ruff (check, format, or fix).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "action": map[string]interface{}{"type": "string", "description": "check, format, fix"}}}},
		{"name": "mypy_check", "description": "Type-check Python with mypy.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "black_format", "description": "Format Python with Black.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "check": map[string]interface{}{"type": "boolean"}}}},
		{"name": "pip_compile", "description": "Compile pip requirements.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		{"name": "uv_install", "description": "Install with uv.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}}}},
		// Node.js/TypeScript
		{"name": "npm_run_script", "description": "Run an npm script (or list all scripts).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "script": map[string]interface{}{"type": "string", "description": "Script name (empty = list all)"}}}},
		{"name": "tsc_check", "description": "TypeScript type checking.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "no_emit": map[string]interface{}{"type": "boolean"}}}},
		{"name": "eslint_check", "description": "ESLint check (with optional --fix).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "fix": map[string]interface{}{"type": "boolean"}}}},
		{"name": "prettier_check", "description": "Prettier format check or write.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "check": map[string]interface{}{"type": "boolean"}}}},
		{"name": "biome_suite", "description": "Biome check, format, or lint.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "action": map[string]interface{}{"type": "string", "description": "check, format, lint"}}}},
	}
	tools = append(tools, compilerTools...)

	// --- Static Analysis, Profiling & Debugging, Code Metrics ---
	analysisTools := []map[string]interface{}{
		// Static Analysis
		{"name": "cppcheck", "description": "Run cppcheck C/C++ static analysis.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "file": map[string]interface{}{"type": "string", "description": "Specific file to check"}, "severity": map[string]interface{}{"type": "string", "description": "Enable checks: warning, style, performance, portability, information, all"}, "enable_all": map[string]interface{}{"type": "boolean", "description": "Enable all checks"}}}},
		{"name": "shellcheck", "description": "Run ShellCheck on shell scripts.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"file"}, "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string"}, "shell": map[string]interface{}{"type": "string", "description": "Shell dialect: sh, bash, dash, ksh"}, "severity": map[string]interface{}{"type": "string", "description": "Minimum severity: error, warning, info, style"}}}},
		{"name": "hadolint", "description": "Lint a Dockerfile with Hadolint.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string", "description": "Dockerfile path (default: Dockerfile)"}, "trusted_registries": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Trusted Docker registries"}}}},
		{"name": "semgrep", "description": "Run Semgrep multi-language static analysis.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "config": map[string]interface{}{"type": "string", "description": "Semgrep config/rules (e.g. p/security-audit, p/owasp-top-ten, auto)"}, "auto_config": map[string]interface{}{"type": "boolean", "description": "Use auto config"}}}},
		{"name": "sonarscanner", "description": "Run SonarQube/SonarCloud scanner.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "project_key": map[string]interface{}{"type": "string"}, "host_url": map[string]interface{}{"type": "string", "description": "SonarQube server URL"}, "token": map[string]interface{}{"type": "string", "description": "Auth token"}}}},
		{"name": "bandit", "description": "Run Bandit Python security analysis.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "file": map[string]interface{}{"type": "string", "description": "Specific file to scan"}, "severity": map[string]interface{}{"type": "string", "description": "Minimum severity filter (l=low, ll=medium, lll=high)"}}}},
		{"name": "gosec", "description": "Run gosec Go security analysis.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "no_fail": map[string]interface{}{"type": "boolean", "description": "Don't return error exit code on findings"}}}},
		{"name": "brakeman", "description": "Run Brakeman Ruby/Rails security scanner.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "confidence": map[string]interface{}{"type": "integer", "description": "Minimum confidence level (1=high, 2=medium, 3=weak)"}}}},
		{"name": "safety_check", "description": "Check Python dependencies for known vulnerabilities.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "file": map[string]interface{}{"type": "string", "description": "Requirements file path"}}}},
		{"name": "trivy_fs_scan", "description": "Run Trivy filesystem vulnerability scan.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "severity": map[string]interface{}{"type": "string", "description": "Severities to report: CRITICAL,HIGH,MEDIUM,LOW"}}}},
		// Profiling & Debugging
		{"name": "valgrind_memcheck", "description": "Run Valgrind memcheck for memory leak detection.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"binary"}, "properties": map[string]interface{}{"binary": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Arguments to pass to the binary"}}}},
		{"name": "valgrind_callgrind", "description": "Run Valgrind callgrind for call graph profiling.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"binary"}, "properties": map[string]interface{}{"binary": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}, "output_file": map[string]interface{}{"type": "string", "description": "Output file (default: callgrind.out)"}}}},
		{"name": "valgrind_massif", "description": "Run Valgrind massif for heap profiling.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"binary"}, "properties": map[string]interface{}{"binary": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}, "output_file": map[string]interface{}{"type": "string", "description": "Output file (default: massif.out)"}}}},
		{"name": "gdb_backtrace", "description": "Get backtrace from a running process or binary using GDB.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"pid": map[string]interface{}{"type": "integer", "description": "Process ID to attach to"}, "binary": map[string]interface{}{"type": "string", "description": "Binary to run and get backtrace from"}}}},
		{"name": "lldb_backtrace", "description": "Get backtrace from a running process or binary using LLDB.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"pid": map[string]interface{}{"type": "integer", "description": "Process ID to attach to"}, "binary": map[string]interface{}{"type": "string", "description": "Binary to run and get backtrace from"}}}},
		{"name": "strace_trace", "description": "Trace system calls of a process (Linux).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"pid": map[string]interface{}{"type": "integer", "description": "Process ID to trace"}, "binary": map[string]interface{}{"type": "string", "description": "Binary to run and trace"}, "syscall_filter": map[string]interface{}{"type": "string", "description": "Syscall filter (e.g. open,read,write,network,file)"}, "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}}},
		{"name": "ltrace_trace", "description": "Trace library calls of a process (Linux).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"pid": map[string]interface{}{"type": "integer", "description": "Process ID to trace"}, "binary": map[string]interface{}{"type": "string", "description": "Binary to run and trace"}, "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}}},
		{"name": "perf_record", "description": "Record performance data using Linux perf.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"binary"}, "properties": map[string]interface{}{"binary": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}, "duration": map[string]interface{}{"type": "integer", "description": "Duration in seconds"}, "output_file": map[string]interface{}{"type": "string", "description": "Output file (default: perf.data)"}}}},
		{"name": "perf_stat", "description": "Collect performance counters using Linux perf.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"binary"}, "properties": map[string]interface{}{"binary": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}, "events": map[string]interface{}{"type": "string", "description": "Events to measure (e.g. cache-misses,instructions,cycles)"}}}},
		{"name": "go_pprof_cpu", "description": "Run Go CPU profiling via test benchmarks or pprof URL.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "duration": map[string]interface{}{"type": "integer", "description": "Profiling duration in seconds (default: 30)"}, "binary": map[string]interface{}{"type": "string", "description": "URL or binary to profile (e.g. http://localhost:6060)"}}}},
		{"name": "go_pprof_heap", "description": "Run Go heap profiling.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "url": map[string]interface{}{"type": "string", "description": "Running service URL (e.g. http://localhost:6060)"}}}},
		{"name": "heaptrack", "description": "Run heaptrack for heap allocation profiling (Linux).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"binary"}, "properties": map[string]interface{}{"binary": map[string]interface{}{"type": "string"}, "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}}},
		// Code Metrics
		{"name": "cyclomatic_complexity", "description": "Measure cyclomatic complexity (radon for Python, gocyclo for Go).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "language": map[string]interface{}{"type": "string", "description": "Language: python, go, js, ts (auto-detected if empty)"}}}},
		{"name": "lizard", "description": "Run lizard code complexity analysis (supports 20+ languages).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "threshold": map[string]interface{}{"type": "integer", "description": "Cyclomatic complexity threshold"}, "languages": map[string]interface{}{"type": "string", "description": "Comma-separated languages to analyze"}}}},
		{"name": "loc_count", "description": "Count lines of code (uses tokei/scc/cloc with fallbacks).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string"}, "tool": map[string]interface{}{"type": "string", "description": "Preferred tool: tokei, scc, cloc"}}}},
	}
	tools = append(tools, analysisTools...)

	// --- System Logs & Debugging ---
	sysLogTools := []map[string]interface{}{
		{"name": "journalctl", "description": "Query systemd journal logs with unit, priority, boot, and time filters.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"unit": map[string]interface{}{"type": "string", "description": "Service unit (e.g. nginx, docker, sshd)"}, "priority": map[string]interface{}{"type": "string", "description": "emerg, alert, crit, err, warning, notice, info, debug"}, "lines": map[string]interface{}{"type": "integer"}, "boot": map[string]interface{}{"type": "boolean"}, "since": map[string]interface{}{"type": "string", "description": "e.g. '1 hour ago', '2024-01-01'"}}}},
		{"name": "journalctl_errors", "description": "Show error-level entries from current boot.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "journalctl_disk_usage", "description": "Show journal disk usage.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "systemctl", "description": "Manage systemd services (status, start, stop, restart, enable, disable, list, failed, timers).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "status, start, stop, restart, enable, disable, list, failed, timers"}, "unit": map[string]interface{}{"type": "string"}}}},
		{"name": "gdb_attach", "description": "Attach GDB to a running process and get backtrace/registers.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"pid"}, "properties": map[string]interface{}{"pid": map[string]interface{}{"type": "integer"}, "commands": map[string]interface{}{"type": "string", "description": "GDB commands (default: bt + threads + registers)"}}}},
		{"name": "gdb_core_dump", "description": "Analyze a core dump file.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"binary", "corefile"}, "properties": map[string]interface{}{"binary": map[string]interface{}{"type": "string"}, "corefile": map[string]interface{}{"type": "string"}}}},
		{"name": "lldb_attach", "description": "Attach LLDB to a running process.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"pid"}, "properties": map[string]interface{}{"pid": map[string]interface{}{"type": "integer"}, "commands": map[string]interface{}{"type": "string"}}}},
		{"name": "coredump_list", "description": "List core dumps.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "coredump_info", "description": "Show core dump details.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"pid"}, "properties": map[string]interface{}{"pid": map[string]interface{}{"type": "string"}}}},
		{"name": "syslog", "description": "Read system log files with optional grep filter.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string", "description": "Log file path (auto-detect if empty)"}, "lines": map[string]interface{}{"type": "integer"}, "filter": map[string]interface{}{"type": "string"}}}},
		{"name": "auth_log", "description": "Show authentication/SSH logs.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"lines": map[string]interface{}{"type": "integer"}}}},
	}
	tools = append(tools, sysLogTools...)

	// --- Project wizard (fullstack generator) ---
	wizardTools := []map[string]interface{}{
		{
			"name":        "project_wizard_start",
			"description": "Start a new fullstack project wizard session. Returns sessionId + first question. Call project_wizard_answer repeatedly, then project_wizard_generate.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "project_wizard_answer",
			"description": "Submit an answer to the current wizard question. Returns the next question (or 'done' kind when complete).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"sessionId", "questionId", "answer"},
				"properties": map[string]interface{}{
					"sessionId":  map[string]interface{}{"type": "string"},
					"questionId": map[string]interface{}{"type": "string"},
					"answer":     map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "project_new_quick",
			"description": "One-shot fullstack project scaffold. Skips the interactive wizard and creates a self-hosted-first monorepo (apps/{web,landing,mobile}, packages/shared, backend/) at parentDir/<slug>. Defaults are built for first-capture with Claude Code/Codex over MCP: local/dev Convex backend that can deploy to hosted Convex, Next.js web on Cloudflare, static landing on Cloudflare Pages, Expo React Native mobile for iOS + Android, Apple/Google/email auth, native builds (xcodebuild + gradle, no EAS). Auto-inits git and pushes to GitHub/GitLab when gitProvider is set.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"name", "slug", "description"},
				"properties": map[string]interface{}{
					"name":           map[string]interface{}{"type": "string", "description": "Brand name (shown in README, landing page, metadata)"},
					"slug":           map[string]interface{}{"type": "string", "description": "URL-safe slug used for folder + package names"},
					"description":    map[string]interface{}{"type": "string", "description": "One-paragraph description — goes into README, landing hero, AI context"},
					"tagline":        map[string]interface{}{"type": "string"},
					"appTemplate":    map[string]interface{}{"type": "string", "description": "Product template, e.g. saas-dashboard, consumer-social, commerce, dev-tool"},
					"audienceType":   map[string]interface{}{"type": "string", "description": "consumers, developers, creators, internal-team, etc."},
					"problem":        map[string]interface{}{"type": "string", "description": "One-sentence user problem; defaults to description"},
					"uniqueAngle":    map[string]interface{}{"type": "string", "description": "What makes this different"},
					"monetization":   map[string]interface{}{"type": "string", "description": "free, freemium, subscription, one-time-purchase, etc."},
					"launchTimeline": map[string]interface{}{"type": "string", "description": "weekend, 1-2-weeks, 1-month, 3-months"},
					"domain":         map[string]interface{}{"type": "string"},
					"primaryColor":   map[string]interface{}{"type": "string", "description": "Hex e.g. #4F46E5"},
					"secondaryColor": map[string]interface{}{"type": "string"},
					"accentColor":    map[string]interface{}{"type": "string"},
					"surfaceColor":   map[string]interface{}{"type": "string"},
					"includeWeb":     map[string]interface{}{"type": "boolean", "description": "Default true"},
					"includeLanding": map[string]interface{}{"type": "boolean", "description": "Default true"},
					"includeMobile":  map[string]interface{}{"type": "boolean", "description": "Default true"},
					"includeBackend": map[string]interface{}{"type": "boolean", "description": "Default true"},
					"webHost":        map[string]interface{}{"type": "string", "enum": []string{"cloudflare", "vercel", "netlify", "self-host"}},
					"backend":        map[string]interface{}{"type": "string", "enum": []string{"sqlite", "postgres", "supabase", "convex", "pocketbase", "appwrite", "none"}},
					"oauthApple":     map[string]interface{}{"type": "boolean"},
					"oauthGoogle":    map[string]interface{}{"type": "boolean"},
					"oauthMicrosoft": map[string]interface{}{"type": "boolean"},
					"iosBundleId":    map[string]interface{}{"type": "string"},
					"androidPackage": map[string]interface{}{"type": "string"},
					"gitProvider":    map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab", "none"}},
					"gitVisibility":  map[string]interface{}{"type": "string", "enum": []string{"private", "public"}},
					"gitOrg":         map[string]interface{}{"type": "string"},
					"parentDir":      map[string]interface{}{"type": "string", "description": "Default: agent working dir"},
				},
			},
		},
		{
			"name":        "project_self_host_create",
			"description": "First-capture MCP tool for a new Yaver user: create the default self-hosted monorepo without asking them to touch npm/yaver CLI. This is an alias of project_new_quick with the intended product defaults: Convex local/dev backend (cloud-deployable later), Next.js Cloudflare web UI, Cloudflare landing page, Expo React Native iOS/Android app, shared package, legal/store-review scaffolds, and next steps for Yaver phone testing. Use this before suggesting hourly Yaver Managed Cloud.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"name", "slug", "description"},
				"properties": map[string]interface{}{
					"name":           map[string]interface{}{"type": "string"},
					"slug":           map[string]interface{}{"type": "string"},
					"description":    map[string]interface{}{"type": "string"},
					"tagline":        map[string]interface{}{"type": "string"},
					"appTemplate":    map[string]interface{}{"type": "string"},
					"audienceType":   map[string]interface{}{"type": "string"},
					"problem":        map[string]interface{}{"type": "string"},
					"uniqueAngle":    map[string]interface{}{"type": "string"},
					"monetization":   map[string]interface{}{"type": "string"},
					"launchTimeline": map[string]interface{}{"type": "string"},
					"domain":         map[string]interface{}{"type": "string"},
					"primaryColor":   map[string]interface{}{"type": "string"},
					"secondaryColor": map[string]interface{}{"type": "string"},
					"accentColor":    map[string]interface{}{"type": "string"},
					"surfaceColor":   map[string]interface{}{"type": "string"},
					"iosBundleId":    map[string]interface{}{"type": "string"},
					"androidPackage": map[string]interface{}{"type": "string"},
					"gitProvider":    map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab", "none"}},
					"gitVisibility":  map[string]interface{}{"type": "string", "enum": []string{"private", "public"}},
					"gitOrg":         map[string]interface{}{"type": "string"},
					"parentDir":      map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "project_wizard_generate",
			"description": "Materialise the scaffold for a completed wizard session. Returns the target directory + next-step checklist. Project folder is created at parentDir/<slug>.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"sessionId"},
				"properties": map[string]interface{}{
					"sessionId": map[string]interface{}{"type": "string"},
					"parentDir": map[string]interface{}{"type": "string", "description": "Parent directory for the new project (default: agent cwd)"},
				},
			},
		},
	}
	tools = append(tools, wizardTools...)

	// --- Self-hosted SaaS replacements ---
	//
	// Every tool below replaces a paid SaaS the solo dev would
	// otherwise be paying monthly for. They all run on the dev's
	// own machine (no Convex, no vendor) and follow the same
	// shape: one tool per common action, always returning
	// structured JSON so other agents can chain them.
	soloStackTools := []map[string]interface{}{
		// Forms
		{"name": "form_list", "description": "List all self-hosted forms with submission counts.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "form_create", "description": "Create a new form. Returns the form ID + a public /forms/:id/submit URL to paste into a landing page <form action=...>.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{
			"name":             map[string]interface{}{"type": "string"},
			"notifyEmail":      map[string]interface{}{"type": "string"},
			"honeypotField":    map[string]interface{}{"type": "string", "description": "Field name that real humans will leave blank; bots will fill."},
			"rateLimitPerHour": map[string]interface{}{"type": "integer"},
			"successRedirect":  map[string]interface{}{"type": "string"},
		}}},
		{"name": "form_submissions", "description": "Tail the most recent submissions for a form.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}}},
		{"name": "form_delete", "description": "Delete a form and its submission log.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}}},

		// Newsletter
		{"name": "newsletter_subscribers", "description": "List newsletter subscribers + status counts (confirmed / pending / unsubscribed).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "newsletter_create", "description": "Create a newsletter campaign draft.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"subject"}, "properties": map[string]interface{}{
			"subject":  map[string]interface{}{"type": "string"},
			"body":     map[string]interface{}{"type": "string"},
			"htmlBody": map[string]interface{}{"type": "string"},
		}}},
		{"name": "newsletter_send", "description": "Broadcast a newsletter campaign to all confirmed subscribers via the SMTP relay. This is irreversible.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}}},
		{"name": "newsletter_compose_from_git", "description": "Walk git log + gh/glab PRs and issues for a repo + window and draft a weekly recap newsletter. Optionally pipe through the AI runner for tone polish, and optionally save as a campaign draft.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"repo"}, "properties": map[string]interface{}{
			"repo":          map[string]interface{}{"type": "string", "description": "Absolute repo path"},
			"sinceDays":     map[string]interface{}{"type": "integer", "description": "Default 7"},
			"includePrs":    map[string]interface{}{"type": "boolean"},
			"includeIssues": map[string]interface{}{"type": "boolean"},
			"subject":       map[string]interface{}{"type": "string"},
			"instructions":  map[string]interface{}{"type": "string", "description": "Tone notes for the AI polish pass"},
			"execute":       map[string]interface{}{"type": "boolean", "description": "Pipe through runner for polish"},
			"saveDraft":     map[string]interface{}{"type": "boolean"},
		}}},

		// Job queue
		{"name": "jobs_list", "description": "List pending queue + dead-letter jobs.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "jobs_enqueue", "description": "Enqueue a new background job. Handlers are registered at agent boot; common ones: newsletter.send, form.notify, pdf.render.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"handler"}, "properties": map[string]interface{}{
			"handler":     map[string]interface{}{"type": "string"},
			"payload":     map[string]interface{}{"type": "object"},
			"delaySec":    map[string]interface{}{"type": "integer"},
			"maxAttempts": map[string]interface{}{"type": "integer"},
			"backoffSec":  map[string]interface{}{"type": "integer"},
		}}},
		{"name": "jobs_retry", "description": "Requeue a DLQ job.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}}},
		{"name": "jobs_cancel", "description": "Drop a pending queue job.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}}},

		// Image optimizer + PDF
		{"name": "img_optimize", "description": "Return a URL that resizes + reencodes an image on demand. Output is disk-cached.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"src"}, "properties": map[string]interface{}{
			"src":  map[string]interface{}{"type": "string"},
			"root": map[string]interface{}{"type": "string", "description": "Optional project root ID"},
			"w":    map[string]interface{}{"type": "integer"},
			"h":    map[string]interface{}{"type": "integer"},
			"fmt":  map[string]interface{}{"type": "string", "enum": []string{"webp", "jpeg", "png"}},
			"q":    map[string]interface{}{"type": "integer", "description": "Quality 1..100"},
		}}},
		{"name": "pdf_render", "description": "Render HTML or a URL to PDF via the embedded Chromium. Returns the PDF as base64. Use for invoices, receipts, reports.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"html":            map[string]interface{}{"type": "string"},
			"url":             map[string]interface{}{"type": "string"},
			"format":          map[string]interface{}{"type": "string", "description": "A4 | Letter | Legal | Tabloid | A3 | A5"},
			"landscape":       map[string]interface{}{"type": "boolean"},
			"printBackground": map[string]interface{}{"type": "boolean"},
		}}},

		// OAuth provider
		{"name": "oauth_client_list", "description": "List registered OAuth clients for the self-hosted identity provider.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "oauth_client_create", "description": "Register a new OAuth client. The returned client_secret is only shown once — save it immediately.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name", "redirectUris"}, "properties": map[string]interface{}{
			"name":         map[string]interface{}{"type": "string"},
			"redirectUris": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"scopes":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		}}},
		{"name": "oauth_user_list", "description": "List registered OAuth users (email + id only, never hashes).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "oauth_user_create", "description": "Create a new OAuth user with a scrypt-hashed password.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"email", "password"}, "properties": map[string]interface{}{
			"email":    map[string]interface{}{"type": "string"},
			"name":     map[string]interface{}{"type": "string"},
			"password": map[string]interface{}{"type": "string"},
		}}},

		// Mail (Gmail / O365 fetch + AI draft)
		{"name": "mail_inbox", "description": "Fetch the solo dev's inbox from Gmail or Microsoft 365. The classifier tags each message as personal / transactional / marketing / bulk using thread replies + List-Unsubscribe + Precedence + sender history — stricter than Gmail's Promotions tab. Returns normalised MailMessage objects.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"provider":     map[string]interface{}{"type": "string", "enum": []string{"gmail", "o365", "auto"}},
			"folder":       map[string]interface{}{"type": "string", "enum": []string{"inbox", "sent"}},
			"query":        map[string]interface{}{"type": "string", "description": "Gmail search syntax or Graph $search value"},
			"limit":        map[string]interface{}{"type": "integer"},
			"onlyPersonal": map[string]interface{}{"type": "boolean"},
		}}},
		{"name": "mail_draft", "description": "Draft a reply to a message. Pulls the thread + recent sent-folder mail for tone, then pipes through the configured AI runner when execute=true and returns the draft text. Otherwise returns the prompt the caller can execute manually.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{
			"id":           map[string]interface{}{"type": "string"},
			"provider":     map[string]interface{}{"type": "string"},
			"instructions": map[string]interface{}{"type": "string"},
			"execute":      map[string]interface{}{"type": "boolean"},
			"runner":       map[string]interface{}{"type": "string", "enum": []string{"", "claude", "codex", "opencode"}, "description": "Override default runner (claude / codex / opencode)."},
		}}},

		// URL shortener
		{"name": "short_list", "description": "List short URLs with click counts.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "short_create", "description": "Create a short URL. Auto-generates a 6-char code if not provided.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"url"}, "properties": map[string]interface{}{
			"url":   map[string]interface{}{"type": "string"},
			"code":  map[string]interface{}{"type": "string"},
			"label": map[string]interface{}{"type": "string"},
		}}},
		{"name": "short_clicks", "description": "Tail the last 500 click rows, optionally filtered by code.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"code": map[string]interface{}{"type": "string"}}}},
		{"name": "short_delete", "description": "Delete a short URL.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"code"}, "properties": map[string]interface{}{"code": map[string]interface{}{"type": "string"}}}},

		// Waitlist
		{"name": "waitlist_list", "description": "List all waitlist entries (owner-only, includes emails).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "waitlist_leaderboard", "description": "Top referrers (redacted — no emails). Safe to surface publicly.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "waitlist_delete", "description": "Remove a waitlist entry by email.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"email"}, "properties": map[string]interface{}{"email": map[string]interface{}{"type": "string"}}}},

		// Docs site
		{"name": "docs_config", "description": "Point the docs site at a markdown folder, set title / theme / logo.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"path":    map[string]interface{}{"type": "string"},
			"title":   map[string]interface{}{"type": "string"},
			"theme":   map[string]interface{}{"type": "string", "enum": []string{"light", "dark"}},
			"logoUrl": map[string]interface{}{"type": "string"},
		}}},
		{"name": "docs_list", "description": "Return the docs site sidebar tree.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "docs_search", "description": "Substring search across all doc pages.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"q"}, "properties": map[string]interface{}{"q": map[string]interface{}{"type": "string"}}}},

		// Meetings (Calendly-lite)
		{"name": "meeting_create", "description": "Create a new bookable event type. Uses Google Calendar (auto-creates Meet links) or Microsoft Graph (auto-creates Teams meetings) via the existing EmailConfig OAuth creds.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"slug", "title", "provider"}, "properties": map[string]interface{}{
			"slug":         map[string]interface{}{"type": "string"},
			"title":        map[string]interface{}{"type": "string"},
			"durationMin":  map[string]interface{}{"type": "integer"},
			"description":  map[string]interface{}{"type": "string"},
			"provider":     map[string]interface{}{"type": "string", "enum": []string{"google", "o365"}},
			"hosting":      map[string]interface{}{"type": "string", "enum": []string{"meet", "teams", "none"}},
			"daysAhead":    map[string]interface{}{"type": "integer"},
			"bufferMin":    map[string]interface{}{"type": "integer"},
			"availability": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object"}},
		}}},
		{"name": "meeting_list", "description": "List all bookable event types.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "meeting_bookings", "description": "List confirmed bookings across all event types.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},

		// --- Studio modules (clips, chat, A/B, invoices, affiliates, asciinema) ---

		// A/B experiments on top of flags
		{"name": "ab_experiment_create", "description": "Create or update an A/B experiment. Variants are weighted (summed, normalised); bucketing is sticky per userId via SHA256. Metric is the event name that counts as a conversion.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"key", "variants"}, "properties": map[string]interface{}{
			"key":      map[string]interface{}{"type": "string"},
			"name":     map[string]interface{}{"type": "string"},
			"variants": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "weight": map[string]interface{}{"type": "integer"}}}},
			"metric":   map[string]interface{}{"type": "string"},
		}}},
		{"name": "ab_experiment_list", "description": "List all A/B experiments.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "ab_assign", "description": "Deterministically assign a userId to a variant. Returns the variant name; empty if the experiment is not running. Logs an exposure event.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"key", "userId"}, "properties": map[string]interface{}{"key": map[string]interface{}{"type": "string"}, "userId": map[string]interface{}{"type": "string"}}}},
		{"name": "ab_event", "description": "Log an A/B exposure or conversion event. Use kind='conversion' when the tracked metric fires.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"key", "variant", "userId", "kind"}, "properties": map[string]interface{}{"key": map[string]interface{}{"type": "string"}, "variant": map[string]interface{}{"type": "string"}, "userId": map[string]interface{}{"type": "string"}, "kind": map[string]interface{}{"type": "string", "enum": []string{"exposure", "conversion"}}}}},
		{"name": "ab_results", "description": "Return exposure + conversion counts and conversion rate per variant.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"key"}, "properties": map[string]interface{}{"key": map[string]interface{}{"type": "string"}}}},

		// Clips (screen recording)
		{"name": "clip_start", "description": "Start a local screen recording on the agent's Mac or Linux box via ffmpeg. Returns the session id; call clip_stop to finalise.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"title": map[string]interface{}{"type": "string"}, "description": map[string]interface{}{"type": "string"}}}},
		{"name": "clip_stop", "description": "Stop the active screen recording. Finalises the mp4 with a flushed moov atom.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "clip_list", "description": "List recorded clip sessions with titles + durations.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},

		// Live chat
		{"name": "chat_conversations", "description": "List open chat conversations with the visitors from the self-hosted chat widget.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "chat_history", "description": "Return the full message history for one conversation (by visitor id).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"vid"}, "properties": map[string]interface{}{"vid": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "integer"}}}},
		{"name": "chat_reply", "description": "Send an owner-side reply to a visitor. Shows up live in the browser widget via SSE.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"vid", "text"}, "properties": map[string]interface{}{"vid": map[string]interface{}{"type": "string"}, "text": map[string]interface{}{"type": "string"}}}},

		// Invoices + Stripe / LemonSqueezy
		{"name": "customer_create", "description": "Add a billing customer (name, email, address). Required before creating an invoice for them.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name", "email"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "email": map[string]interface{}{"type": "string"}, "address": map[string]interface{}{"type": "string"}, "taxId": map[string]interface{}{"type": "string"}}}},
		{"name": "customer_list", "description": "List billing customers.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "invoice_create", "description": "Create a draft invoice with line items. Invoice number is assigned sequentially (INV-001, ...).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"customerId", "lineItems"}, "properties": map[string]interface{}{
			"customerId": map[string]interface{}{"type": "string"},
			"currency":   map[string]interface{}{"type": "string"},
			"dueAt":      map[string]interface{}{"type": "string", "description": "YYYY-MM-DD"},
			"taxPercent": map[string]interface{}{"type": "number"},
			"notes":      map[string]interface{}{"type": "string"},
			"lineItems":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"description": map[string]interface{}{"type": "string"}, "quantity": map[string]interface{}{"type": "number"}, "unitPrice": map[string]interface{}{"type": "number"}}}},
		}}},
		{"name": "invoice_list", "description": "List invoices (draft / sent / paid).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "invoice_render_pdf", "description": "Render an invoice to PDF via the embedded Chromium. Returns base64 bytes.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string", "description": "Invoice ID or number (INV-001)"}}}},
		{"name": "invoice_payment_link", "description": "Mint a Stripe or LemonSqueezy hosted checkout URL for an invoice. Pass the dev's API key (never stored). Writes the resulting link onto the invoice.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id", "provider", "apiKey"}, "properties": map[string]interface{}{
			"id":       map[string]interface{}{"type": "string"},
			"provider": map[string]interface{}{"type": "string", "enum": []string{"stripe", "lemonsqueezy"}},
			"apiKey":   map[string]interface{}{"type": "string"},
		}}},
		{"name": "invoice_send", "description": "Mark an invoice sent and email it to the customer with the payment link via the SMTP relay.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}}},

		// Affiliates
		{"name": "affiliate_create", "description": "Register an affiliate with a commission percentage. Returns the referral code (same space as /s/:code short links).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"email"}, "properties": map[string]interface{}{
			"email":             map[string]interface{}{"type": "string"},
			"name":              map[string]interface{}{"type": "string"},
			"commissionPercent": map[string]interface{}{"type": "number"},
		}}},
		{"name": "affiliate_list", "description": "List affiliates with owed / paid totals.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "affiliate_conversion", "description": "Record a sale attributed to an affiliate. Commission is computed from the affiliate's percentage.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id", "amount"}, "properties": map[string]interface{}{
			"id":        map[string]interface{}{"type": "string", "description": "Affiliate ID or code"},
			"amount":    map[string]interface{}{"type": "number"},
			"currency":  map[string]interface{}{"type": "string"},
			"sourceRef": map[string]interface{}{"type": "string"},
		}}},
		{"name": "affiliate_payout", "description": "Record that a payout was sent to an affiliate. Decrements totalOwed, increments totalPaid.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id", "amount"}, "properties": map[string]interface{}{
			"id":       map[string]interface{}{"type": "string"},
			"amount":   map[string]interface{}{"type": "number"},
			"currency": map[string]interface{}{"type": "string"},
			"method":   map[string]interface{}{"type": "string"},
			"note":     map[string]interface{}{"type": "string"},
		}}},

		// Asciinema-lite
		{"name": "cast_list", "description": "List recorded terminal casts (asciicast v2).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "cast_start", "description": "Start an agent-side terminal recording. Wraps the given command via the asciinema CLI. Only one recording at a time.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"title": map[string]interface{}{"type": "string"}, "command": map[string]interface{}{"type": "string"}}}},
		{"name": "cast_stop", "description": "Stop the active terminal recording and save it to the cast index.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},

		// Copilot-lite (local Ollama)
		{"name": "copilot_complete", "description": "Generate a code completion via the dev's local Ollama model. Supports fill-in-the-middle via prefix+suffix for Qwen2.5-Coder / StarCoder / DeepSeek. Replaces GitHub Copilot / Cursor for solo devs running Ollama.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"prefix"}, "properties": map[string]interface{}{
			"prefix":      map[string]interface{}{"type": "string", "description": "Text before the cursor"},
			"suffix":      map[string]interface{}{"type": "string", "description": "Text after the cursor for FIM"},
			"language":    map[string]interface{}{"type": "string"},
			"file":        map[string]interface{}{"type": "string"},
			"maxTokens":   map[string]interface{}{"type": "integer"},
			"model":       map[string]interface{}{"type": "string", "description": "Ollama tag, default qwen2.5-coder:7b"},
			"temperature": map[string]interface{}{"type": "number"},
		}}},
		{"name": "copilot_models", "description": "List the Ollama models the dev has pulled locally. Handy before calling copilot_complete.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	}
	tools = append(tools, soloStackTools...)

	// --- Guest Access ---
	guestTools := []map[string]interface{}{
		{
			"name":        "guest_invite",
			"description": "Invite a guest by email or Yaver user id to use your machine. Max 5 guests, invitation expires in 2 days. Default scope is 'feedback-only' — the hardened tier for end-users of your app (no /tasks, no /vibing, no dev-server proxy, no project enumeration; /info is redacted; any fix-triggered task runs inside Docker). Use scope='full' for teammate invites that need task / vibing / dev access, or scope='sdk-project' for Feedback SDK style project-scoped access.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"email": map[string]interface{}{
						"type":        "string",
						"description": "Email address of the person to invite. Provide either email or user_id.",
					},
					"user_id": map[string]interface{}{
						"type":        "string",
						"description": "Public Yaver user id of the person to invite. Provide either email or user_id.",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Access tier: 'feedback-only' (default, hardened end-user), 'sdk-project' (Feedback SDK style project-scoped access), or 'full' (classic teammate).",
						"enum":        []string{"full", "feedback-only", "sdk-project"},
					},
					"device_ids": map[string]interface{}{
						"type":        "array",
						"description": "Optional host device ids to pre-scope the invitation to specific machines. Empty = all host machines.",
						"items":       map[string]interface{}{"type": "string"},
					},
					"projects": map[string]interface{}{
						"type":        "array",
						"description": "Narrow this grant to specific project names/slugs on the host. Empty = all. Useful when feedback-only or sdk-project guests should only see Project A, not B/C.",
						"items":       map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		{
			"name":        "guest_list",
			"description": "List all guests who have been invited or have access to your machine.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "guest_revoke",
			"description": "Revoke guest access for an email address. Removes both pending invitations and active access.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"email"},
				"properties": map[string]interface{}{
					"email": map[string]interface{}{
						"type":        "string",
						"description": "Email address of the guest to remove",
					},
				},
			},
		},
		{
			"name":        "guest_config",
			"description": "View or update guest config (limits, runners, share preset, resource controls). Without email: list all. With email: show/update config.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"email": map[string]interface{}{
						"type":        "string",
						"description": "Guest email to view/update (omit to list all)",
					},
					"daily_limit": map[string]interface{}{
						"type":        "integer",
						"description": "Max task-seconds per day (0 = unlimited)",
					},
					"usage_mode": map[string]interface{}{
						"type":        "string",
						"description": "When guest can use: always, idle-only, scheduled",
						"enum":        []string{"always", "idle-only", "scheduled"},
					},
					"allowed_runners": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Runner IDs the guest can use (empty = all)",
					},
					"resource_preset": map[string]interface{}{
						"type":        "string",
						"description": "Share preset: machine-only, machine-with-host-keys, desktop-control, desktop-control-with-host-keys",
						"enum":        []string{"machine-only", "machine-with-host-keys", "desktop-control", "desktop-control-with-host-keys"},
					},
					"use_host_api_keys": map[string]interface{}{
						"type":        "boolean",
						"description": "Let the guest consume host-managed API keys without revealing the raw key",
					},
					"allow_guest_api_keys": map[string]interface{}{
						"type":        "boolean",
						"description": "Allow the guest to bring and use their own API keys on the shared infra",
					},
					"allow_desktop_control": map[string]interface{}{
						"type":        "boolean",
						"description": "Allow future remote desktop/control sessions on this shared machine",
					},
					"allow_browser_control": map[string]interface{}{
						"type":        "boolean",
						"description": "Allow browser automation/control sessions on this shared machine",
					},
					"allow_tunnel_forward": map[string]interface{}{
						"type":        "boolean",
						"description": "Allow guest access to host-approved local tunnel forwards",
					},
					"require_isolation": map[string]interface{}{
						"type":        "boolean",
						"description": "Require this guest's tasks to run in Docker isolation when available",
					},
					"cpu_limit_percent": map[string]interface{}{
						"type":        "integer",
						"description": "Soft CPU share cap for the guest on this host (1-100)",
					},
					"ram_limit_mb": map[string]interface{}{
						"type":        "integer",
						"description": "RAM cap in MB for the guest on this host",
					},
					"priority_mode": map[string]interface{}{
						"type":        "string",
						"description": "Scheduling policy for guest tasks",
						"enum":        []string{"same-priority", "spare-capacity", "background"},
					},
				},
			},
		},
		{
			"name":        "guest_usage",
			"description": "View guest usage stats for today or a specific date.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date": map[string]interface{}{
						"type":        "string",
						"description": "Date in YYYY-MM-DD format (default: today)",
					},
				},
			},
		},
	}
	tools = append(tools, guestTools...)

	// --- Primary-device preference (auto-connect) ---
	primaryTools := []map[string]interface{}{
		{
			"name":        "device_primary_get",
			"description": "Get the user's preferred device for auto-connect. Mobile, web, and the CLI use this when the user has multiple machines registered — single-device users auto-connect regardless.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "device_primary_set",
			"description": "Mark a device as the primary (auto-connect) target. Accepts a deviceId or unique prefix. Pass clear:true instead to unset the preference.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"deviceId": map[string]interface{}{
						"type":        "string",
						"description": "Full deviceId or unique prefix. Ignored when clear=true.",
					},
					"clear": map[string]interface{}{
						"type":        "boolean",
						"description": "Unset the preference instead of picking a device.",
					},
				},
			},
		},
		{
			"name":        "primary_auth",
			"description": "Re-auth on the user's primary remote device. With runner empty (default) this refreshes the Yaver session token via the existing /auth/recover flow (same as `yaver primary auth`). With runner=claude or codex, it kicks off the runner's browser/device-code login flow on the primary box (same as `yaver primary auth claude` / `yaver primary auth codex`); the response carries the URL/code the user opens to finish. Resolves the primary deviceId automatically — no need to look it up first.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"runner": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"", "claude", "claude-code", "codex"},
						"description": "Empty (default) for Yaver-level reauth; claude/claude-code or codex to start that runner's login flow on the primary device.",
					},
				},
			},
		},
		{
			"name":        "primary_status",
			"description": "Live status of the user's primary remote device — agent version, lifecycle (healthy / ready-to-connect / yaver-auth-expired / bootstrap), runners, dev-server, project, transport. Same data as `yaver primary status --json`. Resolves the primary deviceId automatically.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "primary_ping",
			"description": "Short reachability + auth check against the user's primary remote device. Returns transport, latency-class info, agent version, lifecycle, ownerEmail, and whether the box's owner matches the caller. Same shape as `yaver primary ping --json`.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "primary_projects",
			"description": "List projects discovered by the agent's filesystem scanner on the user's primary remote device. Pass mobile_only=true to filter to mobile-capable projects only (Expo / React Native / Flutter / Swift / Kotlin) — same as `yaver primary mobiles`. Discovery runs without any coding-agent installed on the box.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"mobile_only": map[string]interface{}{
						"type":        "boolean",
						"description": "When true, return only mobile-capable projects (mobile + Flutter + Swift + Kotlin).",
					},
				},
			},
		},
	}
	tools = append(tools, primaryTools...)

	// --- Grand MCP: ops + ops_plan + ops_verbs ---
	// Unified verb-based API (see YAVER_MCP_COVERAGE.md). Agents that
	// want one schema instead of 744 specialist tools call `ops`; they
	// discover available verbs via `ops_verbs`. The specialist tools
	// stay — ops is additive, not a replacement.
	opsTools := []map[string]interface{}{
		{
			"name":        "ops",
			"description": "Run one verb on one machine. Single API for every Yaver capability (info, run, build, test, deploy, push, reload, logs, status, env, session, scale, provision, destroy, ...). Discover available verbs via `ops_verbs`.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"verb"},
				"properties": map[string]interface{}{
					"machine": map[string]interface{}{
						"type":        "string",
						"description": "Target: \"local\", \"auto\", \"primary\", or a deviceId / alias. Cross-machine routing is supported; \"auto\" uses project-aware placement for deploy/reload and otherwise prefers the primary device before falling back to local.",
						"default":     "local",
					},
					"verb": map[string]interface{}{
						"type":        "string",
						"description": "Verb name. Call ops_verbs for the registered list + each verb's payload schema.",
					},
					"payload": map[string]interface{}{
						"type":        "object",
						"description": "Verb-specific payload. Shape depends on verb.",
					},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":        "ops_plan",
			"description": "Resolve the execution plan for one ops verb without running it. Returns project context, machine placement, and caller access policy so agents can inspect deploy/reload routing and guest/share constraints ahead of time.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"verb"},
				"properties": map[string]interface{}{
					"machine": map[string]interface{}{
						"type":        "string",
						"description": "Target: \"local\", \"auto\", \"primary\", or a deviceId / alias. Same semantics as `ops`.",
						"default":     "local",
					},
					"verb": map[string]interface{}{
						"type":        "string",
						"description": "Verb name to plan for.",
					},
					"payload": map[string]interface{}{
						"type":        "object",
						"description": "Verb-specific payload used for workDir / target / placement inference.",
					},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":        "ops_verbs",
			"description": "List every registered ops verb with its description, payload schema, whether it streams, and whether guests may call it. Call this once to populate the agent's knowledge of the ops API.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
	tools = append(tools, opsTools...)

	// --- SDK-token lifecycle (MCP) ---
	// CLI has `yaver sdk-token create` with scopes / CIDRs / expiry;
	// wiring the same through MCP lets an agent rotate its own scoped
	// tokens without shelling out. The raw token is returned ONCE —
	// same contract as the CLI — and is never re-fetchable.
	sdkTokenTools := []map[string]interface{}{
		{
			"name":        "sdk_token_create",
			"description": "Mint a new SDK token (scoped, IP-bound, optional expiry). The raw token is returned exactly once — store it immediately. Defaults: scopes=[\"feedback\",\"blackbox\"], no CIDR, 1-year expiry.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"label":        map[string]interface{}{"type": "string", "description": "Human-readable name shown in the dashboard."},
					"scopes":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "e.g. [\"feedback\",\"blackbox\",\"voice\",\"builds\"]. Omit for defaults."},
					"allowedCIDRs": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "CIDR allowlist (IPv4/IPv6)."},
					"expiresInMs":  map[string]interface{}{"type": "integer", "description": "Lifetime in milliseconds. Default: 1 year."},
				},
			},
		},
	}
	tools = append(tools, sdkTokenTools...)

	// --- Feedback SDK (MCP) ---
	// Covers the "attach a bug report from an agent" path. CLI already
	// has `yaver feedback list/show/fix/delete` — this MCP parity
	// means a vibe coder in Cursor can act on an SDK-captured crash
	// without leaving their chat window.
	feedbackTools := []map[string]interface{}{
		{
			"name":        "feedback_list",
			"description": "List recent feedback reports captured from the Feedback SDK (React Native / Flutter / Web). Includes crash logs, screenshots, black-box ring buffers.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "description": "Max items to return. Default 20."},
				},
			},
		},
		{
			"name":        "feedback_show",
			"description": "Return the full feedback report for one id: error metadata, stack, screenshot URL, and a recent-events snapshot from the BlackBox.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "feedback_fix",
			"description": "Create a task that asks the wrapped AI runner to fix the issue captured in this feedback report. Pulls in the stack trace + BlackBox context automatically.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id":     map[string]interface{}{"type": "string"},
					"runner": map[string]interface{}{"type": "string", "description": "Optional runner override (claude-code / codex / aider / ...)."},
				},
			},
		},
		{
			"name":        "feedback_delete",
			"description": "Remove a feedback report. Destructive.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string"},
				},
			},
		},
	}
	tools = append(tools, feedbackTools...)

	// --- Source maps (MCP) ---
	// Table-stakes coverage gap: the CLI has `yaver sourcemaps`
	// upload/list/delete/resolve. Agents that drive mobile releases
	// want to upload a Hermes sourcemap right after a build so crash
	// reports in the Errors dashboard symbolicate. Maps stay on the
	// agent's disk (~/.yaver/sourcemaps/) — never shipped to Convex.
	sourcemapTools := []map[string]interface{}{
		{
			"name":        "sourcemaps_list",
			"description": "List uploaded source maps — returns {app: [versions]}. Maps are stored locally on the agent.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "sourcemaps_delete",
			"description": "Remove the source map for a specific app + version tuple. Destructive.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"app", "version"},
				"properties": map[string]interface{}{
					"app":     map[string]interface{}{"type": "string"},
					"version": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "sourcemaps_resolve",
			"description": "Resolve a compiled line:col against the stored source map for app+version. Returns {source, line, column, name} or an error when the map is missing.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"app", "version", "line", "column"},
				"properties": map[string]interface{}{
					"app":     map[string]interface{}{"type": "string"},
					"version": map[string]interface{}{"type": "string"},
					"line":    map[string]interface{}{"type": "integer"},
					"column":  map[string]interface{}{"type": "integer"},
				},
			},
		},
	}
	tools = append(tools, sourcemapTools...)

	// --- Monorepo workspace manifest ---
	monorepoWorkspaceTools := []map[string]interface{}{
		{
			"name":        "workspace_init",
			"description": "Wire every app declared in yaver.workspace.yaml: scaffold init.md, env-check, per-app setup. Call workspace_scaffold first if no manifest exists. Idempotent — re-runs skip already-initialised apps unless force=true.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"root":           map[string]interface{}{"type": "string", "description": "Repo root. Defaults to agent CWD."},
					"force":          map[string]interface{}{"type": "boolean", "description": "Overwrite existing init.md files."},
					"dryRun":         map[string]interface{}{"type": "boolean"},
					"onlyApp":        map[string]interface{}{"type": "string", "description": "Restrict to a single app name."},
					"autoinitPrompt": map[string]interface{}{"type": "boolean", "description": "Include per-app `yaver autoinit` hints in the result."},
				},
			},
		},
		{
			"name":        "workspace_list",
			"description": "Return apps declared in yaver.workspace.yaml, ordered by dependency (leaf-first).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"root": map[string]interface{}{"type": "string"}},
			},
		},
		{
			"name":        "workspace_status",
			"description": "Per-app runtime status: on-disk presence, init.md freshness, missing env vars. Read-only.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"root": map[string]interface{}{"type": "string"}},
			},
		},
		{
			"name":        "workspace_scaffold",
			"description": "Detect apps in the current directory and return a starter yaver.workspace.yaml. Does NOT write the file — caller decides (keeps the tool side-effect-free for agents that want to review first).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"root": map[string]interface{}{"type": "string"}},
			},
		},
		{
			"name":        "workspace_web_apps",
			"description": "Return workspace apps whose stack maps to a web surface (nextjs, vite, flutter, react-native-expo). Used by the Web Reload dashboard tab to populate its app picker.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"root": map[string]interface{}{"type": "string", "description": "Repo root. Defaults to agent CWD."},
					"kind": map[string]interface{}{"type": "string", "description": "Comma-separated kinds to keep (web,hybrid,mobile). Defaults to web,hybrid."},
				},
			},
		},
		{
			"name":        "web_preview_start",
			"description": "Start a web dev server (Next.js, Vite, Flutter Web, Expo Web) for a named workspace app. Returns the iframe URL to embed.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app":     map[string]interface{}{"type": "string", "description": "workspace app name"},
					"workDir": map[string]interface{}{"type": "string", "description": "absolute project path (when app is empty)"},
					"root":    map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "web_preview_reload",
			"description": "Trigger a hot reload on the active web dev server.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "web_preview_stop",
			"description": "Stop serving the active web preview.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "preview_stop_serving",
			"description": "Stop serving the active preview/dev server, regardless of whether it is Expo Web, Vite, Next.js, Flutter Web, or another active preview surface.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "project_context",
			"description": "Fetch the repo's agent-guidance files (CLAUDE.md, AGENTS.md, AI_ARCH.md, REMOTE_WORKER.md) plus the project's init.md. Every result is prefixed with a stale-docs warning. Use this at the start of a task for context, but remember: the docs may be out of date — always grep the code to verify claims before acting on them.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workDir": map[string]interface{}{"type": "string", "description": "Project root (defaults to the agent's active work-dir)."},
				},
			},
		},
		{
			"name":        "diagnose",
			"description": "Run the yaver self-check (binary paths, running procs, ports, auth state, workspace manifest, systemd unit, runtime deps). Returns the event list and final summary. Equivalent to `yaver diagnose`.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"only": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"skip": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"fix":  map[string]interface{}{"type": "boolean", "default": false},
				},
			},
		},
	}
	tools = append(tools, monorepoWorkspaceTools...)

	// --- Managed / self-hosted toggle (per subsystem) ---
	// Single-checkbox UX across every Yaver surface: each subsystem
	// (relay, dns, analytics, storage, email, ci, voice, llm) runs
	// either against Yaver-hosted infra or against user-provided
	// credentials. Default is neither — subsystems retain their
	// legacy behaviour until the user explicitly opts in.
	managedTools := []map[string]interface{}{
		{
			"name":        "managed_get",
			"description": "Read the per-subsystem managed:true|false toggle. Returns {subsystem: value?} — missing keys mean \"not set\" → legacy behaviour.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "managed_set",
			"description": "Set one subsystem's managed-flag. `managed:true` = use Yaver-hosted infra for this subsystem; `managed:false` = user-hosted; `managed:null` = clear the preference (revert to default). Valid subsystems: relay, dns, analytics, storage, email, ci, voice, llm.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"subsystem"},
				"properties": map[string]interface{}{
					"subsystem": map[string]interface{}{"type": "string", "enum": []string{"relay", "dns", "analytics", "storage", "email", "ci", "voice", "llm"}},
					"managed":   map[string]interface{}{"description": "true | false | null"},
				},
			},
		},
	}
	tools = append(tools, managedTools...)

	// --- Remote Support Sessions ---
	// In-memory, TTL'd, owner-initiated remote-control grant. Think
	// TeamViewer, not Convex-tied guest access.
	supportTools := []map[string]interface{}{
		{
			"name":        "support_start",
			"description": "Open a TeamViewer-style remote-support window on this machine. Returns a 6-char code, a scoped bearer token, and shareable URLs. A guest who redeems the code gets terminal / exec / file-browse access for the TTL. Revoke anytime with support_stop.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"ttl": map[string]interface{}{
						"type":        "string",
						"description": "Duration string (e.g. \"30m\", \"2h\"). Default 30m.",
					},
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Optional tag — e.g. \"cousin\" or \"support-ticket-1234\" — shown in status.",
					},
				},
			},
		},
		{
			"name":        "support_status",
			"description": "Return the active remote-support session (code, expiry, allowed URL prefixes) or {active:false}.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "support_stop",
			"description": "Revoke the active remote-support session. Any bearer token redeemed from it stops working on the next request.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
	tools = append(tools, supportTools...)

	// --- Container Sandbox ---
	sandboxTools := []map[string]interface{}{
		{
			"name":        "sandbox_status",
			"description": "Check Docker container sandbox status (available, image ready, containerization settings).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "sandbox_config",
			"description": "Enable/disable container isolation for guest or host tasks. Configure resource limits and network mode. Changes are persisted to config file.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"containerize_guests": map[string]interface{}{
						"type":        "boolean",
						"description": "Run guest tasks in Docker containers",
					},
					"containerize_host": map[string]interface{}{
						"type":        "boolean",
						"description": "Run host tasks in Docker containers",
					},
					"cpu_limit": map[string]interface{}{
						"type":        "string",
						"description": "CPU limit (e.g. '2.0')",
					},
					"memory_limit": map[string]interface{}{
						"type":        "string",
						"description": "Memory limit (e.g. '4g')",
					},
					"network_mode": map[string]interface{}{
						"type":        "string",
						"description": "Network mode: 'host', 'bridge', or 'none'",
						"enum":        []string{"host", "bridge", "none"},
					},
					"read_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Read-only root filesystem (writes only to /workspace, /tmp)",
					},
				},
			},
		},
		{
			"name":        "sandbox_quickstart",
			"description": "One-step containerization setup for Yaver. Picks a practical default, persists it, and optionally starts building the yaver-sandbox image so remote-dev and shared-infra tasks can use containers without manual setup.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"mode": map[string]interface{}{
						"type":        "string",
						"description": "Quickstart mode: 'guests' for shared infra isolation, or 'host' to containerize all tasks",
						"enum":        []string{"guests", "host"},
					},
					"build_image": map[string]interface{}{
						"type":        "boolean",
						"description": "Start building the sandbox image immediately (default true)",
					},
				},
			},
		},
	}
	tools = append(tools, sandboxTools...)

	// --- Password Management ---
	passwordTools := []map[string]interface{}{
		{
			"name":        "forgot_password",
			"description": "Send a password reset email to an email-authenticated Yaver user. The reset link expires in 1 hour. Rate-limited to 5 requests per email per day.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"email"},
				"properties": map[string]interface{}{
					"email": map[string]interface{}{
						"type":        "string",
						"description": "Email address of the account to reset",
					},
				},
			},
		},
		{
			"name":        "change_password",
			"description": "Change the password of the currently authenticated email user. Requires the current password and a new password (min 8 characters).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"current_password", "new_password"},
				"properties": map[string]interface{}{
					"current_password": map[string]interface{}{
						"type":        "string",
						"description": "The current password",
					},
					"new_password": map[string]interface{}{
						"type":        "string",
						"description": "The new password (minimum 8 characters)",
					},
				},
			},
		},
	}
	tools = append(tools, passwordTools...)

	// --- yaver-test-sdk: local CI runner ---
	// Exposes the embedded test runner over MCP so any AI tool the dev
	// is already using (Claude Code, Cursor, Aider, Codex) can list
	// specs, kick off runs, read failures, and propose patches without
	// any custom integration. Same Go agent process, same data, no
	// cloud round trip.
	testkitTools := []map[string]interface{}{
		{
			"name":        "testkit_list_specs",
			"description": "List the yaver-test-sdk specs in the current project (yaver-tests/**/*.test.yaml). Returns name, path, target, step count.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"root": map[string]interface{}{
						"type":        "string",
						"description": "Spec root directory (default: yaver-tests)",
					},
				},
			},
		},
		{
			"name":        "testkit_run",
			"description": "Run the yaver-test-sdk specs end-to-end on the dev's machine via the embedded chromedp runner. Returns suite results inline. Use this to drive a 'fix → test → fix' loop without spawning Playwright.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"root":        map[string]interface{}{"type": "string", "description": "Spec root (default: yaver-tests)"},
					"only":        map[string]interface{}{"type": "string", "description": "Only run a single spec by name"},
					"concurrency": map[string]interface{}{"type": "integer", "description": "Parallel workers (default 1)"},
					"retries":     map[string]interface{}{"type": "integer", "description": "Flake retries (default 0)"},
					"headful":     map[string]interface{}{"type": "boolean", "description": "Show the browser visibly"},
					"video":       map[string]interface{}{"type": "boolean", "description": "Force screencast capture for every spec (overrides per-spec artifacts.video). Frames flush to the run's artifact dir on both pass + fail so the workspace clip player can scrub the full timeline."},
				},
			},
		},
		{
			"name":        "testkit_last_failure",
			"description": "Read the most recent failed run from local history. Returns the spec name, the failing step, the screenshot path, and the error — exactly what an AI agent needs to propose a patch.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"root": map[string]interface{}{"type": "string", "description": "Spec root (default: yaver-tests)"},
				},
			},
		},
		{
			"name":        "testkit_flake_report",
			"description": "Per-spec failure ratios over the last 100 runs. Use to identify chronically broken or flaky specs that need attention.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"root": map[string]interface{}{"type": "string", "description": "Spec root (default: yaver-tests)"},
				},
			},
		},
		{
			"name":        "testkit_self_heal_selector",
			"description": "Given a CSS selector that no longer matches and a DOM HTML snapshot, ask the user's vision/text LLM to propose a new selector. Returns the suggested replacement plus the model's reasoning. Used by the autonomous test-fix loop.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"failed_selector", "dom_html"},
				"properties": map[string]interface{}{
					"failed_selector": map[string]interface{}{"type": "string", "description": "The CSS selector that failed"},
					"dom_html":        map[string]interface{}{"type": "string", "description": "The current page HTML"},
					"intent":          map[string]interface{}{"type": "string", "description": "Optional: what the selector was supposed to find"},
				},
			},
		},
	}
	tools = append(tools, testkitTools...)

	// --- Monitor tools (errors / flags / releases / uptime / analytics) ---
	monitorTools := []map[string]interface{}{
		{
			"name":        "error_list",
			"description": "List cross-device error records aggregated from every SDK session. Each record has a fingerprint, message, count, device list, and recent samples.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"include_resolved": map[string]interface{}{"type": "boolean", "description": "Include resolved errors in the list"},
				},
			},
		},
		{
			"name":        "error_resolve",
			"description": "Mark an error as resolved with an optional note. The record stays in the ledger but drops off the open-errors view.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"fingerprint"},
				"properties": map[string]interface{}{
					"fingerprint": map[string]interface{}{"type": "string"},
					"note":        map[string]interface{}{"type": "string", "description": "One-liner on what fixed it"},
				},
			},
		},
		{
			"name":        "flag_list",
			"description": "List every self-hosted feature flag with default, rollout percent, and overrides.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "flag_set",
			"description": "Create or update a feature flag. Use this to flip a kill switch, start a rollout, or add a new flag.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"key"},
				"properties": map[string]interface{}{
					"key":            map[string]interface{}{"type": "string"},
					"type":           map[string]interface{}{"type": "string", "enum": []string{"bool", "string"}},
					"defaultBool":    map[string]interface{}{"type": "boolean"},
					"defaultString":  map[string]interface{}{"type": "string"},
					"rolloutPercent": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
					"description":    map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "flag_evaluate",
			"description": "Evaluate every flag for a specific userId. Useful for debugging rollout bucketing.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"userId"},
				"properties": map[string]interface{}{
					"userId": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "release_list",
			"description": "List releases in a self-hosted OTA channel (default: production). Shows latest pointer, rollout percent, and every historical bundle.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"channel": map[string]interface{}{"type": "string", "description": "Channel name, default production"},
				},
			},
		},
		{
			"name":        "release_rollout",
			"description": "Set the rollout percentage for a release channel (0..100).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"channel", "percent"},
				"properties": map[string]interface{}{
					"channel": map[string]interface{}{"type": "string"},
					"percent": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
				},
			},
		},
		{
			"name":        "release_rollback",
			"description": "Roll a channel's latest pointer back to a previously published semver.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"channel", "semver"},
				"properties": map[string]interface{}{
					"channel": map[string]interface{}{"type": "string"},
					"semver":  map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "monitor_list",
			"description": "List every uptime monitor with state, streak, interval, and last-check timestamp.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "monitor_add",
			"description": "Register a new uptime monitor for a URL. The agent probes every interval; three consecutive failures fire a push alert.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"url"},
				"properties": map[string]interface{}{
					"url":      map[string]interface{}{"type": "string"},
					"name":     map[string]interface{}{"type": "string"},
					"interval": map[string]interface{}{"type": "string", "description": "Go duration, e.g. 60s, 5m"},
					"method":   map[string]interface{}{"type": "string", "description": "HTTP method, default GET"},
				},
			},
		},
		{
			"name":        "monitor_remove",
			"description": "Delete an uptime monitor by id or name.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "analytics_events",
			"description": "Read recent business-event records from the analytics ledger (BlackBox track() channel). Returns the tail since a unix-ms timestamp.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"since": map[string]interface{}{"type": "integer", "description": "Unix ms filter — only return events newer than this"},
					"limit": map[string]interface{}{"type": "integer", "description": "Max events to return, default 100"},
				},
			},
		},
	}
	tools = append(tools, monitorTools...)

	autodevTools := []map[string]interface{}{
		{
			"name":        "autoinit_start",
			"description": "Bootstrap a project init.md (cached project context for autonomous yaver runs). Drastically cuts the per-kick token + wall-clock cost of autoideas because runners read init.md instead of re-grepping the project on every kick. Idempotent — re-runs replace only the AI-generated section, preserving any human-edited prose between markers.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"work_dir"},
				"properties": map[string]interface{}{
					"project":  map[string]interface{}{"type": "string"},
					"work_dir": map[string]interface{}{"type": "string"},
					"prompt":   map[string]interface{}{"type": "string", "description": "extra context to bias the description"},
					"engine":   map[string]interface{}{"type": "string", "enum": []string{"claude", "codex"}},
					"output":   map[string]interface{}{"type": "string", "description": "default init.md"},
					"force":    map[string]interface{}{"type": "boolean", "description": "regenerate even if init.md already has a generated section"},
				},
			},
		},
		{
			"name":        "autoinit_status",
			"description": "Quick `is init done?` check for a project. Returns {done, path, bytes, updated_at, has_generated_section, has_history_section}. Mobile / web call this to show a green check or a 'Run autoinit' button.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"work_dir"},
				"properties": map[string]interface{}{
					"work_dir": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "autoideas_start",
			"description": "Start a yaver autoideas run on a local project. Long-lived loop that asks the AI for fresh single-PR-sized ideas every tick and appends them as `- [ ] <title>` lines to ideas.md (or --output). Mobile/web renders them as checkboxes the user can use as a prompt source for /tasks runs.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"work_dir"},
				"properties": map[string]interface{}{
					"project":     map[string]interface{}{"type": "string"},
					"work_dir":    map[string]interface{}{"type": "string"},
					"hours":       map[string]interface{}{"type": "string"},
					"load":        map[string]interface{}{"type": "string", "enum": []string{"lite", "high"}},
					"prompt":      map[string]interface{}{"type": "string"},
					"harden":      map[string]interface{}{"type": "string", "enum": []string{"", "security", "memory", "perf", "quality", "all"}},
					"engine":      map[string]interface{}{"type": "string", "enum": []string{"claude", "codex"}},
					"output":      map[string]interface{}{"type": "string", "description": "default ideas.md"},
					"max_batches": map[string]interface{}{"type": "integer"},
					"tick":        map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			"name":        "autoideas_file",
			"description": "Read the current ideas file as a structured list of {line, checked, title}.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"work_dir"},
				"properties": map[string]interface{}{
					"work_dir": map[string]interface{}{"type": "string"},
					"output":   map[string]interface{}{"type": "string"},
				},
			},
		},
	}
	tools = append(tools, autodevTools...)

	agentTools := []map[string]interface{}{
		{
			"name":        "agent_machine_inventory",
			"description": "List Yaver mesh machines with online state, hardware slots, runner readiness, and machine profile signatures so an MCP client can choose a machine pool.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "agent_graph_start",
			"description": "Start a dependency-aware agent graph. Pass allowed_devices to choose the Yaver mesh pool and allowed_runners to constrain which runners remote nodes may use. Custom nodes can request self-hosted resource modes like build, deploy, browser, sim-ios, sim-android, phone, proof-video, or video-summary and carry prior machine/runner affinity into placement.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"prompt"},
				"properties": map[string]interface{}{
					"name":             map[string]interface{}{"type": "string", "description": "Optional graph name"},
					"work_dir":         map[string]interface{}{"type": "string", "description": "Absolute work directory. Defaults to the current agent work dir."},
					"prompt":           map[string]interface{}{"type": "string", "description": "Goal for the graph."},
					"template":         map[string]interface{}{"type": "string", "description": "Graph template: full or ship"},
					"runner":           map[string]interface{}{"type": "string", "description": "Optional forced runner"},
					"model":            map[string]interface{}{"type": "string", "description": "Optional forced model"},
					"max_parallel":     map[string]interface{}{"type": "integer", "description": "Maximum concurrently running nodes"},
					"preferred_device": map[string]interface{}{"type": "string", "description": "Optional preferred machine id or name"},
					"allowed_devices": map[string]interface{}{
						"type":        "array",
						"description": "Optional machine ids or names to form the execution pool",
						"items":       map[string]interface{}{"type": "string"},
					},
					"allowed_runners": map[string]interface{}{
						"type":        "array",
						"description": "Optional runner IDs to allow for graph nodes, e.g. ollama, opencode, codex",
						"items":       map[string]interface{}{"type": "string"},
					},
					"nodes": map[string]interface{}{
						"type":        "array",
						"description": "Optional explicit node list. If omitted, Yaver builds a template graph from prompt/template. Node resource_modes can request self-hosted build, deploy, browser, simulator, phone, and proof-video resources.",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id":                   map[string]interface{}{"type": "string"},
								"title":                map[string]interface{}{"type": "string"},
								"kind":                 map[string]interface{}{"type": "string", "description": "chat | autodev | autoideas | autotest"},
								"prompt":               map[string]interface{}{"type": "string"},
								"depends_on":           map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"runner":               map[string]interface{}{"type": "string"},
								"model":                map[string]interface{}{"type": "string"},
								"engine":               map[string]interface{}{"type": "string"},
								"work_dir":             map[string]interface{}{"type": "string"},
								"project":              map[string]interface{}{"type": "string"},
								"target":               map[string]interface{}{"type": "string"},
								"load":                 map[string]interface{}{"type": "string"},
								"hours":                map[string]interface{}{"type": "string"},
								"max_iterations":       map[string]interface{}{"type": "integer"},
								"no_autotest":          map[string]interface{}{"type": "boolean"},
								"preferred_device":     map[string]interface{}{"type": "string"},
								"allowed_devices":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"allowed_runners":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"prior_device":         map[string]interface{}{"type": "string", "description": "Bias placement toward the machine that handled earlier rounds for this node."},
								"prior_runner":         map[string]interface{}{"type": "string", "description": "Bias placement toward the runner that handled earlier rounds for this node."},
								"sticky_device":        map[string]interface{}{"type": "boolean", "description": "Strongly prefer prior_device when possible."},
								"sticky_runner":        map[string]interface{}{"type": "boolean", "description": "Strongly prefer prior_runner when possible."},
								"resource_modes":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Self-hosted resource hints like build, deploy, browser, sim-ios, sim-android, phone, proof-video, video-summary, test-video, or custom labels."},
								"preferred_video_mode": map[string]interface{}{"type": "string", "description": "Preferred capture target when resource_modes request proof-video/video-summary: browser, sim-ios, sim-android, phone."},
								"toughness":            map[string]interface{}{"type": "number", "description": "Relative difficulty / decomposition pressure for the node."},
								"design_points":        map[string]interface{}{"type": "number", "description": "Weight toward design/planning oriented runners."},
								"build_points":         map[string]interface{}{"type": "number", "description": "Weight toward build/implementation oriented runners and hosts."},
								"verify_points":        map[string]interface{}{"type": "number", "description": "Weight toward verification/proof resources and runners."},
							},
						},
					},
				},
			},
		},
		{
			"name":        "agent_graph_list",
			"description": "List agent graphs with node status, placement, and summaries.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "agent_graph_show",
			"description": "Show one agent graph with node placements and placement reasons.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"graph_id"},
				"properties": map[string]interface{}{
					"graph_id": map[string]interface{}{"type": "string", "description": "Agent graph id"},
				},
			},
		},
		{
			"name":        "agent_graph_stop",
			"description": "Stop a running agent graph.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"graph_id"},
				"properties": map[string]interface{}{
					"graph_id": map[string]interface{}{"type": "string", "description": "Agent graph id"},
				},
			},
		},
		{
			"name":        "morning_latest",
			"description": "Return the most recent morning match-report — what shipped overnight from an autodev run. One line per task: title, status (shipped/failed/rolled-back), files changed, commit sha, whether a video was captured. Use when the user asks what ran overnight or what's new.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "morning_list",
			"description": "List recent morning match-report runs (newest first). Use when the user wants to see history of overnight runs before drilling into one.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "description": "Max runs to return (default 20)"},
				},
			},
		},
		{
			"name":        "morning_show",
			"description": "Return the full match report for a specific run id: every task's title, status, git stats, video metadata, and rollback state if applicable.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"run_id"},
				"properties": map[string]interface{}{
					"run_id": map[string]interface{}{"type": "string", "description": "Run id as reported by morning_latest or morning_list"},
				},
			},
		},
		{
			"name":        "morning_rollback",
			"description": "Revert a single task's commits (git revert, new commit chain — never destructive). The task must have recorded CommitSHAs. Returns the new HEAD sha. Use only when the user explicitly asks to undo a task.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"run_id", "task_id"},
				"properties": map[string]interface{}{
					"run_id":  map[string]interface{}{"type": "string"},
					"task_id": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "record_drivers",
			"description": "Report which screen-recording drivers are available on this host (ffmpeg for macOS/Linux/Windows screen capture, xcrun for iOS Simulator, adb for Android emulator). Use this before record_start so the user knows what install step they may need.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "record_start",
			"description": "Start capturing a video for the morning reel. Picks the best-available driver for the requested target, falling back to full-screen capture if the specific target (ios-sim/android-emu) is not ready.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"run_id", "task_id"},
				"properties": map[string]interface{}{
					"run_id":  map[string]interface{}{"type": "string"},
					"task_id": map[string]interface{}{"type": "string"},
					"target":  map[string]interface{}{"type": "string", "description": "screen | ios-sim | android-emu (default: screen)"},
				},
			},
		},
		{
			"name":        "record_stop",
			"description": "Finalize the recording started for (run_id, task_id). Returns duration_ms + size_bytes. After this the video is served at /recordings/{run_id}/{task_id}/video.mp4 with byte-range support.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"run_id", "task_id"},
				"properties": map[string]interface{}{
					"run_id":  map[string]interface{}{"type": "string"},
					"task_id": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "totp_status",
			"description": "Show whether two-factor authentication is enabled for the signed-in Yaver user. 2FA is optional and gates only session issuance — in-flight QUIC/relay traffic is never affected.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "totp_enable_begin",
			"description": "Start two-factor enrollment. Returns a base32 secret and an otpauth:// URL the user can scan into Microsoft Authenticator, Google Authenticator, 1Password, or any RFC 6238 TOTP app. After scanning, the user must confirm by running totp_enable_confirm with a 6-digit code from the app.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "totp_enable_confirm",
			"description": "Confirm two-factor enrollment with a 6-digit code from the authenticator app, enabling 2FA for the account. Returns 8 one-time recovery codes — show them to the user once and instruct them to save somewhere safe.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"code"},
				"properties": map[string]interface{}{
					"code": map[string]interface{}{"type": "string", "description": "Current 6-digit TOTP code from the authenticator"},
				},
			},
		},
		{
			"name":        "totp_disable",
			"description": "Disable two-factor authentication. Requires a current 6-digit code from the authenticator (recovery codes are intentionally NOT accepted for disable, to stop an attacker with only leaked recovery codes from turning 2FA off).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"code"},
				"properties": map[string]interface{}{
					"code": map[string]interface{}{"type": "string", "description": "Current 6-digit TOTP code from the authenticator"},
				},
			},
		},
		{
			"name":        "code_mesh_start",
			"description": "Start a `yaver code --mesh` run: plan → implement → verify chat chain across the available machine pool. Thin wrapper over agent_graph_start with defaults matching the yaver code CLI (template=full, max_parallel=2). Shared-infra machines borrowed from other hosts are automatically considered by the placement planner; use allowed_runners when a shared machine only permits local runners like ollama. Optional custom nodes can request build, deploy, browser, simulator, phone, proof-video, and video-summary self-hosted resources.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"prompt"},
				"properties": map[string]interface{}{
					"prompt":          map[string]interface{}{"type": "string", "description": "What you want built."},
					"name":            map[string]interface{}{"type": "string", "description": "Optional session name"},
					"work_dir":        map[string]interface{}{"type": "string", "description": "Absolute work directory. Defaults to the current agent work dir."},
					"max_parallel":    map[string]interface{}{"type": "integer", "description": "Maximum concurrently running nodes (default 2)"},
					"allowed_devices": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional machine ids or names to form the execution pool"},
					"allowed_runners": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional runner IDs to allow (e.g. ollama,opencode,codex)"},
					"nodes": map[string]interface{}{
						"type":        "array",
						"description": "Optional explicit nodes. If omitted, the standard plan → implement → verify template is used.",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id":                   map[string]interface{}{"type": "string"},
								"title":                map[string]interface{}{"type": "string"},
								"kind":                 map[string]interface{}{"type": "string", "description": "chat | autodev | autoideas | autotest"},
								"prompt":               map[string]interface{}{"type": "string"},
								"depends_on":           map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"runner":               map[string]interface{}{"type": "string"},
								"model":                map[string]interface{}{"type": "string"},
								"work_dir":             map[string]interface{}{"type": "string"},
								"preferred_device":     map[string]interface{}{"type": "string"},
								"allowed_devices":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"allowed_runners":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"prior_device":         map[string]interface{}{"type": "string"},
								"prior_runner":         map[string]interface{}{"type": "string"},
								"sticky_device":        map[string]interface{}{"type": "boolean"},
								"sticky_runner":        map[string]interface{}{"type": "boolean"},
								"resource_modes":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"preferred_video_mode": map[string]interface{}{"type": "string"},
								"toughness":            map[string]interface{}{"type": "number"},
								"design_points":        map[string]interface{}{"type": "number"},
								"build_points":         map[string]interface{}{"type": "number"},
								"verify_points":        map[string]interface{}{"type": "number"},
							},
						},
					},
				},
			},
		},
	}
	tools = append(tools, agentTools...)

	// Browser automation tools — AI-driven browser control on the dev machine.
	browserTools := []map[string]interface{}{
		{
			"name":        "browser_open",
			"description": "Open a new Chrome browser session on the dev machine. Returns a session_id to use in subsequent browser_* calls. Sessions persist across tool calls — cookies, auth state, and current URL survive between steps.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Custom session ID (auto-generated if omitted)"},
					"headful":    map[string]interface{}{"type": "boolean", "description": "Show browser window visibly (default: false, headless)"},
				},
			},
		},
		{
			"name":        "browser_close",
			"description": "Close a browser session and release the Chrome process.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session to close"},
				},
			},
		},
		{
			"name":        "browser_sessions",
			"description": "List all active browser sessions with their current URL, title, and age.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "browser_navigate",
			"description": "Navigate to a URL. Returns a screenshot of the page after navigation plus the page title. Use this as the first step after browser_open.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id", "url"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"url":        map[string]interface{}{"type": "string", "description": "URL to navigate to"},
				},
			},
		},
		{
			"name":        "browser_click",
			"description": "Click an element by CSS selector. Returns a screenshot after clicking. Wait for any animations/navigation to settle.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id", "selector"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"selector":   map[string]interface{}{"type": "string", "description": "CSS selector of element to click"},
				},
			},
		},
		{
			"name":        "browser_type",
			"description": "Type text into an input field by CSS selector. Returns a screenshot after typing. Set clear=true to clear existing text first.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id", "selector", "text"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"selector":   map[string]interface{}{"type": "string", "description": "CSS selector of input field"},
					"text":       map[string]interface{}{"type": "string", "description": "Text to type"},
					"clear":      map[string]interface{}{"type": "boolean", "description": "Clear field before typing (default: false)"},
				},
			},
		},
		{
			"name":        "browser_select",
			"description": "Select a value in a <select> dropdown by CSS selector. Returns a screenshot after selection.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id", "selector", "value"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"selector":   map[string]interface{}{"type": "string", "description": "CSS selector of <select> element"},
					"value":      map[string]interface{}{"type": "string", "description": "Option value to select"},
				},
			},
		},
		{
			"name":        "browser_scroll",
			"description": "Scroll the page by pixel offsets. Returns a screenshot after scrolling.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"x":          map[string]interface{}{"type": "integer", "description": "Horizontal scroll pixels (default: 0)"},
					"y":          map[string]interface{}{"type": "integer", "description": "Vertical scroll pixels (default: 300)"},
				},
			},
		},
		{
			"name":        "browser_wait",
			"description": "Wait for a CSS selector to become visible on the page. Use before clicking/typing elements that load dynamically.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id", "selector"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"selector":   map[string]interface{}{"type": "string", "description": "CSS selector to wait for"},
					"timeout_ms": map[string]interface{}{"type": "integer", "description": "Timeout in milliseconds (default: 10000)"},
				},
			},
		},
		{
			"name":        "browser_wait_navigation",
			"description": "Wait for the page URL to change (e.g., after a form submission or OAuth redirect).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"timeout_ms": map[string]interface{}{"type": "integer", "description": "Timeout in milliseconds (default: 10000)"},
				},
			},
		},
		{
			"name":        "browser_screenshot",
			"description": "Capture a screenshot of the current page. Returns base64 PNG. Most action tools (navigate, click, type) already return screenshots — use this only when you need an extra screenshot without performing an action.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
				},
			},
		},
		{
			"name":        "browser_extract_text",
			"description": "Extract visible text content from an element. Useful for reading API keys, status messages, form values, etc.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"selector":   map[string]interface{}{"type": "string", "description": "CSS selector (default: body)"},
				},
			},
		},
		{
			"name":        "browser_extract_attribute",
			"description": "Extract an HTML attribute value from an element (e.g., href, src, value, data-*).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id", "selector", "attribute"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"selector":   map[string]interface{}{"type": "string", "description": "CSS selector"},
					"attribute":  map[string]interface{}{"type": "string", "description": "Attribute name (e.g., href, value, data-id)"},
				},
			},
		},
		{
			"name":        "browser_get_dom",
			"description": "Get the full page HTML (truncated to 50KB). Use to understand page structure when screenshots aren't enough — find element selectors, form fields, buttons, etc.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
				},
			},
		},
		{
			"name":        "browser_evaluate",
			"description": "Execute JavaScript in the browser and return the result. Use for complex interactions, reading localStorage/cookies, or extracting data that CSS selectors can't reach.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"session_id", "javascript"},
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID"},
					"javascript": map[string]interface{}{"type": "string", "description": "JavaScript code to execute. Return value is sent back as JSON."},
				},
			},
		},
	}
	tools = append(tools, browserTools...)

	// --- Vibe Preview ---
	// Lets the AI runner check what its own visual change looked like
	// after a kick — closes the loop "I edited the nav, did it land?"
	// without the runner having to re-read its own screenshots.
	vibePreviewTools := []map[string]interface{}{
		{
			"name":        "vibe_preview_start",
			"description": "Start a vibe-preview session: headless Chrome captures the dev server URL at adaptive FPS. The mobile app + web dashboard see the same SSE stream you do. Returns the session metadata.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"project", "target_url"},
				"properties": map[string]interface{}{
					"project":    map[string]interface{}{"type": "string", "description": "Project name (used as session key)"},
					"target_url": map[string]interface{}{"type": "string", "description": "Dev server URL to capture (e.g. http://127.0.0.1:3000)"},
					"mode":       map[string]interface{}{"type": "string", "enum": []string{"live", "change-only", "summary-only"}, "description": "Capture cadence; default live"},
					"profile":    map[string]interface{}{"type": "string", "description": "Optional profile override: live-direct | live-relay-wifi | live-relay-cell | change-only | summary-only"},
				},
			},
		},
		{
			"name":        "vibe_preview_stop",
			"description": "Stop a vibe-preview session by project. Idempotent.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"project"},
				"properties": map[string]interface{}{
					"project": map[string]interface{}{"type": "string", "description": "Project name"},
				},
			},
		},
		{
			"name":        "vibe_preview_status",
			"description": "List active vibe-preview sessions: project, profile, FPS, mode, frame count, error count.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "vibe_preview_snapshot",
			"description": "Force one capture against an active session. Returns the new frame's seq + content hash. Pair with vibe_preview_summarize to ask Claude what changed.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"project"},
				"properties": map[string]interface{}{
					"project": map[string]interface{}{"type": "string", "description": "Project name"},
				},
			},
		},
		{
			"name":        "vibe_preview_clip_record",
			"description": "Record a short MP4 demo clip from a booted simulator/emulator while the running app is exercised by Maestro (Phase 7). source: 'sim-ios' uses xcrun simctl, 'sim-android' uses adb screenrecord. Returns the clip metadata; poll vibe_preview_clips to see when status flips to ready.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"project"},
				"properties": map[string]interface{}{
					"project":          map[string]interface{}{"type": "string", "description": "Project name"},
					"source":           map[string]interface{}{"type": "string", "enum": []string{"sim-ios", "sim-android", "phone", "browser"}, "description": "Capture source (auto-detect if omitted)"},
					"duration_max_sec": map[string]interface{}{"type": "integer", "description": "Recording duration cap (default 12, max 30)"},
				},
			},
		},
		{
			"name":        "vibe_preview_clips",
			"description": "List recent clips for a project, newest first. Each clip has id, source, status (recording|ready|failed), durationSec, sizeBytes.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"project"},
				"properties": map[string]interface{}{
					"project": map[string]interface{}{"type": "string", "description": "Project name"},
				},
			},
		},
		{
			"name":        "vibe_preview_summaries",
			"description": "Read recent text summaries for a project (Phase 4). Each entry is a one-sentence description of what visibly changed between two frames, with before/after hashes. Default returns last 50; use limit to widen.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"project"},
				"properties": map[string]interface{}{
					"project": map[string]interface{}{"type": "string", "description": "Project name"},
					"limit":   map[string]interface{}{"type": "integer", "description": "Max entries (default 50, cap 500)"},
				},
			},
		},
	}
	tools = append(tools, vibePreviewTools...)

	// --- Workspace features: pipeline, analytics, auth, mail, expose, stripe, monitor, models, lemonsqueezy ---
	workspaceTools := []map[string]interface{}{
		// Pipeline
		{"name": "pipeline_run", "description": "Run a local CI/CD pipeline from GitHub Actions or GitLab CI YAML. Executes on the dev machine — no cloud runner needed. Hardware-aware, supports matrix builds, Docker services, caching, and artifacts.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"file": map[string]interface{}{"type": "string", "description": "YAML file path (auto-detects from .github/workflows/ or .gitlab-ci.yml if empty)"}, "job": map[string]interface{}{"type": "string", "description": "Specific job name (runs all if empty)"}, "dry_run": map[string]interface{}{"type": "boolean", "description": "Print steps without executing"}}}},
		{"name": "pipeline_status", "description": "Show status of current or last pipeline run with per-step results, durations, and hardware profile.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "pipeline_list", "description": "List available CI/CD pipelines (both GitHub Actions and GitLab CI).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"dir": map[string]interface{}{"type": "string", "description": "Project directory (default: current work dir)"}}}},
		{"name": "pipeline_stop", "description": "Cancel a running pipeline.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "pipeline_cancel_cloud", "description": "Cancel running GitHub Actions or GitLab CI for the current commit to save cloud CI costs.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"provider"}, "properties": map[string]interface{}{"provider": map[string]interface{}{"type": "string", "description": "CI provider: github or gitlab"}}}},
		{"name": "pipeline_hardware", "description": "Detect hardware profile: CPU, RAM, disk, GPU, Docker availability. Shows recommended parallel job count.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Analytics
		{"name": "analytics_start", "description": "Start a self-hosted analytics stack via Docker. Replaces PostHog Cloud ($0-450/mo), Mixpanel, Plausible.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"engine": map[string]interface{}{"type": "string", "description": "Analytics engine: plausible (default, light), umami (simplest), or posthog (most features)"}}}},
		{"name": "analytics_stop", "description": "Stop the analytics Docker stack.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "analytics_status", "description": "Show analytics engine status: running, port, memory usage, URL.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "analytics_selfhost_events", "description": "Query recent analytics events from self-hosted engine.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"event": map[string]interface{}{"type": "string", "description": "Event name filter"}, "person_id": map[string]interface{}{"type": "string", "description": "Person/user filter"}, "last": map[string]interface{}{"type": "string", "description": "Time window: 24h, 7d, 30d"}}}},
		{"name": "analytics_dashboard", "description": "Get key analytics metrics: pageviews, visitors, top pages, top events.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "analytics_setup", "description": "Get integration code snippet for your frontend framework.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"framework": map[string]interface{}{"type": "string", "description": "Framework: next, react, vue, html (auto-detects if empty)"}}}},
		// Auth dev server
		{"name": "auth_dev_start", "description": "Start a local auth server for development. Replaces Clerk ($25/mo), Auth0, WorkOS.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"engine": map[string]interface{}{"type": "string", "description": "Auth engine: logto (default) or keycloak"}}}},
		{"name": "auth_dev_stop", "description": "Stop the local auth server.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "auth_dev_status", "description": "Show auth server status: running, port, user count.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "auth_dev_users", "description": "Manage test users in the local auth server.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "list, create, or delete"}, "email": map[string]interface{}{"type": "string"}, "password": map[string]interface{}{"type": "string"}, "role": map[string]interface{}{"type": "string"}}}},
		{"name": "auth_dev_setup", "description": "Get auth integration code for your frontend framework.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"framework": map[string]interface{}{"type": "string", "description": "nextjs, react, or generic"}}}},
		{"name": "auth_dev_tokens", "description": "Generate or inspect JWT tokens for API testing.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "description": "generate or inspect"}, "email": map[string]interface{}{"type": "string", "description": "User email (for generate)"}, "token": map[string]interface{}{"type": "string", "description": "JWT token (for inspect)"}}}},
		// Mail dev server
		{"name": "mail_dev_start", "description": "Start local SMTP catch-all server (mailpit). Replaces Resend ($10-30/mo), Mailtrap. Catches all outgoing email for testing.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "mail_dev_stop", "description": "Stop the local mail server.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "mail_dev_status", "description": "Show mail server status and message count.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "mail_dev_inbox", "description": "List caught emails.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"to": map[string]interface{}{"type": "string", "description": "Filter by recipient"}, "subject": map[string]interface{}{"type": "string", "description": "Search subject"}, "limit": map[string]interface{}{"type": "integer", "description": "Max results (default 25)"}}}},
		{"name": "mail_dev_read", "description": "Read a specific caught email by ID.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string", "description": "Message ID"}}}},
		{"name": "mail_dev_clear", "description": "Delete all caught emails.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "mail_dev_config", "description": "Get SMTP config to add to your app (host, port, user, pass).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Expose (localhost tunnels)
		{"name": "expose_start", "description": "Expose a local port to the internet. Replaces ngrok ($10/mo). Uses Cloudflare Quick Tunnel (free, zero config).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"port"}, "properties": map[string]interface{}{"port": map[string]interface{}{"type": "integer", "description": "Local port to expose"}, "subdomain": map[string]interface{}{"type": "string", "description": "Preferred subdomain (best-effort)"}}}},
		{"name": "expose_stop", "description": "Stop a tunnel.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"port": map[string]interface{}{"type": "integer", "description": "Port to stop (0 = stop all)"}}}},
		{"name": "expose_list", "description": "List active tunnels with their public URLs.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Stripe dev tools
		{"name": "stripe_listen", "description": "Start Stripe webhook listener for local development. Forwards webhooks to localhost.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"port": map[string]interface{}{"type": "integer", "description": "Local port (default 3000)"}, "path": map[string]interface{}{"type": "string", "description": "Webhook path (default /api/webhooks/stripe)"}, "events": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Event filter (e.g. payment_intent.succeeded)"}}}},
		{"name": "stripe_stop", "description": "Stop the Stripe webhook listener.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "stripe_trigger", "description": "Trigger a test Stripe webhook event.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"event"}, "properties": map[string]interface{}{"event": map[string]interface{}{"type": "string", "description": "Event type (e.g. payment_intent.succeeded, checkout.session.completed)"}}}},
		{"name": "stripe_status", "description": "Show Stripe webhook listener status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Uptime monitor
		{"name": "uptime_monitor_add", "description": "Add a URL to uptime monitoring. Replaces BetterStack ($10-25/mo). Alerts via Telegram/Discord/Slack.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name", "url"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "url": map[string]interface{}{"type": "string"}, "interval_sec": map[string]interface{}{"type": "integer", "description": "Check interval in seconds (default 60)"}, "expected_status": map[string]interface{}{"type": "integer", "description": "Expected HTTP status (default 200)"}}}},
		{"name": "uptime_monitor_remove", "description": "Remove a URL from monitoring.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}}}},
		{"name": "uptime_monitor_list", "description": "List all monitored URLs with current status, uptime %, latency.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "uptime_monitor_status", "description": "Quick overview: how many up/down, any active alerts.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "uptime_monitor_history", "description": "Show check history for a monitored URL.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "integer", "description": "Max checks to return (default 50)"}}}},
		// Models (Ollama)
		{"name": "models_list", "description": "List installed Ollama models with sizes.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "models_pull", "description": "Download an Ollama model.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string", "description": "Model name (e.g. llama3.2, codellama, nomic-embed-text)"}}}},
		{"name": "models_remove", "description": "Remove an Ollama model to free disk space.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}}}},
		{"name": "models_run", "description": "Quick inference with a local model.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"model", "prompt"}, "properties": map[string]interface{}{"model": map[string]interface{}{"type": "string"}, "prompt": map[string]interface{}{"type": "string"}, "system": map[string]interface{}{"type": "string", "description": "System prompt (optional)"}}}},
		{"name": "models_serve", "description": "Start Ollama server if not running.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "models_ps", "description": "Show currently loaded models and their memory usage.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "models_recommend", "description": "Recommend models based on your hardware (RAM, GPU).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "models_status", "description": "Check Ollama status: running, port, GPU, model count.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		// Lemon Squeezy
		{"name": "lemonsqueezy_status", "description": "Check Lemon Squeezy API connectivity and store info.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "lemonsqueezy_products", "description": "List Lemon Squeezy products with prices.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"limit": map[string]interface{}{"type": "integer", "description": "Max results (default 25)"}}}},
		{"name": "lemonsqueezy_orders", "description": "List orders, optionally filtered by email.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"limit": map[string]interface{}{"type": "integer"}, "email": map[string]interface{}{"type": "string"}}}},
		{"name": "lemonsqueezy_subscriptions", "description": "List subscriptions, optionally filtered by status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"limit": map[string]interface{}{"type": "integer"}, "status": map[string]interface{}{"type": "string", "description": "active, cancelled, expired, past_due"}}}},
		{"name": "lemonsqueezy_customers", "description": "List customers.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"limit": map[string]interface{}{"type": "integer"}, "email": map[string]interface{}{"type": "string"}}}},
		{"name": "lemonsqueezy_revenue", "description": "Revenue dashboard: total revenue, MRR, active subs, order count.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "lemonsqueezy_discounts", "description": "List discount codes.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"limit": map[string]interface{}{"type": "integer"}}}},
		{"name": "lemonsqueezy_create_discount", "description": "Create a discount code.", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"name", "code", "amount", "amount_type"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "code": map[string]interface{}{"type": "string"}, "amount": map[string]interface{}{"type": "integer", "description": "Amount (percentage or cents)"}, "amount_type": map[string]interface{}{"type": "string", "description": "percent or fixed"}, "product_id": map[string]interface{}{"type": "string"}}}},
		{"name": "lemonsqueezy_webhook_listen", "description": "Start local webhook listener for Lemon Squeezy events.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"port": map[string]interface{}{"type": "integer", "description": "Port (default 9090)"}, "path": map[string]interface{}{"type": "string", "description": "Path (default /webhooks/lemonsqueezy)"}}}},
		{"name": "lemonsqueezy_webhook_stop", "description": "Stop the Lemon Squeezy webhook listener.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		{"name": "lemonsqueezy_setup", "description": "Get Next.js integration code for Lemon Squeezy (webhook handler + checkout).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	}
	tools = append(tools, workspaceTools...)
	tools = append(tools, getWorkspaceMCPTools()...)

	// Phone-first mini backend — desktop/agent/phone_backend.go
	tools = append(tools, phoneProjectMCPTools()...)

	// Native build & deploy (iosNative / androidNative / flutter) — native_build.go
	tools = append(tools, nativeBuildMCPTools()...)

	// Monorepo detection — desktop/agent/monorepo_detect.go
	tools = append(tools, monorepoMCPTools()...)

	// DNS provisioning + Let's Encrypt — desktop/agent/dns_mcp.go
	tools = append(tools, dnsMCPTools()...)

	return map[string]interface{}{
		"tools": tools,
	}
}
