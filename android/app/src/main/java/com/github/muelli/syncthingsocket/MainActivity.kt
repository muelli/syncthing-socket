package com.github.muelli.syncthingsocket

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_STRONG
import androidx.biometric.BiometricManager.Authenticators.DEVICE_CREDENTIAL
import androidx.biometric.BiometricPrompt
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.focusable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.*
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import com.google.zxing.BinaryBitmap
import com.google.zxing.MultiFormatReader
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import kotlinx.coroutines.delay
import mobile.Mobile
import org.json.JSONObject
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

/** What the user is doing. The app is only ever in one of these. */
private enum class Phase { Enroll, ManualEnroll, Scanning, Unlock, KeyLost, NoScreenLock }

/** What the unlock screen is showing. */
private sealed class UnlockUi {
    object Idle : UnlockUi()
    object Working : UnlockUi()
    object Success : UnlockUi()
    data class Failed(val category: String, val detail: String?) : UnlockUi()
}

private data class Pairing(val passphrase: String, val seed: String, val deviceId: String)

/**
 * Credential storage.
 *
 * The master key requires user authentication, so the pairing cannot be decrypted without
 * the screen lock or a strong biometric. Before this, the biometric prompt was only a
 * screen in front of data that anything running as the app could already read.
 *
 * Whether a pairing exists is kept in ordinary preferences. Answering "is this phone set
 * up?" must not require authenticating, or the app would have to prompt before it could
 * even decide which screen to show. That flag reveals only that the app was paired, never
 * with what.
 */
private class CredentialStore(private val context: Context) {

    fun isEnrolled(): Boolean =
        context.getSharedPreferences(FLAG_PREFS, Context.MODE_PRIVATE)
            .getBoolean(KEY_ENROLLED, false)

    /** Requires a recent authentication; see AUTH_VALIDITY_SECONDS. */
    fun load(): Pairing? {
        val prefs = encrypted()
        val passphrase = prefs.getString(KEY_PASSPHRASE, null) ?: return null
        val seed = prefs.getString(KEY_SEED, null) ?: return null
        val deviceId = prefs.getString(KEY_DEVICE_ID, null) ?: return null
        return Pairing(passphrase, seed, deviceId)
    }

    fun save(pairing: Pairing) {
        encrypted().edit()
            .putString(KEY_PASSPHRASE, pairing.passphrase)
            .putString(KEY_SEED, pairing.seed)
            .putString(KEY_DEVICE_ID, pairing.deviceId)
            .commit()
        context.getSharedPreferences(FLAG_PREFS, Context.MODE_PRIVATE)
            .edit().putBoolean(KEY_ENROLLED, true).commit()
    }

    /**
     * Discards a pairing that has become permanently undecryptable, which happens when the
     * phone's biometrics or screen lock change. This is not a "delete my credentials"
     * feature: the ciphertext left behind is unreadable to everyone, including this app, so
     * keeping it would only strand the user on a screen with no way forward.
     */
    fun discardUnreadable() {
        context.deleteSharedPreferences(SECRET_PREFS)
        context.getSharedPreferences(FLAG_PREFS, Context.MODE_PRIVATE)
            .edit().putBoolean(KEY_ENROLLED, false).commit()
    }

    private fun encrypted() = EncryptedSharedPreferences.create(
        context,
        SECRET_PREFS,
        MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .setUserAuthenticationRequired(true, AUTH_VALIDITY_SECONDS)
            .build(),
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
    )

    companion object {
        private const val SECRET_PREFS = "secret_shared_prefs"
        private const val FLAG_PREFS = "enrolment_state"
        private const val KEY_ENROLLED = "enrolled"
        private const val KEY_PASSPHRASE = "passphrase"
        private const val KEY_SEED = "phone_seed"
        private const val KEY_DEVICE_ID = "laptop_id"

        /**
         * How long an authentication stays usable. Long enough to decrypt and run one
         * unlock, short enough that leaving the phone unattended does not leave the key
         * usable.
         */
        const val AUTH_VALIDITY_SECONDS = 30
    }
}

