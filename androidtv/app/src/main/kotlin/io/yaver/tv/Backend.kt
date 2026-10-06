package io.yaver.tv

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.net.URLEncoder
import java.util.concurrent.TimeUnit

/**
 * Backend — Convex origin + RFC 8628 device-code sign-in.
 *
 * Mirrors tvos/YaverTV/Backend.swift exactly (same Convex HTTP contract that
 * `yaver auth` and the CLI device-code flow use):
 *   POST /auth/device-code                        -> { userCode, deviceCode, expiresAt }
 *   GET  /auth/device-code/poll?device_code=...   -> { status, token? }
 * A phone already signed in approves via app/approve-device.
 */

/** The result of one poll/event tick, with the LAN-approval + unreachable
 *  distinctions that make the sign-in screen honest. */
data class DevicePollResult(
    val status: String, // "pending" | "authorized" | "expired"
    val token: String? = null,
    val claimHandle: String? = null,
    val claimRequired: Boolean? = null,
    val lanPending: LanPendingInfo? = null,
    /** Set when the poll never got an answer — the TV must say "Can't reach
     *  Yaver", NOT "Waiting for approval…". */
    val unreachableReason: String? = null,
)

data class LanPendingInfo(
    val approverEmail: String? = null,
    val matchCode: String? = null,
    val expiresAt: Double? = null,
)

data class DeviceCodeStart(
    val userCode: String,
    val deviceCode: String,
    val expiresAt: Double,
    val approveNonce: String? = null,
    val matchCode: String? = null,
) {
    /** QR target that routes a scan into the phone approver. */
    val verifyUrl: String
        get() = "${Backend.webBase}/auth/device?code=${URLEncoder.encode(userCode, "UTF-8")}"
}

sealed class EmailAuthError : Exception() {
    class InvalidCredentials : EmailAuthError()
    class LockedOut : EmailAuthError()
    class RequiresTwoFactor : EmailAuthError()
    class Server(override val message: String) : EmailAuthError()
}

object Backend {
    private const val HOSTED_ORIGIN = "https://perceptive-minnow-557.eu-west-1.convex.site"
    private const val SETTINGS_PREFS = "io.yaver.tv"
    private const val PRIVATE_VPS_KEY = "yaver.privateVpsUrl"
    private var appContext: Context? = null

    fun configure(context: Context) { appContext = context.applicationContext }

    val origin: String
        get() = appContext?.getSharedPreferences(SETTINGS_PREFS, Context.MODE_PRIVATE)
            ?.getString(PRIVATE_VPS_KEY, null)
            ?.let(::normalizePrivateVpsUrl) ?: HOSTED_ORIGIN

    val webBase: String get() = privateVpsUrl() ?: WEB_BASE

    fun privateVpsUrl(): String? = appContext?.getSharedPreferences(SETTINGS_PREFS, Context.MODE_PRIVATE)
        ?.getString(PRIVATE_VPS_KEY, null)?.let(::normalizePrivateVpsUrl)

    fun savePrivateVpsUrl(value: String?): Boolean {
        val context = appContext ?: return false
        val prefs = context.getSharedPreferences(SETTINGS_PREFS, Context.MODE_PRIVATE)
        if (value == null) { prefs.edit().remove(PRIVATE_VPS_KEY).apply(); return true }
        val normalized = privateVpsUrlFromQr(value) ?: return false
        prefs.edit().putString(PRIVATE_VPS_KEY, normalized).apply()
        return true
    }

    fun normalizePrivateVpsUrl(value: String): String? = runCatching {
        val uri = java.net.URI(value.trim())
        if (uri.scheme != "https") return null
        if (uri.host.isNullOrBlank() || uri.userInfo != null || uri.query != null || uri.fragment != null) return null
        val path = (uri.path ?: "").trimEnd('/')
        java.net.URI(uri.scheme, null, uri.host, uri.port, path, null, null).toString().trimEnd('/')
    }.getOrNull()

    fun privateVpsUrlFromQr(value: String): String? {
        normalizePrivateVpsUrl(value)?.let { return it }
        runCatching {
            val uri = java.net.URI(value.trim())
            if (uri.scheme == "yaver" && uri.host == "private-vps") {
                val candidate = uri.rawQuery.orEmpty().split('&').mapNotNull {
                    val pair = it.split('=', limit = 2)
                    if (pair.firstOrNull() == "url") pair.getOrNull(1) else null
                }.firstOrNull()?.let { java.net.URLDecoder.decode(it, "UTF-8") }
                normalizePrivateVpsUrl(candidate.orEmpty())?.let { return it }
            }
        }
        return runCatching {
            val objectValue = JSONObject(value)
            normalizePrivateVpsUrl(objectValue.optString("privateVpsUrl").ifEmpty { objectValue.optString("url") })
        }.getOrNull()
    }

    private val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    private val JSON = "application/json; charset=utf-8".toMediaType()

    // ── Device-code flow ──────────────────────────────────────────────────

