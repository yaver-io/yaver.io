package io.yaver.serverlesstodo

import android.app.Activity
import android.os.Bundle
import android.view.Gravity
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import org.json.JSONArray
import org.json.JSONObject
import java.io.OutputStreamWriter
import java.net.HttpURLConnection
import java.net.URL

data class Todo(val id: String, val title: String, val done: Boolean)

class MainActivity : Activity() {
    private lateinit var baseUrlInput: EditText
    private lateinit var slugInput: EditText
    private lateinit var tokenInput: EditText
    private lateinit var draftInput: EditText
    private lateinit var statusText: TextView
    private lateinit var list: LinearLayout
    private var todos: List<Todo> = emptyList()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(28, 28, 28, 28)
        }
        val scroll = ScrollView(this).apply { addView(root) }

        val title = TextView(this).apply {
            text = "Yaver Serverless Todo"
            textSize = 26f
            setTypeface(null, android.graphics.Typeface.BOLD)
        }
        root.addView(title)

        statusText = TextView(this).apply { text = "Not synced" }
        root.addView(statusText)

        baseUrlInput = input("Yaver Serverless URL", "http://127.0.0.1:18080")
        slugInput = input("Project slug", "yaver-serverless-todo")
        tokenInput = input("Project token", "")
        root.addView(baseUrlInput)
        root.addView(slugInput)
        root.addView(tokenInput)

        val refresh = Button(this).apply {
            text = "Refresh"
            setOnClickListener { refresh() }
        }
        root.addView(refresh)

        val composer = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        draftInput = input("What needs doing?", "").apply {
            layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f)
            imeOptions = EditorInfo.IME_ACTION_DONE
            setOnEditorActionListener { _, _, _ ->
                create()
                true
            }
        }
        composer.addView(draftInput)
        composer.addView(Button(this).apply {
            text = "Add"
            setOnClickListener { create() }
        })
        root.addView(composer)

        list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        root.addView(list)

        setContentView(scroll)
        refresh()
    }

    private fun input(hint: String, value: String) = EditText(this).apply {
        this.hint = hint
        setText(value)
        singleLine = true
    }

    private fun refresh() = runApi("Syncing...") {
        val json = request("GET", "/todos?limit=100")
        val rows = json.optJSONArray("rows") ?: JSONArray()
        val nextTodos = (0 until rows.length()).mapNotNull { index ->
            val row = rows.optJSONObject(index) ?: return@mapNotNull null
            Todo(row.optString("id"), row.optString("title", row.optString("text")), normalizeDone(row.opt("done")))
        }
        runOnUiThread {
            todos = nextTodos
            render()
            statusText.text = "${todos.count { !it.done }} open tasks"
        }
    }

    private fun create() {
        val title = draftInput.text.toString().trim()
        if (title.isEmpty()) return
        draftInput.setText("")
        runApi("Adding...") {
            request(
                "POST",
                "/todos",
                JSONObject()
                    .put("id", "todo-${System.currentTimeMillis()}")
                    .put("title", title)
                    .put("done", false)
                    .put("owner_id", "alice")
            )
            refresh()
        }
    }

    private fun render() {
        list.removeAllViews()
        if (todos.isEmpty()) {
            list.addView(TextView(this).apply {
                text = "No serverless todos yet."
                setPadding(0, 28, 0, 0)
            })
            return
        }
        todos.forEach { todo ->
            val row = LinearLayout(this).apply {
                orientation = LinearLayout.HORIZONTAL
                gravity = Gravity.CENTER_VERTICAL
                setPadding(0, 10, 0, 10)
            }
            row.addView(CheckBox(this).apply {
                isChecked = todo.done
                setOnClickListener {
                    runApi("Updating...") {
                        request("PATCH", "/todos/${encode(todo.id)}", JSONObject().put("done", !todo.done))
                        refresh()
                    }
                }
            })
            row.addView(TextView(this).apply {
                text = todo.title
                textSize = 17f
                layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f)
                paintFlags = if (todo.done) paintFlags or android.graphics.Paint.STRIKE_THRU_TEXT_FLAG else paintFlags
            })
            row.addView(Button(this).apply {
                text = "Delete"
                setOnClickListener {
                    runApi("Deleting...") {
                        request("DELETE", "/todos/${encode(todo.id)}")
                        refresh()
                    }
                }
            })
            list.addView(row)
        }
    }

    private fun runApi(label: String, block: () -> Unit) {
        statusText.text = label
        Thread {
            try {
                block()
            } catch (error: Exception) {
                runOnUiThread { statusText.text = error.message ?: "Request failed" }
            }
        }.start()
    }

    private fun request(method: String, path: String, body: JSONObject? = null): JSONObject {
        val base = baseUrlInput.text.toString().trim().trimEnd('/')
        val slug = slugInput.text.toString().trim()
        val token = tokenInput.text.toString().trim()
        val connection = URL("$base/data/$slug$path").openConnection() as HttpURLConnection
        connection.requestMethod = method
        connection.setRequestProperty("Accept", "application/json")
        if (token.isNotEmpty()) connection.setRequestProperty("Authorization", "Bearer $token")
        if (body != null) {
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "application/json")
            OutputStreamWriter(connection.outputStream).use { it.write(body.toString()) }
        }
        val stream = if (connection.responseCode in 200..299) connection.inputStream else connection.errorStream
        val text = stream?.bufferedReader()?.use { it.readText() }.orEmpty()
        val json = if (text.isBlank()) JSONObject() else JSONObject(text)
        if (connection.responseCode !in 200..299) {
            throw IllegalStateException(json.optString("error", json.optString("message", "Yaver Serverless request failed")))
        }
        return json
    }

    private fun normalizeDone(value: Any?): Boolean {
        return value == true || value == 1 || value == "1" || value == "true"
    }

    private fun encode(value: String) = java.net.URLEncoder.encode(value, "UTF-8")
}
