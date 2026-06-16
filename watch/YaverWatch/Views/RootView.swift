// RootView.swift — the whole interaction in one screen: raise-to-record → speak
// → send → render ONE line + haptic. An async "working…" state while a long task
// runs on the runner. The result sentence is big and legible. No code, no diffs,
// no scrolling output (docs/yaver-smartwatch-voice-terminal.md §0/§5).
//
// Flow:
//   tap mic  → Dictation.dictate() → store.sendTranscript(text)
//            → reply reduces to lastLine + haptic
//   if reply is confirm-needed → ConfirmView sheet (store.pendingConfirm)
//   if reply is working → spinner until the phone/agent wakes us with a summary

import SwiftUI

struct RootView: View {
    @EnvironmentObject var store: WatchStore
    @State private var showSettings = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 14) {
                    resultLine
                    recordButton
                    transportHint
                }
                .padding(.horizontal, 6)
                .padding(.top, 4)
            }
            .navigationTitle("Yaver")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { showSettings = true } label: { Image(systemName: "gearshape") }
                }
            }
            // Confirm-gated writes/deploys: the PHONE decided this needs a yes.
            .sheet(item: confirmBinding) { pending in
                ConfirmView(prompt: pending.prompt) { reply in
                    Task { await store.sendConfirm(token: pending.token, reply: reply) }
                }
            }
            .sheet(isPresented: $showSettings) { SettingsView() }
            // Fold phone→watch background pushes (task-completion wake) into the
            // same reduce path as a direct reply.
            .onReceive(store.phone.$lastPushedReply.compactMap { $0 }) { reply in
                store.absorb(reply)
            }
        }
    }

    // The one-glance line. Big and legible; the only thing the watch "shows".
    private var resultLine: some View {
        Text(store.lastLine.isEmpty ? "Raise and speak a command." : store.lastLine)
            .font(.system(size: 19, weight: .semibold))
            .multilineTextAlignment(.center)
            .foregroundStyle(store.lastLine.isEmpty ? .secondary : .primary)
            .frame(maxWidth: .infinity, minHeight: 60)
            .fixedSize(horizontal: false, vertical: true)
    }

    @ViewBuilder
    private var recordButton: some View {
        switch store.phase {
        case .working:
            VStack(spacing: 8) {
                ProgressView()
                Text("Working… I'll buzz you when it's done.")
                    .font(.footnote).foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity, minHeight: 88)
        case .dispatching:
            ProgressView().frame(maxWidth: .infinity, minHeight: 88)
        case .idle, .listening:
            Button {
                Task { await record() }
            } label: {
                Label("Speak", systemImage: "mic.fill")
                    .font(.system(size: 20, weight: .bold))
                    .frame(maxWidth: .infinity, minHeight: 56)
            }
            .buttonStyle(.borderedProminent)
            .disabled(!store.canDispatch)
        }
    }

    private var transportHint: some View {
        Group {
            if store.phone.canUsePhone {
                label("iPhone", "iphone", .green)
            } else if store.standaloneOptIn && store.hasStandaloneCreds {
                label("Direct to box", "server.rack", .orange)
            } else {
                label("Open Yaver on phone", "iphone.slash", .secondary)
            }
        }
        .font(.footnote)
    }

    private func label(_ text: String, _ icon: String, _ color: Color) -> some View {
        HStack(spacing: 4) {
            Image(systemName: icon)
            Text(text)
        }
        .foregroundStyle(color)
    }

    // Bridge store.pendingConfirm (Equatable) to a sheet(item:) Identifiable.
    private var confirmBinding: Binding<IdentifiedConfirm?> {
        Binding(
            get: { store.pendingConfirm.map { IdentifiedConfirm(token: $0.token, prompt: $0.prompt) } },
            set: { if $0 == nil { store.pendingConfirm = nil } }
        )
    }

    private func record() async {
        store.phase = .listening
        guard let text = await Dictation.dictate(), !text.isEmpty else {
            store.phase = .idle
            return
        }
        await store.sendTranscript(text)
    }
}

/// Identifiable wrapper so a confirm prompt can drive `.sheet(item:)`.
struct IdentifiedConfirm: Identifiable, Equatable {
    let token: String
    let prompt: String
    var id: String { token }
}
