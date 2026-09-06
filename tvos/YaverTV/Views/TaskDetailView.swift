// TaskDetailView.swift — the LIVE console of a task, streamed from the box.
//
// This is the tvOS twin of mobile's LiveConsoleSection: it consumes the RAW
// runner stdout lane (/tasks/{id}/output?rawSince=…) so the couch sees the
// actual opencode/claude/codex console — $ prompts, banners, diffs — as it
// streams, not a collapsed "working…" summary. Buffering is per-task and
// capped at 512 KB (mirroring the agent's rawOutputMaxBytes); reattaching
// passes the authoritative byte cursor back as rawSince so a relay bounce
// resumes without duplicating the scrollback.
//
// Stream-end discipline (FailureSignals): a clean `done` frame is the only
// way the console may stop silently. Any other end — EOF, dropped tunnel,
// cancel — is named, and the view offers a Reattach button instead of
// freezing on the last frame.

import SwiftUI

struct TaskDetailView: View {
    @EnvironmentObject var store: YaverStore

    let task: TaskSummary

    @State private var console = ""
    @State private var live = false
    @State private var status: String?
    @State private var streamMessage: String?
    @State private var reattachNonce = 0
    @State private var stream: Task<Void, Never>?

    /// The agent's authoritative raw byte cursor — passed back as rawSince on
    /// reattach so a dropped stream never duplicates the scrollback. @State so
    /// the value survives the stream task's lifetime and the view struct's
    /// re-renders.
    @State private var rawCursor = 0

