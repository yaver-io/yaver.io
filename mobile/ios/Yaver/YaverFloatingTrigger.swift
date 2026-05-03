import Foundation
import UIKit

/// Yaver's floating draggable trigger — alternative to shake-to-open.
/// A small ~56pt circle with a "Y" glyph mounted in the key window's
/// top-most position. Tapping fires the same path as a shake (the
/// AppDelegate's overlay), so the user gets the exact same Feedback /
/// Agents / Settings / Back-to-Yaver card without having to wave the
/// device around. Useful in the iOS simulator (no accelerometer) and
/// for users who want a deliberate, repeatable trigger.
///
/// Behaviour:
///   - 56pt circle, purple-accent border, semi-transparent dark fill,
///     drop shadow so it floats convincingly over the guest UI.
///   - Pan to move anywhere on the screen; releases snap to the
///     nearer horizontal edge (left or right) so it's not in the
///     middle of the user's content area when idle.
///   - Tap to fire the configured action.
///   - Position remembered in UserDefaults so it stays where the user
///     dropped it across launches.
///   - Auto-dismounts when the host routes back to the Yaver shell
///     (AppDelegate calls dismount() when isGuestAppRunning flips).
final class YaverFloatingTrigger: NSObject {

  static let shared = YaverFloatingTrigger()

  // Dedicated overlay window. Lives at UIWindow.Level.alert + 1 so it
  // sits above ANY view the host is presenting — settings pane, shake
  // overlay, RN-mounted modals, even the system status bar. The
  // previous "add as subview of the AppDelegate's main window" path
  // was racing with the shake overlay / Settings pane add order, and
  // on some scenes self.window pointed at a different UIWindow than
  // the one the user was actually looking at, so the bubble was
  // mounted onto an invisible window and never seen.
  private var overlayWindow: UIWindow?
  private var bubble: UIView?
  private var onTap: (() -> Void)?
  private let prefsKeyX = "yaverFloatingTriggerX"
  private let prefsKeyY = "yaverFloatingTriggerY"
  private let bubbleSize: CGFloat = 64
  private let edgeMargin: CGFloat = 8

  func mount(in window: UIWindow, onTap: @escaping () -> Void) {
    NSLog("[FloatingY] mount called window.bounds=%@ safeAreaTop=%.1f bottom=%.1f",
          NSCoder.string(for: window.bounds),
          window.safeAreaInsets.top, window.safeAreaInsets.bottom)

    // Snap away any prior bubble so a rapid Settings flip
    // (Shake → Floating → Shake → Floating) doesn't no-op on the
    // remount because the prior bubble was mid-fade.
    if let stale = bubble {
      stale.layer.removeAllAnimations()
      stale.removeFromSuperview()
      bubble = nil
    }
    overlayWindow?.isHidden = true
    overlayWindow = nil

    self.onTap = onTap

    // Create a dedicated overlay window in the same scene as the
    // passed window. This is the iOS-canonical way to put something
    // above all other UI — the bubble lives at .alert + 1, so it
    // floats above the system keyboard, the alert layer, and the
    // host app's normal content.
    let win: UIWindow
    if let scene = window.windowScene {
      win = UIWindow(windowScene: scene)
    } else {
      win = UIWindow(frame: window.bounds)
    }
    win.windowLevel = UIWindow.Level.alert + 1
    win.backgroundColor = .clear
    // Container VC required by UIKit for any non-system window.
    let vc = YaverFloatingTriggerOverlayVC()
    vc.view.backgroundColor = .clear
    win.rootViewController = vc
    win.isHidden = false
    overlayWindow = win
    NSLog("[FloatingY] overlay window installed bounds=%@ level=%.0f",
          NSCoder.string(for: win.bounds), win.windowLevel.rawValue)

    let bg = UIView()
    bg.backgroundColor = UIColor(red: 0.06, green: 0.05, blue: 0.12, alpha: 0.95)
    bg.layer.cornerRadius = bubbleSize / 2
    bg.layer.borderWidth = 2
    bg.layer.borderColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1.0).cgColor
    bg.layer.shadowColor = UIColor.black.cgColor
    bg.layer.shadowOpacity = 0.55
    bg.layer.shadowRadius = 10
    bg.layer.shadowOffset = CGSize(width: 0, height: 4)

    let label = UILabel(frame: CGRect(x: 0, y: 0, width: bubbleSize, height: bubbleSize))
    label.text = "Y"
    label.textColor = UIColor(red: 0.85, green: 0.88, blue: 1.0, alpha: 1.0)
    label.font = .systemFont(ofSize: 26, weight: .heavy)
    label.textAlignment = .center
    bg.addSubview(label)

    let initial = restorePosition(in: win)
    bg.frame = CGRect(x: initial.x, y: initial.y, width: bubbleSize, height: bubbleSize)
    vc.view.addSubview(bg)
    bubble = bg
    NSLog("[FloatingY] bubble added at %@", NSCoder.string(for: bg.frame))

