import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;

import 'types.dart';

/// Translate a raw Go-agent error into something a user can act on.
///
/// The agent surfaces Go's low-level error text verbatim inside JSON,
/// e.g. `Get "http://127.0.0.1:8081/reload": dial tcp 127.0.0.1:8081:
/// connect: connection refused`. Accurate but unreadable for a phone
/// user — and the real cause is almost always "no dev server running
/// on the host machine". Mirrors the RN SDK's friendlyReloadError.
String _friendlyReloadError(int status, String body) {
  final lower = body.toLowerCase();
  final hasRefused = lower.contains('connection refused') ||
      lower.contains('econnrefused');
  final hasLoopback =
      lower.contains('127.0.0.1') || lower.contains('localhost');
  if (hasRefused && hasLoopback) {
    return 'No dev server running on your machine. '
        'Start Metro with `yaver dev start` or use "Screenshot & Fix" instead.';
  }
  if (lower.contains('no dev server') || lower.contains('not running')) {
    return 'No dev server running on your machine. Start Metro first.';
  }
  if (status == 401 || status == 403) {
    return 'Agent rejected the session — please sign in again.';
  }
  if (status >= 500) {
    return 'Agent hit an internal error while reloading. Check `yaver logs`.';
  }
  return 'Reload failed ($status).';
}

/// Modes accepted by [P2PClient.reloadApp].
///
///   - [bundle] — rebuild Hermes bytecode on the agent, push via
///     BlackBox SSE. Always safe even when Metro isn't running.
///   - [dev] — tell the local dev server (Metro / Vite / Next.js) to
///     hot-reload. Fast when Metro is up; fails when it isn't.
enum ReloadMode { bundle, dev }

/// Lightweight HTTP client for communicating with a Yaver agent.
///
/// Provides health checks, agent info, feedback upload, build management,
/// and artifact download URL generation.
///
/// ```dart
/// final client = P2PClient(
///   baseUrl: 'http://192.168.1.42:18080',
///   authToken: 'your-token',
/// );
///
/// if (await client.health()) {
///   final info = await client.info();
///   print('Connected to ${info['hostname']}');
/// }
/// ```
class P2PClient {
  /// The base URL of the Yaver agent (e.g. `http://192.168.1.42:18080`).
  final String baseUrl;

  /// Auth token for the Yaver agent.
  final String authToken;

  final http.Client _httpClient;

  /// Creates a new [P2PClient].
  ///
  /// Optionally accepts a custom [http.Client] for testing.
  P2PClient({
    required this.baseUrl,
    required this.authToken,
    http.Client? httpClient,
  }) : _httpClient = httpClient ?? http.Client();

  /// Returns the normalized base URL (without trailing slash).
  String get _base =>
      baseUrl.endsWith('/') ? baseUrl.substring(0, baseUrl.length - 1) : baseUrl;

  /// Standard auth headers.
  Map<String, String> get _headers => {
        'Authorization': 'Bearer $authToken',
        'Content-Type': 'application/json',
      };

