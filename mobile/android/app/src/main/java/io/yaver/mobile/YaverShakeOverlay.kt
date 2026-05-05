package io.yaver.mobile

// Native shake overlay — Android counterpart to AppDelegate.swift's
// showShakeOverlay. Buttons: Feedback / Agents / Deploy / Back to Yaver.
// Adds the card directly to the activity's decor view so it floats above
// the React Native root view without needing a separate Window. Same
// purple-black tint and bottom-sheet feel as iOS.

import android.app.Activity
import android.content.Context
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.os.Handler
import android.os.Looper
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView

object YaverShakeOverlay {

  private var current: View? = null
  private val main = Handler(Looper.getMainLooper())
  private var dismissRunnable: Runnable? = null

  /** Show the 4-button card. Auto-dismisses after 5s if the user
   *  doesn't tap anything; that timer is also cancelled when a button
   *  routes to a sub-pane so the overlay tear-down doesn't fight the
   *  next pane's slide-in. */
  fun show(activity: Activity,
           onFeedback: () -> Unit,
           onAgents: () -> Unit,
           onDeploy: () -> Unit,
           onBack: () -> Unit) {
    main.post { presentInternal(activity, onFeedback, onAgents, onDeploy, onBack) }
  }

  fun dismiss() {
    main.post {
      dismissRunnable?.let { main.removeCallbacks(it) }
      dismissRunnable = null
      current?.let { v ->
        v.animate().alpha(0f).translationY(-30f).setDuration(220).withEndAction {
          (v.parent as? ViewGroup)?.removeView(v)
        }.start()
      }
      current = null
    }
  }

  private fun presentInternal(activity: Activity,
                              onFeedback: () -> Unit,
                              onAgents: () -> Unit,
                              onDeploy: () -> Unit,
                              onBack: () -> Unit) {
    dismiss()  // wipe any previous overlay to avoid stacking
    val ctx = activity
    val root = activity.window.decorView as? ViewGroup ?: return

    // Container card
    val card = LinearLayout(ctx).apply {
      orientation = LinearLayout.VERTICAL
      val bg = GradientDrawable().apply {
        cornerRadius = dp(ctx, 16f)
        // Purple-black tint over a near-black base; matches iOS contentView.
        setColor(Color.argb((0.95 * 255).toInt(), 14, 12, 28))
        setStroke(dp(ctx, 1f).toInt(), Color.argb(40, 255, 255, 255))
      }
      background = bg
      setPadding(dp(ctx, 12f).toInt(), dp(ctx, 12f).toInt(),
                 dp(ctx, 12f).toInt(), dp(ctx, 12f).toInt())
      elevation = dp(ctx, 12f)
    }

    val accent = Color.rgb(127, 140, 247)
    fun makeButton(label: String, action: () -> Unit): Button {
      return Button(ctx).apply {
        text = label
        setTextColor(accent)
        textSize = 15f
        isAllCaps = false
        background = GradientDrawable().apply {
          cornerRadius = dp(ctx, 12f)
          setColor(Color.argb((0.12 * 255).toInt(),
                              Color.red(accent), Color.green(accent), Color.blue(accent)))
        }
        layoutParams = LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT,
            dp(ctx, 46f).toInt()
        ).apply { topMargin = dp(ctx, 5f).toInt(); bottomMargin = dp(ctx, 5f).toInt() }
        setPadding(dp(ctx, 18f).toInt(), 0, dp(ctx, 18f).toInt(), 0)
        gravity = Gravity.CENTER
        setOnClickListener {
          dismissRunnable?.let { main.removeCallbacks(it) }
          dismissRunnable = null
          dismiss()
          action()
        }
      }
    }

    card.addView(makeButton("💬  Feedback", onFeedback))
    card.addView(makeButton("⚙  Agents", onAgents))
    card.addView(makeButton("✈  Deploy", onDeploy))
    card.addView(makeButton("‹  Back to Yaver", onBack))

    val params = FrameLayout.LayoutParams(
        FrameLayout.LayoutParams.MATCH_PARENT,
        FrameLayout.LayoutParams.WRAP_CONTENT
    ).apply {
      gravity = Gravity.TOP
      val sideMargin = dp(ctx, 16f).toInt()
      val topMargin = dp(ctx, 48f).toInt()  // below the status bar
      setMargins(sideMargin, topMargin, sideMargin, 0)
    }

    card.alpha = 0f
    card.translationY = -dp(ctx, 30f)
    root.addView(card, params)
    current = card
    card.animate().alpha(1f).translationY(0f).setDuration(320).start()

    val r = Runnable { dismiss() }
    dismissRunnable = r
    main.postDelayed(r, 5000)
  }

  internal fun dp(ctx: Context, value: Float): Float =
      TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, value, ctx.resources.displayMetrics)
}
