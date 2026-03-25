import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  Dimensions,
  Keyboard,
  PanResponder,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import { YaverFeedback } from './YaverFeedback';
import { FixReport } from './FixReport';
import type { TestSession } from './types';
import { BlackBox } from './BlackBox';

export interface FloatingButtonProps {
  /** Called when user taps the button (opens inline console by default). */
  onPress?: () => void;
  /** Initial position. Default: top-left corner, below status bar. */
  initialPosition?: { x: number; y: number };
  /** Button size in pixels. Default: 40. */
  size?: number;
  /**
   * Button background color. Default: "#6366f1" (indigo).
   * Use a distinctive color so the debug button is never confused
   * with your app's UI. Suggested: pink, purple, lime.
   */
  color?: string;
  /** Show connection status dot on the button. Default: true. */
  showStatusDot?: boolean;
  /**
   * Style preset:
   * - "terminal" (default) — dark terminal look with >_ icon, monospace font, pink accents
   * - "minimal" — small circle, single-letter icon, clean panel
   */
  style?: 'terminal' | 'minimal';
  /** Custom icon text. Default: "y". */
  icon?: string;
  /** Agent base URL (auto-detected from YaverFeedback config if omitted). */
  agentUrl?: string;
  /** Auth token (auto-detected from YaverFeedback config if omitted). */
  authToken?: string;
  /**
   * Health check interval in ms. The button polls the agent's /health
   * endpoint to show connection status. Default: 5000. Set to 0 to disable.
   */
  healthCheckInterval?: number;
  /**
   * Background color of the debug console panel.
   * Default: "#2d2d2d" (dark gray). Override to match your app's theme.
   */
  panelBackgroundColor?: string;
}

const DEFAULT_SIZE = 40;
const DEFAULT_COLOR = '#6366f1';
const DEFAULT_PANEL_BG = '#2d2d2d';

/**
 * Draggable debug console button for the Yaver Feedback SDK.
 *
 * Drop this into any React Native app for an instant debug console
 * with message back-and-forth, hot reload, and build+deploy:
 *
 * ```tsx
 * import { FloatingButton } from '@yaver/feedback-react-native';
 *
 * function App() {
 *   return (
 *     <>
 *       <YourApp />
 *       <FloatingButton />
 *     </>
 *   );
 * }
 * ```
 *
 * Features:
 * - **Tap** → expand terminal-style console panel
 * - **Drag** → reposition anywhere
 * - **Type** → send tasks to the AI agent, see responses
 * - **Hot Reload** → trigger hot reload
 * - **Build iOS** → build + auto-submit to TestFlight
 * - **Build Android** → build + auto-submit to Play Store
 * - **"quit"** → disable the SDK
 */
