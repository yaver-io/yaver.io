package io.yaver.wear.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.background
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.layout.Row
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.graphics.Color
import androidx.wear.compose.material.Button
import androidx.wear.compose.material.Chip
import androidx.wear.compose.material.ChipDefaults
import androidx.wear.compose.material.CircularProgressIndicator
import androidx.wear.compose.material.MaterialTheme
import androidx.wear.compose.material.Text
import io.yaver.wear.BoxLifecycle
import io.yaver.wear.WatchProtocol
import io.yaver.wear.WatchState
import kotlinx.coroutines.delay

/**
 * Wear Compose root.
 *
 * The whole UI is one screen, by design (the watch owns nothing, shows nothing
 * complex): a big legible result line, a record button, and — only when the
 * runner asks — a confirm prompt. No tabs, no lists, no diffs. Ever.
 *
 * It renders purely off [WatchState] flows, which both this Activity and the
 * background ReplyListenerService write to. That's how an async "Done. Tests
 * pass." lands on the wrist whether or not the user is looking.
 */
@Composable
fun WearApp(
    onRecord: () -> Unit,
    onConfirm: (token: String) -> Unit,
    onCancel: (token: String) -> Unit,
    onIntent: (WatchProtocol.FixedIntent) -> Unit,
    onWake: () -> Unit,
    onDismissWake: () -> Unit,
    canRemoveDevice: Boolean,
    onRemoveDevice: () -> Unit,
    onAppearance: (String) -> Unit,
    privateVpsUrl: String,
    onPrivateVps: (String?) -> Unit,
) {
    val line by WatchState.line.collectAsState()
    val phase by WatchState.phase.collectAsState()
    val phoneReachable by WatchState.phoneReachable.collectAsState()
    val wakeStatus by BoxLifecycle.status.collectAsState()
    val appearanceTheme by WatchState.appearanceTheme.collectAsState()
    var confirmRemoval by remember { mutableStateOf(false) }
    var showSettings by remember { mutableStateOf(false) }

    // Wall-clock bound on Phase.Working: the only prior exit was a later
    // phone→watch push, so a lost Data Layer message or a phone that died
    // mid-task left the wrist on "Working…" forever. 90s of silence returns
    // the record button with an honest line — the task itself keeps running
    // on the box and its summary still arrives on the phone.
    LaunchedEffect(phase) {
        if (phase is WatchState.Phase.Working) {
            delay(90_000)
            if (WatchState.phase.value is WatchState.Phase.Working) {
                WatchState.setLine("Still working — I'll let you know on your phone when it's done.")
                WatchState.setPhase(WatchState.Phase.Idle)
            }
        }
    }

    MaterialTheme(
        colors = MaterialTheme.colors.copy(
            background = if (appearanceTheme == "light") Color(0xFFF7F7F9) else Color(0xFF000000),
            surface = if (appearanceTheme == "light") Color.White else Color(0xFF1C1C1E),
            onSurface = if (appearanceTheme == "light") Color(0xFF111114) else Color.White,
            onBackground = if (appearanceTheme == "light") Color(0xFF111114) else Color.White,
        ),
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(MaterialTheme.colors.background)
                .padding(8.dp),
            contentAlignment = Alignment.Center,
        ) {
            when {
                // Box wake/park takes over the whole face while it's relevant —
                // an asleep box, an in-flight wake, or "open your phone".
                wakeStatus !is BoxLifecycle.WakeStatus.None ->
                    WakeProgress(
                        status = wakeStatus,
                        onWake = onWake,
                        onDismiss = onDismissWake,
                    )

                phase is WatchState.Phase.Confirm -> {
                    val p = phase as WatchState.Phase.Confirm
                    ConfirmScreen(
                        prompt = p.prompt,
                        onConfirm = { onConfirm(p.token) },
                        onCancel = { onCancel(p.token) },
                    )
                }

                confirmRemoval -> ConfirmScreen(
                    prompt = "Remove this box from every Yaver device list?",
                    onConfirm = {
                        confirmRemoval = false
                        onRemoveDevice()
                    },
                    onCancel = { confirmRemoval = false },
                )

                showSettings -> PrivateVpsSettingsScreen(
                    initial = privateVpsUrl,
                    onSave = { onPrivateVps(it); showSettings = false },
                    onClose = { showSettings = false },
                )

                else -> MainScreen(
                    line = line,
                    phase = phase,
                    phoneReachable = phoneReachable,
                    onRecord = onRecord,
                    onIntent = onIntent,
                    canRemoveDevice = canRemoveDevice,
                    onRemoveDevice = { confirmRemoval = true },
                    appearanceTheme = appearanceTheme,
                    onAppearance = onAppearance,
                    onSettings = { showSettings = true },
                )
            }
        }
    }
}

