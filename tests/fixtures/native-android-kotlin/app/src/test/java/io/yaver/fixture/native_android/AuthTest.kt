package io.yaver.fixture.native_android

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AuthTest {
    @Test
    fun acceptsValidHardcodedCredentials() {
        assertTrue(Auth.authenticate("admin", "admin"))
    }

    @Test
    fun rejectsWrongPassword() {
        assertFalse(Auth.authenticate("admin", "wrong"))
    }

    @Test
    fun rejectsUnknownUser() {
        assertFalse(Auth.authenticate("intruder", "admin"))
    }

    @Test
    fun rejectsEmptyInputs() {
        assertFalse(Auth.authenticate("", ""))
    }
}
