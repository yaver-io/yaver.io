package io.yaver.wear

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.net.URI
import java.util.concurrent.TimeUnit

/**
 * Drive a remote Windows/Linux/Mac DESKTOP from the wrist, by voice, with no
 * video stream at all.
 *
 * WHY THIS FILE EXISTS:
 * Wear was the one surface with no `/ops` client of any kind — SessionClient
 * speaks only /runner/session/turn and AgentClient only /watch/turn, so the
 * whole ops verb surface (and therefore desktop control) was unreachable from
 * the watch. Every other surface reaches it through the client it already had.
 *
 * WHY SPEECH-ONLY IS THE RIGHT — NOT THE DEGRADED — MODE HERE:
 * A watch cannot usefully render a 1440x900 desktop. `desktop_voice` never
 * needs one: it resolves the spoken phrase against the target machine's OS
 * accessibility tree (AX / UIAutomation / AT-SPI) and returns ONE short
 * sentence meant to be spoken. "what's on screen" → "Save, Cancel, and 4 more".
 * That is also why it is cheap: no video means effectively no relay egress,
 * which is what keeps this usable on the free relay tier.
 *
 * The reply is deliberately [WatchProtocol.Reply], the same type the wrist
 * already renders and speaks — this adds a capability, not a second UI.
 *
 * Ambiguity is surfaced, never guessed: when a phrase matches several controls
 * the agent refuses and answers "2 matches: Save, Save As. Which one?" — the
 * user just says the fuller name. On a screen you would disambiguate by
 * looking; here the question IS the interface.
 *
 * Never throws — network/parse failures become [WatchProtocol.Reply.Error] so
 * the wrist always shows a line (same contract as SessionClient/AgentClient).
 */
