package io.yaver.tv.ui

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
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
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavHostController
import io.yaver.tv.AgentError
import io.yaver.tv.FrameworkStyle
import io.yaver.tv.ProjectRow
import io.yaver.tv.TvStore
import io.yaver.tv.devServerOwnsProject
import io.yaver.tv.filteredVibingProjects
import io.yaver.tv.isRenderableVibingProject
import io.yaver.tv.vibingCaptureTarget
import io.yaver.tv.vibingCapabilityLabels
import io.yaver.tv.vibingDisplayFramework
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext

/** Native Android TV twin of tvOS VibingView: the same four-column searchable
 * app grid, framework identity, compact surface labels, and no backend-only
 * repository cards. */
@Composable
fun VibingScreen(store: TvStore, nav: NavHostController) {
    val box by store.selectedBox.collectAsState()
    var projects by remember { mutableStateOf<List<ProjectRow>>(emptyList()) }
    var query by remember { mutableStateOf("") }
    var loading by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(box?.id) {
        loading = true
        error = null
        try {
            projects = box?.let { store.clientFor(it).listProjects() }.orEmpty()
                .filter(::isRenderableVibingProject)
                .sortedBy { it.name.lowercase() }
        } catch (failure: Throwable) {
            error = (failure as? AgentError)?.message ?: failure.message ?: "Couldn't load apps."
        } finally {
            loading = false
        }
    }

    val visible = filteredVibingProjects(projects, query)
    Column(
        modifier = Modifier.fillMaxSize().background(TvColors.Bg).padding(horizontal = 56.dp, vertical = 34.dp),
        verticalArrangement = Arrangement.spacedBy(20.dp),
    ) {
        BackBar(
            "Vibing",
            box?.name?.let { "Renderable apps on $it" },
            onBack = { nav.popBackStack() },
        )
        OutlinedTextField(
            value = query,
            onValueChange = { query = it },
            modifier = Modifier.width(420.dp),
            singleLine = true,
            label = { Text("Search apps") },
            colors = OutlinedTextFieldDefaults.colors(
                focusedTextColor = TvColors.TextPrimary,
                unfocusedTextColor = TvColors.TextPrimary,
                focusedBorderColor = TvColors.Accent,
                unfocusedBorderColor = TvColors.Border,
                focusedLabelColor = TvColors.Accent,
                unfocusedLabelColor = TvColors.TextSecondary,
            ),
        )
        when {
            loading -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator(color = TvColors.Accent)
            }
            error != null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text(error.orEmpty(), color = TvColors.Orange, fontSize = 20.sp)
            }
            visible.isEmpty() -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text(
                    if (query.isBlank()) "No mobile, web, frontend, or TV apps were found on this machine."
                    else "No renderable app matches “$query”.",
                    color = TvColors.TextSecondary,
                    fontSize = 20.sp,
                )
            }
            else -> LazyVerticalGrid(
                columns = GridCells.Fixed(4),
                modifier = Modifier.fillMaxSize(),
                horizontalArrangement = Arrangement.spacedBy(20.dp),
                verticalArrangement = Arrangement.spacedBy(20.dp),
            ) {
                items(visible, key = { it.path }) { project ->
                    VibingProjectCard(project) { nav.navigate(Routes.preview(project.name)) }
                }
            }
        }
    }
}

@Composable
private fun VibingProjectCard(project: ProjectRow, onClick: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    val style = FrameworkStyle.of(vibingDisplayFramework(project))
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .height(190.dp)
            .graphicsLayer {
                scaleX = if (focused) 1.035f else 1f
                scaleY = if (focused) 1.035f else 1f
            }
            .border(if (focused) 3.dp else 1.dp, if (focused) style.color else TvColors.Border, RoundedCornerShape(20.dp))
            .background(if (focused) TvColors.CardElevated else TvColors.Card, RoundedCornerShape(20.dp))
            .onFocusChanged { focused = it.hasFocus }
            .clickable(onClick = onClick)
            .focusable()
            .padding(22.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(style.glyph, color = style.color, fontSize = 34.sp, fontWeight = FontWeight.Black)
            Spacer(Modifier.weight(1f))
            Text("▶", color = TvColors.TextSecondary, fontSize = 15.sp)
        }
        Text(
            project.name,
            color = TvColors.TextPrimary,
            fontSize = 23.sp,
            fontWeight = FontWeight.Bold,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            listOfNotNull(project.framework, project.branch).joinToString(" · ").ifEmpty { "App" },
            color = TvColors.TextSecondary,
            fontSize = 15.sp,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.weight(1f))
        Row(horizontalArrangement = Arrangement.spacedBy(7.dp)) {
            vibingCapabilityLabels(project).take(3).forEach { label ->
                Text(
                    label.uppercase(),
                    modifier = Modifier.background(style.color.copy(alpha = 0.14f), CircleShape)
                        .padding(horizontal = 9.dp, vertical = 4.dp),
                    color = style.color,
                    fontSize = 11.sp,
                    fontWeight = FontWeight.Black,
                )
            }
        }
    }
}

