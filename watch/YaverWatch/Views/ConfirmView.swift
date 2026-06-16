// ConfirmView.swift — the confirm-gate UI. Every write/deploy/delete verb is
// confirm-gated (docs/yaver-smartwatch-voice-terminal.md §4): the PHONE (or
// agent, standalone) decided the command was risky and replied `confirm-needed`
// with a token + prompt. The watch does NOT assess risk itself — it just shows
// the prompt and sends back the user's choice as a `confirm` message carrying the
// SAME token.
//
// Wrist taps misfire easily, so Cancel is the low-friction default placement and
// Confirm is a deliberate, prominent action.

import SwiftUI

struct ConfirmView: View {
    let prompt: String
    /// Called with the user's choice; the caller sends it as a confirm message.
    let onChoice: (ConfirmReply) -> Void

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 30))
                    .foregroundStyle(.yellow)

                Text(prompt)
                    .font(.system(size: 18, weight: .semibold))
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)

                Button {
                    onChoice(.confirm)
                    dismiss()
                } label: {
                    Label("Confirm", systemImage: "checkmark")
                        .font(.system(size: 18, weight: .bold))
                        .frame(maxWidth: .infinity, minHeight: 48)
                }
                .buttonStyle(.borderedProminent)
                .tint(.green)

                Button(role: .cancel) {
                    onChoice(.cancel)
                    dismiss()
                } label: {
                    Label("Cancel", systemImage: "xmark")
                        .frame(maxWidth: .infinity, minHeight: 44)
                }
                .buttonStyle(.bordered)
            }
            .padding(.horizontal, 6)
        }
    }
}
