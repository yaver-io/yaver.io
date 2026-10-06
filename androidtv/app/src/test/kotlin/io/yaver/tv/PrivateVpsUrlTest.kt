package io.yaver.tv

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PrivateVpsUrlTest {
    @Test fun `normalizes URL and QR payloads`() {
        assertEquals("https://vps.example.com/yaver", Backend.normalizePrivateVpsUrl(" https://vps.example.com/yaver/// "))
        assertEquals(
            "https://vps.example.com/yaver",
            Backend.privateVpsUrlFromQr("yaver://private-vps?url=https%3A%2F%2Fvps.example.com%2Fyaver"),
        )
        assertEquals("https://vps.example.com", Backend.privateVpsUrlFromQr("{\"privateVpsUrl\":\"https://vps.example.com\"}"))
    }

    @Test fun `rejects credentials and query parameters`() {
        assertNull(Backend.normalizePrivateVpsUrl("https://user:secret@vps.example.com"))
        assertNull(Backend.normalizePrivateVpsUrl("https://vps.example.com?token=secret"))
        assertNull(Backend.normalizePrivateVpsUrl("http://vps.example.com"))
        assertNull(Backend.normalizePrivateVpsUrl("ftp://vps.example.com"))
    }
}
