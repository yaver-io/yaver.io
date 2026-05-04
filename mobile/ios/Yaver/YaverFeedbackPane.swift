import Foundation
import React
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
final class YaverFeedbackPane: NSObject, UIGestureRecognizerDelegate {

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
    // Reset the in-flight latch + cancel any orphan request from a
    // prior session. YaverFeedbackPane is a static singleton — without
    // this, dismissing while a Send/Reload was mid-flight (slow remote /
    // relay timeout) leaves inFlight=true on the shared instance, and
    // the FIRST Send/Reload tap on the next open is silently no-opped
    // by the `if inFlight { return }` guard until the prior URLSession
    // callback eventually fires. Symptom: "first send doesn't trigger".
    pendingTask?.cancel()
    pendingTask = nil
    inFlight = false
    // Same for any leftover transcript stream from a prior session
    // (rare — dismiss() already tears it down — but cheap insurance).
    transcriptView?.teardown()
    transcriptView = nil
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

    // Sync the agent+model chip's label with the latest UserDefaults
    // values pushed by DeviceContext. Doing this AFTER the pane is
    // attached + buildCard returned guarantees agentChipButton is
    // already wired up.
    refreshAgentChipLabel()
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
  // Tappable runner+model chip — mirrors the Tasks tab's `OpenAI Codex
  // · gpt-5.4 ▾` pill so the user sees at a glance which coding agent
  // and model their feedback will route to. Reads from
  // `yaverPreferredRunner` / `yaverPreferredModel` UserDefaults pushed
  // by DeviceContext (Convex source of truth: userSettings.
  // primaryRunnerByDevice). Tap → opens YaverAgentsPane (same surface
  // the menu ellipsis already opens).
  private weak var agentChipButton: UIButton?
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
  // Tracks the most recent Send/Reload URLSession dataTask so dismiss()
  // can cancel it. Without this, a dismissed pane still has its callback
  // pending; when it fires it sets inFlight=false but also tries to
  // setStatus on a now-detached label, and worse — it leaves the
  // singleton's inFlight latch unpredictable across the dismiss boundary.
  private var pendingTask: URLSessionDataTask?
  // Holds the SSE subscription to /dev/events while a Reload is
  // streaming. Cancelled in dismiss + when a terminal event lands so a
  // backgrounded pane doesn't keep a long-lived connection open.
  private var reloadStream: YaverSSEReader?
  private var reloadStreamTimeout: DispatchWorkItem?
  // Live transcript subview. Created on Send-success, replaces the
  // composer form, owns its own SSE subscription to /tasks/{id}/output
  // + its own follow-up composer. Torn down in dismiss().
  private weak var transcriptView: YaverFeedbackTranscript?

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

