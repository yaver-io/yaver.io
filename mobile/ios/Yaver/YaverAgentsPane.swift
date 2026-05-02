import Foundation
import UIKit
import SafariServices

/// Native coding-agents pane — second shake-overlay action ("Agents").
/// Shows the three first-class runners (Claude Code / Codex / OpenCode)
/// with auth status read from /runner-auth/status, and routes taps to:
///
///   - Claude Code / Codex: start the browser-auth flow
///     (POST /runner-auth/browser/start) and open the OAuth URL in
///     SFSafariViewController. For Claude, also surface a paste-back
///     code field once the user copies the verifier from the callback.
///
///   - OpenCode: open a sub-pane for picking build vs plan mode and
///     entering optional API keys (GLM, OpenAI, Anthropic) — all stored
///     server-side via /runner-auth/set.
///
/// Shares the deep purple-black bottom-sheet styling from
/// YaverFeedbackPane (visual consistency, code is intentionally
/// duplicated rather than abstracted into a base class — both panes
/// have different content layout and the shared surface is small).
final class YaverAgentsPane: NSObject {

  static let shared = YaverAgentsPane()

  // MARK: - State

  private weak var window: UIWindow?
  private weak var cardView: UIView?
  private weak var listStack: UIStackView?
  private weak var statusLabel: UILabel?
  private var rowsByRunner: [String: AgentRowViews] = [:]
  private var inFlightRunner: String?
  // Active browser-auth session (per-runner). Held so the polling loop
  // and the eventual paste-back submit can reference the same id.
  private var pendingSession: (runner: String, sessionId: String)?
  private var pollTimer: Timer?

  // Encapsulates the visible widgets for one runner row so we can
  // update auth-status pill + spinner in place after async lookups.
  private struct AgentRowViews {
    let row: UIView
    let title: UILabel
    let status: UILabel
    let chevron: UIImageView
    let spinner: UIActivityIndicatorView
  }

  // MARK: - Public entry

  func present(in window: UIWindow) {
    let pane = buildCard()
    window.addSubview(pane)
    NSLayoutConstraint.activate([
      pane.leadingAnchor.constraint(equalTo: window.leadingAnchor),
      pane.trailingAnchor.constraint(equalTo: window.trailingAnchor),
      pane.bottomAnchor.constraint(equalTo: window.bottomAnchor),
    ])
    self.window = window
    self.cardView = pane
    pane.transform = CGAffineTransform(translationX: 0, y: 600)
    UIView.animate(withDuration: 0.32, delay: 0, usingSpringWithDamping: 0.9,
                   initialSpringVelocity: 0.4) {
      pane.transform = .identity
    }
    UIImpactFeedbackGenerator(style: .light).impactOccurred()
    // Fetch fresh auth status now and refresh the rows.
    refreshAuthStatus()
  }

  // MARK: - UI construction

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
    title.text = "Coding Agents"
    title.textColor = .white
    title.font = .systemFont(ofSize: 17, weight: .semibold)

    let subtitle = UILabel()
    subtitle.translatesAutoresizingMaskIntoConstraints = false
    subtitle.text = "tap to sign in or configure"
    subtitle.textColor = UIColor(white: 1, alpha: 0.55)
    subtitle.font = .systemFont(ofSize: 12)

