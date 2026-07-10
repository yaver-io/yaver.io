package io.yaver.wear

import android.content.Context
import android.speech.tts.TextToSpeech
import android.speech.tts.UtteranceProgressListener
import java.util.Locale

/**
 * The wrist's OTHER output channel besides haptics.
 *
 * `android.speech.tts.TextToSpeech` is part of the Android platform (no extra
 * dependency) and was unused — the watch showed ONE line of text + a haptic but
 * stayed silent. Speaking the one-sentence summary aloud is the single
 * highest-value addition on this surface (docs/yaver-watch-surface.md §6 / §8
 * build order #2): a wrist that answers aloud needs no screen at all.
 *
 * Mirrors [Haptics]'s shape so [WatchState.applyReply] can fire both
 * side-by-side: `Haptics(...).fire(WatchState.hapticFor(reply))` +
 * `Speech.forReply(reply)`. The spoken line is exactly the `spoken` field the
 * protocol already carries — no new agent contract, no new wire shape.
 *
 * Lifecycle: TextToSpeech needs a Context + async init, so this is a process-
 * wide singleton initialized once from [YaverWearApp.onCreate] with the
 * application context. Replies arrive from [ReplyListenerService] (which has no
 * Activity), so the speaker must outlive any single activity — the Application
 * scope is the correct owner.
 *
 * Design choices:
 *   • QUEUE_FLUSH interrupts any in-flight utterance — a stale sentence must
 *     never block the latest reply from being heard.
 *   • Speech rate slightly below default (0.9×) for clarity on a tiny speaker.
 *   • Empty / whitespace-only lines are no-ops.
 *   • If TTS isn't ready yet (init still pending on the first reply), the line
 *     is dropped — the haptic still fires, and the next reply will speak.
 */
object Speech {

    @Volatile
    private var tts: TextToSpeech? = null

    @Volatile
    private var ready: Boolean = false

    /** Initialize the TextToSpeech engine. Call once from Application.onCreate().
     *  Idempotent — safe to call again if the engine died and needs re-init. */
    fun init(context: Context) {
        if (tts != null) return
        val app = context.applicationContext
        tts = TextToSpeech(app) { status ->
            if (status == TextToSpeech.SUCCESS) {
                val tts = this.tts ?: return@TextToSpeech
                // Default to US English; the watch UI is English-first and the
                // spoken sentences are agent-generated English. A locale-mismatch
                // falls back to the engine's default voice (still speaks).
                tts.language = Locale.US
                tts.setSpeechRate(0.9f)
                tts.setOnUtteranceProgressListener(object : UtteranceProgressListener() {
                    override fun onStart(utteranceId: String?) {}
                    override fun onDone(utteranceId: String?) {}
                    override fun onError(utteranceId: String?) {}
                })
                ready = true
            }
        }
    }

    /** Speak a sentence. Interrupts anything currently being spoken so the
     *  newest reply is always the one heard. No-op if TTS isn't ready yet. */
    fun speak(text: String) {
        val cleaned = text.trim()
        if (cleaned.isEmpty()) return
        val engine = tts ?: return
        if (!ready) return
        // QUEUE_FLUSH drops any pending/in-flight utterance before speaking the
        // new one — so a rapid ack→summary pair doesn't queue two sentences.
        engine.speak(cleaned, TextToSpeech.QUEUE_FLUSH, null, "yaver_${System.nanoTime()}")
    }

    /** Stop any in-flight speech (e.g. user cancelled a confirm). */
    fun stop() {
        tts?.stop()
    }

    /** Pick the right line to speak for a protocol reply, mirroring
     *  [WatchState.hapticFor]. Same decision shape: speak the `spoken` field
     *  when present, fall back to a per-kind default, and stay silent for
     *  [WatchProtocol.Reply.Working] (a background task shouldn't talk over
     *  the user's next command). */
    fun forReply(reply: WatchProtocol.Reply) {
        when (reply) {
            is WatchProtocol.Reply.Ack -> speak(reply.spoken)
            is WatchProtocol.Reply.Working -> {
                // "Working…" is transient; speaking it would talk over the user.
                // The haptic (CLICK) is enough. The terminal summary speaks.
            }
            is WatchProtocol.Reply.ConfirmNeeded -> speak(reply.prompt)
            is WatchProtocol.Reply.Summary -> speak(reply.spoken)
            is WatchProtocol.Reply.Error -> speak(reply.spoken)
            is WatchProtocol.Reply.Handoff -> speak(reply.spoken)
            is WatchProtocol.Reply.Unknown -> speak(reply.spoken)
        }
    }

    /** Release the TTS engine. Call from Application.onTerminate() if you want
     *  clean shutdown (optional — the OS reclaims it with the process). */
    fun shutdown() {
        tts?.stop()
        tts?.shutdown()
        tts = null
        ready = false
    }
}