class MainActivity : FragmentActivity() {
    private lateinit var cameraExecutor: ExecutorService
    private lateinit var store: CredentialStore

    private val phase = mutableStateOf(Phase.Enroll)
    private val unlockUi = mutableStateOf<UnlockUi>(UnlockUi.Idle)
    private val notice = mutableStateOf<String?>(null)

    private val requestCamera = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        if (granted) {
            phase.value = Phase.Scanning
        } else {
            notice.value = getString(R.string.camera_denied)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        cameraExecutor = Executors.newSingleThreadExecutor()
        store = CredentialStore(this)
        phase.value = startingPhase()

        setContent {
            MaterialTheme(colorScheme = darkColorScheme()) {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background
                ) {
                    when (phase.value) {
                        Phase.Scanning -> QRScannerScreen(
                            onQRCodeScanned = { onScanned(it) },
                            onCancel = { phase.value = Phase.Enroll }
                        )

                        Phase.ManualEnroll -> ManualEnrollScreen(
                            notice = notice.value,
                            onSubmit = { pass, seed, id -> onManualEntry(pass, seed, id) },
                            onBack = { notice.value = null; phase.value = Phase.Enroll }
                        )

                        Phase.Enroll -> EnrollScreen(
                            notice = notice.value,
                            onScanClick = { startScan() },
                            onManualClick = { notice.value = null; phase.value = Phase.ManualEnroll }
                        )

                        Phase.Unlock -> UnlockScreen(
                            state = unlockUi.value,
                            notice = notice.value,
                            onUnlockClick = { notice.value = null; startUnlock() },
                            onFinished = { finish() }
                        )

                        Phase.KeyLost -> MessageScreen(
                            message = stringResource(R.string.key_invalidated),
                            actionLabel = stringResource(R.string.key_invalidated_action),
                            onAction = {
                                store.discardUnreadable()
                                notice.value = null
                                phase.value = Phase.Enroll
                            }
                        )

                        Phase.NoScreenLock -> MessageScreen(
                            message = stringResource(R.string.auth_unavailable),
                            actionLabel = null,
                            onAction = {}
                        )
                    }
                }
            }
        }
    }

    /**
     * The app has exactly two states to choose between, and it must not offer to unlock
     * when there is nothing to unlock with.
     */
    private fun startingPhase(): Phase = when {
        !canAuthenticate() -> Phase.NoScreenLock
        store.isEnrolled() -> Phase.Unlock
        else -> Phase.Enroll
    }

    /**
     * The key demands authentication, so a phone with no screen lock cannot store a
     * pairing at all. Say so rather than failing later with a keystore exception.
     */
    private fun canAuthenticate(): Boolean =
        BiometricManager.from(this)
            .canAuthenticate(BIOMETRIC_STRONG or DEVICE_CREDENTIAL) ==
            BiometricManager.BIOMETRIC_SUCCESS

    private fun startScan() {
        notice.value = null
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) ==
            PackageManager.PERMISSION_GRANTED
        ) {
            phase.value = Phase.Scanning
        } else {
            requestCamera.launch(Manifest.permission.CAMERA)
        }
    }

    private fun onScanned(payload: String) {
        phase.value = Phase.Enroll
        val pairing = parsePairing(payload)
        if (pairing == null) {
            notice.value = getString(R.string.enroll_bad_qr)
            return
        }
        storePairing(pairing)
    }

    private fun onManualEntry(passphrase: String, seed: String, deviceId: String) {
        val cleanId = deviceId.trim().uppercase()
        when {
            passphrase.isBlank() || seed.isBlank() || cleanId.isBlank() ->
                notice.value = getString(R.string.enroll_incomplete)

            !looksLikeDeviceId(cleanId) ->
                notice.value = getString(R.string.enroll_bad_device_id)

            else -> storePairing(Pairing(passphrase, seed.trim(), cleanId))
        }
    }

    /** Writing needs a recent authentication too, because the key demands one. */
    private fun storePairing(pairing: Pairing) {
        authenticate(getString(R.string.enroll_auth_reason)) {
            runCatching { store.save(pairing) }
                .onSuccess {
                    notice.value = getString(R.string.enroll_saved)
                    unlockUi.value = UnlockUi.Idle
                    phase.value = Phase.Unlock
                }
                .onFailure { phase.value = Phase.KeyLost }
        }
    }

    private fun startUnlock() {
        unlockUi.value = UnlockUi.Idle
        authenticate(getString(R.string.unlock_auth_reason)) {
            val pairing = runCatching { store.load() }
                .getOrElse {
                    // The key is gone or unusable, which on this key scheme means the
                    // phone's biometrics or screen lock changed. Nothing can decrypt the
                    // pairing any more, so pairing again is the only way forward.
                    phase.value = Phase.KeyLost
                    return@authenticate
                }
            if (pairing == null) {
                // Nothing to unlock with, so do not pretend otherwise.
                phase.value = Phase.Enroll
                return@authenticate
            }
            unlockUi.value = UnlockUi.Working
            Thread {
                val result = runCatching {
                    Mobile.unlockLUKS(pairing.passphrase, pairing.seed, pairing.deviceId)
                }
                runOnUiThread {
                    unlockUi.value = result.fold(
                        onSuccess = { UnlockUi.Success },
                        onFailure = { e ->
                            val message = e.message ?: ""
                            UnlockUi.Failed(Mobile.classifyUnlockError(message), message)
                        }
                    )
                }
            }.start()
        }
    }

    private fun authenticate(reason: String, onSuccess: () -> Unit) {
        val prompt = BiometricPrompt(
            this,
            ContextCompat.getMainExecutor(this),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    onSuccess()
                }

                override fun onAuthenticationError(code: Int, message: CharSequence) {
                    // Cancelling is a decision, not a failure worth shouting about.
                    if (code != BiometricPrompt.ERROR_USER_CANCELED &&
                        code != BiometricPrompt.ERROR_NEGATIVE_BUTTON &&
                        code != BiometricPrompt.ERROR_CANCELED
                    ) {
                        notice.value = message.toString()
                    }
                }
            }
        )
        prompt.authenticate(
            BiometricPrompt.PromptInfo.Builder()
                .setTitle(getString(R.string.app_name))
                .setSubtitle(reason)
                // Strong biometrics, with the screen lock as the fallback. A negative
                // button cannot be set alongside DEVICE_CREDENTIAL.
                .setAllowedAuthenticators(BIOMETRIC_STRONG or DEVICE_CREDENTIAL)
                .build()
        )
    }

    override fun onDestroy() {
        super.onDestroy()
        cameraExecutor.shutdown()
    }
}

