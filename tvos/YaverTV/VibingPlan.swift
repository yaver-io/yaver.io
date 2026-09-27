// VibingPlan.swift — intersect the agent's repository/box answer with what
// this tvOS build can actually render.
//
// The agent's `remote-runtime` option means the BOX can produce WebRTC. tvOS
// now ships a raw libwebrtc decoder and the remote-runtime control protocol,
// so it can negotiate H.264 or JPEG-over-data-channel and drive the app with
// a soft Siri Remote cursor. TV frames remain the compatibility lane.

import Foundation

private let tvVibingWebFrameworks: Set<String> = [
    "expo", "react-native", "reactnative", "rn", "react", "flutter",
    "nextjs", "next", "vite", "web", "remix", "astro", "svelte",
]

private let tvVibingMobileFrameworks: Set<String> = [
    "expo", "react-native", "reactnative", "rn", "flutter", "swift", "kotlin", "android",
]

/// Vibing is a rendered-app surface, not a repository browser. The agent's
/// canonical `/projects` inventory can also contain API-only services, the
/// daemon's home directory, and other repositories with nothing a TV can
/// display. Keep those truthful rows available to Tasks/Session, but never
/// turn them into blank cards here.
func tvVibingProjectIsRenderable(_ project: ProjectSummary) -> Bool {
    if project.kind != .unknown { return true }

    let frameworks = Set(([project.framework].compactMap { $0 } + (project.frameworks ?? []))
        .map { $0.lowercased() })
    if !frameworks.isDisjoint(with: tvVibingWebFrameworks.union(tvVibingMobileFrameworks)) {
        return true
    }

    let surfaces = Set(((project.surfaces ?? []) + (project.testSurfaces ?? []))
        .map { $0.lowercased() })
    let renderedSurfaces: Set<String> = [
        "mobile", "ios", "android", "web", "frontend", "browser", "tv", "tvos", "tvos-simulator",
    ]
    return !surfaces.isDisjoint(with: renderedSurfaces)
}

func tvVibingFilteredProjects(_ projects: [ProjectSummary], query: String) -> [ProjectSummary] {
    let needle = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    return projects.filter { project in
        guard tvVibingProjectIsRenderable(project) else { return false }
        guard !needle.isEmpty else { return true }
        let haystack = ([project.name, project.framework, project.branch].compactMap { $0 }
            + (project.frameworks ?? [])
            + (project.surfaces ?? [])
            + (project.testSurfaces ?? []))
            .joined(separator: " ")
            .lowercased()
        return haystack.contains(needle)
    }
}

func tvVibingDisplayFramework(_ project: ProjectSummary) -> String? {
    let candidates = [project.framework].compactMap { $0 } + (project.frameworks ?? [])
    return candidates.first { candidate in
        let normalized = candidate.lowercased()
        return tvVibingWebFrameworks.contains(normalized) || tvVibingMobileFrameworks.contains(normalized)
    } ?? project.framework
}

/// Short, shared vocabulary for the 10-foot cards. Framework remains the
/// primary identity; these labels answer which visible app surface it can
/// render without dumping the agent's entire capability inventory.
func tvVibingCapabilityLabels(_ project: ProjectSummary) -> [String] {
    let frameworks = ([project.framework].compactMap { $0 } + (project.frameworks ?? []))
        .map { $0.lowercased() }
    let declared = Set(((project.surfaces ?? []) + (project.testSurfaces ?? []))
        .map { $0.lowercased() })
    var labels: [String] = []

    if frameworks.contains(where: tvVibingMobileFrameworks.contains)
        || !declared.isDisjoint(with: ["mobile", "ios", "android"]) {
        labels.append("Mobile")
    }
    if frameworks.contains(where: tvVibingWebFrameworks.contains)
        || !declared.isDisjoint(with: ["web", "browser", "frontend"]) {
        labels.append("Web")
    }
    if frameworks.contains(where: { ["nextjs", "next", "vite", "react", "web", "remix", "astro", "svelte"].contains($0) }) {
        labels.append("Frontend")
    }
    if !declared.isDisjoint(with: ["tv", "tvos", "tvos-simulator"]) {
        labels.append("TV")
    }
    return labels.reduce(into: [String]()) { unique, label in
        if !unique.contains(label) { unique.append(label) }
    }
}

