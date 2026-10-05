import XCTest
@testable import YaverTV

final class TVTerminalBufferTests: XCTestCase {
    func testCarriageReturnAndCursorMovementOverwriteCells() {
        var terminal = TVTerminalBuffer(columns: 12, rows: 3)
        terminal.feed(Array("hello\rY\u{1b}[2CZ".utf8))
        XCTAssertEqual(terminal.renderedText, "YelZo")
    }

    func testClearScreenAndScrollingStayBounded() {
        var terminal = TVTerminalBuffer(columns: 5, rows: 2)
        terminal.feed(Array("old\u{1b}[2Jnew\r\nline\r\nlast".utf8))
        XCTAssertFalse(terminal.renderedText.contains("old"))
        XCTAssertLessThanOrEqual(terminal.renderedText.split(separator: "\n").count, 2)
        XCTAssertTrue(terminal.renderedText.contains("last"))
    }

    func testEraseLineSupportsTmuxRedraws() {
        var terminal = TVTerminalBuffer(columns: 10, rows: 2)
        terminal.feed(Array("stale\r\u{1b}[2Kfresh".utf8))
        XCTAssertEqual(terminal.renderedText, "fresh")
    }
}
