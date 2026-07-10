import UIKit

#if canImport(CarPlay)
import CarPlay

final class YaverCarPlaySceneDelegate: UIResponder, CPTemplateApplicationSceneDelegate {
  private var interfaceController: CPInterfaceController?
  private var voiceTemplate: CPVoiceControlTemplate?

  func templateApplicationScene(
    _ templateApplicationScene: CPTemplateApplicationScene,
    didConnect interfaceController: CPInterfaceController
  ) {
    self.interfaceController = interfaceController

    let ready = CPVoiceControlState(
      identifier: "ready",
      titleVariants: ["Yaver is ready"],
      image: UIImage(systemName: "waveform.circle"),
      repeats: true
    )
    let listening = CPVoiceControlState(
      identifier: "listening",
      titleVariants: ["Listening"],
      image: UIImage(systemName: "mic.circle"),
      repeats: true
    )
    let working = CPVoiceControlState(
      identifier: "working",
      titleVariants: ["Working on your request"],
      image: UIImage(systemName: "gearshape.2"),
      repeats: true
    )
    let speaking = CPVoiceControlState(
      identifier: "speaking",
      titleVariants: ["Reading back status"],
      image: UIImage(systemName: "speaker.wave.2.circle"),
      repeats: true
    )

    let template = CPVoiceControlTemplate(voiceControlStates: [ready, listening, working, speaking])
    voiceTemplate = template
    interfaceController.setRootTemplate(template, animated: true, completion: nil)
    template.activateVoiceControlState(withIdentifier: "ready")
  }

}

extension YaverCarPlaySceneDelegate {
  func templateApplicationScene(
    _ templateApplicationScene: CPTemplateApplicationScene,
    didDisconnect interfaceController: CPInterfaceController
  ) {
    if self.interfaceController === interfaceController {
      self.interfaceController = nil
    }
    voiceTemplate = nil
  }
}
#endif
