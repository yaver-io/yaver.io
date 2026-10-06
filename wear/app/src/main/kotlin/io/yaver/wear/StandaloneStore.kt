package io.yaver.wear

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey

/**
 * Tiny SharedPreferences wrapper for standalone-mode credentials.
 *
 * In the DEFAULT phone-paired mode the watch holds NOTHING — no token, no box
 * host. Standalone is an explicit opt-in ("use without your phone"), and THIS
 * is the only place the watch keeps a session token + box URL.
 *
 * Mirrors watchOS's @AppStorage("yaver.watch.token") / @AppStorage("yaver.watch.box")
 * (watch/YaverWatch/WatchStore.swift). Same keys so a future cross-platform
 * migration is frictionless.
 */
object StandaloneStore {

    private const val PREFS = "io.yaver.wear.standalone"
    private const val SECURE_PREFS = "io.yaver.wear.standalone.secure"
    private const val KEY_TOKEN = "yaver.watch.token"
    private const val KEY_BOX_URL = "yaver.watch.boxUrl"
    private const val KEY_OPT_IN = "yaver.watch.standaloneOptIn"
    private const val KEY_MACHINE_ID = "yaver.watch.machineId"
    private const val KEY_RELAY_BASE_URL = "yaver.watch.relayBaseUrl"
    private const val KEY_RELAY_PASSWORD = "yaver.watch.relayPassword"
    private const val KEY_APPEARANCE = "yaver.watch.appearance"
    private const val KEY_PRIVATE_VPS_URL = "yaver.privateVpsUrl"

    private fun prefs(ctx: Context): SharedPreferences {
        val app = ctx.applicationContext
        return try {
            val key = MasterKey.Builder(app)
                .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
                .build()
            val secure = EncryptedSharedPreferences.create(
                app,
                SECURE_PREFS,
                key,
                EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
            )
            migrateLegacy(app, secure)
            secure
        } catch (_: Throwable) {
            // Do not strand an already-signed-in wrist if AndroidX Security is
            // unavailable on a particular Wear image. The normal path above is
            // encrypted; this is a compatibility fallback.
            app.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        }
    }

    private fun legacyPrefs(ctx: Context): SharedPreferences =
        ctx.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    private fun migrateLegacy(ctx: Context, secure: SharedPreferences) {
        val legacy = legacyPrefs(ctx)
        if (legacy.all.isEmpty()) return
        val edit = secure.edit()
        for ((key, value) in legacy.all) {
            when (value) {
                is String -> edit.putString(key, value)
                is Boolean -> edit.putBoolean(key, value)
            }
        }
        edit.apply()
        legacy.edit().clear().apply()
    }

    /** The standalone session token (from device-code auth). Empty = not signed in. */
    fun token(ctx: Context): String = prefs(ctx).getString(KEY_TOKEN, "") ?: ""

    /** The box base URL, e.g. "http://192.168.1.50:18080". Empty = not configured. */
    fun boxUrl(ctx: Context): String = prefs(ctx).getString(KEY_BOX_URL, "") ?: ""

    /** Whether the user opted into "use without your phone" mode. */
    fun optIn(ctx: Context): Boolean = prefs(ctx).getBoolean(KEY_OPT_IN, false)

    /** The managed machine id of the box, if known. Empty = unknown; the phone
     *  resolves the target box from the user's current/primary machine when the
     *  wake intent carries an empty id. */
    fun machineId(ctx: Context): String = prefs(ctx).getString(KEY_MACHINE_ID, "") ?: ""

    /** Relay HTTPS origin, e.g. https://relay.yaver.io. Empty = LAN only. */
    fun relayBaseUrl(ctx: Context): String = prefs(ctx).getString(KEY_RELAY_BASE_URL, "") ?: ""

    /** Per-user relay password. Empty = no relay fallback. */
    fun relayPassword(ctx: Context): String = prefs(ctx).getString(KEY_RELAY_PASSWORD, "") ?: ""

