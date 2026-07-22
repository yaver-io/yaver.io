import JavaScriptKit

// Yaver todo — Swift compiled to WebAssembly, rendered in the browser.
//
// THE DEMO: this streams from a Linux Cloud Workspace over WebRTC, and an agent
// changes `backgroundColor` below on request. ONE obvious declaration, so
// "change the background to blue" has an unambiguous edit site — a demo that
// requires guessing which of six colour expressions to touch proves nothing
// about the loop.
//
// Deliberately mirrors yaver-todo-rn's UX so the app is the CONTROL: any
// difference between the RN and Swift previews is the transport, not the app.
//
// JavaScriptKit, not Tokamak: Tokamak is abandoned (last commit Feb 2023) and
// does not build against any SwiftWasm SDK that exists. See
// docs/architecture/swift-linux-webrtc-audit.md §5.6.

/// The single knob the runner demo changes.
let backgroundColor = "#fafafc"

struct Todo {
    let id: Int
    let title: String
    let done: Bool
}

let todos: [Todo] = [
    Todo(id: 1, title: "Open Yaver on your phone", done: true),
    Todo(id: 2, title: "Edit this app from anywhere", done: false),
    Todo(id: 3, title: "Ship it", done: false),
]

// Bind through JSObject rather than JSValue dynamic members: the JSValue form
// is ambiguous under JavaScriptKit 0.19+ and produces
// "ambiguous use of subscript(dynamicMember:)".
let document = JSObject.global.document.object!
let body = document.body.object!

func setStyle(_ node: JSObject, _ property: String, _ value: String) {
    guard let style = node.style.object else { return }
    _ = style.setProperty!(property, value)
}

func makeElement(_ tag: String, text: String? = nil) -> JSObject {
    let node = document.createElement!(tag).object!
    if let text { node.textContent = text.jsValue }
    return node
}

setStyle(body, "background", backgroundColor)
setStyle(body, "font-family", "-apple-system, system-ui, sans-serif")
setStyle(body, "padding", "24px")
setStyle(body, "margin", "0")

let root = makeElement("div")
_ = body.appendChild!(root)

let heading = makeElement("h1", text: "Yaver Todo")
_ = root.appendChild!(heading)

for todo in todos {
    let row = makeElement("div", text: "\(todo.done ? "☑" : "☐") \(todo.title)")
    setStyle(row, "padding", "8px 0")
    setStyle(row, "opacity", todo.done ? "0.5" : "1")
    _ = root.appendChild!(row)
}

let caption = makeElement("p", text: "Swift → WebAssembly → browser → WebRTC")
setStyle(caption, "opacity", "0.6")
setStyle(caption, "font-size", "13px")
_ = root.appendChild!(caption)
