import SwiftUI

/// Account approval is independent of SSH. The approver's bearer only goes to
/// its configured Yaver backend; the requesting device claims a fresh session.
public struct DeviceApprovalView: View {
    let backend: URL
    let token: String
    @State private var code: String
    @State private var request: RequestInfo?
    @State private var busy = false
    @State private var message = ""
    private struct RequestInfo: Decodable {
        let userCode: String
        let machineName: String?
        let platform: String?
        let status: String
        let expiresAt: Double
    }
    public init(backend: URL, token: String, code: String = "") {
        self.backend = backend; self.token = token; _code = State(initialValue: code)
    }
    public var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Approve a Yaver device").font(.headline)
            TextField("Code from the requesting device", text: $code)
                .onChange(of: code) { _, _ in request = nil; message = "" }
            if let request {
                Text("Sign in \(request.machineName ?? "Unnamed device") · \(request.platform ?? "unknown platform")?")
                Text("Match \(request.userCode) on the requesting device. It will receive its own Yaver session.").font(.caption)
            }
            Button(busy ? "Checking…" : request == nil ? "Review device" : "Approve sign-in") {
                Task { await reviewOrApprove() }
            }.disabled(busy || token.isEmpty || code.isEmpty)
            if token.isEmpty { Text("Sign in to Yaver on this device to approve another device. SSH remains available.").font(.caption) }
            if !message.isEmpty { Text(message).font(.caption) }
        }
    }
    @MainActor private func reviewOrApprove() async {
        busy = true; message = ""; defer { busy = false }
        do {
            guard backend.scheme == "https" else { throw SSHFailure("Use your configured HTTPS Yaver backend.") }
            let cleaned = code.uppercased().filter { $0.isASCII && ($0.isLetter || $0.isNumber) }
            guard cleaned.count == 8 else { throw SSHFailure("Enter the eight-character code shown on the requesting device.") }
            let formatted = String(cleaned.prefix(4)) + "-" + String(cleaned.suffix(4))
            if let pending = request {
                guard pending.userCode == formatted, pending.expiresAt > Date().timeIntervalSince1970 * 1000 else {
                    request = nil; throw SSHFailure("This request expired. Start sign-in again on the requesting device.")
                }
                var req = URLRequest(url: backend.appendingPathComponent("auth/device-code/authorize"), timeoutInterval: 12)
                req.httpMethod = "POST"
                req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
                req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                req.httpBody = try JSONSerialization.data(withJSONObject: ["userCode": formatted])
                let (_, response) = try await URLSession.shared.data(for: req)
                guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                    request = nil; throw SSHFailure("Approval failed. Check your Yaver session and review the device again.")
                }
                request = nil; message = "Approved. The requesting device can finish signing in."
            } else {
                var url = URLComponents(url: backend.appendingPathComponent("auth/device-code/info"), resolvingAgainstBaseURL: false)!
                url.queryItems = [URLQueryItem(name: "user_code", value: formatted)]
                let (data, response) = try await URLSession.shared.data(for: URLRequest(url: url.url!, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 12))
                guard let http = response as? HTTPURLResponse, http.statusCode == 200 else { throw SSHFailure("Request not found. Check the code or start sign-in again.") }
                let info = try JSONDecoder().decode(RequestInfo.self, from: data)
                guard info.userCode == formatted, info.status == "pending", info.expiresAt > Date().timeIntervalSince1970 * 1000 else { throw SSHFailure("This request is no longer pending. Start sign-in again.") }
                request = info
            }
        } catch { message = error.localizedDescription }
    }
}
