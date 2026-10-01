package io.gorget.android.vpn

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.VpnService
import io.gorget.android.core.GorgetCore

/** Reconnects after reboot or app update if the user left the VPN on. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED && intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return
        // Only possible when VPN permission was granted before; otherwise the user must open the app.
        if (GorgetCore.wantRunning() && VpnService.prepare(context) == null) {
            GorgetCore.startTunnel(context)
        }
    }
}
