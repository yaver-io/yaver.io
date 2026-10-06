package io.yaver.tv

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * The wake ladder — same labels, same order, same percents as every other
 * surface (mobile, web, tvOS, watch, wear):
 *
 *   Asleep → Waking → Restoring → Booting → Connecting → Online → Ready
 *   (percents 0, 8, 22, 52, 80, 94, 100)
 *
 * Mirrors tvos/YaverTV/BoxLifecycle.swift. The TV can't see the control
 * plane's per-step provision phase like mobile polls off /subscription, so it
 * drives the ladder off the one signal it CAN observe itself: the box
 * answering GET /health.
 */
enum class BoxPhase(val short: String, val label: String, val percent: Int) {
    Asleep("Asleep", "Asleep — parked to save cost", 0),
    Waking("Waking", "Waking your box…", 8),
    Restoring("Restoring", "Recreating from the latest snapshot…", 22),
    Booting("Booting", "Booting the machine…", 52),
    Connecting("Connecting", "Connecting over the free relay…", 80),
    Online("Online", "Network connected — finishing up…", 94),
    Ready("Ready", "Ready", 100),
    NeedsAuth("Sign-in needed", "Awake, but signed out — sign it in from your phone", 80);

    val isNetwork: Boolean get() = this == Connecting || this == Online || this == Ready

    companion object {
        /** Ordered wake steps for the stepper (drops asleep/waking ends). */
        val WAKE_STEPS = listOf(Restoring, Booting, Connecting, Online, Ready)
    }
}

data class HealthProbe(
    val answered: Boolean = false,
    val authExpired: Boolean = false,
    val clientBlocked: String? = null,
)

/**
 * Drives a box back to life: fires the control-plane resume (POST
 * /billing/yaver-cloud/start), then polls the box's /health every ~4s and
 * advances the phase ladder to Online → Ready. Bounded: a wake that never
 * lands must FAIL, not spin forever.
 */
class BoxLifecycle(private val scope: CoroutineScope) {
    private val _phase = MutableStateFlow(BoxPhase.Asleep)
    val phase: StateFlow<BoxPhase> = _phase.asStateFlow()

    private val _percent = MutableStateFlow(0)
    val percent: StateFlow<Int> = _percent.asStateFlow()

    /** True while a wake run is in flight (drive spinners, disable re-tap). */
    private val _isRunning = MutableStateFlow(false)
    val isRunning: StateFlow<Boolean> = _isRunning.asStateFlow()

    /** Last observed reachability of the tracked box (null = not probed). */
    private val _reachable = MutableStateFlow<Boolean?>(null)
    val reachable: StateFlow<Boolean?> = _reachable.asStateFlow()

    /** Why THIS DEVICE refused the request, when it did — non-nil means the
     *  box was never contacted, so "asleep" and "wake it" are both wrong. */
    private val _clientBlocked = MutableStateFlow<String?>(null)
    val clientBlocked: StateFlow<String?> = _clientBlocked.asStateFlow()

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error.asStateFlow()

    private var box: BoxTarget? = null
    private var floor = 0
    private var pollJob: Job? = null
    private var wakeJob: Job? = null

    private val net: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(5, TimeUnit.SECONDS)
        .build()

    /** A managed box observed unreachable — i.e. auto-parked. */
    val isBoxAsleep: Boolean get() = (box?.managed == true) && _reachable.value == false

    /** The box is unreachable and no wake is running. */
    val needsWake: Boolean get() = _reachable.value == false && !_isRunning.value

    // ── Reachability probe ────────────────────────────────────────────────

    /** One-shot /health probe used by the picker/dashboard. Doesn't wake. */
    fun refreshReachability(target: BoxTarget) {
        box = target
        scope.launch(Dispatchers.IO) {
            val probe = healthProbe(target)
            if (!_isRunning.value) {
                _reachable.value = probe.answered && !probe.authExpired
                _clientBlocked.value = probe.clientBlocked
            }
        }
    }

    /** Mark the box unreachable from an observed failure. */
    fun markUnreachable(target: BoxTarget) {
        box = target
        if (!_isRunning.value) _reachable.value = false
    }

    // ── Wake ──────────────────────────────────────────────────────────────

    /** Resume the box, then poll /health and drive the ladder to Ready. */
    fun wake(target: BoxTarget, token: String) {
        if (_isRunning.value) return
        box = target
        _error.value = null

        if (!target.wakeable || target.machineId.isNullOrEmpty()) {
            _error.value = "This box can't be woken from the TV — start it from your computer or phone."
            return
        }

        _isRunning.value = true
        floor = 0
        setPhase(BoxPhase.Waking)
        pollJob?.cancel()
        val machineId = target.machineId
        wakeJob = scope.launch(Dispatchers.IO) {
            run(target, token, machineId)
        }
    }

