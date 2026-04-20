/// Agent discovery for the Yaver Feedback SDK (Flutter).
///
/// Convex is the primary source of truth. The user's Convex account
/// has the freshest IP / port for each registered agent, updated
/// every 2 minutes via heartbeat. Every `discover()` should:
///
///   1. Re-query Convex for the latest device list when `convexUrl`
///      + `authToken` are available (no cache shortcut — a stale
///      URL is worse than no URL).
///   2. Dedup the rows so re-pair leftovers don't fight the picker.
///   3. Race `/health` probes in parallel across `quicHost` + every
///      `localIps` — THE thing that makes direct LAN reloads "just
///      work" on multi-homed Macs (en0 + utun Tailscale + docker0).
///   4. Fall back to the relay only when every direct IP fails.
///   5. Store the successful URL only after it's confirmed
///      reachable. The stored cache is the last-chance shortcut when
///      Convex itself is unreachable.
///
/// Mirrors sdk/feedback/react-native/src/Discovery.ts.
library yaver_feedback.discovery;

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:http/http.dart' as http;

import 'auth.dart';
import 'device.dart';

/// Result of a successful agent discovery or probe.
class DiscoveryResult {
  /// The base URL of the discovered agent (e.g. `http://192.168.1.42:18080`).
  final String url;

  /// The hostname reported by the agent.
  final String hostname;

  /// The agent version string.
  final String version;

  /// Round-trip latency in milliseconds measured during the probe.
  final int latencyMs;

  /// Creates a new [DiscoveryResult].
  const DiscoveryResult({
    required this.url,
    required this.hostname,
    required this.version,
    required this.latencyMs,
  });

  @override
  String toString() =>
      'DiscoveryResult(url: $url, hostname: $hostname, version: $version, latencyMs: ${latencyMs}ms)';
}

/// Small LAN fallback sweep used ONLY when Convex lookup fails AND
/// the stored cache probe fails. Kept tight — covers 192.168.1/0.x
/// and 10.0.0/1.x with a handful of common host suffixes. The primary
/// path is always Convex when credentials are available.
const List<String> _lanSubnets = [
  '192.168.1',
  '192.168.0',
  '10.0.0',
  '10.0.1',
];
const List<int> _lanHostSuffixes = [1, 2, 50, 100, 101, 200];

class YaverDiscovery {
  YaverDiscovery._();

  /// In-memory cache of the last successful discovery result.
  static DiscoveryResult? _cached;

  /// Returns the cached discovery result, if any.
  static DiscoveryResult? get cached => _cached;

  /// Clears the cached discovery result.
  static void clearCache() {
    _cached = null;
  }

  /// Auto-discover an agent.
  ///
  /// Strategy:
  ///   1. Convex (always tried first when credentials are available)
  ///   2. In-memory cache (only if Convex was unreachable)
  ///   3. Small LAN sweep (last resort)
  ///
  /// Passing no options preserves the previous in-process cache-first
  /// behaviour for backwards compatibility with callers that don't
  /// yet know their Convex URL / auth token.
  static Future<DiscoveryResult?> discover({
    String? convexUrl,
    String? authToken,
    String? preferredDeviceId,
  }) async {
    // Strategy 1: Convex — always tried first when credentials are
    // available. No cache shortcut.
    if (convexUrl != null && convexUrl.isNotEmpty &&
        authToken != null && authToken.isNotEmpty) {
      final result = await discoverFromConvex(
        convexUrl: convexUrl,
        authToken: authToken,
        preferredDeviceId: preferredDeviceId,
      );
      if (result != null) {
        _cached = result;
        return result;
      }
    }

    // Strategy 2: in-memory cache (Convex was unreachable / no creds).
    if (_cached != null) {
      final cached = await probe(_cached!.url);
      if (cached != null) return cached;
      _cached = null;
    }

    // Strategy 3: small LAN fallback sweep.
    final candidates = <String>[];
    for (final subnet in _lanSubnets) {
      for (final suffix in _lanHostSuffixes) {
        candidates.add('http://$subnet.$suffix:$defaultAgentHttpPort');
      }
    }
    debugPrint(
      'YaverDiscovery: LAN fallback — scanning ${candidates.length} addresses',
    );
    final results = await Future.wait(candidates.map(probe));
    for (final r in results) {
      if (r != null) {
        _cached = r;
        return r;
      }
    }
    return null;
  }

