import Foundation
import UIKit

/// Yaver's native feedback pane — presented over a loaded guest bundle when
/// the user taps "Feedback" on the shake overlay (AppDelegate.swift::
/// handleFeedbackTap). Lives in the Yaver host so it works for ANY guest
/// app regardless of which version of yaver-feedback-react-native it ships
/// with — even apps that bundle no SDK at all.
///
/// Three controls, mirroring the simplified vibing-task composer:
///   1. Multi-line message input
///   2. Toggle: include screenshot of the running guest
///   3. Two actions — Reload (POST /dev/reload) + Send (POST /tasks with
///      the message + screenshot attachment)
///
/// Auth + agent URL are read from UserDefaults — the same keys the
/// YaverBundleLoader already populates when loading a guest bundle. No
/// JS bridge interaction; works fully native.
final class YaverFeedbackPane: NSObject {

  // MARK: - Public entry point

  static let shared = YaverFeedbackPane()

  /// Slide the pane up over the given window. Captures a snapshot of the
  /// window contents BEFORE the pane appears so the screenshot toggle
  /// shows the guest's UI, not the pane's own card.

  func present(in window: UIWindow) {
    // Stateful guard: if the pane is already on screen, bail. Y-bubble
    // taps + double-shakes used to stack multiple feedback cards
    // simultaneously and the user had to dismiss them one by one.
    if cardView != nil { return }
    // Hide the floating Y bubble while the pane is up — it sits in
    // its own overlay window above this pane and steals taps in
    // the area behind it (text input area in particular).
    YaverFloatingTrigger.shared.hideTemporarily()
    snapshot = captureSnapshot(of: window)
    let pane = buildCard()
    window.addSubview(pane)
    // Pin bottom via a constraint we keep a handle on, so
    // handleKeyboardChange can slide the card up when the keyboard
    // appears (constant = -keyboardHeight) and back down when it
    // dismisses (constant = 0). This keeps the WHOLE card visible
    // above the keyboard — same KeyboardAvoidingView behaviour the
    // Yaver Tasks composer uses on the React-Native side.
    let bottom = pane.bottomAnchor.constraint(equalTo: window.bottomAnchor)
    NSLayoutConstraint.activate([
      pane.leadingAnchor.constraint(equalTo: window.leadingAnchor),
      pane.trailingAnchor.constraint(equalTo: window.trailingAnchor),
      bottom,
    ])
    self.window = window
    self.cardView = pane
    self.cardBottomConstraint = bottom
    pane.transform = CGAffineTransform(translationX: 0, y: 600)
    UIView.animate(withDuration: 0.32, delay: 0, usingSpringWithDamping: 0.9,
                   initialSpringVelocity: 0.4) {
      pane.transform = .identity
    }
    NotificationCenter.default.addObserver(self, selector: #selector(handleKeyboardChange(_:)),
                                           name: UIResponder.keyboardWillChangeFrameNotification, object: nil)
    UIImpactFeedbackGenerator(style: .light).impactOccurred()

    // Preflight: ask the agent which runners are signed in. If
    // nothing is authed, repurpose the subtitle as a "no coding
    // agent signed in · tap to open Agents" CTA so the user knows
    // hitting Send will fail with a relay/auth error and gets a
    // direct route to fix it.
    runRunnerAuthPreflight()
  }

  // MARK: - State

