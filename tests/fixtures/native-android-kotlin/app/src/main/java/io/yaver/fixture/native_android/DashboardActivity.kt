package io.yaver.fixture.native_android

import android.app.Activity
import android.os.Bundle
import android.view.Gravity
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView

class DashboardActivity : Activity() {

    companion object {
        const val EXTRA_USERNAME = "username"
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val username = intent.getStringExtra(EXTRA_USERNAME) ?: "guest"

        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setPadding(48, 48, 48, 48)
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
        }

        root.addView(TextView(this).apply {
            text = "Hello, $username"
            textSize = 28f
            gravity = Gravity.CENTER
        })

        root.addView(TextView(this).apply {
            text = "You are signed in to the Yaver Android fixture."
            textSize = 14f
            gravity = Gravity.CENTER
            setPadding(0, 16, 0, 0)
        })

        setContentView(root)
    }
}
