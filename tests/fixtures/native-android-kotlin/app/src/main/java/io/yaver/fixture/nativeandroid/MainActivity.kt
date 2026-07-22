package io.yaver.fixture.nativeandroid

import android.app.Activity
import android.os.Bundle
import android.widget.TextView

class MainActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(TextView(this).apply {
            text = "Yaver native Android Kotlin fixture"
            textSize = 20f
        })
    }
}