  private weak var window: UIWindow?
  private weak var cardView: UIView?
  private weak var promptField: UITextView?
  private var promptPlaceholder: UILabel?
  private weak var screenshotToggle: UISwitch?
  private weak var sendButton: UIButton?
  private weak var reloadButton: UIButton?
  private weak var statusLabel: UILabel?
  // subtitleLabel doubles as a "no coding agent signed in · tap to
  // open Agents" CTA when the preflight check finds no authed runner
  // on the host. Lights up orange + adds a tap recognizer in that
  // state; otherwise stays the muted descriptive text it always was.
  private weak var subtitleLabel: UILabel?
  // Pill button rendered next to the subtitle ONLY when the preflight
  // detects a missing/broken runner setup. Hidden in the default
  // (everything-fine) state so the card chrome stays minimal.
  private weak var subtitleActionButton: UIButton?
  // ⓘ "..." in the title row that opens the shake-overlay menu so
  // the user can route to Agents / Settings / Back-to-Yaver from
  // inside the Feedback pane (instead of dismissing + shaking again).
  private weak var menuButton: UIButton?
  private weak var bottomConstraint: NSLayoutConstraint?
  // Card's bottom-anchor constraint. handleKeyboardChange adjusts its
  // constant: 0 when keyboard down, -keyboardHeight when up. The card's
  // INTRINSIC layout naturally fills the available vertical space
  // (title + flexible-height prompt + toggle + action row) so the
  // pretty branded Send + Reload buttons always sit just above the
  // keyboard, with the prompt area absorbing whatever vertical room
  // is left. No height constraints to fight with.
  private weak var cardBottomConstraint: NSLayoutConstraint?
  private weak var promptHeightConstraint: NSLayoutConstraint?
  private weak var cardHeightConstraint: NSLayoutConstraint?
  // Prompt min so it never collapses to a thin line. Card min so the
  // resting (no-keyboard) layout is generous.
  private let promptHeightMin: CGFloat = 96
  private let cardHeightMin: CGFloat = 360
  private var snapshot: UIImage?
  private var inFlight = false

  // MARK: - UI


