package io.yaver.mobile

// Yaver's native deploy pane on Android — fifth shake-overlay action
// ("Deploy"). Mirrors YaverDeployPane.swift in flow:
//
//   1. GET /fleet/deploy-options?app=<slug> on background thread
//   2. Show target segment (TestFlight / Play / Both) + a list of every
//      reachable machine, greyed-out for any that can't run all picked
//      targets (Linux box can't TestFlight, etc.)
//   3. Tap a row → POST /deploy/ship {app, target/targets, machine}
//   4. Toast + auto-dismiss
//
// Visual language matches the existing Feedback / Agents panes — purple
// accent, bottom-anchored card, drag handle, close X. Layout is built
// programmatically (no XML) so the pane is portable between flavours
// and survives expo prebuild --clean without any res/ overlays.

import android.app.Activity
import android.content.Context
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.os.Handler
import android.os.Looper
import android.text.TextUtils
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import org.json.JSONArray
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import kotlin.concurrent.thread

object YaverDeployPane {

  private val main = Handler(Looper.getMainLooper())
  private var current: View? = null
  private var statusLabel: TextView? = null
  private var machineList: LinearLayout? = null
  private var subtitle: TextView? = null
  private var selectedTargetIndex: Int = 2  // 0=testflight, 1=playstore, 2=both
  private var options: Options? = null
  private var lastActivity: Activity? = null

  // Target labels — keep in sync with Yaver's iOS pane + agent contract.
  private val targetIds = listOf("testflight", "playstore")
  private val targetLabels = listOf("TestFlight", "Play Store", "Both")

  // MARK: - Models

  private data class TargetCap(val target: String, val ok: Boolean, val reason: String)
  private data class Device(
      val deviceId: String,
      val name: String,
      val alias: String,
      val platform: String,
      val isLocal: Boolean,
      val isOnline: Boolean,
      val probed: Boolean,
      val probeError: String,
      val capabilities: List<TargetCap>
  )
  private data class Options(
      val app: String,
      val devices: List<Device>,
      val warnings: List<String>
  )

  // MARK: - Presentation

  fun show(activity: Activity) {
    main.post { presentInternal(activity) }
  }

  fun dismiss() {
    main.post { dismissInternal() }
  }

  private fun dismissInternal() {
    current?.let { v ->
      v.animate().alpha(0f).translationY(dp(v.context, 600f))
        .setDuration(200).withEndAction {
          (v.parent as? ViewGroup)?.removeView(v)
        }.start()
    }
    current = null
    options = null
    statusLabel = null
    machineList = null
    subtitle = null
  }

  private fun presentInternal(activity: Activity) {
    if (current != null) return
    lastActivity = activity
    val ctx: Context = activity
    val root = activity.window.decorView as? ViewGroup ?: return

    // Card container — bottom sheet with rounded top corners.
    val card = LinearLayout(ctx).apply {
      orientation = LinearLayout.VERTICAL
      val bg = GradientDrawable().apply {
        cornerRadii = floatArrayOf(
            dp(ctx, 22f), dp(ctx, 22f),
            dp(ctx, 22f), dp(ctx, 22f),
            0f, 0f, 0f, 0f
        )
        setColor(Color.argb((0.96 * 255).toInt(), 14, 12, 28))
        setStroke(dp(ctx, 1f).toInt(), Color.argb(40, 255, 255, 255))
      }
      background = bg
      setPadding(dp(ctx, 18f).toInt(), dp(ctx, 14f).toInt(),
                 dp(ctx, 18f).toInt(), dp(ctx, 28f).toInt())
      elevation = dp(ctx, 12f)
    }

    // Drag handle
    val handle = View(ctx).apply {
      val bg = GradientDrawable().apply {
        cornerRadius = dp(ctx, 2.5f)
        setColor(Color.argb(50, 255, 255, 255))
      }
      background = bg
      layoutParams = LinearLayout.LayoutParams(dp(ctx, 38f).toInt(), dp(ctx, 5f).toInt())
        .apply { gravity = Gravity.CENTER_HORIZONTAL; bottomMargin = dp(ctx, 12f).toInt() }
    }
    card.addView(handle)

    // Title row (title + close X)
    val titleRow = FrameLayout(ctx).apply {
      layoutParams = LinearLayout.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          ViewGroup.LayoutParams.WRAP_CONTENT
      )
    }
    val title = TextView(ctx).apply {
      text = "Deploy"
      setTextColor(Color.WHITE)
      textSize = 17f
      typeface = android.graphics.Typeface.DEFAULT_BOLD
      layoutParams = FrameLayout.LayoutParams(
          ViewGroup.LayoutParams.WRAP_CONTENT,
          ViewGroup.LayoutParams.WRAP_CONTENT,
          Gravity.START
      )
    }
    val close = TextView(ctx).apply {
      text = "✕"
      setTextColor(Color.argb(155, 255, 255, 255))
      textSize = 18f
      gravity = Gravity.CENTER
      layoutParams = FrameLayout.LayoutParams(
          dp(ctx, 32f).toInt(), dp(ctx, 32f).toInt(),
          Gravity.END
      )
      setOnClickListener { dismiss() }
    }
    titleRow.addView(title)
    titleRow.addView(close)
    card.addView(titleRow)

