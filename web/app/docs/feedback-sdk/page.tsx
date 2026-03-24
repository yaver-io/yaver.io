"use client";

import Link from "next/link";

function Terminal({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="terminal">
      <div className="terminal-header">
        <div className="terminal-dot bg-[#ff5f57]" />
        <div className="terminal-dot bg-[#febc2e]" />
        <div className="terminal-dot bg-[#28c840]" />
        <span className="ml-3 text-xs text-surface-500">{title}</span>
      </div>
      <div className="terminal-body space-y-2 text-[13px]">{children}</div>
    </div>
  );
}

function Cmd({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <span className="text-surface-400">$</span>{" "}
      <span className="text-surface-200 select-all">{children}</span>
    </div>
  );
}

function Comment({ children }: { children: React.ReactNode }) {
  return <div className="text-surface-500">{children}</div>;
}

function Output({ children }: { children: React.ReactNode }) {
  return <div className="text-green-400/80 pl-2">{children}</div>;
}

function Divider() {
  return <div className="h-px bg-surface-800/60" />;
}

function SectionHeading({
  id,
  children,
}: {
  id: string;
  children: React.ReactNode;
}) {
  return (
    <h2
      id={id}
      className="mb-4 text-2xl font-bold text-surface-50 md:text-3xl"
    >
      {children}
    </h2>
  );
}

function SubHeading({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="mb-3 text-lg font-semibold text-surface-100">{children}</h3>
  );
}

function Prose({ children }: { children: React.ReactNode }) {
  return (
    <p className="mb-6 text-sm leading-relaxed text-surface-400">{children}</p>
  );
}

function InlineCode({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-surface-900 px-1.5 py-0.5 text-xs text-surface-300">
      {children}
    </code>
  );
}

