// VisionCodingPreferencesView.swift — compact Convex-backed coding defaults.
//
// The live machine supplies runner/provider/model availability; Convex keeps
// only the user's non-secret preference. TaskComposerView is shared with tvOS
// and consumes the same YaverStore maps, so a headset selection affects the
// next vibe without a copied dispatch implementation.

import SwiftUI

struct VisionCodingPreferencesView: View {
    @EnvironmentObject private var store: YaverStore
    @Environment(\.dismiss) private var dismiss

    @State private var runners: [AgentRunnerSummary] = []
    @State private var loading = true
    @State private var saving = false
    @State private var notice: String?
    @State private var error: String?
    @State private var privateVPSDraft = Backend.privateVPSURL?.absoluteString ?? ""

    private var deviceId: String? { store.runnerBox()?.id }

    private var selectedRunnerId: String? {
        guard let deviceId else { return nil }
        return store.primaryRunnerByDevice[deviceId]
            ?? runners.first(where: \.isDefault)?.canonicalId
            ?? runners.first?.canonicalId
    }

    private var selectedRunner: AgentRunnerSummary? {
        guard let selectedRunnerId else { return nil }
        return runners.first { $0.canonicalId == selectedRunnerId }
    }

    private var providerChoices: [String] {
        guard selectedRunnerId == "opencode" else { return [] }
        return Array(Set(selectedRunner?.models.compactMap(\.provider) ?? [])).sorted()
    }

    private var selectedProvider: String? {
        guard selectedRunnerId == "opencode", let deviceId else { return nil }
        let savedModel = store.primaryModelByDevice[deviceId]
        return store.primaryProviderByDevice[deviceId]
            ?? selectedRunner?.models.first(where: { $0.id == savedModel })?.provider
            ?? selectedRunner?.models.first(where: { $0.isDefault == true })?.provider
            ?? selectedRunner?.models.first?.provider
    }

    private var modelChoices: [AgentRunnerModel] {
        guard let selectedRunner else { return [] }
        guard selectedRunnerId == "opencode", let selectedProvider else { return selectedRunner.models }
        return selectedRunner.models.filter { $0.provider == selectedProvider }
    }

    private var selectedModel: AgentRunnerModel? {
        let saved = deviceId.flatMap { store.primaryModelByDevice[$0] }
        return modelChoices.first(where: { $0.id == saved })
            ?? modelChoices.first(where: { $0.isDefault == true })
            ?? modelChoices.first
    }

    private var reasoningChoices: [String] {
        selectedModel?.supportedReasoningEfforts?.map(\.reasoningEffort) ?? []
    }

    private var selectedEffort: String? {
        let saved = deviceId.flatMap { store.primaryReasoningEffortByDevice[$0] }
        return saved.flatMap { reasoningChoices.contains($0) ? $0 : nil }
            ?? selectedModel?.defaultReasoningEffort
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Private VPS URL") {
                    TextField("https://vps.example.com", text: $privateVPSDraft)
                        .textContentType(.URL)
                    HStack {
                        Button("Use this VPS") { savePrivateVPS() }.disabled(privateVPSDraft.isEmpty)
                        if Backend.privateVPSURL != nil {
                            Button("Use Yaver hosted", role: .destructive) {
                                store.signOutForServerSwitch()
                                Backend.setPrivateVPSURL(nil)
                                privateVPSDraft = ""
                            }
                        }
                    }
                    Text("Local to this headset. Use your phone to read the VPS QR, then enter the URL here. Switching servers signs out this headset.")
                        .font(.caption).foregroundStyle(.secondary)
                }

