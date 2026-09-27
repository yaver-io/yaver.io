// VibingView.swift — choose a project, then open its live preview.
//
// The old dashboard split "Projects" (where previews lived) from "Vibing"
// (a terminal session). That made the user's intended loop impossible to see:
// pick SFMG → watch the stream while continuing the vibe. The remembered
// project leads the searchable grid and receives focus, but entering Vibing
// never starts a stream by itself: one Select opens it, while the grid still
// gives the user a real chance to choose another rendered app.

import SwiftUI

struct VibingView: View {
    @EnvironmentObject var store: YaverStore

    @State private var projects: [ProjectSummary] = []
    @State private var selectedRepository: ProjectSummary?
    @State private var targets: [ProjectSummary] = []
    @State private var activePreview: ProjectSummary?
    @State private var form: PreviewForm = .phone
    @State private var loading = true
    @State private var loadingOptions = false
    @State private var error: String?
    @State private var searchQuery = ""
    @State private var rememberedProjectId: String?
    @State private var showingProjectStart = false
    @State private var startedTask: TaskSummary?
    @FocusState private var focusedProjectId: String?

    private let projectColumns = Array(
        repeating: GridItem(.flexible(minimum: 250, maximum: 420), spacing: 20, alignment: .top),
        count: 4
    )

    private var visibleProjects: [ProjectSummary] {
        tvVibingFilteredProjects(projects, query: searchQuery)
    }

    var body: some View {
        Group {
            if let target = activePreview {
                previewSurface(for: target)
            } else if let repository = selectedRepository {
                selectedProjectView(repository)
            } else {
                projectPicker
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
        .task { if projects.isEmpty { await loadProjects() } }
        .onChange(of: searchQuery) { _, _ in
            guard !visibleProjects.contains(where: { $0.id == focusedProjectId }) else { return }
            focusedProjectId = visibleProjects.first?.id
        }
        .sheet(isPresented: $showingProjectStart) {
            // ProjectStartView was removed when New Vibe became the native
            // keyboard-only TaskComposerView. Keeping the old symbol here
            // made the standalone tvOS archive fail even though Tasks and
            // Projects already used the replacement surface.
            TaskComposerView { task in
                showingProjectStart = false
                startedTask = task
            }
            .environmentObject(store)
        }
        .fullScreenCover(item: $startedTask) { task in
            TaskDetailView(task: task).environmentObject(store)
        }
    }

    private var projectPicker: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                // Vibing is the live preview/control surface, so give it a
                // screen/stream silhouette. Keep wand/sparkles for AI runner
                // actions such as "New vibe"; the two destinations should not
                // read as the same kind of screen from across the room.
                Image(systemName: "play.rectangle.fill").font(.system(size: 28)).foregroundStyle(.orange)
                VStack(alignment: .leading, spacing: 3) {
                    Text("Vibing").font(.system(size: 34, weight: .bold))
                    Text("Your latest project is ready first. Press Select to open it, or choose another.")
                        .font(.system(size: 16)).foregroundStyle(.secondary)
                }
                Spacer()
                TextField("Search apps", text: $searchQuery)
                    .textFieldStyle(.plain)
                    .padding(.horizontal, 18)
                    .frame(width: 330, height: 52)
                    .background(Color.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 14))
                    .accessibilityIdentifier("vibing.project-search")
                Button("Start a project") { showingProjectStart = true }
                Button { Task { await loadProjects() } } label: { Image(systemName: "arrow.clockwise") }
                    .disabled(loading)
            }
            .padding(.horizontal, 48).padding(.vertical, 24)

