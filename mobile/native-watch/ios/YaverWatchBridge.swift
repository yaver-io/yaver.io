// YaverWatchBridge.swift — the PHONE-side WCSession bridge for the Apple
// Watch companion (docs/yaver-smartwatch-voice-terminal.md §3 mode A).
//
// Role: be the transport between the watchOS app (watch/) and the JS
// phone-side bridge (src/lib/watchEntry.ts → watchBridge.ts). It owns the
// WCSession on the phone and does exactly two things:
//
//   inbound  — on a message from the wrist, emit its JSON to JS via the
//              "yaverWatchMessage" DeviceEvent. The transport replyHandler is
//              answered immediately with a tiny transport ack so the watch
//              knows we received it; the SEMANTIC replies (ack/working/
//              summary/confirm-needed) flow back over transferUserInfo (queued
//              + delivered even if the watch app backgrounds — the async
//              completion wake the design requires).
//   outbound — `sendToWatch(_:)` (called by JS) ships a reply JSON to the
//              wrist via transferUserInfo.
//
// The WCSession message dictionary key is pinned to "yaverWatch" (must match
// watch/YaverWatch/PhoneSession.swift). All semantics live in JS; this file is
// deliberately dumb pipe + lifecycle. NOT built until plugins/withWatchBridge.js
// is registered (see that file's header) — same posture as the mesh tunnel.

import Foundation
import WatchConnectivity
import React

private let kMessageKey = "yaverWatch"

@objc(YaverWatchBridge)
final class YaverWatchBridge: RCTEventEmitter {

  private var hasListeners = false

  override init() {
    super.init()
    if WCSession.isSupported() {
      let s = WCSession.default
      s.delegate = self
      s.activate()
    }
  }

  // RN: emit on the JS thread; requireMainQueueSetup not needed (no UI).
  override static func requiresMainQueueSetup() -> Bool { false }

  override func supportedEvents() -> [String]! { ["yaverWatchMessage"] }
  override func startObserving() { hasListeners = true }
  override func stopObserving() { hasListeners = false }

  // Called by JS (watchEntry.ts sender) with a reply JSON string. We queue it
  // via transferUserInfo so it lands even when the watch app is backgrounded.
  @objc(sendToWatch:)
  func sendToWatch(_ json: String) {
    guard WCSession.isSupported() else { return }
    let s = WCSession.default
    guard s.activationState == .activated else { return }
    s.transferUserInfo([kMessageKey: json])
  }

  private func forwardInbound(_ json: String) {
    guard hasListeners else { return }
    sendEvent(withName: "yaverWatchMessage", body: json)
  }
}

extension YaverWatchBridge: WCSessionDelegate {
  // Live message (watch is foregrounded) — answer the transport handshake
  // immediately, then let JS push the real replies over transferUserInfo.
  func session(_ session: WCSession,
               didReceiveMessage message: [String: Any],
               replyHandler: @escaping ([String: Any]) -> Void) {
    if let json = message[kMessageKey] as? String {
      forwardInbound(json)
    }
    // Transport-level "received" — NOT a semantic reply. The watch listens on
    // the userInfo channel for ack/working/summary.
    replyHandler([kMessageKey: "{\"v\":1,\"kind\":\"working\",\"spoken\":\"…\"}"])
  }

  // Queued message (watch backgrounded at send time).
  func session(_ session: WCSession, didReceiveUserInfo userInfo: [String: Any]) {
    if let json = userInfo[kMessageKey] as? String {
      forwardInbound(json)
    }
  }

  func session(_ session: WCSession,
               activationDidCompleteWith activationState: WCSessionActivationState,
               error: Error?) {}
  func sessionDidBecomeInactive(_ session: WCSession) {}
  func sessionDidDeactivate(_ session: WCSession) {
    // Re-activate so a swapped watch keeps working.
    WCSession.default.activate()
  }
}
