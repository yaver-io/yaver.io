package io.yaver.feedback

import org.json.JSONArray
import org.json.JSONObject

/**
 * Wire types for the Yaver Feedback SDK.
 *
 * ⚠️ THESE FIELD NAMES ARE THE CONTRACT. They mirror `FeedbackReport` in
 * `desktop/agent/feedback.go`, and the agent decodes by name. Renaming a field
 * here does not fail a build — it fails silently at runtime, with the agent
 * receiving a report missing the field it needed. If that struct changes, this
 * file and the Swift equivalent change with it.
 *
 * Hand-rolled JSON rather than a serialization dependency: this SDK is dropped
 * into third-party apps, and every transitive dependency we add is one their
 * build might already have at a conflicting version.
 */

data class FeedbackConfig(
    /** Yaver agent base URL, usually http://<host>:18080 */
    val agentUrl: String,
    /**
     * SDK token — NOT a user session token.
     *
     * SDK tokens are scoped to feedback submission. A session token in an app
     * shipped to third parties would hand them the user's full agent authority,
     * which is a categorically different exposure than "can file a bug".
     */
    val authToken: String,
    val shakeEnabled: Boolean = true,
    /** Acceleration in g before a shake registers. 2.7 rejects normal handling. */
    val shakeThresholdG: Float = 2.7f,
    val captureScreenshot: Boolean = true,
    val captureErrors: Boolean = true,
    /** Optional project slug so the agent can attribute the report. */
    val projectSlug: String? = null,
)

/** A timestamped annotation, seconds from the start of the session. */
data class TimelineEvent(
    val time: Double,
    /** "voice" | "screenshot" | "annotation" | "crash" */
    val type: String,
    val text: String? = null,
    val file: String? = null,
) {
    fun toJson(): JSONObject = JSONObject().apply {
        put("time", time)
        put("type", type)
        text?.let { put("text", it) }
        file?.let { put("file", it) }
    }
}

data class CapturedError(
    val message: String,
    val stack: String? = null,
    val timestamp: String,
) {
    fun toJson(): JSONObject = JSONObject().apply {
        put("message", message)
        stack?.let { put("stack", it) }
        put("timestamp", timestamp)
    }
}

data class DeviceInfo(
    val platform: String,
    val osVersion: String,
    val model: String,
    val screenWidth: Int,
    val screenHeight: Int,
) {
    fun toJson(): JSONObject = JSONObject().apply {
        put("platform", platform)
        put("osVersion", osVersion)
        put("model", model)
        put("screenWidth", screenWidth)
        put("screenHeight", screenHeight)
    }
}

data class FeedbackReport(
    val id: String,
    /**
     * Always "in-app-sdk" from here.
     *
     * The agent uses this to distinguish a report the USER filed from inside
     * their app from one the Yaver viewer triggered remotely ("yaver-app").
     * They mean different things when triaging: one is a user hitting a
     * problem, the other is a developer inspecting.
     */
    val source: String = "in-app-sdk",
    val screenshots: List<String> = emptyList(),
    val timeline: List<TimelineEvent> = emptyList(),
    val errors: List<CapturedError> = emptyList(),
    val deviceInfo: DeviceInfo,
    val appVersion: String? = null,
    val buildId: String? = null,
    val note: String? = null,
    val createdAt: String,
) {
    fun toJson(): JSONObject = JSONObject().apply {
        put("id", id)
        put("source", source)
        if (screenshots.isNotEmpty()) {
            put("screenshots", JSONArray().also { arr -> screenshots.forEach { arr.put(it) } })
        }
        if (timeline.isNotEmpty()) {
            put("timeline", JSONArray().also { arr -> timeline.forEach { arr.put(it.toJson()) } })
        }
        if (errors.isNotEmpty()) {
            put("errors", JSONArray().also { arr -> errors.forEach { arr.put(it.toJson()) } })
        }
        put("deviceInfo", deviceInfo.toJson())
        appVersion?.let { put("appVersion", it) }
        buildId?.let { put("buildId", it) }
        note?.let { put("transcript", it) }
        put("createdAt", createdAt)
    }
}