/** Android TV's current media decoder is the authenticated frame lane. It
 * starts the selected checkout, verifies that checkout owns `/dev/status`,
 * then keeps the last good pixels visible while polling. The catalog/UI stays
 * deliberately parallel to tvOS; WebRTC can replace only this media seam. */
@Composable
fun PreviewStreamScreen(store: TvStore, nav: NavHostController, projectName: String, embedded: Boolean = false) {
    val box by store.selectedBox.collectAsState()
    var project by remember { mutableStateOf<ProjectRow?>(null) }
    var bitmap by remember { mutableStateOf<Bitmap?>(null) }
    var status by remember { mutableStateOf("Opening $projectName…") }
    var error by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(box?.id, projectName) {
        val selectedBox = box ?: run {
            error = "No render machine selected."
            return@LaunchedEffect
        }
        val client = store.clientFor(selectedBox)
        try {
            project = client.listProjects().firstOrNull { it.name.equals(projectName, ignoreCase = true) }
                ?: throw AgentError("$projectName is not on ${selectedBox.name}.")
            val app = project!!
            status = "Starting ${app.name}…"
            var dev = client.devStart(app.name, app.path, app.framework)
            val readyBy = System.currentTimeMillis() + 150_000
            while (System.currentTimeMillis() < readyBy) {
                val ownsProject = devServerOwnsProject(dev.optString("workDir"), app.path)
                val ready = dev.optBoolean("serving", false) ||
                    (dev.optBoolean("running", false) && !dev.optBoolean("building", false))
                dev.optString("error").takeIf(String::isNotBlank)?.let { throw AgentError(it) }
                if (ownsProject && ready) break
                status = dev.optString("servingLabel").ifBlank { "Preparing ${app.name}…" }
                delay(600)
                dev = client.devStatus()
            }
            if (!devServerOwnsProject(dev.optString("workDir"), app.path)) {
                throw AgentError("The render machine is still serving another checkout instead of ${app.name}.")
            }

            var web = org.json.JSONObject()
            if (app.framework.orEmpty().lowercase() in setOf("expo", "react-native", "reactnative", "rn")) {
                status = "Starting the mobile web runtime…"
                web = client.webPreviewStart()
                val webBy = System.currentTimeMillis() + 150_000
                while (dev.optInt("webPort", 0) <= 0 && System.currentTimeMillis() < webBy) {
                    delay(600)
                    dev = client.devStatus()
                }
            }
            val target = vibingCaptureTarget(dev, web)
            if (target.isBlank()) throw AgentError("${app.name} became ready but exposed no browser URL.")
            status = "Opening ${app.name}…"
            client.vibingPreviewStart(app.name, target, app.path)
            while (true) {
                val snapshot = client.vibingPreviewSnapshot(app.name)
                val hash = snapshot.optString("hash")
                if (hash.isNotBlank()) {
                    client.vibingPreviewFrame(hash, app.name)?.let { bytes ->
                        BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.let { frame ->
                            bitmap = frame
                            status = "Live"
                        }
                    }
                }
                delay(350)
            }
        } catch (failure: Throwable) {
            error = (failure as? AgentError)?.message ?: failure.message ?: "Preview unavailable."
        } finally {
            project?.let { app ->
                withContext(NonCancellable) { client.vibingPreviewStop(app.name) }
            }
        }
    }

    Column(Modifier.fillMaxSize().background(TvColors.Bg).padding(horizontal = if (embedded) 8.dp else 42.dp, vertical = if (embedded) 8.dp else 28.dp)) {
        if (!embedded) {
            BackBar(projectName, box?.name?.let { "Live preview on $it" }, onBack = { nav.popBackStack() })
            Spacer(Modifier.height(18.dp))
        }
        Box(
            Modifier.fillMaxWidth().fillMaxHeight()
                .background(TvColors.Card, RoundedCornerShape(24.dp))
                .border(1.dp, TvColors.Border, RoundedCornerShape(24.dp)),
            contentAlignment = Alignment.Center,
        ) {
            when {
                bitmap != null -> androidx.compose.foundation.Image(
                    bitmap = bitmap!!.asImageBitmap(),
                    contentDescription = "$projectName live preview",
                    modifier = Modifier.fillMaxSize().padding(18.dp),
                    contentScale = ContentScale.Fit,
                )
                error != null -> Text(error.orEmpty(), color = TvColors.Orange, fontSize = 20.sp, modifier = Modifier.padding(40.dp))
                else -> Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(14.dp)) {
                    CircularProgressIndicator(color = TvColors.Accent)
                    Text(status, color = TvColors.TextSecondary, fontSize = 18.sp)
                }
            }
        }
    }
}