/// `/dev/start` is an admission response. A green `running` bit for another
/// checkout is not readiness for the selected project—the exact false green
/// that left SFMG behind a permanent spinner while yaver.io/mobile was active.
func tvRuntimeWorkDirMatchesProject(active: String?, selected: String?) -> Bool {
    guard let active = active?.trimmingCharacters(in: .whitespacesAndNewlines),
          let selected = selected?.trimmingCharacters(in: .whitespacesAndNewlines),
          !active.isEmpty, !selected.isEmpty else { return false }
    let normalizedActive = active.replacingOccurrences(of: "/+$", with: "", options: .regularExpression)
    let normalizedSelected = selected.replacingOccurrences(of: "/+$", with: "", options: .regularExpression)
    return normalizedActive == normalizedSelected
}

enum TVPreviewDestination: Equatable {
    case webFrames
    case androidFrames
    case interactiveWebRTC
}

struct TVPreviewChoice: Identifiable, Equatable {
    let id: String
    let title: String
    let detail: String
    let available: Bool
    let primary: Bool
    let destination: TVPreviewDestination?
}

struct TVRenderLaneVerdict: Identifiable, Equatable {
    let id: String
    let label: String
    let usable: Bool
    let reason: String
}

/// The client half of render negotiation. Keep this explicit until the agent
/// exposes NegotiateRenderLanes over HTTP; a surface name is not a decoder.
let tvOSRenderLaneVerdicts: [TVRenderLaneVerdict] = [
    TVRenderLaneVerdict(
        id: "frames",
        label: "TV frames",
        usable: true,
        reason: "Runs on the box and reaches this TV directly when possible, otherwise through the free Yaver relay. Tailscale is optional."
    ),
    TVRenderLaneVerdict(
        id: "webrtc",
        label: "WebRTC",
        usable: true,
        reason: "Interactive WebRTC on Apple TV: Siri Remote pointer, scroll, app keys, and text, with authenticated frame fallback when ICE is slow."
    ),
]

func tvPreviewChoices(project: ProjectSummary, capabilities: ProjectPreviewCapabilities) -> [TVPreviewChoice] {
    capabilities.options.map { option in
        let backendReason = option.reason?.trimmingCharacters(in: .whitespacesAndNewlines)
        switch option.id {
        case "dev-server":
            let runnable = option.supported && project.kind == .web
            let detail: String
            if runnable {
                detail = "Headless browser on the box → authenticated frame session → this TV. Direct or free relay; no Tailscale requirement."
            } else if !option.supported {
                detail = backendReason ?? "The box could not start this browser lane."
            } else {
                detail = "This target does not expose a browser-renderable app. Pick a native runtime target instead."
            }
            return TVPreviewChoice(
                id: option.id,
                title: project.kind == .web ? "Browser → TV frames" : option.label,
                detail: detail,
                available: runnable,
                primary: option.primary == true,
                destination: runnable ? .webFrames : nil
            )

        case "remote-runtime":
            if project.kind == .android {
                let runnable = option.supported
                return TVPreviewChoice(
                    id: option.id,
                    title: "Android runtime → TV frames",
                    detail: runnable
                        ? "Runs the Android app on the box and sends its captured frames to this TV."
                        : (backendReason ?? "No Android runtime is available on this box."),
                    available: runnable,
                    primary: option.primary == true,
                    destination: runnable ? .androidFrames : nil
                )
            }
            let runnable = option.supported && project.kind == .web
            return TVPreviewChoice(
                id: option.id,
                title: "Interactive WebRTC · Apple TV",
                detail: runnable
                    ? tvOSRenderLaneVerdicts.first(where: { $0.id == "webrtc" })!.reason
                    : (backendReason ?? "This target does not expose a browser-renderable WebRTC runtime."),
                available: runnable,
                primary: option.primary == true,
                destination: runnable ? .interactiveWebRTC : nil
            )

        default:
            return TVPreviewChoice(
                id: option.id,
                title: option.label,
                detail: backendReason ?? "This option is available on another Yaver surface, not this TV.",
                available: false,
                primary: option.primary == true,
                destination: nil
            )
        }
    }
}
