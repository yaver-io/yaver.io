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
    }

    func signIn(token: String) {
        self.token = token
        storedToken = token
    }

    func signOut() {
        token = ""
        storedToken = ""
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
