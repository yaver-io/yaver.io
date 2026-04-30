import SwiftUI

struct TodoListView: View {
    @ObservedObject var store: TodoStore

    var body: some View {
        NavigationStack {
            VStack(spacing: 16) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("Swift Todo Fixture")
                        .font(.title2.bold())
                    Text("Use this project to test Yaver remote runtime tap, text, relay mode, and feedback flows.")
                        .font(.footnote)
                        .foregroundColor(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)

                HStack(spacing: 10) {
                    TextField("Add a todo item", text: $store.draft)
                        .textFieldStyle(.roundedBorder)
                        .accessibilityIdentifier("todo-input")
                    Button("Add") {
                        store.addDraft()
                    }
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("todo-add")
                }

                List {
                    ForEach(store.items) { item in
                        HStack(spacing: 12) {
                            Button {
                                store.toggle(item)
                            } label: {
                                Image(systemName: item.isDone ? "checkmark.circle.fill" : "circle")
                                    .foregroundStyle(item.isDone ? .green : .secondary)
                            }
                            .buttonStyle(.plain)

                            VStack(alignment: .leading, spacing: 2) {
                                Text(item.title)
                                    .strikethrough(item.isDone, color: .secondary)
                                Text(item.isDone ? "Completed" : "Open")
                                    .font(.caption)
                                    .foregroundColor(.secondary)
                            }

                            Spacer()

                            Button(role: .destructive) {
                                store.remove(item)
                            } label: {
                                Image(systemName: "trash")
                            }
                            .buttonStyle(.borderless)
                        }
                        .padding(.vertical, 4)
                    }
                }
                .listStyle(.insetGrouped)
            }
            .padding()
            .navigationTitle("Todo")
        }
    }
}

#Preview {
    TodoListView(store: TodoStore())
}
