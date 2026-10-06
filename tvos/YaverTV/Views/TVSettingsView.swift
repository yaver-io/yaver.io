// TVSettingsView.swift — cross-surface defaults, kept out of Chat.
//
// These values are the shared /settings rows used by web and mobile. The TV
// does not maintain a parallel preference model: choosing a primary machine,
// runner, project or MCP here changes what the next one-step vibe uses on every
// client surface.

import SwiftUI

struct TVSettingsView: View {
    @EnvironmentObject private var store: YaverStore
    @Environment(\.dismiss) private var dismiss

    @State private var devices: [RegisteredDevice] = []
    @State private var projects: [ProjectSummary] = []
    @State private var mcpServers: [String] = []
    @State private var liveRunners: [AgentRunnerSummary] = []
    @State private var loading = true
    @State private var saving = false
    @State private var error: String?
    @State private var savedMessage: String?
    @State private var showSignOutConfirmation = false
    @State private var activePicker: SettingsPicker?

    private enum SettingsPicker: String, Identifiable {
        case device, runner, provider, model, reasoning, project, mcp
        var id: String { rawValue }
    }

    private var deviceId: String? {
        store.primaryDeviceId ?? store.selectedBox?.id
    }

    private var selectedDevice: RegisteredDevice? {
        guard let deviceId else { return nil }
        return devices.first(where: { $0.deviceId == deviceId })
    }

    private var canReadRuntimeOptions: Bool {
        guard let deviceId else { return false }
        return deviceId == store.runnerBox()?.id
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    intro

                    NavigationLink(destination: PrivateVPSSettingsView().environmentObject(store)) {
                        HStack(spacing: 20) {
                            Image(systemName: "server.rack")
                                .font(.system(size: 26, weight: .semibold))
                                .foregroundStyle(.blue)
                                .frame(width: 48, height: 48)
                                .background(Color.blue.opacity(0.14), in: RoundedRectangle(cornerRadius: 12))
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Private VPS URL").font(.system(size: 22, weight: .semibold))
                                Text(Backend.privateVPSURL?.absoluteString ?? "Yaver hosted server")
                                    .font(.system(size: 15)).foregroundStyle(.secondary).lineLimit(1)
                            }
                            Spacer(minLength: 30)
                            Text("Open").font(.system(size: 17, weight: .semibold))
                        }
                        .padding(.horizontal, 24)
                        .padding(.vertical, 18)
                        .frame(maxWidth: .infinity, minHeight: 92, alignment: .leading)
                        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("settings.private-vps")

                    settingRow(
                        icon: store.appearanceTheme == "light" ? "sun.max.fill" : "moon.fill",
                        title: "Appearance",
                        detail: "Saved for this Apple TV"
                    ) { appearanceControl }

                    if loading {
                        HStack(spacing: 14) {
                            ProgressView()
                            Text("Loading shared defaults…").foregroundStyle(.secondary)
                        }
                        .padding(.vertical, 24)
                    } else {
                        settingRow(
                            icon: "desktopcomputer",
                            title: "Primary device",
                            detail: "The machine Yaver connects to first"
                        ) { deviceMenu }

                        settingRow(
                            icon: "cpu",
                            title: "Primary runner",
                            detail: "Used automatically when Chat starts a vibe"
                        ) { runnerMenu }

                        if selectedLiveRunner != nil {
                            if selectedRunnerId == "opencode", !providerChoices.isEmpty {
                                settingRow(
                                    icon: "shippingbox.fill",
                                    title: "Provider",
                                    detail: "OpenCode provider available on this machine"
                                ) { providerMenu }
                            }
                            settingRow(
                                icon: "sparkles",
                                title: "Favorite model",
                                detail: "Used for new vibes on this machine"
                            ) { modelMenu }
                        }

                        if selectedRunnerId == "codex", !reasoningChoices.isEmpty {
                            settingRow(
                                icon: "dial.medium",
                                title: "Reasoning",
                                detail: "How deeply Codex reasons by default"
                            ) { reasoningMenu }
                        }

                        settingRow(
                            icon: "folder.fill",
                            title: "Latest project",
                            detail: "Preselected in Chat and first in Vibing"
                        ) { projectMenu }

                        settingRow(
                            icon: "point.3.connected.trianglepath.dotted",
                            title: "MCP defaults",
                            detail: "Optional tools added to new vibes"
                        ) { mcpMenu }

                        if !canReadRuntimeOptions, deviceId != nil {
                            Label(
                                "Connect this primary device from Devices to refresh its live project and MCP lists.",
                                systemImage: "info.circle"
                            )
                            .font(.system(size: 15))
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 8)
                        }
                    }

