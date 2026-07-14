// FeedbackView.swift — review feedback the box collected, from the couch.
//
// The Feedback SDK captures video / voice / screenshots on the device under
// test and posts reports to the box. This lists them for lean-back review:
// source, transcript, app version, and how many screenshots / errors rode
// along. Opening a full report (video, per-shot) stays on phone/web where a
// touch UI fits; the TV is the "what came in" glance.

import SwiftUI
import AVKit

struct FeedbackView: View {
    @EnvironmentObject var store: YaverStore

    @State private var reports: [FeedbackReport] = []
    @State private var loading = true
    @State private var error: String?
    @State private var playing: FeedbackReport?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Image(systemName: "bubble.left.and.text.bubble.right").font(.system(size: 26)).foregroundStyle(.pink)
                Text("Feedback").font(.system(size: 30, weight: .bold))
                Spacer()
                Button { Task { await load() } } label: { Image(systemName: "arrow.clockwise") }
                    .disabled(loading)
            }
            .padding(.horizontal, 48).padding(.vertical, 20)

            Group {
                if loading {
                    ProgressView().scaleEffect(1.4).frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if let error {
                    VStack(spacing: 14) {
                        Text(error).foregroundStyle(.orange).multilineTextAlignment(.center)
                        Button("Try again") { Task { await load() } }
                    }.frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if reports.isEmpty {
                    VStack(spacing: 12) {
                        Image(systemName: "tray").font(.system(size: 56)).foregroundStyle(.secondary)
                        Text("No feedback yet").font(.title2)
                        Text("Reports from the Feedback SDK on your test devices show up here.")
                            .foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 620)
                    }.frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    ScrollView {
                        LazyVStack(spacing: 12) {
                            ForEach(reports) { r in row(r) }
                        }.padding(48)
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
        .task { await load() }
        .fullScreenCover(item: $playing) { report in
            FeedbackVideoPlayer(report: report, host: store.selectedBox?.host ?? "",
                                port: store.selectedBox?.port ?? 18080, token: store.token)
        }
    }

    private func row(_ r: FeedbackReport) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(r.source ?? "feedback").font(.system(size: 20, weight: .semibold))
                Spacer()
                if let v = r.appVersion { Text(v).font(.system(size: 15)).foregroundStyle(.secondary) }
            }
            if !r.safeTranscript.isEmpty {
                Text(r.safeTranscript).font(.system(size: 18)).foregroundStyle(.secondary).lineLimit(3)
            }
            HStack(spacing: 18) {
                if r.hasVideo {
                    Button { playing = r } label: {
                        Label("Play", systemImage: "play.circle.fill")
                            .font(.system(size: 16, weight: .semibold))
                    }
                    .buttonStyle(.borderedProminent)
                }
                if r.shotCount > 0 { pill("\(r.shotCount) shots", "photo", .blue) }
                if r.errorCount > 0 { pill("\(r.errorCount) errors", "exclamationmark.triangle", .red) }
                if let t = r.createdAt { Text(t).font(.system(size: 14)).foregroundStyle(.secondary) }
            }
        }
        .padding(24)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.gray.opacity(0.1), in: RoundedRectangle(cornerRadius: 16))
    }

    private func pill(_ text: String, _ icon: String, _ color: Color) -> some View {
        Label(text, systemImage: icon)
            .font(.system(size: 15, weight: .medium))
            .padding(.horizontal, 12).padding(.vertical, 6)
            .background(color.opacity(0.18), in: Capsule())
            .foregroundStyle(color)
    }

    private func load() async {
        loading = true
        error = nil
        do {
            guard let client = store.client() else { throw AgentError(message: "No machine selected") }
            reports = try await client.listFeedback()
        } catch {
            self.error = error.localizedDescription
        }
        loading = false
    }
}

/// Plays a feedback report's MP4 on the TV. AVPlayer plays MP4 natively (unlike
/// the agent's MJPEG streams), and the box's /feedback/{id}/video is bearer-
/// authed — so the asset carries the Authorization + surface headers. Watching a
/// bug report on the big screen is exactly what a TV is good for.
private struct FeedbackVideoPlayer: View {
    let report: FeedbackReport
    let host: String
    let port: Int
    let token: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VideoPlayer(player: makePlayer())
            .ignoresSafeArea()
            .overlay(alignment: .topLeading) {
                Button("Done") { dismiss() }.padding(32)
            }
    }

    private func makePlayer() -> AVPlayer {
        guard let url = URL(string: "http://\(host):\(port)/feedback/\(report.id)/video") else {
            return AVPlayer()
        }
        let asset = AVURLAsset(url: url, options: [
            "AVURLAssetHTTPHeaderFieldsKey": [
                "Authorization": "Bearer \(token)",
                "X-Yaver-Surface": "tv",
            ],
        ])
        let player = AVPlayer(playerItem: AVPlayerItem(asset: asset))
        player.play()
        return player
    }
}
