package io.yaver.tv

import android.content.Context
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject

/**
 * TvStore — the TV's app state: session token, selected box, the account's
 * boxes, relay settings, auto-connect. Mirror of tvos/YaverTV/YaverStore.swift
 * (the parts the reachable tvOS surface uses).
 *
 * The token lives in [TokenStore] (Keystore-backed). Boxes and UI preferences
 * are plain SharedPreferences. `relayRepair` is injected into every client so
 * a stale per-user relay password self-heals exactly once.
 */
class TvStore(private val appContext: Context, private val scope: CoroutineScope) {

    private val prefs = appContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    private val _token = MutableStateFlow("")
    val token: StateFlow<String> = _token.asStateFlow()

    private val _boxes = MutableStateFlow<List<BoxTarget>>(emptyList())
    val boxes: StateFlow<List<BoxTarget>> = _boxes.asStateFlow()

    private val _selectedBoxId = MutableStateFlow(prefs.getString(SELECTED_BOX_ID, null))

    /** Derived: the selected box row, recomputed when boxes/selection change. */
    val selectedBox: StateFlow<BoxTarget?> = kotlinx.coroutines.flow.combine(_boxes, _selectedBoxId) { boxes, id ->
        if (id == null) null else boxes.firstOrNull { it.id == id }
    }.stateIn(
        scope,
        kotlinx.coroutines.flow.SharingStarted.Eagerly,
        _boxes.value.firstOrNull { it.id == _selectedBoxId.value },
    )

    private val _settings = MutableStateFlow<UserSettings?>(null)
    val settings: StateFlow<UserSettings?> = _settings.asStateFlow()

    private val _appearanceTheme = MutableStateFlow(
        prefs.getString(APPEARANCE_THEME_KEY, "dark").takeIf { it == "light" } ?: "dark"
    )
    val appearanceTheme: StateFlow<String> = _appearanceTheme.asStateFlow()

    private val _autoConnecting = MutableStateFlow(false)
    val autoConnecting: StateFlow<Boolean> = _autoConnecting.asStateFlow()

    private val _autoConnectTarget = MutableStateFlow<String?>(null)
    val autoConnectTarget: StateFlow<String?> = _autoConnectTarget.asStateFlow()

    private val _devices = MutableStateFlow<List<RegisteredDevice>>(emptyList())
    val devices: StateFlow<List<RegisteredDevice>> = _devices.asStateFlow()

    private var lastOwnerUserId: String? = null

    init {
        Backend.configure(appContext)
        // Load the token + persisted boxes from disk.
        _token.value = TokenStore.load(appContext)
        _boxes.value = loadBoxes()
    }

    val isAuthenticated: Boolean get() = _token.value.isNotEmpty()

    /** A client for the given box, wired with this store's relay self-heal. */
    fun clientFor(box: BoxTarget): OpsClient {
        return OpsClient(box, _token.value, relayRepair = { repairRelay() })
    }

    fun sessionClientFor(box: BoxTarget): SessionClient {
        return SessionClient(box, _token.value, relayRepair = { repairRelay() })
    }

    /** Relay-credential self-heal: POST /settings/repair-relay then re-read
     *  settings, returning a repaired box (or null). */
    suspend fun repairRelay(): BoxTarget? {
        val token = _token.value
        if (token.isEmpty()) return null
        return runCatching {
            val repairedPassword = MachineRegistry.repairRelay(token)
            val fresh = MachineRegistry.fetchSettings(token)
            adoptSettings(fresh)
            val box = selectedBox.value ?: return null
            val updated = box.copy(
                relayBaseUrl = fresh.relayUrl ?: box.relayBaseUrl,
                relayPassword = repairedPassword ?: fresh.relayPassword ?: box.relayPassword,
            )
            selectBox(updated)
            updated
        }.getOrNull()
    }

    // ── Auth ──────────────────────────────────────────────────────────────

    suspend fun signIn(token: String) {
        _token.value = token
        TokenStore.save(appContext, token)
        // Best-effort: refresh the session to learn WHO this TV belongs to,
        // so the next sign-in sends the owner hint. Never blocks.
        runCatching { Backend.refreshSession(appContext, token) }
    }

