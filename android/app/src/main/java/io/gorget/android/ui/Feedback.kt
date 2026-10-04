package io.gorget.android.ui

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import io.gorget.android.ui.theme.LocalSteel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.asSharedFlow

/** A message for the snackbar at the bottom of the screen. */
data class SnackMsg(val text: String, val error: Boolean = false, val action: String? = null, val onAction: (() -> Unit)? = null)

/**
 * App-wide feedback: say what happened after something that took a moment.
 * Call from anywhere; [SnackHost] (placed once in MainActivity) shows it.
 */
object Snack {
    private val flow = MutableSharedFlow<SnackMsg>(extraBufferCapacity = 8)
    val messages = flow.asSharedFlow()

    fun show(text: String, action: String? = null, onAction: (() -> Unit)? = null) {
        flow.tryEmit(SnackMsg(text, false, action, onAction))
    }

    fun error(text: String, action: String? = null, onAction: (() -> Unit)? = null) {
        flow.tryEmit(SnackMsg(text, true, action, onAction))
    }
}

@Composable
fun SnackHost(modifier: Modifier = Modifier) {
    val s = LocalSteel.current
    val host = remember { SnackbarHostState() }
    var current by remember { mutableStateOf<SnackMsg?>(null) }
    LaunchedEffect(Unit) {
        Snack.messages.collect { m ->
            current = m
            host.currentSnackbarData?.dismiss()
            // Each message in its own coroutine: showSnackbar suspends until the bar goes away.
            launch {
                val r = host.showSnackbar(m.text, m.action, withDismissAction = m.action == null && m.error, duration = if (m.error) SnackbarDuration.Long else SnackbarDuration.Short)
                if (r == SnackbarResult.ActionPerformed) m.onAction?.invoke()
            }
        }
    }
    SnackbarHost(host, modifier.navigationBarsPadding()) { data ->
        val err = current?.error == true
        Snackbar(
            modifier = Modifier.padding(12.dp),
            shape = RoundedCornerShape(12.dp),
            containerColor = if (err) s.oxide else s.ink,
            contentColor = if (err) Color.White else s.bg,
            action = data.visuals.actionLabel?.let { label ->
                { TextButton(onClick = { data.performAction() }) { Text(label, color = if (err) Color.White else s.bg) } }
            },
            dismissAction = if (data.visuals.withDismissAction) {
                { TextButton(onClick = { data.dismiss() }) { Text("Dismiss", color = Color.White) } }
            } else null,
        ) { Text(data.visuals.message) }
    }
}

/** A button that shows a spinner (and ignores taps) while its action runs. */
@Composable
fun BusyButton(
    text: String,
    busy: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    busyText: String? = null,
    enabled: Boolean = true,
    colors: androidx.compose.material3.ButtonColors = ButtonDefaults.buttonColors(),
) {
    Button(onClick = { if (!busy) onClick() }, enabled = enabled, colors = colors, modifier = modifier, shape = RoundedCornerShape(27.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
            if (busy) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = LocalSteel.current.ink3)
            Text(if (busy && busyText != null) busyText else text, style = androidx.compose.material3.MaterialTheme.typography.titleMedium)
        }
    }
}

/** A thin indeterminate bar for work in the background (applying settings, loading). */
@Composable
fun TopProgress(visible: Boolean, modifier: Modifier = Modifier) {
    if (visible) {
        LinearProgressIndicator(modifier.fillMaxWidth().height(3.dp), color = LocalSteel.current.blued, trackColor = LocalSteel.current.line)
    } else {
        androidx.compose.foundation.layout.Spacer(modifier.height(3.dp))
    }
}

/** A centred spinner with a line of text, for screens that are loading. */
@Composable
fun LoadingBlock(text: String, modifier: Modifier = Modifier) {
    androidx.compose.foundation.layout.Column(
        modifier.fillMaxWidth().padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        CircularProgressIndicator(Modifier.size(28.dp), strokeWidth = 3.dp)
        Text(text, style = androidx.compose.material3.MaterialTheme.typography.bodyMedium, color = LocalSteel.current.ink3)
    }
}

/** Light status-bar icons while a screen with a dark header is shown (restored afterwards). */
@Composable
fun LightStatusIcons() {
    val view = androidx.compose.ui.platform.LocalView.current
    androidx.compose.runtime.DisposableEffect(Unit) {
        val window = (view.context as? android.app.Activity)?.window
        val ctl = window?.let { androidx.core.view.WindowCompat.getInsetsController(it, view) }
        val prev = ctl?.isAppearanceLightStatusBars
        ctl?.isAppearanceLightStatusBars = false
        onDispose { if (ctl != null && prev != null) ctl.isAppearanceLightStatusBars = prev }
    }
}
