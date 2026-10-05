package io.yaver.tv.ui

/** Bounded VT100/xterm text screen shared by the Android TV SSH surface.
 * Styling/private modes are consumed; cursor, erase, insert/delete, save and
 * scrolling operations used by shells and tmux update the visible cells. */
internal class TvTerminalBuffer(
    columns: Int,
    rows: Int,
) {
    private enum class State { TEXT, ESCAPE, CSI }

    private val columns = columns.coerceAtLeast(1)
    private val rows = rows.coerceAtLeast(1)
    private var cells = MutableList(this.rows) { CharArray(this.columns) { ' ' } }
    private var row = 0
    private var column = 0
    private var savedRow = 0
    private var savedColumn = 0
    private var state = State.TEXT
    private val csi = StringBuilder()

    val renderedText: String
        get() {
            val lines = cells.map { String(it).trimEnd() }
            val last = lines.indexOfLast { it.isNotEmpty() }
            return if (last < 0) "" else lines.subList(0, last + 1).joinToString("\n")
        }

    fun feed(bytes: ByteArray) {
        val printable = ArrayList<Byte>()
        fun flush() {
            if (printable.isEmpty()) return
            printable.toByteArray().toString(Charsets.UTF_8).forEach(::put)
            printable.clear()
        }
        bytes.forEach { signed ->
            val byte = signed.toInt() and 0xff
            when (state) {
                State.TEXT -> when {
                    byte == 0x1b -> { flush(); state = State.ESCAPE }
                    byte < 0x20 || byte == 0x7f -> { flush(); control(byte) }
                    else -> printable += signed
                }
                State.ESCAPE -> {
                    when (byte) {
                        0x5b -> { csi.clear(); state = State.CSI }
                        0x37 -> { savedRow = row; savedColumn = column; state = State.TEXT }
                        0x38 -> { row = savedRow; column = savedColumn; state = State.TEXT }
                        else -> state = State.TEXT
                    }
                }
                State.CSI -> if (byte in 0x40..0x7e) {
                    applyCsi(byte.toChar(), csi.toString()); state = State.TEXT
                } else csi.append(byte.toChar())
            }
        }
        flush()
    }

    private fun put(character: Char) {
        cells[row][column] = character
        column++
        if (column >= columns) { column = 0; lineFeed() }
    }

    private fun control(byte: Int) {
        when (byte) {
            0x08 -> column = (column - 1).coerceAtLeast(0)
            0x09 -> column = (((column / 8) + 1) * 8).coerceAtMost(columns - 1)
            0x0a, 0x0b, 0x0c -> lineFeed()
            0x0d -> column = 0
        }
    }

    private fun lineFeed() {
        if (row == rows - 1) {
            cells.removeAt(0); cells.add(CharArray(columns) { ' ' })
        } else row++
    }

    private fun applyCsi(final: Char, raw: String) {
        val values = raw.dropWhile { it in "?>!" }.split(';').map { it.toIntOrNull() ?: 0 }
        fun value(index: Int, fallback: Int = 1): Int = values.getOrNull(index)?.takeIf { it != 0 } ?: fallback
        when (final) {
            'A' -> row = (row - value(0)).coerceAtLeast(0)
            'B' -> row = (row + value(0)).coerceAtMost(rows - 1)
            'C' -> column = (column + value(0)).coerceAtMost(columns - 1)
            'D' -> column = (column - value(0)).coerceAtLeast(0)
            'E' -> { row = (row + value(0)).coerceAtMost(rows - 1); column = 0 }
            'F' -> { row = (row - value(0)).coerceAtLeast(0); column = 0 }
            'G' -> column = (value(0) - 1).coerceIn(0, columns - 1)
            'H', 'f' -> { row = (value(0) - 1).coerceIn(0, rows - 1); column = (value(1) - 1).coerceIn(0, columns - 1) }
            'd' -> row = (value(0) - 1).coerceIn(0, rows - 1)
            'J' -> eraseDisplay(values.firstOrNull() ?: 0)
            'K' -> eraseLine(values.firstOrNull() ?: 0)
            's' -> { savedRow = row; savedColumn = column }
            'u' -> { row = savedRow; column = savedColumn }
            '@' -> repeat(value(0).coerceAtMost(columns - column)) { insertBlank() }
            'P' -> repeat(value(0).coerceAtMost(columns - column)) { deleteCharacter() }
            'L' -> repeat(value(0).coerceAtMost(rows - row)) { cells.add(row, CharArray(columns) { ' ' }); cells.removeAt(rows) }
            'M' -> repeat(value(0).coerceAtMost(rows - row)) { cells.removeAt(row); cells.add(CharArray(columns) { ' ' }) }
        }
    }

    private fun eraseDisplay(mode: Int) {
        when (mode) {
            2, 3 -> { cells = MutableList(rows) { CharArray(columns) { ' ' } }; if (mode == 2) { row = 0; column = 0 } }
            1 -> { for (r in 0 until row) cells[r].fill(' '); for (c in 0..column) cells[row][c] = ' ' }
            else -> { for (c in column until columns) cells[row][c] = ' '; for (r in row + 1 until rows) cells[r].fill(' ') }
        }
    }

    private fun eraseLine(mode: Int) {
        when (mode) {
            2 -> cells[row].fill(' ')
            1 -> for (c in 0..column) cells[row][c] = ' '
            else -> for (c in column until columns) cells[row][c] = ' '
        }
    }

    private fun insertBlank() {
        for (c in columns - 1 downTo column + 1) cells[row][c] = cells[row][c - 1]
        cells[row][column] = ' '
    }

    private fun deleteCharacter() {
        for (c in column until columns - 1) cells[row][c] = cells[row][c + 1]
        cells[row][columns - 1] = ' '
    }
}
