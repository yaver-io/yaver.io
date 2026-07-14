// AddBoxView.swift — register the box (the machine running `yaver serve`).
//
// Lives in the SHARED client layer, not in tvos/YaverTV/Views/, because it is
// the only thing in the entire repo that calls store.addBox(). While it sat
// inside DashboardView.swift — a tvOS-only view file that visionOS does not
// compile — the Vision Pro app had a "No machine selected" screen with no way
// to add a machine. Its empty state told the user to "pick a box in the Yaver
// phone app — it syncs here", which was false: boxes live in
// @AppStorage("yaver.tv.boxes"), a per-app-container UserDefaults on a
// different physical device. Nothing syncs. A fresh install could never reach
// the dashboard.
//
// Keep this file platform-neutral (plain SwiftUI + YaverStore) so both the TV
// and the headset can present it. See visionos/project.yml, which pulls it in
// by path alongside the rest of the shared layer.

import SwiftUI

struct AddBoxView: View {
    @EnvironmentObject var store: YaverStore
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var host = ""
    @State private var machineId = ""

    var body: some View {
        VStack(spacing: 24) {
            Text("Add a box").font(.system(size: 34, weight: .bold))
            TextField("Name (e.g. magara)", text: $name)
            TextField("LAN host or IP (e.g. 192.168.1.20)", text: $host)
            TextField("Machine ID (managed cloud box — optional, enables Wake)", text: $machineId)
            Button("Save") {
                let trimmed = host.trimmingCharacters(in: .whitespaces)
                guard !trimmed.isEmpty else { return }
                let mid = machineId.trimmingCharacters(in: .whitespaces)
                let box = BoxTarget(id: trimmed, name: name.isEmpty ? trimmed : name, host: trimmed,
                                    managed: mid.isEmpty ? nil : true,
                                    machineId: mid.isEmpty ? nil : mid)
                store.addBox(box)
                store.select(box)
                dismiss()
            }
            .disabled(host.trimmingCharacters(in: .whitespaces).isEmpty)
            Button("Cancel", role: .cancel) { dismiss() }
        }
        .padding(64)
        .frame(maxWidth: 900)
    }
}
