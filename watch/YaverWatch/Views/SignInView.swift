// SignInView.swift — STANDALONE-mode sign-in ONLY (mode B/C, "use without your
// phone"). In the DEFAULT phone-paired topology the watch holds no token and
// never reaches this screen; the phone is already signed in.
//
// Device-code flow mirrors tvos/YaverTV/Views/SignInView.swift exactly: show a
// QR + a short code, poll until an already-signed-in phone approves. On the
// tiny wrist screen the QR is hard to scan, so the short code is the primary
// affordance and the QR is secondary (still rendered for the rare close scan).
// After a token arrives, the user also enters the LAN box host (AddBoxView).

import SwiftUI
#if canImport(WatchKit)
import WatchKit
#endif
#if canImport(CoreImage)
import CoreImage.CIFilterBuiltins
#endif

struct SignInView: View {
    @EnvironmentObject var store: WatchStore
    @Environment(\.dismiss) private var dismiss

    @State private var start: DeviceCodeStart?
    @State private var token: String?
    @State private var error: String?
    @State private var expired = false
    @State private var pollTask: Task<Void, Never>?

    var body: some View {
        ScrollView {
            VStack(alignment: .center, spacing: 12) {
                Text("Use without phone")
                    .font(.system(size: 18, weight: .heavy))

                if token == nil {
                    Text("Approve from a signed-in phone, then enter your box.")
                        .font(.footnote).foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)

                    if let code = start?.userCode {
                        VStack(spacing: 4) {
                            Text("CODE").font(.system(size: 11, weight: .bold)).tracking(2)
                                .foregroundStyle(.secondary)
                            Text(code)
                                .font(.system(size: 26, weight: .heavy, design: .monospaced))
                                .tracking(2)
                        }
                        .padding(.vertical, 4)

                        if let url = start?.verifyURL, let img = qrImage(url.absoluteString) {
                            Image(uiImage: img)
                                .interpolation(.none)
                                .resizable()
                                .frame(width: 96, height: 96)
                                .background(.white)
                        }
                    } else {
                        ProgressView()
                    }

                    if let error { Text(error).font(.footnote).foregroundStyle(.orange) }
                    if expired { Text("Code expired — refreshing…").font(.footnote).foregroundStyle(.secondary) }
                } else {
                    // Token in hand; collect the LAN box host to finish standalone setup.
                    AddBoxView(token: token!) { dismiss() }
                }
            }
            .padding(.horizontal, 6)
        }
        .task { if token == nil { await begin() } }
        .onDisappear { pollTask?.cancel() }
    }

    private func begin() async {
        error = nil
        expired = false
        do {
            let s = try await DeviceCodeAuth.start()
            start = s
            startPolling(s)
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func startPolling(_ s: DeviceCodeStart) {
        pollTask?.cancel()
        pollTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                if Task.isCancelled { return }
                let r = await DeviceCodeAuth.poll(deviceCode: s.deviceCode)
                switch r.status {
                case .authorized:
                    if let t = r.token { token = t }   // proceed to AddBoxView
                    return
                case .expired:
                    expired = true
                    await begin()
                    return
                case .pending:
                    continue
                }
            }
        }
    }

    private func qrImage(_ string: String) -> UIImage? {
        #if canImport(CoreImage)
        let context = CIContext()
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 6, y: 6)),
              let cg = context.createCGImage(output, from: output.extent) else { return nil }
        return UIImage(cgImage: cg)
        #else
        return nil
        #endif
    }
}

/// Collect the LAN host of the box running `yaver serve` and persist the
/// standalone credentials. Mirrors tvos/YaverTV/Views/DashboardView.swift::AddBoxView.
struct AddBoxView: View {
    @EnvironmentObject var store: WatchStore
    let token: String
    let onDone: () -> Void

    @State private var name = ""
    @State private var host = ""
    @State private var saving = false
    @State private var identityWarning: String?

    var body: some View {
        VStack(spacing: 10) {
            Text("Your box").font(.system(size: 16, weight: .bold))
            TextField("Name (e.g. magara)", text: $name)
            TextField("LAN host or IP", text: $host)
            Button(saving ? "Saving…" : "Save") { Task { await save() } }
                .disabled(saving || host.trimmingCharacters(in: .whitespaces).isEmpty)
            // Sign-in still SUCCEEDS without an identity — the turn transport
            // only needs host+token. Only "Update agent" needs the deviceId, so
            // a failure here is a note, not a wall.
            if let identityWarning {
                Text(identityWarning)
                    .font(.caption2).foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
        }
        .padding(.horizontal, 6)
    }

    /// Ask the box who it is BEFORE persisting, so the deviceId is captured while
    /// the box is provably reachable — the user just typed its address and is on
    /// its network. Later, when "Update agent" needs that id, the box may be
    /// asleep or a continent away; there is no second chance to ask it.
    private func save() async {
        let trimmed = host.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return }
        saving = true
        defer { saving = false }

        let deviceId = try? await BoxIdentity.fetchDeviceId(
            host: trimmed, port: Backend.agentPort, token: token
        )
        if deviceId == nil {
            identityWarning = "Saved, but couldn't identify this box — Update agent needs it. Open Settings on its network to finish."
        }
        let box = BoxTarget(id: trimmed, name: name.isEmpty ? trimmed : name,
                            host: trimmed, deviceId: deviceId)
        store.signInStandalone(token: token, box: box)
        store.standaloneOptIn = true
        // On a clean resolve, leave immediately. On a miss, hold the sheet for a
        // beat so the note is actually readable on a wrist.
        if deviceId == nil {
            try? await Task.sleep(nanoseconds: 2_500_000_000)
        }
        onDone()
    }
}
