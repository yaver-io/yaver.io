/// Embedded sign-in page for the Yaver Feedback SDK (Flutter).
///
/// Mirrors the React Native SDK's [YaverLoginScreen]: five OAuth providers
/// (Apple / Google / GitHub / GitLab / Microsoft) plus inline email/password.
/// All actions are dispatched via caller-supplied closures so the SDK does
/// not have to declare hard dependencies on `sign_in_with_apple` or
/// `flutter_web_auth_2`.
library yaver_feedback.login_page;

import 'package:flutter/material.dart';

import 'auth.dart';

/// Optional native bindings the host app can plug in. Each is invoked
/// only when the matching button is tapped — leaving them null means the
/// corresponding sign-in path will throw "not configured".
class YaverLoginBindings {
  /// Calls the host's `SignInWithApple.getAppleIDCredential` and adapts
  /// the result to `(identityToken, fullName)`.
  final Future<({String identityToken, String? fullName})> Function()?
      requestAppleCredential;

  /// Opens [authUrl] inside an in-app browser session and resolves with
  /// the redirect URL. Implement using `flutter_web_auth_2`:
  /// `(url) => FlutterWebAuth2.authenticate(url: url, callbackUrlScheme: 'yaver')`.
  final Future<String> Function(String authUrl)? openAuthSession;

  const YaverLoginBindings({
    this.requestAppleCredential,
    this.openAuthSession,
  });
}

class YaverLoginPage extends StatefulWidget {
  /// Invoked once a session token is issued.
  final Future<void> Function(String token) onLoggedIn;

  /// Optional cancel callback — shown as a header button when provided.
  final VoidCallback? onCancel;

  /// Native bindings — see [YaverLoginBindings].
  final YaverLoginBindings bindings;

  const YaverLoginPage({
    super.key,
    required this.onLoggedIn,
    required this.bindings,
    this.onCancel,
  });

  @override
  State<YaverLoginPage> createState() => _YaverLoginPageState();
}

class _YaverLoginPageState extends State<YaverLoginPage> {
  String? _busyProvider;
  bool _showEmail = false;
  bool _isSignUp = false;
  String _name = '';
  String _email = '';
  String _password = '';
  String _confirmPassword = '';
  String? _emailError;
  bool _emailBusy = false;

  Future<void> _finish(String token) async {
    await widget.onLoggedIn(token);
  }

  Future<void> _handleApple() async {
    if (_busyProvider != null) return;
    final cb = widget.bindings.requestAppleCredential;
    if (cb == null) {
      _showSnack(
        'Apple Sign-In is not configured. Add `sign_in_with_apple` to the host app and pass `requestAppleCredential` in YaverLoginBindings.',
      );
      return;
    }
    setState(() => _busyProvider = 'apple');
    try {
      final res = await signInWithApple(requestNativeCredential: cb);
      await _finish(res.token);
    } catch (e) {
      if (e.toString() != 'Exception: cancelled') {
        _showSnack('Sign in failed: $e');
      }
    } finally {
      if (mounted) setState(() => _busyProvider = null);
    }
  }

  Future<void> _handleOAuth(OAuthProvider provider) async {
    if (_busyProvider != null) return;
    final cb = widget.bindings.openAuthSession;
    if (cb == null) {
      _showSnack(
        'OAuth is not configured. Add `flutter_web_auth_2` to the host app and pass `openAuthSession` in YaverLoginBindings.',
      );
      return;
    }
    setState(() => _busyProvider = provider.id);
    try {
      final res = await signInWithOAuth(
        provider: provider,
        openAuthSession: cb,
      );
      await _finish(res.token);
    } catch (e) {
      if (e.toString() != 'Exception: cancelled') {
        _showSnack('Sign in failed: $e');
      }
    } finally {
      if (mounted) setState(() => _busyProvider = null);
    }
  }

  Future<void> _handleEmailSubmit() async {
    setState(() => _emailError = null);
    if (_isSignUp) {
      if (_name.trim().isEmpty) {
        return setState(() => _emailError = 'Full name is required');
      }
      if (_password != _confirmPassword) {
        return setState(() => _emailError = 'Passwords do not match');
      }
      if (_password.length < 8) {
        return setState(
          () => _emailError = 'Password must be at least 8 characters',
        );
      }
    }
    if (_email.trim().isEmpty || _password.isEmpty) {
      return setState(() => _emailError = 'Email and password are required');
    }
    setState(() => _emailBusy = true);
    try {
      final res = _isSignUp
          ? await signupWithEmail(
              fullName: _name.trim(),
              email: _email.trim(),
              password: _password,
            )
          : await loginWithEmail(email: _email.trim(), password: _password);
      await _finish(res.token);
    } catch (e) {
      setState(() => _emailError = e.toString().replaceFirst('Exception: ', ''));
    } finally {
      if (mounted) setState(() => _emailBusy = false);
    }
  }

