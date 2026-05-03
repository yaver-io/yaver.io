import Foundation
import UIKit

/// Yaver's native settings pane — fourth shake-overlay action ("Settings").
/// Bottom-sheet card mirroring YaverFeedbackPane's visual language so the
/// three panes feel like one widget set: same purple-tinted blur, same
/// rounded top corners, same drag handle, same close X.
///
/// Single concern today: pick how the shake overlay opens.
///
///   - "Shake" — current default. Tilt/jolt the phone.
///   - "Floating Y button" — adds a small draggable circular button
///     overlaid on the guest app. Tap it to open the same overlay you
///     would get from a shake. Useful in the iOS simulator (no real
///     accelerometer) and for users who want a deliberate trigger.
///
/// The choice is persisted under UserDefaults("yaverFeedbackTrigger")
/// using the same string values as the standalone yaver-feedback-react-
/// native SDK's `trigger?: 'shake' | 'floating-button' | 'manual'` field
/// — that way an SDK module bridged via YaverInfo or any other consumer
/// can read a single source of truth for the user's preference.
final class YaverSettingsPane: NSObject {

  static let shared = YaverSettingsPane()

  private weak var window: UIWindow?
  private weak var cardView: UIView?
  private var applyTrigger: (() -> Void)?
  private var shakeRow: UIView!
  private var buttonRow: UIView!

  /// Slide the pane up over the given window. `applyTrigger` runs
  /// IMMEDIATELY whenever the user taps an option — not on dismiss —
  /// so picking "Floating Y button" shows the bubble live while the
  /// pane is still open, instead of waiting half a second of animation
  /// for anything to visibly change.
  func present(in window: UIWindow, applyTrigger: @escaping () -> Void) {
    // Same stateful guard as the other panes.
    if cardView != nil { return }
    self.window = window
    self.applyTrigger = applyTrigger
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
  }

  // MARK: - UI

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
    title.text = "Settings"
    title.textColor = .white
    title.font = .systemFont(ofSize: 17, weight: .semibold)

    let subtitle = UILabel()
    subtitle.translatesAutoresizingMaskIntoConstraints = false
    subtitle.text = "how should the overlay open?"
    subtitle.textColor = UIColor(white: 1, alpha: 0.55)
    subtitle.font = .systemFont(ofSize: 12)

    let close = UIButton(type: .system)
    close.translatesAutoresizingMaskIntoConstraints = false
    close.setImage(UIImage(systemName: "xmark", withConfiguration:
                            UIImage.SymbolConfiguration(pointSize: 16, weight: .semibold)), for: .normal)
    close.tintColor = UIColor(white: 1, alpha: 0.6)
    close.addTarget(self, action: #selector(dismissTapped), for: .touchUpInside)

    shakeRow = makeOptionRow(
      title: "Shake to open",
      subtitle: "Tilt or jolt the phone — current default",
      iconName: "hand.tap.fill",
      value: "shake")
    buttonRow = makeOptionRow(
      title: "Floating Y button",
      subtitle: "A draggable button you can tap any time — useful on simulator",
      iconName: "circle.dashed",
      value: "floating-button")

    let content = UIStackView(arrangedSubviews: [shakeRow, buttonRow])
    content.translatesAutoresizingMaskIntoConstraints = false
    content.axis = .vertical
    content.spacing = 10

    bg.contentView.addSubview(handle)
    bg.contentView.addSubview(title)
    bg.contentView.addSubview(subtitle)
    bg.contentView.addSubview(close)
    bg.contentView.addSubview(content)

    NSLayoutConstraint.activate([
      bg.heightAnchor.constraint(greaterThanOrEqualToConstant: 320),

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
      content.topAnchor.constraint(equalTo: subtitle.bottomAnchor, constant: 18),
      content.bottomAnchor.constraint(lessThanOrEqualTo: bg.contentView.bottomAnchor, constant: -28),
    ])

    refreshSelectionIndicator()
    return bg
  }

  private func makeOptionRow(title: String, subtitle: String, iconName: String, value: String) -> UIView {
    let row = UIControl()
    row.translatesAutoresizingMaskIntoConstraints = false
    row.backgroundColor = UIColor(white: 1, alpha: 0.06)
    row.layer.cornerRadius = 14
    row.layer.borderWidth = 1
    row.layer.borderColor = UIColor.clear.cgColor

    let icon = UIImageView(image: UIImage(systemName: iconName,
      withConfiguration: UIImage.SymbolConfiguration(pointSize: 18, weight: .semibold)))
    icon.tintColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1)
    icon.translatesAutoresizingMaskIntoConstraints = false
    icon.contentMode = .scaleAspectFit

