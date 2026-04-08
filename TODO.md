# Yaver.io — TODO

## Phase 1: Foundation (DONE)
- [x] Project structure and documentation (CLAUDE.md, README.md, DOWNLOADS.md, SETUP.md)
- [x] Convex backend: schema (users, sessions, devices), auth.ts, devices.ts, http.ts
- [x] Web landing page: deployed to Cloudflare Workers (https://yaver.io)
- [x] Web: pricing page, downloads page, auth page, dashboard
- [x] Web: Talos-style dark GPT theme (black/gray/white)
- [x] Mobile app scaffold: Expo + expo-router, tabs (tasks/devices/settings), auth context
- [x] Desktop agent: Go + QUIC server, stream-json RPC with Claude CLI, config, auth
- [x] Desktop installer: Electron wizard (DMG/EXE/DEB), service install, OAuth flow
- [x] Linked Cloudflare Workers project (yaver-io)
- [x] Root .gitignore

## Phase 2: Auth Flow
- [x] Convex: Google OAuth action (http.ts /auth/google + /auth/google/callback)
- [x] Convex: Microsoft OAuth action (http.ts /auth/microsoft + /auth/microsoft/callback)
- [x] Convex: Session management (createSession, validateSession, deleteSession)
- [x] Convex: Device registration mutations (registerDevice, heartbeat, listMyDevices)
- [x] Web: OAuth buttons on auth page (Google + Microsoft)
- [x] Web: OAuth callback page (/auth/callback)
- [x] Mobile: OAuth deep link handling (yaver://oauth-callback)
- [x] Mobile: Login screen with Google + Microsoft buttons
- [ ] **YOU**: Link Convex project (`cd backend && npx convex dev`)
- [ ] **YOU**: Set Google OAuth credentials in Convex env
- [ ] **YOU**: Set Microsoft OAuth credentials in Convex env
- [ ] **YOU**: Set AUTH_REDIRECT_URL and MOBILE_DEEP_LINK in Convex env
- [ ] **YOU**: Set NEXT_PUBLIC_CONVEX_SITE_URL in Cloudflare Workers (wrangler secret)
- [ ] **YOU**: Test end-to-end OAuth flow

## Phase 3: P2P / QUIC Layer (CODE DONE)
- [x] Go agent: QUIC server with self-signed TLS (quic.go)
- [x] Go agent: Peer auth (verify token on QUIC connection)
- [x] Go agent: Task protocol over QUIC (create, stop, list, continue)
- [x] Go agent: Output streaming over QUIC streams
- [x] Mobile: QUIC client interface (src/lib/quic.ts) — HTTP fallback for now
- [x] Mobile: Peer discovery via Convex device registry (DeviceContext)
- [ ] Mobile: Native QUIC module (react-native-quic or bridge)
- [ ] NAT traversal / hole punching
- [ ] QUIC relay fallback for restrictive NATs

## Phase 4: Task Execution (CODE DONE)
- [x] Go agent: Claude CLI via stream-json RPC (no tmux dependency)
- [x] Go agent: Task lifecycle (queued → running → finished/failed/stopped)
- [x] Go agent: Output capture from Claude NDJSON events
- [x] Go agent: Session resumption (--resume flag for ContinueTask)
- [x] Mobile: Task creation UI with FAB
- [x] Mobile: Task list (running, queued, completed, failed)
- [x] Mobile: Task detail with output display
- [x] Mobile: Stop/continue task actions
- [ ] Mobile: Real-time output streaming over QUIC (currently placeholder)
- [ ] Go agent: Local task persistence (survive restarts)
- [ ] P2P session data sync between devices

## Phase 5: Desktop Installer (CODE DONE)
- [x] Electron installer UI (5-step wizard)
- [x] macOS/Windows/Linux build config (electron-builder)
- [x] Service installation (launchd/systemd/Windows service)
- [x] OAuth auth flow in installer
- [ ] **YOU**: Build and test (`cd desktop/installer && npm start`)
- [ ] **YOU**: Go agent compile (`cd desktop/agent && go mod tidy && go build`)
- [ ] Auto-update mechanism for agent binary

## Phase 6: Web (DEPLOYED)
- [x] Landing page with terminal mockup
- [x] Feature highlights (6 cards)
- [x] How it works (3 steps)
- [x] Downloads page with platform detection
- [x] Pricing page (Free / Pro $12 / Enterprise)
- [x] Auth page (Google + Microsoft OAuth)
- [x] Dashboard (device list)
- [x] Header + Footer components
- [x] Deployed to https://yaver.io (Cloudflare Workers)
- [x] DNS on Cloudflare, Workers routes configured

## Phase 7: App Store (LATER — needs name decision)
- [ ] Decide product name: yaver.io vs shellport.sh vs other
- [ ] Apple Developer account + App Store listing
- [ ] Google Play Console + Play Store listing
- [ ] Desktop code signing (macOS notarization, Windows Authenticode)

## Phase 8: Polish & Launch
- [ ] End-to-end encryption audit
- [ ] P2P session caching and sync
- [ ] Error handling and offline mode
- [ ] Analytics and crash reporting
- [ ] Documentation site
- [ ] Beta testing program
