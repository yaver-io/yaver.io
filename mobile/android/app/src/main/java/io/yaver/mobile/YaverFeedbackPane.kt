package io.yaver.mobile

// YaverFeedbackPane (Android) — counterpart to iOS YaverFeedbackPane.swift.
// Bottom-sheet with multi-line chat input, screenshot toggle, Reload + Send.
// Calls the SAME agent endpoints as iOS (/tasks for vibing, /dev/reload).
//
// Reads agent base URL + bearer from SharedPreferences (populated by host
// JS via NativeModules.YaverInfo.setInheritedAuth + by YaverBundleLoader-
// equivalent). No JS bridge interaction; works for any guest bundle.

import android.app.Activity
import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.util.Base64
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.Executors

object YaverFeedbackPane {

  private val main = Handler(Looper.getMainLooper())
  private val net = Executors.newSingleThreadExecutor()
  private var current: View? = null
  private var snapshot: Bitmap? = null

  fun show(activity: Activity) {
    main.post {
      // Snapshot the activity's content BEFORE the pane is added so a
      // toggled-on screenshot includes the running guest UI.
      snapshot = captureSnapshot(activity)
      presentInternal(activity)
    }
  }

  private fun presentInternal(activity: Activity) {
    val ctx = activity
    val root = activity.window.decorView as? ViewGroup ?: return
    dismiss()

    val card = LinearLayout(ctx).apply {
      orientation = LinearLayout.VERTICAL
      background = GradientDrawable().apply {
        // Purple-black tint matching iOS YaverFeedbackPane card.
        setColor(Color.argb((0.95 * 255).toInt(), 14, 12, 28))
        cornerRadii = floatArrayOf(
            dp(ctx, 22f), dp(ctx, 22f),  // top-left
            dp(ctx, 22f), dp(ctx, 22f),  // top-right
            0f, 0f, 0f, 0f
        )
        setStroke(dp(ctx, 1f).toInt(), Color.argb(40, 255, 255, 255))
      }
      setPadding(dp(ctx, 18f).toInt(), dp(ctx, 16f).toInt(),
                 dp(ctx, 18f).toInt(), dp(ctx, 26f).toInt())
      elevation = dp(ctx, 16f)
    }

    // Title row
    val titleRow = LinearLayout(ctx).apply {
      orientation = LinearLayout.HORIZONTAL
      gravity = Gravity.CENTER_VERTICAL
    }
    val title = TextView(ctx).apply {
      text = "Feedback"
      setTextColor(Color.WHITE)
      textSize = 17f
      typeface = android.graphics.Typeface.create(android.graphics.Typeface.DEFAULT, android.graphics.Typeface.BOLD)
      layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f)
    }
    val close = Button(ctx).apply {
      text = "×"
      setTextColor(Color.argb(160, 255, 255, 255))
      textSize = 22f
      background = null
      setOnClickListener { dismiss() }
    }
    titleRow.addView(title)
    titleRow.addView(close)

