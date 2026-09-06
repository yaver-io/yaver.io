// TasksView.swift — a glanceable list of what's running on the box.
//
// Chat follows mobile's conversation mechanics while keeping TV navigation
// lean: one default-focused New vibe card, recent threads, and an in-thread
// reply field. Project/MCP configuration is optional and never sits between
// entering Chat and dictating the first prompt.

import SwiftUI

struct TasksView: View {
    @EnvironmentObject var store: YaverStore

    @State private var tasks: [TaskSummary] = []
    @State private var runnerSessions: [RunnerSession] = []
    @State private var loading = true
    @State private var error: String?
    @State private var sessionError: String?
    @State private var filter: Filter = .all
    @State private var createdTask: TaskSummary?
    @State private var destination: ChatDestination?
    @FocusState private var newVibeFocused: Bool

    private enum ChatDestination: Hashable {
        case composer
        case task(String)
    }

    enum Filter: String, CaseIterable, Identifiable {
        case active = "Active", review = "Review", done = "Done", failed = "Failed", all = "All"
        var id: String { rawValue }
        func matches(_ s: String?) -> Bool {
            let st = (s ?? "").lowercased()
            switch self {
            case .active: return st == "running" || st == "queued"
            case .review: return st == "review"
            case .done: return st == "completed"
            case .failed: return st == "failed" || st == "stopped"
            case .all: return true
            }
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            if !store.taskRuntimePlan().available {
                noTaskRuntimeView
            } else {
                ScrollView {
                LazyVStack(spacing: 12) {
                    // This control is intentionally independent of the task
                    // history request. A slow/failed GET /tasks must never
                    // remove the one action the user came to Chat to perform.
                    newVibeButton

                    runnerSessionSection

                    if loading {
                        ProgressView().scaleEffect(1.4).padding(.top, 32)
                    } else if let error {
                        VStack(spacing: 14) {
                            Text(error).foregroundStyle(.orange).multilineTextAlignment(.center)
                            Button("Try loading conversations again") { Task { await load() } }
                        }
                        .padding(.top, 24)
                } else if filtered.isEmpty {
                        Text("No \(filter.rawValue.lowercased()) tasks.")
                            .foregroundStyle(.secondary).padding(.top, 32)
                } else {
                        ForEach(filtered) { t in row(t) }
                    }
                }
                .padding(48)
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
        .task { await load() }
        // One item owns the one pushed Chat destination. Replacing `.composer`
        // with `.task(id)` updates that destination atomically; independent
        // Boolean destinations briefly mounted TaskDetail twice, cancelled its
        // SSE during the transition, and left a visually-correct but frozen
        // running conversation.
        .navigationDestination(item: $destination) { route in
            switch route {
            case .composer:
                TaskComposerView(dismissAfterCreate: false) { task in
                    createdTask = task
                    destination = .task(task.id)
                }
                .environmentObject(store)
            case .task(let id):
                if let createdTask, createdTask.id == id {
                    // New Vibe is a couch conversation: after the prompt is
                    // sent, go straight into the exact returned task's chat.
                    TaskDetailView(task: createdTask)
                }
            }
        }
        .onChange(of: destination) { oldRoute, newRoute in
            if newRoute == nil {
                createdTask = nil
                if oldRoute != nil { Task { await load() } }
            }
        }
        .defaultFocus($newVibeFocused, true)
    }

    private var runnerSessionSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Console sessions")
                    .font(.system(size: 21, weight: .bold))
                Text("tmux · live on \(store.runnerBox()?.name ?? "runner")")
                    .font(.system(size: 14))
                    .foregroundStyle(.secondary)
                Spacer()
            }

            if runnerSessions.isEmpty {
                HStack(spacing: 12) {
                    Image(systemName: "terminal")
                        .foregroundStyle(.secondary)
                    VStack(alignment: .leading, spacing: 3) {
                        Text("No live tmux sessions")
                            .font(.system(size: 17, weight: .semibold))
                        Text(sessionError ?? "Runner sessions appear here as soon as OpenCode, Codex, or Claude opens one.")
                            .font(.system(size: 14))
                            .foregroundStyle(sessionError == nil ? Color.secondary : Color.orange)
                            .lineLimit(2)
                    }
                    Spacer()
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 16)
                .background(Color.white.opacity(0.05), in: RoundedRectangle(cornerRadius: 14))
                .accessibilityIdentifier("chat.no-live-sessions")
            } else {
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(spacing: 14) {
                        ForEach(runnerSessions) { session in
                            NavigationLink(destination: SessionView(preselect: session.name)) {
                                HStack(spacing: 14) {
                                    Image(systemName: "terminal.fill")
                                        .font(.system(size: 22))
                                        .foregroundStyle(.green)
                                    VStack(alignment: .leading, spacing: 4) {
                                        Text(session.name)
                                            .font(.system(size: 18, weight: .semibold))
                                            .lineLimit(1)
                                        Text("\(session.model?.isEmpty == false ? session.model! : runnerDisplayName(session.runner)) · \(session.attached == true ? "attached" : "active")")
                                            .font(.system(size: 14))
                                            .foregroundStyle(.secondary)
                                    }
                                    Image(systemName: "chevron.right").foregroundStyle(.secondary)
                                }
                                .padding(.horizontal, 20)
                                .padding(.vertical, 16)
                                .frame(width: 360, alignment: .leading)
                            }
                            .buttonStyle(.card)
                            .accessibilityIdentifier("chat.session.\(session.name)")
                        }
                    }
                }
            }
        }
        .padding(.top, 10)
    }

    private var noTaskRuntimeView: some View {
        VStack(alignment: .leading, spacing: 18) {
            Image(systemName: store.taskRuntimePlan().kind == .signedOut
                  ? "person.crop.circle.badge.xmark"
                  : "server.rack")
                .font(.system(size: 52))
                .foregroundStyle(.orange)
            Text(store.taskRuntimePlan().kind == .signedOut
                 ? "Sign in to start coding tasks"
                 : "No remote runner connected")
                .font(.system(size: 30, weight: .bold))
            Text(store.taskRuntimePlan().kind == .signedOut
                 ? "Sign in first, then choose the machine that runs OpenCode."
                 : "OpenCode + DeepSeek V4 Flash tasks need a runner machine. A render box is not required for Chat or Git coding.")
                .font(.system(size: 20))
                .foregroundStyle(.secondary)
                .frame(maxWidth: 760, alignment: .leading)
            if store.remotelessAllowed && store.taskRuntimePlan().kind == .boxlessUnavailable {
                Text("remoteless.code-edit.unavailable · This TV's fallback is analysis/chat only. Nothing was sent. Use a primary/secondary runner for Git editing, or open the analysis fallback below.")
                    .font(.system(size: 16))
                    .foregroundStyle(.tertiary)
                    .frame(maxWidth: 760, alignment: .leading)
            }
            NavigationLink("Choose or add a machine", destination: MachinePickerView())
                .buttonStyle(.borderedProminent)
            if store.remotelessAllowed {
                NavigationLink("Use boxless Yaver Code", destination: BoxlessCodeView())
                    .buttonStyle(.bordered)
            }
        }
        .padding(56)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private var filtered: [TaskSummary] { tasks.filter { filter.matches($0.status) } }

    private var newVibeButton: some View {
        Button {
            createdTask = nil
            destination = .composer
        } label: {
            HStack(spacing: 18) {
                Image(systemName: "wand.and.stars")
                    .font(.system(size: 30, weight: .semibold))
                    .foregroundStyle(.blue)
                    .frame(width: 52, height: 52)
                    .background(Color.blue.opacity(0.14), in: RoundedRectangle(cornerRadius: 12))
                VStack(alignment: .leading, spacing: 4) {
                    Text("New vibe").font(.system(size: 26, weight: .bold))
                    Text("Keyboard opens now · starts a session with your defaults")
                        .font(.system(size: 15)).foregroundStyle(.secondary)
                }
                Spacer()
                Image(systemName: "plus.circle.fill")
                    .font(.system(size: 30)).foregroundStyle(.blue)
            }
            .padding(.horizontal, 24).padding(.vertical, 18)
            .overlay(
                RoundedRectangle(cornerRadius: 16)
                    .strokeBorder(style: StrokeStyle(lineWidth: 2, dash: [8, 6]))
                    .foregroundStyle(Color.blue.opacity(0.5))
            )
        }
        .buttonStyle(.plain)
        .focused($newVibeFocused)
        .accessibilityIdentifier("chat.new-vibe")
        .accessibilityLabel("New vibe — opens the keyboard and starts a session")
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: "bubble.left.and.bubble.right.fill").font(.system(size: 26)).foregroundStyle(.blue)
                Text("Chat").font(.system(size: 30, weight: .bold))
                Text("Tasks & vibes").font(.system(size: 17)).foregroundStyle(.secondary)
                Spacer()
                Menu {
                    ForEach(Filter.allCases) { option in
                        Button {
                            filter = option
                        } label: {
                            if filter == option {
                                Label(option.rawValue, systemImage: "checkmark")
                            } else {
                                Text(option.rawValue)
                            }
                        }
                    }
                } label: {
                    Label(filter.rawValue, systemImage: "line.3.horizontal.decrease.circle")
                }
                .accessibilityLabel("Task filter, \(filter.rawValue)")
                Button { Task { await load() } } label: { Image(systemName: "arrow.clockwise") }
                    .disabled(loading)
            }
            // The one speech path a TV has: the Siri Remote's mic button
            // dictates into a focused text field. Everything started from the
            // phone's whisper mic is a task too, so it lands in this same list.
            Text("Press and hold Siri on the remote while the prompt field is focused to dictate a vibe — or start one with whisper on your iPhone; it appears here.")
                .font(.system(size: 15))
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 48).padding(.vertical, 20)
    }

    @ViewBuilder private func row(_ t: TaskSummary) -> some View {
        // Tap a task to open its conversation. Raw runner output remains one
        // optional disclosure inside the detail, matching mobile's hierarchy.
        NavigationLink(destination: TaskDetailView(task: t)) { rowBody(t) }
            .buttonStyle(.card)
    }

    private func rowBody(_ t: TaskSummary) -> some View {
        HStack(spacing: 18) {
            TaskStatusGlyph(status: t.status)
            VStack(alignment: .leading, spacing: 4) {
                Text(t.safeTitle).font(.system(size: 22, weight: .medium)).lineLimit(2)
                Text([
                    conversationLabel(t),
                    statusLabel(t.status),
                ].filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.system(size: 15)).foregroundStyle(.secondary)
            }
            Spacer()
            Image(systemName: "chevron.right")
                .foregroundStyle(.secondary)
                .padding(.trailing, 8)
        }
    }

    private func statusDot(_ s: String?) -> some View {
        Circle().fill(color(for: s)).frame(width: 14, height: 14)
    }

    private func color(for s: String?) -> Color {
        switch (s ?? "").lowercased() {
        case "running": return .green
        case "queued": return .blue
        case "review": return .purple
        case "completed": return .gray
        case "failed", "stopped": return .red
        default: return .secondary
        }
    }

    private func load() async {
        loading = true
        error = nil
        do {
            // Runner/render split: the task list lives on the RUNNER box.
            guard let client = store.runnerClient() else {
                let plan = store.taskRuntimePlan()
                throw AgentError(message: plan.kind == .boxlessUnavailable
                    ? "No task runner is connected. Remote runner tasks remain available; boxless Git+coding is not configured on this TV yet."
                    : store.machineSplitActive
                    ? "Your AI runner machine needs the relay to be reachable from this TV — nothing was read from the wrong box."
                    : "No machine selected")
            }
            async let taskRows: [TaskSummary]? = try? client.listTasks()
            async let sessionRows: RunnerSessions? = try? client.runnerSessions()
            let loadedTasks = await taskRows
            let loadedSessions = await sessionRows
            tasks = loadedTasks ?? []
            runnerSessions = loadedSessions?.sessions ?? []
            if loadedTasks == nil {
                error = "Couldn't load recent conversations."
            }
            if loadedSessions == nil {
                sessionError = "Couldn't refresh live tmux sessions."
            }
        } catch {
            self.error = error.localizedDescription
        }
        loading = false
    }

    private func runnerDisplayName(_ runner: String?) -> String {
        switch runner?.lowercased() {
        case "claude", "claude-code": return "Claude Code"
        case "codex": return "Codex"
        case "opencode": return "OpenCode"
        case .some(let value) where !value.isEmpty: return value
        default: return "Runner"
        }
    }

    private func conversationLabel(_ task: TaskSummary) -> String {
        if let model = task.model?.trimmingCharacters(in: .whitespacesAndNewlines), !model.isEmpty {
            let effort = task.reasoningEffort?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            return [model, effort].filter { !$0.isEmpty }.joined(separator: " · ")
        }
        return runnerDisplayName(task.runner)
    }

    private func statusLabel(_ status: String?) -> String {
        guard let status, !status.isEmpty else { return "" }
        return status.prefix(1).uppercased() + status.dropFirst()
    }
}
