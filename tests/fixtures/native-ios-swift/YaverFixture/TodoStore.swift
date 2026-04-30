import Foundation

@MainActor
final class TodoStore: ObservableObject {
    @Published var draft: String = ""
    @Published private(set) var items: [TodoItem] = [
        TodoItem(title: "Review remote runtime"),
        TodoItem(title: "Test tap controls"),
        TodoItem(title: "Trigger feedback overlay", isDone: true),
    ]

    func addDraft() {
        let trimmed = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        items.insert(TodoItem(title: trimmed), at: 0)
        draft = ""
    }

    func toggle(_ item: TodoItem) {
        guard let index = items.firstIndex(where: { $0.id == item.id }) else { return }
        items[index].isDone.toggle()
    }

    func remove(_ item: TodoItem) {
        items.removeAll { $0.id == item.id }
    }
}