  /// Re-query Convex ignoring any cached URL. The "the IP probably
  /// changed, ask the source of truth again" path — call this right
  /// after a probe/network failure.
  static Future<DiscoveryResult?> refreshFromConvex({
    required String convexUrl,
    required String authToken,
    String? preferredDeviceId,
  }) async {
    clearCache();
    final result = await discoverFromConvex(
      convexUrl: convexUrl,
      authToken: authToken,
      preferredDeviceId: preferredDeviceId,
    );
    if (result != null) _cached = result;
    return result;
  }

  /// Fetch the device list from Convex, dedup it, pick the best
  /// target, race parallel /health probes across its reachable IPs,
  /// then fall back to relay if everything direct fails.
  static Future<DiscoveryResult?> discoverFromConvex({
    required String convexUrl,
    required String authToken,
    String? preferredDeviceId,
  }) async {
    final base = convexUrl.replaceAll(RegExp(r'/$'), '');
    try {
      // Try cloud machines first (CPU/GPU managed machines). Stable
      // IPs + a cheap direct probe.
      try {
        final machinesRes = await http.get(
          Uri.parse('$base/machines'),
          headers: {'Authorization': 'Bearer $authToken'},
        ).timeout(const Duration(seconds: 5));
        if (machinesRes.statusCode == 200) {
          final data = jsonDecode(machinesRes.body) as Map<String, dynamic>;
          final machines = data['machines'] as List? ?? const [];
          for (final m in machines) {
            if (m is! Map<String, dynamic>) continue;
            if (m['status'] != 'active') continue;
            final ip = m['serverIp'] as String?;
            if (ip == null || ip.isEmpty) continue;
            final probed =
                await probe('http://$ip:$defaultAgentHttpPort');
            if (probed != null) return probed;
          }
        }
      } catch (_) {
        // /machines is optional — no-op on error
      }

      // Fall back to personal devices registered in Convex.
      final devicesRes = await http.get(
        Uri.parse('$base/devices/list'),
        headers: {'Authorization': 'Bearer $authToken'},
      ).timeout(const Duration(seconds: 5));
      if (devicesRes.statusCode != 200) return null;
      final body = jsonDecode(devicesRes.body);
      final raw = (body is Map<String, dynamic> && body['devices'] is List)
          ? body['devices'] as List
          : (body is List ? body : const []);
      if (raw.isEmpty) return null;

      final normalised = raw
          .whereType<Map<String, dynamic>>()
          .map(RemoteDevice.fromJson)
          .toList();
      final deduped = collapseRemoteDevices(normalised);
      final target = pickTargetDevice(
        deduped,
        preferredDeviceId: preferredDeviceId,
      );
      if (target == null) return null;

      // Race all reachable candidates in parallel — quicHost plus every
      // LAN IP the agent reported. Multi-homed Macs advertise en0 +
      // utun (tailscale) + docker0 etc.; probing them in parallel makes
      // the SDK "just work" without depending on which NIC the router
      // DHCP'd us from.
      final candidates = buildProbeCandidates(target);
      if (candidates.isNotEmpty) {
        final direct = await _raceProbe(candidates);
        if (direct != null) return direct;
      }

      // Direct probes all failed — route through relay.
      final relay = await _discoverViaRelay(
        convexUrl: base,
        authToken: authToken,
        deviceId: target.deviceId,
      );
      return relay;
    } catch (_) {
      return null;
    }
  }

  static Future<DiscoveryResult?> _raceProbe(List<String> urls) async {
    final res = await raceHealthProbes(urls);
    if (res == null) return null;
    return DiscoveryResult(
      url: res.url,
      hostname: res.hostname ?? 'Unknown',
      version: res.version ?? 'unknown',
      latencyMs: res.latencyMs ?? 0,
    );
  }

