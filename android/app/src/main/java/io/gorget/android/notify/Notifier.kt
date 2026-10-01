package io.gorget.android.notify

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.app.Notification
import io.gorget.android.R
import io.gorget.android.core.GorgetCore
import io.gorget.android.core.Status
import io.gorget.android.ui.MainActivity

/** Local notifications only; nothing is sent anywhere. */
object Notifier {
    private const val CHANNEL = "status"
    private const val ID = 1

    fun createChannel(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        val ch = NotificationChannel(CHANNEL, ctx.getString(R.string.notif_channel_status), NotificationManager.IMPORTANCE_DEFAULT)
        ch.description = ctx.getString(R.string.notif_channel_status_desc)
        nm.createNotificationChannel(ch)
    }

    fun onTransition(ctx: Context, prev: Status, next: Status) {
        if (!GorgetCore.appPrefs.notifications) return
        val msg: Pair<String, String>? = when {
            next.state == Status.STATE_PENDING && prev.state != Status.STATE_PENDING ->
                "Waiting for approval" to "An administrator needs to approve this device before it can connect."
            next.state == Status.STATE_BLOCKED && prev.state != Status.STATE_BLOCKED ->
                "Blocked by security rules" to (next.blockedReasons.firstOrNull() ?: "This device doesn't meet your organisation's security rules.")
            next.state == Status.STATE_EXPIRED ->
                "Sign in again" to (next.error ?: "This device's sign-in expired.")
            next.state == Status.STATE_DISABLED ->
                "Device disabled" to (next.error ?: "An administrator disabled this device.")
            next.state == Status.STATE_NEEDS_LOGIN && prev.isActive ->
                "Signed out" to (next.error ?: "Sign in to reconnect.")
            next.isRunning && prev.state == Status.STATE_PENDING ->
                "Device approved" to "You're connected to ${next.networkName.ifBlank { "your network" }}."
            else -> null
        }
        if (msg == null) {
            if (next.isRunning) cancel(ctx)
            return
        }
        if (Build.VERSION.SDK_INT >= 33 &&
            ctx.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) return
        val pi = PendingIntent.getActivity(ctx, 0, Intent(ctx, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val n = Notification.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_tile)
            .setContentTitle(msg.first)
            .setContentText(msg.second)
            .setStyle(Notification.BigTextStyle().bigText(msg.second))
            .setContentIntent(pi)
            .setAutoCancel(true)
            .build()
        ctx.getSystemService(NotificationManager::class.java).notify(ID, n)
    }

    private fun cancel(ctx: Context) {
        ctx.getSystemService(NotificationManager::class.java).cancel(ID)
    }
}