    let close = UIButton(type: .system)
    close.translatesAutoresizingMaskIntoConstraints = false
    close.setImage(UIImage(systemName: "xmark", withConfiguration:
                            UIImage.SymbolConfiguration(pointSize: 16, weight: .semibold)), for: .normal)
    close.tintColor = UIColor(white: 1, alpha: 0.6)
    close.addTarget(self, action: #selector(dismissTapped), for: .touchUpInside)

    // Three rows — Claude / Codex / OpenCode.
    let claudeRow = makeRow(runnerId: "claude", label: "Claude Code")
    let codexRow = makeRow(runnerId: "codex", label: "Codex")
    let opencodeRow = makeRow(runnerId: "opencode", label: "OpenCode")

    let list = UIStackView(arrangedSubviews: [claudeRow.row, codexRow.row, opencodeRow.row])
    list.translatesAutoresizingMaskIntoConstraints = false
    list.axis = .vertical
    list.spacing = 8
    listStack = list

    rowsByRunner["claude"] = claudeRow
    rowsByRunner["codex"] = codexRow
    rowsByRunner["opencode"] = opencodeRow

    let status = UILabel()
    status.translatesAutoresizingMaskIntoConstraints = false
    status.font = .systemFont(ofSize: 12, weight: .medium)
    status.textColor = UIColor(white: 1, alpha: 0.6)
    status.numberOfLines = 0
    status.textAlignment = .center
    status.text = " "
    statusLabel = status

    bg.contentView.addSubview(handle)
    bg.contentView.addSubview(title)
    bg.contentView.addSubview(subtitle)
    bg.contentView.addSubview(close)
    bg.contentView.addSubview(list)
    bg.contentView.addSubview(status)

    NSLayoutConstraint.activate([
      bg.heightAnchor.constraint(greaterThanOrEqualToConstant: 360),

      handle.centerXAnchor.constraint(equalTo: bg.contentView.centerXAnchor),
      handle.topAnchor.constraint(equalTo: bg.contentView.topAnchor, constant: 8),
      handle.widthAnchor.constraint(equalToConstant: 38),
      handle.heightAnchor.constraint(equalToConstant: 5),

      title.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      title.topAnchor.constraint(equalTo: handle.bottomAnchor, constant: 14),
      subtitle.leadingAnchor.constraint(equalTo: title.leadingAnchor),
      subtitle.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 2),

      close.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -16),
      close.centerYAnchor.constraint(equalTo: title.centerYAnchor),
      close.widthAnchor.constraint(equalToConstant: 32),
      close.heightAnchor.constraint(equalToConstant: 32),

      list.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      list.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -18),
      list.topAnchor.constraint(equalTo: subtitle.bottomAnchor, constant: 18),

