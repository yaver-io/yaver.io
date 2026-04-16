package main

import (
	"fmt"
	"os"
)

func runCompletion(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver completion <bash|zsh|fish>")
		os.Exit(1)
	}

	switch args[0] {
	case "bash":
		fmt.Print(bashCompletion)
	case "zsh":
		fmt.Print(zshCompletion)
	case "fish":
		fmt.Print(fishCompletion)
	default:
		fmt.Fprintf(os.Stderr, "Unsupported shell: %s (use bash, zsh, or fish)\n", args[0])
		os.Exit(1)
	}
}

const bashCompletion = `# yaver bash completion
# Add to ~/.bashrc:  eval "$(yaver completion bash)"
_yaver_completions() {
    local cur prev commands
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    commands="auth signout connect serve logs stop clear-logs restart shutdown ping attach code status devices config relay tunnel set-runner mcp email acl tmux exec session vault build expo debug deploy test repo pipeline feedback voice clean cloud discover purge uninstall doctor completion help version"

    case "$prev" in
        yaver)
            COMPREPLY=($(compgen -W "$commands" -- "$cur"))
            return 0
            ;;
        relay)
            COMPREPLY=($(compgen -W "add list remove test set-password clear-password" -- "$cur"))
            return 0
            ;;
        tunnel)
            COMPREPLY=($(compgen -W "add list remove test setup" -- "$cur"))
            return 0
            ;;
        config)
            COMPREPLY=($(compgen -W "set" -- "$cur"))
            return 0
            ;;
        session)
            COMPREPLY=($(compgen -W "list transfer export import" -- "$cur"))
            return 0
            ;;
        tmux)
            COMPREPLY=($(compgen -W "list adopt detach" -- "$cur"))
            return 0
            ;;
        email)
            COMPREPLY=($(compgen -W "setup test sync status" -- "$cur"))
            return 0
            ;;
        acl)
            COMPREPLY=($(compgen -W "add list remove tools health" -- "$cur"))
            return 0
            ;;
        mcp)
            COMPREPLY=($(compgen -W "deploy list remove status setup" -- "$cur"))
            return 0
            ;;
        expo)
            COMPREPLY=($(compgen -W "setup start build status" -- "$cur"))
            return 0
            ;;
        build)
            COMPREPLY=($(compgen -W "flutter gradle xcode rn custom list status register push" -- "$cur"))
            return 0
            ;;
        debug)
            COMPREPLY=($(compgen -W "flutter rn" -- "$cur"))
            return 0
            ;;
        feedback)
            COMPREPLY=($(compgen -W "list show fix delete" -- "$cur"))
            return 0
            ;;
        voice)
            COMPREPLY=($(compgen -W "setup serve status test providers" -- "$cur"))
            return 0
            ;;
        vault)
            COMPREPLY=($(compgen -W "add list get delete export import" -- "$cur"))
            return 0
            ;;
        test)
            COMPREPLY=($(compgen -W "unit flutter android ios e2e" -- "$cur"))
            return 0
            ;;
        cloud)
            COMPREPLY=($(compgen -W "create status ssh destroy" -- "$cur"))
            return 0
            ;;
        completion)
            COMPREPLY=($(compgen -W "bash zsh fish" -- "$cur"))
            return 0
            ;;
    esac
}
complete -F _yaver_completions yaver
`

