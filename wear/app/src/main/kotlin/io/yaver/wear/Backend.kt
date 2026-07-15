package io.yaver.wear

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import okhttp3.FormBody
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * Standalone-mode authentication: RFC 8628 OAuth 2.0 Device Authorization Grant
 * against Convex — identical in shape to `mobile/src/lib/tvSignIn.ts` and the
 * tvOS `Backend.swift`. Only needed when the watch runs WITHOUT a paired phone
 * (phone-paired mode holds no token; the phone is the brain-of-record).
 *
 *   POST /auth/device-code            → { user_code, verification_uri, device_code, interval, expires_in }
 *   GET  /auth/device-code/poll?device_code=...  → { status: "pending"|"approved", session_token? }
 *
 * Flow on the watch: show the short [DeviceCode.userCode] + a QR of the
 * verification URI (SignInScreen), poll until approved, persist the returned
 * 1-year session token in the watch's secure store, then use it as the Bearer in
 * [AgentClient]. The watch holds NOTHING until the user explicitly opts into
 * standalone use (design §8 "standalone token custody").
 */
class Backend(
    /** Convex deployment origin, e.g. "https://<deployment>.convex.site". */
    private val convexOrigin: String = DEFAULT_CONVEX_ORIGIN,
) {

    companion object {
        /** Public Convex deployment origin. Mirrors the tvOS / watchOS
         *  `Backend.convexSiteURL` and mobile CONVEX_SITE_URL — NOT a secret
         *  (it's the public backend host). Bump here and in the Swift constants
         *  together if the deployment ever moves. */
        const val DEFAULT_CONVEX_ORIGIN = "https://perceptive-minnow-557.eu-west-1.convex.site"
    }

    private val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .build()

    data class DeviceCode(
        val userCode: String,
        val verificationUri: String,
        val deviceCode: String,
        val intervalSeconds: Int,
        val expiresInSeconds: Int,
    )

    sealed class PollResult {
        data class Approved(val sessionToken: String) : PollResult()
        object Pending : PollResult()
        data class Failed(val reason: String) : PollResult()
    }

    /** Start the device-code flow. Returns the code to display + poll handle. */
    suspend fun requestDeviceCode(): DeviceCode = withContext(Dispatchers.IO) {
        val request = Request.Builder()
            .url(convexOrigin.trimEnd('/') + "/auth/device-code")
            .post(FormBody.Builder().add("client", "wear").build())
            .build()
        http.newCall(request).execute().use { resp ->
            val text = resp.body?.string().orEmpty()
            if (!resp.isSuccessful) {
                throw IllegalStateException("device-code request failed: ${resp.code}")
            }
            val obj = JSONObject(text)
            DeviceCode(
                userCode = obj.optString("user_code"),
                verificationUri = obj.optString("verification_uri"),
                deviceCode = obj.optString("device_code"),
                intervalSeconds = obj.optInt("interval", 5),
                expiresInSeconds = obj.optInt("expires_in", 900),
            )
        }
    }

    /** Single poll tick. Caller loops at [DeviceCode.intervalSeconds]. */
    suspend fun pollOnce(deviceCode: String): PollResult = withContext(Dispatchers.IO) {
        try {
            val url = convexOrigin.trimEnd('/') +
                "/auth/device-code/poll?device_code=" + deviceCode
            val request = Request.Builder().url(url).get().build()
            http.newCall(request).execute().use { resp ->
                val text = resp.body?.string().orEmpty()
                if (!resp.isSuccessful) return@use PollResult.Failed("poll http ${resp.code}")
                val obj = JSONObject(text)
                when (obj.optString("status")) {
                    "approved" -> {
                        val token = obj.optString("session_token")
                        if (token.isNotEmpty()) PollResult.Approved(token)
                        else PollResult.Failed("approved without token")
                    }
                    "pending" -> PollResult.Pending
                    else -> PollResult.Pending
                }
            }
        } catch (e: Throwable) {
            PollResult.Failed(e.message ?: "poll error")
        }
    }

    /**
     * Convenience: poll until approved/expired. Returns the session token on
     * success, or null on timeout/failure. SignInScreen can call this directly
     * and react to the result.
     */
    suspend fun pollUntilApproved(code: DeviceCode): String? {
        val deadline = System.currentTimeMillis() + code.expiresInSeconds * 1000L
        while (System.currentTimeMillis() < deadline) {
            when (val r = pollOnce(code.deviceCode)) {
                is PollResult.Approved -> return r.sessionToken
                is PollResult.Failed -> return null
                PollResult.Pending -> delay(code.intervalSeconds * 1000L)
            }
        }
        return null
    }

    /**
     * Extend the standalone 1-year session on launch so an opted-in watch NEVER
     * re-prompts for OAuth — the Netflix contract. Only relevant in standalone
     * mode (phone-paired mode holds no token). Device-code mints a 1-year token
     * but nothing extends it; without this it silently hard-expires and forces a
     * fresh sign-in.
     *
     * Extend-only, NO rotation (no X-Yaver-Rotate-Token): a wrist on flaky Wi-Fi
     * routinely loses the response, and rotating would strand it on a dead token
     * → a false logout of a live session. Mirrors mobile's deliberate no-rotate
     * decision (mobile/src/lib/auth.ts, root-caused 2026-07-15) and the tvOS /
     * watchOS Backend.refreshSession. Security: no wider blast radius — the token
     * already lives a year in the watch's own store; we only reset the clock.
     *
     * Returns the rotated token IF the server ever returns one (it won't without
     * opt-in), else null. Any failure is a silent no-op — the existing token
     * stays valid.
     */
    suspend fun refreshSession(token: String): String? = withContext(Dispatchers.IO) {
        try {
            val request = Request.Builder()
                .url(convexOrigin.trimEnd('/') + "/auth/refresh")
                .header("Authorization", "Bearer $token")
                .header("X-Yaver-Surface", "watch")
                .post(FormBody.Builder().build())
                .build()
            http.newCall(request).execute().use { resp ->
                if (!resp.isSuccessful) return@use null
                val text = resp.body?.string().orEmpty()
                val rotated = JSONObject(text).optString("token")
                if (rotated.isNotEmpty()) rotated else null
            }
        } catch (e: Throwable) {
            null
        }
    }
}
