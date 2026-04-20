/// Remote device registry model + dedup / race-probe helpers.
///
/// Mirrors the canonical client-core implementation that lives at
/// `shared/client-core/src/device.ts` and is re-exported into the
/// RN SDK as `./_core/device.ts`. The RN SDK used to carry its own
/// ~180-line hand-port of mobile's `collapseAliasDevices`; it drifted
/// on `hwid` strong-identity and other subtle merge rules until the
/// mobile + SDK paths were unified. Port the same three-pass dedup
/// here so the Flutter SDK stops inheriting the class of drift bugs
/// the RN / mobile pair already fixed.
///
/// Convex can return multiple `/devices/list` rows per physical
/// machine after a re-pair, a hostname change, or a wipe-and-reinstall
/// (different `hwid`, same `hostname`). Without dedup the picker
/// renders as "Kıvanç's-MacBook-Air.local ×3".
library yaver_feedback.device;

import 'dart:async';
import 'dart:convert';
import 'package:http/http.dart' as http;

import 'auth.dart';

/// A remote Yaver device visible to the signed-in user.
///
/// Kept structurally compatible with the RN SDK's `RemoteDevice` and
/// `CoreDevice` shapes — the same field names and semantics so the
/// three-pass dedup rules below match every other surface.
class RemoteDevice {
  /// Convex-issued device id.
  final String deviceId;

  /// Display name — usually hostname.
  final String name;

  /// OS family — "darwin", "linux", "windows".
  final String platform;

  final bool isOnline;
  final bool needsAuth;
  final bool runnerDown;

  /// Unix ms of the latest heartbeat the agent sent to Convex.
  final int lastHeartbeat;
  final bool isGuest;
  final String? hostName;
  final String? hostEmail;

  /// "owner" | "shared-scoped" | "shared-legacy".
  final String accessScope;

  /// Primary LAN IP (or tunnel host) the agent advertised.
  final String quicHost;
  final int quicPort;

  /// HTTP port the agent listens on (usually 18080). Preferred over
  /// [quicPort] when present.
  final int? httpPort;

  final String? publicKey;

  /// Stable hardware identifier — dedup key for re-pair events.
  final String? hwid;

  /// Every LAN IP the agent reported in its last heartbeat. Races
  /// in parallel via [raceHealthProbes].
  final List<String>? localIps;

  const RemoteDevice({
    required this.deviceId,
    required this.name,
    required this.platform,
    required this.isOnline,
    required this.needsAuth,
    required this.runnerDown,
    required this.lastHeartbeat,
    required this.isGuest,
    this.hostName,
    this.hostEmail,
    this.accessScope = 'owner',
    required this.quicHost,
    required this.quicPort,
    this.httpPort,
    this.publicKey,
    this.hwid,
    this.localIps,
  });

  factory RemoteDevice.fromJson(Map<String, dynamic> j) {
    List<String>? ips;
    final rawLocalIps = j['localIps'] ?? j['lanIps'];
    if (rawLocalIps is List) {
      ips = rawLocalIps.whereType<String>().where((s) => s.isNotEmpty).toList();
      if (ips.isEmpty) ips = null;
    }
    return RemoteDevice(
      deviceId: (j['deviceId'] ?? j['id'] ?? '') as String,
      name: (j['name'] ?? '') as String,
      platform: (j['platform'] ?? j['os'] ?? '') as String,
      isOnline: j['isOnline'] == true,
      needsAuth: j['needsAuth'] == true,
      runnerDown: j['runnerDown'] == true,
      lastHeartbeat: (j['lastHeartbeat'] as num?)?.toInt() ?? 0,
      isGuest: j['isGuest'] == true,
      hostName: j['hostName'] as String?,
      hostEmail: j['hostEmail'] as String?,
      accessScope: (j['accessScope'] as String?) ?? 'owner',
      quicHost: (j['quicHost'] ?? j['host'] ?? '') as String,
      quicPort: (j['quicPort'] as num?)?.toInt() ?? 0,
      httpPort: (j['httpPort'] as num?)?.toInt() ??
          (j['quicPort'] as num?)?.toInt(),
      publicKey: j['publicKey'] as String?,
      hwid: (j['hardwareId'] ?? j['hwid']) as String?,
      localIps: ips,
    );
  }