const zshCompletion = `#compdef yaver
# yaver zsh completion
# Add to ~/.zshrc:  eval "$(yaver completion zsh)"

_yaver() {
    local -a commands subcommands

    commands=(
        'auth:Sign in via browser (Apple/Google/Microsoft)'
        'signout:Sign out and clear credentials'
        'connect:Connect to a remote device'
        'serve:Start the agent server'
        'logs:Show agent logs'
        'stop:Stop running agent'
        'clear-logs:Clear log files'
        'restart:Restart the agent'
        'shutdown:Graceful shutdown'
        'ping:Ping a device'
        'attach:Interactive terminal'
        'code:Terminal-first coding mode'
        'status:Show connection status'
        'devices:List registered devices'
        'config:Get/set configuration'
        'relay:Manage relay server config'
        'tunnel:Cloudflare tunnel management'
        'set-runner:Configure AI agent'
        'mcp:MCP server (stdio or HTTP)'
        'email:Email connector'
        'acl:Agent Communication Layer'
        'tmux:Tmux session management'
        'exec:Execute command on remote device'
        'session:Transfer agent sessions'
        'vault:Encrypted key vault'
        'build:Build mobile/desktop apps'
        'expo:Expo integration (setup, start, build)'
        'debug:Hot reload debug sessions'
        'deploy:Deploy artifacts and CI'
        'test:Run tests'
        'repo:Project discovery'
        'pipeline:Build-test-deploy pipeline'
        'feedback:Visual bug reports from device'
        'voice:Voice AI providers (speech-to-speech)'
        'cloud:Cloud dev machine'
        'clean:Remove old tasks/logs'
        'discover:Discover projects'
        'purge:Complete wipe'
        'uninstall:Remove config and stop agent'
        'doctor:Diagnose issues'
        'completion:Generate shell completions'
        'help:Show help'
        'version:Print version'
    )

    if (( CURRENT == 2 )); then
        _describe 'command' commands
        return
    fi

    case "${words[2]}" in
        relay)
            subcommands=('add:Add relay server' 'list:List relay servers' 'remove:Remove relay' 'test:Test relay connection' 'set-password:Set relay password' 'clear-password:Clear relay password')
            _describe 'subcommand' subcommands
            ;;
        tunnel)
            subcommands=('add:Add tunnel' 'list:List tunnels' 'remove:Remove tunnel' 'test:Test tunnel' 'setup:Setup Cloudflare tunnel')
            _describe 'subcommand' subcommands
            ;;
        config)
            subcommands=('set:Set config value')
            _describe 'subcommand' subcommands
            ;;
        session)
            subcommands=('list:List transferable sessions' 'transfer:Transfer to another device' 'export:Export to file' 'import:Import from file')
            _describe 'subcommand' subcommands
            ;;
        tmux)
            subcommands=('list:List tmux sessions' 'adopt:Adopt a tmux session' 'detach:Stop monitoring session')
            _describe 'subcommand' subcommands
            ;;
        email)
            subcommands=('setup:Interactive email setup' 'test:Send test email' 'sync:Sync emails' 'status:Show email config')
            _describe 'subcommand' subcommands
            ;;
        acl)
            subcommands=('add:Add MCP peer' 'list:List peers' 'remove:Remove peer' 'tools:List peer tools' 'health:Health check')
            _describe 'subcommand' subcommands
            ;;
        mcp)
            subcommands=('deploy:Deploy MCP server' 'list:List deployments' 'remove:Remove deployment' 'status:Check status' 'setup:Configure MCP for editors')
            _describe 'subcommand' subcommands
            ;;
        expo)
            subcommands=('setup:Inject Feedback SDK into Expo project' 'start:Start Metro + P2P tunnel' 'build:Build via Expo (local or EAS)' 'status:Show Expo session status')
            _describe 'subcommand' subcommands
            ;;
        build)
            subcommands=('flutter:Flutter build' 'gradle:Gradle build' 'xcode:Xcode build' 'rn:React Native build' 'custom:Custom command' 'list:List builds' 'status:Build details' 'register:Register artifact' 'push:Push to store')
            _describe 'subcommand' subcommands
            ;;
        debug)
            subcommands=('flutter:Flutter debug session' 'rn:React Native/Metro debug')
            _describe 'subcommand' subcommands
            ;;
        feedback)
            subcommands=('list:List feedback reports' 'show:Show report details' 'fix:Create task from feedback' 'delete:Delete report')
            _describe 'subcommand' subcommands
            ;;
        voice)
            subcommands=('setup:Set up voice provider' 'serve:Start inference server' 'status:Show provider status' 'test:Record and transcribe test clip' 'providers:List available providers')
            _describe 'subcommand' subcommands
            ;;
        vault)
            subcommands=('add:Add secret' 'list:List entries' 'get:Get value' 'delete:Delete entry' 'export:Export vault' 'import:Import vault')
            _describe 'subcommand' subcommands
            ;;
        test)
            subcommands=('unit:Unit tests' 'flutter:Flutter tests' 'android:Android tests' 'ios:iOS tests' 'e2e:E2E tests')
            _describe 'subcommand' subcommands
            ;;
        cloud)
            subcommands=('create:Create cloud machine' 'status:Show status' 'ssh:SSH into machine' 'destroy:Tear down machine')
            _describe 'subcommand' subcommands
            ;;
        completion)
            subcommands=('bash:Bash completions' 'zsh:Zsh completions' 'fish:Fish completions')
            _describe 'subcommand' subcommands
            ;;
    esac
}

_yaver "$@"
`

