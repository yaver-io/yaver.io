package io.yaver.mobile

// Android counterpart of iOS `ShakeDetectingWindow` in
// mobile/ios/Yaver/AppDelegate.swift:71. iOS's UIWindow subclass
// receives motion events automatically; Android has no equivalent
// at the View level, so we register a SensorManager accelerometer
// listener at module-init and emit a `YaverShakeDetected` event
// when the gesture is recognised.
//
// JS contract: `feedbackTrigger.ts::startFeedbackShakeBridge`
// already runs an `expo-sensors` Accelerometer path, but its
// resulting launch goes through `maybeLaunchFeedbackFromShake` with
// `source = "shake"`, which the JS gates on the user's
// `cfg.trigger === "shake"` setting (default is "floating-button",
// so the gate rejects every shake silently). The
// `native-guest-shake` source is documented as unconditional
// (mobile/src/lib/feedbackTrigger.ts:47-58). This module emits
// `YaverShakeDetected` and the JS subscription fires
// `triggerFeedbackLaunch("native-guest-shake")` so shake always
// works on Android — matching iOS where the native
// ShakeDetectingWindow path bypasses the same gate.
//
// Threshold + cooldown tuned to match the existing JS path
// (`delta > 1.45`, 2.5s cooldown). High-threshold + cooldown avoids
// false positives from scrolling, walking, or rotating the device.

import android.content.Context
import android.content.Intent
import android.hardware.Sensor
import android.hardware.SensorEvent
import android.hardware.SensorEventListener
import android.hardware.SensorManager
import android.util.Log
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.modules.core.DeviceEventManagerModule
import java.io.File
import kotlin.math.sqrt

