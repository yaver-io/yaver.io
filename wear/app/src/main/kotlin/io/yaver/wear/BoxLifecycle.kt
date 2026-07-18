package io.yaver.wear

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * Box wake/park lifecycle model for the wrist.
 *
 * A managed cloud box self-parks after idle to save cost: it snapshots + DELETES
 * its server, so a direct LAN/relay turn just fails. Resuming recreates the
 * server from the latest snapshot (~1-2 min) and it re-registers over the free
 * relay with its persisted token — no re-auth. This file gives the watch a
 * first-class "asleep → waking → … → ready" notion instead of a bare error.
 *
 * PHASE LADDER — the same ORDER and the same short LABELS as every other
 * surface, so "waking up" reads the same on the wrist:
 *   Asleep 0 → Waking 8 → Restoring 22 → Booting 52 → Connecting 80 → Online 94 → Ready 100
 *
 * The PERCENTS are the watch's own and do NOT match mobile/web
 * (`mobile/src/lib/wakeMachineCore.ts` PHASE_META: Booting 40, Connecting 65,
 * Online 86). This comment used to claim they were identical; they never were.
 * The divergence is survivable because the bar is per-surface and nothing
 * compares the two, but do not trust the old claim: if you want them aligned,
 * align the numbers here AND in tvos/ and watch/ — don't just edit the prose.
 *
 * What the WATCH can observe by itself: the box answering
 *   GET http://<box.host>:18080/health  → 200 + {"ok":true}
 * Before that it is Booting/Connecting. We poll ~every 4s and advance the
 * ladder; time drives Waking→Restoring→Booting (the control-plane steps the
 * watch can't see), /health confirms Online→Ready.
 *
 * Wake itself is NOT fired from the watch directly — the watch cannot reach the
 * control plane. It routes the intent through the paired phone over the Data
 * Layer (see [PhoneBridge.sendWakeBox], a wake turn on [WatchProtocol.PATH_TURN]); the phone
 * runs the real resume. This object only drives the on-wrist progress.
 */
object BoxLifecycle {

    /** The wake ladder. `percent` fills the bar; `label` is the short chip word. */
    enum class WakePhase(val label: String, val percent: Int) {
        ASLEEP("Asleep", 0),
        WAKING("Waking", 8),
        RESTORING("Restoring", 22),
        BOOTING("Booting", 52),
        CONNECTING("Connecting", 80),
        ONLINE("Online", 94),
        READY("Ready", 100),
        // NOTE: there is deliberately no NEEDS_AUTH rung here. "The box is up
        // but signed out" is not a step on the way to Ready — it is a terminal
        // state that only the user can clear — so it lives on [WakeStatus] next
        // to PhoneNeeded, which has exactly the same shape. tvOS and watchOS
        // model it as a phase only because their state IS a single phase enum.
    }

    /** The moving steps shown as dots (excludes the resting ASLEEP start). */
    val WAKE_STEPS: List<WakePhase> =
        WakePhase.values().filter { it != WakePhase.ASLEEP }

    /** What the wrist should show about the box's reachability/wake. */
    sealed class WakeStatus {
        /** Box reachable (or nothing to show) — normal UI. */
        object None : WakeStatus()

        /** A turn failed because the box was unreachable — offer "Wake". */
        object Asleep : WakeStatus()

        /** Wake routed through the phone; progress in flight. */
        data class Waking(val phase: WakePhase) : WakeStatus()

        /** Phone unreachable, so we can't route the wake — tell the user. */
        object PhoneNeeded : WakeStatus()

        /**
         * The box came back but its Yaver session expired, so it cannot finish
         * connecting on its own. A task for the user, not a step to wait on.
         *
         * This gap was a real bug, not a cosmetic one. [probeHealth] used to
         * read only `ok`, which the agent returns as `true` even when signed
         * out (it still serves the pairing routes), so a wake against an expired
         * box marched ONLINE → READY → None and re-sent the pending turn to a
         * box that could not run it. Both Swift surfaces already guarded this.
         */
        object NeedsAuth : WakeStatus()
    }

    private val _status = MutableStateFlow<WakeStatus>(WakeStatus.None)
    val status: StateFlow<WakeStatus> = _status

    /** True when a turn failed on an unreachable box and we're offering Wake. */
    val needsWake: Boolean get() = _status.value is WakeStatus.Asleep

    /** True while a wake is actively driving the ladder. */
    val isWaking: Boolean get() = _status.value is WakeStatus.Waking

    private val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(4, TimeUnit.SECONDS)
        .readTimeout(4, TimeUnit.SECONDS)
        .build()

    /** Cancels an in-flight wake run when a new one starts. */
    @Volatile
    private var wakeJob: Job? = null

    // ---- Public state transitions ---------------------------------------

    /** A turn couldn't reach the box → surface "Box asleep — Wake". */
    fun markAsleep() {
        if (isWaking) return // don't stomp an in-flight wake
        _status.value = WakeStatus.Asleep
    }

    /** Phone unreachable — we can't route the wake from the wrist. */
    fun markPhoneNeeded() {
        _status.value = WakeStatus.PhoneNeeded
    }

    /** Clear back to normal UI (box reachable, or user dismissed). */
    fun reset() {
        wakeJob?.cancel()
        wakeJob = null
        _status.value = WakeStatus.None
    }

    // ---- Wake driver -----------------------------------------------------

    /**
     * Drive the wake ladder to Ready after the wake intent has been routed to
     * the phone. Advances Waking→Restoring→Booting on a timer (the control-plane
     * steps the watch can't observe), then confirms Online→Ready via /health.
     *
     * @param boxBaseUrl e.g. "http://192.168.1.50:18080" — the box's LAN base.
     *   May be null in phone-paired mode (no stored box URL); then the ladder is
     *   purely time-based and cannot hard-confirm reachability.
     * @param onReady invoked once the box answers /health (or the optimistic
     *   deadline is reached with no URL) — e.g. to re-send the pending turn.
     * @param onTimeout invoked if the box never came back within [timeoutMs].
     */
    fun startWake(
        scope: CoroutineScope,
        boxBaseUrl: String?,
        pollMs: Long = 4_000,
        timeoutMs: Long = 180_000,
        onReady: () -> Unit = {},
        onTimeout: () -> Unit = {},
    ) {
        wakeJob?.cancel()
        wakeJob = scope.launch {
            setPhase(WakePhase.WAKING)
            // Control-plane steps the wrist can't see — estimate on a timer so
            // the bar moves immediately and honestly (mirrors the phone's ladder).
            delay(2_500); setPhase(WakePhase.RESTORING)
            delay(3_500); setPhase(WakePhase.BOOTING)

            val start = System.currentTimeMillis()
            while (System.currentTimeMillis() - start < timeoutMs) {
                when (if (boxBaseUrl != null) probeHealth(boxBaseUrl) else HealthResult.UNREACHABLE) {
                    HealthResult.USABLE -> {
                        setPhase(WakePhase.ONLINE)
                        delay(800)
                        setPhase(WakePhase.READY)
                        delay(1_200) // brief hold on 100% before clearing
                        _status.value = WakeStatus.None
                        onReady()
                        return@launch
                    }
                    // Answering, but signed out. Waiting cannot fix this, and
                    // re-sending the pending turn would just fail — so stop the
                    // ladder here and say so. Deliberately NOT onReady().
                    HealthResult.SIGNED_OUT -> {
                        _status.value = WakeStatus.NeedsAuth
                        return@launch
                    }
                    HealthResult.UNREACHABLE -> Unit // keep waiting
                }
                // No confirmation yet — escalate Booting → Connecting by elapsed
                // time so the ladder keeps advancing while the box comes up.
                val elapsed = System.currentTimeMillis() - start
                if (elapsed > 25_000 && currentPhase() == WakePhase.BOOTING) {
                    setPhase(WakePhase.CONNECTING)
                }
                // Phone-paired fallback: no URL to confirm with — after an
                // optimistic window, call it ready and let the retry prove it.
                if (boxBaseUrl == null && elapsed > 90_000) {
                    setPhase(WakePhase.READY)
                    delay(1_200)
                    _status.value = WakeStatus.None
                    onReady()
                    return@launch
                }
                delay(pollMs)
            }
            // Never came back in time — drop back to the Asleep affordance.
            _status.value = WakeStatus.Asleep
            onTimeout()
        }
    }

    /**
     * What a /health probe actually told us. Reachable is NOT usable: the agent
     * answers `{"ok":true}` even when signed out, serving nothing but the
     * pairing routes. Collapsing the two into a Boolean is what let a wake
     * declare Ready on a box that could not run a single turn.
     */
    enum class HealthResult { UNREACHABLE, SIGNED_OUT, USABLE }

    /** GET /health, classified. Never throws. */
    suspend fun probeHealth(boxBaseUrl: String): HealthResult = withContext(Dispatchers.IO) {
        try {
            val req = Request.Builder()
                .url(boxBaseUrl.trimEnd('/') + "/health")
                .get()
                .build()
            http.newCall(req).execute().use { resp ->
                if (resp.code != 200) return@use HealthResult.UNREACHABLE
                val body = resp.body?.string().orEmpty()
                // 200 with an empty or non-JSON body: reachable, and we have no
                // evidence of a session problem. Treat as usable rather than
                // stranding a healthy box on a sign-in screen it doesn't need.
                if (body.isBlank()) return@use HealthResult.USABLE
                try {
                    val json = JSONObject(body)
                    if (!json.optBoolean("ok", true)) return@use HealthResult.UNREACHABLE
                    val lifecycle = json.optJSONObject("lifecycle")
                    val state = lifecycle?.optString("state").orEmpty()
                    val signedOut =
                        json.optBoolean("authExpired", false) ||
                            json.optBoolean("needsAuth", false) ||
                            lifecycle?.optBoolean("usable", true) == false ||
                            state == "yaver-auth-expired" ||
                            state == "bootstrap"
                    if (signedOut) HealthResult.SIGNED_OUT else HealthResult.USABLE
                } catch (_: Throwable) {
                    HealthResult.USABLE // 200 but non-JSON — still reachable
                }
            }
        } catch (_: Throwable) {
            HealthResult.UNREACHABLE
        }
    }

    private fun currentPhase(): WakePhase? =
        (_status.value as? WakeStatus.Waking)?.phase

    private fun setPhase(phase: WakePhase) {
        _status.value = WakeStatus.Waking(phase)
    }
}
