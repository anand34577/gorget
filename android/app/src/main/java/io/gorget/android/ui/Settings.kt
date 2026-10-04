package io.gorget.android.ui

import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.provider.Settings
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Share
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.core.graphics.drawable.toBitmap
import io.gorget.android.BuildConfig
import io.gorget.android.core.AppPrefs
import io.gorget.android.core.GorgetCore
import io.gorget.android.core.Status
import io.gorget.android.ui.theme.LocalSteel
import io.gorget.android.ui.theme.MonoStyle
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
fun ScreenScaffold(title: String, onBack: () -> Unit, actions: @Composable () -> Unit = {}, content: @Composable () -> Unit) {
    val s = LocalSteel.current
    Column(Modifier.fillMaxSize().background(s.bg)) {
        Row(Modifier.fillMaxWidth().statusBarsPadding().padding(horizontal = 4.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back", tint = s.ink) }
            Text(title, style = MaterialTheme.typography.titleLarge, color = s.ink, modifier = Modifier.weight(1f))
            actions()
        }
        content()
    }
}

@Composable
fun SettingsScreen(status: Status, onBack: () -> Unit, onApps: () -> Unit, onLogs: () -> Unit, onThemeChange: (String) -> Unit) {
    val s = LocalSteel.current
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val prefs = status.prefs
    var confirmLogout by remember { mutableStateOf(false) }
    var notifications by remember { mutableStateOf(GorgetCore.appPrefs.notifications) }
    var theme by remember { mutableStateOf(GorgetCore.appPrefs.theme) }
    var busy by remember { mutableStateOf(0) }
    var signingOut by remember { mutableStateOf(false) }
    val set: (io.gorget.android.core.Prefs) -> Unit = { p ->
        busy++
        scope.launch {
            runCatching { GorgetCore.setPrefs(p) }.onFailure { Snack.error(it.message ?: "Couldn't save the setting") }
            busy--
        }
    }
    val split = GorgetCore.appPrefs.splitMode

    ScreenScaffold("Settings", onBack) {
        TopProgress(busy > 0)
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 32.dp)) {
            SectionLabel("Connection")
            Panel(Modifier.fillMaxWidth()) {
                Column {
                    ToggleRow("Use Gorget DNS", "Reach devices by name (laptop.${status.domain.ifBlank { "gorget.internal" }}) and use your network's resolvers", prefs.useDns) { set(prefs.copy(useDns = it)) }
                    Divider()
                    ToggleRow("Post-quantum protection", "Between your Gorget devices, also protect against future quantum computers", !prefs.noPostQuantum) { set(prefs.copy(noPostQuantum = !it)) }
                    Divider()
                    ToggleRow("Use shared networks", "Reach home or office networks that other devices share", prefs.acceptRoutes) { set(prefs.copy(acceptRoutes = it)) }
                    Divider()
                    ToggleRow("Allow local network access", "Keep your Wi-Fi's printers and devices reachable while using an exit node", prefs.allowLan) { set(prefs.copy(allowLan = it)) }
                    Divider()
                    NavRow(
                        "Choose which apps use Gorget",
                        when (split) {
                            AppPrefs.SplitMode.ALL -> "All apps"
                            AppPrefs.SplitMode.ONLY_SELECTED -> "Only ${GorgetCore.appPrefs.splitApps.size} selected apps"
                            AppPrefs.SplitMode.EXCEPT_SELECTED -> "All except ${GorgetCore.appPrefs.splitApps.size} apps"
                        },
                        onClick = onApps,
                    )
                    Divider()
                    NavRow("Always-on VPN & kill switch", "Open Android's VPN settings to keep Gorget always connected and block traffic when it isn't") {
                        ctx.startActivity(Intent(Settings.ACTION_VPN_SETTINGS))
                    }
                }
            }

            SectionLabel("Appearance & alerts")
            Panel(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp)) {
                    Text("Theme", style = MaterialTheme.typography.titleSmall, color = s.ink)
                    Spacer(Modifier.height(8.dp))
                    val options = listOf("system" to "System", "light" to "Light", "dark" to "Dark")
                    SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                        options.forEachIndexed { i, (v, label) ->
                            SegmentedButton(
                                selected = theme == v,
                                onClick = { theme = v; GorgetCore.appPrefs.theme = v; onThemeChange(v) },
                                shape = SegmentedButtonDefaults.itemShape(i, options.size),
                            ) { Text(label) }
                        }
                    }
                }
                Divider()
            }
            Panel(Modifier.fillMaxWidth().padding(top = 8.dp)) {
                ToggleRow("Notifications", "Sign-in, approval and disconnection alerts", notifications) {
                    notifications = it
                    GorgetCore.appPrefs.notifications = it
                }
            }

            SectionLabel("Account")
            Panel(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    KV("Network", status.networkName.ifBlank { "—" })
                    KV("Server", status.serverUrl.removePrefix("https://"))
                    status.self?.let {
                        KV("This device", it.name)
                        KV("Signed in as", it.user.ifBlank { "setup key" })
                        if (it.keyExpiresAt > 0) KV("Sign-in expires", java.text.DateFormat.getDateInstance().format(java.util.Date(it.keyExpiresAt * 1000)))
                    }
                }
                Divider()
            }
            Panel(Modifier.fillMaxWidth().padding(top = 8.dp)) {
                NavRow("Sign out of this device", "Removes it from the network until you sign in again", danger = true) { confirmLogout = true }
            }

            SectionLabel("About")
            Panel(Modifier.fillMaxWidth()) {
                Column {
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        KV("App version", BuildConfig.VERSION_NAME)
                        KV("Core version", GorgetCore.coreVersion())
                        Text(
                            "Gorget collects nothing. No analytics, no crash reports, no tracking. Logs stay on this phone unless you share them.",
                            style = MaterialTheme.typography.bodySmall, color = s.ink3,
                        )
                        Text("Open source under AGPL-3.0. Fonts: IBM Plex and Bricolage Grotesque (SIL Open Font License).", style = MaterialTheme.typography.bodySmall, color = s.ink3)
                    }
                    Divider()
                    NavRow("Diagnostics", "Connection details and logs", onClick = onLogs)
                }
            }
        }
    }

    if (confirmLogout) {
        AlertDialog(
            onDismissRequest = { if (!signingOut) confirmLogout = false },
            title = { Text("Sign out of this device?") },
            text = { Text("It disconnects and leaves ${status.networkName.ifBlank { "the network" }}. You'll need to sign in again to reconnect.") },
            confirmButton = {
                TextButton(enabled = !signingOut, onClick = {
                    signingOut = true
                    scope.launch {
                        runCatching { GorgetCore.logout() }
                            .onSuccess { Snack.show("Signed out") }
                            .onFailure { Snack.error(it.message ?: "Couldn't sign out") }
                        signingOut = false
                        confirmLogout = false
                    }
                }) {
                    if (signingOut) androidx.compose.material3.CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = s.oxide)
                    else Text("Sign out", color = s.oxide)
                }
            },
            dismissButton = { TextButton(enabled = !signingOut, onClick = { confirmLogout = false }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun KV(k: String, v: String) {
    val s = LocalSteel.current
    Row(Modifier.fillMaxWidth()) {
        Text(k, style = MaterialTheme.typography.bodySmall, color = s.ink3, modifier = Modifier.weight(0.42f))
        Text(v, style = MaterialTheme.typography.bodyMedium, color = s.ink, modifier = Modifier.weight(0.58f))
    }
}

@Composable
fun LogsScreen(status: Status, onBack: () -> Unit) {
    val s = LocalSteel.current
    val ctx = LocalContext.current
    var logs by remember { mutableStateOf(GorgetCore.logs()) }
    LaunchedEffect(Unit) {
        while (true) {
            kotlinx.coroutines.delay(2000)
            logs = GorgetCore.logs()
        }
    }
    val report = buildString {
        appendLine("Gorget ${BuildConfig.VERSION_NAME} (core ${GorgetCore.coreVersion()})")
        appendLine("State: ${status.state}")
        appendLine("Relay: ${status.relayUrl ?: "—"} (${if (status.relayConnected) "connected" else "not connected"})")
        appendLine("Endpoints: ${status.endpoints.joinToString(", ").ifBlank { "—" }}")
        status.peers.forEach { appendLine("Peer ${it.name}: online=${it.online} direct=${it.direct} endpoint=${it.endpoint ?: "-"} latency=${it.latencyMs}ms") }
        appendLine()
        append(logs)
    }
    ScreenScaffold("Diagnostics", onBack, actions = {
        IconButton(onClick = {
            val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, report)
            ctx.startActivity(Intent.createChooser(send, "Share diagnostics"))
        }) { Icon(Icons.Default.Share, contentDescription = "Share diagnostics", tint = s.ink) }
    }) {
        Column(Modifier.padding(horizontal = 20.dp)) {
            Panel(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    KV("State", status.state)
                    KV("Relay", if (status.relayConnected) "Connected" else "Not connected")
                    KV("Endpoints", status.endpoints.size.toString())
                    KV("Direct peers", "${status.peers.count { it.direct }} of ${status.peers.count { it.online }} online")
                }
            }
            Text("Logs stay on this device. Sharing them sends them only where you choose.", style = MaterialTheme.typography.bodySmall, color = s.ink3, modifier = Modifier.padding(vertical = 10.dp))
        }
        SelectionContainer(Modifier.fillMaxSize().padding(horizontal = 20.dp)) {
            Column(Modifier.verticalScroll(rememberScrollState()).horizontalScroll(rememberScrollState())) {
                Text(logs.ifBlank { "No log lines yet." }, style = MonoStyle.copy(fontSize = MonoStyle.fontSize * 0.85f), color = s.ink2)
            }
        }
    }
}
