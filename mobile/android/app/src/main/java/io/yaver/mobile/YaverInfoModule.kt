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
}
