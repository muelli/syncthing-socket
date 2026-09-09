package com.github.muelli.syncthingsocket

import android.graphics.drawable.Icon
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import java.util.concurrent.Executors

/**
 * A quick settings tile that starts the unlock.
 *
 * Tapping it opens the app at the authentication prompt rather than unlocking anything
 * directly. On a locked phone the tap goes through [unlockAndRun] first, so the tile can
 * sit in the shade above the lock screen without becoming a way past it.
 *
 * While the shade is open the tile also reports whether the computer is waiting, which is
 * the question worth answering before the user commits to opening anything.
 */
class UnlockTileService : TileService() {

    // One thread, created when first needed and shut down with the service: the tile is
    // listening only while the shade is open, so this is idle almost always.
    private val checks = Executors.newSingleThreadExecutor()

    override fun onStartListening() {
        super.onStartListening()
        // Show something immediately; the network check replaces it when it lands.
        render(UnlockEntryPoints.Readiness.Unknown, checking = true)
        checks.execute {
            val readiness = UnlockEntryPoints.readiness(this)
            // qsTile is null once the shade closes, so this is a no-op if the user has
            // already moved on rather than a crash.
            render(readiness, checking = false)
        }
    }

    override fun onClick() {
        super.onClick()
        if (isLocked) {
            // Requires the user to get past the lock screen before the activity starts.
            unlockAndRun { launchUnlock() }
        } else {
            launchUnlock()
        }
    }

    private fun launchUnlock() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startActivityAndCollapse(UnlockEntryPoints.unlockPendingIntent(this))
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(UnlockEntryPoints.unlockIntent(this))
        }
    }

    private fun render(readiness: UnlockEntryPoints.Readiness, checking: Boolean) {
        val tile = qsTile ?: return
        tile.label = getString(R.string.tile_label)
        tile.icon = Icon.createWithResource(this, R.drawable.ic_unlock)
        // Active only when the computer is actually waiting, so the tile's own colour
        // carries the same meaning as the dot on the unlock screen.
        tile.state = if (readiness.ready) Tile.STATE_ACTIVE else Tile.STATE_INACTIVE
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            tile.subtitle = getString(
                if (checking) R.string.entry_checking else readiness.labelRes
            )
        }
        tile.updateTile()
    }

    override fun onDestroy() {
        super.onDestroy()
        checks.shutdownNow()
    }
}
