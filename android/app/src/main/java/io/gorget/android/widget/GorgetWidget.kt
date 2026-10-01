package io.gorget.android.widget

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.widget.RemoteViews
import io.gorget.android.R
import io.gorget.android.core.GorgetCore
import io.gorget.android.core.Status
import io.gorget.android.ui.MainActivity

/** Home-screen widget: shows the connection state; tap toggles it. */
class GorgetWidget : AppWidgetProvider() {

    companion object {
        private const val ACTION_TOGGLE = "io.gorget.android.WIDGET_TOGGLE"

        fun update(ctx: Context, s: Status) {
            val mgr = AppWidgetManager.getInstance(ctx)
            val ids = mgr.getAppWidgetIds(ComponentName(ctx, GorgetWidget::class.java))
            if (ids.isEmpty()) return
            val views = RemoteViews(ctx.packageName, R.layout.widget)
            val (title, subtitle) = when (s.state) {
                Status.STATE_RUNNING -> ctx.getString(R.string.widget_connected) to (s.exitNode?.let { "Exit node: ${it.name}" } ?: s.self?.ipv4 ?: s.networkName)
                Status.STATE_CONNECTING, Status.STATE_PENDING -> ctx.getString(R.string.widget_connecting) to s.networkName
                else -> ctx.getString(R.string.widget_disconnected) to ctx.getString(R.string.widget_tap_connect)
            }
            views.setTextViewText(R.id.widget_title, title)
            views.setTextViewText(R.id.widget_subtitle, subtitle)
            views.setImageViewResource(R.id.widget_icon, if (s.isRunning) R.drawable.ic_logo_on else R.drawable.ic_logo_off)
            val toggle = Intent(ctx, GorgetWidget::class.java).setAction(ACTION_TOGGLE)
            views.setOnClickPendingIntent(
                R.id.widget_root,
                PendingIntent.getBroadcast(ctx, 0, toggle, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT),
            )
            mgr.updateAppWidget(ids, views)
        }
    }

    override fun onUpdate(context: Context, appWidgetManager: AppWidgetManager, appWidgetIds: IntArray) {
        update(context, GorgetCore.status.value)
    }

    override fun onReceive(context: Context, intent: Intent) {
        super.onReceive(context, intent)
        if (intent.action != ACTION_TOGGLE) return
        val s = GorgetCore.status.value
        when {
            s.isActive -> GorgetCore.stopTunnel(context)
            GorgetCore.isRegistered() && VpnService.prepare(context) == null -> GorgetCore.startTunnel(context)
            else -> context.startActivity(
                Intent(context, MainActivity::class.java).setAction(MainActivity.ACTION_CONNECT).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
        }
    }
}
