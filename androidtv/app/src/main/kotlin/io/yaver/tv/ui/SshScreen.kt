package io.yaver.tv.ui

import android.view.KeyEvent
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavHostController
import io.yaver.tv.BoxTarget
import io.yaver.tv.TV_SURFACE_ID
import io.yaver.tv.Speech
import io.yaver.tv.TvStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/** Android TV counterpart of tvOS TVSSHView: physical-keyboard-first PTY,
 * with the system/phone text field as the command fallback. */
@Composable
fun SshScreen(store: TvStore, nav: NavHostController) {
    val box by store.selectedBox.collectAsState()
    val token by store.token.collectAsState()
    var launch by remember { mutableStateOf<String?>(null) }

    if (launch == null || box == null) {
        Column(
            Modifier.fillMaxSize().background(TvColors.Bg).padding(56.dp),
            verticalArrangement = Arrangement.spacedBy(24.dp),
        ) {
            BackBar("SSH", "Terminal, tmux, and coding agents", onBack = { nav.popBackStack() })
            Text(
                "Use a Bluetooth or USB keyboard for direct terminal control. The Android TV / Google TV text keyboard can send whole commands when no hardware keyboard is attached.",
                color = TvColors.TextSecondary, fontSize = 20.sp,
            )
            if (box == null) {
                Text("Choose a machine first.", color = TvColors.Orange, fontSize = 24.sp, fontWeight = FontWeight.Bold)
                TvTextButton("Choose machine", onClick = { nav.navigate(Routes.MACHINES) })
            } else {
                Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                    listOf(
                        "terminal" to "Raw shell",
                        "codex" to "Codex",
                        "claude" to "Claude Code",
                        "opencode" to "OpenCode",
                    ).forEach { (mode, label) -> TvTextButton(label, onClick = { launch = mode }) }
                }
                Text("Machine: ${box?.aliasLabel ?: box?.name}", color = TvColors.TextSecondary,
                    fontSize = 18.sp, fontFamily = FontFamily.Monospace)
            }
        }
        return
    }

    TerminalPane(box = box!!, token = token, launch = launch!!, onExit = { launch = null })
}

@Composable
internal fun TerminalPane(
    box: BoxTarget,
    token: String,
    launch: String,
    cwd: String? = null,
    embedded: Boolean = false,
    onExit: () -> Unit = {},
) {
    val context = LocalContext.current
    val preferences = remember { context.getSharedPreferences("yaver-studio", android.content.Context.MODE_PRIVATE) }
    val speech = remember { Speech(context.applicationContext) }
    val controller = remember(box.id, token, launch, cwd) { AndroidTvTerminal(box, token, launch, cwd) }
    val screen by controller.screen.collectAsState()
    val status by controller.status.collectAsState()
    val error by controller.error.collectAsState()
    val focus = remember { FocusRequester() }
    var command by remember { mutableStateOf("") }
    var fontSize by remember { mutableStateOf(preferences.getFloat("terminal-font-size", 18f)) }
    var speaking by remember { mutableStateOf(false) }
    val scroll = rememberScrollState()

    DisposableEffect(controller) {
        controller.connect()
        onDispose { speech.shutdown(); controller.close() }
    }
    LaunchedEffect(Unit) { focus.requestFocus() }
    LaunchedEffect(screen) { scroll.scrollTo(scroll.maxValue) }

    Column(
        Modifier.fillMaxSize().background(Color(0xFF090C10))
            .focusRequester(focus).focusable()
            .onPreviewKeyEvent { controller.handleKey(it.nativeKeyEvent) },
    ) {
        Row(Modifier.fillMaxWidth().background(Color(0xFF11151B)).padding(18.dp),
            horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            if (!embedded) TvTextButton("Exit SSH", onClick = { controller.close(); onExit() })
            Text("${box.aliasLabel ?: box.name} · ${if (launch == "terminal") "shell" else launch}",
                color = TvColors.TextPrimary, fontSize = 20.sp, fontWeight = FontWeight.Bold,
                fontFamily = FontFamily.Monospace)
            Spacer(Modifier.weight(1f))
            Text(status, color = if (status.contains("ready")) TvColors.Green else TvColors.Orange,
                fontSize = 16.sp, fontFamily = FontFamily.Monospace)
            TvTextButton("A−", onClick = {
                fontSize = (fontSize - 1f).coerceAtLeast(13f)
                preferences.edit().putFloat("terminal-font-size", fontSize).apply()
            })
            TvTextButton("${fontSize.toInt()}", onClick = {
                fontSize = 18f
                preferences.edit().putFloat("terminal-font-size", fontSize).apply()
            })
            TvTextButton("A+", onClick = {
                fontSize = (fontSize + 1f).coerceAtMost(34f)
                preferences.edit().putFloat("terminal-font-size", fontSize).apply()
            })
            TvTextButton(if (speaking) "Stop voice" else "Read output", onClick = {
                if (speaking) speech.stop() else if (screen.isNotEmpty()) speech.speakSummary(screen)
                speaking = !speaking
            })
            TvTextButton("Reconnect", onClick = { controller.connect() })
        }

        Box(Modifier.weight(1f).fillMaxWidth().verticalScroll(scroll).padding(26.dp)) {
            Text(if (screen.isEmpty()) "Connecting to the PTY…" else screen,
                color = Color(0xFFD1D9E3), fontSize = fontSize.sp, fontFamily = FontFamily.Monospace)
        }
        error?.let {
            Text(it, color = TvColors.Red, fontSize = 16.sp, fontFamily = FontFamily.Monospace,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 8.dp))
        }
        Row(Modifier.fillMaxWidth().background(Color(0xFF10141A)).padding(14.dp),
            horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            TvTextButton("Esc", onClick = { controller.send(byteArrayOf(0x1b)) })
            TvTextButton("Ctrl-C", onClick = { controller.send(byteArrayOf(0x03)) })
            TvTextButton("Ctrl-B", onClick = { controller.send(byteArrayOf(0x02)) })
            TvTextButton("Detach", onClick = { controller.send(byteArrayOf(0x02, 0x64)) })
            OutlinedTextField(
                value = command, onValueChange = { command = it }, singleLine = true,
                label = { Text("Voice or command from TV keyboard") }, modifier = Modifier.weight(1f),
            )
            TvTextButton("Send", onClick = {
                if (command.isNotEmpty()) {
                    controller.send(command.toByteArray(Charsets.UTF_8) + byteArrayOf(0x0d))
                    command = ""
                    focus.requestFocus()
                }
            })
        }
    }
}

