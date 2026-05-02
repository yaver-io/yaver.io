package io.yaver.fixture.native_android

import android.graphics.Typeface
import android.os.Bundle
import android.view.Gravity
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.ImageButton
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity

class TodoListActivity : AppCompatActivity() {
  private val store = TodoStore()
  private lateinit var draftInput: EditText
  private lateinit var listContainer: LinearLayout

  override fun onCreate(savedInstanceState: Bundle?) {
    super.onCreate(savedInstanceState)

    val root = LinearLayout(this).apply {
      orientation = LinearLayout.VERTICAL
      setPadding(dp(16), dp(20), dp(16), dp(16))
      layoutParams = ViewGroup.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.MATCH_PARENT,
      )
    }

    val title = TextView(this).apply {
      text = "Kotlin Todo Fixture"
      textSize = 24f
      setTypeface(typeface, Typeface.BOLD)
    }
    root.addView(title)

    val subtitle = TextView(this).apply {
      text = "Use this app to test Yaver remote runtime taps, text entry, relay mode, and feedback commands."
      textSize = 13f
      setPadding(0, dp(6), 0, dp(12))
    }
    root.addView(subtitle)

    val composer = LinearLayout(this).apply {
      orientation = LinearLayout.HORIZONTAL
      gravity = Gravity.CENTER_VERTICAL
    }
    draftInput = EditText(this).apply {
      hint = "Add a todo item"
      minLines = 1
      setSingleLine()
      layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
        marginEnd = dp(10)
      }
    }
    composer.addView(draftInput)

    val addButton = Button(this).apply {
      text = "Add"
      setOnClickListener {
        if (store.add(draftInput.text.toString())) {
          draftInput.setText("")
          renderItems()
        }
      }
    }
    composer.addView(addButton)
    root.addView(composer)

    val scroll = ScrollView(this).apply {
      layoutParams = LinearLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        0,
        1f,
      ).apply {
        topMargin = dp(16)
      }
    }
    listContainer = LinearLayout(this).apply {
      orientation = LinearLayout.VERTICAL
    }
    scroll.addView(listContainer)
    root.addView(scroll)

    setContentView(root)
    renderItems()
  }

  private fun renderItems() {
    listContainer.removeAllViews()
    store.items.forEach { item ->
      val row = LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        setPadding(dp(12), dp(12), dp(12), dp(12))
      }

      val toggle = Button(this).apply {
        text = if (item.done) "Done" else "Open"
        minWidth = dp(68)
        setOnClickListener {
          store.toggle(item.id)
          renderItems()
        }
      }
      row.addView(toggle)

      val labelWrap = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
          marginStart = dp(12)
          marginEnd = dp(12)
        }
      }
      val label = TextView(this).apply {
        text = item.title
        textSize = 16f
        paint.isStrikeThruText = item.done
      }
      val state = TextView(this).apply {
        text = if (item.done) "Completed" else "Open"
        textSize = 12f
      }
      labelWrap.addView(label)
      labelWrap.addView(state)
      row.addView(labelWrap)

      val remove = ImageButton(this).apply {
        setImageResource(android.R.drawable.ic_menu_delete)
        contentDescription = "Delete ${item.title}"
        setBackgroundColor(0x00000000)
        setOnClickListener {
          store.remove(item.id)
          renderItems()
        }
      }
      row.addView(remove)

      listContainer.addView(row)
    }
  }

  private fun dp(value: Int): Int = (value * resources.displayMetrics.density).toInt()
}
