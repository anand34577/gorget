package io.gorget.android.ui

import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Icon
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.graphics.drawable.toBitmap
import io.gorget.android.core.AppPrefs
import io.gorget.android.core.GorgetCore
import io.gorget.android.ui.theme.LocalSteel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private data class AppEntry(val pkg: String, val label: String, val icon: ImageBitmap?)

/**
 * Per-app routing. Changes are applied explicitly (applying restarts the connection for a
 * moment), with progress while it happens and a message when it is done.
 */
@Composable
fun AppsScreen(onBack: () -> Unit) {
    val s = LocalSteel.current
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val prefs = GorgetCore.appPrefs
    var mode by remember { mutableStateOf(prefs.splitMode) }
    var selected by remember { mutableStateOf(prefs.splitApps) }
    var apps by remember { mutableStateOf<List<AppEntry>?>(null) }
    var query by remember { mutableStateOf("") }
    var applying by remember { mutableStateOf(false) }
    var askLeave by remember { mutableStateOf(false) }
    // What is in effect now (the preferences are not Compose state, so track it here).
    var appliedMode by remember { mutableStateOf(prefs.splitMode) }
    var appliedApps by remember { mutableStateOf(prefs.splitApps) }
    val dirty = mode != appliedMode || selected != appliedApps

    LaunchedEffect(Unit) {
        apps = withContext(Dispatchers.IO) {
            val pm = ctx.packageManager
            pm.getInstalledApplications(PackageManager.GET_META_DATA)
                .filter { it.packageName != ctx.packageName && (pm.getLaunchIntentForPackage(it.packageName) != null || it.flags and ApplicationInfo.FLAG_SYSTEM == 0) }
                .map { AppEntry(it.packageName, pm.getApplicationLabel(it).toString(), runCatching { pm.getApplicationIcon(it).toBitmap(64, 64).asImageBitmap() }.getOrNull()) }
                .sortedBy { it.label.lowercase() }
        }
    }

    val apply: (() -> Unit) -> Unit = { then ->
        if (!applying) {
            applying = true
            prefs.splitMode = mode
            prefs.splitApps = selected
            scope.launch {
                runCatching { GorgetCore.refreshTun() }
                    .onSuccess {
                        appliedMode = mode
                        appliedApps = selected
                        val n = selected.size
                        Snack.show(
                            when (mode) {
                                AppPrefs.SplitMode.ALL -> "All apps use Gorget"
                                AppPrefs.SplitMode.ONLY_SELECTED -> "Only $n app${if (n == 1) "" else "s"} use Gorget"
                                AppPrefs.SplitMode.EXCEPT_SELECTED -> "$n app${if (n == 1) "" else "s"} bypass Gorget"
                            },
                        )
                        applying = false
                        then()
                    }
                    .onFailure {
                        Snack.error(it.message ?: "Couldn't apply the change")
                        applying = false
                    }
            }
        }
    }
    val leave: () -> Unit = { if (dirty) askLeave = true else onBack() }
    BackHandler(onBack = leave)

    ScreenScaffold("Choose apps", onBack = leave) {
        Box(Modifier.fillMaxSize()) {
            Column(Modifier.fillMaxSize()) {
                TopProgress(applying || (mode != AppPrefs.SplitMode.ALL && apps == null))
                Column(Modifier.padding(horizontal = 20.dp, vertical = 8.dp)) {
                    val options = listOf(AppPrefs.SplitMode.ALL to "All apps", AppPrefs.SplitMode.ONLY_SELECTED to "Only selected", AppPrefs.SplitMode.EXCEPT_SELECTED to "All except")
                    SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                        options.forEachIndexed { i, (v, label) ->
                            SegmentedButton(selected = mode == v, onClick = { mode = v }, enabled = !applying, shape = SegmentedButtonDefaults.itemShape(i, options.size)) { Text(label) }
                        }
                    }
                    Text(
                        when (mode) {
                            AppPrefs.SplitMode.ALL -> "Every app uses Gorget."
                            AppPrefs.SplitMode.ONLY_SELECTED -> "Only the apps you tick use Gorget; everything else uses your normal connection."
                            AppPrefs.SplitMode.EXCEPT_SELECTED -> "Ticked apps bypass Gorget, for example banking apps that block VPNs."
                        },
                        style = MaterialTheme.typography.bodySmall, color = s.ink3, modifier = Modifier.padding(top = 10.dp),
                    )
                    if (mode == AppPrefs.SplitMode.ONLY_SELECTED && selected.isEmpty()) {
                        Banner("No app is ticked, so nothing will use Gorget.", Tone.WARN, Modifier.padding(top = 10.dp))
                    }
                    if (mode != AppPrefs.SplitMode.ALL) {
                        OutlinedTextField(
                            value = query, onValueChange = { query = it }, singleLine = true,
                            leadingIcon = { Icon(Icons.Default.Search, contentDescription = null) },
                            placeholder = { Text("Search apps") }, modifier = Modifier.fillMaxWidth().padding(top = 10.dp),
                        )
                        Row(Modifier.fillMaxWidth().padding(top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text(
                                if (selected.isEmpty()) "None selected" else "${selected.size} selected",
                                style = MaterialTheme.typography.labelMedium, color = s.ink3, modifier = Modifier.weight(1f),
                            )
                            if (selected.isNotEmpty()) TextButton(onClick = { selected = emptySet() }, enabled = !applying) { Text("Clear") }
                        }
                    }
                }
                if (mode != AppPrefs.SplitMode.ALL) {
                    val list = apps
                    if (list == null) {
                        LoadingBlock("Loading your apps…")
                    } else {
                        val shown = list.filter { query.isBlank() || it.label.contains(query, true) || it.pkg.contains(query, true) }
                            .let { l -> if (query.isBlank()) l.sortedByDescending { it.pkg in appliedApps } else l }
                        if (shown.isEmpty()) {
                            Text("No apps match this search.", modifier = Modifier.padding(20.dp), color = s.ink3)
                        } else {
                            LazyColumn(contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 4.dp, bottom = if (dirty) 96.dp else 24.dp)) {
                                items(shown, key = { it.pkg }) { app ->
                                    val checked = app.pkg in selected
                                    Row(
                                        Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable(enabled = !applying, role = Role.Checkbox) {
                                            selected = if (checked) selected - app.pkg else selected + app.pkg
                                        }.padding(vertical = 8.dp, horizontal = 4.dp),
                                        verticalAlignment = Alignment.CenterVertically,
                                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                                    ) {
                                        if (app.icon != null) Image(app.icon, contentDescription = null, modifier = Modifier.size(36.dp)) else Spacer(Modifier.size(36.dp))
                                        Column(Modifier.weight(1f)) {
                                            Text(app.label, style = MaterialTheme.typography.titleSmall, color = s.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                            Text(app.pkg, style = MaterialTheme.typography.bodySmall, color = s.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                        }
                                        Checkbox(checked = checked, onCheckedChange = null)
                                    }
                                }
                            }
                        }
                    }
                } else {
                    Text(
                        "Switch to “Only selected” or “All except” to choose apps.",
                        modifier = Modifier.padding(horizontal = 24.dp, vertical = 12.dp), style = MaterialTheme.typography.bodyMedium, color = s.ink3,
                    )
                }
            }
            if (dirty) {
                Panel(Modifier.align(Alignment.BottomCenter).fillMaxWidth().padding(16.dp)) {
                    Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        Text(
                            if (applying) "Applying… your connection restarts for a moment." else "Unsaved changes",
                            style = MaterialTheme.typography.bodyMedium, color = s.ink2, modifier = Modifier.weight(1f),
                        )
                        BusyButton("Apply", busy = applying, onClick = { apply { } }, busyText = "Applying", modifier = Modifier.height(46.dp))
                    }
                }
            }
        }
    }

    if (askLeave) {
        AlertDialog(
            onDismissRequest = { askLeave = false },
            title = { Text("Apply your changes?") },
            text = { Text("You changed which apps use Gorget. Applying restarts the connection for a moment.") },
            confirmButton = { TextButton(onClick = { askLeave = false; apply { onBack() } }) { Text("Apply") } },
            dismissButton = {
                Row {
                    TextButton(onClick = { askLeave = false }) { Text("Keep editing") }
                    TextButton(onClick = { askLeave = false; onBack() }) { Text("Discard", color = s.oxide) }
                }
            },
        )
    }
}
