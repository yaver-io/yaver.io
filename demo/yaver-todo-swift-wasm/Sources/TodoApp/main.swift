import TokamakDOM

// Yaver todo — Swift/Tokamak fixture.
//
// Deliberately mirrors yaver-todo-rn so the two are comparable: same list, same
// add/complete interactions, same visual weight. Any difference a user notices
// between the RN and Swift previews is the transport, not the app.
//
// THE DEMO TARGET: an agent is asked "change the background to blue" and edits
// `backgroundColor` below. The rebuild → WASM → browser reload → next WebRTC
// frame chain is what proves a Swift-only app is developable from a phone,
// against a Linux box, with no Mac anywhere.
//
// ⚠️ No HMR. A Swift edit is a full WASM recompile plus a page reload, so the
// list resets. Fine for a todo fixture; worth knowing before generalising the
// loop to an app with deep navigation.

/// The single knob the demo changes. Kept as one obvious declaration so an
/// agent asked for "blue" has an unambiguous edit site — a demo that requires
/// guessing which of six colour expressions to touch proves nothing about the
/// loop.
let backgroundColor = Color(red: 0.98, green: 0.98, blue: 0.99)

struct Todo: Identifiable {
    let id: Int
    var title: String
    var done: Bool
}

struct TodoApp: App {
    var body: some Scene {
        WindowGroup("Yaver Todo — Swift") {
            TodoList()
        }
    }
}

struct TodoList: View {
    @State private var todos: [Todo] = [
        Todo(id: 1, title: "Open Yaver on your phone", done: true),
        Todo(id: 2, title: "Edit this app from anywhere", done: false),
        Todo(id: 3, title: "Ship it", done: false),
    ]
    @State private var draft: String = ""
    @State private var nextID: Int = 4

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Yaver Todo")
                .font(.largeTitle)

            HStack {
                TextField("What needs doing?", text: $draft)
                Button("Add") { add() }
            }

            ForEach(todos) { todo in
                HStack {
                    Button(todo.done ? "☑" : "☐") { toggle(todo.id) }
                    Text(todo.title)
                        .opacity(todo.done ? 0.5 : 1.0)
                }
            }

            Spacer()
            Text("Swift → SwiftWasm → Chromium → WebRTC")
                .font(.caption)
                .opacity(0.6)
        }
        .padding(24)
        .background(backgroundColor)
    }

    private func add() {
        let title = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !title.isEmpty else { return }
        todos.append(Todo(id: nextID, title: title, done: false))
        nextID += 1
        draft = ""
    }

    private func toggle(_ id: Int) {
        guard let i = todos.firstIndex(where: { $0.id == id }) else { return }
        todos[i].done.toggle()
    }
}

TodoApp.main()
