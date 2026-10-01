package io.gorget.android.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import io.gorget.android.core.GorgetCore
import io.gorget.android.core.Status
import io.gorget.android.ui.theme.GorgetTheme
import io.gorget.android.ui.theme.LocalSteel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    companion object {
        const val ACTION_CONNECT = "io.gorget.android.ACTION_CONNECT"
    }

    private val themeState = mutableStateOf("system")

    private val vpnPermission = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { res ->
        if (res.resultCode == RESULT_OK) GorgetCore.startTunnel(this)
    }

    private val notifPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    fun connect() {
        val prepare = VpnService.prepare(this)
        if (prepare != null) vpnPermission.launch(prepare) else GorgetCore.startTunnel(this)
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        themeState.value = GorgetCore.appPrefs.theme
        // Live peer details (paths, latency, traffic) while the app is visible.
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                while (true) {
                    GorgetCore.refresh()
                    delay(2000)
                }
            }
        }
        setContent {
            GorgetTheme(themeState.value) {
                val status by GorgetCore.status.collectAsStateWithLifecycle()
                Box(Modifier.fillMaxSize().background(LocalSteel.current.bg).navigationBarsPadding()) {
                    Root(status, onConnect = ::connect, onThemeChange = { themeState.value = it })
                }
            }
        }
        handleIntent(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    private fun handleIntent(intent: Intent?) {
        if (intent?.action == ACTION_CONNECT && GorgetCore.isRegistered()) connect()
    }
}

private enum class Page { HOME, SETTINGS, APPS, LOGS }

@Composable
private fun Root(status: Status, onConnect: () -> Unit, onThemeChange: (String) -> Unit) {
    var page by rememberSaveable { mutableStateOf(Page.HOME) }
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val reset by ServerReset.requested

    // After signing in, connect straight away (asks for VPN permission the first time).
    var prevState by rememberSaveable { mutableStateOf(status.state) }
    LaunchedEffect(status.state) {
        if (prevState == Status.STATE_NEEDS_LOGIN || prevState == Status.STATE_EXPIRED) {
            if (status.state == Status.STATE_STOPPED && GorgetCore.isRegistered()) onConnect()
        }
        prevState = status.state
    }

    when {
        status.state == Status.STATE_NO_SERVER || reset -> {
            OnboardingScreen(onDone = { ServerReset.requested.value = false })
            if (reset) BackHandler { ServerReset.requested.value = false }
        }
        status.state == Status.STATE_NEEDS_LOGIN || status.state == Status.STATE_EXPIRED -> LoginScreen(status)
        else -> {
            BackHandler(enabled = page != Page.HOME) { page = if (page == Page.APPS || page == Page.LOGS) Page.SETTINGS else Page.HOME }
            when (page) {
                Page.HOME -> HomeScreen(status, onConnect = onConnect, onDisconnect = { GorgetCore.stopTunnel(ctx) }, onSettings = { page = Page.SETTINGS })
                Page.SETTINGS -> SettingsScreen(status, onBack = { page = Page.HOME }, onApps = { page = Page.APPS }, onLogs = { page = Page.LOGS }, onThemeChange = onThemeChange)
                Page.APPS -> AppsScreen(onBack = { page = Page.SETTINGS })
                Page.LOGS -> LogsScreen(status, onBack = { page = Page.SETTINGS })
            }
        }
    }
}
