import UIKit

#if canImport(CarPlay)
import CarPlay

final class YaverCarPlaySceneDelegate: UIResponder, CPTemplateApplicationSceneDelegate {
  private var interfaceController: CPInterfaceController?

  func templateApplicationScene(
    _ templateApplicationScene: CPTemplateApplicationScene,
    didConnect interfaceController: CPInterfaceController
  ) {
    self.interfaceController = interfaceController
    interfaceController.setRootTemplate(makeRootTemplate(), animated: false)
  }

  func templateApplicationScene(
    _ templateApplicationScene: CPTemplateApplicationScene,
    didDisconnect interfaceController: CPInterfaceController
  ) {
    if self.interfaceController === interfaceController {
      self.interfaceController = nil
    }
  }

  private func makeRootTemplate() -> CPTemplate {
    let item = CPListItem(
      text: "Yaver",
      detailText: "Voice runtime ready. Use the iPhone voice loop for dictation and confirmation."
    )
    item.isEnabled = false
    let section = CPListSection(items: [item])
    return CPListTemplate(title: "Yaver", sections: [section])
  }
}
#endif
