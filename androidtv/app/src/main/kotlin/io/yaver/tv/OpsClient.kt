package io.yaver.tv

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.TimeUnit

data class HttpResult(val body: ByteArray, val status: Int)

/**
 * OpsClient — the TV's agent transport: LAN-first, relay-second, Bearer auth,
 * with the same relay-credential self-heal as tvOS AgentClient.
 *
 *   POST /ops   { verb, payload, machine: "local" }   — every ops verb
 *   GET /...    REST endpoints (tasks, projects, feedback, …)
 *   SSE GET     event/output/install streams
 *
 * Every request carries X-Yaver-Surface (androidtv) and, on the relay leg only,
 * X-Relay-Password (never in a URL). Two passes max with one relay repair:
 * a stale per-user relay password 401s the relay leg while the box is fine;
 * [relayRepair] re-copies it via POST /settings/repair-relay and endpoints are
 * recomputed.
 */
class OpsClient(
    initialBox: BoxTarget,
    private val token: String,
    private val relayRepair: suspend () -> BoxTarget? = { null },
) {
    @Volatile
    private var box: BoxTarget = initialBox

    val currentBox: BoxTarget get() = box

    private fun clientSessionSettings(): JSONObject = JSONObject()
        .put("appName", "Yaver Android TV")
        .put("appVersion", BuildConfig.VERSION_NAME)
        .put("buildNumber", BuildConfig.VERSION_CODE.toString())
        .put("surface", "android-tv")
        .put("clientSurface", "android-tv")
        .put("platform", "android")
        .put("deviceClass", "tv")
        .put("lane", "yaver-native")
        .put("runtimeMode", "native")
        .put("dogfood", false)
        .put("usageMode", "chat-only")
        .put("chatEnabled", true)
        .put("renderEnabled", false)

    private val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    private val JSON = "application/json; charset=utf-8".toMediaType()

    private fun requestBuilder(endpoint: BoxTarget.Endpoint, path: String): Request.Builder {
        val b = Request.Builder()
            .url(endpoint.url)
            .header("Authorization", "Bearer $token")
            .header("X-Yaver-Surface", TV_SURFACE_ID)
        if (endpoint.relay) {
            box.relayPassword?.takeIf { it.isNotEmpty() }?.let {
                b.header("X-Relay-Password", it)
            }
        }
        return b
    }

    /** Core request: try every endpoint (LAN then relay), with one relay
     *  repair per streak. A 4xx with an `{error}` body is a real refusal from
     *  a reachable agent — throw it, do NOT walk on to the next endpoint. */
    private suspend fun request(method: String, path: String, body: JSONObject?, extraOK: Set<Int> = emptySet()): HttpResult {
        var repairedOnce = false
        var lastError: Throwable = AgentError("request failed: $method $path")
        repeat(2) { pass ->
            val endpoints = box.requestEndpoints(path)
            if (endpoints.isEmpty()) throw AgentError("bad box host")
            var innerError: Throwable? = null
            var repairThisPass = false
            for (endpoint in endpoints) {
                val rb = requestBuilder(endpoint, path)
                val req: Request = when (method) {
                    "GET" -> rb.get().build()
                    else -> rb.method(method, body?.toString()?.toRequestBody(JSON)).build()
                }
                var resp: okhttp3.Response? = null
                try {
                    resp = http.newCall(req).execute()
                    val bytes = resp.body?.bytes() ?: ByteArray(0)
                    if (resp.isSuccessful || extraOK.contains(resp.code)) {
                        return HttpResult(bytes, resp.code)
                    }
                    val errMessage = runCatching {
                        JSONObject(String(bytes)).optString("error").ifEmpty { null }
                    }.getOrNull()
                    if (errMessage != null) {
                        val deny = endpoint.relay && FailureSignals.isRelayCredentialDeny(errMessage)
                        if (deny && !repairedOnce) {
                            // Self-healable: repair once, recompute endpoints.
                            val repaired = relayRepair()
                            if (repaired != null) {
                                box = repaired
                                repairedOnce = true
                                innerError = AgentError(errMessage, relayDeny = true)
                                repairThisPass = true
                                break
                            }
                        }
                        throw AgentError(errMessage, relayDeny = deny)
                    }
                    innerError = AgentError("$path (${resp.code})")
                    // No error body — try the next endpoint.
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Throwable) {
                    innerError = e
                } finally {
                    resp?.close()
                }
            }
            lastError = innerError ?: lastError
            if (!repairThisPass) throw lastError
        }
        throw lastError
    }

    private fun bodyToJson(bytes: ByteArray): JSONObject? =
        runCatching { JSONObject(String(bytes)) }.getOrNull()

    // ── /ops ──────────────────────────────────────────────────────────────

    /** POST /ops. Throws AgentError on `{ok:false}`; returns the parsed
     *  envelope (callers read `initial` where the agent nests streaming
     *  results). */
    suspend fun ops(verb: String, payload: JSONObject): JSONObject = withContext(Dispatchers.IO) {
        val body = JSONObject()
            .put("verb", verb)
            .put("payload", payload)
            .put("machine", "local")
        val result = request("POST", "/ops", body)
        val obj = bodyToJson(result.body) ?: throw AgentError("empty ops response for $verb")
        if (obj.optBoolean("ok", true) == false) {
            throw AgentError(obj.optString("error").ifEmpty { "$verb failed" })
        }
        obj
    }

    suspend fun info(): AgentInfo? {
        val obj = ops("info", JSONObject())
        val initial = obj.optJSONObject("initial") ?: obj
        return parseAgentInfo(initial)
    }

    suspend fun status(): AgentStatus? {
        val obj = ops("status", JSONObject())
        val initial = obj.optJSONObject("initial") ?: obj
        return parseAgentStatus(initial)
    }

    /** List live tmux runner sessions — NOT runner/agents_list, which answers
     *  0 for live runners. */
    suspend fun runnerSessions(): List<RunnerSession> {
        val obj = ops("runner_sessions", JSONObject())
        val initial = obj.optJSONObject("initial") ?: obj
        val sessions = initial.optJSONArray("sessions") ?: JSONArray()
        val out = mutableListOf<RunnerSession>()
        for (i in 0 until sessions.length()) {
            val s = sessions.optJSONObject(i) ?: continue
            val name = s.optString("name").ifEmpty { s.optString("session") }
            if (name.isEmpty()) continue
            out.add(RunnerSession(
                name = name,
                paneId = s.optString("paneId").ifEmpty { null },
                runner = s.optString("runner").ifEmpty { null },
                origin = s.optString("origin").ifEmpty { null },
                inputMode = s.optString("inputMode").ifEmpty { null },
                taskId = s.optString("taskId").ifEmpty { null },
                attached = if (s.has("attached")) s.optBoolean("attached") else null,
            ))
        }
        return out
    }

    suspend fun reload(mode: String) {
        ops("reload", JSONObject().put("mode", mode))
    }

    // ── REST: tasks ───────────────────────────────────────────────────────

    suspend fun getTasks(): List<TaskRow> = withContext(Dispatchers.IO) {
        val result = request("GET", "/tasks", null)
        val obj = bodyToJson(result.body)
        val tasks = obj?.optJSONArray("tasks") ?: runCatching { JSONArray(String(result.body)) }.getOrNull()
        parseTaskRows(tasks)
    }

    suspend fun getTask(taskId: String): JSONObject = withContext(Dispatchers.IO) {
        val result = request("GET", "/tasks/$taskId", null)
        val obj = bodyToJson(result.body) ?: throw AgentError("empty task response")
        obj.optJSONObject("task") ?: obj
    }

    suspend fun createTask(
        title: String,
        description: String,
        model: String? = null,
        runner: String? = null,
        reasoningEffort: String? = null,
        workDir: String? = null,
        projectName: String? = null,
        mode: String? = null,
        goal: String? = null,
        askMode: Boolean = false,
        askFreely: Boolean = false,
        mcpServers: List<String> = emptyList(),
        includeYaverMcp: Boolean = false,
    ): JSONObject = withContext(Dispatchers.IO) {
        val body = JSONObject()
            .put("title", title.ifEmpty { description.take(80) })
            .put("description", description)
        model?.let { body.put("model", it) }
        runner?.let { body.put("runner", it) }
        reasoningEffort?.let { body.put("reasoningEffort", it) }
        workDir?.let { body.put("workDir", it) }
        projectName?.let { body.put("projectName", it) }
        mode?.let { body.put("mode", it) }
        goal?.let { body.put("goal", it) }
        if (askMode) body.put("askMode", true)
        if (askFreely) body.put("askFreely", true)
        if (mcpServers.isNotEmpty()) body.put("mcpServers", JSONArray(mcpServers))
        body.put("includeYaverMcp", includeYaverMcp)
        body.put("sessionSettings", clientSessionSettings())
        val result = request("POST", "/tasks", body)
        val obj = bodyToJson(result.body) ?: throw AgentError("empty task response")
        if (obj.optBoolean("ok", true) == false) throw AgentError(obj.optString("error").ifEmpty { "task failed" })
        obj.optJSONObject("task") ?: obj
    }

    suspend fun continueTask(taskId: String, input: String): JSONObject = withContext(Dispatchers.IO) {
        val result = request("POST", "/tasks/$taskId/continue", JSONObject()
            .put("input", input)
            .put("sessionSettings", clientSessionSettings()))
        val body = bodyToJson(result.body) ?: throw AgentError("empty continue response")
        val execution = body.optJSONObject("executionSession")
        if (body.optString("taskId") != taskId || body.optBoolean("sameTask", true) == false ||
            execution?.optString("taskId") != taskId
        ) {
            throw AgentError("The agent did not confirm the same task and runner session for this follow-up.")
        }
        body
    }

    suspend fun taskRunnerControls(taskId: String): TaskRunnerControlCatalog = withContext(Dispatchers.IO) {
        val result = request("GET", "/tasks/$taskId/control", null)
        val body = bodyToJson(result.body) ?: throw AgentError("empty runner control response")
        parseTaskRunnerControlCatalog(body)
    }

    suspend fun applyTaskRunnerControl(
        taskId: String,
        control: String,
        model: String? = null,
        reasoningEffort: String? = null,
        confirmed: Boolean = false,
    ): JSONObject = withContext(Dispatchers.IO) {
        val requestBody = JSONObject().put("control", control)
        if (!model.isNullOrEmpty()) requestBody.put("model", model)
        if (!reasoningEffort.isNullOrEmpty()) requestBody.put("reasoningEffort", reasoningEffort)
        if (confirmed) requestBody.put("confirmed", true)
        val result = request("POST", "/tasks/$taskId/control", requestBody)
        bodyToJson(result.body) ?: throw AgentError("empty runner control response")
    }

    suspend fun forkTask(taskId: String, input: String, contextWords: Int = 1200): JSONObject =
        withContext(Dispatchers.IO) {
            val result = request(
                "POST",
                "/tasks/$taskId/fork",
                JSONObject().put("input", input).put("contextWords", contextWords),
            )
            bodyToJson(result.body) ?: throw AgentError("empty fork response")
        }

    // ── REST: projects / runners / MCP ────────────────────────────────────

    suspend fun listProjects(): List<ProjectRow> = withContext(Dispatchers.IO) {
        var result = request("GET", "/projects", null)
        var obj = bodyToJson(result.body)
        // Companion scopes may browse projects but cannot mutate inventory.
        // Retry the canonical read once; the agent refreshes its own cache.
        val arr = obj?.optJSONArray("projects")
        if (arr == null || arr.length() == 0) {
            result = request("GET", "/projects", null)
            obj = bodyToJson(result.body)
        }
        val projects = obj?.optJSONArray("projects") ?: JSONArray()
        val out = mutableListOf<ProjectRow>()
        for (i in 0 until projects.length()) {
            val p = projects.optJSONObject(i) ?: continue
            val path = p.optString("path")
            if (path.isEmpty()) continue
            out.add(
                ProjectRow(
                    name = p.optString("name").ifEmpty { path.substringAfterLast('/').ifEmpty { "Project" } },
                    path = path,
                    branch = p.optString("branch").ifEmpty { null },
                    framework = p.optString("framework").ifEmpty { null },
                    gitRemote = p.optString("gitRemote").ifEmpty { null },
                    frameworks = p.optJSONArray("frameworks").toStringList(),
                    surfaces = p.optJSONArray("surfaces").toStringList(),
                    testSurfaces = p.optJSONArray("testSurfaces").toStringList(),
                    isMonorepo = p.optBoolean("isMonorepo", false),
                )
            )
        }
        out
    }

    suspend fun getRunners(): List<RunnerInfo> = withContext(Dispatchers.IO) {
        val result = request("GET", "/agent/runners", null)
        val obj = bodyToJson(result.body) ?: return@withContext emptyList()
        val runners = obj.optJSONArray("runners") ?: obj.optJSONArray("agents") ?: JSONArray()
        val out = mutableListOf<RunnerInfo>()
        for (i in 0 until runners.length()) {
            val r = runners.optJSONObject(i) ?: continue
            val id = r.optString("id").ifEmpty { r.optString("name") }
            if (id.isEmpty()) continue
            val models = r.optJSONArray("models") ?: JSONArray()
            val modelList = mutableListOf<ModelInfo>()
            for (j in 0 until models.length()) {
                val m = models.optJSONObject(j) ?: continue
                modelList.add(
                    ModelInfo(
                        id = m.optString("id"),
                        name = m.optString("name").ifEmpty { null },
                        provider = m.optString("provider").ifEmpty { null },
                        providerName = m.optString("providerName").ifEmpty { null },
                        isDefault = m.optBoolean("isDefault", false),
                        defaultReasoningEffort = m.optString("defaultReasoningEffort").ifEmpty { null },
                        supportedReasoningEfforts = m.optJSONArray("supportedReasoningEfforts")?.let { efforts ->
                            buildList {
                                for (k in 0 until efforts.length()) {
                                    val effort = efforts.optJSONObject(k) ?: continue
                                    val effortId = effort.optString("reasoningEffort")
                                    if (effortId.isNotEmpty()) add(
                                        TaskRunnerReasoningEffort(
                                            id = effortId,
                                            description = effort.optString("description").ifEmpty { null },
                                        )
                                    )
                                }
                            }
                        } ?: emptyList(),
                    )
                )
            }
            out.add(
                RunnerInfo(
                    id = id,
                    installed = r.optBoolean("installed", r.has("installed") || r.has("ready")),
                    ready = if (r.has("ready")) r.optBoolean("ready") else null,
                    isDefault = r.optBoolean("isDefault", false),
                    models = modelList,
                )
            )
        }
        out
    }

    suspend fun listMcpServers(): List<McpServer> = withContext(Dispatchers.IO) {
        val result = request("GET", "/mcp/servers", null)
        val obj = bodyToJson(result.body) ?: return@withContext emptyList()
        val servers = obj.optJSONArray("servers") ?: JSONArray()
        val out = mutableListOf<McpServer>()
        for (i in 0 until servers.length()) {
            val s = servers.optJSONObject(i) ?: continue
            val name = s.optString("name")
            if (name.isEmpty()) continue
            out.add(McpServer(name, s.optBoolean("enabled", false)))
        }
        out
    }

    // ── REST: feedback / capture ──────────────────────────────────────────

    suspend fun getFeedback(): JSONArray = withContext(Dispatchers.IO) {
        val result = request("GET", "/feedback", null)
        bodyToJson(result.body)?.optJSONArray("reports") ?: JSONArray()
    }

    // ── Runner auth (OAuth QR) ────────────────────────────────────────────

    suspend fun runnerAuthBrowserStart(runner: String): JSONObject = withContext(Dispatchers.IO) {
        ops(
            "runner_auth",
            JSONObject().put("op", "browser_start").put("runner", runner),
        )
    }

    suspend fun runnerAuthBrowserStatus(sessionId: String): JSONObject = withContext(Dispatchers.IO) {
        ops(
            "runner_auth",
            JSONObject().put("op", "browser_status").put("sessionId", sessionId),
        )
    }

    // ── SSE streams ───────────────────────────────────────────────────────

    private fun streamRequest(path: String, extraHeaders: Map<String, String> = emptyMap()): Flow<SseEvent> {
        // Endpoint walking: try LAN then relay; use the FIRST endpoint that
        // starts producing. A mid-stream drop is NOT "try the next endpoint".
        val endpoints = box.requestEndpoints(path)
        return kotlinx.coroutines.flow.flow {
            for (endpoint in endpoints) {
                val rb = requestBuilder(endpoint, path).header("Accept", "text/event-stream")
                extraHeaders.forEach { (k, v) -> rb.header(k, v) }
                val connected = try {
                    Sse.stream(http, rb.get().build()).collect { e ->
                        if (!e.data.isNullOrEmpty()) emit(e)
                    }
                    true
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Throwable) {
                    false
                }
                if (connected) return@flow
            }
        }
    }

    /** Live task output: `raw`/`raw_replay`/`output`/`done` frames. The
     *  consumer classifies the end via FailureSignals (no `done` frame ⇒
     *  interrupted ⇒ reattach). */
    fun subscribeTaskOutput(taskId: String, rawSince: Long? = null): Flow<SseEvent> {
        val q = if (rawSince != null) "?rawSince=$rawSince" else ""
        return streamRequest("/tasks/$taskId/output$q")
    }

    /** /dev/events — the dev-server + gap frames (`{type:"error", gap:{…}}`). */
    fun subscribeDevEvents(): Flow<SseEvent> = streamRequest("/dev/events")

    /** A live tmux pane stream for a runner session. */
    fun subscribeTmuxPane(session: String): Flow<SseEvent> =
        streamRequest("/tmux/stream?session=${session}")

    /** An install/log stream (e.g. /streams/install:flutter). Emits
     *  `{type:"line",text}` and `{type:"result",status,error}` frames. */
    fun subscribeInstallStream(name: String): Flow<SseEvent> =
        streamRequest("/streams/$name")

    // ── Dev / preview verbs (Phase 3 surfaces) ────────────────────────────

    suspend fun devStart(projectName: String, workDir: String, framework: String?): JSONObject =
        withContext(Dispatchers.IO) {
            val body = JSONObject()
                .put("caller", "tv-app")
                .put("platform", "web")
                .put("projectName", projectName)
                .put("workDir", workDir)
            framework?.let { body.put("framework", it) }
            val result = request("POST", "/dev/start", body)
            val obj = bodyToJson(result.body)
            if (obj?.optBoolean("ok", true) == false) {
                // A 412 body carries the capability gap — parse it.
                val gap = FailureSignals.capabilityGapFromBody(obj)
                throw AgentError(obj.optString("error").ifEmpty { "dev server failed to start" }, gap?.code)
            }
            obj ?: JSONObject()
        }

    suspend fun devStatus(): JSONObject = withContext(Dispatchers.IO) {
        bodyToJson(request("GET", "/dev/status", null).body) ?: JSONObject()
    }

    suspend fun webPreviewStart(): JSONObject = withContext(Dispatchers.IO) {
        bodyToJson(request("POST", "/dev/web-preview/start", JSONObject()).body) ?: JSONObject()
    }

    suspend fun vibingPreviewStart(
        project: String,
        targetUrl: String,
        workDir: String,
        width: Int = 390,
        height: Int = 844,
    ): JSONObject = withContext(Dispatchers.IO) {
        val body = JSONObject()
            .put("project", project)
            .put("targetUrl", targetUrl)
            .put("workDir", workDir)
            .put("mode", "live")
            .put("width", width)
            .put("height", height)
        bodyToJson(request("POST", "/vibing/preview/start", body).body) ?: JSONObject()
    }

    suspend fun vibingPreviewStop(project: String) {
        runCatching { request("POST", "/vibing/preview/stop", JSONObject().put("project", project)) }
    }

    suspend fun vibingPreviewSnapshot(project: String): JSONObject = withContext(Dispatchers.IO) {
        bodyToJson(request("POST", "/vibing/preview/snapshot", JSONObject().put("project", project)).body) ?: JSONObject()
    }

    suspend fun vibingPreviewFrame(frameHash: String, project: String): ByteArray? =
        withContext(Dispatchers.IO) {
            val result = request("GET", "/vibing/preview/frames/$frameHash?project=$project", null)
            if (result.status in 200..299) result.body else null
        }

    suspend fun droidFrame(): ByteArray? = withContext(Dispatchers.IO) {
        val result = request("GET", "/droid/frame", null)
        if (result.status in 200..299 && result.body.isNotEmpty()) result.body else null
    }

    suspend fun captureFrame(): ByteArray? = withContext(Dispatchers.IO) {
        val result = request("GET", "/capture/frame.jpg", null)
        if (result.status in 200..299 && result.body.isNotEmpty()) result.body else null
    }

    // ── Parsing helpers ───────────────────────────────────────────────────

    private fun parseAgentInfo(o: JSONObject): AgentInfo? = runCatching {
        AgentInfo(
            hostname = o.optString("hostname").ifEmpty { null },
            platform = o.optString("platform").ifEmpty { null },
            arch = o.optString("arch").ifEmpty { null },
            agentVersion = o.optString("agentVersion").ifEmpty { null },
            deviceId = o.optString("deviceId").ifEmpty { null },
            cpuPercent = if (o.has("cpuPercent")) o.optDouble("cpuPercent") else null,
            localIPs = o.optJSONArray("localIPs")?.let { a -> (0 until a.length()).map { a.optString(it) } },
        )
    }.getOrNull()

    private fun parseAgentStatus(o: JSONObject): AgentStatus? = runCatching {
        AgentStatus(
            agentVersion = o.optString("agentVersion").ifEmpty { null },
            authExpired = if (o.has("authExpired")) o.optBoolean("authExpired") else null,
            tasks = o.optJSONObject("tasks")?.let { t ->
                TaskCounts(
                    total = if (t.has("total")) t.optInt("total") else null,
                    running = if (t.has("running")) t.optInt("running") else null,
                )
            },
            devServer = o.optJSONObject("devServer")?.let { d ->
                DevServerStatus(
                    running = if (d.has("running")) d.optBoolean("running") else null,
                    framework = d.optString("framework").ifEmpty { null },
                    port = if (d.has("port")) d.optInt("port") else null,
                    tasksTotal = if (d.has("tasksTotal")) d.optInt("tasksTotal") else null,
                    tasksRunning = if (d.has("tasksRunning")) d.optInt("tasksRunning") else null,
                )
            },
        )
    }.getOrNull()

    private fun parseTaskRows(tasks: JSONArray?): List<TaskRow> {
        if (tasks == null) return emptyList()
        val out = mutableListOf<TaskRow>()
        for (i in 0 until tasks.length()) {
            tasks.optJSONObject(i)?.let { parseTaskRow(it)?.let { out.add(it) } }
        }
        return out
    }
}
