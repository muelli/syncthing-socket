package com.github.muelli.syncthingsocket

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Bundle
import android.widget.Toast
import androidx.fragment.app.FragmentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.biometric.BiometricPrompt
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.border
import androidx.compose.foundation.focusable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.*
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import com.google.zxing.BinaryBitmap
import com.google.zxing.MultiFormatReader
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import org.json.JSONObject
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

import mobile.Mobile

class MainActivity : FragmentActivity() {
    private lateinit var cameraExecutor: ExecutorService
    private var isScanning = mutableStateOf(false)

    private val requestPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { isGranted ->
        if (isGranted) {
            isScanning.value = true
        } else {
            Toast.makeText(this, "Camera permission denied", Toast.LENGTH_SHORT).show()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        cameraExecutor = Executors.newSingleThreadExecutor()

        setContent {
            MaterialTheme(colorScheme = darkColorScheme()) {
                Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                    if (isScanning.value) {
                        QRScannerScreen(
                            onQRCodeScanned = { qrResult ->
                                isScanning.value = false
                                handleQRScanned(qrResult)
                            },
                            onCancel = { isScanning.value = false }
                        )
                    } else {
                        UnlockScreen(
                            onScanClick = {
                                if (ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
                                    isScanning.value = true
                                } else {
                                    requestPermissionLauncher.launch(Manifest.permission.CAMERA)
                                }
                            },
                            onUnlockClick = {
                                showBiometricPrompt()
                            }
                        )
                    }
                }
            }
        }
    }

    private fun handleQRScanned(qrResult: String) {
        try {
            val json = JSONObject(qrResult)
            val passphrase = json.getString("passphrase")
            val phoneSeed = json.getString("phone_seed")
            val laptopId = json.getString("laptop_device_id")

            val masterKey = MasterKey.Builder(this)
                .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
                .build()

            val sharedPreferences = EncryptedSharedPreferences.create(
                this,
                "secret_shared_prefs",
                masterKey,
                EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
            )

            sharedPreferences.edit()
                .putString("passphrase", passphrase)
                .putString("phone_seed", phoneSeed)
                .putString("laptop_id", laptopId)
                .apply()

            Toast.makeText(this, "Setup complete! Credentials securely stored.", Toast.LENGTH_LONG).show()
        } catch (e: Exception) {
            Toast.makeText(this, "Invalid QR Code payload.", Toast.LENGTH_LONG).show()
        }
    }

    private fun showBiometricPrompt() {
        val executor = ContextCompat.getMainExecutor(this)
        val promptInfo = BiometricPrompt.PromptInfo.Builder()
            .setTitle("Syncthing LUKS")
            .setSubtitle("Confirm your identity to unlock your laptop")
            .setNegativeButtonText("Cancel")
            .build()

        val biometricPrompt = BiometricPrompt(this, executor,
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    super.onAuthenticationSucceeded(result)
                    
                    val masterKey = MasterKey.Builder(applicationContext)
                        .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
                        .build()
                    val prefs = EncryptedSharedPreferences.create(
                        applicationContext,
                        "secret_shared_prefs",
                        masterKey,
                        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
                    )
                    
                    val passphrase = prefs.getString("passphrase", null)
                    val phoneSeed = prefs.getString("phone_seed", null)
                    val laptopId = prefs.getString("laptop_id", null)

                    if (passphrase == null || phoneSeed == null || laptopId == null) {
                        Toast.makeText(applicationContext, "No credentials found. Please scan QR first.", Toast.LENGTH_LONG).show()
                        return
                    }

                    Toast.makeText(applicationContext, "Unlocking laptop via Syncthing Relay...", Toast.LENGTH_LONG).show()

                    Thread {
                        try {
                            Mobile.unlockLUKS(passphrase, phoneSeed, laptopId)
                            runOnUiThread {
                                Toast.makeText(applicationContext, "Laptop Unlocked Successfully!", Toast.LENGTH_LONG).show()
                            }
                        } catch (e: Exception) {
                            runOnUiThread {
                                Toast.makeText(applicationContext, "Unlock Failed: ${e.message}", Toast.LENGTH_LONG).show()
                            }
                        }
                    }.start()
                }

                override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                    Toast.makeText(applicationContext, "Authentication error: $errString", Toast.LENGTH_SHORT).show()
                }
            })

        biometricPrompt.authenticate(promptInfo)
    }

    override fun onDestroy() {
        super.onDestroy()
        cameraExecutor.shutdown()
    }
}

