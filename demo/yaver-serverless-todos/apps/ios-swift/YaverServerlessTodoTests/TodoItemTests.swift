import XCTest
@testable import YaverServerlessTodo

final class TodoItemTests: XCTestCase {
    func testDecodesSQLiteIntegerBoolean() throws {
        let data = #"{"id":"t1","title":"Ship","done":1}"#.data(using: .utf8)!
        let item = try JSONDecoder().decode(TodoItem.self, from: data)
        XCTAssertTrue(item.done)
        XCTAssertEqual(item.title, "Ship")
    }
}
