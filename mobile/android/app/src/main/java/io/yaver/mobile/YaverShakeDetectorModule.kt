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
  // Debug sampling: log peak delta every N samples so we can confirm
  // (a) the sensor is firing at all, (b) what the actual g-force
  // deltas look like during a user shake on the target device. The
  // Tab S7 FE's accelerometer noise floor + ergonomic-shake max may
  // differ from the iPad/Pixel norms the threshold was tuned for.
  private var sampleCount = 0
  private var peakDeltaSinceLastLog = 0.0

  init {
    // EVERY new instance must take over. The previous logic
    // ("only first instance takes over") meant that after
    // MainActivity.recreate() — which happens on every Hermes-push
    // load/unload — a NEW YaverShakeDetectorModule got created but
    // skipped startListening() because keepAliveOwner was still the
    // OLD instance. That old instance's ReactApplicationContext is
    // dead post-recreate, so its emitShake() short-circuits at
    // ctx.hasActiveReactInstance() and the new JS subscription
    // never sees the event. RN's host-pause path also unregisters
    // the OLD listener during the recreate (logcat shows
    // `D/SensorManager: unregisterListener` at activity onPause)
    // and the old instance no longer re-registers, so by the time
    // the new bridge is up, NO listener exists. Stop whatever the
    // previous owner had registered, install ourselves on the
    // new ctx, and re-register against SensorManager fresh.
    keepAliveOwner?.let { old ->
      if (old !== this) old.stopListening()
    }
    keepAliveOwner = this
    startListening()
  }

  @ReactMethod
  fun startObserving() {
    if (keepAliveOwner == this || keepAliveOwner == null) {
      keepAliveOwner = this
      startListening()
    } else {
      keepAliveOwner?.startListening()
    }
  }

  @ReactMethod
  fun stopObserving() {
    if (keepAliveOwner == this) {
      stopListening()
      keepAliveOwner = null
    } else {
      keepAliveOwner?.stopListening()
    }
  }

  // RN NativeEventEmitter contract — required even if we don't track counts.
  @ReactMethod fun addListener(eventName: String) {}
  @ReactMethod fun removeListeners(count: Int) {}

  override fun onCatalystInstanceDestroy() {
    super.onCatalystInstanceDestroy()
    // Intentionally keep the process-wide detector alive if this module
    // owns it. Bridge swaps to a guest bundle invalidate the host React
    // context, but we still need native shake handling while that guest
    // is running so Android can jump back into Yaver and resume feedback.
    if (keepAliveOwner != this) {
      stopListening()
    }
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
    if (delta > peakDeltaSinceLastLog) peakDeltaSinceLastLog = delta
    sampleCount++
    if (sampleCount % SAMPLES_PER_LOG == 0) {
      Log.i(TAG, String.format("sensor alive — samples=%d peakDelta=%.3f threshold=%.3f", sampleCount, peakDeltaSinceLastLog, SHAKE_DELTA_THRESHOLD))
      peakDeltaSinceLastLog = 0.0
    }
    if (delta > SHAKE_DELTA_THRESHOLD) {
      val now = System.currentTimeMillis()
      if (now - lastShakeAt > SHAKE_COOLDOWN_MS) {
        lastShakeAt = now
        Log.i(TAG, String.format("SHAKE fired — delta=%.3f", delta))
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
    // emitting the JS event would just dead-letter. Preserve the
    // guest-shake intent in SharedPreferences, then unload the guest
    // and recreate back into Yaver's own bundle. On startup the host
    // JS consumes that flag via YaverInfo.consumePendingFeedbackLaunch()
    // and re-opens feedback as `native-guest-shake`, which preserves
    // the unconditional launch path and guest-aware `/vibing/execute`
    // submission flow.
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
    val guestLoaded = prefs.getBoolean(YaverNativePrefs.GUEST_BUNDLE_LOADED, false) ||
        YaverBundleLoaderModule.savedBundleFile(ctx).exists()
    if (guestLoaded) {
      Log.i(TAG, "shake during guest bundle — marking pending feedback, unloading guest, recreating")
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
          .putBoolean(YaverNativePrefs.PENDING_FEEDBACK_LAUNCH, true)
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
    private var keepAliveOwner: YaverShakeDetectorModule? = null
    // Tuned to Tab S7 FE accelerometer ergonomics — captured peak delta
    // during a deliberate user shake was ~0.758 (1.18.107 logcat 02:46:58).
    // 0.6 catches that and gives a small margin while staying well above
    // the casual-handling floor (~0.34 for picking the tablet up). If
    // false positives appear, raise back to 0.7.
    private const val SHAKE_DELTA_THRESHOLD = 0.6
    private const val SHAKE_COOLDOWN_MS = 2500L
    // Log a heartbeat every ~6s assuming SENSOR_DELAY_UI delivers ~60ms
    // samples (~100 samples ≈ 6s). Lets us confirm the listener is alive
    // without flooding logcat.
    private const val SAMPLES_PER_LOG = 100
  }
}
