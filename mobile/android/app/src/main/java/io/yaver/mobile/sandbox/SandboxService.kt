package io.yaver.mobile.sandbox

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.Uri
import android.os.BatteryManager
import android.os.Build
import android.os.IBinder
import android.os.Handler
import android.os.Looper
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
    private const val STOP_REQUEST_CODE = 8347
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
      ctx.getSystemService(NotificationManager::class.java).notify(
        NOTIF_ID,
        buildOngoingNotification(ctx, if (text.isNotEmpty()) text else "Yaver sandbox running"),
      )
    }

    /** The foreground notification is both truthful and actionable: tapping its
     * body returns to Yaver, while Stop ends the user-started hosting session. */
    private fun buildOngoingNotification(ctx: Context, text: String): Notification {
      val stop = PendingIntent.getService(
        ctx,
        STOP_REQUEST_CODE,
        Intent(ctx, SandboxService::class.java).apply { action = ACTION_STOP },
        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
      )
      return NotificationCompat.Builder(ctx, CHANNEL_ID)
        .setContentTitle("Yaver")
        .setContentText(text)
        .setSmallIcon(ctx.applicationInfo.icon)
        .setOngoing(true)
        .setContentIntent(openTaskIntent(ctx, null))
        .addAction(NotificationCompat.Action(0, "Stop", stop))
        .setPriority(NotificationCompat.PRIORITY_LOW)
        .build()
    }

    /** The payoff: a dismissible "task finished" notification proving the
     *  foreground service kept the on-device coding task alive while the app was
     *  backgrounded, and completed it. This is the core justification a Google
     *  Play reviewer needs for FOREGROUND_SERVICE_SPECIAL_USE / on_device_coding_agent. */
    fun postTaskFinished(ctx: Context, title: String, status: String, taskId: String?) {
      // Self-scoping: only the device actually hosting the on-device sandbox
      // posts a "task finished" notification, so viewing a remote box's tasks
      // from this phone never mis-fires a local notification.
      if (!running) return
      createChannels(ctx)
      val s = status.lowercase()
      val ok = s.isEmpty() || s == "completed" || s == "done"
      val icon = when {
        s == "review" -> "🟣"
        s == "ready" -> "💬"
        ok -> "✅"
        s == "failed" -> "❌"
        else -> "⏹"
      }
      val heading = when {
        s == "review" -> "Task needs review"
        s == "ready" -> "Your turn"
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
        .setContentIntent(openTaskIntent(ctx, taskId))
        .setPriority(NotificationCompat.PRIORITY_DEFAULT)
        .build()
      val id = TASK_NOTIF_BASE + (Math.abs(title.hashCode()) % 1000)
      ctx.getSystemService(NotificationManager::class.java).notify(id, n)
    }

    /**
     * Native sandbox completions bypass Expo Notifications, so they must carry
     * the task route themselves. A bare launcher intent only reopened the app
     * and left the user at a generic tab/list with no way to know which turn
     * had asked for review.
     */
    private fun openTaskIntent(ctx: Context, taskId: String?): PendingIntent? {
      val launch = ctx.packageManager.getLaunchIntentForPackage(ctx.packageName) ?: return null
      val id = taskId?.trim().orEmpty()
      if (id.isNotEmpty()) {
        launch.action = Intent.ACTION_VIEW
        launch.data = Uri.Builder()
          .scheme("yaver")
          .authority("tasks")
          .appendQueryParameter("taskId", id)
          .appendQueryParameter("taskNotificationNonce", System.currentTimeMillis().toString())
          .build()
      }
      launch.addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP)
      val requestCode = if (id.isEmpty()) 0 else id.hashCode()
      return PendingIntent.getActivity(
        ctx,
        requestCode,
        launch,
        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
      )
    }
  }

  private var proc: Process? = null
  private var wakeLock: PowerManager.WakeLock? = null
  private var supervisor: Thread? = null
  private val mainHandler = Handler(Looper.getMainLooper())

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
    val watched = proc ?: return
    supervisor = Thread {
      var exitCode: Int? = null
      try {
        exitCode = watched.waitFor()
        Log.w(TAG, "agent exited code=$exitCode")
      } catch (_: InterruptedException) {
        // stopAgent() interrupted us — normal shutdown.
      } finally {
        mainHandler.post { onAgentExited(watched, exitCode) }
      }
    }.also { it.isDaemon = true; it.start() }
  }

  /** A dead child process is not a running coding box. Remove the foreground
   * claim and wake lock immediately instead of leaving a false notification
   * and battery drain behind. Manual stop clears proc first, so its supervisor
   * callback is ignored here. */
  private fun onAgentExited(watched: Process, exitCode: Int?) {
    if (proc !== watched) return
    Log.w(TAG, "stopping foreground service after agent exit code=$exitCode")
    running = false
    homeHostMode = false
    saveHomeHostMode(false)
    proc = null
    supervisor = null
    releaseWakeLock()
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
      stopForeground(STOP_FOREGROUND_REMOVE)
    } else {
      @Suppress("DEPRECATION") stopForeground(true)
    }
    stopSelf()
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
    return buildOngoingNotification(this, text)
  }
}

/**
 * Foreground lease for the lightweight RN/DeepSeek repo agent and explicit Git
 * commit/push. This service intentionally does not launch libyaver or proot.
 * Its finite dataSync lifetime keeps the app process + CPU available while the
 * user-started operation runs, then stops. A 30-minute native deadline prevents
 * a JS crash from leaving a wake lock or foreground notification behind.
 */
