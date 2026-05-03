package io.yaver.mobile

// YaverAgentsPane (Android) — counterpart to iOS YaverAgentsPane.swift +
// YaverRunnerAuthFlowPane + YaverOpenCodeConfigPane. Three rows
// (Claude / Codex / OpenCode) reading /runner-auth/status; Claude+Codex
// taps push a flow pane that opens ChromeCustomTabs and (for Claude)
// shows paste-back input + Submit. OpenCode tap opens the API-key /
// build-mode config sub-pane.
//
// All HTTP work uses the same agent endpoints as iOS — the platform
// difference is purely UI; the API contract is the source of truth.

import android.app.Activity
import android.content.Context
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.browser.customtabs.CustomTabsIntent
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.Executors as Exec2
import java.util.concurrent.TimeUnit

// humanizeRunnerAuthFailure mirrors iOS YaverAgentsPane.swift's helper.
// Maps an /runner-auth/status HTTP failure (status code + raw body) into
// a single user-facing line — never leaks `{"error":"…"}` JSON or naked
// HTTP codes. Same phrase set as iOS so cross-platform users see the
// same vocabulary.
internal fun humanizeRunnerAuthFailure(code: Int, body: String?): String {
  val normalized = run {
    val raw = body?.trim().orEmpty()
    if (raw.isEmpty()) return@run ""
    val parsed = try { JSONObject(raw) } catch (_: Exception) { null }
    if (parsed != null) {
      val msg = parsed.optString("error", parsed.optString("message", ""))
      if (msg.isNotEmpty()) return@run msg.lowercase()
    }
    raw.lowercase()
  }

  if (normalized.contains("relay password") || normalized.contains("invalid relay")) {
    return "Relay password mismatch · re-auth Yaver"
  }
  if (normalized.contains("expired")) {
    return "Session expired · sign in again"
  }
  if (normalized.contains("not authenticated") ||
      normalized.contains("missing or invalid auth") ||
      normalized.contains("invalid token")) {
    return "Not signed in · tap to sign in"
  }
  // Relay returns 404 + `{"error":"subdomain '<x>' not registered"}` when
  // the bundle URL lost its `/d/<deviceId>` prefix and the request hit
  // the relay root instead of routing to an agent — see iOS sibling for
  // the full story.
  if (normalized.contains("subdomain") && normalized.contains("not registered")) {
    return "Reload this app from the device card"
  }
  if (normalized.contains("device not connected")) {
    return "Host agent offline · start Yaver on the device"
  }

  return when (code) {
    401, 403 -> "Not signed in · tap to sign in"
    404 -> "Endpoint missing · update host Yaver"
    408, 504 -> "Agent timed out · try again"
    429 -> "Rate limited · try again later"
    in 500..599 -> "Agent error · try again"
    0 -> "Agent offline · check device"
    else -> "Agent HTTP $code · tap to retry"
  }
}

object YaverAgentsPane {

  private val main = Handler(Looper.getMainLooper())
  private val net = Executors.newCachedThreadPool()
  private var current: View? = null
  private val rowsByRunner = mutableMapOf<String, RowViews>()
  private var inFlightRunner: String? = null

  private data class RowViews(val container: View, val status: TextView)

  fun show(activity: Activity) {
    main.post { presentInternal(activity) }
  }

  private fun presentInternal(activity: Activity) {
    val ctx = activity
    val root = activity.window.decorView as? ViewGroup ?: return
    dismiss()

    val card = LinearLayout(ctx).apply {
      orientation = LinearLayout.VERTICAL
      background = GradientDrawable().apply {
        setColor(Color.argb((0.95 * 255).toInt(), 14, 12, 28))
        cornerRadii = floatArrayOf(
            dp(ctx, 22f), dp(ctx, 22f),
            dp(ctx, 22f), dp(ctx, 22f),
            0f, 0f, 0f, 0f
        )
        setStroke(dp(ctx, 1f).toInt(), Color.argb(40, 255, 255, 255))
      }
      setPadding(dp(ctx, 18f).toInt(), dp(ctx, 16f).toInt(),
                 dp(ctx, 18f).toInt(), dp(ctx, 26f).toInt())
      elevation = dp(ctx, 16f)
    }

    val titleRow = LinearLayout(ctx).apply {
      orientation = LinearLayout.HORIZONTAL
      gravity = Gravity.CENTER_VERTICAL
    }
    val title = TextView(ctx).apply {
      text = "Coding Agents"
      setTextColor(Color.WHITE)
      textSize = 17f
      typeface = android.graphics.Typeface.create(android.graphics.Typeface.DEFAULT, android.graphics.Typeface.BOLD)
      layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f)
    }
    val close = Button(ctx).apply {
      text = "×"; setTextColor(Color.argb(160, 255, 255, 255)); textSize = 22f
      background = null; setOnClickListener { dismiss() }
    }
    titleRow.addView(title); titleRow.addView(close)