    val sub = TextView(ctx).apply {
      text = "loading machines…"
      setTextColor(Color.argb(140, 255, 255, 255))
      textSize = 12f
      val lp = LinearLayout.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          ViewGroup.LayoutParams.WRAP_CONTENT
      ).apply { topMargin = dp(ctx, 2f).toInt() }
      layoutParams = lp
    }
    subtitle = sub
    card.addView(sub)

    // Target segment — three buttons with mutually exclusive selection.
    val segment = LinearLayout(ctx).apply {
      orientation = LinearLayout.HORIZONTAL
      val lp = LinearLayout.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          dp(ctx, 36f).toInt()
      ).apply { topMargin = dp(ctx, 14f).toInt() }
      layoutParams = lp
      val bg = GradientDrawable().apply {
        cornerRadius = dp(ctx, 10f)
        setColor(Color.argb(20, 255, 255, 255))
      }
      background = bg
    }
    targetLabels.forEachIndexed { i, label ->
      val btn = Button(ctx).apply {
        text = label
        textSize = 13f
        isAllCaps = false
        setTextColor(if (i == selectedTargetIndex) Color.WHITE else Color.argb(160, 255, 255, 255))
        background = GradientDrawable().apply {
          cornerRadius = dp(ctx, 8f)
          setColor(if (i == selectedTargetIndex)
              Color.argb(170, 127, 140, 247)
            else Color.TRANSPARENT)
        }
        layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.MATCH_PARENT, 1f)
          .apply {
            val m = dp(ctx, 3f).toInt()
            setMargins(m, m, m, m)
          }
        setPadding(0, 0, 0, 0)
        setOnClickListener {
          selectedTargetIndex = i
          renderSegment(segment)
          renderMachines()
        }
      }
      segment.addView(btn)
    }
    card.addView(segment)

    // Machine list (scrollable)
    val scroll = ScrollView(ctx).apply {
      layoutParams = LinearLayout.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          0,
          1f
      ).apply { topMargin = dp(ctx, 14f).toInt() }
      isFillViewport = true
    }
    val list = LinearLayout(ctx).apply {
      orientation = LinearLayout.VERTICAL
      layoutParams = ViewGroup.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          ViewGroup.LayoutParams.WRAP_CONTENT
      )
    }
    machineList = list
    scroll.addView(list)
    card.addView(scroll)

    // Status line at bottom (fetch errors / deploy-started toast)
    val status = TextView(ctx).apply {
      text = ""
      setTextColor(Color.argb(140, 255, 255, 255))
      textSize = 12f
      gravity = Gravity.CENTER
      layoutParams = LinearLayout.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          ViewGroup.LayoutParams.WRAP_CONTENT
      ).apply { topMargin = dp(ctx, 8f).toInt() }
    }
    statusLabel = status
    card.addView(status)

    // Add to root, anchored to bottom, animated up.
    val params = FrameLayout.LayoutParams(
        FrameLayout.LayoutParams.MATCH_PARENT,
        FrameLayout.LayoutParams.WRAP_CONTENT
    ).apply { gravity = Gravity.BOTTOM }
    card.alpha = 0f
    card.translationY = dp(ctx, 600f)
    root.addView(card, params)
    current = card
    card.animate().alpha(1f).translationY(0f).setDuration(320).start()

    fetchOptions(ctx)
  }

  private fun renderSegment(segment: LinearLayout) {
    val ctx = segment.context
    for (i in 0 until segment.childCount) {
      val btn = segment.getChildAt(i) as? Button ?: continue
      val selected = i == selectedTargetIndex
      btn.setTextColor(if (selected) Color.WHITE else Color.argb(160, 255, 255, 255))
      btn.background = GradientDrawable().apply {
        cornerRadius = dp(ctx, 8f)
        setColor(if (selected) Color.argb(170, 127, 140, 247) else Color.TRANSPARENT)
      }
    }
  }

  // MARK: - /fleet/deploy-options fetch

  private fun currentAppSlug(prefs: android.content.SharedPreferences): String {
    val inh = (prefs.getString(YaverNativePrefs.GUEST_PROJECT_NAME, "") ?: "").trim()
    if (inh.isNotEmpty()) return inh
    return "main"
  }

  private fun fetchOptions(ctx: Context) {
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
    val base = (prefs.getString(YaverNativePrefs.AGENT_BASE_URL, "") ?: "").trim()
    val auth = bestAuthToken(prefs)
    val relayPassword = (prefs.getString(YaverNativePrefs.RELAY_PASSWORD, "") ?: "").trim()
    val app = currentAppSlug(prefs)
    if (base.isEmpty() || auth.isEmpty()) {
      showError("not signed in to a Yaver agent")
      return
    }
    val url = base.trimEnd('/') + "/fleet/deploy-options?app=" +
        URLEncoder.encode(app, "utf-8")
    thread(start = true, isDaemon = true) {
      val resp = get(url, auth, relayPassword)
      main.post {
        if (!resp.ok) {
          showError("fetch failed (status ${resp.code})")
          return@post
        }
        try {
          options = parseOptions(resp.body)
          renderMachines()
        } catch (e: Exception) {
          showError("decode failed: ${e.message}")
        }
      }
    }
  }

  private fun parseOptions(raw: String): Options {
    val obj = JSONObject(raw)
    val app = obj.optString("app", "")
    val devicesArr = obj.optJSONArray("devices") ?: JSONArray()
    val devices = mutableListOf<Device>()
    for (i in 0 until devicesArr.length()) {
      val d = devicesArr.getJSONObject(i)
      val capsArr = d.optJSONArray("capabilities") ?: JSONArray()
      val caps = mutableListOf<TargetCap>()
      for (j in 0 until capsArr.length()) {
        val c = capsArr.getJSONObject(j)
        caps.add(TargetCap(
            target = c.optString("target", ""),
            ok = c.optBoolean("ok", false),
            reason = c.optString("reason", "")
        ))
      }
      devices.add(Device(
          deviceId = d.optString("deviceId", ""),
          name = d.optString("name", ""),
          alias = d.optString("alias", ""),
          platform = d.optString("platform", ""),
          isLocal = d.optBoolean("isLocal", false),
          isOnline = d.optBoolean("isOnline", false),
          probed = d.optBoolean("probed", false),
          probeError = d.optString("probeError", ""),
          capabilities = caps
      ))
    }
    val warnings = mutableListOf<String>()
    obj.optJSONArray("warnings")?.let {
      for (i in 0 until it.length()) warnings.add(it.optString(i, ""))
    }
    return Options(app, devices, warnings)
  }

  private fun renderMachines() {
    val ctx = lastActivity ?: return
    val list = machineList ?: return
    val opts = options ?: return
    list.removeAllViews()
    subtitle?.text = if (opts.devices.size == 1)
      "1 machine — pick a target, then tap to deploy"
    else
      "${opts.devices.size} machines — pick a target, then tap to deploy"

    val pickedTargets = currentlySelectedTargets()
    for (d in opts.devices) {
      list.addView(makeMachineRow(ctx, d, pickedTargets))
    }
    if (opts.warnings.isNotEmpty()) {
      statusLabel?.text = TextUtils.join(" · ", opts.warnings)
    }
  }

  private fun currentlySelectedTargets(): List<String> = when (selectedTargetIndex) {
    0 -> listOf("testflight")
    1 -> listOf("playstore")
    else -> targetIds
  }

  private fun makeMachineRow(ctx: Context, d: Device, pickedTargets: List<String>): View {
    // Same enable/disable rule as iOS: every picked target must be ok=true
    // for this device, otherwise we grey it out and show the blocker(s).
    val blockers = mutableListOf<String>()
    var allOK = true
    for (t in pickedTargets) {
      val cap = d.capabilities.firstOrNull { it.target == t }
      if (cap == null) {
        allOK = false
        blockers.add("${prettyTarget(t)}: no capability data")
      } else if (!cap.ok) {
        allOK = false
        if (cap.reason.isNotEmpty()) blockers.add("${prettyTarget(t)}: ${cap.reason}")
      }
    }
    if (!d.probed && allOK) {
      allOK = false
      blockers.add(if (d.probeError.isNotEmpty()) d.probeError else "couldn't reach this machine")
    }

    val row = LinearLayout(ctx).apply {
      orientation = LinearLayout.VERTICAL
      val bg = GradientDrawable().apply {
        cornerRadius = dp(ctx, 14f)
        setColor(if (allOK)
            Color.argb(15, 255, 255, 255)
          else
            Color.argb(8, 255, 255, 255))
      }
      background = bg
      setPadding(dp(ctx, 14f).toInt(), dp(ctx, 12f).toInt(),
                 dp(ctx, 14f).toInt(), dp(ctx, 12f).toInt())
      val lp = LinearLayout.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          ViewGroup.LayoutParams.WRAP_CONTENT
      ).apply { bottomMargin = dp(ctx, 8f).toInt() }
      layoutParams = lp
      alpha = if (allOK) 1.0f else 0.55f
      isClickable = allOK
      isEnabled = allOK
    }

    val name = TextView(ctx).apply {
      text = (if (d.alias.isNotEmpty()) d.alias else d.name) +
        (if (d.isLocal) " (this phone's primary)" else "")
      setTextColor(Color.WHITE)
      textSize = 15f
      typeface = android.graphics.Typeface.DEFAULT_BOLD
    }
    val meta = TextView(ctx).apply {
      textSize = 12f
      val tail = if (allOK) "ready" else TextUtils.join(" · ", blockers)
      text = "${d.platform} · $tail"
      setTextColor(if (allOK)
          Color.argb(140, 255, 255, 255)
        else
          Color.rgb(255, 178, 115))
    }
    row.addView(name)
    row.addView(meta)

    if (allOK) {
      row.setOnClickListener {
        val opts = options ?: return@setOnClickListener
        triggerDeploy(ctx, opts.app, d.deviceId)
      }
    }
    return row
  }

  // MARK: - Actions

  private fun triggerDeploy(ctx: Context, app: String, machine: String) {
    statusLabel?.text = "starting deploy on ${prettyMachineName(machine)}…"
    val targets = currentlySelectedTargets()
    val body = JSONObject().apply {
      put("app", app)
      put("machine", machine)
      if (targets.size == 1) {
        put("target", targets[0])
      } else {
        val arr = JSONArray()
        for (t in targets) arr.put(t)
        put("targets", arr)
      }
    }.toString()
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
    val base = (prefs.getString(YaverNativePrefs.AGENT_BASE_URL, "") ?: "").trim()
    val auth = bestAuthToken(prefs)
    val relayPassword = (prefs.getString(YaverNativePrefs.RELAY_PASSWORD, "") ?: "").trim()
    if (base.isEmpty() || auth.isEmpty()) {
      showError("not signed in to a Yaver agent")
      return
    }
    val url = base.trimEnd('/') + "/deploy/ship"
    thread(start = true, isDaemon = true) {
      val resp = post(url, auth, body, relayPassword)
      main.post {
        if (resp.ok) {
          statusLabel?.setTextColor(Color.rgb(34, 197, 94))
          statusLabel?.text = "deploy started — track progress in the desktop / web tab"
          main.postDelayed({ dismiss() }, 1600)
        } else {
          showError("ship failed (status ${resp.code})")
        }
      }
    }
  }

  // MARK: - Helpers

  private fun prettyTarget(t: String): String = when (t) {
    "testflight" -> "TestFlight"
    "playstore" -> "Play Store"
    else -> t
  }

  private fun prettyMachineName(deviceId: String): String {
    val opts = options ?: return deviceId
    val d = opts.devices.firstOrNull { it.deviceId == deviceId } ?: return deviceId
    return if (d.alias.isNotEmpty()) d.alias else d.name
  }

  private fun showError(msg: String) {
    statusLabel?.setTextColor(Color.rgb(255, 115, 115))
    statusLabel?.text = msg
  }

  private fun bestAuthToken(prefs: android.content.SharedPreferences): String {
    val inh = prefs.getString(YaverNativePrefs.INHERITED_AUTH_TOKEN, "") ?: ""
    if (inh.isNotEmpty()) return inh
    return prefs.getString(YaverNativePrefs.AGENT_AUTH, "") ?: ""
  }

  private data class Resp(val code: Int, val ok: Boolean, val body: String)

  private fun get(urlStr: String, auth: String, relayPassword: String): Resp {
    return try {
      val url = URL(urlStr)
      val conn = url.openConnection() as HttpURLConnection
      conn.requestMethod = "GET"
      conn.connectTimeout = 8000
      conn.readTimeout = 30_000
      conn.setRequestProperty("Authorization", "Bearer $auth")
      if (relayPassword.isNotEmpty()) {
        conn.setRequestProperty("X-Relay-Password", relayPassword)
      }
      val code = conn.responseCode
      val stream = if (code in 200..299) conn.inputStream else conn.errorStream
      val body = stream?.bufferedReader()?.use { it.readText() } ?: ""
      Resp(code, code in 200..299, body)
    } catch (e: Exception) {
      Resp(0, false, e.message ?: "network error")
    }
  }

  private fun post(urlStr: String, auth: String, jsonBody: String, relayPassword: String): Resp {
    return try {
      val url = URL(urlStr)
      val conn = url.openConnection() as HttpURLConnection
      conn.requestMethod = "POST"
      conn.connectTimeout = 8000
      conn.readTimeout = 30_000
      conn.doOutput = true
      conn.setRequestProperty("Authorization", "Bearer $auth")
      conn.setRequestProperty("Content-Type", "application/json")
      if (relayPassword.isNotEmpty()) {
        conn.setRequestProperty("X-Relay-Password", relayPassword)
      }
      conn.outputStream.use { it.write(jsonBody.toByteArray()) }
      val code = conn.responseCode
      val stream = if (code in 200..299) conn.inputStream else conn.errorStream
      val body = stream?.bufferedReader()?.use { it.readText() } ?: ""
      Resp(code, code in 200..299, body)
    } catch (e: Exception) {
      Resp(0, false, e.message ?: "network error")
    }
  }

  private fun dp(ctx: Context, value: Float): Float =
      TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, value, ctx.resources.displayMetrics)
}