  RemoteDevice _copyMerged({
    String? quicHost,
    int? quicPort,
    int? httpPort,
    bool? isOnline,
    bool? runnerDown,
    String? publicKey,
    String? hwid,
    int? lastHeartbeat,
    List<String>? localIps,
  }) {
    return RemoteDevice(
      deviceId: deviceId,
      name: name,
      platform: platform,
      isOnline: isOnline ?? this.isOnline,
      needsAuth: needsAuth,
      runnerDown: runnerDown ?? this.runnerDown,
      lastHeartbeat: lastHeartbeat ?? this.lastHeartbeat,
      isGuest: isGuest,
      hostName: hostName,
      hostEmail: hostEmail,
      accessScope: accessScope,
      quicHost: quicHost ?? this.quicHost,
      quicPort: quicPort ?? this.quicPort,
      httpPort: httpPort ?? this.httpPort,
      publicKey: publicKey ?? this.publicKey,
      hwid: hwid ?? this.hwid,
      localIps: localIps ?? this.localIps,
    );
  }
}

/// The user's reachable devices, split by ownership.
class DeviceList {
  final List<RemoteDevice> owned;
  final List<RemoteDevice> shared;
  const DeviceList({required this.owned, required this.shared});
  factory DeviceList.empty() => const DeviceList(owned: [], shared: []);
}

// ── Normalisers ───────────────────────────────────────────────────────

String _normName(String? name) {
  var s = (name ?? '').trim().toLowerCase();
  if (s.endsWith('.local')) s = s.substring(0, s.length - 6);
  return s;
}

String _normHost(String? host) => _normName(host);

// ── Keys ──────────────────────────────────────────────────────────────

String _identityKey(RemoteDevice d) {
  final hwid = d.hwid;
  if (hwid != null && hwid.isNotEmpty) return 'hwid:$hwid';
  final pk = d.publicKey;
  if (pk != null && pk.isNotEmpty) return 'pub:$pk';
  if (d.isGuest) {
    final scope = d.hostEmail ?? d.hostName ?? 'guest';
    final id = d.deviceId.isNotEmpty ? d.deviceId : d.name;
    return 'guest:$scope:$id';
  }
  final n = _normName(d.name);
  final os = d.platform.trim().toLowerCase();
  if (n.isNotEmpty && os.isNotEmpty) return 'host:$os:$n';
  if (d.deviceId.isNotEmpty) return 'id:${d.deviceId}';
  return 'name:${d.name}';
}

String? _aliasKey(RemoteDevice d) {
  if (d.isGuest) return null;
  final n = _normName(d.name);
  final os = d.platform.trim().toLowerCase();
  if (n.isEmpty || os.isEmpty) return null;
  return '$os:$n';
}

String? _endpointKey(RemoteDevice d) {
  if (d.isGuest) return null;
  final h = _normHost(d.quicHost);
  if (h.isEmpty) return null;
  return '$h:${d.quicPort}';
}

// ── Merge ─────────────────────────────────────────────────────────────

RemoteDevice _merge(RemoteDevice a, RemoteDevice b) {
  final incomingWins = (a.needsAuth && !b.needsAuth) ||
      (b.lastHeartbeat) > (a.lastHeartbeat) ||
      (b.isOnline && !a.isOnline);
  final base = incomingWins ? b : a;
  final other = incomingWins ? a : b;

  final ipSet = <String>{};
  for (final ip in a.localIps ?? const <String>[]) {
    if (ip.isNotEmpty) ipSet.add(ip);
  }
  for (final ip in b.localIps ?? const <String>[]) {
    if (ip.isNotEmpty) ipSet.add(ip);
  }

  return base._copyMerged(
    quicHost: base.quicHost.isNotEmpty ? base.quicHost : other.quicHost,
    quicPort: base.quicPort != 0 ? base.quicPort : other.quicPort,
    httpPort: base.httpPort ?? other.httpPort,
    isOnline: base.isOnline || other.isOnline,
    runnerDown: base.runnerDown && other.runnerDown,
    publicKey: base.publicKey ?? other.publicKey,
    hwid: base.hwid ?? other.hwid,
    lastHeartbeat: a.lastHeartbeat > b.lastHeartbeat
        ? a.lastHeartbeat
        : b.lastHeartbeat,
    localIps: ipSet.isEmpty ? null : ipSet.toList(),
  );
}

