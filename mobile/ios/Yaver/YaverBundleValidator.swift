import CryptoKit
import Foundation

struct BundleMetadata: Codable {
  let version: Int?
  let size: Int
  let md5: String
  let hermesBCVersion: Int
  let moduleName: String
  let format: String
  let hostSdkVersion: String?
  let hostExpoVersion: String?
  let supportedRNRange: String?
  let reactNativeVersion: String?
  let hostReactNativeVersion: String?
  let reactNativeVersionMismatch: VersionMismatch?
  let projectReactVersion: String?
  let hostReactVersion: String?
  let projectExpoVersion: String?
  let expoSDKVersion: String?
  let expoVersionMismatch: VersionMismatch?
  let incompatibleNativeModules: [String]?
  let nativeModuleVersionMismatches: [NativeModuleVersionMismatch]?
  let reactVersionMismatch: VersionMismatch?
  let guestRuntime: RuntimeFingerprint?
  let runtimeFamilySelection: RuntimeFamilySelection?
  let hostRuntimeFamilies: [RuntimeFamily]?
}

struct NativeModuleVersionMismatch: Codable {
  let name: String
  let projectVersion: String
  let hostVersion: String
  let reason: String
}

struct VersionMismatch: Codable {
  let projectVersion: String
  let hostVersion: String
  let reason: String
}

struct RuntimeFingerprint: Codable {
  let expoVersion: String?
  let reactNativeVersion: String?
  let reactVersion: String?
  let hermesBCVersion: Int?
}

struct RuntimeFamily: Codable {
  let id: String
  let label: String
  let sdkVersion: String?
  let expoVersion: String?
  let reactNativeVersion: String?
  let reactVersion: String?
  let hermesVersion: String?
  let hermesBCVersion: Int?
  let supportedRNRange: String?
}

struct RuntimeFamilySelection: Codable {
  let guest: RuntimeFingerprint
  let selected: RuntimeFamily
  let exactMatch: Bool
  let matchKind: String
  let reason: String?
  let distance: Int?
  let supported: [RuntimeFamily]?
  let supportedHint: String?
}

struct BundleValidationError {
  let code: String
  let localizedDescription: String
}

enum YaverBundleValidator {
  static func validateMetadata(_ metadata: BundleMetadata) -> BundleValidationError? {
    if let version = metadata.version, version != 1 {
      return BundleValidationError(
        code: "METADATA_VERSION_UNSUPPORTED",
        localizedDescription: "Bundle metadata version \(version) is not supported by this Yaver build."
      )
    }
    if metadata.size < 1024 || metadata.size > 100 * 1024 * 1024 {
      return BundleValidationError(
        code: "BUNDLE_SIZE_INVALID",
        localizedDescription: "Bundle size \(metadata.size) bytes is outside Yaver's expected range."
      )
    }
    if metadata.format.lowercased() != "hbc" {
      return BundleValidationError(
        code: "BUNDLE_FORMAT_INVALID",
        localizedDescription: "Expected a Hermes bytecode bundle, got format '\(metadata.format)'."
      )
    }
    if !isHexMD5(metadata.md5) {
      return BundleValidationError(
        code: "BUNDLE_MD5_INVALID",
        localizedDescription: "Bundle metadata MD5 is malformed."
      )
    }
    let expectedBC = Int(SDKManifest.shared.hermesBytecodeVersion)
    if expectedBC > 0 && metadata.hermesBCVersion != expectedBC {
      return BundleValidationError(
        code: "BC_VERSION_MISMATCH",
        localizedDescription: "Hermes BC\(metadata.hermesBCVersion) does not match Yaver BC\(expectedBC)."
      )
    }
    if let hostSDKVersion = trimmed(metadata.hostSdkVersion),
       let localSDKVersion = trimmed(SDKManifest.shared.sdkVersion),
       hostSDKVersion != localSDKVersion {
      return BundleValidationError(
        code: "SDK_MANIFEST_MISMATCH",
        localizedDescription: "Agent host SDK \(hostSDKVersion) does not match phone SDK \(localSDKVersion)."
      )
    }
    if let rnVersion = trimmed(metadata.reactNativeVersion),
       let supportedRange = trimmed(metadata.supportedRNRange),
       !rnVersionMatchesSupportedRange(rnVersion, supportedRange: supportedRange) {
      return BundleValidationError(
        code: "RN_VERSION_UNSUPPORTED",
        localizedDescription: "Project React Native \(rnVersion) is outside Yaver's supported range \(supportedRange)."
      )
    }
    if let incompat = metadata.incompatibleNativeModules, !incompat.isEmpty {
      return BundleValidationError(
        code: "NATIVE_MODULE_INCOMPATIBLE",
        localizedDescription: "Blocked because this project needs native modules Yaver does not include: \(incompat.joined(separator: ", "))."
      )
    }
    if let versionMismatches = metadata.nativeModuleVersionMismatches, !versionMismatches.isEmpty {
      let summary = versionMismatches
        .map { "\($0.name) (\($0.projectVersion) vs \($0.hostVersion))" }
        .joined(separator: ", ")
      return BundleValidationError(
        code: "NATIVE_MODULE_VERSION_MISMATCH",
        localizedDescription: "Blocked because host-native module versions drift at a likely-breaking boundary: \(summary)."
      )
    }
    if let familySelection = metadata.runtimeFamilySelection {
      guard SDKManifest.shared.supportsRuntimeFamily(id: familySelection.selected.id) else {
        return BundleValidationError(
          code: "RUNTIME_FAMILY_UNSUPPORTED",
          localizedDescription: "Host runtime family \(familySelection.selected.id) is not compiled into this Yaver build."
        )
      }
      if !familySelection.exactMatch {
        return BundleValidationError(
          code: "RUNTIME_FAMILY_MISMATCH",
          localizedDescription: "Blocked because guest runtime is closest to host family \(familySelection.selected.label), but does not match it exactly. Supported families: \(familySelection.supportedHint ?? SDKManifest.shared.runtimeFamiliesSummary)"
        )
      }
    }
    if let reactMismatch = metadata.reactVersionMismatch {
      return BundleValidationError(
        code: "REACT_VERSION_MISMATCH",
        localizedDescription: "Blocked because project React \(reactMismatch.projectVersion) does not match Yaver host React \(reactMismatch.hostVersion) at a supported boundary."
      )
    }
    if let reactNativeMismatch = metadata.reactNativeVersionMismatch {
      return BundleValidationError(
        code: "FRAMEWORK_VERSION_MISMATCH",
        localizedDescription: "Blocked because project React Native \(reactNativeMismatch.projectVersion) does not match Yaver host React Native \(reactNativeMismatch.hostVersion)."
      )
    }
    if let expoMismatch = metadata.expoVersionMismatch {
      return BundleValidationError(
        code: "FRAMEWORK_VERSION_MISMATCH",
        localizedDescription: "Blocked because project Expo \(expoMismatch.projectVersion) does not match Yaver host Expo \(expoMismatch.hostVersion)."
      )
    }
    return nil
  }

