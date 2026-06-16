package io.yaver.wear

import android.content.Context
import android.os.VibrationEffect
import android.os.Vibrator

/**
 * Wrist haptics — the watch's primary "ambient" output channel. A summary that
 * the user never reads is still felt as a success or failure tap.
 *
 * Three cues only, mapped from reply kinds in [WatchState.hapticFor]:
 *   - CLICK   : acknowledgement / state change ("On it", "Working…", confirm-needed)
 *   - SUCCESS : a task completed OK ("Done. Tests pass.")
 *   - FAILURE : a task failed / we couldn't reach the box
 *
 * Kept patterns short and distinct so they're distinguishable by feel alone.
 */
class Haptics(context: Context) {

    enum class Cue { CLICK, SUCCESS, FAILURE }

    private val vibrator: Vibrator? =
        context.getSystemService(Context.VIBRATOR_SERVICE) as? Vibrator

    fun fire(cue: Cue) {
        val v = vibrator ?: return
        if (!v.hasVibrator()) return
        v.vibrate(effectFor(cue))
    }

    fun click() = fire(Cue.CLICK)
    fun success() = fire(Cue.SUCCESS)
    fun failure() = fire(Cue.FAILURE)

    companion object {
        fun effectFor(cue: Cue): VibrationEffect = when (cue) {
            // single short tap
            Cue.CLICK -> VibrationEffect.createOneShot(40, VibrationEffect.DEFAULT_AMPLITUDE)
            // two quick taps = "good"
            Cue.SUCCESS -> VibrationEffect.createWaveform(longArrayOf(0, 30, 60, 30), -1)
            // two long buzzes = "bad"
            Cue.FAILURE -> VibrationEffect.createWaveform(longArrayOf(0, 120, 80, 120), -1)
        }
    }
}