    /** Start the device-code flow. `machineName` registers the device as; the
     *  ownerUserIdHint (when the TV remembers its last owner) gives the owner's
     *  phone a proactive approve event — the hint grants nothing. */
    suspend fun startDeviceCode(
        appContext: Context,
        machineName: String,
        platform: String,
        environment: String,
    ): DeviceCodeStart = withContext(Dispatchers.IO) {
        val body = JSONObject()
            .put("machineName", machineName)
            .put("platform", platform)
            .put("environment", environment)
            .put("deviceId", TokenStore.installationId(appContext))
        lastOwnerUserId(appContext)?.let { body.put("ownerUserIdHint", it) }
        val request = Request.Builder()
            .url("$origin/auth/device-code")
            .header("Content-Type", "application/json")
            .post(body.toString().toRequestBody(JSON))
            .build()
        http.newCall(request).execute().use { resp ->
            if (!resp.isSuccessful) {
                throw IllegalStateException("Couldn't start sign-in (${resp.code}). Check your connection.")
            }
            val o = JSONObject(resp.body?.string().orEmpty())
            val userCode = o.optString("userCode")
            val deviceCode = o.optString("deviceCode")
            if (userCode.isEmpty() || deviceCode.isEmpty()) {
                throw IllegalStateException("device-code response missing userCode/deviceCode")
            }
            DeviceCodeStart(
                userCode = userCode,
                deviceCode = deviceCode,
                expiresAt = o.optDouble("expiresAt", System.currentTimeMillis().toDouble() + 900_000),
                approveNonce = o.optString("approveNonce").ifEmpty { null },
                matchCode = o.optString("matchCode").ifEmpty { null },
            )
        }
    }

    /** Single poll tick. Any transport failure returns Pending WITH
     *  unreachableReason so the screen says "Can't reach Yaver", not
     *  "Waiting for approval…". */
    suspend fun pollDeviceCode(deviceCode: String): DevicePollResult = withContext(Dispatchers.IO) {
        try {
            val url = "$origin/auth/device-code/poll?device_code=${URLEncoder.encode(deviceCode, "UTF-8")}"
            http.newCall(Request.Builder().url(url).get().build()).execute().use { resp ->
                if (!resp.isSuccessful) {
                    return@use DevicePollResult("pending", unreachableReason = "server returned HTTP ${resp.code}")
                }
                parsePollBody(resp.body?.string().orEmpty())
            }
        } catch (e: Throwable) {
            DevicePollResult("pending", unreachableReason = e.message ?: "network error")
        }
    }

    /** Event-first wait: the backend holds the request until the code changes
     *  (or times out), then closes it. Parse the last SSE data line. */
    suspend fun waitDeviceCodeEvent(deviceCode: String): DevicePollResult = withContext(Dispatchers.IO) {
        try {
            val url = "$origin/auth/device-code/events?device_code=${URLEncoder.encode(deviceCode, "UTF-8")}"
            http.newCall(Request.Builder().url(url).get().build()).execute().use { resp ->
                if (!resp.isSuccessful) {
                    return@use DevicePollResult("pending", unreachableReason = "server returned HTTP ${resp.code}")
                }
                val text = resp.body?.string().orEmpty()
                val lastData = text.lineSequence()
                    .filter { it.startsWith("data:") }
                    .map { it.removePrefix("data:").trim() }
                    .lastOrNull()
                if (lastData == null) return@use DevicePollResult("pending")
                parsePollBody(lastData)
            }
        } catch (e: Throwable) {
            DevicePollResult("pending", unreachableReason = e.message ?: "network error")
        }
    }

    /** Claim the session token after approval. */
    suspend fun claimDeviceCode(deviceCode: String, claimHandle: String?): DevicePollResult =
        withContext(Dispatchers.IO) {
            try {
                val body = JSONObject().put("deviceCode", deviceCode)
                if (!claimHandle.isNullOrEmpty()) body.put("claimHandle", claimHandle)
                val request = Request.Builder()
                    .url("$origin/auth/device-code/claim")
                    .header("Content-Type", "application/json")
                    .post(body.toString().toRequestBody(JSON))
                    .build()
                http.newCall(request).execute().use { resp ->
                    if (!resp.isSuccessful) {
                        return@use DevicePollResult("pending", unreachableReason = "server returned HTTP ${resp.code}")
                    }
                    parsePollBody(resp.body?.string().orEmpty())
                }
            } catch (e: Throwable) {
                DevicePollResult("pending", unreachableReason = e.message ?: "network error")
            }
        }

    /** LAN approval, phase 2: the TV shows "Approve sign-in from <email>?" and
     *  the user presses Allow/Deny. */
    data class LanConfirmResult(
        val ok: Boolean = false,
        val denied: Boolean = false,
        val claimHandle: String? = null,
        val reason: String? = null,
    )

