package io.yaver.tv

/**
 * Models — mirrors of the agent's JSON shapes, plus the BoxTarget transport
 * model. Field names match the Go agent and mobile/src/lib twins. Mirrors
 * tvos/YaverTV/Models.swift (BoxTarget + task/device shapes).
 */

const val AGENT_PORT = 18080
const val CONVEX_ORIGIN = "https://perceptive-minnow-557.eu-west-1.convex.site"
const val WEB_BASE = "https://yaver.io"

fun urlAuthorityHost(host: String): String {
    val value = host.trim()
    if (value.startsWith("[") && value.endsWith("]")) return value
    return if (value.contains(':')) "[$value]" else value
}

fun agentHttpBase(host: String, port: Int = AGENT_PORT): String =
    "http://${urlAuthorityHost(host)}:$port"

/** A box (device) the TV can drive — the same model as tvOS `BoxTarget`. */
data class BoxTarget(
    /** deviceId (or a stable local id; a manual Add-Box gets id = host). */
    val id: String,
    val name: String,
    /** Account-local alias. Optional/additive so old persisted boxes decode. */
    val alias: String? = null,
    /** LAN IP / hostname running `yaver serve`. */
    val host: String,
    val port: Int = AGENT_PORT,
    /** Set for a managed cloud box that can be woken from the control plane. */
    val managed: Boolean? = null,
    val machineId: String? = null,
    /** Relay reachability. `relayBaseUrl` is the relay's HTTPS origin; the
     *  proxy path `/d/<deviceId>/ops` is built from [id]. */
    val relayBaseUrl: String? = null,
    val relayPassword: String? = null,
) {
    val aliasLabel: String?
        get() {
            val a = alias ?: return null
            if (a.isEmpty() || a == name) return null
            return if (a.startsWith("@")) a else "@$a"
        }

    /** True when this box can be resumed from the TV (managed + has a machineId). */
    val wakeable: Boolean get() = (managed == true) && !machineId.isNullOrEmpty()

    data class Endpoint(val url: String, val relay: Boolean)

    /** Ordered /ops endpoints to try: direct first, relay second.
     *  Direct-first / relay-fallback is Yaver's documented connection strategy. */
    val opsEndpoints: List<Endpoint>
        get() = requestEndpoints("/ops")

    fun requestEndpoints(rawPath: String): List<Endpoint> {
        val path = if (rawPath.startsWith("/")) rawPath else "/$rawPath"
        val out = mutableListOf<Endpoint>()
        if (host.isNotEmpty()) {
            out.add(Endpoint("${agentHttpBase(host, port)}$path", false))
        }
        val base = relayBaseUrl?.trim() ?: ""
        if (base.isNotEmpty() && id.isNotEmpty()) {
            val trimmed = if (base.endsWith("/")) base.dropLast(1) else base
            // Credentials never belong in a URL; OpsClient sets X-Relay-Password
            // on the relay leg only.
            out.add(Endpoint("$trimmed/d/$id$path", true))
        }
        return out
    }
}

/** One row of the account machine registry (GET /devices/list). */
data class RegisteredDevice(
    val deviceId: String,
    val name: String,
    val alias: String? = null,
    val platform: String? = null,
    val agentVersion: String? = null,
    val hosting: String? = null,
    val machineId: String? = null,
    val localIps: List<String> = emptyList(),
    val quicHost: String? = null,
    val lastSeenAt: Double? = null,
    val runnerIds: List<String> = emptyList(),
) {
    val isManaged: Boolean get() = hosting == "yaver-hosted" || !machineId.isNullOrEmpty()

    fun toBox(relayBaseUrl: String?, relayPassword: String?): BoxTarget {
        val host = (localIps.firstOrNull()?.takeIf { it.isNotBlank() } ?: quicHost) ?: ""
        return BoxTarget(
            id = deviceId,
            name = name,
            alias = alias,
            host = host,
            managed = if (isManaged) true else null,
            machineId = machineId,
            relayBaseUrl = relayBaseUrl,
            relayPassword = relayPassword,
        )
    }
}

data class DeviceList(val devices: List<RegisteredDevice>)

data class UserSettings(
    val relayUrl: String? = null,
    val relayPassword: String? = null,
    val primaryDeviceId: String? = null,
    val secondaryDeviceId: String? = null,
    val primaryRunnerByDevice: Map<String, String>? = null,
    val primaryModelByDevice: Map<String, String>? = null,
    val primaryReasoningEffortByDevice: Map<String, String>? = null,
    val primaryModeByDevice: Map<String, String>? = null,
    val primaryProviderByDevice: Map<String, String>? = null,
    val defaultRuntimeProjectByDevice: Map<String, Map<String, String>>? = null,
    val mcpServersByDevice: Map<String, Map<String, Any>>? = null,
    val appearanceThemeBySurface: List<AppearanceThemePreference> = emptyList(),
)

data class AppearanceThemePreference(
    val surface: String,
    val theme: String,
)