  private func buildCard() -> UIView {
    let bg = UIVisualEffectView(effect: UIBlurEffect(style: .systemUltraThinMaterialDark))
    bg.translatesAutoresizingMaskIntoConstraints = false
    bg.layer.cornerRadius = 22
    bg.layer.maskedCorners = [.layerMinXMinYCorner, .layerMaxXMinYCorner]
    bg.clipsToBounds = true
    // Tint the blur with a deep purple-black so the card feels rooted
    // in Yaver's accent palette rather than the system's neutral dark
    // material. ~RGB(14,12,28) at 60% opacity layered onto the blur.
    bg.contentView.backgroundColor = UIColor(red: 0.055, green: 0.047, blue: 0.110, alpha: 0.62)

    // Drag handle at top
    let handle = UIView()
    handle.translatesAutoresizingMaskIntoConstraints = false
    handle.backgroundColor = UIColor(white: 1, alpha: 0.2)
    handle.layer.cornerRadius = 2.5

    // Header
    let title = UILabel()
    title.translatesAutoresizingMaskIntoConstraints = false
    title.text = "Feedback"
    title.textColor = .white
    title.font = .systemFont(ofSize: 17, weight: .semibold)

    let subtitle = UILabel()
    subtitle.translatesAutoresizingMaskIntoConstraints = false
    subtitle.text = "send a message · reload · screenshot"
    subtitle.textColor = UIColor(white: 1, alpha: 0.55)
    subtitle.font = .systemFont(ofSize: 12, weight: .regular)
    subtitle.isUserInteractionEnabled = true
    subtitleLabel = subtitle
    let subtitleTap = UITapGestureRecognizer(target: self, action: #selector(handleSubtitleTap))
    subtitle.addGestureRecognizer(subtitleTap)

    // Menu button — sits to the LEFT of the close X. Opens the
    // shake-overlay (Feedback / Agents / Settings / Back-to-Yaver)
    // so the user can switch surfaces without having to dismiss
    // this pane and shake / tap-Y again. Tapping dismisses self
    // first to avoid two bottom-sheets stacked.
    let menuBtn = UIButton(type: .system)
    menuBtn.translatesAutoresizingMaskIntoConstraints = false
    menuBtn.setImage(UIImage(systemName: "ellipsis.circle", withConfiguration:
                              UIImage.SymbolConfiguration(pointSize: 18, weight: .semibold)), for: .normal)
    menuBtn.tintColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1)
    menuBtn.addTarget(self, action: #selector(handleMenuTap), for: .touchUpInside)
    self.menuButton = menuBtn

    let close = UIButton(type: .system)
    close.translatesAutoresizingMaskIntoConstraints = false
    close.setImage(UIImage(systemName: "xmark", withConfiguration:
                            UIImage.SymbolConfiguration(pointSize: 16, weight: .semibold)), for: .normal)
    close.tintColor = UIColor(white: 1, alpha: 0.6)
    close.addTarget(self, action: #selector(dismissTapped), for: .touchUpInside)

    // Prompt input
    let promptCard = UIView()
    promptCard.translatesAutoresizingMaskIntoConstraints = false
    promptCard.backgroundColor = UIColor(white: 1, alpha: 0.08)
    promptCard.layer.cornerRadius = 14

    let prompt = UITextView()
    prompt.translatesAutoresizingMaskIntoConstraints = false
    prompt.backgroundColor = .clear
    prompt.textColor = .white
    prompt.font = .systemFont(ofSize: 16)
    prompt.tintColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1)
    prompt.delegate = self
    prompt.autocorrectionType = .yes
    prompt.autocapitalizationType = .sentences
    // No inputAccessoryView toolbar — instead, the card itself shrinks
    // when the keyboard appears (handleKeyboardChange animates the
    // promptHeightConstraint + cardHeightConstraint constants), so the
    // existing on-screen Reload + Send buttons sit right above the
    // keyboard. Same pattern as the Yaver Tasks "New Task" composer.
    promptField = prompt

    let placeholder = UILabel()
    placeholder.translatesAutoresizingMaskIntoConstraints = false
    placeholder.text = "What's broken? Or just describe what to vibe on…"
    placeholder.textColor = UIColor(white: 1, alpha: 0.35)
    placeholder.font = .systemFont(ofSize: 16)
    placeholder.numberOfLines = 0
    promptPlaceholder = placeholder

    promptCard.addSubview(prompt)
    promptCard.addSubview(placeholder)

    // Screenshot toggle row
    let toggleRow = UIView()
    toggleRow.translatesAutoresizingMaskIntoConstraints = false
    let cameraIcon = UIImageView(image: UIImage(systemName: "camera.fill"))
    cameraIcon.translatesAutoresizingMaskIntoConstraints = false
    cameraIcon.tintColor = UIColor(white: 1, alpha: 0.65)
    cameraIcon.contentMode = .scaleAspectFit
    let toggleLabel = UILabel()
    toggleLabel.translatesAutoresizingMaskIntoConstraints = false
    toggleLabel.text = "Include screenshot"
    toggleLabel.textColor = .white
    toggleLabel.font = .systemFont(ofSize: 14, weight: .regular)
    let toggle = UISwitch()
    toggle.translatesAutoresizingMaskIntoConstraints = false
    toggle.isOn = true
    toggle.onTintColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1)
    screenshotToggle = toggle
    toggleRow.addSubview(cameraIcon)
    toggleRow.addSubview(toggleLabel)
    toggleRow.addSubview(toggle)

    // Action buttons
    let reload = makeButton(title: "Reload", icon: "arrow.clockwise",
                            primary: false, action: #selector(reloadTapped))
    reloadButton = reload
    let send = makeButton(title: "Send", icon: "arrow.up",
                          primary: true, action: #selector(sendTapped))
    sendButton = send

    let actionRow = UIStackView(arrangedSubviews: [reload, send])
    actionRow.translatesAutoresizingMaskIntoConstraints = false
    actionRow.axis = .horizontal
    actionRow.distribution = .fillEqually
    actionRow.spacing = 10

    // Status label (inline progress / errors)
    let status = UILabel()
    status.translatesAutoresizingMaskIntoConstraints = false
    status.font = .systemFont(ofSize: 12, weight: .medium)
    status.textColor = UIColor(white: 1, alpha: 0.7)
    status.numberOfLines = 0
    status.textAlignment = .center
    status.text = " "
    statusLabel = status

    // Layout
    let content = UIStackView(arrangedSubviews: [promptCard, toggleRow, actionRow, status])
    content.translatesAutoresizingMaskIntoConstraints = false
    content.axis = .vertical
    content.spacing = 12
    content.setCustomSpacing(16, after: promptCard)
    content.setCustomSpacing(14, after: toggleRow)

    bg.contentView.addSubview(handle)
    bg.contentView.addSubview(title)
    bg.contentView.addSubview(subtitle)
    bg.contentView.addSubview(menuBtn)
    bg.contentView.addSubview(close)
    bg.contentView.addSubview(content)

    // Tap anywhere on the card chrome (the blur background, but NOT the
    // input field itself) to dismiss the keyboard. cancelsTouchesInView=
    // false so this doesn't swallow taps on buttons / toggles inside
    // the card.
    let bgTap = UITapGestureRecognizer(target: self, action: #selector(handleBackgroundTap))
    bgTap.cancelsTouchesInView = false
    bg.addGestureRecognizer(bgTap)

    let bottomCon = bg.heightAnchor.constraint(greaterThanOrEqualToConstant: cardHeightMin)
    bottomConstraint = bottomCon
    cardHeightConstraint = bottomCon

    let promptH = promptCard.heightAnchor.constraint(greaterThanOrEqualToConstant: promptHeightMin)
    promptHeightConstraint = promptH

    NSLayoutConstraint.activate([
      bottomCon,
      promptH,
      handle.centerXAnchor.constraint(equalTo: bg.contentView.centerXAnchor),
      handle.topAnchor.constraint(equalTo: bg.contentView.topAnchor, constant: 8),
      handle.widthAnchor.constraint(equalToConstant: 38),
      handle.heightAnchor.constraint(equalToConstant: 5),

      // Menu sits at the leading edge so it can't be misclicked into
      // close (X). Title slides 4pt right of the menu button.
      menuBtn.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 14),
      menuBtn.topAnchor.constraint(equalTo: handle.bottomAnchor, constant: 8),
      menuBtn.widthAnchor.constraint(equalToConstant: 32),
      menuBtn.heightAnchor.constraint(equalToConstant: 32),

      title.leadingAnchor.constraint(equalTo: menuBtn.trailingAnchor, constant: 4),
      title.topAnchor.constraint(equalTo: handle.bottomAnchor, constant: 14),
      subtitle.leadingAnchor.constraint(equalTo: title.leadingAnchor),
      subtitle.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 2),

      close.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -16),
      close.centerYAnchor.constraint(equalTo: title.centerYAnchor),
      close.widthAnchor.constraint(equalToConstant: 32),
      close.heightAnchor.constraint(equalToConstant: 32),

      content.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      content.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -18),
      content.topAnchor.constraint(equalTo: subtitle.bottomAnchor, constant: 16),
      content.bottomAnchor.constraint(lessThanOrEqualTo: bg.contentView.bottomAnchor, constant: -28),

      // (Prompt min-height constraint is created above as `promptH` and
      //  activated via the array — captured into promptHeightConstraint
      //  so handleKeyboardChange can shrink it when the keyboard
      //  appears, keeping Reload + Send visible.)
      prompt.leadingAnchor.constraint(equalTo: promptCard.leadingAnchor, constant: 12),
      prompt.trailingAnchor.constraint(equalTo: promptCard.trailingAnchor, constant: -12),
      prompt.topAnchor.constraint(equalTo: promptCard.topAnchor, constant: 10),
      prompt.bottomAnchor.constraint(equalTo: promptCard.bottomAnchor, constant: -10),
      placeholder.leadingAnchor.constraint(equalTo: prompt.leadingAnchor, constant: 5),
      placeholder.trailingAnchor.constraint(equalTo: prompt.trailingAnchor, constant: -5),
      placeholder.topAnchor.constraint(equalTo: prompt.topAnchor, constant: 8),

      // Toggle row
      toggleRow.heightAnchor.constraint(equalToConstant: 32),
      cameraIcon.leadingAnchor.constraint(equalTo: toggleRow.leadingAnchor, constant: 4),
      cameraIcon.centerYAnchor.constraint(equalTo: toggleRow.centerYAnchor),
      cameraIcon.widthAnchor.constraint(equalToConstant: 18),
      cameraIcon.heightAnchor.constraint(equalToConstant: 18),
      toggleLabel.leadingAnchor.constraint(equalTo: cameraIcon.trailingAnchor, constant: 10),
      toggleLabel.centerYAnchor.constraint(equalTo: toggleRow.centerYAnchor),
      toggle.trailingAnchor.constraint(equalTo: toggleRow.trailingAnchor),
      toggle.centerYAnchor.constraint(equalTo: toggleRow.centerYAnchor),

      // Action row
      actionRow.heightAnchor.constraint(equalToConstant: 48),
    ])

