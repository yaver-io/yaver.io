import UIKit
import React

#if canImport(CarPlay)
import CarPlay

/// The CarPlay voice surface.
///
/// Apple's `carplay-voice-based-conversation` category (iOS 26.4+) has three
/// criteria, and this file is written to satisfy them literally:
///
///   1. "primary modality of voice upon launch" — connecting the scene starts a
///      turn immediately. There is no menu, no list, no tap-to-begin.
///   2. "only hold an audio session open when voice features are actively being
///      used" — we never touch the audio session here. The JS car-voice screen
///      acquires it for the duration of a turn and releases it on every exit
///      path (car-voice-coding.tsx::releaseAudioSession).
///   3. "don't show text or imagery in response to queries" — the ONLY thing
///      this scene ever renders is a CPVoiceControlTemplate showing which of
///      four states we're in. Results are SPOKEN. Code, diffs, and logs are
///      refused outright upstream (carVoiceCoding.ts::isReadCodeRequest).
///
/// Wiring, deliberately reusing what already exists rather than adding a native
/// module: on connect we set the same `yaverPendingCarVoiceLaunch` flag and open
/// the same `yaver://car-voice-coding?autostart=1` deep link that the home-screen
/// quick action uses (AppDelegate::performActionFor). JS drives the turn and
/// reports progress back through `YaverInfo.setCarPlayVoiceState`, which posts
/// `carPlayVoiceStateNotification`; we observe it and move the template.
///
/// Without the entitlement this scene is never instantiated — CarPlay simply
/// won't launch it. It is safe to ship un-entitled (1.18.143 shipped with the
/// scene manifest and passed review), and it is testable today in the Xcode
/// Simulator via I/O → External Displays → CarPlay.
final class YaverCarPlaySceneDelegate: UIResponder, CPTemplateApplicationSceneDelegate {
  /// Posted by YaverInfo.setCarPlayVoiceState(_:) from JS.
  static let voiceStateNotification = Notification.Name("YaverCarPlayVoiceState")

  private var interfaceController: CPInterfaceController?
  private var voiceTemplate: CPVoiceControlTemplate?
  private var observer: NSObjectProtocol?

  /// The four states the driver can be in. Identifiers are the contract with
  /// JS — car-voice-coding.tsx maps its own status onto exactly these strings.
  private enum VoiceState: String {
    case ready, listening, working, speaking
  }

  func templateApplicationScene(
    _ templateApplicationScene: CPTemplateApplicationScene,
    didConnect interfaceController: CPInterfaceController
  ) {
    self.interfaceController = interfaceController

    let template = CPVoiceControlTemplate(voiceControlStates: [
      Self.state(.ready, "Yaver is ready", "waveform.circle"),
      Self.state(.listening, "Listening", "mic.circle"),
      Self.state(.working, "Working on your request", "gearshape.2"),
      Self.state(.speaking, "Reading back status", "speaker.wave.2.circle"),
    ])
    voiceTemplate = template
    interfaceController.setRootTemplate(template, animated: true, completion: nil)
    template.activateVoiceControlState(withIdentifier: VoiceState.ready.rawValue)

    // JS → template. Buffered by nothing: if the JS side isn't up yet, the
    // template simply stays on "ready" until the first state arrives.
    observer = NotificationCenter.default.addObserver(
      forName: Self.voiceStateNotification,
      object: nil,
      queue: .main
    ) { [weak self] note in
      guard let raw = note.userInfo?["state"] as? String,
            let state = VoiceState(rawValue: raw)
      else { return }
      self?.voiceTemplate?.activateVoiceControlState(withIdentifier: state.rawValue)
    }

    startTurn()
  }

  /// Criterion 1: voice is the primary modality *on launch*. Launching Yaver
  /// from the CarPlay home screen IS the gesture — the driver should not have
  /// to find a button. This routes through the identical path as the phone's
  /// home-screen quick action, so there is exactly one way into a car turn.
  private func startTurn() {
    guard let url = URL(string: "yaver://car-voice-coding?autostart=1") else { return }
    UserDefaults.standard.set(true, forKey: "yaverPendingCarVoiceLaunch")
    _ = RCTLinkingManager.application(UIApplication.shared, open: url, options: [:])
  }

  private static func state(
    _ id: VoiceState,
    _ title: String,
    _ symbol: String
  ) -> CPVoiceControlState {
    CPVoiceControlState(
      identifier: id.rawValue,
      titleVariants: [title],
      image: UIImage(systemName: symbol),
      repeats: true
    )
  }
}

extension YaverCarPlaySceneDelegate {
  func templateApplicationScene(
    _ templateApplicationScene: CPTemplateApplicationScene,
    didDisconnect interfaceController: CPInterfaceController
  ) {
    if let observer {
      NotificationCenter.default.removeObserver(observer)
      self.observer = nil
    }
    if self.interfaceController === interfaceController {
      self.interfaceController = nil
    }
    voiceTemplate = nil
  }
}
#endif