data class RunnerPreferenceMaps(
    val runners: Map<String, String> = emptyMap(),
    val models: Map<String, String> = emptyMap(),
    val reasoningEfforts: Map<String, String> = emptyMap(),
    val modes: Map<String, String> = emptyMap(),
    val providers: Map<String, String> = emptyMap(),
)

/** Convex stores one row per device. Keeping this parser pure makes the wire
 *  shape testable: Android TV previously treated the array as an object and
 *  silently forgot every saved runner/model preference. */
fun parseRunnerPreferenceMaps(rows: org.json.JSONArray?): RunnerPreferenceMaps {
    if (rows == null) return RunnerPreferenceMaps()
    val runners = mutableMapOf<String, String>()
    val models = mutableMapOf<String, String>()
    val reasoningEfforts = mutableMapOf<String, String>()
    val modes = mutableMapOf<String, String>()
    val providers = mutableMapOf<String, String>()
    for (i in 0 until rows.length()) {
        val row = rows.optJSONObject(i) ?: continue
        val deviceId = row.optString("deviceId")
        val runnerId = row.optString("runnerId")
        if (deviceId.isEmpty() || runnerId.isEmpty()) continue
        runners[deviceId] = runnerId
        row.optString("model").takeIf { it.isNotEmpty() }?.let { models[deviceId] = it }
        row.optString("reasoningEffort").takeIf { it.isNotEmpty() }?.let { reasoningEfforts[deviceId] = it }
        row.optString("mode").takeIf { it.isNotEmpty() }?.let { modes[deviceId] = it }
        row.optString("provider").takeIf { it.isNotEmpty() }?.let { providers[deviceId] = it }
    }
    return RunnerPreferenceMaps(runners, models, reasoningEfforts, modes, providers)
}

fun runnerPreferenceSettingsPatch(
    deviceId: String,
    runnerId: String,
    model: String?,
    reasoningEffort: String?,
    provider: String?,
): org.json.JSONObject = org.json.JSONObject().put(
    "primaryRunnerForDevice",
    org.json.JSONObject()
        .put("deviceId", deviceId)
        .put("runnerId", runnerId)
        .put("model", model ?: org.json.JSONObject.NULL)
        .put("reasoningEffort", reasoningEffort ?: org.json.JSONObject.NULL)
        .put("mode", org.json.JSONObject.NULL)
        .put("provider", provider ?: org.json.JSONObject.NULL),
)

data class AgentInfo(
    val hostname: String? = null,
    val platform: String? = null,
    val arch: String? = null,
    val agentVersion: String? = null,
    val deviceId: String? = null,
    val cpuPercent: Double? = null,
    val localIPs: List<String>? = null,
)

data class TaskCounts(val total: Int? = null, val running: Int? = null)

data class DevServerStatus(
    val running: Boolean? = null,
    val framework: String? = null,
    val port: Int? = null,
    val tasksTotal: Int? = null,
    val tasksRunning: Int? = null,
)

data class AgentStatus(
    val agentVersion: String? = null,
    val authExpired: Boolean? = null,
    val tasks: TaskCounts? = null,
    val devServer: DevServerStatus? = null,
)

/** A coding task (GET /tasks). */
data class TaskRow(
    val id: String,
    val title: String? = null,
    val status: String? = null,
    val runner: String? = null,
    val model: String? = null,
    val reasoningEffort: String? = null,
    val projectName: String? = null,
    val sessionId: String? = null,
    val tmuxSession: String? = null,
    val tmuxSessionId: String? = null,
    val tmuxPaneId: String? = null,
    val yaverSessionId: String? = null,
    val remoteBoxId: String? = null,
    val runnerName: String? = null,
    val startedFrom: String? = null,
    val initialSurface: String? = null,
    val lastSurface: String? = null,
    val lastActiveAt: String? = null,
    val presentation: List<TaskPresentationMessage> = emptyList(),
    val createdAt: Double? = null,
) {
    val safeTitle: String get() = redactHomePaths(title ?: "Untitled task")
}

data class TaskListEnvelope(val tasks: List<TaskRow>? = null)

/** A live tmux runner session (runner_sessions verb). */
data class RunnerSession(
    val name: String,
    val paneId: String? = null,
    val runner: String? = null,
    val origin: String? = null,
    val inputMode: String? = null,
    val taskId: String? = null,
    val attached: Boolean? = null,
) {
    val id: String get() = if (paneId.isNullOrEmpty()) name else "$name#$paneId"
}

data class RunnerSessionList(val sessions: List<RunnerSession>? = null)

data class RunnerInfo(
    val id: String,
    val installed: Boolean = false,
    val ready: Boolean? = null,
    val isDefault: Boolean = false,
    val models: List<ModelInfo> = emptyList(),
)

data class TaskPresentationMessage(
    val id: String,
    val kind: String,
    val role: String? = null,
    val text: String,
    val phase: String? = null,
    val state: String? = null,
    val runner: String? = null,
    val project: String? = null,
    val machine: String? = null,
    val platform: String? = null,
)

