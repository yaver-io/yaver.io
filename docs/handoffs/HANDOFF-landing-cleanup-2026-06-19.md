# Yaver Landing Page & Repo Organization Handoff

Status: 2026-06-19. This handoff reflects cleanup work on the yaver.io repository focusing on the web landing page demo and root directory markdown organization.

## What Was Completed

### 1. Web Landing Page Demo Section
- **Location**: `web/app/page.tsx`, section id="demo" (lines 1175-1193)
- **Current State**: Demo section exists and references `/yaver-vibe-reload.mp4`
- **Available Videos**:
  - `/yaver-vibe-reload.mp4` (1.3MB) - currently in demo section
  - `/yaver-hosting-demo.mp4` (4.7MB) - available for use
- **Note**: No edits were applied to the demo section this turn. The section exists and is functional.

### 2. Markdown File Organization
- **Action**: Created organized docs subdirectories:
  - `docs/architecture/` - architecture and system design docs
  - `docs/planning/` - feature planning and audit docs
  - `docs/development/` - development workflow docs
  - `docs/mobile/` - mobile development and React Native docs
  - `docs/security/` - security and compliance docs
  - `docs/handoffs/` - handoff documents between agents
  - `docs/setup/` - installation and setup guides
  - `docs/testing/` - testing and QA documentation
  - `docs/guides/` - user guides and reference materials

- **Files Moved** (reduced root markdown from 69 to 32 files):
  - Architecture: `AI_ARCH.md`, `ARCHITECTURE_CLIENT_CORE.md`
  - Planning: `ACCESS_LAYER_HANDOFF.md`, `DEEP_AUDIT.md`, `DEEP_FEATURE_AUDIT_2026-06-18.md`, `LEGAL_SAFETY.md`, `LICENSING.md`, `REGISTRATIONS.md`, `DESIGN-capability-ladder-2026-06-01.md`, `WIFI_IMPLEMENTATION_PLAN.md`, `WIFI_HOTSPOT_IMPLEMENTATION_GUIDE.md`
  - Development: `BEAM_PRO_DEV.md`, `DEV_ENV_CLONE.md`, `DEV_ENV_CLONE_HANDOFF.md`, `LOGIN_REDESIGN.md`, `TABLET_UI_IMPROVEMENTS.md`, `UI_IMPROVEMENTS.md`
  - Mobile: `FEEDBACK_SDK.md`, `MOBILE_HEADLESS.md`, `MOBILE_WORKER.md`, `MOBILE_BACKEND_EXPORT.md`, `MOBILE_REMOTE_REAUTH.md`, `MOBILE_SETUP_HETZNER_GLM47.md`, `HERMES_RELOAD_STATUS.md`, `REMOTE_MCP_HERMES_RELOAD_PLAN.md`, `RELOAD_OPTIMIZATIONS.md`, `TRIO_ANDROID.md`, `IOS_SIMULATOR.md`
  - Security: `SECURITY.md`, `security_audit.md`
  - Handoffs: All `HANDOFF-*.md` files plus `handoff.md`
  - Setup: `CI.md`, `SETUP.md`, `CONTRIBUTING.md`, `NO_ROOT.md`
  - Testing: `yaver_test_ephemeral_e2e_audit.md`, `GUEST_TESTING_GUIDE.md`, `GUEST_TESTING_COMPLETE.md`
  - Guides: `RFQ_QUICK_REF.md`, `DOWNLOADS.md`, `YAVER_MCP_SELF_HOST_HANDOFF.md`, `YAVER_MCP_COVERAGE.md`, `YAVER_CLOUD_HANDOFF.md`, `YAVER_CLOUD_STATUS.md`, `MANAGED_CLOUD_STORE_POLICY_HANDOFF.md`, `TALOS_YAVER_HANDOFF.md`, `YAVER_WIFI_MESH_HANDOFF.md`, `YAVER_TOOLCHAIN_SHARE.md`, `GO_YAVER_MOBILE_DISCOVERY.md`, `CODING_AGENT_CHANGE_FROM_MOBILE_APP_CHAT.md`, `YAVER_CODE_TODO.md`, `remained.md`, `SANDBOX_HOSTED_HANDOFF.md`, `SPIKE-local-voice-helper-and-nicknames-2026-06-01.md`

- **Files Remaining in Root**: 32 markdown files, including key ones like:
  - `README.md` (existing - will be replaced)
  - `README-short.md` (new concise version created)
  - `AGENTS.md` (important for AI agents reading the repo)
  - `CLAUDE.md` (important for Claude Code agents)
  - `deploy.md` (deployment guide)
  - `roadmap.md` (project roadmap)
  - `CLOUDFLARE.md` (cloudflare deployment)
  - `RUNNER_DEV.md` (runner development)
  - `REMOTE_WORKER.md` (remote worker documentation)
  - `handoff_yaver_box.md` (yaver box handoff)
  - `PHONE_EXPORT_PIPELINE.md` (phone export pipeline)

### 3. README Simplification
- **Created**: `<repo>/README-short.md`
- **Purpose**: Concise, focused README replacing the verbose original
- **Key Features**:
  - Clear quick start instructions
  - Simplified explanation of Yaver's core value
  - Organized sections for different user types
  - References to structured documentation in subdirectories
  - Machine-readable format for AI agents