private fun parsePairing(payload: String): Pairing? = runCatching {
    val json = JSONObject(payload)
    Pairing(
        passphrase = json.getString("passphrase"),
        seed = json.getString("phone_seed"),
        deviceId = json.getString("laptop_device_id")
    )
}.getOrNull()

/** A Syncthing Device ID is 56 base32 characters, usually written in dash-separated groups. */
internal fun looksLikeDeviceId(value: String): Boolean {
    val bare = value.replace("-", "")
    return bare.length == 56 && bare.all { it in 'A'..'Z' || it in '2'..'7' }
}

@Composable
private fun focusBorderColor(focused: Boolean) =
    if (focused) MaterialTheme.colorScheme.primary else Color.Transparent

@OptIn(ExperimentalComposeUiApi::class)
@Composable
private fun EnrollScreen(
    notice: String?,
    onScanClick: () -> Unit,
    onManualClick: () -> Unit
) {
    val scanFocus = remember { FocusRequester() }
    val scanInteraction = remember { MutableInteractionSource() }
    val manualInteraction = remember { MutableInteractionSource() }
    LaunchedEffect(Unit) { scanFocus.requestFocus() }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(32.dp)
            .onPreviewKeyEvent { e ->
                if (e.type == KeyEventType.KeyDown) {
                    when (e.key) {
                        Key.S -> { onScanClick(); true }
                        Key.T -> { onManualClick(); true }
                        else -> false
                    }
                } else false
            },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        Text(stringResource(R.string.enroll_title), style = MaterialTheme.typography.headlineMedium)
        Spacer(Modifier.height(24.dp))
        Text(
            stringResource(R.string.enroll_explainer),
            style = MaterialTheme.typography.bodyLarge,
            textAlign = TextAlign.Center
        )
        Spacer(Modifier.height(12.dp))
        Text(
            stringResource(R.string.enroll_explainer_pairing),
            style = MaterialTheme.typography.bodyMedium,
            textAlign = TextAlign.Center
        )
        notice?.let {
            Spacer(Modifier.height(16.dp))
            NoticeCard(it)
        }
        Spacer(Modifier.height(32.dp))
        Button(
            onClick = onScanClick,
            modifier = Modifier
                .fillMaxWidth()
                .height(56.dp)
                .focusRequester(scanFocus)
                .border(2.dp, focusBorderColor(scanInteraction.collectIsFocusedAsState().value)),
            interactionSource = scanInteraction
        ) { Text(stringResource(R.string.enroll_scan)) }
        Spacer(Modifier.height(12.dp))
        OutlinedButton(
            onClick = onManualClick,
            modifier = Modifier
                .fillMaxWidth()
                .height(56.dp)
                .border(2.dp, focusBorderColor(manualInteraction.collectIsFocusedAsState().value)),
            interactionSource = manualInteraction
        ) { Text(stringResource(R.string.enroll_manual)) }
    }
}

