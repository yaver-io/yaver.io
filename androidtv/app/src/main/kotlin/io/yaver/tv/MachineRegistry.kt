package io.yaver.tv

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
 * MachineRegistry — Convex-direct account data (devices, settings, relay
 * config), mirroring tvos/YaverTV/MachineRegistry.swift. `/devices/list` is the
 * inventory, `/settings` carries the user's relay URL/password (kept out of
 * every device row), and `/config` provides the relay server list.
 */
object MachineRegistry {
    private val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .build()

    private val JSON = "application/json; charset=utf-8".toMediaType()

    private fun auth(path: String, token: String, method: String = "GET", body: JSONObject? = null): okhttp3.Response {
        val rb = Request.Builder()
            .url("$CONVEX_ORIGIN/$path")
            .header("Authorization", "Bearer $token")
            .header("X-Yaver-Surface", TV_SURFACE_ID)
        val req = when (method) {
            "GET" -> rb.get().build()
            else -> rb.method(method, body?.toString()?.toRequestBody(JSON)).build()
        }
        return http.newCall(req).execute()
    }

    /** Fetch the account's machines. Throws AgentError with a readable message
     *  so the picker can show WHY it's empty instead of a silent blank. */
    suspend fun fetchDevices(token: String): List<RegisteredDevice> = withContext(Dispatchers.IO) {
        auth("devices/list", token).use { resp ->
            val text = resp.body?.string().orEmpty()
            if (resp.code == 401 || resp.code == 403) throw AgentError("Your TV session expired — sign in again.")
            if (!resp.isSuccessful) throw AgentError("Couldn't load your machines (${resp.code}).")
            val obj = runCatching { JSONObject(text) }.getOrNull()
                ?: throw AgentError("Yaver returned no device list.")
            val devices = obj.optJSONArray("devices") ?: JSONArray()
            (0 until devices.length()).mapNotNull { i ->
                val d = devices.optJSONObject(i) ?: return@mapNotNull null
                val deviceId = d.optString("deviceId").ifEmpty { d.optString("id") }
                if (deviceId.isEmpty()) return@mapNotNull null
                val ips = d.optJSONArray("localIps")?.let { a ->
                    (0 until a.length()).mapNotNull { a.optString(it).ifEmpty { null } }
                } ?: emptyList()
                val runners = d.optJSONArray("runnerIds")?.let { a ->
                    (0 until a.length()).mapNotNull { a.optString(it).ifEmpty { null } }
                } ?: emptyList()
                RegisteredDevice(
                    deviceId = deviceId,
                    name = d.optString("name").ifEmpty { deviceId },
                    alias = d.optString("alias").ifEmpty { null },
                    platform = d.optString("platform").ifEmpty { null },
                    agentVersion = d.optString("agentVersion").ifEmpty { null },
                    hosting = d.optString("hosting").ifEmpty { null },
                    machineId = d.optString("machineId").ifEmpty { null },
                    localIps = ips,
                    quicHost = d.optString("quicHost").ifEmpty { null },
                    lastHeartbeat = if (d.has("lastHeartbeat")) d.optDouble("lastHeartbeat") else null,
                    isOnline = d.optBoolean("isOnline", false),
                    needsAuth = d.optBoolean("needsAuth", false),
                    relayConnected = if (d.has("relayConnected")) d.optBoolean("relayConnected") else null,
                    controlPlaneState = d.optJSONObject("controlPlaneStatus")?.optString("state")?.ifEmpty { null },
                    controlPlaneReason = d.optJSONObject("controlPlaneStatus")?.optString("reasonCode")?.ifEmpty { null },
                    runnerIds = runners,
                )
            }
        }
    }

