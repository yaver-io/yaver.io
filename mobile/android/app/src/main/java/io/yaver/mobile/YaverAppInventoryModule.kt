package io.yaver.mobile

import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.os.Build
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.WritableArray
import com.facebook.react.bridge.WritableMap

class YaverAppInventoryModule(private val ctx: ReactApplicationContext) :
    ReactContextBaseJavaModule(ctx) {

  override fun getName(): String = "YaverAppInventory"

  @ReactMethod
  fun listLaunchableApps(promise: Promise) {
    try {
      val pm = ctx.packageManager
      val main = Intent(Intent.ACTION_MAIN, null).addCategory(Intent.CATEGORY_LAUNCHER)
      val resolved = if (Build.VERSION.SDK_INT >= 33) {
        pm.queryIntentActivities(main, PackageManager.ResolveInfoFlags.of(0))
      } else {
        @Suppress("DEPRECATION")
        pm.queryIntentActivities(main, 0)
      }

      val seen = linkedMapOf<String, WritableMap>()
      for (ri in resolved) {
        val ai = ri.activityInfo?.applicationInfo ?: continue
        val pkg = ai.packageName ?: continue
        if (seen.containsKey(pkg)) continue
        seen[pkg] = appMap(pm, ai, ri.activityInfo?.name ?: "")
      }

      val out: WritableArray = Arguments.createArray()
      seen.values
          .sortedBy { it.getString("label")?.lowercase() ?: "" }
          .forEach { out.pushMap(it) }
      promise.resolve(out)
    } catch (e: Exception) {
      promise.reject("app_inventory_failed", e.message, e)
    }
  }

  @ReactMethod
  fun getPackageInfo(packageName: String, promise: Promise) {
    try {
      val pm = ctx.packageManager
      val pkg = packageName.trim()
      if (pkg.isEmpty()) {
        promise.reject("bad_package", "packageName is required")
        return
      }
      val app = if (Build.VERSION.SDK_INT >= 33) {
        pm.getApplicationInfo(pkg, PackageManager.ApplicationInfoFlags.of(0))
      } else {
        @Suppress("DEPRECATION")
        pm.getApplicationInfo(pkg, 0)
      }
      promise.resolve(appMap(pm, app, ""))
    } catch (e: Exception) {
      promise.reject("package_not_visible", e.message, e)
    }
  }

  private fun appMap(pm: PackageManager, ai: ApplicationInfo, activityName: String): WritableMap {
    val pkgInfo = try {
      if (Build.VERSION.SDK_INT >= 33) {
        pm.getPackageInfo(ai.packageName, PackageManager.PackageInfoFlags.of(PackageManager.GET_PERMISSIONS.toLong()))
      } else {
        @Suppress("DEPRECATION")
        pm.getPackageInfo(ai.packageName, PackageManager.GET_PERMISSIONS)
      }
    } catch (_: Exception) {
      null
    }
    val m = Arguments.createMap()
    m.putString("packageName", ai.packageName)
    m.putString("label", pm.getApplicationLabel(ai).toString())
    m.putString("activityName", activityName)
    m.putString("versionName", pkgInfo?.versionName ?: "")
    if (Build.VERSION.SDK_INT >= 28) {
      m.putDouble("versionCode", (pkgInfo?.longVersionCode ?: 0L).toDouble())
    } else {
      @Suppress("DEPRECATION")
      m.putDouble("versionCode", (pkgInfo?.versionCode ?: 0).toDouble())
    }
    m.putBoolean("system", (ai.flags and ApplicationInfo.FLAG_SYSTEM) != 0)
    m.putBoolean("launchable", activityName.isNotEmpty())
    val perms = Arguments.createArray()
    pkgInfo?.requestedPermissions?.forEach { perms.pushString(it) }
    m.putArray("requestedPermissions", perms)
    return m
  }
}
