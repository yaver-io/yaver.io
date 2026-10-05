package io.yaver.tv

import android.content.Context
import android.speech.tts.TextToSpeech
import android.speech.tts.UtteranceProgressListener
import java.util.Locale
import java.util.regex.Pattern

/**
 * Speech — the TV's voice output channel (Android TextToSpeech).
 *
 * Mirrors tvos/YaverTV/Speech.swift: speaking the session summary aloud turns
 * the TV into a genuine "lean-back" coding surface — send a prompt from the
 * remote, hear the result spoken back without reading a wall of text. The TV
 * has room to render the pane visually, so TTS is complementary (speak a
 * summary while showing the full pane), not the sole output channel.
 */
class Speech(private val context: Context) {
    private var tts: TextToSpeech? = null
    private var ready = false
    private var pendingText: String? = null

    private fun ensure(): TextToSpeech? {
        tts?.let { if (ready) return it }
        val engine = TextToSpeech(context) { status ->
            ready = status == TextToSpeech.SUCCESS
            if (ready) {
                tts?.language = Locale.getDefault()
                tts?.setSpeechRate(0.85f)
                pendingText?.let { queued ->
                    pendingText = null
                    tts?.speak(queued, TextToSpeech.QUEUE_FLUSH, null, "yaver-${System.currentTimeMillis()}")
                }
            }
        }
        tts = engine
        return engine
    }

    /** Speak a sentence. Interrupts anything currently being spoken. */
    fun speak(text: String) {
        val cleaned = text.trim()
        if (cleaned.isEmpty()) return
        val t = ensure() ?: return
        if (!ready) {
            pendingText = cleaned
            return
        }
        t.stop()
        t.speak(cleaned, TextToSpeech.QUEUE_FLUSH, null, "yaver-${System.currentTimeMillis()}")
    }

    /** Stop any in-flight speech. */
    fun stop() {
        pendingText = null
        runCatching { tts?.stop() }
    }

    /** Speak a one-sentence summary of a pane tail. */
    fun speakSummary(pane: String) = speak(summarize(pane))

    fun shutdown() {
        runCatching { tts?.shutdown() }
        tts = null
        ready = false
        pendingText = null
    }

    // ── Pane summarization (mirrors watch_risk.go::watchFirstStatusClause) ─

    companion object {
        private val CODE_PATTERN = Pattern.compile("[{}<>;=]|```|\\b(function|const|class|def|import|return)\\b|/\\w+/")
        private val SENTENCE_PATTERN = Pattern.compile("^(.{1,120}?[.!?])(\\s|$)", Pattern.DOTALL)
        private val MARKDOWN_PATTERN = Pattern.compile("[#*`_~]")

        /** First clean non-code line, clamped to 120 chars. */
        fun summarize(pane: String): String {
            val lines = pane.split("\n").map { it.trim() }
            for (line in lines) {
                if (line.isEmpty()) continue
                if (!CODE_PATTERN.matcher(line).find()) {
                    return clampSentence(stripMarkdown(line))
                }
            }
            return "Done."
        }

        private fun clampSentence(s: String): String {
            val m = SENTENCE_PATTERN.matcher(s)
            if (m.find() && m.group(1) != null) {
                val clause = m.group(1)!!
                return if (clause.length <= 120) clause else clause.take(119) + "…"
            }
            return if (s.length <= 120) s else s.take(119) + "…"
        }

        private fun stripMarkdown(s: String): String =
            MARKDOWN_PATTERN.matcher(s).replaceAll("").trim()
    }
}