  /// Discover agent via relay HTTP proxy. Uses the user's configured
  /// relay first (from `/auth/validate`), then the platform relay
  /// list.
  static Future<DiscoveryResult?> _discoverViaRelay({
    required String convexUrl,
    required String authToken,
    required String deviceId,
  }) async {
    try {
      String? relayUrl;
      String? relayPassword;

      try {
        final settingsRes = await http.get(
          Uri.parse('$convexUrl/auth/validate'),
          headers: {'Authorization': 'Bearer $authToken'},
        ).timeout(const Duration(seconds: 5));
        if (settingsRes.statusCode == 200) {
          final data = jsonDecode(settingsRes.body) as Map<String, dynamic>;
          relayUrl = data['relayUrl'] as String?;
          relayPassword = data['relayPassword'] as String?;
        }
      } catch (_) {
        // /auth/validate is optional for the relay URL — fall through
      }

      if (relayUrl == null || relayUrl.isEmpty) {
        try {
          final configRes = await http
              .get(Uri.parse('$convexUrl/platform-config?key=relay_servers'))
              .timeout(const Duration(seconds: 5));
          if (configRes.statusCode == 200) {
            final data = jsonDecode(configRes.body) as Map<String, dynamic>;
            final value = data['value'];
            final servers = value is String ? jsonDecode(value) : value;
            if (servers is List) {
              for (final s in servers) {
                if (s is Map && s['httpUrl'] is String) {
                  relayUrl = s['httpUrl'] as String;
                  break;
                }
              }
            }
          }
        } catch (_) {
          // platform-config is optional
        }
      }

      if (relayUrl == null || relayUrl.isEmpty) return null;

      final relayBase =
          '${relayUrl.replaceAll(RegExp(r'/$'), '')}/d/$deviceId';
      return probeWithHeaders(relayBase, {
        'X-Relay-Password': relayPassword ?? '',
      });
    } catch (_) {
      return null;
    }
  }

  /// Probe a URL with custom headers. Relay requests need a
  /// `X-Relay-Password` header, which makes them distinct from direct
  /// LAN probes.
  static Future<DiscoveryResult?> probeWithHeaders(
    String url,
    Map<String, String> headers,
  ) async {
    final base = url.endsWith('/') ? url.substring(0, url.length - 1) : url;
    final start = DateTime.now().millisecondsSinceEpoch;
    try {
      final res = await http
          .get(Uri.parse('$base/health'), headers: headers)
          .timeout(const Duration(milliseconds: relayProbeTimeoutMs));
      if (res.statusCode != 200) return null;
      String hostname = 'Unknown';
      String version = 'unknown';
      try {
        final data = jsonDecode(res.body) as Map<String, dynamic>;
        hostname = (data['hostname'] as String?) ??
            (data['name'] as String?) ??
            'Unknown';
        version = (data['version'] as String?) ?? 'unknown';
      } catch (_) {
        // /health may return plain text
      }
      return DiscoveryResult(
        url: base,
        hostname: hostname,
        version: version,
        latencyMs: DateTime.now().millisecondsSinceEpoch - start,
      );
    } catch (_) {
      return null;
    }
  }

  /// Probes a specific URL to check if a Yaver agent is reachable.
  ///
  /// Hits `<url>/health` and expects a JSON response with `hostname`
  /// and `version` fields. Returns `null` if the probe fails or times
  /// out.
  static Future<DiscoveryResult?> probe(String url) async {
    final normalized =
        url.endsWith('/') ? url.substring(0, url.length - 1) : url;
    final uri = Uri.parse('$normalized/health');

    try {
      final stopwatch = Stopwatch()..start();
      final response = await http
          .get(uri)
          .timeout(const Duration(milliseconds: probeTimeoutMs));
      stopwatch.stop();

      if (response.statusCode == 200) {
        final body = jsonDecode(response.body) as Map<String, dynamic>;
        final result = DiscoveryResult(
          url: normalized,
          hostname: body['hostname'] as String? ?? 'unknown',
          version: body['version'] as String? ?? 'unknown',
          latencyMs: stopwatch.elapsedMilliseconds,
        );
        _cached = result;
        return result;
      }
    } on TimeoutException {
      // probe timed out
    } on SocketException {
      // host unreachable
    } on http.ClientException {
      // connection refused or other HTTP error
    } on FormatException {
      // invalid JSON response
    } catch (e) {
      debugPrint('YaverDiscovery: probe error for $url: $e');
    }

    return null;
  }

  /// Manually connect to a known agent URL, verify it, and cache the
  /// result. The recommended method when the user enters an IP/URL
  /// manually. Returns `null` if the agent is not reachable.
  static Future<DiscoveryResult?> connect(String url) async {
    var normalized = url.trim();
    if (!normalized.startsWith('http://') &&
        !normalized.startsWith('https://')) {
      normalized = 'http://$normalized';
    }

    final uri = Uri.parse(normalized);
    if (uri.port == 0 ||
        (uri.port == 80 && !normalized.contains(':$defaultAgentHttpPort'))) {
      normalized = '${uri.scheme}://${uri.host}:$defaultAgentHttpPort';
    }

    final result = await probe(normalized);
    if (result != null) _cached = result;
    return result;
  }
}
