import SwiftUI

@MainActor
final class TodoStore: ObservableObject {
    @Published var baseUrl = "http://127.0.0.1:18080"
    @Published var slug = "yaver-serverless-todo"
    @Published var token = ""
    @Published var draft = ""
    @Published var items: [TodoItem] = []
    @Published var error = ""
    @Published var loading = false

    private var api: YaverTodoAPI {
        YaverTodoAPI(baseUrl: baseUrl, slug: slug, token: token)
    }

    func refresh() {
        loading = true
        error = ""
        Task {
            do {
                items = try await api.list()
            } catch {
                self.error = error.localizedDescription
            }
            loading = false
        }
    }

    func addDraft() {
        let title = draft
        draft = ""
        Task {
            do {
                try await api.create(title: title)
                refresh()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    func toggle(_ item: TodoItem) {
        Task {
            do {
                try await api.setDone(id: item.id, done: !item.done)
                refresh()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    func remove(_ item: TodoItem) {
        Task {
            do {
                try await api.delete(id: item.id)
                refresh()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

struct TodoListView: View {
    @StateObject private var store = TodoStore()

    var body: some View {
        NavigationStack {
            List {
                Section {
                    TextField("Yaver Serverless URL", text: $store.baseUrl)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Project slug", text: $store.slug)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    SecureField("Project token", text: $store.token)
                }
                if !store.error.isEmpty {
                    Section {
                        Text(store.error)
                            .foregroundStyle(.red)
                    }
                }
                Section {
                    HStack {
                        TextField("What needs doing?", text: $store.draft)
                            .onSubmit { store.addDraft() }
                        Button(action: store.addDraft) {
                            Image(systemName: "plus")
                        }
                        .disabled(store.draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }
                }
                Section {
                    if store.items.isEmpty {
                        Text("No serverless todos yet.")
                            .foregroundStyle(.secondary)
                    }
                    ForEach(store.items) { item in
                        HStack {
                            Button(action: { store.toggle(item) }) {
                                Image(systemName: item.done ? "checkmark.circle.fill" : "circle")
                            }
                            .buttonStyle(.plain)
                            Text(item.title)
                                .strikethrough(item.done)
                                .foregroundStyle(item.done ? .secondary : .primary)
                            Spacer()
                            Button(role: .destructive, action: { store.remove(item) }) {
                                Image(systemName: "trash")
                            }
                        }
                    }
                } header: {
                    Text(store.loading ? "Syncing" : "\(store.items.filter { !$0.done }.count) open tasks")
                }
            }
            .navigationTitle("Yaver Serverless Todo")
            .toolbar {
                Button(action: store.refresh) {
                    Image(systemName: "arrow.clockwise")
                }
            }
            .onAppear { store.refresh() }
        }
    }
}