class DesktopVoiceClient(
    /** e.g. "http://192.168.1.50:18080" — host:port of the box on the LAN. */
    private val boxBaseUrl: String,
    private val bearerToken: String,
) {
    private fun hasPrivatePeerRoute(): Boolean {
        val host = runCatching { URI(boxBaseUrl).host?.lowercase().orEmpty() }.getOrDefault("")
        if (host == "localhost" || host == "::1" ||
            (host.contains(':') && (host.startsWith("fc") || host.startsWith("fd")))) return true
        val parts = host.split('.').mapNotNull { it.toIntOrNull() }
        if (parts.size != 4) return false
        return parts[0] == 10 || parts[0] == 127 ||
            (parts[0] == 192 && parts[1] == 168) ||
            (parts[0] == 172 && parts[1] in 16..31) ||
            (parts[0] == 100 && parts[1] in 64..127)
    }

    data class HetznerPowerServer(
        val id: String,
        val name: String,
        val ip: String,
        val status: String,
        val type: String,
        val location: String,
    )

    private val http = OkHttpClient.Builder()
        // Desktop actions are local to the target machine (a click, a
        // keystroke, a tree read), so they return fast. A short ceiling keeps a
        // wedged box from freezing the wrist.
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .build()

    private fun opsRequest(verb: String, payload: JSONObject, machine: String = "local"): Request {
        val body = JSONObject()
            .put("verb", verb)
            .put("machine", machine)
            .put("payload", payload)
            .toString()
            .toRequestBody("application/json".toMediaType())
        return Request.Builder()
            .url("$boxBaseUrl/ops")
            .addHeader("Authorization", "Bearer $bearerToken")
            .addHeader("X-Yaver-Surface", "watch")
            .post(body)
            .build()
    }

    suspend fun listHetznerServers(): List<HetznerPowerServer> = withContext(Dispatchers.IO) {
        if (!hasPrivatePeerRoute()) error("Use the same LAN, Tailscale, or Yaver Mesh for Hetzner control.")
        http.newCall(opsRequest("hetzner_power", JSONObject().put("action", "list"))).execute().use { resp ->
            val root = JSONObject(resp.body?.string().orEmpty())
            if (!root.optBoolean("ok", false)) error(root.optString("error", "Could not list Hetzner servers."))
            val rows = root.optJSONObject("initial")?.optJSONArray("servers") ?: return@withContext emptyList()
            buildList {
                for (i in 0 until rows.length()) {
                    val row = rows.optJSONObject(i) ?: continue
                    val id = row.optString("id")
                    if (id.isBlank()) continue
                    add(HetznerPowerServer(
                        id, row.optString("name", id), row.optString("ip"),
                        row.optString("status", "unknown"), row.optString("type"), row.optString("location")
                    ))
                }
            }
        }
    }

    suspend fun setHetznerServerPower(serverId: String, powerOn: Boolean): WatchProtocol.Reply =
        withContext(Dispatchers.IO) {
            if (!hasPrivatePeerRoute()) {
                return@withContext WatchProtocol.Reply.Error("Connect over your private network or Yaver Mesh first.")
            }
            if (!serverId.matches(Regex("^[1-9][0-9]*$"))) {
                return@withContext WatchProtocol.Reply.Error("An exact server number is required.")
            }
            try {
                http.newCall(opsRequest("hetzner_power", JSONObject()
                    .put("action", if (powerOn) "power_on" else "shutdown")
                    .put("serverId", serverId)
                    .put("confirm", true))).execute().use { resp ->
                    parseReply(resp.body?.string().orEmpty())
                }
            } catch (e: Exception) {
                WatchProtocol.Reply.Error("Could not reach your trusted endpoint.", boxUnreachable = true)
            }
        }

    /**
     * Send a spoken phrase to a desktop.
     *
     * @param transcript what the user said, verbatim ("click Save").
     * @param machine    which box to drive. "local" is the box named by
     *                   [boxBaseUrl]; any other device id/alias is proxied by
     *                   the agent's dispatchOps, so the watch can drive a
     *                   Windows tower through whichever box it can reach.
     */
    suspend fun speak(transcript: String, machine: String = "local"): WatchProtocol.Reply =
        withContext(Dispatchers.IO) {
            val trimmed = transcript.trim()
            if (trimmed.isEmpty()) {
                return@withContext WatchProtocol.Reply.Error("I didn't catch that.")
            }

            // JSONObject does the escaping — a spoken phrase can contain quotes
            // and must never be concatenated into a request body.
            val payload = JSONObject()
                .put("transcript", trimmed)
            val req = opsRequest("desktop_voice", payload, machine)

            try {
                http.newCall(req).execute().use { resp ->
                    val text = resp.body?.string().orEmpty()
                    if (text.isBlank()) {
                        return@withContext WatchProtocol.Reply.Error("No answer from the box.")
                    }
                    parseReply(text)
                }
            } catch (e: Exception) {
                WatchProtocol.Reply.Error(e.message ?: "Could not reach the box.", boxUnreachable = true)
            }
        }

    /**
     * Map an OpsResult into a wrist reply.
     *
     * The agent always attaches a `spoken` sentence — on success AND on
     * refusal — precisely so thin surfaces do not have to interpret the
     * structured result. Prefer it over `error`, which is developer-facing.
     */
    private fun parseReply(text: String): WatchProtocol.Reply = try {
        val root = JSONObject(text)
        val initial = root.optJSONObject("initial")
        val spoken = initial?.optString("spoken").orEmpty()
        when {
            root.optBoolean("ok", false) ->
                WatchProtocol.Reply.Summary(taskId = "", status = "done", spoken = spoken.ifBlank { "Done." })
            spoken.isNotBlank() ->
                // Includes the ambiguity question, which is a prompt for the
                // user's next utterance rather than a dead end.
                WatchProtocol.Reply.Error(spoken)
            else ->
                WatchProtocol.Reply.Error(
                    root.optString("error").ifBlank { "That didn't work." }
                )
        }
    } catch (e: Exception) {
        WatchProtocol.Reply.Error("Unreadable answer from the box.")
    }
}
