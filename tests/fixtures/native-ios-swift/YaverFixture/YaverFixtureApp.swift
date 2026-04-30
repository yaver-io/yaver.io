import SwiftUI

@main
struct YaverFixtureApp: App {
    @StateObject private var store = TodoStore()

    var body: some Scene {
        WindowGroup {
            TodoListView(store: store)
        }
    }
}
