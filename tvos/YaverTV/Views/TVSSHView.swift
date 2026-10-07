// TVSSHView.swift — a real owner-authenticated PTY on Apple TV.
//
// Bluetooth keyboards reach TVKeyboardCapture through UIKit's press responder
// chain, preserving Ctrl/Option/arrows for tmux and full-screen coding TUIs.
// The command field remains as the Wi-Fi/iPhone Continuity Keyboard fallback.
// A bounded in-repo VT screen model applies the cursor/erase operations used by
// shells and tmux. Keeping this text-only surface local avoids making a clean
// tvOS archive depend on Xcode's optional Metal toolchain.

import SwiftUI
import UIKit
import PlainSSH

struct TVSSHView: View {
    @EnvironmentObject private var store: YaverStore
    @State private var launch: String?

    var body: some View {
        PlainSSHView(approvalBackend: Backend.convexSiteURL, approvalToken: store.token,
                     paneContent: { AnyView(TVDirectPaneScreen(model: $0)) })
    }

    private var launcher: some View {
        VStack(alignment: .leading, spacing: 24) {
            Text("SSH")
                .font(.system(size: 44, weight: .black))
            Text("Use a Bluetooth keyboard for direct terminal control, or the Apple TV Remote keyboard for whole commands. Sessions started below live in tmux and survive leaving this screen.")
                .font(.system(size: 20)).foregroundStyle(.secondary)
                .frame(maxWidth: 950, alignment: .leading)
            if store.selectedBox == nil {
                Label("Choose a machine first", systemImage: "exclamationmark.triangle.fill")
                    .font(.system(size: 24, weight: .semibold)).foregroundStyle(.orange)
            } else {
                HStack(spacing: 16) {
                    launchButton("Raw shell", "terminal", icon: "terminal")
                    launchButton("Codex", "codex", icon: "sparkles")
                    launchButton("Claude Code", "claude", icon: "brain.head.profile")
                    launchButton("OpenCode", "opencode", icon: "chevron.left.forwardslash.chevron.right")
                }
                Text("Machine: \(store.selectedBox?.aliasLabel ?? store.selectedBox?.name ?? "selected box")")
                    .font(.system(size: 18, design: .monospaced)).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(56)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Color.black)
    }

    private func launchButton(_ title: String, _ mode: String, icon: String) -> some View {
        Button { launch = mode } label: {
            Label(title, systemImage: icon)
                .font(.system(size: 21, weight: .semibold))
                .padding(.horizontal, 20).padding(.vertical, 14)
        }
        .buttonStyle(.borderedProminent)
    }
}

private struct TVTerminalScreen: View {
    @StateObject private var model: TVTerminalModel
    @State private var command = ""
    @State private var paneChat = false
    @State private var lastSubmitted = ""
    @AppStorage("studioTerminalFontSize") private var fontSize = 19.0
    @State private var speaking = false
    let onExit: () -> Void

    init(box: BoxTarget, token: String, launch: String, onExit: @escaping () -> Void) {
        _model = StateObject(wrappedValue: TVTerminalModel(box: box, token: token, launch: launch))
        self.onExit = onExit
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 16) {
                Button("Exit SSH") { model.disconnect(); onExit() }
                Text(model.title).font(.system(size: 20, weight: .bold, design: .monospaced))
                Spacer()
                Text(model.status).font(.system(size: 16, design: .monospaced))
                    .foregroundStyle(model.connected ? .green : .orange)
                Button("A−") { fontSize = max(13, fontSize - 1) }
                    .accessibilityLabel("Zoom terminal out")
                Button("\(Int(fontSize))") { fontSize = 19 }
                    .accessibilityLabel("Reset terminal zoom")
                Button("A+") { fontSize = min(34, fontSize + 1) }
                    .accessibilityLabel("Zoom terminal in")
                Button(speaking ? "Stop voice" : "Read output") {
                    if speaking {
                        Speech.stop()
                        speaking = false
                    } else if !model.screen.isEmpty {
                        Speech.speakSummary(of: model.screen)
                        speaking = true
                    }
                }
                Picker("Terminal view", selection: $paneChat) {
                    Text("Raw").tag(false)
                    Text("Pane chat").tag(true)
                }.pickerStyle(.segmented).frame(width: 260)
                Button("Reconnect") { model.reconnect() }
            }
            .padding(.horizontal, 34).padding(.vertical, 18)
            .background(Color(white: 0.07))

