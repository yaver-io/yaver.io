import Foundation
import UIKit
import SafariServices

// humanizeRunnerAuthFailure converts an /runner-auth/status request error
// into a single short user-facing line. Avoid leaking raw JSON bodies
// (`{"error":"invalid relay password"}`), HTTP status codes by themselves,
// or NSURLError prefixes — none of them tell the user what they should
// actually do next. The mapping is deliberately small: one phrase per
// failure class with a hint on the recovery action.
func humanizeRunnerAuthFailure(code: Int, body: String?, networkErr: Error?) -> String {
  // Pull a normalized error message out of the body so we can spot
  // common server-side reasons (relay password mismatch, missing token,
  // etc.) without showing the raw JSON to the user.
  let normalizedBody: String = {
    guard let body = body?.trimmingCharacters(in: .whitespacesAndNewlines), !body.isEmpty else {
      return ""
    }
    if let data = body.data(using: .utf8),
       let parsed = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
      if let s = parsed["error"] as? String { return s.lowercased() }
      if let s = parsed["message"] as? String { return s.lowercased() }
    }
    return body.lowercased()
  }()

  if networkErr != nil {
    return "Agent unreachable · check the device is online"
  }

  if normalizedBody.contains("relay password") || normalizedBody.contains("invalid relay") {
    return "Relay password mismatch · re-auth Yaver"
  }
  if normalizedBody.contains("expired") {
    return "Session expired · sign in again"
  }
  if normalizedBody.contains("not authenticated") || normalizedBody.contains("missing or invalid auth") || normalizedBody.contains("invalid token") {
    return "Not signed in · tap to sign in"
  }

  switch code {
  case 401, 403:
    return "Not signed in · tap to sign in"
  case 404:
    return "Endpoint missing · update host Yaver"
  case 408, 504:
    return "Agent timed out · try again"
  case 429:
    return "Rate limited · try again later"
  case 500...599:
    return "Agent error · try again"
  case 0:
    return "Agent offline · check device"
  default:
    return "Agent HTTP \(code) · tap to retry"
  }
}

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
    // Allow status text to wrap to 2 lines so longer humanized error
    // strings ("Relay password mismatch · re-auth Yaver", etc.) aren't
    // clipped to a single line that runs off the right edge.
    status.numberOfLines = 2
    status.lineBreakMode = .byWordWrapping

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
    guard !agentBase.isEmpty, let url = URL(string: "\(agentBase)/runner-auth/status") else {
      let msg = "no agent URL set — load a guest bundle first"
      setRowStatus(runner: "claude", text: msg, tone: .error)
      setRowStatus(runner: "codex", text: msg, tone: .error)
      setRowStatus(runner: "opencode", text: msg, tone: .error)
      return
    }
    var req = URLRequest(url: url)
    req.setValue("Bearer \(auth)", forHTTPHeaderField: "Authorization")
    URLSession.shared.dataTask(with: req) { data, resp, err in
      DispatchQueue.main.async {
        // Show humanized status text instead of raw JSON / NSURLError
        // prefixes — `HTTP 401: {"error":"invalid relay password"}` is
        // unhelpful UI; "Not authenticated · tap to sign in" is what the
        // user actually needs to see.
        if let err = err {
          let msg = humanizeRunnerAuthFailure(code: 0, body: nil, networkErr: err)
          self.setRowStatus(runner: "claude", text: msg, tone: .error)
          self.setRowStatus(runner: "codex", text: msg, tone: .error)
          self.setRowStatus(runner: "opencode", text: msg, tone: .error)
          return
        }
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if code < 200 || code >= 300 {
          let body = data.flatMap { String(data: $0, encoding: .utf8) }
          let msg = humanizeRunnerAuthFailure(code: code, body: body, networkErr: nil)
          self.setRowStatus(runner: "claude", text: msg, tone: .error)
          self.setRowStatus(runner: "codex", text: msg, tone: .error)
          self.setRowStatus(runner: "opencode", text: msg, tone: .error)
          return
        }
        guard
          let data = data,
          let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let runners = json["runners"] as? [[String: Any]]
        else {
          let msg = "Agent returned unexpected data · tap to retry"
          self.setRowStatus(runner: "claude", text: msg, tone: .error)
          self.setRowStatus(runner: "codex", text: msg, tone: .error)
          self.setRowStatus(runner: "opencode", text: msg, tone: .error)
          return
        }
        // Initialize all rows to "not installed" then apply observed runners,
        // so a runner missing entirely from the response shows the right state.
        self.setRowStatus(runner: "claude", text: "not installed on agent", tone: .warning)
        self.setRowStatus(runner: "codex", text: "not installed on agent", tone: .warning)
        self.setRowStatus(runner: "opencode", text: "not installed on agent", tone: .warning)
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
        // Agent wraps the session in {ok, session: {...}} — see
        // desktop/agent/runner_auth_browser_http.go::handleRunnerBrowserAuthStart.
        let session = (json["session"] as? [String: Any]) ?? [:]
        let sessionId = (session["id"] as? String) ?? ""
        let openUrl = (session["openUrl"] as? String) ?? ""
        let userCode = (session["code"] as? String) ?? ""
        if sessionId.isEmpty || openUrl.isEmpty {
          let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
          let body = String(data: data, encoding: .utf8)?.prefix(180) ?? ""
          self.finishBrowserAuth(runner: runner, ok: false,
                                 msg: "no session URL (HTTP \(code)) \(body)")
          return
        }
        self.pendingSession = (runner: runner, sessionId: sessionId)
        self.setRowStatus(runner: runner, text: "complete sign-in in browser…", tone: .warning)
        // Push the auth flow sub-pane. Claude needs paste-back, Codex
        // doesn't — the sub-pane shows the right UI per runner. The
        // sub-pane owns polling + cancel + paste-back submission, and
        // calls back to refresh auth-status when it terminates.
        guard let win = self.window else { return }
        YaverRunnerAuthFlowPane.shared.present(
          in: win,
          runner: runner,
          sessionId: sessionId,
          openUrl: openUrl,
          userCode: userCode,
          authToken: self.bestAuthToken(),
          agentBase: agentBase,
          onTerminal: { [weak self] outcome in
            guard let self = self else { return }
            switch outcome {
            case .success:
              self.finishBrowserAuth(runner: runner, ok: true, msg: "✓ signed in")
              self.refreshAuthStatus()
            case .failed(let msg):
              self.finishBrowserAuth(runner: runner, ok: false, msg: "sign-in failed: \(msg)")
            case .cancelled:
              self.finishBrowserAuth(runner: runner, ok: false, msg: "sign-in cancelled")
            }
          }
        )
      }
    }.resume()
  }

  private func finishBrowserAuth(runner: String, ok: Bool, msg: String) {
    inFlightRunner = nil
    rowsByRunner[runner]?.spinner.stopAnimating()
    setRowStatus(runner: runner, text: msg, tone: ok ? .ok : .error)
    if ok { UINotificationFeedbackGenerator().notificationOccurred(.success) }
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

// MARK: - Runner browser-auth flow sub-pane

/// Shown after YaverAgentsPane successfully starts a /runner-auth/browser/
/// session for Claude or Codex. Owns the user-facing auth flow:
///
///   - Always: a button that opens the OAuth URL in SFSafariViewController.
///   - Codex: shows the user-code (e.g. "ABCD-EFGH") for the device-auth
///     page; user enters it on auth.openai.com and authorizes; agent
///     polls OpenAI internally and the status flips to completed without
///     anything else from the user.
///   - Claude (claude / claude-code): paste-back input + Submit. After
///     authorising on platform.claude.com, Anthropic's callback page
///     hands the user a verifier code they must paste here, which we
///     POST to /runner-auth/browser/submit-code. Agent then forwards
///     the code to claude CLI to finalise the OAuth handshake.
///
/// Polls /runner-auth/browser/status every 1.5s. On terminal status
/// (completed / failed / cancelled) calls onTerminal and dismisses.
final class YaverRunnerAuthFlowPane: NSObject {

  static let shared = YaverRunnerAuthFlowPane()

  enum Outcome {
    case success
    case failed(String)
    case cancelled
  }

  private weak var window: UIWindow?
  private weak var cardView: UIView?
  private weak var statusLabel: UILabel?
  private weak var openButton: UIButton?
  private weak var pasteField: UITextField?
  private weak var submitButton: UIButton?
  private var runner: String = ""
  private var sessionId: String = ""
  private var openUrl: String = ""
  private var userCode: String = ""
  private var authToken: String = ""
  private var agentBase: String = ""
  private var pollTimer: Timer?
  private var onTerminal: ((Outcome) -> Void)?
  private var didSettle = false

  private var requiresPasteBack: Bool {
    let r = runner.lowercased()
    return r == "claude" || r == "claude-code"
  }

  func present(in window: UIWindow,
               runner: String,
               sessionId: String,
               openUrl: String,
               userCode: String,
               authToken: String,
               agentBase: String,
               onTerminal: @escaping (Outcome) -> Void) {
    self.runner = runner
    self.sessionId = sessionId
    self.openUrl = openUrl
    self.userCode = userCode
    self.authToken = authToken
    self.agentBase = agentBase
    self.onTerminal = onTerminal
    self.didSettle = false

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

    // Start polling immediately. For Codex this carries the whole flow
    // home; for Claude it'll be redundant once the paste-back submit
    // returns the final state, but harmless.
    startPolling()
    // Open the authorize page automatically — saves the user a tap.
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.35) { [weak self] in
      self?.openAuthorizePage()
    }
  }

  private func runnerLabel() -> String {
    switch runner.lowercased() {
    case "claude", "claude-code": return "Claude Code"
    case "codex": return "Codex"
    default: return runner
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
    title.text = "Sign in to \(runnerLabel())"
    title.textColor = .white
    title.font = .systemFont(ofSize: 17, weight: .semibold)

    let subtitle = UILabel()
    subtitle.translatesAutoresizingMaskIntoConstraints = false
    subtitle.text = requiresPasteBack
      ? "Authorize on platform.claude.com, then paste the code below."
      : "Authorize on the device-auth page; this dialog turns green automatically."
    subtitle.textColor = UIColor(white: 1, alpha: 0.6)
    subtitle.font = .systemFont(ofSize: 12)
    subtitle.numberOfLines = 0

    let close = UIButton(type: .system)
    close.translatesAutoresizingMaskIntoConstraints = false
    close.setImage(UIImage(systemName: "xmark", withConfiguration:
                            UIImage.SymbolConfiguration(pointSize: 16, weight: .semibold)), for: .normal)
    close.tintColor = UIColor(white: 1, alpha: 0.6)
    close.addTarget(self, action: #selector(cancelTapped), for: .touchUpInside)

    let openBtn = UIButton(type: .system)
    openBtn.translatesAutoresizingMaskIntoConstraints = false
    openBtn.setTitle("↗ Open authorize page", for: .normal)
    openBtn.titleLabel?.font = .systemFont(ofSize: 15, weight: .semibold)
    openBtn.setTitleColor(.white, for: .normal)
    openBtn.backgroundColor = UIColor(red: 0.46, green: 0.51, blue: 0.96, alpha: 1)
    openBtn.layer.cornerRadius = 12
    openBtn.heightAnchor.constraint(equalToConstant: 48).isActive = true
    openBtn.addTarget(self, action: #selector(openTapped), for: .touchUpInside)
    openButton = openBtn

    // Codex: show the user code prominently; that's what the user types
    // on the OpenAI device-auth page. For Claude, agent rarely returns
    // a code in the start payload (it comes back from the user via
    // paste-back) so this card stays hidden.
    let codeCard = UIView()
    codeCard.translatesAutoresizingMaskIntoConstraints = false
    codeCard.backgroundColor = UIColor(white: 1, alpha: 0.06)
    codeCard.layer.cornerRadius = 12
    codeCard.heightAnchor.constraint(equalToConstant: 64).isActive = true
    let codeTitle = UILabel()
    codeTitle.translatesAutoresizingMaskIntoConstraints = false
    codeTitle.text = "Enter this code"
    codeTitle.textColor = UIColor(white: 1, alpha: 0.55)
    codeTitle.font = .systemFont(ofSize: 11, weight: .semibold)
    let codeValue = UILabel()
    codeValue.translatesAutoresizingMaskIntoConstraints = false
    codeValue.text = userCode
    codeValue.textColor = .white
    codeValue.font = .monospacedSystemFont(ofSize: 20, weight: .bold)
    codeCard.addSubview(codeTitle)
    codeCard.addSubview(codeValue)
    NSLayoutConstraint.activate([
      codeTitle.leadingAnchor.constraint(equalTo: codeCard.leadingAnchor, constant: 14),
      codeTitle.topAnchor.constraint(equalTo: codeCard.topAnchor, constant: 10),
      codeValue.leadingAnchor.constraint(equalTo: codeTitle.leadingAnchor),
      codeValue.topAnchor.constraint(equalTo: codeTitle.bottomAnchor, constant: 2),
    ])
    codeCard.isHidden = userCode.isEmpty || requiresPasteBack

    // Paste-back row (Claude only).
    let pasteRow = UIView()
    pasteRow.translatesAutoresizingMaskIntoConstraints = false
    let pasteField = UITextField()
    pasteField.translatesAutoresizingMaskIntoConstraints = false
    pasteField.backgroundColor = UIColor(white: 1, alpha: 0.08)
    pasteField.layer.cornerRadius = 10
    pasteField.textColor = .white
    pasteField.font = .systemFont(ofSize: 14)
    pasteField.placeholder = "Paste code from claude.com"
    pasteField.attributedPlaceholder = NSAttributedString(
      string: "Paste code from claude.com",
      attributes: [.foregroundColor: UIColor(white: 1, alpha: 0.35)]
    )
    pasteField.autocapitalizationType = .none
    pasteField.autocorrectionType = .no
    pasteField.spellCheckingType = .no
    pasteField.heightAnchor.constraint(equalToConstant: 42).isActive = true
    pasteField.leftView = UIView(frame: CGRect(x: 0, y: 0, width: 12, height: 42))
    pasteField.leftViewMode = .always
    self.pasteField = pasteField

    let submitBtn = UIButton(type: .system)
    submitBtn.translatesAutoresizingMaskIntoConstraints = false
    submitBtn.setTitle("Submit", for: .normal)
    submitBtn.titleLabel?.font = .systemFont(ofSize: 14, weight: .semibold)
    submitBtn.setTitleColor(.white, for: .normal)
    submitBtn.backgroundColor = UIColor(red: 0.46, green: 0.51, blue: 0.96, alpha: 1)
    submitBtn.layer.cornerRadius = 10
    submitBtn.widthAnchor.constraint(equalToConstant: 92).isActive = true
    submitBtn.heightAnchor.constraint(equalToConstant: 42).isActive = true
    submitBtn.addTarget(self, action: #selector(submitTapped), for: .touchUpInside)
    submitButton = submitBtn

    let pasteHStack = UIStackView(arrangedSubviews: [pasteField, submitBtn])
    pasteHStack.translatesAutoresizingMaskIntoConstraints = false
    pasteHStack.axis = .horizontal
    pasteHStack.spacing = 10
    pasteRow.addSubview(pasteHStack)
    NSLayoutConstraint.activate([
      pasteHStack.leadingAnchor.constraint(equalTo: pasteRow.leadingAnchor),
      pasteHStack.trailingAnchor.constraint(equalTo: pasteRow.trailingAnchor),
      pasteHStack.topAnchor.constraint(equalTo: pasteRow.topAnchor),
      pasteHStack.bottomAnchor.constraint(equalTo: pasteRow.bottomAnchor),
    ])
    pasteRow.isHidden = !requiresPasteBack

    let status = UILabel()
    status.translatesAutoresizingMaskIntoConstraints = false
    status.font = .systemFont(ofSize: 12, weight: .medium)
    status.textColor = UIColor(white: 1, alpha: 0.65)
    status.numberOfLines = 0
    status.textAlignment = .center
    status.text = "waiting for sign-in…"
    statusLabel = status

    let stack = UIStackView(arrangedSubviews: [openBtn, codeCard, pasteRow, status])
    stack.translatesAutoresizingMaskIntoConstraints = false
    stack.axis = .vertical
    stack.spacing = 14

    bg.contentView.addSubview(title)
    bg.contentView.addSubview(subtitle)
    bg.contentView.addSubview(close)
    bg.contentView.addSubview(stack)

    NSLayoutConstraint.activate([
      bg.heightAnchor.constraint(greaterThanOrEqualToConstant: 320),
      title.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      title.topAnchor.constraint(equalTo: bg.contentView.topAnchor, constant: 22),
      subtitle.leadingAnchor.constraint(equalTo: title.leadingAnchor),
      subtitle.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -56),
      subtitle.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 4),
      close.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -16),
      close.centerYAnchor.constraint(equalTo: title.centerYAnchor),
      close.widthAnchor.constraint(equalToConstant: 32),
      close.heightAnchor.constraint(equalToConstant: 32),
      stack.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      stack.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -18),
      stack.topAnchor.constraint(equalTo: subtitle.bottomAnchor, constant: 18),
      stack.bottomAnchor.constraint(lessThanOrEqualTo: bg.contentView.bottomAnchor, constant: -28),
    ])

    return bg
  }

  // MARK: - Actions

  @objc private func openTapped() { openAuthorizePage() }

  private func openAuthorizePage() {
    guard let url = URL(string: openUrl),
          let host = self.window?.rootViewController?.presentedViewController ?? self.window?.rootViewController
    else { return }
    let safari = SFSafariViewController(url: url)
    safari.preferredControlTintColor = UIColor(red: 0.62, green: 0.66, blue: 0.99, alpha: 1)
    host.present(safari, animated: true)
  }

  @objc private func submitTapped() {
    let raw = (pasteField?.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    if raw.isEmpty { setStatus("Paste the code from the callback page", tone: .error); return }
    setStatus("verifying…", tone: .progress)
    submitButton?.isEnabled = false

    guard let url = URL(string: "\(agentBase)/runner-auth/browser/submit-code") else { return }
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    req.setValue("Bearer \(authToken)", forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    let body: [String: Any] = ["id": sessionId, "code": raw]
    req.httpBody = try? JSONSerialization.data(withJSONObject: body)
    URLSession.shared.dataTask(with: req) { data, resp, err in
      DispatchQueue.main.async {
        self.submitButton?.isEnabled = true
        if let err = err {
          self.setStatus("submit failed: \(err.localizedDescription)", tone: .error); return
        }
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if code < 200 || code >= 300 {
          var detail = "HTTP \(code)"
          if let d = data, let body = String(data: d, encoding: .utf8) {
            let trimmed = body.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty { detail = "HTTP \(code): \(trimmed.prefix(200))" }
          }
          self.setStatus("submit failed — \(detail)", tone: .error); return
        }
        // Submit accepted; the polling loop will see status=completed
        // shortly when claude CLI finalises and mark the row green.
        self.setStatus("code accepted, finalising…", tone: .progress)
      }
    }.resume()
  }

  @objc private func cancelTapped() {
    if didSettle { dismiss(); return }
    didSettle = true
    pollTimer?.invalidate(); pollTimer = nil

    // Best-effort cancel on the agent side so the runner CLI exits.
    if !agentBase.isEmpty, let url = URL(string: "\(agentBase)/runner-auth/browser/cancel?id=\(sessionId)") {
      var req = URLRequest(url: url)
      req.httpMethod = "POST"
      req.setValue("Bearer \(authToken)", forHTTPHeaderField: "Authorization")
      URLSession.shared.dataTask(with: req).resume()
    }
    let cb = onTerminal
    onTerminal = nil
    cb?(.cancelled)
    dismiss()
  }

  // MARK: - Polling

  private func startPolling() {
    pollTimer?.invalidate()
    pollTimer = Timer.scheduledTimer(withTimeInterval: 1.5, repeats: true) { [weak self] _ in
      self?.pollOnce()
    }
  }

  private func pollOnce() {
    guard !didSettle else { return }
    guard let url = URL(string: "\(agentBase)/runner-auth/browser/status?id=\(sessionId)") else { return }
    var req = URLRequest(url: url)
    req.setValue("Bearer \(authToken)", forHTTPHeaderField: "Authorization")
    URLSession.shared.dataTask(with: req) { data, _, _ in
      DispatchQueue.main.async {
        guard !self.didSettle else { return }
        guard let data = data,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return }
        let session = (json["session"] as? [String: Any]) ?? [:]
        let status = (session["status"] as? String) ?? (json["status"] as? String) ?? ""
        switch status {
        case "completed":
          self.didSettle = true
          self.pollTimer?.invalidate(); self.pollTimer = nil
          self.setStatus("✓ signed in", tone: .ok)
          UINotificationFeedbackGenerator().notificationOccurred(.success)
          let cb = self.onTerminal
          self.onTerminal = nil
          DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
            cb?(.success)
            self.dismiss()
          }
        case "failed", "cancelled":
          self.didSettle = true
          self.pollTimer?.invalidate(); self.pollTimer = nil
          let detail = (session["error"] as? String) ?? (json["error"] as? String) ?? status
          self.setStatus("\(status): \(detail)", tone: .error)
          let cb = self.onTerminal
          self.onTerminal = nil
          DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            if status == "cancelled" { cb?(.cancelled) } else { cb?(.failed(detail)) }
            self.dismiss()
          }
        default:
          break
        }
      }
    }.resume()
  }

  // MARK: - Helpers

  private enum Tone { case progress, ok, error }
  private func setStatus(_ msg: String, tone: Tone) {
    statusLabel?.text = msg
    switch tone {
    case .progress: statusLabel?.textColor = UIColor(white: 1, alpha: 0.7)
    case .ok:       statusLabel?.textColor = UIColor(red: 0.34, green: 0.85, blue: 0.55, alpha: 1)
    case .error:    statusLabel?.textColor = UIColor(red: 1.00, green: 0.45, blue: 0.45, alpha: 1)
    }
  }

  private func dismiss() {
    pollTimer?.invalidate(); pollTimer = nil
    pasteField?.resignFirstResponder()
    guard let card = cardView else { return }
    UIView.animate(withDuration: 0.22, animations: {
      card.transform = CGAffineTransform(translationX: 0, y: 600)
      card.alpha = 0
    }, completion: { _ in
      card.removeFromSuperview()
      self.cardView = nil
    })
  }
}
