package io.yaver.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavHostController
import io.yaver.tv.BoxLifecycle
import io.yaver.tv.BoxPhase
import io.yaver.tv.BoxTarget
import io.yaver.tv.TvStore
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * DashboardScreen — the lean-back launcher, shaped like tvOS DashboardView:
 * header + profile menu, the selected-machine card (name, alias, status chip,
 * detail), a wake panel when the box is asleep, and one predictable 4-tile rail
 * (Chat / Vibing / Devices / Settings). LESS IS MORE: exactly the four primary
 * surfaces, sign-out lives in the profile menu.
 */
@Composable
fun DashboardScreen(store: TvStore, nav: NavHostController) {
    val scope = rememberCoroutineScope()
    val boxes by store.boxes.collectAsState()
    val selectedBox by store.selectedBox.collectAsState()
    val token by store.token.collectAsState()
    val autoConnecting by store.autoConnecting.collectAsState()
    val autoConnectTarget by store.autoConnectTarget.collectAsState()

    val lifecycle = remember { BoxLifecycle(scope) }
    val reachable by lifecycle.reachable.collectAsState()
    val clientBlocked by lifecycle.clientBlocked.collectAsState()
    val isRunning by lifecycle.isRunning.collectAsState()
    val wakeError by lifecycle.error.collectAsState()
    val phase by lifecycle.phase.collectAsState()
    val percent by lifecycle.percent.collectAsState()

    // Probe the selected box's reachability whenever the selection changes.
    LaunchedEffect(selectedBox?.id) {
        selectedBox?.let { box ->
            lifecycle.refreshReachability(box)
            // Seamless connectivity self-heal: if the box isn't answering over
            // direct/relay and it isn't a parkable managed box, re-resolve once.
            delay(2500)
            if (reachable == false && !(box.managed == true)) {
                store.healReachability()
            }
        }
    }

    // Stream C: on launch, silently connect to a live machine + narrate.
    LaunchedEffect(Unit) { store.autoConnectOnLaunch() }

    Column(
        modifier = Modifier.fillMaxSize().background(TvColors.Bg).verticalScroll(rememberScrollState()).padding(56.dp),
        verticalArrangement = Arrangement.spacedBy(36.dp),
    ) {
        // Header: brand + profile menu (sign out / update agent).
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text("Yaver", color = TvColors.TextPrimary, fontSize = 48.sp, fontWeight = FontWeight.Black)
                Text(
                    selectedBox?.let { "Remote runtime on ${it.name}" } ?: "No box selected",
                    color = TvColors.TextSecondary,
                    fontSize = 20.sp,
                )
            }
            Spacer(Modifier.weight(1f))
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                TvTextButton(label = "Update agent", onClick = { nav.navigate(Routes.UPDATE_AGENT) })
                TvTextButton(
                    label = "Sign out",
                    danger = true,
                    onClick = { store.signOut() },
                )
            }
        }

        when {
            selectedBox == null -> {
                if (autoConnecting) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        CircularProgressIndicator(modifier = Modifier.size(32.dp), color = TvColors.Accent, strokeWidth = 3.dp)
                        Text(
                            autoConnectTarget?.let { "Connecting to $it…" } ?: "Connecting…",
                            color = TvColors.TextPrimary,
                            fontSize = 26.sp,
                            fontWeight = FontWeight.SemiBold,
                        )
                    }
                    Text(
                        "Connecting automatically. This opens the moment your machine is ready.",
                        color = TvColors.TextSecondary,
                        fontSize = 19.sp,
                    )
                    TvTextButton(label = "Choose a machine myself", onClick = {
                        store.cancelAutoConnect()
                        nav.navigate(Routes.MACHINES)
                    })
                } else {
                    Text("Pick a machine", color = TvColors.TextPrimary, fontSize = 26.sp, fontWeight = FontWeight.SemiBold)
                    Text(
                        "Choose one of the machines on your account, or type a LAN address. A machine appears here once it's running `yaver serve` signed in as you.",
                        color = TvColors.TextSecondary,
                        fontSize = 19.sp,
                    )
                    TvTextButton(label = "Choose machine", onClick = { nav.navigate(Routes.MACHINES) })
                }
            }
            else -> {
                selectedMachineCard(selectedBox, reachable)
                wakePanel(selectedBox, lifecycle, isRunning, phase, percent, clientBlocked, wakeError, token)

                // One predictable horizontal rail — Chat / Vibing / Devices /
                // Settings. Every primary surface is visible at once.
                Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    TvTile(icon = "💬", title = "Chat", onClick = { nav.navigate(Routes.TASKS) })
                    TvTile(icon = "▶", title = "Vibing", onClick = { nav.navigate(Routes.VIBING) })
                    TvTile(icon = ">_", title = "SSH", onClick = { nav.navigate(Routes.SSH) })
                    TvTile(icon = "🖥", title = "Devices", onClick = { nav.navigate(Routes.MACHINES) })
                    TvTile(icon = "⚙", title = "Settings", onClick = { nav.navigate(Routes.SETTINGS) })
                }
            }
        }
    }
}

