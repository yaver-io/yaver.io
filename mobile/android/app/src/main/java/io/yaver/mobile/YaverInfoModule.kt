package io.yaver.mobile

// YaverInfoModule — Android counterpart to mobile/ios/Yaver/YaverInfo.swift.
//
// Exposes:
//   - constants: isYaver, version, build, sdkVersion, guestSafeMode,
//     and the inheritedAuthToken / inheritedAgentUrl / inheritedDeviceId
//     a guest bundle's bundled feedback SDK reads to skip its own login
//     and inherit Yaver's session.
//   - setInheritedAuth(token, agentUrl, deviceId) — host JS calls this on
//     sign-in; we persist into SharedPreferences. Constants are recomputed
//     at every bundle load, so guest reload picks up fresh values.
//   - clearInheritedAuth — wipe on logout.
//
// Same JS surface as iOS so mobile/src/lib/auth.ts's saveToken /
// clearToken paths just work cross-platform via NativeModules.YaverInfo.

import android.content.Context
import android.content.SharedPreferences
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod

class YaverInfoModule(private val ctx: ReactApplicationContext) :
    ReactContextBaseJavaModule(ctx) {

  override fun getName(): String = "YaverInfo"

  private fun prefs(): SharedPreferences =
      ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)

  override fun getConstants(): Map<String, Any> {
    val p = prefs()
    val pkg = ctx.packageManager.getPackageInfo(ctx.packageName, 0)
    return mapOf(
        "isYaver" to true,
        "version" to (pkg.versionName ?: ""),
        @Suppress("DEPRECATION")
        "build" to pkg.versionCode.toString(),
        "guestSafeMode" to true,
        "suppressPushNotifications" to true,
        "suppressLocalizationProbe" to true,
        "inheritedAuthToken" to (p.getString(YaverNativePrefs.INHERITED_AUTH_TOKEN, "") ?: ""),
        "inheritedAgentUrl" to (p.getString(YaverNativePrefs.AGENT_BASE_URL, "") ?: ""),
        "inheritedDeviceId" to (p.getString(YaverNativePrefs.INHERITED_DEVICE_ID, "") ?: ""),
    )
  }

  @ReactMethod
  fun setInheritedAuth(token: String, agentUrl: String, deviceId: String) {
    val edit = prefs().edit()
    if (token.isNotEmpty()) edit.putString(YaverNativePrefs.INHERITED_AUTH_TOKEN, token)
    if (agentUrl.isNotEmpty()) edit.putString(YaverNativePrefs.AGENT_BASE_URL, agentUrl)
    if (deviceId.isNotEmpty()) edit.putString(YaverNativePrefs.INHERITED_DEVICE_ID, deviceId)
    edit.apply()
  }

  @ReactMethod
  fun clearInheritedAuth() {
    prefs().edit()
        .remove(YaverNativePrefs.INHERITED_AUTH_TOKEN)
        .remove(YaverNativePrefs.INHERITED_DEVICE_ID)
        .apply()
  }

  /// Mirror of mobile/ios/Yaver/YaverInfo.swift's setInheritedPrimaryRunner.
  /// DeviceContext (host JS) calls this whenever the active device's
  /// primary runner / model changes. The native feedback pane reads the
  /// stored values when constructing its POST /tasks payload so the
  /// feedback drawer routes to the same runner the user picked in the
  /// Tasks tab. Empty values clear (e.g. the user removed their pick).
  @ReactMethod
  fun setInheritedPrimaryRunner(runner: String, model: String) {
    val edit = prefs().edit()
    val r = runner.trim()
    val m = model.trim()
    if (r.isEmpty()) edit.remove(YaverNativePrefs.PREFERRED_RUNNER)
    else edit.putString(YaverNativePrefs.PREFERRED_RUNNER, r)
    if (m.isEmpty()) edit.remove(YaverNativePrefs.PREFERRED_MODEL)
    else edit.putString(YaverNativePrefs.PREFERRED_MODEL, m)
    edit.apply()
  }

  /// Mirror of mobile/ios/Yaver/YaverInfo.swift's setInheritedRelayPassword.
  /// Required so the Android feedback pane can attach X-Relay-Password
  /// to its POST /tasks request. Without this header, relay-routed
  /// agents reject with "invalid relay password" / 401.
  @ReactMethod
  fun setInheritedRelayPassword(password: String) {
    val edit = prefs().edit()
    val p = password.trim()
    if (p.isEmpty()) edit.remove(YaverNativePrefs.RELAY_PASSWORD)
    else edit.putString(YaverNativePrefs.RELAY_PASSWORD, p)
    edit.apply()
  }

  /// Mirror of mobile/ios/Yaver/YaverInfo.swift's setInheritedGuestProject.
  /// Lets the host JS push the active Hot-Reload project's name + path
  /// so the feedback pane can prepend a project banner to the user's
  /// prompt — same as iOS — letting the AI on the remote know which
  /// app the feedback applies to.
  @ReactMethod
  fun setInheritedGuestProject(name: String, path: String) {
    val edit = prefs().edit()
    val n = name.trim()
    val p = path.trim()
    if (n.isEmpty()) edit.remove(YaverNativePrefs.GUEST_PROJECT_NAME)
    else edit.putString(YaverNativePrefs.GUEST_PROJECT_NAME, n)
    if (p.isEmpty()) edit.remove(YaverNativePrefs.GUEST_PROJECT_PATH)
    else edit.putString(YaverNativePrefs.GUEST_PROJECT_PATH, p)
    edit.apply()
  }
}

/** SharedPreferences keys shared across the Android native panes — must
 *  stay in sync with the iOS UserDefaults keys (YaverInfo.swift,
 *  YaverFeedbackPane.swift, YaverAgentsPane.swift) so the same JS surface
 *  drives both platforms identically. */
object YaverNativePrefs {
  const val NAME = "yaver_native_prefs"
  const val INHERITED_AUTH_TOKEN = "yaverInheritedAuthToken"
  const val AGENT_BASE_URL = "yaverAgentBaseURL"
  const val AGENT_AUTH = "yaverAgentAuth"
  const val INHERITED_DEVICE_ID = "yaverInheritedDeviceId"
  const val GUEST_BUNDLE_LOADED = "yaverGuestAppRunning"
  // Preferred runner + model pushed by DeviceContext (Convex source of
  // truth: userSettings.primaryRunnerByDevice). Read by the feedback
  // pane so its POST /tasks routes to the same runner the user picked
  // in the Tasks tab. iOS counterparts: yaverPreferredRunner /
  // yaverPreferredModel UserDefaults keys.
  const val PREFERRED_RUNNER = "yaverPreferredRunner"
  const val PREFERRED_MODEL = "yaverPreferredModel"
  // Relay password for X-Relay-Password header on relay-routed agent
  // requests. Without this, the feedback POST fails 401 / "invalid
  // relay password" on relay-tunnelled agents.
  const val RELAY_PASSWORD = "yaverInheritedRelayPassword"
  // Active Hot-Reload project name + path. Prepended as a banner to
  // the prompt so the AI on the remote knows which app the user's
  // feedback applies to.
  const val GUEST_PROJECT_NAME = "yaverInheritedGuestProjectName"
  const val GUEST_PROJECT_PATH = "yaverInheritedGuestProjectPath"
}