      status.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      status.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -18),
      status.topAnchor.constraint(equalTo: list.bottomAnchor, constant: 14),
      status.bottomAnchor.constraint(lessThanOrEqualTo: bg.contentView.bottomAnchor, constant: -28),
    ])

    return bg
  }

  private func makeRow(runnerId: String, label: String) -> AgentRowViews {
    let row = UIView()
    row.translatesAutoresizingMaskIntoConstraints = false
    row.backgroundColor = UIColor(white: 1, alpha: 0.06)
    row.layer.cornerRadius = 14
    row.heightAnchor.constraint(greaterThanOrEqualToConstant: 64).isActive = true
    row.tag = runnerId.hashValue
    let tap = UITapGestureRecognizer(target: self, action: #selector(rowTapped(_:)))
    row.addGestureRecognizer(tap)
    row.isUserInteractionEnabled = true

    let icon = UIImageView(image: UIImage(systemName: iconName(for: runnerId)))
    icon.translatesAutoresizingMaskIntoConstraints = false
    icon.tintColor = UIColor(red: 0.62, green: 0.66, blue: 0.99, alpha: 1)
    icon.contentMode = .scaleAspectFit

    let title = UILabel()
    title.translatesAutoresizingMaskIntoConstraints = false
    title.text = label
    title.textColor = .white
    title.font = .systemFont(ofSize: 16, weight: .semibold)

    let status = UILabel()
    status.translatesAutoresizingMaskIntoConstraints = false
    status.text = "checking…"
    status.textColor = UIColor(white: 1, alpha: 0.55)
    status.font = .systemFont(ofSize: 12)

    let chevron = UIImageView(image: UIImage(systemName: "chevron.right"))
    chevron.translatesAutoresizingMaskIntoConstraints = false
    chevron.tintColor = UIColor(white: 1, alpha: 0.4)
    chevron.contentMode = .scaleAspectFit

    let spinner = UIActivityIndicatorView(style: .medium)
    spinner.translatesAutoresizingMaskIntoConstraints = false
    spinner.color = UIColor(white: 1, alpha: 0.7)
    spinner.hidesWhenStopped = true

    row.addSubview(icon)
    row.addSubview(title)
    row.addSubview(status)
    row.addSubview(chevron)
    row.addSubview(spinner)

    NSLayoutConstraint.activate([
      icon.leadingAnchor.constraint(equalTo: row.leadingAnchor, constant: 14),
      icon.centerYAnchor.constraint(equalTo: row.centerYAnchor),
      icon.widthAnchor.constraint(equalToConstant: 22),
      icon.heightAnchor.constraint(equalToConstant: 22),

      title.leadingAnchor.constraint(equalTo: icon.trailingAnchor, constant: 12),
      title.topAnchor.constraint(equalTo: row.topAnchor, constant: 12),

      status.leadingAnchor.constraint(equalTo: title.leadingAnchor),
      status.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 2),
      status.bottomAnchor.constraint(lessThanOrEqualTo: row.bottomAnchor, constant: -12),

      chevron.trailingAnchor.constraint(equalTo: row.trailingAnchor, constant: -14),
      chevron.centerYAnchor.constraint(equalTo: row.centerYAnchor),
      chevron.widthAnchor.constraint(equalToConstant: 12),
      chevron.heightAnchor.constraint(equalToConstant: 12),

      spinner.trailingAnchor.constraint(equalTo: chevron.leadingAnchor, constant: -10),
      spinner.centerYAnchor.constraint(equalTo: row.centerYAnchor),
    ])

    // Stash the runner id on the row's accessibilityIdentifier so the
    // tap handler can map back. tag-based mapping isn't reliable across
    // runner-id changes so an explicit string is safer.
    row.accessibilityIdentifier = runnerId

    return AgentRowViews(row: row, title: title, status: status, chevron: chevron, spinner: spinner)
  }

  private func iconName(for runner: String) -> String {
    switch runner {
    case "claude": return "sparkles"
    case "codex": return "chevron.left.forwardslash.chevron.right"
    case "opencode": return "shippingbox"
    default: return "person.crop.circle"
    }
  }

  // MARK: - Auth status refresh

  private func refreshAuthStatus() {
    let agentBase = UserDefaults.standard.string(forKey: "yaverAgentBaseURL") ?? ""
    let auth = bestAuthToken()
    guard let url = URL(string: "\(agentBase)/runner-auth/status") else {
      setRowStatus(runner: "claude", text: "no agent URL", tone: .error)
      setRowStatus(runner: "codex", text: "no agent URL", tone: .error)
      setRowStatus(runner: "opencode", text: "no agent URL", tone: .error)
      return
    }
    var req = URLRequest(url: url)
    req.setValue("Bearer \(auth)", forHTTPHeaderField: "Authorization")
    URLSession.shared.dataTask(with: req) { data, resp, _ in
      DispatchQueue.main.async {
        guard
          let data = data,
          let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let runners = json["runners"] as? [[String: Any]]
        else {
          self.setRowStatus(runner: "claude", text: "status check failed", tone: .error)
          self.setRowStatus(runner: "codex", text: "status check failed", tone: .error)
          self.setRowStatus(runner: "opencode", text: "status check failed", tone: .error)
          return
        }
        for r in runners {
          guard let id = r["id"] as? String else { continue }
          let normalized = id.lowercased() == "claude-code" ? "claude" : id.lowercased()
          let authConfigured = (r["authConfigured"] as? Bool) ?? false
          let installed = (r["installed"] as? Bool) ?? false
          self.applyRow(runner: normalized, installed: installed, authed: authConfigured)
        }
      }
    }.resume()
  }

  private func applyRow(runner: String, installed: Bool, authed: Bool) {
    if !installed {
      setRowStatus(runner: runner, text: "not installed on agent", tone: .warning)
      return
    }
    if runner == "opencode" {
      setRowStatus(runner: runner, text: authed ? "configured · tap to edit" : "tap to set API keys", tone: authed ? .ok : .warning)
      return
    }
    setRowStatus(runner: runner, text: authed ? "✓ signed in · tap to re-auth" : "✗ not signed in · tap to sign in", tone: authed ? .ok : .warning)
  }

  enum Tone { case ok, warning, error }

  private func setRowStatus(runner: String, text: String, tone: Tone) {
    guard let row = rowsByRunner[runner] else { return }
    row.status.text = text
    switch tone {
    case .ok:      row.status.textColor = UIColor(red: 0.34, green: 0.85, blue: 0.55, alpha: 1)
    case .warning: row.status.textColor = UIColor(red: 1.00, green: 0.74, blue: 0.27, alpha: 1)
    case .error:   row.status.textColor = UIColor(red: 1.00, green: 0.45, blue: 0.45, alpha: 1)
    }
  }

  // MARK: - Tap handlers

  @objc private func rowTapped(_ rec: UITapGestureRecognizer) {
    guard let runner = rec.view?.accessibilityIdentifier else { return }
    if inFlightRunner != nil { return }
    if runner == "opencode" {
      presentOpencodeSubpane()
      return
    }
    startBrowserAuth(runner: runner)
  }

  @objc private func dismissTapped() { dismiss() }

  private func dismiss() {
    pollTimer?.invalidate(); pollTimer = nil
    pendingSession = nil
    guard let card = cardView else { return }
    UIView.animate(withDuration: 0.22, animations: {
      card.transform = CGAffineTransform(translationX: 0, y: 600)
      card.alpha = 0
    }, completion: { _ in
      card.removeFromSuperview()
      self.cardView = nil
    })
  }

  // MARK: - Browser auth flow

  private func startBrowserAuth(runner: String) {
    inFlightRunner = runner
    setRowStatus(runner: runner, text: "starting sign-in…", tone: .warning)
    rowsByRunner[runner]?.spinner.startAnimating()

    let agentBase = UserDefaults.standard.string(forKey: "yaverAgentBaseURL") ?? ""
    let auth = bestAuthToken()
    guard let url = URL(string: "\(agentBase)/runner-auth/browser/start") else {
      finishBrowserAuth(runner: runner, ok: false, msg: "no agent URL")
      return
    }
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    req.setValue("Bearer \(auth)", forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    let body: [String: Any] = ["runner": runner]
    req.httpBody = try? JSONSerialization.data(withJSONObject: body)
    URLSession.shared.dataTask(with: req) { data, resp, err in
      DispatchQueue.main.async {
        guard let data = data,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else {
          let detail = err?.localizedDescription ?? "no response"
          self.finishBrowserAuth(runner: runner, ok: false, msg: "start failed: \(detail)")
          return
        }
        if let errStr = json["error"] as? String {
          self.finishBrowserAuth(runner: runner, ok: false, msg: errStr)
          return
        }
        let sessionId = (json["id"] as? String) ?? ""
        let openUrl = (json["openUrl"] as? String) ?? ""
        if sessionId.isEmpty || openUrl.isEmpty {
          self.finishBrowserAuth(runner: runner, ok: false, msg: "agent did not return session URL")
          return
        }
        self.pendingSession = (runner: runner, sessionId: sessionId)
        self.openInSafariView(urlString: openUrl)
        self.setRowStatus(runner: runner, text: "complete sign-in in browser…", tone: .warning)
        self.startPollingStatus(sessionId: sessionId, runner: runner)
      }
    }.resume()
  }

  private func startPollingStatus(sessionId: String, runner: String) {
    pollTimer?.invalidate()
    pollTimer = Timer.scheduledTimer(withTimeInterval: 1.5, repeats: true) { [weak self] _ in
      guard let self = self else { return }
      let agentBase = UserDefaults.standard.string(forKey: "yaverAgentBaseURL") ?? ""
      let auth = self.bestAuthToken()
      guard let url = URL(string: "\(agentBase)/runner-auth/browser/status?id=\(sessionId)") else { return }
      var req = URLRequest(url: url)
      req.setValue("Bearer \(auth)", forHTTPHeaderField: "Authorization")
      URLSession.shared.dataTask(with: req) { data, _, _ in
        DispatchQueue.main.async {
          guard let data = data,
                let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
          else { return }
          let status = (json["status"] as? String) ?? ""
          if status == "completed" {
            self.pollTimer?.invalidate(); self.pollTimer = nil
            self.finishBrowserAuth(runner: runner, ok: true, msg: "✓ signed in")
            self.refreshAuthStatus()
          } else if status == "failed" || status == "cancelled" {
            self.pollTimer?.invalidate(); self.pollTimer = nil
            let detail = (json["error"] as? String) ?? status
            self.finishBrowserAuth(runner: runner, ok: false, msg: "sign-in \(status): \(detail)")
          }
        }
      }.resume()
    }
  }

  private func finishBrowserAuth(runner: String, ok: Bool, msg: String) {
    inFlightRunner = nil
    rowsByRunner[runner]?.spinner.stopAnimating()
    setRowStatus(runner: runner, text: msg, tone: ok ? .ok : .error)
    if ok { UINotificationFeedbackGenerator().notificationOccurred(.success) }
  }

  private func openInSafariView(urlString: String) {
    guard let url = URL(string: urlString),
          let host = self.window?.rootViewController else { return }
    let safari = SFSafariViewController(url: url)
    safari.preferredControlTintColor = UIColor(red: 0.62, green: 0.66, blue: 0.99, alpha: 1)
    host.present(safari, animated: true)
  }

  // MARK: - OpenCode sub-pane

  private func presentOpencodeSubpane() {
    guard let win = self.window else { return }
    YaverOpenCodeConfigPane.shared.present(in: win, onSaved: { [weak self] in
      self?.refreshAuthStatus()
    })
  }

  // MARK: - Helpers

  private func bestAuthToken() -> String {
    let inherited = UserDefaults.standard.string(forKey: "yaverInheritedAuthToken") ?? ""
    if !inherited.isEmpty { return inherited }
    return UserDefaults.standard.string(forKey: "yaverAgentAuth") ?? ""
  }
}

