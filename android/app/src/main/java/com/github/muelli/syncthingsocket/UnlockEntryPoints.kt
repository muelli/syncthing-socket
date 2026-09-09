package com.github.muelli.syncthingsocket

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import mobile.Mobile

/**
 * The ways into the unlock besides opening the app: a launcher shortcut, a home screen
 * widget and a quick settings tile.
 *
 * All three do exactly one thing, which is start MainActivity with [ACTION_UNLOCK]. None
 * of them unlocks anything by itself, and that is deliberate: the pairing is encrypted
 * under a key that requires the screen lock or a strong biometric, so every path arrives
 * at the same authentication prompt. A shortcut that could skip it would be a way of
 * turning a stolen, locked phone into a working key.
 */
object UnlockEntryPoints {

    /** Sent to MainActivity to mean "go straight to unlocking". */
    const val ACTION_UNLOCK = "com.github.muelli.syncthingsocket.action.UNLOCK"

    fun unlockIntent(context: Context): Intent =
        Intent(context, MainActivity::class.java).apply {
            action = ACTION_UNLOCK
            // Started from a widget or a tile, so there is no task to join.
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        }

    fun unlockPendingIntent(context: Context): PendingIntent =
        PendingIntent.getActivity(
            context,
            0,
            unlockIntent(context),
            // Immutable because nothing should be able to fill in extras on our behalf,
            // and required outright from API 31.
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

    /**
     * What the computer looks like it is doing, as one short line plus whether it is
     * ready. Used by the quick settings tile, which can ask honestly because it is only
     * asked while the shade is open. The widget deliberately shows no status at all.
     *
     * Does network I/O, so never call it on the main thread.
     */
    fun readiness(context: Context): Readiness {
        val deviceId = pairedDeviceId(context) ?: return Readiness.NotPaired
        val status = runCatching { Mobile.checkServerStatus(deviceId) }.getOrNull()
            ?: return Readiness.Unknown
        return when (status.state) {
            "WAITING" -> Readiness.Waiting
            "STALE" -> Readiness.Stale
            "ABSENT" -> Readiness.Absent
            else -> Readiness.Unknown
        }
    }

    /** The readiness states, as the tile needs them. */
    enum class Readiness(val labelRes: Int, val ready: Boolean) {
        Waiting(R.string.entry_waiting, true),
        Stale(R.string.entry_stale, false),
        Absent(R.string.entry_absent, false),
        Unknown(R.string.entry_unknown, false),
        NotPaired(R.string.entry_not_paired, false)
    }
}
