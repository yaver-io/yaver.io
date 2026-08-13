// TasksView.swift — a glanceable list of what's running on the box.
//
// tvOS deliberately does NOT reimplement mobile's full task queue (create,
// fork, chain, model pickers) — that's a phone-in-hand workflow, wrong for a
// D-pad across the room. What a TV wants is to SEE what's queued / running /
// awaiting review, and to jump into a live one. This is that: read-only status,
// grouped by state, titles path-redacted, tap a live task to drive its session.

import SwiftUI

struct TasksView: View {
    @EnvironmentObject var store: YaverStore

    @State private var tasks: [TaskSummary] = []
    @State private var loading = true
    @State private var error: String?
    @State private var filter: Filter = .active
    @State private var showComposer = false

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
            Picker("Filter", selection: $filter) {
                ForEach(Filter.allCases) { f in Text(f.rawValue).tag(f) }
            }
            .pickerStyle(.segmented)
            .padding(.horizontal, 48).padding(.bottom, 12)

            Group {
                if loading {
                    center { ProgressView().scaleEffect(1.4) }
                } else if let error {
                    center {
                        VStack(spacing: 14) {
                            Text(error).foregroundStyle(.orange).multilineTextAlignment(.center)
                            Button("Try again") { Task { await load() } }
                        }
                    }
                } else {
                    ScrollView {
                        LazyVStack(spacing: 12) {
                            // The big, dashed, FIRST card (2026-08-13): "New
                            // vibe" is the action the couch reaches for — a
                            // styled card (like the active task cards, but
                            // dashed + wand) that opens the blank composer
                            // with the prompt focused so the Siri Remote mic
                            // dictates immediately (the one STT path a TV has).
                            Button {
                                showComposer = true
                            } label: {
                                HStack(spacing: 18) {
                                    Image(systemName: "wand.and.stars")
                                        .font(.system(size: 30, weight: .semibold))
                                        .foregroundStyle(.blue)
                                        .frame(width: 52, height: 52)
                                        .background(Color.blue.opacity(0.14), in: RoundedRectangle(cornerRadius: 12))
                                    VStack(alignment: .leading, spacing: 4) {
                                        Text("New vibe").font(.system(size: 26, weight: .bold))
                                        Text("Press the mic button on the Siri Remote to dictate — the composer opens mic-ready.")
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
                            .accessibilityLabel("New vibe — dictate with the Siri Remote mic")

                            if filtered.isEmpty {
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
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
        .task { await load() }
        .sheet(isPresented: $showComposer) {
            TaskComposerView()
                .environmentObject(store)
        }
        // The composer dismisses itself after a successful POST — reload so the
        // new task is visible at the top of Active instead of a stale list.
        .onChange(of: showComposer) { _, open in
            if !open { Task { await load() } }
        }
    }

    private var filtered: [TaskSummary] { tasks.filter { filter.matches($0.status) } }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: "bubble.left.and.bubble.right.fill").font(.system(size: 26)).foregroundStyle(.blue)
                Text("Chat").font(.system(size: 30, weight: .bold))
                Text("Tasks & vibes").font(.system(size: 17)).foregroundStyle(.secondary)
                Spacer()
                Button {
                    showComposer = true
                } label: {
                    Label("New vibe", systemImage: "wand.and.stars")
                        .font(.system(size: 17, weight: .semibold))
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                Button { Task { await load() } } label: { Image(systemName: "arrow.clockwise") }
                    .disabled(loading)
            }
            // The one speech path a TV has: the Siri Remote's mic button
            // dictates into a focused text field. Everything started from the
            // phone's whisper mic is a task too, so it lands in this same list.
            Text("Press the mic button on the Siri Remote to dictate a vibe — or start one with whisper on your iPhone; it appears here.")
                .font(.system(size: 15))
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 48).padding(.vertical, 20)
    }

    @ViewBuilder private func row(_ t: TaskSummary) -> some View {
        // Tap a task to stream its LIVE console (raw runner stdout over
        // /tasks/{id}/output?rawSince=) — the tvOS twin of mobile's
        // LiveConsoleSection. A task with a tmux session additionally offers
        // "Drive session" from the detail's footer.
        NavigationLink(destination: TaskDetailView(task: t)) { rowBody(t) }
            .buttonStyle(.card)
    }

    private func rowBody(_ t: TaskSummary) -> some View {
        HStack(spacing: 18) {
            TaskStatusGlyph(status: t.status)
            VStack(alignment: .leading, spacing: 4) {
                Text(t.safeTitle).font(.system(size: 22, weight: .medium)).lineLimit(2)
                Text([t.runner, t.status].compactMap { $0 }.joined(separator: " · "))
                    .font(.system(size: 15)).foregroundStyle(.secondary)
            }
            Spacer()
            Image(systemName: "chevron.right").foregroundStyle(.secondary)
        }
    }

    private func center<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        content().frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func load() async {
        loading = true
        error = nil
        do {
            // Runner/render split: the task list lives on the RUNNER box.
            guard let client = store.runnerClient() else {
                throw AgentError(message: store.machineSplitActive
                    ? "Your AI runner machine needs the relay to be reachable from this TV — nothing was read from the wrong box."
                    : "No machine selected")
            }
            tasks = try await client.listTasks()
        } catch {
            self.error = error.localizedDescription
        }
        loading = false
    }
}
