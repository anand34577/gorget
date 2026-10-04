package io.gorget.android.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.gorget.android.core.GorgetCore
import io.gorget.android.core.PeerView
import io.gorget.android.core.Status
import io.gorget.android.ui.theme.LocalSteel
import io.gorget.android.ui.theme.MonoStyle
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(status: Status, onConnect: () -> Unit, onDisconnect: () -> Unit, onSettings: () -> Unit) {
    val s = LocalSteel.current
    var query by rememberSaveable { mutableStateOf("") }
    var showExit by remember { mutableStateOf(false) }
    var selected by remember { mutableStateOf<PeerView?>(null) }

    val peers = status.peers.filter {
        query.isBlank() || listOf(it.name, it.ipv4, it.user, it.os).any { f -> f.contains(query, ignoreCase = true) }
    }

    LazyColumn(
        Modifier.fillMaxSize().background(s.bg),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, bottom = 32.dp),
    ) {
        item {
            Row(Modifier.fillMaxWidth().statusBarsPadding().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(status.networkName.ifBlank { "Gorget" }, style = MaterialTheme.typography.titleLarge, color = s.ink)
                    Text(status.self?.user ?: status.serverUrl.removePrefix("https://"), style = MaterialTheme.typography.bodySmall, color = s.ink3)
                }
                IconButton(onClick = onSettings) { Icon(Icons.Default.Settings, contentDescription = "Settings", tint = s.ink2) }
            }
        }
        item { TopProgress(status.state == Status.STATE_CONNECTING, Modifier.padding(top = 4.dp)) }
        item { Hero(status, onConnect, onDisconnect) }
        status.error?.takeIf { status.state != Status.STATE_RUNNING }?.let { item { Banner(it, Tone.DANGER, Modifier.padding(top = 12.dp)) } }
        status.notice?.let { item { Banner(it, Tone.BLUED, Modifier.padding(top = 12.dp)) } }
        if (status.isRunning || status.state == Status.STATE_STOPPED) {
            item {
                SectionLabel("Internet traffic")
                ExitCard(status) { showExit = true }
            }
        }
        if (status.isRunning) {
            item {
                Row(verticalAlignment = Alignment.Bottom) {
                    SectionLabel("Devices · ${status.peers.count { it.online }} online", Modifier.weight(1f))
                }
                if (status.peers.size > 6) {
                    OutlinedTextField(
                        value = query, onValueChange = { query = it }, singleLine = true,
                        leadingIcon = { Icon(Icons.Default.Search, contentDescription = null) },
                        placeholder = { Text("Search devices") }, modifier = Modifier.fillMaxWidth().padding(bottom = 10.dp),
                    )
                }
            }
            if (peers.isEmpty()) {
                item {
                    Panel(Modifier.fillMaxWidth()) {
                        Text(
                            if (status.peers.isEmpty()) "No other devices are shared with you yet. Access rules decide who you can reach." else "No devices match.",
                            style = MaterialTheme.typography.bodyMedium, color = s.ink3, modifier = Modifier.padding(18.dp),
                        )
                    }
                }
            } else {
                item {
                    Panel(Modifier.fillMaxWidth()) {
                        Column {
                            peers.forEachIndexed { i, p ->
                                PeerRow(p, status.exitNodeId == p.id) { selected = p }
                                if (i < peers.size - 1) Divider()
                            }
                        }
                    }
                }
            }
        }
    }

    if (showExit) {
        ModalBottomSheet(onDismissRequest = { showExit = false }) { ExitSheet(status) { showExit = false } }
    }
    selected?.let { p ->
        val live = status.peers.firstOrNull { it.id == p.id } ?: p
        ModalBottomSheet(onDismissRequest = { selected = null }) { PeerSheet(live, status.domain) }
    }
}