    val sub = TextView(ctx).apply {
      text = "tap to sign in or configure"
      setTextColor(Color.argb(140, 255, 255, 255)); textSize = 12f
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
      lp.bottomMargin = dp(ctx, 14f).toInt(); layoutParams = lp
    }

    rowsByRunner.clear()
    val claudeRow = makeRow(ctx, "claude", "Claude Code") { rowTapped(activity, "claude") }
    val codexRow = makeRow(ctx, "codex", "Codex") { rowTapped(activity, "codex") }
    val opencodeRow = makeRow(ctx, "opencode", "OpenCode") { rowTapped(activity, "opencode") }

    rowsByRunner["claude"] = claudeRow
    rowsByRunner["codex"] = codexRow
    rowsByRunner["opencode"] = opencodeRow

    card.addView(titleRow); card.addView(sub)
    card.addView(claudeRow.container); card.addView(codexRow.container); card.addView(opencodeRow.container)

    val params = android.widget.FrameLayout.LayoutParams(
        android.widget.FrameLayout.LayoutParams.MATCH_PARENT,
        android.widget.FrameLayout.LayoutParams.WRAP_CONTENT
    ).apply { gravity = Gravity.BOTTOM }
    card.translationY = dp(ctx, 600f)
    root.addView(card, params)
    current = card
    card.animate().translationY(0f).setDuration(320).start()