                    if let savedMessage {
                        Label(savedMessage, systemImage: "checkmark.circle.fill")
                            .font(.system(size: 15, weight: .semibold))
                            .foregroundStyle(.green)
                            .padding(.horizontal, 8)
                    }
                    if let error {
                        Label(error, systemImage: "exclamationmark.triangle.fill")
                            .font(.system(size: 15))
                            .foregroundStyle(.orange)
                            .padding(.horizontal, 8)
                    }

                    registrationSection
                    accountSection
                }
                .padding(40)
            }
            .navigationTitle("Settings")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
        .task { await load() }
        .alert("Sign out of Yaver?", isPresented: $showSignOutConfirmation) {
            Button("Cancel", role: .cancel) {}
            Button("Sign out", role: .destructive) {
                store.signOut()
                dismiss()
            }
        } message: {
            Text("This Apple TV will forget its session and machine list. Your account and remote machines are not deleted.")
        }
        .sheet(item: $activePicker) { picker in
            pickerSheet(for: picker)
        }
    }

    private var accountSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Account")
                .font(.system(size: 24, weight: .bold))
            Text("Sign out only clears this Apple TV. Your account and remote machines stay intact.")
                .font(.system(size: 15))
                .foregroundStyle(.secondary)
                .frame(maxWidth: 760, alignment: .leading)
            HStack(spacing: 14) {
                Button("Sign out") { showSignOutConfirmation = true }
                    .buttonStyle(.bordered)
            }
        }
        .padding(24)
        .background(Color.red.opacity(0.08), in: RoundedRectangle(cornerRadius: 18))
        .accessibilityIdentifier("settings.account-actions")
    }

    private var registrationSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Registration & approvals")
                .font(.system(size: 24, weight: .bold))
            Text("Keep Apple TV focused. Use QR-first sign-in and secure handoff here; heavier third-party app management stays in the web UI and desktop app.")
                .font(.system(size: 15))
                .foregroundStyle(.secondary)
                .frame(maxWidth: 760, alignment: .leading)
            settingRow(
                icon: "iphone.and.arrow.forward",
                title: "Phone-approved sign-in",
                detail: "At TV sign-in, show the QR on this screen and approve it from your signed-in phone."
            ) {
                Text("QR at sign-in")
                    .font(.system(size: 17, weight: .semibold))
                    .foregroundStyle(.secondary)
            }
            NavigationLink(destination: SecureHandoffView()) {
                HStack(spacing: 20) {
                    Image(systemName: "qrcode.viewfinder")
                        .font(.system(size: 26, weight: .semibold))
                        .foregroundStyle(.blue)
                        .frame(width: 48, height: 48)
                        .background(Color.blue.opacity(0.14), in: RoundedRectangle(cornerRadius: 12))
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Secure credential handoff")
                            .font(.system(size: 22, weight: .semibold))
                        Text("Show or scan the encrypted QR to move credentials onto this Apple TV without typing.")
                            .font(.system(size: 15))
                            .foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 30)
                    Text("Open")
                        .font(.system(size: 17, weight: .semibold))
                }
                .padding(.horizontal, 24)
                .padding(.vertical, 18)
                .frame(maxWidth: .infinity, minHeight: 92, alignment: .leading)
                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("settings.secure-handoff")
            settingRow(
                icon: "rectangle.3.group.bubble.left",
                title: "Developer app management",
                detail: "Register apps, testers, and install approvals from the shared web UI or desktop GUI; tvOS stays uncluttered."
            ) {
                Text("Web / desktop")
                    .font(.system(size: 17, weight: .semibold))
                    .foregroundStyle(.secondary)
            }
        }
        .padding(24)
        .background(Color.blue.opacity(0.08), in: RoundedRectangle(cornerRadius: 18))
        .accessibilityIdentifier("settings.registration-approvals")
    }

    private var intro: some View {
        HStack(alignment: .top, spacing: 18) {
            VStack(alignment: .leading, spacing: 5) {
                Text("Defaults")
                    .font(.system(size: 30, weight: .bold))
                Text("Chat uses these silently. Vibing shows the latest project first, but waits for you to open it. Registration and approvals live one level deeper.")
                    .font(.system(size: 16))
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer(minLength: 12)
            Button { Task { await load() } } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 18, weight: .semibold))
                    .frame(width: 42, height: 42)
            }
            .buttonStyle(.bordered)
            .accessibilityLabel("Refresh settings")
            .disabled(loading || saving)
        }
        .padding(.bottom, 8)
    }

    private func settingRow<Control: View>(
        icon: String,
        title: String,
        detail: String,
        @ViewBuilder control: () -> Control
    ) -> some View {
        HStack(spacing: 20) {
            Image(systemName: icon)
                .font(.system(size: 26, weight: .semibold))
                .foregroundStyle(.blue)
                .frame(width: 48, height: 48)
                .background(Color.blue.opacity(0.14), in: RoundedRectangle(cornerRadius: 12))
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.system(size: 22, weight: .semibold))
                Text(detail).font(.system(size: 15)).foregroundStyle(.secondary)
            }
            Spacer(minLength: 30)
            control()
                .buttonStyle(.bordered)
                .frame(maxWidth: 420, alignment: .trailing)
        }
        .padding(.horizontal, 24)
        .padding(.vertical, 18)
        .frame(maxWidth: .infinity, minHeight: 92, alignment: .leading)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
        .focusSection()
    }

    private var deviceMenu: some View {
        Button { activePicker = .device } label: {
            VStack(alignment: .trailing, spacing: 3) {
                Text(selectedDevice?.realName ?? "Choose device")
                    .font(.system(size: 17, weight: .semibold))
                    .lineLimit(1)
                if let alias = selectedDevice?.aliasLabel {
                    Text(alias).font(.system(size: 13)).foregroundStyle(.secondary)
                }
            }
        }
        .disabled(saving || devices.isEmpty)
        .accessibilityIdentifier("settings.primary-device")
    }

    private var appearanceControl: some View {
        HStack(spacing: 10) {
            appearanceButton("Dark")
            appearanceButton("Light")
        }
        .accessibilityIdentifier("settings.appearance")
    }

    private func appearanceButton(_ label: String) -> some View {
        let value = label.lowercased()
        return Button(label) { Task { await chooseAppearance(value) } }
            .buttonStyle(.bordered)
            .tint(store.appearanceTheme == value ? .blue : .secondary)
            .disabled(saving)
            .accessibilityValue(store.appearanceTheme == value ? "Selected" : "Not selected")
    }

    private var runnerMenu: some View {
        let rows = availableRunners
        let selected = deviceId.flatMap { store.primaryRunnerByDevice[$0] }
        return Button { activePicker = .runner } label: {
            Text(runnerLabel(selected))
                .font(.system(size: 17, weight: .semibold))
                .lineLimit(1)
        }
        .disabled(saving || deviceId == nil || rows.isEmpty)
        .accessibilityIdentifier("settings.primary-runner")
    }

    private var selectedRunnerId: String? {
        deviceId.flatMap { store.primaryRunnerByDevice[$0] }.map(RegisteredRunner.canonical)
    }

    private var selectedLiveRunner: AgentRunnerSummary? {
        guard let selectedRunnerId else { return nil }
        return liveRunners.first(where: { $0.canonicalId == selectedRunnerId })
    }

    private var selectedLiveModel: AgentRunnerModel? {
        guard selectedLiveRunner != nil else { return nil }
        let saved = deviceId.flatMap { store.primaryModelByDevice[$0] }
        return availableModels.first(where: { $0.id == saved })
            ?? availableModels.first(where: { $0.isDefault == true })
            ?? availableModels.first
    }

    private var providerChoices: [String] {
        guard selectedRunnerId == "opencode" else { return [] }
        return Array(Set(selectedLiveRunner?.models.compactMap(\.provider) ?? [])).sorted()
    }

    private var selectedProvider: String? {
        guard selectedRunnerId == "opencode" else { return nil }
        return deviceId.flatMap { store.primaryProviderByDevice[$0] }
            ?? selectedLiveRunner?.models.first(where: {
                $0.id == deviceId.flatMap { store.primaryModelByDevice[$0] }
            })?.provider
            ?? selectedLiveRunner?.models.first(where: { $0.isDefault == true })?.provider
            ?? selectedLiveRunner?.models.first?.provider
    }

    private var availableModels: [AgentRunnerModel] {
        guard let runner = selectedLiveRunner else { return [] }
        guard selectedRunnerId == "opencode", let selectedProvider else { return runner.models }
        return runner.models.filter { $0.provider == selectedProvider }
    }

    private var providerMenu: some View {
        Button { activePicker = .provider } label: {
            Text(selectedProvider ?? "Choose provider")
                .font(.system(size: 17, weight: .semibold))
                .lineLimit(1)
        }
        .disabled(saving || providerChoices.isEmpty)
        .accessibilityIdentifier("settings.opencode-provider")
    }

    private var modelMenu: some View {
        Button { activePicker = .model } label: {
            Text(selectedLiveModel?.name ?? "Runner default")
                .font(.system(size: 17, weight: .semibold))
                .lineLimit(1)
        }
        .disabled(saving || availableModels.isEmpty)
        .accessibilityIdentifier("settings.favorite-model")
    }

    private var reasoningChoices: [String] {
        selectedLiveModel?.supportedReasoningEfforts?.map(\.reasoningEffort) ?? []
    }

    private var selectedReasoningEffort: String {
        let saved = deviceId.flatMap { store.primaryReasoningEffortByDevice[$0] }
        if let saved, reasoningChoices.contains(saved) { return saved }
        return selectedLiveModel?.defaultReasoningEffort ?? "medium"
    }

    private var reasoningMenu: some View {
        Button { activePicker = .reasoning } label: {
            Text(reasoningLabel(selectedReasoningEffort))
                .font(.system(size: 17, weight: .semibold))
        }
        .disabled(saving || reasoningChoices.isEmpty)
        .accessibilityIdentifier("settings.reasoning-effort")
    }

    private var projectMenu: some View {
        let remembered = store.lastProject(for: deviceId, projects: projects)
        let savedName = deviceId.flatMap { store.lastProjectByDevice[$0]?.projectName }
        return Button { activePicker = .project } label: {
            Text(remembered?.name ?? savedName ?? "No latest project")
                .font(.system(size: 17, weight: .semibold))
                .lineLimit(1)
        }
        .disabled(!canReadRuntimeOptions || projects.isEmpty)
        .accessibilityIdentifier("settings.latest-project")
    }

    private var mcpMenu: some View {
        let pref = deviceId.flatMap { store.lastMCPServersByDevice[$0] }
        let selected = Set(pref?.mcpServers ?? [])
        let includeYaver = pref?.includeYaverMcp ?? false
        let count = selected.count + (includeYaver ? 1 : 0)
        return Button { activePicker = .mcp } label: {
            Text(count == 0 ? "No MCP" : "\(count) enabled")
                .font(.system(size: 17, weight: .semibold))
                .lineLimit(1)
        }
        .disabled(!canReadRuntimeOptions)
        .accessibilityIdentifier("settings.latest-mcp")
    }

    @ViewBuilder
    private func pickerSheet(for picker: SettingsPicker) -> some View {
        NavigationStack {
            List {
                switch picker {
                case .device:
                    ForEach(devices) { device in
                        Button {
                            activePicker = nil
                            Task { await chooseDevice(device) }
                        } label: {
                            selectionLabel(device.realName, selected: device.deviceId == deviceId)
                        }
                    }
                case .runner:
                    ForEach(availableRunners) { runner in
                        Button {
                            activePicker = nil
                            Task { await chooseRunner(runner.id) }
                        } label: {
                            selectionLabel(runner.label, selected: runner.id == deviceId.flatMap { store.primaryRunnerByDevice[$0] })
                        }
                    }
                case .provider:
                    ForEach(providerChoices, id: \.self) { provider in
                        Button {
                            activePicker = nil
                            Task { await chooseProvider(provider) }
                        } label: {
                            selectionLabel(provider, selected: provider == selectedProvider)
                        }
                    }
                case .model:
                    ForEach(availableModels) { model in
                        Button {
                            activePicker = nil
                            Task { await chooseModel(model) }
                        } label: {
                            selectionLabel(model.name, selected: model.id == selectedLiveModel?.id)
                        }
                    }
                case .reasoning:
                    ForEach(reasoningChoices, id: \.self) { effort in
                        Button {
                            activePicker = nil
                            Task { await chooseReasoning(effort) }
                        } label: {
                            selectionLabel(reasoningLabel(effort), selected: effort == selectedReasoningEffort)
                        }
                    }
                case .project:
                    ForEach(projects) { project in
                        Button {
                            guard let deviceId else { return }
                            store.rememberProject(project, for: deviceId)
                            savedMessage = "Latest project saved"
                            error = nil
                            activePicker = nil
                        } label: {
                            selectionLabel(project.name, selected: project.id == store.lastProject(for: deviceId, projects: projects)?.id)
                        }
                    }
                case .mcp:
                    Button {
                        let pref = deviceId.flatMap { store.lastMCPServersByDevice[$0] }
                        saveMCP(Set(pref?.mcpServers ?? []), includeYaver: !(pref?.includeYaverMcp ?? false))
                    } label: {
                        selectionLabel("Yaver MCP", selected: deviceId.flatMap { store.lastMCPServersByDevice[$0]?.includeYaverMcp } ?? false)
                    }
                    ForEach(mcpServers, id: \.self) { name in
                        Button {
                            guard let deviceId else { return }
                            let current = Set(store.lastMCPServersByDevice[deviceId]?.mcpServers ?? [])
                            var next = current
                            if next.contains(name) { next.remove(name) } else { next.insert(name) }
                            saveMCP(next, includeYaver: store.lastMCPServersByDevice[deviceId]?.includeYaverMcp ?? false)
                        } label: {
                            selectionLabel(name, selected: deviceId.flatMap { store.lastMCPServersByDevice[$0]?.mcpServers?.contains(name) } ?? false)
                        }
                    }
                }
            }
            .navigationTitle(pickerTitle(picker))
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { activePicker = nil }
                }
            }
        }
        .frame(width: 900, height: 650)
    }

    private func selectionLabel(_ title: String, selected: Bool) -> some View {
        HStack {
            Text(title)
            Spacer()
            if selected { Image(systemName: "checkmark").foregroundStyle(.blue) }
        }
    }

    private func pickerTitle(_ picker: SettingsPicker) -> String {
        switch picker {
        case .device: return "Primary device"
        case .runner: return "Primary runner"
        case .provider: return "OpenCode provider"
        case .model: return "Favorite model"
        case .reasoning: return "Reasoning"
        case .project: return "Latest project"
        case .mcp: return "MCP defaults"
        }
    }

    private var availableRunners: [RegisteredRunner] {
        guard let device = selectedDevice else { return [] }
        var rows = (device.runners ?? []).filter { $0.installed != false }
        if rows.isEmpty {
            rows = (device.installedRunnerIds ?? []).map {
                RegisteredRunner(runnerId: $0, installed: true, ready: nil, authConfigured: nil, status: nil)
            }
        }
        var seen = Set<String>()
        return rows.filter { seen.insert($0.id).inserted }
    }

    private func runnerLabel(_ value: String?) -> String {
        guard let value, !value.isEmpty else { return "Choose runner" }
        switch RegisteredRunner.canonical(value) {
        case "claude": return "Claude Code"
        case "codex": return "Codex"
        case "opencode": return "OpenCode"
        default: return value
        }
    }

    private func reasoningLabel(_ effort: String) -> String {
        if effort == "xhigh" { return "Extra high" }
        if effort == "max" { return "More reasoning" }
        return effort.capitalized
    }

    private func chooseDevice(_ device: RegisteredDevice) async {
        saving = true
        error = nil
        savedMessage = nil
        defer { saving = false }
        do {
            try await store.setPrimaryDevice(device.deviceId)
            savedMessage = "Primary device saved"
            await loadRuntimeOptions()
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func chooseRunner(_ runner: String) async {
        guard let deviceId else { return }
        saving = true
        error = nil
        savedMessage = nil
        defer { saving = false }
        do {
            let canonical = RegisteredRunner.canonical(runner)
            let liveRunner = liveRunners.first(where: { $0.canonicalId == canonical })
            let model = liveRunner?.models.first(where: { $0.isDefault == true }) ?? liveRunner?.models.first
            let effort = canonical == "codex" ? (model?.defaultReasoningEffort ?? "medium") : nil
            try await store.setPrimaryRunnerPreference(
                canonical, model: model?.id, reasoningEffort: effort,
                provider: canonical == "opencode" ? model?.provider : nil, for: deviceId)
            savedMessage = "Primary runner saved"
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func chooseProvider(_ provider: String) async {
        guard let deviceId, selectedRunnerId == "opencode", let runner = selectedLiveRunner else { return }
        let models = runner.models.filter { $0.provider == provider }
        guard let model = models.first(where: { $0.isDefault == true }) ?? models.first else { return }
        saving = true
        error = nil
        savedMessage = nil
        defer { saving = false }
        do {
            try await store.setPrimaryRunnerPreference(
                "opencode", model: model.id, reasoningEffort: nil,
                provider: provider, for: deviceId)
            savedMessage = "OpenCode provider saved"
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func chooseModel(_ model: AgentRunnerModel) async {
        guard let deviceId, let runner = selectedRunnerId else { return }
        saving = true
        error = nil
        savedMessage = nil
        defer { saving = false }
        do {
            var effort: String?
            if runner == "codex" {
                let supported = model.supportedReasoningEfforts?.map(\.reasoningEffort) ?? []
                let current = store.primaryReasoningEffortByDevice[deviceId]
                effort = current.flatMap { supported.isEmpty || supported.contains($0) ? $0 : nil }
                    ?? model.defaultReasoningEffort
                    ?? "medium"
            }
            try await store.setPrimaryRunnerPreference(
                runner, model: model.id, reasoningEffort: effort,
                provider: runner == "opencode" ? model.provider : nil, for: deviceId)
            savedMessage = "Favorite model saved"
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func chooseReasoning(_ effort: String) async {
        guard let deviceId, selectedRunnerId == "codex", let model = selectedLiveModel else { return }
        saving = true
        error = nil
        savedMessage = nil
        defer { saving = false }
        do {
            try await store.setPrimaryRunnerPreference(
                "codex", model: model.id, reasoningEffort: effort,
                provider: nil, for: deviceId)
            savedMessage = "Reasoning preference saved"
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func chooseAppearance(_ theme: String) async {
        guard theme != store.appearanceTheme else { return }
        saving = true
        error = nil
        savedMessage = nil
        defer { saving = false }
        do {
            try await store.setAppearanceTheme(theme)
            savedMessage = "Appearance saved for this Apple TV"
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func saveMCP(_ selected: Set<String>, includeYaver: Bool) {
        guard let deviceId else { return }
        store.rememberMCPServers(Array(selected).sorted(), includeYaverMcp: includeYaver, for: deviceId)
        savedMessage = "MCP defaults saved"
        error = nil
    }

    private func load() async {
        loading = true
        error = nil
        savedMessage = nil
        do {
            async let deviceRows = MachineRegistry.fetch(token: store.token)
            async let settings = MachineRegistry.fetchSettings(token: store.token)
            let (loadedDevices, loadedSettings) = try await (deviceRows, settings)
            devices = loadedDevices
                .sorted { $0.realName.localizedCaseInsensitiveCompare($1.realName) == .orderedAscending }
            store.adoptSettings(loadedSettings, devices: loadedDevices)
            await loadRuntimeOptions()
        } catch {
            self.error = error.localizedDescription
        }
        loading = false
    }

    private func loadRuntimeOptions() async {
        projects = []
        mcpServers = []
        liveRunners = []
        guard canReadRuntimeOptions, let client = store.runnerClient() else { return }
        async let projectRows: [ProjectSummary]? = try? client.listProjects()
        async let mcpRows: [McpServerSummary]? = try? client.listMCPServers()
        async let runnerRows: AgentRunnerList? = try? client.listRunners()
        let (loadedProjects, loadedMCPs, loadedRunners) = await (projectRows, mcpRows, runnerRows)
        projects = (loadedProjects ?? []).sorted {
            $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
        }
        mcpServers = (loadedMCPs ?? []).map(\.name).sorted()
        liveRunners = (loadedRunners?.runners ?? []).filter(\.installed)
    }
}

private struct PrivateVPSSettingsView: View {
    @EnvironmentObject private var store: YaverStore
    @Environment(\.dismiss) private var dismiss
    @State private var draft = Backend.privateVPSURL?.absoluteString ?? ""
    @State private var error: String?
    @State private var showScanner = false

    var body: some View {
        VStack(alignment: .leading, spacing: 26) {
            Text("Private VPS URL").font(.largeTitle.bold())
            Text("Use your own Yaver backend on this Apple TV. The address stays in local UserDefaults and is never synced to your account.")
                .font(.title3).foregroundStyle(.secondary).frame(maxWidth: 900, alignment: .leading)
            HStack(spacing: 16) {
                Button("Scan QR") { showScanner = true }.buttonStyle(.borderedProminent)
                TextField("https://vps.example.com", text: $draft)
                    .textContentType(.URL)
                    .frame(maxWidth: 700)
            }
            if let error { Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange) }
            HStack(spacing: 16) {
                Button("Use this VPS") { save() }.buttonStyle(.borderedProminent).disabled(draft.isEmpty)
                if Backend.privateVPSURL != nil {
                    Button("Use Yaver hosted server", role: .destructive) {
                        store.signOutForServerSwitch()
                        Backend.setPrivateVPSURL(nil)
                        dismiss()
                    }
                }
            }
            Text("Switching servers signs out only this Apple TV. URLs containing credentials, query parameters, or fragments are rejected.")
                .font(.callout).foregroundStyle(.secondary)
            Spacer()
        }
        .padding(60)
        .sheet(isPresented: $showScanner) {
            ContinuityQRScannerView { value in
                if let url = Backend.privateVPSURL(fromQRCode: value) {
                    draft = url.absoluteString
                    error = nil
                } else {
                    error = "That QR doesn't contain a valid private VPS URL."
                }
            }
        }
    }

    private func save() {
        guard let url = Backend.normalizedPrivateVPSURL(draft) else {
            error = "Enter a valid HTTPS URL without credentials, query text, or a fragment."
            return
        }
        store.signOutForServerSwitch()
        Backend.setPrivateVPSURL(url)
        dismiss()
    }
}