            ZStack(alignment: .topLeading) {
                ScrollViewReader { proxy in
                    ScrollView {
                        if paneChat && !lastSubmitted.isEmpty {
                            Text(lastSubmitted).font(.system(size: fontSize))
                                .padding(18).background(Color.white.opacity(0.1), in: RoundedRectangle(cornerRadius: 14))
                                .frame(maxWidth: .infinity, alignment: .trailing).padding(.horizontal, 28)
                        }
                        if paneChat { Text("Live pane").font(.caption).foregroundStyle(.secondary) }
                        Text(model.screen.isEmpty ? "Connecting to the PTY…" : model.screen)
                            .font(.system(size: fontSize, design: .monospaced))
                            .foregroundStyle(Color(red: 0.82, green: 0.86, blue: 0.90))
                            .frame(maxWidth: .infinity, alignment: .topLeading)
                            .padding(28)
                            .id("terminal-tail")
                    }
                    .onChange(of: model.screen) { _, _ in proxy.scrollTo("terminal-tail", anchor: .bottom) }
                }
                TVKeyboardCapture { model.send($0) }
                    .frame(width: 2, height: 2)
                    .opacity(0.01)
                    .accessibilityHidden(true)
            }
            .background(Color(red: 0.035, green: 0.045, blue: 0.06))

            if let error = model.error {
                Text(error).font(.system(size: 16, design: .monospaced)).foregroundStyle(.red)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 28).padding(.top, 10)
            }

            HStack(spacing: 12) {
                macro("Esc", [0x1b])
                macro("Tab", [0x09])
                macro("Ctrl-C", [0x03])
                macro("Ctrl-D", [0x04])
                macro("Ctrl-B", [0x02])
                macro("Detach tmux", [0x02, 0x64])
                TextField("Dictate or type a command with Apple TV Remote…", text: $command)
                    .textFieldStyle(.plain)
                    .font(.system(size: 18, design: .monospaced))
                    .padding(14).background(Color(white: 0.12), in: RoundedRectangle(cornerRadius: 10))
                    .onSubmit { sendCommand() }
                Button("Send") { sendCommand() }.buttonStyle(.borderedProminent)
            }
            .padding(18).background(Color(white: 0.06))
        }
        .background(Color.black)
        .onAppear { model.connect() }
        .onDisappear { Speech.stop(); model.disconnect() }
    }

    private func macro(_ title: String, _ bytes: [UInt8]) -> some View {
        Button(title) { model.send(bytes) }.buttonStyle(.bordered)
    }

    private func sendCommand() {
        guard !command.isEmpty, model.connected else { return }
        lastSubmitted = command
        model.send(Array(command.utf8) + [0x0d])
        command = ""
    }
}

@MainActor
private final class TVTerminalModel: NSObject, ObservableObject {
    @Published var screen = ""
    @Published var status = "connecting"
    @Published var error: String?
    @Published var connected = false
    @Published var title: String

    private let box: BoxTarget
    private let token: String
    private let launch: String
    private var socket: URLSessionWebSocketTask?
    private var receiveTask: Task<Void, Never>?
    private var endpointIndex = 0
    private var terminal = TVTerminalBuffer(columns: 110, rows: 34)

    init(box: BoxTarget, token: String, launch: String) {
        self.box = box
        self.token = token
        self.launch = launch
        self.title = "\(box.aliasLabel ?? box.name) · \(launch == "terminal" ? "shell" : launch)"
    }