    refreshAuthStatus(ctx)
  }

  private fun makeRow(ctx: Context, runnerId: String, label: String,
                      onTap: () -> Unit): RowViews {
    val row = LinearLayout(ctx).apply {
      orientation = LinearLayout.VERTICAL
      background = GradientDrawable().apply {
        cornerRadius = dp(ctx, 14f)
        setColor(Color.argb(15, 255, 255, 255))
      }
      setPadding(dp(ctx, 14f).toInt(), dp(ctx, 12f).toInt(),
                 dp(ctx, 14f).toInt(), dp(ctx, 12f).toInt())
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT,
          LinearLayout.LayoutParams.WRAP_CONTENT
      )
      lp.topMargin = dp(ctx, 4f).toInt(); lp.bottomMargin = dp(ctx, 4f).toInt()
      layoutParams = lp
      isClickable = true
      setOnClickListener { onTap() }
    }
    val name = TextView(ctx).apply {
      text = label
      setTextColor(Color.WHITE)
      textSize = 16f
      typeface = android.graphics.Typeface.create(android.graphics.Typeface.DEFAULT, android.graphics.Typeface.BOLD)
    }
    val status = TextView(ctx).apply {
      text = "checking…"; setTextColor(Color.argb(140, 255, 255, 255)); textSize = 12f
      // Allow status text to wrap to 2 lines so longer humanized error
      // strings ("Relay password mismatch · re-auth Yaver", etc.) aren't
      // clipped to a single line that runs off the right edge.
      maxLines = 2
      ellipsize = android.text.TextUtils.TruncateAt.END
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
      lp.topMargin = dp(ctx, 2f).toInt(); layoutParams = lp
    }
    row.addView(name); row.addView(status)
    return RowViews(row, status)
  }

  fun dismiss() {
    main.post {
      current?.let { v ->
        v.animate().translationY(dp(v.context, 600f)).setDuration(220).withEndAction {
          (v.parent as? ViewGroup)?.removeView(v)
        }.start()
      }
      current = null
    }
  }

  // ---- HTTP helpers (mirror iOS endpoints exactly) ----------------------

  private fun bestAuthToken(prefs: android.content.SharedPreferences): String {
    val inh = prefs.getString(YaverNativePrefs.INHERITED_AUTH_TOKEN, "") ?: ""
    if (inh.isNotEmpty()) return inh
    return prefs.getString(YaverNativePrefs.AGENT_AUTH, "") ?: ""
  }

  private fun refreshAuthStatus(ctx: Context) {
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
    val base = prefs.getString(YaverNativePrefs.AGENT_BASE_URL, "") ?: ""
    val auth = bestAuthToken(prefs)
    if (base.isEmpty()) {
      setRowAll("no agent URL set — load a guest bundle first", Tone.Error); return
    }
    net.execute {
      val resp = httpGet("$base/runner-auth/status", auth)
      main.post {
        if (!resp.ok) {
          // Show a humanized status line instead of the raw JSON body —
          // `HTTP 401: {"error":"invalid relay password"}` is unhelpful UI.
          setRowAll(humanizeRunnerAuthFailure(resp.code, resp.body), Tone.Error); return@post
        }
        val obj = try { JSONObject(resp.body) } catch (e: Exception) { null }
        val arr = obj?.optJSONArray("runners")
        if (arr == null) { setRowAll("Agent returned unexpected data · tap to retry", Tone.Error); return@post }
        // Initialize all to "not installed", then overlay observed.
        listOf("claude", "codex", "opencode").forEach {
          setRow(it, "not installed on agent", Tone.Warning)
        }
        for (i in 0 until arr.length()) {
          val r = arr.optJSONObject(i) ?: continue
          val id = (r.optString("id", "") ?: "").lowercase()
          val key = if (id == "claude-code") "claude" else id
          val installed = r.optBoolean("installed", false)
          val authed = r.optBoolean("authConfigured", false)
          applyRowState(key, installed, authed)
        }
      }
    }
  }

  private fun applyRowState(runner: String, installed: Boolean, authed: Boolean) {
    if (!installed) { setRow(runner, "not installed on agent", Tone.Warning); return }
    if (runner == "opencode") {
      setRow(runner,
             if (authed) "configured · tap to edit" else "tap to set API keys",
             if (authed) Tone.Ok else Tone.Warning)
      return
    }
    setRow(runner,
           if (authed) "✓ signed in · tap to re-auth" else "✗ not signed in · tap to sign in",
           if (authed) Tone.Ok else Tone.Warning)
  }

  private enum class Tone { Ok, Warning, Error }

  private fun setRow(runner: String, msg: String, tone: Tone) {
    val v = rowsByRunner[runner] ?: return
    v.status.text = msg
    v.status.setTextColor(when (tone) {
      Tone.Ok      -> Color.rgb(86, 217, 140)
      Tone.Warning -> Color.rgb(255, 188, 70)
      Tone.Error   -> Color.rgb(255, 116, 116)
    })
  }

  private fun setRowAll(msg: String, tone: Tone) {
    listOf("claude", "codex", "opencode").forEach { setRow(it, msg, tone) }
  }

  // ---- Tap routing ------------------------------------------------------

  private fun rowTapped(activity: Activity, runner: String) {
    if (inFlightRunner != null) return
    if (runner == "opencode") {
      YaverOpenCodeConfigPane.show(activity, onSaved = { refreshAuthStatus(activity) })
      return
    }
    startBrowserAuth(activity, runner)
  }

  // ---- Browser auth -----------------------------------------------------

  private fun startBrowserAuth(activity: Activity, runner: String) {
    inFlightRunner = runner
    setRow(runner, "starting sign-in…", Tone.Warning)
    val prefs = activity.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
    val base = prefs.getString(YaverNativePrefs.AGENT_BASE_URL, "") ?: ""
    val auth = bestAuthToken(prefs)
    if (base.isEmpty()) {
      finishAuth(runner, false, "no agent URL"); return
    }
    val body = JSONObject().apply { put("runner", runner) }.toString()
    net.execute {
      val resp = httpPost("$base/runner-auth/browser/start", auth, body)
      main.post {
        if (!resp.ok) {
          finishAuth(runner, false, "start failed (HTTP ${resp.code}) ${resp.bodyTrim(180)}"); return@post
        }
        val obj = try { JSONObject(resp.body) } catch (e: Exception) { null }
        val sess = obj?.optJSONObject("session")
        val sid = sess?.optString("id", "") ?: ""
        val openUrl = sess?.optString("openUrl", "") ?: ""
        val userCode = sess?.optString("code", "") ?: ""
        if (sid.isEmpty() || openUrl.isEmpty()) {
          finishAuth(runner, false, "no session URL: ${resp.bodyTrim(180)}"); return@post
        }
        setRow(runner, "complete sign-in in browser…", Tone.Warning)
        YaverRunnerAuthFlowPane.show(
            activity = activity,
            runner = runner,
            sessionId = sid,
            openUrl = openUrl,
            userCode = userCode,
            agentBase = base,
            authToken = auth,
            onTerminal = { ok, msg ->
              finishAuth(runner, ok, msg)
              if (ok) refreshAuthStatus(activity)
            }
        )
      }
    }
  }

  private fun finishAuth(runner: String, ok: Boolean, msg: String) {
    inFlightRunner = null
    setRow(runner, msg, if (ok) Tone.Ok else Tone.Error)
  }

  // ---- Networking primitives --------------------------------------------

  private data class Resp(val code: Int, val ok: Boolean, val body: String) {
    fun bodyTrim(n: Int): String = body.trim().take(n)
  }

  private fun httpGet(urlStr: String, auth: String): Resp {
    return try {
      val url = URL(urlStr)
      val conn = url.openConnection() as HttpURLConnection
      conn.connectTimeout = 8000; conn.readTimeout = 15_000
      conn.setRequestProperty("Authorization", "Bearer $auth")
      val code = conn.responseCode
      val stream = if (code in 200..299) conn.inputStream else conn.errorStream
      val body = stream?.bufferedReader()?.use { it.readText() } ?: ""
      Resp(code, code in 200..299, body)
    } catch (e: Exception) { Resp(0, false, e.message ?: "network error") }
  }

  private fun httpPost(urlStr: String, auth: String, jsonBody: String): Resp {
    return try {
      val url = URL(urlStr)
      val conn = url.openConnection() as HttpURLConnection
      conn.requestMethod = "POST"
      conn.connectTimeout = 8000; conn.readTimeout = 30_000
      conn.doOutput = true
      conn.setRequestProperty("Authorization", "Bearer $auth")
      conn.setRequestProperty("Content-Type", "application/json")
      conn.outputStream.use { it.write(jsonBody.toByteArray()) }
      val code = conn.responseCode
      val stream = if (code in 200..299) conn.inputStream else conn.errorStream
      val body = stream?.bufferedReader()?.use { it.readText() } ?: ""
      Resp(code, code in 200..299, body)
    } catch (e: Exception) { Resp(0, false, e.message ?: "network error") }
  }

  internal fun dp(ctx: Context, value: Float): Float =
      TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, value, ctx.resources.displayMetrics)
}