fun parseTaskPresentation(array: org.json.JSONArray?): List<TaskPresentationMessage> {
    if (array == null) return emptyList()
    return buildList {
        for (i in 0 until array.length()) {
            val item = array.optJSONObject(i) ?: continue
            val id = item.optString("id")
            val text = item.optString("text")
            if (id.isEmpty() || text.isEmpty()) continue
            add(TaskPresentationMessage(
                id = id,
                kind = item.optString("kind").ifEmpty { "status" },
                role = item.optString("role").ifEmpty { null },
                text = text,
                phase = item.optString("phase").ifEmpty { null },
                state = item.optString("state").ifEmpty { null },
                runner = item.optString("runner").ifEmpty { null },
                project = item.optString("project").ifEmpty { null },
                machine = item.optString("machine").ifEmpty { null },
                platform = item.optString("platform").ifEmpty { null },
            ))
        }
    }
}

data class ModelInfo(
    val id: String,
    val name: String? = null,
    val provider: String? = null,
    val providerName: String? = null,
    val isDefault: Boolean = false,
    val defaultReasoningEffort: String? = null,
    val supportedReasoningEfforts: List<TaskRunnerReasoningEffort> = emptyList(),
)

data class TaskRunnerReasoningEffort(val id: String, val description: String? = null)
data class TaskRunnerControlModel(
    val id: String,
    val name: String? = null,
    val isDefault: Boolean = false,
    val defaultReasoningEffort: String? = null,
    val supportedReasoningEfforts: List<TaskRunnerReasoningEffort> = emptyList(),
)
data class TaskRunnerControlCatalog(
    val runnerId: String,
    val model: String? = null,
    val reasoningEffort: String? = null,
    val models: List<TaskRunnerControlModel> = emptyList(),
    val isAdopted: Boolean = false,
)

fun parseTaskRunnerControlCatalog(obj: org.json.JSONObject): TaskRunnerControlCatalog {
    val models = buildList {
        val rows = obj.optJSONArray("models")
        if (rows != null) for (i in 0 until rows.length()) {
            val row = rows.optJSONObject(i) ?: continue
            val efforts = buildList {
                val values = row.optJSONArray("supportedReasoningEfforts")
                if (values != null) for (j in 0 until values.length()) {
                    val effort = values.optJSONObject(j) ?: continue
                    val id = effort.optString("reasoningEffort")
                    if (id.isNotEmpty()) add(TaskRunnerReasoningEffort(id, effort.optString("description").ifEmpty { null }))
                }
            }
            val id = row.optString("id")
            if (id.isNotEmpty()) add(TaskRunnerControlModel(
                id = id,
                name = row.optString("name").ifEmpty { null },
                isDefault = row.optBoolean("isDefault"),
                defaultReasoningEffort = row.optString("defaultReasoningEffort").ifEmpty { null },
                supportedReasoningEfforts = efforts,
            ))
        }
    }
    return TaskRunnerControlCatalog(
        runnerId = obj.optString("runnerId"),
        model = obj.optString("model").ifEmpty { null },
        reasoningEffort = obj.optString("reasoningEffort").ifEmpty { null },
        models = models,
        isAdopted = obj.optBoolean("isAdopted"),
    )
}

data class ProjectRow(
    val name: String,
    val path: String,
    val branch: String? = null,
    val framework: String? = null,
    val gitRemote: String? = null,
)

data class McpServer(val name: String, val enabled: Boolean = false)

/** Parse a project row, tolerating both `{task:…}` and bare shapes. */
fun parseTaskRow(obj: org.json.JSONObject): TaskRow? {
    val id = obj.optString("id").ifEmpty { obj.optString("taskId") }
    if (id.isEmpty()) return null
    val execution = obj.optJSONObject("executionSession")
    return TaskRow(
        id = id,
        title = obj.optString("title").ifEmpty { null },
        status = obj.optString("status").ifEmpty { null },
        runner = obj.optString("runner").ifEmpty { null },
        model = obj.optString("model").ifEmpty { null },
        reasoningEffort = obj.optString("reasoningEffort").ifEmpty { null },
        projectName = obj.optString("projectName").ifEmpty { null },
        sessionId = obj.optString("sessionId").ifEmpty { null },
        tmuxSession = obj.optString("tmuxSession").ifEmpty { null },
        tmuxSessionId = obj.optString("tmuxSessionId").ifEmpty { null },
        tmuxPaneId = obj.optString("tmuxPaneId").ifEmpty { null },
        yaverSessionId = execution?.optString("yaverSessionId")?.ifEmpty { null },
        remoteBoxId = execution?.optString("remoteBoxId")?.ifEmpty { null },
        runnerName = execution?.optString("runnerName")?.ifEmpty { null },
        startedFrom = execution?.optString("startedFrom")?.ifEmpty { null },
        initialSurface = execution?.optString("initialSurface")?.ifEmpty { null },
        lastSurface = execution?.optString("lastSurface")?.ifEmpty { null },
        lastActiveAt = execution?.optString("lastActiveAt")?.ifEmpty { null },
        presentation = parseTaskPresentation(obj.optJSONArray("presentation")),
        createdAt = if (obj.has("createdAt")) obj.optDouble("createdAt") else null,
    )
}