    fun cancel() {
        pollJob?.cancel()
        pollJob = null
        _isRunning.value = false
    }

    private suspend fun run(box: BoxTarget, token: String, machineId: String) {
        // 1. Ask the control plane to resume.
        try {
            requestResume(token, machineId)
        } catch (e: Throwable) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            finish(error = e.message ?: "Wake failed")
            return
        }
        if (!_isRunning.value) { finish(error = null); return }
        setPhase(BoxPhase.Restoring)

        // 2. Poll /health until the box answers (max ~3 min), walking
        //    Booting → Connecting while it's still cold.
        val maxTicks = 45
        var ticks = 0
        while (_isRunning.value) {
            val probe = healthProbe(box)
            if (probe.authExpired) {
                setPhase(BoxPhase.NeedsAuth)
                finish(error = "This box is awake but signed out. Sign it in from Yaver on your phone.")
                return
            }
            if (probe.answered) {
                _reachable.value = true
                setPhase(BoxPhase.Online)
                delay(900)
                setPhase(BoxPhase.Ready)
                finish(error = null)
                return
            }
            ticks += 1
            if (ticks >= maxTicks) {
                finish(error = "The box didn't come back within 3 minutes. Try Wake again, or start it from a computer.")
                return
            }
            if (ticks == 1) setPhase(BoxPhase.Booting)
            else if (ticks >= 4) setPhase(BoxPhase.Connecting)
            delay(4000)
        }
        finish(error = null)
    }

    private suspend fun requestResume(token: String, machineId: String) {
        withContext(Dispatchers.IO) {
            val body = JSONObject().put("machineId", machineId).toString()
            val request = Request.Builder()
                .url("${Backend.origin}/billing/yaver-cloud/start")
                .header("Authorization", "Bearer $token")
                .header("X-Yaver-Surface", TV_SURFACE_ID)
                .header("Content-Type", "application/json")
                .post(body.toRequestBody())
                .build()
            net.newCall(request).execute().use { resp ->
                if (!resp.isSuccessful) {
                    val text = resp.body?.string().orEmpty()
                    val msg = runCatching { JSONObject(text).optString("error") }.getOrNull()
                    throw AgentError(msg?.ifEmpty { null } ?: "Wake request failed (${resp.code}).")
                }
            }
        }
    }

    private suspend fun healthProbe(box: BoxTarget): HealthProbe {
        val endpoints = box.requestEndpoints("/health")
        if (endpoints.isEmpty()) return HealthProbe()
        for (endpoint in endpoints) {
            try {
                val rb = Request.Builder().url(endpoint.url)
                if (endpoint.relay) {
                    box.relayPassword?.takeIf { it.isNotEmpty() }?.let { rb.header("X-Relay-Password", it) }
                }
                net.newCall(rb.get().build()).execute().use { resp ->
                    if (resp.code != 200) return@use
                    val obj = runCatching { JSONObject(resp.body?.string().orEmpty()) }.getOrNull()
                    if (obj == null) return HealthProbe(answered = true)
                    val ok = obj.optBoolean("ok", true)
                    var expired = obj.optBoolean("authExpired", false)
                    obj.optJSONObject("lifecycle")?.let { lc ->
                        if (lc.optString("state") == "yaver-auth-expired") expired = true
                        if (lc.optBoolean("usable", true) == false) expired = true
                    }
                    if (obj.optString("lifecycleState") == "yaver-auth-expired") expired = true
                    return HealthProbe(answered = ok, authExpired = expired)
                }
            } catch (e: Throwable) {
                // KEEP THE REASON: a client-side refusal must never surface as
                // "the box did not answer" → "Box asleep".
                val why = FailureSignals.clientPolicyReason(e)
                if (why != null) return HealthProbe(clientBlocked = why)
            }
        }
        return HealthProbe()
    }

    // ── Phase bookkeeping (monotonic — the bar only ever fills) ───────────

    private fun setPhase(p: BoxPhase) {
        _phase.value = p
        floor = maxOf(floor, p.percent)
        _percent.value = floor
    }

    private fun finish(error: String?) {
        _isRunning.value = false
        if (error != null) _error.value = error
    }
}

private fun String.toRequestBody(): okhttp3.RequestBody =
    toRequestBody("application/json; charset=utf-8".toMediaType())
