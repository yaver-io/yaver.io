// SignInView.swift — TV device-code sign-in. Shows steps + a QR + short code,
// polls until an already-signed-in phone approves. Mirrors mobile/app/tv-signin.tsx.

import SwiftUI
import UIKit
import CoreImage.CIFilterBuiltins

struct SignInView: View {
    @EnvironmentObject var store: YaverStore
    @State private var start: DeviceCodeStart?
    @State private var error: String?
    @State private var expired = false
    @State private var approving = false      // approval seen; token arriving
    @State private var pollTask: Task<Void, Never>?

    var body: some View {
        HStack(spacing: 56) {
            VStack(alignment: .leading, spacing: 14) {
                Text("Sign in to Yaver")
                    .font(.system(size: 44, weight: .heavy))
                    .padding(.bottom, 12)
                stepText("1. Scan the code with your phone, or visit yaver.io/auth/device in any browser")
                stepText("2. Sign in if asked, then tap Approve")
                stepText("3. This Apple TV signs in automatically")

                if let code = start?.userCode {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("OR ENTER THIS CODE")
                            .font(.system(size: 15, weight: .bold)).tracking(2)
                            .foregroundStyle(.secondary)
                        Text(code)
                            .font(.system(size: 46, weight: .heavy, design: .monospaced))
                            .tracking(4)
                    }
                    .padding(.top, 24)
                }

                if approving {
                    Label("Approved — signing in…", systemImage: "checkmark.circle.fill")
                        .font(.system(size: 22, weight: .semibold))
                        .foregroundStyle(.green).padding(.top, 20)
                } else if start != nil {
                    // A quiet live indicator so the screen never looks frozen while
                    // it waits — the Netflix "waiting for you to enter the code" feel.
                    HStack(spacing: 10) {
                        ProgressView()
                        Text("Waiting for approval…").foregroundStyle(.secondary)
                    }
                    .font(.system(size: 18)).padding(.top, 20)
                }

                if let error {
                    VStack(alignment: .leading, spacing: 10) {
                        Text(error).foregroundStyle(.orange)
                        Button("Try again") { Task { await begin() } }   // was: hang forever with no way out
                    }
                    .padding(.top, 16)
                }
                if expired { Text("Code expired — generating a new one…").foregroundStyle(.secondary).padding(.top, 8) }
            }
            .frame(maxWidth: 560, alignment: .leading)

            ZStack {
                RoundedRectangle(cornerRadius: 24).fill(.white)
                if let url = start?.verifyURL, let img = qrImage(url.absoluteString) {
                    Image(uiImage: img)
                        .interpolation(.none)
                        .resizable()
                        .frame(width: 300, height: 300)
                } else {
                    ProgressView()
                }
            }
            .frame(width: 360, height: 360)
        }
        .padding(64)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task { await begin() }
        .onDisappear { pollTask?.cancel() }
    }

    private func stepText(_ s: String) -> some View {
        Text(s).font(.system(size: 22)).foregroundStyle(.secondary)
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
                    approving = true
                    if let token = r.token { store.signIn(token: token) }
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
        let context = CIContext()
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 12, y: 12)),
              let cg = context.createCGImage(output, from: output.extent) else { return nil }
        return UIImage(cgImage: cg)
    }
}
