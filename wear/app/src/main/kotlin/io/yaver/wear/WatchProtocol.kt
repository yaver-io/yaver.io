package io.yaver.wear

import org.json.JSONObject

/**
 * WIRE PROTOCOL v1 — single source of truth for the watch ⇄ phone (and watch →
 * box standalone) message shapes.
 *
 * Why org.json and not kotlinx.serialization: the protocol is tiny (a handful of
 * flat string fields), the payloads cross a process/device boundary as raw
 * UTF-8 bytes, and org.json ships in the Android platform with zero extra
 * dependency or codegen. Keeping it dependency-free here means PhoneBridge,
 * ReplyListenerService, and AgentClient all agree on exactly these keys/paths
 * without a serialization plugin in the build. If this protocol grows, switch to
 * kotlinx.serialization and make THIS file the only edit site.
 *
 * Transport paths (Wear Data Layer MessageClient):
 *   Watch → Phone : PATH_TURN  = "/yaver/watch/turn"   (payload = JSON UTF-8)
 *   Phone → Watch : PATH_REPLY = "/yaver/watch/reply"  (payload = JSON UTF-8)
 *
 * Standalone (no phone): the watch POSTs the SAME turn JSON to
 *   POST http://<box>:18080/watch/turn  (Authorization: Bearer <session-token>)
 * and the response body is one of the reply messages below.
 */
object WatchProtocol {

    // --- Data Layer message paths -------------------------------------------
    const val PATH_TURN = "/yaver/watch/turn"
    const val PATH_REPLY = "/yaver/watch/reply"

    /** Capability the phone app advertises so the watch can find the right node
     *  (CapabilityClient). The phone declares this in its wear.xml. */
    const val CAPABILITY_PHONE = "yaver_phone"

    const val PROTOCOL_VERSION = 1

    // --- JSON keys (pinned; do not rename without bumping PROTOCOL_VERSION) --
    private const val K_V = "v"
    private const val K_KIND = "kind"
    private const val K_TEXT = "text"
    private const val K_TOKEN = "token"
    private const val K_REPLY = "reply"
    private const val K_INTENT = "intent"
    private const val K_SPOKEN = "spoken"
    private const val K_PROMPT = "prompt"
    private const val K_TASK_ID = "taskId"
    private const val K_STATUS = "status"
    private const val K_TARGET = "target"
    private const val K_MACHINE_ID = "machineId"

    // ========================================================================
    //  Watch → Phone  (outbound turns)
    // ========================================================================

    /** {"v":1,"kind":"transcript","text":"<spoken command>"} */
    fun transcript(text: String): String =
        JSONObject()
            .put(K_V, PROTOCOL_VERSION)
            .put(K_KIND, "transcript")
            .put(K_TEXT, text)
            .toString()

    /** {"v":1,"kind":"confirm","token":"<token>","reply":"confirm"|"cancel"} */
    fun confirm(token: String, reply: ConfirmReply): String =
        JSONObject()
            .put(K_V, PROTOCOL_VERSION)
            .put(K_KIND, "confirm")
            .put(K_TOKEN, token)
            .put(K_REPLY, reply.wire)
            .toString()

    /** {"v":1,"kind":"intent","intent":"run-tests"|"deploy"|"status"} */
    fun intent(intent: FixedIntent): String =
        JSONObject()
            .put(K_V, PROTOCOL_VERSION)
            .put(K_KIND, "intent")
            .put(K_INTENT, intent.wire)
            .toString()

    /** {"v":1,"kind":"wake","machineId":"<id>"} — resume a parked managed box.
     *  Rides PATH_TURN like every other request. machineId omitted when empty so
     *  the phone resolves the current/primary managed box. */
    fun wake(machineId: String): String =
        JSONObject()
            .put(K_V, PROTOCOL_VERSION)
            .put(K_KIND, "wake")
            .apply { if (machineId.isNotEmpty()) put(K_MACHINE_ID, machineId) }
            .toString()

    enum class ConfirmReply(val wire: String) {
        CONFIRM("confirm"),
        CANCEL("cancel"),
    }

