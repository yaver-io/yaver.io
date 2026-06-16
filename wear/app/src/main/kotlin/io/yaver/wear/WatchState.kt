package io.yaver.wear

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/**
 * Process-wide UI state for the watch.
 *
 * It is a plain object (not a ViewModel) because the inbound replies arrive on a
 * WearableListenerService — a different component from the Activity — and both
 * need to write/read the same state. A StateFlow here is the simplest bridge:
 * the service emits a parsed [WatchProtocol.Reply], the Compose UI collects it.
 *
 * The watch holds NOTHING durable: this is in-memory only. If the process is
 * killed, the task keeps running on the phone/box; when the wrist re-opens, the
 * phone re-sends or the user just asks again. The watch only ever held a
 * reference (taskId/token), never the work.
 */
object WatchState {

    /** What the wrist is currently doing / showing. */
    sealed class Phase {
        /** Idle — show the record affordance + last line (if any). */
        object Idle : Phase()

        /** Capturing speech (dictation in progress). */
        object Listening : Phase()

        /** Turn sent, awaiting the phone's first reply. */
        object Sending : Phase()

        /** A task is running on the runner; we're waiting for the summary. */
        data class Working(val taskId: String) : Phase()

        /** The runner asked us to confirm a risky (write/deploy) verb. */
        data class Confirm(val token: String, val prompt: String) : Phase()
    }

    /** The single short line shown big on the watch (≤ ~1 sentence). */
    private val _line = MutableStateFlow("Raise & speak")
    val line: StateFlow<String> = _line

    private val _phase = MutableStateFlow<Phase>(Phase.Idle)
    val phase: StateFlow<Phase> = _phase

    /** Set by MainActivity at startup — true when no phone node is reachable so
     *  the UI can hint "standalone" / "phone not reachable". */
    private val _phoneReachable = MutableStateFlow(true)
    val phoneReachable: StateFlow<Boolean> = _phoneReachable

    fun setPhoneReachable(reachable: Boolean) {
        _phoneReachable.value = reachable
    }

    fun setLine(text: String) {
        _line.value = text
    }

    fun setPhase(phase: Phase) {
        _phase.value = phase
    }

    fun listening() {
        _phase.value = Phase.Listening
        _line.value = "Listening…"
    }

    fun sending(transcript: String) {
        _phase.value = Phase.Sending
        // Echo the heard command briefly so the user can see we got it right.
        _line.value = "“$transcript”"
    }

    /**
     * Apply a parsed reply from the phone (or box) to the UI state. This is the
     * one place a [WatchProtocol.Reply] turns into wrist state — keeps the
     * listener service and the standalone client path identical.
     */
    fun applyReply(reply: WatchProtocol.Reply) {
        when (reply) {
            is WatchProtocol.Reply.Ack -> {
                _line.value = reply.spoken
                // Stay in/return to a neutral "we heard you" state; a Working or
                // Summary reply will follow for async tasks.
                if (_phase.value is Phase.Sending) _phase.value = Phase.Idle
            }
            is WatchProtocol.Reply.ConfirmNeeded -> {
                _phase.value = Phase.Confirm(reply.token, reply.prompt)
                _line.value = reply.prompt
            }
            is WatchProtocol.Reply.Working -> {
                _phase.value = Phase.Working(reply.taskId)
                _line.value = reply.spoken
            }
            is WatchProtocol.Reply.Summary -> {
                _phase.value = Phase.Idle
                _line.value = reply.spoken
            }
            is WatchProtocol.Reply.Error -> {
                _phase.value = Phase.Idle
                _line.value = reply.spoken
            }
            is WatchProtocol.Reply.Handoff -> {
                _phase.value = Phase.Idle
                _line.value = reply.spoken
            }
            is WatchProtocol.Reply.Unknown -> {
                _phase.value = Phase.Idle
                _line.value = reply.spoken
            }
        }
    }

    /** Distinct haptic cue to fire for a given reply (MainActivity wires this to
     *  the Vibrator). Kept here so the policy is centralized. */
    fun hapticFor(reply: WatchProtocol.Reply): Haptics.Cue = when (reply) {
        is WatchProtocol.Reply.Ack -> Haptics.Cue.CLICK
        is WatchProtocol.Reply.ConfirmNeeded -> Haptics.Cue.CLICK
        is WatchProtocol.Reply.Working -> Haptics.Cue.CLICK
        is WatchProtocol.Reply.Summary ->
            if (reply.status.equals("completed", ignoreCase = true)) Haptics.Cue.SUCCESS
            else Haptics.Cue.FAILURE
        is WatchProtocol.Reply.Error -> Haptics.Cue.FAILURE
        is WatchProtocol.Reply.Handoff -> Haptics.Cue.CLICK
        is WatchProtocol.Reply.Unknown -> Haptics.Cue.CLICK
    }
}
