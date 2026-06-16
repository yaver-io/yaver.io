package io.yaver.wear

import android.app.Activity
import android.content.Intent
import android.speech.RecognizerIntent
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts

/**
 * On-watch speech-to-text → a transcript string.
 *
 * Uses the system [RecognizerIntent.ACTION_RECOGNIZE_SPEECH] — Wear OS routes
 * this to the platform dictation UI (the same one keyboards use), which is the
 * battery-friendly, no-extra-permission path. NOTE per the design (§8): on-watch
 * STT is weaker than the phone's; in the DEFAULT phone-paired mode you may
 * instead forward raw audio and let the phone transcribe with its better models.
 * This helper covers the standalone case and the simple "dictate then send"
 * phone-paired case.
 *
 * Wired in MainActivity via the Activity Result API: register [contract] +
 * [parseResult], launch with [launchIntent], get the best transcript back.
 */
object Dictation {

    /** The contract to register in MainActivity (StartActivityForResult). */
    val contract = ActivityResultContracts.StartActivityForResult()

    /** Build the dictation intent. `prompt` is the on-screen hint. */
    fun launchIntent(prompt: String = "Speak"): Intent =
        Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
            putExtra(
                RecognizerIntent.EXTRA_LANGUAGE_MODEL,
                RecognizerIntent.LANGUAGE_MODEL_FREE_FORM,
            )
            // Let the system pick the user's language; do not hardcode a locale.
            putExtra(RecognizerIntent.EXTRA_PROMPT, prompt)
            putExtra(RecognizerIntent.EXTRA_MAX_RESULTS, 1)
        }

    /**
     * Extract the best transcript from a dictation result, or null if the user
     * cancelled / nothing was heard. Never throws.
     */
    fun parseResult(resultCode: Int, data: Intent?): String? {
        if (resultCode != Activity.RESULT_OK || data == null) return null
        val results = data.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)
        return results?.firstOrNull()?.trim()?.takeIf { it.isNotEmpty() }
    }

    /**
     * Convenience launcher: in MainActivity, register an
     * `ActivityResultLauncher<Intent>` with [contract], then call this to start
     * dictation with a prompt.
     */
    fun launch(launcher: ActivityResultLauncher<Intent>, prompt: String = "Speak") {
        launcher.launch(launchIntent(prompt))
    }
}