const fishCompletion = `# yaver fish completion
# Add to fish config:  yaver completion fish | source

# Disable file completions by default
complete -c yaver -f

# Top-level commands
complete -c yaver -n '__fish_use_subcommand' -a 'auth' -d 'Sign in via browser'
complete -c yaver -n '__fish_use_subcommand' -a 'signout' -d 'Sign out'
complete -c yaver -n '__fish_use_subcommand' -a 'connect' -d 'Connect to remote device'
complete -c yaver -n '__fish_use_subcommand' -a 'serve' -d 'Start agent server'
complete -c yaver -n '__fish_use_subcommand' -a 'logs' -d 'Show agent logs'
complete -c yaver -n '__fish_use_subcommand' -a 'stop' -d 'Stop running agent'
complete -c yaver -n '__fish_use_subcommand' -a 'clear-logs' -d 'Clear log files'
complete -c yaver -n '__fish_use_subcommand' -a 'restart' -d 'Restart agent'
complete -c yaver -n '__fish_use_subcommand' -a 'shutdown' -d 'Graceful shutdown'
complete -c yaver -n '__fish_use_subcommand' -a 'ping' -d 'Ping a device'
complete -c yaver -n '__fish_use_subcommand' -a 'attach' -d 'Interactive terminal'
complete -c yaver -n '__fish_use_subcommand' -a 'code' -d 'Terminal-first coding mode'
complete -c yaver -n '__fish_use_subcommand' -a 'status' -d 'Show connection status'
complete -c yaver -n '__fish_use_subcommand' -a 'devices' -d 'List registered devices'
complete -c yaver -n '__fish_use_subcommand' -a 'config' -d 'Get/set configuration'
complete -c yaver -n '__fish_use_subcommand' -a 'relay' -d 'Manage relay servers'
complete -c yaver -n '__fish_use_subcommand' -a 'tunnel' -d 'Cloudflare tunnel management'
complete -c yaver -n '__fish_use_subcommand' -a 'set-runner' -d 'Configure AI agent'
complete -c yaver -n '__fish_use_subcommand' -a 'mcp' -d 'MCP server'
complete -c yaver -n '__fish_use_subcommand' -a 'email' -d 'Email connector'
complete -c yaver -n '__fish_use_subcommand' -a 'acl' -d 'Agent Communication Layer'
complete -c yaver -n '__fish_use_subcommand' -a 'tmux' -d 'Tmux session management'
complete -c yaver -n '__fish_use_subcommand' -a 'exec' -d 'Execute remote command'
complete -c yaver -n '__fish_use_subcommand' -a 'session' -d 'Transfer agent sessions'
complete -c yaver -n '__fish_use_subcommand' -a 'voice' -d 'Voice AI providers'
complete -c yaver -n '__fish_use_subcommand' -a 'clean' -d 'Remove old tasks/logs'
complete -c yaver -n '__fish_use_subcommand' -a 'discover' -d 'Discover projects'
complete -c yaver -n '__fish_use_subcommand' -a 'purge' -d 'Complete wipe'
complete -c yaver -n '__fish_use_subcommand' -a 'uninstall' -d 'Remove config and stop'
complete -c yaver -n '__fish_use_subcommand' -a 'doctor' -d 'Diagnose issues'
complete -c yaver -n '__fish_use_subcommand' -a 'completion' -d 'Generate shell completions'
complete -c yaver -n '__fish_use_subcommand' -a 'help' -d 'Show help'
complete -c yaver -n '__fish_use_subcommand' -a 'version' -d 'Print version'

# relay subcommands
complete -c yaver -n '__fish_seen_subcommand_from relay' -a 'add' -d 'Add relay server'
complete -c yaver -n '__fish_seen_subcommand_from relay' -a 'list' -d 'List relays'
complete -c yaver -n '__fish_seen_subcommand_from relay' -a 'remove' -d 'Remove relay'
complete -c yaver -n '__fish_seen_subcommand_from relay' -a 'test' -d 'Test relay'
complete -c yaver -n '__fish_seen_subcommand_from relay' -a 'set-password' -d 'Set password'
complete -c yaver -n '__fish_seen_subcommand_from relay' -a 'clear-password' -d 'Clear password'

# tunnel subcommands
complete -c yaver -n '__fish_seen_subcommand_from tunnel' -a 'add' -d 'Add tunnel'
complete -c yaver -n '__fish_seen_subcommand_from tunnel' -a 'list' -d 'List tunnels'
complete -c yaver -n '__fish_seen_subcommand_from tunnel' -a 'remove' -d 'Remove tunnel'
complete -c yaver -n '__fish_seen_subcommand_from tunnel' -a 'test' -d 'Test tunnel'
complete -c yaver -n '__fish_seen_subcommand_from tunnel' -a 'setup' -d 'Setup Cloudflare tunnel'

# config subcommands
complete -c yaver -n '__fish_seen_subcommand_from config' -a 'set' -d 'Set config value'

# session subcommands
complete -c yaver -n '__fish_seen_subcommand_from session' -a 'list' -d 'List sessions'
complete -c yaver -n '__fish_seen_subcommand_from session' -a 'transfer' -d 'Transfer session'
complete -c yaver -n '__fish_seen_subcommand_from session' -a 'export' -d 'Export session'
complete -c yaver -n '__fish_seen_subcommand_from session' -a 'import' -d 'Import session'

# tmux subcommands
complete -c yaver -n '__fish_seen_subcommand_from tmux' -a 'list' -d 'List sessions'
complete -c yaver -n '__fish_seen_subcommand_from tmux' -a 'adopt' -d 'Adopt session'
complete -c yaver -n '__fish_seen_subcommand_from tmux' -a 'detach' -d 'Detach session'

# email subcommands
complete -c yaver -n '__fish_seen_subcommand_from email' -a 'setup' -d 'Setup email'
complete -c yaver -n '__fish_seen_subcommand_from email' -a 'test' -d 'Send test'
complete -c yaver -n '__fish_seen_subcommand_from email' -a 'sync' -d 'Sync emails'
complete -c yaver -n '__fish_seen_subcommand_from email' -a 'status' -d 'Show status'

# acl subcommands
complete -c yaver -n '__fish_seen_subcommand_from acl' -a 'add' -d 'Add MCP peer'
complete -c yaver -n '__fish_seen_subcommand_from acl' -a 'list' -d 'List peers'
complete -c yaver -n '__fish_seen_subcommand_from acl' -a 'remove' -d 'Remove peer'
complete -c yaver -n '__fish_seen_subcommand_from acl' -a 'tools' -d 'List peer tools'
complete -c yaver -n '__fish_seen_subcommand_from acl' -a 'health' -d 'Health check'

# mcp subcommands
complete -c yaver -n '__fish_seen_subcommand_from mcp' -a 'deploy' -d 'Deploy MCP server'
complete -c yaver -n '__fish_seen_subcommand_from mcp' -a 'list' -d 'List deployments'
complete -c yaver -n '__fish_seen_subcommand_from mcp' -a 'remove' -d 'Remove deployment'
complete -c yaver -n '__fish_seen_subcommand_from mcp' -a 'status' -d 'Check status'
complete -c yaver -n '__fish_seen_subcommand_from mcp' -a 'setup' -d 'Configure MCP for editors'

# voice subcommands
complete -c yaver -n '__fish_seen_subcommand_from voice' -a 'setup' -d 'Set up voice provider'
complete -c yaver -n '__fish_seen_subcommand_from voice' -a 'serve' -d 'Start inference server'
complete -c yaver -n '__fish_seen_subcommand_from voice' -a 'status' -d 'Show provider status'
complete -c yaver -n '__fish_seen_subcommand_from voice' -a 'test' -d 'Record and transcribe test clip'
complete -c yaver -n '__fish_seen_subcommand_from voice' -a 'providers' -d 'List available providers'

# completion subcommands
complete -c yaver -n '__fish_seen_subcommand_from completion' -a 'bash' -d 'Bash completions'
complete -c yaver -n '__fish_seen_subcommand_from completion' -a 'zsh' -d 'Zsh completions'
complete -c yaver -n '__fish_seen_subcommand_from completion' -a 'fish' -d 'Fish completions'
`