                Section {
                    if loading {
                        ProgressView("Reading this machine…")
                    } else if runners.isEmpty {
                        ContentUnavailableView(
                            "No coding runners",
                            systemImage: "desktopcomputer.trianglebadge.exclamationmark",
                            description: Text("Connect the runner machine and try again."))
                    } else {
                        preferenceRow("Runner", value: selectedRunner?.displayName ?? "Choose") {
                            ForEach(runners) { runner in
                                Button(runner.displayName) { Task { await chooseRunner(runner) } }
                            }
                        }

                        if selectedRunnerId == "opencode", !providerChoices.isEmpty {
                            preferenceRow("Provider", value: selectedProvider ?? "Choose") {
                                ForEach(providerChoices, id: \.self) { provider in
                                    Button(provider) { Task { await chooseProvider(provider) } }
                                }
                            }
                        }

                        if !modelChoices.isEmpty {
                            preferenceRow("Model", value: selectedModel?.name ?? "Runner default") {
                                ForEach(modelChoices) { model in
                                    Button(model.name) { Task { await chooseModel(model) } }
                                }
                            }
                        }

                        if !reasoningChoices.isEmpty {
                            preferenceRow("Reasoning", value: reasoningLabel(selectedEffort ?? "medium")) {
                                ForEach(reasoningChoices, id: \.self) { effort in
                                    Button(reasoningLabel(effort)) { Task { await chooseReasoning(effort) } }
                                }
                            }
                        }
                    }
                } header: {
                    Text("Favorite coding setup")
                } footer: {
                    Text("Options come from Convex and the selected machine. API keys remain on that machine.")
                }

                if let notice {
                    Label(notice, systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                }
                if let error {
                    Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                }
            }
            .navigationTitle("Coding")
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
        .frame(minWidth: 680, minHeight: 560)
        .task { await load() }
    }

    private func preferenceRow<Choices: View>(
        _ label: String,
        value: String,
        @ViewBuilder choices: () -> Choices
    ) -> some View {
        LabeledContent(label) {
            Menu(value) { choices() }
                .disabled(saving)
        }
    }

    private func load() async {
        loading = true
        defer { loading = false }
        guard let client = store.runnerClient() else {
            error = "Connect the runner machine to load its coding options."
            return
        }
        do {
            runners = try await client.listRunners().runners.filter(\.installed)
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func savePrivateVPS() {
        guard let url = Backend.normalizedPrivateVPSURL(privateVPSDraft) else {
            error = "Enter a valid HTTPS URL without credentials, query text, or a fragment."
            return
        }
        store.signOutForServerSwitch()
        Backend.setPrivateVPSURL(url)
        privateVPSDraft = url.absoluteString
        notice = "Private VPS saved — sign in again"
    }

    private func chooseRunner(_ runner: AgentRunnerSummary) async {
        let model = runner.models.first(where: { $0.isDefault == true }) ?? runner.models.first
        await save(
            runner: runner.canonicalId,
            model: model,
            effort: runner.canonicalId == "codex" ? (model?.defaultReasoningEffort ?? "medium") : nil,
            provider: runner.canonicalId == "opencode" ? model?.provider : nil,
            message: "Favorite runner saved")
    }

    private func chooseProvider(_ provider: String) async {
        let choices = selectedRunner?.models.filter { $0.provider == provider } ?? []
        let model = choices.first(where: { $0.isDefault == true }) ?? choices.first
        await save(runner: "opencode", model: model, effort: nil, provider: provider,
                   message: "OpenCode provider saved")
    }

    private func chooseModel(_ model: AgentRunnerModel) async {
        guard let runner = selectedRunnerId else { return }
        let effort = runner == "codex"
            ? selectedEffort.flatMap { reasoningChoices.contains($0) ? $0 : nil }
                ?? model.defaultReasoningEffort ?? "medium"
            : nil
        await save(runner: runner, model: model, effort: effort,
                   provider: runner == "opencode" ? model.provider : nil,
                   message: "Favorite model saved")
    }

    private func chooseReasoning(_ effort: String) async {
        guard let runner = selectedRunnerId, let model = selectedModel else { return }
        await save(runner: runner, model: model, effort: effort,
                   provider: runner == "opencode" ? model.provider : nil,
                   message: "Reasoning preference saved")
    }

    private func save(
        runner: String,
        model: AgentRunnerModel?,
        effort: String?,
        provider: String?,
        message: String
    ) async {
        guard let deviceId else { return }
        saving = true
        notice = nil
        error = nil
        defer { saving = false }
        do {
            try await store.setPrimaryRunnerPreference(
                runner, model: model?.id, reasoningEffort: effort,
                provider: provider, for: deviceId)
            notice = message
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func reasoningLabel(_ effort: String) -> String {
        switch effort {
        case "xhigh": return "Extra high"
        case "max": return "More reasoning"
        default: return effort.capitalized
        }
    }
}