  static func validateBundle(data: Data, metadata: BundleMetadata) -> BundleValidationError? {
    if data.count != metadata.size {
      return BundleValidationError(
        code: "BUNDLE_SIZE_MISMATCH",
        localizedDescription: "Bundle size \(data.count) does not match metadata size \(metadata.size)."
      )
    }
    let md5 = Insecure.MD5.hash(data: data).map { String(format: "%02x", $0) }.joined()
    if md5 != metadata.md5.lowercased() {
      return BundleValidationError(
        code: "BUNDLE_MD5_MISMATCH",
        localizedDescription: "Bundle checksum does not match metadata."
      )
    }
    guard data.count >= 12 else {
      return BundleValidationError(
        code: "BUNDLE_TOO_SMALL",
        localizedDescription: "Bundle is too small to contain a Hermes header."
      )
    }
    let magic = readUInt32LE(data, offset: 4)
    if magic != 0x1F1903C1 {
      return BundleValidationError(
        code: "BUNDLE_NOT_HERMES",
        localizedDescription: String(format: "Expected Hermes bytecode magic at offset 4, got 0x%08X.", magic)
      )
    }
    let bcVersion = Int(readUInt32LE(data, offset: 8))
    if bcVersion != metadata.hermesBCVersion {
      return BundleValidationError(
        code: "BUNDLE_METADATA_BC_MISMATCH",
        localizedDescription: "Bundle BC\(bcVersion) does not match metadata BC\(metadata.hermesBCVersion)."
      )
    }
    let expectedBC = Int(SDKManifest.shared.hermesBytecodeVersion)
    if expectedBC > 0 && bcVersion != expectedBC {
      return BundleValidationError(
        code: "BC_VERSION_MISMATCH",
        localizedDescription: "Bundle BC\(bcVersion) does not match Yaver BC\(expectedBC)."
      )
    }
    return nil
  }

  private static func isHexMD5(_ value: String) -> Bool {
    guard value.count == 32 else { return false }
    return value.range(of: "^[0-9a-fA-F]{32}$", options: .regularExpression) != nil
  }

  private static func readUInt32LE(_ data: Data, offset: Int) -> UInt32 {
    let b0 = UInt32(data[offset])
    let b1 = UInt32(data[offset + 1]) << 8
    let b2 = UInt32(data[offset + 2]) << 16
    let b3 = UInt32(data[offset + 3]) << 24
    return b0 | b1 | b2 | b3
  }

