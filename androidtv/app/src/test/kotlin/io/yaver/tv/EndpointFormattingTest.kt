package io.yaver.tv

import org.junit.Assert.assertEquals
import org.junit.Test

class EndpointFormattingTest {
    @Test
    fun bracketsIPv6DirectEndpointExactlyOnce() {
        assertEquals("http://192.0.2.10:18080", agentHttpBase("192.0.2.10"))
        assertEquals("http://[2001:db8::10]:18080", agentHttpBase("2001:db8::10"))
        assertEquals("http://[2001:db8::10]:18080", agentHttpBase("[2001:db8::10]"))
    }
}
