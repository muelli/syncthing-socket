package com.github.muelli.syncthingsocket

import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.Context
import android.widget.RemoteViews

/**
 * A home screen widget that is one button: tap it to start the unlock.
 *
 * Deliberately passive. It shows nothing that changes, touches no network, and asks
 * Android for no update cycle, so it costs nothing to have on a home screen. An earlier
 * version showed whether the computer was waiting, which sounds useful and is not
 * achievable honestly: Android will not run a widget update more often than every 30
 * minutes, and polling in the background at the rate that status actually changes, once a
 * minute, would spend far more battery than the answer is worth. A status that stale is
 * worse than none, because it looks current.
 *
 * The quick settings tile does report that status, because it can do so honestly: it is
 * asked only while the shade is open, which is exactly when someone is looking.
 */
class UnlockWidgetProvider : AppWidgetProvider() {

    override fun onUpdate(
        context: Context,
        appWidgetManager: AppWidgetManager,
        appWidgetIds: IntArray
    ) {
        val views = RemoteViews(context.packageName, R.layout.widget_unlock).apply {
            // Both the button and the surrounding box, so a tap anywhere works rather
            // than only on the pill.
            val unlock = UnlockEntryPoints.unlockPendingIntent(context)
            setOnClickPendingIntent(R.id.widget_unlock, unlock)
            setOnClickPendingIntent(R.id.widget_root, unlock)
        }
        for (id in appWidgetIds) {
            appWidgetManager.updateAppWidget(id, views)
        }
    }
}