// MARK: - Runner browser-auth flow pane

object YaverRunnerAuthFlowPane {

  private val main = Handler(Looper.getMainLooper())
  private val net = Executors.newCachedThreadPool()
  private val sched: ScheduledExecutorService = Exec2.newSingleThreadScheduledExecutor()
  private var current: View? = null
  private var pollHandle: java.util.concurrent.ScheduledFuture<*>? = null
  private val didSettle = AtomicBoolean(false)

  fun show(activity: Activity,
           runner: String,
           sessionId: String,
           openUrl: String,
           userCode: String,
           agentBase: String,
           authToken: String,
           onTerminal: (Boolean, String) -> Unit) {
    main.post {
      didSettle.set(false)
      presentInternal(activity, runner, sessionId, openUrl, userCode, agentBase, authToken, onTerminal)
    }
  }

  fun dismiss() {
    main.post {
      pollHandle?.cancel(false); pollHandle = null
      current?.let { v ->
        v.animate().translationY(YaverAgentsPane.dp(v.context, 600f)).setDuration(220).withEndAction {
          (v.parent as? ViewGroup)?.removeView(v)
        }.start()
      }
      current = null
    }
  }

  private fun presentInternal(activity: Activity,
                              runner: String,
                              sessionId: String,
                              openUrl: String,
                              userCode: String,
                              agentBase: String,
                              authToken: String,
                              onTerminal: (Boolean, String) -> Unit) {
    val ctx = activity
    val root = activity.window.decorView as? ViewGroup ?: return

    val needsPasteBack = runner.equals("claude", true) || runner.equals("claude-code", true)
    val runnerLabel = when (runner.lowercase()) {
      "claude", "claude-code" -> "Claude Code"
      "codex" -> "Codex"
      else -> runner
    }

    val card = LinearLayout(ctx).apply {
      orientation = LinearLayout.VERTICAL
      background = GradientDrawable().apply {
        setColor(Color.argb((0.95 * 255).toInt(), 14, 12, 28))
        cornerRadii = floatArrayOf(
            YaverAgentsPane.dp(ctx, 22f), YaverAgentsPane.dp(ctx, 22f),
            YaverAgentsPane.dp(ctx, 22f), YaverAgentsPane.dp(ctx, 22f),
            0f, 0f, 0f, 0f
        )
        setStroke(YaverAgentsPane.dp(ctx, 1f).toInt(), Color.argb(40, 255, 255, 255))
      }
      setPadding(YaverAgentsPane.dp(ctx, 18f).toInt(), YaverAgentsPane.dp(ctx, 16f).toInt(),
                 YaverAgentsPane.dp(ctx, 18f).toInt(), YaverAgentsPane.dp(ctx, 26f).toInt())
      elevation = YaverAgentsPane.dp(ctx, 16f)
    }

    val titleRow = LinearLayout(ctx).apply { orientation = LinearLayout.HORIZONTAL; gravity = Gravity.CENTER_VERTICAL }
    val title = TextView(ctx).apply {
      text = "Sign in to $runnerLabel"; setTextColor(Color.WHITE); textSize = 17f
      typeface = android.graphics.Typeface.create(android.graphics.Typeface.DEFAULT, android.graphics.Typeface.BOLD)
      layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f)
    }
    val close = Button(ctx).apply {
      text = "×"; setTextColor(Color.argb(160, 255, 255, 255)); textSize = 22f
      background = null
      setOnClickListener {
        if (didSettle.compareAndSet(false, true)) {
          // Best-effort cancel on agent.
          if (agentBase.isNotEmpty()) {
            net.execute {
              try {
                val u = URL("$agentBase/runner-auth/browser/cancel?id=$sessionId")
                val c = u.openConnection() as HttpURLConnection
                c.requestMethod = "POST"
                c.setRequestProperty("Authorization", "Bearer $authToken")
                c.responseCode
              } catch (_: Exception) {}
            }
          }
          onTerminal(false, "sign-in cancelled")
          dismiss()
        } else dismiss()
      }
    }
    titleRow.addView(title); titleRow.addView(close)

