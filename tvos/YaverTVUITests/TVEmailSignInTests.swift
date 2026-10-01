import Network
import XCTest

final class TVEmailSignInTests: XCTestCase {
    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    func testSiriRemoteSelectSubmitsEmailCredentials() throws {
        let server = try TVEmailAuthHTTPFixture()
        addTeardownBlock { server.stop() }

        let app = XCUIApplication()
        app.launchArguments = [
            "-yaver.tv.token", "",
            "-yaver.tv.backendURL", "http://127.0.0.1:\(server.port)",
        ]
        app.launch()

        let email = app.textFields["signin.email"]
        XCTAssertTrue(email.waitForExistence(timeout: 8))
        XCTAssertTrue(email.hasFocus)
        XCUIRemote.shared.press(.select)
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        email.typeText("review@example.test")
        XCUIRemote.shared.press(.menu)

        let password = app.secureTextFields["signin.password"]
        XCTAssertTrue(password.waitForExistence(timeout: 5))
        XCUIRemote.shared.press(.down)
        XCTAssertTrue(password.hasFocus)
        XCUIRemote.shared.press(.select)
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        password.typeText("correct horse battery staple")
        XCUIRemote.shared.press(.menu)

        let submit = app.buttons["signin.email-submit"]
        XCTAssertTrue(submit.waitForExistence(timeout: 5))
        for _ in 0..<3 where !submit.hasFocus {
            XCUIRemote.shared.press(.down)
        }
        XCTAssertTrue(submit.hasFocus, "the email submit action must be reachable from the password field")
        XCUIRemote.shared.press(.select)

        let deadline = Date().addingTimeInterval(5)
        while server.loginCount == 0 && Date() < deadline {
            RunLoop.current.run(until: Date().addingTimeInterval(0.05))
        }
        XCTAssertEqual(server.loginCount, 1, "one Select press must produce exactly one POST /auth/login")
        XCTAssertTrue(server.sawExpectedCredentials)
        XCTAssertTrue(submit.waitForNonExistence(timeout: 8), "a successful login must leave the sign-in screen")
    }
}

private final class TVEmailAuthHTTPFixture: @unchecked Sendable {
    private let listener: NWListener
    private let queue = DispatchQueue(label: "io.yaver.tests.tv-email-auth")
    private let lock = NSLock()
    private var connections: [ObjectIdentifier: NWConnection] = [:]
    private var _loginCount = 0
    private var _sawExpectedCredentials = false

    private(set) var port: UInt16 = 0
    var loginCount: Int { locked { _loginCount } }
    var sawExpectedCredentials: Bool { locked { _sawExpectedCredentials } }

    init() throws {
        listener = try NWListener(using: .tcp, on: .any)
        let ready = DispatchSemaphore(value: 0)
        var startupError: NWError?
        var chosenPort: UInt16 = 0
        listener.stateUpdateHandler = { [weak listener] state in
            switch state {
            case .ready:
                chosenPort = listener?.port?.rawValue ?? 0
                ready.signal()
            case .failed(let error):
                startupError = error
                ready.signal()
            default:
                break
            }
        }
        listener.newConnectionHandler = { [weak self] connection in self?.accept(connection) }
        listener.start(queue: queue)
        guard ready.wait(timeout: .now() + 5) == .success,
              startupError == nil,
              chosenPort != 0 else {
            listener.cancel()
            throw startupError ?? NSError(
                domain: "TVEmailAuthHTTPFixture", code: 1,
                userInfo: [NSLocalizedDescriptionKey: "loopback server did not become ready"]
            )
        }
        port = chosenPort
    }

    func stop() {
        listener.cancel()
        queue.sync {
            connections.values.forEach { $0.cancel() }
            connections.removeAll()
        }
    }

    private func accept(_ connection: NWConnection) {
        connections[ObjectIdentifier(connection)] = connection
        connection.start(queue: queue)
        receive(on: connection, buffer: Data())
    }

    private func receive(on connection: NWConnection, buffer: Data) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1_048_576) { [weak self] data, _, _, error in
            guard let self else { connection.cancel(); return }
            var next = buffer
            if let data { next.append(data) }
            if self.requestIsComplete(next) {
                self.respond(to: next, on: connection)
            } else if error == nil {
                self.receive(on: connection, buffer: next)
            } else {
                self.finish(connection)
            }
        }
    }

    private func requestIsComplete(_ data: Data) -> Bool {
        guard let text = String(data: data, encoding: .utf8),
              let headerEnd = text.range(of: "\r\n\r\n") else { return false }
        let headers = String(text[..<headerEnd.lowerBound])
        let length = headers.split(separator: "\n").first { line in
            line.lowercased().hasPrefix("content-length:")
        }.flatMap { Int($0.split(separator: ":", maxSplits: 1).last?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "") } ?? 0
        let bodyStart = text.distance(from: text.startIndex, to: headerEnd.upperBound)
        return data.count >= bodyStart + length
    }

    private func respond(to data: Data, on connection: NWConnection) {
        guard let request = String(data: data, encoding: .utf8) else {
            send(#"{"error":"bad request"}"#, status: "400 Bad Request", on: connection)
            return
        }
        let requestLine = request.split(separator: "\r\n", maxSplits: 1).first.map(String.init) ?? ""
        if requestLine.hasPrefix("POST /auth/login ") {
            let body = request.components(separatedBy: "\r\n\r\n").dropFirst().joined(separator: "\r\n\r\n")
            let json = body.data(using: .utf8).flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: String] }
            locked {
                _loginCount += 1
                _sawExpectedCredentials = json?["email"] == "review@example.test"
                    && json?["password"] == "correct horse battery staple"
            }
            send(#"{"token":"ui-test-token","userId":"ui-test-user"}"#, on: connection)
        } else {
            send(#"{"error":"not found"}"#, status: "404 Not Found", on: connection)
        }
    }

    private func send(_ body: String, status: String = "200 OK", on connection: NWConnection) {
        let payload = Data(body.utf8)
        let head = "HTTP/1.1 \(status)\r\nContent-Type: application/json\r\nContent-Length: \(payload.count)\r\nConnection: close\r\n\r\n"
        connection.send(content: Data(head.utf8) + payload, completion: .contentProcessed { [weak self] _ in
            self?.finish(connection)
        })
    }

    private func finish(_ connection: NWConnection) {
        connection.cancel()
        connections.removeValue(forKey: ObjectIdentifier(connection))
    }

    private func locked<T>(_ body: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body()
    }
}