/// When two rows share an alias key (hostname + OS) but differ on
/// strong identity (hwid / publicKey), prefer the authenticated +
/// online row over a stale "needsAuth + offline" leftover. That
/// leftover pattern is what re-pair / wipe-and-reinstall produces on
/// Convex.
RemoteDevice? _pickActiveOverStaleNeedsAuth(RemoteDevice a, RemoteDevice b) {
  final aDead = a.needsAuth && !a.isOnline;
  final bDead = b.needsAuth && !b.isOnline;
  final aLive = !a.needsAuth && a.isOnline;
  final bLive = !b.needsAuth && b.isOnline;
  if (aDead && bLive) return b;
  if (bDead && aLive) return a;
  return null;
}

// ── Collapse (three-pass dedup) ───────────────────────────────────────

/// Collapse duplicate Convex rows so each physical machine appears
/// exactly once. Three passes — identity key → alias key → endpoint
/// key. Safe on empty lists; idempotent on already-deduped input.
List<RemoteDevice> collapseRemoteDevices(List<RemoteDevice> devices) {
  if (devices.isEmpty) return const [];

  // Pass 1: identity key (hwid / publicKey / name+os).
  final byIdentity = <String, RemoteDevice>{};
  for (final d in devices) {
    final k = _identityKey(d);
    final prev = byIdentity[k];
    byIdentity[k] = prev == null ? d : _merge(prev, d);
  }

  // Pass 2: alias key (os + normalised hostname), with strong-identity
  // conflict resolution so two genuinely-different machines sharing a
  // hostname don't silently merge.
  final byAlias = <String, RemoteDevice>{};
  for (final d in byIdentity.values) {
    final k = _aliasKey(d);
    if (k == null) {
      byAlias['id:${d.deviceId}'] = d;
      continue;
    }
    final prev = byAlias[k];
    if (prev == null) {
      byAlias[k] = d;
      continue;
    }
    final strongConflict = (prev.hwid != null &&
            d.hwid != null &&
            prev.hwid!.isNotEmpty &&
            d.hwid!.isNotEmpty &&
            prev.hwid != d.hwid) ||
        (prev.publicKey != null &&
            d.publicKey != null &&
            prev.publicKey!.isNotEmpty &&
            d.publicKey!.isNotEmpty &&
            prev.publicKey != d.publicKey);
    if (strongConflict) {
      final winner = _pickActiveOverStaleNeedsAuth(prev, d);
      if (winner != null) {
        byAlias[k] = winner;
        continue;
      }
    }
    byAlias[k] = _merge(prev, d);
  }

  // Pass 3: endpoint key (host:port) — last-chance dedup for rows
  // that share a LAN address but slipped through identity + alias.
  final byEndpoint = <String, RemoteDevice>{};
  for (final d in byAlias.values) {
    final k = _endpointKey(d);
    if (k == null) {
      byEndpoint['id:${d.deviceId}'] = d;
      continue;
    }
    final prev = byEndpoint[k];
    byEndpoint[k] = prev == null ? d : _merge(prev, d);
  }

  return byEndpoint.values.toList();
}

// ── Freshness + target pick ──────────────────────────────────────────

/// Trust Convex's `isOnline` as the primary signal — backend already
/// applies its own 90 s gate from the server clock. Client-side
/// re-checks produce false-yellows from phone↔backend clock skew
/// around the 89-90 s mark. We expose this helper only so auto-pick
/// can compare two devices on the client-side relative ordering.
bool isDeviceFresh(RemoteDevice d, {int? now}) {
  if (!d.isOnline) return false;
  if (d.lastHeartbeat == 0) return true;
  final nowMs = now ?? DateTime.now().millisecondsSinceEpoch;
  return (nowMs - d.lastHeartbeat) < heartbeatStaleMs;
}

/// Choose the best candidate for an auto-connect attempt. Preference:
///   1. explicit `preferredDeviceId` that's still fresh
///   2. fresh (online + recent heartbeat) + has a quicHost
///   3. online + has a quicHost
///   4. first with a quicHost
RemoteDevice? pickTargetDevice(
  List<RemoteDevice> devices, {
  String? preferredDeviceId,
}) {
  if (devices.isEmpty) return null;
  if (preferredDeviceId != null && preferredDeviceId.isNotEmpty) {
    RemoteDevice? preferred;
    for (final d in devices) {
      if (d.deviceId == preferredDeviceId && d.quicHost.isNotEmpty) {
        preferred = d;
        break;
      }
    }
    if (preferred != null && isDeviceFresh(preferred)) return preferred;
    if (preferred != null) return preferred;
  }
  for (final d in devices) {
    if (isDeviceFresh(d) && d.quicHost.isNotEmpty) return d;
  }
  for (final d in devices) {
    if (d.isOnline && d.quicHost.isNotEmpty) return d;
  }
  for (final d in devices) {
    if (d.quicHost.isNotEmpty) return d;
  }
  return devices.first;
}

