// WebPreviewStreamView.swift — render a web project on the TV as a pixel stream.
//
// tvOS has no WebKit, so a web app can't run in-process. The box captures it
// headless at the chosen viewport (phone/tablet/desktop) and the TV polls the
// frames. A Rebuild button re-triggers the box's reload and the stream keeps
// flowing — the vibe loop, lean-back: watch → tweak (on your machine) → rebuild
// → watch again, on the big screen.

import SwiftUI
import UIKit

struct WebPreviewStreamView: View {
    @EnvironmentObject var store: YaverStore
    let project: ProjectSummary
    let form: PreviewForm

    @State private var frame: UIImage?
    @State private var status = "Starting preview…"
    @State private var error: String?
    @State private var started = false
    @State private var pollTask: Task<Void, Never>?
    @State private var rebuilding = false

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            if let frame {
                Image(uiImage: frame).resizable().aspectRatio(contentMode: .fit).padding(24)
            } else if let error {
                VStack(spacing: 16) {
                    Image(systemName: "globe.badge.chevron.backward").font(.system(size: 56)).foregroundStyle(.secondary)
                    Text("Preview unavailable").font(.title2)
                    Text(error).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 680)
                    Button("Try again") { restart() }
                }
            } else {
                VStack(spacing: 14) { ProgressView().scaleEffect(1.5); Text(status).foregroundStyle(.secondary) }
            }

            VStack {
                HStack(spacing: 14) {
                    Label("\(project.name) · \(form.rawValue)", systemImage: form.icon)
                        .font(.system(size: 16, weight: .semibold))
                        .padding(.horizontal, 14).padding(.vertical, 8)
                        .background(.ultraThinMaterial, in: Capsule())
                    Spacer()
                    Button { Task { await rebuild() } } label: {
                        Label(rebuilding ? "Rebuilding…" : "Rebuild", systemImage: "arrow.triangle.2.circlepath")
                            .font(.system(size: 16, weight: .semibold))
                    }
                    .disabled(rebuilding)
                }
                .padding(32)
                Spacer()
            }
        }
        .onAppear { if !started { restart() } }
        .onDisappear {
            pollTask?.cancel()
            Task { await store.client()?.stopWebPreview(project: project.name) }
        }
    }

    private func restart() {
        error = nil
        started = true
        pollTask?.cancel()
        pollTask = Task { await run() }
    }

    private func run() async {
        guard let client = store.client() else { error = "No machine selected"; return }
        do {
            status = "Booting the web server…"
            let server = try await client.startWebServer()
            // Prefer the server's own URL; fall back to a conventional dev port.
            let target = server.webUrl.map { "http://\(store.selectedBox?.host ?? "127.0.0.1"):\(server.port ?? 0)\($0)" }
                ?? "http://127.0.0.1:3000"
            status = "Capturing at \(form.rawValue) size…"
            try await client.startWebPreview(project: project.name, targetUrl: target,
                                              width: form.width, height: form.height)
            await poll(client)
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func poll(_ client: AgentClient) async {
        var lastHash = ""
        var misses = 0
        while !Task.isCancelled {
            do {
                let meta = try await client.previewSnapshot(project: project.name)
                if let hash = meta.hash, !hash.isEmpty, hash != lastHash {
                    let data = try await client.previewFrame(hash: hash)
                    if let img = UIImage(data: data) { frame = img; lastHash = hash; error = nil; misses = 0 }
                }
            } catch {
                misses += 1
                if misses >= 4 && frame == nil { self.error = error.localizedDescription }
            }
            try? await Task.sleep(nanoseconds: 700_000_000)
        }
    }

    private func rebuild() async {
        guard let client = store.client() else { return }
        rebuilding = true
        defer { rebuilding = false }
        do {
            _ = try await client.call("reload", ["mode": "dev"])
        } catch {
            self.error = error.localizedDescription
        }
    }
}
