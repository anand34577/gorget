package io.gorget.android.vpn

import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import io.gorget.android.core.GorgetCore
import io.gorget.android.core.Status
import io.gorget.android.ui.MainActivity
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/** Quick-settings tile: one tap to connect or disconnect. */
class GorgetTileService : TileService() {
    private var job: Job? = null

    override fun onStartListening() {
        job = GorgetCore.scope.launch {
            GorgetCore.status.collect { render(it) }
        }
    }

    override fun onStopListening() {
        job?.cancel()
        job = null
    }

    private fun render(s: Status) {
        val tile = qsTile ?: return
        tile.state = when {
            s.isActive -> Tile.STATE_ACTIVE
            s.state == Status.STATE_NO_SERVER -> Tile.STATE_UNAVAILABLE
            else -> Tile.STATE_INACTIVE
        }
        if (Build.VERSION.SDK_INT >= 29) {
            tile.subtitle = when (s.state) {
                Status.STATE_RUNNING -> s.exitNode?.let { "via ${it.name}" } ?: s.networkName.ifBlank { "Connected" }
                Status.STATE_CONNECTING -> "Connecting…"
                Status.STATE_PENDING -> "Awaiting approval"
                Status.STATE_BLOCKED -> "Blocked"
                Status.STATE_NEEDS_LOGIN, Status.STATE_EXPIRED -> "Sign in needed"
                else -> "Off"
            }
        }
        tile.updateTile()
    }

    @SuppressLint("StartActivityAndCollapseDeprecated")
    override fun onClick() {
        val s = GorgetCore.status.value
        if (s.isActive) {
            GorgetCore.stopTunnel(this)
            return
        }
        val needsUi = !GorgetCore.isRegistered() || VpnService.prepare(this) != null
        if (needsUi) {
            val intent = Intent(this, MainActivity::class.java)
                .setAction(MainActivity.ACTION_CONNECT)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            if (Build.VERSION.SDK_INT >= 34) {
                startActivityAndCollapse(PendingIntent.getActivity(this, 1, intent, PendingIntent.FLAG_IMMUTABLE))
            } else {
                @Suppress("DEPRECATION")
                startActivityAndCollapse(intent)
            }
            return
        }
        GorgetCore.startTunnel(this)
    }
}