@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun UnlockScreen(onScanClick: () -> Unit, onUnlockClick: () -> Unit) {
    val scanFocusRequester = remember { FocusRequester() }
    val unlockFocusRequester = remember { FocusRequester() }
    val scanInteractionSource = remember { MutableInteractionSource() }
    val unlockInteractionSource = remember { MutableInteractionSource() }

    LaunchedEffect(Unit) {
        unlockFocusRequester.requestFocus()
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(32.dp)
            .onPreviewKeyEvent { keyEvent ->
                if (keyEvent.type == KeyEventType.KeyDown) {
                    when (keyEvent.key) {
                        Key.Escape -> true
                        Key.S -> { onScanClick(); true }
                        Key.U -> { onUnlockClick(); true }
                        else -> false
                    }
                } else {
                    false
                }
            },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        Text(text = "Syncthing LUKS Unlocker", style = MaterialTheme.typography.headlineMedium)
        Spacer(modifier = Modifier.height(64.dp))
        Button(
            onClick = onScanClick,
            modifier = Modifier
                .fillMaxWidth()
                .height(56.dp)
                .focusRequester(scanFocusRequester)
                .focusProperties {
                    next = unlockFocusRequester
                }
                .border(
                    width = 2.dp,
                    color = if (scanInteractionSource.collectIsFocusedAsState().value) {
                        MaterialTheme.colorScheme.primary
                    } else {
                        androidx.compose.ui.graphics.Color.Transparent
                    }
                ),
            interactionSource = scanInteractionSource
        ) {
            Text("1. Scan Setup QR Code")
        }
        Spacer(modifier = Modifier.height(16.dp))
        Button(
            onClick = onUnlockClick,
            modifier = Modifier
                .fillMaxWidth()
                .height(80.dp)
                .focusRequester(unlockFocusRequester)
                .focusProperties {
                    previous = scanFocusRequester
                }
                .border(
                    width = 2.dp,
                    color = if (unlockInteractionSource.collectIsFocusedAsState().value) {
                        MaterialTheme.colorScheme.primary
                    } else {
                        androidx.compose.ui.graphics.Color.Transparent
                    }
                ),
            colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.primary),
            interactionSource = unlockInteractionSource
        ) {
            Text("2. One-Tap Unlock", style = MaterialTheme.typography.titleLarge)
        }
    }
}

@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun QRScannerScreen(onQRCodeScanned: (String) -> Unit, onCancel: () -> Unit) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val cameraProviderFuture = remember { ProcessCameraProvider.getInstance(context) }
    val cancelFocusRequester = remember { FocusRequester() }
    val cancelInteractionSource = remember { MutableInteractionSource() }

    LaunchedEffect(Unit) {
        cancelFocusRequester.requestFocus()
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .onPreviewKeyEvent { keyEvent ->
                if (keyEvent.type == KeyEventType.KeyDown) {
                    when (keyEvent.key) {
                        Key.Escape -> { onCancel(); true }
                        else -> false
                    }
                } else {
                    false
                }
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
                    imageAnalysis.setAnalyzer(executor) { imageProxy ->
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
                    } catch (e: Exception) {}
                }, executor)
                previewView
            },
            modifier = Modifier
                .fillMaxSize()
                .focusable(false)
        )

        Button(
            onClick = onCancel,
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .padding(32.dp)
                .focusRequester(cancelFocusRequester)
                .border(
                    width = 2.dp,
                    color = if (cancelInteractionSource.collectIsFocusedAsState().value) {
                        MaterialTheme.colorScheme.primary
                    } else {
                        androidx.compose.ui.graphics.Color.Transparent
                    }
                ),
            interactionSource = cancelInteractionSource
        ) {
            Text("Cancel")
        }
    }
}
