package io.yaver.tv.ui

import androidx.compose.foundation.background
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavHostController
import io.yaver.tv.MachineRegistry
import io.yaver.tv.RegisteredDevice
import io.yaver.tv.TvStore
import kotlinx.coroutines.launch

/**
 * UpdateAgentScreen — registry-wide update request. Unlike the selected box
 * only, an unreachable box needs an update request MORE, not less (its agent is
 * the one that can't answer). Honest no-progress contract: the request applies
 * at the box's next check-in, about a minute. Mirrors
 * tvos/YaverTV/Views/UpdateAgentView.swift.
 */
@Composable
fun UpdateAgentScreen(store: TvStore, nav: NavHostController) {
    val scope = rememberCoroutineScope()
    val token by store.token.collectAsState()
    val devices by store.devices.collectAsState()

    var requested by remember { mutableStateOf<Set<String>>(emptySet()) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) { store.refreshDevices() }

    Column(
        modifier = Modifier.fillMaxSize().background(TvColors.Bg).padding(48.dp),
        verticalArrangement = Arrangement.spacedBy(28.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                BackBar(title = "Update agent", onBack = { nav.popBackStack() })
            }
            TvTextButton(
                label = "Update all",
                onClick = {
                    scope.launch {
                        busy = true
                        error = null
                        try {
                            MachineRegistry.requestUpdate(devices.map { it.deviceId }, token)
                            requested = devices.map { it.deviceId }.toSet()
                        } catch (e: Throwable) {
                            error = e.message ?: "Update request failed."
                        } finally {
                            busy = false
                        }
                    }
                },
            )
        }

        Text(
            "Requests an agent update for every machine on the account. Each applies at its next check-in (about a minute) — there is no progress bar because there is no progress signal.",
            color = TvColors.TextSecondary,
            fontSize = 18.sp,
        )

        error?.let { ErrorPanel(message = it) }

        LazyColumn(verticalArrangement = Arrangement.spacedBy(14.dp)) {
            items(devices, key = { it.deviceId }) { device ->
                UpdateRow(
                    device = device,
                    requested = device.deviceId in requested,
                    onRequest = {
                        scope.launch {
                            busy = true
                            error = null
                            try {
                                MachineRegistry.requestUpdate(listOf(device.deviceId), token)
                                requested = requested + device.deviceId
                            } catch (e: Throwable) {
                                error = e.message ?: "Update request failed."
                            } finally {
                                busy = false
                            }
                        }
                    },
                )
            }
        }
    }
}

@Composable
private fun UpdateRow(
    device: RegisteredDevice,
    requested: Boolean,
    onRequest: () -> Unit,
) {
    val online = device.controlPlaneState?.let { it != "offline" }
        ?: (device.isOnline && (device.lastHeartbeat?.let { System.currentTimeMillis() - it < 15 * 60 * 1000 } ?: true))
    Row(
        modifier = Modifier.fillMaxWidth().background(TvColors.Card, RoundedCornerShape(16.dp)).padding(20.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(device.name, color = TvColors.TextPrimary, fontSize = 20.sp, fontWeight = FontWeight.Bold)
            Text(
                "${device.platform ?: "platform unknown"} · v${device.agentVersion ?: "?"}",
                color = TvColors.TextSecondary,
                fontSize = 15.sp,
            )
        }
        if (requested) {
            StatusChip("Requested · applies at next check-in", TvColors.Green)
        } else {
            StatusChip(if (online) "Online" else "Offline", if (online) TvColors.Green else TvColors.TextMuted)
        }
        TvTextButton(label = "Update", onClick = onRequest)
    }
}