    val sub = TextView(ctx).apply {
      text = "send a message · reload · screenshot"
      setTextColor(Color.argb(140, 255, 255, 255))
      textSize = 12f
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT
      )
      lp.bottomMargin = dp(ctx, 14f).toInt()
      layoutParams = lp
    }

    val prompt = EditText(ctx).apply {
      hint = "What's broken? Or just describe what to vibe on…"
      setHintTextColor(Color.argb(90, 255, 255, 255))
      setTextColor(Color.WHITE)
      textSize = 16f
      gravity = Gravity.TOP or Gravity.START
      inputType = InputType.TYPE_CLASS_TEXT or
          InputType.TYPE_TEXT_FLAG_MULTI_LINE or
          InputType.TYPE_TEXT_FLAG_CAP_SENTENCES
      background = GradientDrawable().apply {
        cornerRadius = dp(ctx, 14f)
        setColor(Color.argb(20, 255, 255, 255))
      }
      setPadding(dp(ctx, 12f).toInt(), dp(ctx, 10f).toInt(),
                 dp(ctx, 12f).toInt(), dp(ctx, 10f).toInt())
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT,
          dp(ctx, 110f).toInt()
      )
      layoutParams = lp
    }

    val toggleRow = LinearLayout(ctx).apply {
      orientation = LinearLayout.HORIZONTAL
      gravity = Gravity.CENTER_VERTICAL
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT
      )
      lp.topMargin = dp(ctx, 14f).toInt()
      lp.bottomMargin = dp(ctx, 14f).toInt()
      layoutParams = lp
    }
    val cb = CheckBox(ctx).apply {
      text = "Include screenshot"
      setTextColor(Color.WHITE)
      isChecked = true
      buttonTintList = android.content.res.ColorStateList.valueOf(
          Color.rgb(127, 140, 247))
    }
    toggleRow.addView(cb)

    val actionRow = LinearLayout(ctx).apply {
      orientation = LinearLayout.HORIZONTAL
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT,
          dp(ctx, 48f).toInt()
      )
      layoutParams = lp
    }
    val accent = Color.rgb(117, 130, 245)
    val reload = Button(ctx).apply {
      text = "Reload"
      setTextColor(Color.WHITE)
      textSize = 15f
      isAllCaps = false
      background = GradientDrawable().apply {
        cornerRadius = dp(ctx, 12f)
        setColor(Color.argb(30, 255, 255, 255))
      }
      layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.MATCH_PARENT, 1f)
          .apply { rightMargin = dp(ctx, 8f).toInt() }
    }
    val send = Button(ctx).apply {
      text = "Send"
      setTextColor(Color.WHITE)
      textSize = 15f
      isAllCaps = false
      background = GradientDrawable().apply {
        cornerRadius = dp(ctx, 12f)
        setColor(accent)
      }
      layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.MATCH_PARENT, 1f)
    }
    actionRow.addView(reload)
    actionRow.addView(send)

    val status = TextView(ctx).apply {
      text = " "
      setTextColor(Color.argb(180, 255, 255, 255))
      textSize = 12f
      gravity = Gravity.CENTER
      val lp = LinearLayout.LayoutParams(
          LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT
      )
      lp.topMargin = dp(ctx, 12f).toInt()
      layoutParams = lp
    }

    card.addView(titleRow)
    card.addView(sub)
    card.addView(prompt)
    card.addView(toggleRow)
    card.addView(actionRow)
    card.addView(status)

    val params = FrameLayout.LayoutParams(
        FrameLayout.LayoutParams.MATCH_PARENT,
        FrameLayout.LayoutParams.WRAP_CONTENT
    ).apply { gravity = Gravity.BOTTOM }

    card.translationY = dp(ctx, 600f)
    root.addView(card, params)
    current = card
    card.animate().translationY(0f).setDuration(320).start()

    reload.setOnClickListener { hitReload(ctx, status) }
    send.setOnClickListener {
      val txt = prompt.text?.toString()?.trim().orEmpty()
      if (txt.isEmpty()) {
        setStatus(status, "Type something to send", Tone.Error); return@setOnClickListener
      }
      hitSend(ctx, status, prompt = txt, includeScreenshot = cb.isChecked)
    }
  }

  fun dismiss() {
    main.post {
      current?.let { v ->
        v.animate().translationY(dp(v.context, 600f)).setDuration(220).withEndAction {
          (v.parent as? ViewGroup)?.removeView(v)
        }.start()
      }
      current = null
      snapshot = null
    }
  }

  // ---- Networking -------------------------------------------------------

  private enum class Tone { Progress, Success, Error }

  private fun setStatus(view: TextView, msg: String, tone: Tone) {
    main.post {
      view.text = msg
      view.setTextColor(when (tone) {
        Tone.Progress -> Color.argb(180, 255, 255, 255)
        Tone.Success  -> Color.rgb(86, 217, 140)
        Tone.Error    -> Color.rgb(255, 116, 116)
      })
    }
  }

  private fun hitReload(ctx: Context, status: TextView) {
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
    val base = prefs.getString(YaverNativePrefs.AGENT_BASE_URL, "") ?: ""
    val auth = bestAuthToken(prefs)
    if (base.isEmpty()) { setStatus(status, "no agent URL", Tone.Error); return }
    setStatus(status, "Reloading…", Tone.Progress)
    net.execute {
      val resp = post("$base/dev/reload", auth, "{}")
      if (resp.ok) setStatus(status, "Reload requested ✓", Tone.Success)
      else setStatus(status, "Reload failed (HTTP ${resp.code}) ${resp.bodyTrim(160)}", Tone.Error)
    }
  }

  private fun hitSend(ctx: Context, status: TextView,
                      prompt: String, includeScreenshot: Boolean) {
    val prefs = ctx.getSharedPreferences(YaverNativePrefs.NAME, Context.MODE_PRIVATE)
    val base = prefs.getString(YaverNativePrefs.AGENT_BASE_URL, "") ?: ""
    val auth = bestAuthToken(prefs)
    if (base.isEmpty()) { setStatus(status, "no agent URL", Tone.Error); return }
    setStatus(status, "Sending…", Tone.Progress)
    val images = JSONArray()
    if (includeScreenshot && snapshot != null) {
      val baos = ByteArrayOutputStream()
      snapshot!!.compress(Bitmap.CompressFormat.JPEG, 70, baos)
      val b64 = Base64.encodeToString(baos.toByteArray(), Base64.NO_WRAP)
      images.put(JSONObject().apply {
        put("base64", b64)
        put("mimeType", "image/jpeg")
        put("filename", "yaver-feedback-${System.currentTimeMillis() / 1000}.jpg")
      })
    }
    val body = JSONObject().apply {
      put("title", prompt.take(80))
      put("description", prompt)
      put("userPrompt", prompt)
      put("runner", "claude")
      put("source", "mobile-feedback")
      put("images", images)
    }.toString()
    net.execute {
      val resp = post("$base/tasks", auth, body)
      if (resp.ok) {
        setStatus(status, "Sent ✓", Tone.Success)
        main.postDelayed({ dismiss() }, 900)
      } else {
        setStatus(status, "Send failed — HTTP ${resp.code} ${resp.bodyTrim(220)}", Tone.Error)
      }
    }
  }

  private data class Resp(val code: Int, val ok: Boolean, val body: String) {
    fun bodyTrim(n: Int): String = body.trim().take(n)
  }

  private fun post(urlStr: String, auth: String, jsonBody: String): Resp {
    return try {
      val url = URL(urlStr)
      val conn = url.openConnection() as HttpURLConnection
      conn.requestMethod = "POST"
      conn.connectTimeout = 8000
      conn.readTimeout = 30_000
      conn.doOutput = true
      conn.setRequestProperty("Authorization", "Bearer $auth")
      conn.setRequestProperty("Content-Type", "application/json")
      conn.outputStream.use { it.write(jsonBody.toByteArray()) }
      val code = conn.responseCode
      val stream = if (code in 200..299) conn.inputStream else conn.errorStream
      val body = stream?.bufferedReader()?.use { it.readText() } ?: ""
      Resp(code, code in 200..299, body)
    } catch (e: Exception) {
      Resp(0, false, e.message ?: "network error")
    }
  }

  private fun bestAuthToken(prefs: android.content.SharedPreferences): String {
    val inh = prefs.getString(YaverNativePrefs.INHERITED_AUTH_TOKEN, "") ?: ""
    if (inh.isNotEmpty()) return inh
    return prefs.getString(YaverNativePrefs.AGENT_AUTH, "") ?: ""
  }

  private fun captureSnapshot(activity: Activity): Bitmap? {
    return try {
      val view = activity.window.decorView
      val bmp = Bitmap.createBitmap(view.width.coerceAtLeast(1),
                                    view.height.coerceAtLeast(1),
                                    Bitmap.Config.ARGB_8888)
      view.draw(Canvas(bmp))
      bmp
    } catch (e: Exception) { null }
  }

  internal fun dp(ctx: Context, value: Float): Float =
      TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, value, ctx.resources.displayMetrics)
}