            Group {
                if loading {
                    center { ProgressView().scaleEffect(1.4) }
                } else if let error {
                    center {
                        VStack(spacing: 14) {
                            Text(error).foregroundStyle(.orange).multilineTextAlignment(.center)
                            Button("Try again") { Task { await loadProjects() } }
                        }
                    }
                } else if projects.isEmpty {
                    center {
                        VStack(spacing: 18) {
                            Text("No mobile, web, frontend, or TV apps were found on this machine.")
                                .foregroundStyle(.secondary)
                            Button("Start a project") { showingProjectStart = true }
                        }
                    }
                } else if visibleProjects.isEmpty {
                    center {
                        VStack(spacing: 14) {
                            Text(searchQuery.isEmpty
                                ? "No mobile, web, frontend, or TV apps were found on this machine."
                                : "No renderable app matches “\(searchQuery)”.")
                                .foregroundStyle(.secondary)
                            if !searchQuery.isEmpty {
                                Button("Clear search") { searchQuery = "" }
                            }
                        }
                    }
                } else {
                    ScrollView(.vertical, showsIndicators: false) {
                        LazyVGrid(columns: projectColumns, alignment: .leading, spacing: 20) {
                            ForEach(visibleProjects) { projectRow($0) }
                        }
                        .padding(.horizontal, 48)
                        .padding(.vertical, 24)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func projectRow(_ project: ProjectSummary) -> some View {
        let style = FrameworkStyle.of(tvVibingDisplayFramework(project))
        Button {
            Task { await openProject(project) }
        } label: {
            VStack(alignment: .leading, spacing: 18) {
                HStack {
                    Image(systemName: style.symbol)
                        .font(.system(size: 32))
                        .foregroundStyle(style.color)
                        .frame(width: 48, height: 48)
                    Spacer()
                    if project.id == rememberedProjectId {
                        Text("LATEST")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundStyle(.blue)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 6)
                            .background(Color.blue.opacity(0.16), in: Capsule())
                    }
                }
                VStack(alignment: .leading, spacing: 5) {
                    Text(project.name)
                        .font(.system(size: 24, weight: .semibold))
                        .lineLimit(1)
                        .truncationMode(.tail)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Text(projectSummary(project))
                        .font(.system(size: 15))
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                Spacer()
                HStack(spacing: 7) {
                    ForEach(tvVibingCapabilityLabels(project), id: \.self) { label in
                        Text(label.uppercased())
                            .font(.system(size: 11, weight: .bold))
                            .foregroundStyle(style.color)
                            .padding(.horizontal, 9)
                            .padding(.vertical, 5)
                            .background(style.color.opacity(0.14), in: Capsule())
                    }
                    Spacer()
                    Image(systemName: "play.fill")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundStyle(.secondary)
                }
            }
            .padding(22)
            .frame(maxWidth: .infinity, minHeight: 205, maxHeight: 205, alignment: .leading)
            .background(
                focusedProjectId == project.id ? Color.white.opacity(0.16) : Color.white.opacity(0.07),
                in: RoundedRectangle(cornerRadius: 20)
            )
            .overlay {
                RoundedRectangle(cornerRadius: 20)
                    .stroke(focusedProjectId == project.id ? style.color : Color.white.opacity(0.10), lineWidth: focusedProjectId == project.id ? 4 : 1)
            }
            .scaleEffect(focusedProjectId == project.id ? 1.035 : 1)
            .animation(.easeOut(duration: 0.12), value: focusedProjectId)
        }
        #if os(tvOS)
        .buttonStyle(.plain)
        #endif
        .focused($focusedProjectId, equals: project.id)
        .accessibilityIdentifier("vibing.project.\(project.name)")
        .accessibilityLabel(project.id == rememberedProjectId
            ? "\(project.name), latest project, open"
            : "\(project.name), open")
    }

    private func selectedProjectView(_ repository: ProjectSummary) -> some View {
        VStack(alignment: .leading, spacing: 24) {
            HStack(spacing: 16) {
                Button {
                    selectedRepository = nil
                    activePreview = nil
                    targets = []
                    error = nil
                } label: {
                    Label("Change project", systemImage: "chevron.left")
                }
                VStack(alignment: .leading, spacing: 3) {
                    Text(repository.name).font(.system(size: 32, weight: .bold)).lineLimit(1)
                    Text("Choose the app to open live").foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer()
            }

            if loadingOptions {
                HStack(spacing: 16) {
                    ProgressView()
                    Text("Finding runnable apps…").foregroundStyle(.secondary).lineLimit(1)
                }
                .padding(.vertical, 28)
            } else if let error {
                VStack(alignment: .leading, spacing: 14) {
                    Label("Vibing unavailable", systemImage: "exclamationmark.triangle.fill")
                        .font(.title3.bold()).foregroundStyle(.orange)
                    Text(error).foregroundStyle(.secondary)
                    Button("Try again") { Task { await loadTargets(for: repository) } }
                }
                .padding(22)
                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
            } else {
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(alignment: .top, spacing: 18) {
                        ForEach(targets) { target in
                            targetButton(target)
                        }
                    }
                    .padding(.vertical, 18)
                }
            }
            Spacer()
        }
        .padding(48)
    }

    @ViewBuilder
    private func targetButton(_ target: ProjectSummary) -> some View {
        Button {
            activate(target, in: selectedRepository)
        } label: {
            HStack(spacing: 14) {
                Image(systemName: FrameworkStyle.of(target.framework).symbol)
                    .foregroundStyle(FrameworkStyle.of(target.framework).color)
                VStack(alignment: .leading, spacing: 4) {
                    Text(target.name)
                        .font(.system(size: 20, weight: .semibold))
                        .lineLimit(1)
                    Text(projectSummary(target))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                Spacer()
            }
            .padding(20)
            .frame(width: 360, height: 150, alignment: .leading)
        }
        .buttonStyle(.bordered)
        .tint(.secondary)
    }

    private func projectSummary(_ project: ProjectSummary) -> String {
        let surfaces = (project.surfaces ?? []).filter { $0 != "backend" }
        let surfaceText = surfaces.isEmpty ? "" : " · " + surfaces.map { $0.capitalized }.joined(separator: " + ")
        let monorepo = project.isMonorepo == true ? " · monorepo" : ""
        return "\(project.frameworkLabel)\(surfaceText)\(monorepo)"
    }

    private func loadProjects() async {
        loading = true
        error = nil
        do {
            guard let client = store.runnerClient() ?? store.renderClient() else {
                throw AgentError(message: "No connected machine can provide project inventory")
            }
            let loaded = try await client.listProjects().filter(tvVibingProjectIsRenderable).sorted {
                $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
            }
            if store.lastProject(for: store.runnerBox()?.id, projects: loaded) == nil,
               let settings = try? await MachineRegistry.fetchSettings(token: store.token) {
                store.adoptSettings(settings)
            }
            let remembered = store.lastProject(for: store.runnerBox()?.id, projects: loaded)
            rememberedProjectId = remembered?.id
            // Latest first is both a visual and focus contract. Inventory order
            // is otherwise alphabetical and never silently changes the default.
            projects = remembered.map { latest in
                [latest] + loaded.filter { $0.id != latest.id }
            } ?? loaded
            focusedProjectId = remembered?.id ?? projects.first?.id
        } catch {
            self.error = error.localizedDescription
        }
        loading = false
        // The project buttons do not exist while `loading` is true. Re-assert
        // focus on the next run loop after the rail is mounted.
        DispatchQueue.main.async {
            focusedProjectId = rememberedProjectId ?? projects.first?.id
        }
    }

    private func loadTargets(for repository: ProjectSummary) async {
        loadingOptions = true
        error = nil
        do {
            guard let client = store.runnerClient() else { throw AgentError(message: "No machine selected") }
            if repository.isMonorepo == true, let root = repository.path, !root.isEmpty {
                let apps = try await client.workspaceApps(root: root)
                targets = apps.filter(\.isPreviewable).map { $0.asProject(in: repository) }
                if targets.isEmpty {
                    error = "No runnable app is declared in this workspace. Add it to yaver.workspace.yaml so every surface gets the same answer."
                } else if let resumed = store.rememberedVibingTarget(
                    for: store.renderBox()?.id,
                    repository: repository,
                    targets: targets
                ) {
                    form = resumed.form
                    activePreview = resumed.target
                    loadingOptions = false
                    return
                } else {
                    // Older builds remembered only the repository. Do not make
                    // those users re-select a child on every entry: open the
                    // agent-declared first runnable app once, then persist that
                    // exact page for subsequent launches.
                    activate(targets[0], in: repository)
                    loadingOptions = false
                    return
                }
            } else {
                targets = [repository]
                if let resumed = store.rememberedVibingTarget(
                    for: store.renderBox()?.id,
                    repository: repository,
                    targets: targets
                ) {
                    form = resumed.form
                }
                activate(repository, in: repository)
                loadingOptions = false
                return
            }
        } catch {
            self.error = error.localizedDescription
        }
        loadingOptions = false
    }

    private func openProject(_ project: ProjectSummary) async {
        selectedRepository = project
        activePreview = nil
        if let boxId = store.runnerBox()?.id {
            store.rememberProject(project, for: boxId)
        }
        await loadTargets(for: project)
    }

    private func activate(_ target: ProjectSummary, in repository: ProjectSummary?) {
        guard let repository else { return }
        activePreview = target
        store.rememberVibingTarget(
            target,
            repository: repository,
            form: form,
            for: store.renderBox()?.id
        )
    }

    @ViewBuilder
    private func previewSurface(for target: ProjectSummary) -> some View {
        switch target.kind {
        case .web:
            RemoteRuntimeWebRTCView(project: target, form: form)
                .accessibilityIdentifier("vibing.live-preview")
        case .tvOS:
            // Dogfooding the TV app means the real native tvOS target runs on
            // the selected render Mac. This surface may only view/control that
            // simulator through the authenticated WebRTC session; it must not
            // silently fall back to a browser-window or phone simulator.
            RemoteRuntimeWebRTCView(
                project: target,
                form: .desktop,
                forcedTargetID: "tvos-simulator",
                launchGuest: true
            )
            .accessibilityIdentifier("vibing.dogfood-tvos-webrtc")
        case .android:
            DroidStreamView()
                .accessibilityIdentifier("vibing.live-preview")
        case .unknown:
            VStack(spacing: 18) {
                Image(systemName: "questionmark.square.dashed")
                    .font(.system(size: 56)).foregroundStyle(.secondary)
                Text("\(target.name) has no TV preview lane.")
                    .font(.title2).lineLimit(2).multilineTextAlignment(.center)
                Button("Choose another project") {
                    activePreview = nil
                    selectedRepository = nil
                }
            }
        }
    }

    private func center<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        content().frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
