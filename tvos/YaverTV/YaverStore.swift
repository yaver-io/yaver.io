// YaverStore.swift — app-wide session + selected-box state, persisted in
// UserDefaults. The session token is the 1-year token from device-code auth
// (same contract as the phone). Kept deliberately small: this is a lean-back
// control app, not the full account surface.

import Foundation
import SwiftUI

@MainActor
final class YaverStore: ObservableObject {
    @AppStorage("yaver.tv.token") private var storedToken: String = ""
    @AppStorage("yaver.tv.boxes") private var storedBoxesJSON: String = "[]"
    @AppStorage("yaver.tv.selectedBox") private var selectedBoxId: String = ""

    @Published var token: String = ""
    @Published var boxes: [BoxTarget] = []
    @Published var selectedBox: BoxTarget?

    var isAuthenticated: Bool { !token.isEmpty }

    init() {
        token = storedToken
        boxes = (try? JSONDecoder().decode([BoxTarget].self, from: Data(storedBoxesJSON.utf8))) ?? []
        selectedBox = boxes.first(where: { $0.id == selectedBoxId }) ?? boxes.first
        refreshSessionOnLaunch()
    }

    /// Netflix-on-AppleTV contract: extend the 1-year session every launch so a
    /// signed-in TV never re-prompts for OAuth. No-op when signed out. See
    /// Backend.refreshSession for the extend-only (no-rotation) rationale.
    private func refreshSessionOnLaunch() {
        let current = storedToken
        guard !current.isEmpty else { return }
        Task { [weak self] in
            let rotated = await DeviceCodeAuth.refreshSession(token: current)
            guard let rotated, !rotated.isEmpty else { return }
            await MainActor.run {
                guard let self else { return }
                // Only adopt the rotated token if we're still on the same one —
                // the user may have signed out/in while the refresh was in flight.
                guard self.token == current else { return }
                self.token = rotated
                self.storedToken = rotated
            }
        }
    }

    func signIn(token: String) {
        self.token = token
        storedToken = token
    }

    func signOut() {
        token = ""
        storedToken = ""
        // Clear the machine list too. On a family Apple TV, leaving boxes behind
        // hands the next person the previous user's machine names and LAN IPs.
        boxes = []
        selectedBox = nil
        selectedBoxId = ""
        storedBoxesJSON = "[]"
    }

    /// Remove a box (a typo'd address, a decommissioned machine). Without this a
    /// bad entry was permanent — the dashboard could only ever ADD.
    func removeBox(_ box: BoxTarget) {
        boxes.removeAll { $0.id == box.id }
        if selectedBox?.id == box.id {
            selectedBox = boxes.first
            selectedBoxId = boxes.first?.id ?? ""
        }
        persistBoxes()
    }

    func addBox(_ box: BoxTarget) {
        if let idx = boxes.firstIndex(where: { $0.id == box.id }) {
            boxes[idx] = box
        } else {
            boxes.append(box)
        }
        persistBoxes()
        if selectedBox == nil { select(box) }
    }

    func select(_ box: BoxTarget) {
        selectedBox = box
        selectedBoxId = box.id
    }

    func client() -> AgentClient? {
        guard isAuthenticated, let box = selectedBox else { return nil }
        return AgentClient(token: token, box: box)
    }

    private func persistBoxes() {
        if let data = try? JSONEncoder().encode(boxes), let s = String(data: data, encoding: .utf8) {
            storedBoxesJSON = s
        }
    }
}
