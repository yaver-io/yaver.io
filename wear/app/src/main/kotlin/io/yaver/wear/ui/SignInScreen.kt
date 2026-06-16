package io.yaver.wear.ui

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.wear.compose.material.MaterialTheme
import androidx.wear.compose.material.Text
import com.google.zxing.BarcodeFormat
import com.google.zxing.qrcode.QRCodeWriter
import io.yaver.wear.Backend

/**
 * Standalone-ONLY sign-in: RFC 8628 device-code flow (see [Backend]).
 *
 * This screen is NOT part of the default phone-paired path — in phone-paired
 * mode the watch holds no token and never signs in (the phone is the
 * brain-of-record). It's reached only behind an explicit "use without your
 * phone" opt-in, the one place the watch starts holding something sensitive.
 *
 * It shows a QR of the verification URI plus the short user code; the user
 * approves from any already-signed-in browser/phone. While visible, the caller
 * polls [Backend.pollUntilApproved] and persists the returned session token.
 */
@Composable
fun SignInScreen(deviceCode: Backend.DeviceCode) {
    Column(
        modifier = Modifier
            .fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = "Sign in",
            style = MaterialTheme.typography.title3,
        )
        Spacer(modifier = Modifier.height(6.dp))

        val qr = remember(deviceCode.verificationUri) {
            qrBitmap(deviceCode.verificationUri)
        }
        if (qr != null) {
            Image(
                bitmap = qr.asImageBitmap(),
                contentDescription = "Sign-in QR code",
                modifier = Modifier.size(96.dp),
            )
        }
        Spacer(modifier = Modifier.height(6.dp))
        Text(
            text = deviceCode.userCode,
            textAlign = TextAlign.Center,
            style = MaterialTheme.typography.title2,
        )
        Spacer(modifier = Modifier.height(4.dp))
        Text(
            text = deviceCode.verificationUri,
            textAlign = TextAlign.Center,
            style = MaterialTheme.typography.caption2,
            maxLines = 2,
        )
    }
}

/** Render a QR PNG bitmap for [content]. Returns null on failure (UI shows the
 *  short code regardless). */
private fun qrBitmap(content: String, sizePx: Int = 256): Bitmap? {
    return try {
        val matrix = QRCodeWriter().encode(content, BarcodeFormat.QR_CODE, sizePx, sizePx)
        val bmp = Bitmap.createBitmap(sizePx, sizePx, Bitmap.Config.ARGB_8888)
        for (x in 0 until sizePx) {
            for (y in 0 until sizePx) {
                bmp.setPixel(
                    x, y,
                    if (matrix[x, y]) android.graphics.Color.BLACK
                    else android.graphics.Color.WHITE,
                )
            }
        }
        bmp
    } catch (_: Throwable) {
        null
    }
}
