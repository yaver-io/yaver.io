package io.yaver.wear.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.wear.compose.material.Button
import androidx.wear.compose.material.Chip
import androidx.wear.compose.material.ChipDefaults
import androidx.wear.compose.material.CircularProgressIndicator
import androidx.wear.compose.material.MaterialTheme
import androidx.wear.compose.material.Text
import io.yaver.wear.WatchProtocol
import io.yaver.wear.WatchState

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
) {
    val line by WatchState.line.collectAsState()
    val phase by WatchState.phase.collectAsState()
    val phoneReachable by WatchState.phoneReachable.collectAsState()

    MaterialTheme {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(8.dp),
            contentAlignment = Alignment.Center,
        ) {
            when (val p = phase) {
                is WatchState.Phase.Confirm ->
                    ConfirmScreen(
                        prompt = p.prompt,
                        onConfirm = { onConfirm(p.token) },
                        onCancel = { onCancel(p.token) },
                    )

                else -> MainScreen(
                    line = line,
                    phase = phase,
                    phoneReachable = phoneReachable,
                    onRecord = onRecord,
                    onIntent = onIntent,
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
                Button(onClick = onRecord) {
                    Text("Speak")
                }
                if (!phoneReachable) {
                    Spacer(modifier = Modifier.height(6.dp))
                    Text(
                        text = "Phone not reachable",
                        textAlign = TextAlign.Center,
                        style = MaterialTheme.typography.caption2,
                    )
                }
                // Quick one-tap intents (the "complication" equivalents on-screen).
                Spacer(modifier = Modifier.height(8.dp))
                QuickIntentChip("Run tests", WatchProtocol.FixedIntent.RUN_TESTS, onIntent)
                QuickIntentChip("Status", WatchProtocol.FixedIntent.STATUS, onIntent)
            }
        }
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
