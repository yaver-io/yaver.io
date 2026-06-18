# Development Studio Setup Guide

## Overview

The Development Studio enables you to manage and deploy your three main projects (Yaver, Talos, OCPP) to Hetzner from your mobile device, with support for multiple AI agents and rapid testing workflows.

## What's Completed ✅

1. **Hetzner Deployment Script** - Automated deployment to Hetzner servers
2. **Project Manager** - Multi-project orchestration system
3. **Mobile Development Studio** - React Native interface for mobile management
4. **HTTP API Integration** - RESTful endpoints for project management
5. **Agent Switching** - Support for OpenCode, Codex, and Claude Code

## What's Still Needed 🚧

1. **Hetzner Server Setup** - Initial server provisioning
2. **Testing & Validation** - End-to-end testing
3. **Error Handling** - Robust error management
4. **Documentation** - User guides and API docs

## Quick Start

### 1. Setup Hetzner Server

```bash
# SSH into your Hetzner server
ssh root@your-hetzner-server

# Install Yaver CLI
npm install -g yaver-cli

# Authenticate Yaver
yaver auth --headless

# Start Yaver agent
yaver serve
```

### 2. Deploy Projects from Mobile

1. Open Yaver mobile app
2. Navigate to **Dev Studio** tab
3. Select projects to deploy (Yaver, Talos, OCPP)
4. Choose workflow:
   - **Full Development Cycle** - Deploy + mobile testing + hot-reload
   - **Mobile Testing Only** - Setup mobile access
   - **Quick Deploy** - Deploy without testing
5. Select AI agent (OpenCode, Codex, Claude Code)
6. Tap **Execute Workflow**

### 3. Test Projects

Once deployed, you can:
- Access projects via mobile browser
- Test hot-reload changes
- Monitor deployment status
- Switch between AI agents
- View active tasks and logs

## Manual Deployment

If you prefer manual deployment:

```bash
# Deploy specific project
./scripts/hetzner-deploy.sh yaver development deploy

# Deploy all projects
./scripts/hetzner-deploy.sh yaver development deploy
./scripts/hetzner-deploy.sh talos development deploy
./scripts/hetzner-deploy.sh ocpp development deploy

# Enable hot-reload
./scripts/hetzner-deploy.sh yaver development hot-reload

# Setup mobile testing
./scripts/hetzner-deploy.sh all development mobile-test
```

## HTTP API Endpoints

### Project Management
- `GET /projects/list` - List all projects and their status
- `POST /projects/deploy` - Deploy a project to Hetzner
- `POST /projects/hot-reload` - Enable hot-reload for a project
- `POST /mobile-test/setup` - Setup mobile testing for projects

### Workflow & Agent Management
- `POST /agent/switch` - Switch active AI agent
- `POST /workflow/execute` - Execute a development workflow
- `GET /tasks/list` - List all active tasks

### Example API Usage

```bash
# Deploy Yaver to Hetzner
curl -X POST http://your-hetzner-server:18080/projects/deploy \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"project_name": "yaver", "environment": "development"}'

# Switch to Claude Code agent
curl -X POST http://your-hetzner-server:18080/agent/switch \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"agent_id": "claude"}'

# Execute full workflow
curl -X POST http://your-hetzner-server:18080/workflow/execute \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "projects": ["yaver", "talos", "ocpp"],
    "test_mode": "mobile",
    "agent_id": "opencode"
  }'
```

## Project Configuration

Projects are configured in `desktop/agent/project_manager.go`:

```go
projects := []struct {
    name string
    path string
    repo string
    port int
}{
    {"yaver", "yaver.io", "git@github.com:kivanccakmak/yaver.io.git", 18080},
    {"talos", "talos", "git@github.com:kivanccakmak/talos.git", 3000},
    {"ocpp", "ocpp", "git@github.com:kivanccakmak/ocpp.git", 8080},
}
```

## Troubleshooting

### Mobile App Can't Connect
1. Check Yaver agent is running on Hetzner: `yaver serve`
2. Verify network connectivity: `yaver ping your-hetzner-server`
3. Check relay password: `yaver relay list`

### Deployment Fails
1. Verify project paths are correct
2. Check git repository access
3. Ensure sufficient disk space on Hetzner
4. Review deployment logs: `yaver logs`

### Hot-Reload Not Working
1. Check project dependencies are installed
2. Verify port is not already in use
3. Check file watcher permissions
4. Review hot-reload logs in project directory

### Agent Switching Issues
1. Verify agent is installed: `yaver runner-auth status`
2. Check agent credentials are configured
3. Test agent manually: `yaver runner-auth test opencode`

## Next Steps

1. **Setup Hetzner Server** - Provision and configure your Hetzner instance
2. **Test Basic Deployment** - Deploy one project first
3. **Configure Mobile Access** - Setup mobile testing environment
4. **Test Workflows** - Try different development workflows
5. **Customize Configuration** - Adjust settings for your needs

## Architecture

```
Mobile App (Development Studio)
    ↓ HTTP API
Yaver Agent (Project Manager)
    ↓ SSH/Commands
Hetzner Server
    ├─ Yaver Project
    ├─ Talos Project
    └─ OCPP Project
```

## Support

For issues or questions:
1. Check logs: `yaver logs`
2. Review error messages in mobile app
3. Test API endpoints manually
4. Check Hetzner server status
5. Verify network connectivity

---

**Status**: Core implementation complete, testing and refinement needed
**Last Updated**: 2025-06-18
**Version**: 0.1.0-alpha