@OptIn(ExperimentalComposeUiApi::class, ExperimentalMaterial3Api::class)
@Composable
private fun ManualEnrollScreen(
    notice: String?,
    onSubmit: (String, String, String) -> Unit,
    onBack: () -> Unit
) {
    var passphrase by remember { mutableStateOf("") }
    var seed by remember { mutableStateOf("") }
    var deviceId by remember { mutableStateOf("") }
    val firstField = remember { FocusRequester() }
    LaunchedEffect(Unit) { firstField.requestFocus() }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(24.dp)
            .onPreviewKeyEvent { e ->
                if (e.type == KeyEventType.KeyDown && e.key == Key.Escape) {
                    onBack(); true
                } else false
            },
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Spacer(Modifier.height(24.dp))
        Text(stringResource(R.string.enroll_manual_title), style = MaterialTheme.typography.headlineSmall)
        Spacer(Modifier.height(8.dp))
        Text(
            stringResource(R.string.enroll_manual_hint),
            style = MaterialTheme.typography.bodyMedium,
            textAlign = TextAlign.Center
        )
        notice?.let {
            Spacer(Modifier.height(16.dp))
            NoticeCard(it)
        }
        Spacer(Modifier.height(24.dp))
        OutlinedTextField(
            value = passphrase,
            onValueChange = { passphrase = it },
            label = { Text(stringResource(R.string.enroll_field_passphrase)) },
            singleLine = true,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
            modifier = Modifier.fillMaxWidth().focusRequester(firstField)
        )
        Spacer(Modifier.height(12.dp))
        OutlinedTextField(
            value = seed,
            onValueChange = { seed = it },
            label = { Text(stringResource(R.string.enroll_field_seed)) },
            singleLine = true,
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Ascii,
                imeAction = ImeAction.Next
            ),
            modifier = Modifier.fillMaxWidth()
        )
        Spacer(Modifier.height(12.dp))
        OutlinedTextField(
            value = deviceId,
            onValueChange = { deviceId = it },
            label = { Text(stringResource(R.string.enroll_field_device)) },
            singleLine = true,
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Ascii,
                imeAction = ImeAction.Done
            ),
            modifier = Modifier.fillMaxWidth()
        )
        Spacer(Modifier.height(24.dp))
        Button(
            onClick = { onSubmit(passphrase, seed, deviceId) },
            modifier = Modifier.fillMaxWidth().height(56.dp)
        ) { Text(stringResource(R.string.enroll_save)) }
        Spacer(Modifier.height(8.dp))
        TextButton(onClick = onBack, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.enroll_back))
        }
    }
}

