package io.yaver.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavHostController
import io.yaver.tv.AgentError
import io.yaver.tv.TaskRow
import io.yaver.tv.TaskRunnerControlCatalog
import io.yaver.tv.TaskRunnerControlModel
import io.yaver.tv.TvStore
import kotlinx.coroutines.launch
import kotlinx.coroutines.delay

/**
 * PLACEHOLDER screens for the Phase 2/3 routes — replaced by the real
 * implementations in TasksScreen.kt / TaskComposerScreen.kt / TaskDetailScreen.kt /
 * SessionScreen.kt / VibingScreen.kt / PreviewStreamScreen.kt /
 * DroidStreamScreen.kt. Kept in one file so the navigation host compiles while
 * the surface is built out incrementally.
 */
@Composable
private fun placeholder(modifier: Modifier, title: String) {
    Column(
        modifier = modifier.fillMaxSize().background(TvColors.Bg).padding(56.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(title, color = TvColors.TextPrimary, fontSize = 40.sp, fontWeight = androidx.compose.ui.text.font.FontWeight.Black)
        Text("Under construction — the Phase 2/3 surface lands next.", color = TvColors.TextSecondary, fontSize = 18.sp)
    }
}

@Composable
fun TasksScreen(store: TvStore, nav: NavHostController) {
    val box by store.selectedBox.collectAsState()
    val scope = rememberCoroutineScope()
    var tasks by remember { mutableStateOf<List<TaskRow>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf<String?>(null) }

    fun reload() {
        scope.launch {
            loading = true
            error = null
            try {
                tasks = box?.let { store.clientFor(it).getTasks() }.orEmpty()
            } catch (e: Throwable) {
                error = (e as? AgentError)?.message ?: e.message ?: "Couldn't load tasks."
            } finally {
                loading = false
            }
        }
    }
    LaunchedEffect(box?.id) { reload() }

    Column(
        modifier = Modifier.fillMaxSize().background(TvColors.Bg).padding(56.dp),
        verticalArrangement = Arrangement.spacedBy(24.dp),
    ) {
        BackBar("Tasks", box?.name?.let { "Coding work on $it" }, onBack = { nav.popBackStack() })
        Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            TvTextButton("New task", onClick = { nav.navigate(Routes.COMPOSER) })
            TvTextButton("Refresh", onClick = ::reload)
        }
        when {
            box == null -> {
                Text("remoteless.code-edit.unavailable", color = TvColors.Orange, fontSize = 18.sp, fontWeight = FontWeight.Bold)
                Text("Android TV has no phone-local repository or safe background coding runtime. Choose your primary/secondary machine; use Cloud Workspace when neither can provide the required capability.", color = TvColors.TextSecondary, fontSize = 20.sp)
                TvTextButton("Choose a capable device", onClick = { nav.navigate(Routes.MACHINES) })
            }
            loading -> Text("Loading tasks…", color = TvColors.TextSecondary, fontSize = 22.sp)
            error != null -> ErrorPanel(error!!, onRetry = ::reload)
            tasks.isEmpty() -> Text("No tasks on this machine yet.", color = TvColors.TextSecondary, fontSize = 22.sp)
            else -> LazyColumn(
                verticalArrangement = Arrangement.spacedBy(14.dp),
                modifier = Modifier.fillMaxWidth(),
            ) {
                items(tasks, key = { it.id }) { task ->
                    TaskCard(task) { nav.navigate(Routes.taskDetail(task.id)) }
                }
            }
        }
    }
}

