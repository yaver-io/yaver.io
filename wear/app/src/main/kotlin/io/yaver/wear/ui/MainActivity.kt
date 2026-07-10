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
import io.yaver.wear.SessionClient
import io.yaver.wear.StandaloneStore
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
 * Transport policy: phone-paired first. If no phone node is reachable AND the
 * user has opted into standalone mode (StandaloneStore), fall back to
 * SessionClient — which drives a LIVE coding session via
 * POST /runner/session/turn (docs/yaver-watch-surface.md §4.2), NOT the
 * task-spawning /watch/turn. The standalone path holds a token; the phone-
 * paired path holds nothing.
 */
class MainActivity : ComponentActivity() {

    private lateinit var phoneBridge: PhoneBridge
    private lateinit var haptics: Haptics
    private lateinit var dictationLauncher: ActivityResultLauncher<Intent>
    private var sessionClient: SessionClient? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        phoneBridge = PhoneBridge(applicationContext)
        haptics = Haptics(applicationContext)

        // Resolve the standalone session client once if the user has opted in
        // and has creds. If phone-paired is available, we won't use it — but
        // it's cheap to hold and ready as an instant fallback.
        if (StandaloneStore.isReady(this)) {
            sessionClient = SessionClient(
                boxBaseUrl = StandaloneStore.boxUrl(this),
                bearerToken = StandaloneStore.token(this),
            )
        }

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
                if (phoneBridge.isPhoneReachable()) {
                    phoneBridge.sendTranscript(transcript)
                    // The actual reply (ack/working/summary) arrives async via
                    // ReplyListenerService → WatchState. Nothing to await here.
                } else {
                    // Phone not reachable — fall back to standalone SessionClient
                    // if the user has opted in + has creds.
                    val client = sessionClient
                    if (client == null) {
                        WatchState.setPhoneReachable(false)
                        WatchState.setPhase(WatchState.Phase.Idle)
                        WatchState.setLine("Phone not reachable")
                        haptics.failure()
                    } else {
                        // Drive the live session directly. The reply is
                        // synchronous (the endpoint waits + reads the pane),
                        // so we apply it right here.
                        val reply = client.sendText(transcript)
                        WatchState.applyReply(reply)
                        Haptics(applicationContext).fire(WatchState.hapticFor(reply))
                    }
                }
            } catch (_: PhoneBridge.PhoneUnreachableException) {
                fallBackOrFail(transcript, isChoice = false)
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
                if (phoneBridge.isPhoneReachable()) {
                    phoneBridge.sendConfirm(token, reply)
                } else {
                    val client = sessionClient
                    if (client == null) {
                        WatchState.setLine("Phone not reachable")
                        haptics.failure()
                    } else {
                        // Session choice: confirm → "1", cancel → "2".
                        val r = client.sendConfirm(reply)
                        WatchState.applyReply(r)
                        Haptics(applicationContext).fire(WatchState.hapticFor(r))
                    }
                }
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
                if (phoneBridge.isPhoneReachable()) {
                    phoneBridge.sendIntent(intent)
                } else {
                    val client = sessionClient
                    if (client == null) {
                        WatchState.setPhase(WatchState.Phase.Idle)
                        WatchState.setLine("Phone not reachable")
                        haptics.failure()
                    } else {
                        // Expand the intent to a transcript and send as a session prompt.
                        val text = intentToTranscript(intent)
                        val r = client.sendText(text)
                        WatchState.applyReply(r)
                        Haptics(applicationContext).fire(WatchState.hapticFor(r))
                    }
                }
            } catch (_: Throwable) {
                WatchState.setPhase(WatchState.Phase.Idle)
                WatchState.setLine("Phone not reachable")
                haptics.failure()
            }
        }
    }

    /** Fall back to standalone when the phone is unreachable, or surface the error. */
    private fun fallBackOrFail(transcript: String, isChoice: Boolean) {
        val client = sessionClient
        if (client == null) {
            WatchState.setPhoneReachable(false)
            WatchState.setPhase(WatchState.Phase.Idle)
            WatchState.setLine("Phone not reachable")
            haptics.failure()
        } else {
            lifecycleScope.launch {
                val r = client.sendText(transcript)
                WatchState.applyReply(r)
                Haptics(applicationContext).fire(WatchState.hapticFor(r))
            }
        }
    }

    /** Expand a complication intent to a transcript (mirrors watch_risk.go). */
    private fun intentToTranscript(intent: WatchProtocol.FixedIntent): String =
        when (intent) {
            WatchProtocol.FixedIntent.RUN_TESTS ->
                "run the tests on the primary device and tell me if they pass"
            WatchProtocol.FixedIntent.DEPLOY -> "deploy"
            WatchProtocol.FixedIntent.STATUS -> "give me a one-line status of the current work"
        }
}
