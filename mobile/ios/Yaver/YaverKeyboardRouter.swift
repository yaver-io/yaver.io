import Foundation
import React
import UIKit
import GameController

/// YaverKeyboardRouter — phone-side exclusive(-ish) HID grab for a paired
/// Bluetooth keyboard. Forwards keystrokes to JS as `YaverKey` events so
/// the TS-side `keyboardRouter` can dispatch them to the right remote
/// sink (terminal pty / remote browser DataChannel / voice control).
///
/// Apple's iPadOS does NOT let third-party apps fully suppress system
/// shortcuts (⌘-H goes home, ⌘-Space opens Spotlight on iPad with hw
/// keyboard, etc.) — those bypass any in-app capture. Everything else
/// is fair game: we receive keyDown via GCKeyboard.coalesced and JS
/// decides what to do with it.
///
/// JS side:
///
///   const router = NativeEventEmitter(NativeModules.YaverKeyboardRouter)
///   router.addListener("YaverKey", (ev) => keyboardRouter.handleKey(ev))
///   await NativeModules.YaverKeyboardRouter.grab({ exclusive: false })
///   …
///   await NativeModules.YaverKeyboardRouter.release()
@objc(YaverKeyboardRouter)
class YaverKeyboardRouter: RCTEventEmitter {

  private var isGrabbed = false
  private var connectObserver: NSObjectProtocol?
  private var disconnectObserver: NSObjectProtocol?

  override static func requiresMainQueueSetup() -> Bool { return true }
  override func supportedEvents() -> [String]! {
    return ["YaverKey", "YaverKeyboardConnected", "YaverKeyboardDisconnected"]
  }

  override func startObserving() {}
  override func stopObserving() {}

