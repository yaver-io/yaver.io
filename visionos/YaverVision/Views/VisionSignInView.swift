// VisionSignInView.swift — device-code sign-in for Vision Pro.
//
// Identical flow to tvOS (DeviceCodeAuth.start → poll → store.signIn) because it
// is the same contract with Convex; only the presentation is spatial. The head-
// set never asks for a password: an already-signed-in phone approves it.

import SwiftUI
import UIKit
import CoreImage.CIFilterBuiltins

struct VisionSignInView: View {
    @EnvironmentObject var store: YaverStore
    @State private var start: DeviceCodeStart?
    @State private var error: String?
    @State private var expired = false
    @State private var pollTask: Task<Void, Never>?

    var body: some View {
        HStack(spacing: 48) {
            VStack(alignment: .leading, spacing: 16) {
                Text("Sign in to Yaver")
                    .font(.extraLargeTitle2)
                    .padding(.bottom, 4)

                step("1. Open Yaver on your phone")
                step("2. Scan this code (or visit yaver.io/auth/device)")
                step("3. Tap Approve — this headset signs in instantly")

                if let code = start?.userCode {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("OR ENTER THIS CODE")
                            .font(.caption).bold().tracking(2)
                            .foregroundStyle(.secondary)
                        Text(code)
                            .font(.system(size: 40, weight: .heavy, design: .monospaced))
                            .tracking(4)
                    }
                    .padding(.top, 20)
                }

                if let error {
                    Text(error).foregroundStyle(.orange).padding(.top, 12)
                }
                if expired {
                    Text("Code expired — generating a new one…")
                        .foregroundStyle(.secondary).padding(.top, 8)
                }
            }
            .frame(maxWidth: 420, alignment: .leading)

            // The QR stays on a solid white plate: a translucent code is a code a
            // camera can't read.
            ZStack {
                RoundedRectangle(cornerRadius: 24).fill(.white)
                if let url = start?.verifyURL, let img = qrImage(url.absoluteString) {
                    Image(uiImage: img)
                        .interpolation(.none)
                        .resizable()
                        .frame(width: 260, height: 260)
                } else {
                    ProgressView()
                }
            }
            .frame(width: 320, height: 320)
        }
        .padding(48)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .glassBackgroundEffect()
        .task { await begin() }
        .onDisappear { pollTask?.cancel() }
    }

    private func step(_ s: String) -> some View {
        Text(s).font(.title3).foregroundStyle(.secondary)
    }

    private func begin() async {
        error = nil
        expired = false
        do {
            let s = try await DeviceCodeAuth.start(machineName: "Apple Vision Pro")
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
