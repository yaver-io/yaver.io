package io.yaver.tv.ui

import android.os.Bundle
import android.view.KeyEvent
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import io.yaver.tv.TvApp

/**
 * MainActivity — the single Compose-hosting activity of the TV app. The whole
 * app is one activity + one navigation host; the auth gate lives in the host,
 * exactly like tvOS RootView (YaverTVApp.swift): signed-in → Dashboard, else
 * SignIn. D-pad/back handling is Compose's default focus navigation.
 */
class MainActivity : ComponentActivity() {

    private val app: TvApp get() = application as TvApp

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        TvColors.applyTheme(app.store.appearanceTheme.value)
        setContent {
            androidx.compose.foundation.layout.Column {
                androidx.compose.ui.viewinterop.AndroidView(factory = { context -> android.widget.Button(context).apply { text = "SSH panes · no Yaver sign-in"; setOnClickListener { context.startActivity(android.content.Intent(context, io.yaver.plainssh.PlainSSHActivity::class.java)) } } })

            Surface(modifier = Modifier.fillMaxSize()) {
                YaverTvApp(store = app.store)
            }

            }
        }
    }

    override fun onDestroy() {
        app.speech.shutdown()
        super.onDestroy()
    }

    /** Let the system back/menu key pop the navigation back-stack. */
    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        return super.onKeyDown(keyCode, event)
    }
}
