import Foundation

/// Minimal Server-Sent Events reader for native panes that want to
/// surface streaming build progress (Hermes bundle compile, expo
/// bundling, etc.) inside Yaver's bottom-sheet UIs without spinning
/// up a full React-Native subview.
///
/// The agent's /dev/events endpoint emits JSON-encoded frames in
/// the standard SSE format:
///
///     data: {"kind":"log","line":"Bundling Hermes bytecode…"}
///
///     data: {"kind":"status","phase":"compile","message":"Compiling…"}
///
/// We don't try to mirror EventSource's full feature set (last-event-id,
/// retry hints, custom event names) — `/dev/events` doesn't use them
/// and the simpler the parser, the smaller the regression surface for
/// a feature-pane's "what's happening right now?" line.
///
/// Lifecycle:
///   * `start(url:headers:)` opens a long-lived URLSession data task.
///   * `onEvent` fires once per `data: …` line, on the main queue.
///   * `onComplete` fires when the connection ends (either side).
///   * `stop()` cancels — call from `viewWillDisappear` so a long
///     stream doesn't outlive the pane.
final class YaverSSEReader: NSObject, URLSessionDataDelegate {

  private let onEvent: (String) -> Void
  private let onComplete: () -> Void
  private var buffer = Data()
  private var task: URLSessionDataTask?
  private var session: URLSession?

  init(onEvent: @escaping (String) -> Void, onComplete: @escaping () -> Void) {
    self.onEvent = onEvent
    self.onComplete = onComplete
    super.init()
  }

  /// Start streaming. Reuses the agent's relay-aware URL resolution
  /// upstream — pass an already-built absolute URL (typically from
  /// `yaverResolveAgentURL("/dev/events")`).
  func start(url: URL, headers: [String: String]) {
    let config = URLSessionConfiguration.default
    // Prevent NSURLSession from caching the streaming response — we
    // want every byte fresh, and the cache layer just adds GC pressure
    // for an event stream that's never re-fetched.
    config.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
    config.timeoutIntervalForRequest = 60
    config.timeoutIntervalForResource = 600
    let s = URLSession(configuration: config, delegate: self, delegateQueue: nil)
    self.session = s

    var req = URLRequest(url: url)
    req.httpMethod = "GET"
    req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
    req.setValue("no-cache", forHTTPHeaderField: "Cache-Control")
    for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
    let t = s.dataTask(with: req)
    self.task = t
    t.resume()
  }

  func stop() {
    task?.cancel()
    task = nil
    session?.invalidateAndCancel()
    session = nil
  }

  // MARK: - URLSessionDataDelegate

  func urlSession(_: URLSession, dataTask _: URLSessionDataTask, didReceive data: Data) {
    buffer.append(data)
    // SSE frames are separated by `\n\n`. Walk the buffer and
    // dispatch one event per complete frame; leave any trailing
    // partial bytes for the next chunk.
    let separator = Data("\n\n".utf8)
    while let r = buffer.range(of: separator) {
      let frame = buffer.subdata(in: 0 ..< r.lowerBound)
      buffer.removeSubrange(0 ..< r.upperBound)
      guard let text = String(data: frame, encoding: .utf8) else { continue }
      // Each frame can have multiple lines; we only care about
      // `data: …`. Comments (`: keepalive`) and other field types
      // are intentionally ignored.
      for line in text.split(separator: "\n", omittingEmptySubsequences: false) {
        if line.hasPrefix("data: ") {
          let payload = String(line.dropFirst("data: ".count))
          DispatchQueue.main.async { [weak self] in
            self?.onEvent(payload)
          }
        } else if line.hasPrefix("data:") {
          // Some emitters omit the space after the colon — accept
          // that variant too rather than silently dropping events.
          let payload = String(line.dropFirst("data:".count))
          DispatchQueue.main.async { [weak self] in
            self?.onEvent(payload)
          }
        }
      }
    }
  }

  func urlSession(_: URLSession, task _: URLSessionTask, didCompleteWithError _: Error?) {
    DispatchQueue.main.async { [weak self] in
      self?.onComplete()
    }
  }
}
