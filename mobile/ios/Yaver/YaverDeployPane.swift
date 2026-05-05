import Foundation
import UIKit

/// Yaver's native deploy pane — fifth shake-overlay action ("Deploy").
///
/// Visual language matches YaverFeedbackPane / YaverAgentsPane / YaverSettings-
/// Pane: same purple-tinted blur, rounded top corners, drag handle, close X.
///
/// Three-step flow on one card so the user never sees a multi-screen wizard:
///
///   1. Pick a target — TestFlight (iOS) / Play Store (Android) / Both.
///   2. Pick a machine — every device the user can reach, with per-target
///      capability surfaced as a green/grey dot. Linux machines greyed out
///      for TestFlight ("xcodebuild: only on darwin"). Macs without Xcode
///      installed greyed out the same way ("missing xcodebuild"). The
///      capability data comes from the agent's GET /fleet/deploy-options
///      endpoint, which fans out /doctor/build to every reachable device.
///   3. Tap → POST /deploy/ship with {app, target/targets, machine}. The
///      pane shows a brief "Deploy started on <name>" toast and dismisses.
///      Live SSE log viewing is intentionally NOT in this pane — that's
///      the desktop/web Deploy tab's job. The whole point of this surface
///      is "kick a deploy from your phone in three taps."
///
/// App slug source: prefer `yaverInheritedGuestProjectName` (set by the
/// guest bundle when it loaded), fall back to `yaverLoadedModuleName`
/// (the registered module of whatever's currently running). The slug is
/// what /deploy/ship's `app` field expects — same value /deploy/generate
/// and the workspace manifest use.
final class YaverDeployPane: NSObject {

  static let shared = YaverDeployPane()

  private weak var window: UIWindow?
  private weak var cardView: UIView?

  // Card body sections (kept as strong refs so async fetch callbacks can
  // mutate them without re-querying the view hierarchy).
  private var subtitleLabel: UILabel?
  private var targetSegment: UISegmentedControl?
  private var machineList: UIStackView?
  private var statusLabel: UILabel?
  private var loadingSpinner: UIActivityIndicatorView?

  // Fetched fleet state.
  private var options: FleetDeployOptions?
  private var inFlight: URLSessionDataTask?

  // Currently selected target index — same index space as the segmented
  // control. 0=testflight, 1=playstore, 2=both.
  private var selectedTargetIndex: Int = 2

  // MARK: - Models — must match desktop/agent/fleet_deploy_options.go.

  private struct FleetDeployTargetCap: Decodable {
    let target: String
    let ok: Bool
    let reason: String?
  }
  private struct FleetDeployDevice: Decodable {
    let deviceId: String
    let name: String
    let alias: String?
    let platform: String
    let isLocal: Bool
    let isOnline: Bool
    let probed: Bool
    let probeError: String?
    let capabilities: [FleetDeployTargetCap]
  }
  private struct FleetDeployOptions: Decodable {
    let app: String
    let stack: String?
    let targets: [String]
    let devices: [FleetDeployDevice]
    let warnings: [String]?
  }

  // MARK: - Presentation

  func present(in window: UIWindow) {
    if cardView != nil { return }
    self.window = window
    let pane = buildCard()
    window.addSubview(pane)
    NSLayoutConstraint.activate([
      pane.leadingAnchor.constraint(equalTo: window.leadingAnchor),
      pane.trailingAnchor.constraint(equalTo: window.trailingAnchor),
      pane.bottomAnchor.constraint(equalTo: window.bottomAnchor),
    ])
    self.cardView = pane
    pane.transform = CGAffineTransform(translationX: 0, y: 600)
    UIView.animate(withDuration: 0.32, delay: 0, usingSpringWithDamping: 0.9,
                   initialSpringVelocity: 0.4) {
      pane.transform = .identity
    }
    UIImpactFeedbackGenerator(style: .light).impactOccurred()
    fetchOptions()
  }

  // MARK: - Card layout

