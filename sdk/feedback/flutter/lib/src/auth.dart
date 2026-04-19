/// Authentication API for the Yaver Feedback SDK (Flutter).
///
/// Mirrors `yaver-feedback-react-native` 0.6+:
///   - Native Apple Sign-In via `sign_in_with_apple` → `/auth/apple-native`.
///   - In-app browser OAuth (Google / GitHub / GitLab / Microsoft) via
///     `flutter_web_auth_2` against the same callback URL the Yaver mobile
///     app uses (`yaver://oauth-callback`).
///   - Email + password sign-up / sign-in (no 2FA — accounts with TOTP are
///     directed to OAuth, which completes the second factor on yaver.io).
///   - Token validation against Convex.
///
/// Consumers who only need email/password can skip the optional deps. To
/// enable Apple Sign-In add `sign_in_with_apple` to your `pubspec.yaml`;
/// for the four OAuth providers add `flutter_web_auth_2`. Both packages
/// are imported lazily so the SDK does not force them on you.
library yaver_feedback.auth;

import 'dart:async';
import 'dart:convert';
import 'package:http/http.dart' as http;

const String defaultConvexSiteUrl =
    'https://shocking-echidna-394.eu-west-1.convex.site';
const String defaultWebBaseUrl = 'https://yaver.io';
const String defaultOAuthRedirect = 'yaver://oauth-callback';

String _convexSiteUrl = defaultConvexSiteUrl;
String _webBaseUrl = defaultWebBaseUrl;

/// Override the Convex site URL + web base URL (staging vs prod).
void configureAuthEndpoints({String? convexSiteUrl, String? webBaseUrl}) {
  if (convexSiteUrl != null) _convexSiteUrl = convexSiteUrl;
  if (webBaseUrl != null) _webBaseUrl = webBaseUrl;
}

String getConvexSiteUrl() => _convexSiteUrl;
String getWebBaseUrl() => _webBaseUrl;

enum OAuthProvider { google, microsoft, apple, github, gitlab }

extension OAuthProviderName on OAuthProvider {
  String get id {
    switch (this) {
      case OAuthProvider.google:
        return 'google';
      case OAuthProvider.microsoft:
        return 'microsoft';
      case OAuthProvider.apple:
        return 'apple';
      case OAuthProvider.github:
        return 'github';
      case OAuthProvider.gitlab:
        return 'gitlab';
    }
  }
}

class YaverUser {
  final String id;
  final String email;
  final String name;
  final String? provider;
  final String? avatarUrl;
  YaverUser({
    required this.id,
    required this.email,
    required this.name,
    this.provider,
    this.avatarUrl,
  });
}

Future<YaverUser?> validateToken(String token) async {
  try {
    final res = await http
        .get(
          Uri.parse('$_convexSiteUrl/auth/validate'),
          headers: {'Authorization': 'Bearer $token'},
        )
        .timeout(const Duration(seconds: 5));
    if (res.statusCode != 200) return null;
    final data = jsonDecode(res.body) as Map<String, dynamic>;
    final u = data['user'] as Map<String, dynamic>;
    return YaverUser(
      id: (u['userId'] ?? u['id']) as String,
      email: u['email'] as String,
      name: (u['fullName'] ?? u['name']) as String,
      provider: u['provider'] as String?,
      avatarUrl: u['avatarUrl'] as String?,
    );
  } catch (_) {
    return null;
  }
}

/// Sign in with Apple via the native ASAuthorization flow. Requires the
/// `sign_in_with_apple` Flutter package and the host app's bundle to have
/// the "Sign in with Apple" capability enabled. iOS only.
///
/// `signInWithApple` is invoked through a caller-supplied closure so this
/// SDK does not have to declare a hard dependency on `sign_in_with_apple`.
/// Pass a closure that delegates to `SignInWithApple.getAppleIDCredential`.
///
/// Throws `Exception('cancelled')` if the user dismisses the sheet.
Future<({String token, String userId})> signInWithApple({
  required Future<({String identityToken, String? fullName})> Function()
      requestNativeCredential,
}) async {
  final credential = await requestNativeCredential();
  final body = jsonEncode({
    'identityToken': credential.identityToken,
    if (credential.fullName != null) 'fullName': credential.fullName,
  });
  final res = await http
      .post(
        Uri.parse('$_convexSiteUrl/auth/apple-native'),
        headers: {'Content-Type': 'application/json'},
        body: body,
      )
      .timeout(const Duration(seconds: 10));
  if (res.statusCode != 200) {
    throw Exception(res.body.isNotEmpty ? res.body : 'Apple sign-in failed');
  }
  final data = jsonDecode(res.body) as Map<String, dynamic>;
  return (token: data['token'] as String, userId: data['userId'] as String);
}

/// Sign in via in-app browser OAuth. Requires the `flutter_web_auth_2`
/// package on the host app. The closure is responsible for opening the
/// `authUrl` and returning the redirect URL once the auth session
/// completes.
///
/// Example using `flutter_web_auth_2`:
/// ```dart
/// signInWithOAuth(
///   provider: OAuthProvider.google,
///   openAuthSession: (url) async => FlutterWebAuth2.authenticate(
///     url: url,
///     callbackUrlScheme: 'yaver',
///   ),
/// );
/// ```
///
/// Throws `Exception('cancelled')` if the user dismisses the browser.
Future<({String token})> signInWithOAuth({
  required OAuthProvider provider,
  required Future<String> Function(String authUrl) openAuthSession,
  String redirectUrl = defaultOAuthRedirect,
}) async {
  final authUrl = '$_webBaseUrl/api/auth/oauth/${provider.id}?client=mobile';
  final String resultUrl;
  try {
    resultUrl = await openAuthSession(authUrl);
  } catch (e) {
    throw Exception('cancelled');
  }
  final uri = Uri.tryParse(resultUrl);
  final token = uri?.queryParameters['token'];
  if (token == null || token.isEmpty) {
    throw Exception('OAuth callback did not include a token');
  }
  return (token: token);
}

Future<({String token, String userId})> signupWithEmail({
  required String fullName,
  required String email,
  required String password,
}) async {
  final res = await http
      .post(
        Uri.parse('$_convexSiteUrl/auth/signup'),
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode({
          'fullName': fullName,
          'email': email,
          'password': password,
        }),
      )
      .timeout(const Duration(seconds: 10));
  if (res.statusCode != 200) {
    final data =
        (jsonDecode(res.body) as Map<String, dynamic>?) ?? <String, dynamic>{};
    throw Exception(data['error'] ?? 'Signup failed');
  }
  final data = jsonDecode(res.body) as Map<String, dynamic>;
  return (token: data['token'] as String, userId: data['userId'] as String);
}

Future<({String token, String userId})> loginWithEmail({
  required String email,
  required String password,
}) async {
  final res = await http
      .post(
        Uri.parse('$_convexSiteUrl/auth/login'),
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode({'email': email, 'password': password}),
      )
      .timeout(const Duration(seconds: 10));
  if (res.statusCode != 200) {
    final data =
        (jsonDecode(res.body) as Map<String, dynamic>?) ?? <String, dynamic>{};
    throw Exception(data['error'] ?? 'Login failed');
  }
  final data = jsonDecode(res.body) as Map<String, dynamic>;
  if (data['requires2fa'] == true) {
    throw Exception(
      '2FA is enabled on this account. Sign in with Apple/Google/GitHub/GitLab/Microsoft instead.',
    );
  }
  return (token: data['token'] as String, userId: data['userId'] as String);
}
