package io.yaver.wear.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.wear.compose.material.Button
import androidx.wear.compose.material.ButtonDefaults
import androidx.wear.compose.material.MaterialTheme
import androidx.wear.compose.material.Text

/**
 * The confirm gate — shown when the runner (via the phone) sends a
 * `confirm-needed` reply for a risky write/deploy verb.
 *
 * Per the design invariant, EVERY write/deploy is confirm-gated and the PHONE
 * decides what needs confirming; the watch only renders the prompt and sends
 * back confirm/cancel with the opaque token. Wrist taps are easy to fire by
 * accident, so this is a deliberate two-button choice — no swipe-to-confirm.
 */
@Composable
fun ConfirmScreen(
    prompt: String,
    onConfirm: () -> Unit,
    onCancel: () -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = prompt,
            textAlign = TextAlign.Center,
            style = MaterialTheme.typography.title3,
            maxLines = 4,
        )
        Spacer(modifier = Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.Center) {
            // Cancel is the safe default — placed first / left.
            Button(
                onClick = onCancel,
                colors = ButtonDefaults.secondaryButtonColors(),
            ) {
                Text("Cancel")
            }
            Spacer(modifier = Modifier.width(12.dp))
            Button(
                onClick = onConfirm,
                colors = ButtonDefaults.primaryButtonColors(),
            ) {
                Text("Confirm")
            }
        }
    }
}
