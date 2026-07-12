package io.yaver.wear

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * Standalone transport that drives a LIVE coding session via
 * POST /runner/session/turn.
 *
 * This is the endpoint a WATCH should call (docs/yaver-watch-surface.md §4.2).
 * The older /watch/turn (AgentClient.kt) spawns a NEW task; this one drives the
 * session the user already has running — "keep developing this" means the ubuntu
 * session, not a fresh task.
 *
 * Maps the session endpoint's response (awaitingChoice + options[] + pane) into
 * the SAME [WatchProtocol.Reply] the watch already renders:
 *   awaitingChoice: true  → [WatchProtocol.Reply.ConfirmNeeded]
 *   awaitingChoice: false → [WatchProtocol.Reply.Summary] (spoken = first clean line of pane)
 *   error / unreachable    → [WatchProtocol.Reply.Error]
 *
 * Choice loop: the client tracks `lastAwaitingChoice`. When the user speaks a
 * number after a menu, [sendText] recognizes it as a choice and sends {choice}
 * instead of {text}. ConfirmScreen's confirm/cancel maps to choice "1"/"2" as a
 * fallback (voice is the preferred path for picking a specific option).
 *
 * The four server-side guards (docs/yaver-watch-surface.md §4.2) are honored
 * by the endpoint itself — this client just maps the response. 409 status codes
 * (the guards firing) are decoded the same as 200: the body still carries
 * awaitingChoice + options + pane.
 *
 * Never throws — network/parse failures become [WatchProtocol.Reply.Error] so
 * the wrist always shows a line (same pattern as AgentClient).
 */
