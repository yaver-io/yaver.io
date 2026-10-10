package io.yaver.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavHostController
import io.yaver.tv.AgentError
import io.yaver.tv.ProjectRow
import io.yaver.tv.TvStore

/** Configuration first; after launch, exactly mobile 30% / raw SSH 70%. */
@Composable
fun StudioScreen(store: TvStore, nav: NavHostController) {
    val boxes by store.boxes.collectAsState()
    val box by store.selectedBox.collectAsState()
    val token by store.token.collectAsState()
    var projects by remember { mutableStateOf<List<ProjectRow>>(emptyList()) }
    var projectName by remember { mutableStateOf("") }
    var runner by remember { mutableStateOf("terminal") }
    var lane by remember { mutableStateOf("Phone") }
    var launched by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(box?.id) {
        projects = box?.let { selected ->
            runCatching { store.clientFor(selected).listProjects() }
                .onFailure { error = (it as? AgentError)?.message ?: it.message }
                .getOrDefault(emptyList())
        }.orEmpty()
        if (projects.none { it.name == projectName }) projectName = projects.firstOrNull()?.name.orEmpty()
    }

    val project = projects.firstOrNull { it.name == projectName }
    if (launched && box != null && project != null) {
        Row(Modifier.fillMaxSize().background(TvColors.Bg).padding(8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Box(
                Modifier.weight(0.30f).fillMaxHeight()
                    .background(TvColors.Card, RoundedCornerShape(22.dp))
                    .border(5.dp, TvColors.Border, RoundedCornerShape(22.dp))
                    .padding(5.dp),
                contentAlignment = Alignment.Center,
            ) {
                Box(Modifier.fillMaxHeight().aspectRatio(if (lane == "Phone") 390f / 844f else if (lane == "Tablet") 820f / 1180f else 16f / 9f)) {
                    PreviewStreamScreen(store, nav, project.name, embedded = true)
                }
            }
            Box(Modifier.weight(0.70f).fillMaxHeight()) {
                TerminalPane(box = box!!, token = token, launch = runner, cwd = project.path, embedded = true)
            }
        }
        return
    }

    Column(
        Modifier.fillMaxSize().background(TvColors.Bg).verticalScroll(rememberScrollState()).padding(56.dp),
        verticalArrangement = Arrangement.spacedBy(22.dp),
    ) {
        BackBar("Studio", "Configure first; then mobile 30% / SSH console 70%", onBack = { nav.popBackStack() })
        SectionTitle("RUNNER PC")
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            boxes.forEach { candidate -> TvChip(candidate.aliasLabel ?: candidate.name, candidate.id == box?.id) { store.selectBox(candidate) } }
        }
        SectionTitle("PROJECT")
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            projects.take(8).forEach { candidate -> TvChip(candidate.name, candidate.name == projectName) { projectName = candidate.name } }
        }
        SectionTitle("RUNNER")
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            listOf("terminal" to "Shell / tmux", "codex" to "Codex", "claude" to "Claude Code", "opencode" to "OpenCode")
                .forEach { (id, label) -> TvChip(label, runner == id) { runner = id } }
        }
        SectionTitle("LANE")
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            listOf("Phone", "Tablet", "TV").forEach { form -> TvChip(form, lane == form) { lane = form } }
        }
        Text("Layout · mobile 30% / SSH console 70%", color = TvColors.TextSecondary, fontSize = 18.sp, fontFamily = FontFamily.Monospace)
        error?.let { Text(it, color = TvColors.Red, fontSize = 17.sp, fontFamily = FontFamily.Monospace) }
        if (box != null && project != null) {
            TvTextButton("Save and open Studio", onClick = { launched = true })
        } else {
            Text("Choose a runner PC and project to continue.", color = TvColors.TextMuted, fontSize = 17.sp)
        }
        Spacer(Modifier.weight(1f))
    }
}