@Composable
private fun selectedMachineCard(box: BoxTarget?, reachable: Boolean?) {
    val (color, chip) = when (reachable) {
        true -> TvColors.Green to "Connected"
        false -> TvColors.Orange to "Unreachable"
        null -> TvColors.TextMuted to "Checking…"
    }
    Row(
        modifier = Modifier.fillMaxWidth().background(TvColors.Card, RoundedCornerShape(18.dp)).padding(24.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier.size(width = 54.dp, height = 54.dp)
                .background(color.copy(alpha = 0.16f), RoundedCornerShape(12.dp)),
            contentAlignment = Alignment.Center,
        ) {
            Text("🖥", fontSize = 28.sp)
        }
        Column(Modifier.padding(horizontal = 22.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(box?.name ?: "Selected machine", color = TvColors.TextPrimary, fontSize = 26.sp, fontWeight = FontWeight.Bold)
                box?.aliasLabel?.let { Text(it, color = TvColors.TextSecondary, fontSize = 19.sp, fontWeight = FontWeight.SemiBold) }
                StatusChip(chip, color)
            }
            Text(machineDetail(box), color = TvColors.TextSecondary, fontSize = 17.sp, maxLines = 1)
        }
        Spacer(Modifier.weight(1f))
    }
}

private fun machineDetail(box: BoxTarget?): String {
    if (box == null) return "No machine selected"
    val parts = mutableListOf<String>()
    if (box.host.isNotEmpty()) parts.add(box.host)
    if (box.wakeable) parts.add("wakeable")
    if (!box.relayBaseUrl.isNullOrEmpty()) parts.add("relay fallback")
    return if (parts.isEmpty()) "Account relay" else parts.joinToString(" · ")
}

@Composable
private fun wakePanel(
    box: BoxTarget?,
    lifecycle: BoxLifecycle,
    isRunning: Boolean,
    phase: BoxPhase,
    percent: Int,
    clientBlocked: String?,
    wakeError: String?,
    token: String,
) {
    if (isRunning) {
        WakeProgress(phase = phase, percent = percent)
        return
    }
    if (clientBlocked != null) {
        // NOT ASLEEP — THIS DEVICE REFUSED THE REQUEST. Deliberately NO Wake
        // button: waking cannot fix a client-side policy.
        Column(
            modifier = Modifier.fillMaxWidth().background(TvColors.Card, RoundedCornerShape(20.dp)).padding(28.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text("This device blocked the connection", color = TvColors.Orange, fontSize = 28.sp, fontWeight = FontWeight.Bold)
            Text(clientBlocked, color = TvColors.TextSecondary, fontSize = 19.sp)
        }
        return
    }
    if ((lifecycle.needsWake || wakeError != null) && box != null) {
        Column(
            modifier = Modifier.fillMaxWidth().background(TvColors.Card, RoundedCornerShape(20.dp)).padding(28.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text("Box asleep", color = TvColors.Orange, fontSize = 28.sp, fontWeight = FontWeight.Bold)
            if (box.wakeable) {
                Text(
                    "${box.name} isn't answering. It may have parked itself to save cost. Wake it to keep working.",
                    color = TvColors.TextSecondary,
                    fontSize = 19.sp,
                )
                TvTextButton(label = if (wakeError == null) "Wake" else "Try again", onClick = {
                    lifecycle.wake(box, token)
                })
            } else {
                Text(
                    "${box.name} isn't answering, and it can't be woken from the TV — start it from your computer or phone.",
                    color = TvColors.TextSecondary,
                    fontSize = 19.sp,
                )
            }
            wakeError?.let { Text(it, color = TvColors.Red, fontSize = 16.sp, fontFamily = FontFamily.Monospace) }
        }
    }
}
