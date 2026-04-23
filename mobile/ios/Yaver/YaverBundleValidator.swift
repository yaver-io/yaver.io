import Foundation

struct BundleMetadata: Codable {
  let size: Int
  let md5: String
  let hermesBCVersion: Int
  let moduleName: String
  let format: String
}

struct BundleValidationError {
  let code: String
  let localizedDescription: String
}

enum YaverBundleValidator {
  static func validateMetadata(_ metadata: BundleMetadata) -> BundleValidationError? {
    return nil
  }

  static func validateBundle(data: Data, metadata: BundleMetadata) -> BundleValidationError? {
    return nil
  }
}

final class SDKManifest {
  static let shared = SDKManifest()

  let raw: [String: Any]
  let hermesBytecodeVersion: UInt32

  private init() {
    var parsed: [String: Any] = [:]
    if let url = Bundle.main.url(forResource: "sdk-manifest", withExtension: "json"),
       let data = try? Data(contentsOf: url),
       let obj = try? JSONSerialization.jsonObject(with: data),
       let dict = obj as? [String: Any] {
      parsed = dict
    }
    self.raw = parsed
    var bc: UInt32 = 0
    if let hermes = parsed["hermes"] as? [String: Any],
       let version = hermes["bytecodeVersion"] as? Int {
      bc = UInt32(version)
    }
    self.hermesBytecodeVersion = bc
  }
}