// MARK: - OpenCode configuration sub-pane

/// Secondary pane reached from YaverAgentsPane → OpenCode row.
/// Lets the user pick the agent mode (build / plan) and enter optional
/// API keys (GLM, OpenAI, Anthropic). Posts to /runner-auth/set on save.
final class YaverOpenCodeConfigPane: NSObject {

  static let shared = YaverOpenCodeConfigPane()

  private weak var window: UIWindow?
  private weak var cardView: UIView?
  private weak var modeSegment: UISegmentedControl?
  private weak var glmField: UITextField?
  private weak var openaiField: UITextField?
  private weak var anthropicField: UITextField?
  private weak var statusLabel: UILabel?
  private var inFlight = false
  private var onSaved: (() -> Void)?

  func present(in window: UIWindow, onSaved: @escaping () -> Void) {
    self.onSaved = onSaved
    let pane = buildCard()
    window.addSubview(pane)
    NSLayoutConstraint.activate([
      pane.leadingAnchor.constraint(equalTo: window.leadingAnchor),
      pane.trailingAnchor.constraint(equalTo: window.trailingAnchor),
      pane.bottomAnchor.constraint(equalTo: window.bottomAnchor),
    ])
    self.window = window
    self.cardView = pane
    pane.transform = CGAffineTransform(translationX: 0, y: 600)
    UIView.animate(withDuration: 0.32, delay: 0, usingSpringWithDamping: 0.9,
                   initialSpringVelocity: 0.4) {
      pane.transform = .identity
    }
  }

