package io.yaver.mobile

// Android port of mobile/ios/Yaver/YaverBundleValidator.swift.
// Minimum-viable subset: the structural checks the loader cannot skip
// without risking a JS-engine crash at boot (size, MD5, Hermes magic,
// BC version). The richer semantic checks the iOS validator performs
// (runtime-family selection, native-module version drift, supported
// RN range) ride on top of these — porting them is Phase 2; for MVP
// we log them as warnings instead of blocking.

import org.json.JSONObject
import java.security.MessageDigest

data class BundleValidationError(val code: String, val message: String)

/** Compact metadata view the validator works against. JSON parsing
 *  stays at the call site so the validator itself is pure. Mirrors
 *  the iOS `BundleMetadata` Codable fields the Swift validator
 *  consumes (mobile/ios/Yaver/YaverBundleValidator.swift:4-28). */
data class BundleMetadata(
    val version: Int?,
    val size: Long,
    val md5: String,
    val hermesBCVersion: Int,
    val moduleName: String,
    val format: String,
    val hostSdkVersion: String?,
    val reactNativeVersion: String?,
    val supportedRNRange: String?,
    val runtimeFamilyId: String?,
    val runtimeFamilyLabel: String?,
    val runtimeFamilyExactMatch: Boolean,
    /** Names of host-native modules a guest expects but Yaver was
     *  not compiled with — agent populates this when the project's
     *  package.json references a module not in the host SDK manifest. */
    val incompatibleNativeModules: List<String>,
    /** Native-module version drift (project vs host) that the agent
     *  flagged as likely-breaking. Each entry: "name:projectVersion vs hostVersion". */
    val nativeModuleVersionMismatchSummary: String?,
    val reactVersionMismatchSummary: String?,
    val reactNativeVersionMismatchSummary: String?,
    val expoVersionMismatchSummary: String?,
) {
  companion object {
    fun fromHeader(json: String): BundleMetadata? {
      return try {
        val obj = JSONObject(json)
        val sel = obj.optJSONObject("runtimeFamilySelection")
        val selected = sel?.optJSONObject("selected")
        val incompat = obj.optJSONArray("incompatibleNativeModules")?.let { arr ->
          (0 until arr.length()).mapNotNull { i -> arr.optString(i).takeIf { it.isNotEmpty() } }
        } ?: emptyList()
        val nmvSummary = obj.optJSONArray("nativeModuleVersionMismatches")?.let { arr ->
          if (arr.length() == 0) null
          else (0 until arr.length()).mapNotNull { i ->
            arr.optJSONObject(i)?.let { m ->
              "${m.optString("name")} (${m.optString("projectVersion")} vs ${m.optString("hostVersion")})"
            }
          }.joinToString(", ")
        }
        BundleMetadata(
            version = if (obj.has("version")) obj.optInt("version") else null,
            size = obj.optLong("size", 0L),
            md5 = obj.optString("md5", ""),
            hermesBCVersion = obj.optInt("hermesBCVersion", 0),
            moduleName = obj.optString("moduleName", ""),
            format = obj.optString("format", ""),
            hostSdkVersion = obj.optString("hostSdkVersion").ifEmpty { null },
            reactNativeVersion = obj.optString("reactNativeVersion").ifEmpty { null },
            supportedRNRange = obj.optString("supportedRNRange").ifEmpty { null },
            runtimeFamilyId = selected?.optString("id"),
            runtimeFamilyLabel = selected?.optString("label"),
            runtimeFamilyExactMatch = sel?.optBoolean("exactMatch", true) ?: true,
            incompatibleNativeModules = incompat,
            nativeModuleVersionMismatchSummary = nmvSummary,
            reactVersionMismatchSummary = obj.optJSONObject("reactVersionMismatch")?.let {
              "React ${it.optString("projectVersion")} vs host ${it.optString("hostVersion")}"
            },
            reactNativeVersionMismatchSummary = obj.optJSONObject("reactNativeVersionMismatch")?.let {
              "RN ${it.optString("projectVersion")} vs host ${it.optString("hostVersion")}"
            },
            expoVersionMismatchSummary = obj.optJSONObject("expoVersionMismatch")?.let {
              "Expo ${it.optString("projectVersion")} vs host ${it.optString("hostVersion")}"
            },
        )
      } catch (_: Throwable) {
        null
      }
    }
  }
}

object YaverBundleValidator {

