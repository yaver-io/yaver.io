package io.yaver.wear

import android.content.Context
import android.content.SharedPreferences

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
    private const val KEY_TOKEN = "yaver.watch.token"
    private const val KEY_BOX_URL = "yaver.watch.boxUrl"
    private const val KEY_OPT_IN = "yaver.watch.standaloneOptIn"

    private fun prefs(ctx: Context): SharedPreferences =
        ctx.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    /** The standalone session token (from device-code auth). Empty = not signed in. */
    fun token(ctx: Context): String = prefs(ctx).getString(KEY_TOKEN, "") ?: ""

    /** The box base URL, e.g. "http://192.168.1.50:18080". Empty = not configured. */
    fun boxUrl(ctx: Context): String = prefs(ctx).getString(KEY_BOX_URL, "") ?: ""

    /** Whether the user opted into "use without your phone" mode. */
    fun optIn(ctx: Context): Boolean = prefs(ctx).getBoolean(KEY_OPT_IN, false)

    /** Persist standalone creds (called after device-code auth succeeds). */
    fun save(ctx: Context, token: String, boxUrl: String) {
        prefs(ctx).edit()
            .putString(KEY_TOKEN, token)
            .putString(KEY_BOX_URL, boxUrl)
            .apply()
    }

    /** Set the opt-in flag (called from Settings when the user toggles standalone). */
    fun setOptIn(ctx: Context, on: Boolean) {
        prefs(ctx).edit().putBoolean(KEY_OPT_IN, on).apply()
    }

    /** Clear all standalone creds (sign out). */
    fun clear(ctx: Context) {
        prefs(ctx).edit().clear().apply()
    }

    /** True when standalone transport is viable: opted in + has token + has box URL. */
    fun isReady(ctx: Context): Boolean =
        optIn(ctx) && token(ctx).isNotEmpty() && boxUrl(ctx).isNotEmpty()
}
