import { readFileSync } from 'fs';
import { join } from 'path';

const plugin = require('../../app.plugin.js') as {
  __test: {
    patchHotReloadBootConfirmation(contents: string): string;
    patchHotReloadRestore(contents: string): string;
    patchDogfoodAppShortcut(contents: string): string;
    patchDogfoodSceneDelegate(contents: string): string;
  };
};

describe('native Dogfood shortcut contract', () => {
  it('never treats process uptime as a successful React render', () => {
    const staleHook = `
    // Crash-revert safety net: clear the boot-attempt counter once
    // RN renders its first frame, OR after 10 s of uptime — whichever
    // fires first. If neither fires (bundle crashes before render),
    // YaverHotReload.bundleURL() will eventually revert to the
    // TestFlight-installed bundle after 3 failed boots. See
    // YaverHotReload.swift for the full state machine.
    NotificationCenter.default.addObserver(
      forName: NSNotification.Name(rawValue: "RCTContentDidAppearNotification"),
      object: nil,
      queue: .main
    ) { _ in YaverHotReload.markBootSuccessful() }
    DispatchQueue.main.asyncAfter(deadline: .now() + 10) {
      YaverHotReload.markBootSuccessful()
    }
`;
    const patched = plugin.__test.patchHotReloadBootConfirmation(staleHook);
    expect(patched).toContain('RCTContentDidAppearNotification');
    expect(patched).toContain('only a real first React frame confirms boot');
    expect(patched).not.toContain('asyncAfter(deadline: .now() + 10)');
    expect(plugin.__test.patchHotReloadBootConfirmation(patched)).toBe(patched);
  });

  it('patches cold and warm iOS shortcut delivery idempotently', () => {
    const source = `
public class AppDelegate: ExpoAppDelegate {
  public override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
  ) -> Bool {
    setupYaverHotReload()
    return true
  }
}
`;
    const once = plugin.__test.patchDogfoodAppShortcut(source);
    expect(once).toContain('launchOptions?[.shortcutItem]');
    expect(once).toContain('performActionFor shortcutItem');
    expect(once).toContain('markDogfoodShortcutPending()');
    expect(plugin.__test.patchDogfoodAppShortcut(once)).toBe(once);
  });

  it('patches cold and warm scene-lifecycle delivery idempotently', () => {
    const source = `
final class TalosSceneDelegate: UIResponder, UIWindowSceneDelegate {
  func scene(
    _ scene: UIScene,
    willConnectTo session: UISceneSession,
    options connectionOptions: UIScene.ConnectionOptions
  ) {
    guard scene is UIWindowScene else { return }
  }
}
`;
    const once = plugin.__test.patchDogfoodSceneDelegate(source);
    expect(once).toContain('connectionOptions.shortcutItem');
    expect(once).toContain('func windowScene(');
    expect(once).toContain('markDogfoodShortcutPending()');
    expect(plugin.__test.patchDogfoodSceneDelegate(once)).toBe(once);
  });

  it('uses dynamic native shortcuts on both platforms', () => {
    const ios = readFileSync(join(__dirname, '../../ios/YaverHotReload.swift'), 'utf8');
    const android = readFileSync(join(__dirname, '../../android/src/main/java/io/yaver/feedback/YaverHotReloadModule.java'), 'utf8');
    expect(ios).toContain('UIApplication.shared.shortcutItems');
    expect(ios).toContain('setDogfoodShortcut');
    expect(android).toContain('ShortcutManager');
    expect(android).toContain('addDynamicShortcuts');
    expect(android).toContain('consumeDogfoodShortcut');
  });

  it('rejects an invalid Hermes download before replacing the last good app bundle', () => {
    const ios = readFileSync(join(__dirname, '../../ios/YaverHotReload.swift'), 'utf8');
    const android = readFileSync(join(__dirname, '../../android/src/main/java/io/yaver/feedback/YaverHotReloadModule.java'), 'utf8');
    expect(ios).toContain('INVALID_HERMES_BUNDLE');
    expect(ios.indexOf('INVALID_HERMES_BUNDLE')).toBeLessThan(ios.indexOf('data.write(to: savePath'));
    expect(android).toContain('new AtomicFile(bundleFile)');
    expect(android).toContain('INVALID_HERMES_BUNDLE');
    expect(android.indexOf('INVALID_HERMES_BUNDLE')).toBeLessThan(android.indexOf('atomicFile.startWrite()'));
    expect(android).toContain('private static final ExecutorService RELOAD_EXECUTOR');
    expect(android).not.toContain('Executors.newSingleThreadExecutor().execute');
  });

  it('exits Dogfood by clearing the hot bundle and recreating the embedded app', () => {
    const ios = readFileSync(join(__dirname, '../../ios/YaverHotReload.swift'), 'utf8');
    const iosBridge = readFileSync(join(__dirname, '../../ios/YaverHotReload.m'), 'utf8');
    const android = readFileSync(join(__dirname, '../../android/src/main/java/io/yaver/feedback/YaverHotReloadModule.java'), 'utf8');
    expect(ios).toContain('func clearBundleAndReload');
    expect(ios).toContain('"restoreEmbedded": true');
    expect(iosBridge).toContain('clearBundleAndReload');
    expect(android).toContain('void clearBundleAndReload');
    expect(android).toMatch(/clearBundleAndReload[\s\S]*reloadBridge\(\)/);
  });

  it('upgrades an older iOS AppDelegate so Exit can restore the embedded bundle', () => {
    const staleHandler = `
  @objc private func yaverHandleHotReload(_ notification: Notification) {
    guard let bundlePath = notification.userInfo?["bundlePath"] as? String else {
      yaverIsReloading = false
      return
    }

    let bundleURL = URL(fileURLWithPath: bundlePath)
    guard FileManager.default.fileExists(atPath: bundlePath) else {
      NSLog("[YaverHotReload] bundle not found at %@", bundlePath)
      yaverIsReloading = false
      return
    }

    NSLog("[YaverHotReload] reloading bridge with %@", bundlePath)
      _ = bundleURL  // silence "unused" warning; the file path was
                    // baked into YaverHotReload by loadBundle() above
  }
`;
    const patched = plugin.__test.patchHotReloadRestore(staleHandler);
    expect(patched).toContain('restoreEmbedded');
    expect(patched).toContain('restoring app-bundled React Native surface');
    expect(patched).not.toContain('let bundleURL =');
    expect(plugin.__test.patchHotReloadRestore(patched)).toBe(patched);
  });

  it('observes a three-finger hold without consuming host app touches', () => {
    const ios = readFileSync(join(__dirname, '../../ios/YaverDogfoodGesture.swift'), 'utf8');
    const android = readFileSync(join(__dirname, '../../android/src/main/java/io/yaver/feedback/YaverDogfoodGestureModule.java'), 'utf8');
    const controls = readFileSync(join(__dirname, '../DogfoodQuickControls.tsx'), 'utf8');
    const menu = readFileSync(join(__dirname, '../DogfoodControlMenu.tsx'), 'utf8');
    expect(ios).toContain('numberOfTouchesRequired = 3');
    expect(ios).toContain('cancelsTouchesInView = false');
    expect(ios).toContain('isVoiceOverRunning');
    expect(android).toContain('event.getPointerCount() >= 3');
    expect(android).toContain('method.invoke(callback, args)');
    expect(android).toContain('isTouchExplorationEnabled');
    expect(controls).toContain('<DogfoodControlMenu');
    expect(menu).toContain('yaver-dogfood-fast-reload');
    expect(menu).toContain('yaver-dogfood-chat');
    expect(controls).toContain('yaver-dogfood-minimized-control');
  });
});