  /** Pre-validate metadata BEFORE we even look at the bundle bytes —
   *  catches BC mismatch (cheapest signal a guest project was built
   *  against the wrong RN) without reading 5+ MB of payload. */
  fun validateMetadata(metadata: BundleMetadata): BundleValidationError? {
    metadata.version?.let { v ->
      if (v != 1) {
        return BundleValidationError(
            "METADATA_VERSION_UNSUPPORTED",
            "Bundle metadata version $v is not supported by this Yaver build.")
      }
    }
    if (metadata.size < 1024 || metadata.size > 100L * 1024 * 1024) {
      return BundleValidationError(
          "BUNDLE_SIZE_INVALID",
          "Bundle size ${metadata.size} bytes is outside Yaver's expected range.")
    }
    if (metadata.format.isNotEmpty() && metadata.format.lowercase() != "hbc") {
      return BundleValidationError(
          "BUNDLE_FORMAT_INVALID",
          "Expected a Hermes bytecode bundle, got format '${metadata.format}'.")
    }
    if (metadata.md5.isNotEmpty() && !isHexMD5(metadata.md5)) {
      return BundleValidationError(
          "BUNDLE_MD5_INVALID", "Bundle metadata MD5 is malformed.")
    }
    val expectedBC = YaverSDKManifest.hermesBytecodeVersion
    if (expectedBC > 0 && metadata.hermesBCVersion > 0 && metadata.hermesBCVersion != expectedBC) {
      return BundleValidationError(
          "BC_VERSION_MISMATCH",
          "Hermes BC${metadata.hermesBCVersion} does not match Yaver BC$expectedBC.")
    }
    // Host SDK mismatch — agent's runtime-families manifest disagrees
    // with the SDK manifest baked into this Yaver build. iOS rejects
    // ("SDK_MANIFEST_MISMATCH") because a guest built against a
    // different host SDK loads against a different native module
    // surface. Android did the same gap silently — now matches iOS.
    val hostSdk = metadata.hostSdkVersion?.trim().orEmpty()
    val localSdk = YaverSDKManifest.sdkVersion?.trim().orEmpty()
    if (hostSdk.isNotEmpty() && localSdk.isNotEmpty() && hostSdk != localSdk) {
      return BundleValidationError(
          "SDK_MANIFEST_MISMATCH",
          "Agent host SDK $hostSdk does not match phone SDK $localSdk.")
    }
    // RN version outside the host's supported range — e.g. project
    // built against RN 0.79 trying to mount in a Yaver compiled for
    // RN 0.81.x.
    val rnVersion = metadata.reactNativeVersion?.trim().orEmpty()
    val rnRange = metadata.supportedRNRange?.trim().orEmpty()
    if (rnVersion.isNotEmpty() && rnRange.isNotEmpty() && !rnVersionInRange(rnVersion, rnRange)) {
      return BundleValidationError(
          "RN_VERSION_UNSUPPORTED",
          "Project React Native $rnVersion is outside Yaver's supported range $rnRange.")
    }
    if (metadata.incompatibleNativeModules.isNotEmpty()) {
      return BundleValidationError(
          "NATIVE_MODULE_INCOMPATIBLE",
          "Blocked because this project needs native modules Yaver does not include: " +
              metadata.incompatibleNativeModules.joinToString(", ") + ".")
    }
    metadata.nativeModuleVersionMismatchSummary?.let {
      return BundleValidationError(
          "NATIVE_MODULE_VERSION_MISMATCH",
          "Blocked because host-native module versions drift at a likely-breaking boundary: $it.")
    }
    if (metadata.runtimeFamilyId != null && !metadata.runtimeFamilyExactMatch) {
      return BundleValidationError(
          "RUNTIME_FAMILY_MISMATCH",
          "Blocked because guest runtime is closest to host family " +
              "${metadata.runtimeFamilyLabel ?: metadata.runtimeFamilyId} but does not match it exactly.")
    }
    metadata.reactVersionMismatchSummary?.let {
      return BundleValidationError("REACT_VERSION_MISMATCH", "Blocked because $it at a supported boundary.")
    }
    metadata.reactNativeVersionMismatchSummary?.let {
      return BundleValidationError("FRAMEWORK_VERSION_MISMATCH", "Blocked because $it.")
    }
    metadata.expoVersionMismatchSummary?.let {
      return BundleValidationError("FRAMEWORK_VERSION_MISMATCH", "Blocked because $it.")
    }
    return null
  }

