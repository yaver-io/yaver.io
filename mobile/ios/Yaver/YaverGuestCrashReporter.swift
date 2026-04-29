import Foundation

struct YaverGuestCrashReport: Codable {
  let timestamp: String
  let phase: String
  let message: String
  let moduleName: String?
  let sourceURL: String?
  let bundlePath: String?
  let appVersion: String?
  let appBuild: String?
}

struct YaverGuestSessionMarker: Codable {
  let startedAt: String
  let updatedAt: String
  let phase: String
  let moduleName: String?
  let sourceURL: String?
  let bundlePath: String?
  let appVersion: String?
  let appBuild: String?
}

enum YaverGuestCrashReporter {
  private static let markerKey = "yaver.guest.activeSession"
  private static let reportKey = "yaver.guest.lastCrashReport"

  static func recoverCrashIfNeeded() {
    guard let marker: YaverGuestSessionMarker = load(YaverGuestSessionMarker.self, forKey: markerKey) else {
      return
    }
    let report = YaverGuestCrashReport(
      timestamp: isoNow(),
      phase: marker.phase,
      message: "The guest app terminated unexpectedly while Yaver was in phase '\(marker.phase)'.",
      moduleName: marker.moduleName,
      sourceURL: marker.sourceURL,
      bundlePath: marker.bundlePath,
      appVersion: marker.appVersion,
      appBuild: marker.appBuild
    )
    save(report, forKey: reportKey)
    clear(forKey: markerKey)
  }

  static func markGuestPhase(
    _ phase: String,
    moduleName: String? = nil,
    sourceURL: String? = nil,
    bundlePath: String? = nil
  ) {
    let defaults = UserDefaults.standard
    let current = load(YaverGuestSessionMarker.self, forKey: markerKey)
    let marker = YaverGuestSessionMarker(
      startedAt: current?.startedAt ?? isoNow(),
      updatedAt: isoNow(),
      phase: phase,
      moduleName: moduleName ?? current?.moduleName,
      sourceURL: sourceURL ?? current?.sourceURL,
      bundlePath: bundlePath ?? current?.bundlePath,
      appVersion: appVersion(),
      appBuild: appBuild()
    )
    if let data = try? JSONEncoder().encode(marker) {
      defaults.set(data, forKey: markerKey)
    }
  }

  static func recordGuestFailure(
    phase: String,
    message: String,
    moduleName: String? = nil,
    sourceURL: String? = nil,
    bundlePath: String? = nil
  ) {
    let report = YaverGuestCrashReport(
      timestamp: isoNow(),
      phase: phase,
      message: message,
      moduleName: moduleName,
      sourceURL: sourceURL,
      bundlePath: bundlePath,
      appVersion: appVersion(),
      appBuild: appBuild()
    )
    save(report, forKey: reportKey)
  }

  static func clearGuestSession() {
    clear(forKey: markerKey)
  }

  static func loadLastCrashReport() -> [String: Any]? {
    guard let report: YaverGuestCrashReport = load(YaverGuestCrashReport.self, forKey: reportKey),
          let data = try? JSONEncoder().encode(report),
          let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
      return nil
    }
    return object
  }

  @discardableResult
  static func clearLastCrashReport() -> Bool {
    clear(forKey: reportKey)
    return true
  }

  private static func save<T: Encodable>(_ value: T, forKey key: String) {
    if let data = try? JSONEncoder().encode(value) {
      UserDefaults.standard.set(data, forKey: key)
    }
  }

  private static func load<T: Decodable>(_ type: T.Type, forKey key: String) -> T? {
    guard let data = UserDefaults.standard.data(forKey: key) else { return nil }
    return try? JSONDecoder().decode(type, from: data)
  }

  private static func clear(forKey key: String) {
    UserDefaults.standard.removeObject(forKey: key)
  }

  private static func isoNow() -> String {
    ISO8601DateFormatter().string(from: Date())
  }

  private static func appVersion() -> String? {
    Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
  }

  private static func appBuild() -> String? {
    Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String
  }
}