    val sub = TextView(ctx).apply {
      text = if (needsPasteBack)
        "Authorize on platform.claude.com, then paste the code below."
      else
        "Authorize on the device-auth page; this dialog turns green automatically."
      setTextColor(Color.argb(150, 255, 255, 255)); textSize = 12f
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
      lp.bottomMargin = YaverAgentsPane.dp(ctx, 14f).toInt(); layoutParams = lp
    }

    val openBtn = Button(ctx).apply {
      text = "↗ Open authorize page"
      setTextColor(Color.WHITE); textSize = 15f; isAllCaps = false
      background = GradientDrawable().apply {
        cornerRadius = YaverAgentsPane.dp(ctx, 12f); setColor(Color.rgb(117, 130, 245))
      }
      val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,
                                         YaverAgentsPane.dp(ctx, 48f).toInt())
      lp.bottomMargin = YaverAgentsPane.dp(ctx, 12f).toInt(); layoutParams = lp
      setOnClickListener { openCustomTab(activity, openUrl) }
    }

    // Codex code card (visible only when user-code returned + not paste-back).
    val codeCard: View? = if (!needsPasteBack && userCode.isNotEmpty()) {
      LinearLayout(ctx).apply {
        orientation = LinearLayout.VERTICAL
        background = GradientDrawable().apply {
          cornerRadius = YaverAgentsPane.dp(ctx, 12f); setColor(Color.argb(15, 255, 255, 255))
        }
        setPadding(YaverAgentsPane.dp(ctx, 14f).toInt(), YaverAgentsPane.dp(ctx, 10f).toInt(),
                   YaverAgentsPane.dp(ctx, 14f).toInt(), YaverAgentsPane.dp(ctx, 10f).toInt())
        addView(TextView(ctx).apply {
          text = "Enter this code"
          setTextColor(Color.argb(140, 255, 255, 255)); textSize = 11f
        })
        addView(TextView(ctx).apply {
          text = userCode
          setTextColor(Color.WHITE); textSize = 22f
          typeface = android.graphics.Typeface.MONOSPACE
        })
        val lp = LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
        lp.bottomMargin = YaverAgentsPane.dp(ctx, 12f).toInt(); layoutParams = lp
      }
    } else null

    // Paste-back row (Claude only).
    val pasteRow: View? = if (needsPasteBack) {
      val r = LinearLayout(ctx).apply {
        orientation = LinearLayout.HORIZONTAL
        val lp = LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT,
            YaverAgentsPane.dp(ctx, 44f).toInt())
        lp.bottomMargin = YaverAgentsPane.dp(ctx, 12f).toInt(); layoutParams = lp
      }
      val field = EditText(ctx).apply {
        hint = "Paste code from claude.com"
        setHintTextColor(Color.argb(90, 255, 255, 255))
        setTextColor(Color.WHITE); textSize = 14f
        inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
        background = GradientDrawable().apply {
          cornerRadius = YaverAgentsPane.dp(ctx, 10f); setColor(Color.argb(20, 255, 255, 255))
        }
        setPadding(YaverAgentsPane.dp(ctx, 12f).toInt(), 0,
                   YaverAgentsPane.dp(ctx, 12f).toInt(), 0)
        layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.MATCH_PARENT, 1f)
            .apply { rightMargin = YaverAgentsPane.dp(ctx, 8f).toInt() }
      }
      val submit = Button(ctx).apply {
        text = "Submit"; setTextColor(Color.WHITE); textSize = 14f; isAllCaps = false
        background = GradientDrawable().apply {
          cornerRadius = YaverAgentsPane.dp(ctx, 10f); setColor(Color.rgb(117, 130, 245))
        }
        layoutParams = LinearLayout.LayoutParams(YaverAgentsPane.dp(ctx, 92f).toInt(),
                                                 LinearLayout.LayoutParams.MATCH_PARENT)
      }
      r.addView(field); r.addView(submit)
      submit.setOnClickListener {
        val code = field.text?.toString()?.trim().orEmpty()
        if (code.isEmpty()) return@setOnClickListener
        submit.isEnabled = false
        net.execute {
          val body = JSONObject().apply { put("id", sessionId); put("code", code) }.toString()
          try {
            val u = URL("$agentBase/runner-auth/browser/submit-code")
            val c = u.openConnection() as HttpURLConnection
            c.requestMethod = "POST"; c.connectTimeout = 8000; c.readTimeout = 20_000
            c.doOutput = true
            c.setRequestProperty("Authorization", "Bearer $authToken")
            c.setRequestProperty("Content-Type", "application/json")
            c.outputStream.use { it.write(body.toByteArray()) }
            val code2 = c.responseCode
            // Polling loop will see status=completed shortly; leave UI alone.
            if (code2 !in 200..299) {
              main.post { submit.isEnabled = true }
            }
          } catch (e: Exception) {
            main.post { submit.isEnabled = true }
          }
        }
      }
      r
    } else null

    val status = TextView(ctx).apply {
      text = "waiting for sign-in…"
      setTextColor(Color.argb(170, 255, 255, 255)); textSize = 12f
      gravity = Gravity.CENTER
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
      lp.topMargin = YaverAgentsPane.dp(ctx, 8f).toInt(); layoutParams = lp
    }

    card.addView(titleRow); card.addView(sub); card.addView(openBtn)
    codeCard?.let { card.addView(it) }
    pasteRow?.let { card.addView(it) }
    card.addView(status)

    val params = android.widget.FrameLayout.LayoutParams(
        android.widget.FrameLayout.LayoutParams.MATCH_PARENT,
        android.widget.FrameLayout.LayoutParams.WRAP_CONTENT
    ).apply { gravity = Gravity.BOTTOM }
    card.translationY = YaverAgentsPane.dp(ctx, 600f)
    root.addView(card, params)
    current = card
    card.animate().translationY(0f).setDuration(320).start()

    // Auto-open the authorize page so the user doesn't need an extra tap.
    main.postDelayed({ openCustomTab(activity, openUrl) }, 350)

    // Polling.
    pollHandle?.cancel(false)
    pollHandle = sched.scheduleWithFixedDelay({
      if (didSettle.get()) return@scheduleWithFixedDelay
      val resp = try {
        val u = URL("$agentBase/runner-auth/browser/status?id=$sessionId")
        val c = u.openConnection() as HttpURLConnection
        c.connectTimeout = 5000; c.readTimeout = 8000
        c.setRequestProperty("Authorization", "Bearer $authToken")
        val code = c.responseCode
        val s = if (code in 200..299) c.inputStream else c.errorStream
        s?.bufferedReader()?.use { it.readText() } ?: ""
      } catch (_: Exception) { return@scheduleWithFixedDelay }
      val obj = try { JSONObject(resp) } catch (_: Exception) { return@scheduleWithFixedDelay }
      val sess = obj.optJSONObject("session") ?: JSONObject()
      val sStatus = sess.optString("status", "") ?: ""
      if (sStatus == "completed") {
        if (didSettle.compareAndSet(false, true)) {
          main.post {
            status.text = "✓ signed in"
            status.setTextColor(Color.rgb(86, 217, 140))
            onTerminal(true, "✓ signed in")
            main.postDelayed({ dismiss() }, 600)
          }
        }
      } else if (sStatus == "failed" || sStatus == "cancelled") {
        if (didSettle.compareAndSet(false, true)) {
          val detail = sess.optString("error", "") ?: sStatus
          main.post {
            status.text = "$sStatus: $detail"
            status.setTextColor(Color.rgb(255, 116, 116))
            onTerminal(false, "sign-in $sStatus: $detail")
            main.postDelayed({ dismiss() }, 1200)
          }
        }
      }
    }, 1500, 1500, TimeUnit.MILLISECONDS)
  }

  private fun openCustomTab(activity: Activity, url: String) {
    try {
      val intent = CustomTabsIntent.Builder()
          .setShowTitle(false)
          .build()
      intent.launchUrl(activity, Uri.parse(url))
    } catch (e: Exception) {
      // Fall back to a normal browser intent.
      try {
        val i = android.content.Intent(android.content.Intent.ACTION_VIEW, Uri.parse(url))
        activity.startActivity(i)
      } catch (_: Exception) {}
    }
  }
}