export const FloatingButton: React.FC<FloatingButtonProps> = ({
  onPress,
  initialPosition,
  size = DEFAULT_SIZE,
  color = DEFAULT_COLOR,
  showStatusDot = true,
  style: stylePreset = 'terminal',
  icon,
  agentUrl: agentUrlProp,
  authToken: authTokenProp,
  healthCheckInterval = 5000,
  panelBackgroundColor,
}) => {
  const { width: screenWidth } = Dimensions.get('window');
  const defaultX = initialPosition?.x ?? 10;
  const defaultY = initialPosition?.y ?? 90;

  const pan = useRef(new Animated.ValueXY({ x: defaultX, y: defaultY })).current;
  const isDragging = useRef(false);
  const [chatOpen, setChatOpen] = useState(false);
  const [fullSize, setFullSize] = useState(false);
  const [message, setMessage] = useState('');
  const [sending, setSending] = useState(false);
  const [output, setOutput] = useState<string[]>([]);
  const [reloading, setReloading] = useState(false);
  const [isConnected, setIsConnected] = useState(false);
  const [testSession, setTestSession] = useState<TestSession | null>(null);
  const [showFixReport, setShowFixReport] = useState(false);
  const testPollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const outputScrollRef = useRef<ScrollView>(null);

  // Resolve agent URL and token
  const config = YaverFeedback.getConfig();
  const agentUrl = agentUrlProp || config?.agentUrl;
  const authToken = authTokenProp || config?.authToken;
  const panelBg = panelBackgroundColor || config?.panelBackgroundColor || DEFAULT_PANEL_BG;

  const addOutput = useCallback((line: string) => {
    setOutput((prev) => [...prev.slice(-20), line]);
  }, []);

  // Connection health polling
  useEffect(() => {
    if (!healthCheckInterval || !agentUrl) return;

    const check = async () => {
      try {
        const client = YaverFeedback.getP2PClient();
        if (client) {
          setIsConnected(await client.health());
        } else if (agentUrl) {
          const controller = new AbortController();
          const timeout = setTimeout(() => controller.abort(), 3000);
          const resp = await fetch(`${agentUrl.replace(/\/$/, '')}/health`, {
            signal: controller.signal,
          });
          clearTimeout(timeout);
          setIsConnected(resp.ok);
        }
      } catch {
        setIsConnected(false);
      }
    };

    check();
    const interval = setInterval(check, healthCheckInterval);
    return () => clearInterval(interval);
  }, [agentUrl, healthCheckInterval]);

  const panResponder = useRef(
    PanResponder.create({
      onStartShouldSetPanResponder: () => true,
      onMoveShouldSetPanResponder: (_, gs) =>
        Math.abs(gs.dx) > 4 || Math.abs(gs.dy) > 4,
      onPanResponderGrant: () => {
        pan.extractOffset();
        isDragging.current = false;
      },
      onPanResponderMove: (_, gs) => {
        if (Math.abs(gs.dx) > 4 || Math.abs(gs.dy) > 4) isDragging.current = true;
        Animated.event([null, { dx: pan.x, dy: pan.y }], { useNativeDriver: false })(_, gs);
      },
      onPanResponderRelease: () => pan.flattenOffset(),
    }),
  ).current;

  const handleTap = useCallback(() => {
    if (isDragging.current) return;
    if (onPress) {
      onPress();
    } else {
      setChatOpen((prev) => !prev);
    }
  }, [onPress]);

  // Send message → create task → poll for response
  const handleSend = useCallback(async () => {
    if (!message.trim() || !agentUrl || !authToken) return;
    const msg = message.trim();
    setSending(true);
    setMessage('');
    Keyboard.dismiss();
    addOutput(`> ${msg}`);

    try {
      const url = agentUrl.replace(/\/$/, '');
      const resp = await fetch(`${url}/tasks`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${authToken}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: msg, source: 'feedback-console' }),
      });
      if (!resp.ok) {
        addOutput(`err: ${resp.status}`);
        setSending(false);
        return;
      }
      const data = await resp.json();
      const taskId = data.taskId ?? data.id ?? data.task?.id;
      if (!taskId) {
        addOutput('task created (no id)');
        setSending(false);
        return;
      }
      addOutput(`task ${taskId} started...`);
      BlackBox.log(`Console task: ${msg}`, 'FloatingButton');

      // Poll task output for up to 60s
      let attempts = 0;
      const poll = setInterval(async () => {
        attempts++;
        try {
          const sr = await fetch(`${url}/tasks/${taskId}`, {
            headers: { Authorization: `Bearer ${authToken}` },
          });
          if (!sr.ok) { clearInterval(poll); setSending(false); return; }
          const task = await sr.json();
          const t = task.task ?? task;

          if (t.status === 'completed' || t.status === 'failed' || t.status === 'stopped') {
            const out = t.output ?? t.rawOutput ?? '';
            if (out) {
              const lines = out.split('\n').filter((l: string) => l.trim());
              for (const l of lines.slice(-5)) addOutput(l.slice(0, 80));
            }
            addOutput(t.status === 'completed' ? 'done.' : `${t.status}.`);
            clearInterval(poll);
            setSending(false);
          } else if (attempts >= 30) {
            addOutput('running in background...');
            clearInterval(poll);
            setSending(false);
          }
        } catch { clearInterval(poll); setSending(false); }
      }, 2000);
    } catch (e) {
      addOutput(`fail: ${String(e).slice(0, 50)}`);
      setSending(false);
    }
  }, [message, agentUrl, authToken, addOutput]);

  // Generic action: send task to agent, poll for output
  const runAgentAction = useCallback(async (label: string, prompt: string) => {
    if (!agentUrl || !authToken) return;
    addOutput(`> ${label}`);
    setSending(true);
    try {
      const url = agentUrl.replace(/\/$/, '');
      const resp = await fetch(`${url}/tasks`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${authToken}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: prompt,
          source: 'feedback-sdk',
          description: `[Feedback SDK] User triggered "${label}" from the debug console.`,
        }),
      });
      if (!resp.ok) { addOutput(`err: ${resp.status}`); setSending(false); return; }
      const data = await resp.json();
      const taskId = data.taskId ?? data.id ?? data.task?.id;
      if (!taskId) { addOutput('started (no id)'); setSending(false); return; }
      addOutput(`${label}: task ${taskId}...`);
      BlackBox.log(`Action: ${label}`, 'FloatingButton');

      // Poll output
      let attempts = 0;
      const poll = setInterval(async () => {
        attempts++;
        try {
          const sr = await fetch(`${url}/tasks/${taskId}`, {
            headers: { Authorization: `Bearer ${authToken}` },
          });
          if (!sr.ok) { clearInterval(poll); setSending(false); return; }
          const task = await sr.json();
          const t = task.task ?? task;
          if (t.status === 'completed' || t.status === 'failed' || t.status === 'stopped') {
            const out = t.output ?? t.rawOutput ?? '';
            if (out) {
              for (const l of out.split('\n').filter((l: string) => l.trim()).slice(-5)) {
                addOutput(l.slice(0, 80));
              }
            }
            addOutput(t.status === 'completed' ? 'done.' : `${t.status}.`);
            clearInterval(poll); setSending(false);
          } else if (attempts >= 60) {
            addOutput('running in background...');
            clearInterval(poll); setSending(false);
          }
        } catch { clearInterval(poll); setSending(false); }
      }, 2000);
    } catch (e) {
      addOutput(`fail: ${String(e).slice(0, 50)}`);
      setSending(false);
    }
  }, [agentUrl, authToken, addOutput]);

  const handleReload = useCallback(() => {
    // @ts-ignore — __DEV__ is defined by React Native bundler
    const isDev = typeof __DEV__ !== 'undefined' ? __DEV__ : true;
    if (isDev) {
      runAgentAction('hot-reload', 'Hot reload the app. Send the reload signal to the dev server to trigger a fast refresh.');
    } else {
      runAgentAction(
        'rebuild',
        'This is a release build — hot reload is not available. ' +
        'Rebuild the app using native tools (xcodebuild for iOS, gradle for Android — no Expo) ' +
        'and upload to TestFlight/Play Store. Auto-increment build number. Report progress.',
      );
    }
  }, [runAgentAction]);

  // Build config from SDK settings
  const buildPlatforms = config?.buildPlatforms ?? 'both';
  const autoDeploy = config?.autoDeploy !== false; // default true

  const handleBuild = useCallback(() => {
    const platforms = buildPlatforms === 'both' ? ['ios', 'android'] : [buildPlatforms];
    const parts: string[] = [];
    for (const p of platforms) {
      if (p === 'ios') {
        parts.push(
          autoDeploy
            ? 'Build the iOS app, archive, and upload to TestFlight. Auto-increment the build number.'
            : 'Build the iOS app and archive it locally. Auto-increment the build number. Do NOT upload to TestFlight.',
        );
      } else if (p === 'android') {
        parts.push(
          autoDeploy
            ? 'Build the Android app (release AAB) and upload to Google Play internal testing. Auto-increment the versionCode.'
            : 'Build the Android app (release AAB) locally. Auto-increment the versionCode. Do NOT upload to Play Store.',
        );
      } else if (p === 'web') {
        parts.push('Build the web app for production.');
      }
    }
    const deployLabel = autoDeploy ? ' + deploy' : '';
    const platformLabel = buildPlatforms === 'both' ? 'iOS & Android' : buildPlatforms;
    runAgentAction(
      `build-${platformLabel}${deployLabel}`,
      parts.join(' Then, ') + ' Report progress and result.',
    );
  }, [runAgentAction, buildPlatforms, autoDeploy]);

  const handleBugReport = useCallback(async () => {
    if (!agentUrl || !authToken) return;
    addOutput('> bug report');
    setSending(true);
    try {
      // Try to capture screenshot (without SDK overlay)
      let screenshotUri: string | undefined;
      try {
        const { captureScreenshot } = require('./capture');
        screenshotUri = await captureScreenshot();
      } catch {
        // Screenshot capture not available — send text-only report
      }

      const url = agentUrl.replace(/\/$/, '');
      if (screenshotUri) {
        // Upload screenshot as feedback with bug flag
        const formData = new FormData();
        formData.append('metadata', JSON.stringify({
          timestamp: new Date().toISOString(),
          type: 'bug-report',
          source: 'feedback-sdk',
        }));
        formData.append('screenshot_0', {
          uri: screenshotUri,
          type: 'image/png',
          name: 'bug_screenshot.png',
        } as any);
        const resp = await fetch(`${url}/feedback`, {
          method: 'POST',
          headers: { Authorization: `Bearer ${authToken}` },
          body: formData,
        });
        if (resp.ok) {
          addOutput('screenshot captured & sent');
        } else {
          addOutput(`screenshot upload err: ${resp.status}`);
        }
      }

      // Also create a task so the agent investigates
      const resp = await fetch(`${url}/tasks`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${authToken}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: 'Bug report from device — investigate the attached screenshot and fix any visible issues.',
          source: 'feedback-sdk',
          description: '[Feedback SDK] User tapped the bug report button. A screenshot of the current screen was captured and sent. Investigate the UI state and fix any issues.',
          hasScreenshot: !!screenshotUri,
        }),
      });
      if (resp.ok) {
        const data = await resp.json();
        const taskId = data.taskId ?? data.id ?? data.task?.id;
        addOutput(taskId ? `bug task ${taskId} created` : 'bug report sent');
        BlackBox.log('Bug report submitted', 'FloatingButton');
      } else {
        addOutput(`err: ${resp.status}`);
      }
    } catch (e) {
      addOutput(`fail: ${String(e).slice(0, 50)}`);
    } finally {
      setSending(false);
    }
  }, [agentUrl, authToken, addOutput]);

  // Start autonomous test session
  const handleTestApp = useCallback(async () => {
    if (!agentUrl || !authToken) return;
    const isRunning = testSession?.active;

    if (isRunning) {
      // Stop test session
      addOutput('> stopping test...');
      try {
        await fetch(`${agentUrl.replace(/\/$/, '')}/test-app/stop`, {
          method: 'POST',
          headers: { Authorization: `Bearer ${authToken}` },
        });
        addOutput('test session stopped.');
        if (testPollRef.current) clearInterval(testPollRef.current);
        testPollRef.current = null;
        // Fetch final report
        try {
          const resp = await fetch(`${agentUrl.replace(/\/$/, '')}/test-app/status`, {
            headers: { Authorization: `Bearer ${authToken}` },
          });
          if (resp.ok) {
            const session: TestSession = await resp.json();
            setTestSession(session);
            if (session.fixes.length > 0) {
              addOutput(`${session.fixes.length} fixes applied (not committed).`);
              addOutput('tap "fixes" to view report.');
              setShowFixReport(true);
            }
          }
        } catch {}
      } catch (e) {
        addOutput(`fail: ${String(e).slice(0, 50)}`);
      }
      return;
    }

    // Start test session
    addOutput('> starting autonomous test...');
    setSending(true);
    try {
      const url = agentUrl.replace(/\/$/, '');
      const resp = await fetch(`${url}/test-app/start`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${authToken}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ source: 'feedback-sdk' }),
      });
      if (!resp.ok) {
        addOutput(`err: ${resp.status}`);
        setSending(false);
        return;
      }
      const data = await resp.json();
      addOutput(`test session ${data.sessionId ?? 'started'}...`);
      addOutput('agent reading codebase for context...');
      BlackBox.log('Test session started', 'FloatingButton');

      // Poll test status every 3s
      testPollRef.current = setInterval(async () => {
        try {
          const sr = await fetch(`${url}/test-app/status`, {
            headers: { Authorization: `Bearer ${authToken}` },
          });
          if (sr.ok) {
            const session: TestSession = await sr.json();
            setTestSession(session);
            if (!session.active && testPollRef.current) {
              clearInterval(testPollRef.current);
              testPollRef.current = null;
              addOutput(`test complete: ${session.errorsFound} errors, ${session.fixes.length} fixes.`);
              if (session.fixes.length > 0) {
                addOutput('tap "fixes" to view report.');
              }
              setSending(false);
            }
          }
        } catch {}
      }, 3000);
    } catch (e) {
      addOutput(`fail: ${String(e).slice(0, 50)}`);
      setSending(false);
    }
  }, [agentUrl, authToken, addOutput, testSession]);

  // Cleanup test poll on unmount
  useEffect(() => {
    return () => {
      if (testPollRef.current) clearInterval(testPollRef.current);
    };
  }, []);

  const handleDisable = useCallback(() => {
    YaverFeedback.setEnabled(false);
    setChatOpen(false);
  }, []);

  // Auto-scroll output
  useEffect(() => {
    if (outputScrollRef.current) {
      setTimeout(() => outputScrollRef.current?.scrollToEnd({ animated: true }), 50);
    }
  }, [output]);

  const isTerminal = stylePreset === 'terminal';
  const buttonIcon = icon ?? 'y';
  const btnBg = isConnected ? color : `${color}88`;

  const panelWidth = fullSize ? screenWidth - 24 : 280;

  return (
    <Animated.View
      style={[s.root, { transform: [{ translateX: pan.x }, { translateY: pan.y }] }]}
      {...panResponder.panHandlers}
    >
      {/* Console panel */}
      {chatOpen && (
        <View style={[
          s.panel,
          isTerminal ? s.panelTerminal : s.panelMinimal,
          { borderColor: `${color}44`, width: panelWidth, backgroundColor: panelBg },
          fullSize && { position: 'absolute', left: 0, top: size + 8 },
        ]}>
          {/* Header */}
          <View style={s.headerRow}>
            <Text style={[s.headerTitle, isTerminal && s.mono, { color }]}>
              {isTerminal ? 'YAVER DEBUG' : 'Yaver'}
            </Text>
            <View style={[s.dotSmall, isConnected ? s.green : s.red]} />
            <Text style={[s.headerStatus, isTerminal && s.mono]}>
              {isConnected ? 'live' : 'off'}
            </Text>
            <TouchableOpacity onPress={() => setFullSize(!fullSize)} style={s.xBtn}>
              <Text style={s.xBtnText}>{fullSize ? '\u25A1' : '\u2197'}</Text>
            </TouchableOpacity>
            <TouchableOpacity onPress={() => { setChatOpen(false); setFullSize(false); }} style={s.xBtn}>
              <Text style={s.xBtnText}>{'\u2715'}</Text>
            </TouchableOpacity>
          </View>

          {/* Output area */}
          <ScrollView
            ref={outputScrollRef}
            style={[s.outputArea, fullSize && s.outputAreaFull]}
            contentContainerStyle={s.outputContent}
          >
            {output.length > 0 ? output.map((line, i) => (
              <Text
                key={i}
                style={[
                  s.outputLine,
                  isTerminal && s.mono,
                  fullSize && s.outputLineFull,
                  line.startsWith('>')
                    ? s.outputLineUser
                    : line === 'done.' || line.startsWith('task ')
                      ? s.outputLineStatus
                      : s.outputLineAgent,
                ]}
              >
                {line}
              </Text>
            )) : (
              <Text style={[s.outputLine, isTerminal && s.mono, { color: '#666' }]}>
                {isConnected ? 'connected. type a message or use actions below.' : 'not connected to agent.'}
              </Text>
            )}
            {sending && <ActivityIndicator color={color} size="small" style={{ marginTop: 4 }} />}
          </ScrollView>

          {/* Input */}
          <View style={s.inputRow}>
            {isTerminal && <Text style={[s.prompt, { color }]}>&gt;</Text>}
            <TextInput
              style={[s.input, isTerminal && s.mono, fullSize && s.inputFull]}
              placeholder={isTerminal ? 'tell the agent...' : 'Type a message...'}
              placeholderTextColor="#444"
              value={message}
              onChangeText={setMessage}
              onSubmitEditing={handleSend}
              returnKeyType="send"
              multiline={fullSize}
            />
            <TouchableOpacity
              style={[s.goBtn, { backgroundColor: color }, (sending || !message.trim()) && s.dim]}
              onPress={handleSend}
              disabled={sending || !message.trim() || !isConnected}
            >
              {sending ? (
                <ActivityIndicator color="#fff" size="small" />
              ) : (
                <Text style={[s.goBtnText, isTerminal && s.mono]}>
                  {isTerminal ? 'run' : 'Send'}
                </Text>
              )}
            </TouchableOpacity>
          </View>

          {/* Action cards row 1 — Reload | Build | Bug */}
          <View style={s.cardRow}>
            <TouchableOpacity
              style={[s.card, fullSize && s.cardFull, !isConnected && s.dim]}
              onPress={handleReload}
              disabled={sending || !isConnected}
            >
              <Text style={[s.cardIcon, { color: '#fbbf24' }]}>{'\u21BB'}</Text>
              <Text style={[s.cardLabel, isTerminal && s.mono]}>Hot Reload</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={[s.card, fullSize && s.cardFull, !isConnected && s.dim]}
              onPress={handleBuild}
              disabled={sending || !isConnected}
            >
              <Text style={[s.cardIcon, { color: '#60a5fa' }]}>{'\u2692'}</Text>
              <Text style={[s.cardLabel, isTerminal && s.mono]}>
                {buildPlatforms === 'both' ? 'Build' : `Build ${buildPlatforms}`}
              </Text>
              {autoDeploy && (
                <Text style={[s.cardSub, isTerminal && s.mono]}>
                  {buildPlatforms === 'ios' ? '+ TestFlight'
                    : buildPlatforms === 'android' ? '+ Play Store'
                    : buildPlatforms === 'both' ? '+ Deploy'
                    : ''}
                </Text>
              )}
            </TouchableOpacity>
            <TouchableOpacity
              style={[s.card, fullSize && s.cardFull, !isConnected && s.dim]}
              onPress={handleBugReport}
              disabled={sending || !isConnected}
            >
              <Text style={[s.cardIcon, { color: '#f87171' }]}>{'\u{1F41B}'}</Text>
              <Text style={[s.cardLabel, isTerminal && s.mono]}>Report Bug</Text>
            </TouchableOpacity>
          </View>

          {/* Action cards row 2 — Test | Fixes */}
          <View style={[s.cardRow, { marginTop: -2 }]}>
            <TouchableOpacity
              style={[s.card, fullSize && s.cardFull, !isConnected && s.dim,
                testSession?.active && { borderColor: '#a78bfa44', backgroundColor: '#a78bfa08' }]}
              onPress={handleTestApp}
              disabled={!isConnected}
            >
              <Text style={[s.cardIcon, { color: '#a78bfa' }]}>
                {testSession?.active ? '\u23F8' : '\u25B6'}
              </Text>
              <Text style={[s.cardLabel, isTerminal && s.mono]}>
                {testSession?.active ? 'Stop Test' : 'Test App'}
              </Text>
              {testSession?.active && (
                <Text style={[s.cardSub, isTerminal && s.mono]}>
                  {testSession.screensTested}/{testSession.screensDiscovered}
                </Text>
              )}
            </TouchableOpacity>
            {testSession && testSession.fixes.length > 0 && (
              <TouchableOpacity
                style={[s.card, fullSize && s.cardFull]}
                onPress={() => setShowFixReport(true)}
              >
                <Text style={[s.cardIcon, { color: '#22c55e' }]}>{'\u2713'}</Text>
                <Text style={[s.cardLabel, isTerminal && s.mono]}>
                  {testSession.fixes.length} Fixes
                </Text>
              </TouchableOpacity>
            )}
          </View>

          {/* Bottom row */}
          <View style={s.actionsRow}>
            <TouchableOpacity style={s.actionBtn} onPress={() => setOutput([])}>
              <Text style={[s.actionText, isTerminal && s.mono]}>clear</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={s.actionBtn}
              onPress={() => {
                setChatOpen(false);
                YaverFeedback.startReport();
              }}
            >
              <Text style={[s.actionText, isTerminal && s.mono]}>report</Text>
            </TouchableOpacity>
            <TouchableOpacity style={s.actionBtn} onPress={handleDisable}>
              <Text style={[s.actionText, isTerminal && s.mono, { color: '#f87171' }]}>quit</Text>
            </TouchableOpacity>
          </View>
        </View>
      )}

      {/* Fix Report Modal */}
      <FixReport
        session={testSession}
        visible={showFixReport}
        onClose={() => setShowFixReport(false)}
        color={color}
      />

      {/* The button */}
      <TouchableOpacity
        style={[
          s.button,
          isTerminal ? s.buttonTerminal : s.buttonMinimal,
          { backgroundColor: btnBg, width: size, height: size },
          !isTerminal && { borderRadius: size / 2 },
        ]}
        activeOpacity={0.7}
        onPress={handleTap}
      >
        <Text style={[s.buttonIcon, isTerminal && s.mono, { fontSize: 22 }]}>
          {chatOpen ? '\u2715' : buttonIcon}
        </Text>
        {showStatusDot && (
          <View style={[s.statusDot, isConnected ? s.green : s.red]} />
        )}
      </TouchableOpacity>
    </Animated.View>
  );
};

