package com.github.muelli.syncthingsocket

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.widget.RemoteViews
import java.util.concurrent.Executors

/**
 * A home screen widget: one tap to unlock, and a line saying whether the computer is
 * waiting.
 *
 * The status is refreshed when the widget is added, when its row is tapped, and on the
 * system's own update cycle. That cycle is the slowest part and it is not ours to choose:
 * Android will not run a widget update more often than every 30 minutes, and polling the
 * network in the background at the rate this status actually changes, once a minute, would
 * cost far more battery than the feature is worth. So the status line says when it was
 * last checked rather than pretending to be live, and tapping it checks again.
 */
class UnlockWidgetProvider : AppWidgetProvider() {

    override fun onUpdate(
        context: Context,
        appWidgetManager: AppWidgetManager,
        appWidgetIds: IntArray
    ) {
        // Draw immediately from what we can know without touching the network, then
        // replace it once the check comes back.
        for (id in appWidgetIds) {
            appWidgetManager.updateAppWidget(id, buildViews(context, null))
        }
        refreshAsync(context)
    }

    override fun onReceive(context: Context, intent: Intent) {
        super.onReceive(context, intent)
        if (intent.action == UnlockEntryPoints.ACTION_REFRESH_WIDGET) {
            // Show that something is happening, so a tap is never silent.
            renderAll(context, buildViews(context, null))
            refreshAsync(context)
        }
    }

    /**
     * Runs the readiness check off the main thread and redraws.
     *
     * A broadcast receiver is dead the moment onReceive returns unless it says otherwise,
     * so goAsync holds it alive for the network call.
     */
    private fun refreshAsync(context: Context) {
        val pending = goAsync()
        val appContext = context.applicationContext
        Executors.newSingleThreadExecutor().execute {
            try {
                val readiness = UnlockEntryPoints.readiness(appContext)
                renderAll(appContext, buildViews(appContext, readiness))
            } finally {
                pending.finish()
            }
        }
    }

    private fun renderAll(context: Context, views: RemoteViews) {
        val manager = AppWidgetManager.getInstance(context)
        val ids = manager.getAppWidgetIds(
            ComponentName(context, UnlockWidgetProvider::class.java)
        )
        if (ids.isNotEmpty()) {
            manager.updateAppWidget(ids, views)
        }
    }

    /** A null readiness means "checking", which is also the state before the first check. */
    private fun buildViews(
        context: Context,
        readiness: UnlockEntryPoints.Readiness?
    ): RemoteViews {
        val views = RemoteViews(context.packageName, R.layout.widget_unlock)

        views.setTextViewText(
            R.id.widget_status,
            context.getString(readiness?.labelRes ?: R.string.entry_checking)
        )
        views.setImageViewResource(R.id.widget_dot, dotFor(readiness))

        // The button unlocks; the status row re-checks. Two targets, so a glance at the
        // status never risks starting an unlock by accident.
        views.setOnClickPendingIntent(
            R.id.widget_unlock,
            UnlockEntryPoints.unlockPendingIntent(context)
        )
        views.setOnClickPendingIntent(R.id.widget_status_row, refreshPendingIntent(context))
        return views
    }

    private fun dotFor(readiness: UnlockEntryPoints.Readiness?): Int = when (readiness) {
        UnlockEntryPoints.Readiness.Waiting -> R.drawable.dot_waiting
        UnlockEntryPoints.Readiness.Stale -> R.drawable.dot_stale
        UnlockEntryPoints.Readiness.Absent -> R.drawable.dot_absent
        else -> R.drawable.dot_unknown
    }

    private fun refreshPendingIntent(context: Context): PendingIntent {
        val intent = Intent(context, UnlockWidgetProvider::class.java).apply {
            action = UnlockEntryPoints.ACTION_REFRESH_WIDGET
        }
        return PendingIntent.getBroadcast(
            context,
            0,
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
    }
}