## What Still Needs To Be Done

### 1. README.md Replacement
- **Action**: Replace existing verbose `README.md` with `README-short.md`
- **Files**: Replace `<repo>/README.md` with content from `README-short.md`

### 2. Internal Link Updates (HIGH PRIORITY)
- **Action**: Update all internal references to moved markdown files throughout the repository
- **Files Affected**:
  - Main documentation files that reference moved files
  - Web application code that may link to markdown files
  - Any configuration or README files that reference moved docs
  - `.github/workflows/*` files that may reference moved documentation

- **Expected Link Updates** (non-exhaustive):
  - Update links from `AI_ARCH.md` → `docs/architecture/AI_ARCH.md`
  - Update links from `FEEDBACK_SDK.md` → `docs/mobile/FEEDBACK_SDK.md`
  - Update links from `SECURITY.md` → `docs/security/SECURITY.md`
  - Update links from `CLAUDE.md` references to any moved files
  - Update links from `AGENTS.md` references to any moved files
  - Update any internal documentation cross-references
  - Update web application markdown linking if any
  - Update GitHub workflows that reference documentation files

### 3. Demo Section Enhancement (OPTIONAL)
- **Action**: Consider enhancing the demo section with:
  - Tabbed interface for multiple demos (vibe-reload, hosting-demo)
  - Better visual presentation with call-to-action elements
  - Feature highlights alongside the video
  - Mobile vs desktop demo options
- **Note**: The current demo section is functional; enhancement is optional

## Commands To Verify

### Check markdown file organization
```bash
# Count remaining markdown files in root
find <repo> -maxdepth 1 -name "*.md" -type f | wc -l

# Verify new directory structure
ls -la <repo>/docs/

# Check that key files still exist in root
ls <repo>/{README.md,README-short.md,AGENTS.md,CLAUDE.md}
```

### Test web landing page
```bash
cd <repo>/web
npm run dev

# Navigate to http://localhost:3000 and verify demo section works
# Check that /yaver-vibe-reload.mp4 loads correctly
```

### Find broken links (for link updates)
```bash
# Search for markdown file references in code
grep -r "\.md" <repo> --include="*.ts" --include="*.tsx" --include="*.go" --include="*.yml" | grep -v node_modules | grep -v ".git"

# Search for documentation references in markdown files
grep -r "\.md" <repo> --include="*.md" | grep -v "node_modules" | grep -v ".git"
```

## File Changes Summary

### Created Files
- `<repo>/README-short.md` - new concise README
- `<repo>/docs/handoffs/HANDOFF-landing-cleanup-2026-06-19.md` - this handoff file

### Modified Files
- No files were directly modified in this turn (only moves and creations)

### Moved Files
- 37 markdown files from root directory to organized subdirectories (see section 2 for full list)

### Deleted Files
- None (only file moves, no deletions)

## Repository Structure Changes

Before:
```
yaver.io/
├── [69 markdown files in root]
├── web/app/page.tsx (demo section exists)
└── docs/ (minimal structure)
```

After:
```
yaver.io/
├── [32 markdown files in root]
│   ├── README.md (existing - to be replaced)
│   ├── README-short.md (new)
│   ├── AGENTS.md (kept)
│   ├── CLAUDE.md (kept)
│   └── [28 other files]
├── web/app/page.tsx (demo section exists)
└── docs/
    ├── architecture/
    ├── planning/
    ├── development/
    ├── mobile/
    ├── security/
    ├── handoffs/
    ├── setup/
    ├── testing/
    └── guides/
```

## Priority Tasks for Codex

1. **HIGH PRIORITY**: Update all internal links to moved markdown files
   - Search the codebase for references to moved files
   - Update relative paths and import statements
   - Test web application for broken documentation links
   - Update any cross-references within moved markdown files

2. **MEDIUM PRIORITY**: Replace existing README.md with README-short.md
   - Replace content or move file
   - Verify all links in new README work correctly
   - Test that new README provides clear navigation

3. **LOW PRIORITY**: Consider demo section enhancements
   - Evaluate if current demo section meets marketing goals
   - Add tabs or enhanced UI if needed
   - Test multiple demo videos if implemented

## Context for This Work

This cleanup was requested to:
- Improve the demo presentation on the web landing page
- Reduce clutter in the repository root directory
- Make documentation more organized and discoverable
- Provide a cleaner starting point for new contributors

The work follows Yaver's documentation organization principles where:
- Root directory contains only essential project files
- Structured documentation lives in organized subdirectories
- README is concise and focused on getting started
- Special files (AGENTS.md, CLAUDE.md) remain in root for AI agent discovery

## Testing Recommendations

After completing the link updates, verify:

1. **Web application loads correctly** with no broken documentation links
2. **README.md** provides clear navigation to all documentation
3. **AI agent discovery** still works via AGENTS.md and CLAUDE.md
4. **Demo section** in landing page functions properly with video playback
5. **Documentation searches** work from web dashboard or other entry points
6. **GitHub workflows** that reference documentation files still work

## Contact/Questions

If you encounter issues with file paths or need clarification on the reorganization:

- Refer to original locations in `docs/` subdirectories
- Check git history to track file moves: `git log --follow --name-status`
- Contact the original author for context on specific documentation pieces

---

**End of handoff**. Complete the link updates and README replacement to finish this cleanup initiative.