    fun signOut() {
        val sessionToRevoke = _token.value
        clearLocalSession()
        if (sessionToRevoke.isNotEmpty()) {
            scope.launch { Backend.revokeSession(sessionToRevoke) }
        }
    }

    /** Server switching is local-only: do not send the old server's bearer to
     * the newly selected origin while the asynchronous revoke is scheduled. */
    fun signOutForServerSwitch() = clearLocalSession()

    private fun clearLocalSession() {
        TokenStore.clear(appContext)
        _token.value = ""
        _boxes.value = emptyList()
        _selectedBoxId.value = null
        _settings.value = null
        _appearanceTheme.value = "dark"
        prefs.edit().remove(APPEARANCE_THEME_KEY).apply()
        prefs.edit().remove(SELECTED_BOX_ID).apply()
        prefs.edit().remove(BOXES_KEY).apply()
    }

    suspend fun refreshSessionOnLaunch() {
        val token = _token.value
        if (token.isEmpty()) return
        Backend.refreshSession(appContext, token)?.let { rotated ->
            if (rotated.isNotEmpty() && rotated != token) {
                // Extend-only: adopt only if we're still on the same token.
                _token.value = rotated
                TokenStore.save(appContext, rotated)
            }
        }
    }

    // ── Box selection / persistence ───────────────────────────────────────

    fun selectBox(box: BoxTarget) {
        val idx = _boxes.value.indexOfFirst { it.id == box.id }
        val updated = if (idx >= 0) {
            _boxes.value.toMutableList().also { it[idx] = box }
        } else {
            _boxes.value + box
        }
        _boxes.value = updated
        _selectedBoxId.value = box.id
        prefs.edit().putString(SELECTED_BOX_ID, box.id).apply()
        saveBoxes()
    }

    fun addBox(name: String, host: String, machineId: String?) {
        val id = machineId?.takeIf { it.isNotEmpty() } ?: host
        selectBox(
            BoxTarget(
                id = id,
                name = name.ifEmpty { host },
                host = host.trim(),
                managed = if (!machineId.isNullOrEmpty()) true else null,
                machineId = machineId?.takeIf { it.isNotEmpty() },
            )
        )
    }

    fun removeBox(id: String) {
        _boxes.value = _boxes.value.filterNot { it.id == id }
        if (_selectedBoxId.value == id) {
            _selectedBoxId.value = null
            prefs.edit().remove(SELECTED_BOX_ID).apply()
        }
        saveBoxes()
    }

    /** Merge the account registry into the local box list (registry rows
     *  become relay-capable boxes; manual LAN boxes stay). */
    suspend fun refreshDevices() {
        val token = _token.value
        if (token.isEmpty()) return
        runCatching {
            val (deviceRows, settings) = coroutineScope {
                val d = async { MachineRegistry.fetchDevices(token) }
                val s = async { runCatching { MachineRegistry.fetchSettings(token) }.getOrDefault(_settings.value) }
                Pair(d.await(), s.await())
            }
            _devices.value = deviceRows
            if (settings != null) adoptSettings(settings)
            val registryBoxes = deviceRows.map { it.toBox(settings?.relayUrl, settings?.relayPassword) }
            // Keep manual boxes that aren't in the registry.
            val manual = _boxes.value.filter { local -> registryBoxes.none { it.id == local.id } }
            _boxes.value = registryBoxes + manual
            saveBoxes()
        }.onFailure { e ->
            if (e is AgentError && e.message.contains("session expired")) {
                // A dead token is a sign-out, not a hidden error state.
                _token.value = ""
                TokenStore.clear(appContext)
            }
        }
    }

    /** Re-resolve a reachable address for the selected box (relay/direct
     *  self-heal). No-op when the current one answers. */
    suspend fun healReachability() {
        val box = selectedBox.value ?: return
        val probe = runCatching {
            clientFor(box).info()
        }.getOrNull()
        if (probe != null) return
        // Re-fetch settings (the relay URL/password may have changed).
        val fresh = runCatching { MachineRegistry.fetchSettings(_token.value) }.getOrNull() ?: return
        adoptSettings(fresh)
        selectBox(
            box.copy(
                relayBaseUrl = fresh.relayUrl ?: box.relayBaseUrl,
                relayPassword = fresh.relayPassword ?: box.relayPassword,
            )
        )
    }