    let titleLabel = UILabel()
    titleLabel.translatesAutoresizingMaskIntoConstraints = false
    titleLabel.text = title
    titleLabel.textColor = .white
    titleLabel.font = .systemFont(ofSize: 15, weight: .semibold)

    let subLabel = UILabel()
    subLabel.translatesAutoresizingMaskIntoConstraints = false
    subLabel.text = subtitle
    subLabel.textColor = UIColor(white: 1, alpha: 0.55)
    subLabel.font = .systemFont(ofSize: 12)
    subLabel.numberOfLines = 2

    let check = UIImageView(image: UIImage(systemName: "checkmark.circle.fill",
      withConfiguration: UIImage.SymbolConfiguration(pointSize: 22, weight: .semibold)))
    check.tintColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1)
    check.translatesAutoresizingMaskIntoConstraints = false
    check.contentMode = .scaleAspectFit
    check.isHidden = true
    check.tag = 8888 // findable by refreshSelectionIndicator

    row.addSubview(icon)
    row.addSubview(titleLabel)
    row.addSubview(subLabel)
    row.addSubview(check)

    NSLayoutConstraint.activate([
      row.heightAnchor.constraint(greaterThanOrEqualToConstant: 64),

      icon.leadingAnchor.constraint(equalTo: row.leadingAnchor, constant: 14),
      icon.centerYAnchor.constraint(equalTo: row.centerYAnchor),
      icon.widthAnchor.constraint(equalToConstant: 24),
      icon.heightAnchor.constraint(equalToConstant: 24),

      titleLabel.leadingAnchor.constraint(equalTo: icon.trailingAnchor, constant: 12),
      titleLabel.topAnchor.constraint(equalTo: row.topAnchor, constant: 12),
      titleLabel.trailingAnchor.constraint(lessThanOrEqualTo: check.leadingAnchor, constant: -8),

      subLabel.leadingAnchor.constraint(equalTo: titleLabel.leadingAnchor),
      subLabel.topAnchor.constraint(equalTo: titleLabel.bottomAnchor, constant: 2),
      subLabel.trailingAnchor.constraint(lessThanOrEqualTo: check.leadingAnchor, constant: -8),
      subLabel.bottomAnchor.constraint(equalTo: row.bottomAnchor, constant: -12),

      check.trailingAnchor.constraint(equalTo: row.trailingAnchor, constant: -14),
      check.centerYAnchor.constraint(equalTo: row.centerYAnchor),
      check.widthAnchor.constraint(equalToConstant: 24),
      check.heightAnchor.constraint(equalToConstant: 24),
    ])

    row.accessibilityIdentifier = value
    row.addTarget(self, action: #selector(optionTapped(_:)), for: .touchUpInside)
    return row
  }

  @objc private func optionTapped(_ sender: UIControl) {
    guard let value = sender.accessibilityIdentifier else { return }
    UserDefaults.standard.set(value, forKey: "yaverFeedbackTrigger")
    UISelectionFeedbackGenerator().selectionChanged()
    refreshSelectionIndicator()
    // Apply the change LIVE so the floating Y bubble appears (or
    // disappears) the instant the user taps an option, while the
    // pane is still on screen. Previous version waited until dismiss
    // animated out — user reverted choice before seeing any effect
    // and concluded the setting did nothing.
    applyTrigger?()
    // Brief pause for visual checkmark feedback, then dismiss.
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) { [weak self] in
      self?.dismiss()
    }
  }

  private func refreshSelectionIndicator() {
    let mode = UserDefaults.standard.string(forKey: "yaverFeedbackTrigger") ?? "shake"
    for row in [shakeRow, buttonRow] {
      guard let r = row else { continue }
      let value = r.accessibilityIdentifier
      let selected = value == mode
      r.layer.borderColor = selected
        ? UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 0.65).cgColor
        : UIColor.clear.cgColor
      r.backgroundColor = selected
        ? UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 0.10)
        : UIColor(white: 1, alpha: 0.06)
      if let check = r.viewWithTag(8888) {
        check.isHidden = !selected
      }
    }
  }

  @objc private func dismissTapped() { dismiss() }

  private func dismiss() {
    guard let card = cardView else { return }
    UIView.animate(withDuration: 0.22, animations: {
      card.transform = CGAffineTransform(translationX: 0, y: 600)
      card.alpha = 0
    }, completion: { _ in
      card.removeFromSuperview()
      self.cardView = nil
      self.applyTrigger = nil
    })
  }
}
