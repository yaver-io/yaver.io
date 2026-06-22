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

    private const val CHANNEL_TASKS = "yaver_tasks"
    private const val TASK_NOTIF_BASE = 8348

    /** Create both channels: the ongoing FGS channel (LOW) and the dismissible
     *  task-update channel (DEFAULT, so completion is actually visible). */
    fun createChannels(ctx: Context) {
      if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
      val nm = ctx.getSystemService(NotificationManager::class.java)
      if (nm.getNotificationChannel(CHANNEL_ID) == null) {
        nm.createNotificationChannel(
          NotificationChannel(CHANNEL_ID, "Yaver sandbox", NotificationManager.IMPORTANCE_LOW).apply {
            description = "The on-device coding agent is running"
            setShowBadge(false)
          },
        )
      }
      if (nm.getNotificationChannel(CHANNEL_TASKS) == null) {
        nm.createNotificationChannel(
          NotificationChannel(CHANNEL_TASKS, "Yaver tasks", NotificationManager.IMPORTANCE_DEFAULT).apply {
            description = "On-device coding-agent task updates"
          },
        )
      }
    }

    /** Reflect the active task in the ongoing foreground notification (so the
     *  reviewer/user sees WHAT is running, not just "sandbox running"). No-op
     *  unless the service is actually up. */
    fun updateStatus(ctx: Context, text: String) {
      if (!running) return
      createChannels(ctx)
      val n = NotificationCompat.Builder(ctx, CHANNEL_ID)
        .setContentTitle("Yaver")
        .setContentText(if (text.isNotEmpty()) text else "Yaver sandbox running")
        .setSmallIcon(ctx.applicationInfo.icon)
        .setOngoing(true)
        .setPriority(NotificationCompat.PRIORITY_LOW)
        .build()
      ctx.getSystemService(NotificationManager::class.java).notify(NOTIF_ID, n)
    }

    /** The payoff: a dismissible "task finished" notification proving the
     *  foreground service kept the on-device coding task alive while the app was
     *  backgrounded, and completed it. This is the core justification a Google
     *  Play reviewer needs for FOREGROUND_SERVICE_SPECIAL_USE / on_device_coding_agent. */
    fun postTaskFinished(ctx: Context, title: String, status: String) {
      // Self-scoping: only the device actually hosting the on-device sandbox
      // posts a "task finished" notification, so viewing a remote box's tasks
      // from this phone never mis-fires a local notification.
      if (!running) return
      createChannels(ctx)
      val s = status.lowercase()
      val ok = s.isEmpty() || s == "completed" || s == "review" || s == "done"
      val icon = when {
        ok -> "✅"
        s == "failed" -> "❌"
        else -> "⏹"
      }
      val heading = when {
        ok -> "Task finished"
        s == "failed" -> "Task failed"
        else -> "Task stopped"
      }
      val body = if (title.isNotEmpty()) title else "Your on-device coding task is done"
      val n = NotificationCompat.Builder(ctx, CHANNEL_TASKS)
        .setContentTitle("$icon $heading")
        .setContentText(body)
        .setStyle(NotificationCompat.BigTextStyle().bigText(body))
        .setSmallIcon(ctx.applicationInfo.icon)
        .setAutoCancel(true)
        .setPriority(NotificationCompat.PRIORITY_DEFAULT)
        .build()
      val id = TASK_NOTIF_BASE + (Math.abs(title.hashCode()) % 1000)
      ctx.getSystemService(NotificationManager::class.java).notify(id, n)
    }
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

  private fun createChannel() = createChannels(this)

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