    // Agent + model chip — `OpenAI Codex · gpt-5.4 ▾` style, mirroring
    // the Tasks tab's runner pill. Sits on its own row right above the
    // Send/Reload action row so the user sees what their feedback will
    // route to before tapping Send. Empty when no preferred runner is
    // pushed (host has never set one) — in that state we hide the row
    // to keep the drawer tidy.
    let agentChip = UIButton(type: .system)
    agentChip.translatesAutoresizingMaskIntoConstraints = false
    agentChip.contentEdgeInsets = UIEdgeInsets(top: 6, left: 12, bottom: 6, right: 12)
    agentChip.backgroundColor = UIColor(white: 1, alpha: 0.06)
    agentChip.layer.cornerRadius = 10
    agentChip.layer.borderWidth = 1
    agentChip.layer.borderColor = UIColor(white: 1, alpha: 0.10).cgColor
    agentChip.titleLabel?.font = .systemFont(ofSize: 12, weight: .medium)
    agentChip.setTitleColor(UIColor(white: 1, alpha: 0.78), for: .normal)
    agentChip.addTarget(self, action: #selector(agentChipTapped), for: .touchUpInside)
    agentChipButton = agentChip
    let agentChipRow = UIStackView(arrangedSubviews: [UIView(), agentChip])
    agentChipRow.translatesAutoresizingMaskIntoConstraints = false
    agentChipRow.axis = .horizontal
    agentChipRow.alignment = .center
    agentChipRow.distribution = .fill

    // Status label (inline progress / errors)
    let status = UILabel()
    status.translatesAutoresizingMaskIntoConstraints = false
    status.font = .systemFont(ofSize: 12, weight: .medium)
    status.textColor = UIColor(white: 1, alpha: 0.7)
    status.numberOfLines = 0
    status.textAlignment = .center
    status.text = " "
    statusLabel = status

    // Layout — agentChipRow sits between the screenshot toggle and the
    // Send/Reload buttons. Hidden when there's no preferred runner
    // pushed yet; refreshAgentChipLabel() decides on present().
    let content = UIStackView(arrangedSubviews: [promptCard, toggleRow, agentChipRow, actionRow, status])
    content.translatesAutoresizingMaskIntoConstraints = false
    content.axis = .vertical
    content.spacing = 12
    content.setCustomSpacing(16, after: promptCard)
    content.setCustomSpacing(10, after: toggleRow)
    content.setCustomSpacing(10, after: agentChipRow)

    bg.contentView.addSubview(handle)
    bg.contentView.addSubview(title)
    bg.contentView.addSubview(subtitle)
    bg.contentView.addSubview(menuBtn)
    bg.contentView.addSubview(close)
    bg.contentView.addSubview(content)

    // Tap anywhere on the card chrome (the blur background, but NOT the
    // input field itself) to dismiss the keyboard. cancelsTouchesInView=
    // false so this doesn't swallow taps on buttons / toggles inside
    // the card. The delegate also rejects touches that land on a
    // UIControl (Send / Reload / screenshotToggle) — without that,
    // tapping Send would fire the bgTap, dismiss the keyboard, reflow
    // the card, and pull Send out from under the user's finger before
    // touchUpInside fired. Net effect: Send needed two taps to submit.
    let bgTap = UITapGestureRecognizer(target: self, action: #selector(handleBackgroundTap))
    bgTap.cancelsTouchesInView = false
    bgTap.delegate = self
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
    guard let url = yaverResolveAgentURL("/runner-auth/status") else { return }
    var req = URLRequest(url: url)
    for (k, v) in yaverRelayHeaders() { req.setValue(v, forHTTPHeaderField: k) }
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
        // /runner-auth/status emits Go-default PascalCase keys
        // ("AuthConfigured", "Installed", "ID"). Reading the camelCase
        // mirror returns nil for every row, which made the preflight
        // mark devices as "no coding agent signed in" even when codex /
        // claude / ollama were authed. Read both spellings so this
        // doesn't regress if the agent ever switches to lowercase
        // marshaling later.
        let anyAuthed = runners.contains { row in
          if let v = row["AuthConfigured"] as? Bool, v { return true }
          if let v = row["authConfigured"] as? Bool, v { return true }
          return false
        }
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

  /// Build the `description` field POSTed to /tasks. Wraps the user's
  /// raw feedback with enough context for the remote AI to act
  /// without guessing — and constrains its OUTPUT shape so the
  /// streamed transcript reads like Claude Code / Codex CLI rather
  /// than a tarball dump.
  ///
  /// Inputs may all be empty: when neither projectName nor projectPath
  /// is set we still emit a useful preface ("user is providing
  /// feedback while testing an app inside Yaver mobile, screenshot
  /// included if attached, please act on this") instead of an empty
  /// string, because feedback without project context still benefits
  /// from the "screenshot is the current screen" hint.
  private func buildFeedbackPrompt(userPrompt: String,
                                   projectName: String,
                                   projectPath: String,
                                   hasScreenshot: Bool) -> String {
    var sb = "[Mobile feedback from inside Yaver]\n"
    sb += "The user is providing this feedback while running a mobile app inside the Yaver mobile container "
    sb += "and is currently looking at a specific screen of that app.\n\n"
    if !projectName.isEmpty || !projectPath.isEmpty {
      sb += "App being tested:\n"
      if !projectName.isEmpty { sb += "  name: \(projectName)\n" }
      if !projectPath.isEmpty { sb += "  path: \(projectPath)\n" }
      sb += "\n"
    }
    if hasScreenshot {
      sb += "A screenshot of the current screen is attached as the first image. "
      sb += "Open it before deciding what to change — the user is pointing at what they SEE, "
      sb += "not necessarily what is named most prominently in the source.\n\n"
    } else {
      sb += "(The user chose not to attach a screenshot for this round.)\n\n"
    }

    // Operation contract — what the agent SHOULD do and (importantly)
    // SHOULDN'T spew. Without this, codex on the remote box cloned
    // node_modules / dumped tarball logs into the SSE stream and
    // froze the mobile transcript renderer with multi-MB chunks.
    sb += "Operation contract:\n"
    sb += "1. Locate the file(s) responsible for what the user described and EDIT them in place. "
    sb += "Save the changes — that is the deliverable.\n"
    sb += "2. Stream a CONCISE Claude-Code / Codex-style narration as you work: "
    sb += "one short line per step (e.g. \"Reading app/index.tsx\", \"Editing safe.backgroundColor\", "
    sb += "\"Saved app/index.tsx\"). Show small diffs only — never dump entire files, "
    sb += "never paste node_modules contents, never echo build / install logs.\n"
    sb += "3. Do NOT run npm install / yarn / pnpm / git clone / cargo build / docker pull or any other "
    sb += "long-running install / fetch command. The repo is already prepared on this machine. "
    sb += "If a dependency is genuinely missing, say so in one line and stop — the user will install it.\n"
    sb += "4. Do NOT trigger a Hermes reload yourself. The user has a Reload button in the drawer "
    sb += "and decides when to refresh.\n"
    sb += "5. Keep total output under a few hundred lines. Heavy ripgrep / find / cat with no filter "
    sb += "are usually the wrong tool — use targeted reads.\n"
    if projectName.isEmpty && projectPath.isEmpty {
      sb += "6. If you can identify the project from the prompt or the screenshot, work there. "
      sb += "Otherwise ask the user briefly which project to target — one short line, no exhaustive list.\n"
    }
    sb += "\nUser feedback:\n\(userPrompt)"
    return sb
  }

  /// Refresh the agent+model chip's label from the same UserDefaults
  /// keys sendTapped reads, so what the user sees IS what the request
  /// will use. Hides the chip row when neither value is present.
  /// Called from present() (every time the pane appears) so a runner
  /// switch in the host's Tasks tab is reflected the next time the
  /// user opens feedback in the guest.
  private func refreshAgentChipLabel() {
    guard let chip = agentChipButton else { return }
    let runner = (UserDefaults.standard.string(forKey: "yaverPreferredRunner") ?? "")
      .trimmingCharacters(in: .whitespaces)
    let model = (UserDefaults.standard.string(forKey: "yaverPreferredModel") ?? "")
      .trimmingCharacters(in: .whitespaces)
    if runner.isEmpty && model.isEmpty {
      // No preference pushed yet — hide the row entirely so the user
      // doesn't see a confusing empty pill.
      chip.superview?.isHidden = true
      return
    }
    chip.superview?.isHidden = false
    let runnerLabel: String = {
      switch runner.lowercased() {
      case "claude": return "Claude"
      case "codex":  return "OpenAI Codex"
      case "opencode": return "opencode"
      default: return runner.isEmpty ? "Claude" : runner
      }
    }()
    let combined = model.isEmpty ? runnerLabel : "\(runnerLabel) · \(model)"
    chip.setTitle("\(combined)  ▾", for: .normal)
  }

  /// Tap handler for the agent chip — opens the same Coding Agents
  /// pane the ellipsis menu opens, which is where the user actually
  /// changes their primary runner / model. Reusing the existing pane
  /// means: one source of truth for runner pick, no parallel UI to
  /// keep in sync.
  @objc private func agentChipTapped() {
    UIImpactFeedbackGenerator(style: .light).impactOccurred()
    guard let win = window else { return }
    YaverAgentsPane.shared.present(in: win)
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
    // A reload SSE stream can outlive the pane itself if the user
    // dismisses mid-build. Tear it down so we don't keep an idle
    // long-lived connection on the relay.
    stopReloadEventStream()
    // Same for the live transcript stream + its spinner timer.
    transcriptView?.teardown()
    transcriptView = nil
    // Cancel any in-flight Send/Reload request so its callback won't
    // fire after the pane is gone — the cancellation also releases
    // the inFlight latch on this singleton (the next present() resets
    // it explicitly too, but we belt-and-suspenders here).
    pendingTask?.cancel()
    pendingTask = nil
    inFlight = false
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
    UIImpactFeedbackGenerator(style: .light).impactOccurred()

    // Open the SSE stream FIRST so we don't miss the early build
    // events the agent emits between /dev/reload-app POST and the
    // first compile pass.
    startReloadEventStream()

    guard let url = yaverResolveAgentURL("/dev/reload-app") else {
      setStatus("Missing agent URL", tone: .error); inFlight = false; stopReloadEventStream(); return
    }
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    for (k, v) in yaverRelayHeaders() { req.setValue(v, forHTTPHeaderField: k) }
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    // mode:"bundle" → full Hermes recompile + reload broadcast.
    // mode:"dev" is a Metro-only refresh and skips the rebuild we
    // need for whatever the AI just committed to land on the device.
    req.httpBody = #"{"mode":"bundle"}"#.data(using: .utf8)
    let task = URLSession.shared.dataTask(with: req) { [weak self] _, resp, err in
      DispatchQueue.main.async {
        guard let self = self else { return }
        self.pendingTask = nil
        self.inFlight = false
        if let err = err {
          // Cancellation isn't a user-visible error — it just means
          // dismiss() pulled the rug. Suppress so the next pane shows a
          // clean status pill.
          if (err as NSError).code == NSURLErrorCancelled { return }
          self.setStatus("Reload failed: \(err.localizedDescription)", tone: .error)
          self.stopReloadEventStream()
        } else if let http = resp as? HTTPURLResponse, http.statusCode >= 200, http.statusCode < 300 {
          // Don't override the streaming status here — events will
          // overwrite it as the build progresses. Just leave the
          // last SSE message visible.
          UIImpactFeedbackGenerator(style: .medium).impactOccurred()
        } else {
          let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
          self.setStatus("Reload failed (HTTP \(code))", tone: .error)
          self.stopReloadEventStream()
        }
      }
    }
    pendingTask = task
    task.resume()
  }

  // MARK: - Reload event stream

  /// Subscribe to /dev/events for ~60 seconds and surface each
  /// "data: {…}" frame inline as a status line. Events come from the
  /// agent's DevServerManager — same source the Hot Reload tab's
  /// streaming UI uses.
  private func startReloadEventStream() {
    stopReloadEventStream()
    guard let url = yaverResolveAgentURL("/dev/events") else { return }
    let reader = YaverSSEReader(
      onEvent: { [weak self] payload in
        self?.handleReloadEvent(payload)
      },
      onComplete: { [weak self] in
        // Not necessarily an error — the agent closes the SSE when
        // its event buffer drains. Don't recolor the status pill.
        self?.reloadStream = nil
      }
    )
    reader.start(url: url, headers: yaverRelayHeaders())
    reloadStream = reader

    // Hard timeout — Hermes rebuilds typically land in < 30s on the
    // dev box, but we cap at 60s so a stuck build doesn't peg the
    // connection forever. The stream itself will continue past the
    // pane being dismissed (stopped in dismiss()).
    let timeout = DispatchWorkItem { [weak self] in
      self?.stopReloadEventStream()
    }
    reloadStreamTimeout = timeout
    DispatchQueue.main.asyncAfter(deadline: .now() + 60, execute: timeout)
  }

  private func stopReloadEventStream() {
    reloadStream?.stop()
    reloadStream = nil
    reloadStreamTimeout?.cancel()
    reloadStreamTimeout = nil
  }

  private func handleReloadEvent(_ payload: String) {
    // /dev/events emits JSON objects of varying shapes — log lines,
    // operation status, build phase events. Pull whatever human-
    // readable string we can find and use it as the current status.
    guard let data = payload.data(using: .utf8),
          let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
      // Non-JSON payload (rare — agent always emits JSON, but be
      // tolerant). Surface as-is.
      let trimmed = payload.trimmingCharacters(in: .whitespacesAndNewlines)
      if !trimmed.isEmpty { setStatus(trimmed, tone: .progress) }
      return
    }
    // Common shapes the agent emits:
    //   {kind:"log",  line:"Bundling Hermes bytecode…"}
    //   {kind:"status", phase:"compile", message:"Compiling…"}
    //   {kind:"build", message:"…", success:true}
    //   {type:"reload_complete"}    ← terminal
    //   {type:"build_failed", error:"…"}
    let kind = (json["kind"] as? String) ?? (json["type"] as? String) ?? ""
    let message =
      (json["message"] as? String) ??
      (json["line"] as? String) ??
      (json["phase"] as? String) ??
      ""

    let lower = kind.lowercased()
    if lower.contains("complete") || lower.contains("reload_done") || lower == "reload_complete" {
      setStatus("Reloaded ✓", tone: .success)
      UINotificationFeedbackGenerator().notificationOccurred(.success)
      stopReloadEventStream()
      return
    }
    if lower.contains("fail") || lower.contains("error") {
      let detail = message.isEmpty ? (json["error"] as? String ?? "build failed") : message
      setStatus("Reload failed: \(detail)", tone: .error)
      stopReloadEventStream()
      return
    }
    if !message.isEmpty {
      setStatus(message, tone: .progress)
    }
  }

  @objc private func sendTapped() {
    NSLog("[YaverFeedback] sendTapped fired")
    if inFlight {
      NSLog("[YaverFeedback] sendTapped: inFlight=true, bailing")
      return
    }
    let userPrompt = (promptField?.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    NSLog("[YaverFeedback] userPrompt len=%d", userPrompt.count)
    if userPrompt.isEmpty {
      setStatus("Type something to send", tone: .error); return
    }
    inFlight = true
    setStatus("Sending…", tone: .progress)
    NSLog("[YaverFeedback] resolving agent URL…")
    guard let url = yaverResolveAgentURL("/tasks") else {
      NSLog("[YaverFeedback] yaverResolveAgentURL returned nil — aborting")
      setStatus("Missing agent URL", tone: .error); inFlight = false; return
    }
    NSLog("[YaverFeedback] resolved /tasks → %{public}@", url.absoluteString)

    // Capture every input on the main thread, then move ALL heavy work
    // (JPEG encode → base64 → JSON serialize) to a background queue.
    // Doing this on main blocked the runloop long enough on iPhone for
    // iOS's watchdog (typical budget ~5 s, less in guest contexts) to
    // kill the host with no surfaced crash log: a full-screen retina
    // screenshot is ~600 KB JPEG → ~800 KB base64 string, and
    // JSONSerialization over that string + the rest of the payload
    // pushed the host past the watchdog while showing "Sending…".
    let includeScreenshot = (screenshotToggle?.isOn == true)
    let snapshotForUpload = self.snapshot
    let projectName = UserDefaults.standard.string(forKey: "yaverInheritedGuestProjectName") ?? ""
    var projectPath = UserDefaults.standard.string(forKey: "yaverInheritedGuestProjectPath") ?? ""
    if projectPath.isEmpty {
      projectPath = UserDefaults.standard.string(forKey: "yaverPendingDevServerWorkDir") ?? ""
    }
    let preferredRunner = UserDefaults.standard.string(forKey: "yaverPreferredRunner") ?? ""
    let preferredModel = UserDefaults.standard.string(forKey: "yaverPreferredModel") ?? ""
    let relayHeaders = yaverRelayHeaders()
    let resolvedURL = url

    NSLog("[YaverFeedback] dispatching to bg queue (includeScreenshot=%d, snapshot=%@)",
          includeScreenshot ? 1 : 0,
          snapshotForUpload == nil ? "nil" : "present")
    DispatchQueue.global(qos: .userInitiated).async { [weak self] in
      NSLog("[YaverFeedback] bg: building images")
      var images: [[String: String]] = []
      if includeScreenshot, let img = snapshotForUpload,
         let jpeg = img.jpegData(compressionQuality: 0.7) {
        NSLog("[YaverFeedback] bg: jpeg encoded %d bytes", jpeg.count)
        images.append([
          "base64": jpeg.base64EncodedString(),
          "mimeType": "image/jpeg",
          "filename": "yaver-feedback-\(Int(Date().timeIntervalSince1970)).jpg",
        ])
        NSLog("[YaverFeedback] bg: image base64 ready")
      } else {
        NSLog("[YaverFeedback] bg: no image attached")
      }
      let hasScreenshot = !images.isEmpty
      let description = (self?.buildFeedbackPrompt(userPrompt: userPrompt,
                                                   projectName: projectName,
                                                   projectPath: projectPath,
                                                   hasScreenshot: hasScreenshot)) ?? userPrompt

      var payload: [String: Any] = [
        "title": String(userPrompt.prefix(80)),
        "description": description,
        "userPrompt": userPrompt,
        "source": "mobile-feedback",
        "images": images,
      ]
      if !projectPath.isEmpty { payload["workDir"] = projectPath }
      if !projectName.isEmpty { payload["projectName"] = projectName }
      if !preferredRunner.isEmpty { payload["runner"] = preferredRunner }
      if !preferredModel.isEmpty { payload["model"] = preferredModel }

      var req = URLRequest(url: resolvedURL)
      req.httpMethod = "POST"
      for (k, v) in relayHeaders { req.setValue(v, forHTTPHeaderField: k) }
      req.setValue("application/json", forHTTPHeaderField: "Content-Type")
      req.httpBody = try? JSONSerialization.data(withJSONObject: payload)

      let task = URLSession.shared.dataTask(with: req) { [weak self] data, resp, err in
      DispatchQueue.main.async {
        guard let self = self else { return }
        self.pendingTask = nil
        self.inFlight = false
        if let err = err {
          // Cancellation isn't a real error — dismiss() yanked the task.
          if (err as NSError).code == NSURLErrorCancelled { return }
          self.setStatus(humanizeRunnerAuthFailure(code: 0, body: nil, networkErr: err),
                         tone: .error)
        } else if let http = resp as? HTTPURLResponse, http.statusCode >= 200, http.statusCode < 300 {
          UINotificationFeedbackGenerator().notificationOccurred(.success)
          // Pull the freshly-created taskId out of the response so we
          // can subscribe to its /tasks/{id}/output SSE stream and
          // render the live vibing run in-pane (same UX as the Tasks
          // tab). Falling back to the legacy auto-dismiss path only if
          // the response shape is unexpected — keeps the user from
          // staring at a frozen "Sent ✓" if the agent ever changes
          // the create-task contract.
          let taskId = data
            .flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }
            .flatMap { $0["taskId"] as? String }
          if let taskId = taskId, !taskId.isEmpty {
            // 1.18.66 → 1.18.67: instead of mutating the drawer's view
            // tree (which raced the keyboard animation and crashed),
            // present the transcript as a SEPARATE overlay view added
            // directly to the window at a higher Z-index. The drawer
            // stays exactly where it is; the overlay slides up over it
            // and owns its own dismiss. View-tree race: gone.
            UINotificationFeedbackGenerator().notificationOccurred(.success)
            self.presentTranscriptOverlay(taskId: taskId, userPrompt: userPrompt)
          } else {
            self.setStatus("Sent ✓", tone: .success)
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.9) { self.dismiss() }
          }
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
    }
    // Hop back to main only for the property write + task.resume.
    // task.resume() itself doesn't strictly need to be on main, but
    // pendingTask is read from dismiss() (main) so writing it here
    // serialises against that. Resuming on main is fine — URLSession
    // schedules its own background work for the actual transfer.
      DispatchQueue.main.async { [weak self] in
        self?.pendingTask = task
        task.resume()
      }
    }
  }

  // Live overlay holding the YaverFeedbackTranscript on a fresh
  // top-level UIView. NOT a child of the drawer's card — that's the
  // layout race we're avoiding.
  private weak var transcriptOverlay: UIView?

  /// Slide a transcript overlay UP over the entire window. The
  /// existing drawer is left untouched; this is purely additive on
  /// top, so AutoLayout can't race with the drawer's keyboard
  /// animation. Tapping Close on the overlay dismisses both the
  /// overlay AND the drawer.
  ///
  /// Why this is safe (vs. the old enterTranscriptMode):
  ///  - No removeFromSuperview calls on drawer subviews
  ///  - No NSLayoutConstraint.activate inside an in-flight
  ///    UIView.animate from handleKeyboardChange
  ///  - The new view's constraints are added to the WINDOW, not
  ///    the drawer's card — independent layout pass
  ///  - The drawer's keyboard handler can finish its animation
  ///    without seeing any structural change underneath it
  private func presentTranscriptOverlay(taskId: String, userPrompt: String) {
    NSLog("[YaverFeedback] presentTranscriptOverlay taskId=%{public}@", taskId)
    guard let win = self.window, win.window != nil || true else {
      // Fallback if window is gone for any reason: just show the
      // legacy success message and dismiss the drawer.
      self.setStatus("Sent ✓ — open Tasks tab to follow", tone: .success)
      DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) { self.dismiss() }
      return
    }

    // Hide keyboard first — happens BEFORE we add the overlay so the
    // drawer's keyboard handler runs and settles. We add the overlay
    // a tick later so its layout doesn't race the keyboard animation.
    promptField?.resignFirstResponder()

    // Resolve the agent base URL once; transcript needs it for the
    // SSE subscribe call.
    guard let tasksURL = yaverResolveAgentURL("/tasks") else {
      self.setStatus("Missing agent URL", tone: .error); return
    }
    let baseURL = tasksURL.deletingLastPathComponent()
    let relayHeaders = yaverRelayHeaders()

    DispatchQueue.main.asyncAfter(deadline: .now() + 0.30) { [weak self] in
      guard let self = self, let win = self.window else { return }

      // Container: full-screen UIView added to the window, with a
      // dim backdrop and a card hosting the transcript. Card slides
      // up from the bottom on appear.
      let overlay = UIView()
      overlay.translatesAutoresizingMaskIntoConstraints = false
      overlay.backgroundColor = UIColor.black.withAlphaComponent(0)
      win.addSubview(overlay)
      NSLayoutConstraint.activate([
        overlay.leadingAnchor.constraint(equalTo: win.leadingAnchor),
        overlay.trailingAnchor.constraint(equalTo: win.trailingAnchor),
        overlay.topAnchor.constraint(equalTo: win.topAnchor),
        overlay.bottomAnchor.constraint(equalTo: win.bottomAnchor),
      ])
      self.transcriptOverlay = overlay

      let card = UIView()
      card.translatesAutoresizingMaskIntoConstraints = false
      card.backgroundColor = UIColor(red: 0.05, green: 0.05, blue: 0.07, alpha: 1)
      card.layer.cornerRadius = 22
      card.layer.maskedCorners = [.layerMinXMinYCorner, .layerMaxXMinYCorner]
      card.layer.borderWidth = 1
      card.layer.borderColor = UIColor(white: 1, alpha: 0.10).cgColor
      overlay.addSubview(card)

      // Card pinned to the bottom and ~85% of the screen tall — same
      // shape as the drawer but covers it entirely. Bottom sits flush
      // so when the keyboard appears we'll just inset the card up
      // (handleKeyboardChange already does this for the drawer; we
      // intentionally don't subscribe again here — the overlay's
      // composer manages its own keyboard avoidance).
      NSLayoutConstraint.activate([
        card.leadingAnchor.constraint(equalTo: overlay.leadingAnchor),
        card.trailingAnchor.constraint(equalTo: overlay.trailingAnchor),
        card.bottomAnchor.constraint(equalTo: overlay.bottomAnchor),
        card.heightAnchor.constraint(equalTo: overlay.heightAnchor, multiplier: 0.92),
      ])

      // Tiny grab handle at the top of the card.
      let handle = UIView()
      handle.translatesAutoresizingMaskIntoConstraints = false
      handle.backgroundColor = UIColor(white: 1, alpha: 0.18)
      handle.layer.cornerRadius = 2.5
      card.addSubview(handle)
      NSLayoutConstraint.activate([
        handle.centerXAnchor.constraint(equalTo: card.centerXAnchor),
        handle.topAnchor.constraint(equalTo: card.topAnchor, constant: 8),
        handle.widthAnchor.constraint(equalToConstant: 38),
        handle.heightAnchor.constraint(equalToConstant: 5),
      ])

      // Header: a single elegant `↻ Reload App` chip on the left
      // (replaces the old "Vibing" static title — the user is
      // obviously vibing, the chip serves a dual purpose) plus the
      // close ✕ on the right. Tapping the chip fires the overlay
      // reload flow (kickOverlayReload), same path the bottom-of-
      // composer reload used to use.
      let title = UIButton(type: .system)
      title.translatesAutoresizingMaskIntoConstraints = false
      let titleCfg = UIImage.SymbolConfiguration(pointSize: 13, weight: .semibold)
      title.setImage(UIImage(systemName: "arrow.clockwise", withConfiguration: titleCfg), for: .normal)
      title.setTitle("  Reload App", for: .normal)
      title.titleLabel?.font = .systemFont(ofSize: 14, weight: .semibold)
      title.contentEdgeInsets = UIEdgeInsets(top: 8, left: 14, bottom: 8, right: 16)
      title.backgroundColor = UIColor(white: 1, alpha: 0.08)
      title.tintColor = UIColor(red: 0.62, green: 0.66, blue: 1.0, alpha: 1)
      title.setTitleColor(UIColor(red: 0.62, green: 0.66, blue: 1.0, alpha: 1), for: .normal)
      title.layer.cornerRadius = 12
      title.layer.borderWidth = 1
      title.layer.borderColor = UIColor(white: 1, alpha: 0.12).cgColor
      title.addTarget(self, action: #selector(self.headerReloadChipTapped),
                      for: .touchUpInside)
      card.addSubview(title)

      let close = UIButton(type: .system)
      close.translatesAutoresizingMaskIntoConstraints = false
      close.setTitle("✕", for: .normal)
      close.titleLabel?.font = .systemFont(ofSize: 18, weight: .medium)
      close.tintColor = UIColor(white: 1, alpha: 0.6)
      close.setTitleColor(UIColor(white: 1, alpha: 0.6), for: .normal)
      close.addTarget(self, action: #selector(self.closeTranscriptOverlayTapped), for: .touchUpInside)
      card.addSubview(close)

      NSLayoutConstraint.activate([
        title.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 14),
        title.topAnchor.constraint(equalTo: handle.bottomAnchor, constant: 8),
        title.heightAnchor.constraint(equalToConstant: 34),
        close.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -14),
        close.centerYAnchor.constraint(equalTo: title.centerYAnchor),
        close.widthAnchor.constraint(equalToConstant: 32),
        close.heightAnchor.constraint(equalToConstant: 32),
      ])

      // Drop in the existing transcript view — it already handles
      // SSE subscribe, phase chip, follow-up composer. Reload now
      // lives in the header chip (`↻ Reload App`) wired via
      // headerReloadChipTapped → kickOverlayReload.
      let transcript = YaverFeedbackTranscript()
      transcript.translatesAutoresizingMaskIntoConstraints = false
      transcript.onCloseTap = { [weak self] in self?.closeTranscriptOverlay() }
      card.addSubview(transcript)
      NSLayoutConstraint.activate([
        transcript.leadingAnchor.constraint(equalTo: card.leadingAnchor),
        transcript.trailingAnchor.constraint(equalTo: card.trailingAnchor),
        transcript.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 8),
        transcript.bottomAnchor.constraint(equalTo: card.bottomAnchor),
      ])
      self.transcriptView = transcript
      transcript.attach(taskId: taskId, baseURL: baseURL,
                        headers: relayHeaders, userPrompt: userPrompt)

      // Slide-up animation. Initial transform off-screen, then spring
      // into place. Fade backdrop in over the same duration.
      card.transform = CGAffineTransform(translationX: 0, y: win.bounds.height)
      UIView.animate(withDuration: 0.32, delay: 0,
                     usingSpringWithDamping: 0.9,
                     initialSpringVelocity: 0.35,
                     options: [.curveEaseOut]) {
        overlay.backgroundColor = UIColor.black.withAlphaComponent(0.55)
        card.transform = .identity
      }

      // Update the drawer's status label so if the user closes the
      // overlay the drawer shows that the task was sent.
      self.setStatus("Sent ✓", tone: .success)
    }
  }

  /// Reload chip in the transcript overlay → fires /dev/reload-app
  /// AND subscribes to /dev/events so the streaming Hot-Reload phases
  /// (bundling → compiling → downloading → ready) appear inline in
  /// the transcript. User stays on the same screen, sees progress
  /// like the rest of the vibing output.
  ///
  /// Why we duplicate the drawer's reloadTapped logic instead of
  /// reusing it: drawer's path writes to the drawer's status label
  /// (hidden under the overlay) and uses the in-flight latch to gate
  /// further taps. The overlay needs its OWN narration sink (the
  /// transcript) and its OWN latch so a vibing follow-up isn't
  /// blocked by an in-flight reload.
  private func kickOverlayReload(transcript: YaverFeedbackTranscript?) {
    NSLog("[YaverFeedback] kickOverlayReload")
    guard let t = transcript else { return }
    if overlayReloadInFlight {
      t.appendNarration("\n\n_reload already in flight…_")
      return
    }
    overlayReloadInFlight = true
    UIImpactFeedbackGenerator(style: .light).impactOccurred()

    // Resolve each endpoint via yaverResolveAgentURL directly. The old
    // approach (yaverResolveAgentURL("/") + deletingLastPathComponent)
    // produced corrupted URLs when the cached agentBase ended in a
    // trailing slash: Foundation's deletingLastPathComponent on
    // `https://public.yaver.io/d/<id>/` STRIPS the deviceId segment,
    // and re-appending "dev/reload-app" gave `/d/dev/reload-app` —
    // which the relay treats as deviceId="dev" and 502s with "device
    // not connected to relay" because no agent registered under that
    // ID. /dev/events SSE was also broken the same way.
    //
    // ?fresh=1 on /dev/events skips the agent's 200-event history
    // replay so the overlay sees only events from THIS reload cycle.
    // Without it, the user got the prior reload's hbc_cache_lookup
    // → ready cycle on the wire as "memory" plus the live cycle, so
    // the transcript narrated "Hot reload triggered" twice and the
    // safety timeout fired before any of them registered as terminal.
    guard let reloadURL = yaverResolveAgentURL("/dev/reload-app"),
          let eventsURL = yaverResolveAgentURL("/dev/events?fresh=1") else {
      t.appendNarration("\n\n**Reload failed:** missing agent URL.")
      overlayReloadInFlight = false
      return
    }
    let headers = yaverRelayHeaders()

    // Start a fresh narration block so reload progress doesn't
    // splice into the previous assistant turn.
    t.startNewBlock()
    t.appendNarration("**↻ Reload requested**\nSubscribing to /dev/events…")

    // Subscribe to /dev/events FIRST, then POST /dev/reload-app.
    // Order matters: the agent's /dev/reload-app handler is
    // synchronous — it runs the full hermesc compile + bundle
    // broadcast inline (4–17s) before writing the HTTP response.
    // If we subscribe AFTER the POST returns, every phase event
    // (including the terminal `reload_done`) has already fired and
    // ?fresh=1 (no replay) drops them — the spinner sat forever
    // until the 90s safety timeout. Subscribing first means the
    // SSE channel is hot when the agent starts emitting and we
    // see the events as they arrive.
    subscribeOverlayReloadEvents(eventsURL: eventsURL, headers: headers, transcript: t)
    postReloadAppWithRetry(reloadURL: reloadURL, eventsURL: eventsURL,
                           headers: headers, transcript: t,
                           attempt: 1, maxAttempts: 4)
  }

  /// POST /dev/reload-app with a small retry budget. The relay can
  /// return HTTP 502 "device not connected to relay" when its tunnel
  /// session map briefly forgets the agent — happens after the
  /// agent restarts, after a long-idle keepalive, and occasionally
  /// mid-session for reasons that aren't worth a full investigation
  /// when the cheap fix (retry) works. Without this, the user sees
  /// "Reload HTTP 502" the first time and has to tap Reload again
  /// — which is exactly what we'd otherwise be coding into them.
  /// Backoff is 600 ms / 1.2 s / 2 s; total worst-case wait ≈ 4 s.
  private func postReloadAppWithRetry(reloadURL: URL,
                                      eventsURL: URL,
                                      headers: [String: String],
                                      transcript t: YaverFeedbackTranscript?,
                                      attempt: Int,
                                      maxAttempts: Int) {
    var req = URLRequest(url: reloadURL)
    req.httpMethod = "POST"
    for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = "{\"mode\":\"bundle\"}".data(using: .utf8)
    let task = URLSession.shared.dataTask(with: req) { [weak self, weak t] data, resp, err in
      DispatchQueue.main.async {
        guard let self = self else { return }
        if let err = err {
          t?.appendNarration("\n\n**Reload error:** \(err.localizedDescription)")
          self.overlayReloadInFlight = false
          return
        }
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        let body = data.flatMap { String(data: $0, encoding: .utf8) } ?? ""
        if code < 200 || code >= 300 {
          // 502 = relay can't reach agent (tunnel transiently lost).
          // 503 = agent reachable but dev-server build queue full.
          // Both recover on retry; 4xx are caller errors and stick.
          let retryable = code == 502 || code == 503
          if retryable && attempt < maxAttempts {
            let backoffMs = min(2000, 400 + attempt * 400)
            t?.appendNarration("\n_relay returned \(code); retrying (\(attempt)/\(maxAttempts - 1))…_")
            DispatchQueue.main.asyncAfter(deadline: .now() + .milliseconds(backoffMs)) { [weak self, weak t] in
              self?.postReloadAppWithRetry(reloadURL: reloadURL, eventsURL: eventsURL,
                                           headers: headers, transcript: t,
                                           attempt: attempt + 1, maxAttempts: maxAttempts)
            }
            return
          }
          t?.appendNarration("\n\n**Reload HTTP \(code):** \(body.prefix(120))")
          self.overlayReloadInFlight = false
          return
        }
        t?.appendNarration("\n_/dev/reload-app accepted; subscribing to /dev/events…_")
        self.subscribeOverlayReloadEvents(eventsURL: eventsURL, headers: headers, transcript: t)
      }
    }
    overlayReloadTask = task
    task.resume()
  }

  /// Subscribe to /dev/events SSE and turn each event into a one-line
  /// narration in the transcript. We strip down the JSON payload to
  /// the most useful field (kind/type + message/phase) so the user
  /// sees a tight progress trail, not a JSON dump.
  private func subscribeOverlayReloadEvents(
    eventsURL: URL,
    headers: [String: String],
    transcript: YaverFeedbackTranscript?
  ) {
    overlayReloadStream?.stop()
    overlayReloadStream = nil
    let url = eventsURL
    // YaverSSEReader splits init from start: init takes only the
    // event handlers; start(url:headers:) opens the actual session.
    let reader = YaverSSEReader(
      onEvent: { [weak self, weak transcript] payload in
        guard let t = transcript else { return }
        let line = self?.summarizeReloadEvent(payload) ?? payload
        if !line.isEmpty {
          DispatchQueue.main.async { t.appendNarration("\n" + line) }
        }
      },
      onComplete: { [weak self] in
        DispatchQueue.main.async {
          self?.overlayReloadInFlight = false
        }
      }
    )
    reader.start(url: url, headers: headers)
    overlayReloadStream = reader
    // Hard timeout — if the reload never reports complete in 90s,
    // release the latch so the user can retry.
    let timeout = DispatchWorkItem { [weak self] in
      if self?.overlayReloadInFlight == true {
        transcript?.appendNarration("\n_…(reload stream still running, giving up after 90s)_")
        self?.overlayReloadStream?.stop()
        self?.overlayReloadStream = nil
        self?.overlayReloadInFlight = false
      }
    }
    DispatchQueue.main.asyncAfter(deadline: .now() + 90, execute: timeout)
    overlayReloadTimeout = timeout
  }

  /// Squeeze the most useful field out of a /dev/events SSE payload.
  /// Returns "" for events we should silently drop (heartbeats etc.).
  ///
  /// Side effect: when a terminal event (`reload_done` / phase=done on
  /// topic="reload-app") arrives, clears `overlayReloadInFlight` so the
  /// composer's send button re-enables and the safety 90s timeout is
  /// no-op. Without this the spinner stayed up until the hard timeout
  /// even though the build had finished and the bundle had broadcast.
  private func summarizeReloadEvent(_ payload: String) -> String {
    guard let data = payload.data(using: .utf8),
          let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
      return ""
    }
    let type = (json["type"] as? String) ?? (json["kind"] as? String) ?? ""
    if type == "heartbeat" { return "" }
    let topic = (json["topic"] as? String ?? "").lowercased()
    let phase = (json["phase"] as? String ?? "").lowercased()
    // Pull message from the four shapes the agent uses: emit() sets
    // Message / Phase / LogLine on different paths. EmitLog now
    // mirrors logLine into message but older agents (still on the
    // wire while we roll out 1.99.150+) only set logLine.
    let message = (json["message"] as? String)
      ?? (json["logLine"] as? String)
      ?? (json["phase"] as? String)
      ?? (json["line"] as? String)
      ?? ""
    let lower = type.lowercased()
    // STRICT terminal detection. Earlier the broader
    // `lower.contains("complete")` matched `hermesc compile complete`
    // and other mid-build phase messages — clearing the spinner BEFORE
    // the agent's own reload_done event ever arrived, so the iOS
    // overlay never saw the bundleUrl payload and `swapBundleViaNativeLoader`
    // never fired. Now: only the agent's explicit `reload_done` type
    // or topic="reload-app" + phase="done" counts. Anything looser is
    // a false positive.
    let isTerminal = lower == "reload_done"
      || (topic == "reload-app" && phase == "done")
    if isTerminal {
      // Pull the agent-supplied bundleUrl out of the event so the
      // overlay can swap the running guest bridge directly. We can't
      // rely on the JS-side BlackBox listener for reload_bundle: when
      // the user has a guest loaded (todo-rn etc.), Yaver's host
      // bridge has been invalidated (the listener died with it) and
      // the guest's yaver-feedback-react-native SDK suppresses its
      // own listener via IS_HOST_MODE. Without this direct native
      // path, the broadcast was a tree-falls-in-the-forest event.
      let bundleURL = (json["bundleUrl"] as? String ?? "").trimmingCharacters(in: .whitespaces)
      NSLog("[YaverFeedback] reload_done received bundleURL=%@", bundleURL)
      DispatchQueue.main.async { [weak self] in
        // Stop polling /dev/events — we have what we needed.
        self?.overlayReloadStream?.stop()
        self?.overlayReloadStream = nil
        self?.overlayReloadTimeout?.cancel()
        self?.overlayReloadTimeout = nil
        // Note: we DON'T clear overlayReloadInFlight here — that
        // happens after swapBundleViaNativeLoader resolves, so the
        // user knows the actual app swap succeeded. Otherwise the
        // overlay said ✓ Reloaded while the bundle was still
        // downloading and the guest behind was unchanged.
        if !bundleURL.isEmpty {
          self?.swapBundleViaNativeLoader(bundleURL: bundleURL)
        } else {
          NSLog("[YaverFeedback] reload_done has no bundleURL — leaving guest as-is")
          self?.overlayReloadInFlight = false
        }
      }
      return "✓ **Bundle ready** — swapping app…"
    }
    if lower.contains("fail") || lower.contains("error") || phase == "error" {
      let detail = message.isEmpty ? (json["error"] as? String ?? "") : message
      DispatchQueue.main.async { [weak self] in
        self?.overlayReloadInFlight = false
      }
      return "**Reload failed:** " + detail
    }
    if !message.isEmpty {
      return "· " + message
    }
    return ""
  }

  // Latch + handles for the overlay reload flow. Mirrors the drawer's
  // reload state but in a different name space so an in-flight
  // overlay reload doesn't gate the drawer's reload (or vice versa).
  private var overlayReloadInFlight = false
  private var overlayReloadTask: URLSessionDataTask?
  private var overlayReloadStream: YaverSSEReader?
  private var overlayReloadTimeout: DispatchWorkItem?

  /// Swap the running guest's RN bridge to the freshly-built bundle.
  /// Stage-2 of the reload protocol:
  ///   stage 1 (agent):    build → emit `reload_done` with bundleUrl
  ///   stage 2 (this fn):  download HBC → post YaverBundleLoader.reloadNotification
  ///   stage 3 (AppDelegate.initGuestBridge): new bridge mounted →
  ///                       post AppDelegate.guestReloadCompleteNotification
  ///                       → overlay clears spinner with "✓ Reloaded"
  ///
  /// We get the YaverBundleLoader instance from the live RN bridge —
  /// it's a native module bundled into the app binary so it's
  /// re-registered in EVERY bridge instance, including the guest's
  /// post-swap bridge. Calling .loadBundle on it triggers the same
  /// download + reloadNotification + AppDelegate swap path as the JS
  /// route would, but bypasses the dead JS listeners (Yaver's host
  /// listener died with the original bridge; the guest's
  /// yaver-feedback-react-native SDK suppresses its own listener
  /// under IS_HOST_MODE).
  private func swapBundleViaNativeLoader(bundleURL: String) {
    NSLog("[YaverFeedback] swapBundleViaNativeLoader bundleURL=%@", bundleURL)
    guard let absoluteURL = yaverResolveAgentURL(bundleURL.hasPrefix("/") ? bundleURL : "/" + bundleURL) else {
      NSLog("[YaverFeedback] swapBundleViaNativeLoader: yaverResolveAgentURL returned nil")
      transcriptView?.appendNarration("\n**Reload failed:** could not resolve bundle URL.")
      overlayReloadInFlight = false
      return
    }
    // CRITICAL ARCHITECTURAL NOTE — why we go through the static
    // YaverBundleLoader.swap instead of looking up an instance via
    // the RN bridge:
    //
    // Yaver's host (and the guest bundle Yaver loads via "Open in
    // Yaver") use Expo's ReactAppDependencyProvider, which runs
    // React Native in BRIDGELESS / RCTHost mode. In that mode there
    // is NO `RCTBridge` object — the root view is
    // `RCTSurfaceHostingProxyRootView`, `as? RCTRootView` always
    // fails, and `bridge.module(for:)` doesn't exist. Walking
    // windows or scenes can't help; the API just isn't there.
    //
    // The fix: extract the entire download → validate → save →
    // post-reloadNotification pipeline into a static class func
    // (YaverBundleLoader.swap). The instance method `loadBundle`
    // that JS callers go through (initial "Open in Yaver" via
    // NativeModules.YaverBundleLoader.loadBundle) is untouched —
    // it still runs end-to-end. Native panes invoke the static
    // counterpart, which hits the same NotificationCenter-driven
    // AppDelegate swap path and produces the exact same end state.
    // No bridge, no instance, no architectural mode dependency.
    let headers = yaverRelayHeaders()
    let moduleName = UserDefaults.standard.string(forKey: "yaverLoadedModuleName") ?? "main"
    NSLog("[YaverFeedback] invoking YaverBundleLoader.swap url=%@ module=%@", absoluteURL.absoluteString, moduleName)

    // Arm AppDelegate's stage-3 listener BEFORE kicking the swap
    // so a fast-path bridge re-init can't beat us to the
    // notification post.
    armReloadCompleteListener()

    YaverBundleLoader.swap(url: absoluteURL.absoluteString,
                           moduleName: moduleName,
                           headers: headers) { [weak self] errorMessage in
      // completion fires on the main queue.
      if let err = errorMessage {
        NSLog("[YaverFeedback] swap failed: %@", err)
        self?.transcriptView?.appendNarration("\n**Reload failed:** \(err)")
        self?.overlayReloadInFlight = false
        self?.disarmReloadCompleteListener()
        return
      }
      NSLog("[YaverFeedback] swap success — bundle saved + reloadNotification posted")
      self?.transcriptView?.appendNarration("\n🔄 **Swapping app…**")
      // overlayReloadInFlight stays true — armReloadCompleteListener's
      // observer (or its 8s fallback) clears it and posts ✓ Reloaded
      // when AppDelegate finishes mounting the new bridge.
    }
  }

  /// Stage-3 listener — clears the overlay's spinner the moment
  /// AppDelegate's new bridge has mounted. Without this we'd either
  /// (a) declare ✓ Reloaded too early (when the agent finished build,
  /// before the bundle even downloaded — the user's complaint that
  /// the underlying app was still compiling) or (b) leave the
  /// spinner up until the 90s safety timeout fired even though the
  /// reload had succeeded.
  private var reloadCompleteObserver: NSObjectProtocol?
  private var reloadCompleteFallback: DispatchWorkItem?

  private func armReloadCompleteListener() {
    disarmReloadCompleteListener()
    reloadCompleteObserver = NotificationCenter.default.addObserver(
      forName: AppDelegate.guestReloadCompleteNotification,
      object: nil,
      queue: .main
    ) { [weak self] _ in
      NSLog("[YaverFeedback] guestReloadComplete notification received")
      self?.transcriptView?.appendNarration("\n✓ **Reloaded** — changes are live")
      self?.overlayReloadInFlight = false
      self?.disarmReloadCompleteListener()
    }
    // Belt-and-braces fallback: if the swap completes but the
    // notification doesn't reach us for some reason (timing race,
    // notification dropped during reentry), clear after 8s — the
    // bridge invalidate+rebuild on a 5-MB bundle takes 1–3s in
    // practice, so 8s is generous without being annoying.
    let fallback = DispatchWorkItem { [weak self] in
      guard let self = self, self.overlayReloadInFlight else { return }
      NSLog("[YaverFeedback] reload-complete fallback fired (8s)")
      self.transcriptView?.appendNarration("\n✓ **Reloaded** — bridge swap complete")
      self.overlayReloadInFlight = false
      self.disarmReloadCompleteListener()
    }
    reloadCompleteFallback = fallback
    DispatchQueue.main.asyncAfter(deadline: .now() + 8, execute: fallback)
  }

  private func disarmReloadCompleteListener() {
    if let observer = reloadCompleteObserver {
      NotificationCenter.default.removeObserver(observer)
      reloadCompleteObserver = nil
    }
    reloadCompleteFallback?.cancel()
    reloadCompleteFallback = nil
  }

  @objc private func closeTranscriptOverlayTapped() { closeTranscriptOverlay() }

  /// Header `↻ Reload App` chip in the transcript overlay. Same path
  /// as the (now-removed) bottom reload chip — fires /dev/reload-app
  /// and subscribes to /dev/events so the streaming Hot-Reload phases
  /// appear inline in the transcript.
  @objc private func headerReloadChipTapped() {
    NSLog("[YaverFeedback] headerReloadChipTapped")
    self.kickOverlayReload(transcript: self.transcriptView)
  }

  private func closeTranscriptOverlay() {
    NSLog("[YaverFeedback] closeTranscriptOverlay")
    transcriptView?.teardown()
    transcriptView = nil
    guard let overlay = transcriptOverlay else { return }
    UIView.animate(withDuration: 0.22, animations: {
      overlay.backgroundColor = UIColor.black.withAlphaComponent(0)
      // Slide the inner card off-screen — find it by being the only
      // subview that's a real card (the overlay only has one).
      for sub in overlay.subviews {
        sub.transform = CGAffineTransform(translationX: 0, y: overlay.bounds.height)
      }
    }, completion: { _ in
      overlay.removeFromSuperview()
      self.transcriptOverlay = nil
      // Closing the transcript also dismisses the underlying drawer —
      // user has finished the vibing round.
      self.dismiss()
    })
  }

  // MARK: - Transcript mode

  /// Swaps the prompt + screenshot-toggle + Send/Reload action row for
  /// a live transcript that streams the just-spawned task's output.
  /// Mirrors the Tasks tab's PhaseStatusLine + AssistantFrameRenderer
  /// so the user sees the same searching/compiling/working chip + the
  /// same purple inline-code styling without the React-Native subview.
  private func enterTranscriptMode(taskId: String, userPrompt: String) {
    // Already-transitioned guard. Re-entering this path while
    // transcriptView is non-nil would re-add a second transcript on
    // top of the first and is the most likely path to a dangling-view
    // crash if a delayed network callback fires after the user has
    // already dismissed and re-presented the pane.
    if transcriptView != nil { return }
    guard let card = cardView, card.window != nil else { return }
    // Resolve the agent base URL from the same /tasks URL we just
    // POSTed to, by stripping the trailing path component. This keeps
    // the relay/peer routing logic in one place (yaverResolveAgentURL).
    guard let tasksURL = yaverResolveAgentURL("/tasks") else { return }
    let baseURL = tasksURL.deletingLastPathComponent()

    // Tear down the form INSIDE a single layout-frozen block so the
    // keyboard-driven UIView.animate that runs in handleKeyboardChange
    // doesn't pick up a half-rebuilt view tree. Each removeFromSuperview
    // also nil-checks first — a simultaneous dismiss() on the same
    // pane has been seen to nil these out before we get here.
    promptField?.resignFirstResponder()
    UIView.performWithoutAnimation {
      promptField?.superview?.removeFromSuperview()
      screenshotToggle?.superview?.removeFromSuperview()
      agentChipButton?.superview?.removeFromSuperview()
      sendButton?.superview?.removeFromSuperview()
    }

    // Rebuild a transcript inside the card. The card already has its
    // header at the top; we hang the transcript below that, pinned
    // bottom to the card so the composer keyboard-avoids correctly.
    let transcript = YaverFeedbackTranscript()
    transcript.translatesAutoresizingMaskIntoConstraints = false
    transcript.onCloseTap = { [weak self] in self?.dismiss() }
    card.addSubview(transcript)

    // Pin below the title row (~64pt down from the top) and to the
    // card's bottom — handleKeyboardChange already moves the card
    // bottom to sit above the keyboard, so the composer follows.
    NSLayoutConstraint.activate([
      transcript.leadingAnchor.constraint(equalTo: card.leadingAnchor),
      transcript.trailingAnchor.constraint(equalTo: card.trailingAnchor),
      transcript.topAnchor.constraint(equalTo: card.topAnchor, constant: 64),
      transcript.bottomAnchor.constraint(equalTo: card.bottomAnchor),
    ])

    transcript.attach(taskId: taskId,
                      baseURL: baseURL,
                      headers: yaverRelayHeaders(),
                      userPrompt: userPrompt)
    transcriptView = transcript

    // Update the subtitle to reflect the new mode — the preflight CTA
    // is no longer relevant once a task is live.
    if let subtitle = subtitleLabel {
      subtitle.text = "live · vibing on remote"
      subtitle.textColor = UIColor(white: 1, alpha: 0.55)
      subtitle.tag = 0
    }
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

// MARK: - UIGestureRecognizerDelegate
//
// Without this, the bg tap recognizer (added at init time to dismiss
// the keyboard when the user taps outside the prompt) eats taps on
// Send / Reload / the screenshot toggle / the agent chip — first tap
// on Send dismisses the keyboard, the card reflows, the button moves
// out from under the user's finger, and `touchUpInside` never fires.
// User-visible symptom: "first Send click does nothing" / Send needs
// two taps.
//
// The delegate filter returns false whenever the touch lands on a
// UIControl (or its UIControl ancestor — UIButton's title label is
// itself a UILabel inside the button), so the gesture never recognises
// and the button receives the touch normally.
extension YaverFeedbackPane {
  func gestureRecognizer(_ gestureRecognizer: UIGestureRecognizer,
                         shouldReceive touch: UITouch) -> Bool {
    var v: UIView? = touch.view
    while v != nil {
      if v is UIControl { return false }
      v = v?.superview
    }
    return true
  }
}

extension YaverFeedbackPane: UITextViewDelegate {
  func textViewDidChange(_ textView: UITextView) {
    promptPlaceholder?.isHidden = !textView.text.isEmpty
  }
}
