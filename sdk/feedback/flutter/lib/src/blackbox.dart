import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:http/http.dart' as http;

import 'feedback.dart';
import 'types.dart';

/// A single event in the black box flight recorder.
class BlackBoxEvent {
  final String type; // log, error, navigation, lifecycle, network, state, render
  final String? level; // info, warn, error
  final String message;
  final int timestamp; // Unix ms
  final List<String>? stack;
  final bool? isFatal;
  final Map<String, dynamic>? metadata;
  final String? source;
  final double? duration;
  final String? route;
  final String? prevRoute;

  const BlackBoxEvent({
    required this.type,
    this.level,
    required this.message,
    required this.timestamp,
    this.stack,
    this.isFatal,
    this.metadata,
    this.source,
    this.duration,
    this.route,
    this.prevRoute,
  });

  Map<String, dynamic> toJson() => {
        'type': type,
        if (level != null) 'level': level,
        'message': message,
        'timestamp': timestamp,
        if (stack != null && stack!.isNotEmpty) 'stack': stack,
        if (isFatal == true) 'isFatal': isFatal,
        if (metadata != null && metadata!.isNotEmpty) 'metadata': metadata,
        if (source != null) 'source': source,
        if (duration != null) 'duration': duration,
        if (route != null) 'route': route,
        if (prevRoute != null) 'prevRoute': prevRoute,
      };
}

/// Configuration for the black box stream.
class BlackBoxConfig {
  final String? deviceId;
  final String? appName;
  final Duration flushInterval;
  final int maxBufferSize;

  const BlackBoxConfig({
    this.deviceId,
    this.appName,
    this.flushInterval = const Duration(seconds: 2),
    this.maxBufferSize = 50,
  });
}

/// Flight-recorder-style streaming from the Flutter app to the Yaver agent.
///
/// Captures logs, errors, navigation, lifecycle events, network requests,
/// and state changes — streams them continuously to `/blackbox/events`.
///
/// **Does not hijack any global handlers.** All capture is explicit.
/// Use [BlackBox.wrapFlutterErrorHandler] or [BlackBox.wrapNavigatorObserver]
/// only if you explicitly opt in.
class BlackBox {
  BlackBox._();

  static String? _baseUrl;
  static String? _authToken;
  static String _deviceId = '';
  static String _appName = '';
  static List<BlackBoxEvent> _buffer = [];
  static Timer? _flushTimer;
  static int _maxBufferSize = 50;
  static bool _started = false;
  static final http.Client _httpClient = http.Client();

  /// Start the black box stream. Call after [YaverFeedback.init].
  static void start([BlackBoxConfig config = const BlackBoxConfig()]) {
    final client = YaverFeedback.client;
    if (client == null) {
      debugPrint('[BlackBox] No P2P client. Call YaverFeedback.init() first.');
      return;
    }

    _baseUrl = client.baseUrl.replaceAll(RegExp(r'/$'), '');
    _authToken = client.authToken;
    _deviceId = config.deviceId ?? _generateDeviceId();
    _appName = config.appName ?? '';
    _maxBufferSize = config.maxBufferSize;
    _buffer = [];
    _started = true;

    _flushTimer?.cancel();
    _flushTimer = Timer.periodic(config.flushInterval, (_) => flush());

    _push(BlackBoxEvent(
      type: 'lifecycle',
      message: 'Black box streaming started',
      timestamp: DateTime.now().millisecondsSinceEpoch,
    ));
  }

  /// Stop streaming and flush remaining events.
  static void stop() {
    if (!_started) return;
    _push(BlackBoxEvent(
      type: 'lifecycle',
      message: 'Black box streaming stopped',
      timestamp: DateTime.now().millisecondsSinceEpoch,
    ));
    flush();
    _flushTimer?.cancel();
    _flushTimer = null;
    _started = false;
  }

  static bool get isStreaming => _started;

  // ─── Logging ───────────────────────────────────────────────────

  static void log(String message, {String? source, Map<String, dynamic>? metadata}) {
    _push(BlackBoxEvent(type: 'log', level: 'info', message: message, timestamp: DateTime.now().millisecondsSinceEpoch, source: source, metadata: metadata));
  }

  static void warn(String message, {String? source, Map<String, dynamic>? metadata}) {
    _push(BlackBoxEvent(type: 'log', level: 'warn', message: message, timestamp: DateTime.now().millisecondsSinceEpoch, source: source, metadata: metadata));
  }

  static void error(String message, {String? source, Map<String, dynamic>? metadata}) {
    _push(BlackBoxEvent(type: 'log', level: 'error', message: message, timestamp: DateTime.now().millisecondsSinceEpoch, source: source, metadata: metadata));
  }

  // ─── Errors ────────────────────────────────────────────────────

  /// Record a caught error with stack trace.
  static void captureError(Object err, StackTrace? stack, {bool isFatal = false, Map<String, dynamic>? metadata}) {
    _push(BlackBoxEvent(
      type: 'error',
      message: err.toString(),
      timestamp: DateTime.now().millisecondsSinceEpoch,
      stack: stack?.toString().split('\n').where((l) => l.trim().isNotEmpty).toList(),
      isFatal: isFatal,
      metadata: metadata,
    ));
    YaverFeedback.attachError(err, stack, metadata: metadata);
  }

