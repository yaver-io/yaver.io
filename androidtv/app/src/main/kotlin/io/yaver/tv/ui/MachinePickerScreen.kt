package io.yaver.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
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
import io.yaver.tv.AgentError
import io.yaver.tv.BoxLifecycle
import io.yaver.tv.MachineRegistry
import io.yaver.tv.RegisteredDevice
import io.yaver.tv.TvStore
import kotlinx.coroutines.launch

/**
 * MachinePickerScreen — the account's machines as a badge grid: Selected /
 * Primary (blue), Wake (orange), Online / LAN-only (green), Stale (yellow),
 * Offline (gray). Rows are live-sorted (fresh first, then name). Removal goes
 * through the hosting-correct route; Wake goes through BoxLifecycle. Mirrors
 * tvos/YaverTV/Views/MachinePickerView.swift.
 */
@Composable
fun MachinePickerScreen(store: TvStore, nav: NavHostController) {
    val scope = rememberCoroutineScope()
    val token by store.token.collectAsState()
    val devices by store.devices.collectAsState()
    val boxes by store.boxes.collectAsState()
    val selectedBox by store.selectedBox.collectAsState()
    val settings by store.settings.collectAsState()

    var loading by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var removingId by remember { mutableStateOf<String?>(null) }

    val lifecycle = remember { BoxLifecycle(scope) }

    suspend fun reload() {
        loading = true
        error = null
        try {
            store.refreshDevices()
        } catch (e: Throwable) {
            error = e.message ?: "Couldn't load your machines."
        } finally {
            loading = false
        }
    }

    LaunchedEffect(Unit) { reload() }

    Column(
        modifier = Modifier.fillMaxSize().background(TvColors.Bg).padding(48.dp),
        verticalArrangement = Arrangement.spacedBy(28.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                BackBar(title = "Devices", subtitle = "Choose where the TV connects", onBack = { nav.popBackStack() })
            }
            TvTextButton(label = "Refresh", onClick = { scope.launch { reload() } })
            if (settings?.primaryDeviceId != null && (devices.size + boxes.size) > 1) {
                TvTextButton(
                    label = if (settings?.showWorkerDevices == true) {
                        "Hide workers · ${devices.count { it.deviceId != settings?.primaryDeviceId && it.isOnline }} ready"
                    } else {
                        "Show workers · ${devices.count { it.deviceId != settings?.primaryDeviceId && it.isOnline }}/${devices.count { it.deviceId != settings?.primaryDeviceId }} ready"
                    },
                    onClick = {
                        scope.launch {
                            MachineRegistry.writeSetting(
                                token,
                                org.json.JSONObject().put("showWorkerDevices", settings?.showWorkerDevices != true),
                            )
                            reload()
                        }
                    },
                )
            }
            TvTextButton(label = "Type an address", onClick = { nav.navigate(Routes.ADD_BOX) })
        }

        error?.let { ErrorPanel(message = it, onRetry = { scope.launch { reload() } }) }

        val allRows = devices.ifEmpty {
            boxes.map { b ->
                RegisteredDevice(
                    deviceId = b.id,
                    name = b.name,
                    alias = b.alias,
                    machineId = b.machineId,
                )
            }
        }
        val rows = if (settings?.primaryDeviceId != null && settings?.showWorkerDevices != true) {
            allRows.filter { it.deviceId == settings?.primaryDeviceId }
        } else allRows

        if (!loading && rows.isEmpty()) {
            Text(
                "No machines yet — run `yaver serve` signed in as you on a computer, then it appears here.",
                color = TvColors.TextSecondary,
                fontSize = 19.sp,
            )
        }

        LazyVerticalGrid(
            columns = GridCells.Fixed(4),
            modifier = Modifier.fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(20.dp),
            horizontalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            items(rows.sortedWith(compareByDescending<RegisteredDevice> { it.isManaged }.thenBy { it.name.lowercase() })) { device ->
                MachineCard(
                    device = device,
                    isSelected = device.deviceId == selectedBox?.id,
                    isPrimary = device.deviceId == settings?.primaryDeviceId,
                    onSelect = {
                        store.selectBox(device.toBox(settings?.relayUrl, settings?.relayPassword))
                    },
                    onWake = {
                        val box = device.toBox(settings?.relayUrl, settings?.relayPassword)
                        lifecycle.refreshReachability(box)
                        lifecycle.wake(box, token)
                    },
                    onRemove = {
                        scope.launch {
                            removingId = device.deviceId
                            try {
                                if (device.isManaged) {
                                    device.machineId?.let { MachineRegistry.decommissionCloudMachine(it, token) }
                                } else {
                                    MachineRegistry.removeDevice(device.deviceId, token)
                                }
                                store.removeBox(device.deviceId)
                            } catch (e: Throwable) {
                                error = (e as? AgentError)?.message ?: (e.message ?: "Couldn't remove this machine.")
                            } finally {
                                removingId = null
                            }
                        }
                    },
                )
            }
        }
    }
}

@Composable
private fun MachineCard(
    device: RegisteredDevice,
    isSelected: Boolean,
    isPrimary: Boolean,
    onSelect: () -> Unit,
    onWake: () -> Unit,
    onRemove: () -> Unit,
) {
    var focused by remember { mutableStateOf(false) }
    val now = System.currentTimeMillis()

    val online = device.controlPlaneState?.let { it != "offline" }
        ?: (device.isOnline && (device.lastHeartbeat?.let { now - it < 15 * 60 * 1000 } ?: true))
    val wakeable = device.isManaged

    val badge: Pair<String, Color> = when {
        isSelected || isPrimary -> "Selected" to TvColors.Blue
        device.needsAuth || device.controlPlaneState == "needs-auth" -> "Sign in" to TvColors.Orange
        !online && wakeable -> "Wake" to TvColors.Orange
        online && device.relayConnected == false -> "LAN only" to TvColors.Orange
        online -> "Reporting" to TvColors.Green
        else -> "Offline" to TvColors.TextMuted
    }

    Column(
        modifier = Modifier
            .background(if (focused) TvColors.AccentSoft else TvColors.Card, RoundedCornerShape(18.dp))
            .padding(22.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("🖥", fontSize = 24.sp)
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(device.name, color = TvColors.TextPrimary, fontSize = 20.sp, fontWeight = FontWeight.Bold, maxLines = 1)
                device.alias?.let { Text("@$it", color = TvColors.TextSecondary, fontSize = 16.sp, maxLines = 1) }
            }
        }
        StatusChip(badge.first, badge.second)
        Text(
            listOfNotNull(
                device.platform,
                device.agentVersion?.let { "v$it" },
                if (device.isManaged) "managed" else null,
            ).joinToString(" · "),
            color = TvColors.TextSecondary,
            fontSize = 15.sp,
            maxLines = 1,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            TvTextButton(label = "Connect", onClick = onSelect)
            if (wakeable && !online) {
                TvTextButton(label = "Wake", onClick = onWake)
            }
            TvTextButton(label = "Remove", danger = true, onClick = onRemove)
        }
    }
}
