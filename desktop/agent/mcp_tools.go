package main

// getMCPToolsList returns the full MCP tools list for tools/list responses.
func (s *HTTPServer) getMCPToolsList() interface{} {
	tools := []map[string]interface{}{
		// --- Task Management ---
		{
			"name":        "create_task",
			"description": "Create a new coding task. The AI runner will execute this task on the connected development machine.",
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
				},
			},
		},
		{
			"name":        "list_tasks",
			"description": "List all tasks and their current status (queued, running, completed, failed, stopped).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "get_task",
			"description": "Get detailed information about a specific task, including its full output.",
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
			"name":        "get_info",
			"description": "Get information about the connected development machine (hostname, working directory, version).",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
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
						"description": "Runner ID (claude, codex, aider)",
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
			"description": "Get help about Yaver features and capabilities. Use this when a user asks what Yaver can do, how to set up, or how features work (tmux adoption, relay servers, tunnels, MCP tools, mobile app, etc.).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic": map[string]interface{}{
						"type":        "string",
						"description": "Optional topic: overview, tmux, relay, tunnel, mobile, mcp, runners, tasks, auth",
					},
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
	}
	tools = append(tools, diagnosticTools...)

	// --- Config Management ---
	configTools := []map[string]interface{}{
		{
			"name":        "config_set",
			"description": "Set a Yaver configuration value. Keys: auto-start (true/false), auto-update (true/false).",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"key", "value"},
				"properties": map[string]interface{}{
					"key":   map[string]interface{}{"type": "string", "description": "Config key (auto-start, auto-update)"},
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
				"type": "object",
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
		// GitHub
		{"name": "github_prs", "description": "List pull requests from the current repo (requires gh CLI).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Repo directory"}, "state": map[string]interface{}{"type": "string", "description": "Filter: open, closed, merged, all (default: open)"}}}},
		{"name": "github_issues", "description": "List issues from the current repo (requires gh CLI).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Repo directory"}, "state": map[string]interface{}{"type": "string", "description": "Filter: open, closed, all (default: open)"}}}},
		{"name": "github_ci_status", "description": "Show recent GitHub Actions workflow runs and their status (requires gh CLI).", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"directory": map[string]interface{}{"type": "string", "description": "Repo directory"}}}},
	}
	tools = append(tools, devTools...)

	// --- Developer Tools 2 ---
	devTools2 := []map[string]interface{}{
		// Database
		{"name": "db_query", "description": "Execute a database query (SQLite, PostgreSQL, MySQL, Redis).", "inputSchema": map[string]interface{}{"type": "object", "required": []string{"driver", "query"}, "properties": map[string]interface{}{"driver": map[string]interface{}{"type": "string", "description": "Database: sqlite, postgres, mysql, redis"}, "dsn": map[string]interface{}{"type": "string", "description": "Connection string (or path for SQLite). Uses DATABASE_URL env if empty for postgres."}, "query": map[string]interface{}{"type": "string", "description": "SQL query or Redis command"}}}},
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

	// --- Guest Access ---
	guestTools := []map[string]interface{}{
		{
			"name":        "guest_invite",
			"description": "Invite a guest by email to use your machine. They can connect from their Yaver mobile app. Max 5 guests, invitation expires in 2 days.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"email"},
				"properties": map[string]interface{}{
					"email": map[string]interface{}{
						"type":        "string",
						"description": "Email address of the person to invite",
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
			"description": "View or update guest config (daily limit, allowed runners, usage mode). Without email: list all. With email: show/update config.",
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
				"type":       "object",
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
					"key":             map[string]interface{}{"type": "string"},
					"type":            map[string]interface{}{"type": "string", "enum": []string{"bool", "string"}},
					"defaultBool":     map[string]interface{}{"type": "boolean"},
					"defaultString":   map[string]interface{}{"type": "string"},
					"rolloutPercent":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 100},
					"description":     map[string]interface{}{"type": "string"},
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
				"type":       "object",
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
				"type":       "object",
				"properties": map[string]interface{}{
					"since": map[string]interface{}{"type": "integer", "description": "Unix ms filter — only return events newer than this"},
					"limit": map[string]interface{}{"type": "integer", "description": "Max events to return, default 100"},
				},
			},
		},
	}
	tools = append(tools, monitorTools...)

	return map[string]interface{}{
		"tools": tools,
	}
}
