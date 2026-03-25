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
import type { TestSession, TodoItemSummary } from './types';
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
}

const DEFAULT_SIZE = 40;
const DEFAULT_COLOR = '#6366f1';

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
  const [todoCount, setTodoCount] = useState(0);
  const [todoItems, setTodoItems] = useState<TodoItemSummary[]>([]);
  const [showTodoList, setShowTodoList] = useState(false);
  const [projectName, setProjectName] = useState<string>(config?.projectName || '');
  const [todoDone, setTodoDone] = useState(0);
  const [todoTotal, setTodoTotal] = useState(0);
  const [hasDevServer, setHasDevServer] = useState(false);
  const testPollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const outputScrollRef = useRef<ScrollView>(null);

  // Resolve agent URL and token
  const config = YaverFeedback.getConfig();
  const agentUrl = agentUrlProp || config?.agentUrl;
  const authToken = authTokenProp || config?.authToken;

  const addOutput = useCallback((line: string) => {
    setOutput((prev) => [...prev.slice(-20), line]);
  }, []);

  // Connection health + todo count polling
  useEffect(() => {
    if (!healthCheckInterval || !agentUrl) return;

    const check = async () => {
      try {
        const client = YaverFeedback.getP2PClient();
        if (client) {
          const healthy = await client.health();
          setIsConnected(healthy);
          if (healthy) {
            try {
              const info = await client.agentInfo();
              setTodoCount(info.todoCount ?? 0);
              setTodoDone(info.todoDone ?? 0);
              setTodoTotal(info.todoTotal ?? 0);
              setProjectName(info.project?.name ?? '');
              setHasDevServer(info.devServer?.running ?? false);
            } catch {
              const count = await client.todoCount();
              setTodoCount(count);
            }
          }
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

  // Send message → auto-classify → queue as todo or execute as action
  const handleSend = useCallback(async () => {
    if (!message.trim() || !agentUrl || !authToken) return;
    const msg = message.trim();
    setSending(true);
    setMessage('');
    Keyboard.dismiss();
    addOutput(`> ${msg}`);

    try {
      const client = YaverFeedback.getP2PClient();
      if (client) {
        // Use smart chat — auto-classifies and acts
        // Include project context so agent knows what app we're testing
        const projectCtx = config?.projectContext;
        const fullMsg = projectCtx ? `[${config?.projectName || 'app'}] ${msg}` : msg;
        const result = await client.smartChat(fullMsg, {
          platform: require('react-native').Platform.OS,
          model: require('react-native').Platform.OS === 'ios' ? 'iOS Device' : 'Android Device',
          osVersion: String(require('react-native').Platform.Version),
        });

        if (result.intent === 'todo') {
          addOutput(`\u{1F4CB} queued: ${msg.slice(0, 50)}${msg.length > 50 ? '...' : ''}`);
          setTodoCount(result.todoCount ?? todoCount + 1);
          BlackBox.log(`Auto-queued: ${msg}`, 'FloatingButton');
          setSending(false);
          return;
        }

        if (result.intent === 'continuation' && result.todoItemId) {
          addOutput(`\u{1F4CB} added to ${result.todoItemId}`);
          BlackBox.log(`Continuation: ${msg}`, 'FloatingButton');
          setSending(false);
          return;
        }

        // Action — execute immediately
        if (result.taskId) {
          addOutput(`\u26A1 task ${result.taskId} started...`);
          BlackBox.log(`Action: ${msg}`, 'FloatingButton');

          // Poll task output
          const url = agentUrl.replace(/\/$/, '');
          let attempts = 0;
          const poll = setInterval(async () => {
            attempts++;
            try {
              const sr = await fetch(`${url}/tasks/${result.taskId}`, {
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
                addOutput(t.status === 'completed' ? '\u2713 done.' : `${t.status}.`);
                clearInterval(poll);
                setSending(false);
              } else if (attempts >= 30) {
                addOutput('running in background...');
                clearInterval(poll);
                setSending(false);
              }
            } catch { clearInterval(poll); setSending(false); }
          }, 2000);
          return;
        }
      }

      // Fallback: direct task creation
      const url = agentUrl.replace(/\/$/, '');
      const resp = await fetch(`${url}/tasks`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${authToken}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: msg, source: 'feedback-console' }),
      });
      if (!resp.ok) { addOutput(`err: ${resp.status}`); setSending(false); return; }
      const data = await resp.json();
      const taskId = data.taskId ?? data.id;
      addOutput(taskId ? `task ${taskId} started...` : 'sent');
      setSending(false);
    } catch (e) {
      addOutput(`fail: ${String(e).slice(0, 50)}`);
      setSending(false);
    }
  }, [message, agentUrl, authToken, addOutput, todoCount]);

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

  // Todo list handlers
  const handleOpenTodoList = useCallback(async () => {
    const client = YaverFeedback.getP2PClient();
    if (!client) return;
    try {
      const items = await client.listTodoItems();
      setTodoItems(items);
      setShowTodoList(true);
    } catch (e) {
      addOutput(`todo list err: ${String(e).slice(0, 50)}`);
    }
  }, [addOutput]);

  const handleRemoveTodoItem = useCallback(async (id: string) => {
    const client = YaverFeedback.getP2PClient();
    if (!client) return;
    try {
      await client.removeTodoItem(id);
      setTodoItems(prev => prev.filter(i => i.id !== id));
      setTodoCount(prev => Math.max(0, prev - 1));
    } catch {}
  }, []);

  const handleImplementAll = useCallback(async () => {
    const client = YaverFeedback.getP2PClient();
    if (!client) return;
    setSending(true);
    addOutput('> implementing all queued items...');
    try {
      const result = await client.implementAllTodos();
      addOutput(`batch task ${result.taskId} started (${result.itemCount} items)`);
      setShowTodoList(false);
      BlackBox.log(`Implement all: ${result.itemCount} items`, 'FloatingButton');
    } catch (e) {
      addOutput(`implement err: ${String(e).slice(0, 50)}`);
    } finally {
      setSending(false);
    }
  }, [addOutput]);

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
          { borderColor: `${color}44`, width: panelWidth },
          fullSize && { position: 'absolute', left: 0, top: size + 8 },
        ]}>
          {/* Header */}
          <View style={s.headerRow}>
            <Text style={[s.headerTitle, isTerminal && s.mono, { color }]}>
              {isTerminal ? 'YAVER DEBUG' : 'Yaver'}
            </Text>
            {projectName ? (
              <View style={s.projectChip}>
                <Text style={s.projectChipText}>{projectName}</Text>
              </View>
            ) : null}
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
                  line.startsWith('>') && { color: '#9ca3af' },
                ]}
              >
                {line}
              </Text>
            )) : (
              <Text style={[s.outputLine, isTerminal && s.mono, { color: '#333' }]}>
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

          {/* Action cards row 3 — Todo Queue + Hot Reload */}
          {(todoTotal > 0 || hasDevServer) && (
            <View style={[s.cardRow, { marginTop: -2 }]}>
              {todoTotal > 0 && (
                <TouchableOpacity
                  style={[s.card, fullSize && s.cardFull, { borderColor: '#f59e0b44', backgroundColor: '#f59e0b08' }]}
                  onPress={handleOpenTodoList}
                  disabled={!isConnected}
                >
                  <Text style={[s.cardIcon, { color: '#f59e0b' }]}>
                    {'\u{1F4CB}'}
                  </Text>
                  <Text style={[s.cardLabel, isTerminal && s.mono]}>
                    Todo {todoDone}/{todoTotal}
                  </Text>
                  {todoCount > 0 && (
                    <Text style={[s.cardSub, isTerminal && s.mono, { color: '#f59e0b' }]}>
                      {todoCount} pending
                    </Text>
                  )}
                </TouchableOpacity>
              )}
              {hasDevServer && (
                <TouchableOpacity
                  style={[s.card, fullSize && s.cardFull, { borderColor: '#22c55e44', backgroundColor: '#22c55e08' }]}
                  onPress={handleReload}
                  disabled={sending || !isConnected}
                >
                  <Text style={[s.cardIcon, { color: '#22c55e' }]}>{'\u21BB'}</Text>
                  <Text style={[s.cardLabel, isTerminal && s.mono]}>Reload</Text>
                </TouchableOpacity>
              )}
            </View>
          )}

          {/* Inline Todo List */}
          {showTodoList && (
            <View style={[s.todoListContainer, fullSize && { maxHeight: 240 }]}>
              <View style={s.todoHeader}>
                <Text style={[s.todoTitle, isTerminal && s.mono]}>
                  Todo Queue ({todoItems.length})
                </Text>
                <TouchableOpacity onPress={() => setShowTodoList(false)}>
                  <Text style={s.xBtnText}>{'\u2715'}</Text>
                </TouchableOpacity>
              </View>
              <ScrollView style={s.todoScroll}>
                {todoItems.map((item) => (
                  <View key={item.id} style={s.todoItem}>
                    <View style={[
                      s.todoStatusDot,
                      item.status === 'pending' && { backgroundColor: '#f59e0b' },
                      item.status === 'implementing' && { backgroundColor: '#3b82f6' },
                      item.status === 'done' && { backgroundColor: '#22c55e' },
                      item.status === 'failed' && { backgroundColor: '#ef4444' },
                    ]} />
                    <View style={s.todoTextContainer}>
                      <Text style={[s.todoDesc, isTerminal && s.mono]} numberOfLines={2}>
                        {item.description}
                      </Text>
                      <Text style={[s.todoMeta, isTerminal && s.mono]}>
                        {item.status}{item.taskId ? ` \u2022 task ${item.taskId}` : ''}
                      </Text>
                    </View>
                    {item.status === 'pending' && (
                      <TouchableOpacity onPress={() => handleRemoveTodoItem(item.id)} style={s.todoRemoveBtn}>
                        <Text style={{ color: '#ef4444', fontSize: 12 }}>{'\u2715'}</Text>
                      </TouchableOpacity>
                    )}
                    {item.taskId && (
                      <TouchableOpacity
                        onPress={async () => {
                          const client = YaverFeedback.getP2PClient();
                          if (!client || !item.taskId) return;
                          try {
                            const { output: taskOutput, status } = await client.getTaskOutput(item.taskId);
                            const lines = taskOutput.split('\n').filter((l: string) => l.trim()).slice(-5);
                            addOutput(`--- ${item.id} (${status}) ---`);
                            for (const l of lines) addOutput(l.slice(0, 80));
                          } catch {}
                        }}
                        style={s.todoLogBtn}
                      >
                        <Text style={{ color: '#60a5fa', fontSize: 10 }}>logs</Text>
                      </TouchableOpacity>
                    )}
                  </View>
                ))}
              </ScrollView>
              {todoItems.some(i => i.status === 'pending') && (
                <TouchableOpacity
                  style={[s.implementAllBtn, { backgroundColor: color }]}
                  onPress={handleImplementAll}
                  disabled={sending}
                >
                  <Text style={s.implementAllText}>
                    Implement All ({todoItems.filter(i => i.status === 'pending').length})
                  </Text>
                </TouchableOpacity>
              )}
            </View>
          )}

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
        {todoCount > 0 && !chatOpen && (
          <View style={s.todoBadge}>
            <Text style={s.todoBadgeText}>{todoCount > 9 ? '9+' : todoCount}</Text>
          </View>
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
    backgroundColor: '#0a0a0a',
    borderRadius: 12,
  },
  panelMinimal: {
    backgroundColor: '#1a1a2e',
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
    backgroundColor: '#111',
    borderRadius: 8,
    padding: 8,
    marginBottom: 6,
    maxHeight: 140,
  },
  outputAreaFull: { maxHeight: 300, minHeight: 160 },
  outputContent: { paddingBottom: 4 },
  outputLine: {
    fontSize: 11,
    color: '#22c55e',
    lineHeight: 16,
  },
  outputLineFull: { fontSize: 13, lineHeight: 20 },
  // Input
  inputRow: { flexDirection: 'row', alignItems: 'center', gap: 4, marginBottom: 6 },
  prompt: { fontSize: 14, fontWeight: '700' },
  input: {
    flex: 1,
    backgroundColor: '#111',
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 6,
    color: '#e5e5e5',
    fontSize: 12,
    borderWidth: 1,
    borderColor: '#222',
  },
  inputFull: { fontSize: 15, paddingVertical: 10 },
  goBtn: { borderRadius: 6, paddingHorizontal: 10, paddingVertical: 6 },
  goBtnText: { color: '#fff', fontSize: 11, fontWeight: '700' },
  dim: { opacity: 0.3 },
  // Action cards
  cardRow: { flexDirection: 'row', gap: 6, marginBottom: 6 },
  card: {
    flex: 1,
    backgroundColor: '#111',
    borderRadius: 8,
    paddingVertical: 10,
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#1a1a1a',
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
    backgroundColor: '#111',
    borderWidth: 1,
    borderColor: '#1a1a1a',
  },
  actionText: { fontSize: 10, color: '#888', fontWeight: '600' },
  // Todo badge
  todoBadge: {
    position: 'absolute',
    top: -6,
    left: -6,
    minWidth: 18,
    height: 18,
    borderRadius: 9,
    backgroundColor: '#f59e0b',
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 4,
    borderWidth: 1.5,
    borderColor: '#000',
  },
  todoBadgeText: { color: '#000', fontSize: 10, fontWeight: '800' },
  // Project chip
  projectChip: {
    backgroundColor: '#6366f122',
    borderRadius: 4,
    paddingHorizontal: 5,
    paddingVertical: 1,
    marginRight: 4,
  },
  projectChipText: { color: '#818cf8', fontSize: 9, fontWeight: '600' },
  // Todo list
  todoListContainer: {
    backgroundColor: '#111',
    borderRadius: 8,
    padding: 8,
    marginBottom: 6,
    maxHeight: 180,
    borderWidth: 1,
    borderColor: '#f59e0b33',
  },
  todoHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: 6,
  },
  todoTitle: { color: '#f59e0b', fontSize: 11, fontWeight: '700' },
  todoScroll: { maxHeight: 120 },
  todoItem: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingVertical: 4,
    borderBottomWidth: 0.5,
    borderBottomColor: '#1a1a1a',
    gap: 6,
  },
  todoStatusDot: { width: 8, height: 8, borderRadius: 4 },
  todoTextContainer: { flex: 1 },
  todoDesc: { color: '#ccc', fontSize: 11, lineHeight: 15 },
  todoMeta: { color: '#555', fontSize: 9, marginTop: 1 },
  todoRemoveBtn: { padding: 4 },
  todoLogBtn: { padding: 4, marginLeft: 2 },
  implementAllBtn: {
    marginTop: 6,
    borderRadius: 6,
    paddingVertical: 8,
    alignItems: 'center',
  },
  implementAllText: { color: '#fff', fontSize: 12, fontWeight: '700' },
});
