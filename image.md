# Yaver Pi Image Plan

## Goal

Build a flashable Yaver OS image for a **Raspberry Pi 5 with 16 GB RAM and 256 GB storage** that turns the device into a **headless remote developer machine** controlled from Yaver mobile, web, CLI, and MCP.

The image should reduce solo-developer AI spend by making Yaver the orchestration layer:

- `Claude Code` / `Codex` / `OpenCode` do high-leverage work:
  - planning
  - architecture
  - review
  - failure analysis
  - escalation
- `Ollama` + `Qwen` do bounded cheap work:
  - narrow code edits
  - unit test generation
  - low-risk retries
  - repetitive refactors
  - local patch/test loops

Yaver becomes the dispatcher, verifier, and budget controller between the two.

## Product Framing

This is **not firmware**.

This is:

- a prebuilt Raspberry Pi OS image
- a first-boot provisioning flow
- a module installer
- a Yaver-managed headless dev node
- a mobile-first control plane

Headline:

`flash image -> pair in Yaver -> add keys -> choose modules -> run remote AI coding economically`

## Core User Story

Solo developer has:

- a Pi 5 running at home
- a phone with Yaver
- existing Claude / Codex access
- optional Ollama local models

They want to:

- run coding tasks remotely
- avoid burning premium tokens on repetitive low-value work
- keep a personal dev box always reachable
- trigger and monitor work from mobile
- use MCP surfaces naturally

## Economic Thesis

The product is valuable if it can compress premium-model usage.

Bad path:

- use Claude Code or Codex for every edit, every retry, every test generation, every failing loop

Good path:

1. premium model receives the real request
2. premium model creates a structured plan
3. Yaver slices the work into bounded subtasks
4. local Ollama/Qwen executes cheap subtasks
5. Yaver runs tests/lints/builds
6. Yaver retries locally when failure is narrow
7. Yaver escalates back to premium model only when needed

The moat is **economic orchestration**, not just “we installed many tools on a Pi”.

## Hardware Assumption

Primary target:

- Raspberry Pi 5
- 16 GB RAM
- 256 GB microSD minimum

Recommended accessories:

- official 27W PSU
- active cooling
- Ethernet preferred
- external SSD recommended for serious Ollama usage

Why this target:

- enough RAM for Yaver + coding CLIs + local model experimentation
- enough storage for OS, repos, Docker, test artifacts, and a few models
- still cheap and personal enough to fit the “my own cloud” positioning

## Storage Budget

For the Pi 5 image, assume rough usage:

- base OS + updates: `8-12 GB`
- Yaver agent + runtimes + logs: `2-5 GB`
- Node/Python/dev CLIs: `3-6 GB`
- repos + caches + virtualenvs + node_modules: `20-60 GB`
- Docker images/volumes: `10-50 GB`
- Playwright/test artifacts: `2-8 GB`
- Ollama small/medium models: `10-60 GB`

Conclusion:

- `256 GB` is viable
- `128 GB` is workable but tighter
- `256 GB + SSD for models` is better for power users

## Non-Goals For V1

Do not try to ship all of this initially:

- perfect local-model-first coding
- full Pi-specific hardware emulation
- generic homelab control panel
- multi-tenant enterprise appliance
- redistributing every proprietary CLI inside the image

V1 should optimize for a **single owner-operated dev node**.

## V1 Product Definition

Yaver Pi Image V1 should provide:

- headless pairing into Yaver
- persistent Yaver agent on boot
- mobile/web/MCP visibility
- installable AI coding modules
- API key and auth setup flow
- logs / health / storage visibility
- cost-aware hybrid routing
- safe local-model usage for bounded tasks

## User-Facing Modes

Expose simple modes in UI instead of raw runner complexity.

Suggested modes:

- `Low Cost`
  - premium model for planning/review only
  - Ollama/Qwen for bounded implementation and tests
- `Balanced`
  - premium model for planning and some implementation
  - local model for retries / low-risk work
- `Max Quality`
  - premium model end-to-end
  - local model optional only for support tasks

Advanced settings can still expose:

- planner
- implementer
- model
- retry cap
- test policy
- escalation threshold

## System Architecture

### 1. Base OS Layer

Start from:

- Raspberry Pi OS Lite 64-bit

Base system includes:

- SSH
- systemd
- unattended upgrades optional
- Yaver agent
- first-boot setup service

### 2. Yaver Core Layer

Core services:

- `yaver-agent`
- `yaver-bootstrap`
- `yaver-updater`
- `yaver-health`
- `yaver-storage-monitor`
- `yaver-secrets`

Core responsibilities:

- auth/pairing
- remote control plane
- module installation
- runner detection
- health/metrics
- state persistence

### 3. Runner Layer

Primary premium runners:

- Claude Code
- Codex
- OpenCode

Local runners:

- Ollama
- Qwen variants
- aider + Ollama bridge

### 4. Orchestration Layer

Yaver owns:

- task decomposition
- task contracts
- prompt shaping
- test execution
- retry loops
- escalation logic
- cost heuristics

### 5. Mobile / MCP Layer

The same node should be operable through:

- Yaver mobile app
- web dashboard
- CLI
- MCP tools

The Pi is a node in the existing Yaver network, not a one-off side project.

## Task Contract Design

This is the most important technical layer.

Each cheap local subtask should carry:

- `goal`
- `allowed_files`
- `max_files`
- `acceptance_checks`
- `allowed_commands`
- `timeout`
- `max_retries`
- `escalate_on`

Example escalation conditions:

- touches more than N files
- test failures remain after 2 local retries
- diff exceeds threshold
- parser/type/build errors spread outside allowed files
- local model asks for repo-wide reasoning

## Routing Policy

### Premium runners should handle

- vague or ambiguous requests
- architecture decisions
- multi-file reasoning
- review of local changes
- root-cause analysis
- final escalation after cheap failures

### Local runners should handle

- small file-local edits
- unit tests from explicit contract
- lint fixes
- narrow refactors
- repetitive CRUD/test boilerplate
- bounded retries

### Yaver should decide

- whether a task is safe for local execution
- whether to retry locally
- when to escalate
- whether final review is needed

## Ollama Strategy On Pi 5

Supported but not primary.

Recommended defaults:

- `qwen2.5-coder:7b`
- `qwen2.5-coder:14b` as opt-in
- small fallback models for tiny tasks

Not recommended as default:

- 32B-class models
- multi-session local heavy coding
- long repo-wide reasoning passes

Ollama positioning:

- cheap worker
- private fallback
- offline helper
- bounded implementation engine

## TDD / Quality Strategy

TDD should be first-class in the image.

Base quality modules:

- `pytest`
- `ruff`
- `vitest`
- `eslint`
- `prettier`
- `playwright`
- `pre-commit`

Yaver actions:

- generate failing test first
- run unit tests
- run changed tests
- run lint/format
- fix until tests pass
- escalate failing loops to premium runner

## Networking Strategy

Default posture:

- no public SSH exposure
- private-first remote access

Modules:

- Tailscale
- Cloudflare Tunnel
- local-only mode

Recommended default:

- Tailscale for admin
- Cloudflare Tunnel optional for dashboards/webhooks

## Security / Secrets

Secrets must not be baked into the image.

Provision on first boot:

- OpenAI key
- Anthropic key
- OpenRouter key
- GitHub token if needed
- tunnel tokens

Storage:

- encrypted local secret store where possible
- strict file permissions
- never expose full secret values in mobile/web logs

## Module Matrix

### Core

- `yaver`
- `mobile`
- `node`
- `tmux`
- `ffmpeg`

### AI Coding

- `codex`
- `claude`
- `opencode`
- `aider`
- `ollama`
- `hybrid`

### Dev Tooling

- `git`
- `gh`
- `uv`
- `docker`
- `docker-compose`

### Testing

- `pytest`
- `vitest`
- `playwright`
- `pre-commit`

### Networking

- `tailscale`
- `cloudflared`

## First-Boot Flow

1. boot image
2. device advertises local pairing endpoint
3. user pairs from Yaver mobile app
4. user enters provider keys and repo/network settings
5. user chooses install profile
6. Yaver installs selected modules
7. node appears as healthy in mobile/web/MCP

Suggested profiles:

- `Minimal`
- `Standard Dev Node`
- `Economic Hybrid Node`
- `Power Node`

## Standard Dev Node Profile

Should include:

- Yaver core
- mobile runtime support
- tmux
- Codex
- OpenCode
- Ollama
- aider
- hybrid support

Optional at setup:

- Claude Code
- Playwright
- Tailscale
- Cloudflare Tunnel
- Docker

## Economic Hybrid Node Profile

The most important profile for this project.

Install:

- Yaver core
- mobile stack
- Codex
- OpenCode
- Ollama
- aider
- Qwen model preset
- TDD tooling

Defaults:

- premium planner
- local implementer
- retry locally first
- escalate only on bounded failure policy

## Execution Plan

### Phase 1: Planning and Installer Groundwork

- write image plan
- define install profiles
- extend installer catalogue for Pi-oriented dev-node setup
- make mobile/web/docs reflect the profile concept

### Phase 2: Node Provisioning

- add first-boot bootstrap config
- add module state persistence
- add storage/health reporting for the node
- add pairing-friendly provisioning status

### Phase 3: Cost-Aware Orchestration

- formalize task contract shape
- add local retry policy
- add escalation policy
- add “Low Cost / Balanced / Max Quality” UX
- add usage/cost reporting

### Phase 4: Image Build

- image build script
- first-boot service
- module installer hooks
- docs for flashing/pairing

### Phase 5: Hardening

- cold boot tests
- power loss recovery
- storage cleanup policies
- model cleanup and warnings
- low-disk handling

## Immediate Implementation Backlog

1. Add Pi-focused install profile(s) to `yaver install`
2. Add missing core dev-node integrations useful for Pi provisioning
3. Add image/profile docs entry points
4. Add cost-aware profile defaults in UI or API surfaces
5. Add explicit hybrid-local-worker policy knobs

## Success Criteria

The project is successful when a solo developer can:

- flash the image
- pair from phone
- install the economic hybrid stack
- run Codex/Claude planning from mobile
- have Yaver delegate bounded edits/tests to Ollama/Qwen
- see retries and escalations automatically
- spend noticeably fewer premium tokens than end-to-end premium execution

## Current Repo Alignment

Already present in repo:

- Linux arm64 install path
- Raspberry Pi manual
- hybrid planner/implementer mode
- mobile/web client support for hybrid
- autodev support for `claude`, `codex`, `hybrid`
- QEMU exploration for isolated continuation/testing

Missing for the image product:

- dedicated install profile
- first-boot appliance flow
- image build path
- stronger cost-routing product layer

## First Execution Slice

Start with the smallest useful vertical:

- add a **Pi dev-node install profile** in `yaver install`
- make it install the core tools this image needs
- use that as the base for later image/bootstrap work