    /** GET /settings — per-user transport metadata (relay URL/password,
     *  primary device/runner, default project, MCP rows). */
    suspend fun fetchSettings(token: String): UserSettings = withContext(Dispatchers.IO) {
        auth("settings", token).use { resp ->
            val text = resp.body?.string().orEmpty()
            if (resp.code == 401 || resp.code == 403) throw AgentError("Your TV session expired — sign in again.")
            if (!resp.isSuccessful) throw AgentError("Couldn't load relay settings (${resp.code}).")
            val o = runCatching { JSONObject(text) }.getOrNull()
            val s = o?.optJSONObject("settings") ?: o ?: return@use UserSettings()
            val preferences = parseRunnerPreferenceMaps(s.optJSONArray("primaryRunnerByDevice"))
            UserSettings(
                relayUrl = s.optString("relayUrl").ifEmpty { null },
                relayPassword = s.optString("relayPassword").ifEmpty { null },
                primaryDeviceId = s.optString("primaryDeviceId").ifEmpty { null },
                secondaryDeviceId = s.optString("secondaryDeviceId").ifEmpty { null },
                workerDeviceIds = s.optJSONArray("workerDeviceIds")?.let { rows ->
                    (0 until rows.length()).mapNotNull { i -> rows.optString(i).takeIf(String::isNotEmpty) }
                } ?: emptyList(),
                showWorkerDevices = s.optBoolean("showWorkerDevices", false),
                opportunisticFleet = if (s.has("opportunisticFleet")) s.optBoolean("opportunisticFleet") else true,
                primaryRunnerByDevice = preferences.runners.takeIf { it.isNotEmpty() },
                primaryModelByDevice = preferences.models.takeIf { it.isNotEmpty() },
                primaryReasoningEffortByDevice = preferences.reasoningEfforts.takeIf { it.isNotEmpty() },
                primaryModeByDevice = preferences.modes.takeIf { it.isNotEmpty() },
                primaryProviderByDevice = preferences.providers.takeIf { it.isNotEmpty() },
                defaultRuntimeProjectByDevice = s.optJSONObject("defaultRuntimeProjectByDevice")?.toNestedStringMap(),
                mcpServersByDevice = s.optJSONObject("mcpServersByDevice")?.toNestedAnyMap(),
                appearanceThemeBySurface = s.optJSONArray("appearanceThemeBySurface")?.let { rows ->
                    (0 until rows.length()).mapNotNull { i ->
                        val row = rows.optJSONObject(i) ?: return@mapNotNull null
                        val surface = row.optString("surface")
                        val theme = row.optString("theme")
                        if (surface.isEmpty() || (theme != "light" && theme != "dark")) null
                        else AppearanceThemePreference(surface, theme)
                    }
                } ?: emptyList(),
            )
        }
    }

    /** POST /settings/repair-relay — re-sync this account's per-user relay
     *  password with the platform-managed value. The TV twin of mobile's
     *  repairRelay. */
    suspend fun repairRelay(token: String): String? = withContext(Dispatchers.IO) {
        runCatching {
            auth("settings/repair-relay", token, "POST", JSONObject()).use { resp ->
                if (resp.code == 401 || resp.code == 403) throw AgentError("Your TV session expired — sign in again to repair the relay.")
                if (!resp.isSuccessful) {
                    val text = resp.body?.string().orEmpty()
                    val msg = runCatching { JSONObject(text).optString("error") }.getOrNull()
                    throw AgentError(msg?.ifEmpty { null } ?: "Relay repair failed (${resp.code}).")
                }
                resp.body?.string().orEmpty().let { text ->
                    runCatching { JSONObject(text).optString("relayPassword") }
                        .getOrNull()?.takeIf { it.isNotEmpty() }
                }
            }
        }.getOrNull()
    }

    /** POST /settings — generic row write (primaryDeviceId, primaryRunnerForDevice, …).
     *  Same Convex row the phone/web write, so the TV choice is remembered
     *  everywhere. */
    suspend fun writeSetting(token: String, body: JSONObject) {
        auth("settings", token, "POST", body).use { resp ->
            if (!resp.isSuccessful) {
                val text = resp.body?.string().orEmpty()
                val msg = runCatching { JSONObject(text).optString("error") }.getOrNull()
                throw AgentError(msg?.ifEmpty { null } ?: "Settings save failed (${resp.code}).")
            }
        }
    }

    /** Save one coherent per-machine coding preference. Model/provider and
     *  reasoning stay on the same Convex row as the runner so another client
     *  never observes a half-updated selection. Secrets are not accepted. */
    suspend fun writeRunnerPreference(
        token: String,
        deviceId: String,
        runnerId: String,
        model: String?,
        reasoningEffort: String?,
        provider: String?,
    ) {
        writeSetting(token, runnerPreferenceSettingsPatch(
            deviceId, runnerId, model, reasoningEffort, provider))
    }

    /** POST /settings — write a default-project row to Convex. Same
     *  `defaultRuntimeProjectForDevice` the phone and web write, so a project
     *  picked on the TV is remembered everywhere. Fire-and-forget. */
    suspend fun writeDefaultProject(token: String, deviceId: String, project: Map<String, String>) {
        runCatching {
            auth(
                "settings",
                token,
                "POST",
                JSONObject().put(
                    "defaultRuntimeProjectForDevice",
                    JSONObject().put(deviceId, JSONObject(project)),
                ),
            ).close()
        }
    }