    /** Stream C: on launch, silently connect to a live machine + narrate,
     *  rather than dropping the user on the "Choose machine" wall. */
    fun autoConnectOnLaunch() {
        if (_selectedBoxId.value != null) return
        if (_autoConnecting.value) return
        _autoConnecting.value = true
        scope.launch(Dispatchers.IO) {
            val boxes = _boxes.value
            val preferredIds = listOfNotNull(_settings.value?.primaryDeviceId, _settings.value?.secondaryDeviceId)
            val ordered = (preferredIds.mapNotNull { id -> boxes.firstOrNull { it.id == id } } + boxes)
                .distinctBy { it.id }
            for (box in ordered) {
                _autoConnectTarget.value = box.name
                val probe = runCatching { clientFor(box).info() }.getOrNull()
                if (probe != null) {
                    _selectedBoxId.value = box.id
                    prefs.edit().putString(SELECTED_BOX_ID, box.id).apply()
                    break
                }
            }
            _autoConnecting.value = false
        }
    }

    fun cancelAutoConnect() {
        _autoConnecting.value = false
        _autoConnectTarget.value = null
    }

    private fun adoptSettings(settings: UserSettings) {
        _settings.value = settings
        settings.appearanceThemeBySurface
            .lastOrNull { it.surface == TV_SURFACE_ID }
            ?.theme
            ?.takeIf { it == "light" || it == "dark" }
            ?.let {
                _appearanceTheme.value = it
                prefs.edit().putString(APPEARANCE_THEME_KEY, it).apply()
            }
    }

    suspend fun setAppearanceTheme(theme: String) {
        val next = theme.takeIf { it == "light" || it == "dark" }
            ?: throw IllegalArgumentException("Unknown appearance theme")
        val previous = _appearanceTheme.value
        _appearanceTheme.value = next
        prefs.edit().putString(APPEARANCE_THEME_KEY, next).apply()
        try {
            MachineRegistry.writeSetting(
                _token.value,
                JSONObject().put(
                    "appearanceThemeForSurface",
                    JSONObject().put("surface", TV_SURFACE_ID).put("theme", next),
                ),
            )
        } catch (error: Throwable) {
            _appearanceTheme.value = previous
            prefs.edit().putString(APPEARANCE_THEME_KEY, previous).apply()
            throw error
        }
    }

    // ── Persistence ───────────────────────────────────────────────────────

    private fun loadBoxes(): List<BoxTarget> {
        val raw = prefs.getString(BOXES_KEY, null) ?: return emptyList()
        return runCatching {
            val arr = JSONArray(raw)
            (0 until arr.length()).mapNotNull { i ->
                val o = arr.optJSONObject(i) ?: return@mapNotNull null
                val id = o.optString("id")
                if (id.isEmpty()) return@mapNotNull null
                BoxTarget(
                    id = id,
                    name = o.optString("name"),
                    alias = o.optString("alias").ifEmpty { null },
                    host = o.optString("host"),
                    port = o.optInt("port", AGENT_PORT),
                    managed = if (o.has("managed")) o.optBoolean("managed") else null,
                    machineId = o.optString("machineId").ifEmpty { null },
                    relayBaseUrl = o.optString("relayBaseUrl").ifEmpty { null },
                    relayPassword = o.optString("relayPassword").ifEmpty { null },
                )
            }
        }.getOrDefault(emptyList())
    }

    private fun saveBoxes() {
        val arr = JSONArray()
        _boxes.value.forEach { b ->
            val o = JSONObject()
                .put("id", b.id)
                .put("name", b.name)
                .put("host", b.host)
                .put("port", b.port)
            b.alias?.let { o.put("alias", it) }
            b.managed?.let { o.put("managed", it) }
            b.machineId?.let { o.put("machineId", it) }
            b.relayBaseUrl?.let { o.put("relayBaseUrl", it) }
            b.relayPassword?.let { o.put("relayPassword", it) }
            arr.put(o)
        }
        prefs.edit().putString(BOXES_KEY, arr.toString()).apply()
    }

    companion object {
        private const val PREFS = "io.yaver.tv"
        private const val BOXES_KEY = "yaver.tv.boxes"
        private const val SELECTED_BOX_ID = "yaver.tv.selectedBox"
        private const val APPEARANCE_THEME_KEY = "yaver.tv.appearanceTheme"
    }
}