const s = StyleSheet.create({
  root: { position: 'absolute', zIndex: 99999, alignItems: 'flex-start' },
  mono: { fontFamily: 'Courier' },
  // Button variants
  button: {
    alignItems: 'center',
    justifyContent: 'center',
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 3 },
    shadowOpacity: 0.5,
    shadowRadius: 5,
    elevation: 10,
  },
  buttonTerminal: { borderRadius: 10 },
  buttonMinimal: { /* borderRadius set inline */ },
  buttonIcon: { color: '#fff', fontWeight: '800', fontStyle: 'italic' as const },
  statusDot: {
    position: 'absolute',
    top: -2,
    right: -2,
    width: 9,
    height: 9,
    borderRadius: 5,
    borderWidth: 1.5,
    borderColor: '#000',
  },
  green: { backgroundColor: '#22c55e' },
  red: { backgroundColor: '#ef4444' },
  // Panel
  panel: {
    padding: 10,
    marginBottom: 6,
    borderWidth: 1,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.5,
    shadowRadius: 8,
    elevation: 12,
  },
  panelTerminal: {
    borderRadius: 12,
  },
  panelMinimal: {
    borderRadius: 16,
  },
  // Header
  headerRow: { flexDirection: 'row', alignItems: 'center', marginBottom: 6, gap: 5 },
  headerTitle: { flex: 1, fontSize: 11, fontWeight: '700', textTransform: 'uppercase', letterSpacing: 1 },
  dotSmall: { width: 6, height: 6, borderRadius: 3 },
  headerStatus: { fontSize: 10, color: '#666' },
  xBtn: { paddingHorizontal: 6, paddingVertical: 2 },
  xBtnText: { color: '#666', fontSize: 12 },
  // Output
  outputArea: {
    backgroundColor: '#1a1a1a',
    borderRadius: 8,
    padding: 8,
    marginBottom: 6,
    maxHeight: 140,
  },
  outputAreaFull: { maxHeight: 300, minHeight: 160 },
  outputContent: { paddingBottom: 4 },
  outputLine: {
    fontSize: 11,
    lineHeight: 16,
  },
  outputLineUser: {
    color: '#9ca3af',
    fontStyle: 'italic' as const,
  },
  outputLineAgent: {
    color: '#e5e5e5',
  },
  outputLineStatus: {
    color: '#22c55e',
  },
  outputLineFull: { fontSize: 13, lineHeight: 20 },
  // Input
  inputRow: { flexDirection: 'row', alignItems: 'center', gap: 4, marginBottom: 6 },
  prompt: { fontSize: 14, fontWeight: '700' },
  input: {
    flex: 1,
    backgroundColor: '#1a1a1a',
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 6,
    color: '#e5e5e5',
    fontSize: 12,
    borderWidth: 1,
    borderColor: '#3a3a3a',
  },
  inputFull: { fontSize: 15, paddingVertical: 10 },
  goBtn: { borderRadius: 6, paddingHorizontal: 10, paddingVertical: 6 },
  goBtnText: { color: '#fff', fontSize: 11, fontWeight: '700' },
  dim: { opacity: 0.3 },
  // Action cards
  cardRow: { flexDirection: 'row', gap: 6, marginBottom: 6 },
  card: {
    flex: 1,
    backgroundColor: '#1a1a1a',
    borderRadius: 8,
    paddingVertical: 10,
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#3a3a3a',
  },
  cardFull: { paddingVertical: 14 },
  cardIcon: { fontSize: 18, marginBottom: 2 },
  cardLabel: { fontSize: 10, color: '#999', fontWeight: '600' },
  cardSub: { fontSize: 8, color: '#555', marginTop: 1 },
  // Bottom actions
  actionsRow: { flexDirection: 'row', gap: 4 },
  actionBtn: {
    flex: 1,
    paddingVertical: 5,
    borderRadius: 6,
    alignItems: 'center',
    backgroundColor: '#1a1a1a',
    borderWidth: 1,
    borderColor: '#3a3a3a',
  },
  actionText: { fontSize: 10, color: '#999', fontWeight: '600' },
});