    func connect() {
        disconnect(markClosed: false)
        endpointIndex = 0
        openNextEndpoint()
    }

    func reconnect() { connect() }

    func disconnect() { disconnect(markClosed: true) }

    private func disconnect(markClosed: Bool) {
        receiveTask?.cancel()
        receiveTask = nil
        socket?.cancel(with: .goingAway, reason: nil)
        socket = nil
        connected = false
        if markClosed { status = "detached" }
    }

    private func openNextEndpoint() {
        let endpoints = box.requestEndpoints(path: "/ws/terminal")
        guard endpointIndex < endpoints.count else {
            status = "unreachable"
            error = "The PTY could not connect over LAN or relay. Make sure yaver serve is running on \(box.name)."
            return
        }
        let endpoint = endpoints[endpointIndex]
        endpointIndex += 1
        guard var components = URLComponents(url: endpoint.url, resolvingAgainstBaseURL: false) else {
            openNextEndpoint(); return
        }
        components.scheme = components.scheme == "https" ? "wss" : "ws"
        components.queryItems = launch == "terminal"
            ? [
                URLQueryItem(name: "profile_tmux", value: "yaver-studio"),
                URLQueryItem(name: "profile_shell", value: "default"),
                URLQueryItem(name: "term", value: "xterm-256color"),
            ]
            : [
                URLQueryItem(name: "launch", value: launch),
                URLQueryItem(name: "term", value: "xterm-256color"),
            ]
        guard let url = components.url else { openNextEndpoint(); return }
        var request = URLRequest(url: url)
        request.timeoutInterval = 12
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        if endpoint.relay, let password = box.relayPassword, !password.isEmpty {
            request.setValue(password, forHTTPHeaderField: "X-Relay-Password")
        }
        let task = URLSession.shared.webSocketTask(with: request)
        socket = task
        status = endpoint.relay ? "connecting via relay" : "connecting via LAN"
        error = nil
        task.resume()
        sendControl(["resize": ["cols": 110, "rows": 34]])
        receiveTask = Task { [weak self] in await self?.receiveLoop(task, relay: endpoint.relay) }
    }

    private func receiveLoop(_ task: URLSessionWebSocketTask, relay: Bool) async {
        do {
            while !Task.isCancelled {
                let message = try await task.receive()
                if !connected {
                    connected = true
                    status = relay ? "relay · keyboard ready" : "LAN · keyboard ready"
                }
                switch message {
                case .data(let data):
                    terminal.feed([UInt8](data))
                    screen = terminal.renderedText
                case .string(let text):
                    if let data = text.data(using: .utf8),
                       let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                       let terminalError = object["error"] as? String {
                        error = terminalError
                    }
                @unknown default: break
                }
            }
        } catch {
            guard !Task.isCancelled else { return }
            socket = nil
            connected = false
            openNextEndpoint()
        }
    }

    func send(_ bytes: [UInt8]) {
        guard connected, let socket else { return }
        Task { try? await socket.send(.data(Data(bytes))) }
    }

    private func sendControl(_ object: [String: Any]) {
        guard let socket,
              let data = try? JSONSerialization.data(withJSONObject: object),
              let text = String(data: data, encoding: .utf8) else { return }
        Task { try? await socket.send(.string(text)) }
    }

}

/// A deliberately bounded VT100/xterm screen for the TV text renderer.
///
/// It implements the cursor, erase, insert/delete, save/restore and scrolling
/// operations emitted by common shells and tmux. Styling and private mode
/// toggles are consumed but intentionally not rendered. The remote PTY remains
/// the source of truth, so keyboard bytes are never reconstructed from here.
struct TVTerminalBuffer {
    private enum ParseState { case text, escape, csi }

    let columns: Int
    let rows: Int
    private(set) var cells: [[Character]]
    private var row = 0
    private var column = 0
    private var savedRow = 0
    private var savedColumn = 0
    private var state: ParseState = .text
    private var csi = ""
    private let blank: Character = " "

