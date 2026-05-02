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
    snapshot = captureSnapshot(of: window)
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
    NotificationCenter.default.addObserver(self, selector: #selector(handleKeyboardChange(_:)),
                                           name: UIResponder.keyboardWillChangeFrameNotification, object: nil)
    UIImpactFeedbackGenerator(style: .light).impactOccurred()
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
  private weak var bottomConstraint: NSLayoutConstraint?
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
    bg.contentView.addSubview(close)
    bg.contentView.addSubview(content)

    let bottomCon = bg.heightAnchor.constraint(greaterThanOrEqualToConstant: 280)
    bottomConstraint = bottomCon

    NSLayoutConstraint.activate([
      bottomCon,
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

      content.leadingAnchor.constraint(equalTo: bg.contentView.leadingAnchor, constant: 18),
      content.trailingAnchor.constraint(equalTo: bg.contentView.trailingAnchor, constant: -18),
      content.topAnchor.constraint(equalTo: subtitle.bottomAnchor, constant: 16),
      content.bottomAnchor.constraint(lessThanOrEqualTo: bg.contentView.bottomAnchor, constant: -28),

      // Prompt card sizing
      promptCard.heightAnchor.constraint(greaterThanOrEqualToConstant: 96),
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

    let payload: [String: Any] = [
      "title": String(prompt.prefix(80)),
      "description": prompt,
      "userPrompt": prompt,
      "runner": "claude",
      "source": "mobile-feedback",
      "images": images,
    ]
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    req.setValue("Bearer \(auth)", forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = try? JSONSerialization.data(withJSONObject: payload)
    URLSession.shared.dataTask(with: req) { data, resp, err in
      DispatchQueue.main.async {
        self.inFlight = false
        if let err = err {
          self.setStatus("Send failed: \(err.localizedDescription)", tone: .error)
        } else if let http = resp as? HTTPURLResponse, http.statusCode >= 200, http.statusCode < 300 {
          self.setStatus("Sent ✓", tone: .success)
          UINotificationFeedbackGenerator().notificationOccurred(.success)
          DispatchQueue.main.asyncAfter(deadline: .now() + 0.9) { self.dismiss() }
        } else {
          let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
          // Surface the agent's error body so failures aren't opaque
          // (auth failure, missing runner, no workDir, etc. all return
          // structured JSON {error: "..."} the user needs to see).
          var detail = "HTTP \(code)"
          if let data = data, let body = String(data: data, encoding: .utf8) {
            let trimmed = body.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty {
              detail = "HTTP \(code): \(trimmed.prefix(220))"
            }
          }
          self.setStatus("Send failed — \(detail)", tone: .error)
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

  @objc private func handleKeyboardChange(_ note: Notification) {
    guard let window = self.window,
          let card = cardView,
          let info = note.userInfo,
          let endFrame = info[UIResponder.keyboardFrameEndUserInfoKey] as? CGRect
    else { return }
    let intersection = window.bounds.intersection(window.convert(endFrame, from: nil))
    let inset = max(0, intersection.height)
    UIView.animate(withDuration: 0.25) {
      card.transform = CGAffineTransform(translationX: 0, y: -inset)
    }
  }
}

extension YaverFeedbackPane: UITextViewDelegate {
  func textViewDidChange(_ textView: UITextView) {
    promptPlaceholder?.isHidden = !textView.text.isEmpty
  }
}
