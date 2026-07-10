package io.yaver.wear

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.util.concurrent.TimeUnit

/**
 * LEGACY standalone transport: watch → box over LAN HTTP via /watch/turn.
 *
 * ⚠️ SUPERSEDED by [SessionClient], which drives a LIVE coding session via
 * POST /runner/session/turn (docs/yaver-watch-surface.md §4.2). This file is
 * kept because:
 *   1. It's a valid fallback if /runner/session/turn is unavailable on an older
 *      agent — /watch/turn still works (it spawns a new task instead of driving
 *      the live session).
 *   2. It documents the original wire protocol shape for reference.
 * MainActivity no longer routes to this client; SessionClient is the standalone
 * transport. See docs/yaver-watch-surface.md §8 build order #1.
 *
 * Original docstring preserved below:
 * ---------------------------------------------------------------------------
 * Standalone (SECONDARY) transport: watch → box over LAN HTTP.
 *
 * Used only when there is no paired phone (or the user explicitly opts into
 * "works without your phone"). The watch POSTs the SAME turn JSON that it would
 * have sent over the Data Layer to:
 *
 *   POST http://<box>:18080/watch/turn
 *   Authorization: Bearer <session-token>
 *   Content-Type: application/json
 *   body = {"v":1,"kind":"transcript","text":"..."}   (or confirm / intent)
 *
 * The response body is a single protocol reply message (ack / confirm-needed /
 * working / summary / error / handoff). Because there is no phone to do the
 * async wake here, the box is expected to either reply terminally or return a
 * `working` reply that the watch can re-poll (poll loop is a follow-up; the
 * scaffold sends one turn and surfaces one reply).
 *
 * The bearer token comes from device-code auth ([Backend]); it is held by the
 * caller (the watch's secure store) and passed in per request — AgentClient is
 * stateless.
 */
class AgentClient(
    /** e.g. "http://192.168.1.50:18080" — host:port of the box on the LAN. */
    private val boxBaseUrl: String,
    private val bearerToken: String,
) {

    private val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    /** Send a turn JSON, return the parsed reply. Never throws — network/parse
     *  failures become a [WatchProtocol.Reply.Error] so the wrist shows a line. */
    suspend fun sendTurn(turnJson: String): WatchProtocol.Reply = withContext(Dispatchers.IO) {
        try {
            val body = turnJson.toRequestBody(JSON)
            val request = Request.Builder()
                .url(boxBaseUrl.trimEnd('/') + "/watch/turn")
                .header("Authorization", "Bearer $bearerToken")
                .post(body)
                .build()
            http.newCall(request).execute().use { resp ->
                if (!resp.isSuccessful) {
                    return@use WatchProtocol.Reply.Error(
                        when (resp.code) {
                            401 -> "Sign in again."
                            in 500..599 -> "Your box hit an error."
                            else -> "I couldn't reach your box."
                        }
                    )
                }
                val text = resp.body?.string().orEmpty()
                WatchProtocol.parseReply(text)
            }
        } catch (_: Throwable) {
            WatchProtocol.Reply.Error("I couldn't reach your box.")
        }
    }

    // Convenience wrappers mirroring PhoneBridge's API surface.
    suspend fun sendTranscript(text: String) = sendTurn(WatchProtocol.transcript(text))
    suspend fun sendConfirm(token: String, reply: WatchProtocol.ConfirmReply) =
        sendTurn(WatchProtocol.confirm(token, reply))
    suspend fun sendIntent(intent: WatchProtocol.FixedIntent) =
        sendTurn(WatchProtocol.intent(intent))

    companion object {
        private val JSON = "application/json; charset=utf-8".toMediaType()
    }
}