    init(columns: Int, rows: Int) {
        self.columns = max(1, columns)
        self.rows = max(1, rows)
        self.cells = Array(repeating: Array(repeating: " ", count: max(1, columns)), count: max(1, rows))
    }

    mutating func feed(_ bytes: [UInt8]) {
        var printable: [UInt8] = []
        func flushPrintable(_ buffer: inout TVTerminalBuffer, _ bytes: inout [UInt8]) {
            guard !bytes.isEmpty else { return }
            for character in String(decoding: bytes, as: UTF8.self) { buffer.put(character) }
            bytes.removeAll(keepingCapacity: true)
        }

        for byte in bytes {
            switch state {
            case .text:
                if byte == 0x1b {
                    flushPrintable(&self, &printable)
                    state = .escape
                } else if byte < 0x20 || byte == 0x7f {
                    flushPrintable(&self, &printable)
                    control(byte)
                } else {
                    printable.append(byte)
                }
            case .escape:
                if byte == 0x5b { csi = ""; state = .csi }
                else {
                    if byte == 0x37 { savedRow = row; savedColumn = column }
                    if byte == 0x38 { row = savedRow; column = savedColumn }
                    state = .text
                }
            case .csi:
                if byte >= 0x40 && byte <= 0x7e {
                    applyCSI(final: Character(UnicodeScalar(byte)), body: csi)
                    state = .text
                } else {
                    let scalar = UnicodeScalar(byte)
                    csi.unicodeScalars.append(scalar)
                }
            }
        }
        flushPrintable(&self, &printable)
    }

    var renderedText: String {
        let lines = cells.map { line in
            var value = String(line)
            while value.last == blank { value.removeLast() }
            return value
        }
        guard let last = lines.lastIndex(where: { !$0.isEmpty }) else { return "" }
        return lines[...last].joined(separator: "\n")
    }

    private mutating func put(_ character: Character) {
        cells[row][column] = character
        column += 1
        if column >= columns { column = 0; lineFeed() }
    }

    private mutating func control(_ byte: UInt8) {
        switch byte {
        case 0x08: column = max(0, column - 1)
        case 0x09: column = min(columns - 1, ((column / 8) + 1) * 8)
        case 0x0a, 0x0b, 0x0c: lineFeed()
        case 0x0d: column = 0
        default: break
        }
    }

    private mutating func lineFeed() {
        if row == rows - 1 {
            cells.removeFirst()
            cells.append(Array(repeating: blank, count: columns))
        } else { row += 1 }
    }

    private mutating func applyCSI(final: Character, body: String) {
        let clean = body.drop(while: { "?>!".contains($0) })
        let values = clean.split(separator: ";", omittingEmptySubsequences: false).map { Int($0) ?? 0 }
        func value(_ index: Int, default fallback: Int = 1) -> Int {
            guard index < values.count, values[index] != 0 else { return fallback }
            return values[index]
        }
        switch final {
        case "A": row = max(0, row - value(0))
        case "B": row = min(rows - 1, row + value(0))
        case "C": column = min(columns - 1, column + value(0))
        case "D": column = max(0, column - value(0))
        case "E": row = min(rows - 1, row + value(0)); column = 0
        case "F": row = max(0, row - value(0)); column = 0
        case "G": column = min(columns - 1, value(0) - 1)
        case "H", "f": row = min(rows - 1, value(0) - 1); column = min(columns - 1, value(1) - 1)
        case "d": row = min(rows - 1, value(0) - 1)
        case "J": eraseDisplay(values.first ?? 0)
        case "K": eraseLine(values.first ?? 0)
        case "s": savedRow = row; savedColumn = column
        case "u": row = savedRow; column = savedColumn
        case "@": insertBlanks(value(0))
        case "P": deleteCharacters(value(0))
        case "L": insertLines(value(0))
        case "M": deleteLines(value(0))
        default: break // SGR, modes, margins and reports do not change text cells.
        }
    }

