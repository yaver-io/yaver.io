// WakeProgressView.swift — the "waking up" visual for the 10-foot TV UI.
//
// The tvOS mirror of mobile's WakeProgress: one large animated bar + the
// current phase sentence + a labelled step ladder + percent. Sized big for
// couch-distance reading and tinted like the app (green once the relay leg
// is up, cyan while still cold, red on error). Driven entirely by
// BoxLifecycle's derived state.

import SwiftUI

struct WakeProgressView: View {
    @ObservedObject var lifecycle: BoxLifecycle
    var boxName: String?

    private var tint: Color {
        if lifecycle.error != nil { return .red }
        return lifecycle.phase.isNetwork ? .green : .cyan
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            HStack(alignment: .firstTextBaseline) {
                Circle()
                    .fill(tint)
                    .frame(width: 16, height: 16)
                    .opacity(lifecycle.isRunning ? 1 : 0.7)
                Text(lifecycle.error ?? lifecycle.phase.label)
                    .font(.system(size: 30, weight: .semibold))
                    .foregroundStyle(lifecycle.error != nil ? Color.red : .primary)
                    .lineLimit(2)
                Spacer(minLength: 24)
                Text("\(lifecycle.percent)%")
                    .font(.system(size: 30, weight: .heavy, design: .rounded))
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            }

            // Big progress bar.
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    Capsule().fill(.white.opacity(0.12))
                    Capsule()
                        .fill(tint)
                        .frame(width: max(0, geo.size.width * CGFloat(lifecycle.percent) / 100))
                        .animation(.easeOut(duration: 0.55), value: lifecycle.percent)
                }
            }
            .frame(height: 18)

            // Step ladder.
            HStack(spacing: 0) {
                ForEach(BoxPhase.wakeSteps, id: \.self) { step in
                    StepDot(step: step, current: lifecycle.phase, percent: lifecycle.percent, error: lifecycle.error != nil)
                    if step != BoxPhase.wakeSteps.last {
                        Spacer(minLength: 0)
                    }
                }
            }

            Text(hint)
                .font(.system(size: 20))
                .foregroundStyle(.secondary)
                .lineLimit(2)
        }
        .padding(36)
        .frame(maxWidth: 1100, alignment: .leading)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 24))
    }

    private var hint: String {
        if let name = boxName, !name.isEmpty, lifecycle.phase.isNetwork {
            return "\(name) is coming up over the free relay — no re-auth needed."
        }
        if lifecycle.phase.isNetwork {
            return "Coming up over the free relay — no re-auth needed."
        }
        return "Recreating from the latest snapshot — about a minute."
    }
}

private struct StepDot: View {
    let step: BoxPhase
    let current: BoxPhase
    let percent: Int
    let error: Bool

    private var isDone: Bool { percent >= step.percent && current != step }
    private var isCurrent: Bool { current == step }

    private var color: Color {
        if error { return isCurrent ? .red : .secondary }
        if isDone { return .green }
        if isCurrent { return .cyan }
        return .secondary
    }

    var body: some View {
        VStack(spacing: 10) {
            ZStack {
                Circle()
                    .strokeBorder(color, lineWidth: 3)
                    .background(Circle().fill(isDone ? Color.green : .clear))
                    .frame(width: 30, height: 30)
                if isDone {
                    Image(systemName: "checkmark")
                        .font(.system(size: 14, weight: .black))
                        .foregroundStyle(.white)
                }
            }
            .scaleEffect(isCurrent ? 1.15 : 1)
            .animation(.easeInOut(duration: 0.3), value: isCurrent)
            Text(step.short)
                .font(.system(size: 18, weight: isCurrent ? .bold : .medium))
                .foregroundStyle(isCurrent ? Color.primary : .secondary)
                .lineLimit(1)
        }
        .frame(width: 150)
    }
}