  /// Checks if the agent is reachable.
  ///
  /// Returns `true` if `/health` responds with HTTP 200.
  Future<bool> health() async {
    try {
      final response = await _httpClient
          .get(Uri.parse('$_base/health'))
          .timeout(const Duration(seconds: 3));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  /// Fetches agent information from `/health`.
  ///
  /// Returns a map containing `hostname`, `version`, and other agent metadata.
  ///
  /// Throws [HttpException] on non-2xx response.
  Future<Map<String, dynamic>> info() async {
    final response = await _httpClient.get(
      Uri.parse('$_base/health'),
      headers: _headers,
    );
    _checkResponse(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  /// Uploads a [FeedbackBundle] to the agent as a multipart POST.
  ///
  /// Returns the feedback report ID assigned by the agent.
  ///
  /// Throws [HttpException] on non-2xx response.
  Future<String> uploadFeedback(FeedbackBundle bundle) async {
    final uri = Uri.parse('$_base/feedback');
    final request = http.MultipartRequest('POST', uri);

    request.headers['Authorization'] = 'Bearer $authToken';
    request.fields['metadata'] = jsonEncode(bundle.toJson());

    // Attach video
    if (bundle.videoPath != null) {
      final file = File(bundle.videoPath!);
      if (await file.exists()) {
        request.files.add(
          await http.MultipartFile.fromPath('video', bundle.videoPath!),
        );
      }
    }

    // Attach audio
    if (bundle.audioPath != null) {
      final file = File(bundle.audioPath!);
      if (await file.exists()) {
        request.files.add(
          await http.MultipartFile.fromPath('audio', bundle.audioPath!),
        );
      }
    }

    // Attach screenshots
    for (final path in bundle.screenshotPaths) {
      final file = File(path);
      if (await file.exists()) {
        request.files.add(
          await http.MultipartFile.fromPath('screenshots', path),
        );
      }
    }

    final streamedResponse = await request.send();
    final body = await streamedResponse.stream.bytesToString();

    if (streamedResponse.statusCode >= 400) {
      throw HttpException(
        'Upload failed: HTTP ${streamedResponse.statusCode}: $body',
      );
    }

    final json = jsonDecode(body) as Map<String, dynamic>;
    return json['feedbackId'] as String? ?? json['id'] as String? ?? '';
  }

  /// Lists available builds from the agent.
  ///
  /// Returns a list of build metadata maps.
  ///
  /// Throws [HttpException] on non-2xx response.
  Future<List<Map<String, dynamic>>> listBuilds() async {
    final response = await _httpClient.get(
      Uri.parse('$_base/builds'),
      headers: _headers,
    );
    _checkResponse(response);

    final body = jsonDecode(response.body);
    if (body is List) {
      return body.cast<Map<String, dynamic>>();
    }
    if (body is Map && body.containsKey('builds')) {
      return (body['builds'] as List).cast<Map<String, dynamic>>();
    }
    return [];
  }

  /// Starts a build for the given [platform] (e.g. `"ios"`, `"android"`).
  ///
  /// Returns the build metadata map including the assigned build ID.
  ///
  /// Throws [HttpException] on non-2xx response.
  Future<Map<String, dynamic>> startBuild(String platform) async {
    final response = await _httpClient.post(
      Uri.parse('$_base/builds'),
      headers: _headers,
      body: jsonEncode({'platform': platform}),
    );
    _checkResponse(response);
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  /// Returns the download URL for a build artifact.
  String getArtifactUrl(String buildId) {
    return '$_base/builds/$buildId/artifact';
  }

  /// Trigger a reload of the third-party app.
  ///
  /// Defaults to [ReloadMode.bundle] — always produces a correct
  /// Hermes bytecode bundle from the current filesystem state, which
  /// takes ~30–60s and hits `/dev/reload-app`. The agent then uses
  /// the BlackBox SSE command channel to push the fresh bundle URL to
  /// the device. This is safe even when Metro isn't running on the
  /// Mac (common for TestFlight users or vibe coders away from their
  /// desk).
  ///
  /// [ReloadMode.dev] is the fast path: tell the local dev server
  /// (Metro / Vite / Next.js) to hot-reload. If the dev server isn't
  /// running, this call falls through to the bundle path instead of
  /// surfacing the raw "connection refused" error. Mirrors the RN
  /// SDK's `P2PClient.reloadApp`.
  Future<Map<String, dynamic>> reloadApp({
    ReloadMode mode = ReloadMode.bundle,
  }) async {
    if (mode == ReloadMode.dev) {
      try {
        final primary = await _httpClient.post(
          Uri.parse('$_base/dev/reload'),
          headers: {'Authorization': 'Bearer $authToken'},
        );
        if (primary.statusCode < 400) {
          try {
            final parsed = jsonDecode(primary.body);
            if (parsed is Map<String, dynamic>) return parsed;
          } catch (_) {}
          return {'ok': true};
        }
        // Dev mode failed — fall through to bundle rebuild rather
        // than surfacing the raw error. The user never has to know
        // Metro wasn't running.
      } catch (_) {
        // Fall through to bundle
      }
    }

    final res = await _httpClient.post(
      Uri.parse('$_base/dev/reload-app'),
      headers: _headers,
      body: jsonEncode({'mode': 'bundle'}),
    );
    if (res.statusCode >= 400) {
      throw HttpException(_friendlyReloadError(res.statusCode, res.body));
    }
    try {
      final parsed = jsonDecode(res.body);
      if (parsed is Map<String, dynamic>) return parsed;
    } catch (_) {}
    return {'ok': true};
  }

  /// Sends a feedback event to the live stream endpoint.
  ///
  /// Used in [FeedbackMode.live] to stream events in real-time.
  ///
  /// Throws [HttpException] on non-2xx response.
  Future<void> streamEvent(Map<String, dynamic> event) async {
    final response = await _httpClient.post(
      Uri.parse('$_base/feedback/stream'),
      headers: _headers,
      body: jsonEncode(event),
    );
    _checkResponse(response);
  }

  /// Fetches agent commentary messages.
  ///
  /// Returns a list of commentary message maps with `text`, `level`, and
  /// `timestamp` fields.
  Future<List<Map<String, dynamic>>> getCommentary({int? since}) async {
    var url = '$_base/feedback/commentary';
    if (since != null) {
      url += '?since=$since';
    }

    try {
      final response = await _httpClient.get(
        Uri.parse(url),
        headers: _headers,
      );
      if (response.statusCode != 200) return [];

      final body = jsonDecode(response.body);
      if (body is List) return body.cast<Map<String, dynamic>>();
      if (body is Map && body.containsKey('messages')) {
        return (body['messages'] as List).cast<Map<String, dynamic>>();
      }
    } catch (_) {
      // Commentary is best-effort — never fail on it
    }
    return [];
  }

  /// Disposes the underlying HTTP client.
  void dispose() {
    _httpClient.close();
  }

  void _checkResponse(http.Response response) {
    if (response.statusCode >= 400) {
      throw HttpException(
        'HTTP ${response.statusCode}: ${response.body}',
      );
    }
  }
}
