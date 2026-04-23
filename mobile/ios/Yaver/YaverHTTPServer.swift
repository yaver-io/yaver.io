import Foundation

final class YaverHTTPServer {
  static let shared = YaverHTTPServer()

  var onBundleReceived: (() -> Void)?

  private init() {}

  func start() {}
  func stop() {}
}
