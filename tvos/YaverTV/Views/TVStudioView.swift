// TVStudioView.swift — configuration first, then the same lean 30/70 Studio
// contract as desktop: a phone preview on the left and the selected remote
// machine's real PTY on the right. Studio never renders chat or Markdown.

import SwiftUI

struct TVStudioView: View {
    @EnvironmentObject private var store: YaverStore

    @State private var configured = false
    @State private var projects: [ProjectSummary] = []
    @State private var projectName = ""
    @State private var runner = "terminal"
    @State private var form: PreviewForm = .phone
    @State private var loading = false
    @State private var error: String?

    private var project: ProjectSummary? {
        projects.first(where: { $0.name == projectName })
    }

    var body: some View {
        Group {
            if configured, let box = store.selectedBox, let project {
                GeometryReader { geometry in
                    HStack(spacing: 8) {
                        WebPreviewStreamView(project: project, form: form, studioMode: true)
                            .frame(width: geometry.size.width * 0.30)
                            .clipShape(RoundedRectangle(cornerRadius: 20))
                            .accessibilityIdentifier("studio.mobile-preview")
                        TVTerminalScreen(
                            box: box,
                            token: store.token,
                            launch: runner,
                            cwd: project.path,
                            consoleOnly: true
                        )
                        .frame(width: geometry.size.width * 0.70 - 8)
                        .clipShape(RoundedRectangle(cornerRadius: 20))
                        .accessibilityIdentifier("studio.ssh-console")
                    }
                    .padding(8)
                }
                .toolbar {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button("Configure") { configured = false }
                    }
                }
            } else {
                configuration
            }
        }
        .background(Color.black)
        .navigationTitle("Studio")
        .task(id: store.selectedBox?.id) { await loadProjects() }
    }

    private var configuration: some View {
        VStack(alignment: .leading, spacing: 24) {
            Text("Open Studio").font(.system(size: 44, weight: .black))
            Text("Choose the runner PC, project, runner and preview lane before opening the workspace.")
                .font(.system(size: 20)).foregroundStyle(.secondary)

            Picker("Runner PC", selection: Binding(
                get: { store.selectedBox?.id ?? "" },
                set: { id in if let box = store.boxes.first(where: { $0.id == id }) { store.select(box) } }
            )) {
                ForEach(store.boxes) { box in
                    Text(box.aliasLabel ?? box.name).tag(box.id)
                }
            }
            .accessibilityIdentifier("studio.runner-pc")

            Picker("Project", selection: $projectName) {
                if projects.isEmpty { Text(loading ? "Loading projects…" : "No projects found").tag("") }
                ForEach(projects) { project in Text(project.name).tag(project.name) }
            }
            .disabled(projects.isEmpty)
            .accessibilityIdentifier("studio.project")

            Picker("Runner", selection: $runner) {
                Text("Shell / tmux").tag("terminal")
                Text("Codex").tag("codex")
                Text("Claude Code").tag("claude")
                Text("OpenCode").tag("opencode")
            }
            .accessibilityIdentifier("studio.runner")

            Picker("Lane", selection: $form) {
                ForEach(PreviewForm.allCases) { form in Text(form.rawValue).tag(form) }
            }
            .accessibilityIdentifier("studio.lane")

            Text("Layout · mobile 30% / SSH console 70%")
                .font(.system(size: 18, design: .monospaced)).foregroundStyle(.secondary)
            if let error { Text(error).foregroundStyle(.red).font(.system(size: 17, design: .monospaced)) }

            Button("Save and open Studio") {
                guard store.selectedBox != nil else { error = "Choose a runner PC first."; return }
                guard project != nil else { error = "Choose a project first."; return }
                error = nil
                configured = true
            }
            .buttonStyle(.borderedProminent)
            .disabled(store.selectedBox == nil || project == nil)
            Spacer()
        }
        .padding(56)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .accessibilityIdentifier("studio.configuration")
    }

    @MainActor
    private func loadProjects() async {
        guard let box = store.selectedBox else {
            projects = []; projectName = ""; return
        }
        loading = true
        error = nil
        do {
            let client = AgentClient(token: store.token, box: box)
            let next = try await client.listProjects()
            projects = next
            if !next.contains(where: { $0.name == projectName }) {
                projectName = store.lastProject(for: box.id, projects: next)?.name ?? next.first?.name ?? ""
            }
        } catch {
            projects = []
            projectName = ""
            self.error = error.localizedDescription
        }
        loading = false
    }
}
