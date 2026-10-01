package io.gorget.android.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.widget.Toast
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.animateColorAsState
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.gorget.android.ui.theme.LocalSteel
import io.gorget.android.ui.theme.TemperGradient

enum class LameState { OFF, CONNECTING, ON, WAITING }

/**
 * Signature control: the gorget's three articulated lames. Off = bare steel,
 * connecting = plates heat one after another, on = verdigris, waiting = straw.
 */
@Composable
fun Lames(state: LameState, modifier: Modifier = Modifier, size: Dp = 168.dp) {
    val steel = LocalSteel.current
    val transition = rememberInfiniteTransition(label = "lames")
    val phase by transition.animateFloat(
        initialValue = 0f, targetValue = 3f,
        animationSpec = infiniteRepeatable(tween(1500, easing = LinearEasing), RepeatMode.Restart), label = "phase",
    )
    val base by animateColorAsState(
        when (state) {
            LameState.ON -> steel.verdigris
            LameState.WAITING -> steel.straw
            else -> steel.lineStrong
        }, label = "c",
    )
    Canvas(modifier.size(size)) {
        val w = this.size.width
        val stroke = w * 0.085f
        // Three arcs, outer to inner, like the logo (viewBox 32: radii 10, 7.5, 4.5).
        val arcs = listOf(Triple(10f, 11f, 1f), Triple(7.5f, 16.5f, 0.8f), Triple(4.5f, 21.5f, 0.6f))
        val unit = w / 32f
        arcs.forEachIndexed { i, (r, cy, alpha) ->
            val radius = r * unit
            val center = Offset(16f * unit, cy * unit)
            val heated = state == LameState.CONNECTING && phase.toInt() == i
            val brush = if (heated) {
                Brush.horizontalGradient(TemperGradient, startX = center.x - radius, endX = center.x + radius)
            } else {
                Brush.linearGradient(listOf(base, base))
            }
            drawArc(
                brush = brush,
                startAngle = 0f, sweepAngle = 180f, useCenter = false,
                topLeft = Offset(center.x - radius, center.y - radius), size = Size(radius * 2, radius * 2),
                alpha = if (state == LameState.CONNECTING && !heated) 0.45f else alpha,
                style = Stroke(width = stroke, cap = StrokeCap.Round),
            )
        }
    }
}

@Composable
fun Panel(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    val s = LocalSteel.current
    Box(
        modifier
            .clip(RoundedCornerShape(14.dp))
            .background(s.surface)
            .border(1.dp, s.line, RoundedCornerShape(14.dp)),
    ) { content() }
}

@Composable
fun SectionLabel(text: String, modifier: Modifier = Modifier) {
    Text(
        text.uppercase(), style = MaterialTheme.typography.labelSmall, color = LocalSteel.current.ink3,
        modifier = modifier.padding(start = 4.dp, bottom = 8.dp, top = 20.dp),
    )
}

@Composable
fun Chip(text: String, tone: Tone = Tone.NEUTRAL) {
    val s = LocalSteel.current
    val (bg, fg) = when (tone) {
        Tone.OK -> s.verdigrisSoft to s.verdigris
        Tone.WARN -> s.strawSoft to s.straw
        Tone.DANGER -> s.oxideSoft to s.oxide
        Tone.BLUED -> s.bluedSoft to s.blued
        Tone.NEUTRAL -> s.sunken to s.ink2
    }
    Text(
        text, style = MaterialTheme.typography.labelMedium, color = fg,
        modifier = Modifier.clip(RoundedCornerShape(6.dp)).background(bg).padding(horizontal = 7.dp, vertical = 2.dp),
    )
}

enum class Tone { NEUTRAL, OK, WARN, DANGER, BLUED }

@Composable
fun Dot(on: Boolean, warn: Boolean = false) {
    val s = LocalSteel.current
    Box(Modifier.size(9.dp).clip(CircleShape).background(if (warn) s.straw else if (on) s.verdigris else s.lineStrong))
}

@Composable
fun Banner(text: String, tone: Tone, modifier: Modifier = Modifier) {
    val s = LocalSteel.current
    val (bg, fg) = when (tone) {
        Tone.DANGER -> s.oxideSoft to s.oxide
        Tone.WARN -> s.strawSoft to s.ink
        else -> s.bluedSoft to s.ink
    }
    Text(
        text, style = MaterialTheme.typography.bodyMedium, color = fg,
        modifier = modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(bg).padding(horizontal = 14.dp, vertical = 10.dp),
    )
}

@Composable
fun ToggleRow(title: String, subtitle: String?, checked: Boolean, enabled: Boolean = true, onChange: (Boolean) -> Unit) {
    val s = LocalSteel.current
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(enabled = enabled, role = Role.Switch) { onChange(!checked) }
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleSmall, color = if (enabled) s.ink else s.ink3)
            if (subtitle != null) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = s.ink3)
        }
        Spacer(Modifier.width(12.dp))
        Switch(
            checked = checked, onCheckedChange = null, enabled = enabled,
            colors = SwitchDefaults.colors(checkedTrackColor = s.blued, uncheckedTrackColor = s.sunken, uncheckedBorderColor = s.lineStrong),
        )
    }
}

@Composable
fun NavRow(title: String, subtitle: String? = null, trailing: String? = null, danger: Boolean = false, onClick: () -> Unit) {
    val s = LocalSteel.current
    Row(
        Modifier.fillMaxWidth().clickable(role = Role.Button, onClick = onClick).padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleSmall, color = if (danger) s.oxide else s.ink)
            if (subtitle != null) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = s.ink3)
        }
        if (trailing != null) Text(trailing, style = MaterialTheme.typography.bodySmall, color = s.ink3)
        Text("›", style = MaterialTheme.typography.titleLarge, color = s.ink3)
    }
}

@Composable
fun Divider() {
    Box(Modifier.fillMaxWidth().padding(start = 16.dp).size(height = 1.dp, width = 0.dp).background(LocalSteel.current.line))
}

fun copyToClipboard(ctx: Context, label: String, text: String) {
    val cm = ctx.getSystemService(ClipboardManager::class.java)
    cm.setPrimaryClip(ClipData.newPlainText(label, text))
    Toast.makeText(ctx, "Copied $label", Toast.LENGTH_SHORT).show()
}

fun Modifier.describe(text: String) = this.semantics { contentDescription = text }

fun formatBytes(n: Long): String {
    if (n <= 0) return "0 B"
    val units = listOf("B", "KB", "MB", "GB", "TB")
    var v = n.toDouble()
    var i = 0
    while (v >= 1024 && i < units.size - 1) {
        v /= 1024; i++
    }
    return if (i == 0) "$n B" else String.format("%.1f %s", v, units[i])
}

fun relTime(unix: Long): String {
    if (unix <= 0) return "never"
    val d = System.currentTimeMillis() / 1000 - unix
    return when {
        d < 60 -> "just now"
        d < 3600 -> "${d / 60} min ago"
        d < 86400 -> "${d / 3600} h ago"
        else -> "${d / 86400} d ago"
    }
}

val osLabel = mapOf("linux" to "Linux", "windows" to "Windows", "darwin" to "macOS", "android" to "Android", "wireguard" to "WireGuard app")

@Suppress("unused")
private val transparent = Color.Transparent
