// YaverMeshVpnService.kt — Yaver Mesh on-device WireGuard tunnel (Android).
//
// REFERENCE IMPLEMENTATION. NOT yet wired into the Android build — see
// docs/mesh-mobile-tunnel.md for the gradle dependency, manifest entries, and
// config-plugin steps. Uses the WireGuard GoBackend
// (com.wireguard.android:tunnel), which embeds wireguard-go.
//
// Lives under mobile/native-mesh/ (tracked) because `expo prebuild --clean`
// regenerates mobile/android; the config plugin copies this into the app's
// source set and registers the service + foreground notification.

package io.yaver.mobile.mesh

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Intent
import android.net.VpnService
import android.os.Build
import com.wireguard.android.backend.Backend
import com.wireguard.android.backend.GoBackend
import com.wireguard.android.backend.Tunnel
import com.wireguard.config.Config
import java.io.StringReader

class YaverMeshVpnService : VpnService() {
    companion object {
        const val ACTION_UP = "io.yaver.mesh.UP"
        const val ACTION_DOWN = "io.yaver.mesh.DOWN"
        const val EXTRA_CONFIG = "wgQuickConfig" // wg-quick config built from /mesh/peers
        private const val CHANNEL_ID = "yaver_mesh"
        private const val NOTIF_ID = 0x4D455348 // "MESH"
    }

    private lateinit var backend: Backend
    private val tunnel = object : Tunnel {
        override fun getName() = "yaver-mesh"
        override fun onStateChange(newState: Tunnel.State) {}
    }

    override fun onCreate() {
        super.onCreate()
        // GoBackend integrates with VpnService.Builder via this service.
        backend = GoBackend(applicationContext)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_DOWN -> {
                runCatching { backend.setState(tunnel, Tunnel.State.DOWN, null) }
                stopForeground(true)
                stopSelf()
            }
            else -> {
                val cfgText = intent?.getStringExtra(EXTRA_CONFIG) ?: return START_NOT_STICKY
                startForeground(NOTIF_ID, buildNotification())
                runCatching {
                    val config = Config.parse(StringReader(cfgText))
                    backend.setState(tunnel, Tunnel.State.UP, config)
                }
            }
        }
        return START_STICKY
    }

    private fun buildNotification(): Notification {
        val nm = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "Yaver Mesh", NotificationManager.IMPORTANCE_LOW)
            )
        }
        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("Yaver Mesh")
            .setContentText("Connected to your mesh")
            .setSmallIcon(applicationInfo.icon)
            .setOngoing(true)
            .build()
    }
}
