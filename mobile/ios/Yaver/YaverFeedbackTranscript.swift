import Foundation
import UIKit

/// Transcript view for the native Feedback pane — displays the live
/// stream coming back from the remote box's vibing run, with the same
/// phase chip + braille spinner the React-Native Tasks tab uses
/// (working… / searching… / compiling… / cooking… / shipping…).
///
/// Mirrors `mobile/app/(tabs)/tasks.tsx` PhaseStatusLine + AssistantFrameRenderer
/// at the level the feedback pane needs: scrollable output area, phase
/// chip below it, follow-up composer at the bottom. Markdown rendering
/// is intentionally simpler than the JS side — we render plain text +
/// inline `code` spans coloured purple, which covers 95% of what the
/// user actually sees during a vibing run.
///
/// Lifecycle: caller hands us a taskId after `POST /tasks` returns 201,
/// we subscribe to `GET /tasks/{taskId}/output` (SSE: `{"type":"output",
/// "text":"…"}` and terminal `{"type":"done","status":"…"}`), render as
/// each chunk arrives, recompute the phase from the tail of the buffer.
/// Follow-up taps POST `/tasks/{taskId}/continue` and the stream keeps
/// emitting on the same channel.
final class YaverFeedbackTranscript: UIView {

  // MARK: - Public

  /// Called when the user taps the embedded Reload chip so the host
  /// pane can dispatch the same `/dev/reload-app` POST it does today.
  var onReloadTap: (() -> Void)?
  /// Called when the user taps Close / X so the host pane can dismiss.
  var onCloseTap: (() -> Void)?

  /// Configure the transcript with the just-created task and start
  /// streaming. `headers` is whatever yaverRelayHeaders() returns —
  /// includes the auth bearer + any X-Yaver-Relay-* shape.
  func attach(taskId: String, baseURL: URL, headers: [String: String], userPrompt: String) {
    self.taskId = taskId
    self.baseURL = baseURL
    self.headers = headers
    appendUserBubble(userPrompt)
    startStream()
    startSpinner()
  }

  func teardown() {
    stream?.stop(); stream = nil
    spinnerTimer?.invalidate(); spinnerTimer = nil
    pendingFollowUp?.cancel(); pendingFollowUp = nil
  }

  // MARK: - State

  private var taskId: String?
  private var baseURL: URL?
  private var headers: [String: String] = [:]
  private var stream: YaverSSEReader?
  private var pendingFollowUp: URLSessionDataTask?

  // Output buffer + the live UILabel we mutate. We use a label (not
  // a UITextView) for the streamed output so the height auto-grows
  // inside the scroll view — UITextView's intrinsic content size is
  // unreliable when its text mutates frequently.
  private var outputBuffer = ""
  private weak var outputLabel: UILabel?
  private weak var scroll: UIScrollView?
  private weak var stack: UIStackView?

  // Phase chip — one labelled UIView with a spinner glyph + lowercase
  // phase phrase. Rebuilt on each recomputePhase() rather than animated,
  // because a recycled label preserves UIKit's intrinsic-size cache and
  // we don't have to fight the layout pass.
  private weak var phaseLabel: UILabel?
  private var spinnerTimer: Timer?
  private var spinnerIndex = 0
  private var currentPhase = "working"
  private var taskDone = false

  // Follow-up composer
  private weak var composerField: UITextField?
  private weak var composerSendBtn: UIButton?
  private var followUpInFlight = false

