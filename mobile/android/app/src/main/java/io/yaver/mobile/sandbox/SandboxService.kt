package io.yaver.mobile.sandbox

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.BatteryManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import androidx.core.app.NotificationCompat
import java.io.File

/**
 * SandboxService — launches and supervises the on-device Yaver agent
 * (`libyaver.so serve`) so the phone hosts its own coding agent on loopback
 * (127.0.0.1:18080). The agent runs NATIVE (static Go binary from jniLibs); it
 * opens /dev/ptmx and, when the env below is set, wraps runner/PTY subprocesses
 * in a proot Alpine rootfs (see desktop/agent/sandbox_proot.go).
 *
 * Foreground + WAKE_LOCK so Android doesn't freeze the agent (and the proot
 * tree under it) the moment the app backgrounds — this is the #1 on-device risk
 * (see docs/coding-agent-on-device.md, test T5).
 *
 * Launcher contract (must match sandbox_proot.go env names):
 *   YAVER_ANDROID_ROOTFS    extracted Alpine rootfs dir   (filesDir/rootfs)
 *   YAVER_ANDROID_PROOT     proot executable              (nativeLibraryDir/libproot.so)
 *   YAVER_ANDROID_LOADER    proot loader                  (nativeLibraryDir/libproot-loader.so)
 *   YAVER_ANDROID_TMP       writable PROOT_TMP_DIR         (cacheDir/proot-tmp)
 *   YAVER_ANDROID_CRED_HOME agent $HOME holding mirrored creds (filesDir/home)
 *   HOME                    = CRED_HOME so os.UserHomeDir() and the cred binds agree
 */
class SandboxService : Service() {