@OptIn(ExperimentalComposeUiApi::class)
@Composable
private fun UnlockScreen(
    state: UnlockUi,
    notice: String?,
    onUnlockClick: () -> Unit,
    onFinished: () -> Unit
) {
    val unlockFocus = remember { FocusRequester() }
    val unlockInteraction = remember { MutableInteractionSource() }
    LaunchedEffect(state) { if (state !is UnlockUi.Working) unlockFocus.requestFocus() }

    // Success ends the task: there is nothing else to do here, and leaving the app open
    // invites a second unlock nobody asked for.
    if (state is UnlockUi.Success) {
        LaunchedEffect(Unit) {
            delay(2000)
            onFinished()
        }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(32.dp)
            .onPreviewKeyEvent { e ->
                if (e.type == KeyEventType.KeyDown && e.key == Key.U &&
                    state !is UnlockUi.Working && state !is UnlockUi.Success
                ) {
                    onUnlockClick(); true
                } else false
            },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        Text(stringResource(R.string.unlock_title), style = MaterialTheme.typography.headlineMedium)
        if (notice != null && state is UnlockUi.Idle) {
            Spacer(Modifier.height(24.dp))
            NoticeCard(notice)
        }
        Spacer(Modifier.height(40.dp))

        when (state) {
            is UnlockUi.Working -> {
                CircularProgressIndicator()
                Spacer(Modifier.height(16.dp))
                Text(stringResource(R.string.unlock_working))
            }

            is UnlockUi.Success -> SuccessMark()

            is UnlockUi.Failed -> {
                FailureCard(state)
                Spacer(Modifier.height(24.dp))
                Button(
                    onClick = onUnlockClick,
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(64.dp)
                        .focusRequester(unlockFocus)
                        .border(
                            2.dp,
                            focusBorderColor(unlockInteraction.collectIsFocusedAsState().value)
                        ),
                    interactionSource = unlockInteraction
                ) { Text(stringResource(R.string.unlock_retry)) }
            }

            is UnlockUi.Idle -> Button(
                onClick = onUnlockClick,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(80.dp)
                    .focusRequester(unlockFocus)
                    .border(
                        2.dp,
                        focusBorderColor(unlockInteraction.collectIsFocusedAsState().value)
                    ),
                interactionSource = unlockInteraction
            ) {
                Text(
                    stringResource(R.string.unlock_button),
                    style = MaterialTheme.typography.titleLarge
                )
            }
        }
    }
}

/** A drawn checkmark, so no icon dependency is pulled in for one glyph. */
@Composable
private fun SuccessMark() {
    Box(
        modifier = Modifier
            .size(96.dp)
            .background(Color(0xFF2E7D32), CircleShape),
        contentAlignment = Alignment.Center
    ) {
        Text("✓", style = MaterialTheme.typography.displayMedium, color = Color.White)
    }
    Spacer(Modifier.height(16.dp))
    Text(stringResource(R.string.unlock_success), style = MaterialTheme.typography.titleLarge)
}

/**
 * Failures say what to do next. A toast was too easy to miss for something the user is
 * actively waiting on, and "unlock failed" alone tells them nothing about whether to wait,
 * retry, or go and look at the computer.
 */
@Composable
private fun FailureCard(state: UnlockUi.Failed) {
    val advice = when (state.category) {
        "SERVER_OFFLINE" -> stringResource(R.string.err_server_offline)
        "SERVER_UNREACHABLE" -> stringResource(R.string.err_server_unreachable)
        "WRONG_DEVICE" -> stringResource(R.string.err_wrong_device)
        "NO_NETWORK" -> stringResource(R.string.err_no_network)
        "REJECTED" -> stringResource(R.string.err_rejected)
        else -> stringResource(R.string.err_unknown)
    }
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer),
        modifier = Modifier.fillMaxWidth()
    ) {
        Column(Modifier.padding(16.dp)) {
            Text(advice, color = MaterialTheme.colorScheme.onErrorContainer)
            state.detail?.takeIf { it.isNotBlank() }?.let {
                Spacer(Modifier.height(8.dp))
                Text(
                    stringResource(R.string.err_details, it),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onErrorContainer
                )
            }
        }
    }
}