class YaverShakeDetectorModule(private val ctx: ReactApplicationContext) :
    ReactContextBaseJavaModule(ctx), SensorEventListener {

  override fun getName(): String = NAME

  private val sensorManager: SensorManager? =
      ctx.getSystemService(Context.SENSOR_SERVICE) as? SensorManager
  private val accelerometer: Sensor? =
      sensorManager?.getDefaultSensor(Sensor.TYPE_ACCELEROMETER)

  private var lastShakeAt = 0L
  private var lastMagnitude = 0.0
  private var listening = false

  init {
    // Start listening immediately on construction so the user gets
    // shake-to-feedback without first opening any tab. iOS's
    // ShakeDetectingWindow is installed at AppDelegate launch with
    // the same intent.
    startListening()
  }

  @ReactMethod
  fun startObserving() {
    startListening()
  }

  @ReactMethod
  fun stopObserving() {
    stopListening()
  }

  // RN NativeEventEmitter contract — required even if we don't track counts.
  @ReactMethod fun addListener(eventName: String) {}
  @ReactMethod fun removeListeners(count: Int) {}

  override fun onCatalystInstanceDestroy() {
    super.onCatalystInstanceDestroy()
    stopListening()
  }

  private fun startListening() {
    if (listening) return
    val sm = sensorManager ?: return
    val sensor = accelerometer ?: run {
      Log.w(TAG, "no accelerometer available on this device")
      return
    }
    // SENSOR_DELAY_UI ≈ 60ms intervals — fast enough to catch a
    // shake within a few samples, slow enough not to wake the CPU
    // unnecessarily. Matches the JS path's setUpdateInterval(220ms)
    // closely enough that combined detection doesn't double-fire
    // (cooldown handles overlaps).
    val ok = sm.registerListener(this, sensor, SensorManager.SENSOR_DELAY_UI)
    if (ok) {
      listening = true
      Log.i(TAG, "shake detector listening on accelerometer")
    }
  }

  private fun stopListening() {
    if (!listening) return
    sensorManager?.unregisterListener(this)
    listening = false
  }

  override fun onSensorChanged(event: SensorEvent) {
    if (event.sensor.type != Sensor.TYPE_ACCELEROMETER) return
    val x = event.values[0] / SensorManager.GRAVITY_EARTH
    val y = event.values[1] / SensorManager.GRAVITY_EARTH
    val z = event.values[2] / SensorManager.GRAVITY_EARTH
    val magnitude = sqrt(x * x + y * y + z * z).toDouble()
    val delta = kotlin.math.abs(magnitude - lastMagnitude)
    lastMagnitude = magnitude
    if (delta > SHAKE_DELTA_THRESHOLD) {
      val now = System.currentTimeMillis()
      if (now - lastShakeAt > SHAKE_COOLDOWN_MS) {
        lastShakeAt = now
        emitShake()
      }
    }
  }

  override fun onAccuracyChanged(sensor: Sensor?, accuracy: Int) {
    // Don't care — accelerometer accuracy doesn't affect shake detection.
  }

  private fun emitShake() {
    // When a guest bundle is loaded, the React JS running is the
    // GUEST's bundle (e.g. todo-rn) — Yaver's feedback overlay JS
    // never gets a chance to subscribe to `YaverShakeDetected`, so
    // emitting the JS event would just dead-letter. iOS handles
    // this at the UIWindow level so the guest never even sees the
    // motion. Mirror that: unload the guest natively + recreate
    // the activity. After recreate, getJSBundleFile() returns null
    // (we deleted the saved bundle) so RN boots back into Yaver's
    // own embedded bundle.
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
    val guestLoaded = prefs.getBoolean(YaverNativePrefs.GUEST_BUNDLE_LOADED, false) ||
        YaverBundleLoaderModule.savedBundleFile(ctx).exists()
    if (guestLoaded) {
      Log.i(TAG, "shake during guest bundle — unloading guest and recreating")
      unloadGuestAndRecreate(prefs)
      return
    }
    // No guest active — emit JS event so Yaver's feedback bridge
    // can open the overlay. JS subscription lives in
    // mobile/src/lib/feedbackTrigger.ts.
    if (!ctx.hasActiveReactInstance()) return
    Log.i(TAG, "shake detected — emitting YaverShakeDetected")
    val payload = Arguments.createMap().apply {
      putDouble("at", System.currentTimeMillis().toDouble())
    }
    ctx.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
        .emit("YaverShakeDetected", payload)
  }

  /** Native-only guest unload — independent of which JS bundle is
   *  currently running. Mirrors YaverBundleLoaderModule.unloadBundle
   *  but doesn't go through the Promise/JS contract since the guest
   *  JS may not have any module subscribed to that path. */
  private fun unloadGuestAndRecreate(prefs: android.content.SharedPreferences) {
    try {
      val dir = File(ctx.filesDir, "bundles")
      File(dir, YaverBundleLoaderModule.BUNDLE_FILENAME).delete()
      File(dir, "metadata.json").delete()
      prefs.edit()
          .remove(YaverNativePrefs.LOADED_MODULE_NAME)
          .remove(YaverNativePrefs.LOADED_BUNDLE_MD5)
          .remove(YaverNativePrefs.SELECTED_RUNTIME_FAMILY_ID)
          .remove(YaverNativePrefs.SELECTED_RUNTIME_FAMILY_LABEL)
          .putBoolean(YaverNativePrefs.GUEST_BUNDLE_LOADED, false)
          .apply()
      val intent = Intent(YaverBundleLoaderModule.ACTION_RELOAD).apply {
        setPackage(ctx.packageName)
      }
      ctx.sendBroadcast(intent)
      Log.i(TAG, "guest unloaded + reload broadcast sent")
    } catch (t: Throwable) {
      Log.e(TAG, "failed to unload guest from shake handler", t)
    }
  }

  companion object {
    const val NAME = "YaverShakeDetector"
    private const val TAG = "YaverShakeDetector"
    // Same threshold as feedbackTrigger.ts:102 — keeps detection
    // ergonomics identical regardless of which path fires.
    private const val SHAKE_DELTA_THRESHOLD = 1.45
    private const val SHAKE_COOLDOWN_MS = 2500L
  }
}
