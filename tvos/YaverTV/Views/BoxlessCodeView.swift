import SwiftUI

/// Native Yaver Code for Apple surfaces when no runner box is selected.
///
/// This is intentionally a chat/audit surface. It never presents a fake
/// terminal and never claims to edit Git or run a build without a runner.
struct BoxlessCodeView: View {
    @Environment(\.dismiss) private var dismiss
    @EnvironmentObject private var store: YaverStore
    @State private var prompt = ""
    @State private var editingRequest = 0
    @State private var answer = ""
    @State private var busy = false
    @State private var error: String?
    @FocusState private var focus: Field?

    private enum Field: Hashable { case prompt, send }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack {
                Label("Remoteless fallback · analysis", systemImage: "sparkles")
                    .font(.system(size: 28, weight: .bold))
                Spacer()
                Button("Done") { dismiss() }
            }
            Text("remoteless.analysis-chat.supported · DeepSeek V4 Flash chat and deep audit without a remote box. Your primary/secondary machine automatically regains precedence when available. Git edits, shell commands, builds, tests, simulators, rendering, and deploys require a capable machine or VPS.")
                .font(.system(size: 17)).foregroundStyle(.secondary)
                .frame(maxWidth: 900, alignment: .leading)
            Label(
                BoxlessDeepSeekKeyStore.load().isEmpty
                    ? "No DeepSeek credential is available on this Apple TV yet."
                    : "DeepSeek credential is available from secure device storage.",
                systemImage: BoxlessDeepSeekKeyStore.load().isEmpty ? "iphone.and.arrow.forward" : "checkmark.shield"
            )
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("boxless.credential-status")
            // Shared dictation field (see YaverDictationField). The old
            // multiline SwiftUI axis was not a reliable dictation target on
            // tvOS; the bridge is single-line and claims the UIKit responder,
            // while the answer area carries the multiline content.
            YaverDictationField(
                text: $prompt,
                editingRequestID: editingRequest,
                onSubmit: { send(mode: "deep-audit") },
                placeholder: "Ask Yaver Code for a deep audit or explanation…",
                font: .systemFont(ofSize: 20),
                accessibilityIdentifier: "boxless.prompt"
            )
                .focused($focus, equals: .prompt)
                .frame(minHeight: 72)
                .padding(8)
                .background(Color.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
                .onAppear { InputStateReporter.shared.route = "boxless" }
            HStack {
                Button(busy ? "Thinking…" : "Deep audit / ask") { send(mode: "deep-audit") }
                    .buttonStyle(.borderedProminent)
                    .disabled(busy || prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .focused($focus, equals: .send)
                    .accessibilityIdentifier("boxless.send")
                if let error { Text(error).foregroundStyle(.orange).lineLimit(2) }
            }
            ScrollView {
                Text(answer.isEmpty ? "Your Yaver Code answer will appear here." : answer)
                    .font(.system(size: 18))
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(18)
            .background(Color.white.opacity(0.05), in: RoundedRectangle(cornerRadius: 14))
        }
        .padding(48)
        .frame(maxWidth: 1100, maxHeight: .infinity, alignment: .topLeading)
        .background(Color.black)
        .defaultFocus($focus, .prompt)
        .onAppear {
            // The Siri Remote microphone has no public button callback; it
            // only dictates into the currently focused text field.
            DispatchQueue.main.async {
                focus = .prompt
                editingRequest += 1
            }
        }
    }

    private func send(mode: String) {
        let text = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !busy else { return }
        guard store.remotelessAllowed else {
            error = "Remoteless is temporarily available only to the Yaver owner account."
            return
        }
        busy = true; error = nil; answer = ""
        Task {
            do {
                let key = BoxlessDeepSeekKeyStore.load()
                guard !key.isEmpty else {
                    throw BoxlessDeepSeekError.missingKey
                }
                let result = try await BoxlessDeepSeekClient(apiKey: key).answer(prompt: text, mode: mode)
                await MainActor.run { answer = result; busy = false }
            } catch {
                await MainActor.run { self.error = error.localizedDescription; busy = false }
            }
        }
    }
}