private class AndroidTvTerminal(
    private val box: BoxTarget,
    private val token: String,
    private val launch: String,
    private val cwd: String? = null,
) {
    private val _screen = MutableStateFlow("")
    val screen: StateFlow<String> = _screen
    private val _status = MutableStateFlow("connecting")
    val status: StateFlow<String> = _status
    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error

    private val http = OkHttpClient.Builder().connectTimeout(8, TimeUnit.SECONDS).build()
    private var socket: WebSocket? = null
    private var endpointIndex = 0
    @Volatile private var closed = false
    private val lock = Any()

    private val terminal = TvTerminalBuffer(columns = 110, rows = 34)

    fun connect() {
        closeSocket()
        closed = false
        endpointIndex = 0
        _error.value = null
        openNext()
    }

    private fun openNext() {
        if (closed) return
        val endpoints = box.requestEndpoints("/ws/terminal")
        if (endpointIndex >= endpoints.size) {
            _status.value = "unreachable"
            _error.value = "The PTY could not connect over LAN or relay. Make sure yaver serve is running on ${box.name}."
            return
        }
        val endpoint = endpoints[endpointIndex++]
        val base = endpoint.url.toHttpUrl()
        val urlBuilder = base.newBuilder()
            .scheme(if (base.isHttps) "wss" else "ws")
            .addQueryParameter("term", "xterm-256color")
        if (launch == "terminal") {
            urlBuilder.addQueryParameter("profile_tmux", "yaver-studio")
            urlBuilder.addQueryParameter("profile_shell", "default")
        } else {
            urlBuilder.addQueryParameter("launch", launch)
        }
        cwd?.takeIf { it.isNotBlank() }?.let { urlBuilder.addQueryParameter("cwd", it) }
        val url = urlBuilder.build()
        val request = Request.Builder().url(url)
            .header("Authorization", "Bearer $token")
            .header("X-Yaver-Surface", TV_SURFACE_ID)
            .apply {
                if (endpoint.relay) box.relayPassword?.takeIf { it.isNotEmpty() }
                    ?.let { header("X-Relay-Password", it) }
            }.build()
        _status.value = if (endpoint.relay) "connecting via relay" else "connecting via LAN"
        socket = http.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                _status.value = if (endpoint.relay) "relay · keyboard ready" else "LAN · keyboard ready"
                webSocket.send(JSONObject().put("resize", JSONObject().put("cols", 110).put("rows", 34)).toString())
            }
            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                synchronized(lock) {
                    val data = bytes.toByteArray()
                    terminal.feed(data)
                    _screen.value = terminal.renderedText
                }
            }
            override fun onMessage(webSocket: WebSocket, text: String) {
                runCatching { JSONObject(text).optString("error") }.getOrNull()?.takeIf { it.isNotEmpty() }
                    ?.let { _error.value = it }
            }
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (!closed) openNext()
            }
            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (!closed && code != 1000) openNext()
            }
        })
    }

    fun send(bytes: ByteArray) { socket?.send(bytes.toByteString()) }

    fun handleKey(event: KeyEvent): Boolean {
        if (event.action != KeyEvent.ACTION_DOWN) return false
        val bytes: ByteArray? = when (event.keyCode) {
            KeyEvent.KEYCODE_DPAD_UP -> "\u001b[A".toByteArray()
            KeyEvent.KEYCODE_DPAD_DOWN -> "\u001b[B".toByteArray()
            KeyEvent.KEYCODE_DPAD_LEFT -> "\u001b[D".toByteArray()
            KeyEvent.KEYCODE_DPAD_RIGHT -> "\u001b[C".toByteArray()
            KeyEvent.KEYCODE_ESCAPE -> byteArrayOf(0x1b)
            KeyEvent.KEYCODE_TAB -> byteArrayOf(0x09)
            KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> byteArrayOf(0x0d)
            KeyEvent.KEYCODE_DEL -> byteArrayOf(0x7f)
            else -> {
                if (event.isCtrlPressed && event.keyCode in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z) {
                    byteArrayOf((event.keyCode - KeyEvent.KEYCODE_A + 1).toByte())
                } else {
                    val codePoint = event.unicodeChar
                    if (codePoint == 0) null else {
                        val text = String(Character.toChars(codePoint)).toByteArray(Charsets.UTF_8)
                        if (event.isAltPressed) byteArrayOf(0x1b) + text else text
                    }
                }
            }
        }
        if (bytes == null) return false
        send(bytes)
        return true
    }

    fun close() { closed = true; closeSocket(); _status.value = "detached" }
    private fun closeSocket() { socket?.close(1000, "detach"); socket = null }
}
