package com.muelli.syncthingluks

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.biometric.BiometricPrompt
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.common.InputImage
import org.json.JSONObject
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

import mobile.Mobile

class MainActivity : ComponentActivity() {
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

@Composable
fun UnlockScreen(onScanClick: () -> Unit, onUnlockClick: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        Text(text = "Syncthing LUKS Unlocker", style = MaterialTheme.typography.headlineMedium)
        Spacer(modifier = Modifier.height(64.dp))
        Button(onClick = onScanClick, modifier = Modifier.fillMaxWidth().height(56.dp)) {
            Text("1. Scan Setup QR Code")
        }
        Spacer(modifier = Modifier.height(16.dp))
        Button(
            onClick = onUnlockClick,
            modifier = Modifier.fillMaxWidth().height(80.dp),
            colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.primary)
        ) {
            Text("2. One-Tap Unlock", style = MaterialTheme.typography.titleLarge)
        }
    }
}

@Composable
fun QRScannerScreen(onQRCodeScanned: (String) -> Unit, onCancel: () -> Unit) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val cameraProviderFuture = remember { ProcessCameraProvider.getInstance(context) }

    Box(modifier = Modifier.fillMaxSize()) {
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

                    val scanner = BarcodeScanning.getClient()
                    imageAnalysis.setAnalyzer(executor) { imageProxy ->
                        val mediaImage = imageProxy.image
                        if (mediaImage != null) {
                            val image = InputImage.fromMediaImage(mediaImage, imageProxy.imageInfo.rotationDegrees)
                            scanner.process(image)
                                .addOnSuccessListener { barcodes ->
                                    for (barcode in barcodes) {
                                        barcode.rawValue?.let {
                                            if (it.contains("laptop_device_id")) {
                                                onQRCodeScanned(it)
                                                imageProxy.close()
                                                return@addOnSuccessListener
                                            }
                                        }
                                    }
                                }
                                .addOnCompleteListener { imageProxy.close() }
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
            modifier = Modifier.fillMaxSize()
        )
        
        Button(
            onClick = onCancel,
            modifier = Modifier.align(Alignment.BottomCenter).padding(32.dp)
        ) {
            Text("Cancel")
        }
    }
}