@Composable
fun TaskComposerScreen(store: TvStore, nav: NavHostController) {
    val box by store.selectedBox.collectAsState()
    val settings by store.settings.collectAsState()
    val scope = rememberCoroutineScope()
    var prompt by remember { mutableStateOf("") }
    var sending by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    Column(
        modifier = Modifier.fillMaxSize().background(TvColors.Bg).padding(56.dp),
        verticalArrangement = Arrangement.spacedBy(22.dp),
    ) {
        BackBar("New task", box?.name?.let { "A separate runner seat on $it" }, onBack = { nav.popBackStack() })
        Text("Describe the outcome. You can continue the same runner conversation from any Yaver surface.", color = TvColors.TextSecondary, fontSize = 19.sp)
        OutlinedTextField(
            value = prompt,
            onValueChange = { prompt = it },
            label = { Text("What should Yaver build or change?") },
            minLines = 4,
            modifier = Modifier.fillMaxWidth(),
            colors = OutlinedTextFieldDefaults.colors(
                focusedTextColor = TvColors.TextPrimary,
                unfocusedTextColor = TvColors.TextPrimary,
                focusedBorderColor = TvColors.Accent,
                unfocusedBorderColor = TvColors.Border,
            ),
        )
        error?.let { ErrorPanel(it) { error = null } }
        TvTextButton(if (sending) "Starting…" else "Start task", onClick = {
            val text = prompt.trim()
            val target = box
            if (text.isEmpty() || target == null || sending) return@TvTextButton
            sending = true
            error = null
            scope.launch {
                try {
                    val created = store.clientFor(target).createTask(
                        title = text.take(80),
                        description = text,
                        runner = settings?.primaryRunnerByDevice?.get(target.id),
                        model = settings?.primaryModelByDevice?.get(target.id),
                        reasoningEffort = settings?.primaryReasoningEffortByDevice?.get(target.id),
                        mode = settings?.primaryModeByDevice?.get(target.id),
                    )
                    val id = created.optString("id").ifEmpty { created.optString("taskId") }
                    if (id.isEmpty()) throw AgentError("The machine started no identifiable task.")
                    nav.navigate(Routes.taskDetail(id))
                } catch (e: Throwable) {
                    error = (e as? AgentError)?.message ?: e.message ?: "Couldn't start the task."
                } finally { sending = false }
            }
        })
    }
}

