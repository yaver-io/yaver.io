import Foundation
import React

@objc(YaverInfo)
final class YaverInfo: NSObject {

  @objc static func requiresMainQueueSetup() -> Bool { false }

  @objc func constantsToExport() -> [AnyHashable: Any] {
    var availableModules: [String] = []
    if let modules = SDKManifest.shared.raw["nativeModules"] as? [String: String] {
      availableModules = Array(modules.keys).sorted()
    }
    let runtimeFamilies: [[String: Any]] = SDKManifest.shared.runtimeFamilies.map { family in
      var payload: [String: Any] = [
        "id": family.id,
        "label": family.label,
        "sdkVersion": family.sdkVersion ?? "",
        "expoVersion": family.expoVersion ?? "",
        "reactNativeVersion": family.reactNativeVersion ?? "",
        "reactVersion": family.reactVersion ?? "",
        "hermesVersion": family.hermesVersion ?? "",
        "hermesBCVersion": family.hermesBCVersion ?? 0,
        "supportedRNRange": family.supportedRNRange ?? "",
      ]
      payload["compiledIn"] = family.compiledIn ?? false
      payload["status"] = family.status ?? ""
      payload["manifestResource"] = family.manifestResource ?? ""
      payload["packageRoot"] = family.packageRoot ?? ""
      payload["preferredPackageNames"] = family.preferredPackageNames ?? []
      return payload
    }
    // Inherited auth so a guest's bundled feedback SDK can skip its own
    // login flow. AppDelegate persists these into UserDefaults at the
    // moment the guest bundle is loaded (YaverBundleLoader writes
    // yaverAgentBaseURL + yaverAgentAuth on every loadBundle call).
    // The user's Convex bearer is also stored when Yaver authenticates.
    let inheritedToken = UserDefaults.standard.string(forKey: "yaverInheritedAuthToken") ?? ""
    let inheritedAgentURL = UserDefaults.standard.string(forKey: "yaverAgentBaseURL") ?? ""
    let inheritedDeviceId = UserDefaults.standard.string(forKey: "yaverInheritedDeviceId") ?? ""

    return [
      "isYaver": true,
      "version": (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "",
      "build": (Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String) ?? "",
      "sdkVersion": SDKManifest.shared.sdkVersion ?? "",
      "hermesBCVersion": Int(SDKManifest.shared.hermesBytecodeVersion),
      "runtimeFamilies": runtimeFamilies,
      "currentRuntimeFamilyId": UserDefaults.standard.string(forKey: "yaverSelectedRuntimeFamilyID")
        ?? SDKManifest.shared.defaultRuntimeFamilyID,
      "defaultRuntimeFamilyId": SDKManifest.shared.defaultRuntimeFamilyID,
      // Guest bundles run inside Yaver's own super-host, not their original app
      // container. Startup code that assumes project-specific entitlements or
      // push-token wiring should opt out when these flags are true.
      "guestSafeMode": true,
      "suppressPushNotifications": true,
      "suppressLocalizationProbe": true,
      "availableModules": availableModules,
      "lastGuestCrashReport": YaverGuestCrashReporter.loadLastCrashReport() ?? NSNull(),
      // Inherited from the host so the guest's SDK can do auth pass-through
      // without re-prompting the user. The SDK validates the token before
      // trusting it; a stale value just falls through to the SDK's own
      // login screen, never crashes.
      "inheritedAuthToken": inheritedToken,
      "inheritedAgentUrl": inheritedAgentURL,
      "inheritedDeviceId": inheritedDeviceId,
    ]
  }

  @objc func getLastGuestCrashReport(
    _ resolve: RCTPromiseResolveBlock,
    rejecter reject: RCTPromiseRejectBlock
  ) {
    resolve(YaverGuestCrashReporter.loadLastCrashReport() ?? NSNull())
  }

  @objc func clearLastGuestCrashReport(
    _ resolve: RCTPromiseResolveBlock,
    rejecter reject: RCTPromiseRejectBlock
  ) {
    resolve(YaverGuestCrashReporter.clearLastCrashReport())
  }

  @objc func consumePendingFeedbackLaunch(
    _ resolve: RCTPromiseResolveBlock,
    rejecter reject: RCTPromiseRejectBlock
  ) {
    let pending = UserDefaults.standard.bool(forKey: "yaverPendingFeedbackLaunch")
    if pending {
      UserDefaults.standard.removeObject(forKey: "yaverPendingFeedbackLaunch")
    }
    resolve(pending)
  }

  // Called by Yaver mobile (host) JS on sign-in / heartbeat so the
  // guest's bundled feedback SDK can read the user's Convex bearer +
  // selected agent URL + deviceId via constantsToExport (above) and
  // skip its own login screen. Stored in NSUserDefaults so it survives
  // a guest-bundle reload — the guest's YaverInfo NativeModule.
  // constants are evaluated each time the bundle initialises, picking
  // up whatever's currently in UserDefaults.
  @objc func setInheritedAuth(_ token: String, agentUrl: String, deviceId: String) {
    let defaults = UserDefaults.standard
    if !token.isEmpty { defaults.set(token, forKey: "yaverInheritedAuthToken") }
    if !agentUrl.isEmpty { defaults.set(agentUrl, forKey: "yaverAgentBaseURL") }
    if !deviceId.isEmpty { defaults.set(deviceId, forKey: "yaverInheritedDeviceId") }
  }

  // Push the relay password independently. The user's relay password is
  // fetched from /settings on sign-in and rotates rarely; it's tied to
  // the user account rather than to any single bundle load, so we
  // accept it as its own setter rather than overloading setInheritedAuth.
  // Native panes (YaverFeedbackPane / YaverAgentsPane) read this via
  // yaverRelayHeaders() to attach X-Relay-Password to every relay-routed
  // request — without it, the relay rejects with HTTP 401 "invalid relay
  // password" or HTTP 404 when the path isn't recognised at all.
  @objc func setInheritedRelayPassword(_ password: String) {
    if password.isEmpty {
      UserDefaults.standard.removeObject(forKey: "yaverInheritedRelayPassword")
    } else {
      UserDefaults.standard.set(password, forKey: "yaverInheritedRelayPassword")
    }
  }

  @objc func clearInheritedAuth() {
    let defaults = UserDefaults.standard
    defaults.removeObject(forKey: "yaverInheritedAuthToken")
    defaults.removeObject(forKey: "yaverInheritedDeviceId")
    defaults.removeObject(forKey: "yaverInheritedRelayPassword")
  }
}

/// yaverResolveAgentURL builds an agent URL from the cached
/// yaverAgentBaseURL, defensively appending /d/<deviceId> when the
/// base looks like a relay host (host ends with .yaver.io or matches
/// the public.yaver.io we configure as the default relay) AND no /d/
/// segment is already present. Without this, a stale base URL
/// (e.g. "https://public.yaver.io" persisted from a prior session
/// before the relay-routing prefix was preserved) sends every native
/// request to the relay's expose-proxy, which 404s with
/// "subdomain 'public' not registered".
///
/// `path` should start with "/" — no leading slash is added.
func yaverResolveAgentURL(_ path: String) -> URL? {
  let agentBase = UserDefaults.standard.string(forKey: "yaverAgentBaseURL") ?? ""
  if agentBase.isEmpty { return nil }
  guard let parsed = URL(string: agentBase) else { return nil }
  let host = (parsed.host ?? "").lowercased()
  let needsRelayPrefix =
    (host.hasSuffix(".yaver.io") || host == "yaver.io") &&
    !parsed.path.hasPrefix("/d/")
  var resolved = agentBase
  if needsRelayPrefix {
    let deviceId = UserDefaults.standard.string(forKey: "yaverInheritedDeviceId") ?? ""
    if !deviceId.isEmpty {
      while resolved.hasSuffix("/") { resolved.removeLast() }
      resolved += "/d/\(deviceId)"
    }
  }
  while resolved.hasSuffix("/") { resolved.removeLast() }
  return URL(string: resolved + path)
}

/// yaverRelayHeaders returns the headers a native pane should attach
/// to every relay-routed request: bearer auth + (when present) the
/// X-Relay-Password the user's relay configuration requires.
func yaverRelayHeaders() -> [String: String] {
  var headers: [String: String] = [:]
  let token = UserDefaults.standard.string(forKey: "yaverInheritedAuthToken") ?? ""
  if !token.isEmpty {
    headers["Authorization"] = "Bearer \(token)"
  }
  let pw = UserDefaults.standard.string(forKey: "yaverInheritedRelayPassword") ?? ""
  if !pw.isEmpty {
    headers["X-Relay-Password"] = pw
  }
  return headers
}
