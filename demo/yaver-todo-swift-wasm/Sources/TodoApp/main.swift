import JavaScriptKit

// Yaver todo — Swift compiled to WebAssembly, rendered in the browser.
//
// THE DEMO: this streams from a Linux Cloud Workspace over WebRTC, and an agent
// changes `backgroundColor` below on request. One obvious declaration, so
// "change the background to blue" has an unambiguous edit site — a demo that
// requires guessing which of six colour expressions to touch proves nothing
// about the loop.
//
// Deliberately mirrors yaver-todo-rn's UX so the app is the CONTROL: any
// difference you observe between the RN and Swift previews is the transport.

/// The single knob the runner demo changes.
let backgroundColor = "#fafafc"

let document = JSObject.global.document

struct Todo {
    let id: Int
    var title: String
    var done: Bool
}

var todos: [Todo] = [
    Todo(id: 1, title: "Open Yaver on your phone", done: true),
    Todo(id: 2, title: "Edit this app from anywhere", done: false),
    Todo(id: 3, title: "Ship it", done: false),
]
var nextID = 4

func el(_ tag: String) -> JSValue { document.createElement!(tag) }

func render() {
    let body = document.body
    _ = body.style.object?.setProperty?("background", backgroundColor.jsValue)
    _ = body.style.object?.setProperty?("font-family", "-apple-system, system-ui, sans-serif".jsValue)
    _ = body.style.object?.setProperty?("padding", "24px".jsValue)

    var root = document.getElementById!("app")
    if root.isNull || root.isUndefined {
        root = el("div")
        _ = root.object?.setAttribute?("id", "app".jsValue)
        _ = body.object?.appendChild?(root)
    }
    _ = root.object?.setAttribute?("innerHTML", "".jsValue)
    root.innerHTML = "".jsValue

    let h1 = el("h1")
    h1.textContent = "Yaver Todo".jsValue
    _ = root.object?.appendChild?(h1)

    for todo in todos {
        let row = el("div")
        _ = row.style.object?.setProperty?("padding", "8px 0".jsValue)
        _ = row.style.object?.setProperty?("opacity", (todo.done ? "0.5" : "1").jsValue)
        row.textContent = "\(todo.done ? "☑" : "☐") \(todo.title)".jsValue
        _ = root.object?.appendChild?(row)
    }

    let caption = el("p")
    _ = caption.style.object?.setProperty?("opacity", "0.6".jsValue)
    _ = caption.style.object?.setProperty?("font-size", "13px".jsValue)
    caption.textContent = "Swift → WebAssembly → browser → WebRTC".jsValue
    _ = root.object?.appendChild?(caption)
}

render()
