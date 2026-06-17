import Foundation
import React
import WatchConnectivity

private let yaverWatchMessageKey = "yaverWatch"

@objc(YaverWatchBridge)
final class YaverWatchBridge: RCTEventEmitter {
  private var hasListeners = false

  override init() {
    super.init()
    if WCSession.isSupported() {
      let session = WCSession.default
      session.delegate = self
      session.activate()
    }
  }

  override static func requiresMainQueueSetup() -> Bool { false }

  override func supportedEvents() -> [String]! {
    ["yaverWatchMessage"]
  }

  override func startObserving() {
    hasListeners = true
  }

  override func stopObserving() {
    hasListeners = false
  }

  @objc(sendToWatch:)
  func sendToWatch(_ json: String) {
    guard WCSession.isSupported() else { return }
    let session = WCSession.default
    guard session.activationState == .activated else { return }
    session.transferUserInfo([yaverWatchMessageKey: json])
  }

  private func forwardInbound(_ json: String) {
    guard hasListeners else { return }
    sendEvent(withName: "yaverWatchMessage", body: json)
  }
}

extension YaverWatchBridge: WCSessionDelegate {
  func session(
    _ session: WCSession,
    didReceiveMessage message: [String: Any],
    replyHandler: @escaping ([String: Any]) -> Void
  ) {
    if let json = message[yaverWatchMessageKey] as? String {
      forwardInbound(json)
    }
    replyHandler([yaverWatchMessageKey: "{\"v\":1,\"kind\":\"working\",\"spoken\":\"...\"}"])
  }

  func session(_ session: WCSession, didReceiveUserInfo userInfo: [String: Any]) {
    if let json = userInfo[yaverWatchMessageKey] as? String {
      forwardInbound(json)
    }
  }

  func session(
    _ session: WCSession,
    activationDidCompleteWith activationState: WCSessionActivationState,
    error: Error?
  ) {}

  func sessionDidBecomeInactive(_ session: WCSession) {}

  func sessionDidDeactivate(_ session: WCSession) {
    WCSession.default.activate()
  }
}