@Composable
fun TaskDetailScreen(store: TvStore, nav: NavHostController, taskId: String) {
    val box by store.selectedBox.collectAsState()
    val scope = rememberCoroutineScope()
    var task by remember { mutableStateOf<org.json.JSONObject?>(null) }
    var loading by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf<String?>(null) }
    var showRunnerDetails by remember { mutableStateOf(false) }
    var followUp by remember { mutableStateOf("") }
    var followUpSending by remember { mutableStateOf(false) }
    var runnerControl by remember { mutableStateOf<String?>(null) }
    var runnerControlCatalog by remember { mutableStateOf<TaskRunnerControlCatalog?>(null) }
    var runnerControlModel by remember { mutableStateOf("") }
    var runnerControlBusy by remember { mutableStateOf(false) }
    var runnerControlError by remember { mutableStateOf<String?>(null) }
    var runnerControlNotice by remember { mutableStateOf<String?>(null) }
    fun reload() {
        scope.launch {
            loading = true
            error = null
            try { task = box?.let { store.clientFor(it).getTask(taskId) } }
            catch (e: Throwable) { error = (e as? AgentError)?.message ?: e.message ?: "Couldn't load this task." }
            finally { loading = false }
        }
    }
    fun openRunnerControl(mode: String) {
        val target = box ?: run {
            runnerControlError = "No machine selected"
            return
        }
        runnerControl = mode
        runnerControlBusy = true
        runnerControlError = null
        runnerControlNotice = null
        scope.launch {
            try {
                val catalog = store.clientFor(target).taskRunnerControls(taskId)
                runnerControlCatalog = catalog
                runnerControlModel = catalog.model
                    ?: catalog.models.firstOrNull { it.isDefault }?.id
                    ?: catalog.models.firstOrNull()?.id
                    .orEmpty()
            } catch (e: Throwable) {
                runnerControlError = (e as? AgentError)?.message ?: e.message ?: "Runner controls are unavailable."
            } finally { runnerControlBusy = false }
        }
    }
    fun applyRunnerModel(model: TaskRunnerControlModel, effort: String? = null) {
        val target = box ?: return
        runnerControlBusy = true
        runnerControlError = null
        scope.launch {
            try {
                val result = store.clientFor(target).applyTaskRunnerControl(taskId, "model", model.id, effort)
                val display = result.optString("display").ifEmpty { listOfNotNull(model.id, effort).joinToString(" · ") }
                runnerControl = null
                runnerControlNotice = "Model set to $display for the next turn."
                reload()
            } catch (e: Throwable) {
                runnerControlError = (e as? AgentError)?.message ?: e.message ?: "The model could not be changed."
            } finally { runnerControlBusy = false }
        }
    }
    fun exitRunner() {
        val target = box ?: return
        runnerControlBusy = true
        runnerControlError = null
        scope.launch {
            try {
                val result = store.clientFor(target).applyTaskRunnerControl(taskId, "exit", confirmed = true)
                if (!result.optBoolean("verified")) throw AgentError("The runner did not verify that it exited.")
                runnerControl = null
                runnerControlNotice = if (result.optBoolean("alreadyExited"))
                    "Runner session was already exited; the agent verified no seat remains."
                else "Runner session exited and verified."
                reload()
            } catch (e: Throwable) {
                runnerControlError = (e as? AgentError)?.message ?: e.message ?: "The runner did not exit."
            } finally { runnerControlBusy = false }
        }
    }
    LaunchedEffect(box?.id, taskId) {
        reload()
        while (true) {
            val status = task?.optString("status").orEmpty()
            delay(if (status == "running" || status == "queued") 1_500 else 10_000)
            try { box?.let { task = store.clientFor(it).getTask(taskId) } } catch (_: Throwable) { /* keep last good task visible */ }
        }
    }
    Column(
        modifier = Modifier.fillMaxSize().background(TvColors.Bg).padding(56.dp),
        verticalArrangement = Arrangement.spacedBy(22.dp),
    ) {
        BackBar("Task", taskId, onBack = { nav.popBackStack() })
        when {
            loading -> Text("Loading task…", color = TvColors.TextSecondary, fontSize = 22.sp)
            error != null -> ErrorPanel(error!!, onRetry = ::reload)
            task != null -> {
                Text(task!!.optString("title").ifEmpty { "Untitled task" }, color = TvColors.TextPrimary, fontSize = 30.sp, fontWeight = FontWeight.Bold)
                val model = task!!.optString("model")
                val effort = task!!.optString("reasoningEffort")
                val runner = task!!.optString("runnerId").ifEmpty { task!!.optString("runner") }
                Text(
                    listOf(listOf(model, effort).filter { it.isNotEmpty() }.joinToString(" · ").ifEmpty { runner }, task!!.optString("status").ifEmpty { "unknown" }).filter { it.isNotEmpty() }.joinToString(" · "),
                    color = TvColors.Accent,
                    fontSize = 20.sp,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    TvTextButton(if (model.isEmpty()) "Model" else listOf(model, effort).filter { it.isNotEmpty() }.joinToString(" · "), onClick = { openRunnerControl("model") })
                    TvTextButton("Exit", onClick = { openRunnerControl("exit") })
                }
                val presentation = io.yaver.tv.parseTaskPresentation(task!!.optJSONArray("presentation"))
                val primaryUpdate = if (status == "running" || status == "queued") {
                    presentation.lastOrNull { it.kind == "message" && it.role == "assistant" }
                } else null
                (primaryUpdate ?: presentation.lastOrNull { it.kind != "message" })?.let { summary ->
                    Column(verticalArrangement = Arrangement.spacedBy(5.dp), modifier = Modifier.fillMaxWidth().background(TvColors.Card, RoundedCornerShape(14.dp)).padding(16.dp)) {
                        if (summary.kind == "message") Text("LATEST UPDATE FROM YAVER", color = TvColors.Accent, fontSize = 13.sp, fontWeight = FontWeight.Bold)
                        Text(summary.text, color = TvColors.TextPrimary, fontSize = 20.sp, fontWeight = FontWeight.Bold, maxLines = if (summary.kind == "message") 4 else 2)
                        Text(listOfNotNull(summary.machine, summary.platform, summary.runner, summary.project).joinToString(" · "), color = TvColors.TextSecondary, fontSize = 14.sp)
                    }
                }
                val turns = task!!.optJSONArray("turns")
                var lastAssistantText: String? = null
                if (turns != null) for (i in 0 until turns.length()) {
                    turns.optJSONObject(i)?.let { turn ->
                        val role = turn.optString("role")
                        val content = turn.optString("content")
                        if (role == "assistant" && content.isNotEmpty()) lastAssistantText = content
                        if (content.isNotEmpty()) Text(
                            "${if (role == "user") "You" else "Yaver"}: $content",
                            color = if (role == "user") TvColors.Accent else TvColors.TextPrimary,
                            fontSize = 18.sp,
                            modifier = Modifier.fillMaxWidth().background(TvColors.Card, RoundedCornerShape(14.dp)).padding(16.dp),
                        )
                    }
                }
                presentation.lastOrNull { it.kind == "message" && it.role == "assistant" && it.text != lastAssistantText }?.let { message ->
                    Text("Yaver: ${message.text}", color = TvColors.TextPrimary, fontSize = 18.sp, modifier = Modifier.fillMaxWidth().background(TvColors.Card, RoundedCornerShape(14.dp)).padding(16.dp))
                }
                runnerControl?.let { mode ->
                    Column(verticalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.fillMaxWidth().background(TvColors.Card, RoundedCornerShape(14.dp)).padding(16.dp)) {
                        Text(
                            when (mode) { "exit" -> "Exit runner session?"; "effort" -> "Choose reasoning level"; else -> "Choose this conversation's model" },
                            color = TvColors.TextPrimary,
                            fontSize = 20.sp,
                            fontWeight = FontWeight.Bold,
                        )
                        if (runnerControlBusy) Text("Checking the runner on this machine…", color = TvColors.TextSecondary, fontSize = 16.sp)
                        runnerControlError?.let { Text(it, color = TvColors.Orange, fontSize = 16.sp) }
                        val catalog = runnerControlCatalog
                        if (mode == "model" && catalog != null) {
                            if (catalog.isAdopted) Text("This is an adopted terminal. Change its model in the live runner details so Yaver never guesses at terminal menu positions.", color = TvColors.Orange, fontSize = 16.sp)
                            catalog.models.forEach { option ->
                                TvTextButton("${if (option.id == catalog.model) "✓ " else ""}${option.name ?: option.id}", onClick = {
                                    runnerControlModel = option.id
                                    if (catalog.runnerId == "codex" && option.supportedReasoningEfforts.isNotEmpty()) runnerControl = "effort"
                                    else applyRunnerModel(option)
                                })
                            }
                        }
                        if (mode == "effort" && catalog != null) {
                            catalog.models.firstOrNull { it.id == runnerControlModel }?.let { selected ->
                                selected.supportedReasoningEfforts.forEach { option ->
                                    TvTextButton(option.id, onClick = { applyRunnerModel(selected, option.id) })
                                }
                            }
                        }
                        if (mode == "exit") {
                            Text("Stops this task's real runner seat. Yaver verifies it is gone before reporting success.", color = TvColors.TextSecondary, fontSize = 16.sp)
                            TvTextButton("Exit and verify", onClick = ::exitRunner)
                        }
                        TvTextButton("Close", onClick = { runnerControl = null })
                    }
                }
                runnerControlNotice?.let { Text(it, color = TvColors.Green, fontSize = 16.sp) }
                val raw = task!!.optString("rawOutput").ifEmpty { task!!.optString("output") }
                if (raw.isNotEmpty()) {
                    TvTextButton(if (showRunnerDetails) "Hide runner details" else "Show runner details", onClick = { showRunnerDetails = !showRunnerDetails })
                    if (showRunnerDetails) Text(raw.takeLast(64 * 1024), color = TvColors.TextSecondary, fontSize = 14.sp)
                }
                OutlinedTextField(
                    value = followUp,
                    onValueChange = { followUp = it },
                    label = { Text("Continue this runner conversation") },
                    modifier = Modifier.fillMaxWidth(),
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedTextColor = TvColors.TextPrimary,
                        unfocusedTextColor = TvColors.TextPrimary,
                        focusedBorderColor = TvColors.Accent,
                        unfocusedBorderColor = TvColors.Border,
                    ),
                )
                TvTextButton(if (followUpSending) "Sending…" else "Send follow-up", onClick = {
                    val text = followUp.trim()
                    val target = box
                    if (text.isEmpty() || target == null || followUpSending) return@TvTextButton
                    if (text.equals("/model", ignoreCase = true)) {
                        followUp = ""
                        openRunnerControl("model")
                        return@TvTextButton
                    }
                    if (text.equals("/exit", ignoreCase = true)) {
                        followUp = ""
                        openRunnerControl("exit")
                        return@TvTextButton
                    }
                    followUpSending = true
                    scope.launch {
                        try {
                            store.clientFor(target).continueTask(taskId, text)
                            followUp = ""
                            reload()
                        } catch (e: Throwable) {
                            error = (e as? AgentError)?.message ?: e.message ?: "Couldn't continue this task."
                        } finally { followUpSending = false }
                    }
                })
                TvTextButton("Refresh", onClick = ::reload)
            }
        }
    }
}