    /** The fixed one-tap intents bound to quick actions / complications. */
    enum class FixedIntent(val wire: String) {
        RUN_TESTS("run-tests"),
        DEPLOY("deploy"),
        STATUS("status"),
    }

    // ========================================================================
    //  Phone → Watch  (inbound replies)
    // ========================================================================

    /**
     * Parsed reply from the phone (or the box, in standalone mode). The watch
     * only ever needs to render `spoken` and react to `kind`; everything else is
     * a reference (token / taskId) it holds opaquely and echoes back.
     */
    sealed class Reply {
        /** {"v":1,"kind":"ack","spoken":"On it."} */
        data class Ack(val spoken: String) : Reply()

        /** {"v":1,"kind":"confirm-needed","token":"...","prompt":"..."} */
        data class ConfirmNeeded(val token: String, val prompt: String) : Reply()

        /** {"v":1,"kind":"working","taskId":"...","spoken":"Working…"} */
        data class Working(val taskId: String, val spoken: String) : Reply()

        /** {"v":1,"kind":"summary","taskId":"...","status":"completed","spoken":"Done. Tests pass."} */
        data class Summary(val taskId: String, val status: String, val spoken: String) : Reply()

        /** {"v":1,"kind":"error","spoken":"I couldn't reach your box."}
         *  `boxUnreachable` is set when the failure was a connectivity failure to
         *  the box (connection refused / timeout) — i.e. a self-parked box — so
         *  the wrist can offer "Wake" instead of a bare error line. */
        data class Error(val spoken: String, val boxUnreachable: Boolean = false) : Reply()

        /** {"v":1,"kind":"handoff","target":"phone","spoken":"Sent it to your phone."} */
        data class Handoff(val target: String, val spoken: String) : Reply()

        /** Anything we don't recognize — surfaced as a generic line, never a crash. */
        data class Unknown(val rawKind: String, val spoken: String) : Reply()
    }

    /**
     * Parse a reply payload. NEVER throws: malformed bytes from the wire become
     * an [Reply.Error] with a neutral line so the wrist never crashes on a bad
     * message. The one-line `spoken` is always populated (falls back to a
     * sensible default per kind) so the UI always has something to show.
     */
    fun parseReply(json: String): Reply {
        val obj = try {
            JSONObject(json)
        } catch (_: Throwable) {
            return Reply.Error("Something went wrong.")
        }
        // We tolerate a missing/mismatched version rather than hard-failing —
        // forward compatibility for the wrist favors "show a line" over "blank".
        val kind = obj.optString(K_KIND, "")
        val spoken = obj.optString(K_SPOKEN, "")
        return when (kind) {
            "ack" -> Reply.Ack(spoken.ifEmpty { "On it." })
            "confirm-needed" -> Reply.ConfirmNeeded(
                token = obj.optString(K_TOKEN, ""),
                prompt = obj.optString(K_PROMPT, "Confirm?"),
            )
            "working" -> Reply.Working(
                taskId = obj.optString(K_TASK_ID, ""),
                spoken = spoken.ifEmpty { "Working…" },
            )
            "summary" -> Reply.Summary(
                taskId = obj.optString(K_TASK_ID, ""),
                status = obj.optString(K_STATUS, "completed"),
                spoken = spoken.ifEmpty { "Done." },
            )
            "error" -> Reply.Error(spoken.ifEmpty { "Something went wrong." })
            "handoff" -> Reply.Handoff(
                target = obj.optString(K_TARGET, "phone"),
                spoken = spoken.ifEmpty { "Sent it to your phone." },
            )
            else -> Reply.Unknown(rawKind = kind, spoken = spoken.ifEmpty { "Got it." })
        }
    }

    /** UTF-8 bytes for a turn message — the exact payload MessageClient sends. */
    fun bytes(json: String): ByteArray = json.toByteArray(Charsets.UTF_8)

    /** UTF-8 decode for an inbound payload. */
    fun text(bytes: ByteArray): String = String(bytes, Charsets.UTF_8)
}