    let pan = UIPanGestureRecognizer(target: self, action: #selector(handlePan(_:)))
    let tap = UITapGestureRecognizer(target: self, action: #selector(handleTap))
    pan.delegate = self
    tap.delegate = self
    bg.addGestureRecognizer(pan)
    bg.addGestureRecognizer(tap)

    // Subtle entrance animation
    bg.alpha = 0
    bg.transform = CGAffineTransform(scaleX: 0.6, y: 0.6)
    UIView.animate(withDuration: 0.25, delay: 0, usingSpringWithDamping: 0.7,
                   initialSpringVelocity: 0.5) {
      bg.alpha = 1.0
      bg.transform = .identity
    }
  }

  func dismount() {
    guard let b = bubble else { return }
    // Reset bubble + onTap immediately so a subsequent mount() during
    // the fade-out doesn't see stale state. The view itself fades out
    // on its own.
    bubble = nil
    onTap = nil
    UIView.animate(withDuration: 0.18, animations: {
      b.alpha = 0
      b.transform = CGAffineTransform(scaleX: 0.6, y: 0.6)
    }, completion: { _ in
      b.removeFromSuperview()
    })
  }

  // MARK: - Gesture handlers

  @objc private func handlePan(_ gr: UIPanGestureRecognizer) {
    guard let b = bubble, let win = window else { return }
    let translation = gr.translation(in: win)
    var newCenter = CGPoint(x: b.center.x + translation.x, y: b.center.y + translation.y)
    // Clamp so the bubble stays fully on-screen
    let half = bubbleSize / 2
    newCenter.x = max(half + edgeMargin, min(win.bounds.width - half - edgeMargin, newCenter.x))
    newCenter.y = max(half + win.safeAreaInsets.top + edgeMargin,
                      min(win.bounds.height - half - win.safeAreaInsets.bottom - edgeMargin, newCenter.y))
    b.center = newCenter
    gr.setTranslation(.zero, in: win)

    if gr.state == .ended || gr.state == .cancelled {
      // Snap to nearer horizontal edge for a tidy resting position.
      let snappedX = b.center.x < win.bounds.width / 2
        ? half + edgeMargin
        : win.bounds.width - half - edgeMargin
      UIView.animate(withDuration: 0.18, delay: 0, usingSpringWithDamping: 0.85,
                     initialSpringVelocity: 0.4) {
        b.center = CGPoint(x: snappedX, y: b.center.y)
      } completion: { _ in
        self.savePosition(b.frame.origin)
      }
    }
  }

  @objc private func handleTap() {
    UIImpactFeedbackGenerator(style: .light).impactOccurred()
    onTap?()
  }

  // MARK: - Persistence

  private func restorePosition(in window: UIWindow) -> CGPoint {
    let defaults = UserDefaults.standard
    let storedX = defaults.object(forKey: prefsKeyX) as? CGFloat
    let storedY = defaults.object(forKey: prefsKeyY) as? CGFloat
    if let x = storedX, let y = storedY,
       x >= 0, y >= 0,
       x + bubbleSize <= window.bounds.width,
       y + bubbleSize <= window.bounds.height {
      return CGPoint(x: x, y: y)
    }
    // Default: top-right under the safe area top. Picked so that when
    // the user toggles "Floating Y button" from the Settings bottom-
    // sheet, the bubble appears above the still-open pane — they get
    // immediate visual confirmation the setting took effect, instead
    // of nothing changing until pane dismiss reveals a bubble that
    // was hidden under the sheet the whole time.
    let x = window.bounds.width - bubbleSize - 16
    let y = window.safeAreaInsets.top + 24
    return CGPoint(x: x, y: y)
  }

  private func savePosition(_ origin: CGPoint) {
    let defaults = UserDefaults.standard
    defaults.set(origin.x, forKey: prefsKeyX)
    defaults.set(origin.y, forKey: prefsKeyY)
  }
}

extension YaverFloatingTrigger: UIGestureRecognizerDelegate {
  // Allow tap + pan to coexist on the same view: tap fires only on a
  // tiny movement (UITapGestureRecognizer's default), pan kicks in
  // for anything larger. UIKit handles the disambiguation natively.
  func gestureRecognizer(_ gestureRecognizer: UIGestureRecognizer,
                         shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool {
    return false
  }
}

// View that passes through every touch EXCEPT touches that land inside
// one of its subviews. Without this, the floating-Y window would
// swallow every tap on the screen — making the host app unusable while
// the bubble is mounted.
private final class YaverFloatingTriggerPassThroughView: UIView {
  override func point(inside point: CGPoint, with event: UIEvent?) -> Bool {
    for sub in subviews where !sub.isHidden && sub.alpha > 0.01 &&
      sub.frame.contains(point) {
      return true
    }
    return false
  }
}

private final class YaverFloatingTriggerOverlayVC: UIViewController {
  override func loadView() {
    self.view = YaverFloatingTriggerPassThroughView()
    self.view.backgroundColor = .clear
  }
  // Don't rotate-restrict — match whatever the host's window is doing.
  override var prefersStatusBarHidden: Bool { false }
}