@Composable
private fun TaskCard(task: TaskRow, onClick: () -> Unit) {
    val status = task.status?.ifEmpty { "unknown" } ?: "unknown"
    TvCard(
        modifier = Modifier.fillMaxWidth().clickable(onClick = onClick).focusable(),
        shape = RoundedCornerShape(16.dp),
    ) {
        Column(verticalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.weight(1f)) {
            Text(task.safeTitle, color = TvColors.TextPrimary, fontSize = 23.sp, fontWeight = FontWeight.Bold)
            Text(
                listOfNotNull(status, task.projectName, task.model?.let { listOfNotNull(it, task.reasoningEffort).joinToString(" · ") } ?: task.runner).joinToString(" · "),
                color = statusColor(status),
                fontSize = 17.sp,
            )
            val primaryUpdate = if (status == "running" || status == "queued") {
                task.presentation.lastOrNull { it.kind == "message" && it.role == "assistant" }
            } else null
            (primaryUpdate ?: task.presentation.lastOrNull { it.kind != "message" })?.let { summary ->
                Text(summary.text, color = TvColors.TextSecondary, fontSize = 16.sp, maxLines = 2)
            }
            val execution = listOfNotNull(
                task.yaverSessionId?.let { "yaver $it" },
                task.remoteBoxId?.let { "box $it" },
                task.sessionId?.let { "runner $it" },
                (task.tmuxSession ?: task.tmuxSessionId)?.let { "tmux $it" },
                task.tmuxPaneId,
            ).joinToString(" · ")
            if (execution.isNotEmpty()) {
                Text(execution, color = TvColors.TextSecondary, fontSize = 13.sp, maxLines = 1)
            }
        }
        Text("Select ›", color = TvColors.TextSecondary, fontSize = 17.sp)
    }
}