  private func buildCard() -> UIView {
    let bg = UIVisualEffectView(effect: UIBlurEffect(style: .systemUltraThinMaterialDark))
    bg.translatesAutoresizingMaskIntoConstraints = false
    bg.layer.cornerRadius = 22
    bg.layer.maskedCorners = [.layerMinXMinYCorner, .layerMaxXMinYCorner]
    bg.clipsToBounds = true
    bg.contentView.backgroundColor = UIColor(red: 0.055, green: 0.047, blue: 0.110, alpha: 0.62)

    let title = UILabel()
    title.translatesAutoresizingMaskIntoConstraints = false
    title.text = "OpenCode"
    title.textColor = .white
    title.font = .systemFont(ofSize: 17, weight: .semibold)

    let close = UIButton(type: .system)
    close.translatesAutoresizingMaskIntoConstraints = false
    close.setImage(UIImage(systemName: "xmark", withConfiguration:
                            UIImage.SymbolConfiguration(pointSize: 16, weight: .semibold)), for: .normal)
    close.tintColor = UIColor(white: 1, alpha: 0.6)
    close.addTarget(self, action: #selector(dismissTapped), for: .touchUpInside)

    let modeLabel = UILabel()
    modeLabel.translatesAutoresizingMaskIntoConstraints = false
    modeLabel.text = "Mode"
    modeLabel.textColor = UIColor(white: 1, alpha: 0.7)
    modeLabel.font = .systemFont(ofSize: 12, weight: .semibold)
    let segment = UISegmentedControl(items: ["Build", "Plan"])
    segment.translatesAutoresizingMaskIntoConstraints = false
    segment.selectedSegmentIndex = 0
    segment.selectedSegmentTintColor = UIColor(red: 0.46, green: 0.51, blue: 0.96, alpha: 1)
    segment.setTitleTextAttributes([.foregroundColor: UIColor.white], for: .selected)
    segment.setTitleTextAttributes([.foregroundColor: UIColor(white: 1, alpha: 0.7)], for: .normal)
    modeSegment = segment

    let glm = makeKeyField(placeholder: "GLM API key (optional)")
    glmField = glm
    let openai = makeKeyField(placeholder: "OpenAI API key (optional)")
    openaiField = openai
    let anthropic = makeKeyField(placeholder: "Anthropic API key (optional)")
    anthropicField = anthropic

    let saveBtn = UIButton(type: .system)
    saveBtn.translatesAutoresizingMaskIntoConstraints = false
    saveBtn.setTitle("Save", for: .normal)
    saveBtn.titleLabel?.font = .systemFont(ofSize: 15, weight: .semibold)
    saveBtn.setTitleColor(.white, for: .normal)
    saveBtn.backgroundColor = UIColor(red: 0.46, green: 0.51, blue: 0.96, alpha: 1)
    saveBtn.layer.cornerRadius = 12
    saveBtn.heightAnchor.constraint(equalToConstant: 48).isActive = true
    saveBtn.addTarget(self, action: #selector(saveTapped), for: .touchUpInside)

    let status = UILabel()
    status.translatesAutoresizingMaskIntoConstraints = false
    status.font = .systemFont(ofSize: 12, weight: .medium)
    status.textColor = UIColor(white: 1, alpha: 0.7)
    status.numberOfLines = 0
    status.textAlignment = .center
    status.text = " "
    statusLabel = status

    let stack = UIStackView(arrangedSubviews: [
      modeLabel, segment, glm, openai, anthropic, saveBtn, status,
    ])
    stack.translatesAutoresizingMaskIntoConstraints = false
    stack.axis = .vertical
    stack.spacing = 10
    stack.setCustomSpacing(14, after: segment)

    bg.contentView.addSubview(title)
    bg.contentView.addSubview(close)
    bg.contentView.addSubview(stack)

    NSLayoutConstraint.activate([
      bg.heightAnchor.constraint(greaterThanOrEqualToConstant: 460),
      title.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      title.topAnchor.constraint(equalTo: bg.contentView.topAnchor, constant: 22),
      close.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -16),
      close.centerYAnchor.constraint(equalTo: title.centerYAnchor),
      close.widthAnchor.constraint(equalToConstant: 32),
      close.heightAnchor.constraint(equalToConstant: 32),
      stack.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      stack.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -18),
      stack.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 22),
      stack.bottomAnchor.constraint(lessThanOrEqualTo: bg.contentView.bottomAnchor, constant: -28),
    ])

