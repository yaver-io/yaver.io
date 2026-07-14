// ProjectsView.swift — browse the box's projects and preview one on the TV.
//
// The entry to the vibe loop on a TV: pick a project, choose a form factor
// (phone / tablet / desktop), and watch it render on the big screen. Because
// the redroid container runs the app on the BOX (not a physical phone), there's
// no Hermes push to a device — the TV just streams pixels:
//   * an RN/Android project runs in redroid → /droid/frame
//   * a web project is captured headless at the chosen viewport → vibe frames
// tvOS has no WebKit, so a web app is always streamed as pixels, never a real
// in-process webview.

import SwiftUI

/// The device form factor to render a preview at. Drives the headless viewport
/// for web, and is advisory for redroid.
enum PreviewForm: String, CaseIterable, Identifiable {
    case phone = "Phone", tablet = "Tablet", desktop = "Desktop"
    var id: String { rawValue }
    var width: Int { self == .phone ? 390 : self == .tablet ? 820 : 1280 }
    var height: Int { self == .phone ? 844 : self == .tablet ? 1180 : 720 }
    var icon: String { self == .phone ? "iphone" : self == .tablet ? "ipad" : "display" }
}

struct ProjectsView: View {
    @EnvironmentObject var store: YaverStore

    @State private var projects: [ProjectSummary] = []
    @State private var loading = true
    @State private var error: String?
    @State private var form: PreviewForm = .phone

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            formPicker
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
                } else if projects.isEmpty {
                    center { Text("No projects on this machine.").foregroundStyle(.secondary) }
                } else {
                    ScrollView {
                        LazyVStack(spacing: 12) {
                            ForEach(projects) { p in row(p) }
                        }.padding(48)
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
        .task { await load() }
    }

    private var header: some View {
        HStack {
            Image(systemName: "folder.fill").font(.system(size: 26)).foregroundStyle(.orange)
            Text("Projects").font(.system(size: 30, weight: .bold))
            Spacer()
            Button { Task { await load() } } label: { Image(systemName: "arrow.clockwise") }.disabled(loading)
        }
        .padding(.horizontal, 48).padding(.vertical, 20)
    }

    private var formPicker: some View {
        HStack(spacing: 12) {
            Text("Render as").foregroundStyle(.secondary).font(.system(size: 18))
            Picker("Form", selection: $form) {
                ForEach(PreviewForm.allCases) { f in Label(f.rawValue, systemImage: f.icon).tag(f) }
            }
            .pickerStyle(.segmented)
            .frame(maxWidth: 560)
            Spacer()
        }
        .padding(.horizontal, 48).padding(.bottom, 12)
    }

    @ViewBuilder private func row(_ p: ProjectSummary) -> some View {
        NavigationLink(destination: destination(for: p)) {
            HStack(spacing: 18) {
                Image(systemName: icon(for: p.kind)).font(.system(size: 26)).frame(width: 40)
                VStack(alignment: .leading, spacing: 4) {
                    Text(p.name).font(.system(size: 24, weight: .semibold))
                    Text([p.frameworkLabel, p.branch].compactMap { $0 }.joined(separator: " · "))
                        .font(.system(size: 15)).foregroundStyle(.secondary)
                }
                Spacer()
                Image(systemName: "play.rectangle.fill").foregroundStyle(.secondary)
            }
            .padding(.horizontal, 24).padding(.vertical, 18)
        }
        .buttonStyle(.card)
    }

    @ViewBuilder private func destination(for p: ProjectSummary) -> some View {
        switch p.kind {
        case .android:
            DroidStreamView()
        case .web:
            WebPreviewStreamView(project: p, form: form)
        case .flutter:
            unsupported("Flutter previews aren't streamable to the TV yet — run it on a device or the web.")
        case .unknown:
            unsupported("No preview known for \(p.frameworkLabel). Open it in Session to run it.")
        }
    }

    private func unsupported(_ msg: String) -> some View {
        VStack(spacing: 16) {
            Image(systemName: "questionmark.square.dashed").font(.system(size: 56)).foregroundStyle(.secondary)
            Text(msg).multilineTextAlignment(.center).frame(maxWidth: 640).foregroundStyle(.secondary)
        }.frame(maxWidth: .infinity, maxHeight: .infinity).background(Color.black)
    }

    private func icon(for kind: ProjectSummary.Kind) -> String {
        switch kind {
        case .android: return "iphone.gen3"
        case .web: return "globe"
        case .flutter: return "f.square"
        case .unknown: return "shippingbox"
        }
    }

    private func center<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        content().frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func load() async {
        loading = true
        error = nil
        do {
            guard let client = store.client() else { throw AgentError(message: "No machine selected") }
            projects = try await client.listProjects()
        } catch {
            self.error = error.localizedDescription
        }
        loading = false
    }
}