// MARK: - OpenCode config sub-pane

object YaverOpenCodeConfigPane {

  private val main = Handler(Looper.getMainLooper())
  private val net = Executors.newCachedThreadPool()
  private var current: View? = null

  fun show(activity: Activity, onSaved: () -> Unit) {
    main.post { presentInternal(activity, onSaved) }
  }

  private fun presentInternal(activity: Activity, onSaved: () -> Unit) {
    val ctx = activity
    val root = activity.window.decorView as? ViewGroup ?: return

    val card = LinearLayout(ctx).apply {
      orientation = LinearLayout.VERTICAL
      background = GradientDrawable().apply {
        setColor(Color.argb((0.95 * 255).toInt(), 14, 12, 28))
        cornerRadii = floatArrayOf(
            YaverAgentsPane.dp(ctx, 22f), YaverAgentsPane.dp(ctx, 22f),
            YaverAgentsPane.dp(ctx, 22f), YaverAgentsPane.dp(ctx, 22f),
            0f, 0f, 0f, 0f
        )
      }
      setPadding(YaverAgentsPane.dp(ctx, 18f).toInt(), YaverAgentsPane.dp(ctx, 16f).toInt(),
                 YaverAgentsPane.dp(ctx, 18f).toInt(), YaverAgentsPane.dp(ctx, 26f).toInt())
      elevation = YaverAgentsPane.dp(ctx, 16f)
    }

    val titleRow = LinearLayout(ctx).apply { orientation = LinearLayout.HORIZONTAL; gravity = Gravity.CENTER_VERTICAL }
    val title = TextView(ctx).apply {
      text = "OpenCode"; setTextColor(Color.WHITE); textSize = 17f
      typeface = android.graphics.Typeface.create(android.graphics.Typeface.DEFAULT, android.graphics.Typeface.BOLD)
      layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f)
    }
    val close = Button(ctx).apply {
      text = "×"; setTextColor(Color.argb(160, 255, 255, 255)); textSize = 22f
      background = null; setOnClickListener { dismiss() }
    }
    titleRow.addView(title); titleRow.addView(close)

