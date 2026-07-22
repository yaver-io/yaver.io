package io.yaver.feedback

import android.content.Context
import android.hardware.Sensor
import android.hardware.SensorEvent
import android.hardware.SensorEventListener
import android.hardware.SensorManager
import kotlin.math.sqrt

/**
 * Accelerometer shake detection.
 *
 * ─── Why the thresholds are what they are ───────────────────────────────────
 *
 * A shake detector that fires too easily is worse than none: it interrupts the
 * user mid-task with a feedback overlay they did not ask for, and they learn to
 * distrust the app. So this is deliberately conservative.
 *
 *   - 2.7g rejects ordinary handling (walking, pulling the phone from a pocket,
 *     setting it down). A real "I am shaking this" gesture clears 3g easily.
 *   - THREE crossings within the window, not one. A single spike is a bump; a
 *     shake is oscillation.
 *   - 1s cooldown after firing, so one gesture cannot produce three reports.
 *
 * Runs on the sensor thread. Everything it touches is either atomic or handed
 * straight to the callback, which is expected to hop to the main thread itself.
 */
internal class ShakeDetector(
    private val context: Context,
    private val thresholdG: Float,
    private val onShake: () -> Unit,
) : SensorEventListener {

    private companion object {
        /** Crossings needed before we believe it. */
        const val REQUIRED_CROSSINGS = 3
        /** Crossings must fall inside this window to count as one gesture. */
        const val WINDOW_MS = 1_000L
        /** Ignore everything for this long after firing. */
        const val COOLDOWN_MS = 1_000L
        const val GRAVITY = SensorManager.GRAVITY_EARTH
    }

    private var sensorManager: SensorManager? = null
    private var crossings = 0
    private var firstCrossingAt = 0L
    private var lastFiredAt = 0L

    fun start() {
        val sm = context.getSystemService(Context.SENSOR_SERVICE) as? SensorManager ?: return
        val accel = sm.getDefaultSensor(Sensor.TYPE_ACCELEROMETER)
        if (accel == null) {
            // No accelerometer (emulator without sensors, some tablets). Not an
            // error: the app can still call YaverFeedback.open() directly, and
            // a streamed session triggers via the viewer anyway.
            return
        }
        sensorManager = sm
        sm.registerListener(this, accel, SensorManager.SENSOR_DELAY_UI)
    }

    fun stop() {
        sensorManager?.unregisterListener(this)
        sensorManager = null
        crossings = 0
    }

    override fun onSensorChanged(event: SensorEvent) {
        if (event.sensor.type != Sensor.TYPE_ACCELEROMETER) return
        val now = System.currentTimeMillis()
        if (now - lastFiredAt < COOLDOWN_MS) return

        val (x, y, z) = Triple(event.values[0], event.values[1], event.values[2])
        // Magnitude in g, gravity removed. At rest this sits near 0.
        val gForce = sqrt(x * x + y * y + z * z) / GRAVITY - 1.0f

        if (gForce < thresholdG) return

        // Reset the window if the previous crossings are stale — otherwise two
        // unrelated bumps minutes apart would eventually add up to a "shake".
        if (crossings == 0 || now - firstCrossingAt > WINDOW_MS) {
            crossings = 1
            firstCrossingAt = now
            return
        }
        crossings++
        if (crossings >= REQUIRED_CROSSINGS) {
            crossings = 0
            lastFiredAt = now
            onShake()
        }
    }

    override fun onAccuracyChanged(sensor: Sensor?, accuracy: Int) {
        // Accuracy changes do not affect a threshold this coarse.
    }
}