private fun statusColor(status: String): Color = when (status.lowercase()) {
    "running", "queued" -> TvColors.Accent
    "completed", "review" -> TvColors.Green
    "failed", "cancelled" -> TvColors.Red
    else -> TvColors.TextSecondary
}


@Composable
fun SessionScreen(store: TvStore, nav: NavHostController) {
    placeholder(Modifier, "Session")
}

@Composable
fun VibingScreen(store: TvStore, nav: NavHostController) {
    val box by store.selectedBox.collectAsState()
    Column(
        modifier = Modifier.fillMaxSize().background(TvColors.Bg).padding(56.dp),
        verticalArrangement = Arrangement.spacedBy(18.dp),
    ) {
        BackBar("Vibing", box?.name?.let { "Render on $it" }, onBack = { nav.popBackStack() })
        Text("remoteless.dev-server.unavailable", color = TvColors.Orange, fontSize = 18.sp, fontWeight = FontWeight.Bold)
        Text("This TV can display an already-served preview, but cannot run a shell, package manager, Flutter SDK, dev server, simulator, build, test, or deploy. Use the primary/secondary render machine or Cloud Workspace.", color = TvColors.TextSecondary, fontSize = 20.sp)
        TvTextButton("Choose a capable device", onClick = { nav.navigate(Routes.MACHINES) })
    }
}

@Composable
fun PreviewStreamScreen(store: TvStore, nav: NavHostController, projectName: String) {
    placeholder(Modifier, "Preview $projectName")
}

@Composable
fun DroidStreamScreen(store: TvStore, nav: NavHostController) {
    placeholder(Modifier, "Android screen")
}