    private mutating func eraseDisplay(_ mode: Int) {
        if mode == 2 || mode == 3 {
            cells = Array(repeating: Array(repeating: blank, count: columns), count: rows)
            if mode == 2 { row = 0; column = 0 }
        } else if mode == 1 {
            for r in 0..<row { cells[r] = Array(repeating: blank, count: columns) }
            for c in 0...column { cells[row][c] = blank }
        } else {
            for c in column..<columns { cells[row][c] = blank }
            if row + 1 < rows {
                for r in (row + 1)..<rows { cells[r] = Array(repeating: blank, count: columns) }
            }
        }
    }

    private mutating func eraseLine(_ mode: Int) {
        if mode == 2 { cells[row] = Array(repeating: blank, count: columns) }
        else if mode == 1 { for c in 0...column { cells[row][c] = blank } }
        else { for c in column..<columns { cells[row][c] = blank } }
    }

    private mutating func insertBlanks(_ count: Int) {
        for _ in 0..<min(count, columns - column) {
            cells[row].insert(blank, at: column); cells[row].removeLast()
        }
    }

    private mutating func deleteCharacters(_ count: Int) {
        for _ in 0..<min(count, columns - column) {
            cells[row].remove(at: column); cells[row].append(blank)
        }
    }

    private mutating func insertLines(_ count: Int) {
        for _ in 0..<min(count, rows - row) {
            cells.insert(Array(repeating: blank, count: columns), at: row); cells.removeLast()
        }
    }

    private mutating func deleteLines(_ count: Int) {
        for _ in 0..<min(count, rows - row) {
            cells.remove(at: row); cells.append(Array(repeating: blank, count: columns))
        }
    }
}

private struct TVKeyboardCapture: UIViewRepresentable {
    let onBytes: ([UInt8]) -> Void
    func makeUIView(context: Context) -> RawKeyboardView {
        let view = RawKeyboardView()
        view.onBytes = onBytes
        DispatchQueue.main.async { _ = view.becomeFirstResponder() }
        return view
    }
    func updateUIView(_ view: RawKeyboardView, context: Context) {
        view.onBytes = onBytes
        if !view.isFirstResponder { DispatchQueue.main.async { _ = view.becomeFirstResponder() } }
    }
}

private final class RawKeyboardView: UIView, UIKeyInput {
    var onBytes: (([UInt8]) -> Void)?
    override var canBecomeFirstResponder: Bool { true }
    var hasText: Bool { false }
    func insertText(_ text: String) { onBytes?(text == "\n" ? [0x0d] : Array(text.utf8)) }
    func deleteBackward() { onBytes?([0x7f]) }

    override func pressesBegan(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        var handled = false
        for press in presses {
            guard let key = press.key else { continue }
            let value = key.charactersIgnoringModifiers
            let special: [UInt8]?
            switch value {
            case UIKeyCommand.inputUpArrow: special = Array("\u{1b}[A".utf8)
            case UIKeyCommand.inputDownArrow: special = Array("\u{1b}[B".utf8)
            case UIKeyCommand.inputLeftArrow: special = Array("\u{1b}[D".utf8)
            case UIKeyCommand.inputRightArrow: special = Array("\u{1b}[C".utf8)
            case UIKeyCommand.inputEscape: special = [0x1b]
            case "\t": special = [0x09]
            case "\r", "\n": special = [0x0d]
            default: special = nil
            }
            if let special { onBytes?(special); handled = true; continue }
            if key.modifierFlags.contains(.control), let scalar = value.unicodeScalars.first {
                onBytes?([UInt8(scalar.value & 0x1f)]); handled = true
            } else if key.modifierFlags.contains(.alternate), !key.characters.isEmpty {
                onBytes?([0x1b] + Array(key.characters.utf8)); handled = true
            }
        }
        if !handled { super.pressesBegan(presses, with: event) }
    }
}


