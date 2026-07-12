package io.yaver.wear.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.background
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.wear.compose.material.Chip
import androidx.wear.compose.material.ChipDefaults
import androidx.wear.compose.material.CircularProgressIndicator
import androidx.wear.compose.material.MaterialTheme
import androidx.wear.compose.material.Text
import io.yaver.wear.BoxLifecycle

/**
 * The box wake/park progress surface.
 *
 * Rendered whenever [BoxLifecycle.status] is not [BoxLifecycle.WakeStatus.None]:
 *   - Asleep      → a "Box asleep" line + a big Wake chip.
 *   - Waking      → a circular bar filled to the phase percent, the phase label,
 *                   and a row of step dots (Waking..Ready) filled to here.
 *   - PhoneNeeded → tell the user to open Yaver on their phone.
 *
 * Purely a function of the passed [status] — the same canonical ladder the phone
 * and TV render, so "waking up" reads identically on the wrist.
 */
@Composable
fun WakeProgress(
    status: BoxLifecycle.WakeStatus,
    onWake: () -> Unit,
    onDismiss: () -> Unit,
) {
    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        when (status) {
            is BoxLifecycle.WakeStatus.Asleep -> AsleepView(onWake = onWake, onDismiss = onDismiss)
            is BoxLifecycle.WakeStatus.Waking -> WakingView(phase = status.phase)
            is BoxLifecycle.WakeStatus.PhoneNeeded -> PhoneNeededView(onDismiss = onDismiss)
            is BoxLifecycle.WakeStatus.None -> Unit // caller shouldn't render this
        }
    }
}

@Composable
private fun AsleepView(onWake: () -> Unit, onDismiss: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = "Box asleep",
            textAlign = TextAlign.Center,
            style = MaterialTheme.typography.title3,
            maxLines = 2,
        )
        Spacer(modifier = Modifier.height(4.dp))
        Text(
            text = "Parked to save cost",
            textAlign = TextAlign.Center,
            style = MaterialTheme.typography.caption2,
            color = MaterialTheme.colors.onSurfaceVariant,
        )
        Spacer(modifier = Modifier.height(12.dp))
        Chip(
            label = { Text("Wake") },
            onClick = onWake,
            colors = ChipDefaults.primaryChipColors(),
        )
        Spacer(modifier = Modifier.height(6.dp))
        Chip(
            label = { Text("Later") },
            onClick = onDismiss,
            colors = ChipDefaults.secondaryChipColors(),
        )
    }
}

@Composable
private fun WakingView(phase: BoxLifecycle.WakePhase) {
    // The bar wraps the whole watch face; label + dots sit inside it.
    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        CircularProgressIndicator(
            progress = phase.percent / 100f,
            modifier = Modifier.fillMaxSize(),
            strokeWidth = 4.dp,
        )
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = "Waking your box",
                textAlign = TextAlign.Center,
                style = MaterialTheme.typography.caption1,
                color = MaterialTheme.colors.onSurfaceVariant,
            )
            Spacer(modifier = Modifier.height(2.dp))
            Text(
                text = phase.label,
                textAlign = TextAlign.Center,
                style = MaterialTheme.typography.title3,
                maxLines = 1,
            )
            Spacer(modifier = Modifier.height(8.dp))
            StepDots(phase = phase)
        }
    }
}

/** A row of small dots, one per moving step, filled up to (and including) the
 *  current phase — the wrist-sized version of the phone's stepper. */
@Composable
private fun StepDots(phase: BoxLifecycle.WakePhase) {
    Row(
        horizontalArrangement = Arrangement.Center,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        BoxLifecycle.WAKE_STEPS.forEach { step ->
            val reached = step.percent <= phase.percent
            val color =
                if (reached) MaterialTheme.colors.primary
                else MaterialTheme.colors.onSurfaceVariant
            Box(
                modifier = Modifier
                    .size(if (step == phase) 7.dp else 5.dp)
                    .clip(CircleShape)
                    .background(color),
            )
            Spacer(modifier = Modifier.width(4.dp))
        }
    }
}

@Composable
private fun PhoneNeededView(onDismiss: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = "Open Yaver on your phone to wake the box.",
            textAlign = TextAlign.Center,
            style = MaterialTheme.typography.title3,
            maxLines = 4,
        )
        Spacer(modifier = Modifier.height(12.dp))
        Chip(
            label = { Text("OK") },
            onClick = onDismiss,
            colors = ChipDefaults.secondaryChipColors(),
        )
    }
}