  // Phase phrase rotation cycle from tasks.tsx::deriveTaskPhases.
  // Lowercased, with the action gerund the chip should display.
  private static let spinnerFrames = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]

  // MARK: - Build

  override init(frame: CGRect) {
    super.init(frame: frame)
    buildUI()
  }
  required init?(coder: NSCoder) { fatalError("not used") }

  private func buildUI() {
    backgroundColor = .clear

    let scrollView = UIScrollView()
    scrollView.translatesAutoresizingMaskIntoConstraints = false
    scrollView.alwaysBounceVertical = true
    scrollView.showsVerticalScrollIndicator = false
    addSubview(scrollView)
    scroll = scrollView

    let stackView = UIStackView()
    stackView.translatesAutoresizingMaskIntoConstraints = false
    stackView.axis = .vertical
    stackView.spacing = 12
    stackView.alignment = .fill
    scrollView.addSubview(stackView)
    stack = stackView

    // Phase chip lives at the BOTTOM of the scroll content so it sits
    // just above the most recent output. We don't pin it to the pane —
    // it stays inline with the transcript so when the user scrolls up
    // through history the spinner moves with it.
    let phase = makePhaseLabel()
    stackView.addArrangedSubview(phase)

    // Composer at the very bottom of the pane — sticks above the
    // keyboard via parent's keyboard avoidance constraint (handled
    // by YaverFeedbackPane.handleKeyboardChange).
    let composerRow = makeComposer()

    addSubview(composerRow)

    NSLayoutConstraint.activate([
      scrollView.topAnchor.constraint(equalTo: topAnchor),
      scrollView.leadingAnchor.constraint(equalTo: leadingAnchor),
      scrollView.trailingAnchor.constraint(equalTo: trailingAnchor),
      scrollView.bottomAnchor.constraint(equalTo: composerRow.topAnchor, constant: -8),

      stackView.topAnchor.constraint(equalTo: scrollView.contentLayoutGuide.topAnchor, constant: 4),
      stackView.leadingAnchor.constraint(equalTo: scrollView.frameLayoutGuide.leadingAnchor, constant: 16),
      stackView.trailingAnchor.constraint(equalTo: scrollView.frameLayoutGuide.trailingAnchor, constant: -16),
      stackView.bottomAnchor.constraint(equalTo: scrollView.contentLayoutGuide.bottomAnchor, constant: -4),

      composerRow.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
      composerRow.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -12),
      composerRow.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -10),
      composerRow.heightAnchor.constraint(equalToConstant: 44),
    ])
  }

  private func makePhaseLabel() -> UILabel {
    let label = UILabel()
    label.translatesAutoresizingMaskIntoConstraints = false
    // Match tasks.tsx::PhaseStatusLine: monospace 13pt, muted purple
    // when active, fades to neutral on done.
    label.font = .monospacedSystemFont(ofSize: 13, weight: .regular)
    label.textColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 0.85)
    label.text = "⠋  working…"
    phaseLabel = label
    return label
  }

  private func makeComposer() -> UIView {
    let row = UIView()
    row.translatesAutoresizingMaskIntoConstraints = false

    let field = UITextField()
    field.translatesAutoresizingMaskIntoConstraints = false
    field.placeholder = "Follow up…"
    field.font = .systemFont(ofSize: 15)
    field.textColor = .white
    field.backgroundColor = UIColor(white: 1, alpha: 0.08)
    field.layer.cornerRadius = 12
    field.attributedPlaceholder = NSAttributedString(
      string: "Follow up…",
      attributes: [.foregroundColor: UIColor(white: 1, alpha: 0.35)])
    field.leftView = UIView(frame: CGRect(x: 0, y: 0, width: 12, height: 1))
    field.leftViewMode = .always
    field.returnKeyType = .send
    field.delegate = self
    composerField = field

    let sendBtn = UIButton(type: .system)
    sendBtn.translatesAutoresizingMaskIntoConstraints = false
    let cfg = UIImage.SymbolConfiguration(pointSize: 14, weight: .semibold)
    sendBtn.setImage(UIImage(systemName: "arrow.up", withConfiguration: cfg), for: .normal)
    sendBtn.backgroundColor = UIColor(red: 0.46, green: 0.51, blue: 0.96, alpha: 1)
    sendBtn.tintColor = .white
    sendBtn.layer.cornerRadius = 12
    sendBtn.addTarget(self, action: #selector(handleFollowUpSendTap), for: .touchUpInside)
    composerSendBtn = sendBtn

    row.addSubview(field)
    row.addSubview(sendBtn)

    NSLayoutConstraint.activate([
      field.topAnchor.constraint(equalTo: row.topAnchor),
      field.bottomAnchor.constraint(equalTo: row.bottomAnchor),
      field.leadingAnchor.constraint(equalTo: row.leadingAnchor),
      field.trailingAnchor.constraint(equalTo: sendBtn.leadingAnchor, constant: -10),

      sendBtn.topAnchor.constraint(equalTo: row.topAnchor),
      sendBtn.bottomAnchor.constraint(equalTo: row.bottomAnchor),
      sendBtn.trailingAnchor.constraint(equalTo: row.trailingAnchor),
      sendBtn.widthAnchor.constraint(equalToConstant: 56),
    ])
    return row
  }

  // MARK: - User bubbles + assistant output

  /// Right-aligned purple chip for the prompt the user just sent.
  /// Mirrors the Tasks tab's chat bubble for the user-input line.
  private func appendUserBubble(_ text: String) {
    guard let stack = stack else { return }
    let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
    if trimmed.isEmpty { return }
    let row = UIView()
    row.translatesAutoresizingMaskIntoConstraints = false
    let bubble = UILabel()
    bubble.translatesAutoresizingMaskIntoConstraints = false
    bubble.text = "  " + trimmed + "  "
    bubble.numberOfLines = 0
    bubble.font = .systemFont(ofSize: 15, weight: .regular)
    bubble.textColor = .white
    bubble.backgroundColor = UIColor(red: 0.46, green: 0.51, blue: 0.96, alpha: 1)
    bubble.layer.cornerRadius = 14
    bubble.layer.masksToBounds = true
    bubble.textAlignment = .left
    row.addSubview(bubble)
    NSLayoutConstraint.activate([
      bubble.topAnchor.constraint(equalTo: row.topAnchor, constant: 4),
      bubble.bottomAnchor.constraint(equalTo: row.bottomAnchor, constant: -4),
      bubble.trailingAnchor.constraint(equalTo: row.trailingAnchor),
      bubble.leadingAnchor.constraint(greaterThanOrEqualTo: row.leadingAnchor, constant: 60),
    ])
    // Insert ABOVE the phase chip so the phase chip stays at the bottom.
    if let phase = phaseLabel, let idx = stack.arrangedSubviews.firstIndex(of: phase) {
      stack.insertArrangedSubview(row, at: idx)
    } else {
      stack.addArrangedSubview(row)
    }
    scrollToBottom()
  }

  // Render-throttle state. Without these the transcript re-rendered the
  // ENTIRE accumulated buffer on every SSE chunk on the main thread —
  // when codex dumped a few hundred KB (e.g. an `npm install` log or a
  // bytecode dump), every chunk fired a synchronous renderInlineMarkdown
  // over a string that grew unboundedly, blocking the runloop and
  // freezing the whole UI (touch events stopped servicing). User
  // symptom on 1.18.67: phone stuck inside the overlay, couldn't even
  // tap Close.
  //
  // Mitigation:
  //   - Cap the rendered buffer at MAX_BUFFER_BYTES; older content is
  //     truncated from the head (keep tail). Stream view always shows
  //     the most recent ~32 KB of output, more than enough for
  //     readability while keeping renderInlineMarkdown's input bounded.
  //   - Throttle re-renders: chunks accumulate into a pending buffer,
  //     a single render fires every RENDER_INTERVAL_MS at most.
  private static let MAX_BUFFER_BYTES = 32 * 1024
  private static let RENDER_INTERVAL_MS: Int = 150
  private var pendingRender = false
  private var lastRenderAt: TimeInterval = 0

  /// Lazy-create the assistant output label and append into it. We use
  /// ONE accumulating label per assistant turn — re-creating per chunk
  /// would explode memory and shred the layout cache. New turns (user
  /// follow-ups) reset the label so the next assistant reply gets its
  /// own block.
  private func appendAssistantChunk(_ text: String) {
    guard let stack = stack else { return }
    if text.isEmpty { return }
    outputBuffer += text
    // Truncate the buffer if it overshoots the cap. Keep the tail —
    // that's what the user is reading. UTF-8 safety: count() works on
    // Character (grapheme clusters); we approximate bytes by scalar
    // count, which is a strict upper bound on glyph layout work.
    if outputBuffer.count > Self.MAX_BUFFER_BYTES {
      let dropCount = outputBuffer.count - Self.MAX_BUFFER_BYTES
      let idx = outputBuffer.index(outputBuffer.startIndex, offsetBy: dropCount)
      outputBuffer = "…[earlier output trimmed]…\n" + outputBuffer[idx...]
    }
    // Lazy-create the label without a render — the throttle below
    // handles content. We only need the label to exist so the
    // throttle's render target is live.
    if outputLabel == nil {
      let label = UILabel()
      label.translatesAutoresizingMaskIntoConstraints = false
      label.numberOfLines = 0
      label.font = .systemFont(ofSize: 15, weight: .regular)
      label.textColor = .white
      label.lineBreakMode = .byWordWrapping
      outputLabel = label
      if let phase = phaseLabel, let idx = stack.arrangedSubviews.firstIndex(of: phase) {
        stack.insertArrangedSubview(label, at: idx)
      } else {
        stack.addArrangedSubview(label)
      }
    }
    scheduleThrottledRender()
  }

  /// Coalesces incoming SSE chunks into one render every
  /// RENDER_INTERVAL_MS. Cheap on idle (single bool check), bounded on
  /// burst (one render per interval no matter the chunk rate).
  private func scheduleThrottledRender() {
    if pendingRender { return }
    pendingRender = true
    let now = Date().timeIntervalSince1970 * 1000.0
    let interval = TimeInterval(Self.RENDER_INTERVAL_MS)
    let elapsed = now - (lastRenderAt * 1000.0)
    let delay = max(0.0, (interval - elapsed) / 1000.0)
    DispatchQueue.main.asyncAfter(deadline: .now() + delay) { [weak self] in
      self?.flushPendingRender()
    }
  }

  private func flushPendingRender() {
    pendingRender = false
    lastRenderAt = Date().timeIntervalSince1970
    guard let label = outputLabel else { return }
    label.attributedText = renderInlineMarkdown(outputBuffer)
    recomputePhase()
    scrollToBottom()
  }

  /// Reset the assistant-label pointer so the next streamed chunk
  /// builds a fresh block (called when the user sends a follow-up).
  private func startNewAssistantTurn() {
    outputLabel = nil
    outputBuffer = ""
    taskDone = false
    startSpinner()
  }

  private func scrollToBottom() {
    guard let scroll = scroll else { return }
    DispatchQueue.main.async {
      scroll.layoutIfNeeded()
      let bottomY = max(0, scroll.contentSize.height - scroll.bounds.height + scroll.contentInset.bottom)
      scroll.setContentOffset(CGPoint(x: 0, y: bottomY), animated: false)
    }
  }

  // MARK: - Markdown (just inline `code` spans + soft-wrap)

  /// Render a string with inline backtick-delimited code spans coloured
  /// purple — same treatment the React-Native AssistantFrameRenderer
  /// applies via react-native-markdown-display's code_inline style. We
  /// don't try to handle fences (```block```) here — the tasks.tsx side
  /// renders them as code blocks but for the feedback pane the inline
  /// path covers the bulk of what users see during a vibing run.
  private func renderInlineMarkdown(_ raw: String) -> NSAttributedString {
    let baseAttrs: [NSAttributedString.Key: Any] = [
      .font: UIFont.systemFont(ofSize: 15, weight: .regular),
      .foregroundColor: UIColor.white,
    ]
    let codeAttrs: [NSAttributedString.Key: Any] = [
      .font: UIFont.monospacedSystemFont(ofSize: 13, weight: .regular),
      .foregroundColor: UIColor(red: 0.91, green: 0.47, blue: 0.97, alpha: 1),
      .backgroundColor: UIColor(white: 1, alpha: 0.08),
    ]
    let out = NSMutableAttributedString()
    var inCode = false
    var buf = ""
    for ch in raw {
      if ch == "`" {
        if inCode {
          out.append(NSAttributedString(string: " " + buf + " ", attributes: codeAttrs))
        } else {
          out.append(NSAttributedString(string: buf, attributes: baseAttrs))
        }
        buf.removeAll()
        inCode.toggle()
      } else {
        buf.append(ch)
      }
    }
    if !buf.isEmpty {
      // If we ended mid-code (unmatched backtick), render the trailing
      // run as plain text rather than swallowing it.
      out.append(NSAttributedString(string: buf, attributes: inCode ? baseAttrs : baseAttrs))
    }
    return out
  }

  // MARK: - Phase chip

  /// tasks.tsx::deriveTaskPhases — match one of six gerund families
  /// against the tail of the assistant output. Order matters: searching
  /// is the most common false positive, so it goes first; shipping is
  /// the rarest but a strong signal so it goes last.
  private func phaseFor(tail: String) -> String {
    let lower = String(tail.suffix(120)).lowercased()
    if matchesAny(lower, ["search", "find", "grep", "rg ", "ripgrep", "scan", "inspect", "trace", "ls ", "cat "]) {
      return "searching"
    }
    if matchesAny(lower, ["plan", "reason", "thinking", "analyz", "investigat", "review"]) {
      return "mapping"
    }
    if matchesAny(lower, ["edit", "patch", "write", "refactor", "implement", "apply_patch", "create file"]) {
      return "cooking"
    }
    if matchesAny(lower, ["build", "compil", "tsc", "xcodebuild", "gradle", "go build", "cargo build", "bundle", "hermes"]) {
      return "compiling"
    }
    if matchesAny(lower, ["test", "jest", "vitest", "pytest", "go test", "cargo test", "unit test"]) {
      return "checking"
    }
    if matchesAny(lower, ["publish", "deploy", "upload", "ship", "release", "testflight", "play store", "pypi", "npm publish"]) {
      return "shipping"
    }
    return "working"
  }

  private func matchesAny(_ haystack: String, _ needles: [String]) -> Bool {
    for needle in needles where haystack.contains(needle) { return true }
    return false
  }

  private func recomputePhase() {
    let next = phaseFor(tail: outputBuffer)
    if next != currentPhase {
      currentPhase = next
    }
    refreshPhaseLabel()
  }

  private func refreshPhaseLabel() {
    guard let label = phaseLabel else { return }
    if taskDone {
      label.text = "✓  done"
      label.textColor = UIColor(red: 0.27, green: 0.85, blue: 0.49, alpha: 1)
    } else {
      let frame = Self.spinnerFrames[spinnerIndex % Self.spinnerFrames.count]
      label.text = "\(frame)  \(currentPhase)…"
      label.textColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 0.85)
    }
  }

  private func startSpinner() {
    spinnerTimer?.invalidate()
    spinnerIndex = 0
    refreshPhaseLabel()
    let timer = Timer(timeInterval: 0.09, repeats: true) { [weak self] _ in
      guard let self = self else { return }
      if self.taskDone { return }
      self.spinnerIndex = (self.spinnerIndex + 1) % Self.spinnerFrames.count
      self.refreshPhaseLabel()
    }
    RunLoop.main.add(timer, forMode: .common)
    spinnerTimer = timer
  }

  private func stopSpinnerWithDone(_ status: String) {
    taskDone = true
    spinnerTimer?.invalidate()
    spinnerTimer = nil
    if let label = phaseLabel {
      switch status.lowercased() {
      case "completed", "finished", "done":
        label.text = "✓  done"
        label.textColor = UIColor(red: 0.27, green: 0.85, blue: 0.49, alpha: 1)
      case "failed", "error":
        label.text = "✗  failed"
        label.textColor = UIColor(red: 1, green: 0.45, blue: 0.45, alpha: 1)
      case "stopped", "cancelled", "canceled":
        label.text = "■  stopped"
        label.textColor = UIColor(white: 1, alpha: 0.55)
      default:
        label.text = "—  \(status)"
        label.textColor = UIColor(white: 1, alpha: 0.55)
      }
    }
  }

  // MARK: - Stream

  private func startStream() {
    guard let baseURL = baseURL, let taskId = taskId else { return }
    let url = baseURL.appendingPathComponent("tasks").appendingPathComponent(taskId).appendingPathComponent("output")
    let reader = YaverSSEReader(
      onEvent: { [weak self] payload in self?.handleStreamEvent(payload) },
      onComplete: { [weak self] in
        // Connection closed — if the agent didn't already send a `done`
        // event, mark stopped so the spinner doesn't spin forever.
        guard let self = self, !self.taskDone else { return }
        self.stopSpinnerWithDone("stopped")
      }
    )
    reader.start(url: url, headers: headers)
    stream = reader
  }

  private func handleStreamEvent(_ payload: String) {
    guard let data = payload.data(using: .utf8),
          let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
      return
    }
    let kind = (json["type"] as? String) ?? ""
    switch kind {
    case "output":
      if let text = json["text"] as? String { appendAssistantChunk(text) }
    case "done":
      let status = (json["status"] as? String) ?? "completed"
      stopSpinnerWithDone(status)
      stream?.stop(); stream = nil
    default:
      // Unknown event — ignore. The agent may add more types over time
      // (cost summary, progress %, etc.) and we'd rather forward-compat
      // by skipping than crash.
      break
    }
  }

  // MARK: - Follow-up

  @objc private func handleFollowUpSendTap() {
    sendFollowUp()
  }

  private func sendFollowUp() {
    if followUpInFlight { return }
    guard let field = composerField else { return }
    let text = (field.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    if text.isEmpty { return }
    guard let baseURL = baseURL, let taskId = taskId else { return }
    let url = baseURL.appendingPathComponent("tasks").appendingPathComponent(taskId).appendingPathComponent("continue")
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = try? JSONSerialization.data(withJSONObject: ["input": text])

    followUpInFlight = true
    composerSendBtn?.isEnabled = false
    appendUserBubble(text)
    field.text = ""
    startNewAssistantTurn()

    let task = URLSession.shared.dataTask(with: req) { [weak self] _, resp, err in
      DispatchQueue.main.async {
        guard let self = self else { return }
        self.pendingFollowUp = nil
        self.followUpInFlight = false
        self.composerSendBtn?.isEnabled = true
        if let err = err {
          if (err as NSError).code == NSURLErrorCancelled { return }
          self.appendAssistantChunk("\n\n_follow-up failed: \(err.localizedDescription)_")
          self.stopSpinnerWithDone("failed")
          return
        }
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if code < 200 || code >= 300 {
          self.appendAssistantChunk("\n\n_follow-up failed (HTTP \(code))_")
          self.stopSpinnerWithDone("failed")
          return
        }
        // Re-open the SSE if the previous /output stream ended on the
        // prior `done` event — /continue reuses the same task, so the
        // next chunks stream over the same /tasks/{id}/output channel.
        if self.stream == nil { self.startStream() }
      }
    }
    pendingFollowUp = task
    task.resume()
  }
}

extension YaverFeedbackTranscript: UITextFieldDelegate {
  func textFieldShouldReturn(_ textField: UITextField) -> Bool {
    sendFollowUp()
    return false
  }
}
