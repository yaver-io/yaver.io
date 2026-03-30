package io.yaver.mobile

import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.widget.FrameLayout
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import com.facebook.react.ReactRootView
import com.facebook.react.modules.core.DefaultHardwareBackBtnHandler

/**
 * Activity that hosts the loaded external React Native app.
 * Presents a full-screen ReactRootView with a "Back to Yaver" floating button.
 */
class YaverBundleHostActivity : AppCompatActivity(), DefaultHardwareBackBtnHandler {

    private var reactRootView: ReactRootView? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val instanceManager = YaverBundleLoaderModule.loadedInstanceManager
        val moduleName = YaverBundleLoaderModule.pendingModuleName ?: "main"

        if (instanceManager == null) {
            finish()
            return
        }

        // Create the React root view
        reactRootView = ReactRootView(this).apply {
            startReactApplication(instanceManager, moduleName, null)
        }

        // Wrap in a FrameLayout with a floating back button
        val container = FrameLayout(this)
        container.addView(reactRootView, FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT,
            FrameLayout.LayoutParams.MATCH_PARENT
        ))

        // Floating "Back to Yaver" button
        val backButton = TextView(this).apply {
            text = "Back to Yaver"
            setTextColor(0xFFFFFFFF.toInt())
            textSize = 13f
            setPadding(40, 20, 40, 20)
            setBackgroundColor(0xE61A1A26.toInt())
            gravity = Gravity.CENTER

            // Rounded corners
            background = android.graphics.drawable.GradientDrawable().apply {
                setColor(0xE61A1A26.toInt())
                cornerRadius = 40f
            }

            setOnClickListener {
                finish()
            }
        }

        val buttonParams = FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.WRAP_CONTENT,
            FrameLayout.LayoutParams.WRAP_CONTENT
        ).apply {
            gravity = Gravity.TOP or Gravity.START
            topMargin = 120  // below status bar
            leftMargin = 30
        }
        container.addView(backButton, buttonParams)

        setContentView(container)
    }

    override fun invokeDefaultOnBackPressed() {
        super.onBackPressed()
    }

    override fun onDestroy() {
        reactRootView?.unmountReactApplication()
        reactRootView = null
        YaverBundleLoaderModule.loadedInstanceManager?.onHostDestroy(this)
        super.onDestroy()
    }

    override fun onResume() {
        super.onResume()
        YaverBundleLoaderModule.loadedInstanceManager?.onHostResume(this, this)
    }

    override fun onPause() {
        super.onPause()
        YaverBundleLoaderModule.loadedInstanceManager?.onHostPause(this)
    }
}
