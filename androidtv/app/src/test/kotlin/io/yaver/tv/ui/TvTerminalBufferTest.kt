package io.yaver.tv.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TvTerminalBufferTest {
    @Test fun cursorMovementOverwritesCells() {
        val terminal = TvTerminalBuffer(12, 3)
        terminal.feed("hello\rY\u001b[2CZ".toByteArray())
        assertEquals("YelZo", terminal.renderedText)
    }

    @Test fun clearAndScrollRemainBounded() {
        val terminal = TvTerminalBuffer(5, 2)
        terminal.feed("old\u001b[2Jnew\r\nline\r\nlast".toByteArray())
        assertFalse(terminal.renderedText.contains("old"))
        assertTrue(terminal.renderedText.contains("last"))
        assertTrue(terminal.renderedText.lines().size <= 2)
    }

    @Test fun eraseLineSupportsTmuxRedraws() {
        val terminal = TvTerminalBuffer(10, 2)
        terminal.feed("stale\r\u001b[2Kfresh".toByteArray())
        assertEquals("fresh", terminal.renderedText)
    }
}