@Composable
private fun Hero(status: Status, onConnect: () -> Unit, onDisconnect: () -> Unit) {
    val s = LocalSteel.current
    val lame = when (status.state) {
        Status.STATE_RUNNING -> LameState.ON
        Status.STATE_CONNECTING -> LameState.CONNECTING
        Status.STATE_PENDING, Status.STATE_BLOCKED -> LameState.WAITING
        else -> LameState.OFF
    }
    val (title, sub) = when (status.state) {
        Status.STATE_RUNNING -> "Connected" to (status.exitNode?.let { "All traffic goes through ${it.name}" } ?: "Private traffic goes through Gorget")
        Status.STATE_CONNECTING -> "Connecting…" to "Reaching ${status.networkName.ifBlank { "the server" }}"
        Status.STATE_PENDING -> "Waiting for approval" to "An administrator needs to approve this device."
        Status.STATE_DISABLED -> "Device disabled" to "Ask an administrator to enable it again."
        Status.STATE_BLOCKED -> "Blocked by security rules" to
            ("Fix this to connect:\n" + status.blockedReasons.ifEmpty { listOf("Your organisation's security rules are not met") }.joinToString("\n") { "• $it" })
        else -> "Not connected" to "Your traffic uses your normal connection."
    }
    Column(Modifier.fillMaxWidth().padding(top = 18.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Lames(lame, size = 150.dp)
        Text(title, style = MaterialTheme.typography.displaySmall, color = s.ink)
        Spacer(Modifier.height(4.dp))
        Text(sub, style = MaterialTheme.typography.bodyMedium, color = s.ink2)
        status.self?.takeIf { status.isRunning }?.let { self ->
            val ctx = LocalContext.current
            Spacer(Modifier.height(10.dp))
            Text(
                "${self.ipv4} · ${self.name}",
                style = MonoStyle, color = s.ink2,
                modifier = Modifier.clip(RoundedCornerShape(8.dp)).clickable { copyToClipboard(ctx, "IP address", self.ipv4) }
                    .background(s.sunken).padding(horizontal = 10.dp, vertical = 4.dp),
            )
        }
        Spacer(Modifier.height(18.dp))
        val active = status.isActive
        if (status.state != Status.STATE_DISABLED) {
            val connecting = status.state == Status.STATE_CONNECTING
            Button(
                onClick = if (active) onDisconnect else onConnect,
                colors = if (active) ButtonDefaults.buttonColors(containerColor = s.sunken, contentColor = s.ink) else ButtonDefaults.buttonColors(),
                modifier = Modifier.widthIn(min = 220.dp).height(54.dp),
                shape = RoundedCornerShape(27.dp),
            ) {
                if (connecting) {
                    CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = s.ink3)
                    Spacer(Modifier.size(10.dp))
                }
                Text(if (connecting) "Cancel" else if (active) "Disconnect" else "Connect", style = MaterialTheme.typography.titleMedium)
            }
        }
        if (status.killSwitchEnforced && !active) {
            Spacer(Modifier.height(8.dp))
            Text("Your organisation requires the kill switch. Turn on \"Block connections without VPN\" in Android's VPN settings.", style = MaterialTheme.typography.bodySmall, color = s.ink3)
        }
    }
}

@Composable
private fun ExitCard(status: Status, onClick: () -> Unit) {
    val s = LocalSteel.current
    val exit = status.exitNode
    Panel(Modifier.fillMaxWidth()) {
        Row(
            Modifier.fillMaxWidth().clickable(enabled = status.canChooseExit && status.isRunning, role = Role.Button, onClick = onClick).padding(16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                Text(if (exit != null) "Exit node: ${exit.name}" else "Private traffic only", style = MaterialTheme.typography.titleSmall, color = s.ink)
                Text(
                    when {
                        status.forcedExit -> "Set by your administrator"
                        exit != null -> "Websites see ${exit.name}'s internet connection"
                        else -> "Browsing uses this device's own connection"
                    },
                    style = MaterialTheme.typography.bodySmall, color = s.ink3,
                )
                status.exitWarning?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = s.straw) }
                if (exit != null && exit.txBytes > 8192 && exit.rxBytes == 0L) {
                    Text("Nothing is coming back from ${exit.name} yet. Check that it is online and that an access rule lets you use it as an exit node.", style = MaterialTheme.typography.bodySmall, color = s.straw)
                }
            }
            if (status.canChooseExit && status.isRunning) Text("Change", style = MaterialTheme.typography.labelLarge, color = s.blued)
        }
    }
}

