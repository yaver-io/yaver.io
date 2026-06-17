package io.yaver.mobile.sandbox

import android.content.Context
import android.content.Intent
import android.os.Build
import android.provider.Settings
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.WritableMap
import com.facebook.react.modules.core.DeviceEventManagerModule
import java.io.File

/**
 * YaverSandbox native module — JS control plane for the on-device agent.
 * JS side: mobile/src/lib/sandboxControl.ts.
 *
 *   start()                       → start the foreground SandboxService
 *   startHomeHost()               → start owner-only relay-only home hosting
 *   stop()                        → stop it
 *   status()                      → { running, rootfsInstalled, version, nativeLibDir, credHome }
 *   installRootfs(url,sha,version)→ download+verify+extract (emits YaverSandboxProgress)
 */
class SandboxModule(private val ctx: ReactApplicationContext) :
  ReactContextBaseJavaModule(ctx) {

  override fun getName(): String = "YaverSandbox"

  @ReactMethod
  fun start(promise: Promise) {
    try {
      val i = Intent(ctx, SandboxService::class.java).apply { action = SandboxService.ACTION_START }
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) ctx.startForegroundService(i) else ctx.startService(i)
      promise.resolve(true)
    } catch (e: Exception) {
      promise.reject("start_failed", e.message, e)
    }
  }

  @ReactMethod
  fun startHomeHost(promise: Promise) {
    try {
      val i = Intent(ctx, SandboxService::class.java).apply { action = SandboxService.ACTION_START_HOME_HOST }
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) ctx.startForegroundService(i) else ctx.startService(i)
      promise.resolve(true)
    } catch (e: Exception) {
      promise.reject("start_home_host_failed", e.message, e)
    }
  }

  @ReactMethod
  fun stop(promise: Promise) {
    try {
      val i = Intent(ctx, SandboxService::class.java).apply { action = SandboxService.ACTION_STOP }
      ctx.startService(i)
      promise.resolve(true)
    } catch (e: Exception) {
      promise.reject("stop_failed", e.message, e)
    }
  }

  @ReactMethod
  fun openFactoryResetSettings(promise: Promise) {
    try {
      val i = Intent(Settings.ACTION_PRIVACY_SETTINGS).apply {
        addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
      }
      try {
        ctx.startActivity(i)
      } catch (_: Exception) {
        ctx.startActivity(Intent(Settings.ACTION_SETTINGS).apply {
          addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        })
      }
      promise.resolve(true)
    } catch (e: Exception) {
      promise.reject("open_reset_settings_failed", e.message, e)
    }
  }

  @ReactMethod
  fun status(promise: Promise) {
    val m: WritableMap = Arguments.createMap()
    m.putBoolean("running", SandboxService.running)
    m.putBoolean("rootfsInstalled", RootfsInstaller.isInstalled(ctx))
    m.putString("version", RootfsInstaller.installedVersion(ctx))
    m.putString("nativeLibDir", SandboxService.nativeLibDir(ctx))
    m.putString("credHome", SandboxService.credHomeDir(ctx).absolutePath)
    m.putBoolean("prootPresent", File(SandboxService.nativeLibDir(ctx), "libproot.so").exists())
    m.putBoolean("agentPresent", File(SandboxService.nativeLibDir(ctx), "libyaver.so").exists())
    m.putBoolean("homeHostMode", SandboxService.homeHostMode)
    m.putBoolean("relayOnly", SandboxService.homeHostMode)
    val battery = SandboxService.batteryStatus(ctx)
    m.putInt("batteryPercent", battery.first)
    m.putBoolean("charging", battery.second)
    promise.resolve(m)
  }

  @ReactMethod
  fun installRootfs(url: String, sha256: String, version: String, force: Boolean, promise: Promise) {
    Thread {
      val ok = RootfsInstaller.install(ctx, url, sha256, version, force) { p ->
        val ev: WritableMap = Arguments.createMap()
        ev.putString("phase", p.phase)
        ev.putDouble("bytes", p.bytes.toDouble())
        ev.putDouble("total", p.total.toDouble())
        emit("YaverSandboxProgress", ev)
      }
      if (ok) promise.resolve(true) else promise.reject("install_failed", "rootfs install failed (see logcat $logTag)")
    }.apply { isDaemon = true }.start()
  }

  // Renamed from `name` — a `val name` synthesizes getName(), which clashes
  // with the required `override fun getName()` (JVM signature collision) and
  // fails compileReleaseKotlin on newer Kotlin.
  private val logTag get() = "YaverSandbox"

  private fun emit(event: String, payload: WritableMap) {
    if (!ctx.hasActiveReactInstance()) return
    ctx.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java).emit(event, payload)
  }

  // RN requires these when you emit events, to avoid "no listeners registered" warns.
  @ReactMethod fun addListener(eventName: String) {}
  @ReactMethod fun removeListeners(count: Int) {}
}