  void _showSnack(String msg) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: const Color(0xFF1A1A2E),
      appBar: widget.onCancel != null
          ? AppBar(
              backgroundColor: const Color(0xFF1A1A2E),
              elevation: 0,
              actions: [
                TextButton(
                  onPressed: widget.onCancel,
                  child: const Text(
                    'Cancel',
                    style: TextStyle(color: Color(0xFF9CA3AF)),
                  ),
                ),
              ],
            )
          : null,
      body: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.symmetric(horizontal: 24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Text(
                  'Yaver',
                  style: TextStyle(
                    color: Color(0xFFE0E0E0),
                    fontSize: 36,
                    fontWeight: FontWeight.w800,
                  ),
                ),
                const SizedBox(height: 6),
                const Text(
                  'Sign in to send feedback',
                  style: TextStyle(color: Color(0xFF9CA3AF), fontSize: 14),
                ),
                const SizedBox(height: 32),
                _providerBtn('apple', 'Continue with Apple', _handleApple),
                const SizedBox(height: 10),
                _providerBtn('google', 'Continue with Google',
                    () => _handleOAuth(OAuthProvider.google)),
                const SizedBox(height: 10),
                _providerBtn('github', 'Continue with GitHub',
                    () => _handleOAuth(OAuthProvider.github)),
                const SizedBox(height: 10),
                _providerBtn('gitlab', 'Continue with GitLab',
                    () => _handleOAuth(OAuthProvider.gitlab)),
                const SizedBox(height: 10),
                _providerBtn('microsoft', 'Continue with Microsoft',
                    () => _handleOAuth(OAuthProvider.microsoft)),
                const SizedBox(height: 10),
                if (!_showEmail)
                  _providerBtn(
                    'email-show',
                    'Continue with Email',
                    () => setState(() => _showEmail = true),
                  )
                else
                  _emailForm(),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _providerBtn(String id, String label, VoidCallback onTap) {
    final loading = _busyProvider == id;
    return SizedBox(
      width: double.infinity,
      child: ElevatedButton(
        style: ElevatedButton.styleFrom(
          backgroundColor: const Color(0x10FFFFFF),
          foregroundColor: const Color(0xFFE0E0E0),
          padding: const EdgeInsets.symmetric(vertical: 14),
          shape: RoundedRectangleBorder(
            side: const BorderSide(color: Color(0x1FFFFFFF)),
            borderRadius: BorderRadius.circular(12),
          ),
        ),
        onPressed: _busyProvider != null ? null : onTap,
        child: loading
            ? const SizedBox(
                width: 16,
                height: 16,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            : Text(
                label,
                style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w600),
              ),
      ),
    );
  }

  Widget _emailForm() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SizedBox(height: 16),
        const Row(
          children: [
            Expanded(child: Divider(color: Color(0x1FFFFFFF))),
            Padding(
              padding: EdgeInsets.symmetric(horizontal: 12),
              child: Text('email',
                  style: TextStyle(color: Color(0xFF6B7280), fontSize: 11)),
            ),
            Expanded(child: Divider(color: Color(0x1FFFFFFF))),
          ],
        ),
        const SizedBox(height: 12),
        if (_isSignUp) ...[
          _input('Full Name', (v) => _name = v),
          const SizedBox(height: 10),
        ],
        _input('Email', (v) => _email = v, keyboardType: TextInputType.emailAddress),
        const SizedBox(height: 10),
        _input('Password', (v) => _password = v, obscure: true),
        if (_isSignUp) ...[
          const SizedBox(height: 10),
          _input('Confirm Password', (v) => _confirmPassword = v, obscure: true),
        ],
        if (_emailError != null) ...[
          const SizedBox(height: 8),
          Text(_emailError!,
              textAlign: TextAlign.center,
              style: const TextStyle(color: Color(0xFFEF4444), fontSize: 12)),
        ],
        const SizedBox(height: 12),
        SizedBox(
          width: double.infinity,
          child: ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: const Color(0xFF6366F1),
              foregroundColor: Colors.white,
              padding: const EdgeInsets.symmetric(vertical: 14),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
              ),
            ),
            onPressed: _emailBusy ? null : _handleEmailSubmit,
            child: _emailBusy
                ? const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: Colors.white),
                  )
                : Text(
                    _isSignUp ? 'Create Account' : 'Sign In',
                    style: const TextStyle(
                        fontSize: 14, fontWeight: FontWeight.w700),
                  ),
          ),
        ),
        const SizedBox(height: 8),
        TextButton(
          onPressed: () => setState(() {
            _isSignUp = !_isSignUp;
            _emailError = null;
          }),
          child: Text(
            _isSignUp
                ? 'Already have an account? Sign in'
                : "Don't have an account? Sign up",
            style: const TextStyle(color: Color(0xFF818CF8), fontSize: 13),
          ),
        ),
      ],
    );
  }

  Widget _input(
    String label,
    void Function(String) onChanged, {
    bool obscure = false,
    TextInputType? keyboardType,
  }) {
    return TextField(
      onChanged: onChanged,
      obscureText: obscure,
      keyboardType: keyboardType,
      autocorrect: false,
      autofillHints: obscure
          ? (_isSignUp
              ? const [AutofillHints.newPassword]
              : const [AutofillHints.password])
          : (label == 'Email' ? const [AutofillHints.email] : null),
      style: const TextStyle(color: Color(0xFFE0E0E0), fontSize: 14),
      decoration: InputDecoration(
        hintText: label,
        hintStyle: const TextStyle(color: Color(0xFF6B7280)),
        filled: true,
        fillColor: const Color(0x10FFFFFF),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(10),
          borderSide: const BorderSide(color: Color(0x1FFFFFFF)),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(10),
          borderSide: const BorderSide(color: Color(0x1FFFFFFF)),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(10),
          borderSide: const BorderSide(color: Color(0xFF6366F1)),
        ),
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      ),
    );
  }
}