    return bg
  }


  // runRunnerAuthPreflight hits /runner-auth/status to decide whether
  // ANY coding runner on the host is actually signed in. If not, we
  // repurpose the subtitle as a tappable CTA that opens YaverAgentsPane
  // — gives the user a one-tap route to fix their setup instead of
  // hitting Send and watching it fail with "invalid relay password" /
  // "no runner ready" / similar.
  private func runRunnerAuthPreflight() {
    let agentBase = UserDefaults.standard.string(forKey: "yaverAgentBaseURL") ?? ""
    guard !agentBase.isEmpty,
          let url = URL(string: "\(agentBase)/runner-auth/status") else { return }
    var req = URLRequest(url: url)
    req.setValue("Bearer \(bestAuthToken())", forHTTPHeaderField: "Authorization")
    URLSession.shared.dataTask(with: req) { [weak self] data, resp, _ in
      DispatchQueue.main.async {
        guard let self = self, let label = self.subtitleLabel else { return }
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if code < 200 || code >= 300 {
          // Preflight failed (likely stale relay password / mobile
          // session out of sync — `yaver primary status` from the
          // host probably still shows the runner authed). Stay silent
          // here to avoid false-positive "no agent" warnings; the
          // actual Send error (humanized via humanizeRunnerAuthFailure)
          // is the right place for that signal.
          return
        }
        guard
          let data = data,
          let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let runners = json["runners"] as? [[String: Any]]
        else { return }
        let anyAuthed = runners.contains { ($0["authConfigured"] as? Bool) == true }
        if !anyAuthed {
          self.markSubtitleNoAgent(label, msg: "⚠ no coding agent signed in")
        }
      }
    }.resume()
  }

  private func markSubtitleNoAgent(_ label: UILabel, msg: String) {
    label.text = msg
    label.textColor = UIColor(red: 1.0, green: 0.78, blue: 0.4, alpha: 1.0)
    label.tag = 9001 // sentinel for handleSubtitleTap to know "this is the CTA state"
  }

  @objc private func handleMenuTap() {
    // Dismiss self + ask AppDelegate to show the shake-overlay so
    // the user can switch surfaces (Agents / Settings / Back-to-Yaver)
    // without dropping out and shaking again.
    guard let win = self.window else { return }
    let pane = self.cardView
    UIView.animate(withDuration: 0.18, animations: {
      pane?.transform = CGAffineTransform(translationX: 0, y: 600)
      pane?.alpha = 0
    }, completion: { _ in
      pane?.removeFromSuperview()
      self.cardView = nil
      // Reach back to AppDelegate via UIApplication.shared.delegate.
      if let app = UIApplication.shared.delegate as? AppDelegate {
        // handleShakeGesture re-uses the existing showShakeOverlay
        // path, which is now stateful (no double-stack risk).
        _ = win // keep referenced
        app.handleShakeGesture()
      }
    })
  }

  @objc private func handleSubtitleTap() {
    guard subtitleLabel?.tag == 9001, let win = self.window else { return }
    // Dismiss the feedback pane first so the agents pane doesn't stack
    // a second bottom-sheet on top.
    let pane = self.cardView
    UIView.animate(withDuration: 0.18, animations: {
      pane?.transform = CGAffineTransform(translationX: 0, y: 600)
      pane?.alpha = 0
    }, completion: { _ in
      pane?.removeFromSuperview()
      self.cardView = nil
      YaverAgentsPane.shared.present(in: win)
    })
  }

  @objc private func handleBackgroundTap() {
    // Tap outside the prompt's text input area to dismiss the keyboard.
    // The toolbar's Done button is the explicit dismiss; this is the
    // ergonomic path for users who instinctively tap "elsewhere" to
    // close a keyboard.
    promptField?.resignFirstResponder()
  }

  private func makeButton(title: String, icon: String, primary: Bool, action: Selector) -> UIButton {
    let btn = UIButton(type: .system)
    let iconCfg = UIImage.SymbolConfiguration(pointSize: 14, weight: .semibold)
    btn.setImage(UIImage(systemName: icon, withConfiguration: iconCfg), for: .normal)
    btn.setTitle("  \(title)", for: .normal)
    btn.titleLabel?.font = .systemFont(ofSize: 15, weight: .semibold)
    btn.layer.cornerRadius = 12
    if primary {
      btn.backgroundColor = UIColor(red: 0.46, green: 0.51, blue: 0.96, alpha: 1)
      btn.tintColor = .white
      btn.setTitleColor(.white, for: .normal)
    } else {
      btn.backgroundColor = UIColor(white: 1, alpha: 0.08)
      btn.tintColor = UIColor(white: 1, alpha: 0.85)
      btn.setTitleColor(UIColor(white: 1, alpha: 0.85), for: .normal)
    }
    btn.addTarget(self, action: action, for: .touchUpInside)
    return btn
  }

  // MARK: - Actions

  @objc private func dismissTapped() { dismiss() }


  private func dismiss() {
    NotificationCenter.default.removeObserver(self,
      name: UIResponder.keyboardWillChangeFrameNotification, object: nil)
    promptField?.resignFirstResponder()
    guard let card = cardView else { return }
    UIView.animate(withDuration: 0.22, animations: {
      card.transform = CGAffineTransform(translationX: 0, y: 600)
      card.alpha = 0
    }, completion: { _ in
      card.removeFromSuperview()
      self.cardView = nil
      self.snapshot = nil
      // Bring the floating Y back so the user can re-open feedback
      // (or whatever surface they want via long-press).
      YaverFloatingTrigger.shared.showAgain()
    })
  }

  @objc private func reloadTapped() {
    if inFlight { return }
    inFlight = true
    setStatus("Reloading…", tone: .progress)
    let agentBase = UserDefaults.standard.string(forKey: "yaverAgentBaseURL") ?? ""
    let auth = bestAuthToken()
    guard let url = URL(string: "\(agentBase)/dev/reload") else {
      setStatus("Missing agent URL", tone: .error); inFlight = false; return
    }
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    req.setValue("Bearer \(auth)", forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = "{}".data(using: .utf8)
    URLSession.shared.dataTask(with: req) { _, resp, err in
      DispatchQueue.main.async {
        self.inFlight = false
        if let err = err {
          self.setStatus("Reload failed: \(err.localizedDescription)", tone: .error)
        } else if let http = resp as? HTTPURLResponse, http.statusCode >= 200, http.statusCode < 300 {
          self.setStatus("Reload requested ✓", tone: .success)
          UIImpactFeedbackGenerator(style: .light).impactOccurred()
        } else {
          let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
          self.setStatus("Reload failed (HTTP \(code))", tone: .error)
        }
      }
    }.resume()
  }

  @objc private func sendTapped() {
    if inFlight { return }
    let prompt = (promptField?.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    if prompt.isEmpty {
      setStatus("Type something to send", tone: .error); return
    }
    inFlight = true
    setStatus("Sending…", tone: .progress)
    let agentBase = UserDefaults.standard.string(forKey: "yaverAgentBaseURL") ?? ""
    let auth = bestAuthToken()
    guard let url = URL(string: "\(agentBase)/tasks") else {
      setStatus("Missing agent URL", tone: .error); inFlight = false; return
    }

    var images: [[String: String]] = []
    if screenshotToggle?.isOn == true, let img = snapshot,
       let jpeg = img.jpegData(compressionQuality: 0.7) {
      images.append([
        "base64": jpeg.base64EncodedString(),
        "mimeType": "image/jpeg",
        "filename": "yaver-feedback-\(Int(Date().timeIntervalSince1970)).jpg",
      ])
    }

    // Don't hardcode runner: "claude". User may have picked codex /
    // opencode as the device's primary runner in DeviceDetailsModal,
    // and forcing claude here makes the agent ignore that pick. Read
    // the cached preferred runner from UserDefaults (populated by the
    // mobile JS side when the user picks one) and only include the
    // field when we have a value — otherwise let the agent fall back
    // to its own configured default for the device.
    var payload: [String: Any] = [
      "title": String(prompt.prefix(80)),
      "description": prompt,
      "userPrompt": prompt,
      "source": "mobile-feedback",
      "images": images,
    ]
    let preferredRunner = UserDefaults.standard.string(forKey: "yaverPreferredRunner") ?? ""
    if !preferredRunner.isEmpty {
      payload["runner"] = preferredRunner
    }
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    req.setValue("Bearer \(auth)", forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = try? JSONSerialization.data(withJSONObject: payload)
    URLSession.shared.dataTask(with: req) { data, resp, err in
      DispatchQueue.main.async {
        self.inFlight = false
        if let err = err {
          self.setStatus(humanizeRunnerAuthFailure(code: 0, body: nil, networkErr: err),
                         tone: .error)
        } else if let http = resp as? HTTPURLResponse, http.statusCode >= 200, http.statusCode < 300 {
          self.setStatus("Sent ✓", tone: .success)
          UINotificationFeedbackGenerator().notificationOccurred(.success)
          DispatchQueue.main.asyncAfter(deadline: .now() + 0.9) { self.dismiss() }
        } else {
          // Reuse the same humanizer the Coding Agents pane uses, so
          // "invalid relay password" / 401 / 503 / etc. all surface as
          // a single readable line instead of raw JSON.
          let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
          let body = data.flatMap { String(data: $0, encoding: .utf8) }
          self.setStatus(humanizeRunnerAuthFailure(code: code, body: body, networkErr: nil),
                         tone: .error)
        }
      }
    }.resume()
  }

  // MARK: - Helpers

  private func bestAuthToken() -> String {
    let inherited = UserDefaults.standard.string(forKey: "yaverInheritedAuthToken") ?? ""
    if !inherited.isEmpty { return inherited }
    return UserDefaults.standard.string(forKey: "yaverAgentAuth") ?? ""
  }

  enum StatusTone { case progress, success, error }


  private func setStatus(_ msg: String, tone: StatusTone) {
    statusLabel?.text = msg
    switch tone {
    case .progress:
      statusLabel?.textColor = UIColor(white: 1, alpha: 0.7)
    case .success:
      statusLabel?.textColor = UIColor(red: 0.13, green: 0.77, blue: 0.37, alpha: 1)
    case .error:
      statusLabel?.textColor = UIColor(red: 1, green: 0.45, blue: 0.45, alpha: 1)
    }
  }


  private func captureSnapshot(of window: UIWindow) -> UIImage? {
    let renderer = UIGraphicsImageRenderer(size: window.bounds.size)
    return renderer.image { _ in
      window.drawHierarchy(in: window.bounds, afterScreenUpdates: false)
    }
  }

  // Tasks-composer pattern: slide the card's BOTTOM up to sit on top
  // of the keyboard. Card content (title / prompt / toggle / action
  // row) flows naturally inside the new card height — Send + Reload
  // stay pinned at the bottom of the card, which is now the top of
  // the keyboard. No height shrink, no translation that could push
  // the title above the safe area.
  @objc private func handleKeyboardChange(_ note: Notification) {
    guard let card = cardView,
          let window = self.window,
          let info = note.userInfo,
          let endFrame = info[UIResponder.keyboardFrameEndUserInfoKey] as? CGRect
    else { return }
    let intersection = window.bounds.intersection(window.convert(endFrame, from: nil))
    let keyboardOverlap = max(0, intersection.height)

    cardBottomConstraint?.constant = -keyboardOverlap

    let duration = (info[UIResponder.keyboardAnimationDurationUserInfoKey] as? Double) ?? 0.25
    UIView.animate(withDuration: duration) {
      card.superview?.layoutIfNeeded()
    }
  }
}

extension YaverFeedbackPane: UITextViewDelegate {
  func textViewDidChange(_ textView: UITextView) {
    promptPlaceholder?.isHidden = !textView.text.isEmpty
  }
}