    return bg
  }

  private func makeKeyField(placeholder: String) -> UITextField {
    let f = UITextField()
    f.translatesAutoresizingMaskIntoConstraints = false
    f.placeholder = placeholder
    f.attributedPlaceholder = NSAttributedString(
      string: placeholder,
      attributes: [.foregroundColor: UIColor(white: 1, alpha: 0.35)]
    )
    f.textColor = .white
    f.backgroundColor = UIColor(white: 1, alpha: 0.08)
    f.layer.cornerRadius = 10
    f.font = .systemFont(ofSize: 14)
    f.autocapitalizationType = .none
    f.autocorrectionType = .no
    f.isSecureTextEntry = true
    f.heightAnchor.constraint(equalToConstant: 42).isActive = true
    f.leftView = UIView(frame: CGRect(x: 0, y: 0, width: 12, height: 42))
    f.leftViewMode = .always
    f.rightView = UIView(frame: CGRect(x: 0, y: 0, width: 12, height: 42))
    f.rightViewMode = .always
    return f
  }

  @objc private func dismissTapped() {
    guard let card = cardView else { return }
    UIView.animate(withDuration: 0.22, animations: {
      card.transform = CGAffineTransform(translationX: 0, y: 600)
      card.alpha = 0
    }, completion: { _ in
      card.removeFromSuperview()
      self.cardView = nil
    })
  }

  @objc private func saveTapped() {
    if inFlight { return }
    let agentBase = UserDefaults.standard.string(forKey: "yaverAgentBaseURL") ?? ""
    let inherited = UserDefaults.standard.string(forKey: "yaverInheritedAuthToken") ?? ""
    let auth = !inherited.isEmpty ? inherited : (UserDefaults.standard.string(forKey: "yaverAgentAuth") ?? "")
    guard let url = URL(string: "\(agentBase)/runner-auth/set") else {
      setStatus("no agent URL", ok: false); return
    }

    let mode = (modeSegment?.selectedSegmentIndex == 0) ? "build" : "plan"
    var body: [String: Any] = [
      "runner": "opencode",
      "notes": "mode=\(mode)",
    ]
    if let v = glmField?.text, !v.trimmingCharacters(in: .whitespaces).isEmpty {
      body["glm_api_key"] = v.trimmingCharacters(in: .whitespaces)
    }
    if let v = openaiField?.text, !v.trimmingCharacters(in: .whitespaces).isEmpty {
      body["openai_api_key"] = v.trimmingCharacters(in: .whitespaces)
    }
    if let v = anthropicField?.text, !v.trimmingCharacters(in: .whitespaces).isEmpty {
      body["anthropic_api_key"] = v.trimmingCharacters(in: .whitespaces)
    }

    inFlight = true
    setStatus("saving…", ok: true)
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    req.setValue("Bearer \(auth)", forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = try? JSONSerialization.data(withJSONObject: body)
    URLSession.shared.dataTask(with: req) { data, resp, err in
      DispatchQueue.main.async {
        self.inFlight = false
        if let err = err {
          self.setStatus("save failed: \(err.localizedDescription)", ok: false)
        } else if let http = resp as? HTTPURLResponse, http.statusCode >= 200, http.statusCode < 300 {
          self.setStatus("Saved ✓", ok: true)
          UINotificationFeedbackGenerator().notificationOccurred(.success)
          self.onSaved?()
          DispatchQueue.main.asyncAfter(deadline: .now() + 0.7) { self.dismissTapped() }
        } else {
          let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
          var detail = "HTTP \(code)"
          if let data = data, let body = String(data: data, encoding: .utf8) {
            let trimmed = body.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty { detail = "HTTP \(code): \(trimmed.prefix(200))" }
          }
          self.setStatus("save failed — \(detail)", ok: false)
        }
      }
    }.resume()
  }

  private func setStatus(_ msg: String, ok: Bool) {
    statusLabel?.text = msg
    statusLabel?.textColor = ok
      ? UIColor(red: 0.34, green: 0.85, blue: 0.55, alpha: 1)
      : UIColor(red: 1.00, green: 0.45, blue: 0.45, alpha: 1)
  }
}
