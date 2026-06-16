// AppleTVRemoteView.swift — D-pad + transport + now-playing card, and the home
// capture-card frame. All actions go through AgentClient → appletv_/capture_
// verbs on the selected box. Control + metadata is always-legal; the capture
// frame is the user's own non-protected source (the agent reports, never
// strips, HDCP — we just display whatever bytes it returns, incl. black).

import SwiftUI
import UIKit

struct AppleTVRemoteView: View {
    @EnvironmentObject var store: YaverStore
    var captureFirst: Bool = false

    @State private var np: NowPlaying?
    @State private var capture: CaptureStatus?
    @State private var frame: UIImage?
    @State private var status: String?
    @State private var refreshTask: Task<Void, Never>?

    var body: some View {
        HStack(alignment: .top, spacing: 48) {
            nowPlayingCard
            controls
        }
        .padding(56)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .task { await startLoop() }
        .onDisappear { refreshTask?.cancel() }
    }

    // ---- now playing / capture ----------------------------------------
    private var nowPlayingCard: some View {
        VStack(alignment: .leading, spacing: 16) {
            if let frame {
                Image(uiImage: frame)
                    .resizable().aspectRatio(contentMode: .fit)
                    .frame(maxWidth: 640)
                    .clipShape(RoundedRectangle(cornerRadius: 16))
            } else if let art = artworkImage {
                Image(uiImage: art)
                    .resizable().aspectRatio(contentMode: .fit)
                    .frame(width: 220, height: 220)
                    .clipShape(RoundedRectangle(cornerRadius: 12))
            } else {
                RoundedRectangle(cornerRadius: 12)
                    .fill(.thinMaterial).frame(width: 220, height: 220)
                    .overlay(Image(systemName: "tv").font(.system(size: 64)).foregroundStyle(.secondary))
            }

            Text(np?.title ?? "Nothing playing").font(.system(size: 30, weight: .bold))
            if let artist = np?.artist, !artist.isEmpty {
                Text(artist).font(.system(size: 22)).foregroundStyle(.secondary)
            }
            if let app = np?.app, !app.isEmpty {
                Text(app).font(.system(size: 18)).foregroundStyle(.tertiary)
            }
            if let hint = capture?.blackHint {
                Text(hint).font(.system(size: 15)).foregroundStyle(.secondary)
            }
            if let status { Text(status).font(.system(size: 15)).foregroundStyle(.orange) }
        }
        .frame(maxWidth: 680, alignment: .leading)
    }

    private var artworkImage: UIImage? {
        guard let b64 = np?.artworkB64, let data = Data(base64Encoded: b64) else { return nil }
        return UIImage(data: data)
    }

    // ---- D-pad + transport --------------------------------------------
    private var controls: some View {
        VStack(spacing: 28) {
            VStack(spacing: 16) {
                keyButton("chevron.up", .up)
                HStack(spacing: 16) {
                    keyButton("chevron.left", .left)
                    keyButton("circle.fill", .select)
                    keyButton("chevron.right", .right)
                }
                keyButton("chevron.down", .down)
            }
            HStack(spacing: 16) {
                keyButton("backward.fill", .previous)
                keyButton("playpause.fill", .playPause)
                keyButton("forward.fill", .next)
            }
            HStack(spacing: 16) {
                keyButton("line.3.horizontal", .menu)
                keyButton("house.fill", .home)
            }
        }
    }

    private func keyButton(_ icon: String, _ key: RemoteKey) -> some View {
        Button {
            Task { await send(key) }
        } label: {
            Image(systemName: icon)
                .font(.system(size: 28))
                .frame(width: 92, height: 72)
        }
    }

    // ---- actions ------------------------------------------------------
    private func send(_ key: RemoteKey) async {
        guard let client = store.client() else { return }
        do {
            if key == .playPause || key == .next || key == .previous {
                try await client.transport(key)
            } else {
                try await client.sendKey(key)
            }
            await refresh(client)
        } catch {
            status = error.localizedDescription
        }
    }

    private func startLoop() async {
        guard let client = store.client() else { status = "No box selected"; return }
        await refresh(client)
        refreshTask?.cancel()
        refreshTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 3_000_000_000)
                if Task.isCancelled { return }
                await refresh(client)
            }
        }
    }

    private func refresh(_ client: AgentClient) async {
        if let n = try? await client.nowPlaying() { np = n }
        if let c = try? await client.captureStatus() {
            capture = c
            if c.running || captureFirst, let data = try? await client.frameData(), let img = UIImage(data: data) {
                frame = img
            }
        }
    }
}