  private static func rnVersionMatchesSupportedRange(_ version: String, supportedRange: String) -> Bool {
    let cleanVersion = version.trimmingCharacters(in: .whitespacesAndNewlines)
      .trimmingCharacters(in: CharacterSet(charactersIn: "^~>=<"))
    let cleanRange = supportedRange.trimmingCharacters(in: .whitespacesAndNewlines)
    if cleanRange.hasSuffix(".x") {
      let prefix = String(cleanRange.dropLast(2))
      return cleanVersion.hasPrefix(prefix + ".") || cleanVersion == prefix
    }
    return cleanVersion == cleanRange
  }

  private static func trimmed(_ value: String?) -> String? {
    guard let value else { return nil }
    let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
    return trimmed.isEmpty ? nil : trimmed
  }
}

final class SDKManifest {
  static let shared = SDKManifest()

  let raw: [String: Any]
  let hermesBytecodeVersion: UInt32
  let sdkVersion: String?
  let reactNativeVersion: String?
  let supportedRNRange: String?
  let runtimeFamilies: [RuntimeFamily]
  let defaultRuntimeFamilyID: String

  private init() {
    var parsed: [String: Any] = [:]
    if let url = Bundle.main.url(forResource: "sdk-manifest", withExtension: "json"),
       let data = try? Data(contentsOf: url),
       let obj = try? JSONSerialization.jsonObject(with: data),
       let dict = obj as? [String: Any] {
      parsed = dict
    }
    self.raw = parsed
    self.sdkVersion = parsed["sdkVersion"] as? String
    self.reactNativeVersion = parsed["reactNative"] as? String
    self.supportedRNRange = parsed["supportedRNRange"] as? String
    var bc: UInt32 = 0
    if let hermes = parsed["hermes"] as? [String: Any],
       let version = hermes["bytecodeVersion"] as? Int {
      bc = UInt32(version)
    }
    self.hermesBytecodeVersion = bc
    if let families = SDKManifest.parseRuntimeFamilies(from: parsed), !families.isEmpty {
      self.runtimeFamilies = families
    } else {
      self.runtimeFamilies = [SDKManifest.synthesizedDefaultFamily(from: parsed, bc: Int(bc))]
    }
    self.defaultRuntimeFamilyID = self.runtimeFamilies.first?.id ?? "default"
  }

  func supportsRuntimeFamily(id: String?) -> Bool {
    guard let id = trimmed(id) else { return false }
    return runtimeFamilies.contains(where: { $0.id == id })
  }

  func runtimeFamily(id: String?) -> RuntimeFamily? {
    guard let id = trimmed(id) else { return nil }
    return runtimeFamilies.first(where: { $0.id == id })
  }

  var runtimeFamiliesSummary: String {
    runtimeFamilies.map { "\($0.id) (\($0.label))" }.joined(separator: "; ")
  }

  private static func parseRuntimeFamilies(from parsed: [String: Any]) -> [RuntimeFamily]? {
    guard let rawFamilies = parsed["runtimeFamilies"] else { return nil }
    guard let data = try? JSONSerialization.data(withJSONObject: rawFamilies) else { return nil }
    return try? JSONDecoder().decode([RuntimeFamily].self, from: data)
  }

  private static func synthesizedDefaultFamily(from parsed: [String: Any], bc: Int) -> RuntimeFamily {
    let sdkVersion = parsed["sdkVersion"] as? String
    let expo = parsed["expo"] as? String
    let reactNative = parsed["reactNative"] as? String
    let react = parsed["react"] as? String
    let supportedRNRange = parsed["supportedRNRange"] as? String
    let hermesVersion = (parsed["hermes"] as? [String: Any])?["version"] as? String
    let id = "expo-\(runtimeToken(expo))-rn-\(runtimeToken(reactNative))-react-\(runtimeToken(react))-bc-\(bc)"
    let label = "Expo \(expo ?? "?") / RN \(reactNative ?? "?") / React \(react ?? "?") / BC\(bc)"
    return RuntimeFamily(
      id: id,
      label: label,
      sdkVersion: sdkVersion,
      expoVersion: expo,
      reactNativeVersion: reactNative,
      reactVersion: react,
      hermesVersion: hermesVersion,
      hermesBCVersion: bc,
      supportedRNRange: supportedRNRange
    )
  }

  private static func runtimeToken(_ raw: String?) -> String {
    let value = trimmed(raw) ?? "unknown"
    return value
      .replacingOccurrences(of: ".", with: "-")
      .replacingOccurrences(of: "^", with: "")
      .replacingOccurrences(of: "~", with: "")
      .replacingOccurrences(of: " ", with: "")
      .lowercased()
  }

  private static func trimmed(_ value: String?) -> String? {
    guard let value else { return nil }
    let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
    return trimmed.isEmpty ? nil : trimmed
  }
}
