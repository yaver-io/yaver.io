// WakeProgressView.swift — the "waking up" progress UX shown on the wrist while
// a self-parked box is being started back up. A horizontal bar + the current
// phase label + a small step indicator, tinted like the rest of the app.
//
// It renders the canonical ladder (BoxLifecycle / wakeMachine.ts):
//   Asleep → Waking → Restoring → Booting → Connecting → Online → Ready
// so the wrist reads the same as the phone, web, TV and CLI.

import SwiftUI

struct WakeProgressView: View {
    @ObservedObject var lifecycle: BoxLifecycle

    private var tint: Color { .orange } // matches the "direct to box" accent

    var body: some View {
        VStack(spacing: 8) {
            // Phase heading: icon + short label, with the percent on the right.
            HStack(spacing: 6) {
                Image(systemName: lifecycle.phase.symbol)
                    .foregroundStyle(tint)
                Text(lifecycle.phase.label)
                    .font(.system(size: 16, weight: .semibold))
                Spacer(minLength: 4)
                Text("\(lifecycle.phase.percent)%")
                    .font(.system(size: 13, weight: .medium).monospacedDigit())
                    .foregroundStyle(.secondary)
            }

            // The bar. Animated so the fill glides between phases.
            ProgressView(value: Double(lifecycle.phase.percent), total: 100)
                .tint(tint)
                .animation(.easeInOut(duration: 0.4), value: lifecycle.phase)

            // Step dots — one per wake step, filled as the ladder advances.
            HStack(spacing: 6) {
                ForEach(WakePhase.wakeSteps) { step in
                    Circle()
                        .fill(lifecycle.phase.percent >= step.percent ? tint : Color.secondary.opacity(0.3))
                        .frame(width: 6, height: 6)
                }
            }

            // The full sentence for the current phase (mirrors the phone).
            Text(lifecycle.phase.detail)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)

            // A hint when the wake can't proceed on its own (e.g. no phone).
            if let message = lifecycle.message {
                Text(message)
                    .font(.caption2)
                    .foregroundStyle(tint)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 4)
    }
}