class RemotelessTaskService : Service() {
  companion object {
    private const val CHANNEL_ID = "yaver_remoteless_tasks"
    private const val COMPLETION_CHANNEL_ID = "yaver_remoteless_results"
    private const val NOTIFICATION_ID = 9347
    private const val MAX_RUNTIME_MS = 30L * 60L * 1000L
    const val ACTION_START = "io.yaver.mobile.remoteless.START"
    const val ACTION_UPDATE = "io.yaver.mobile.remoteless.UPDATE"
    const val ACTION_FINISH = "io.yaver.mobile.remoteless.FINISH"
    const val EXTRA_ID = "task_id"
    const val EXTRA_TITLE = "task_title"
    const val EXTRA_PHASE = "task_phase"
    const val EXTRA_STATUS = "task_status"
  }

  private val handler = Handler(Looper.getMainLooper())
  private var wakeLock: PowerManager.WakeLock? = null
  private var taskId = ""
  private var title = "Coding on this device"
  private var phase = "starting"
  private val deadline = Runnable { finish("failed", "Background time limit reached before the task reported completion — open Yaver to continue or retry") }

  override fun onBind(intent: Intent?): IBinder? = null

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    when (intent?.action) {
      ACTION_FINISH -> {
        if (taskId.isEmpty()) stopSelf(startId)
        else if (matches(intent)) finish(intent.getStringExtra(EXTRA_STATUS) ?: "completed", null)
        return START_NOT_STICKY
      }
      ACTION_UPDATE -> {
        if (taskId.isEmpty()) stopSelf(startId)
        else if (matches(intent)) {
          phase = intent.getStringExtra(EXTRA_PHASE)?.take(80) ?: phase
          notifyForeground()
        }
        return START_NOT_STICKY
      }
      else -> {
        taskId = intent?.getStringExtra(EXTRA_ID).orEmpty()
        title = intent?.getStringExtra(EXTRA_TITLE)?.take(100).orEmpty().ifEmpty { "Coding on this device" }
        phase = intent?.getStringExtra(EXTRA_PHASE)?.take(80).orEmpty().ifEmpty { "starting" }
        createChannels()
        startForeground(NOTIFICATION_ID, buildNotification(title, phase, true))
        acquireWakeLock()
        handler.removeCallbacks(deadline)
        handler.postDelayed(deadline, MAX_RUNTIME_MS)
        return START_NOT_STICKY
      }
    }
  }

  private fun matches(intent: Intent): Boolean {
    val incoming = intent.getStringExtra(EXTRA_ID).orEmpty()
    return incoming.isEmpty() || incoming == taskId
  }

  private fun finish(status: String, overrideText: String?) {
    handler.removeCallbacks(deadline)
    val ok = status == "completed"
    val heading = if (ok) "Task finished" else if (status == "ready") "Your turn" else if (status == "failed") "Task failed" else "Task needs review"
    val body = overrideText ?: title
    val notification = NotificationCompat.Builder(this, COMPLETION_CHANNEL_ID)
      .setContentTitle(heading)
      .setContentText(body)
      .setStyle(NotificationCompat.BigTextStyle().bigText(body))
      .setSmallIcon(applicationInfo.icon)
      .setAutoCancel(true)
      .setContentIntent(openAppIntent())
      .setPriority(NotificationCompat.PRIORITY_DEFAULT)
      .build()
    getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID + 1, notification)
    releaseWakeLock()
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
      stopForeground(STOP_FOREGROUND_REMOVE)
    } else {
      @Suppress("DEPRECATION") stopForeground(true)
    }
    stopSelf()
  }

  override fun onDestroy() {
    handler.removeCallbacks(deadline)
    releaseWakeLock()
    super.onDestroy()
  }

  private fun acquireWakeLock() {
    if (wakeLock?.isHeld == true) return
    val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
    wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "yaver:remoteless-task").apply {
      setReferenceCounted(false)
      acquire(MAX_RUNTIME_MS + 60_000L)
    }
  }

  private fun releaseWakeLock() {
    wakeLock?.let { if (it.isHeld) it.release() }
    wakeLock = null
  }

  private fun createChannels() {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
    val manager = getSystemService(NotificationManager::class.java)
    if (manager.getNotificationChannel(CHANNEL_ID) == null) {
      manager.createNotificationChannel(NotificationChannel(CHANNEL_ID, "On-device tasks", NotificationManager.IMPORTANCE_LOW).apply {
        description = "Finite coding and Git tasks running on this device"
        setShowBadge(false)
      })
    }
    if (manager.getNotificationChannel(COMPLETION_CHANNEL_ID) == null) {
      manager.createNotificationChannel(NotificationChannel(COMPLETION_CHANNEL_ID, "On-device task results", NotificationManager.IMPORTANCE_DEFAULT))
    }
  }

  private fun notifyForeground() {
    getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, buildNotification(title, phase, true))
  }

  private fun buildNotification(heading: String, detail: String, ongoing: Boolean): Notification =
    NotificationCompat.Builder(this, CHANNEL_ID)
      .setContentTitle(heading)
      .setContentText(detail)
      .setSmallIcon(applicationInfo.icon)
      .setOngoing(ongoing)
      .setContentIntent(openAppIntent())
      .setPriority(NotificationCompat.PRIORITY_LOW)
      .build()

  private fun openAppIntent(): PendingIntent? {
    val launch = packageManager.getLaunchIntentForPackage(packageName) ?: return null
    launch.addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP)
    return PendingIntent.getActivity(this, 0, launch, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
  }
}