  @objc func grab(_ opts: NSDictionary?,
                  resolver resolve: @escaping RCTPromiseResolveBlock,
                  rejecter reject: @escaping RCTPromiseRejectBlock) {
    if #available(iOS 14.0, *) {
      DispatchQueue.main.async {
        if self.isGrabbed {
          resolve(["alreadyGrabbed": true])
          return
        }
        self.isGrabbed = true
        self.attachHandler()
        self.connectObserver = NotificationCenter.default.addObserver(
          forName: .GCKeyboardDidConnect,
          object: nil,
          queue: .main
        ) { [weak self] _ in
          self?.attachHandler()
          self?.sendEvent(withName: "YaverKeyboardConnected", body: [:])
        }
        self.disconnectObserver = NotificationCenter.default.addObserver(
          forName: .GCKeyboardDidDisconnect,
          object: nil,
          queue: .main
        ) { [weak self] _ in
          self?.sendEvent(withName: "YaverKeyboardDisconnected", body: [:])
        }
        resolve(["alreadyGrabbed": false])
      }
    } else {
      reject("UNSUPPORTED",
             "GCKeyboard requires iOS 14+. Update the device or use the TS-only keyboardRouter fallback.",
             nil)
    }
  }

  @objc func release(_ resolve: @escaping RCTPromiseResolveBlock,
                     rejecter reject: @escaping RCTPromiseRejectBlock) {
    DispatchQueue.main.async {
      self.detachHandler()
      if let obs = self.connectObserver {
        NotificationCenter.default.removeObserver(obs)
        self.connectObserver = nil
      }
      if let obs = self.disconnectObserver {
        NotificationCenter.default.removeObserver(obs)
        self.disconnectObserver = nil
      }
      self.isGrabbed = false
      resolve(nil)
    }
  }

  @objc func isAttached(_ resolve: @escaping RCTPromiseResolveBlock,
                        rejecter reject: @escaping RCTPromiseRejectBlock) {
    if #available(iOS 14.0, *) {
      resolve(GCKeyboard.coalesced != nil)
    } else {
      resolve(false)
    }
  }

  // MARK: - GCKeyboard plumbing

  private func attachHandler() {
    if #available(iOS 14.0, *) {
      guard let kb = GCKeyboard.coalesced?.keyboardInput else { return }
      kb.keyChangedHandler = { [weak self] _, _, keyCode, pressed in
        guard let self = self, self.isGrabbed, pressed else { return }
        let payload = self.payload(for: keyCode)
        self.sendEvent(withName: "YaverKey", body: payload)
      }
    }
  }

  private func detachHandler() {
    if #available(iOS 14.0, *) {
      GCKeyboard.coalesced?.keyboardInput?.keyChangedHandler = nil
    }
  }

  @available(iOS 14.0, *)
  private func payload(for keyCode: GCKeyCode) -> [String: Any] {
    let kb = GCKeyboard.coalesced?.keyboardInput
    let shift = kb?.button(forKeyCode: .leftShift)?.isPressed == true ||
                kb?.button(forKeyCode: .rightShift)?.isPressed == true
    let ctrl  = kb?.button(forKeyCode: .leftControl)?.isPressed == true ||
                kb?.button(forKeyCode: .rightControl)?.isPressed == true
    let alt   = kb?.button(forKeyCode: .leftAlt)?.isPressed == true ||
                kb?.button(forKeyCode: .rightAlt)?.isPressed == true
    let meta  = kb?.button(forKeyCode: .leftGUI)?.isPressed == true ||
                kb?.button(forKeyCode: .rightGUI)?.isPressed == true
    return [
      "key": Self.named(for: keyCode, shift: shift),
      "modifiers": [
        "shift": shift,
        "ctrl":  ctrl,
        "alt":   alt,
        "meta":  meta,
      ]
    ]
  }

  /// Map GCKeyCode → the same string keys the JS side expects.
  /// Matches the `NAMED_KEYS` set in `mobile/src/lib/keyboardRouter.ts`,
  /// plus printable characters.
  @available(iOS 14.0, *)
  private static func named(for keyCode: GCKeyCode, shift: Bool) -> String {
    switch keyCode {
    case .returnOrEnter: return "Enter"
    case .tab: return "Tab"
    case .deleteOrBackspace: return "Backspace"
    case .escape: return "Escape"
    case .leftArrow: return "ArrowLeft"
    case .rightArrow: return "ArrowRight"
    case .upArrow: return "ArrowUp"
    case .downArrow: return "ArrowDown"
    case .home: return "Home"
    case .end: return "End"
    case .pageUp: return "PageUp"
    case .pageDown: return "PageDown"
    case .deleteForward: return "Delete"
    case .spacebar: return " "
    case .keyA: return shift ? "A" : "a"
    case .keyB: return shift ? "B" : "b"
    case .keyC: return shift ? "C" : "c"
    case .keyD: return shift ? "D" : "d"
    case .keyE: return shift ? "E" : "e"
    case .keyF: return shift ? "F" : "f"
    case .keyG: return shift ? "G" : "g"
    case .keyH: return shift ? "H" : "h"
    case .keyI: return shift ? "I" : "i"
    case .keyJ: return shift ? "J" : "j"
    case .keyK: return shift ? "K" : "k"
    case .keyL: return shift ? "L" : "l"
    case .keyM: return shift ? "M" : "m"
    case .keyN: return shift ? "N" : "n"
    case .keyO: return shift ? "O" : "o"
    case .keyP: return shift ? "P" : "p"
    case .keyQ: return shift ? "Q" : "q"
    case .keyR: return shift ? "R" : "r"
    case .keyS: return shift ? "S" : "s"
    case .keyT: return shift ? "T" : "t"
    case .keyU: return shift ? "U" : "u"
    case .keyV: return shift ? "V" : "v"
    case .keyW: return shift ? "W" : "w"
    case .keyX: return shift ? "X" : "x"
    case .keyY: return shift ? "Y" : "y"
    case .keyZ: return shift ? "Z" : "z"
    case .one:   return shift ? "!" : "1"
    case .two:   return shift ? "@" : "2"
    case .three: return shift ? "#" : "3"
    case .four:  return shift ? "$" : "4"
    case .five:  return shift ? "%" : "5"
    case .six:   return shift ? "^" : "6"
    case .seven: return shift ? "&" : "7"
    case .eight: return shift ? "*" : "8"
    case .nine:  return shift ? "(" : "9"
    case .zero:  return shift ? ")" : "0"
    case .hyphen:      return shift ? "_" : "-"
    case .equalSign:   return shift ? "+" : "="
    case .openBracket: return shift ? "{" : "["
    case .closeBracket:return shift ? "}" : "]"
    case .backslash:   return shift ? "|" : "\\"
    case .semicolon:   return shift ? ":" : ";"
    case .quote:       return shift ? "\"" : "'"
    case .graveAccentAndTilde: return shift ? "~" : "`"
    case .comma:       return shift ? "<" : ","
    case .period:      return shift ? ">" : "."
    case .slash:       return shift ? "?" : "/"
    default: return ""
    }
  }
}
