// Yaver native-flutter-app fixture.
// Hardcoded creds (admin/admin) — DO NOT use as production auth pattern.

import 'package:flutter/material.dart';

const _validUsername = 'admin';
const _validPassword = 'admin';

bool authenticate(String username, String password) {
  return username == _validUsername && password == _validPassword;
}

void main() => runApp(const YaverFixtureApp());

class YaverFixtureApp extends StatelessWidget {
  const YaverFixtureApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Yaver Native Flutter Fixture',
      theme: ThemeData(useMaterial3: true, colorSchemeSeed: Colors.indigo),
      home: const LoginScreen(),
    );
  }
}

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key});

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _username = TextEditingController(text: _validUsername);
  final _password = TextEditingController(text: _validPassword);
  String? _error;

  void _submit() {
    if (authenticate(_username.text, _password.text)) {
      Navigator.of(context).pushReplacement(
        MaterialPageRoute(builder: (_) => DashboardScreen(username: _username.text)),
      );
    } else {
      setState(() => _error = 'Invalid credentials. Use admin / admin.');
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Sign in to Yaver Fixture')),
      body: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const Text('Hardcoded creds: admin / admin'),
            const SizedBox(height: 16),
            TextField(
              controller: _username,
              decoration: const InputDecoration(labelText: 'Username'),
              key: const Key('username'),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _password,
              decoration: const InputDecoration(labelText: 'Password'),
              obscureText: true,
              key: const Key('password'),
            ),
            const SizedBox(height: 24),
            FilledButton(
              key: const Key('signin'),
              onPressed: _submit,
              child: const Text('Sign in'),
            ),
            if (_error != null) ...[
              const SizedBox(height: 16),
              Text(_error!, style: const TextStyle(color: Colors.red)),
            ],
          ],
        ),
      ),
    );
  }
}

class DashboardScreen extends StatelessWidget {
  const DashboardScreen({super.key, required this.username});
  final String username;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Dashboard')),
      body: Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            const Icon(Icons.check_circle, size: 64, color: Colors.green),
            const SizedBox(height: 16),
            Text('Hello, $username', style: Theme.of(context).textTheme.headlineMedium),
            const SizedBox(height: 8),
            const Text('You are signed in to the Yaver Flutter fixture.'),
          ],
        ),
      ),
    );
  }
}