    private static let consoleCap = 512 * 1024

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            consolePane
            footer
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
        .task(id: reattachNonce) { await startStream() }
        .onDisappear { stream?.cancel() }
    }

    private var header: some View {
        HStack(spacing: 14) {
            statusDot(status ?? task.status)
            VStack(alignment: .leading, spacing: 3) {
                Text(task.safeTitle).font(.system(size: 22, weight: .semibold)).lineLimit(2)
                Text([task.runner, status ?? task.status].compactMap { $0 }.joined(separator: " · "))
                    .font(.system(size: 15)).foregroundStyle(.secondary)
            }
            Spacer()
            if live {
                // YouTube-style live indicator — the equalizer dances while the
                // runner is working, so a streamed run reads as alive even
                // between stdout bursts (replaces the static rotating orb,
                // 2026-08-13).
                HStack(spacing: 8) {
                    EqualizerBars(barCount: 4, color: .green, active: true)
                    Text("live").font(.system(size: 14, weight: .semibold)).foregroundStyle(.green)
                }
            }
        }
        .padding(.horizontal, 48).padding(.vertical, 20)
    }

    private var consolePane: some View {
        ScrollViewReader { proxy in
            ScrollView {
                // The user's submitted prompt — ALWAYS visible above the
                // stream, opencode-style `$` line. The runner echoes it into
                // the raw console too, but showing it explicitly means a
                // follow-up sent while streaming can never disappear from the
                // couch (2026-08-12 cross-surface parity: web/mobile/desktop
                // all render the submitted text; tvOS had only the raw echo).
                Text("$ \(task.safeTitle)")
                    .font(.system(size: 15, design: .monospaced))
                    .foregroundStyle(.green)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 48).padding(.top, 16)
                Text(cleanConsole.isEmpty ? "Waiting for output…" : cleanConsole)
                    .font(.system(size: 15, design: .monospaced))
                    .foregroundStyle(cleanConsole.isEmpty ? .secondary : .primary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .id("console-bottom")
                    .padding(.horizontal, 48).padding(.vertical, 16)
            }
            .onChange(of: console.count) { _, _ in
                withAnimation(.none) { proxy.scrollTo("console-bottom", anchor: .bottom) }
            }
        }
        .background(Color.black.opacity(0.6))
    }

    private var footer: some View {
        HStack(spacing: 14) {
            if let msg = streamMessage {
                HStack(spacing: 8) {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(msg).foregroundStyle(.orange)
                }
                .font(.system(size: 15))
                Button("Reattach") {
                    streamMessage = nil
                    reattachNonce += 1
                }
                .buttonStyle(.bordered)
            } else {
                Text("Streaming raw console · \(byteCount) buffered")
                    .font(.system(size: 14)).foregroundStyle(.secondary)
            }
            Spacer()
            if task.tmuxSession?.isEmpty == false {
                NavigationLink(destination: SessionView(preselect: task.tmuxSession)) {
                    Label("Drive session", systemImage: "terminal.fill")
                }
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(.horizontal, 48).padding(.vertical, 18)
    }

    private var byteCount: String {
        let kb = console.utf8.count / 1024
        return "\(kb) KB"
    }

    /// The raw runner stream keeps its ANSI/VT escapes (SGR colours, cursor
    /// moves, OSC titles) so a real terminal can paint it. A SwiftUI `Text`
    /// cannot — it would render the escapes as literal garbage on the couch.
    /// Strip CSI (ESC [ … final), OSC (ESC ] … BEL/ST) and two-byte controls;
    /// the monospaced console look is what matters here, not the colours.
    private var cleanConsole: String { Self.stripANSI(console) }

    private static func stripANSI(_ s: String) -> String {
        var out = ""
        var state = 0 // 0=text 1=esc 2=csi 3=osc
        for ch in s.unicodeScalars {
            switch state {
            case 0:
                if ch == "\u{1B}" { state = 1 } else { out.unicodeScalars.append(ch) }
            case 1:
                if ch == "[" { state = 2 }
                else if ch == "]" { state = 3 }
                else { state = 0 } // two-byte controls (e.g. ESC ( B) — drop
            case 2:
                // CSI: params (0x30…0x3F), intermediates (0x20…0x2F),
                // then a final byte (0x40…0x7E) ends the sequence.
                if ch.value >= 0x40 && ch.value <= 0x7E { state = 0 }
            case 3:
                if ch == "\u{07}" { state = 0 }            // BEL ends OSC
                else if ch == "\u{1B}" { state = 1 }       // possible ST (ESC \)
                else if ch == "\\" { state = 0 }           // ST ends OSC
            default:
                state = 0
            }
        }
        return out
    }

    private func statusDot(_ s: String?) -> some View {
        Circle().fill(color(for: s)).frame(width: 14, height: 14)
    }

    private func color(for s: String?) -> Color {
        switch (s ?? "").lowercased() {
        case "running": return .green
        case "queued": return .blue
        case "review": return .purple
        case "completed": return .gray
        case "failed", "stopped": return .red
        default: return .secondary
        }
    }

    private func startStream() async {
        stream?.cancel()
        guard let client = store.runnerClient() else {
            streamMessage = "No machine selected"
            return
        }
        // rawSince=0 seeds the console with the full retained raw tail
        // (raw_replay full=true); live raw frames append from there. Reattach
        // passes the last authoritative cursor.
        let since = rawCursor
        live = false
        let s = await client.subscribeTaskOutput(
            taskId: task.id,
            rawSince: since,
            onRaw: { text, offset, full in
                Task { @MainActor in
                    if full {
                        console = String(text.prefix(Self.consoleCap))
                    } else {
                        console = String((console + text).suffix(Self.consoleCap))
                    }
                    // The agent's byte cursor is authoritative for resume.
                    rawCursor = offset
                    live = !full
                }
            },
            onDone: { doneStatus in
                Task { @MainActor in
                    status = doneStatus
                    live = false
                }
            },
            onEnd: { kind, reason in
                Task { @MainActor in
                    live = false
                    switch kind {
                    case .done:
                        streamMessage = nil
                    case .cancelled:
                        streamMessage = nil
                    case .interrupted:
                        streamMessage = reason ?? "the console stream stopped"
                    }
                }
            }
        )
        stream = s
    }
}