    val modeLabel = TextView(ctx).apply {
      text = "Mode"; setTextColor(Color.argb(180, 255, 255, 255)); textSize = 12f
      val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,
                                         LinearLayout.LayoutParams.WRAP_CONTENT)
      lp.topMargin = YaverAgentsPane.dp(ctx, 18f).toInt(); layoutParams = lp
    }
    val modeRow = LinearLayout(ctx).apply {
      orientation = LinearLayout.HORIZONTAL
      val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,
                                         LinearLayout.LayoutParams.WRAP_CONTENT)
      lp.topMargin = YaverAgentsPane.dp(ctx, 6f).toInt(); layoutParams = lp
    }
    var selectedMode = "build"
    val buildBtn = Button(ctx).apply {
      text = "Build"; setTextColor(Color.WHITE); textSize = 14f; isAllCaps = false
      background = GradientDrawable().apply {
        cornerRadius = YaverAgentsPane.dp(ctx, 10f); setColor(Color.rgb(117, 130, 245))
      }
      layoutParams = LinearLayout.LayoutParams(0, YaverAgentsPane.dp(ctx, 40f).toInt(), 1f)
          .apply { rightMargin = YaverAgentsPane.dp(ctx, 6f).toInt() }
    }
    val planBtn = Button(ctx).apply {
      text = "Plan"; setTextColor(Color.WHITE); textSize = 14f; isAllCaps = false
      background = GradientDrawable().apply {
        cornerRadius = YaverAgentsPane.dp(ctx, 10f); setColor(Color.argb(40, 255, 255, 255))
      }
      layoutParams = LinearLayout.LayoutParams(0, YaverAgentsPane.dp(ctx, 40f).toInt(), 1f)
    }
    fun applyMode(mode: String) {
      selectedMode = mode
      buildBtn.background = GradientDrawable().apply {
        cornerRadius = YaverAgentsPane.dp(ctx, 10f)
        setColor(if (mode == "build") Color.rgb(117, 130, 245) else Color.argb(40, 255, 255, 255))
      }
      planBtn.background = GradientDrawable().apply {
        cornerRadius = YaverAgentsPane.dp(ctx, 10f)
        setColor(if (mode == "plan") Color.rgb(117, 130, 245) else Color.argb(40, 255, 255, 255))
      }
    }
    buildBtn.setOnClickListener { applyMode("build") }
    planBtn.setOnClickListener { applyMode("plan") }
    modeRow.addView(buildBtn); modeRow.addView(planBtn)

    fun makeKeyField(placeholder: String): EditText = EditText(ctx).apply {
      hint = placeholder
      setHintTextColor(Color.argb(90, 255, 255, 255))
      setTextColor(Color.WHITE); textSize = 14f
      inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD or
          InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
      background = GradientDrawable().apply {
        cornerRadius = YaverAgentsPane.dp(ctx, 10f); setColor(Color.argb(20, 255, 255, 255))
      }
      setPadding(YaverAgentsPane.dp(ctx, 12f).toInt(), 0,
                 YaverAgentsPane.dp(ctx, 12f).toInt(), 0)
      val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,
                                         YaverAgentsPane.dp(ctx, 42f).toInt())
      lp.topMargin = YaverAgentsPane.dp(ctx, 8f).toInt(); layoutParams = lp
    }
    val glm = makeKeyField("GLM API key (optional)")
    val openai = makeKeyField("OpenAI API key (optional)")
    val anthropic = makeKeyField("Anthropic API key (optional)")

    val save = Button(ctx).apply {
      text = "Save"; setTextColor(Color.WHITE); textSize = 15f; isAllCaps = false
      background = GradientDrawable().apply {
        cornerRadius = YaverAgentsPane.dp(ctx, 12f); setColor(Color.rgb(117, 130, 245))
      }
      val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,
                                         YaverAgentsPane.dp(ctx, 48f).toInt())
      lp.topMargin = YaverAgentsPane.dp(ctx, 16f).toInt(); layoutParams = lp
    }
    val status = TextView(ctx).apply {
      text = " "; setTextColor(Color.argb(180, 255, 255, 255)); textSize = 12f; gravity = Gravity.CENTER
      val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,
                                         LinearLayout.LayoutParams.WRAP_CONTENT)
      lp.topMargin = YaverAgentsPane.dp(ctx, 10f).toInt(); layoutParams = lp
    }

    card.addView(titleRow); card.addView(modeLabel); card.addView(modeRow)
    card.addView(glm); card.addView(openai); card.addView(anthropic)
    card.addView(save); card.addView(status)

    val params = android.widget.FrameLayout.LayoutParams(
        android.widget.FrameLayout.LayoutParams.MATCH_PARENT,
        android.widget.FrameLayout.LayoutParams.WRAP_CONTENT
    ).apply { gravity = Gravity.BOTTOM }
    card.translationY = YaverAgentsPane.dp(ctx, 600f)
    root.addView(card, params)
    current = card
    card.animate().translationY(0f).setDuration(320).start()

    save.setOnClickListener {
      val prefs = activity.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
      val base = prefs.getString(YaverNativePrefs.AGENT_BASE_URL, "") ?: ""
      val auth = (prefs.getString(YaverNativePrefs.INHERITED_AUTH_TOKEN, "") ?: "")
          .ifEmpty { prefs.getString(YaverNativePrefs.AGENT_AUTH, "") ?: "" }
      if (base.isEmpty()) {
        status.text = "no agent URL"; status.setTextColor(Color.rgb(255, 116, 116)); return@setOnClickListener
      }
      val payload = JSONObject().apply {
        put("runner", "opencode")
        put("notes", "mode=$selectedMode")
        glm.text?.toString()?.trim()?.takeIf { it.isNotEmpty() }?.let { put("glm_api_key", it) }
        openai.text?.toString()?.trim()?.takeIf { it.isNotEmpty() }?.let { put("openai_api_key", it) }
        anthropic.text?.toString()?.trim()?.takeIf { it.isNotEmpty() }?.let { put("anthropic_api_key", it) }
      }.toString()
      status.text = "saving…"; status.setTextColor(Color.argb(180, 255, 255, 255))
      net.execute {
        try {
          val u = URL("$base/runner-auth/set")
          val c = u.openConnection() as HttpURLConnection
          c.requestMethod = "POST"; c.connectTimeout = 8000; c.readTimeout = 20_000
          c.doOutput = true
          c.setRequestProperty("Authorization", "Bearer $auth")
          c.setRequestProperty("Content-Type", "application/json")
          c.outputStream.use { it.write(payload.toByteArray()) }
          val code = c.responseCode
          val ok = code in 200..299
          val body = (if (ok) c.inputStream else c.errorStream)
              ?.bufferedReader()?.use { it.readText() } ?: ""
          main.post {
            if (ok) {
              status.text = "Saved ✓"; status.setTextColor(Color.rgb(86, 217, 140))
              onSaved()
              main.postDelayed({ dismiss() }, 700)
            } else {
              status.text = "save failed — HTTP $code: ${body.take(180)}"
              status.setTextColor(Color.rgb(255, 116, 116))
            }
          }
        } catch (e: Exception) {
          main.post {
            status.text = "save failed: ${e.message ?: "network error"}"
            status.setTextColor(Color.rgb(255, 116, 116))
          }
        }
      }
    }
  }

  fun dismiss() {
    main.post {
      current?.let { v ->
        v.animate().translationY(YaverAgentsPane.dp(v.context, 600f)).setDuration(220).withEndAction {
          (v.parent as? ViewGroup)?.removeView(v)
        }.start()
      }
      current = null
    }
  }
}