@Composable
private fun ExitSheet(status: Status, onDone: () -> Unit) {
    val s = LocalSteel.current
    val scope = rememberCoroutineScope()
    val exits = status.peers.filter { it.exitNode }
    var busyId by remember { mutableStateOf<String?>(null) }
    val choose = { id: String ->
        if (busyId == null) {
            busyId = id
            scope.launch {
                runCatching { GorgetCore.setExitNode(id) }
                    .onSuccess {
                        Snack.show(if (id.isEmpty()) "Exit node off: only private traffic uses Gorget" else "Internet traffic now goes through ${exits.firstOrNull { it.id == id }?.name ?: "the exit node"}")
                    }
                    .onFailure { Snack.error(it.message ?: "Couldn't change the exit node") }
                busyId = null
                onDone()
            }
        }
    }
    Column(Modifier.padding(horizontal = 20.dp).padding(bottom = 28.dp)) {
        Text("Send internet traffic through", style = MaterialTheme.typography.titleLarge)
        Spacer(Modifier.height(4.dp))
        Text("An exit node hides your location and protects you on untrusted Wi-Fi.", style = MaterialTheme.typography.bodySmall, color = s.ink3)
        Spacer(Modifier.height(8.dp))
        TopProgress(busyId != null)
        Spacer(Modifier.height(4.dp))
        ExitOption("None: private traffic only", "Fastest; websites see your own connection", status.exitNodeId.isEmpty(), enabled = busyId == null, busy = busyId == "") { choose("") }
        exits.forEach { p ->
            ExitOption(p.name, buildString {
                append(if (p.online) "Online" else "Offline")
                if (p.direct && p.latencyMs > 0) append(" · ${p.latencyMs} ms")
                if (p.gateway) append(" · server gateway")
            }, status.exitNodeId == p.id, enabled = p.online && busyId == null, busy = busyId == p.id) { choose(p.id) }
        }
        if (exits.isEmpty()) Text("No exit nodes are available to you.", style = MaterialTheme.typography.bodyMedium, color = s.ink3, modifier = Modifier.padding(vertical = 12.dp))
        if (status.exitNodeId.isNotEmpty() || exits.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            val prefs = status.prefs
            ToggleRow("Allow local network access", "Reach printers and devices on your Wi-Fi while using an exit node", prefs.allowLan) { v ->
                scope.launch {
                    runCatching { GorgetCore.setPrefs(prefs.copy(allowLan = v)) }
                        .onSuccess { Snack.show(if (v) "Local network access on" else "Local network access off") }
                        .onFailure { Snack.error(it.message ?: "Couldn't save the setting") }
                }
            }
        }
    }
}

@Composable
private fun ExitOption(title: String, subtitle: String, selected: Boolean, enabled: Boolean = true, busy: Boolean = false, onClick: () -> Unit) {
    val s = LocalSteel.current
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable(enabled = enabled, role = Role.RadioButton, onClick = onClick).padding(vertical = 10.dp, horizontal = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (busy) {
            Box(Modifier.size(40.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp) }
        } else {
            RadioButton(selected = selected, onClick = null, enabled = enabled || selected)
        }
        Spacer(Modifier.size(10.dp))
        Column {
            Text(title, style = MaterialTheme.typography.titleSmall, color = if (enabled || selected) s.ink else s.ink3)
            Text(subtitle, style = MaterialTheme.typography.bodySmall, color = s.ink3)
        }
    }
}

