package io.yaver.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Text
import androidx.compose.material3.OutlinedTextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavHostController
import io.yaver.tv.TvStore
import kotlinx.coroutines.launch
import org.json.JSONObject

/**
 * SettingsScreen — account defaults: primary device, primary runner, latest
 * project, MCP defaults. Each row writes the same Convex `POST /settings` row
 * the phone/web write, so a choice made on the TV is remembered everywhere.
 * Mirrors tvos/YaverTV/Views/TVSettingsView.swift.
 */
@Composable
fun SettingsScreen(store: TvStore, nav: NavHostController) {
    val scope = rememberCoroutineScope()
    val token by store.token.collectAsState()
    val devices by store.devices.collectAsState()
    val boxes by store.boxes.collectAsState()
    val settings by store.settings.collectAsState()
    val selectedBox by store.selectedBox.collectAsState()
    val appearanceTheme by store.appearanceTheme.collectAsState()

    var saved by remember { mutableStateOf<String?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var runners by remember { mutableStateOf<Map<String, List<io.yaver.tv.RunnerInfo>>>(emptyMap()) }
    var privateVpsDraft by remember { mutableStateOf(io.yaver.tv.Backend.privateVpsUrl().orEmpty()) }

    LaunchedEffect(Unit) {
        store.refreshDevices()
    }
    LaunchedEffect(boxes.map { it.id }) {
        // The selected machine is the authority for what OpenCode can run.
        val map = mutableMapOf<String, List<io.yaver.tv.RunnerInfo>>()
        boxes.forEach { box ->
            runCatching {
                store.clientFor(box).getRunners()
            }.getOrNull()?.let { map[box.id] = it }
        }
        runners = map
    }

    val boxId = selectedBox?.id ?: boxes.firstOrNull()?.id
    val boxRunners = boxId?.let { runners[it] }.orEmpty().filter { it.installed }
    val selectedRunnerId = boxId?.let { settings?.primaryRunnerByDevice?.get(it) }
        ?: boxRunners.firstOrNull { it.isDefault }?.id
        ?: boxRunners.firstOrNull()?.id
    val selectedRunner = boxRunners.firstOrNull { it.id == selectedRunnerId }
    val savedModel = boxId?.let { settings?.primaryModelByDevice?.get(it) }
    val savedProvider = boxId?.let { settings?.primaryProviderByDevice?.get(it) }
    val selectedProvider = if (selectedRunnerId == "opencode") {
        savedProvider
            ?: selectedRunner?.models?.firstOrNull { it.id == savedModel }?.provider
            ?: selectedRunner?.models?.firstOrNull { it.isDefault }?.provider
            ?: selectedRunner?.models?.firstOrNull()?.provider
    } else null
    val providerChoices = if (selectedRunnerId == "opencode") {
        selectedRunner?.models.orEmpty().mapNotNull { it.provider }.distinct().sorted()
    } else emptyList()
    val modelChoices = selectedRunner?.models.orEmpty().filter {
        selectedRunnerId != "opencode" || selectedProvider == null || it.provider == selectedProvider
    }
    val selectedModel = modelChoices.firstOrNull { it.id == savedModel }
        ?: modelChoices.firstOrNull { it.isDefault }
        ?: modelChoices.firstOrNull()
    val reasoningChoices = selectedModel?.supportedReasoningEfforts.orEmpty()
    val savedEffort = boxId?.let { settings?.primaryReasoningEffortByDevice?.get(it) }
    val selectedEffort = savedEffort?.takeIf { value -> reasoningChoices.any { it.id == value } }
        ?: selectedModel?.defaultReasoningEffort

    fun saveRunnerPreference(
        runnerId: String,
        model: io.yaver.tv.ModelInfo?,
        reasoningEffort: String?,
        provider: String?,
        message: String,
    ) {
        val targetId = boxId ?: return
        scope.launch {
            error = null
            saved = null
            runCatching {
                io.yaver.tv.MachineRegistry.writeRunnerPreference(
                    token = token,
                    deviceId = targetId,
                    runnerId = runnerId,
                    model = model?.id,
                    reasoningEffort = reasoningEffort,
                    provider = provider,
                )
                store.refreshDevices()
            }.onSuccess { saved = message }
                .onFailure { error = it.message }
        }
    }

    Column(
        modifier = Modifier.fillMaxSize().background(TvColors.Bg).verticalScroll(rememberScrollState()).padding(48.dp),
        verticalArrangement = Arrangement.spacedBy(28.dp),
    ) {
        BackBar(title = "Settings", subtitle = "Defaults and this TV's appearance", onBack = { nav.popBackStack() })

        error?.let { ErrorPanel(message = it) }
        saved?.let { StatusChip(it, TvColors.Green) }

        SettingsRow(
            title = "Private VPS URL",
            detail = "Local to this Android TV · paste a scanned QR link or enter the address manually",
        ) {
            item {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    OutlinedTextField(
                        value = privateVpsDraft,
                        onValueChange = { privateVpsDraft = it; error = null },
                        label = { Text("https://vps.example.com") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(0.62f),
                    )
                    androidx.compose.foundation.layout.Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        TvTextButton(label = "Use this VPS", onClick = {
                            if (io.yaver.tv.Backend.savePrivateVpsUrl(privateVpsDraft)) {
                                store.signOutForServerSwitch()
                                saved = "Private VPS saved — sign in again"
                            } else {
                                error = "Enter a valid HTTPS URL without credentials, query text, or a fragment."
                            }
                        })
                        if (io.yaver.tv.Backend.privateVpsUrl() != null) {
                            TvTextButton(label = "Use Yaver hosted server", onClick = {
                                store.signOutForServerSwitch()
                                io.yaver.tv.Backend.savePrivateVpsUrl(null)
                                privateVpsDraft = ""
                                saved = "Hosted server restored — sign in again"
                            })
                        }
                    }
                }
            }
        }

        SettingsRow(
            title = "Appearance",
            detail = "Saved for Android TV; other Yaver surfaces keep their own choice",
        ) {
            items(listOf("dark", "light")) { theme ->
                TvChip(
                    text = if (theme == "dark") "Dark" else "Light",
                    selected = appearanceTheme == theme,
                    onClick = {
                        scope.launch {
                            error = null
                            runCatching { store.setAppearanceTheme(theme) }
                                .onSuccess { saved = "Appearance saved" }
                                .onFailure { error = it.message }
                        }
                    },
                )
            }
        }

        // ── Primary device ─────────────────────────────────────────────────
        SettingsRow(
            title = "Primary device",
            detail = "Where the TV connects by default",
        ) {
            val deviceRows = devices.ifEmpty {
                boxes.map { io.yaver.tv.RegisteredDevice(deviceId = it.id, name = it.name, alias = it.alias) }
            }
            items(deviceRows, key = { it.deviceId }) { d ->
                TvChip(
                    text = d.name,
                    selected = d.deviceId == settings?.primaryDeviceId,
                    onClick = {
                        scope.launch {
                            runCatching {
                                io.yaver.tv.MachineRegistry.writeSetting(
                                    token,
                                    JSONObject().put("primaryDeviceId", d.deviceId),
                                )
                                store.refreshDevices()
                                saved = "Primary device saved"
                            }.onFailure { error = it.message }
                        }
                    },
                )
            }
        }

        // ── Primary runner ─────────────────────────────────────────────────
        SettingsRow(
            title = "Primary runner",
            detail = "The coding agent used by default",
        ) {
            if (boxId == null) {
                item { Text("Connect to a box to pick its runner.", color = TvColors.TextMuted, fontSize = 16.sp) }
            } else if (boxRunners.isEmpty()) {
                item { Text("No installed coding runner was reported.", color = TvColors.TextMuted, fontSize = 16.sp) }
            } else {
                items(boxRunners, key = { it.id }) { runner ->
                    TvChip(
                        text = runner.id,
                        selected = selectedRunnerId == runner.id,
                        onClick = {
                            val model = runner.models.firstOrNull { it.isDefault } ?: runner.models.firstOrNull()
                            val effort = model?.defaultReasoningEffort
                            saveRunnerPreference(runner.id, model, effort, model?.provider, "Primary runner saved")
                        },
                    )
                }
            }
        }

        if (selectedRunnerId == "opencode" && providerChoices.isNotEmpty()) {
            SettingsRow(
                title = "Provider",
                detail = "OpenCode providers available on this machine",
            ) {
                items(providerChoices, key = { it }) { provider ->
                    TvChip(
                        text = selectedRunner?.models?.firstOrNull { it.provider == provider }?.providerName ?: provider,
                        selected = provider == selectedProvider,
                        onClick = {
                            val choices = selectedRunner?.models.orEmpty().filter { it.provider == provider }
                            val model = choices.firstOrNull { it.isDefault } ?: choices.firstOrNull()
                            saveRunnerPreference("opencode", model, null, provider, "OpenCode provider saved")
                        },
                    )
                }
            }
        }

        if (selectedRunner != null && modelChoices.isNotEmpty()) {
            SettingsRow(
                title = "Favorite model",
                detail = "Live choices reported by the selected machine",
            ) {
                items(modelChoices, key = { it.id }) { model ->
                    TvChip(
                        text = model.name ?: model.id,
                        selected = model.id == selectedModel?.id,
                        onClick = {
                            val effort = selectedEffort?.takeIf { value -> model.supportedReasoningEfforts.any { it.id == value } }
                                ?: model.defaultReasoningEffort
                            saveRunnerPreference(
                                selectedRunner.id,
                                model,
                                effort,
                                if (selectedRunner.id == "opencode") model.provider else null,
                                "Favorite model saved",
                            )
                        },
                    )
                }
            }
        }

        if (selectedRunner != null && selectedModel != null && reasoningChoices.isNotEmpty()) {
            SettingsRow(
                title = "Reasoning",
                detail = "Only levels supported by ${selectedModel.name ?: selectedModel.id}",
            ) {
                items(reasoningChoices, key = { it.id }) { effort ->
                    TvChip(
                        text = when (effort.id) {
                            "xhigh" -> "Extra high"
                            "max" -> "More reasoning"
                            else -> effort.id.replaceFirstChar { it.uppercase() }
                        },
                        selected = effort.id == selectedEffort,
                        onClick = {
                            saveRunnerPreference(
                                selectedRunner.id,
                                selectedModel,
                                effort.id,
                                if (selectedRunner.id == "opencode") selectedModel.provider else null,
                                "Reasoning preference saved",
                            )
                        },
                    )
                }
            }
        }

        // ── Latest project ─────────────────────────────────────────────────
        SettingsRow(
            title = "Latest project",
            detail = "The project a new task opens with",
        ) {
            if (boxId == null) {
                item { Text("Connect to a box first.", color = TvColors.TextMuted, fontSize = 16.sp) }
            } else {
                item { TvChip(text = "Remember project (on)", selected = true, onClick = { }) }
            }
        }
    }
}

@Composable
private fun SettingsRow(
    title: String,
    detail: String,
    chips: LazyListScope.() -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxWidth().background(TvColors.Card, RoundedCornerShape(18.dp)).padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Text(title, color = TvColors.TextPrimary, fontSize = 24.sp, fontWeight = androidx.compose.ui.text.font.FontWeight.Bold)
        Text(detail, color = TvColors.TextSecondary, fontSize = 16.sp)
        LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp), content = chips)
    }
}
