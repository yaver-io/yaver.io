package io.yaver.fixture.native_android

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast

class LoginActivity : Activity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(48, 96, 48, 48)
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
        }

        val title = TextView(this).apply {
            text = "Sign in to Yaver Fixture"
            textSize = 22f
            gravity = Gravity.CENTER
        }
        root.addView(title)

        val hint = TextView(this).apply {
            text = "Hardcoded creds: admin / admin"
            textSize = 13f
            gravity = Gravity.CENTER
            setPadding(0, 16, 0, 32)
        }
        root.addView(hint)

        val username = EditText(this).apply {
            hint = "Username"
            setText(Auth.VALID_USERNAME)
        }
        root.addView(username)

        val password = EditText(this).apply {
            hint = "Password"
            inputType = InputType.TYPE_TEXT_VARIATION_PASSWORD or InputType.TYPE_CLASS_TEXT
            setText(Auth.VALID_PASSWORD)
        }
        root.addView(password)

        val signIn = Button(this).apply {
            text = "Sign in"
            setPadding(0, 32, 0, 0)
        }
        signIn.setOnClickListener {
            if (Auth.authenticate(username.text.toString(), password.text.toString())) {
                val intent = Intent(this@LoginActivity, DashboardActivity::class.java)
                intent.putExtra(DashboardActivity.EXTRA_USERNAME, username.text.toString())
                startActivity(intent)
                finish()
            } else {
                Toast.makeText(this, "Invalid credentials. Use admin / admin.", Toast.LENGTH_SHORT).show()
            }
        }
        root.addView(signIn)

        setContentView(root)
    }
}
