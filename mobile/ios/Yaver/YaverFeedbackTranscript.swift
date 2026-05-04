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

  /// Public hook for the parent pane to push narration into the
  /// transcript — used by the Reload chip flow so the user sees
  /// streaming Hot-Reload progress (bundling → compiling →
  /// downloading → ready) IN the transcript instead of staring at
  /// a frozen-looking overlay.
  ///
  /// The text is appended to the current assistant turn's buffer
  /// and runs through the same throttle + markdown pipeline as
  /// vibing output, so a stream of "Bundling Expo for ios…" /
  /// "Compiling Hermes bytecode…" / "Reloaded ✓" lines render with
  /// the same chrome.
  func appendNarration(_ text: String) {
    appendAssistantChunk(text)
  }

  /// Reset the assistant accumulator so the next narration block
  /// starts fresh (used when Reload is tapped between turns — we
  /// want the reload progress to land in its own card, not appended
  /// to the prior assistant output).
  func startNewBlock() {
    startNewAssistantTurn()
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
      // Bottom inset bumped from -16 to -28 so "Send a command…" sits
      // visibly above the home-indicator gesture area instead of
      // hugging the bottom edge. Output scroll-area absorbs the
      // 12-pt difference automatically (its bottom anchors to the
      // composer's top).
      composerRow.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -28),
      // Composer is just the input row now — bottom Reload chip and
      // agent label moved into the header. 48pt (input row 44 + 4pt
      // touch-target slack); was 52pt, trimmed because we now reserve
      // the saved pixels for bottom inset instead.
      composerRow.heightAnchor.constraint(equalToConstant: 48),
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
    // Composer is just the input row + ↑ Send. Reload chip and agent
    // label moved to the overlay header (the `↻ Reload App` chip
    // there fires the same path) — this keeps the bottom of the pane
    // uncluttered so the user can fire follow-ups without stepping
    // over a crowded button row.
    let outer = UIView()
    outer.translatesAutoresizingMaskIntoConstraints = false

    let row = UIView()
    row.translatesAutoresizingMaskIntoConstraints = false
    outer.addSubview(row)

    let field = UITextField()
    field.translatesAutoresizingMaskIntoConstraints = false
    field.placeholder = "Send a command…"
    field.font = .systemFont(ofSize: 15)
    field.textColor = .white
    field.backgroundColor = UIColor(white: 1, alpha: 0.08)
    field.layer.cornerRadius = 12
    field.attributedPlaceholder = NSAttributedString(
      string: "Send a command…",
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
      field.heightAnchor.constraint(equalToConstant: 44),
      field.leadingAnchor.constraint(equalTo: row.leadingAnchor),
      field.trailingAnchor.constraint(equalTo: sendBtn.leadingAnchor, constant: -10),
      sendBtn.topAnchor.constraint(equalTo: row.topAnchor),
      sendBtn.heightAnchor.constraint(equalToConstant: 44),
      sendBtn.trailingAnchor.constraint(equalTo: row.trailingAnchor),
      sendBtn.widthAnchor.constraint(equalToConstant: 56),
      row.bottomAnchor.constraint(equalTo: field.bottomAnchor),
    ])

    NSLayoutConstraint.activate([
      row.topAnchor.constraint(equalTo: outer.topAnchor),
      row.leadingAnchor.constraint(equalTo: outer.leadingAnchor),
      row.trailingAnchor.constraint(equalTo: outer.trailingAnchor),
      row.bottomAnchor.constraint(equalTo: outer.bottomAnchor),
    ])
    return outer
  }

  // MARK: - User bubbles + assistant output

  /// Right-aligned purple chip for the prompt the user just sent.
  /// Mirrors the Tasks tab's chat bubble for the user-input line.
  ///
  /// Pads the text properly with a container view (was: bubble label
  /// with `"  " + text + "  "` hack which only padded HORIZONTAL
  /// edges and gave zero vertical breathing room — the previous
  /// build looked squeezed at the top/bottom on multi-line prompts).
  private func appendUserBubble(_ text: String) {
    guard let stack = stack else { return }
    let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
    if trimmed.isEmpty { return }

    let row = UIView()
    row.translatesAutoresizingMaskIntoConstraints = false

    // Padded container = the visible bubble. Holds the label inset
    // so the text never touches the rounded corners.
    let bubble = UIView()
    bubble.translatesAutoresizingMaskIntoConstraints = false
    bubble.backgroundColor = UIColor(red: 0.46, green: 0.51, blue: 0.96, alpha: 1)
    bubble.layer.cornerRadius = 18
    bubble.layer.masksToBounds = true

    let label = UILabel()
    label.translatesAutoresizingMaskIntoConstraints = false
    label.text = trimmed
    label.numberOfLines = 0
    label.lineBreakMode = .byWordWrapping
    label.font = .systemFont(ofSize: 15, weight: .regular)
    label.textColor = .white
    label.textAlignment = .left
    // Slight line-height bump for readability on multi-line prompts.
    let para = NSMutableParagraphStyle()
    para.lineSpacing = 2
    para.lineBreakMode = .byWordWrapping
    label.attributedText = NSAttributedString(
      string: trimmed,
      attributes: [
        .font: UIFont.systemFont(ofSize: 15, weight: .regular),
        .foregroundColor: UIColor.white,
        .paragraphStyle: para,
      ])

    bubble.addSubview(label)
    row.addSubview(bubble)
    NSLayoutConstraint.activate([
      // Bubble pinned to the right; capped at 85% of row width so it
      // has room to breathe like the response card while still
      // visually distinct from a full-width assistant block. Was
      // 0.78 — bumped because the user wanted a "more relaxed box,
      // like the response".
      bubble.topAnchor.constraint(equalTo: row.topAnchor, constant: 6),
      bubble.bottomAnchor.constraint(equalTo: row.bottomAnchor, constant: -6),
      bubble.trailingAnchor.constraint(equalTo: row.trailingAnchor),
      bubble.leadingAnchor.constraint(greaterThanOrEqualTo: row.leadingAnchor, constant: 40),
      bubble.widthAnchor.constraint(lessThanOrEqualTo: row.widthAnchor, multiplier: 0.85),

      // Label inset — match the response card's interior padding
      // (14×18) so single-line and multi-line prompts both look
      // intentional. Was 10×14 (squeezed); 14×18 matches the
      // assistant card and reads as a real "speech bubble".
      label.topAnchor.constraint(equalTo: bubble.topAnchor, constant: 14),
      label.bottomAnchor.constraint(equalTo: bubble.bottomAnchor, constant: -14),
      label.leadingAnchor.constraint(equalTo: bubble.leadingAnchor, constant: 18),
      label.trailingAnchor.constraint(equalTo: bubble.trailingAnchor, constant: -18),
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
    // Lazy-create the assistant CARD (matches Tasks tab's
    // AssistantBubble: dark rounded container with subtle border
    // and 12pt padding, label inside). The card replaces the bare
    // UILabel so streamed output gets the same chrome as the chat
    // bubbles in /tasks/<id> — visual parity per the user's
    // "make UI same as Tasks" ask.
    if outputLabel == nil {
      let card = UIView()
      card.translatesAutoresizingMaskIntoConstraints = false
      card.backgroundColor = UIColor(white: 1, alpha: 0.04)
      card.layer.cornerRadius = 12
      card.layer.borderWidth = 1
      card.layer.borderColor = UIColor(white: 1, alpha: 0.10).cgColor

      let label = UILabel()
      label.translatesAutoresizingMaskIntoConstraints = false
      label.numberOfLines = 0
      label.font = .systemFont(ofSize: 14.5, weight: .regular)
      label.textColor = .white
      label.lineBreakMode = .byWordWrapping
      card.addSubview(label)
      // Tighter padding (8/10) per the "make card shorter, give more
      // breathing room to composer + reload" request. Font also drops
      // a hair (14.5 → 14) so the same content fits in less vertical
      // space — visual delta matches Tasks tab's chat detail.
      NSLayoutConstraint.activate([
        label.topAnchor.constraint(equalTo: card.topAnchor, constant: 8),
        label.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 10),
        label.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -10),
        label.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -8),
      ])
      label.font = .systemFont(ofSize: 14, weight: .regular)
      outputLabel = label
      // Card sits left-aligned to ~92% of the column width — same
      // as Tasks tab where assistant bubbles never reach the right
      // edge (leaves room for visual grouping with user bubbles).
      let row = UIView()
      row.translatesAutoresizingMaskIntoConstraints = false
      row.addSubview(card)
      NSLayoutConstraint.activate([
        card.topAnchor.constraint(equalTo: row.topAnchor, constant: 4),
        card.bottomAnchor.constraint(equalTo: row.bottomAnchor, constant: -4),
        card.leadingAnchor.constraint(equalTo: row.leadingAnchor),
        card.trailingAnchor.constraint(lessThanOrEqualTo: row.trailingAnchor, constant: -40),
      ])
      if let phase = phaseLabel, let idx = stack.arrangedSubviews.firstIndex(of: phase) {
        stack.insertArrangedSubview(row, at: idx)
      } else {
        stack.addArrangedSubview(row)
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

  // MARK: - Markdown rendering + cleanup

  // Last assistant-side rendered NSAttributedString cached by raw input
  // so consecutive throttled flushes with the same buffer skip the
  // (now-heavier) markdown pass.
  private var lastRenderInputHash: Int = 0
  private var lastRenderResult: NSAttributedString?

  // ANSI / CSI escape stripper. Mirrors stripANSI in
  // desktop/agent/result_cleanup.go — codex emits 35m/3m/0m/etc. and
  // when the SSE stream ships raw outputCh chunks (vs cleaned
  // ResultText) those land in our buffer as literal `[35m[3mcodex[0m`.
  //
  // Implemented as a manual byte-walk rather than NSRegularExpression
  // because the previous regex pattern used Swift's `\u{001B}` syntax
  // which ICU doesn't accept — `try!` on the constructor crashed the
  // whole host (1.18.71). The walk is also strictly faster and can
  // never raise.

  // System-context end markers — last sentence of each agent-injected
  // context block (yaverDevServerContext / yaverWrapperCapability /
  // mobileTaskResponseContext / consoleTaskResponseContext). When
  // codex echoes the prompt back into stdout, slicing AFTER the LAST
  // marker gives us just the actual answer. Keep in sync with
  // desktop/agent/result_cleanup.go::systemContextEndMarkers and the
  // mobile SYSTEM_CONTEXT_END_MARKERS constant in tasks.tsx.
  private static let systemContextEndMarkers: [String] = [
    "Kill any stale expo/metro processes before retrying.",
    "or related Yaver preview tools instead of asking them to guess.",
    "pick up where you left off.",
    "give correct results when using with locales other than en-US.",
    // Was "the user wants…" — wrong word; the agent's actual string
    // (consoleTaskResponseContext / mobileTaskResponseContext in
    // task_context.go) says "the HUMAN wants to read the output
    // themselves." That typo silently disabled this slice and let
    // the entire "[Inspection commands — show raw output]" /
    // "Do NOT paraphrase" / "Do NOT replace the listing" prompt
    // block leak into the transcript.
    "the human wants to read the output themselves.",
    // End of Operation Contract from buildFeedbackPrompt — last
    // bullet of the contract block, just before "User feedback:".
    "use targeted reads.",
    "one short line, no exhaustive list.",
    "[Attached images — use the Read tool to examine these files]",
    // Final line of mobileTaskResponseContext when the older copy
    // is in flight on the agent — covers a parallel-session change.
    "Be concise without dropping critical information.",
  ]

  /// Render a string with bold, inline code, fenced code blocks, and
  /// bullets — close to what react-native-markdown-display gives the
  /// Tasks tab's AssistantBubble. Cheap (single pass, regex-light)
  /// since renders are already throttled to 150 ms.
  ///
  /// Pre-pass:
  ///   1. Strip ANSI escapes.
  ///   2. Slice after the LAST system-context end marker if any are
  ///      present — codex echoes our prompt block, we don't want to
  ///      show that to the user.
  private func renderInlineMarkdown(_ raw: String) -> NSAttributedString {
    var clean = stripANSI(raw)
    clean = sliceAfterSystemContext(clean)

    // Build attribute presets.
    let base: [NSAttributedString.Key: Any] = [
      .font: UIFont.systemFont(ofSize: 14.5, weight: .regular),
      .foregroundColor: UIColor(white: 1, alpha: 0.92),
    ]
    let bold: [NSAttributedString.Key: Any] = [
      .font: UIFont.systemFont(ofSize: 14.5, weight: .semibold),
      .foregroundColor: UIColor.white,
    ]
    let inlineCode: [NSAttributedString.Key: Any] = [
      .font: UIFont.monospacedSystemFont(ofSize: 13, weight: .regular),
      .foregroundColor: UIColor(red: 0.86, green: 0.55, blue: 1.0, alpha: 1),
      .backgroundColor: UIColor(white: 1, alpha: 0.07),
    ]
    let blockCode: [NSAttributedString.Key: Any] = [
      .font: UIFont.monospacedSystemFont(ofSize: 12.5, weight: .regular),
      .foregroundColor: UIColor(white: 1, alpha: 0.92),
      .backgroundColor: UIColor(white: 1, alpha: 0.06),
    ]
    let header: [NSAttributedString.Key: Any] = [
      .font: UIFont.systemFont(ofSize: 16, weight: .semibold),
      .foregroundColor: UIColor.white,
    ]

    let out = NSMutableAttributedString()
    let lines = clean.components(separatedBy: "\n")
    var inFence = false
    var fenceBuf = ""

    for (lineIdx, rawLine) in lines.enumerated() {
      // Fenced code block boundaries — opening / closing ```.
      if rawLine.trimmingCharacters(in: .whitespaces).hasPrefix("```") {
        if inFence {
          // Close fence: emit accumulated block.
          if !fenceBuf.isEmpty {
            // Trailing newline removed; pad with leading + trailing
            // newlines for visual breathing room.
            let body = fenceBuf.hasSuffix("\n") ? String(fenceBuf.dropLast()) : fenceBuf
            let padded = "\n" + body + "\n"
            out.append(NSAttributedString(string: padded, attributes: blockCode))
          }
          fenceBuf = ""
          inFence = false
        } else {
          inFence = true
        }
        continue
      }
      if inFence {
        fenceBuf += rawLine + "\n"
        continue
      }
      // Headers (#, ##, ###).
      if let hdr = rawLine.range(of: #"^#{1,6}\s+"#, options: .regularExpression) {
        let body = String(rawLine[hdr.upperBound...])
        out.append(NSAttributedString(string: body, attributes: header))
        if lineIdx < lines.count - 1 {
          out.append(NSAttributedString(string: "\n", attributes: base))
        }
        continue
      }
      // Bullet markers (- or *) — translate to a typographic bullet so
      // the line gets a visual indent identical to Tasks tab's list
      // rendering.
      var line = rawLine
      if let bul = line.range(of: #"^\s*[-*]\s+"#, options: .regularExpression) {
        let body = String(line[bul.upperBound...])
        line = "  •  " + body
      }
      // Inline pass — bold + code in one walk over the line.
      appendInlineSpans(line, baseAttrs: base, boldAttrs: bold,
                        codeAttrs: inlineCode, into: out)
      if lineIdx < lines.count - 1 {
        out.append(NSAttributedString(string: "\n", attributes: base))
      }
    }
    // Flush an unclosed fence so the user sees the in-flight block as
    // it streams in (codex emits the contents before closing ```).
    if inFence && !fenceBuf.isEmpty {
      let body = fenceBuf.hasSuffix("\n") ? String(fenceBuf.dropLast()) : fenceBuf
      out.append(NSAttributedString(string: "\n" + body + "\n", attributes: blockCode))
    }
    return out
  }

  /// Inline-pass: walk the line, emitting code spans (`...`) and
  /// bold spans (**...**) with their respective attributes; everything
  /// else gets the base attrs.
  private func appendInlineSpans(_ line: String,
                                 baseAttrs: [NSAttributedString.Key: Any],
                                 boldAttrs: [NSAttributedString.Key: Any],
                                 codeAttrs: [NSAttributedString.Key: Any],
                                 into out: NSMutableAttributedString) {
    var i = line.startIndex
    var buf = ""
    let end = line.endIndex
    while i < end {
      let c = line[i]
      // Inline code: `…`
      if c == "`" {
        if !buf.isEmpty {
          out.append(NSAttributedString(string: buf, attributes: baseAttrs)); buf.removeAll()
        }
        let after = line.index(after: i)
        if let close = line.range(of: "`", options: [], range: after..<end) {
          let inner = String(line[after..<close.lowerBound])
          out.append(NSAttributedString(string: " " + inner + " ", attributes: codeAttrs))
          i = line.index(after: close.lowerBound)
          continue
        }
        // Unmatched backtick — render as literal.
        buf.append(c)
        i = after
        continue
      }
      // Bold: **…**
      if c == "*" {
        let next = line.index(after: i)
        if next < end && line[next] == "*" {
          if !buf.isEmpty {
            out.append(NSAttributedString(string: buf, attributes: baseAttrs)); buf.removeAll()
          }
          let after = line.index(after: next)
          if after <= end,
             let close = line.range(of: "**", options: [], range: after..<end) {
            let inner = String(line[after..<close.lowerBound])
            out.append(NSAttributedString(string: inner, attributes: boldAttrs))
            i = line.index(after: line.index(after: close.lowerBound))
            continue
          }
          // Unmatched `**` — render literally.
          buf.append("**")
          i = after
          continue
        }
      }
      buf.append(c)
      i = line.index(after: i)
    }
    if !buf.isEmpty {
      out.append(NSAttributedString(string: buf, attributes: baseAttrs))
    }
  }

  /// Strip the most common ANSI / CSI runs without using a regex.
  /// Two cases handled:
  ///   1. `ESC[…final` — actual ESC byte (0x1B) followed by `[` then
  ///       params then a final byte in the @-~ range.
  ///   2. `[NNm` / `[NN;NN…m` — bare CSI run when ESC was already
  ///       filtered upstream (codex's stdout often shows these as
  ///       literal text after pty stripping).
  /// Anything that doesn't match those drops through unmodified.
  private func stripANSI(_ s: String) -> String {
    if s.isEmpty { return s }
    var out = ""
    out.reserveCapacity(s.count)
    let chars = Array(s)
    var i = 0
    while i < chars.count {
      let c = chars[i]
      // Case 1: ESC (0x1B) + '[' + params + final
      if c == "\u{001B}" && i + 1 < chars.count && chars[i + 1] == "[" {
        var j = i + 2
        while j < chars.count {
          let cj = chars[j]
          if cj.asciiValue != nil &&
             cj.asciiValue! >= 0x40 && cj.asciiValue! <= 0x7E {
            j += 1
            break
          }
          j += 1
        }
        i = j
        continue
      }
      // Case 2: bare '[NN(;NN)*m' (no ESC prefix)
      if c == "[" {
        var j = i + 1
        var sawDigit = false
        while j < chars.count {
          let cj = chars[j]
          if cj.isNumber || cj == ";" {
            if cj.isNumber { sawDigit = true }
            j += 1
            continue
          }
          break
        }
        if sawDigit && j < chars.count && chars[j] == "m" {
          i = j + 1
          continue
        }
      }
      out.append(c)
      i += 1
    }
    return out
  }

  private func sliceAfterSystemContext(_ s: String) -> String {
    var bestEnd = -1
    for marker in Self.systemContextEndMarkers {
      if let r = s.range(of: marker, options: .backwards) {
        let end = s.distance(from: s.startIndex, to: r.upperBound)
        if end > bestEnd { bestEnd = end }
      }
    }
    if bestEnd > 0 {
      let idx = s.index(s.startIndex, offsetBy: bestEnd)
      // Drop any leading whitespace/newlines so the cleaned content
      // starts cleanly without a giant empty gap.
      let tail = s[idx...].drop(while: { $0.isWhitespace || $0.isNewline })
      return String(tail)
    }
    return s
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
