package io.yaver.wear.ui

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.ActivityResultLauncher
import android.content.Intent
import androidx.lifecycle.lifecycleScope
import io.yaver.wear.Dictation
import io.yaver.wear.Haptics
import io.yaver.wear.PhoneBridge
import io.yaver.wear.WatchProtocol
import io.yaver.wear.WatchState
import kotlinx.coroutines.launch

/**
 * The single Compose-hosting activity.
 *
 * Flow it drives:
 *   raise / tap record → system dictation → transcript →
 *   send over the DEFAULT phone-paired transport (PhoneBridge) →
 *   show a brief "On it" + haptic → async working → wake on summary (the
 *   ReplyListenerService delivers replies into WatchState even when this UI is
 *   backgrounded; while foregrounded the Compose tree just collects them).
 *
 * The watch NEVER blocks on the remote task. Dictation returns, we fire-and-
 * forget the turn, and the wrist is immediately interactive again.
 *
 * Transport policy: phone-paired first. If no phone node is reachable, we flag
 * standalone in WatchState (the UI can then surface the sign-in / box path). The
 * standalone HTTP client (AgentClient) + device-code auth (Backend) are wired
 * through SignInScreen; this activity keeps the happy path (phone present) lean.
 */
class MainActivity : ComponentActivity() {

    private lateinit var phoneBridge: PhoneBridge
    private lateinit var haptics: Haptics
    private lateinit var dictationLauncher: ActivityResultLauncher<Intent>

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        phoneBridge = PhoneBridge(applicationContext)
        haptics = Haptics(applicationContext)

        // Register the dictation result callback. On a good transcript we send a
        // turn; on cancel we drop back to idle.
        dictationLauncher = registerForActivityResult(Dictation.contract) { result ->
            val transcript = Dictation.parseResult(result.resultCode, result.data)
            if (transcript == null) {
                WatchState.setPhase(WatchState.Phase.Idle)
                WatchState.setLine("Didn't catch that")
                return@registerForActivityResult
            }
            onTranscript(transcript)
        }

        setContent {
            WearApp(
                onRecord = { startDictation() },
                onConfirm = { token -> onConfirm(token, WatchProtocol.ConfirmReply.CONFIRM) },
                onCancel = { token -> onConfirm(token, WatchProtocol.ConfirmReply.CANCEL) },
                onIntent = { intent -> onIntent(intent) },
            )
        }

        // Probe phone reachability once so the UI can hint standalone if needed.
        // Cheap, non-blocking — the record button works regardless.
        lifecycleScope.launch {
            val reachable = phoneBridge.isPhoneReachable()
            WatchState.setPhoneReachable(reachable)
        }
    }

    /** Tap / raise → launch system dictation. */
    private fun startDictation() {
        haptics.click()
        WatchState.listening()
        Dictation.launch(dictationLauncher, prompt = "What should Yaver do?")
    }

    /** Got a transcript → echo it, fire-and-forget to the phone, stay snappy. */
    private fun onTranscript(transcript: String) {
        WatchState.sending(transcript)
        haptics.click()
        lifecycleScope.launch {
            try {
                phoneBridge.sendTranscript(transcript)
                // The actual reply (ack/working/summary) arrives async via
                // ReplyListenerService → WatchState. Nothing to await here.
            } catch (_: PhoneBridge.PhoneUnreachableException) {
                // No phone. Surface standalone mode rather than blocking.
                WatchState.setPhoneReachable(false)
                WatchState.setPhase(WatchState.Phase.Idle)
                WatchState.setLine("Phone not reachable")
                haptics.failure()
            } catch (_: Throwable) {
                WatchState.setPhase(WatchState.Phase.Idle)
                WatchState.setLine("Couldn't send")
                haptics.failure()
            }
        }
    }

    /** Confirm / cancel a confirm-needed prompt. */
    private fun onConfirm(token: String, reply: WatchProtocol.ConfirmReply) {
        haptics.click()
        WatchState.setPhase(WatchState.Phase.Idle)
        WatchState.setLine(
            if (reply == WatchProtocol.ConfirmReply.CONFIRM) "Confirmed" else "Cancelled"
        )
        lifecycleScope.launch {
            try {
                phoneBridge.sendConfirm(token, reply)
            } catch (_: Throwable) {
                WatchState.setLine("Couldn't send")
                haptics.failure()
            }
        }
    }

    /** A fixed one-tap intent (run-tests / deploy / status). */
    private fun onIntent(intent: WatchProtocol.FixedIntent) {
        haptics.click()
        WatchState.setPhase(WatchState.Phase.Sending)
        WatchState.setLine(intent.wire.replace('-', ' '))
        lifecycleScope.launch {
            try {
                phoneBridge.sendIntent(intent)
            } catch (_: Throwable) {
                WatchState.setPhase(WatchState.Phase.Idle)
                WatchState.setLine("Phone not reachable")
                haptics.failure()
            }
        }
    }
}
