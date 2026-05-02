package io.yaver.mobile

// Accelerometer-based shake detector — Android counterpart to
// ShakeDetectingWindow's motionEnded handler in iOS AppDelegate.swift.
// Wired up by MainActivity's onResume / onPause to avoid burning battery
// when the app is backgrounded. Fires onShake when the linear-acceleration
// magnitude crosses SHAKE_THRESHOLD with at least MIN_INTERVAL_MS between
// triggers (so a single shake doesn't fire 3 times).

import android.content.Context
import android.hardware.Sensor
import android.hardware.SensorEvent
import android.hardware.SensorEventListener
import android.hardware.SensorManager
import kotlin.math.sqrt

class YaverShakeDetector(
    private val context: Context,
    private val onShake: () -> Unit,
) : SensorEventListener {

  companion object {
    /** Acceleration magnitude (m/s² minus gravity). 18 ≈ a vigorous flick. */
    private const val SHAKE_THRESHOLD = 18.0f
    /** Suppress repeats — one user gesture must produce one callback. */
    private const val MIN_INTERVAL_MS = 1500L
  }

  private val sensorManager: SensorManager? =
      context.getSystemService(Context.SENSOR_SERVICE) as? SensorManager
  private val accelerometer: Sensor? =
      sensorManager?.getDefaultSensor(Sensor.TYPE_LINEAR_ACCELERATION)
          ?: sensorManager?.getDefaultSensor(Sensor.TYPE_ACCELEROMETER)
  private var lastShakeMs: Long = 0L

  /** isLinearAccel decides whether to subtract gravity from each sample. */
  private val isLinearAccel: Boolean =
      accelerometer?.type == Sensor.TYPE_LINEAR_ACCELERATION

  fun start() {
    val sm = sensorManager ?: return
    val s = accelerometer ?: return
    sm.registerListener(this, s, SensorManager.SENSOR_DELAY_GAME)
  }

  fun stop() {
    sensorManager?.unregisterListener(this)
  }

  override fun onSensorChanged(event: SensorEvent) {
    val x = event.values[0]
    val y = event.values[1]
    val z = event.values[2]

    val mag = if (isLinearAccel) {
      sqrt((x * x + y * y + z * z).toDouble()).toFloat()
    } else {
      // Subtract gravity — at rest TYPE_ACCELEROMETER sums to ~9.8.
      val gx = x / SensorManager.GRAVITY_EARTH
      val gy = y / SensorManager.GRAVITY_EARTH
      val gz = z / SensorManager.GRAVITY_EARTH
      val gforce = sqrt((gx * gx + gy * gy + gz * gz).toDouble()).toFloat()
      // Convert back into m/s² above gravity.
      (gforce - 1f) * SensorManager.GRAVITY_EARTH
    }

    if (mag < SHAKE_THRESHOLD) return
    val now = System.currentTimeMillis()
    if (now - lastShakeMs < MIN_INTERVAL_MS) return
    lastShakeMs = now
    onShake()
  }

  override fun onAccuracyChanged(sensor: Sensor?, accuracy: Int) {}
}