@Composable
private fun NoticeCard(text: String) {
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.secondaryContainer),
        modifier = Modifier.fillMaxWidth()
    ) {
        Text(
            text,
            modifier = Modifier.padding(16.dp),
            color = MaterialTheme.colorScheme.onSecondaryContainer
        )
    }
}

@Composable
private fun MessageScreen(message: String, actionLabel: String?, onAction: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        Text(message, style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center)
        if (actionLabel != null) {
            Spacer(Modifier.height(24.dp))
            Button(onClick = onAction, modifier = Modifier.fillMaxWidth().height(56.dp)) {
                Text(actionLabel)
            }
        }
    }
}

@OptIn(ExperimentalComposeUiApi::class)
@Composable
private fun QRScannerScreen(onQRCodeScanned: (String) -> Unit, onCancel: () -> Unit) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val cameraProviderFuture = remember { ProcessCameraProvider.getInstance(context) }
    val cancelFocus = remember { FocusRequester() }
    val cancelInteraction = remember { MutableInteractionSource() }

    LaunchedEffect(Unit) { cancelFocus.requestFocus() }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .onPreviewKeyEvent { e ->
                if (e.type == KeyEventType.KeyDown && e.key == Key.Escape) {
                    onCancel(); true
                } else false
            }
    ) {
        AndroidView(
            factory = { ctx ->
                val previewView = PreviewView(ctx)
                val executor = ContextCompat.getMainExecutor(ctx)
                cameraProviderFuture.addListener({
                    val cameraProvider = cameraProviderFuture.get()
                    val preview = Preview.Builder().build().also {
                        it.setSurfaceProvider(previewView.surfaceProvider)
                    }

                    val imageAnalysis = ImageAnalysis.Builder()
                        .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                        .build()

                    val reader = MultiFormatReader()
                    imageAnalysis.setAnalyzer(executor) { imageProxy: ImageProxy ->
                        val yBuffer = imageProxy.planes[0].buffer
                        val ySize = yBuffer.remaining()
                        val yArray = ByteArray(ySize)
                        yBuffer.get(yArray)

                        val source = PlanarYUVLuminanceSource(
                            yArray,
                            imageProxy.width,
                            imageProxy.height,
                            0, 0,
                            imageProxy.width,
                            imageProxy.height,
                            false
                        )
                        val binaryBitmap = BinaryBitmap(HybridBinarizer(source))

                        try {
                            val result = reader.decode(binaryBitmap)
                            if (result.text.contains("laptop_device_id")) {
                                onQRCodeScanned(result.text)
                            }
                        } catch (e: Exception) {
                            // No barcode in this frame, which is the common case.
                        } finally {
                            imageProxy.close()
                        }
                    }

                    try {
                        cameraProvider.unbindAll()
                        cameraProvider.bindToLifecycle(
                            lifecycleOwner,
                            CameraSelector.DEFAULT_BACK_CAMERA,
                            preview,
                            imageAnalysis
                        )
                    } catch (e: Exception) {
                        // Nothing to bind to; the cancel button remains the way out.
                    }
                }, executor)
                previewView
            },
            modifier = Modifier.fillMaxSize().focusable(false)
        )

        Button(
            onClick = onCancel,
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .padding(32.dp)
                .focusRequester(cancelFocus)
                .border(2.dp, focusBorderColor(cancelInteraction.collectIsFocusedAsState().value)),
            interactionSource = cancelInteraction
        ) { Text(stringResource(R.string.scanner_cancel)) }
    }
}