    suspend fun lanConfirm(deviceCode: String, allow: Boolean): LanConfirmResult =
        withContext(Dispatchers.IO) {
            try {
                val body = JSONObject().put("deviceCode", deviceCode).put("allow", allow)
                val request = Request.Builder()
                    .url("$origin/auth/device-code/lan-confirm")
                    .header("Content-Type", "application/json")
                    .post(body.toString().toRequestBody(JSON))
                    .build()
                http.newCall(request).execute().use { resp ->
                    if (!resp.isSuccessful) {
                        return@use LanConfirmResult(reason = "HTTP ${resp.code}")
                    }
                    val o = JSONObject(resp.body?.string().orEmpty())
                    LanConfirmResult(
                        ok = o.optBoolean("ok", false),
                        denied = o.optBoolean("denied", false),
                        claimHandle = o.optString("claimHandle").ifEmpty { null },
                        reason = o.optString("reason").ifEmpty { null },
                    )
                }
            } catch (e: Throwable) {
                LanConfirmResult(reason = e.message ?: "network error")
            }
        }

    /** Extend the 1-year session on launch so a lean-back device NEVER
     *  re-prompts — the Netflix contract. Extend-only, NO rotation. */
    suspend fun refreshSession(appContext: Context, token: String): String? = withContext(Dispatchers.IO) {
        try {
            val request = Request.Builder()
                .url("$origin/auth/refresh")
                .header("Authorization", "Bearer $token")
                .header("X-Yaver-Surface", TV_SURFACE_ID)
                .post(ByteArray(0).toRequestBody(JSON))
                .build()
            http.newCall(request).execute().use { resp ->
                if (!resp.isSuccessful) return@use null
                val o = JSONObject(resp.body?.string().orEmpty())
                val userId = o.optString("userId").ifEmpty { null }
                if (!userId.isNullOrEmpty()) {
                    appContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
                        .edit().putString(LAST_OWNER_USER_ID, userId).apply()
                }
                o.optString("token").ifEmpty { null }
            }
        } catch (e: Throwable) {
            null
        }
    }

    /** Best-effort backend revocation; local sign-out never depends on it. */
    suspend fun revokeSession(token: String) = withContext(Dispatchers.IO) {
        if (token.isEmpty()) return@withContext
        runCatching {
            val request = Request.Builder()
                .url("$origin/auth/logout")
                .header("Authorization", "Bearer $token")
                .header("X-Yaver-Surface", TV_SURFACE_ID)
                .post(ByteArray(0).toRequestBody(JSON))
                .build()
            http.newCall(request).execute().close()
        }
        Unit
    }

    // ── Email/password (option one) ───────────────────────────────────────

    /** POST /auth/login {email, password} → {token} | {requires2fa} | error.
     *  Rate-limited server-side (429) and gated by the deployment's
     *  email-password allowlist (403 carries the server's message verbatim). */
    suspend fun emailSignIn(email: String, password: String): String = withContext(Dispatchers.IO) {
        val body = JSONObject()
            .put("email", email.trim())
            .put("password", password)
        val request = Request.Builder()
            .url("$origin/auth/login")
            .header("Content-Type", "application/json")
            .post(body.toString().toRequestBody(JSON))
            .build()
        http.newCall(request).execute().use { resp ->
            val text = resp.body?.string().orEmpty()
            val o = runCatching { JSONObject(text) }.getOrNull()
            when {
                resp.code == 429 -> throw EmailAuthError.LockedOut()
                resp.code == 401 -> throw EmailAuthError.InvalidCredentials()
                !resp.isSuccessful ->
                    throw EmailAuthError.Server(o?.optString("error") ?: "Email sign-in failed (${resp.code}).")
            }
            if (o?.optBoolean("requires2fa") == true) throw EmailAuthError.RequiresTwoFactor()
            val token = o?.optString("token").orEmpty()
            if (token.isEmpty()) throw EmailAuthError.Server("Yaver didn't return a session token.")
            token
        }
    }

    // ── Owner hint ────────────────────────────────────────────────────────

    private const val PREFS = "io.yaver.tv"
    private const val LAST_OWNER_USER_ID = "yaver.tv.lastOwnerUserId"

    fun lastOwnerUserId(context: Context): String? {
        val v = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(LAST_OWNER_USER_ID, null)
        return v?.takeIf { it.isNotEmpty() }
    }

    // ── Internal ──────────────────────────────────────────────────────────

    private fun parsePollBody(text: String): DevicePollResult {
        val o = runCatching { JSONObject(text) }.getOrNull() ?: return DevicePollResult(
            "pending",
            unreachableReason = "bad JSON from Yaver",
        )
        val status = o.optString("status")
        if (status.isEmpty()) return DevicePollResult("pending", unreachableReason = "unexpected poll response")
        val lanPending = o.optJSONObject("lanPending")?.let { lp ->
            LanPendingInfo(
                approverEmail = lp.optString("approverEmail").ifEmpty { null },
                matchCode = lp.optString("matchCode").ifEmpty { null },
                expiresAt = if (lp.has("expiresAt")) lp.optDouble("expiresAt") else null,
            )
        }
        return DevicePollResult(
            status = status,
            token = o.optString("token").ifEmpty { null },
            claimHandle = o.optString("claimHandle").ifEmpty { null },
            claimRequired = if (o.has("claimRequired")) o.optBoolean("claimRequired") else null,
            lanPending = lanPending,
        )
    }
}