class SessionClient(
    /** e.g. "http://192.168.1.50:18080" — host:port of the box on the LAN. */
    private val boxBaseUrl: String,
    private val bearerToken: String,
) {

    private val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        // The session endpoint waits up to `waitMs` for the runner to react,
        // then reads the pane. 30s covers the 6s default wait + settle.
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    /** Tracks whether the last response had awaitingChoice: true. When the user
     *  speaks a number after a menu, sendText() uses this to send {choice}. */
    @Volatile
    private var lastAwaitingChoice: Boolean = false

    // ---- Public API ------------------------------------------------------

    /**
     * Send a spoken transcript. If the box was awaiting a choice and the text
     * looks like a number or number-word, send it as {choice} instead.
     */
    suspend fun sendText(text: String): WatchProtocol.Reply = withContext(Dispatchers.IO) {
        if (lastAwaitingChoice) {
            parseChoice(text)?.let { choice -> return@withContext turn(text = null, choice = choice) }
        }
        turn(text = text, choice = null)
    }

    /** Send a menu choice directly (e.g. from ConfirmScreen confirm → "1"). */
    suspend fun sendChoice(choice: String): WatchProtocol.Reply =
        withContext(Dispatchers.IO) { turn(text = null, choice = choice) }

    /**
     * Map a [WatchProtocol.ConfirmReply] (from ConfirmScreen) to a session choice.
     * confirm → option "1" (first option), cancel → option "2" (second).
     * This is a lossy fallback — the voice path (speak the number) is preferred
     * because menus renumber and option 1 isn't always "yes".
     */
    suspend fun sendConfirm(reply: WatchProtocol.ConfirmReply): WatchProtocol.Reply =
        withContext(Dispatchers.IO) {
            val choice = if (reply == WatchProtocol.ConfirmReply.CONFIRM) "1" else "2"
            turn(text = null, choice = choice)
        }

    // ---- Core POST -------------------------------------------------------

    private suspend fun turn(text: String?, choice: String?): WatchProtocol.Reply {
        return try {
            val body = JSONObject().apply {
                put("waitMs", 6000) // short + snappy for a wrist
                if (text != null) put("text", text)
                if (choice != null) put("choice", choice)
            }.toString().toRequestBody(JSON)

            val request = Request.Builder()
                .url(boxBaseUrl.trimEnd('/') + "/runner/session/turn")
                .header("Authorization", "Bearer $bearerToken")
                .post(body)
                .build()

            http.newCall(request).execute().use { resp ->
                val text = resp.body?.string().orEmpty()
                // 409 is the guards firing — the body still carries
                // awaitingChoice + options + pane, so decode it the same as 200.
                if (!resp.isSuccessful && resp.code != 409) {
                    val err = try {
                        JSONObject(text).optString("error", "")
                    } catch (_: Throwable) { "" }
                    return@use WatchProtocol.Reply.Error(
                        if (err.isNotEmpty()) err
                        else when (resp.code) {
                            401 -> "Sign in again."
                            in 500..599 -> "Your box hit an error."
                            else -> "I couldn't reach your box."
                        }
                    )
                }
                mapSessionResponse(text)
            }
        } catch (_: Throwable) {
            // Connection refused / timeout — the box is unreachable (likely a
            // self-parked managed box). Flag it so the wrist offers "Wake".
            WatchProtocol.Reply.Error("I couldn't reach your box.", boxUnreachable = true)
        }
    }

    /** Map the session endpoint's JSON response → WatchProtocol.Reply. */
    private fun mapSessionResponse(json: String): WatchProtocol.Reply {
        val obj = try {
            JSONObject(json)
        } catch (_: Throwable) {
            return WatchProtocol.Reply.Error("Something went wrong.")
        }

        val awaiting = obj.optBoolean("awaitingChoice", false)
        lastAwaitingChoice = awaiting

        if (awaiting) {
            // The pane is showing a menu. Show the options as the prompt; the
            // user speaks a number (or taps Confirm for option 1).
            // The prompt IS what gets spoken (Speech.forReply speaks it for
            // ConfirmNeeded) and shown on ConfirmScreen — both paths read the
            // same field, so the options list serves double duty.
            val options = obj.optJSONArray("options")
            val optionList = options?.toStringList() ?: emptyList()
            val prompt = if (optionList.isEmpty()) "Choose an option."
                         else optionList.joinToString("\n")
            return WatchProtocol.Reply.ConfirmNeeded(
                token = SESSION_CHOICE_TOKEN,
                prompt = prompt,
            )
        }

        // Not awaiting a choice → summarize the pane to one sentence.
        val pane = obj.optString("pane", "")
        val ok = obj.optBoolean("ok", true)
        val error = obj.optString("error", "")
        val spoken = summarizePane(pane)

        if (!ok || (error.isNotEmpty() && spoken.isEmpty())) {
            return WatchProtocol.Reply.Error(error.ifEmpty { "Something went wrong." })
        }
        val session = obj.optString("session", "")
        return WatchProtocol.Reply.Summary(
            taskId = session,
            status = "completed",
            spoken = spoken,
        )
    }

    // ---- Pane summarization (mirrors watch_risk.go::watchFirstStatusClause) ----

    /**
     * Pull the first short, status-shaped clause out of a pane tail. Refuses
     * code/markup/path-dump lines (the watch must never speak code). Clamps to
     * 120 chars — the same ceiling the agent's watch summariser uses.
     */
    private fun summarizePane(pane: String): String {
        val lines = pane.split("\n").map { it.trim() }.filter { it.isNotEmpty() }
        if (lines.isEmpty()) return "Done."

        for (line in lines) {
            if (!looksLikeCode(line)) {
                return clampSentence(stripMarkdown(line))
            }
        }
        // All lines looked like code.
        return "Done."
    }

    /** True when a line smells like code/markup/path-dump (mirrors the Go regex). */
    private fun looksLikeCode(line: String): Boolean {
        if (CODE_PATTERN.containsMatchIn(line)) return true
        if (line.contains("```")) return true
        return false
    }

    /** First sentence only, clamped to 120 chars. */
    private fun clampSentence(s: String): String {
        val m = SENTENCE_PATTERN.find(s)
        if (m != null) {
            val clause = m.groupValues[1]
            return if (clause.length <= 120) clause else clause.take(119) + "…"
        }
        return if (s.length <= 120) s else s.take(119) + "…"
    }

    private fun stripMarkdown(s: String): String =
        MARKDOWN_PATTERN.replace(s, "").trim()

    /** Map a spoken word/number to a bare digit the session endpoint accepts. */
    private fun parseChoice(text: String): String? {
        val t = text.trim().lowercase()
        if (t.isEmpty()) return null
        // Bare digit ("1", "2", "12").
        if (t.all { it.isDigit() }) return t
        // Common number words.
        return WORD_TO_NUMBER[t]
    }

    /** Helper: JSONArray → List<String> (null-safe). */
    private fun JSONArray.toStringList(): List<String> =
        (0 until length()).mapNotNull { i -> optString(i, "").takeIf { it.isNotEmpty() } }

    companion object {
        private val JSON = "application/json; charset=utf-8".toMediaType()

        /** Sentinel token for session-choice confirms (mirrors Swift). */
        const val SESSION_CHOICE_TOKEN = "__yaver_session_choice__"

        // Mirrors watch_risk.go::watchFirstStatusClause code-detection regex.
        private val CODE_PATTERN = Regex(
            """[{}<>;=]|\b(function|const|class|def|import|return)\b|/\w+/"""
        )
        private val SENTENCE_PATTERN = Regex("""^(.{1,120}?[.!?])(\s|$)""")
        private val MARKDOWN_PATTERN = Regex("[#*`_~]")

        private val WORD_TO_NUMBER = mapOf(
            "one" to "1", "two" to "2", "three" to "3", "four" to "4",
            "five" to "5", "six" to "6", "seven" to "7", "eight" to "8",
            "nine" to "9", "first" to "1", "second" to "2", "third" to "3",
            "yes" to "1", "no" to "2",
        )
    }
}