/// Build the set of `/health` candidate URLs for a target device —
/// `quicHost` plus every LAN IP the agent reported in `localIps`,
/// uniqued and formatted. The mobile app + RN SDK race these in
/// parallel via [raceHealthProbes]; it's THE thing that makes direct
/// LAN reloads "just work" on multi-homed Macs (en0 + utun Tailscale
/// + docker0).
List<String> buildProbeCandidates(
  RemoteDevice target, {
  int defaultHttpPort = defaultAgentHttpPort,
}) {
  final port = target.httpPort ?? target.quicPort;
  final usePort = port == 0 ? defaultHttpPort : port;
  final ips = <String>{};
  if (target.quicHost.isNotEmpty) ips.add(target.quicHost);
  for (final ip in target.localIps ?? const <String>[]) {
    if (ip.isNotEmpty) ips.add(ip);
  }
  return ips.map((ip) => 'http://$ip:$usePort').toList();
}

// ── Parallel /health race ────────────────────────────────────────────

class ProbeResult {
  final String url;
  final String? hostname;
  final String? version;
  final int? latencyMs;
  const ProbeResult({
    required this.url,
    this.hostname,
    this.version,
    this.latencyMs,
  });
}

/// Race `/health` probes across N URLs. First 200 wins; every other
/// probe is abandoned. Dart has no built-in `Completer.first(...)`
/// with cancellation so we roll our own with a timeout per probe.
Future<ProbeResult?> raceHealthProbes(
  List<String> urls, {
  Duration timeout = const Duration(milliseconds: probeTimeoutMs),
  Map<String, String>? headers,
}) async {
  if (urls.isEmpty) return null;

  final completer = Completer<ProbeResult?>();
  var remaining = urls.length;

  Future<void> probeOne(String url) async {
    final base = url.endsWith('/') ? url.substring(0, url.length - 1) : url;
    final start = DateTime.now().millisecondsSinceEpoch;
    try {
      final res = await http
          .get(Uri.parse('$base/health'), headers: headers)
          .timeout(timeout);
      if (!completer.isCompleted) {
        if (res.statusCode == 200) {
          String? hostname;
          String? version;
          try {
            final data = jsonDecode(res.body) as Map<String, dynamic>?;
            hostname =
                (data?['hostname'] as String?) ?? (data?['name'] as String?);
            version = data?['version'] as String?;
          } catch (_) {
            // /health may return plain text
          }
          completer.complete(ProbeResult(
            url: base,
            hostname: hostname,
            version: version,
            latencyMs: DateTime.now().millisecondsSinceEpoch - start,
          ));
          return;
        }
      }
    } catch (_) {
      // probe failed — fall through to the remaining counter
    }
    remaining -= 1;
    if (remaining <= 0 && !completer.isCompleted) {
      completer.complete(null);
    }
  }

  for (final url in urls) {
    unawaited(probeOne(url));
  }
  return completer.future;
}

// ── Convex /devices/list ─────────────────────────────────────────────

/// Fetch the set of remote dev machines this user can reach, then
/// collapse the duplicates and split into owned (user is the host)
/// vs shared (host invited them as a guest).
Future<DeviceList> listReachableDevices(String token) async {
  try {
    final res = await http.get(
      Uri.parse('${getConvexSiteUrl()}/devices/list'),
      headers: {'Authorization': 'Bearer $token'},
    ).timeout(const Duration(seconds: 5));
    if (res.statusCode != 200) return DeviceList.empty();
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final raw = body['devices'] as List? ?? const [];
    final normalised = raw
        .whereType<Map<String, dynamic>>()
        .map(RemoteDevice.fromJson)
        .toList();
    final deduped = collapseRemoteDevices(normalised);
    return DeviceList(
      owned: deduped.where((d) => !d.isGuest).toList(),
      shared: deduped.where((d) => d.isGuest).toList(),
    );
  } catch (_) {
    return DeviceList.empty();
  }
}