  companion object {
    private const val TAG = "YaverSandbox"
    private const val CHANNEL_ID = "yaver_sandbox"
    private const val PREFS = "yaver_sandbox"
    private const val PREF_HOME_HOST = "home_host"
    private const val NOTIF_ID = 8347 // arbitrary stable id (the yaver phone port)
    const val ACTION_START = "io.yaver.mobile.sandbox.START"
    const val ACTION_START_HOME_HOST = "io.yaver.mobile.sandbox.START_HOME_HOST"
    const val ACTION_STOP = "io.yaver.mobile.sandbox.STOP"

    @Volatile var running: Boolean = false
      private set
    @Volatile var homeHostMode: Boolean = false
      private set

    fun rootfsDir(ctx: Context): File = File(ctx.filesDir, "rootfs")
    fun credHomeDir(ctx: Context): File = File(ctx.filesDir, "home")
    fun prootTmpDir(ctx: Context): File = File(ctx.cacheDir, "proot-tmp")
    fun logFile(ctx: Context): File = File(ctx.filesDir, "sandbox/agent.log")
    fun batteryStatus(ctx: Context): Pair<Int, Boolean> {
      val i = ctx.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
      val level = i?.getIntExtra(BatteryManager.EXTRA_LEVEL, -1) ?: -1
      val scale = i?.getIntExtra(BatteryManager.EXTRA_SCALE, -1) ?: -1
      val pct = if (level >= 0 && scale > 0) ((level * 100f) / scale).toInt() else -1
      val st = i?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) ?: -1
      val plugged = i?.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0) ?: 0
      val charging = st == BatteryManager.BATTERY_STATUS_CHARGING ||
        st == BatteryManager.BATTERY_STATUS_FULL ||
        plugged != 0
      return Pair(pct, charging)
    }

    /** The dir Android extracted the jniLibs into, executable on disk. */
    fun nativeLibDir(ctx: Context): String = ctx.applicationInfo.nativeLibraryDir
  }

  private var proc: Process? = null
  private var wakeLock: PowerManager.WakeLock? = null
  private var supervisor: Thread? = null

  override fun onBind(intent: Intent?): IBinder? = null

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    when (intent?.action) {
      ACTION_STOP -> {
        stopAgent()
        stopSelf()
        return START_NOT_STICKY
      }
      ACTION_START_HOME_HOST -> startAgent(homeHost = true)
      ACTION_START -> startAgent(homeHost = false)
      else -> startAgent(homeHost = loadHomeHostMode())
    }
    // START_STICKY: if the OS kills us under memory pressure, recreate the
    // service (the supervisor re-launches the agent). The proot tree dies with
    // the agent, which is acceptable — tasks resume from the runner's session.
    return START_STICKY
  }

  private fun startAgent(homeHost: Boolean) {
    if (running) return
    homeHostMode = homeHost
    saveHomeHostMode(homeHost)
    createChannel()
    startForeground(
      NOTIF_ID,
      buildNotification(if (homeHost) "Hosting your assistant via relay" else "Yaver sandbox running"),
    )
    acquireWakeLock()

    val nativeDir = nativeLibDir(this)
    val yaver = File(nativeDir, "libyaver.so")
    val proot = File(nativeDir, "libproot.so")
    val loader = File(nativeDir, "libproot-loader.so")

    val rootfs = rootfsDir(this).apply { mkdirs() }
    val credHome = credHomeDir(this).apply { mkdirs() }
    val tmp = prootTmpDir(this).apply { mkdirs() }
    logFile(this).parentFile?.mkdirs()

    if (!yaver.exists()) {
      Log.e(TAG, "libyaver.so missing in $nativeDir — check jniLibs payload + useLegacyPackaging")
      stopSelf()
      return
    }

    val args = mutableListOf(yaver.absolutePath, "serve", "--port", "18080")
    if (homeHost) args.add("--relay-only")
    val pb = ProcessBuilder(args)
    pb.redirectErrorStream(true)
    pb.redirectOutput(ProcessBuilder.Redirect.appendTo(logFile(this)))

    val env = pb.environment()
    env["HOME"] = credHome.absolutePath
    env["YAVER_ANDROID_CRED_HOME"] = credHome.absolutePath
    // Only activate proot once the rootfs is actually populated; an empty rootfs
    // would make every runner spawn fail. RootfsInstaller writes .installed.
    val rootfsReady = File(rootfs, ".installed").exists() && proot.exists()
    if (rootfsReady) {
      env["YAVER_ANDROID_ROOTFS"] = rootfs.absolutePath
      env["YAVER_ANDROID_PROOT"] = proot.absolutePath
      if (loader.exists()) env["YAVER_ANDROID_LOADER"] = loader.absolutePath
      env["YAVER_ANDROID_TMP"] = tmp.absolutePath
    } else {
      Log.w(TAG, "rootfs not installed yet — agent runs WITHOUT proot (control-plane only)")
    }

    try {
      proc = pb.start()
      running = true
      Log.i(TAG, "agent started (homeHost=${homeHost}, relayOnly=${homeHost}, proot=${rootfsReady})")
      superviseProcess()
    } catch (e: Exception) {
      Log.e(TAG, "failed to launch agent: ${e.message}", e)
      running = false
      releaseWakeLock()
      stopSelf()
    }
  }

  private fun superviseProcess() {
    supervisor = Thread {
      try {
        val code = proc?.waitFor()
        Log.w(TAG, "agent exited code=$code")
      } catch (_: InterruptedException) {
        // stopAgent() interrupted us — normal shutdown.
      } finally {
        running = false
      }
    }.also { it.isDaemon = true; it.start() }
  }

  private fun stopAgent() {
    running = false
    homeHostMode = false
    saveHomeHostMode(false)
    supervisor?.interrupt()
    supervisor = null
    proc?.destroy()
    proc = null
    releaseWakeLock()
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
      stopForeground(STOP_FOREGROUND_REMOVE)
    } else {
      @Suppress("DEPRECATION") stopForeground(true)
    }
  }

  override fun onDestroy() {
    stopAgent()
    super.onDestroy()
  }

  private fun acquireWakeLock() {
    if (wakeLock != null) return
    val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
    wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "yaver:sandbox").apply {
      setReferenceCounted(false)
      acquire()
    }
  }

  private fun releaseWakeLock() {
    wakeLock?.let { if (it.isHeld) it.release() }
    wakeLock = null
  }

  private fun loadHomeHostMode(): Boolean =
    getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean(PREF_HOME_HOST, false)

  private fun saveHomeHostMode(enabled: Boolean) {
    getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean(PREF_HOME_HOST, enabled).apply()
  }

  private fun createChannel() {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
    val nm = getSystemService(NotificationManager::class.java)
    if (nm.getNotificationChannel(CHANNEL_ID) != null) return
    val ch = NotificationChannel(CHANNEL_ID, "Yaver sandbox", NotificationManager.IMPORTANCE_LOW).apply {
      description = "The on-device coding agent is running"
      setShowBadge(false)
    }
    nm.createNotificationChannel(ch)
  }

  private fun buildNotification(text: String): Notification {
    return NotificationCompat.Builder(this, CHANNEL_ID)
      .setContentTitle("Yaver")
      .setContentText(text)
      .setSmallIcon(applicationInfo.icon)
      .setOngoing(true)
      .setPriority(NotificationCompat.PRIORITY_LOW)
      .build()
  }
}