  // ─── Navigation ────────────────────────────────────────────────

  /// Record a navigation event.
  static void navigation(String route, {String? prevRoute, Map<String, dynamic>? metadata}) {
    _push(BlackBoxEvent(
      type: 'navigation',
      message: 'Navigate: ${prevRoute != null ? '$prevRoute -> ' : ''}$route',
      timestamp: DateTime.now().millisecondsSinceEpoch,
      route: route,
      prevRoute: prevRoute,
      metadata: metadata,
    ));
  }

  // ─── Lifecycle ─────────────────────────────────────────────────

  static void lifecycle(String event, {Map<String, dynamic>? metadata}) {
    _push(BlackBoxEvent(type: 'lifecycle', message: event, timestamp: DateTime.now().millisecondsSinceEpoch, metadata: metadata));
  }

  // ─── Network ───────────────────────────────────────────────────

  static void networkRequest(String method, String url, {int? status, double? durationMs, Map<String, dynamic>? metadata}) {
    final msg = status != null ? '$method $url → $status' : '$method $url';
    _push(BlackBoxEvent(type: 'network', message: msg, timestamp: DateTime.now().millisecondsSinceEpoch, duration: durationMs, metadata: metadata));
  }

  // ─── State ─────────────────────────────────────────────────────

  static void stateChange(String description, {Map<String, dynamic>? metadata}) {
    _push(BlackBoxEvent(type: 'state', message: description, timestamp: DateTime.now().millisecondsSinceEpoch, metadata: metadata));
  }

  // ─── Render ────────────────────────────────────────────────────

  static void render(String component, {double? durationMs, Map<String, dynamic>? metadata}) {
    _push(BlackBoxEvent(type: 'render', message: component, timestamp: DateTime.now().millisecondsSinceEpoch, duration: durationMs, metadata: metadata));
  }

  // ─── Navigator observer (opt-in) ──────────────────────────────

  /// Returns a [NavigatorObserver] that records navigation events.
  /// Add it to your [MaterialApp.navigatorObservers] list.
  ///
  /// ```dart
  /// MaterialApp(
  ///   navigatorObservers: [BlackBox.navigatorObserver()],
  /// )
  /// ```
  static NavigatorObserver navigatorObserver() => _BlackBoxNavigatorObserver();

  // ─── Flutter error handler wrapper (opt-in) ───────────────────

  /// Returns a pass-through [FlutterExceptionHandler] that streams
  /// errors to the black box AND calls [next].
  static FlutterExceptionHandler wrapFlutterErrorHandler(FlutterExceptionHandler? next) {
    return (FlutterErrorDetails details) {
      captureError(details.exception, details.stack);
      next?.call(details);
    };
  }

  // ─── Internal ──────────────────────────────────────────────────

  static void _push(BlackBoxEvent event) {
    if (!_started) return;
    _buffer.add(event);
    if (_buffer.length >= _maxBufferSize) {
      flush();
    }
  }

  static Future<void> flush() async {
    if (_baseUrl == null || _authToken == null || _buffer.isEmpty) return;

    final events = _buffer;
    _buffer = [];

    try {
      await _httpClient.post(
        Uri.parse('$_baseUrl/blackbox/events'),
        headers: {
          'Authorization': 'Bearer $_authToken',
          'Content-Type': 'application/json',
          'X-Device-ID': _deviceId,
          'X-Platform': Platform.isIOS ? 'ios' : Platform.isAndroid ? 'android' : 'unknown',
          'X-App-Name': _appName,
        },
        body: jsonEncode(events.map((e) => e.toJson()).toList()),
      );
    } catch (_) {
      // Re-add failed events (capped)
      if (_buffer.length + events.length <= _maxBufferSize * 2) {
        _buffer = [...events, ..._buffer];
      }
    }
  }

  static String _generateDeviceId() {
    final chars = '0123456789abcdef';
    return List.generate(8, (_) => chars[DateTime.now().microsecond % 16]).join();
  }
}

/// Navigator observer that records route changes to the black box.
class _BlackBoxNavigatorObserver extends NavigatorObserver {
  String? _currentRoute;

  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previousRoute) {
    final name = route.settings.name ?? route.runtimeType.toString();
    final prev = _currentRoute;
    _currentRoute = name;
    BlackBox.navigation(name, prevRoute: prev);
  }

  @override
  void didPop(Route<dynamic> route, Route<dynamic>? previousRoute) {
    final name = previousRoute?.settings.name ?? previousRoute?.runtimeType.toString() ?? 'unknown';
    final prev = _currentRoute;
    _currentRoute = name;
    BlackBox.navigation(name, prevRoute: prev);
  }

  @override
  void didReplace({Route<dynamic>? newRoute, Route<dynamic>? oldRoute}) {
    final name = newRoute?.settings.name ?? newRoute?.runtimeType.toString() ?? 'unknown';
    final prev = _currentRoute;
    _currentRoute = name;
    BlackBox.navigation(name, prevRoute: prev);
  }
}