    fun appearanceTheme(ctx: Context): String =
        if (prefs(ctx).getString(KEY_APPEARANCE, "dark") == "light") "light" else "dark"

    fun privateVpsUrl(ctx: Context): String = prefs(ctx).getString(KEY_PRIVATE_VPS_URL, "") ?: ""

    fun setPrivateVpsUrl(ctx: Context, value: String?): Boolean {
        if (value == null) {
            prefs(ctx).edit().remove(KEY_PRIVATE_VPS_URL).apply()
            return true
        }
        val normalized = Backend.normalizePrivateVpsUrl(value) ?: return false
        prefs(ctx).edit().putString(KEY_PRIVATE_VPS_URL, normalized).apply()
        return true
    }

    fun setAppearanceTheme(ctx: Context, theme: String) {
        prefs(ctx).edit().putString(KEY_APPEARANCE, if (theme == "light") "light" else "dark").apply()
    }

    /** Persist standalone creds (called after device-code auth succeeds).
     *  `machineId` is optional — pass the managed machine id so the wrist can
     *  route a targeted wake; empty is fine (the phone resolves it). */
    fun save(
        ctx: Context,
        token: String,
        boxUrl: String,
        machineId: String = "",
        relayBaseUrl: String = relayBaseUrl(ctx),
        relayPassword: String = relayPassword(ctx),
    ) {
        prefs(ctx).edit()
            .putString(KEY_TOKEN, token)
            .putString(KEY_BOX_URL, boxUrl)
            .putString(KEY_MACHINE_ID, machineId)
            .putString(KEY_RELAY_BASE_URL, relayBaseUrl)
            .putString(KEY_RELAY_PASSWORD, relayPassword)
            .apply()
    }

    /** Set the opt-in flag (called from Settings when the user toggles standalone). */
    fun setOptIn(ctx: Context, on: Boolean) {
        prefs(ctx).edit().putBoolean(KEY_OPT_IN, on).apply()
    }

    /** Clear all standalone creds (sign out). */
    fun clear(ctx: Context) {
        val appearance = appearanceTheme(ctx)
        val privateVps = privateVpsUrl(ctx)
        prefs(ctx).edit().clear().putString(KEY_APPEARANCE, appearance)
            .apply { if (privateVps.isNotEmpty()) putString(KEY_PRIVATE_VPS_URL, privateVps) }
            .apply()
    }

    /** True when standalone transport is viable: opted in + has token + has box URL. */
    fun isReady(ctx: Context): Boolean =
        optIn(ctx) && token(ctx).isNotEmpty() && boxUrl(ctx).isNotEmpty()

    fun endpointConfig(ctx: Context): StandaloneEndpointConfig =
        StandaloneEndpointConfig(
            boxBaseUrl = boxUrl(ctx),
            deviceId = machineId(ctx),
            relayBaseUrl = relayBaseUrl(ctx),
            relayPassword = relayPassword(ctx),
        )
}

data class StandaloneEndpointConfig(
    val boxBaseUrl: String,
    val deviceId: String = "",
    val relayBaseUrl: String = "",
    val relayPassword: String = "",
) {
    data class Endpoint(val url: String, val relay: Boolean)

    fun endpoints(path: String): List<Endpoint> {
        val cleanPath = if (path.startsWith("/")) path else "/$path"
        val out = mutableListOf<Endpoint>()
        val lan = boxBaseUrl.trimEnd('/')
        if (lan.isNotEmpty()) out += Endpoint(lan + cleanPath, false)
        val relay = relayBaseUrl.trimEnd('/')
        if (relay.isNotEmpty() && deviceId.isNotEmpty()) {
            val sep = if (cleanPath.contains("?")) "&" else "?"
            val pw = relayPassword.takeIf { it.isNotEmpty() }?.let {
                sep + "__rp=" + java.net.URLEncoder.encode(it, "UTF-8")
            }.orEmpty()
            out += Endpoint("$relay/d/$deviceId$cleanPath$pw", true)
        }
        return out
    }
}