    /** POST /settings — write the MCP selection for a device. Fire-and-forget. */
    suspend fun writeMcpSelection(token: String, deviceId: String, mcpServers: List<String>, includeYaverMcp: Boolean) {
        runCatching {
            auth(
                "settings",
                token,
                "POST",
                JSONObject().put(
                    "mcpServersByDevice",
                    JSONObject().put(
                        deviceId,
                        JSONObject()
                            .put("mcpServers", JSONArray(mcpServers))
                            .put("includeYaverMcp", includeYaverMcp),
                    ),
                ),
            ).close()
        }
    }

    /** Account removal for BYO/self-hosted devices. */
    suspend fun removeDevice(deviceId: String, token: String) {
        auth("devices/remove", token, "POST", JSONObject().put("deviceId", deviceId)).use { resp ->
            if (!resp.isSuccessful) {
                val msg = runCatching { JSONObject(resp.body?.string().orEmpty()).optString("error") }.getOrNull()
                throw AgentError(msg?.ifEmpty { null } ?: "Couldn't remove this machine (${resp.code}).")
            }
        }
    }

    /** Provider-aware removal for Yaver-hosted boxes (cancels linked billing). */
    suspend fun decommissionCloudMachine(machineId: String, token: String) {
        auth("billing/yaver-cloud/dev-deprovision", token, "POST", JSONObject().put("machineId", machineId)).use { resp ->
            if (!resp.isSuccessful) {
                val msg = runCatching { JSONObject(resp.body?.string().orEmpty()).optString("error") }.getOrNull()
                throw AgentError(msg?.ifEmpty { null } ?: "Couldn't decommission this machine (${resp.code}).")
            }
        }
    }

    /** POST /devices/request-update — desired state, applied at next heartbeat. */
    suspend fun requestUpdate(deviceIds: List<String>, token: String) {
        auth("devices/request-update", token, "POST", JSONObject().put("deviceIds", JSONArray(deviceIds))).use { resp ->
            if (!resp.isSuccessful) {
                val msg = runCatching { JSONObject(resp.body?.string().orEmpty()).optString("error") }.getOrNull()
                throw AgentError(msg?.ifEmpty { null } ?: "Update request failed (${resp.code}).")
            }
        }
    }

    /** GET /config — the configured relay servers (the free relay + any
     *  Relay Pro entries), used to build a relay fallback for a manual box. */
    data class RelayServerRow(val id: String, val label: String, val httpUrl: String?, val quicAddr: String?)

    suspend fun fetchRelayConfig(token: String): List<RelayServerRow> = withContext(Dispatchers.IO) {
        runCatching {
            auth("config", token).use { resp ->
                if (!resp.isSuccessful) return@use emptyList()
                val o = runCatching { JSONObject(resp.body?.string().orEmpty()) }.getOrNull() ?: return@use emptyList()
                val relays = o.optJSONArray("relayServers") ?: o.optJSONArray("relays") ?: JSONArray()
                (0 until relays.length()).mapNotNull { i ->
                    val r = relays.optJSONObject(i) ?: return@mapNotNull null
                    RelayServerRow(
                        id = r.optString("id"),
                        label = r.optString("label").ifEmpty { r.optString("id") },
                        httpUrl = r.optString("httpUrl").ifEmpty { null },
                        quicAddr = r.optString("quicAddr").ifEmpty { null },
                    )
                }
            }
        }.getOrDefault(emptyList())
    }

    private fun JSONObject.toMap(): Map<String, String>? {
        if (length() == 0) return null
        val out = mutableMapOf<String, String>()
        names()?.let { names ->
            for (i in 0 until names.length()) {
                val k = names.getString(i)
                val v = opt(k)
                if (v is String) out[k] = v
            }
        }
        return out
    }

    private fun JSONObject.toNestedStringMap(): Map<String, Map<String, String>>? =
        if (length() == 0) null else buildMap {
            names()?.let { keys ->
                for (i in 0 until keys.length()) {
                    val key = keys.getString(i)
                    optJSONObject(key)?.toMap()?.let { put(key, it) }
                }
            }
        }

    private fun JSONObject.toNestedAnyMap(): Map<String, Map<String, Any>>? =
        if (length() == 0) null else buildMap {
            names()?.let { keys ->
                for (i in 0 until keys.length()) {
                    val key = keys.getString(i)
                    val child = optJSONObject(key) ?: continue
                    val values = buildMap<String, Any> {
                        child.names()?.let { childKeys ->
                            for (j in 0 until childKeys.length()) {
                                val childKey = childKeys.getString(j)
                                child.opt(childKey)?.let { put(childKey, it) }
                            }
                        }
                    }
                    put(key, values)
                }
            }
        }
}
