import XCTest
@testable import YaverFixture

@MainActor
final class TodoStoreTests: XCTestCase {
    func testAddDraftInsertsTrimmedItem() {
        let store = TodoStore()
        let initialCount = store.items.count

        store.draft = "  Validate relay fallback  "
        store.addDraft()

        XCTAssertEqual(store.items.count, initialCount + 1)
        XCTAssertEqual(store.items.first?.title, "Validate relay fallback")
        XCTAssertEqual(store.draft, "")
    }

    func testToggleFlipsCompletion() {
        let store = TodoStore()
        let item = store.items[0]

        store.toggle(item)

        XCTAssertEqual(store.items[0].isDone, !item.isDone)
    }

    func testRemoveDeletesItem() {
        let store = TodoStore()
        let item = store.items[1]

        store.remove(item)

        XCTAssertFalse(store.items.contains(item))
    }
}