@Composable
private fun PeerRow(p: PeerView, isExit: Boolean, onClick: () -> Unit) {
    val s = LocalSteel.current
    Row(
        Modifier.fillMaxWidth().clickable(role = Role.Button, onClick = onClick).padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Dot(p.online)
        Column(Modifier.weight(1f)) {
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(p.name, style = MaterialTheme.typography.titleSmall, color = s.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (isExit) Chip("exit", Tone.BLUED)
                if (p.gateway) Chip("gateway")
            }
            Text(
                listOfNotNull(osLabel[p.os] ?: p.os.ifBlank { null }, p.user.ifBlank { null }).joinToString(" · ").ifBlank { p.tags.joinToString(" ") },
                style = MaterialTheme.typography.bodySmall, color = s.ink3, maxLines = 1,
            )
        }
        Column(horizontalAlignment = Alignment.End) {
            Text(p.ipv4, style = MonoStyle, color = s.ink2)
            if (p.online && p.lastHandshake > 0) {
                Text((if (p.direct) "direct · ${p.latencyMs} ms" else "relayed") + (if (p.postQuantum) " · PQ" else ""), style = MaterialTheme.typography.bodySmall, color = if (p.direct) s.verdigris else s.ink3)
            } else if (!p.online) {
                Text(relTime(p.lastSeen), style = MaterialTheme.typography.bodySmall, color = s.ink3)
            }
        }
    }
}

@Composable
private fun PeerSheet(p: PeerView, domain: String) {
    val s = LocalSteel.current
    val ctx = LocalContext.current
    Column(Modifier.padding(horizontal = 20.dp).padding(bottom = 28.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Dot(p.online)
            Text(p.name, style = MaterialTheme.typography.headlineSmall)
        }
        Text(if (p.online) "Online" else "Last seen ${relTime(p.lastSeen)}", style = MaterialTheme.typography.bodySmall, color = s.ink3)
        Spacer(Modifier.height(8.dp))
        Panel(Modifier.fillMaxWidth()) {
            Column {
                CopyRow("Name", p.fqdn.ifBlank { "${p.name}.$domain" })
                Divider()
                CopyRow("IPv4", p.ipv4)
                if (p.ipv6.isNotBlank()) {
                    Divider()
                    CopyRow("IPv6", p.ipv6)
                }
            }
        }
        Spacer(Modifier.height(4.dp))
        Panel(Modifier.fillMaxWidth()) {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                if (p.postQuantum) Info("Encryption", "Protected against future quantum computers")
                Info("Connection", when {
                    p.lastHandshake == 0L -> "Not connected yet"
                    p.direct -> "Direct (peer-to-peer)"
                    else -> "Through the relay"
                })
                if (p.direct && p.endpoint != null) Info("Path", p.endpoint)
                if (p.direct) Info("Latency", "${p.latencyMs} ms")
                Info("Traffic", "↓ ${formatBytes(p.rxBytes)}  ↑ ${formatBytes(p.txBytes)}")
                if (p.lastHandshake > 0) Info("Last handshake", relTime(p.lastHandshake))
                Info("System", osLabel[p.os] ?: p.os.ifBlank { "—" })
                if (p.user.isNotBlank()) Info("Owner", p.user)
                if (p.tags.isNotEmpty()) Info("Tags", p.tags.joinToString(", "))
                if (p.routes.isNotEmpty()) Info("Shares networks", p.routes.joinToString(", "))
            }
        }
        Spacer(Modifier.height(4.dp))
        OutlinedButton(onClick = { copyToClipboard(ctx, "address", p.ipv4) }, modifier = Modifier.fillMaxWidth()) { Text("Copy IP address") }
    }
}

@Composable
private fun CopyRow(label: String, value: String) {
    val s = LocalSteel.current
    val ctx = LocalContext.current
    Row(
        Modifier.fillMaxWidth().clickable(role = Role.Button) { copyToClipboard(ctx, label, value) }.padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(label, style = MaterialTheme.typography.bodySmall, color = s.ink3, modifier = Modifier.size(width = 64.dp, height = 18.dp))
        Text(value, style = MonoStyle, color = s.ink, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text("Copy", style = MaterialTheme.typography.labelMedium, color = s.blued)
    }
}

@Composable
private fun Info(k: String, v: String) {
    val s = LocalSteel.current
    Row(Modifier.fillMaxWidth()) {
        Text(k, style = MaterialTheme.typography.bodySmall, color = s.ink3, modifier = Modifier.weight(0.4f))
        Text(v, style = MaterialTheme.typography.bodyMedium, color = s.ink, modifier = Modifier.weight(0.6f))
    }
}

@Suppress("unused")
@Composable
private fun Spacer2() = Box(Modifier)
