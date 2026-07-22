# Yaver Todo — Swift / Tokamak (SwiftWasm)

The same todo UX as [`yaver-todo-rn`](https://github.com/yaver-io/yaver-todo-rn),
written in Swift against Tokamak. **The app is the control** — any difference you
observe against the RN version is the transport, not the app.

## Why this exists

It proves a **Swift-only app is developable from a phone against a Linux Cloud
Workspace, with no Mac anywhere**:

```
Swift source
  → SwiftWasm (carton dev)     ← Linux
  → WebAssembly + HTML
  → headless Chromium          ← Linux
  → WebRTC                     → your phone
```

`desktop/agent/swift_project_detect.go` detects this as `SwiftKindTokamak` and
routes it to `chrome-webrtc`.

## The demo

1. Provision a workspace on the `yaver-swiftwasm` image.
2. `carton dev` starts automatically (`SwiftWasmDevServer`).
3. The preview streams to your phone.
4. Ask the runner: **"change the background to blue"**.
5. It edits `backgroundColor` in `main.swift` — one obvious declaration, so
   there is no ambiguity about the edit site.
6. Rebuild → WASM → reload → next frame. Blue.

## What this does NOT prove

- **It is not iOS.** Tokamak is a SwiftUI-*compatible* API rendering to the DOM.
  It will not match UIKit pixel-for-pixel and must never be described as an iOS
  preview. UIKit and Apple's SwiftUI need a Mac host.
- **There is no HMR.** A Swift edit is a full WASM recompile plus a page reload,
  so the list resets and the loop is seconds rather than milliseconds. Fine for a
  todo fixture; measure before generalising to an app with deep navigation.

## Note on location

The other fixtures live in their own repos, deliberately — a fixture inside the
monorepo inherits its tooling and stops being an honest test of what a user's
project hits. This one sits in `demo/` while the toolchain is being proven and
**should move to `yaver-io/yaver-todo-swift-wasm`** once it does.