export default function FeedbackSdkPage() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        {/* Back link */}
        <Link
          href="/"
          className="mb-12 inline-block text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to home
        </Link>

        {/* Header */}
        <div className="mb-16">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            Feedback SDK
          </h1>
          <p className="text-sm leading-relaxed text-surface-400">
            Error capture, black box streaming, screen recording, voice, and
            screenshots &mdash; all sent directly to the AI agent on your dev
            machine. The agent sees what broke, reads the flight recorder logs,
            and fixes bugs automatically.
          </p>
        </div>

        {/* Table of contents */}
        <div className="mb-16 rounded-xl border border-surface-800 bg-surface-900 p-6">
          <h3 className="mb-4 text-sm font-semibold text-surface-200">
            On this page
          </h3>
          <nav className="space-y-2 text-sm">
            {[
              ["overview", "Overview"],
              ["installation", "Installation"],
              ["quick-start", "Quick Start"],
              ["error-capture", "Error Capture"],
              ["black-box", "Black Box Streaming"],
              ["hot-reload", "Hot Reload Button"],
              ["agent-integration", "Agent Integration"],
              ["api-reference", "API Reference"],
            ].map(([id, label]) => (
              <a
                key={id}
                href={`#${id}`}
                className="block text-surface-500 hover:text-surface-200"
              >
                {label}
              </a>
            ))}
          </nav>
        </div>

        {/* ─── Overview ─── */}
        <section className="mb-20">
          <SectionHeading id="overview">Overview</SectionHeading>
          <Prose>
            The Feedback SDK gives your mobile or web app a direct channel back
            to the AI coding agent running on your dev machine. When your app
            crashes, renders incorrectly, or behaves unexpectedly, the SDK
            captures everything the agent needs to understand and fix the
            problem &mdash; without you typing a single bug report.
          </Prose>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Screen Recording
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Capture a video of what happened. The agent sees the exact UI
                state, transitions, and visual glitches leading up to the bug.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Voice
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Tap the mic and describe the problem. Audio is transcribed and
                included as context in the fix task sent to the agent.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Screenshots
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Annotated screenshots are attached to the feedback. The agent
                receives them as base64 images alongside the error context.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Error Capture
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Observe-only error capture that never hijacks your global error
                handlers. Works alongside Sentry, Crashlytics, Bugsnag, or any
                other crash reporting tool.
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Black Box Streaming
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                A flight recorder that continuously streams logs, navigation
                events, network requests, state changes, and lifecycle events to
                the agent. When a crash happens, the agent has full context of
                everything that led up to it.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Installation ─── */}
        <section className="mb-20">
          <SectionHeading id="installation">Installation</SectionHeading>
          <Prose>
            The SDK is available for React Native, Flutter, and Web. Install the
            package for your platform.
          </Prose>

          <SubHeading>React Native</SubHeading>
          <div className="mb-8">
            <Terminal title="install-rn">
              <Cmd>npm install @yaver/feedback-react-native</Cmd>
              <Comment># or</Comment>
              <Cmd>yarn add @yaver/feedback-react-native</Cmd>
            </Terminal>
          </div>

          <SubHeading>Flutter</SubHeading>
          <div className="mb-8">
            <Terminal title="install-flutter">
              <Cmd>flutter pub add yaver_feedback</Cmd>
            </Terminal>
          </div>

          <SubHeading>Web</SubHeading>
          <div className="mb-8">
            <Terminal title="install-web">
              <Cmd>npm install @yaver/feedback-web</Cmd>
              <Comment># or</Comment>
              <Cmd>yarn add @yaver/feedback-web</Cmd>
            </Terminal>
          </div>
        </section>

        {/* ─── Quick Start ─── */}
        <section className="mb-20">
          <SectionHeading id="quick-start">Quick Start</SectionHeading>
          <Prose>
            Initialize the SDK with your agent&apos;s address. Gate it behind
            your developer user ID so only your team sees the debug console.
            The SDK auto-discovers the agent via LAN beacon if no host is
            specified.
          </Prose>

          <SubHeading>React Native</SubHeading>
          <div className="mb-8">
            <Terminal title="quick-start-rn.tsx">
              <pre className="text-surface-300">
                {`import { YaverFeedback, FloatingButton, BlackBox } from '@yaver/feedback-react-native';
import { useAuth } from './auth';

function App() {
  const { user } = useAuth();

  // Only show for your team — gate by user ID or email
  const isDev = __DEV__ && user?.id === 'YOUR_USER_ID';
  // or: user?.email?.endsWith('@yourcompany.com')

  if (isDev && !YaverFeedback.isInitialized()) {
    YaverFeedback.init({ trigger: 'floating-button' });
    BlackBox.start();
    BlackBox.wrapConsole();
  }

  return (
    <>
      <YourApp />
      {isDev && <FloatingButton />}
      {/* Debug console includes:
          - Message back-and-forth with AI agent
          - Hot Reload button
          - Build iOS → auto-submit to TestFlight
          - Build Android → auto-submit to Play Store */}
    </>
  );
}`}
              </pre>
            </Terminal>
          </div>

          <SubHeading>Flutter</SubHeading>
          <div className="mb-8">
            <Terminal title="quick-start-flutter.dart">
              <pre className="text-surface-300">
                {`import 'package:yaver_feedback/yaver_feedback.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();

  // Gate by developer user ID
  final isDev = kDebugMode && currentUser.id == 'YOUR_USER_ID';

  if (isDev) {
    YaverFeedback.init(FeedbackConfig(
      trigger: FeedbackTrigger.floatingButton,
      mode: FeedbackMode.live,
    ));
    BlackBox.start();
    FlutterError.onError = YaverFeedback.wrapFlutterErrorHandler(
      FlutterError.onError,
    );
  }

  runApp(const MyApp());
}`}
              </pre>
            </Terminal>
          </div>

          <SubHeading>Web</SubHeading>
          <div className="mb-8">
            <Terminal title="quick-start-web.ts">
              <pre className="text-surface-300">
                {`import { YaverFeedback } from '@yaver/feedback-web';

// Only show for the developer
const isDev = process.env.NODE_ENV === 'development'
  && currentUser?.id === 'YOUR_USER_ID';

if (isDev) {
  YaverFeedback.init({ trigger: 'floating-button' });
  // Floating button: message agent, hot reload, build + deploy
}`}
              </pre>
            </Terminal>
          </div>
        </section>

        {/* ─── Error Capture ─── */}
        <section className="mb-20">
          <SectionHeading id="error-capture">Error Capture</SectionHeading>
          <Prose>
            The SDK uses an observe-only design. It never replaces or hijacks
            your global error handlers. Your existing crash reporters (Sentry,
            Crashlytics, Bugsnag) continue to work exactly as before. The SDK
            either wraps the existing handler chain as a pass-through or accepts
            manual error reports from your catch blocks.
          </Prose>

          <SubHeading>Option 1: wrapErrorHandler</SubHeading>
          <Prose>
            Wrap your existing error handler. The SDK observes the error, then
            passes it through to the original handler unchanged. The error
            propagation chain is never interrupted.
          </Prose>

          <div className="mb-8">
            <Terminal title="error-wrap-rn.tsx">
              <Comment># React Native &mdash; wrapping ErrorUtils</Comment>
              <pre className="text-surface-300">
                {`import { wrapErrorHandler } from '@yaver/feedback-react-native';

// Get the existing global handler
const originalHandler = ErrorUtils.getGlobalHandler();

// Wrap it — errors still reach the original handler
ErrorUtils.setGlobalHandler(
  wrapErrorHandler(originalHandler)
);

// Sentry, Crashlytics, etc. still work.
// The SDK just observes; it never swallows errors.`}
              </pre>
            </Terminal>
          </div>

          <div className="mb-8">
            <Terminal title="error-wrap-flutter.dart">
              <Comment># Flutter &mdash; wrapping FlutterError.onError</Comment>
              <pre className="text-surface-300">
                {`import 'package:yaver_feedback/yaver_feedback.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();

  // Wrap FlutterError.onError (framework errors)
  final originalOnError = FlutterError.onError;
  FlutterError.onError = wrapErrorHandler(originalOnError);

  // Wrap PlatformDispatcher (async errors)
  final originalDispatcher =
      PlatformDispatcher.instance.onError;
  PlatformDispatcher.instance.onError =
      wrapPlatformErrorHandler(originalDispatcher);

  YaverFeedback.init(errorCapture: true);
  runApp(const MyApp());
}`}
              </pre>
            </Terminal>
          </div>

          <div className="mb-8">
            <Terminal title="error-wrap-web.ts">
              <Comment># Web &mdash; wrapping addEventListener</Comment>
              <pre className="text-surface-300">
                {`import { wrapErrorHandler } from '@yaver/feedback-web';

// Observe 'error' events (sync errors)
window.addEventListener('error', wrapErrorHandler(
  (event) => {
    // Your existing handler logic
    console.error('Caught:', event.error);
  }
));

// Observe 'unhandledrejection' (async errors)
window.addEventListener('unhandledrejection',
  wrapErrorHandler((event) => {
    console.error('Unhandled:', event.reason);
  })
);`}
              </pre>
            </Terminal>
          </div>

          <SubHeading>Option 2: attachError</SubHeading>
          <Prose>
            For errors caught in try/catch blocks, manually report them to the
            SDK. Attach optional metadata (component name, user action, custom
            tags) so the agent has richer context for the fix.
          </Prose>

          <div className="mb-8">
            <Terminal title="error-attach.tsx">
              <pre className="text-surface-300">
                {`import { attachError } from '@yaver/feedback-react-native';

async function fetchUserProfile(userId: string) {
  try {
    const res = await fetch(\`/api/users/\${userId}\`);
    if (!res.ok) throw new Error(\`HTTP \${res.status}\`);
    return await res.json();
  } catch (err) {
    // Report to SDK with metadata
    attachError(err, {
      component: 'UserProfile',
      action: 'fetchUserProfile',
      userId,
    });

    // Re-throw or handle as usual
    throw err;
  }
}`}
              </pre>
            </Terminal>
          </div>

          <div className="mb-4 rounded-xl border border-surface-800 bg-surface-900 p-4">
            <p className="text-sm leading-relaxed text-surface-400">
              Both options can be used together. Use{" "}
              <InlineCode>wrapErrorHandler</InlineCode> for global uncaught
              errors and <InlineCode>attachError</InlineCode> for caught
              exceptions where you want to add context. The SDK deduplicates
              errors that arrive through both paths.
            </p>
          </div>
        </section>

        {/* ─── Black Box Streaming ─── */}
        <section className="mb-20">
          <SectionHeading id="black-box">
            Black Box Streaming
          </SectionHeading>
          <Prose>
            The black box is a flight recorder that continuously streams events
            to the agent over the P2P connection. When a crash or feedback
            report is triggered, the agent already has the full timeline of
            everything that happened in your app &mdash; logs, navigation, network
            requests, state changes, renders, and lifecycle events.
          </Prose>

          <SubHeading>Event Types</SubHeading>
          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Type
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Source
                  </th>
                  <th className="pb-3 font-medium text-surface-200">
                    Description
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>log</InlineCode>
                  </td>
                  <td className="py-3 pr-4">console / print</td>
                  <td className="py-3">Console output (log, warn, error, debug)</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>error</InlineCode>
                  </td>
                  <td className="py-3 pr-4">error handler</td>
                  <td className="py-3">Captured errors with stack traces</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>navigation</InlineCode>
                  </td>
                  <td className="py-3 pr-4">router / navigator</td>
                  <td className="py-3">Screen transitions with from/to routes</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>lifecycle</InlineCode>
                  </td>
                  <td className="py-3 pr-4">app state</td>
                  <td className="py-3">Foreground, background, resume, suspend</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>network</InlineCode>
                  </td>
                  <td className="py-3 pr-4">HTTP client</td>
                  <td className="py-3">Request method, URL, status, duration</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>state</InlineCode>
                  </td>
                  <td className="py-3 pr-4">state manager</td>
                  <td className="py-3">State mutations (key, previous, next)</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4">
                    <InlineCode>render</InlineCode>
                  </td>
                  <td className="py-3 pr-4">UI framework</td>
                  <td className="py-3">Component mount, unmount, re-render counts</td>
                </tr>
              </tbody>
            </table>
          </div>

          <SubHeading>Starting and Stopping</SubHeading>
          <Prose>
            The black box starts automatically when you pass{" "}
            <InlineCode>blackBox: true</InlineCode> to{" "}
            <InlineCode>init()</InlineCode>. You can also control it manually.
          </Prose>

          <div className="mb-8">
            <Terminal title="blackbox-control.ts">
              <pre className="text-surface-300">
                {`import { BlackBox } from '@yaver/feedback-react-native';

// Manual start/stop
BlackBox.start();
BlackBox.stop();

// Check if recording
const isActive = BlackBox.isActive();`}
              </pre>
            </Terminal>
          </div>

          <SubHeading>Console Interception</SubHeading>
          <Prose>
            By default, the black box does not intercept console output. Call{" "}
            <InlineCode>BlackBox.wrapConsole()</InlineCode> to opt in. This
            patches <InlineCode>console.log</InlineCode>,{" "}
            <InlineCode>console.warn</InlineCode>,{" "}
            <InlineCode>console.error</InlineCode>, and{" "}
            <InlineCode>console.debug</InlineCode> to stream their output to
            the agent. The original console methods still fire normally.
          </Prose>

          <div className="mb-8">
            <Terminal title="blackbox-console.ts">
              <pre className="text-surface-300">
                {`import { BlackBox } from '@yaver/feedback-react-native';

// Opt in to console streaming
BlackBox.wrapConsole();

// These now stream to the agent AND print normally
console.log('User tapped checkout');
console.error('Payment failed:', err);`}
              </pre>
            </Terminal>
          </div>

          <SubHeading>Navigation Tracking</SubHeading>
          <Prose>
            Report screen changes so the agent knows which screen the user was
            on when a bug occurred.
          </Prose>

          <div className="mb-8">
            <Terminal title="blackbox-nav-rn.tsx">
              <Comment># React Native &mdash; manual</Comment>
              <pre className="text-surface-300">
                {`import { BlackBox } from '@yaver/feedback-react-native';

// In your navigation listener
navigation.addListener('state', (e) => {
  const currentRoute = getActiveRouteName(e.data.state);
  BlackBox.navigation(currentRoute, previousRoute);
  previousRoute = currentRoute;
});`}
              </pre>
            </Terminal>
          </div>

          <div className="mb-8">
            <Terminal title="blackbox-nav-flutter.dart">
              <Comment># Flutter &mdash; automatic via NavigatorObserver</Comment>
              <pre className="text-surface-300">
                {`import 'package:yaver_feedback/yaver_feedback.dart';

MaterialApp(
  navigatorObservers: [
    BlackBox.navigatorObserver(),
  ],
  // ...
);

// Routes are tracked automatically.
// No manual calls needed.`}
              </pre>
            </Terminal>
          </div>

          <SubHeading>Network Request Tracking</SubHeading>
          <Prose>
            Log HTTP requests so the agent can correlate API failures with UI
            bugs.
          </Prose>

          <div className="mb-8">
            <Terminal title="blackbox-network.ts">
              <pre className="text-surface-300">
                {`import { BlackBox } from '@yaver/feedback-react-native';

// In your HTTP client wrapper or interceptor
const start = Date.now();
const res = await fetch(url, { method: 'POST', body });
const durationMs = Date.now() - start;

BlackBox.networkRequest('POST', url, res.status, durationMs);`}
              </pre>
            </Terminal>
          </div>

          <SubHeading>What the Agent Receives</SubHeading>
          <Prose>
            When a feedback report or crash is sent, the agent receives a
            formatted prompt with the full black box timeline. Events are sorted
            by timestamp, most recent first.
          </Prose>

          <div className="mb-8">
            <Terminal title="agent-receives.txt">
              <pre className="text-surface-300">
                {`── Black Box (last 30 seconds) ──────────────────────

[14:32:05.123] navigation  HomeScreen → CheckoutScreen
[14:32:05.890] network     POST /api/cart/checkout → 200 (342ms)
[14:32:06.100] log         "Processing payment for order #4821"
[14:32:06.455] network     POST /api/payments/charge → 500 (1203ms)
[14:32:06.460] error       PaymentError: gateway timeout
                            at processPayment (checkout.ts:47)
                            at handleCheckout (CheckoutScreen.tsx:112)
[14:32:06.461] state       paymentStatus: "processing" → "failed"
[14:32:06.470] render      ErrorBanner mounted
[14:32:06.512] log         "Payment failed, showing retry UI"

── Error ───────────────────────────────────────────

PaymentError: gateway timeout
  component: CheckoutScreen
  action: handleCheckout

── User Voice Note ─────────────────────────────────

"I tapped pay and it just shows an error banner.
 The amount was $42.99."`}
              </pre>
            </Terminal>
          </div>
        </section>

        {/* ─── Hot Reload Button ─── */}
        <section className="mb-20">
          <SectionHeading id="hot-reload">Hot Reload Button</SectionHeading>
          <Prose>
            The FeedbackModal includes a reload button and a streaming status
            indicator. After the agent applies a fix, tap reload to see the
            change immediately without rebuilding.
          </Prose>

          <div className="mb-8">
            <Terminal title="hot-reload.tsx">
              <pre className="text-surface-300">
                {`import { FeedbackModal } from '@yaver/feedback-react-native';

// The modal shows:
// - Streaming status (connected / reconnecting / offline)
// - Reload button (triggers RN fast refresh or Flutter hot reload)
// - Fix progress when the agent is working on a task

<FeedbackModal
  visible={showFeedback}
  onClose={() => setShowFeedback(false)}
  // Reload callback — default uses RN DevSettings.reload()
  onReload={() => DevSettings.reload()}
/>`}
              </pre>
            </Terminal>
          </div>

          <div className="space-y-4">
            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Streaming Status Indicator
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                Shows whether the black box stream to the agent is active.
                Green dot = connected and streaming. Yellow = reconnecting.
                Red = offline (events are buffered locally and flushed on
                reconnect).
              </p>
            </div>

            <div className="card">
              <h4 className="mb-2 text-sm font-medium text-surface-200">
                Reload Button
              </h4>
              <p className="text-sm leading-relaxed text-surface-400">
                After the agent pushes a fix, the modal shows a reload button.
                On React Native this triggers{" "}
                <InlineCode>DevSettings.reload()</InlineCode>. On Flutter it
                sends a hot reload signal. On Web it calls{" "}
                <InlineCode>window.location.reload()</InlineCode>.
              </p>
            </div>
          </div>
        </section>

        {/* ─── Agent Integration ─── */}
        <section className="mb-20">
          <SectionHeading id="agent-integration">
            Agent Integration
          </SectionHeading>
          <Prose>
            The desktop agent uses data from the Feedback SDK to understand bugs
            and generate fixes. Here is how the pieces connect.
          </Prose>

          <SubHeading>Black Box Context in Fix Prompts</SubHeading>
          <Prose>
            When a feedback report arrives, the agent injects the black box
            timeline into the prompt context. The AI model sees the full
            sequence of events &mdash; navigation, network calls, state changes,
            logs &mdash; alongside the error and any voice/screenshot attachments.
            This gives the model enough context to locate the bug and generate a
            targeted fix.
          </Prose>

          <SubHeading>Fatal Crash Auto-Tasks</SubHeading>
          <Prose>
            When <InlineCode>errorCapture</InlineCode> is enabled and the SDK
            observes a fatal crash, it automatically creates a fix task on the
            agent. No manual feedback submission required. The task includes the
            error, stack trace, black box timeline, and device info. The agent
            picks it up and starts working on a fix immediately.
          </Prose>

          <SubHeading>Live Log Watching via SSE</SubHeading>
          <Prose>
            The agent exposes a{" "}
            <InlineCode>/blackbox/subscribe</InlineCode> SSE endpoint. Tools
            and dashboards can subscribe to the live black box stream for
            real-time monitoring.
          </Prose>

          <div className="mb-8">
            <Terminal title="blackbox-sse.sh">
              <Cmd>curl -N http://localhost:18080/blackbox/subscribe \</Cmd>
              <div className="text-surface-200 pl-4 select-all">
                -H &quot;Authorization: Bearer $YAVER_TOKEN&quot;
              </div>
              <Divider />
              <Output>data: {`{"type":"log","ts":1711300325123,"msg":"User tapped checkout"}`}</Output>
              <Output>data: {`{"type":"network","ts":1711300325890,"method":"POST","url":"/api/cart","status":200,"ms":342}`}</Output>
              <Output>data: {`{"type":"error","ts":1711300326455,"error":"PaymentError: gateway timeout","stack":"..."}`}</Output>
            </Terminal>
          </div>
        </section>

        {/* ─── API Reference ─── */}
        <section className="mb-20">
          <SectionHeading id="api-reference">API Reference</SectionHeading>
          <Prose>
            Method reference for each platform. All methods are available after
            calling <InlineCode>YaverFeedback.init()</InlineCode>.
          </Prose>

          {/* React Native API */}
          <SubHeading>React Native</SubHeading>
          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Method
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Parameters
                  </th>
                  <th className="pb-3 font-medium text-surface-200">
                    Description
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>YaverFeedback.init(config)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>FeedbackConfig</InlineCode>
                  </td>
                  <td className="py-3">Initialize the SDK with agent connection and feature flags</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>wrapErrorHandler(handler)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>(error, isFatal) =&gt; void</InlineCode>
                  </td>
                  <td className="py-3">Wrap an existing error handler; pass-through, never swallows</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>attachError(err, meta?)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>Error, ErrorMeta?</InlineCode>
                  </td>
                  <td className="py-3">Manually capture an error with optional metadata</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.start()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Start the flight recorder</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.stop()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Stop recording and flush buffered events</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.wrapConsole()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Opt in to streaming console output to the agent</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.navigation(route, prev?)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>string, string?</InlineCode>
                  </td>
                  <td className="py-3">Record a screen transition</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.networkRequest(...)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>method, url, status, ms</InlineCode>
                  </td>
                  <td className="py-3">Record an HTTP request with timing</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.isActive()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Returns true if the flight recorder is running</td>
                </tr>
              </tbody>
            </table>
          </div>

          {/* Flutter API */}
          <SubHeading>Flutter</SubHeading>
          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Method
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Parameters
                  </th>
                  <th className="pb-3 font-medium text-surface-200">
                    Description
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>YaverFeedback.init(...)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">Named params</td>
                  <td className="py-3">Initialize with agent host, port, and feature flags</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>wrapErrorHandler(handler)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>FlutterExceptionHandler?</InlineCode>
                  </td>
                  <td className="py-3">Wrap FlutterError.onError; pass-through</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>wrapPlatformErrorHandler(handler)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>ErrorCallback?</InlineCode>
                  </td>
                  <td className="py-3">Wrap PlatformDispatcher.instance.onError; pass-through</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>attachError(error, meta?)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>Object, Map&lt;String, dynamic&gt;?</InlineCode>
                  </td>
                  <td className="py-3">Manually capture an error with optional metadata</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.start()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Start the flight recorder</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.stop()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Stop recording and flush buffered events</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.navigatorObserver()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Returns a NavigatorObserver for automatic route tracking</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.navigation(route, prev?)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>String, String?</InlineCode>
                  </td>
                  <td className="py-3">Record a screen transition manually</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.networkRequest(...)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>method, url, status, ms</InlineCode>
                  </td>
                  <td className="py-3">Record an HTTP request with timing</td>
                </tr>
              </tbody>
            </table>
          </div>

          {/* Web API */}
          <SubHeading>Web</SubHeading>
          <div className="mb-8 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-surface-800 text-left">
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Method
                  </th>
                  <th className="pb-3 pr-4 font-medium text-surface-200">
                    Parameters
                  </th>
                  <th className="pb-3 font-medium text-surface-200">
                    Description
                  </th>
                </tr>
              </thead>
              <tbody className="text-surface-400">
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>YaverFeedback.init(config)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>FeedbackConfig</InlineCode>
                  </td>
                  <td className="py-3">Initialize the SDK with agent connection and feature flags</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>wrapErrorHandler(handler)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>(event: ErrorEvent) =&gt; void</InlineCode>
                  </td>
                  <td className="py-3">Wrap a window error/rejection listener; pass-through</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>attachError(err, meta?)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>Error, ErrorMeta?</InlineCode>
                  </td>
                  <td className="py-3">Manually capture an error with optional metadata</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.start()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Start the flight recorder</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.stop()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Stop recording and flush buffered events</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.wrapConsole()</InlineCode>
                  </td>
                  <td className="py-3 pr-4">&mdash;</td>
                  <td className="py-3">Opt in to streaming console output to the agent</td>
                </tr>
                <tr className="border-b border-surface-800/50">
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.navigation(route, prev?)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>string, string?</InlineCode>
                  </td>
                  <td className="py-3">Record a route change (integrate with your router)</td>
                </tr>
                <tr>
                  <td className="py-3 pr-4">
                    <InlineCode>BlackBox.networkRequest(...)</InlineCode>
                  </td>
                  <td className="py-3 pr-4">
                    <InlineCode>method, url, status, ms</InlineCode>
                  </td>
                  <td className="py-3">Record an HTTP request with timing</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        {/* Footer */}
        <Divider />
        <div className="mt-8 space-y-2 text-center text-xs text-surface-500">
          <p>
            Source:{" "}
            <a
              href="https://github.com/kivanccakmak/yaver.io"
              className="underline hover:text-surface-200"
              target="_blank"
              rel="noopener noreferrer"
            >
              github.com/kivanccakmak/yaver.io
            </a>
          </p>
        </div>

        {/* Back to home */}
        <div className="mt-8 text-center">
          <Link
            href="/"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            Back to home
          </Link>
        </div>
      </div>
    </div>
  );
}