@Composable
private fun MainScreen(
    line: String,
    phase: WatchState.Phase,
    phoneReachable: Boolean,
    onRecord: () -> Unit,
    onIntent: (WatchProtocol.FixedIntent) -> Unit,
    canRemoveDevice: Boolean,
    onRemoveDevice: () -> Unit,
    appearanceTheme: String,
    onAppearance: (String) -> Unit,
    onSettings: () -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        // The one big line — the readback, the heard command, or a hint.
        Text(
            text = line,
            textAlign = TextAlign.Center,
            style = MaterialTheme.typography.title3,
            maxLines = 3,
        )

        Spacer(modifier = Modifier.height(10.dp))

        when (phase) {
            is WatchState.Phase.Sending,
            is WatchState.Phase.Working,
            is WatchState.Phase.Listening -> {
                // Async in flight — show a spinner; the wrist is still free.
                CircularProgressIndicator()
            }

            else -> {
                // Idle — the primary affordance: tap to speak.
                Row(
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Button(onClick = onRecord) {
                        Text("Speak")
                    }
                    Button(
                        onClick = { onAppearance(if (appearanceTheme == "light") "dark" else "light") },
                    ) {
                        Text(if (appearanceTheme == "light") "☾" else "☀")
                    }
                }
                if (!phoneReachable) {
                    Spacer(modifier = Modifier.height(6.dp))
                    Text(
                        text = "Phone not reachable",
                        textAlign = TextAlign.Center,
                        style = MaterialTheme.typography.caption2,
                    )
                }
                Chip(
                    label = { Text("Private VPS URL") },
                    onClick = onSettings,
                    colors = ChipDefaults.secondaryChipColors(),
                )
                // Quick one-tap intents (the "complication" equivalents on-screen).
                Spacer(modifier = Modifier.height(8.dp))
                QuickIntentChip("Run tests", WatchProtocol.FixedIntent.RUN_TESTS, onIntent)
                QuickIntentChip("Status", WatchProtocol.FixedIntent.STATUS, onIntent)
                if (canRemoveDevice) {
                    Chip(
                        label = { Text("Remove box") },
                        onClick = onRemoveDevice,
                        colors = ChipDefaults.secondaryChipColors(),
                    )
                }
            }
        }
    }
}

@Composable
private fun PrivateVpsSettingsScreen(
    initial: String,
    onSave: (String?) -> Unit,
    onClose: () -> Unit,
) {
    var draft by remember { mutableStateOf(initial) }
    var error by remember { mutableStateOf<String?>(null) }
    Column(
        modifier = Modifier.fillMaxSize().padding(8.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Text("Private VPS URL", style = MaterialTheme.typography.title3)
        BasicTextField(
            value = draft,
            onValueChange = { draft = it; error = null },
            singleLine = true,
            textStyle = MaterialTheme.typography.caption1.copy(color = MaterialTheme.colors.onBackground),
            modifier = Modifier.background(MaterialTheme.colors.surface).padding(6.dp),
        )
        if (error != null) Text(error!!, color = MaterialTheme.colors.error, style = MaterialTheme.typography.caption2)
        Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            Button(onClick = {
                val normalized = io.yaver.wear.Backend.normalizePrivateVpsUrl(draft)
                if (normalized == null) error = "Invalid URL" else onSave(normalized)
            }) { Text("Save") }
            Button(onClick = onClose) { Text("Back") }
        }
        if (initial.isNotEmpty()) {
            Chip(label = { Text("Use Yaver hosted") }, onClick = { onSave(null) })
        }
        Text("Read the QR on your phone, then dictate the URL", style = MaterialTheme.typography.caption2, textAlign = TextAlign.Center)
    }
}

@Composable
private fun QuickIntentChip(
    label: String,
    intent: WatchProtocol.FixedIntent,
    onIntent: (WatchProtocol.FixedIntent) -> Unit,
) {
    Chip(
        label = { Text(label) },
        onClick = { onIntent(intent) },
        colors = ChipDefaults.secondaryChipColors(),
    )
}
