package io.gorget.android.core

import android.content.Context
import android.content.Intent
import android.os.Build
import android.provider.Settings
import android.util.Log
import io.gorget.android.notify.Notifier
import io.gorget.android.vpn.GorgetVpnService
import io.gorget.android.widget.GorgetWidget
import io.gorget.gorgetcore.Client
import io.gorget.gorgetcore.Gorgetcore
import io.gorget.gorgetcore.Platform
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.text.SimpleDateFormat
import java.util.ArrayDeque
import java.util.Date
import java.util.Locale

/**
 * Process-wide bridge between the Go core and Android. The Go client lives for the
 * whole process; the VpnService attaches itself while the tunnel is up so the core
 * can create the interface and protect its sockets.
 */
object GorgetCore {
    private const val TAG = "Gorget"
    private lateinit var appContext: Context
    private lateinit var client: Client
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    private val _status = MutableStateFlow(Status())
    val status: StateFlow<Status> = _status.asStateFlow()

    @Volatile
    private var service: GorgetVpnService? = null

    private val logLines = ArrayDeque<String>()
    private val logTime = SimpleDateFormat("HH:mm:ss.SSS", Locale.US)

    lateinit var appPrefs: AppPrefs
        private set

    private val platform = object : Platform {
        override fun protect(fd: Int): Boolean = service?.protect(fd) ?: true

        override fun establishTUN(configJSON: String): Int {
            val svc = service ?: return -1
            return svc.establish(json.decodeFromString(TunConfig.serializer(), configJSON))
        }

        override fun onStatus(statusJSON: String) = publish(statusJSON)

        override fun log(level: Int, msg: String) {
            when (level) {
                0 -> Log.d(TAG, msg)
                1 -> Log.i(TAG, msg)
                2 -> Log.w(TAG, msg)
                else -> Log.e(TAG, msg)
            }
            synchronized(logLines) {
                logLines.addLast("${logTime.format(Date())} ${"DIWE"[level.coerceIn(0, 3)]} $msg")
                while (logLines.size > 800) logLines.removeFirst()
            }
        }
    }

    fun init(context: Context) {
        appContext = context.applicationContext
        appPrefs = AppPrefs(appContext)
        client = Gorgetcore.newClient(appContext.filesDir.absolutePath, deviceName(appContext), Build.VERSION.RELEASE, platform)
        refresh()
    }

    private fun deviceName(ctx: Context): String {
        val n = runCatching { Settings.Global.getString(ctx.contentResolver, Settings.Global.DEVICE_NAME) }.getOrNull()
        return n?.takeIf { it.isNotBlank() } ?: "${Build.MANUFACTURER} ${Build.MODEL}"
    }

    private fun publish(statusJSON: String) {
        val prev = _status.value
        val next = runCatching { json.decodeFromString(Status.serializer(), statusJSON) }.getOrElse { return }
        _status.value = next
        if (prev.state != next.state || prev.isRunning != next.isRunning) {
            Notifier.onTransition(appContext, prev, next)
            GorgetWidget.update(appContext, next)
        }
    }

    /** Re-reads the full status (peer paths, traffic) from the core. */
    fun refresh() = publish(client.status())

    fun logs(): String = synchronized(logLines) { logLines.joinToString("\n") }

    // ---------- service wiring ----------

    fun attach(svc: GorgetVpnService) {
        service = svc
    }

    fun detach(svc: GorgetVpnService) {
        if (service === svc) service = null
    }

    fun networkChanged(localAddresses: String) {
        client.setLocalAddresses(localAddresses)
        client.networkChanged()
    }

    // ---------- actions (call from coroutines) ----------

    suspend fun setServer(url: String): String = withContext(Dispatchers.IO) { client.setServer(url).also { refresh() } }

    suspend fun startBrowserLogin(): LoginStart = withContext(Dispatchers.IO) {
        json.decodeFromString(LoginStart.serializer(), client.loginInteractive()).also { refresh() }
    }

    suspend fun loginWithSetupKey(key: String) = withContext(Dispatchers.IO) {
        client.loginWithSetupKey(key)
        refresh()
    }

    suspend fun logout() = withContext(Dispatchers.IO) {
        stopTunnel(appContext)
        client.logout()
        refresh()
    }

    suspend fun setPrefs(p: Prefs) = withContext(Dispatchers.IO) {
        client.setPrefs(json.encodeToString(Prefs.serializer(), p))
        refresh()
    }

    suspend fun setExitNode(id: String) = withContext(Dispatchers.IO) {
        client.setExitNode(id)
        refresh()
    }

    suspend fun refreshTun() {
        withContext(Dispatchers.IO) { client.refreshTUN() }
    }

    fun isRegistered() = client.isRegistered()

    fun wantRunning() = client.wantRunning()

    fun coreVersion(): String = client.version()

    /** Called by the VpnService once it is in the foreground of the VPN stack. */
    fun up() {
        try {
            client.up()
        } catch (e: Exception) {
            platform.log(3, "up failed: ${e.message}")
        }
        refresh()
    }

    fun down() {
        client.down()
        refresh()
    }

    // ---------- tunnel control ----------

    /** Starts the tunnel; VPN permission must already be granted (VpnService.prepare). */
    fun startTunnel(ctx: Context) {
        val i = Intent(ctx, GorgetVpnService::class.java).setAction(GorgetVpnService.ACTION_CONNECT)
        ctx.startService(i)
    }

    fun stopTunnel(ctx: Context) {
        val i = Intent(ctx, GorgetVpnService::class.java).setAction(GorgetVpnService.ACTION_DISCONNECT)
        runCatching { ctx.startService(i) }
    }

    fun launchScope(block: suspend () -> Unit) = scope.launch { runCatching { block() } }
}