  private func buildCard() -> UIView {
    let bg = UIVisualEffectView(effect: UIBlurEffect(style: .systemUltraThinMaterialDark))
    bg.translatesAutoresizingMaskIntoConstraints = false
    bg.layer.cornerRadius = 22
    bg.layer.maskedCorners = [.layerMinXMinYCorner, .layerMaxXMinYCorner]
    bg.clipsToBounds = true
    bg.contentView.backgroundColor = UIColor(red: 0.055, green: 0.047, blue: 0.110, alpha: 0.62)

    let handle = UIView()
    handle.translatesAutoresizingMaskIntoConstraints = false
    handle.backgroundColor = UIColor(white: 1, alpha: 0.2)
    handle.layer.cornerRadius = 2.5

    let title = UILabel()
    title.translatesAutoresizingMaskIntoConstraints = false
    title.text = "Deploy"
    title.textColor = .white
    title.font = .systemFont(ofSize: 17, weight: .semibold)

    let subtitle = UILabel()
    subtitle.translatesAutoresizingMaskIntoConstraints = false
    subtitle.text = "loading machines…"
    subtitle.textColor = UIColor(white: 1, alpha: 0.55)
    subtitle.font = .systemFont(ofSize: 12)
    subtitleLabel = subtitle

    let close = UIButton(type: .system)
    close.translatesAutoresizingMaskIntoConstraints = false
    close.setImage(UIImage(systemName: "xmark", withConfiguration:
                            UIImage.SymbolConfiguration(pointSize: 16, weight: .semibold)), for: .normal)
    close.tintColor = UIColor(white: 1, alpha: 0.6)
    close.addTarget(self, action: #selector(dismissTapped), for: .touchUpInside)

    let segment = UISegmentedControl(items: ["TestFlight", "Play Store", "Both"])
    segment.translatesAutoresizingMaskIntoConstraints = false
    segment.selectedSegmentIndex = selectedTargetIndex
    segment.selectedSegmentTintColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 0.65)
    segment.setTitleTextAttributes([.foregroundColor: UIColor.white], for: .selected)
    segment.setTitleTextAttributes([.foregroundColor: UIColor(white: 1, alpha: 0.65)], for: .normal)
    segment.addTarget(self, action: #selector(targetChanged(_:)), for: .valueChanged)
    targetSegment = segment

    let scroll = UIScrollView()
    scroll.translatesAutoresizingMaskIntoConstraints = false
    scroll.showsVerticalScrollIndicator = false
    let machines = UIStackView()
    machines.translatesAutoresizingMaskIntoConstraints = false
    machines.axis = .vertical
    machines.spacing = 8
    machineList = machines
    scroll.addSubview(machines)

    let spinner = UIActivityIndicatorView(style: .medium)
    spinner.translatesAutoresizingMaskIntoConstraints = false
    spinner.color = UIColor(white: 1, alpha: 0.5)
    spinner.startAnimating()
    spinner.hidesWhenStopped = true
    loadingSpinner = spinner

    let status = UILabel()
    status.translatesAutoresizingMaskIntoConstraints = false
    status.text = ""
    status.textColor = UIColor(white: 1, alpha: 0.55)
    status.font = .systemFont(ofSize: 12)
    status.numberOfLines = 2
    status.textAlignment = .center
    statusLabel = status

    bg.contentView.addSubview(handle)
    bg.contentView.addSubview(title)
    bg.contentView.addSubview(subtitle)
    bg.contentView.addSubview(close)
    bg.contentView.addSubview(segment)
    bg.contentView.addSubview(scroll)
    bg.contentView.addSubview(spinner)
    bg.contentView.addSubview(status)

    NSLayoutConstraint.activate([
      bg.heightAnchor.constraint(greaterThanOrEqualToConstant: 480),
      bg.heightAnchor.constraint(lessThanOrEqualToConstant: 720),

      handle.centerXAnchor.constraint(equalTo: bg.contentView.centerXAnchor),
      handle.topAnchor.constraint(equalTo: bg.contentView.topAnchor, constant: 8),
      handle.widthAnchor.constraint(equalToConstant: 38),
      handle.heightAnchor.constraint(equalToConstant: 5),

      title.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      title.topAnchor.constraint(equalTo: handle.bottomAnchor, constant: 14),
      subtitle.leadingAnchor.constraint(equalTo: title.leadingAnchor),
      subtitle.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 2),
      subtitle.trailingAnchor.constraint(lessThanOrEqualTo: close.leadingAnchor, constant: -8),

      close.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -16),
      close.centerYAnchor.constraint(equalTo: title.centerYAnchor),
      close.widthAnchor.constraint(equalToConstant: 32),
      close.heightAnchor.constraint(equalToConstant: 32),

      segment.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      segment.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -18),
      segment.topAnchor.constraint(equalTo: subtitle.bottomAnchor, constant: 18),
      segment.heightAnchor.constraint(equalToConstant: 36),

      scroll.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      scroll.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -18),
      scroll.topAnchor.constraint(equalTo: segment.bottomAnchor, constant: 16),
      scroll.bottomAnchor.constraint(equalTo: status.topAnchor, constant: -8),