  /** Tiny semver-range checker matching the iOS validator's behaviour.
   *  Handles the two forms the manifest uses: "0.81.x" wildcard and
   *  exact version strings. Range prefixes ^/~/>= are stripped before
   *  comparison. Not a full semver implementation — deliberately. */
  private fun rnVersionInRange(version: String, range: String): Boolean {
    val cleanVersion = version.trim().trimStart('^', '~', '>', '=', '<', ' ')
    val cleanRange = range.trim()
    return if (cleanRange.endsWith(".x")) {
      val prefix = cleanRange.dropLast(2)
      cleanVersion.startsWith("$prefix.") || cleanVersion == prefix
    } else {
      cleanVersion == cleanRange
    }
  }

  /** Full byte-level validation: size, MD5, Hermes magic at offset 4,
   *  BC version at offset 8. iOS's validator does the same in
   *  YaverBundleValidator.validateBundle. */
  fun validateBundle(data: ByteArray, metadata: BundleMetadata): BundleValidationError? {
    if (metadata.size > 0 && data.size.toLong() != metadata.size) {
      return BundleValidationError(
          "BUNDLE_SIZE_MISMATCH",
          "Bundle size ${data.size} does not match metadata size ${metadata.size}.")
    }
    if (metadata.md5.isNotEmpty()) {
      val actual = md5Hex(data)
      if (!actual.equals(metadata.md5, ignoreCase = true)) {
        return BundleValidationError(
            "BUNDLE_MD5_MISMATCH", "Bundle checksum does not match metadata.")
      }
    }
    if (data.size < 12) {
      return BundleValidationError(
          "BUNDLE_TOO_SMALL", "Bundle is too small to contain a Hermes header.")
    }
    val magic = readUInt32LE(data, 4)
    if (magic != 0x1F1903C1L) {
      return BundleValidationError(
          "BUNDLE_NOT_HERMES",
          String.format("Expected Hermes bytecode magic at offset 4, got 0x%08X.", magic))
    }
    val bcVersion = readUInt32LE(data, 8).toInt()
    if (metadata.hermesBCVersion > 0 && bcVersion != metadata.hermesBCVersion) {
      return BundleValidationError(
          "BUNDLE_METADATA_BC_MISMATCH",
          "Bundle BC$bcVersion does not match metadata BC${metadata.hermesBCVersion}.")
    }
    val expectedBC = YaverSDKManifest.hermesBytecodeVersion
    if (expectedBC > 0 && bcVersion != expectedBC) {
      return BundleValidationError(
          "BC_VERSION_MISMATCH",
          "Bundle BC$bcVersion does not match Yaver BC$expectedBC.")
    }
    return null
  }

  /** Legacy-path BC check the loader runs when the agent didn't send
   *  an X-Yaver-Bundle-Metadata header (older agents, raw curl pushes).
   *  Mirrors the same fallback iOS YaverBundleLoader.swift uses. */
  fun legacyBCCheck(data: ByteArray): BundleValidationError? {
    if (data.size < 12) return null
    val magic = readUInt32LE(data, 4)
    if (magic != 0x1F1903C1L) {
      // Plain JS bundle — release Hermes bridge will refuse to load it,
      // but iOS proceeds with a warning here; match that so a dev who
      // forgot to compile to HBC gets the SAME failure mode they'd see
      // on iOS.
      return null
    }
    val bcVersion = readUInt32LE(data, 8).toInt()
    val expectedBC = YaverSDKManifest.hermesBytecodeVersion
    if (expectedBC > 0 && bcVersion != expectedBC) {
      return BundleValidationError(
          "BC_VERSION_MISMATCH",
          "Hermes BC$bcVersion != expected BC$expectedBC")
    }
    return null
  }

  private fun isHexMD5(value: String): Boolean =
      value.length == 32 && value.all { it.isDigit() || it.lowercaseChar() in 'a'..'f' }

  private fun readUInt32LE(data: ByteArray, offset: Int): Long {
    val b0 = (data[offset].toLong() and 0xFF)
    val b1 = (data[offset + 1].toLong() and 0xFF) shl 8
    val b2 = (data[offset + 2].toLong() and 0xFF) shl 16
    val b3 = (data[offset + 3].toLong() and 0xFF) shl 24
    return b0 or b1 or b2 or b3
  }

  private fun md5Hex(data: ByteArray): String {
    val digest = MessageDigest.getInstance("MD5").digest(data)
    val sb = StringBuilder(digest.size * 2)
    for (b in digest) sb.append(String.format("%02x", b.toInt() and 0xFF))
    return sb.toString()
  }
}