/// TV reads the exact existing tmux pane, never creates a second runner. The
/// system TextField exposes Apple's iPhone Remote/Continuity keyboard.
struct TVDirectPaneScreen: View {
    @ObservedObject var model: PaneModel
    @State private var draft = ""
    @State private var chat = false
    @State private var account = false
    @State private var rawKeyboard = false
    @State private var fontSize = 19.0
    @State private var inputQueue: Task<Void, Never>?
    @State private var queuedBytes = 0
    @EnvironmentObject private var store: YaverStore
    var body: some View {
        VStack(spacing: 12) {
            HStack {
                Text(model.pane?.label ?? "SSH").font(.headline)
                Spacer()
                Button("A−") { fontSize = max(12, fontSize - 1) }
                Button("A+") { fontSize = min(32, fontSize + 1) }
                Picker("Pane view", selection: $chat) { Text("Raw").tag(false); Text("Pane chat").tag(true) }.frame(width: 300)
                Button("Read pane") { Speech.speakSummary(of: model.output) }
                Button(rawKeyboard ? "Keyboard: raw" : "Keyboard: compose") { rawKeyboard.toggle() }
                Button("Yaver") { rawKeyboard = false; account.toggle() }
                Button("Detach") { model.detach() }
            }
            if chat && !model.lastInput.isEmpty {
                Text(model.lastInput).padding().background(.quaternary, in: RoundedRectangle(cornerRadius: 12)).frame(maxWidth: .infinity, alignment: .trailing)
            }
            ScrollView([.horizontal, .vertical]) {
                Text(model.output.isEmpty ? "Reading pane…" : model.output)
                    .font(.system(size: fontSize, design: .monospaced))
                    .frame(maxWidth: .infinity, alignment: .topLeading)
            }.frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            Text("Live tmux screen · use the iPhone Apple TV Remote keyboard or connect a Bluetooth keyboard").font(.caption).foregroundStyle(.secondary)
            if !model.error.isEmpty { Text(model.error).foregroundStyle(.red).font(.caption) }
            HStack {
                Button("Esc") { send("\u{1b}", submit: false) }
                Button("Tab") { send("\t", submit: false) }
                Button("Ctrl-C") { send("\u{3}", submit: false) }
                TextField("Type or dictate from your phone", text: $draft).onSubmit { submitDraft() }
                Button("Send") { submitDraft() }.disabled(draft.isEmpty || !model.connected)
            }
            if rawKeyboard { TVKeyboardCapture { send(String(decoding: $0, as: UTF8.self), submit: false) }.frame(width: 2, height: 2).opacity(0.01).accessibilityHidden(true) }
            // Capturing only while the explicit raw keyboard mode is focused
            // avoids stealing focus from the system phone keyboard composer.
        }
        .padding(30).background(Color.black)
        .sheet(isPresented: $account) {
            ScrollView { VStack(spacing: 16) {
                Button("Sign this remote into Yaver") { Task { await model.enroll() } }.disabled(model.busy)
                Text(model.enrollment).font(.system(size: 14, design: .monospaced))
                DeviceApprovalView(backend: Backend.convexSiteURL, token: store.token)
                Button("Back to pane") { account = false }
            }.padding(40) }
        }
        .onDisappear { Speech.stop() }
    }
    private func submitDraft() { let value = draft; guard !value.isEmpty else { return }; draft = ""; send(value, submit: true) }
    private func send(_ text: String, submit: Bool) {
        guard queuedBytes + text.utf8.count <= 32768 else { model.error = "Input queue is full. Wait for the connection before sending more."; return }
        queuedBytes += text.utf8.count
        let previous = inputQueue
        inputQueue = Task { await previous?.value; guard model.error.isEmpty else { queuedBytes -= text.utf8.count; return }; await model.send(text, submit: submit); queuedBytes -= text.utf8.count }
    }
}