      machines.leadingAnchor.constraint(equalTo: scroll.leadingAnchor),
      machines.trailingAnchor.constraint(equalTo: scroll.trailingAnchor),
      machines.topAnchor.constraint(equalTo: scroll.topAnchor),
      machines.bottomAnchor.constraint(equalTo: scroll.bottomAnchor),
      machines.widthAnchor.constraint(equalTo: scroll.widthAnchor),

      spinner.centerXAnchor.constraint(equalTo: scroll.centerXAnchor),
      spinner.centerYAnchor.constraint(equalTo: scroll.centerYAnchor),

      status.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      status.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -18),
      status.bottomAnchor.constraint(equalTo: bg.contentView.bottomAnchor, constant: -28),
    ])

    return bg
  }

  // MARK: - /fleet/deploy-options fetch

  private func currentAppSlug() -> String {
    let inherited = UserDefaults.standard.string(forKey: "yaverInheritedGuestProjectName") ?? ""
    if !inherited.isEmpty { return inherited }
    return UserDefaults.standard.string(forKey: "yaverLoadedModuleName") ?? "main"
  }

  private func fetchOptions() {
    let app = currentAppSlug()
    guard let url = yaverResolveAgentURL("/fleet/deploy-options?app=\(app.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? app)") else {
      showError("agent URL not configured")
      return
    }
    var req = URLRequest(url: url)
    req.httpMethod = "GET"
    for (k, v) in yaverRelayHeaders() { req.setValue(v, forHTTPHeaderField: k) }
    inFlight?.cancel()
    let task = URLSession.shared.dataTask(with: req) { [weak self] data, resp, err in
      DispatchQueue.main.async {
        guard let self = self else { return }
        self.loadingSpinner?.stopAnimating()
        if let err = err {
          self.showError("fetch failed: \(err.localizedDescription)")
          return
        }
        guard let http = resp as? HTTPURLResponse, http.statusCode == 200, let data = data else {
          let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
          self.showError("fetch failed (status \(code))")
          return
        }
        do {
          let decoded = try JSONDecoder().decode(FleetDeployOptions.self, from: data)
          self.options = decoded
          self.renderMachines()
        } catch {
          self.showError("decode failed: \(error.localizedDescription)")
        }
      }
    }
    inFlight = task
    task.resume()
  }

  private func renderMachines() {
    guard let opts = options, let machines = machineList else { return }
    machines.arrangedSubviews.forEach { $0.removeFromSuperview() }
    subtitleLabel?.text = opts.devices.count == 1
      ? "1 machine — pick a target, then tap to deploy"
      : "\(opts.devices.count) machines — pick a target, then tap to deploy"

    let pickedTargets = currentlySelectedTargets()
    for d in opts.devices {
      machines.addArrangedSubview(makeMachineRow(d, pickedTargets: pickedTargets))
    }
    if let warnings = opts.warnings, !warnings.isEmpty {
      statusLabel?.text = warnings.joined(separator: " · ")
    }
  }

  private func currentlySelectedTargets() -> [String] {
    switch selectedTargetIndex {
    case 0: return ["testflight"]
    case 1: return ["playstore"]
    default: return ["testflight", "playstore"]
    }
  }

  private func makeMachineRow(_ d: FleetDeployDevice, pickedTargets: [String]) -> UIView {
    let row = UIControl()
    row.translatesAutoresizingMaskIntoConstraints = false
    row.layer.cornerRadius = 14
    row.layer.borderWidth = 1
    row.layer.borderColor = UIColor.clear.cgColor

    // OK if EVERY picked target's capability for this device says ok=true.
    // This makes the "Both" segment correctly grey out a Linux box (which
    // can do playstore but not testflight) — the user gets a clear "use
    // a different machine if you want both at once" signal instead of a
    // half-deploy that fails server-side.
    var blockers: [String] = []
    var allOK = true
    for t in pickedTargets {
      if let cap = d.capabilities.first(where: { $0.target == t }) {
        if !cap.ok { allOK = false }
        if !cap.ok, let r = cap.reason, !r.isEmpty {
          blockers.append("\(prettyTarget(t)): \(r)")
        }
      } else {
        allOK = false
        blockers.append("\(prettyTarget(t)): no capability data")
      }
    }
    if !d.probed && allOK {
      // Couldn't probe — treat as blocked so we don't ship a request that
      // will fail at the transport layer.
      allOK = false
      blockers.append(d.probeError ?? "couldn't reach this machine")
    }

    row.backgroundColor = allOK
      ? UIColor(white: 1, alpha: 0.06)
      : UIColor(white: 1, alpha: 0.03)
    row.alpha = allOK ? 1.0 : 0.55
    row.isEnabled = allOK

    let nameLabel = UILabel()
    nameLabel.translatesAutoresizingMaskIntoConstraints = false
    nameLabel.text = (d.alias?.isEmpty == false ? d.alias! : d.name) + (d.isLocal ? " (this phone's primary)" : "")
    nameLabel.textColor = .white
    nameLabel.font = .systemFont(ofSize: 15, weight: .semibold)

    let metaLabel = UILabel()
    metaLabel.translatesAutoresizingMaskIntoConstraints = false
    metaLabel.font = .systemFont(ofSize: 12)
    metaLabel.numberOfLines = 0
    metaLabel.textColor = UIColor(white: 1, alpha: 0.55)
    if allOK {
      metaLabel.text = d.platform + " · ready"
      metaLabel.textColor = UIColor(white: 1, alpha: 0.55)
    } else {
      metaLabel.text = d.platform + " · " + blockers.joined(separator: " · ")
      metaLabel.textColor = UIColor(red: 1, green: 0.7, blue: 0.45, alpha: 1)
    }

    let chevron = UIImageView(image: UIImage(systemName: "chevron.right",
      withConfiguration: UIImage.SymbolConfiguration(pointSize: 13, weight: .semibold)))
    chevron.tintColor = UIColor(white: 1, alpha: 0.45)
    chevron.translatesAutoresizingMaskIntoConstraints = false
    chevron.contentMode = .scaleAspectFit
    chevron.isHidden = !allOK

    row.addSubview(nameLabel)
    row.addSubview(metaLabel)
    row.addSubview(chevron)

    NSLayoutConstraint.activate([
      row.heightAnchor.constraint(greaterThanOrEqualToConstant: 64),

      nameLabel.leadingAnchor.constraint(equalTo: row.leadingAnchor, constant: 14),
      nameLabel.topAnchor.constraint(equalTo: row.topAnchor, constant: 12),
      nameLabel.trailingAnchor.constraint(lessThanOrEqualTo: chevron.leadingAnchor, constant: -8),

      metaLabel.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),
      metaLabel.topAnchor.constraint(equalTo: nameLabel.bottomAnchor, constant: 2),
      metaLabel.trailingAnchor.constraint(lessThanOrEqualTo: chevron.leadingAnchor, constant: -8),
      metaLabel.bottomAnchor.constraint(equalTo: row.bottomAnchor, constant: -12),

      chevron.trailingAnchor.constraint(equalTo: row.trailingAnchor, constant: -14),
      chevron.centerYAnchor.constraint(equalTo: row.centerYAnchor),
      chevron.widthAnchor.constraint(equalToConstant: 14),
      chevron.heightAnchor.constraint(equalToConstant: 14),
    ])

    row.accessibilityIdentifier = d.deviceId
    row.addTarget(self, action: #selector(machineTapped(_:)), for: .touchUpInside)
    return row
  }

  // MARK: - Actions

  @objc private func targetChanged(_ sender: UISegmentedControl) {
    selectedTargetIndex = sender.selectedSegmentIndex
    renderMachines()
  }

  @objc private func machineTapped(_ sender: UIControl) {
    guard let deviceId = sender.accessibilityIdentifier, let opts = options else { return }
    UISelectionFeedbackGenerator().selectionChanged()
    triggerDeploy(app: opts.app, machine: deviceId)
  }

  /// POST /deploy/ship with {app, target/targets, machine}. We don't wait
  /// for the SSE stream — fire, show a toast, dismiss. The user can watch
  /// progress in the desktop/web Deploy tab. Mobile-side full streaming
  /// is a future enhancement.
  private func triggerDeploy(app: String, machine: String) {
    statusLabel?.text = "starting deploy on \(prettyMachineName(machine))…"
    let targets = currentlySelectedTargets()
    var body: [String: Any] = [
      "app": app,
      "machine": machine,
    ]
    if targets.count == 1 {
      body["target"] = targets[0]
    } else {
      body["targets"] = targets
    }
    guard let url = yaverResolveAgentURL("/deploy/ship") else {
      showError("agent URL not configured")
      return
    }
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    for (k, v) in yaverRelayHeaders() { req.setValue(v, forHTTPHeaderField: k) }
    req.httpBody = try? JSONSerialization.data(withJSONObject: body)

    let task = URLSession.shared.dataTask(with: req) { [weak self] _, resp, err in
      DispatchQueue.main.async {
        guard let self = self else { return }
        if let err = err {
          self.showError("ship failed: \(err.localizedDescription)")
          return
        }
        guard let http = resp as? HTTPURLResponse else {
          self.showError("ship failed: no response")
          return
        }
        if http.statusCode >= 200 && http.statusCode < 300 {
          self.statusLabel?.text = "deploy started — track progress in the desktop / web tab"
          self.statusLabel?.textColor = UIColor(red: 0.13, green: 0.77, blue: 0.37, alpha: 1)
          UINotificationFeedbackGenerator().notificationOccurred(.success)
          DispatchQueue.main.asyncAfter(deadline: .now() + 1.6) { [weak self] in
            self?.dismiss()
          }
        } else {
          self.showError("ship failed (status \(http.statusCode))")
        }
      }
    }
    task.resume()
  }

  // MARK: - Helpers

  private func prettyTarget(_ t: String) -> String {
    switch t {
    case "testflight": return "TestFlight"
    case "playstore": return "Play Store"
    default: return t
    }
  }

  private func prettyMachineName(_ deviceId: String) -> String {
    guard let opts = options else { return deviceId }
    if let d = opts.devices.first(where: { $0.deviceId == deviceId }) {
      return (d.alias?.isEmpty == false ? d.alias! : d.name)
    }
    return deviceId
  }

  private func showError(_ msg: String) {
    loadingSpinner?.stopAnimating()
    statusLabel?.text = msg
    statusLabel?.textColor = UIColor(red: 1, green: 0.45, blue: 0.45, alpha: 1)
  }

  @objc private func dismissTapped() { dismiss() }

  private func dismiss() {
    inFlight?.cancel()
    inFlight = nil
    guard let card = cardView else { return }
    UIView.animate(withDuration: 0.22, animations: {
      card.transform = CGAffineTransform(translationX: 0, y: 600)
      card.alpha = 0
    }, completion: { _ in
      card.removeFromSuperview()
      self.cardView = nil
      self.options = nil
    })
  }
}
